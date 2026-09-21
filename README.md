<div align="center">

# Originless

**Zero-auth backend for the open web — signed events, binary blobs, live streams.**
No accounts. No API keys. One container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![Agent Prompt](https://img.shields.io/badge/agent prompt-agent.txt-1f6feb)](static/agent.txt)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/ISC)
[![Available on Umbrel App Store](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## Run in 10 seconds

```bash
docker run -d --name originless --restart unless-stopped \
  -p 3232:3232 -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

Podman works identically — replace `docker` with `podman`.

Open **http://localhost:3232** for the dashboard · Point agents at **[/agent.txt](static/agent.txt)** for the full machine-readable contract.

No storage cap, no upload size cap. Blobs persist until their size-weighted retention window elapses (30 days at 512 MiB → 1 year near 0 bytes), then the janitor evicts them. Data lives in the `/data` volume.

---

## 30-second tour

```bash
BASE=http://localhost:3232

# 1. Health: blob / event counts + live SSE subscribers
curl $BASE/status

# 2. Drop a binary blob (filename MUST end in .bin), get its content address
printf '\x00\x01\x02\x03binary-data' > save.bin
curl -X POST -F "file=@save.bin" $BASE/up
# -> {"status":"success","hash":"<sha256>","url":"/down/<sha256>"}

# 3. Download it back from any machine
curl -O $BASE/down/<sha256>

# 4. Query signed events (see "Sign once" below for publishing)
curl "$BASE/events?collection=chat&label=room:lobby&limit=20"

# 5. Live-tail them (Server-Sent Events, no WebSocket needed)
curl -N "$BASE/events/stream?collection=chat&label=room:lobby"
```

---

## Minimal API

Two primitives, no auth. Identity is an Ed25519 keypair you hold — the server only verifies.

### Events — signed JSON docs (8 KB max, TTL ≤ 1 year)

| Method | Endpoint | What it does |
| :--- | :--- | :--- |
| `POST` | `/events` | Publish a signed event. `201` created, `200` duplicate (same payload = same id), `401` bad sig. Alias: `/records` |
| `GET` | `/events?collection=&label=&owner=&since=&until=&search=&limit=&cursor=` | Query newest-first. `limit` 1–100 (default 50). Expired hidden unless `?include_expired=true` |
| `GET` | `/events/{id}` | Fetch one. Add `?resolve=blob` to inline a linked blob's metadata |
| `GET` | `/events/stream?collection=&label=&owner=&search=` | Live SSE feed. Filters match publish filters. Keepalive `: keepalive` every 15 s |

Event body (never send `id` — server computes it):

```json
{
  "owner": "ed25519:<64 hex pubkey>",
  "collection": "chat",
  "created_at": 1758420000,
  "expires_at": 1789956000,
  "data": { "text": "gg" },
  "labels": ["room:lobby"],
  "sig": "<128 hex chars>"
}
```

### Blobs — opaque `.bin` files, content-addressed by SHA-256

| Method | Endpoint | What it does |
| :--- | :--- | :--- |
| `POST` | `/up` | Upload `multipart/form-data` field `file`, filename must end `.bin`. Text / images / PDFs rejected (415) even if renamed. `201` new, `200` duplicate |
| `GET` | `/down/{hash}` | Download by 64-char hex SHA-256 (`HEAD` also works). `ETag`, `nosniff`, immutable cache |
| `GET` | `/blobs?limit=50&offset=0` | List newest-first |

### System

| Method | Endpoint | What it does |
| :--- | :--- | :--- |
| `GET` | `/status` | Node snapshot: blob / event counts, SSE clients |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/agent.txt` | Plain-text contract for AI agents (`/agent` redirects here) |

Link a blob to an event: upload first, then set `data._blob = "<sha256>"`. The hash is covered by the signature, so the link is tamper-proof. Live (unexpired) events protect their blob from janitor eviction.

---

## Sign once, publish anywhere (Python)

Signing is the only "tricky" bit. `id = sha256(owner:collection:created_at:expires_at:canonical(data):labels)` where `canonical(data)` is sorted-keys, whitespace-free JSON. Sign the **raw 32 hash bytes**, not the hex string.

```python
import json, time, hashlib
from nacl.signing import SigningKey  # pip install pynacl

BASE = "http://localhost:3232"
sk = SigningKey.generate()
owner = "ed25519:" + sk.verify_key.encode().hex()

collection = "chat"
created = int(time.time())
expires = created + 3600
data = {"room": "lobby", "text": "hello"}
labels = ["room:lobby"]

canonical = json.dumps(data, sort_keys=True, separators=(",", ":"))
id_bytes = hashlib.sha256(
    f"{owner}:{collection}:{created}:{expires}:{canonical}:{','.join(labels)}".encode()
).digest()
sig = sk.sign(id_bytes).signature.hex()

import requests
r = requests.post(f"{BASE}/events", json={
    "owner": owner, "collection": collection,
    "created_at": created, "expires_at": expires,
    "data": data, "labels": labels, "sig": sig,
})
print(r.status_code, r.json())  # 201 {"status":"success","id":"..."}
```

Node equivalent: `canonical = JSON.stringify(sortKeys(data))`, `idBytes = sha256(...)`, `sig = crypto.sign(null, idBytes, privKey)`. Same field layout.

---

## Use cases

### 1. Ephemeral chat rooms (browser, no WebSocket server)

Topic filtering via `labels`, auto-expiry via `expires_at`, zero-polling sync via `EventSource`. Publish with the snippet above, subscribe like this:

```javascript
// Live-tail one room
const room = new EventSource(
  "http://localhost:3232/events/stream?collection=chat&label=room:lobby"
);
room.addEventListener("event", (e) => {
  const ev = JSON.parse(e.data); // {id, owner, collection, data, labels, ...}
  console.log(`[${ev.data.user ?? ev.owner.slice(-6)}]: ${ev.data.text}`);
});

// Catch up after a disconnect — same filter, plain GET
const res = await fetch(
  "http://localhost:3232/events?collection=chat&label=room:lobby&limit=50"
);
const { events } = await res.json();
```

Why Originless: no socket server to run, no auth to provision, messages evaporate when `expires_at` passes.

### 2. AI agent task bus (zero key provisioning)

Point any agent at `/agent.txt` — it documents every endpoint and rule in plain text. Agents generate their own keypair, publish results as signed events, and stream each other's output:

```bash
BASE=http://localhost:3232

# Agent publishes a completed task (sign as above; body ≤ 8 KB)
curl -X POST $BASE/events -H "Content-Type: application/json" -d '{
  "owner": "ed25519:<agent pubkey>",
  "collection": "agent-tasks",
  "created_at": 1758420000, "expires_at": 1789956000,
  "data": {"task_id": "audit-42", "result": "passed", "confidence": 0.98},
  "labels": ["status:completed", "agent:scanner-01"],
  "sig": "<sign as above>"
}'

# Coordinator live-tails completions
curl -N "$BASE/events/stream?collection=agent-tasks&label=status:completed"
```

Pattern: one `collection` per queue, one `label` per state (`status:open`, `status:completed`). Keep payloads small; put large artifacts in blobs and link via `data._blob`.

### 3. Game saves & binary attachments (events + blobs)

Store the raw bytes once (deduplicated by SHA-256), link them from a signed manifest. The live event shields the blob from eviction; fetch both in one round-trip with `?resolve=blob`:

```bash
# 1. Upload the save (must be .bin)
curl -X POST -F "file=@save.bin" $BASE/up
# -> {"status":"success","hash":"e3b0...85","url":"/down/e3b0...85"}

# 2. Publish a signed manifest pointing at it
curl -X POST $BASE/events -H "Content-Type: application/json" -d '{
  "owner": "ed25519:<player pubkey>",
  "collection": "gamesaves",
  "created_at": 1758420000, "expires_at": 1789956000,
  "data": {"slot": 1, "level": 34, "score": 9200, "_blob": "e3b0...85"},
  "labels": ["slot:1", "player:hero"],
  "sig": "<sign as above>"
}'

# 3. Load save + blob URL together
curl "$BASE/events/<event-id>?resolve=blob"
# -> {"event": {...}, "blob": {"hash": "...", "url": "/down/...", ...}}
```

Same shape works for firmware images, SQLite snapshots, model weights — anything opaque.

### 4. CLI drops & IoT telemetry (one-liners)

```bash
# Instant expiring drop from any server
curl -X POST -F "file=@dump.bin" $BASE/up
curl -O $BASE/down/<sha256>

# Signed device reading (sign on-device, fire-and-forget)
curl -X POST $BASE/events -H "Content-Type: application/json" -d '{
  "owner": "ed25519:<device pubkey>",
  "collection": "alerts",
  "created_at": 1758420000, "expires_at": 1758506400,
  "data": {"temp_c": 91.4, "rack": "a3"},
  "labels": ["severity:critical", "rack:a3"],
  "sig": "<sign as above>"
}'

# Ops dashboard tails only critical alerts live
curl -N "$BASE/events/stream?collection=alerts&label=severity:critical"
```

Why Originless: devices need no accounts or rotated tokens — the keypair *is* the identity, and `collection` + `label` filters replace per-device topics.

---

## Query cheat sheet

```bash
# Newest-first, filter by anything
curl "$BASE/events?collection=chat&label=room:lobby&limit=20"
curl "$BASE/events?owner=ed25519:...&since=1758420000&until=1758506400"
curl "$BASE/events?search=timeout&limit=50&cursor=<created_at>:<id>"

# Health + metrics
curl $BASE/status
curl $BASE/metrics
```

Rules worth knowing: `collection` must match `^[a-z0-9/_-]{1,32}$`, ≤ 10 labels per event, `data` must be a JSON object, `created_at` tolerates 15 min future skew, `expires_at - created_at` ≤ 1 year. Same payload re-POSTed returns `200 duplicate` with the same id — safe to retry.
