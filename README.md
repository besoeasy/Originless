

<div align="center">

<img width="1542" height="1171" alt="image" src="https://github.com/user-attachments/assets/7f474225-2120-469a-92b2-8692dcaaa0d0" />


# Originless

**Your all-in-one storage backend** — upload, pin, and serve files over IPFS.  
No accounts. No API keys. One Docker container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![API](https://img.shields.io/badge/HTTP%20API-api.md-1f6feb)](api.md)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/licenses/ISC)
[![Available on Umbrel App Store](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## What it is

Originless is a multipurpose storage backend for apps, agents, pastebins, screenshots, and static sites.

- **Put** a file or folder → get a CID  
- **Get** it back at `/ipfs/{cid}` (same host as the API)  
- **Share** as `ipfs://…` — content-addressed, verifiable, no lock-in  

One port for humans and machines: **`3232`**.

---

## Run

```bash
docker compose up -d --build
```

Or with Podman:

```bash
podman compose up -d --build
```

Or standalone:

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
| **3232** | Dashboard, API, and `/ipfs/{cid}` (everything HTTP) |
| **4001** TCP+UDP | IPFS swarm — other nodes Bitswap your pins |

Open **http://localhost:3232** · Tools at **/examples/** · Full API in **[api.md](api.md)**

---

## Use it

```bash
# upload
curl -X POST -F "file=@photo.png" http://localhost:3232/upload

# photos without EXIF/GPS
curl -X POST -F "file=@photo.jpg" http://localhost:3232/media

# fetch (swarm retrieve is built in)
curl -O "http://localhost:3232/ipfs/$CID"
```

```json
{ "status": "success", "cid": "QmX...", "size": 1048576, "type": "image/png", "filename": "photo.png" }
```

Same bytes as `ipfs://QmX...`. Good for:

| You need… | Originless does… |
| :-------- | :--------------- |
| Media / attachments | `POST /media` or `/upload` |
| Static site / DApp `dist/` | `POST /uploadfolder` |
| Agent / script output | One `curl` — no auth |
| Paste / snippet hosting | Tools → snippet uploader |

> For dedicated Nostr media backup and mirroring, see [nostr-backup](https://github.com/besoeasy/nostr-backup).

---

## How it fits together

```
App / Agent / Browser
        │  POST /upload  ·  GET /ipfs/{cid}
        ▼
   Originless (:3232)
        │  pin + serve
        ▼
   IPFS swarm (:4001)
```

---

## Config (common)

| Variable | Default | Notes |
| :------- | :------ | :---- |
| `STORAGE_MAX` | `100GB` | Cap for IPFS data |
| `PIN_EXPIRY_DAYS` | `30` | Janitor may evict after this threshold |
| `ENABLE_GATEWAY` | `true` | `/ipfs` on **3232**. Set `false` to pin-only |
| `GATEWAY_NO_FETCH` | off | Set `true` for local-pins-only (no swarm fetch) |
| `IPFS_PROFILE` | `lowpower` | Umbrel/home-friendly Kubo init |
| `SWARM_ANNOUNCE` | | Public multiaddrs if **4001** is behind NAT |

More env vars and every route: **[api.md](api.md)**.

---

## License

**ISC** — free for personal and commercial use.
