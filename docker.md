# Docker Configuration

**Full configuration:**

```bash
docker run -d --restart unless-stopped \
  -p 3232:3232 \
  -p 4001:4001/tcp \
  -p 4001:4001/udp \
  -v originless-data:/data \
  -e STORAGE_MAX=200GB \
  -e FILE_LIMIT=5GB \
  -e REMOTE_FILE_LIMIT=250MB \
  -e NPUB=npub1yourkey1...,npub2yourkey2...,npub3yourkey3... \
  --stop-timeout 15 \
  --name originless \
  ghcr.io/besoeasy/originless:main
```

**Docker Compose:**

```yaml
services:
  originless:
    image: ghcr.io/besoeasy/originless:main
    container_name: originless
    restart: unless-stopped
    stop_grace_period: 15s
    ports:
      - "3232:3232"
      - "4001:4001/tcp"
      - "4001:4001/udp"
    volumes:
      - originless-data:/data
    environment:
      - STORAGE_MAX=200GB
      - FILE_LIMIT=5GB
      - REMOTE_FILE_LIMIT=250MB
      - NPUB=npub1yourkey1...,npub2yourkey2...,npub3yourkey3...

volumes:
  originless-data:
```

Open http://localhost:3232 after starting.

## Configuration

- `STORAGE_MAX` (default: 200GB) – IPFS storage cap before GC
- `FILE_LIMIT` (default: 1/10 of STORAGE_MAX, e.g., 20GB when STORAGE_MAX is 200GB) – Max size per file upload
- `REMOTE_FILE_LIMIT` (default: 1/10 of STORAGE_MAX, e.g., 20GB when STORAGE_MAX is 200GB) – Max size for remote URL uploads
- `NPUB` – Comma-separated list of Nostr pubkeys (npub or hex) to enable Nostr mode. Example: `npub1abc...,npub2def...,npub3ghi...`

Persist your IPFS repo by mounting `/data` (recommended).
