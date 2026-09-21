<div align="center">

# Originless

**Zero-auth backend for the open web — signed records, binary blobs, live streams.**  
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

Open **http://localhost:3232** for the dashboard · Full API in **[api.md](api.md)**

---

## What it does

Two primitives, no auth:

- **Quick Records** (`POST /records`, `GET /records`, `GET /records/stream`) — Ed25519-signed JSON documents (8 KB max) with collections, labels, mandatory TTL, and a real-time SSE feed. Identity is a keypair; IDs are server-computed content hashes.
- **Binary Blobs** (`POST /up`, `GET /down/{hash}`, `GET /blobs`) — opaque `.bin` files stored as `<sha256>.bin`, deduplicated, with size-weighted retention (30 days at 512 MiB → 1 year near 0 bytes). Expired blobs are evicted periodically by the janitor. Non-binary uploads (HTML, images, PDFs, text) are rejected; downloads send `nosniff`.

Records link blobs via `"_blob": "<sha256>"` in `data` — validated on publish, exempt from eviction while the record lives, inlinable with `GET /records/{id}?resolve=blob`.

---

## Usage

```bash
# Publish a signed record (see api.md for the signing scheme)
curl -X POST http://localhost:3232/records \
  -H "Content-Type: application/json" \
  -d '{"owner":"ed25519:3b6a...29","collection":"chat","created_at":1758420000,"expires_at":1790040000,"data":{"room":"general","text":"gg"},"labels":["room:general"],"sig":"a3f1...c9"}'

# Query it back
curl "http://localhost:3232/records?collection=chat&label=room:general&limit=5"

# Stream it live (Server-Sent Events, zero polling)
curl -N "http://localhost:3232/records/stream?collection=chat&label=room:general"

# Store a binary blob, fetch it by hash
curl -X POST -F "file=@savegame.bin" http://localhost:3232/up
curl -O "http://localhost:3232/down/<sha256>"

# Health snapshot for monitoring / healthchecks
curl http://localhost:3232/status
```
