<div align="center">

<img width="1542" height="1171" alt="Originless" src="https://github.com/user-attachments/assets/7f474225-2120-469a-92b2-8692dcaaa0d0" />

# Originless

**The all-in-one backend for the open web.**  
Records, binary blobs, real-time SSE streams, and decentralized files — client-controlled, operator-safe, and zero-auth.  
No accounts. No API keys. No copyright strikes. One Docker container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![API](https://img.shields.io/badge/HTTP%20API-api.md-1f6feb)](api.md)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/licenses/ISC)
[![Available on Umbrel App Store](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## Why Originless? (Built for the Open Internet)

Traditional backends force you to become a babysitter: managing user tables, hashing passwords, rotating API tokens, and dreading DMCA takedowns, copyright strikes, or registrar domain freezes when users or agents upload data.

**Originless flips the model:**

- 🛡️ **Zero Legal Troubles for Operators**: Built for the client-encrypted web. Clients encrypt their files, saves, and messages before uploading and decrypt after fetching. The node operator only holds opaque bytes and cryptographic hashes — completely insulated from copyright liability and content takedowns.
- 🚫 **No Domain Drawdowns**: Originless separates ingestion and storage from public web rendering. By not hosting an open cleartext HTTP gateway on your domain, abusers cannot hijack your domain to serve phishing, malware, or illicit media. Content is distributed peer-to-peer over the libp2p swarm (`4001`) and fetched via dedicated gateways like [**Rainbow**](https://github.com/ipfs/rainbow) or public gateways.
- ⚡ **Zero Auth Overhead**: No account signups, no passwords, no JWT servers. Identity is cryptographic (**Ed25519**) — clients prove ownership with their private key on every write.
- 🌐 **One Unified Port**: One port for humans and machines: **`3232`**.

---

## The 2 Primitives That Power Everything

```
                        ORIGINLESS
          (All-in-One Zero-Auth Open Backend)
                           │
            ┌──────────────┴──────────────┐
            │                             │
            ▼                             ▼
     Quick Records                 Binary Blobs
     (/records, /records/stream)   (/up, /down)
     ─────────────────────         ─────────────────────
     • Multiplayer Games           • Game Save Buffers
     • Ephemeral Chat Rooms        • Serialized State
     • Real-Time SSE Streams       • Raw Binary Caches
     • Push Notifications          • Fast SHA-256 Dedupe
     • Web3 & Crypto Profiles      • 30d–1y Size-Weighted Retention
     • AI Agent Memory             • LRU Auto-Eviction
```

---

### 1. ⚡ Quick Records (`/records`, `/records/stream`) — The Universal State Engine

A fast, cryptographic document store for structured JSON data. Replaces traditional user tables and database servers across:

- 🎮 **Games**: Save slots, player levels, leaderboards, inventory state, character gear.
- 💬 **Chat & Web Rooms**: Ephemeral channel feeds, multiplayer lobby rooms, presence pings.
- 🔔 **Notifications & Signals**: Event feeds, device alerts, agent webhooks, IoT state logs.
- 🪙 **Crypto & Web3**: Wallet-linked metadata, decentralized identity profiles, transaction memos.
- 🤖 **AI Agents**: Long-term memory, inter-agent message buses, task queues.

**How it works**:
- **Identity via Ed25519**: `owner: "ed25519:<64-hex-pubkey>"`. No passwords or accounts.
- **Tamper-Proof IDs**: Server-computed deterministic hash `sha256(owner:collection:created_at:expires_at:canonical(data):labels)`. Clients cannot spoof or tamper with IDs.
- **Mandatory Expiration (TTL)**: `expires_at` is required (minutes to 10 years). Expired state naturally fades away; queries auto-hide expired records.
- **Instant Search & Labels**: Filter by collection, indexed tags (up to 10 labels), time range, or substring search inside the data payload.
- **Real-Time SSE Streaming**: Connect to `GET /records/stream` to receive incoming matching records as Server-Sent Events with zero polling.

```bash
# Store game progress or a room message (Ed25519 signed, 8 KB cap)
curl -X POST http://localhost:3232/records \
  -H "Content-Type: application/json" \
  -d '{
    "owner": "ed25519:3b6a...29",
    "collection": "gamesaves",
    "created_at": 1758420000,
    "expires_at": 1790040000,
    "data": { "slot": 1, "level": 42, "xp": 9820, "items": ["sword_of_light"] },
    "labels": ["slot:1", "player:alice"],
    "sig": "a3f1...c9"
  }'

# Query state (newest-first, filtered by collection & label)
curl "http://localhost:3232/records?collection=gamesaves&label=slot:1&limit=1"

# Stream matching records in real-time (Server-Sent Events)
curl -N "http://localhost:3232/records/stream?collection=gamesaves&label=slot:1"

# Fetch record by its server-computed hash ID
curl "http://localhost:3232/records/8f2c...1a"
```

```javascript
// Browser or Node.js real-time event subscriber (zero polling)
const stream = new EventSource("http://localhost:3232/records/stream?collection=gamesaves&label=slot:1");
stream.onmessage = (event) => {
  const record = JSON.parse(event.data);
  console.log("Live save update from", record.owner, record.data);
};
```

---

### 2. 💾 Content-Addressed Blobs (`/up`, `/down`) — Fast Binary Store

Direct binary blob storage without IPFS DAG chunking overhead. Ideal for game saves, binary caches, serialized buffers, and client-encrypted payloads:

- **SHA-256 Addressing**: Stored strictly as `<sha256>.bin` under `/data/blobs`.
- **Deduplication**: Uploading identical bytes calculates the same hash and touches recency without wasting disk space.
- **Size-Weighted Retention**: New blobs are protected from eviction for `30 days` at `512 MiB` up to `1 year` at size `0`, following `retention = min_age + (min_age - max_age) * (size/max_size - 1)^3`.
- **Automated LRU Eviction**: Pruned least-recently-used only when total storage exceeds quota (`STORAGE_MAX`).
- **Record Linkage**: Records attach a blob via `"_blob": "<sha256>"` in `data` — validated on publish, exempt from eviction while the record lives, resolvable via `GET /records/{id}?resolve=blob`.
- **Immutable Caching**: Served with `ETag` and `Cache-Control: public, max-age=86400, immutable`.

```bash
# Upload a binary blob (returns SHA-256 hash)
curl -X POST -F "file=@savegame.bin" http://localhost:3232/up

# Fetch it back directly by hash
curl -O "http://localhost:3232/down/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
```

---

## What Originless Replaces

Instead of juggling a dozen different SaaS subscriptions, cloud accounts, API keys, and specialized daemons, Originless consolidates your stack into **one lightweight container**:

| Category | Traditional Services | Originless Replacement | Key Advantage |
| :------- | :------------------- | :--------------------- | :------------ |
| **CLI File Drop** | **[0x0.st](https://0x0.st/)**, **[file.io](https://www.file.io/)** | `POST /up` & `POST /upload` | Unlimited self-hosted storage, SHA-256 deduplication, automated LRU pruning |
| **Push Alerts & Signals** | **[ntfy.sh](https://ntfy.sh/)**, **Pushover** | `POST /records` & `GET /records/stream` | Cryptographically signed (Ed25519), label-indexed topics, real-time SSE push alerts |
| **Code & Text Pastes** | **[Pastebin](https://pastebin.com/)**, **[PrivateBin](https://privatebin.info/)**, **[GitHub Gist](https://gist.github.com/)** | `POST /upload` + client templates | Content-addressed CIDs, client-side encryption, tamper-proof, no account needed |
| **Expiring Transfers** | **[WeTransfer](https://wetransfer.com/)**, **[Wormhole](https://wormhole.app/)** | `POST /up` (size-weighted retention + LRU) | Direct content-addressed hash links, client-side encryptable, zero tracking or ads |
| **Object Storage & Buckets** | **[Amazon S3](https://aws.amazon.com/s3/)**, **[MinIO](https://min.io/)**, **Backblaze B2** | `POST /up` & `POST /upload` | Simple HTTP `POST`/`GET` without IAM policies, access keys, or bucket CORS headaches |
| **Ephemeral Key-Value & Cache** | **[Redis](https://redis.io/)**, **[Upstash](https://upstash.com/)**, **Memcached** | `POST /records` (TTL Expiration) | Zero memory bloat, automatic TTL expiration, keypair-authenticated writes |
| **App State & Game Saves** | **[Firebase Realtime DB](https://firebase.google.com/)**, **[Supabase](https://supabase.com/)** | `POST /records` (Signed JSON) | Keypair auth matches crypto wallets, zero server database management |
| **Lightweight Backends** | **[PocketBase](https://pocketbase.io/)**, **[Appwrite](https://appwrite.io/)** | `POST /records` (Signed Documents) | No server user tables, cryptographic identity (Ed25519), embedded SQLite with WAL |
| **Pub/Sub Event Bus** | **[Pusher](https://pusher.com/)**, **[Ably](https://ably.com/)** | `POST /records` & `GET /records/stream` | Native Server-Sent Events (SSE), cryptographic verification, zero socket fees |
| **IPFS Pinning Services** | **[Pinata](https://www.pinata.cloud/)**, **[Web3.Storage](https://web3.storage/)**, **Infura** | Native Kubo daemon + Janitor | Zero subscription tiers, no credit card, auto-GC & disk quotas |
| **Static DApp Deployment** | **[Vercel](https://vercel.com/)**, **[Cloudflare Pages](https://pages.cloudflare.com/)**, **Netlify** | `POST /uploadfolder` | One `curl` command pins full `dist/` directory tree under a content-addressed root CID |
| **Media Hosting** | **[Imgur](https://imgur.com/)**, **Cloudinary** | `POST /upload` | Client-encrypted, Nostr NIP-92 `imeta` ready, zero DMCA operator liability |

<details>
<summary><strong>View detailed breakdown for each replacement</strong></summary>

### 1. 0x0.st / file.io &rarr; Instant CLI Blobs (`/up`, `/down`)
Upload any log file, diagnostic tarball, or binary directly from your terminal:
```bash
# Upload and get immediate SHA-256 address
curl -X POST -F "file=@dump.bin" http://localhost:3232/up
# Response: {"status":"success","hash":"a3f1...","url":"http://localhost:3232/down/a3f1..."}
```
No rate limits, no third-party server seeing your uploads, and automated LRU cleanup when disk quota is reached.

### 2. ntfy.sh / Pushover &rarr; Signed Real-Time Alert Feeds (`/records`, `/records/stream`)
Broadcast notifications, IoT telemetry, or inter-process events with cryptographic signatures:
```bash
curl -X POST http://localhost:3232/records -H "Content-Type: application/json" -d '{
  "owner": "ed25519:<pubkey>", "collection": "alerts",
  "created_at": 1758420000, "expires_at": 1758423600,
  "data": { "title": "Backup Finished", "status": "ok" },
  "labels": ["topic:backups", "level:info"], "sig": "..."
}'

# Live push stream via SSE (zero polling):
curl -N "http://localhost:3232/records/stream?collection=alerts&label=topic:backups"

# Or query historical notifications:
curl "http://localhost:3232/records?collection=alerts&label=topic:backups&limit=5"
```
Nobody can spoof notifications because every message is signed by the publisher's Ed25519 private key.

### 3. Pastebin / PrivateBin / GitHub Gist &rarr; Content-Addressed Snippets (`/upload`)
Paste code, configs, or encrypted secrets. The resulting IPFS CID is immutable — unlike paste sites where content can be altered or removed by third-party admins.

### 4. WeTransfer / Wormhole &rarr; Expiring File Drops (`/up`, `/upload`)
Drop files for friends or colleagues without third-party email tracking, upload caps, or intrusive ad walls. Content carries a size-weighted retention guarantee (30 days at 512 MiB, up to 1 year for tiny files) before LRU eviction.

### 5. Amazon S3 / MinIO &rarr; Zero-Config Raw Object Storage (`/up`, `/down`)
Eliminate the complexity of configuring AWS IAM users, bucket policies, secret keys, and region endpoints. Storing an object is a single HTTP POST; retrieving it is a simple GET by SHA-256 hash.

### 6. Redis / Upstash &rarr; Persistent State with Automatic TTL (`/records`)
Use Originless as a lightweight key-value cache and session engine. Every record requires an expiration timestamp (`expires_at`), so outdated cache entries, user sessions, and room locks naturally fade away without memory bloat.

### 7. Firebase / Supabase &rarr; Zero-DB State Engine (`/records`)
Build games, decentralized chats, and web rooms without spinning up PostgreSQL or managing user authentication tables:
- User's public key **is** their identity.
- Signed state updates verify client authenticity.
- Built-in TTL automatically cleans up expired rooms and sessions.

### 8. PocketBase / Appwrite &rarr; Cryptographic Document Store (`/records`)
Rapidly prototype web and mobile apps without writing database schemas. Query by collection, label, or date with built-in SQLite WAL persistence and instant zero-auth writes.

### 9. Pusher / Ably &rarr; Real-Time SSE & Pub/Sub Bus (`/records/stream`)
Avoid costly per-message cloud fees and socket limits. Originless provides native Server-Sent Events (SSE):
- Broadcast messages: `POST /records` with labels like `labels: ["room:lobby"]`.
- Subscribe live in browser or node without extra client libraries:
```javascript
const feed = new EventSource("http://localhost:3232/records/stream?collection=chat&label=room:lobby");
feed.onmessage = (e) => console.log("Incoming event:", JSON.parse(e.data));
```

### 10. Pinata / Web3.Storage &rarr; Built-in IPFS Ingestion Node
Never pay a monthly subscription or worry about an IPFS provider shutting down or hiking API pricing. Originless runs an optimized low-power Kubo node, broadcasts blocks across the libp2p swarm (`4001`), and cleans up old unpinned files automatically.

### 11. Vercel / Cloudflare Pages &rarr; Static DApp & Site Deployment (`/uploadfolder`)
Deploy frontends, documentation, or client-side DApps directly from CI/CD with a single curl request:
```bash
curl -X POST \
  -F "file=@dist/index.html;filename=index.html" \
  -F "file=@dist/app.js;filename=app.js" \
  http://localhost:3232/uploadfolder
```
The output is a root CID that can be opened anywhere through decentralized gateways.

### 12. Imgur / Cloudinary &rarr; Client-Encrypted Media Host (`/upload`)
Upload pictures, audio, or video attachments without operator copyright or CSAM liabilities. Clients encrypt or strip metadata before uploading, preserving end-to-end privacy.

</details>

---

## Run

Standalone container:

```bash
podman run -d \
  --name originless \
  --restart unless-stopped \
  -p 3232:3232 \
  -p 4001:4001 \
  -p 4001:4001/udp \
  -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

| Port | Why |
| :--- | :--- |
| **3232** | Dashboard, REST API, Records, and Blobs |
| **4001** TCP+UDP | IPFS swarm — other nodes Bitswap your pins |

Open **http://localhost:3232** · Client Tools in **[`examples/`](examples/)** · Full API in **[api.md](api.md)**

---

## Capabilities at a Glance

| Application | Originless Tier | Route | Why it fits |
| :--- | :--- | :--- | :--- |
| **Real-Time Feeds & Alerts** | Quick Records | `GET /records/stream` | Instant Server-Sent Events push stream, zero polling, filter by collection & label |
| **Chat Apps & Web Rooms** | Quick Records | `POST /records` & `GET /records/stream` | Ephemeral rooms, label filtering, auto-expiring messages, live SSE sync |
| **Games & Save States** | Quick Records / Blobs | `POST /records` or `POST /up` | Ed25519 identity, instant state query, SHA-256 `.bin` save buffers |
| **Push Notifications & Signals** | Quick Records | `POST /records` & `GET /records/stream` | Timestamped event feeds, zero-polling live SSE subscriber streams |
| **Crypto & Web3 DApps** | Quick Records | `POST /records` | Keypair auth matches crypto wallets, no server DB or account signups |
| **Encrypted File Sharing** | IPFS Pinning | `POST /upload` | Client encrypts, server pins, zero operator liability |
| **Static Sites & DApps** | Folder Pinning | `POST /uploadfolder` | Single `curl` pins full `dist/` directory tree under one root CID |
| **Fast Binary Blobs** | Content-Addressed Blobs | `POST /up` & `GET /down/{hash}` | Direct SHA-256 blobs, size-weighted retention window, LRU auto-pruning |
| **AI Agents & Bots** | Direct API | Any endpoint | Single `curl` — no auth tokens or API key provisioning |
| **Web Snippets & Tools** | Client Templates | [`examples/`](examples/) | Standalone client templates ready to open or pin to IPFS |

---

### Decoupled Client Templates (`examples/`)

Originless is a headless, zero-auth backend binary. A suite of production-ready, zero-dependency client apps is provided under [`examples/`](examples/) — open them directly in any browser or pin them to IPFS:

- 📋 **[crypto-paste](examples/crypto-paste.html)**: Zero-knowledge client-encrypted pastebin (AES-GCM-256 key in URL hash).
- 📁 **[file-share](examples/file-share.html)**: Drag-and-drop file sharing with automatic IPFS pinning and gateway links.
- 💬 **[chat-room](examples/chat-room.html)**: Ephemeral decentralized chat rooms with real-time SSE streaming.
- 🎮 **[game-save](examples/game-save.html)**: Keypair-authenticated cloud game save manager using Ed25519 signatures.
- 🖼️ **[nip92-uploader](examples/nip92-uploader.html)**: Nostr NIP-92 media uploader with SHA-256 hashes and `imeta` tag generation.
- 🤖 **[agent-memory](examples/agent-memory.html)**: State inspector and key-value memory browser for autonomous AI agents.

---

## Config (common)

| Variable | Default | Notes |
| :------- | :------ | :---- |
| `STORAGE_MAX` | `100GB` | Shared quota for IPFS datastore and `.bin` blobs |
| `PIN_EXPIRY_DAYS` | `30` | Janitor may evict IPFS pins after this threshold |

More env vars and every route: **[api.md](api.md)**.

---

## License

**ISC** — free for personal and commercial use.
