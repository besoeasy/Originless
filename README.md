# Originless

Private, decentralized file sharing for Nostr and the web.

One all-in-one storage backend you can drop into anything: your own apps, screenshot tools, pastebin-style pastes, Nostr clients, Reddit posts, forum embeds—anything that needs durable, anonymous file hosting. One Originless to rule them all and keep you anonymous.

<img width="2241" height="1608" alt="filedrop" src="https://github.com/user-attachments/assets/141cc0cf-9684-421d-8214-b1ed45e1e813" />

## Why Originless

- Anonymous uploads: no accounts, no tracking, no logs
- Resilient by design: served from IPFS; your node needn’t be online 24/7
- Self-healing: content repopulates across IPFS when your node comes online


## Supported Platforms

Originless is integrated into the following platforms:

| Platform    | Description                                              | Link                                            |
| ----------- | -------------------------------------------------------- | ----------------------------------------------- |
| **0xchat**  | Private, decentralized Nostr chat                        | [0xchat.com](https://0xchat.com/)               |
| **ZeroNote** | Anonymous encrypted notes sharing                        | [zeronote.js.org](https://zeronote.js.org/)     |

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


## API Documentation

See the full API docs in [api.md](api.md).

