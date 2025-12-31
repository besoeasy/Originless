// Express middleware configuration
const express = require("express");
const cors = require("cors");
const compression = require("compression");
const multer = require("multer");
const path = require("path");
const { UPLOAD_TEMP_DIR, FILE_LIMIT } = require("./config");

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

// Setup all middleware for the app
const setupMiddleware = (app) => {
  app.use(compression());
  app.use(express.json());
  app.use(express.urlencoded({ extended: true }));
  app.use(cors());
  app.use(express.static(path.join(__dirname, "../public")));
};

module.exports = {
  upload,
  errorHandler,
  setupMiddleware,
};
