<div align="center">

<img width="1542" height="1171" alt="Originless" src="https://github.com/user-attachments/assets/7f474225-2120-469a-92b2-8692dcaaa0d0" />

# Originless

**The all-in-one backend for the open web.**  
Records, binary blobs, and decentralized files — client-controlled, operator-safe, and zero-auth.  
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

## The 3 Primitives That Power Everything

```
                                  ORIGINLESS
                    (All-in-One Zero-Auth Open Backend)
                                     │
         ┌───────────────────────────┼───────────────────────────┐
         │                           │                           │
         ▼                           ▼                           ▼
  Quick Records               Binary Blobs                IPFS Swarm
  (/records)                  (/up, /down)                (/upload)
  ─────────────────────       ─────────────────────       ─────────────────────
  • Multiplayer Games         • Game Save Buffers         • Encrypted Backups
  • Ephemeral Chat Rooms      • Serialized State          • Static Sites / DApps
  • Push Notifications        • Raw Binary Caches         • Media Attachments
  • Web3 & Crypto Profiles    • Fast SHA-256 Dedupe       • Global P2P Bitswap
  • AI Agent Memory           • LRU Auto-Eviction         • Pinned on Port 4001
```

---

### 1. ⚡ Quick Records (`/records`) — The Universal State Engine

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

# Fetch record by its server-computed hash ID
curl "http://localhost:3232/records/8f2c...1a"
```

---

### 2. 💾 Content-Addressed Blobs (`/up`, `/down`) — Fast Binary Store

Direct binary blob storage without IPFS DAG chunking overhead. Ideal for game saves, binary caches, serialized buffers, and client-encrypted payloads:

- **SHA-256 Addressing**: Stored strictly as `<sha256>.bin` under `/data/blobs`.
- **Deduplication**: Uploading identical bytes calculates the same hash and touches recency without wasting disk space.
- **7-Day Retention Guarantee**: New blobs are protected from eviction for at least 7 days.
- **Automated LRU Eviction**: Pruned least-recently-used only when total storage exceeds quota (`STORAGE_MAX`).
- **Immutable Caching**: Served with `ETag` and `Cache-Control: public, max-age=86400, immutable`.

```bash
# Upload a binary blob (returns SHA-256 hash)
curl -X POST -F "file=@savegame.bin" http://localhost:3232/up

# Fetch it back directly by hash
curl -O "http://localhost:3232/down/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
```

---

### 3. 🌐 Decentralized IPFS Swarm (`/upload`, `/uploadfolder`) — Global Distribution

Pin files or full directory trees with content-addressed multihashes (`ipfs://bafy...`):

