# Originless HTTP API

Base URL: **`http://localhost:3232`**

No API keys, accounts, or authentication.

**CORS** — JSON API and dashboard pages send standard Originless headers: `Access-Control-Allow-Origin: *`, methods `GET, HEAD, POST, OPTIONS`, headers `Content-Type`. `OPTIONS` preflights return `204`.

**Bodies** — JSON uses `Content-Type: application/json`. Responses are gzip-compressed when the client sends `Accept-Encoding: gzip` (`HEAD` and the SSE stream are not wrapped).

Uploads (`POST /up`) are limited to **3 concurrent requests**. Extra uploads return `503`. There is no per-file size cap and no total storage quota — blobs persist until their size-weighted retention window elapses.

**Timeouts** — the server imposes no read/write deadline: slow uploads are never cut mid-body, and SSE streams are long-lived (read-header and keep-alive idle are still bounded for normal clients).

---

## Route index

| Method | Path | What it does |
| :----- | :--- | :----------- |
| `GET` | [`/status`](#get-status) | Node and storage snapshot |
| `POST` | [`/events`](#post-events) | Store a signed JSON event (8 KB max) · alias: `/records` |
| `GET` | [`/events`](#get-events) | Query signed events (owner, collection, label, time) · alias: `/records` |
| `GET` | [`/events/stream`](#get-eventsstream) | Real-time Server-Sent Events (SSE) feed · alias: `/records/stream` |
| `GET` | [`/events/{id}`](#get-eventsid) | Fetch one event by ID · alias: `/records/{id}` |
| `POST` | [`/up`](#post-up) | Store a `.bin` file as `<sha256>.bin` |
| `GET` | [`/blobs`](#get-blobs) | List stored `.bin` blobs (limit, offset) |
| `GET`/`HEAD` | [`/down/{hash}`](#get-downhash) | Serve a stored `.bin` by sha256 |
| `GET` | [`/metrics`](#get-metrics) | Prometheus text metrics |

Dashboard pages (`/`, `/agent.txt`, …) are HTML, not JSON.

---

## `GET /status`

Node snapshot for the dashboard, operators, and container healthchecks.

```bash
curl http://localhost:3232/status
```

**200** fields:

| Field | Meaning |
| :---- | :------ |
| `status` | `"success"` |
| `timestamp` | RFC 3339 UTC |
| `version` | Server build version |
| `blobs` | Tracked blobs (`count`, `size`, `sizeStr`) |
| `records` | Live (unexpired) records (`count`) |
| `sse` | Live stream state (`clients`, `dropped` — records lost to slow consumers) |

```json
{
  "status": "success",
  "timestamp": "2026-09-21T02:00:00Z",
  "version": "0.1.0",
  "blobs": { "count": 12, "size": 1048576, "sizeStr": "1.00 MB" },
  "records": { "count": 42 },
  "sse": { "clients": 3, "dropped": 0 }
}
```

---

## `POST /events`

Store a signed JSON event for app data (posts, chat, game saves, profiles). Legacy alias `POST /records` is supported. No accounts — ownership is an `ed25519` keypair, verified server-side on every publish.

- **Content-Type:** `application/json`
- **Max size:** total body `<= 8192` bytes (`413` if over)
- **ID:** server-computed `sha256(owner:collection:created_at:expires_at:canonical(data):labels)` — never send `id`

| Field | Required | Rule |
| :---- | :------ | :--- |
| `owner` | yes | `ed25519:<64 hex>` (32-byte pubkey) |
| `collection` | yes | `^[a-z0-9/_-]{1,32}$` — your table: `chat`, `saves`, `profile` |
| `created_at` | yes | unix seconds (≤ 15 min future skew allowed) |
| `expires_at` | yes | unix seconds, `created_at < expires_at <= created_at + 31536000` (1y) |
| `data` | yes | JSON **object** (stringify app structs into it) |
| `labels` | yes | `0–10` strings for filtering (`[]` for none) |
| `sig` | yes | `128` hex chars: `ed25519_sign(id)` |

```bash
curl -X POST http://localhost:3232/events \
  -H "Content-Type: application/json" \
  -d '{"owner":"ed25519:3b6a...29","collection":"chat","created_at":1758420000,"expires_at":1789956000,"data":{"room":"general","text":"gg"},"labels":["room:general"],"sig":"a3f1...c9"}'
```

**201** (new) or **200** (`duplicate: true` — same payload republishes to the same ID):

```json
{ "status": "success", "id": "8f2c...1a", "stored_at": "2026-09-21T02:00:00Z" }
```

Errors: `400` schema/TTL failure (`referenced blob not found` when `data._blob` points at an unknown hash), `401` bad signature, `413` over 8 KB.

**Linking a blob** — `data` may carry `"_blob": "<64 hex sha256>"` to attach the record to a stored binary (upload via `POST /up` first; the hash is covered by the record signature, so the link is tamper-proof). Blobs linked from live records are exempt from janitor eviction. Clients that want the link queryable should also add a `blob:<hash>` label.

---

## `GET /events`

Query signed events, newest-first. Legacy alias `GET /records` is supported. Expired events are hidden unless `include_expired=true`.

| Query | Default | Notes |
| :---- | :------ | :---- |
| `owner` | | exact `ed25519:…` match |
| `collection` | | exact match |
| `label` | | events carrying this label |
| `since` / `until` | | unix seconds on `created_at` |
| `search` | | substring match inside `data` |
| `limit` | `50` | 1–100 |
| `cursor` | | keyset token `"<created_at>:<id>"` from `next_cursor`; follow it while non-empty. Plain numeric cursors (old offset style) still work. |
| `include_expired` | | `true` to show expired rows |

```bash
curl "http://localhost:3232/events?collection=chat&label=room:general&limit=20"
```

**200:**

```json
{
  "status": "success",
  "events": [
    {
      "id": "8f2c...1a",
      "owner": "ed25519:3b6a...29",
      "collection": "chat",
      "created_at": 1758420000,
      "expires_at": 1789956000,
      "data": { "room": "general", "text": "gg" },
      "labels": ["room:general"],
      "sig": "a3f1...c9",
      "stored_at": "2026-09-21T02:00:00Z",
      "size": 312
    }
  ],
  "records": [ ... ],
  "limit": 20,
  "cursor": "",
  "next_cursor": "1790000000:8f2c...1a"
}
```

Tip for replaceable state (profile, save slots): publish a new event per change and read with `limit=1` — latest wins, older versions fade via `expires_at`.

---

## `GET /events/stream`

Real-time Server-Sent Events (SSE) stream of newly published events matching query filters. Legacy alias `GET /records/stream` is supported. Ideal for live chats, multiplayer game events, push notifications, and IoT signal listening without polling.

| Parameter | Filter |
| :-------- | :----- |
| `collection` | Stream events matching exact collection name |
| `label` | Stream events containing specific label |
| `owner` | Stream events published by specific `ed25519:<pubkey>` |
| `search` | Stream events where JSON `data` contains substring |

### SSE Event Format

Response headers:

```
Content-Type: text/event-stream; charset=utf-8
Cache-Control: no-cache, no-transform
Connection: keep-alive
```

Clients receive an initial comment, followed by standard SSE event messages as events are published (plus `: keepalive` comments every 15 s):

```text
: connected

event: event
id: 8f2c...1a
data: {"id":"8f2c...1a","owner":"ed25519:...","collection":"chat","created_at":1758420000,"expires_at":1789956000,"data":{"room":"lobby","msg":"hi"},"labels":["room:lobby"],"sig":"...","stored_at":"..."}

: keepalive
```

### Example Usage (curl & JavaScript)

```bash
# Listen for all chat messages in room:lobby
curl -N "http://localhost:3232/events/stream?collection=chat&label=room:lobby"
```

```javascript
// Browser / Node EventSource
const es = new EventSource("http://localhost:3232/events/stream?collection=chat&label=room:lobby");
es.addEventListener("event", (e) => {
  const event = JSON.parse(e.data);
  console.log("New event:", event.data);
});
```

**Delivery guarantee** — SSE is useful but inherently lossy for a slow consumer: if a client reads too slowly, the server skips it rather than blocking others, and counts the skip in `originless_sse_dropped_total`. Reconnect (or poll `GET /events?limit=…`) to catch up after any disconnect or drop.

---

## `GET /events/{id}`

Fetch one event by its server-computed ID. Legacy alias `GET /records/{id}` is supported. Expired events return `404` unless `?include_expired=true`.

```bash
curl http://localhost:3232/events/8f2c...1a
```

**200:** `{ "status": "success", "event": { … }, "record": { … } }` · **404:** unknown or expired ID.

`?resolve=blob` inlines the event's linked blob in one round trip (event must carry `data._blob`):

```bash
curl "http://localhost:3232/events/8f2c...1a?resolve=blob"
```

**200:** `{ "status": "success", "event": { … }, "record": { … }, "blob": { "hash": "…", "size": 4096, "sizeStr": "4.00 KB", "url": "/down/…", "retained_until": "2027-09-21T02:00:00Z", "protected": true } }` — `blob` is `null` when the linked blob is gone.

---

## `POST /up`

Store a binary blob, content-addressed by its sha256. Only `.bin` files accepted, and the head bytes are sniffed — content detected as text, HTML, images, or PDF is rejected even when renamed to `.bin`. Identical bytes dedupe to the same hash.

- **Content-Type:** `multipart/form-data`
- **Field name:** `file` (filename must end in `.bin`, case-insensitive)
- Saved as `<sha256>.bin` under `/data/blobs`
- Retention: size-weighted guarantee of **30 days at 512 MiB** up to **1 year at size 0** (`retention = min_age + (min_age - max_age) * (size/max_size - 1)^3`); after expiry the janitor evicts the blob. There is no total storage quota. Blobs linked via `_blob` from live records are never evicted.

```bash
curl -X POST -F "file=@save.bin" http://localhost:3232/up
```

**201** (new) or **200** (`duplicate: true`):

```json
{ "status": "success", "hash": "e3b0...85", "size": 4096, "url": "/down/e3b0...85", "duplicate": false }
```

Errors: `400` missing/empty part, `415` not a `.bin` file or sniffed non-binary content (`detected` names the MIME), `503` server busy.

To reference the blob from a record, publish with `"data": { …, "_blob": "<hash>" }` (see [`POST /records`](#post-records)).

---

## `GET /blobs`

List stored `.bin` binary blobs, ordered newest-first. Each entry carries its computed retention window.

```bash
curl "http://localhost:3232/blobs?limit=50&offset=0"
```

**200 OK:**

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
      "access_count": 2,
      "retention_secs": 31535337,
      "retained_until": "2027-09-21T09:00:00Z",
      "protected": true
    }
  ]
}
```

---

## `GET /down/{hash}`

Serve a stored blob by sha256. `HEAD` is also allowed. Every hit refreshes LRU recency.

- `{hash}` must be 64 hex chars, case-insensitive (`400` otherwise)
- Responses send `Content-Type: application/octet-stream`, `Content-Disposition: inline; filename="<hash>.bin"`, `ETag: "<hash>"`, `X-Content-Type-Options: nosniff`, and immutable-friendly `Cache-Control`
- Unknown or evicted hashes return `404` (stale DB rows are dropped)

```bash
curl -O "http://localhost:3232/down/e3b0...85"
```

---

## `GET /metrics`

Prometheus text exposition (`text/plain; version=0.0.4`). Gauges refresh on each scrape.

```bash
curl http://localhost:3232/metrics
```

| Metric | Type | Meaning |
| :----- | :--- | :------ |
| `originless_http_requests_total{path=…}` | counter | Requests by path (`/records/{id}` and `/down/{hash}` are label-normalized) |
| `originless_http_errors_total` | counter | Responses with status ≥ 400 |
| `originless_uploads_total` | counter | Successful `POST /up` uploads |
| `originless_upload_bytes_total` | counter | Bytes stored via `POST /up` |
| `originless_storage_used_bytes` | gauge | Tracked blob bytes (refreshed per scrape) |
| `originless_sse_clients` | gauge | Active `records/stream` connections |
| `originless_sse_dropped_total` | counter | Records dropped because a consumer's buffer was full (too-slow consumer) |

---

## UI pages (not JSON)

| Path | Notes |
| :--- | :---- |
| `GET /` | Node dashboard (Quick Records + Binary Blobs) |
| `GET /agent` | `301` → `/agent.txt` |
| `GET /agent.txt` | Plain-text Agent Skill guide (`text/plain`) |
| `GET /agent.html` | `301` → `/agent.txt` (legacy) |
| `GET /library.html` | `301` → `/` |

---

## Shared upload errors

| Status | When |
| :----- | :--- |
| `400` | Missing `file` part, empty filename, or malformed multipart |
| `415` | Filename does not end in `.bin`, or content sniffed as text/HTML/image/PDF |
| `503` | 3 uploads already in flight (`"Server busy"`) |
