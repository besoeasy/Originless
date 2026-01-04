// IPFS-related helper functions - SIMPLIFIED
const axios = require("axios");
const { spawn } = require("child_process");
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

let cidarray = [];
let cidarrayupdateTime = Date.now();

/**
 * Pin a CID in IPFS using CLI (blocking, waits for completion)
 * @param {string} cid - The CID to pin
 * @returns {Promise<{success: boolean, size: number, message: string, alreadyPinned: boolean}>}
 */
const pinCid = async (cid) => {
  try {
    // Check if already pinned first (most efficient check)
    const alreadyPinned = await isPinned(cid);
    if (alreadyPinned) {
      const size = await getCidSize(cid);
      const sizeMB = (size / 1024 / 1024).toFixed(2);
      return {
        success: true,
        size,
        message: `Already pinned (${sizeMB} MB)`,
        alreadyPinned: true,
      };
    }

    if (!cidarray.includes(cid)) {

      cidarray.push(cid);
      
      console.log(`[IPFS-CLI] PIN_STARTING cid=${cid}`);

      // Use IPFS CLI to pin - fire-and-forget (non-blocking)
      const child = spawn("ipfs", ["pin", "add", cid, "--recursive", "--progress"]);

      child.on("close", (code) => {
        if (code === 0) {
          console.log(`[IPFS-CLI] PIN_COMPLETED cid=${cid}`);
        } else {
          console.error(`[IPFS-CLI] PIN_FAILED cid=${cid} exit_code=${code}`);
        }
      });

      child.on("error", (err) => {
        console.error(`[IPFS-CLI] SPAWN_ERROR cid=${cid} error="${err.message}"`);
      });

    }

    if (Date.now() - cidarrayupdateTime > 2 * 60 * 60 * 1000) {
      cidarray = [];
      cidarrayupdateTime = Date.now();
    }

    // Return immediately - pin is queued
    return {
      success: false,
      pending: true,
      size: 0,
      message: "Pin queued (background process started)",
      alreadyPinned: false,
    };
  } catch (err) {
    console.error(`[IPFS-CLI] UNEXPECTED_ERROR cid=${cid} error="${err.message}"`);
    return {
      success: false,
      size: 0,
      message: err.message || "Pin request failed",
      alreadyPinned: false,
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
};
