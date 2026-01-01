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
const RELAY_COUNT = 8;

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

// Check if a CID is already pinned
const isPinned = async (cid, ipfsApi = IPFS_API) => {
  const startTime = Date.now();
  try {
    const endpoint = `${ipfsApi}/api/v0/pin/ls?arg=${encodeURIComponent(cid)}&type=recursive`;
    const res = await axios.post(endpoint, null, { timeout: 1000 });
    const isPinned = res.data?.Keys && Object.keys(res.data.Keys).length > 0;
    const duration = Date.now() - startTime;
    console.log(`[isPinned] ${cid} → ${isPinned ? 'PINNED' : 'NOT PINNED'} (${duration}ms)`);
    return isPinned;
  } catch (err) {
    const duration = Date.now() - startTime;
    console.log(`[isPinned] ${cid} → ERROR: ${err.message} (${duration}ms)`);
    // If error occurs, assume it's not pinned
    return false;
  }
};

const pinCid = async (cid, onProgress = null, ipfsApi = IPFS_API) => {
  const startTime = Date.now();
  console.log(`[pinCid] Starting pin for ${cid}`);

  // Check if already pinned
  const alreadyPinned = await isPinned(cid, ipfsApi);

  if (alreadyPinned) {
    const duration = Date.now() - startTime;
    console.log(`[pinCid] ${cid} already pinned (${duration}ms)`);
    return { alreadyPinned: true, Pins: [cid] };
  }

  // Use progress=true for streaming progress updates (prevents timeout on large files)
  const endpoint = `${ipfsApi}/api/v0/pin/add?arg=${encodeURIComponent(cid)}&progress=true`;
  
  try {
    console.log(`[pinCid] Attempting to pin ${cid} with progress tracking`);
    const res = await axios.post(endpoint, null, { 
      timeout: 0, // No timeout - progress keeps connection alive
      responseType: 'stream',
      httpAgent: new (require('http').Agent)({ 
        keepAlive: true,
        keepAliveMsecs: 30000
      })
    });
    
    // Process streaming newline-delimited JSON responses
    let lastProgress = 0;
    let finalResult = null;
    
    await new Promise((resolve, reject) => {
      let buffer = '';
      
      res.data.on('data', (chunk) => {
        buffer += chunk.toString();
        
        // Process complete lines (newline-delimited JSON)
        const lines = buffer.split('\n');
        buffer = lines.pop() || ''; // Keep incomplete line in buffer
        
        lines.forEach(line => {
          if (!line.trim()) return;
          
          try {
            const data = JSON.parse(line);
            
            // Progress update
            if (data.Progress !== undefined && data.Progress !== lastProgress) {
              lastProgress = data.Progress;
              const progressMB = (data.Progress / 1024 / 1024).toFixed(2);
              console.log(`[pinCid] Progress: ${progressMB} MB`);
              
              // Call progress callback if provided
              if (onProgress) {
                onProgress({ cid, bytes: data.Progress, timestamp: Date.now() });
              }
            }
            
            // Final result with Pins array
            if (data.Pins) {
              finalResult = data;
            }
          } catch (e) {
            // Ignore parse errors for incomplete JSON chunks
          }
        });
      });
      
      res.data.on('end', () => {
        // If we got a result with Pins, success
        if (finalResult) {
          resolve(finalResult);
        } else {
          // No Pins in response, but stream ended - assume success
          resolve({ Pins: [cid] });
        }
      });
      
      res.data.on('error', reject);
    });
    
    const duration = Date.now() - startTime;
    const totalMB = (lastProgress / 1024 / 1024).toFixed(2);
    console.log(`[pinCid] ✓ Successfully pinned ${cid} (${totalMB} MB, ${duration}ms)`);
    return { ...finalResult, alreadyPinned: false, newlyPinned: true };
    
  } catch (err) {
    // If timeout or error, try caching first then pin
    if (err.code === 'ECONNABORTED' || err.code === 'ETIMEDOUT') {
      console.log(`[pinCid] Pin timeout, caching ${cid} first then pinning...`);
      try {
        // Cache it first (pass along progress callback)
        const cacheResult = await addCid(cid, ipfsApi, onProgress);
        console.log(`[pinCid] Cached ${(cacheResult.size / 1024 / 1024).toFixed(2)} MB, now pinning...`);
        
        // Now pin the cached data (should be instant) - still use progress
        const res2 = await axios.post(endpoint, null, { 
          timeout: 0, // No timeout even for cached pins
          responseType: 'stream',
          httpAgent: new (require('http').Agent)({ 
            keepAlive: true,
            keepAliveMsecs: 30000
          })
        });
        
        // Read stream to completion
        const result = await new Promise((resolve, reject) => {
          let buffer = '';
          res2.data.on('data', (chunk) => { buffer += chunk; });
          res2.data.on('end', () => {
            try {
              const lines = buffer.split('\n').filter(l => l.trim());
              const lastLine = lines[lines.length - 1];
              resolve(JSON.parse(lastLine));
            } catch (e) {
              resolve({ Pins: [cid] });
            }
          });
          res2.data.on('error', reject);
        });
        
        const duration = Date.now() - startTime;
        console.log(`[pinCid] ✓ Successfully pinned ${cid} after caching (${duration}ms total)`);
        return { ...result, alreadyPinned: false, newlyPinned: true, cached: true };
      } catch (cacheErr) {
        const duration = Date.now() - startTime;
        console.error(`[pinCid] Failed to cache+pin ${cid} (${duration}ms):`, cacheErr.message);
        throw cacheErr;
      }
    }
    
    // Other errors, just throw
    const duration = Date.now() - startTime;
    console.error(`[pinCid] Failed to pin ${cid} (${duration}ms):`, err.message);
    throw err;
  }
};

