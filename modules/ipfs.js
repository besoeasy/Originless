// IPFS-related helper functions - SIMPLIFIED
const axios = require("axios");
const http = require("http");
const https = require("https");
const { IPFS_API } = require("./config");

// Track pin requests to avoid duplicates (CID -> timestamp)
const pinRequestCache = new Map();
const PIN_REQUEST_COOLDOWN = 3 * 60 * 60 * 1000; // 3 hours in milliseconds

// Create persistent HTTP agents with keep-alive for long-running connections
const httpAgent = new http.Agent({
  keepAlive: true,
  keepAliveMsecs: 30000,
  maxSockets: 10,
  timeout: 0,
});

const httpsAgent = new https.Agent({
  keepAlive: true,
  keepAliveMsecs: 30000,
  maxSockets: 10,
  timeout: 0,
});

/**
 * Clean up expired entries from the pin request cache
 * Removes entries older than PIN_REQUEST_COOLDOWN
 */
const cleanupExpiredCacheEntries = () => {
  const now = Date.now();
  let removedCount = 0;

  for (const [cid, timestamp] of pinRequestCache.entries()) {
    if (now - timestamp >= PIN_REQUEST_COOLDOWN) {
      pinRequestCache.delete(cid);
      removedCount++;
    }
  }

  if (removedCount > 0) {
    console.log(`[IPFS] CACHE_CLEANUP removed=${removedCount} cache_size=${pinRequestCache.size}`);
  }
};

/**
 * Check if a CID is pinned in IPFS
 * @param {string} cid - The CID to check
 * @returns {Promise<boolean>} - True if pinned, false otherwise
 */
const isPinned = async (cid) => {
  try {
    const endpoint = `${IPFS_API}/api/v0/pin/ls?arg=${encodeURIComponent(cid)}&type=recursive`;
    const res = await axios.post(endpoint, null, { timeout: 10000 });
    return res.data?.Keys && Object.keys(res.data.Keys).length > 0;
  } catch (err) {
    // If error (404, timeout, etc), assume not pinned
    return false;
  }
};

/**
 * Pin a CID in IPFS (fire-and-forget - returns immediately, IPFS handles it in background)
 * @param {string} cid - The CID to pin
 * @returns {Promise<{success: boolean, size: number, message: string}>}
 */
const pinCid = async (cid) => {
  try {
    // Check if already pinned first (most efficient check)
    const alreadyPinned = await isPinned(cid);
    if (alreadyPinned) {
      const size = await getCidSize(cid);
      const sizeMB = (size / 1024 / 1024).toFixed(2);
      console.log(`[IPFS] PIN_ALREADY_EXISTS cid=${cid} size_mb=${sizeMB}`);
      // Remove from cache if it's already pinned
      pinRequestCache.delete(cid);
      return {
        success: true,
        size,
        message: `Already pinned (${sizeMB} MB)`,
        alreadyPinned: true,
      };
    }

    // Clean up expired cache entries (lazy cleanup)
    cleanupExpiredCacheEntries();

    // Check if we recently requested this CID
    const lastRequest = pinRequestCache.get(cid);
    const now = Date.now();

    if (lastRequest && now - lastRequest < PIN_REQUEST_COOLDOWN) {
      const hoursAgo = ((now - lastRequest) / (1000 * 60 * 60)).toFixed(1);
      console.log(`[IPFS] PIN_REQUEST_BLOCKED cid=${cid} reason="requested ${hoursAgo}h ago, cooldown=3h"`);
      return {
        success: false,
        size: 0,
        message: `Pin request blocked (already requested ${hoursAgo}h ago)`,
        blocked: true,
      };
    }

    // Record this pin request
    pinRequestCache.set(cid, now);

    // Fire and forget - just start the pin operation, don't wait for completion
    // IPFS daemon will handle the pinning in the background
    console.log(`[IPFS] PIN_INITIATED cid=${cid}`);

    // Start the pin operation without waiting for completion (background=true if supported)
    axios
      .post(`${IPFS_API}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&recursive=true`, null, {
        timeout: 5000,
        httpAgent: IPFS_API.startsWith("http://") ? httpAgent : undefined,
        httpsAgent: IPFS_API.startsWith("https://") ? httpsAgent : undefined,
      })
      .catch((err) => {
        console.error(`[IPFS] PIN_REQUEST_ERROR cid=${cid} error="${err.message}"`);
      });

    // Return immediately - pin is being handled by IPFS daemon
    return {
      success: false,
      size: 0,
      message: `Pin initiated (processing in background)`,
      alreadyPinned: false,
      background: true,
    };
  } catch (err) {
    console.error(`[IPFS] PIN_CHECK_ERROR cid=${cid} error="${err.message}"`);
    return {
      success: false,
      size: 0,
      message: `Failed: ${err.message}`,
      error: err.message,
    };
  }
};

