# File Drop

Private, decentralized file sharing for Nostr and the web.

## Why File Drop

- Anonymous uploads: no accounts, no tracking, no logs
- Resilient by design: served from IPFS; your node needn’t be online 24/7
- Nostr-optimized: you are your own media host (no domain or servers)
- Self-healing: content repopulates across IPFS when your node comes online
- Friend mesh: optionally cache media from people you follow for redundancy (ephemeral)

## Supported Platforms

FileDrop is integrated into the following platforms:

| Platform | Description | Link |
|----------|-------------|------|
| **0xchat** | Private, decentralized Nostr chat | [0xchat.com](https://0xchat.com/) |

## Quick Start

[![Install on Umbrel](https://img.shields.io/badge/Umbrel-Install%20Now-5351FB?style=for-the-badge&logo=umbrel&logoColor=white)](https://apps.umbrel.com/app/file-drop)

```bash
docker run -d --restart unless-stopped \
  -p 3232:3232 \
  -p 4001:4001/tcp \
  -p 4001:4001/udp \
  -v file-drop-data:/data \
  -e STORAGE_MAX=200GB \
  -e FILE_LIMIT=5GB \
  -e NPUB=npub1yourkey... \
  -e PINFRIENDS=false \
  --stop-timeout 15 \
  --name file-drop \
  ghcr.io/besoeasy/file-drop:main
```

**Docker Compose:**
```yaml
services:
  file-drop:
    image: ghcr.io/besoeasy/file-drop:main
    container_name: file-drop
    restart: unless-stopped
    stop_grace_period: 15s
    ports:
      - "3232:3232"
      - "4001:4001/tcp"
      - "4001:4001/udp"
    volumes:
      - file-drop-data:/data
    environment:
      - STORAGE_MAX=200GB
      - FILE_LIMIT=5GB
      - NPUB=npub1yourkey...
      - PINFRIENDS=false

volumes:
  file-drop-data:
```

Open http://localhost:3232 after starting.

## Configuration

- `STORAGE_MAX` (default: 200GB) – IPFS storage cap before GC
- `FILE_LIMIT` (default: 5GB) – Max size per upload
- `NPUB` – Your Nostr pubkey (npub or hex) to enable Nostr mode
- `PINFRIENDS` (default: false) – Cache media from accounts you follow (ephemeral, allows garbage collection)

Persist your IPFS repo by mounting `/data` (recommended).

## How It Works

1. Upload – Files stream to your local IPFS node (unpinned)
2. Propagate – Content spreads via IPFS as peers request it
3. Repopulate – If garbage collected, your node repopulates content when it comes online
4. Optional Nostr mode – Automatically fetches your notes, extracts IPFS CIDs, and pins them

## Nostr Mode (Optional)

- Fetches all your posts (pagination) and filters out expired notes
- Extracts IPFS CIDs and pins them locally (permanent)
- Optionally caches media from people you follow for redundancy (ephemeral, garbage collected)
- Runs automatically every 3 hours; view status in the admin

## Admin & API

- Admin dashboard: /admin.html
- Upload API: `PUT /upload` with form field `file`
