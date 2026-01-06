// API route handlers
const axios = require("axios");
const FormData = require("form-data");
const fs = require("fs");
const { promisify } = require("util");
const mime = require("mime-types");

const { IPFS_API, STORAGE_MAX, FILE_LIMIT, PROXY_FILE_LIMIT, formatBytes } = require("./config");
const { getPinnedSize, checkIPFSHealth, getIPFSStats } = require("./ipfs");

const {
  toNpub,
  constants: { DEFAULT_RELAYS },
} = require("./nostr");

const {
  getPins,
  getPinsByType,
  getPinsGroupedByNpub,
  getStatsByNpub,
  countByNpub,
  getStats,
  getTotalCount,
  getRecentPins,
  countByTypeAndStatus,
  getLastPinnerActivity,
  getLastNostrRun,
} = require("./database");

const unlinkAsync = promisify(fs.unlink);

// Concurrency control for remote uploads
const MAX_CONCURRENT_DOWNLOADS = 3;
let activeDownloads = 0;

// Health check endpoint
const healthHandler = async (req, res) => {
  try {
    const { healthy, peers, error } = await checkIPFSHealth();

    if (healthy) {
      res.status(200).json({ status: "healthy", peers });
    } else {
      res.status(503).json({ status: "unhealthy", peers: peers || 0, reason: error || "No peers connected" });
    }
  } catch (err) {
    res.status(503).json({ status: "unhealthy", error: err.message });
  }
};

// Status endpoint
const statusHandler = async (req, res) => {
  try {
    const stats = await getIPFSStats();
    const { version: appVersion } = require("../package.json");

    res.json({
      status: "success",
      timestamp: new Date().toISOString(),
      bandwidth: stats.bandwidth,
      repository: stats.repository,
      node: stats.node,
      peers: stats.peers,
      storageLimit: {
        configured: STORAGE_MAX,
        current: formatBytes(stats.repository.storageMax),
      },
      fileLimit: {
        configured: process.env.FILE_LIMIT || "5GB",
        bytes: FILE_LIMIT,
        formatted: formatBytes(FILE_LIMIT),
      },
      remoteFileLimit: {
        configured: process.env.REMOTE_FILE_LIMIT || "2GB",
        bytes: PROXY_FILE_LIMIT,
        formatted: formatBytes(PROXY_FILE_LIMIT),
      },
      appVersion,
    });
  } catch (err) {
    console.error("Status check error:", {
      message: err.message,
      stack: err.stack,
      timestamp: new Date().toISOString(),
    });

    res.status(503).json({
      error: "Failed to retrieve IPFS status",
      details: err.message,
      status: "failed",
      timestamp: new Date().toISOString(),
    });
  }
};

// Nostr stats endpoint
const nostrHandler = async (req, res, NPUBS) => {
  if (!NPUBS || NPUBS.length === 0) {
    return res.status(200).json({
      enabled: false,
      reason: "No NPUBs configured",
    });
  }

  try {
    // Fetch repo stats and pinned size in parallel
    const [repoResponse, pinnedStats] = await Promise.all([
      axios.post(`${IPFS_API}/api/v0/repo/stat`, { timeout: 5000 }),
      getPinnedSize()
    ]);

    const repo = {
      size: repoResponse.data.RepoSize,
      storageMax: repoResponse.data.StorageMax,
      numObjects: repoResponse.data.NumObjects,
    };

    const lastNostrRun = getLastNostrRun();

    // Convert all NPUBs to npub format for display
    const operatorNpubs = NPUBS.map(npub => npub.startsWith("npub") ? npub : toNpub(npub));

    // Get counts from database
    const selfPinned = countByTypeAndStatus('self', 'pinned');
    const selfPending = countByTypeAndStatus('self', 'pending');
    const selfFailed = countByTypeAndStatus('self', 'failed');

    // Build response with multi-NPUB support
    const response = {
      enabled: true,
      operators: operatorNpubs, // Array of NPUBs
      operatorCount: NPUBS.length,
      relays: DEFAULT_RELAYS,
      repo,
      pins: {
        self: {
          pinned: selfPinned,
          pending: selfPending,
          failed: selfFailed,
          total: selfPinned + selfPending + selfFailed,
        },
        totalSize: pinnedStats.totalSize,
        pinnedCount: pinnedStats.count,
      },
      activity: {
        lastDiscovery: lastNostrRun?.at || null,
        lastPinner: getLastPinnerActivity(),
      },
    };

    // Add lastRun data if available
    if (lastNostrRun?.at) {
      response.lastRun = {
        at: lastNostrRun.at,
        error: lastNostrRun.error,
        npubs: lastNostrRun.npubs || null,
        aggregate: lastNostrRun.aggregate || null,
      };
    }

    res.status(200).json(response);
  } catch (err) {
    console.error("Nostr stats error:", err.message);
    return res.status(503).json({
      enabled: true,
      error: "Failed to retrieve stats",
      details: err.message,
    });
  }
};

