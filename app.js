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
} = require("./modules/routes");

const { runNostrJob, pinnerJob } = require("./modules/jobs");
const { decodePubkey } = require("./modules/nostr");

// Validate NPUB - treat invalid NPUBs as unset
let NPUB = null;
if (process.env.NPUB) {
  try {
    // Validate by attempting to decode
    decodePubkey(process.env.NPUB);
    NPUB = process.env.NPUB;
    console.log(`[STARTUP] NPUB_VALID npub=${NPUB}`);
  } catch (err) {
    console.error(`[STARTUP] NPUB_INVALID npub="${process.env.NPUB}" error="${err.message}" action=nostr_disabled`);
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
app.get("/nostr", (req, res) => nostrHandler(req, res, NPUB));
app.get("/api/pins", pinsHandler);
app.post("/upload", upload.single("file"), uploadHandler);

// Apply error handler
app.use(errorHandler);

// Start server
const server = app.listen(PORT, HOST, () => {
  console.log(`[STARTUP] SERVER_LISTENING host=${HOST} port=${PORT} url=http://${HOST}:${PORT}`);
  console.log(`[STARTUP] IPFS_API_ENDPOINT url=http://127.0.0.1:5001`);
  console.log(`[STARTUP] ROUTES_ENABLED endpoints=[/health,/status,/nostr,/api/pins,/upload]`);
  console.log(`[STARTUP] FRONTEND_PAGES pages=[/index.html,/admin.html]`);
});

// Setup Nostr jobs
let nostrTimers = { discovery: null, pinner: null };

if (NPUB) {
  runNostrJob(NPUB); // Initial run
  nostrTimers.discovery = setInterval(() => runNostrJob(NPUB), NOSTR_CHECK_INTERVAL_MS);
  pinnerJob(); // Start continuous pinner loop
  console.log(`[STARTUP] NOSTR_ENABLED npub=${NPUB} discovery_interval_ms=${NOSTR_CHECK_INTERVAL_MS} pinner_mode=continuous`);
} else {
  console.log(`[STARTUP] NOSTR_DISABLED reason=no_npub_configured`);
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
