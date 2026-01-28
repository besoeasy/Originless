// API route handlers (Node.js, axios-based)
const fs = require("fs");
const mime = require("mime-types");
const axios = require("axios");
const FormData = require("form-data");

const { IPFS_API, STORAGE_MAX, FILE_LIMIT, PROXY_FILE_LIMIT, formatBytes, UPLOAD_TEMP_DIR } = require("./config");
const { checkIPFSHealth, getIPFSStats, pinCid, unpinCid } = require("./ipfs");
const { getGatewayUrl, refreshGateways } = require("./gateways");


const {
  getPins,
  getPinsByType,
  getStats,
  getTotalCount,
  getRecentPins,
  countByTypeAndStatus,
  getLastPinnerActivity,
  getPinsByAuthor,
  recordPin,
  deletePin,
  getPinByCid,
} = require("./database");

const unlinkSafe = async (filePath, context) => {
  if (!filePath) return;

  try {
    await fs.promises.unlink(filePath);
  } catch (err) {
    if (err.code !== "ENOENT") {
      console.warn(`${context || "Failed to delete temp file"}: ${err.message}`);
    }
  }
};

const axiosRequest = async (config, timeoutMs = 10000) => {
  const res = await axios({
    timeout: timeoutMs,
    validateStatus: () => true,
    ...config,
  });

  if (res.status < 200 || res.status >= 300) {
    let text = "";
    if (typeof res.data === "string") {
      text = res.data;
    } else if (Buffer.isBuffer(res.data)) {
      text = res.data.toString("utf8");
    } else if (res.data && typeof res.data === "object") {
      try {
        text = JSON.stringify(res.data);
      } catch {
        text = "";
      }
    }

    const error = new Error(`HTTP ${res.status} ${res.statusText}${text ? `: ${text}` : ""}`);
    error.status = res.status;
    throw error;
  }

  return res;
};

const axiosStream = async (config, timeoutMs = 10000) => {
  const res = await axios({
    responseType: "stream",
    timeout: timeoutMs,
    validateStatus: () => true,
    ...config,
  });

  if (res.status < 200 || res.status >= 300) {
    const error = new Error(`Remote server returned ${res.status}: ${res.statusText}`);
    error.status = res.status;
    if (res.data && res.data.destroy) {
      res.data.destroy();
    }
    throw error;
  }

  return res;
};

