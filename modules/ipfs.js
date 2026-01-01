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
    const res = await axios.post(endpoint, null, { timeout: 10000 });
    return res.data?.Keys && Object.keys(res.data.Keys).length > 0;
  } catch (err) {
    // If error (404, timeout, etc), assume not pinned
    return false;
  }
};

/**
 * Check if a CID is pinned (read-only check, does not attempt to pin)
 * @param {string} cid - The CID to check
 * @returns {Promise<{success: boolean, size: number, message: string}>}
 */
const pinCid = async (cid) => {
  const startTime = Date.now();

  try {
    console.log(`[IPFS] PIN_CHECK_START cid=${cid}`);

    // Check if already pinned
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

    console.log(`[IPFS] PIN_ADD_START cid=${cid}`);

    await axios.post(
      `${IPFS_API}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&recursive=true`,
      null,
      {
        timeout: 10800000, // allow long pins without timing out (3 hours)
        maxContentLength: Infinity,
        maxBodyLength: Infinity,
      }
    );

    const size = await getCidSize(cid);
    const sizeMB = (size / 1024 / 1024).toFixed(2);
    const duration = Date.now() - startTime;
    console.log(`[IPFS] PIN_ADDED cid=${cid} size_mb=${sizeMB} duration_ms=${duration}`);

    return {
      success: true,
      size,
      message: `Pinned (${sizeMB} MB)`,
      alreadyPinned: false
    };

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
  pinCid,
  getCidSize,
  getPinnedSize,
  checkIPFSHealth,
  getIPFSStats,
};
