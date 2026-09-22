# Node.js & JavaScript Integration Guide

Originless can be used in Node.js (v18+) with **zero external dependencies**. Modern Node.js provides native `node:crypto`, native `fetch`, `FormData`, and `Blob`.

---

## 1. Zero-Dependency Signing Helper (`originless.mjs`)

Create a lightweight helper module:

```javascript
import crypto from "node:crypto";

/**
 * Generates an Ed25519 keypair and the formatted owner string.
 */
export function generateKeypair() {
  const { publicKey, privateKey } = crypto.generateKeyPairSync("ed25519");
  
  // Extract 32-byte raw public key buffer
  const rawPublic = publicKey.export({ type: "spki", format: "der" }).subarray(-32);
  const owner = `ed25519:${rawPublic.toString("hex")}`;
  
  return { publicKey, privateKey, owner };
}

/**
 * Returns deterministic canonical JSON (sorted keys, compact whitespace).
 */
export function canonicalJSON(obj) {
  if (obj === null || typeof obj !== "object") return JSON.stringify(obj);
  if (Array.isArray(obj)) return `[${obj.map(canonicalJSON).join(",")}]`;
  const keys = Object.keys(obj).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonicalJSON(obj[k])}`).join(",")}}`;
}

/**
 * Signs and formats an Originless event.
 */
export function signEvent({ privateKey, owner, collection, data, labels = [], ttlSeconds = 86400 * 30, blob = "" }) {
  const now = Math.floor(Date.now() / 1000);
  const expiresAt = now + ttlSeconds;
  const canonicalData = canonicalJSON(data);
  const sortedLabels = [...labels].sort();

  // owner:collection:created:expires:canonical(data):blob:labels
  const msg = `${owner}:${collection}:${now}:${expiresAt}:${canonicalData}:${blob}:${sortedLabels.join(",")}`;
  const msgHash = crypto.createHash("sha256").update(msg).digest();

  // Ed25519 signature
  const sig = crypto.sign(null, msgHash, privateKey).toString("hex");

  const event = {
    owner,
    collection,
    created_at: now,
    expires_at: expiresAt,
    data,
    labels: sortedLabels,
    sig,
  };

  if (blob) {
    event.blob = blob;
  }

  return event;
}
```

---

## 2. Publishing an Event

```javascript
import { generateKeypair, signEvent } from "./originless.mjs";

const BASE_URL = "http://localhost:3232";
const { privateKey, owner } = generateKeypair();

const event = signEvent({
  privateKey,
  owner,
  collection: "logs",
  data: { level: "info", message: "Node service started successfully", port: 8080 },
  labels: ["env:production", "service:auth"],
  ttlSeconds: 86400 * 14, // 14 days
});

const res = await fetch(`${BASE_URL}/events`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(event),
});

const data = await res.json();
console.log("Published event:", data);
// Output: { status: 'success', id: '...', stored_at: '...' }
```

---

## 3. Uploading an Event with Attached Binary Blob (Atomic Multipart)

Upload an event and an arbitrary binary file (e.g. tarball, database, model file) in a single atomic request:

```javascript
import fs from "node:fs/promises";
import crypto from "node:crypto";
import { generateKeypair, signEvent } from "./originless.mjs";

const BASE_URL = "http://localhost:3232";
const { privateKey, owner } = generateKeypair();

// 1. Read binary file and compute SHA-256
const fileBytes = await fs.readFile("archive.tar.gz");
const blobHash = crypto.createHash("sha256").update(fileBytes).digest("hex");

// 2. Sign event with top-level blob pointer
const event = signEvent({
  privateKey,
  owner,
  collection: "archives",
  data: { filename: "archive.tar.gz", size: fileBytes.length },
  labels: ["type:backup"],
  blob: blobHash,
});

// 3. Construct multipart payload (event part FIRST)
const form = new FormData();
form.append("event", new Blob([JSON.stringify(event)], { type: "application/json" }));
form.append("blob", new Blob([fileBytes], { type: "application/octet-stream" }), "archive.tar.gz");

const res = await fetch(`${BASE_URL}/events`, {
  method: "POST",
  body: form,
});

const result = await res.json();
console.log("Stored event and blob:", result);
```

---

## 4. Querying Events & Downloading Blobs

```javascript
import crypto from "node:crypto";
import fs from "node:fs/promises";

const BASE_URL = "http://localhost:3232";

// 1. Query events
const queryRes = await fetch(`${BASE_URL}/events?collection=logs&limit=5`);
const { events } = await queryRes.json();
console.log(`Found ${events.length} records`);

// 2. Query event with resolved blob metadata
const eventId = events[0].id;
const detailRes = await fetch(`${BASE_URL}/events/${eventId}?resolve=blob`);
const detail = await detailRes.json();
console.log("Event detail:", detail);

// 3. Download binary blob by hash
const blobHash = detail.blob?.hash;
if (blobHash) {
  const blobRes = await fetch(`${BASE_URL}/blob/${blobHash}`);
  const arrayBuffer = await blobRes.arrayBuffer();
  const buffer = Buffer.from(arrayBuffer);

  // Validate integrity
  const downloadedHash = crypto.createHash("sha256").update(buffer).digest("hex");
  if (downloadedHash !== blobHash) {
    throw new Error("SHA-256 verification failed");
  }

  await fs.writeFile("downloaded_archive.tar.gz", buffer);
  console.log("Verified and saved blob successfully.");
}
```

---

## 5. Real-Time Streaming (Server-Sent Events)

### In Node.js:
```javascript
const BASE_URL = "http://localhost:3232";

const response = await fetch(`${BASE_URL}/events/stream?collection=logs`);
const reader = response.body.getReader();
const decoder = new TextDecoder();

while (true) {
  const { done, value } = await reader.read();
  if (done) break;

  const chunk = decoder.decode(value, { stream: true });
  for (const line of chunk.split("\n")) {
    if (line.startsWith("data: ")) {
      const event = JSON.parse(line.slice(6));
      console.log(`[LIVE EVENT] ${event.id}:`, event.data);
    }
  }
}
```

### In Web Browsers:
```javascript
// Browser native EventSource
const stream = new EventSource("http://localhost:3232/events/stream?collection=logs");

stream.addEventListener("event", (e) => {
  const record = JSON.parse(e.data);
  console.log("New event:", record);
});
```