const streamToFileWithLimit = (readableStream, filePath, sizeLimit) => new Promise((resolve, reject) => {
  if (!readableStream) {
    reject(new Error("Response has no body"));
    return;
  }

  const sink = fs.createWriteStream(filePath);
  let downloadedSize = 0;

  const onError = (err) => {
    sink.destroy();
    reject(err);
  };

  readableStream.on("data", (chunk) => {
    downloadedSize += chunk.length;
    if (downloadedSize > sizeLimit) {
      const error = new Error(`File size exceeds limit of ${formatBytes(sizeLimit)}`);
      error.code = "FILE_TOO_LARGE";
      readableStream.destroy(error);
    }
  });

  readableStream.on("error", onError);
  sink.on("error", onError);
  sink.on("finish", () => resolve(downloadedSize));

  readableStream.pipe(sink);
});

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

    // Prepare file for IPFS using Node stream
    const formData = new FormData();

    // Detect correct MIME type from file extension
    const mimeType = mime.lookup(req.file.originalname) || req.file.mimetype || "application/octet-stream";

    formData.append("file", fs.createReadStream(filePath), {
      filename: req.file.originalname,
      contentType: mimeType,
    });

    // Upload to IPFS
    const uploadStart = Date.now();
    console.log(`Starting IPFS upload for ${req.file.originalname} ...`);

    const response = await axiosRequest({
      url: `${IPFS_API}/api/v0/add?pin=false`,
      method: "POST",
      data: formData,
      headers: formData.getHeaders(),
      maxContentLength: Infinity,
      maxBodyLength: Infinity,
    }, 3600000);

    const responseJson = response.data;

    // Detailed logging
    const uploadDetails = {
      name: req.file.originalname,
      size_bytes: req.file.size,
      mime_type: mimeType,
      cid: responseJson.Hash,
      upload_duration_ms: Date.now() - uploadStart,
      timestamp: new Date().toISOString(),
    };
    console.log("File uploaded successfully:", uploadDetails);

    // Clean up temp file after successful upload
    await unlinkSafe(filePath, "Failed to delete temp file");

    // Simple response
    res.json({
      status: "success",
      url: await getGatewayUrl(responseJson.Hash, req.file.originalname),
      cid: responseJson.Hash,
      size: uploadDetails.size_bytes,
      type: mimeType,
      filename: req.file.originalname,
    });
  } catch (err) {
    if (filePath) {
      await unlinkSafe(filePath, "Failed to delete temp file on error");
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
          // npub: pin.npub,
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

    // Download the file with fetch streaming support
    const downloadStart = Date.now();
    const response = await axiosStream({
      url: targetUrl,
      method: "GET",
      maxRedirects: 5,
    }, 1800000);

    const contentLength = Number(response.headers["content-length"]) || 0;
    if (contentLength && contentLength > PROXY_FILE_LIMIT) {
      const error = new Error(`File size exceeds limit of ${formatBytes(PROXY_FILE_LIMIT)}`);
      error.code = "FILE_TOO_LARGE";
      throw error;
    }

    // Get filename from URL or Content-Disposition header
    let filename = path.basename(url.pathname) || "download";
    let mimeType = "application/octet-stream";

    // Get headers from response
    const contentDisposition = response.headers["content-disposition"];
    if (contentDisposition) {
      const filenameMatch = contentDisposition.match(/filename[^;=\n]*=((['"]).*?\2|[^;\n]*)/);
      if (filenameMatch && filenameMatch[1]) {
        filename = filenameMatch[1].replace(/['"]/g, "");
      }
    }

    // Detect MIME type from Content-Type header
    const contentType = response.headers["content-type"];
    mimeType = contentType?.split(";")[0] || mime.lookup(filename) || "application/octet-stream";

    // Generate temp file path
    const randomName = crypto.randomBytes(16).toString("hex");
    tempFilePath = path.join(UPLOAD_TEMP_DIR, randomName);

    // Create write stream
    const downloadedSize = await streamToFileWithLimit(response.data, tempFilePath, PROXY_FILE_LIMIT);

    const downloadDuration = Date.now() - downloadStart;
    console.log(`[REMOTE-UPLOAD] Downloaded ${formatBytes(downloadedSize)} in ${downloadDuration}ms`);

    // Upload to IPFS
    const formData = new FormData();
    formData.append("file", fs.createReadStream(tempFilePath), { filename });

    const uploadStart = Date.now();
    console.log(`[REMOTE-UPLOAD] Starting IPFS upload for ${filename}...`);

    const ipfsResponse = await axiosRequest({
      url: `${IPFS_API}/api/v0/add?pin=false`,
      method: "POST",
      data: formData,
      headers: formData.getHeaders(),
      maxContentLength: Infinity,
      maxBodyLength: Infinity,
    }, 3600000);

    const ipfsJson = ipfsResponse.data;

    const uploadDuration = Date.now() - uploadStart;
    const cid = ipfsJson.Hash;

    console.log(`[REMOTE-UPLOAD] Upload complete: CID=${cid}, duration=${uploadDuration}ms`);

    // Clean up temp file
    await unlinkSafe(tempFilePath, "[REMOTE-UPLOAD] Failed to delete temp file");
    tempFilePath = null;

    // Return JSON response with detailed information
    const uploadDetails = {
      status: "success",
      cid: cid,
      url: await getGatewayUrl(cid, filename),
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
    await unlinkSafe(tempFilePath, "[REMOTE-UPLOAD] Failed to delete temp file on error");

    console.error("[REMOTE-UPLOAD] Error:", {
      message: err.message,
      code: err.code,
      name: err.name,
      timestamp: new Date().toISOString(),
    });

    // Handle specific error types

    // File size limit error
    if (err.code === "FILE_TOO_LARGE") {
      return res.status(413).json({
        error: "File too large",
        status: "error",
        message: err.message,
        limit: formatBytes(PROXY_FILE_LIMIT),
        timestamp: new Date().toISOString(),
      });
    }

    // Timeout
    if (err.code === "ECONNABORTED" || err.message?.toLowerCase().includes("timeout")) {
      return res.status(504).json({
        error: "Download timeout",
        status: "error",
        message: "Timeout during download",
        details: err.message || "Request timeout",
        timestamp: new Date().toISOString(),
      });
    }

    // HTTP error
    if (err.status) {
      return res.status(err.status).json({
        error: "HTTP error",
        status: "error",
        message: err.message,
        timestamp: new Date().toISOString(),
      });
    }

    // Network errors
    if (err.name === "TypeError" || err.code === "ENOTFOUND" || err.code === "ECONNREFUSED") {
      return res.status(502).json({
        error: "Failed to download URL",
        status: "error",
        message: "Could not connect to the remote server",
        details: err.message,
        timestamp: new Date().toISOString(),
      });
    }

    // Fallback for any other errors
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

// Add Pin Handler (Auth required)
const pinAddHandler = async (req, res) => {
  try {
    const { cids } = req.body;
    if (!Array.isArray(cids)) {
      return res.status(400).json({ error: "cids must be an array" });
    }

    const userId = req.user.id;
    const results = [];

    for (const cid of cids) {
      // pinCid handles pinning logic
      const result = await pinCid(cid);

      // Record in DB
      recordPin({
        cid,
        author: userId,
        type: 'user_pin',
        status: result.alreadyPinned ? 'pinned' : 'pending',
        timestamp: Date.now(),
        // other fields implicit in recordPin or handled by it
      });

      results.push({ cid, status: result.message, pinned: result.success || result.alreadyPinned });
    }

    res.json({ success: true, results });
  } catch (err) {
    console.error("Pin add error:", err);
    res.status(500).json({ error: err.message });
  }
};

// List Pins Handler (Auth required)
const pinListHandler = async (req, res) => {
  try {
    const limit = Math.min(parseInt(req.query.limit) || 50, 100);
    const offset = parseInt(req.query.offset) || 0;
    const pins = getPinsByAuthor(req.user.id, limit, offset);
    res.json({ success: true, pins });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
};

// Remove Pin Handler (Auth required)
const pinRemoveHandler = async (req, res) => {
  try {
    const { cid } = req.body;
    if (!cid) return res.status(400).json({ error: "CID required" });

    // Verify ownership
    const pin = getPinByCid(cid);
    if (!pin) {
      // If not in DB, maybe just try to unpin from IPFS if user claims it?
      // But for security, we only allow removing tracked pins.
      return res.status(404).json({ error: "Pin not found" });
    }

    if (pin.author !== req.user.id) {
      return res.status(403).json({ error: "Not authorized to remove this pin" });
    }

    await unpinCid(cid);
    deletePin(cid);
    res.json({ success: true, cid });
  } catch (err) {
    console.error("Pin remove error:", err);
    res.status(500).json({ error: err.message });
  }
};

module.exports = {
  healthHandler,
  statusHandler,
  uploadHandler,
  pinsHandler,
  remoteUploadHandler,
  pinAddHandler,
  pinListHandler,
  pinRemoveHandler,
};
