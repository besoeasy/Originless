// IPFS-related helper functions (Bun-optimized, fetch-based)
const { IPFS_API } = require("./config");

const fetchWithTimeout = async (url, options = {}, timeoutMs = 10000) => {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);

  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } finally {
    clearTimeout(timeout);
  }
};

const fetchJson = async (url, options = {}, timeoutMs = 10000) => {
  const res = await fetchWithTimeout(url, options, timeoutMs);
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    const error = new Error(`HTTP ${res.status} ${res.statusText}${text ? `: ${text}` : ""}`);
    error.status = res.status;
    throw error;
  }
  return res.json();
};

/**
 * Check if a CID is pinned in IPFS
 * @param {string} cid - The CID to check
 * @returns {Promise<boolean>} - True if pinned, false otherwise
 */
const isPinned = async (cid) => {
  try {
    const endpoint = `${IPFS_API}/api/v0/pin/ls?arg=${encodeURIComponent(cid)}&type=recursive`;
    const data = await fetchJson(endpoint, { method: "POST" }, 10000);
    return data?.Keys && Object.keys(data.Keys).length > 0;
  } catch (err) {
    // If error (404, timeout, etc), assume not pinned
    return false;
  }
};

/**
 * Unpin a CID in IPFS
 * @param {string} cid - The CID to unpin
 * @returns {Promise<boolean>} - True if unpinned successfully
 */
const unpinCid = async (cid) => {
  try {
    const endpoint = `${IPFS_API}/api/v0/pin/rm?arg=${encodeURIComponent(cid)}&recursive=true`;
    await fetchWithTimeout(endpoint, { method: "POST" }, 10000);
    return true;
  } catch (err) {
    console.warn(`[IPFS-API] Failed to unpin ${cid}: ${err.message}`);
    return false;
  }
};

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
      const peersResponse = await fetchJson(`${IPFS_API}/api/v0/swarm/peers`, { method: "POST" }, 3000);
      peerCount = peersResponse.Peers?.length || 0;
    } catch (err) {
      console.warn(`[IPFS-API] Failed to get peer count: ${err.message}`);
    }

    console.log(`[IPFS-API] PIN_STARTING cid=${cid} mode=fire_and_forget peers=${peerCount}`);
    console.log(`[IPFS] Trying to fetch CID ${cid.slice(0, 12)}..., using ${peerCount} peers`);

    // Fire-and-forget: Start pin in background without waiting
    const endpoint = `${IPFS_API}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&recursive=true`;

    // Start the pin operation but don't await it (cap body size to avoid buffer growth)
    fetchWithTimeout(endpoint, { method: "POST" }, 3 * 60 * 60 * 1000)
      .then(async (res) => {
        if (!res.ok) {
          const text = await res.text().catch(() => "");
          console.error(`[IPFS-API] PIN_FAILED cid=${cid} status=${res.status} ${text}`);
          return;
        }

        const data = await res.json().catch(() => null);
        if (data && data.Pins) {
          console.log(`[IPFS-API] PIN_COMPLETED cid=${cid}`);
        } else {
          console.error(`[IPFS-API] PIN_FAILED cid=${cid} no_pins_in_response`);
        }
      })
      .catch((err) => {
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
    const statResponse = await fetchJson(
      `${IPFS_API}/api/v0/files/stat?arg=/ipfs/${encodeURIComponent(cid)}`,
      { method: "POST" },
      15000
    );
    return statResponse.CumulativeSize || statResponse.Size || 0;
  } catch (err) {
    // Try block/stat as fallback
    try {
      const blockResponse = await fetchJson(
        `${IPFS_API}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`,
        { method: "POST" },
        15000
      );
      return blockResponse.Size || 0;
    } catch (blockErr) {
      return 0;
    }
  }
};

// Get total size of pinned content
const getPinnedSize = async () => {
  try {
    const pinResponse = await fetchJson(`${IPFS_API}/api/v0/pin/ls?type=recursive`, { method: "POST" }, 10000);
    const pins = pinResponse.Keys || {};
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
    const peersResponse = await fetchJson(`${IPFS_API}/api/v0/swarm/peers`, { method: "POST" }, 5000);
    const peerCount = peersResponse.Peers?.length || 0;
    return { healthy: peerCount >= 1, peers: peerCount };
  } catch (err) {
    return { healthy: false, error: err.message };
  }
};

// Get comprehensive IPFS stats
const getIPFSStats = async () => {
  const [bwResponse, repoResponse, idResponse, peersResponse] = await Promise.all([
    fetchJson(`${IPFS_API}/api/v0/stats/bw?interval=5m`, { method: "POST" }, 5000),
    fetchJson(`${IPFS_API}/api/v0/repo/stat`, { method: "POST" }, 5000),
    fetchJson(`${IPFS_API}/api/v0/id`, { method: "POST" }, 5000),
    fetchJson(`${IPFS_API}/api/v0/swarm/peers`, { method: "POST" }, 5000),
  ]);

  return {
    bandwidth: {
      totalIn: bwResponse.TotalIn,
      totalOut: bwResponse.TotalOut,
      rateIn: bwResponse.RateIn,
      rateOut: bwResponse.RateOut,
      interval: "1h",
    },
    repository: {
      size: repoResponse.RepoSize,
      storageMax: repoResponse.StorageMax,
      numObjects: repoResponse.NumObjects,
      path: repoResponse.RepoPath,
      version: repoResponse.Version,
    },
    node: {
      id: idResponse.ID,
      publicKey: idResponse.PublicKey,
      agentVersion: idResponse.AgentVersion,
      protocolVersion: idResponse.ProtocolVersion,
    },
    peers: {
      count: peersResponse.Peers.length,
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
  unpinCid,
};

