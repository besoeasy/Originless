const express = require("express");
const multer = require("multer");
const axios = require("axios");
const FormData = require("form-data");
const path = require("path");
const cors = require("cors");
const compression = require("compression");
const fs = require("fs");
const { promisify } = require("util");
const mime = require("mime-types");
const unlinkAsync = promisify(fs.unlink);
const {
  syncNostrPins,
  syncFollowPins,
  fetchFollowingPubkeys,
  decodePubkey,
  toNpub,
  constants: { DEFAULT_RELAYS },
} = require("./nostr");

// Parse human-readable size format (e.g., "5GB", "50MB") to bytes
const parseSize = (sizeStr) => {
  const units = {
    B: 1,
    KB: 1024,
    MB: 1024 * 1024,
    GB: 1024 * 1024 * 1024,
    TB: 1024 * 1024 * 1024 * 1024,
  };

  const match = sizeStr.trim().match(/^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)$/i);
  if (!match) {
    throw new Error(`Invalid size format: ${sizeStr}`);
  }

  const value = parseFloat(match[1]);
  const unit = match[2].toUpperCase();
  return Math.floor(value * units[unit]);
};

// Constants
const IPFS_API = "http://127.0.0.1:5001";
const PORT = 3232;
const STORAGE_MAX = process.env.STORAGE_MAX || "200GB";
const FILE_LIMIT = parseSize(process.env.FILE_LIMIT || "5GB");
const HOST = "0.0.0.0";
const UPLOAD_TEMP_DIR = "/tmp/filedrop";

// Validate NPUB - treat invalid NPUBs as unset
let NPUB = null;
if (process.env.NPUB) {
  try {
    // Validate by attempting to decode
    decodePubkey(process.env.NPUB);
    NPUB = process.env.NPUB;
    console.log(`Valid NPUB configured: ${NPUB}`);
  } catch (err) {
    console.error(`Invalid NPUB provided: "${process.env.NPUB}". Nostr pinning disabled. Error: ${err.message}`);
  }
}

const NOSTR_CHECK_INTERVAL_MS = 7 * 60 * 1000; 

let lastNostrRun = {
  at: null,
  self: null,
  friends: null,
  error: null,
};

// Ensure temp directory exists
if (!fs.existsSync(UPLOAD_TEMP_DIR)) {
  fs.mkdirSync(UPLOAD_TEMP_DIR, { recursive: true });
}

// Initialize Express app
const app = express();

// Middleware setup
app.use(compression());
app.use(express.json());
app.use(express.urlencoded({ extended: true }));
app.use(cors());
app.use(express.static(path.join(__dirname, "public")));

// Configure multer for file uploads with disk storage for streaming
const storage = multer.diskStorage({
  destination: (req, file, cb) => {
    cb(null, UPLOAD_TEMP_DIR);
  },
  filename: (req, file, cb) => {
    // Generate unique filename with timestamp
    const uniqueSuffix = Date.now() + "-" + Math.round(Math.random() * 1e9);
    cb(null, uniqueSuffix + "-" + file.originalname);
  },
});

const upload = multer({
  storage,
  limits: {
    fileSize: FILE_LIMIT, // Max file size in bytes
  },
  fileFilter: (req, file, cb) => {
    cb(null, true);
  },
});

// Error handling middleware
const errorHandler = (err, req, res, next) => {
  // Handle Multer file size limit error
  if (err instanceof multer.MulterError && err.code === "LIMIT_FILE_SIZE") {
    return res.status(413).json({
      error: "File too large",
      message: `File exceeds the maximum allowed size of ${process.env.FILE_LIMIT || "5GB"}`,
      maxSize: process.env.FILE_LIMIT || "5GB",
    });
  }

  console.error("Unexpected error:", err.stack);
  res.status(500).json({ error: "Internal server error" });
};

// Health check endpoint for Docker
app.get("/health", async (req, res) => {
  try {
    const peersResponse = await axios.post(`${IPFS_API}/api/v0/swarm/peers`, { timeout: 5000 });
    const peerCount = peersResponse.data.Peers?.length || 0;

    if (peerCount >= 1) {
      res.status(200).json({ status: "healthy", peers: peerCount });
    } else {
      res.status(503).json({ status: "unhealthy", peers: peerCount, reason: "No peers connected" });
    }
  } catch (err) {
    res.status(503).json({ status: "unhealthy", error: err.message });
  }
});