// Upload handler
const uploadHandler = async (req, res) => {
  let filePath = null;

  try {
    // Validate file presence
    if (!req.file) {
      return res.status(400).json({
        error: "No file uploaded",
        status: "error",
        message: "No file uploaded",
        timestamp: new Date().toISOString(),
      });
    }

    filePath = req.file.path;

    // Prepare file for IPFS using stream
    const formData = new FormData();
    const fileStream = fs.createReadStream(filePath);

    // Detect correct MIME type from file extension
    const mimeType = mime.lookup(req.file.originalname) || req.file.mimetype || "application/octet-stream";

    formData.append("file", fileStream, {
      filename: req.file.originalname,
      contentType: mimeType,
      knownLength: req.file.size,
    });

    // Upload to IPFS
    const uploadStart = Date.now();
    console.log(`Starting IPFS upload for ${req.file.originalname} ...`);

    const response = await axios.post(`${IPFS_API}/api/v0/add?pin=false`, formData, {
      headers: { ...formData.getHeaders() },
      timeout: 3600000, // 1 hour timeout
      maxContentLength: Infinity,
      maxBodyLength: Infinity,
    });

    // Detailed logging
    const uploadDetails = {
      name: req.file.originalname,
      size_bytes: req.file.size,
      mime_type: mimeType,
      cid: response.data.Hash,
      upload_duration_ms: Date.now() - uploadStart,
      timestamp: new Date().toISOString(),
    };
    console.log("File uploaded successfully:", uploadDetails);

    // Clean up temp file after successful upload
    await unlinkAsync(filePath).catch((err) => console.warn("Failed to delete temp file:", err.message));

    // Simple response
    res.json({
      status: "success",
      url: `https://dweb.link/ipfs/${response.data.Hash}?filename=${encodeURIComponent(req.file.originalname)}`,
      cid: response.data.Hash,
      size: uploadDetails.size_bytes,
      type: mimeType,
      filename: req.file.originalname,
    });
  } catch (err) {
    if (filePath) {
      await unlinkAsync(filePath).catch((cleanupErr) => console.warn("Failed to delete temp file on error:", cleanupErr.message));
    }

    const multer = require("multer");
    if (err instanceof multer.MulterError) {
      return res.status(400).json({
        error: err.message,
        status: "error",
        message: err.message,
        timestamp: new Date().toISOString(),
      });
    }

    console.error("IPFS upload error:", {
      message: err.message,
      stack: err.stack,
      timestamp: new Date().toISOString(),
    });

    res.status(500).json({
      error: "Failed to upload to IPFS",
      details: err.message,
      status: "error",
      message: "Failed to upload to IPFS",
      timestamp: new Date().toISOString(),
    });
  }
};

