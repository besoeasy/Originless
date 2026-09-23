<div align="center">

# Originless

**The Universal Zero-Auth Backend for Modern Apps & Autonomous Agents.**  
Signed events. Content-addressed binary blobs. Native real-time streams. Autonomous P2P mesh.  
No accounts. No API keys. No database migrations. One single binary.

<br>

[![Available on Umbrel](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## 1. The Universal Backend: Why Originless?

### The Problem We Solve
Building modern connected applications today is unnecessarily painful. A simple collaborative app, mobile client, or AI agent tool typically requires:
* **An Auth Provider** (Auth0, Clerk, Cognito) to issue JWTs and manage user databases.
* **A Database** (PostgreSQL, MongoDB, Supabase) with schema migrations, connection pooling, and ORMs.
* **Object Storage** (AWS S3, Cloudflare R2) with bucket policies, signed upload URLs, and orphan file cleanup.
* **A Pub/Sub & WebSocket Broker** (Redis, Pusher, Socket.io) to push real-time updates and keep state synchronized.
* **A P2P or Federation Protocol** (Nostr, Matrix, libp2p) when attempting decentralized multi-node replication.

You end up managing 5 separate cloud services, juggling API keys and secrets, paying escalating per-MAU SaaS bills, and wrestling with complex vendor lock-in.

### The Originless Solution
Originless collapses your entire backend stack into **two universal primitives** delivered by a **single zero-dependency binary**:

1. **Signed JSON Events**: Ephemeral or durable structured data signed client-side with **Ed25519**. The client holds its own private key; the node verifies the signature mathematically. There are no accounts to create, no passwords to reset, and no API keys to leak.
2. **Content-Addressed Blobs**: Opaque binary files identified and deduplicated by **SHA-256**. Uploaded atomically with events, protected against abuse, and automatically garbage-collected when referencing events expire.

Every node exposes standard HTTP REST endpoints and native Server-Sent Events (`GET /events/stream`), backed by high-throughput SQLite WAL storage and an autonomous libp2p P2P swarm.

### Protocols & Cloud Services Replaced

| Layer | Traditional Stack | What Originless Does Instead | Why It Is Better |
| :--- | :--- | :--- | :--- |
| **Authentication & Identity** | Auth0, Clerk, Firebase Auth, Cognito | Client-side **Ed25519 asymmetric cryptography**. Identity is a cryptographic keypair generated in 2 lines of code. | Zero auth state on server. No passwords to leak, no JWT expiration races, no user tables to maintain or breach. |
| **Real-Time Pub/Sub** | Redis Pub/Sub, Pusher, Ably, Socket.io | Native HTTP **Server-Sent Events (`GET /events/stream`)** with collection and label filtering. | Works natively in every browser with `new EventSource()` and in terminal with `curl -N`. Zero client SDKs needed. |
| **Binary & File Storage** | AWS S3, Cloudflare R2, MinIO, 0x0.st | Content-addressed storage (**`/blob/{sha256}`**) with streaming byte transfers and ETag verification. | Atomic multipart upload with event; automatic orphan GC; built-in opaque content shield against XSS and hotlinking. |
| **Database & Persistence** | PostgreSQL, Firestore, PocketBase | Indexed **SQLite WAL Event Store** with full-text search, label indexes, and keyset pagination. | Microsecond queries, zero config, automatic TTL expiration, and crash-safe single-file persistence. |
| **P2P Swarm & Relays** | Nostr Relays, Matrix, BitTorrent | Embedded **libp2p mesh** with Kademlia DHT discovery, AutoNAT hole-punching, and $O(1)$ state vector root sync. | Run 1 node or 1,000 nodes worldwide. They discover each other and replicate state automatically on a single shared port. |

### Possibility Is What You Can Imagine

With Originless handling identity, persistence, binary storage, real-time push, and P2P distribution, you can build full-featured applications in a single static HTML file or a small script:

* 🎨 **Collaborative Real-Time Canvases & Whiteboards**: Infinite shared canvas or r/place style boards (see live demo on any node dashboard) where every brush stroke or pixel is an Ed25519-signed event pushed instantly over SSE.
* 💬 **Encrypted Chat & Social Networks**: End-to-end encrypted messaging, public community rooms, topic forums, or decentralized Twitter-like feeds without a central server operator.
* 🤖 **Autonomous AI Agent Swarms**: Blackboard coordination architectures where LLMs and agents post tasks, tail live event streams, claim jobs, and exchange binary model weights or artifacts without needing API keys.
* 📱 **Local-First & Multi-Device Sync**: Note-taking apps (Obsidian-style sync), personal wikis, todo managers, and password vaults that store data locally and sync silently across phones, laptops, and home servers.
* 🎮 **Turn-Based Multiplayer Games**: Chess, checkers, card games, and turn-based strategy where each move is a cryptographically signed event, guaranteeing cheat-proof move history and player provenance.
* 📡 **IoT & Edge Sensor Fleets**: Environmental sensors, home automation hubs, and edge cameras that log time-series telemetry events and image blobs with automatic 7-day or 30-day TTL expiry.
* 📦 **Decentralized Package & Firmware Mirrors**: Immutable distribution of binaries, WASM modules, container layers, and datasets verified purely by SHA-256 hash.

---

## 2. Quickstart

Run a full node with Docker (or Podman) in 10 seconds:

```bash
docker run -d --name originless --restart unless-stopped \
  -p 3232:3232/tcp -p 3232:3232/udp -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

Or with `compose.yml`:

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

Your node is now live at **http://localhost:3232**.

### Configuration

Configuration is deliberately minimal. Everything works out of the box with zero configuration:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `NETWORK_ID` | `originless` | P2P swarm identity. Nodes with matching IDs automatically discover each other over DHT. Set to `off` (or `none`) to run offline, or set to a custom string (e.g. `my-project`) for a private swarm. |
| `MAX_BLOB_BYTES` | `1GiB` (`1073741824`) | Maximum single blob upload cap. Accepts raw bytes or human units (e.g. `500MB`, `2GB`). Set `0` for unlimited. |
| `SSE_MAX_SUBSCRIBERS` | `256` | Maximum concurrent live stream subscribers (`GET /events/stream`). Set `0` for unlimited. |

* **Built-in Disk Guard**: 90% hard admission ceiling, 85% background janitor sweep, and 5-minute emergency eviction cooldown. Hardcoded to ensure bulletproof, predictable node stability without configuration fatigue.

---

## 3. The Core Primitives

### Events (Signed JSON Documents)
Events are immutable, cryptographically signed JSON documents (≤ 8 KB, TTL ≤ 1 year). The client holds the Ed25519 private key; the node verifies the signature and validates TTL:

```json
{
  "owner": "ed25519:<64 hex public key>",
  "collection": "chat",
  "created_at": 1758420000,
  "expires_at": 1789956000,
  "data": { "user": "alice", "message": "Hello world!" },
  "blob": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "labels": ["room:lobby"],
  "sig": "<128 hex chars>"
}
```
*Signature covers:* `owner:collection:created_at:expires_at:canonical(data):blob:labels`.

### Blobs (Content-Addressed Binary Storage)
Upload raw binary bytes up to `MAX_BLOB_BYTES` (default 1 GiB). Originless re-hashes bytes and stores them by SHA-256:
* **Atomic Multipart Upload**: Publish a signed event and its binary attachment in a single `multipart/form-data` request (`event` part + `blob` part).
* **Reference-Driven Retention**: A blob survives while at least one unexpired event references it. Once all referencing events expire, the blob is cleanly evicted as an orphan.
* **Opaque Content Shield**: Only raw binary data is accepted (sniffed `text/*`, `image/*`, and `application/pdf` are rejected). All blobs download with `application/octet-stream` and `nosniff`, preventing hotlinking, XSS, and media CDN abuse.

### API Summary

| Endpoint | Method | Purpose |
| :--- | :--- | :--- |
| `GET /status` | `GET` | Node health, connected peers, storage vitals, and sync telemetry |
| `POST /events` | `POST` | Publish a signed event, or event + blob atomically via multipart |
| `GET /events` | `GET` | Query records (`?collection=&label=&owner=&since=&until=&search=&blob=&cursor=`) |
| `GET /events/{id}` | `GET` | Retrieve single event (`?resolve=blob` inlines linked blob metadata) |
| `GET /events/stream` | `GET` | Real-time Server-Sent Events stream (`?collection=&label=`) |
| `GET /blob/{hash}` | `GET` | Stream raw binary blob by SHA-256 (`HEAD`, ranges, and `ETag` supported) |
| `GET /blobs` | `GET` | List stored blobs sorted newest first |
| `GET /metrics` | `GET` | Prometheus telemetry metrics |
| `GET /agent.txt` | `GET` | Machine-readable API contract for AI agents and LLMs |

---

## 4. Automatic P2P Mesh

Originless includes an autonomous, zero-configuration P2P mesh powered by **libp2p**:

* **Single-Port Multiplexing**: Both the HTTP REST API and libp2p WebSocket transport share port `3232/tcp`.
* **Zero-Config Discovery**: Local nodes discover each other instantly via **mDNS**. Internet nodes rendezvous over the **Kademlia DHT** under `originless/<NETWORK_ID>`.
* **NAT Traversal & Relays**: Employs **AutoNAT**, **DCUtR hole punching**, and built-in **Circuit Relay v2** to connect nodes behind strict or symmetric firewalls.
* **$O(1)$ State Root Sync**: Nodes exchange a commutative multiset XOR state root. If roots match, sync is a 120-byte no-op. If roots differ, the engine triggers continuous salted Bloom filter reconciliation.
* **Zero-Trust Swarm Ingestion**: Every event and blob received over the swarm is fully cryptographically re-verified. P2P writes are bounded by admission control without triggering local eviction.

---

## 5. Documentation & Integration Guides

Complete copy-pasteable guides and reference implementations are available in the [`docs/`](docs/) directory:

### [cURL & Shell Guide](docs/curl.md)
* [Node Health & Prometheus Telemetry](docs/curl.md#1-node-health--telemetry) — Inspecting `/status` and `/metrics` with `curl` and `jq`.
* [Generating Ed25519 Keypairs](docs/curl.md#step-1-generate-an-ed25519-keypair-once) — One-line OpenSSL key generation and public key hex extraction.
* [Canonical Signing & Event Publishing](docs/curl.md#step-2-sign-and-publish-an-event) — Canonical JSON sorting, SHA-256 digest creation, Ed25519 signing, and `POST /events`.
* [Atomic Multipart Blob Uploads](docs/curl.md#3-uploading-binary-blobs--events-atomic-multipart) — Uploading an event together with raw binary bytes in one request.
* [Downloading & Verifying Blobs](docs/curl.md#4-downloading-blobs) — Content-addressed downloads, ETag verification, and `/blobs` listing.
* [Querying, Filtering & Keyset Pagination](docs/curl.md#5-querying-events) — Filtering by collection, labels, owner, full-text search, and keyset cursor paging.
* [Live Event Streaming](docs/curl.md#6-real-time-streaming-server-sent-events) — Tailing live feeds with `curl -N /events/stream`.

### [Node.js & JavaScript Guide](docs/nodejs.md)
* [Zero-Dependency Helper (`originless.mjs`)](docs/nodejs.md#1-zero-dependency-signing-helper-originlessmjs) — Pure standard library signing utility using `node:crypto` and native `fetch`.
* [Event Generation & Publishing](docs/nodejs.md#2-publishing-an-event) — Creating, canonicalizing, signing, and posting events in Node.js v18+.
* [Multipart Event + Blob Uploads](docs/nodejs.md#3-uploading-an-event-with-attached-binary-blob-atomic-multipart) — Streaming files with native `FormData` and `Blob`.
* [Querying Events & Resolving Blobs](docs/nodejs.md#4-querying-events--downloading-blobs) — Querying records, inlining metadata with `?resolve=blob`, and downloading buffers.
* [Real-Time Streaming in Node.js & Web Browsers](docs/nodejs.md#5-real-time-streaming-server-sent-events) — Processing SSE streams in Node.js and in front-end browser `EventSource`.

### [Python Guide](docs/python.md)
* [Prerequisites & Key Generation](docs/python.md#prerequisites) — Setup with `cryptography` and `requests`.
* [Ed25519 Helper Functions](docs/python.md#1-key-generation--event-signing) — Clean, reusable `generate_keypair()` and `sign_event()` routines.
* [Publishing JSON Events](docs/python.md#2-publishing-an-event) — Posting structured event payloads.
* [Multipart Event + Binary File Uploads](docs/python.md#3-uploading-an-event-with-attached-binary-blob-atomic-multipart) — Packaging multipart payloads with correct part ordering.
* [Querying & Downloading](docs/python.md#4-querying-events--downloading-blobs) — Querying with parameters, inspecting attached blobs, and binary file downloads.
* [Streaming Events with Chunked Decoding](docs/python.md#5-streaming-live-events-server-sent-events) — Reading live SSE streams using `requests(stream=True)`.

### [AI Agent Specification (`/agent.txt`)](static/agent.txt)
* Plaintext system contract served at `/agent.txt` describing cryptographic signing rules, endpoint specifications, and JSON schemas for LLMs and autonomous coding assistants (Claude, Cursor, Copilot, Antigravity).

### [Live Web Applications](https://originless.besoeasy.com/)
* Interactive single-file web applications built on Originless: Room Chat, 2-Player Board Game, Collaborative Pixel Canvas, Encrypted Pastebin, and Soundboard.
