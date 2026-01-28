# Originless

Private, decentralized file sharing for Nostr and the web.

One all-in-one storage backend you can drop into anything: your own apps, screenshot tools, pastebin-style pastes, Nostr clients, Reddit posts, forum embeds—anything that needs durable, anonymous file hosting. One Originless to rule them all and keep you anonymous.

<img width="2241" height="1608" alt="filedrop" src="https://github.com/user-attachments/assets/141cc0cf-9684-421d-8214-b1ed45e1e813" />

## Why Originless

- Anonymous uploads: no accounts, no tracking, no logs
- Resilient by design: served from IPFS; your node needn’t be online 24/7
- Nostr-optimized: you are your own media host (no domain or servers)
- Self-healing: content repopulates across IPFS when your node comes online


## Supported Platforms

Originless is integrated into the following platforms:

| Platform   | Description                       | Link                              |
| ---------- | --------------------------------- | --------------------------------- |
| **0xchat** | Private, decentralized Nostr chat | [0xchat.com](https://0xchat.com/) |

## Quick Start

[![Install on Umbrel](https://img.shields.io/badge/Umbrel-Install%20Now-5351FB?style=for-the-badge&logo=umbrel&logoColor=white)](https://apps.umbrel.com/app/originless)

**Minimal setup:**

```bash
docker run -d --restart unless-stopped \
  -p 3232:3232 \
  -p 4001:4001/tcp \
  -p 4001:4001/udp \
  -e STORAGE_MAX=200GB \
  ghcr.io/besoeasy/originless:main
```

Open http://localhost:3232 after starting.

For full Docker configuration options and Docker Compose setup, see [docker.md](docker.md).

## How It Works

1. Upload – Files stream to your local IPFS node (unpinned)
2. Propagate – Content spreads via IPFS as peers request it
3. Repopulate – If garbage collected, your node repopulates content when it comes online
4. Optional Nostr mode – Automatically fetches your notes, extracts IPFS CIDs, and pins them

## Nostr Mode (Optional)

- Supports multiple NPUBs (comma-separated)
- Fetches all posts from each configured NPUB (with pagination) and filters out expired notes
- Extracts IPFS CIDs and pins them locally (permanent)
- Caches media from people you follow for redundancy (ephemeral, garbage collected)
- Runs automatically every 3 hours; view status in the admin

## API Endpoints

### File Upload
Upload a file directly from your local system:

```bash
curl -X POST -F "file=@yourfile.pdf" http://localhost:3232/upload
```

**Response:**
```json
{
  "status": "success",
  "cid": "QmX...",
  "url": "https://dweb.link/ipfs/QmX...?filename=yourfile.pdf",
  "size": 12345,
  "type": "application/pdf",
  "filename": "yourfile.pdf"
}
```

### Remote URL Upload
Download and upload content from any URL to IPFS:

```bash
curl -X POST http://localhost:3232/remoteupload \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/image.png"}'
```

**Response:**
```json
{
  "status": "success",
  "cid": "QmX...",
  "url": "https://dweb.link/ipfs/QmX...",
  "filename": "image.png",
  "size": 12345,
  "type": "image/png",
  "sourceUrl": "https://example.com/image.png",
  "timing": {
    "download_ms": 1234,
    "upload_ms": 5678,
    "total_ms": 6912
  },
  "timestamp": "2026-01-07T03:18:00.000Z"
}
```