// Pins history endpoint
const pinsHandler = async (req, res) => {
  try {
    const limit = Math.min(parseInt(req.query.limit) || 50, 200);
    const offset = parseInt(req.query.offset) || 0;

    // Get pins grouped by NPUB
    const groupedPins = getPinsGroupedByNpub(limit, offset);

    // Build response with stats for each NPUB
    const npubData = {};
    Object.keys(groupedPins).forEach(npub => {
      const pins = groupedPins[npub];
      const stats = getStatsByNpub(npub);
      const totalCount = countByNpub(npub);

      // Calculate totals
      const totalSize = stats.reduce((sum, stat) => sum + (stat.total_size || 0), 0);
      const pinnedCount = stats.find(s => s.status === 'pinned')?.count || 0;
      const pendingCount = stats.find(s => s.status === 'pending')?.count || 0;
      const failedCount = stats.find(s => s.status === 'failed')?.count || 0;

      npubData[npub] = {
        npub,
        pins: pins.map(pin => ({
          id: pin.id,
          eventId: pin.event_id,
          cid: pin.cid,
          size: pin.size,
          timestamp: pin.timestamp,
          author: pin.author,
          type: pin.type,
          status: pin.status,
          createdAt: pin.created_at,
          updatedAt: pin.updated_at,
          npub: pin.npub,
        })),
        stats: {
          total: totalCount,
          pinned: pinnedCount,
          pending: pendingCount,
          failed: failedCount,
          totalSize: totalSize,
        },
        pagination: {
          limit,
          offset,
          total: totalCount,
          hasMore: offset + limit < totalCount,
        },
      };
    });

    const total = getTotalCount();
    const globalStats = getStats();

    res.json({
      success: true,
      byNpub: npubData,
      pagination: {
        limit,
        offset,
        total,
        hasMore: offset + limit < total,
      },
      stats: globalStats.reduce((acc, stat) => {
        const key = `${stat.type}_${stat.status}`;
        acc[key] = {
          count: stat.count,
          totalSize: stat.total_size,
        };
        return acc;
      }, {}),
    });
  } catch (err) {
    console.error("Pins handler error:", err);
    res.status(500).json({
      success: false,
      error: err.message,
    });
  }
};

