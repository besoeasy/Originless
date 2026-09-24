# Originless

Originless is a single-container backend for real-time web applications. It combines a small Go HTTP service with an embedded [Kubo](https://github.com/ipfs/kubo) IPFS node, so signed events and content-addressed files share one deployment.

- **Signed events** — immutable, Ed25519-verified JSON documents with TTL expiry.
- **Live updates** — Server-Sent Events at `/events/stream`; no WebSocket server required.
- **IPFS uploads** — files and folders are added with `pin=false` and addressed by CID.
- **Built-in demos** — a shared 256×256 canvas, chat, event stream, and upload dashboard.
- **CORS enabled** — browser applications can call the HTTP API from another origin.

## Run with Docker

Docker is the supported runtime. Start the published image with one command:

```bash
docker run -d --name originless -p 3232:3232 -e STORAGE_MAX=20GB ghcr.io/besoeasy/originless:latest
```

Open the dashboard at [http://localhost:3232](http://localhost:3232).

The container has no volume mount. Its IPFS repository lives in the container filesystem, so stopping the container preserves its writable layer, while removing the container deletes that data.

```bash
docker logs -f originless
docker stop originless
docker rm originless
```

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `STORAGE_MAX` | `20GB` | Human-readable soft limit for the Kubo repository. |

Automatic garbage collection is enabled at a fixed **90%** watermark and runs hourly. `STORAGE_MAX` is the only storage setting exposed as a Docker environment variable. Because uploads are unpinned, a CID may become unavailable after garbage collection.

## Upload and download files

Upload a single file:

```bash
curl -F "file=@hello.txt" http://localhost:3232/up
```

Upload a folder using `/upf`:

```bash
curl \
  -F "file=@folder/one.txt;filename=folder/one.txt" \
  -F "file=@folder/nested/two.txt;filename=folder/nested/two.txt" \
  http://localhost:3232/upf
```

The response contains the resulting CID. Download content available in the local node through the standard gateway-shaped path:

```bash
curl -OJ http://localhost:3232/ipfs/<cid>
```

For content available through the public IPFS network, use [inbrowser.link](https://inbrowser.link/ipfs/), [Helia Verified Fetch](https://github.com/ipfs/helia-verified-fetch), or a dedicated gateway such as [Rainbow](https://github.com/ipfs/rainbow).

## API

The complete request and response reference is in [`docs/api.md`](docs/api.md).

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/` | Dashboard and live demos |
| `GET` | `/healthz` | Container health |
| `GET` | `/stats` | IPFS and event metrics |
| `POST` | `/up` | Upload a file or folder |
| `POST` | `/upf` | Explicit folder upload |
| `GET` | `/ipfs/{cid}` | Retrieve locally available content |
| `GET` | `/cid/{cid}` | JSON metadata and availability for a CID |
| `POST` | `/events` | Publish a signed event |
| `GET` | `/events` | Query events with filters and pagination |
| `GET` | `/events/{id}` | Retrieve one event |
| `GET` | `/events/stream` | Stream events over SSE |

Events are currently held in memory and are lost when the container is restarted. Content uploaded to IPFS is also ephemeral from Originless's perspective: the container does not mount a persistent volume and does not pin uploads.

## Signed events

An event is a small JSON document signed by its creator. The signature is verified by the server using the Ed25519 public key in `owner`; the server does not manage user accounts or passwords.

```json
{
  "owner": "ed25519:<64-hex-public-key>",
  "collection": "originless/chat",
  "created_at": 1758420000,
  "expires_at": 1789956000,
  "data": {"message": "Hello from the browser"},
  "labels": ["chat:lobby"],
  "sig": "<128-hex-signature>"
}
```

Use `/events/stream` to receive matching events live. Event documents are limited to 8 KiB and one year of TTL.

## Links

- [GitHub repository](https://github.com/besoeasy/originless)
- [API reference](docs/api.md)
- [Kubo](https://github.com/ipfs/kubo)
- [IPFS HTTP Gateway specification](https://specs.ipfs.tech/http-gateways/)
