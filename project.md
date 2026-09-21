# Originless — project basics

Originless is a zero-auth backend: no logins, no sessions. Identity is an
ed25519 keypair held by the client; the server only verifies signatures.

## Core primitives

- **Event** (legacy alias: Record) — a small signed JSON object: `owner`, `collection`, `labels`,
  `data`, `created_at`, `expires_at`, `sig`. The server computes `id` from a
  canonical hash of those fields, so republishing the same payload yields the
  same id and is idempotent (`200 duplicate`, never a second `201`). TTL is
  capped at 1 year (31,536,000 s).
- **Blob** — an opaque binary addressed by its sha256 (`<hash>.bin`).
  Uploads must be `.bin` and pass a binary sniff check; identical bytes dedupe
  to the same hash. An event can link a blob via `data._blob`; the hash is
  covered by the event signature.
- **Stream** — `GET /events/stream` is SSE: new events matching
  `owner/collection/label/search` are fanned out to subscribers with a
  15 s keepalive. Slow consumers are skipped (counted, not blocking).

## Request lifecycle

1. **Publish** (`POST /events`, alias `/records`): validate body size/TTL (max 1y) → verify ed25519
   signature → `INSERT OR IGNORE` record + labels in one transaction →
   broadcast to matching SSE subscribers only when newly created.
   Responses: `201` created, `200` duplicate, `401` bad sig, `413` oversize.
2. **Read** (`GET /events`, `GET /events/{id}`, alias `/records`): newest-first, expired
   hidden unless `include_expired=true`. List pagination is a keyset cursor
   `<created_at>:<id>` in `next_cursor` (numeric offsets still accepted).
3. **Upload** (`POST /up`, max 3 concurrent): stream to an `up-*` temp file,
   sniff content, rename to `<sha256>.bin`, upsert DB row.
4. **Download** (`GET /down/{hash}`, `HEAD` allowed): serve bytes, refresh LRU
   recency; unknown/evicted hashes are `404` and stale DB rows are dropped.
5. **Janitor** (hourly + startup): size-weighted blob retention (30 d at
   512 MiB → 1 y at size 0), eviction of blobs whose retention has expired,
   expiry purge of events, hash re-verification, and sweep of crashed
   `up-*` temps. Blobs linked from live events are never evicted.

## Storage and limits

- SQLite (`/data/originless.db`), WAL + `synchronous=NORMAL`, one connection.
- Blobs live in `/data/blobs`; no total pool cap and no per-file size cap —
  blobs persist until their size-weighted retention elapses. Events capped
  at 8 KB and 1 year TTL.
- SSE capped at `SSE_MAX_SUBSCRIBERS` (default 256, `503` beyond); each stream
  frame carries a write deadline so stuck clients are disconnected, not leaked.
- No read/write server timeouts (slow uploads and long streams survive);
  header reads and keep-alive idle stay bounded.

## Observability

- `GET /status`: storage policy, blob/record counts, live `sse` clients/drops.
- `GET /metrics`: Prometheus text, incl. `originless_sse_clients` and
  `originless_sse_dropped_total`.
- Full contract in `api.md`; run with `go run .` (listens on `:3232`).
