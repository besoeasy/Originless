const axios = require("axios");
const { SimplePool, nip19 } = require("nostr-tools");
const WebSocket = require("ws");
const { webcrypto } = require("node:crypto");

// Provide globals required by nostr-tools when running in Node.js
if (typeof global.WebSocket === "undefined") {
  global.WebSocket = WebSocket;
}
if (typeof global.crypto === "undefined") {
  global.crypto = webcrypto;
}

const IPFS_API = process.env.IPFS_API || "http://127.0.0.1:5001";
const DEFAULT_RELAYS = ["wss://relay.damus.io", "wss://nos.lol"];
const KIND_WHITELIST = [1, 6, 30023, 30024, 9802];

// CID patterns for common forms (CIDv0 and CIDv1 in base58/base32)
const CID_V0_PATTERN = /\bQm[1-9A-HJ-NP-Za-km-z]{44}\b/g;
const CID_V1_PATTERN = /\b[bB][a-zA-Z2-7]{58,}\b/g;
const IPFS_URL_PATTERN = /\b(?:ipfs:\/\/|https?:\/\/(?:[^/]+\.)?(?:dweb\.link|ipfs\.io)\/ipfs\/)([A-Za-z0-9]+(?:[A-Za-z0-9._-]*))/gi;

const cleanCid = (raw) => {
  if (!raw) return null;
  const firstSegment = raw.split(/[/?#]/)[0];
  const trimmed = firstSegment.replace(/[^A-Za-z0-9]/g, "");
  if (trimmed.length < 46 || trimmed.length > 120) return null;
  return trimmed;
};

const decodePubkey = (input) => {
  if (!input || typeof input !== "string") {
    throw new Error("A nostr pubkey or npub is required");
  }

  const candidate = input.trim();

  if (/^[0-9a-fA-F]{64}$/.test(candidate)) {
    return candidate.toLowerCase();
  }

  if (candidate.startsWith("npub")) {
    const decoded = nip19.decode(candidate);
    if (decoded.type !== "npub" || !decoded.data) {
      throw new Error("Invalid npub");
    }
    return decoded.data;
  }

  throw new Error("Unsupported pubkey format. Provide hex or npub.");
};

const extractCidsFromContent = (text = "") => {
  const found = new Set();

  // Match URL-based references
  let urlMatch;
  while ((urlMatch = IPFS_URL_PATTERN.exec(text)) !== null) {
    const cid = cleanCid(urlMatch[1]);
    if (cid) found.add(cid);
  }

  // Match bare CIDv0
  const v0Matches = text.match(CID_V0_PATTERN) || [];
  v0Matches.forEach((cid) => {
    const clean = cleanCid(cid);
    if (clean) found.add(clean);
  });

  // Match bare CIDv1
  const v1Matches = text.match(CID_V1_PATTERN) || [];
  v1Matches.forEach((cid) => {
    const clean = cleanCid(cid);
    if (clean) found.add(clean);
  });

  return Array.from(found);
};

const fetchFollowingPubkeys = async ({ pubkey }) => {
  const pool = new SimplePool({ connectionTimeout: 7000 });
  try {
    const events = await pool.querySync(DEFAULT_RELAYS, { authors: [pubkey], kinds: [3], limit: 1 }, { maxWait: 8000 });
    if (!events.length) return [];
    const latest = events.sort((a, b) => b.created_at - a.created_at)[0];
    const follows = new Set();
    latest.tags.forEach((tag) => {
      if (Array.isArray(tag) && tag[0] === "p" && tag[1] && /^[0-9a-fA-F]{64}$/.test(tag[1])) {
        follows.add(tag[1].toLowerCase());
      }
    });
    return Array.from(follows);
  } finally {
    pool.destroy();
  }
};

const fetchEvents = async ({ pubkey, authors, relays = DEFAULT_RELAYS, kinds = KIND_WHITELIST, since, limit }) => {
  const pool = new SimplePool({ connectionTimeout: 7000 });
  const filter = { authors: authors || [pubkey], kinds, since, limit };

  try {
    const events = await pool.querySync(relays, filter, { maxWait: 8000 });
    // Deduplicate by id and sort newest first
    const unique = new Map();
    events.forEach((evt) => {
      if (!unique.has(evt.id)) {
        unique.set(evt.id, evt);
      }
    });
    return Array.from(unique.values()).sort((a, b) => b.created_at - a.created_at);
  } finally {
    pool.destroy();
  }
};

const getDeletedIds = (deleteEvents = []) => {
  const deleted = new Set();
  deleteEvents.forEach((evt) => {
    evt.tags.forEach((tag) => {
      if (Array.isArray(tag) && tag[0] === "e" && tag[1]) {
        deleted.add(tag[1]);
      }
    });
  });
  return deleted;
};

const pinCid = async (cid, ipfsApi = IPFS_API) => {
  const endpoint = `${ipfsApi}/api/v0/pin/add?arg=${encodeURIComponent(cid)}`;
  const res = await axios.post(endpoint, null, { timeout: 120000 });
  return res.data;
};

const syncNostrPins = async ({
  npubOrPubkey,
  ipfsApi = IPFS_API,
  kinds = KIND_WHITELIST,
  since,
  limit,
  maxPins,
  dryRun = false,
} = {}) => {
  if (!npubOrPubkey) {
    throw new Error("npubOrPubkey is required");
  }

  const pubkey = decodePubkey(npubOrPubkey);
  const events = await fetchEvents({ pubkey, relays: DEFAULT_RELAYS, kinds, since, limit });
  const deleteEvents = await fetchEvents({ pubkey, relays: DEFAULT_RELAYS, kinds: [5], since, limit });
  const deletedIds = getDeletedIds(deleteEvents);

  const cidSet = new Set();
  events
    .filter((evt) => !deletedIds.has(evt.id))
    .forEach((evt) => {
      extractCidsFromContent(evt.content).forEach((cid) => cidSet.add(cid));
    });

  const cids = Array.from(cidSet);
  const toPin = typeof maxPins === "number" ? cids.slice(0, maxPins) : cids;

  if (dryRun) {
    return {
      dryRun: true,
      relaysUsed: DEFAULT_RELAYS,
      eventsScanned: events.length,
      deletesSeen: deletedIds.size,
      cidsFound: cids.length,
      plannedPins: toPin,
    };
  }

  const results = [];
  for (const cid of toPin) {
    try {
      const data = await pinCid(cid, ipfsApi);
      results.push({ cid, ok: true, data });
    } catch (err) {
      results.push({ cid, ok: false, error: err.message });
    }
  }

  return {
    dryRun: false,
    relaysUsed: DEFAULT_RELAYS,
    eventsScanned: events.length,
    deletesSeen: deletedIds.size,
    cidsFound: cids.length,
    pinned: results.filter((r) => r.ok).length,
    failed: results.filter((r) => !r.ok).length,
    results,
  };
};

const toNpub = (hex) => {
  try {
    return nip19.npubEncode(hex);
  } catch (_) {
    return hex;
  }
};

const syncFollowPins = async ({
  npubOrPubkey,
  ipfsApi = IPFS_API,
  kinds = KIND_WHITELIST,
  since,
  limit,
  maxPins,
  dryRun = false,
} = {}) => {
  if (!npubOrPubkey) {
    throw new Error("npubOrPubkey is required");
  }

  const pubkey = decodePubkey(npubOrPubkey);
  const following = await fetchFollowingPubkeys({ pubkey });
  if (!following.length) {
    return {
      dryRun,
      relaysUsed: DEFAULT_RELAYS,
      following: [],
      eventsScanned: 0,
      deletesSeen: 0,
      cidsFound: 0,
      plannedPins: [],
    };
  }

  const events = await fetchEvents({ authors: following, relays: DEFAULT_RELAYS, kinds, since, limit });
  const deleteEvents = await fetchEvents({ authors: following, relays: DEFAULT_RELAYS, kinds: [5], since, limit });
  const deletedIds = getDeletedIds(deleteEvents);

  const cidSet = new Set();
  events
    .filter((evt) => !deletedIds.has(evt.id))
    .forEach((evt) => {
      extractCidsFromContent(evt.content).forEach((cid) => cidSet.add(cid));
    });

  const cids = Array.from(cidSet);
  const toPin = typeof maxPins === "number" ? cids.slice(0, maxPins) : cids;

  if (dryRun) {
    return {
      dryRun: true,
      relaysUsed: DEFAULT_RELAYS,
      following: following.map(toNpub),
      eventsScanned: events.length,
      deletesSeen: deletedIds.size,
      cidsFound: cids.length,
      plannedPins: toPin,
    };
  }

  const results = [];
  for (const cid of toPin) {
    try {
      const data = await pinCid(cid, ipfsApi);
      results.push({ cid, ok: true, data });
    } catch (err) {
      results.push({ cid, ok: false, error: err.message });
    }
  }

  return {
    dryRun: false,
    relaysUsed: DEFAULT_RELAYS,
    following: following.map(toNpub),
    eventsScanned: events.length,
    deletesSeen: deletedIds.size,
    cidsFound: cids.length,
    pinned: results.filter((r) => r.ok).length,
    failed: results.filter((r) => !r.ok).length,
    results,
  };
};

module.exports = {
  decodePubkey,
  extractCidsFromContent,
  fetchEvents,
  fetchFollowingPubkeys,
  pinCid,
  syncNostrPins,
  syncFollowPins,
  toNpub,
  constants: {
    IPFS_API,
    DEFAULT_RELAYS,
    KIND_WHITELIST,
  },
};
