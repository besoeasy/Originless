<div align="center">

# Originless

**Zero-auth backend for the open web — signed events, binary blobs, live P2P sync.**  
No accounts. No API keys. Zero complex setup. One single binary.

<br>

[![Available on Umbrel](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## 1. Overview

Originless replaces sprawling backend microservices with **two simple primitives** running inside a single zero-dependency container:

1. **Signed JSON Events**: Ephemeral or durable structured data signed client-side with **Ed25519**.
2. **Content-Addressed Blobs**: Opaque binary files identified and deduplicated by **SHA-256**.

| Service Replaced | What Originless Does Instead | Key Advantage |
| :--- | :--- | :--- |
| **ntfy / Pusher** | Real-time pub/sub streams over Server-Sent Events (`GET /events/stream`). | Native browser `EventSource` and `curl -N`. No daemon required. |
| **Sentry / Logs** | Signed telemetry and error ingestion (`POST /events`) with automatic TTL expiry. | Zero auth tokens to manage or rotate. Filter by label or tail live. |
| **Nostr Relays** | Cryptographically verified Ed25519 events with libp2p swarm replication. | Standard HTTP REST + SSE; no custom relay protocols. |
| **0x0.st / S3** | Content-addressed binary blob storage (`/blob/{sha256}`) with streaming downloads. | Blobs are tied to signed events; automatic orphan garbage collection. |
| **Redis Pub/Sub** | Real-time streaming backed by high-performance SQLite WAL storage. | Microsecond queries, crash-safe persistence, zero memory bloat. |

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
| `SSE_MAX_SUBSCRIBERS` | `256` | Maximum concurrent live stream subscribers (`GET /events/stream`). Set `0` for unlimited. |

* **Built-in Disk Guard**: 90% hard admission ceiling, 85% background janitor sweep, 1 GiB max single blob, and 5-minute emergency eviction cooldown. Hardcoded to ensure bulletproof, predictable node stability without configuration fatigue.

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
Upload raw binary bytes up to 1 GiB. Originless re-hashes bytes and stores them by SHA-256:
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
