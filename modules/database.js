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

// Get pins by Author (daku userId) with pagination
const getPinsByAuthor = (author, limit = 50, offset = 0) => {
  try {
    const pins = Array.from(pinsMap.values())
      .filter(pin => pin.author === author)
      .sort((a, b) => b.created_at - a.created_at);
    return pins.slice(offset, offset + limit);
  } catch (err) {
    console.error(`[DB] Failed to get pins by author:`, err.message);
    return [];
  }
};

// Delete pin
const deletePin = (cid) => {
  try {
    return pinsMap.delete(cid);
  } catch (err) {
    console.error(`[DB] Failed to delete pin:`, err.message);
    return false;
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
  getPinByCid,
  getPins,
  getPinsByType,
  getPinsByAuthor,
  deletePin,
  getStats,
  getTotalCount,
  getRecentPins,
  insertCidIfNotExists,
  batchInsertCids,
  countByTypeAndStatus,
  getStoreStats,
};
