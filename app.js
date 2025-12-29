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
const NPUB = process.env.NPUB || "npub1x6au4qgw9t403yushl34tgngmgcaqv9yna7ywf8e6x4xf686ln7qc7y6wq";
const PIN_FRIENDS = (process.env.PINFRIENDS || "").toLowerCase() === "true";
const NOSTR_INTERVAL_MS = 3 * 60 * 60 * 1000;

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
      addresses: idResponse.data.Addresses,
      agentVersion: idResponse.data.AgentVersion,
      protocolVersion: idResponse.data.ProtocolVersion,
    };

    const peersResponse = await axios.post(`${IPFS_API}/api/v0/swarm/peers`, {
      timeout: 5000,
    });

    const connectedPeers = {
      count: peersResponse.data.Peers.length,
      list: peersResponse.data.Peers,
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

// Periodic Nostr pinning job (every 3 hours) if NPUB is configured
const runNostrJob = async () => {
  if (!NPUB) {
    return;
  }

  try {
    const selfResult = await syncNostrPins({ npubOrPubkey: NPUB, dryRun: false });
    let friendsResult = null;

    if (PIN_FRIENDS) {
      friendsResult = await syncFollowPins({ npubOrPubkey: NPUB, dryRun: false });
    }

    lastNostrRun = {
      at: new Date().toISOString(),
      self: selfResult,
      friends: friendsResult,
      error: null,
    };

    console.log("Nostr pin job completed", {
      at: lastNostrRun.at,
      selfPinned: selfResult?.pinned ?? selfResult?.plannedPins?.length ?? 0,
      friendsPinned: friendsResult?.pinned ?? friendsResult?.plannedPins?.length ?? 0,
    });
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

if (NPUB) {
  // Kick off shortly after start, then repeat on interval
  setTimeout(runNostrJob, 5_000);
  setInterval(runNostrJob, NOSTR_INTERVAL_MS);
} else {
  console.log("Nostr pinning disabled: NPUB not set");
}

// Simple UI endpoint to show operator and friends (if enabled)
app.get("/nostr-info", async (req, res) => {
  if (!NPUB) {
    return res.status(200).send("<html><body style=\"font-family: Arial, sans-serif; padding:20px;\"><h2>FileDrop</h2><p>Nostr pinning is disabled (NPUB not set).</p></body></html>");
  }

  let friendsList = [];
  if (PIN_FRIENDS) {
    if (lastNostrRun?.friends?.following) {
      friendsList = lastNostrRun.friends.following;
    } else {
      try {
        const hex = decodePubkey(NPUB);
        const follows = await fetchFollowingPubkeys({ pubkey: hex });
        friendsList = follows.map((f) => toNpub(f));
      } catch (err) {
        console.error("Failed to fetch following list for UI", err.message);
      }
    }
  }

  const operatorNpub = NPUB.startsWith("npub") ? NPUB : toNpub(NPUB);
  const friendItems = friendsList
    .map((f) => `<li><a href=\"https://nosta.me/${encodeURIComponent(f)}\" target=\"_blank\">${f}</a></li>`)
    .join("");

  const html = `<!doctype html>
  <html><head><title>FileDrop Nostr</title>
  <style>
    body { font-family: Arial, sans-serif; background: #0d0f12; color: #e8ecf2; padding: 32px; }
    .card { max-width: 720px; margin: 0 auto; background: #161a21; border: 1px solid #2a3140; border-radius: 14px; padding: 24px 28px; box-shadow: 0 18px 40px rgba(0,0,0,0.35); }
    h1 { margin: 0 0 12px; font-size: 26px; letter-spacing: 0.3px; }
    .muted { color: #9aa7bd; }
    .pill { display: inline-block; padding: 6px 10px; border-radius: 999px; background: #1f2633; color: #c8d4e6; font-size: 12px; margin-right: 6px; }
    ul { list-style: none; padding: 0; margin: 12px 0 0; display: grid; gap: 6px; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); }
    li { background: #1b202a; border: 1px solid #262d3b; border-radius: 10px; padding: 10px 12px; }
    a { color: #7bc4ff; text-decoration: none; }
    a:hover { text-decoration: underline; }
  </style></head>
  <body>
    <div class="card">
      <h1>FileDrop Node</h1>
      <div class="muted" style="margin-bottom: 12px;">This node automatically pins Nostr media every 3 hours.</div>
      <div style="margin-bottom: 10px;">
        <span class="pill">Operator</span>
        <a href="https://nosta.me/${encodeURIComponent(operatorNpub)}" target="_blank">${operatorNpub}</a>
      </div>
      <div style="margin-bottom: 14px;">
        <span class="pill">Relays</span>
        wss://relay.damus.io · wss://nos.lol
      </div>
      ${PIN_FRIENDS ? `<div style="margin-top: 10px;">
        <span class="pill">Friends pinned</span>
        ${friendsList.length ? "" : "<span class=\"muted\">No follows detected</span>"}
        ${friendsList.length ? `<ul>${friendItems}</ul>` : ""}
      </div>` : `<div class="muted">Friend pinning is disabled (set PINFRIENDS=true to enable).</div>`}
      ${lastNostrRun?.at ? `<div style="margin-top: 16px;" class="muted">Last run: ${lastNostrRun.at}${lastNostrRun.error ? " · Error: " + lastNostrRun.error : ""}</div>` : ""}
    </div>
  </body></html>`;

  res.status(200).send(html);
});

// Apply error handler
app.use(errorHandler);

// Start server
app.listen(PORT, HOST, () => {
  console.log(`Server running on http://${HOST}:${PORT}`);
  console.log(`IPFS API endpoint: ${IPFS_API}`);
});
