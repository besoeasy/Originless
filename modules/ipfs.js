// IPFS-related helper functions - SIMPLIFIED
const axios = require("axios");
const { IPFS_API } = require("./config");

// Track CIDs currently being fetched in background to prevent duplicates
const fetchingInProgress = new Set();

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
 * Check if a CID is available locally (pinned or cached)
 * @param {string} cid - The CID to check
 * @returns {Promise<boolean>} - True if available locally, false otherwise
 */
const isCachedLocally = async (cid) => {
  try {
    // Try to stat the CID - if successful, it's available locally
    const endpoint = `${IPFS_API}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`;
    await axios.post(endpoint, null, { timeout: 15000 });
    return true;
  } catch (err) {
    // If error (404, timeout, etc), it's not available locally
    return false;
  }
};

/**
 * Start fetching a CID in the background (fire-and-forget)
 * @param {string} cid - The CID to fetch
 */
const startFetchingInBackground = (cid) => {
  // Prevent duplicate background fetch operations
  if (fetchingInProgress.has(cid)) {
    console.log(`[IPFS] BACKGROUND_FETCH_ALREADY_IN_PROGRESS cid=${cid}`);
    return;
  }
  
  fetchingInProgress.add(cid);
  
  const endpoint = `${IPFS_API}/api/v0/block/get?arg=${encodeURIComponent(cid)}`;
  const startTime = Date.now();
  
  console.log(`[IPFS] BACKGROUND_FETCH_START cid=${cid}`);
  
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
      let lastLog = Date.now();
      
      res.data.on('data', (chunk) => { 
        size += chunk.length;
        // Log progress every 30 seconds
        if (Date.now() - lastLog > 30000) {
          const sizeMB = (size / 1024 / 1024).toFixed(2);
          const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
          console.log(`[IPFS] BACKGROUND_FETCH_PROGRESS cid=${cid} size_mb=${sizeMB} elapsed_sec=${elapsed}`);
          lastLog = Date.now();
        }
      });
      
      res.data.on('end', () => {
        const sizeMB = (size / 1024 / 1024).toFixed(2);
        const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
        console.log(`[IPFS] BACKGROUND_FETCH_COMPLETE cid=${cid} size_mb=${sizeMB} elapsed_sec=${elapsed}`);
        fetchingInProgress.delete(cid);
      });
      
      res.data.on('error', (err) => {
        const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
        console.error(`[IPFS] BACKGROUND_FETCH_ERROR cid=${cid} error="${err.message}" elapsed_sec=${elapsed}`);
        fetchingInProgress.delete(cid);
      });
    })
    .catch(err => {
      const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
      console.error(`[IPFS] BACKGROUND_FETCH_FAILED cid=${cid} error="${err.message}" elapsed_sec=${elapsed}`);
      fetchingInProgress.delete(cid);
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
    console.log(`[IPFS] PIN_CHECK_START cid=${cid}`);
    
    // First check if already pinned
    const alreadyPinned = await isPinned(cid);
    if (alreadyPinned) {
      const size = await getCidSize(cid);
      const sizeMB = (size / 1024 / 1024).toFixed(2);
      const duration = Date.now() - startTime;
      console.log(`[IPFS] PIN_ALREADY_EXISTS cid=${cid} size_mb=${sizeMB} duration_ms=${duration}`);
      return { 
        success: true, 
        size, 
        message: `Already pinned (${sizeMB} MB)`,
        alreadyPinned: true
      };
    }

    // Not pinned, check if it's cached/available locally
    console.log(`[IPFS] PIN_CHECK_CACHE cid=${cid}`);
    const isCached = await isCachedLocally(cid);
    
    if (isCached) {
      // It's cached, so we can pin it quickly
      console.log(`[IPFS] PIN_FROM_CACHE_START cid=${cid}`);
      const endpoint = `${IPFS_API}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&progress=false`;
      
      await axios.post(endpoint, null, { 
        timeout: 120000, // 120 seconds - can be slow for large files
        httpAgent: new (require('http').Agent)({ keepAlive: true })
      });
      
      const size = await getCidSize(cid);
      const sizeMB = (size / 1024 / 1024).toFixed(2);
      const duration = ((Date.now() - startTime) / 1000).toFixed(1);
      console.log(`[IPFS] PIN_FROM_CACHE_SUCCESS cid=${cid} size_mb=${sizeMB} duration_sec=${duration}`);
      
      return { 
        success: true, 
        size, 
        message: `Pinned from cache (${sizeMB} MB)`,
        alreadyPinned: false
      };
    } else {
      // Not cached, fire off a fetch request (fire-and-forget)
      const duration = Date.now() - startTime;
      console.log(`[IPFS] PIN_FETCH_NEEDED cid=${cid} action=background_fetch_started duration_ms=${duration}`);
      startFetchingInBackground(cid);
      
      return { 
        success: true, 
        size: 0, 
        message: `Fetching in background`,
        fetching: true
      };
    }
    
  } catch (err) {
    const duration = ((Date.now() - startTime) / 1000).toFixed(1);
    console.error(`[IPFS] PIN_ERROR cid=${cid} error="${err.message}" duration_sec=${duration}`);
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
      { timeout: 15000 }
    );
    return statResponse.data.CumulativeSize || statResponse.data.Size || 0;
  } catch (err) {
    // Try block/stat as fallback
    try {
      const blockResponse = await axios.post(
        `${IPFS_API}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`, 
        {}, 
        { timeout: 15000 }
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
  getCidSize,
  getPinnedSize,
  checkIPFSHealth,
  getIPFSStats,
};
