require("dotenv").config();

// Main application entry point
const express = require("express");
const fs = require("fs");

// Import modules
const {
  PORT,
  HOST,
  UPLOAD_TEMP_DIR,
  NOSTR_CHECK_INTERVAL_MS,
} = require("./modules/config");

const { setupMiddleware, upload, errorHandler } = require("./modules/middleware");
const {
  healthHandler,
  statusHandler,
  nostrHandler,
  uploadHandler,
  pinsHandler,
  remoteUploadHandler,
} = require("./modules/routes");

const { runNostrJob, pinnerJob } = require("./modules/jobs");
const { decodePubkey } = require("./modules/nostr");

// Validate NPUBs - treat invalid NPUBs as unset
let NPUBS = [];
if (process.env.NPUB) {
  // Parse comma-separated NPUBs
  const rawNpubs = process.env.NPUB.split(',').map(n => n.trim()).filter(n => n);
  const validNpubs = [];
  const invalidNpubs = [];

  for (const npub of rawNpubs) {
    try {
      // Validate by attempting to decode
      decodePubkey(npub);
      validNpubs.push(npub);
    } catch (err) {
      invalidNpubs.push({ npub, error: err.message });
    }
  }

  NPUBS = validNpubs;

  if (validNpubs.length > 0) {
    console.log(`[STARTUP] NPUBS_VALID count=${validNpubs.length} npubs=${validNpubs.map(n => n.slice(0, 12) + '...').join(',')}`);
  }

  if (invalidNpubs.length > 0) {
    invalidNpubs.forEach(({ npub, error }) => {
      console.error(`[STARTUP] NPUB_INVALID npub="${npub}" error="${error}" action=skipped`);
    });
  }

  if (validNpubs.length === 0) {
    console.error(`[STARTUP] NO_VALID_NPUBS total_provided=${rawNpubs.length} action=nostr_disabled`);
  }
}

// Ensure temp directory exists
if (!fs.existsSync(UPLOAD_TEMP_DIR)) {
  fs.mkdirSync(UPLOAD_TEMP_DIR, { recursive: true });
}

// Initialize Express app
const app = express();

// Setup middleware
setupMiddleware(app);

// API Routes
app.get("/health", healthHandler);
app.get("/status", statusHandler);
app.get("/nostr", (req, res) => nostrHandler(req, res, NPUBS));
app.get("/api/pins", pinsHandler);
app.post("/upload", upload.single("file"), uploadHandler);
app.post("/remoteupload", remoteUploadHandler);


// Apply error handler
app.use(errorHandler);

// Start server
const server = app.listen(PORT, HOST, () => {
  console.log(`[STARTUP] SERVER_LISTENING host=${HOST} port=${PORT} url=http://${HOST}:${PORT}`);
});

// Setup Nostr jobs
let nostrTimers = { discovery: null, pinner: null };

if (NPUBS.length > 0) {
  runNostrJob(NPUBS); // Initial run
  nostrTimers.discovery = setInterval(() => runNostrJob(NPUBS), NOSTR_CHECK_INTERVAL_MS);
  pinnerJob(); // Start continuous pinner loop
  console.log(`[STARTUP] NOSTR_ENABLED npub_count=${NPUBS.length} discovery_interval_ms=${NOSTR_CHECK_INTERVAL_MS} pinner_mode=continuous`);
} else {
  console.log(`[STARTUP] NOSTR_DISABLED reason=no_valid_npubs_configured`);
}

// Graceful shutdown handler
const gracefulShutdown = (signal) => {
  console.log(`[SHUTDOWN] SIGNAL_RECEIVED signal=${signal} action=graceful_shutdown`);

  // Stop accepting new connections
  server.close(() => {
    console.log(`[SHUTDOWN] HTTP_SERVER_CLOSED`);
  });

  // Clear Nostr timers
  if (nostrTimers.discovery) clearInterval(nostrTimers.discovery);
  console.log(`[SHUTDOWN] NOSTR_TIMERS_CLEARED`);

  // Give active requests 5 seconds to complete
  setTimeout(() => {
    console.log(`[SHUTDOWN] FORCE_EXIT timeout_sec=5`);
    process.exit(0);
  }, 5000);
};

process.on("SIGTERM", () => gracefulShutdown("SIGTERM"));
process.on("SIGINT", () => gracefulShutdown("SIGINT"));
