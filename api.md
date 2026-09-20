# Originless HTTP API

Base URL: **`http://localhost:3232`**

No API keys, accounts, or authentication.

**CORS** — JSON API and dashboard pages send Originless headers: `Access-Control-Allow-Origin: *`, methods `GET, HEAD, POST, OPTIONS`, headers `Content-Type`. **`/ipfs` and `/ipns` (including `{cid}.ipfs.*` hosts) do not.** Those responses are reverse-proxied from Kubo and already include CORS (`GET`/`HEAD`/`OPTIONS`, `Range`, `X-Ipfs-Path`, …). Originless must not add its own `Access-Control-*` on that path: browsers treat a second `Access-Control-Allow-Origin` as invalid and `fetch()` throws `TypeError: Failed to fetch` even on HTTP 200.

**Bodies** — JSON uses `Content-Type: application/json`. JSON and HTML are gzip-compressed when the client sends `Accept-Encoding: gzip` (`HEAD` is not wrapped). `/ipfs` and `/ipns` streams are passed through; Kubo may compress them itself.

Uploads (`POST /upload`, `/media`, `/uploadfolder`) are limited to **3 concurrent requests**. Extra uploads return `503`. Per-file size cap is `STORAGE_MAX / 100` (1 GB when `STORAGE_MAX=100GB`).

---

## Route index

