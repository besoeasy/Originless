# Originless

Private, decentralized file sharing for Nostr and the web.

One all-in-one storage backend you can drop into anything: your own apps, screenshot tools, pastebin-style pastes, Nostr clients, Reddit posts, forum embeds—anything that needs durable, anonymous file hosting. One Originless to rule them all and keep you anonymous.

<img width="1536" height="1024" src="https://github.com/user-attachments/assets/5014810c-cc51-4ad4-a1b8-6e4db510c09f" />

## Install

```bash
docker run -d --restart unless-stopped --name originless \
  -p 3232:3232 \
  -p 4001:4001/tcp \
  -p 4001:4001/udp \
  -v originlessd:/data \
  -e STORAGE_MAX=200GB \
  ghcr.io/besoeasy/originless
```

Open http://localhost:3232 after starting.

## Example Web Apps

We have built a few example web apps using Originless. You can explore them here:

https://dweb.link/ipfs/QmTiXA22rjzx2g1yzDJmXu21AL5zM5dz7W9UquWSpL2ss2

## Screenshot

<img width="1479" height="1151" src="https://github.com/user-attachments/assets/6ed4908c-37aa-4973-a9c0-edb7c0fe479f" />

## Why Originless

- Anonymous uploads: no accounts, no tracking, no logs
- Resilient by design: served from IPFS; your node needn’t be online 24/7
- Self-healing: content repopulates across IPFS when your node comes online

## Supported Platforms

Originless is integrated into the following platforms:

| Platform     | Description                       | Link                                        |
| ------------ | --------------------------------- | ------------------------------------------- |
| **0xchat**   | Private, decentralized Nostr chat | [0xchat.com](https://0xchat.com/)           |
| **ZeroNote** | Anonymous encrypted notes sharing | [zeronote.js.org](https://zeronote.js.org/) |

## How It Works

1. Upload – Files stream to your local IPFS node (unpinned)
2. Propagate – Content spreads via IPFS as peers request it
3. Repopulate – If garbage collected, your node repopulates content when it comes online

## API Documentation

See the full API docs in [api.md](api.md).
