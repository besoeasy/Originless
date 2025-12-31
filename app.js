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
  isPinned,
  pinCid,
  addCid,
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

const NOSTR_CHECK_INTERVAL_MS = 11 * 60 * 1000; // 11 minutes
const PINNER_INTERVAL_MS = 2 * 60 * 1000; // 2 minutes

let lastNostrRun = {
  at: null,
  self: null,
  friends: null,
  error: null,
};

// CID queues for pinning
let selfCidQueue = [];
let friendsCidQueue = [];

// Counters for tracking actual pins/caches
let totalPinnedSelf = 0;
let totalCachedFriends = 0;
let lastPinnerActivity = null;

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

    // Build lastRun object with queue-based data
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
        self: totalPinnedSelf,
        friends: totalCachedFriends,
        total: totalPinnedSelf + totalCachedFriends,
      },
      queues: {
        self: {
          pending: selfCidQueue.length,
          processed: totalPinnedSelf,
        },
        friends: {
          pending: friendsCidQueue.length,
          processed: totalCachedFriends,
        },
      },
      activity: {
        lastDiscovery: lastNostrRun?.at || null,
        lastPinner: lastPinnerActivity,
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

let timerprobilitymethod = 0.9;

const runNostrJob = async () => {
  if (!NPUB) {
    return;
  }

  if (Math.random() < timerprobilitymethod) {
    timerprobilitymethod = timerprobilitymethod - 0.025;
    if (timerprobilitymethod < 0.2) {
      timerprobilitymethod = 0.2;
    }
    console.log("Nostr discovery job: Executing (random trigger)");
  } else {
    console.log("Nostr discovery job: Skipping (random delay)");
    return;
  }

  try {
    // Fetch CIDs without pinning (dryRun = true)
    const selfResult = await syncNostrPins({ npubOrPubkey: NPUB, dryRun: true });
    const friendsResult = await syncFollowPins({ npubOrPubkey: NPUB, dryRun: true });

    // Add discovered CIDs to queues (avoid duplicates)
    const selfCids = selfResult.plannedPins || [];
    const friendCids = friendsResult.plannedAdds || [];

    const selfSet = new Set(selfCidQueue);
    const friendSet = new Set(friendsCidQueue);

    const newSelfCids = selfCids.filter(cid => !selfSet.has(cid));
    const newFriendCids = friendCids.filter(cid => !friendSet.has(cid));

    selfCidQueue.push(...newSelfCids);
    friendsCidQueue.push(...newFriendCids);

    lastNostrRun = {
      at: new Date().toISOString(),
      self: {
        eventsScanned: selfResult.eventsScanned,
        cidsFound: selfResult.cidsFound,
        newCids: newSelfCids.length,
        queueSize: selfCidQueue.length,
      },
      friends: {
        eventsScanned: friendsResult.eventsScanned,
        cidsFound: friendsResult.cidsFound,
        newCids: newFriendCids.length,
        queueSize: friendsCidQueue.length,
      },
      error: null,
    };

    console.log("\n=== Discovery Summary ===");
    console.log({
      self: {
        discovered: selfCids.length,
        new: newSelfCids.length,
        queueSize: selfCidQueue.length,
      },
      friends: {
        discovered: friendCids.length,
        new: newFriendCids.length,
        queueSize: friendsCidQueue.length,
      },
    });
  } catch (err) {
    lastNostrRun = {
      at: new Date().toISOString(),
      self: null,
      friends: null,
      error: err.message,
    };
    console.error("Nostr discovery job failed", err.message);
  }
};

const pinnerJob = async () => {
  try {
    console.log(`\n════ Pinner Job Started ════`);
    console.log(`Queue Status: Self=${selfCidQueue.length}, Friends=${friendsCidQueue.length}`);
    console.log(`Counters: Pinned=${totalPinnedSelf}, Cached=${totalCachedFriends}`);

    let didWork = false;

    // Process self queue: pin CID
    if (selfCidQueue.length > 0) {
      let cidToPinIndex = -1;
      let cidToPin = null;
      const checkedIndices = new Set();

      // Keep trying random CIDs until we find one that's not pinned
      while (checkedIndices.size < selfCidQueue.length) {
        const randomIndex = Math.floor(Math.random() * selfCidQueue.length);
        
        if (checkedIndices.has(randomIndex)) {
          continue; // Already checked this one
        }
        
        checkedIndices.add(randomIndex);
        const cid = selfCidQueue[randomIndex];

        console.log(`\n[Self] Checking CID (${selfCidQueue.length} in queue): ${cid}`);

        const alreadyPinned = await isPinned(cid);
        if (alreadyPinned) {
          console.log(`⏭️  Already pinned, removing from queue: ${cid}`);
          selfCidQueue.splice(randomIndex, 1);
          totalPinnedSelf++;
          didWork = true;
          // Adjust checked indices after splice
          const newCheckedIndices = new Set();
          checkedIndices.forEach(idx => {
            if (idx < randomIndex) {
              newCheckedIndices.add(idx);
            } else if (idx > randomIndex) {
              newCheckedIndices.add(idx - 1);
            }
          });
          checkedIndices.clear();
          newCheckedIndices.forEach(idx => checkedIndices.add(idx));
        } else {
          // Found an unpinned CID
          cidToPinIndex = randomIndex;
          cidToPin = cid;
          break;
        }
      }

      if (cidToPin) {
        console.log(`\n[Self] Pinning CID: ${cidToPin}`);
        await pinCid(cidToPin);
        console.log(`✓ Successfully pinned: ${cidToPin}`);
        selfCidQueue.splice(cidToPinIndex, 1);
        totalPinnedSelf++;
        console.log(`📊 Counter updated: totalPinnedSelf = ${totalPinnedSelf}`);
        console.log(`📋 Queue updated: ${selfCidQueue.length} CIDs remaining`);
        didWork = true;
      } else if (checkedIndices.size > 0) {
        console.log(`✓ All checked CIDs were already pinned and removed`);
      }
    } else {
      console.log(`[Self] Queue empty, nothing to process`);
    }

    // Process friends queue: cache CID
    if (friendsCidQueue.length > 0) {
      const randomIndex = Math.floor(Math.random() * friendsCidQueue.length);
      const cid = friendsCidQueue[randomIndex];

      console.log(`\n[Friend] Caching CID (${friendsCidQueue.length} in queue): ${cid}`);

      await addCid(cid);
      console.log(`✓ Successfully cached: ${cid}`);
      friendsCidQueue.splice(randomIndex, 1);
      totalCachedFriends++;
      console.log(`📊 Counter updated: totalCachedFriends = ${totalCachedFriends}`);
      console.log(`📋 Queue updated: ${friendsCidQueue.length} CIDs remaining`);
      didWork = true;
    } else {
      console.log(`[Friend] Queue empty, nothing to process`);
    }

    if (didWork) {
      lastPinnerActivity = new Date().toISOString();
      console.log(`\n⏰ Activity timestamp updated: ${lastPinnerActivity}`);
      console.log(`📈 Total processed: ${totalPinnedSelf + totalCachedFriends} (Self: ${totalPinnedSelf}, Friends: ${totalCachedFriends})`);
    } else {
      console.log(`\n⏸  No work performed - all queues empty`);
    }

    console.log(`════ Pinner Job Complete ════\n`);
  } catch (err) {
    console.error(`\n❌ Pinner job error:`, err.message);
    console.error(`Stack trace:`, err.stack);
  }
};

let nostrTimers = { discovery: null, pinner: null };

if (NPUB) {
  nostrTimers.discovery = setInterval(runNostrJob, NOSTR_CHECK_INTERVAL_MS);
  nostrTimers.pinner = setInterval(pinnerJob, PINNER_INTERVAL_MS);
  console.log("Nostr queue-based pinning enabled");
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
  if (nostrTimers.discovery) clearInterval(nostrTimers.discovery);
  if (nostrTimers.pinner) clearInterval(nostrTimers.pinner);
  console.log("Nostr timers cleared");

  // Give active requests 5 seconds to complete
  setTimeout(() => {
    console.log("Forcing shutdown");
    process.exit(0);
  }, 5000);
};

process.on("SIGTERM", () => gracefulShutdown("SIGTERM"));
process.on("SIGINT", () => gracefulShutdown("SIGINT"));
