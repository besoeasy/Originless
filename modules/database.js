// In-memory database module for tracking pinned content
// Stateless between reboots - resets on restart

// In-memory store
const pinsMap = new Map(); // CID -> pin object
let nextId = 1;

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
      existing.size = size;
      existing.status = status;
      existing.updated_at = now;
      console.log(`[DB] Updated ${type} pin: ${cid}`);
    } else {
      // Insert new
      const id = nextId++;
      const createdAt = Math.floor(Date.now() / 1000);
      const pin = createPinObject(id, eventId, cid, size, timestamp, author, type, status, createdAt, now);
      pinsMap.set(cid, pin);
      console.log(`[DB] Recorded ${type} pin: ${cid} from event ${eventId}`);
    }
    return true;
  } catch (err) {
    console.error(`[DB] Failed to record pin:`, err.message);
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
      console.log(`[DB] Updated pin size: ${cid} = ${size} bytes, status = ${status}`);
      return true;
    } else {
      console.warn(`[DB] CID not found: ${cid}`);
      return false;
    }
  } catch (err) {
    console.error(`[DB] Failed to update pin size:`, err.message);
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
      }
    }
    
    console.log(`[DB] Batch inserted ${inserted} new CIDs (${cids.length - inserted} duplicates ignored)`);
    return inserted;
  } catch (err) {
    console.error(`[DB] Failed to batch insert:`, err.message);
    return 0;
  }
};

// Get pending CIDs by type
const getPendingCidsByType = (type, limit = 1) => {
  try {
    const pins = Array.from(pinsMap.values())
      .filter(pin => pin.type === type && pin.status === 'pending')
      .sort((a, b) => b.timestamp - a.timestamp);
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
  
  // Utility
  getStoreStats,
};
