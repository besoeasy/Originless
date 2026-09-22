<div align="center">

# Originless

**Zero-auth backend for the open web — signed events, binary blobs, live P2P sync.**  
No accounts. No API keys. Zero complex setup. One single container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![P2P Mesh](https://img.shields.io/badge/p2p-BitTorrent%20DHT-orange)](https://github.com/besoeasy/originless)
[![Agent Contract](https://img.shields.io/badge/agent%20contract-agent.txt-1f6feb)](static/agent.txt)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/ISC)
[![Available on Umbrel](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## What Originless Replaces

Instead of running and configuring half a dozen microservices, API keys, and databases, Originless replaces them all with **two simple primitives** (signed JSON events + content-addressed binary blobs) running in a single binary:

| App / Service | What Originless Does Instead | Why It's Better |
| :--- | :--- | :--- |
| **ntfy / Pusher** | Real-time live pub/sub streams via Server-Sent Events (`GET /events/stream?label=...`). | No WebSockets, no server daemons. Subscribe directly in browser `EventSource` or `curl -N`. |
| **Sentry** | Fire-and-forget signed telemetry and error logs (`POST /events`) with labels and automatic TTL expiry. | Zero auth tokens to provision or rotate. Filter by error tag or live-tail critical alerts. |
| **Nostr Relays** | Cryptographically signed Ed25519 events with tamper-proof IDs and DHT swarm sync. | No complex NIP protocols, no paid relay operators. Standard HTTP REST + SSE. |
| **0x0.st / Pastebin** | Content-addressed ephemeral binary file drops (`POST /events` with a `blob` part, `GET /blob/<sha256>`). | Deduplicated by SHA-256. Reference-driven retention pins blobs while any signed event links them, then auto-evicts orphans cleanly. |
| **Redis Pub/Sub** | Ephemeral JSON documents with self-expiring TTLs and real-time streaming. | Pure SQLite WAL storage with microsecond query latency and zero RAM bloat. |
| **S3 / Object Store** | Content-addressed blob storage with streaming downloads and checksum verification. | Zero IAM policies or bucket configuration. Upload once, verify everywhere. |

---

## Run in 10 seconds

Zero configuration. One command starts your node, maps router ports via UPnP, joins the BitTorrent P2P mesh, and starts syncing automatically:

### Docker
```bash
docker run -d --name originless --restart unless-stopped \
  -p 3232:3232 -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

### Podman
```bash
podman run -d --name originless --restart unless-stopped \
  -p 3232:3232 -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

That's it! Your node is live at **http://localhost:3232**.

### Mesh Network Options

* **Public Mesh (Default)**: No extra flags needed. By default, `NETWORK_ID` is `originless` — your nodes immediately discover each other across different PCs or cloud servers over BitTorrent Mainline DHT and sync events and blobs.
* **Private Mesh**: Add `-e NETWORK_ID=my-project` on your containers to create an isolated private swarm.
* **Standalone / Local Only**: Add `-e NETWORK_ID=off` (or `none`) to disable P2P sync and run strictly offline.

* **Dashboard**: Open **http://localhost:3232** in your browser.
* **AI Agents**: Point LLMs or agents at **[/agent.txt](static/agent.txt)** for the complete machine-readable contract.

---

## Automatic P2P Mesh (Torrent Network)

Originless is built for zero-config Docker and Podman deployments. With P2P active (`NETWORK_ID=originless` by default):

1. **BitTorrent Mainline DHT (BEP 5)**: Nodes announce themselves to the global BitTorrent DHT swarm under `sha1("originless:" + NETWORK_ID)`.
2. **Auto UPnP & NAT-PMP Port Forwarding**: On startup, Originless automatically maps port 3232 on your home or office Wi-Fi router.
3. **Peer Exchange (PEX)**: Connected peers share their peer lists, creating a resilient, fully connected swarm.
4. **Local Service Discovery (BEP 14 LSD)**: Nodes on the same LAN or Wi-Fi discover each other via UDP multicast and sync at local wire speed.
5. **Bloom Filter Set Reconciliation**: Upon connection, nodes exchange 1% false-positive Bloom filters to transfer missing records and binary blobs without redundant bandwidth.

---

## 30-Second Tour

```bash
BASE=http://localhost:3232

# 1. Health check: active events, blobs, and connected P2P peers
curl $BASE/status

# 2. Publish a signed event AND raw bytes in one request (event part first).
# The event below carries top-level "blob": "<sha256>" (signed); the server
# validates the signature before staging any bytes, then re-hashes them.
head -c 64 /dev/urandom > firmware.dump
HASH=$(sha256sum firmware.dump | cut -d' ' -f1)
curl -X POST -F "event=@event.json;type=application/json" \
     -F "blob=@firmware.dump;type=application/octet-stream" $BASE/events

# 3. Any machine in the swarm can fetch the bytes back by content address:
curl -O $BASE/blob/$HASH

# 4. Query signed events (filtered by collection, label, or blob link)
curl "$BASE/events?collection=chat&label=room:general&limit=20"
curl "$BASE/events?blob=$HASH"

# 5. Live stream events (Server-Sent Events — no WebSocket required)
curl -N "$BASE/events/stream?collection=chat&label=room:general"
```

---

## The Two Primitives

### 1. Events — Signed JSON Documents (8 KB max, TTL ≤ 1 year)
Events are immutable, cryptographically signed JSON documents. You own the private key; Originless verifies the Ed25519 signature and stores the event.

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `POST` | `/events` | Publish a signed event (`201` created, `200` duplicate) |
| `GET` | `/events?collection=&label=&owner=&since=&until=&search=&blob=&limit=&cursor=` | Query newest-first. Expired events hidden by default. |
| `GET` | `/events/{id}` | Fetch a single event. Add `?resolve=blob` to inline linked blob metadata. |
| `GET` | `/events/stream?collection=&label=&owner=&search=` | Real-time Server-Sent Events (SSE) feed. |

```json
{
  "owner": "ed25519:<64 hex pubkey>",
  "collection": "alerts",
  "created_at": 1758420000,
  "expires_at": 1789956000,
  "data": { "service": "api", "error": "database connection timeout" },
  "blob": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "labels": ["severity:critical", "env:prod"],
  "sig": "<128 hex chars>"
}
```
(`blob` is the optional top-level attachment pointer — a SHA-256 hex string. Omit it or send `null` for blob-less events.)

### 2. Blobs — Opaque Binary Objects (Content-Addressed)
Upload raw binary bytes. Originless verifies the SHA-256 checksum and serves it with immutable caching headers.

| **Method** | **Endpoint** | **Description** |
| :--- | :--- | :--- |
| `POST` | `/events` | Publish a signed event; add a `blob` multipart part (after the `event` part) to carry the raw bytes of one content-addressed blob in the same request. Signature is validated before any bytes touch disk. |
| `GET` | `/blob/{hash}` | Download by SHA-256 hash (64-char hex). Supports `HEAD`, `ETag`, and byte ranges. |
| `GET` | `/blobs?limit=50&offset=0` | List stored blobs sorted by newest upload. |

* **Retention**: Reference-driven, size-neutral. A blob is pinned while any unexpired signed event references it; once the last event expires it becomes an orphan and is auto-evicted after `BlobOrphanGraceDays` (default 7).
* **Link to Events**: Set the top-level `blob = "<sha256>"` field before signing. The hash feeds the event ID, so a linked blob can't be silently swapped — and `data` stays 100% yours, no reserved keys inside it.

#### Opaque Bytes Only (Abuse Immunity & Security)
Originless accepts only opaque binary bytes and rejects renderable/textual payloads (`text/*`, `image/*`, `application/pdf`) by sniffing content, not filenames. All downloads are served as `application/octet-stream` with `X-Content-Type-Options: nosniff`:

* **No Free Media CDN / Hotlinking**: Prevents third-party sites from embedding images or streaming video through your node, protecting your bandwidth.
* **Immunity to XSS & Phishing**: Browsers never execute, parse, or inline-render uploaded files under your origin. Malicious HTML, scripts, or weaponized SVGs are completely neutralized.
* **Legal & Abuse Shield**: Open unauthenticated image/video hosts are magnets for copyright infringement, pirate streaming, and illicit media. Opaque blobs keep Originless a blind, neutral data pipe.
* **Zero Parsing Attack Surface**: No image thumbnailers, EXIF extractors, or video transcoders that can be targeted by decompression bombs or memory corruption exploits.
* **Client-Sovereign Media**: Need to store an image or document? Package or encrypt it into opaque bytes, set the event's top-level `blob` field to its SHA-256, and decode or render it client-side (`URL.createObjectURL`).

---

## Client Guides & Documentation

Originless requires no proprietary SDKs or API keys. Complete copy-pasteable guides are available in the [`docs/`](docs/) directory:

* 📖 [**cURL & Shell Guide**](docs/curl.md) — Signing with OpenSSL, multipart atomic blob uploads, SSE streaming with `curl -N`, and keyset pagination.
* 🐍 [**Python Guide**](docs/python.md) — Zero-auth Ed25519 signing helper, JSON publishing, binary blob uploads, and real-time streaming.
* 🟢 [**Node.js & JavaScript Guide**](docs/nodejs.md) — Zero-dependency integration using Node.js native `node:crypto`, `fetch`, and `FormData`.
* 🤖 [**AI Agent Skill Contract**](static/agent.txt) — Plaintext specification served at `/agent.txt` for autonomous LLM agents (Claude, Cursor, Copilot, Antigravity).

---

## API Summary

| Endpoint | Method | Purpose |
| :--- | :--- | :--- |
| `GET /status` | `GET` | Health, event/blob counts, active SSE clients, and P2P mesh status |
| `POST /events` | `POST` | Publish signed event, or event + one content-addressed blob's bytes (multipart `event` + `blob` parts, event first) |
| `GET /events` | `GET` | Query events with filtering (`collection`, `label`, `owner`, `since`, `until`, `blob`, `cursor`) |
| `GET /events/{id}` | `GET` | Fetch event by ID (`?resolve=blob` to inline linked blob) |
| `GET /events/stream` | `GET` | Real-time Server-Sent Events stream |
| `GET /blob/{hash}` | `GET` | Download blob bytes by hash (`HEAD` supported) |
| `GET /blobs` | `GET` | List stored blobs |
| `GET /metrics` | `GET` | Prometheus telemetry metrics |
| `GET /agent.txt` | `GET` | Machine-readable contract for AI agents |
