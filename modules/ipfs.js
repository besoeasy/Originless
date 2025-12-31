// IPFS-related helper functions - SIMPLIFIED
const axios = require("axios");
const { IPFS_API } = require("./config");

/**
 * Check if a CID is pinned in IPFS
 * @param {string} cid - The CID to check
 * @returns {Promise<boolean>} - True if pinned, false otherwise
 */
const isPinned = async (cid) => {
  try {
    const endpoint = `${IPFS_API}/api/v0/pin/ls?arg=${encodeURIComponent(cid)}&type=recursive`;
    const res = await axios.post(endpoint, null, { timeout: 3000 });
    return res.data?.Keys && Object.keys(res.data.Keys).length > 0;
  } catch (err) {
    // If error (404, timeout, etc), assume not pinned
    return false;
  }
};

/**
 * Check if a CID is available locally (pinned or cached)
 * @param {string} cid - The CID to check
 * @returns {Promise<boolean>} - True if available locally, false otherwise
 */
const isCachedLocally = async (cid) => {
  try {
    // Try to stat the CID - if successful, it's available locally
    const endpoint = `${IPFS_API}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`;
    await axios.post(endpoint, null, { timeout: 3000 });
    return true;
  } catch (err) {
    // If error (404, timeout, etc), it's not available locally
    return false;
  }
};

/**
 * Start caching a CID in the background (fire-and-forget)
 * @param {string} cid - The CID to cache
 */
const startCachingInBackground = (cid) => {
  const endpoint = `${IPFS_API}/api/v0/block/get?arg=${encodeURIComponent(cid)}`;
  
  // Fire and forget - don't await, just start the request
  axios.post(endpoint, null, { 
    timeout: 0, // No timeout
    responseType: 'stream',
    maxContentLength: Infinity,
    httpAgent: new (require('http').Agent)({ keepAlive: true })
  })
    .then(res => {
      // Stream and discard data to ensure it's cached
      let size = 0;
      res.data.on('data', (chunk) => { size += chunk.length; });
      res.data.on('end', () => {
        console.log(`[Background Cache] ✓ Cached ${cid} (${(size / 1024 / 1024).toFixed(2)} MB)`);
      });
      res.data.on('error', (err) => {
        console.error(`[Background Cache] ✗ Failed to cache ${cid}:`, err.message);
      });
    })
    .catch(err => {
      console.error(`[Background Cache] ✗ Failed to start caching ${cid}:`, err.message);
    });
};

/**
 * Pin a CID (permanent storage)
 * @param {string} cid - The CID to pin
 * @returns {Promise<{success: boolean, size: number, message: string}>}
 */
const pinCid = async (cid) => {
  const startTime = Date.now();
  
  try {
    // First check if already pinned
    const alreadyPinned = await isPinned(cid);
    if (alreadyPinned) {
      const size = await getCidSize(cid);
      const duration = Date.now() - startTime;
      return { 
        success: true, 
        size, 
        message: `Already pinned (${(size / 1024 / 1024).toFixed(2)} MB, ${duration}ms)`,
        alreadyPinned: true
      };
    }

    // Not pinned, check if it's cached/available locally
    console.log(`[Pin] Checking if ${cid} is cached locally...`);
    const isCached = await isCachedLocally(cid);
    
    if (isCached) {
      // It's cached, so we can pin it quickly
      console.log(`[Pin] ${cid} is cached, pinning now...`);
      const endpoint = `${IPFS_API}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&progress=false`;
      
      await axios.post(endpoint, null, { 
        timeout: 30000, // 30 seconds - should be fast since it's cached
        httpAgent: new (require('http').Agent)({ keepAlive: true })
      });
      
      const size = await getCidSize(cid);
      const duration = Date.now() - startTime;
      console.log(`[Pin] ✓ Pinned cached content ${cid} (${(size / 1024 / 1024).toFixed(2)} MB, ${(duration/1000).toFixed(1)}s)`);
      
      return { 
        success: true, 
        size, 
        message: `Pinned cached content ${(size / 1024 / 1024).toFixed(2)} MB in ${(duration/1000).toFixed(1)}s`,
        alreadyPinned: false
      };
    } else {
      // Not cached, fire off a cache request (fire-and-forget)
      console.log(`[Pin] ${cid} not cached, requesting cache (fire-and-forget)...`);
      startCachingInBackground(cid);
      
      const duration = Date.now() - startTime;
      return { 
        success: true, 
        size: 0, 
        message: `Cache request started (${duration}ms)`,
        caching: true
      };
    }
    
  } catch (err) {
    const duration = Date.now() - startTime;
    console.error(`[Pin] ✗ Error processing ${cid} (${(duration/1000).toFixed(1)}s):`, err.message);
    return { 
      success: false, 
      size: 0, 
      message: `Failed: ${err.message}`,
      error: err.message
    };
  }
};

/**
 * Cache a CID (fetch without pinning, can be garbage collected)
 * @param {string} cid - The CID to cache
 * @returns {Promise<{success: boolean, size: number, message: string}>}
 */
const cacheCid = async (cid) => {
  const startTime = Date.now();
  
  try {
    // First check if already available locally
    const alreadyAvailable = await isCachedLocally(cid);
    if (alreadyAvailable) {
      const size = await getCidSize(cid);
      const duration = Date.now() - startTime;
      return { 
        success: true, 
        size, 
        message: `Already available locally (${(size / 1024 / 1024).toFixed(2)} MB, ${duration}ms)`,
        alreadyCached: true
      };
    }

    // Not cached, fire off cache request (fire-and-forget)
    console.log(`[Cache] Starting cache for ${cid} (fire-and-forget)...`);
    startCachingInBackground(cid);
    
    const duration = Date.now() - startTime;
    return { 
      success: true, 
      size: 0, 
      message: `Cache request started (${duration}ms)`,
      caching: true
    };
    
  } catch (err) {
    const duration = Date.now() - startTime;
    console.error(`[Cache] ✗ Error processing ${cid} (${(duration/1000).toFixed(1)}s):`, err.message);
    return { 
      success: false, 
      size: 0, 
      message: `Failed: ${err.message}`,
      error: err.message
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
    const statResponse = await axios.post(
      `${IPFS_API}/api/v0/files/stat?arg=/ipfs/${encodeURIComponent(cid)}`, 
      {}, 
      { timeout: 5000 }
    );
    return statResponse.data.CumulativeSize || statResponse.data.Size || 0;
  } catch (err) {
    // Try block/stat as fallback
    try {
      const blockResponse = await axios.post(
        `${IPFS_API}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`, 
        {}, 
        { timeout: 5000 }
      );
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
    console.error('Failed to get pinned size:', err.message);
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
  isCachedLocally,
  pinCid,
  cacheCid,
  getCidSize,
  getPinnedSize,
  checkIPFSHealth,
  getIPFSStats,
};