- **Zero Gateway Liability**: Originless pins content to the local Kubo datastore and advertises blocks over the libp2p swarm on port `4001`.
- **P2P Swarm Bitswap**: Other nodes retrieve pinned blocks directly via Bitswap.
- **Fetching over HTTP**: Handled safely via [**Rainbow**](https://github.com/ipfs/rainbow) (with built-in `badbits` denylists and origin sandboxing) or public gateways (`inbrowser.link`, `ipfs.io`).

```bash
# Pin an encrypted payload or asset (returns CID)
curl -X POST -F "file=@backup.enc" http://localhost:3232/upload

# Pin an entire static site or DApp dist folder under one root CID
curl -X POST \
  -F "file=@dist/index.html;filename=index.html" \
  -F "file=@dist/app.js;filename=assets/app.js" \
  http://localhost:3232/uploadfolder
```

---

## What Originless Replaces

Instead of juggling 7 different SaaS subscriptions, API keys, and specialized daemons, Originless consolidates your stack into **one lightweight container**:

| Traditional Service | What it's used for | Originless Replacement | Advantage |
| :------------------ | :----------------- | :--------------------- | :-------- |
| **[0x0.st](https://0x0.st/) / [file.io](https://www.file.io/)** | Ephemeral CLI file & script sharing | `POST /up` & `POST /upload` | Unlimited self-hosted storage, SHA-256 dedupe, automated LRU pruning |
| **[ntfy.sh](https://ntfy.sh/) / Pushover** | Push alerts, webhooks, event feeds | `POST /records` & `GET /records` | Cryptographically signed (Ed25519), label-indexed, auto-expiring TTL |
| **[Pastebin](https://pastebin.com/) / [PrivateBin](https://privatebin.info/)** | Code snippets, logs, text pastes | `POST /upload` + client templates | Content-addressed CIDs, client-side encryption, tamper-proof |
| **[Pinata](https://www.pinata.cloud/) / [Web3.Storage](https://web3.storage/)** | IPFS pinning & decentralized storage | Native Kubo daemon + Janitor | Zero subscription tiers, no credit card, auto-GC & disk quotas |
| **[Firebase Realtime DB](https://firebase.google.com/)** | Game saves, player state, chat rooms | `POST /records` (Signed JSON) | Keypair auth matches crypto wallets, zero server database management |
| **[Vercel](https://vercel.com/) / [Cloudflare Pages](https://pages.cloudflare.com/)** | Static web apps & DApp deployment | `POST /uploadfolder` | One `curl` command pins full `dist/` directory tree under root CID |
| **[Imgur](https://imgur.com/) / Media Hosts** | Media uploads & social attachments | `POST /upload` | Client-encrypted, Nostr NIP-92 `imeta` ready, zero DMCA operator liability |

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

### 2. ntfy.sh / Pushover &rarr; Signed Event & Alert Feeds (`/records`)
Broadcast notifications, IoT telemetry, or inter-process events with cryptographic signatures:
```bash
curl -X POST http://localhost:3232/records -H "Content-Type: application/json" -d '{
  "owner": "ed25519:<pubkey>", "collection": "alerts",
  "created_at": 1758420000, "expires_at": 1758423600,
  "data": { "title": "Backup Finished", "status": "ok" },
  "labels": ["topic:backups", "level:info"], "sig": "..."
}'

# Subscribers poll or fetch:
curl "http://localhost:3232/records?collection=alerts&label=topic:backups&limit=5"
```
Nobody can spoof notifications because every message is signed by the publisher's Ed25519 private key.

### 3. Pastebin / PrivateBin &rarr; Content-Addressed Snippets (`/upload`)
Paste code, configs, or encrypted secrets. The resulting IPFS CID is immutable — unlike paste sites where content can be altered or removed by third-party admins.

### 4. Pinata / Web3.Storage &rarr; Built-in IPFS Ingestion Node
Never pay a monthly subscription or worry about an IPFS provider shutting down or hiking API pricing. Originless runs an optimized low-power Kubo node, broadcasts blocks across the libp2p swarm (`4001`), and cleans up old unpinned files automatically.

### 5. Firebase / Supabase &rarr; Zero-DB State Engine (`/records`)
Build games, decentralized chats, and web rooms without spinning up PostgreSQL or managing user authentication tables:
- User's public key **is** their identity.
- Signed state updates verify client authenticity.
- Built-in TTL automatically cleans up expired rooms and sessions.

### 6. Vercel / Cloudflare Pages &rarr; Static DApp & Site Deployment (`/uploadfolder`)
Deploy frontends, documentation, or client-side DApps directly from CI/CD with a single curl request:
```bash
curl -X POST \
  -F "file=@dist/index.html;filename=index.html" \
  -F "file=@dist/app.js;filename=app.js" \
  http://localhost:3232/uploadfolder
```
The output is a root CID that can be opened anywhere through decentralized gateways.

### 7. Imgur / Cloudinary &rarr; Client-Encrypted Media Host (`/upload`)
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
  -e STORAGE_MAX=100GB \
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
| **Games & Save States** | Quick Records / Blobs | `POST /records` or `/up` | Ed25519 identity, instant state query, `.bin` saves |
| **Chat Apps & Web Rooms** | Quick Records | `POST /records` & `GET /records` | Ephemeral rooms, label filtering, auto-expiring messages |
| **Push Notifications & Signals** | Quick Records | `POST /records` | Timestamped event feeds, subscriber polling |
| **Crypto & Web3 DApps** | Quick Records | `POST /records` | Keypair auth matches crypto wallets, no server DB |
| **Encrypted File Sharing** | IPFS Pinning | `POST /upload` | Client encrypts, server pins, zero operator liability |
| **Static Sites & DApps** | Folder Pinning | `POST /uploadfolder` | Root CID preserves directory paths |
| **AI Agents & Bots** | Direct API | Any endpoint | Single `curl` — no auth tokens or API key provisioning |
| **Web Snippets & Tools** | Client Templates | [`examples/`](examples/) | Standalone client templates ready to open or pin |

---

## Config (common)

| Variable | Default | Notes |
| :------- | :------ | :---- |
| `STORAGE_MAX` | `100GB` | Shared quota for IPFS datastore and `.bin` blobs |
| `PIN_EXPIRY_DAYS` | `30` | Janitor may evict IPFS pins after this threshold |
| `BLOB_DIR` | `/data/blobs` | On-disk storage path for `.bin` blobs |
| `IPFS_PROFILE` | `lowpower` | Umbrel/home-friendly Kubo initialization |
| `SWARM_ANNOUNCE` | | Public multiaddrs if **4001** is behind NAT |

More env vars and every route: **[api.md](api.md)**.

---

## License

**ISC** — free for personal and commercial use.
