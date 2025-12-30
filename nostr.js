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
const RELAY_COUNT = 4;

const DEFAULT_RELAYS = [
  "wss://auth.nostr1.com",
  "wss://bostr.bitcointxoko.com",
  "wss://eden.nostr.land",
  "wss://groups.0xchat.com",
  "wss://inbox.nostr.wine",
  "wss://no.str.cr",
  "wss://nos.lol",
  "wss://nostr-01.yakihonne.com",
  "wss://nostr.band",
  "wss://nostr.data.haus",
  "wss://nostr.mom",
  "wss://nostr.oxtr.dev",
  "wss://nostr.swiss-enigma.ch",
  "wss://nostrue.com",
  "wss://offchain.pub",
  "wss://orangepiller.org",
  "wss://purplepag.es",
  "wss://pyramid.fiatjaf.com",
  "wss://relay.0xchat.com",
  "wss://relay.coinos.io",
  "wss://relay.current.fyi",
  "wss://relay.damus.io",
  "wss://relay.fountain.fm",
  "wss://relay.lumina.rocks",
  "wss://relay.nostr.band",
  "wss://relay.nostr.bg",
  "wss://relay.nostr.wirednet.jp",
  "wss://relay.primal.net",
  "wss://relay.siamstr.com",
  "wss://wheat.happytavern.co",
];
const KIND_WHITELIST = [1, 6, 30023, 30024, 9802];

// Randomly select N relays from DEFAULT_RELAYS
const getRandomRelays = (count = RELAY_COUNT) => {
  const shuffled = [...DEFAULT_RELAYS].sort(() => Math.random() - 0.5);
  return shuffled.slice(0, Math.min(count, DEFAULT_RELAYS.length));
};

// CID patterns for common forms (CIDv0 and CIDv1 in base58/base32)
const CID_V0_PATTERN = /\bQm[1-9A-HJ-NP-Za-km-z]{44}\b/g;
const CID_V1_PATTERN = /\b[bB][a-zA-Z2-7]{58,}\b/g;
const IPFS_URL_PATTERN = /\b(?:ipfs:\/\/|https?:\/\/(?:[^/]+\.)?(?:dweb\.link|ipfs\.io)\/ipfs\/)([A-Za-z0-9]+(?:[A-Za-z0-9._-]*))/gi;

