# Originless HTTP API

Base URL: **`http://localhost:3232`**

No API keys, accounts, or authentication.

**CORS** — JSON API and dashboard pages send standard Originless headers: `Access-Control-Allow-Origin: *`, methods `GET, HEAD, POST, OPTIONS`, headers `Content-Type`.

**Bodies** — JSON uses `Content-Type: application/json`. Responses are gzip-compressed when the client sends `Accept-Encoding: gzip` (`HEAD` is not wrapped).

Uploads (`POST /up`) are limited to **3 concurrent requests**. Extra uploads return `503`. Per-file size cap is `min(STORAGE_MAX / 100, 512 MiB)`.

---

## Route index

| Method | Path | What it does |
| :----- | :--- | :----------- |
| `GET` | [`/health`](#get-health) | Liveness + IPFS peer count |
| `GET` | [`/status`](#get-status) | Node and storage snapshot |
| `POST` | [`/upload`](#post-upload) | Pin a file to IPFS; returns a CID |
| `POST` | [`/uploadfolder`](#post-uploadfolder) | Pin a directory tree as one root CID |
| `GET` | [`/history`](#get-history) | Paginated upload log |
| `GET` | [`/pins`](#get-pins) | Pinned count, bytes, and janitor threshold |
| `POST` | [`/records`](#post-records) | Store a signed JSON record (8 KB max) |
| `GET` | [`/records`](#get-records) | Query signed records (owner, collection, label, time) |
| `GET` | [`/records/stream`](#get-recordsstream) | Real-time Server-Sent Events (SSE) feed |
| `GET` | [`/records/{id}`](#get-recordsid) | Fetch one record by ID |
| `POST` | [`/up`](#post-up) | Store a `.bin` file as `<sha256>.bin` |
| `GET` | [`/blobs`](#get-blobs) | List stored `.bin` blobs (limit, offset) |
| `GET`/`HEAD` | [`/down/{hash}`](#get-downhash) | Serve a stored `.bin` by sha256 |
| `GET` | [`/metrics`](#get-metrics) | Prometheus text metrics |

Dashboard pages (`/`, `/agent.html`, …) are HTML, not JSON.

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

```json
{
  "status": "success",
  "timestamp": "2026-08-28T12:00:00Z",
  "bandwidth": { "totalIn": 0, "totalOut": 0, "rateIn": 0, "rateOut": 0 },
  "repository": { "size": 1048576, "storageMax": 107374182400, "numObjects": 12 },
  "node": { "id": "12D3KooW...", "agentVersion": "kubo/0.34.0" },
  "peers": { "count": 140 },
  "storageLimit": { "configured": "100GB", "current": "1.00 MB" },
  "fileLimit": { "configured": "512.00 MB", "bytes": 536870912 }
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

## `POST /records`

Store a signed JSON record for app data (posts, chat, game saves, profiles). No accounts — ownership is an `ed25519` keypair, verified server-side on every publish.

- **Content-Type:** `application/json`
- **Max size:** total body `<= 8192` bytes (`413` if over)
- **ID:** server-computed `sha256(owner:collection:created_at:expires_at:canonical(data):labels)` — never send `id`

| Field | Required | Rule |
| :---- | :------ | :--- |
| `owner` | yes | `ed25519:<64 hex>` (32-byte pubkey) |
| `collection` | yes | `^[a-z0-9/_-]{1,32}$` — your table: `chat`, `saves`, `profile` |
| `created_at` | yes | unix seconds (≤ 15 min future skew allowed) |
| `expires_at` | yes | unix seconds, `created_at < expires_at <= created_at + 315360000` (10y) |
| `data` | yes | JSON **object** (stringify app structs into it) |
| `labels` | yes | `0–10` strings for filtering (`[]` for none) |
| `sig` | yes | `128` hex chars: `ed25519_sign(id)` |

```bash
curl -X POST http://localhost:3232/records \
  -H "Content-Type: application/json" \
  -d '{"owner":"ed25519:3b6a...29","collection":"chat","created_at":1758420000,"expires_at":1790040000,"data":{"room":"general","text":"gg"},"labels":["room:general"],"sig":"a3f1...c9"}'
```

**201** (new) or **200** (`duplicate: true` — same payload republishes to the same ID):

```json
{ "status": "success", "id": "8f2c...1a", "stored_at": "2026-09-21T02:00:00Z" }
```

Errors: `400` schema/TTL failure (`referenced blob not found` when `data._blob` points at an unknown hash), `401` bad signature, `413` over 8 KB.

**Linking a blob** — `data` may carry `"_blob": "<64 hex sha256>"` to attach the record to a stored binary (upload via `POST /up` first; the hash is covered by the record signature, so the link is tamper-proof). Blobs linked from live records are exempt from janitor eviction. Clients that want the link queryable should also add a `blob:<hash>` label.

---

## `GET /records`

Query records, newest-first. Expired records are hidden unless `include_expired=true`.

| Query | Default | Notes |
| :---- | :------ | :---- |
| `owner` | | exact `ed25519:…` match |
| `collection` | | exact match |
| `label` | | records carrying this label |
| `since` / `until` | | unix seconds on `created_at` |
| `search` | | substring match inside `data` |
| `limit` | `50` | 1–100 |
| `cursor` | `0` | offset; follow `next_cursor` while non-empty |
| `include_expired` | | `true` to show expired rows |

```bash
curl "http://localhost:3232/records?collection=chat&label=room:general&limit=20"
```

**200:**

```json
{
  "status": "success",
  "records": [
    {
      "id": "8f2c...1a",
      "owner": "ed25519:3b6a...29",
      "collection": "chat",
      "created_at": 1758420000,
      "expires_at": 1790040000,
      "data": { "room": "general", "text": "gg" },
      "labels": ["room:general"],
      "sig": "a3f1...c9",
      "stored_at": "2026-09-21T02:00:00Z",
      "size": 312
    }
  ],
  "limit": 20,
  "cursor": "0",
  "next_cursor": ""
}
```

Tip for replaceable state (profile, save slots): publish a new record per change and read with `limit=1` — latest wins, older versions fade via `expires_at`.

---

## `GET /records/stream`

Real-time Server-Sent Events (SSE) stream of newly published records matching query filters. Ideal for live chats, multiplayer game events, push notifications, and IoT signal listening without polling.

| Parameter | Filter |
| :-------- | :----- |
| `collection` | Stream records matching exact collection name |
| `label` | Stream records containing specific label |
| `owner` | Stream records published by specific `ed25519:<pubkey>` |
| `search` | Stream records where JSON `data` contains substring |

### SSE Event Format

Response headers:
```
Content-Type: text/event-stream; charset=utf-8
Cache-Control: no-cache, no-transform
Connection: keep-alive
```

Clients receive an initial comment, followed by standard SSE event messages as records are published:

```text
: connected

event: record
id: 8f2c...1a
data: {"id":"8f2c...1a","owner":"ed25519:...","collection":"chat","created_at":1758420000,"expires_at":1790040000,"data":{"room":"lobby","msg":"hi"},"labels":["room:lobby"],"sig":"...","stored_at":"..."}

: keepalive
```

### Example Usage (curl & JavaScript)

```bash
# Listen for all chat messages in room:lobby
curl -N "http://localhost:3232/records/stream?collection=chat&label=room:lobby"
```

```javascript
// Browser / Node EventSource
const es = new EventSource("http://localhost:3232/records/stream?collection=chat&label=room:lobby");
es.addEventListener("record", (e) => {
  const record = JSON.parse(e.data);
  console.log("New record:", record.data);
});
```

---

## `GET /records/{id}`

Fetch one record by its server-computed ID. Expired records return `404` unless `?include_expired=true`.

```bash
curl http://localhost:3232/records/8f2c...1a
```

**200:** `{ "status": "success", "record": { … } }` · **404:** unknown or expired ID.

`?resolve=blob` inlines the record's linked blob in one round trip (record must carry `data._blob`):

```bash
curl "http://localhost:3232/records/8f2c...1a?resolve=blob"
```

**200:** `{ "status": "success", "record": { … }, "blob": { "hash": "…", "size": 4096, "sizeStr": "4.00 KB", "url": "/down/…", "retained_until": "2027-09-21T02:00:00Z", "protected": true } }` — `blob` is `null` when the linked blob is gone.

---

## `POST /up`

Store a binary blob, content-addressed by its sha256. Only `.bin` files accepted; identical bytes dedupe to the same hash.

- **Content-Type:** `multipart/form-data`
- **Field name:** `file` (filename must end in `.bin`, case-insensitive)
- Saved as `<sha256>.bin` under `/data/blobs`
- Retention: size-weighted guarantee of **30 days at 512 MiB** up to **1 year at size 0** (`retention = min_age + (min_age - max_age) * (size/max_size - 1)^3`); after expiry, LRU-evicted only under storage pressure (shared `STORAGE_MAX` quota). Blobs linked via `_blob` from live records are never evicted.

```bash
curl -X POST -F "file=@save.bin" http://localhost:3232/up
```

**201** (new) or **200** (`duplicate: true`):

```json
{ "status": "success", "hash": "e3b0...85", "size": 4096, "url": "/down/e3b0...85", "duplicate": false }
```

Errors: `400` missing/empty part, `413` over per-file cap, `415` not a `.bin` file, `503` server busy.

---

## `GET /blobs`

List stored `.bin` binary blobs, ordered newest-first.

```bash
curl "http://localhost:3232/blobs?limit=50&offset=0"
```

**200 OK**:

```json
{
  "status": "success",
  "count": 1,
  "total_bytes": 4096,
  "total_bytes_str": "4.00 KB",
  "limit": 50,
  "offset": 0,
  "blobs": [
    {
      "hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "size": 4096,
      "created_at": "2026-09-21T09:00:00Z",
      "last_access": "2026-09-21T09:05:00Z",
      "access_count": 2
    }
  ]
}
```

---

## `GET /down/{hash}`

Serve a stored blob by sha256. `HEAD` is also allowed. Every hit refreshes LRU recency.

- `{hash}` must be 64 lowercase hex chars (`400` otherwise)
- Responses send `Content-Type: application/octet-stream`, `ETag: "<hash>"`, and immutable-friendly `Cache-Control`
- Unknown or evicted hashes return `404`

```bash
curl -O "http://localhost:3232/down/e3b0...85"
```

---

## Gateway Fetching (Rainbow & Public Gateways)

To protect node operators from serving abusive web traffic and legal liabilities, Originless does not host an open HTTP gateway on port `3232`. Requests to `/ipfs/` or `/ipns/` return `404 Not Found`.

Content pinned by Originless is broadcast across the IPFS swarm on port `4001` (Bitswap). To fetch content over HTTP:

- **[Rainbow](https://github.com/ipfs/rainbow)** (Recommended) — The official standalone IPFS HTTP gateway implementation in Go. Provides automated denylist enforcement (`badbits`), subdomain origin isolation, and caching.
- **Public Gateways**:
  ```bash
  curl -O "https://inbrowser.link/ipfs/$CID"
  curl -O "https://ipfs.io/ipfs/$CID"
  ```
- **Native IPFS URIs**: `ipfs://$CID`

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

---

## UI pages (not JSON)

| Path | Notes |
| :--- | :---- |
| `GET /` | Node dashboard (library, telemetry, status) |
| `GET /agent` | `301` → `/agent.html` |
| `GET /agent.html` | Standalone Agent Prompt & Skill guide |
| `GET /library.html` | `301` → `/` |

---

## Shared upload errors

| Status | When |
| :----- | :--- |
| `400` | Missing `file` part, empty filename, or malformed multipart |
| `413` | File larger than `min(STORAGE_MAX / 100, 512 MiB)` (`maxSize` is included) |
| `415` | `/up` only: filename does not end in `.bin` |
| `503` | 3 uploads already in flight (`"Server busy"`) |
| `500` | Kubo add/pin failed |
