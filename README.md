<div align="center">

# Originless

**Zero-auth backend for the open web — signed events, binary blobs, live streams.**  
No accounts. No API keys. One container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![API](https://img.shields.io/badge/HTTP%20API-api.md-1f6feb)](api.md)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/ISC)
[![Available on Umbrel App Store](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## Install

Pulls `ghcr.io/besoeasy/originless:latest`, persists state in a named volume, serves everything on port `3232`.

**Docker:**

```bash
docker run -d \
  --name originless \
  --restart unless-stopped \
  -p 3232:3232 \
  -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

**Podman:**

```bash
podman run -d \
  --name originless \
  --restart unless-stopped \
  -p 3232:3232 \
  -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

There is **no total storage cap** and uploads are **not size-limited**: blobs persist until their size-weighted retention window elapses, then the janitor evicts them. Data persists in the `/data` volume.

Open **http://localhost:3232** for the dashboard · Full API in **[api.md](api.md)** · Plain-text agent prompt at **[/agent.txt](static/agent.txt)**

---

## What it does

Two primitives, no auth:

- **Signed Events** (`POST /events`, `GET /events`, `GET /events/stream`) — Ed25519-signed JSON documents (8 KB max) with collections, labels, mandatory TTL (up to 1 year), and a real-time SSE feed. Identity is a keypair; IDs are server-computed content hashes. (Legacy `/records` endpoints supported as aliases).
- **Binary Blobs** (`POST /up`, `GET /down/{hash}`, `GET /blobs`) — opaque `.bin` files stored as `<sha256>.bin`, deduplicated, with size-weighted retention (30 days at 512 MiB → 1 year near 0 bytes). Expired blobs are evicted periodically by the janitor. Non-binary uploads (HTML, images, PDFs, text) are rejected; downloads send `nosniff`.

Events link blobs via `"_blob": "<sha256>"` in `data` — validated on publish, exempt from eviction while the event lives, inlinable with `GET /events/{id}?resolve=blob`.

---

## Use Cases & Architecture Patterns

| Pattern | Primitives | Endpoints | Why Originless Fits |
| :--- | :--- | :--- | :--- |
| **Real-Time Chat & Web Rooms** | Signed Events + SSE | `POST /events`<br>`GET /events/stream` | Ephemeral chat rooms, topic filtering via `labels`, auto-expiring messages, and zero-polling browser `EventSource` sync. |
| **AI Agents & Autonomous Bots** | Signed Events + Agent Skill | `GET /agent.txt`<br>`POST /events` | Point any AI agent directly to `/agent.txt` for instant tool onboarding. Autonomous keypair identity with zero API key provisioning. |
| **Decentralized Game Saves & Sync** | Blobs + Linked Events | `POST /up`<br>`GET /events/{id}?resolve=blob` | SHA-256 deduplicated binary buffers linked to signed player state. Active events protect blobs from janitor eviction. |
| **IoT Telemetry & Incident Alerts** | Signed Events + SSE | `POST /events`<br>`GET /events/stream` | Cryptographically signed device telemetry without database user accounts. Filter live push streams by `collection=alerts&label=severity:critical`. |
| **Instant CLI Binary Drops** | Content-Addressed Blobs | `POST /up`<br>`GET /down/{hash}` | Quick upload for diagnostic tarballs, SQLite snapshots, or game assets. Automated size-weighted retention window and LRU janitor pruning. |

---

## Practical Examples

### 1. Real-Time Web Chat (Browser JavaScript)

Subscribe to room events live without WebSockets or third-party client libraries:

```javascript
// Connect to the room's live push feed
const room = new EventSource("http://localhost:3232/events/stream?collection=chat&label=room:lobby");

room.addEventListener("event", (e) => {
  const event = JSON.parse(e.data);
  console.log(`[${event.data.user}]: ${event.data.text}`);
});
```

### 2. AI Agent Task Coordination (cURL)

Give autonomous agents a shared coordination bus without managing passwords or access tokens:

```bash
# Agent publishes a completed task result
curl -X POST http://localhost:3232/events \
  -H "Content-Type: application/json" \
  -d '{
    "owner": "ed25519:3b6a...29",
    "collection": "agent-tasks",
    "created_at": 1758420000,
    "expires_at": 1789956000,
    "data": { "task_id": "audit-42", "result": "passed", "confidence": 0.98 },
    "labels": ["status:completed", "agent:scanner-01"],
    "sig": "a3f1...c9"
  }'

# Coordinator streams completed tasks in real-time
curl -N "http://localhost:3232/events/stream?collection=agent-tasks&label=status:completed"
```

### 3. Game Save State with Linked Binary Blob

Store a raw binary save file (`save.bin`) and link it to an Ed25519-signed manifest:

```bash
# 1. Upload binary save state (deduplicated by SHA-256)
curl -X POST -F "file=@save.bin" http://localhost:3232/up
# Response: {"status":"success","hash":"e3b0...85","url":"/down/e3b0...85"}

# 2. Publish signed save manifest linking the blob (exempts the blob from janitor eviction)
curl -X POST http://localhost:3232/events \
  -H "Content-Type: application/json" \
  -d '{
    "owner": "ed25519:3b6a...29",
    "collection": "gamesaves",
    "created_at": 1758420000,
    "expires_at": 1789956000,
    "data": { "slot": 1, "level": 34, "score": 9200, "_blob": "e3b0...85" },
    "labels": ["slot:1", "player:hero"],
    "sig": "b2f4...d1"
  }'

# 3. Fetch save state + inlined blob download URL in a single round-trip
curl "http://localhost:3232/events/8f2c...1a?resolve=blob"
```

### 4. CLI Diagnostic Dump & Expiring Drop

Drop any `.bin` file directly from a server or terminal:

```bash
# Upload and receive direct content address
curl -X POST -F "file=@dump.bin" http://localhost:3232/up
# Output: {"status":"success","hash":"a3f1...","url":"http://localhost:3232/down/a3f1..."}

# Download from any machine
curl -O http://localhost:3232/down/a3f1...
```

---

## Quick Reference & Health

```bash
# Node health, blob counts, event counts, and active SSE subscribers
curl http://localhost:3232/status

# Prometheus metrics
curl http://localhost:3232/metrics
```
