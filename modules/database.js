// Database module for tracking pinned content
const Database = require('better-sqlite3');
const path = require('path');
const fs = require('fs');

// Initialize database
const dbDir = path.join(__dirname, '..', 'db');
const dbPath = process.env.DB_PATH || path.join(dbDir, 'pins.db');

// Ensure db directory exists
if (!fs.existsSync(dbDir)) {
  fs.mkdirSync(dbDir, { recursive: true });
}

const db = new Database(dbPath);

// Enable WAL mode for better concurrency
db.pragma('journal_mode = WAL');

// Create pins table
db.exec(`
  CREATE TABLE IF NOT EXISTS pins (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL,
    cid TEXT NOT NULL UNIQUE,
    size INTEGER DEFAULT 0,
    timestamp INTEGER NOT NULL,
    author TEXT,
    type TEXT NOT NULL CHECK(type IN ('self', 'friend')),
    status TEXT NOT NULL DEFAULT 'pinned' CHECK(status IN ('pending', 'pinned', 'cached', 'failed')),
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
  );

  CREATE INDEX IF NOT EXISTS idx_pins_cid ON pins(cid);
  CREATE INDEX IF NOT EXISTS idx_pins_event_id ON pins(event_id);
  CREATE INDEX IF NOT EXISTS idx_pins_type ON pins(type);
  CREATE INDEX IF NOT EXISTS idx_pins_status ON pins(status);
  CREATE INDEX IF NOT EXISTS idx_pins_timestamp ON pins(timestamp DESC);
  CREATE INDEX IF NOT EXISTS idx_pins_created_at ON pins(created_at DESC);
`);

// Prepared statements
const insertPinStmt = db.prepare(`
  INSERT OR REPLACE INTO pins (event_id, cid, size, timestamp, author, type, status, updated_at)
  VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'))
`);

const updatePinSizeStmt = db.prepare(`
  UPDATE pins SET size = ?, status = ?, updated_at = strftime('%s', 'now')
  WHERE cid = ?
`);

const getPinByCidStmt = db.prepare(`
  SELECT * FROM pins WHERE cid = ?
`);

const getPinsStmt = db.prepare(`
  SELECT * FROM pins
  ORDER BY created_at DESC
  LIMIT ? OFFSET ?
`);

const getPinsByTypeStmt = db.prepare(`
  SELECT * FROM pins
  WHERE type = ?
  ORDER BY created_at DESC
  LIMIT ? OFFSET ?
`);

const getStatsStmt = db.prepare(`
  SELECT 
    type,
    status,
    COUNT(*) as count,
    COALESCE(SUM(size), 0) as total_size
  FROM pins
  GROUP BY type, status
`);

const getTotalCountStmt = db.prepare(`
  SELECT COUNT(*) as total FROM pins
`);

const getRecentPinsStmt = db.prepare(`
  SELECT * FROM pins
  ORDER BY created_at DESC
  LIMIT ?
`);

const insertIfNotExistsStmt = db.prepare(`
  INSERT OR IGNORE INTO pins (event_id, cid, size, timestamp, author, type, status)
  VALUES (?, ?, 0, ?, ?, ?, 'pending')
`);

const getPendingByTypeStmt = db.prepare(`
  SELECT * FROM pins
  WHERE type = ? AND status = 'pending'
  ORDER BY timestamp DESC
  LIMIT ?
`);

const countByTypeAndStatusStmt = db.prepare(`
  SELECT COUNT(*) as count FROM pins
  WHERE type = ? AND status = ?
`);

// Functions
const recordPin = ({ eventId, cid, size = 0, timestamp, author, type, status = 'pinned' }) => {
  try {
    insertPinStmt.run(eventId, cid, size, timestamp, author, type, status);
    console.log(`[DB] Recorded ${type} pin: ${cid} from event ${eventId}`);
    return true;
  } catch (err) {
    console.error(`[DB] Failed to record pin:`, err.message);
    return false;
  }
};

const insertCidIfNotExists = ({ eventId, cid, timestamp, author, type }) => {
  try {
    const result = insertIfNotExistsStmt.run(eventId, cid, timestamp, author, type);
    return result.changes > 0; // Returns true if inserted, false if already exists
  } catch (err) {
    console.error(`[DB] Failed to insert CID:`, err.message);
    return false;
  }
};

const batchInsertCids = (cids) => {
  const insertMany = db.transaction((cidList) => {
    let inserted = 0;
    for (const cidObj of cidList) {
      const result = insertIfNotExistsStmt.run(
        cidObj.eventId,
        cidObj.cid,
        cidObj.timestamp,
        cidObj.author,
        cidObj.type
      );
      if (result.changes > 0) inserted++;
    }
    return inserted;
  });
  
  try {
    const inserted = insertMany(cids);
    console.log(`[DB] Batch inserted ${inserted} new CIDs (${cids.length - inserted} duplicates ignored)`);
    return inserted;
  } catch (err) {
    console.error(`[DB] Failed to batch insert:`, err.message);
    return 0;
  }
};

const updatePinSize = (cid, size, status = 'pinned') => {
  try {
    updatePinSizeStmt.run(size, status, cid);
    console.log(`[DB] Updated pin size: ${cid} = ${size} bytes`);
    return true;
  } catch (err) {
    console.error(`[DB] Failed to update pin size:`, err.message);
    return false;
  }
};

const getPinByCid = (cid) => {
  try {
    return getPinByCidStmt.get(cid);
  } catch (err) {
    console.error(`[DB] Failed to get pin by CID:`, err.message);
    return null;
  }
};

const getPins = (limit = 50, offset = 0) => {
  try {
    return getPinsStmt.all(limit, offset);
  } catch (err) {
    console.error(`[DB] Failed to get pins:`, err.message);
    return [];
  }
};

const getPinsByType = (type, limit = 50, offset = 0) => {
  try {
    return getPinsByTypeStmt.all(type, limit, offset);
  } catch (err) {
    console.error(`[DB] Failed to get pins by type:`, err.message);
    return [];
  }
};

const getStats = () => {
  try {
    return getStatsStmt.all();
  } catch (err) {
    console.error(`[DB] Failed to get stats:`, err.message);
    return [];
  }
};

const getTotalCount = () => {
  try {
    const result = getTotalCountStmt.get();
    return result ? result.total : 0;
  } catch (err) {
    console.error(`[DB] Failed to get total count:`, err.message);
    return 0;
  }
};

const getRecentPins = (limit = 10) => {
  try {
    return getRecentPinsStmt.all(limit);
  } catch (err) {
    console.error(`[DB] Failed to get recent pins:`, err.message);
    return [];
  }
};

const getPendingCidsByType = (type, limit = 1) => {
  try {
    return getPendingByTypeStmt.all(type, limit);
  } catch (err) {
    console.error(`[DB] Failed to get pending CIDs:`, err.message);
    return [];
  }
};

const countByTypeAndStatus = (type, status) => {
  try {
    const result = countByTypeAndStatusStmt.get(type, status);
    return result ? result.count : 0;
  } catch (err) {
    console.error(`[DB] Failed to count:`, err.message);
    return 0;
  }
};

// Cleanup on exit
process.on('exit', () => {
  db.close();
});

process.on('SIGINT', () => {
  db.close();
  process.exit(0);
});

module.exports = {
  db,
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
};
