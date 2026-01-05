// IPFS-related helper functions - SIMPLIFIED
const axios = require("axios");
const { spawn } = require("child_process");
const { IPFS_API } = require("./config");
const fs = require("fs");
const path = require("path");
const os = require("os");

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
 * Pin a CID in IPFS using API (fire-and-forget, non-blocking)
 * @param {string} cid - The CID to pin
 * @returns {Promise<{success: boolean, size: number, message: string, alreadyPinned: boolean, pending: boolean}>}
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
        pending: false,
      };
    }

    // Get peer count before pinning
    let peerCount = 0;
    try {
      const peersResponse = await axios.post(`${IPFS_API}/api/v0/swarm/peers`, {}, { timeout: 3000 });
      peerCount = peersResponse.data.Peers?.length || 0;
    } catch (err) {
      console.warn(`[IPFS-API] Failed to get peer count: ${err.message}`);
    }

    console.log(`[IPFS-API] PIN_STARTING cid=${cid} mode=fire_and_forget peers=${peerCount}`);
    console.log(`[IPFS] Trying to fetch CID ${cid.slice(0, 12)}..., using ${peerCount} peers`);

    // Fire-and-forget: Start pin in background without waiting
    const endpoint = `${IPFS_API}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&recursive=true`;

    // Start the pin operation but don't await it (cap body size to avoid buffer growth)
    axios.post(endpoint, null, {
      timeout: 3 * 60 * 60 * 1000, // 3 hours
      responseType: "json",
      maxContentLength: 1024 * 1024, // 1 MB safety cap
      maxBodyLength: 1024 * 1024,
    }).then((pinResponse) => {
      if (pinResponse.data && pinResponse.data.Pins) {
        console.log(`[IPFS-API] PIN_COMPLETED cid=${cid}`);
      } else {
        console.error(`[IPFS-API] PIN_FAILED cid=${cid} no_pins_in_response`);
      }
    }).catch((err) => {
      console.error(`[IPFS-API] PIN_ERROR cid=${cid} error="${err.message}"`);
    });

    // Return immediately - pin is now running in background
    return {
      success: false,
      pending: true,
      size: 0,
      message: "Pin started in background",
      alreadyPinned: false,
    };
  } catch (err) {
    console.error(`[IPFS-API] PIN_CHECK_ERROR cid=${cid} error="${err.message}"`);
    return {
      success: false,
      pending: false,
      size: 0,
      message: err.message || "Pin check failed",
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
