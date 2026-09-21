

<div align="center">

<img width="1542" height="1171" alt="image" src="https://github.com/user-attachments/assets/7f474225-2120-469a-92b2-8692dcaaa0d0" />


# Originless

**Your all-in-one data & storage backend** — structured records, binary blobs, and IPFS files.  
No accounts. No API keys. One Docker container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![API](https://img.shields.io/badge/HTTP%20API-api.md-1f6feb)](api.md)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/licenses/ISC)
[![Available on Umbrel App Store](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## What it is

Originless is a zero-friction, multi-tier data and storage backend for apps, AI agents, local-first software, and static sites. Instead of managing a relational database, an S3 object store, an authentication service, and an IPFS pinner, Originless unifies them into three simple tiers:

- 🗂️ **Signed Records (`/records`)** — Ed25519-authenticated JSON documents for app state, chat, profiles, and game saves. Users and agents bring their own keys; no user tables or token servers needed.
- ⚡ **Content-Addressed Blobs (`/up`, `/down`)** — Fast, direct SHA-256 `.bin` storage for raw binary state, caches, and buffers with automatic deduplication, 7-day minimum retention guarantee, and LRU eviction.
- 🌐 **Decentralized Files (`/upload`, `/ipfs`)** — IPFS file and folder pinning, automatic EXIF/GPS privacy stripping, and built-in HTTP gateway with swarm Bitswap on port 4001.

One port for humans and machines: **`3232`**.

---

## Run

standalone:

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
| **3232** | Dashboard, API, Records, Blobs, and `/ipfs/{cid}` gateway (everything HTTP) |
| **4001** TCP+UDP | IPFS swarm — other nodes Bitswap your pins |

Open **http://localhost:3232** · Tools at **/examples/** · Full API in **[api.md](api.md)**

---

## Use it

### 1. App State & Records (Ed25519-signed JSON)

Store structured application state without user accounts or server credentials. Clients verify identity with an Ed25519 keypair:

```bash
# publish a record (chat, profile, save slot, config) — ID is server-computed
curl -X POST http://localhost:3232/records \
  -H "Content-Type: application/json" \
  -d '{"owner":"ed25519:3b6a...29","collection":"chat","created_at":1758420000,"expires_at":1790040000,"data":{"room":"general","text":"hello world"},"labels":["room:general"],"sig":"a3f1...c9"}'

# query records (newest-first, auto-hides expired, filtered by collection/label/search)
curl "http://localhost:3232/records?collection=chat&label=room:general&limit=20"

# fetch a single record by ID
curl "http://localhost:3232/records/8f2c...1a"
```

### 2. Fast Content-Addressed Blobs (SHA-256)

Store raw binary state without IPFS DAG chunking overhead:

```bash
# store a binary blob (returns SHA-256 hash, dedupes automatically)
curl -X POST -F "file=@save.bin" http://localhost:3232/up

# fetch it back directly by hash (includes immutable caching and LRU touch)
curl -O "http://localhost:3232/down/$HASH"
```

### 3. IPFS Files & Media (P2P Swarm)

Pin files, directory trees, or privacy-cleaned photos with global content addressing:

```bash
# upload any file
curl -X POST -F "file=@document.pdf" http://localhost:3232/upload

# photos with EXIF/GPS/XMP stripped before pinning
curl -X POST -F "file=@photo.jpg" http://localhost:3232/media

# upload an entire directory tree under a root CID
curl -X POST -F "file=@dist/index.html;filename=index.html" -F "file=@dist/app.js;filename=assets/app.js" http://localhost:3232/uploadfolder

# fetch via built-in gateway (swarm retrieve is built in)
curl -O "http://localhost:3232/ipfs/$CID"
```

---

## Capabilities at a glance

| You need… | Originless tier | Route | Notes |
| :-------- | :-------------- | :---- | :---- |
| **App state / profiles / chat** | Signed Records | `POST /records` & `GET /records` | Ed25519 authenticated, 8 KB cap, query & TTL |
| **Game saves / raw binary cache** | Binary Blobs | `POST /up` & `GET /down/{hash}` | Content-addressed SHA-256, LRU eviction |
| **Photos & media (privacy-first)** | Sanitized Media | `POST /media` | EXIF, GPS, and XMP stripped before pin |
| **General files & attachments** | IPFS File Pin | `POST /upload` | Returns CID, pinned and swarm-accessible |
| **Static site / DApp `dist/`** | IPFS Directory Pin | `POST /uploadfolder` | Preserves folder structure under one root CID |
| **Agent / script output** | Zero-Auth API | Any endpoint | Single `curl` — no auth or API keys |
| **Paste / snippet hosting** | Web Client Tools | `/examples/` | Browser UI built into the container |

> For dedicated Nostr media backup and mirroring, see [nostr-backup](https://github.com/besoeasy/nostr-backup).

---

## Config (common)

| Variable | Default | Notes |
| :------- | :------ | :---- |
| `STORAGE_MAX` | `100GB` | Shared quota for IPFS data and `.bin` blobs |
| `PIN_EXPIRY_DAYS` | `30` | Janitor may evict IPFS pins after this threshold |
| `BLOB_DIR` | `/data/blobs` | On-disk storage path for `.bin` blobs |
| `ENABLE_GATEWAY` | `true` | `/ipfs` and `/ipns` on **3232**. Set `false` for pin-only |
| `GATEWAY_NO_FETCH` | off | Set `true` for local-pins-only (no swarm fetch) |
| `IPFS_PROFILE` | `lowpower` | Umbrel/home-friendly Kubo init |
| `SWARM_ANNOUNCE` | | Public multiaddrs if **4001** is behind NAT |

More env vars and every route: **[api.md](api.md)**.

---

## License

**ISC** — free for personal and commercial use.