const addCid = async (cid, ipfsApi = IPFS_API, onProgress = null) => {
  const startTime = Date.now();
  console.log(`[addCid] Starting cache for ${cid}`);

  // Check if already in local repo (pinned or cached)
  const alreadyPinned = await isPinned(cid, ipfsApi);

  // Fetch the CID without pinning - will be cached but can be garbage collected
  console.log(`[addCid] Fetching ${cid} to cache (this may take a while)`);
  const endpoint = `${ipfsApi}/api/v0/block/get?arg=${encodeURIComponent(cid)}`;
  const res = await axios.post(endpoint, null, { 
    timeout: 0, // No timeout - let it run as long as needed
    responseType: 'stream',
    maxContentLength: Infinity,
    maxBodyLength: Infinity,
    httpAgent: new (require('http').Agent)({ 
      keepAlive: true,
      keepAliveMsecs: 30000 // Keep socket alive with 30s pings
    })
  });
  
  // Stream the data and count bytes (avoids loading entire file into memory)
  let size = 0;
  let lastProgressReport = Date.now();
  await new Promise((resolve, reject) => {
    res.data.on('data', (chunk) => { 
      size += chunk.length;
      
      // Report progress every 5 seconds if callback provided
      if (onProgress && Date.now() - lastProgressReport > 5000) {
        lastProgressReport = Date.now();
        onProgress({ cid, bytes: size, timestamp: Date.now() });
      }
    });
    res.data.on('end', resolve);
    res.data.on('error', reject);
  });
  
  const duration = Date.now() - startTime;
  const sizeMB = (size / 1024 / 1024).toFixed(2);
  console.log(`[addCid] \u2713 Successfully cached ${cid} (${sizeMB} MB, ${duration}ms)`);
  return { cid, size, alreadyPinned, newlyAdded: !alreadyPinned };
};

// Get size of a CID
const getCidSize = async (cid, ipfsApi = IPFS_API) => {
  try {
    // Try files/stat first
    const statResponse = await axios.post(`${ipfsApi}/api/v0/files/stat?arg=/ipfs/${encodeURIComponent(cid)}`, {}, { timeout: 5000 });
    return statResponse.data.CumulativeSize || statResponse.data.Size || 0;
  } catch (err) {
    // Try block/stat as fallback
    try {
      const blockResponse = await axios.post(`${ipfsApi}/api/v0/block/stat?arg=${encodeURIComponent(cid)}`, {}, { timeout: 5000 });
      return blockResponse.data.Size || 0;
    } catch (blockErr) {
      console.warn(`Failed to get size for CID ${cid}:`, blockErr.message);
      return 0;
    }
  }
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

  const cidMap = new Map();
  events
    .filter((evt) => !deletedIds.has(evt.id))
    .forEach((evt) => {
      extractCidsFromContent(evt.content).forEach((cid) => {
        if (!cidMap.has(cid)) {
          cidMap.set(cid, {
            cid,
            eventId: evt.id,
            timestamp: evt.created_at,
            author: evt.pubkey,
          });
        }
      });
    });

  const cids = Array.from(cidMap.values());
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
  for (const cidObj of toPin) {
    try {
      const data = await pinCid(cidObj.cid, ipfsApi);
      results.push({ ...cidObj, ok: true, data });
    } catch (err) {
      results.push({ ...cidObj, ok: false, error: err.message });
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

module.exports = {
  decodePubkey,
  extractCidsFromContent,
  fetchEvents,
  fetchAllUserEvents,
  fetchAllFollowingEvents,
  fetchFollowingPubkeys,
  getRandomRelays,
  isPinned,
  pinCid,
  addCid,
  getCidSize,
  syncNostrPins,
  toNpub,
  constants: {
    IPFS_API,
    DEFAULT_RELAYS,
    RELAY_COUNT,
    KIND_WHITELIST,
  },
};
