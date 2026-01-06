// Configuration and constants

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

// Format bytes to human-readable format
const formatBytes = (bytes) => {
  const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
  if (bytes === 0) return "0 Bytes";
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(2) + " " + sizes[i];
};

// Application constants
const IPFS_API = "http://127.0.0.1:5001";
const PORT = 3232;
const STORAGE_MAX = process.env.STORAGE_MAX || "200GB";
const STORAGE_MAX_BYTES = parseSize(STORAGE_MAX);
const FILE_LIMIT = process.env.FILE_LIMIT ? parseSize(process.env.FILE_LIMIT) : Math.floor(STORAGE_MAX_BYTES / 100);
const PROXY_FILE_LIMIT = process.env.REMOTE_FILE_LIMIT ? parseSize(process.env.REMOTE_FILE_LIMIT) : Math.floor(STORAGE_MAX_BYTES / 100); // Remote upload file size limit
const HOST = "0.0.0.0";
const UPLOAD_TEMP_DIR = "/tmp/filedrop";

// Nostr timing configuration
const NOSTR_CHECK_INTERVAL_MS = 7 * 60 * 1000; // 7 minutes

module.exports = {
  parseSize,
  formatBytes,
  IPFS_API,
  PORT,
  STORAGE_MAX,
  FILE_LIMIT,
  PROXY_FILE_LIMIT,
  HOST,
  UPLOAD_TEMP_DIR,
  NOSTR_CHECK_INTERVAL_MS,
};
