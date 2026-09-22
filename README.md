<div align="center">

# Originless

**Zero-auth backend for the open web — signed events, binary blobs, live P2P sync.**  
No accounts. No API keys. Zero complex setup. One single container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![P2P Mesh](https://img.shields.io/badge/p2p-libp2p-orange)](https://github.com/besoeasy/originless)
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
| **Nostr Relays** | Cryptographically signed Ed25519 events with tamper-proof IDs and libp2p swarm sync. | No complex NIP protocols, no paid relay operators. Standard HTTP REST + SSE. |
| **0x0.st / Pastebin** | Content-addressed ephemeral binary file drops (`POST /events` with a `blob` part, `GET /blob/<sha256>`). | Deduplicated by SHA-256. Reference-driven retention pins blobs while any signed event links them, then auto-evicts orphans cleanly. |
| **Redis Pub/Sub** | Ephemeral JSON documents with self-expiring TTLs and real-time streaming. | Pure SQLite WAL storage with microsecond query latency and zero RAM bloat. |
| **S3 / Object Store** | Content-addressed blob storage with streaming downloads and checksum verification. | Zero IAM policies or bucket configuration. Upload once, verify everywhere. |

---

## Run in 10 seconds

Zero configuration. One command starts your node, joins the libp2p mesh, and starts syncing automatically:

```bash
docker run -d --name originless --restart unless-stopped \
  -p 3232:3232/tcp -p 3232:3232/udp -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

*(Works identically with `podman` by replacing `docker` with `podman`.)*

Or using **`compose.yml`**:

```yaml
services:
  originless:
    image: ghcr.io/besoeasy/originless:latest
    container_name: originless
    restart: unless-stopped
    ports:
      - "3232:3232/tcp"
      - "3232:3232/udp"
    volumes:
      - originless-data:/data

volumes:
  originless-data:
```

That's it! Your node is live at **http://localhost:3232**.

### Environment Variables

All environment variables are optional with zero-config defaults:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `NETWORK_ID` | `originless` | P2P swarm network ID. Nodes with matching IDs discover and sync with each other. Set to `off` (or `none`, `false`, `0`) to disable P2P sync entirely and run strictly offline. Set to a custom name (e.g. `my-project`) to create an isolated private swarm. |
| `BOOTSTRAP_PEERS` | *(built-in DHT)* | Comma-separated list of libp2p multiaddrs (e.g. `/ip4/1.2.3.4/tcp/3232/ws/p2p/<peer-id>`) to bootstrap DHT routing and connect to specific seed peers. |
| `ANNOUNCE_ADDRS` | *(auto-detected)* | Comma-separated list of multiaddrs to advertise to the DHT instead of auto-detected local IPs. Useful when running behind a reverse proxy, NAT port forward, or custom domain. |
| `SSE_MAX_SUBSCRIBERS` | `256` | Maximum concurrent Server-Sent Events subscribers (`GET /events/stream`) before returning HTTP 503. Set to `0` or negative for unlimited. |

### Mesh Network Modes

* **Public Mesh (Default)**: Zero configuration. By default, `NETWORK_ID` is `originless` — nodes automatically join the global libp2p Kad DHT via built-in bootstrap peers, rendezvous under `originless/originless`, punch holes across NATs, and relay through peers when needed.
* **Private Mesh**: Add `-e NETWORK_ID=my-project` and `-e BOOTSTRAP_PEERS=<multiaddrs>` so isolated swarms can find each other.
* **Standalone / Local Only**: Add `-e NETWORK_ID=off` (or `none`) to disable P2P sync and run strictly offline.

* **Dashboard**: Open **http://localhost:3232** in your browser.
* **AI Agents**: Point LLMs or agents at **[/agent.txt](static/agent.txt)** for the complete machine-readable contract.

---

## Automatic P2P Mesh (libp2p)

Originless is built for zero-config Docker and Podman deployments. With P2P active (`NETWORK_ID=originless` by default):

1. **libp2p WebSocket on TCP 3232**: The HTTP API and P2P share one published port. Docker `-p 3232:3232` is enough for outbound mesh join and inbound WS.
2. **QUIC on UDP 3232** (optional): Enables hole punching when you also publish UDP.
3. **Kad DHT Rendezvous**: Nodes automatically join the global Kad DHT via built-in public bootstrap peers and rendezvous under `originless/<NETWORK_ID>` with zero configuration. Private meshes (`NETWORK_ID != originless`) isolate under `/originless/kad/1.0.0` with custom bootstrap peers.
4. **AutoNAT, Identify, DCUtR**: Observed addresses replace UPnP. Direct connect first, then hole punch.
5. **Circuit relay v2**: Any publicly reachable Originless node may hop traffic for NATted Docker peers, with resource caps.
6. **mDNS**: Same-LAN / `--network host` discovery at local wire speed.
7. **Bloom Filter Set Reconciliation**: Upon connection, nodes exchange 1% false-positive Bloom filters to transfer missing records and binary blobs without redundant bandwidth.
8. **Persistent identity**: Peer ID is stored at `/data/p2p.key` so a volume keeps the same node across restarts.

Private meshes set `BOOTSTRAP_PEERS` to one or more libp2p multiaddrs (including `/p2p/<peer-id>`). Optional `ANNOUNCE_ADDRS` overrides advertised listen addresses behind a reverse proxy.

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
| `GET /status` | `GET` | Health, event/blob counts, active SSE clients, and libp2p mesh status |
| `POST /events` | `POST` | Publish signed event, or event + one content-addressed blob's bytes (multipart `event` + `blob` parts, event first) |
| `GET /events` | `GET` | Query events with filtering (`collection`, `label`, `owner`, `since`, `until`, `blob`, `cursor`) |
| `GET /events/{id}` | `GET` | Fetch event by ID (`?resolve=blob` to inline linked blob) |
| `GET /events/stream` | `GET` | Real-time Server-Sent Events stream |
| `GET /blob/{hash}` | `GET` | Download blob bytes by hash (`HEAD` supported) |
| `GET /blobs` | `GET` | List stored blobs |
| `GET /metrics` | `GET` | Prometheus telemetry metrics |
| `GET /agent.txt` | `GET` | Machine-readable contract for AI agents |