/**
 * Get the size of a CID
 * @param {string} cid - The CID to get size for
 * @returns {Promise<number>} - Size in bytes
 */
const getCidSize = async (cid) => {
  try {
    const statResponse = await axios.post(`${IPFS_API}/api/v0/files/stat?arg=/ipfs/${encodeURIComponent(cid)}`, {}, { timeout: 15000 });
    return statResponse.data.CumulativeSize || statResponse.data.Size || 0;
  } catch (err) {
    // Try block/stat as fallback
    try {
      const blockResponse = await axios.post(`${IPFS_API}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`, {}, { timeout: 15000 });
      return blockResponse.data.Size || 0;
    } catch (blockErr) {
      return 0;
    }
  }
};

// Get total size of pinned content
const getPinnedSize = async () => {
  try {
    const pinResponse = await axios.post(`${IPFS_API}/api/v0/pin/ls?type=recursive`, {}, { timeout: 10000 });
    const pins = pinResponse.data.Keys || {};
    const cids = Object.keys(pins);

    let totalSize = 0;
    for (const cid of cids) {
      const size = await getCidSize(cid);
      totalSize += size;
    }
    return { totalSize, count: cids.length };
  } catch (err) {
    console.error("Failed to get pinned size:", err.message);
    return { totalSize: 0, count: 0 };
  }
};

// Check IPFS health
const checkIPFSHealth = async () => {
  try {
    const peersResponse = await axios.post(`${IPFS_API}/api/v0/swarm/peers`, { timeout: 5000 });
    const peerCount = peersResponse.data.Peers?.length || 0;
    return { healthy: peerCount >= 1, peers: peerCount };
  } catch (err) {
    return { healthy: false, error: err.message };
  }
};

// Get comprehensive IPFS stats
const getIPFSStats = async () => {
  const [bwResponse, repoResponse, idResponse, peersResponse] = await Promise.all([
    axios.post(`${IPFS_API}/api/v0/stats/bw?interval=5m`, { timeout: 5000 }),
    axios.post(`${IPFS_API}/api/v0/repo/stat`, { timeout: 5000 }),
    axios.post(`${IPFS_API}/api/v0/id`, { timeout: 5000 }),
    axios.post(`${IPFS_API}/api/v0/swarm/peers`, { timeout: 5000 }),
  ]);

  return {
    bandwidth: {
      totalIn: bwResponse.data.TotalIn,
      totalOut: bwResponse.data.TotalOut,
      rateIn: bwResponse.data.RateIn,
      rateOut: bwResponse.data.RateOut,
      interval: "1h",
    },
    repository: {
      size: repoResponse.data.RepoSize,
      storageMax: repoResponse.data.StorageMax,
      numObjects: repoResponse.data.NumObjects,
      path: repoResponse.data.RepoPath,
      version: repoResponse.data.Version,
    },
    node: {
      id: idResponse.data.ID,
      publicKey: idResponse.data.PublicKey,
      agentVersion: idResponse.data.AgentVersion,
      protocolVersion: idResponse.data.ProtocolVersion,
    },
    peers: {
      count: peersResponse.data.Peers.length,
    },
  };
};

module.exports = {
  isPinned,
  pinCid,
  getCidSize,
  getPinnedSize,
  checkIPFSHealth,
  getIPFSStats,
  pinRequestCache, // Export for debugging/admin purposes
};