// Remote upload handler - downloads URL and uploads to IPFS, returns JSON
const remoteUploadHandler = async (req, res) => {
  const crypto = require("crypto");
  const path = require("path");
  const { UPLOAD_TEMP_DIR } = require("./config");
  let tempFilePath = null;

  // Check concurrency limit
  if (activeDownloads >= MAX_CONCURRENT_DOWNLOADS) {
    return res.status(429).json({
      error: "Too many concurrent downloads",
      status: "error",
      message: `Maximum ${MAX_CONCURRENT_DOWNLOADS} concurrent downloads in progress. Please try again later.`,
      activeDownloads: activeDownloads,
      maxConcurrent: MAX_CONCURRENT_DOWNLOADS,
      timestamp: new Date().toISOString(),
    });
  }

  // Increment active downloads counter
  activeDownloads++;
  console.log(`[REMOTE-UPLOAD] Active downloads: ${activeDownloads}/${MAX_CONCURRENT_DOWNLOADS}`);

  try {
    // Extract URL from request body
    const { url: targetUrl } = req.body;

    if (!targetUrl) {
      return res.status(400).json({
        error: "No URL provided",
        status: "error",
        message: "Request body must contain 'url' field",
        timestamp: new Date().toISOString(),
      });
    }

    // Validate URL format
    let url;
    try {
      url = new URL(targetUrl);
      if (!["http:", "https:"].includes(url.protocol)) {
        throw new Error("Only HTTP and HTTPS protocols are supported");
      }
    } catch (err) {
      return res.status(400).json({
        error: "Invalid URL",
        status: "error",
        message: err.message,
        timestamp: new Date().toISOString(),
      });
    }

    console.log(`[REMOTE-UPLOAD] Starting download from: ${targetUrl}`);

    // Download the file with streaming and size limit
    const downloadStart = Date.now();
    let downloadedSize = 0;
    let lastDataTime = Date.now();

    const response = await axios({
      method: "GET",
      url: targetUrl,
      responseType: "stream",
      timeout: 60000, // 60 second timeout for connection
      maxRedirects: 5,
    });

    // Generate temp file path
    const randomName = crypto.randomBytes(16).toString("hex");
    tempFilePath = path.join(UPLOAD_TEMP_DIR, randomName);

    // Create write stream
    const writeStream = fs.createWriteStream(tempFilePath);

    // Timeout safeguards
    let idleTimeoutId = null;
    let overallTimeoutId = null;
    let isAborted = false;

    const cleanup = () => {
      if (idleTimeoutId) clearTimeout(idleTimeoutId);
      if (overallTimeoutId) clearTimeout(overallTimeoutId);
    };

    const abortDownload = (reason) => {
      if (isAborted) return;
      isAborted = true;

      console.error(`[REMOTE-UPLOAD] Aborting download: ${reason}`);
      cleanup();
      response.data.destroy();
      writeStream.destroy();

      // Clean up temp file
      if (fs.existsSync(tempFilePath)) {
        fs.unlinkSync(tempFilePath);
      }
    };

    // Overall download timeout (30 minutes)
    overallTimeoutId = setTimeout(() => {
      const error = new Error("Download timeout: exceeded 30 minute maximum");
      error.code = "DOWNLOAD_TIMEOUT";
      abortDownload("overall timeout (30 minutes)");
    }, 30 * 60 * 1000);

    // Function to reset idle timeout
    const resetIdleTimeout = () => {
      if (idleTimeoutId) clearTimeout(idleTimeoutId);

      // Idle timeout (1 minute of no data)
      idleTimeoutId = setTimeout(() => {
        const error = new Error("Download stalled: no data received for 60 seconds");
        error.code = "DOWNLOAD_STALLED";
        abortDownload("idle timeout (60 seconds)");
      }, 60 * 1000);
    };

    // Start idle timeout
    resetIdleTimeout();

    // Monitor download size
    response.data.on("data", (chunk) => {
      if (isAborted) return;

      downloadedSize += chunk.length;
      lastDataTime = Date.now();
      resetIdleTimeout(); // Reset idle timer on each data chunk

      // Check size limit
      if (downloadedSize > PROXY_FILE_LIMIT) {
        abortDownload(`size limit exceeded (${formatBytes(PROXY_FILE_LIMIT)})`);

        const error = new Error(`File size exceeds limit of ${formatBytes(PROXY_FILE_LIMIT)}`);
        error.code = "FILE_TOO_LARGE";
        throw error;
      }
    });

    // Pipe download to file
    response.data.pipe(writeStream);

    // Wait for download to complete
    await new Promise((resolve, reject) => {
      writeStream.on("finish", () => {
        cleanup();
        resolve();
      });
      writeStream.on("error", (err) => {
        cleanup();
        reject(err);
      });
      response.data.on("error", (err) => {
        cleanup();
        reject(err);
      });
    });

    const downloadDuration = Date.now() - downloadStart;
    console.log(`[REMOTE-UPLOAD] Downloaded ${formatBytes(downloadedSize)} in ${downloadDuration}ms`);

    // Get filename from URL or Content-Disposition header
    let filename = path.basename(url.pathname) || "download";
    const contentDisposition = response.headers["content-disposition"];
    if (contentDisposition) {
      const filenameMatch = contentDisposition.match(/filename[^;=\n]*=((['"]).*?\2|[^;\n]*)/);
      if (filenameMatch && filenameMatch[1]) {
        filename = filenameMatch[1].replace(/['"]/g, "");
      }
    }

    // Detect MIME type
    const contentType = response.headers["content-type"];
    const mimeType = contentType?.split(";")[0] || mime.lookup(filename) || "application/octet-stream";

    // Upload to IPFS
    const formData = new FormData();
    const fileStream = fs.createReadStream(tempFilePath);

    formData.append("file", fileStream, {
      filename: filename,
      contentType: mimeType,
      knownLength: downloadedSize,
    });

    const uploadStart = Date.now();
    console.log(`[REMOTE-UPLOAD] Starting IPFS upload for ${filename}...`);

    const ipfsResponse = await axios.post(`${IPFS_API}/api/v0/add?pin=false`, formData, {
      headers: { ...formData.getHeaders() },
      timeout: 3600000, // 1 hour timeout
      maxContentLength: Infinity,
      maxBodyLength: Infinity,
    });

    const uploadDuration = Date.now() - uploadStart;
    const cid = ipfsResponse.data.Hash;

    console.log(`[REMOTE-UPLOAD] Upload complete: CID=${cid}, duration=${uploadDuration}ms`);

    // Clean up temp file
    await unlinkAsync(tempFilePath).catch((err) => console.warn("[REMOTE-UPLOAD] Failed to delete temp file:", err.message));
    tempFilePath = null;

    // Return JSON response with detailed information
    const uploadDetails = {
      status: "success",
      cid: cid,
      url: `https://dweb.link/ipfs/${cid}`,
      filename: filename,
      size: downloadedSize,
      type: mimeType,
      sourceUrl: targetUrl,
      timing: {
        download_ms: downloadDuration,
        upload_ms: uploadDuration,
        total_ms: downloadDuration + uploadDuration,
      },
      timestamp: new Date().toISOString(),
    };

    console.log(`[REMOTE-UPLOAD] Success:`, uploadDetails);

    res.json(uploadDetails);

  } catch (err) {
    // Clean up temp file on error
    if (tempFilePath && fs.existsSync(tempFilePath)) {
      await unlinkAsync(tempFilePath).catch((cleanupErr) =>
        console.warn("[REMOTE-UPLOAD] Failed to delete temp file on error:", cleanupErr.message)
      );
    }

    console.error("[REMOTE-UPLOAD] Error:", {
      message: err.message,
      code: err.code,
      timestamp: new Date().toISOString(),
    });

    // Handle specific error types
    if (err.code === "FILE_TOO_LARGE") {
      return res.status(413).json({
        error: "File too large",
        status: "error",
        message: err.message,
        limit: formatBytes(PROXY_FILE_LIMIT),
        timestamp: new Date().toISOString(),
      });
    }

    if (err.code === "DOWNLOAD_TIMEOUT") {
      return res.status(504).json({
        error: "Download timeout",
        status: "error",
        message: "Download exceeded 30 minute maximum",
        timestamp: new Date().toISOString(),
      });
    }

    if (err.code === "DOWNLOAD_STALLED") {
      return res.status(504).json({
        error: "Download stalled",
        status: "error",
        message: "No data received for 60 seconds",
        timestamp: new Date().toISOString(),
      });
    }

    if (err.code === "ENOTFOUND" || err.code === "ECONNREFUSED") {
      return res.status(502).json({
        error: "Failed to download URL",
        status: "error",
        message: "Could not connect to the remote server",
        timestamp: new Date().toISOString(),
      });
    }

    res.status(500).json({
      error: "Remote upload failed",
      status: "error",
      message: err.message,
      timestamp: new Date().toISOString(),
    });
  } finally {
    // Always decrement the counter, even on errors
    activeDownloads--;
    console.log(`[REMOTE-UPLOAD] Download complete. Active downloads: ${activeDownloads}/${MAX_CONCURRENT_DOWNLOADS}`);
  }
};

module.exports = {
  healthHandler,
  statusHandler,
  nostrHandler,
  uploadHandler,
  pinsHandler,
  remoteUploadHandler,
};
