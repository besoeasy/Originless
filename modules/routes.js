// API route handlers
const axios = require("axios");
const FormData = require("form-data");
const fs = require("fs");
const { promisify } = require("util");
const mime = require("mime-types");

const { IPFS_API, STORAGE_MAX, FILE_LIMIT, formatBytes } = require("./config");
const { getPinnedSize, checkIPFSHealth, getIPFSStats } = require("./ipfs");
const {
  getLastPinnerActivity,
  getLastNostrRun,
} = require("./queue");

const {
  decodePubkey,
  fetchFollowingPubkeys,
  toNpub,
  constants: { DEFAULT_RELAYS },
} = require("./nostr");

const {
  getPins,
  getPinsByType,
  getStats,
  getTotalCount,
  getRecentPins,
  countByTypeAndStatus,
} = require("./database");

const unlinkAsync = promisify(fs.unlink);

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
const nostrHandler = async (req, res, NPUB) => {
  if (!NPUB) {
    return res.status(200).json({
      enabled: false,
      reason: "NPUB not set",
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

    // Get friends list
    let friendsList = [];
    if (lastNostrRun?.friends?.following) {
      friendsList = lastNostrRun.friends.following;
    } else {
      try {
        const hex = decodePubkey(NPUB);
        const follows = await fetchFollowingPubkeys({ pubkey: hex });
        friendsList = follows.map((f) => toNpub(f));
      } catch (err) {
        console.error("Failed to fetch following list for API", err.message);
      }
    }

    const operatorNpub = NPUB.startsWith("npub") ? NPUB : toNpub(NPUB);

    // Get counts from database
    const selfPinned = countByTypeAndStatus('self', 'pinned');
    const selfPending = countByTypeAndStatus('self', 'pending');
    const selfFailed = countByTypeAndStatus('self', 'failed');
    
    const friendsCached = countByTypeAndStatus('friend', 'cached');
    const friendsPending = countByTypeAndStatus('friend', 'pending');
    const friendsFailed = countByTypeAndStatus('friend', 'failed');

    // Build lastRun object
    let lastRun = null;
    if (lastNostrRun?.at) {
      lastRun = {
        at: lastNostrRun.at,
        error: lastNostrRun.error,
        self: lastNostrRun.self || null,
        friends: lastNostrRun.friends || null,
      };
    }

    res.status(200).json({
      enabled: true,
      operator: operatorNpub,
      relays: DEFAULT_RELAYS,
      friends: friendsList,
      repo,
      pins: {
        self: {
          pinned: selfPinned,
          pending: selfPending,
          failed: selfFailed,
          total: selfPinned + selfPending + selfFailed,
        },
        friends: {
          cached: friendsCached,
          pending: friendsPending,
          failed: friendsFailed,
          total: friendsCached + friendsPending + friendsFailed,
        },
        totalSize: pinnedStats.totalSize,
        pinnedCount: pinnedStats.count,
      },
      activity: {
        lastDiscovery: lastNostrRun?.at || null,
        lastPinner: getLastPinnerActivity(),
      },
      lastRun,
    });
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
    const type = req.query.type; // 'self', 'friend', or undefined for all

    let pins;
    if (type && (type === 'self' || type === 'friend')) {
      pins = getPinsByType(type, limit, offset);
    } else {
      pins = getPins(limit, offset);
    }

    const total = getTotalCount();
    const stats = getStats();

    res.json({
      success: true,
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
      })),
      pagination: {
        limit,
        offset,
        total,
        hasMore: offset + limit < total,
      },
      stats: stats.reduce((acc, stat) => {
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

module.exports = {
  healthHandler,
  statusHandler,
  nostrHandler,
  uploadHandler,
  pinsHandler,
};
