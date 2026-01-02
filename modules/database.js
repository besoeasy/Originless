// In-memory database module for tracking pinned content
// Stateless between reboots - resets on restart

// In-memory store
const pinsMap = new Map(); // CID -> pin object
const inProgressMap = new Map(); // CID -> { startTime, lastProgress, type }
let nextId = 1;

// State tracking for jobs
let lastPinnerActivity = null;
let lastNostrRun = {
  at: null,
  self: null,
  error: null,
};

// Helper to create pin object
const createPinObject = (id, eventId, cid, size, timestamp, author, type, status, createdAt, updatedAt) => ({
  id,
  event_id: eventId,
  cid,
  size,
  timestamp,
  author,
  type,
  status,
  created_at: createdAt,
  updated_at: updatedAt,
});

// Record pin (INSERT OR REPLACE)
const recordPin = ({ eventId, cid, size = 0, timestamp, author, type, status = 'pinned' }) => {
  try {
    const now = Math.floor(Date.now() / 1000);
    
    if (pinsMap.has(cid)) {
      // Update existing
      const existing = pinsMap.get(cid);
      const sizeMB = (size / 1024 / 1024).toFixed(2);
      existing.size = size;
      existing.status = status;
      existing.updated_at = now;
      console.log(`[DB] PIN_UPDATE cid=${cid} type=${type} status=${status} size_mb=${sizeMB}`);
    } else {
      // Insert new
      const id = nextId++;
      const createdAt = Math.floor(Date.now() / 1000);
      const pin = createPinObject(id, eventId, cid, size, timestamp, author, type, status, createdAt, now);
      pinsMap.set(cid, pin);
      const sizeMB = (size / 1024 / 1024).toFixed(2);
      console.log(`[DB] PIN_INSERT cid=${cid} type=${type} status=${status} event_id=${eventId} size_mb=${sizeMB}`);
    }
    return true;
  } catch (err) {
    console.error(`[DB] PIN_RECORD_ERROR cid=${cid} error="${err.message}"`);
    return false;
  }
};

// Update pin size and status
const updatePinSize = (cid, size, status = 'pinned') => {
  try {
    if (pinsMap.has(cid)) {
      const pin = pinsMap.get(cid);
      pin.size = size;
      pin.status = status;
      pin.updated_at = Math.floor(Date.now() / 1000);
      return true;
    } else {
      return false;
    }
  } catch (err) {
    console.error(`[DB] PIN_SIZE_UPDATE_ERROR cid=${cid} error="${err.message}"`);
    return false;
  }
};

// Get pin by CID
const getPinByCid = (cid) => {
  try {
    return pinsMap.get(cid) || null;
  } catch (err) {
    console.error(`[DB] Failed to get pin by CID:`, err.message);
    return null;
  }
};

// Get all pins with pagination
const getPins = (limit = 50, offset = 0) => {
  try {
    const pins = Array.from(pinsMap.values())
      .sort((a, b) => b.created_at - a.created_at);
    return pins.slice(offset, offset + limit);
  } catch (err) {
    console.error(`[DB] Failed to get pins:`, err.message);
    return [];
  }
};

// Get pins by type with pagination
const getPinsByType = (type, limit = 50, offset = 0) => {
  try {
    const pins = Array.from(pinsMap.values())
      .filter(pin => pin.type === type)
      .sort((a, b) => b.created_at - a.created_at);
    return pins.slice(offset, offset + limit);
  } catch (err) {
    console.error(`[DB] Failed to get pins by type:`, err.message);
    return [];
  }
};

// Get statistics by type and status
const getStats = () => {
  try {
    const stats = {};
    
    for (const pin of pinsMap.values()) {
      const key = `${pin.type}_${pin.status}`;
      if (!stats[key]) {
        stats[key] = { count: 0, total_size: 0 };
      }
      stats[key].count++;
      stats[key].total_size += pin.size;
    }
    
    return Object.entries(stats).map(([key, value]) => ({
      type: key.split('_')[0],
      status: key.split('_')[1],
      count: value.count,
      total_size: value.total_size,
    }));
  } catch (err) {
    console.error(`[DB] Failed to get stats:`, err.message);
    return [];
  }
};

// Get total count
const getTotalCount = () => {
  try {
    return pinsMap.size;
  } catch (err) {
    console.error(`[DB] Failed to get total count:`, err.message);
    return 0;
  }
};

// Get recent pins
const getRecentPins = (limit = 10) => {
  try {
    const pins = Array.from(pinsMap.values())
      .sort((a, b) => b.created_at - a.created_at);
    return pins.slice(0, limit);
  } catch (err) {
    console.error(`[DB] Failed to get recent pins:`, err.message);
    return [];
  }
};

// Insert CID if not exists
const insertCidIfNotExists = ({ eventId, cid, timestamp, author, type }) => {
  try {
    if (pinsMap.has(cid)) {
      return false; // Already exists
    }
    
    const id = nextId++;
    const now = Math.floor(Date.now() / 1000);
    const pin = createPinObject(id, eventId, cid, 0, timestamp, author, type, 'pending', now, now);
    pinsMap.set(cid, pin);
    return true; // Inserted
  } catch (err) {
    console.error(`[DB] Failed to insert CID:`, err.message);
    return false;
  }
};