| Method | Path | What it does |
| :----- | :--- | :----------- |
| `GET` | [`/health`](#get-health) | Liveness + IPFS peer count |
| `GET` | [`/status`](#get-status) | Node, storage, and gateway snapshot |
| `POST` | [`/upload`](#post-upload) | Pin a file as-is; returns a CID |
| `POST` | [`/media`](#post-media) | Strip EXIF/GPS/XMP from an image, then pin |
| `POST` | [`/uploadfolder`](#post-uploadfolder) | Pin a directory tree as one root CID |
| `GET` | [`/history`](#get-history) | Paginated upload log |
| `GET` | [`/pins`](#get-pins) | Pinned count, bytes, and janitor threshold |
| `GET` | [`/metrics`](#get-metrics) | Prometheus text metrics |
| `GET`/`HEAD` | [`/ipfs/{cid}`](#get-ipfscid--ipnspath) | Serve bytes for a CID (local pin or swarm fetch) |
| `GET`/`HEAD` | [`/ipns/{name}`](#get-ipfscid--ipnspath) | Resolve and serve an IPNS name |
| `GET` | [`/api/examples`](#get-apiexamples) | JSON catalog of client tools |
| `GET` | [`/examples/manifest.json`](#get-apiexamples) | Same catalog as `/api/examples` |

Dashboard and Tools pages (`/`, `/examples/`, …) are HTML, not JSON.

---

## `GET /health`

IPFS swarm check. Use this for container healthchecks and load balancers.

```bash
curl http://localhost:3232/health
```

**200** — daemon is up and has at least one peer:

```json
{ "status": "healthy", "peers": 140 }
```

**503** — daemon down, unreachable, or zero peers:

```json
{ "status": "unhealthy", "peers": 0, "reason": "No peers connected" }
```

---

## `GET /status`

Full node snapshot for the dashboard and operators.

```bash
curl http://localhost:3232/status
```

**200** fields:

| Field | Meaning |
| :---- | :------ |
| `status` | `"success"` |
| `timestamp` | RFC 3339 UTC |
| `bandwidth` | Kubo totals and rates (`totalIn`, `totalOut`, `rateIn`, `rateOut`, `interval`) |
| `repository` | Repo `size`, `storageMax`, `numObjects`, `path`, `version` |
| `node` | Peer `id`, `publicKey`, `agentVersion`, `protocolVersion` |
| `peers.count` | Connected swarm peers |
| `storageLimit` | Configured `STORAGE_MAX` and current repo size |
| `fileLimit` | Per-upload cap (`configured` string, `bytes` integer) |
| `appVersion` | Originless version string |
| `gateway.enabled` / `gateway.serving` | Whether this node serves `/ipfs` and `/ipns` over HTTP |
| `gateway.path` / `gateway.ipns` | `"/ipfs/"` and `"/ipns/"` |
| `gateway.url` / `gateway.ipnsUrl` | Public fetch URLs on this node (omitted when disabled) |
| `gateway.kubo` | Native Kubo gateway URL (omitted when disabled) |

```json
{
  "status": "success",
  "timestamp": "2026-08-28T12:00:00Z",
  "bandwidth": { "totalIn": 0, "totalOut": 0, "rateIn": 0, "rateOut": 0 },
  "repository": { "size": 1048576, "storageMax": 107374182400, "numObjects": 12 },
  "node": { "id": "12D3KooW...", "agentVersion": "kubo/0.34.0" },
  "peers": { "count": 140 },
  "storageLimit": { "configured": "100GB", "current": "1.00 MB" },
  "fileLimit": { "configured": "1.00 GB", "bytes": 1073741824 },
  "appVersion": "1.0.4",
  "gateway": { "enabled": true, "serving": true, "path": "/ipfs/", "ipns": "/ipns/" }
}
```

---

## `POST /upload`

Upload and pin any file.

- **Content-Type:** `multipart/form-data`
- **Field name:** `file` (only the first file part is read)

```bash
curl -X POST -F "file=@document.pdf" http://localhost:3232/upload
```

**200:**

```json
{
  "status": "success",
  "cid": "bafybeicg2oxl5gah64cvk44phwsr33m42x3fvwg6b2kdt6v2iylndr2mqu",
  "size": 245760,
  "type": "application/pdf",
  "filename": "document.pdf",
  "pinned": true
}
```

---

## `POST /media`

Upload an image and strip private metadata before pinning.

- Supported inputs: JPEG, PNG, GIF, WebP
- Actions: Strips EXIF, GPS, XMP, IPTC; normalizes orientation
- **Field name:** `file`

```bash
curl -X POST -F "file=@photo.jpg" http://localhost:3232/media
```

**200:**

```json
{
  "status": "success",
  "cid": "bafybeihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku",
  "size": 1823400,
  "originalSize": 2104500,
  "type": "image/jpeg",
  "filename": "photo.jpg",
  "pinned": true,
  "anonymized": true,
  "stripped": ["EXIF", "GPS", "XMP"]
}
```

---

## `POST /uploadfolder`

Upload a directory tree under a single root CID.

```bash
curl -X POST \
  -F "file=@dist/index.html;filename=index.html" \
  -F "file=@dist/app.js;filename=assets/app.js" \
  http://localhost:3232/uploadfolder
```

**200:**

```json
{
  "status": "success",
  "cid": "bafybeihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku",
  "files": 2,
  "size": 40960,
  "pinned": true
}
```

---

## `GET /history`

Paginated list of pinned uploads.

| Query | Default | Range |
| :---- | :------ | :---- |
| `limit` | `50` | 1–100 |
| `offset` | `0` | ≥ 0 |

```bash
curl "http://localhost:3232/history?limit=20&offset=0"
```

**200:**

```json
{
  "status": "success",
  "uploads": [
    {
      "id": 1,
      "cid": "bafybei...",
      "filename": "document.pdf",
      "size": 245760,
      "created_at": "2026-08-28T12:00:00Z",
      "unpinned": false
    }
  ],
  "limit": 20,
  "offset": 0
}
```

---

## `GET /pins`

Stats on currently tracked pins and eviction threshold.

```bash
curl http://localhost:3232/pins
```

**200:**

```json
{
  "status": "success",
  "pinnedCount": 42,
  "pinnedSize": 524288000,
  "pinnedSizeStr": "500.00 MB",
  "storageLimit": "100GB",
  "threshold": 75
}
```

---

## `GET /ipfs/{cid}` / `/ipns/{name}`

Path-style HTTP gateway. Reverse-proxies Kubo (`IPFS_GATEWAY`, default `http://127.0.0.1:8080`).

```bash
curl -O "http://localhost:3232/ipfs/$CID"
curl "http://localhost:3232/ipfs/$CID?filename=photo.png"
curl -O "http://localhost:3232/ipfs/$CID/index.html"
```

---

## `GET /metrics`

Prometheus text exposition (`text/plain; version=0.0.4`). Gauges refresh on each scrape.

```bash
curl http://localhost:3232/metrics
```

| Metric | Type | Meaning |
| :----- | :--- | :------ |
| `originless_build_info{version=…}` | gauge | Build version (`1.0.4`) |
| `originless_http_requests_total{path=…}` | counter | Requests by path |
| `originless_http_errors_total` | counter | Responses with status ≥ 400 |
| `originless_uploads_total` | counter | Successful upload operations |
| `originless_upload_bytes_total` | counter | Bytes added via upload endpoints |
| `originless_pinned_count` / `_bytes` | gauge | Current janitor-tracked pins |
| `originless_storage_limit_bytes` | gauge | `STORAGE_MAX` |
| `originless_storage_used_bytes` | gauge | Kubo repo size |
| `originless_ipfs_healthy` | gauge | `1` / `0` |
| `originless_ipfs_peers` | gauge | Swarm peer count |
| `originless_gateway_enabled` | gauge | `1` if `/ipfs` is served |

---

## `GET /api/examples`

JSON catalog of HTML client tools under [`examples/`](examples/). `GET /examples/manifest.json` returns the same payload.

```bash
curl http://localhost:3232/api/examples
```

---

## UI pages (not JSON)

| Path | Notes |
| :--- | :---- |
| `GET /` | Node dashboard (library, status) |
| `GET /examples/` | Client tools index |
| `GET /examples/{file}` | Individual tool HTML |
| `GET /examples` | `301` → `/examples/` |
| `GET /library.html` | `301` → `/` |

---

## Shared upload errors

| Status | When |
| :----- | :--- |
| `400` | Missing `file` part, empty filename, or malformed multipart |
| `413` | File larger than `STORAGE_MAX / 100` (`maxSize` is included) |
| `415` | `/media` only: not JPEG/PNG/GIF/WebP |
| `503` | 3 uploads already in flight (`"Server busy"`) |
| `500` | Kubo add/pin failed |