// Enhanced status endpoint
app.get("/status", async (req, res) => {
  try {
    // Fetch multiple IPFS stats concurrently
    const [bwResponse, repoResponse, idResponse] = await Promise.all([
      axios.post(`${IPFS_API}/api/v0/stats/bw?interval=5m`, { timeout: 5000 }),
      axios.post(`${IPFS_API}/api/v0/repo/stat`, { timeout: 5000 }),
      axios.post(`${IPFS_API}/api/v0/id`, { timeout: 5000 }),
    ]);

    // Format bandwidth data
    const bandwidth = {
      totalIn: bwResponse.data.TotalIn,
      totalOut: bwResponse.data.TotalOut,
      rateIn: bwResponse.data.RateIn,
      rateOut: bwResponse.data.RateOut,
      interval: "1h",
    };

    // Format repository stats
    const repo = {
      size: repoResponse.data.RepoSize,
      storageMax: repoResponse.data.StorageMax,
      numObjects: repoResponse.data.NumObjects,
      path: repoResponse.data.RepoPath,
      version: repoResponse.data.Version,
    };

    // Node identity info
    const nodeInfo = {
      id: idResponse.data.ID,
      publicKey: idResponse.data.PublicKey,
      agentVersion: idResponse.data.AgentVersion,
      protocolVersion: idResponse.data.ProtocolVersion,
    };

    const peersResponse = await axios.post(`${IPFS_API}/api/v0/swarm/peers`, {
      timeout: 5000,
    });

    const connectedPeers = {
      count: peersResponse.data.Peers.length,
    };

    // Get app version from package.json
    const { version: appVersion } = require("./package.json");

    // Format file size limit in human readable form
    const formatBytes = (bytes) => {
      const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
      if (bytes === 0) return "0 Bytes";
      const i = Math.floor(Math.log(bytes) / Math.log(1024));
      return (bytes / Math.pow(1024, i)).toFixed(2) + " " + sizes[i];
    };

    res.json({
      status: "success",
      timestamp: new Date().toISOString(),
      bandwidth,
      repository: repo,
      node: nodeInfo,
      peers: connectedPeers,
      storageLimit: {
        configured: STORAGE_MAX,
        current: formatBytes(repo.storageMax),
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
});

// Combined Nostr stats endpoint (includes repo stats and pin counts)
app.get("/nostr", async (req, res) => {
  if (!NPUB) {
    return res.status(200).json({
      enabled: false,
      reason: "NPUB not set",
    });
  }

  try {
    // Fetch repo stats
    const repoResponse = await axios.post(`${IPFS_API}/api/v0/repo/stat`, { timeout: 5000 });
    const repo = {
      size: repoResponse.data.RepoSize,
      storageMax: repoResponse.data.StorageMax,
      numObjects: repoResponse.data.NumObjects,
    };

    // Get friends list (avoid duplication - only fetch if not in lastRun)
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

    // Calculate pin counts
    const pinnedSelf = lastNostrRun?.self?.pinned ?? lastNostrRun?.self?.plannedPins?.length ?? 0;
    const addedFriends = lastNostrRun?.friends?.added ?? lastNostrRun?.friends?.plannedAdds?.length ?? 0;

    // Build optimized lastRun object (remove redundant relay lists and friend lists)
    let lastRun = null;
    if (lastNostrRun?.at) {
      lastRun = {
        at: lastNostrRun.at,
        error: lastNostrRun.error,
        self: lastNostrRun.self
          ? {
              eventsScanned: lastNostrRun.self.eventsScanned,
              deletesSeen: lastNostrRun.self.deletesSeen,
              cidsFound: lastNostrRun.self.cidsFound,
              pinned: lastNostrRun.self.pinned ?? 0,
              failed: lastNostrRun.self.failed ?? 0,
              results: lastNostrRun.self.results || [],
            }
          : null,
        friends: lastNostrRun.friends
          ? {
              eventsScanned: lastNostrRun.friends.eventsScanned,
              deletesSeen: lastNostrRun.friends.deletesSeen,
              cidsFound: lastNostrRun.friends.cidsFound,
              added: lastNostrRun.friends.added ?? 0,
              failed: lastNostrRun.friends.failed ?? 0,
              results: lastNostrRun.friends.results || [],
            }
          : null,
      };
    }

    res.status(200).json({
      enabled: true,
      operator: operatorNpub,
      relays: DEFAULT_RELAYS,
      friends: friendsList,
      repo,
      pins: {
        self: pinnedSelf,
        friends: addedFriends,
        total: pinnedSelf + addedFriends,
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
});

// Shared upload handler logic
const handleUpload = async (req, res) => {
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

    // Standard Upload Logic
    filePath = req.file.path;

    // --- IPFS Upload Logic (Shared) ---
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

// POST Upload endpoint
app.post("/upload", upload.single("file"), handleUpload);

// Periodic Nostr pinning job (checks every 15 minutes, 40% chance to run) if NPUB is configured
const runNostrJob = async () => {
  if (!NPUB) {
    return;
  }

  if (Math.random() < 0.3) {
    console.log("Nostr job check: Executing (random trigger)");
  } else {
    console.log("Nostr job check: Skipping (random delay)");
    return;
  }

  try {
    const selfResult = await syncNostrPins({ npubOrPubkey: NPUB, dryRun: false });

    let friendsResult = await syncFollowPins({ npubOrPubkey: NPUB, dryRun: false });

    lastNostrRun = {
      at: new Date().toISOString(),
      self: selfResult,
      friends: friendsResult,
      error: null,
    };

    console.log("Nostr pin job completed", {
      at: lastNostrRun.at,
      self: {
        notesScanned: selfResult?.eventsScanned ?? 0,
        cidsFound: selfResult?.cidsFound ?? 0,
        pinned: selfResult?.pinned ?? selfResult?.plannedPins?.length ?? 0,
        failed: selfResult?.failed ?? 0,
      },
      ...(friendsResult && {
        friends: {
          notesScanned: friendsResult?.eventsScanned ?? 0,
          cidsFound: friendsResult?.cidsFound ?? 0,
          added: friendsResult?.added ?? friendsResult?.plannedAdds?.length ?? 0,
          failed: friendsResult?.failed ?? 0,
        },
      }),
    });

    // Log failures with details
    const selfFailures = selfResult?.results?.filter((r) => !r.ok) || [];
    const friendFailures = friendsResult?.results?.filter((r) => !r.ok) || [];

    if (selfFailures.length > 0) {
      console.error("Self pin failures:");
      selfFailures.forEach((f) => console.error(`  CID: ${f.cid} - ${f.error}`));
    }

    if (friendFailures.length > 0) {
      console.error("Friend add failures:");
      friendFailures.forEach((f) => console.error(`  CID: ${f.cid} - ${f.error}`));
    }
  } catch (err) {
    lastNostrRun = {
      at: new Date().toISOString(),
      self: null,
      friends: null,
      error: err.message,
    };

    console.error("Nostr pin job failed", err.message);
  }
};

let nostrTimers = { initial: null, interval: null };

if (NPUB) {
  // Kick off shortly after start, then repeat on interval
  nostrTimers.initial = setTimeout(runNostrJob, 5_000);
  nostrTimers.interval = setInterval(runNostrJob, NOSTR_CHECK_INTERVAL_MS);
} else {
  console.log("Nostr pinning disabled: NPUB not set");
}

// Apply error handler
app.use(errorHandler);

// Start server
const server = app.listen(PORT, HOST, () => {
  console.log(`Server running on http://${HOST}:${PORT}`);
  console.log(`IPFS API endpoint: ${IPFS_API}`);
});

// Graceful shutdown handler
const gracefulShutdown = (signal) => {
  console.log(`\n${signal} received, shutting down gracefully...`);

  // Stop accepting new connections
  server.close(() => {
    console.log("HTTP server closed");
  });

  // Clear Nostr timers
  if (nostrTimers.initial) clearTimeout(nostrTimers.initial);
  if (nostrTimers.interval) clearInterval(nostrTimers.interval);
  console.log("Nostr timers cleared");

  // Give active requests 5 seconds to complete
  setTimeout(() => {
    console.log("Forcing shutdown");
    process.exit(0);
  }, 5000);
};

process.on("SIGTERM", () => gracefulShutdown("SIGTERM"));
process.on("SIGINT", () => gracefulShutdown("SIGINT"));