// Batch insert CIDs
const batchInsertCids = (cids) => {
  try {
    let inserted = 0;
    let duplicates = 0;
    
    for (const cidObj of cids) {
      if (!pinsMap.has(cidObj.cid)) {
        const id = nextId++;
        const now = Math.floor(Date.now() / 1000);
        const pin = createPinObject(
          id,
          cidObj.eventId,
          cidObj.cid,
          0,
          cidObj.timestamp,
          cidObj.author,
          cidObj.type,
          'pending',
          now,
          now
        );
        pinsMap.set(cidObj.cid, pin);
        inserted++;
      } else {
        duplicates++;
      }
    }
    
    console.log(`[DB] BATCH_INSERT total=${cids.length} inserted=${inserted} duplicates=${duplicates}`);
    return inserted;
  } catch (err) {
    console.error(`[DB] BATCH_INSERT_ERROR error="${err.message}"`);
    return 0;
  }
};

// Get pending CIDs by type
const getPendingCidsByType = (type, limit = 1) => {
  try {
    const pins = Array.from(pinsMap.values())
      .filter(pin => pin.type === type && pin.status !== 'pinned' && !inProgressMap.has(pin.cid));
    
    // Shuffle randomly (Fisher-Yates)
    for (let i = pins.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [pins[i], pins[j]] = [pins[j], pins[i]];
    }
    
    return pins.slice(0, limit);
  } catch (err) {
    console.error(`[DB] Failed to get pending CIDs:`, err.message);
    return [];
  }
};

// Count by type and status
const countByTypeAndStatus = (type, status) => {
  try {
    let count = 0;
    for (const pin of pinsMap.values()) {
      if (pin.type === type && pin.status === status) {
        count++;
      }
    }
    return count;
  } catch (err) {
    console.error(`[DB] Failed to count:`, err.message);
    return 0;
  }
};

// Get current store stats (for debugging)
const getStoreStats = () => {
  const stats = {
    totalCids: pinsMap.size,
    byType: {},
    byStatus: {},
  };
  
  for (const pin of pinsMap.values()) {
    if (!stats.byType[pin.type]) {
      stats.byType[pin.type] = 0;
    }
    if (!stats.byStatus[pin.status]) {
      stats.byStatus[pin.status] = 0;
    }
    stats.byType[pin.type]++;
    stats.byStatus[pin.status]++;
  }
  
  return stats;
};

// Cleanup on exit (no-op for in-memory)
process.on('exit', () => {
  console.log(`[DB] Shutting down. Final store stats:`, getStoreStats());
});

process.on('SIGINT', () => {
  console.log(`[DB] Received SIGINT. Final store stats:`, getStoreStats());
  process.exit(0);
});

// Track in-progress operations
const markInProgress = (cid, type) => {
  inProgressMap.set(cid, {
    startTime: Date.now(),
    lastProgress: Date.now(),
    type,
  });
};

const updateProgress = (cid, bytes) => {
  const progress = inProgressMap.get(cid);
  if (progress) {
    progress.lastProgress = Date.now();
    progress.bytes = bytes;
  }
};

const clearInProgress = (cid) => {
  inProgressMap.delete(cid);
};

const isInProgress = (cid) => {
  return inProgressMap.has(cid);
};

const getInProgressCids = () => {
  return Array.from(inProgressMap.entries()).map(([cid, info]) => ({
    cid,
    ...info,
    elapsed: Date.now() - info.startTime,
  }));
};

// Clean up stale in-progress entries (no activity for 10 minutes)
const cleanupStaleInProgress = () => {
  const now = Date.now();
  const staleThreshold = 10 * 60 * 1000; // 10 minutes
  
  for (const [cid, info] of inProgressMap.entries()) {
    if (now - info.lastProgress > staleThreshold) {
      console.log(`[DB] Removing stale in-progress entry: ${cid} (no activity for ${Math.floor((now - info.lastProgress) / 1000)}s)`);
      inProgressMap.delete(cid);
    }
  }
};

// Get a random CID for pinner job
const getRandomCid = () => {
  try {
    // Get all CIDs that are not in progress
    const availablePins = Array.from(pinsMap.values())
      .filter(pin => !inProgressMap.has(pin.cid));
    
    if (availablePins.length === 0) {
      return null;
    }
    
    // Return a random pin
    const randomIndex = Math.floor(Math.random() * availablePins.length);
    return availablePins[randomIndex];
  } catch (err) {
    console.error(`[DB] Failed to get random CID:`, err.message);
    return null;
  }
};

// State management functions
const getLastPinnerActivity = () => lastPinnerActivity;
const setLastPinnerActivity = (timestamp) => {
  lastPinnerActivity = timestamp;
};

const getLastNostrRun = () => lastNostrRun;
const setLastNostrRun = (data) => {
  lastNostrRun = data;
};

module.exports = {
  // Core functions (same API as SQLite version)
  recordPin,
  updatePinSize,
  getPinByCid,
  getPins,
  getPinsByType,
  getStats,
  getTotalCount,
  getRecentPins,
  insertCidIfNotExists,
  batchInsertCids,
  getPendingCidsByType,
  countByTypeAndStatus,
  
  // In-progress tracking
  markInProgress,
  updateProgress,
  clearInProgress,
  isInProgress,
  getInProgressCids,
  cleanupStaleInProgress,
  
  // State management
  getLastPinnerActivity,
  setLastPinnerActivity,
  getLastNostrRun,
  setLastNostrRun,
  
  // Utility
  getStoreStats,
  getRandomCid,
};
