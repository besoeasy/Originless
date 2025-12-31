// Main application entry point
const express = require("express");
const fs = require("fs");

// Import modules
const { 
  PORT, 
  HOST, 
  UPLOAD_TEMP_DIR,
  NOSTR_CHECK_INTERVAL_MS,
  PINNER_INTERVAL_MS,
} = require("./modules/config");

const { setupMiddleware, upload, errorHandler } = require("./modules/middleware");
const { 
  healthHandler, 
  statusHandler, 
  nostrHandler, 
  uploadHandler 
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
    console.log(`Valid NPUB configured: ${NPUB}`);
  } catch (err) {
    console.error(`Invalid NPUB provided: "${process.env.NPUB}". Nostr pinning disabled. Error: ${err.message}`);
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
app.post("/upload", upload.single("file"), uploadHandler);

// Apply error handler
app.use(errorHandler);

// Start server
const server = app.listen(PORT, HOST, () => {
  console.log(`Server running on http://${HOST}:${PORT}`);
  console.log(`IPFS API endpoint: http://127.0.0.1:5001`);
});

// Setup Nostr jobs
let nostrTimers = { discovery: null, pinner: null };

if (NPUB) {
  nostrTimers.discovery = setInterval(() => runNostrJob(NPUB), NOSTR_CHECK_INTERVAL_MS);
  nostrTimers.pinner = setInterval(pinnerJob, PINNER_INTERVAL_MS);
  console.log("Nostr queue-based pinning enabled");
} else {
  console.log("Nostr pinning disabled: NPUB not set");
}

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
