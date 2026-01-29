// Load .env for Node.js
require("dotenv").config();

// Main application entry point
const express = require("express");
const fs = require("fs");

// Import modules
const {
  PORT,
  HOST,
  UPLOAD_TEMP_DIR,
} = require("./modules/config");

const { authMiddleware } = require("./modules/auth");

const { setupMiddleware, upload, errorHandler } = require("./modules/middleware");
const {
  healthHandler,
  statusHandler,
  uploadHandler,
  uploadZipHandler,
  pinsHandler,
  remoteUploadHandler,
  pinAddHandler,
  pinListHandler,
  pinRemoveHandler,
} = require("./modules/routes");

const { pinnerJob } = require("./modules/jobs");
const { refreshGateways } = require("./modules/gateways");


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
app.get("/api/pins", pinsHandler);
app.post("/upload", upload.single("file"), uploadHandler);
app.post("/uploadzip", upload.single("file"), uploadZipHandler);
app.post("/remoteupload", remoteUploadHandler);

// Authenticated Pin Routes
app.post("/pin/add", authMiddleware, pinAddHandler);
app.get("/pin/list", authMiddleware, pinListHandler);
app.post("/pin/remove", authMiddleware, pinRemoveHandler);


// Apply error handler
app.use(errorHandler);

// Start server
const server = app.listen(PORT, HOST, () => {
  console.log(`[STARTUP] SERVER_LISTENING host=${HOST} port=${PORT} url=http://${HOST}:${PORT}`);
});

// Start pinner loop
pinnerJob();

// Warm gateway cache and probe every minute
const scheduleGatewayRefresh = () => {
  refreshGateways().catch((err) => {
    console.warn(`[GATEWAY] Refresh failed: ${err.message}`);
  });
};

scheduleGatewayRefresh();
setInterval(scheduleGatewayRefresh, 60 * 1000);

// Graceful shutdown handler
const gracefulShutdown = (signal) => {
  console.log(`[SHUTDOWN] SIGNAL_RECEIVED signal=${signal} action=graceful_shutdown`);

  // Stop accepting new connections
  server.close(() => {
    console.log(`[SHUTDOWN] HTTP_SERVER_CLOSED`);
  });


  // Give active requests 5 seconds to complete
  setTimeout(() => {
    console.log(`[SHUTDOWN] FORCE_EXIT timeout_sec=5`);
    process.exit(0);
  }, 5000);
};

process.on("SIGTERM", () => gracefulShutdown("SIGTERM"));
process.on("SIGINT", () => gracefulShutdown("SIGINT"));