const isExpired = (event) => {
  const expireTag = event.tags.find((tag) => tag[0] === "expiration");
  if (!expireTag || !expireTag[1]) return false;
  const expireTime = parseInt(expireTag[1], 10);
  return Date.now() / 1000 > expireTime;
};

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
  const relays = getRandomRelays();
  try {
    const events = await pool.querySync(relays, { authors: [pubkey], kinds: [3], limit: 1 }, { maxWait: 8000 });
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

const fetchEvents = async ({ pubkey, authors, relays = getRandomRelays(), kinds = KIND_WHITELIST, since, until, limit = 250 }) => {
  const pool = new SimplePool({ connectionTimeout: 7000 });
  const filter = { authors: authors || [pubkey], kinds, since, until, limit };

  try {
    const events = await pool.querySync(relays, filter, { maxWait: 15000 });
    // Deduplicate by id and sort newest first
    const unique = new Map();
    events.forEach((evt) => {
      if (!unique.has(evt.id)) {
        unique.set(evt.id, evt);
      }
    });
    return Array.from(unique.values())
      .filter((evt) => !isExpired(evt))
      .sort((a, b) => b.created_at - a.created_at);
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

const fetchAllUserEvents = async ({ pubkey, relays = getRandomRelays(), kinds = KIND_WHITELIST, pageSize = 250 } = {}) => {
  const allEvents = new Map();
  let until = Math.floor(Date.now() / 1000);
  let hasMore = true;
  let pageCount = 0;

  while (hasMore && pageCount < 100) {
    const events = await fetchEvents({ pubkey, relays, kinds, until, limit: pageSize });
    if (!events.length) break;

    events.forEach((evt) => {
      if (!allEvents.has(evt.id)) {
        allEvents.set(evt.id, evt);
      }
    });

    const minTimestamp = Math.min(...events.map((e) => e.created_at));
    if (minTimestamp === until) {
      hasMore = false;
      break;
    }

    until = minTimestamp - 1;
    pageCount++;
  }

  return Array.from(allEvents.values()).sort((a, b) => b.created_at - a.created_at);
};

const fetchAllFollowingEvents = async ({ authors, relays = getRandomRelays(), kinds = KIND_WHITELIST, pageSize = 250 } = {}) => {
  const allEvents = new Map();
  let until = Math.floor(Date.now() / 1000);
  let hasMore = true;
  let pageCount = 0;

  while (hasMore && pageCount < 100) {
    const events = await fetchEvents({ authors, relays, kinds, until, limit: pageSize });
    if (!events.length) break;

    events.forEach((evt) => {
      if (!allEvents.has(evt.id)) {
        allEvents.set(evt.id, evt);
      }
    });

    const minTimestamp = Math.min(...events.map((e) => e.created_at));
    if (minTimestamp === until) {
      hasMore = false;
      break;
    }

    until = minTimestamp - 1;
    pageCount++;
  }

  return Array.from(allEvents.values()).sort((a, b) => b.created_at - a.created_at);
};

const pinCid = async (cid, ipfsApi = IPFS_API) => {
  const endpoint = `${ipfsApi}/api/v0/pin/add?arg=${encodeURIComponent(cid)}`;
  const res = await axios.post(endpoint, null, { timeout: 900000 });
  return res.data;
};

const addCid = async (cid, ipfsApi = IPFS_API) => {
  // Fetch the CID without pinning - will be cached but can be garbage collected
  const endpoint = `${ipfsApi}/api/v0/block/get?arg=${encodeURIComponent(cid)}`;
  const res = await axios.post(endpoint, null, { timeout: 900000, responseType: 'arraybuffer' });
  return { cid, size: res.data.length };
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
  const relays = getRandomRelays();
  const events = limit ? await fetchEvents({ pubkey, relays, kinds, since, limit }) : await fetchAllUserEvents({ pubkey, relays, kinds });
  const deleteEvents = await fetchAllUserEvents({ pubkey, relays, kinds: [5] });
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
      relaysUsed: relays,
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
    relaysUsed: relays,
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
  const relays = getRandomRelays();
  const following = await fetchFollowingPubkeys({ pubkey });
  if (!following.length) {
    return {
      dryRun,
      relaysUsed: relays,
      following: [],
      eventsScanned: 0,
      deletesSeen: 0,
      cidsFound: 0,
      plannedPins: [],
    };
  }

  const events = limit ? await fetchEvents({ authors: following, relays, kinds, since, limit }) : await fetchAllFollowingEvents({ authors: following, relays, kinds });
  const deleteEvents = await fetchAllFollowingEvents({ authors: following, relays, kinds: [5] });
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
      relaysUsed: relays,
      following: following.map(toNpub),
      eventsScanned: events.length,
      deletesSeen: deletedIds.size,
      cidsFound: cids.length,
      plannedAdds: toPin, // Changed from plannedPins to plannedAdds
    };
  }

  // For friends, add without pinning (ephemeral cache, allows garbage collection)
  const results = [];
  for (const cid of toPin) {
    try {
      const data = await addCid(cid, ipfsApi);
      results.push({ cid, ok: true, data });
    } catch (err) {
      results.push({ cid, ok: false, error: err.message });
    }
  }

  return {
    dryRun: false,
    relaysUsed: relays,
    following: following.map(toNpub),
    eventsScanned: events.length,
    deletesSeen: deletedIds.size,
    cidsFound: cids.length,
    added: results.filter((r) => r.ok).length, // Changed from 'pinned' to 'added'
    failed: results.filter((r) => !r.ok).length,
    results,
  };
};

module.exports = {
  decodePubkey,
  extractCidsFromContent,
  fetchEvents,
  fetchAllUserEvents,
  fetchAllFollowingEvents,
  fetchFollowingPubkeys,
  getRandomRelays,
  pinCid,
  addCid,
  syncNostrPins,
  syncFollowPins,
  toNpub,
  constants: {
    IPFS_API,
    DEFAULT_RELAYS,
    RELAY_COUNT,
    KIND_WHITELIST,
  },
};
