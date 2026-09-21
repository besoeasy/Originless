<div align="center">

# Originless

**Zero-auth backend for the open web — signed events, binary blobs, live P2P sync.**  
No accounts. No API keys. Zero complex setup. One single container.

[![Docker](https://img.shields.io/badge/docker-ghcr.io-0db7ed?logo=docker&logoColor=white)](https://ghcr.io/besoeasy/originless)
[![P2P Mesh](https://img.shields.io/badge/p2p-BitTorrent%20DHT-orange)](https://github.com/besoeasy/originless)
[![Agent Contract](https://img.shields.io/badge/agent%20contract-agent.txt-1f6feb)](static/agent.txt)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](https://opensource.org/ISC)
[![Available on Umbrel](https://apps.umbrel.com/api/app/originless/badge-light.svg)](https://apps.umbrel.com/app/originless)

</div>

---

## What Originless Replaces

Instead of running and configuring half a dozen microservices, API keys, and databases, Originless replaces them all with **two simple primitives** (signed JSON events + content-addressed binary blobs) running in a single binary:

| App / Service | What Originless Does Instead | Why It's Better |
| :--- | :--- | :--- |
| **ntfy / Pusher** | Real-time live pub/sub streams via Server-Sent Events (`GET /events/stream?label=...`). | No WebSockets, no server daemons. Subscribe directly in browser `EventSource` or `curl -N`. |
| **Sentry** | Fire-and-forget signed telemetry and error logs (`POST /events`) with labels and automatic TTL expiry. | Zero auth tokens to provision or rotate. Filter by error tag or live-tail critical alerts. |
| **Nostr Relays** | Cryptographically signed Ed25519 events with tamper-proof IDs and DHT swarm sync. | No complex NIP protocols, no paid relay operators. Standard HTTP REST + SSE. |
| **0x0.st / Pastebin** | Content-addressed ephemeral binary file drops (`POST /up`, `GET /down/<sha256>`). | Deduplicated by SHA-256. Size-weighted retention auto-evicts old files cleanly. |
| **Redis Pub/Sub** | Ephemeral JSON documents with self-expiring TTLs and real-time streaming. | Pure SQLite WAL storage with microsecond query latency and zero RAM bloat. |
| **S3 / Object Store** | Content-addressed `.bin` storage with streaming downloads and checksum verification. | Zero IAM policies or bucket configuration. Upload once, verify everywhere. |

---

## Run in 10 seconds

Zero configuration. One command starts your node, maps router ports via UPnP, joins the BitTorrent P2P mesh, and starts syncing automatically:

### Docker
```bash
docker run -d --name originless --restart unless-stopped \
  -p 3232:3232 -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

### Podman
```bash
podman run -d --name originless --restart unless-stopped \
  -p 3232:3232 -v originless-data:/data \
  ghcr.io/besoeasy/originless:latest
```

That's it! Your node is live at **http://localhost:3232**.

### Mesh Network Options

* **Public Mesh (Default)**: No extra flags needed. By default, `NETWORK_ID` is `originless` — your nodes immediately discover each other across different PCs or cloud servers over BitTorrent Mainline DHT and sync events and blobs.
* **Private Mesh**: Add `-e NETWORK_ID=my-project` on your containers to create an isolated private swarm.
* **Standalone / Local Only**: Add `-e NETWORK_ID=off` (or `none`) to disable P2P sync and run strictly offline.

* **Dashboard**: Open **http://localhost:3232** in your browser.
* **AI Agents**: Point LLMs or agents at **[/agent.txt](static/agent.txt)** for the complete machine-readable contract.

---

## Automatic P2P Mesh (Torrent Network)

Originless is built for zero-config Docker and Podman deployments. With P2P active (`NETWORK_ID=originless` by default):

1. **BitTorrent Mainline DHT (BEP 5)**: Nodes announce themselves to the global BitTorrent DHT swarm under `sha1("originless:" + NETWORK_ID)`.
2. **Auto UPnP & NAT-PMP Port Forwarding**: On startup, Originless automatically maps port 3232 on your home or office Wi-Fi router.
3. **Peer Exchange (PEX)**: Connected peers share their peer lists, creating a resilient, fully connected swarm.
4. **Local Service Discovery (BEP 14 LSD)**: Nodes on the same LAN or Wi-Fi discover each other via UDP multicast and sync at local wire speed.
5. **Bloom Filter Set Reconciliation**: Upon connection, nodes exchange 1% false-positive Bloom filters to transfer missing records and binary blobs without redundant bandwidth.

---

## 30-Second Tour

```bash
BASE=http://localhost:3232

# 1. Health check: active events, blobs, and connected P2P peers
curl $BASE/status

# 2. Drop a binary blob (must end in .bin), get its SHA-256 content address
head -c 64 /dev/urandom > data.bin
curl -X POST -F "file=@data.bin" $BASE/up
# -> {"status":"success","hash":"<sha256>","url":"/down/<sha256>"}

# 3. Download it back from any machine in the swarm
curl -O $BASE/down/<sha256>

# 4. Query signed events (filtered by collection or label)
curl "$BASE/events?collection=chat&label=room:general&limit=20"

# 5. Live stream events (Server-Sent Events — no WebSocket required)
curl -N "$BASE/events/stream?collection=chat&label=room:general"
```

---

## The Two Primitives

### 1. Events — Signed JSON Documents (8 KB max, TTL ≤ 1 year)
Events are immutable, cryptographically signed JSON documents. You own the private key; Originless verifies the Ed25519 signature and stores the event.

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `POST` | `/events` | Publish a signed event (`201` created, `200` duplicate). Alias: `/records` |
| `GET` | `/events?collection=&label=&owner=&since=&until=&search=&limit=&cursor=` | Query newest-first. Expired events hidden by default. |
| `GET` | `/events/{id}` | Fetch a single event. Add `?resolve=blob` to inline linked blob metadata. |
| `GET` | `/events/stream?collection=&label=&owner=&search=` | Real-time Server-Sent Events (SSE) feed. |

```json
{
  "owner": "ed25519:<64 hex pubkey>",
  "collection": "alerts",
  "created_at": 1758420000,
  "expires_at": 1789956000,
  "data": { "service": "api", "error": "database connection timeout" },
  "labels": ["severity:critical", "env:prod"],
  "sig": "<128 hex chars>"
}
```

### 2. Blobs — Opaque Binary Files (`.bin`, Content-Addressed)
Upload raw binary bytes. Originless verifies the SHA-256 checksum and serves it with immutable caching headers.

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `POST` | `/up` | Upload `multipart/form-data` with field `file` (`.bin` only). Plaintext/HTML rejected. |
| `GET` | `/down/{hash}` | Download by SHA-256 hash. Supports `HEAD`, `ETag`, and byte ranges. |
| `GET` | `/blobs?limit=50&offset=0` | List stored blobs sorted by newest upload. |

* **Retention**: Size-weighted retention policy (30 days at 512 MiB → 1 year near 0 bytes).
* **Link to Events**: Add `data._blob = "<sha256>"` inside any event. As long as the signed event is unexpired, the janitor will never evict its blob.

---

## Showcase: Replacing Common Architectures

### 1. Replacing `ntfy` / Pusher (Browser Push & Alerts)
Subscribe to notifications directly in client-side JavaScript with zero WebSocket overhead:

```javascript
// Listen to room alerts live
const feed = new EventSource("http://localhost:3232/events/stream?collection=alerts&label=env:prod");

feed.addEventListener("event", (e) => {
  const alert = JSON.parse(e.data);
  console.log(`[ALERT] ${alert.data.service}: ${alert.data.error}`);
});
```

### 2. Replacing `Sentry` (Zero-Auth Crash Telemetry)
Devices, background workers, and scripts publish errors using their local Ed25519 key without credential provisioning:

```bash
curl -X POST http://localhost:3232/events -H "Content-Type: application/json" -d '{
  "owner": "ed25519:<worker pubkey>",
  "collection": "crashes",
  "created_at": 1758420000, "expires_at": 1759024800,
  "data": {"error": "Out of memory", "stack": "main.go:42", "node": "worker-04"},
  "labels": ["env:prod", "service:indexer"],
  "sig": "<sign raw sha256 bytes with private key>"
}'
```

### 3. Replacing `0x0.st` / Pastebin (Temporary File Drops)
Upload binary dumps, SQLite databases, firmware snapshots, or compressed bundles:

```bash
# Upload
curl -X POST -F "file=@backup.bin" http://localhost:3232/up
# -> {"status":"success","hash":"a8b3...","url":"/down/a8b3..."}

# Download from anywhere
curl -O http://localhost:3232/down/a8b3...
```

### 4. Replacing Nostr Relays (Social Feeds & Sovereign Identities)
Publish signed public posts. Any node running the same `NETWORK_ID` receives and verifies the post over the P2P swarm:

```bash
# Fetch latest posts in public timeline
curl "http://localhost:3232/events?collection=timeline&limit=25"

# Tail new posts live
curl -N "http://localhost:3232/events/stream?collection=timeline"
```

### 5. Multi-Agent Swarm Bus (`/agent.txt`)
AI coding agents and autonomous bots can discover the entire API spec at `/agent.txt`, generate keypairs on the fly, and coordinate distributed tasks across nodes:

```bash
# Agent streams pending tasks
curl -N "http://localhost:3232/events/stream?collection=agent-tasks&label=status:pending"
```

---

## Signing an Event (Python Example)

Event IDs are deterministic:
```text
id = sha256(owner + ":" + collection + ":" + created_at + ":" + expires_at + ":" + canonical(data) + ":" + labels.join(","))
```
Sign the raw 32 SHA-256 hash bytes using Ed25519:

```python
import json, time, hashlib, requests
from nacl.signing import SigningKey  # pip install pynacl

sk = SigningKey.generate()
owner = "ed25519:" + sk.verify_key.encode().hex()

created = int(time.time())
expires = created + 86400  # 24 hour TTL
data = {"status": "ok", "message": "All systems operational"}
labels = ["env:prod", "team:ops"]

canonical = json.dumps(data, sort_keys=True, separators=(",", ":"))
payload = f"{owner}:status:{created}:{expires}:{canonical}:{','.join(labels)}"
id_bytes = hashlib.sha256(payload.encode()).digest()
sig = sk.sign(id_bytes).signature.hex()

res = requests.post("http://localhost:3232/events", json={
    "owner": owner,
    "collection": "status",
    "created_at": created,
    "expires_at": expires,
    "data": data,
    "labels": labels,
    "sig": sig,
})
print(res.status_code, res.json())
```

---

## API Summary

| Endpoint | Method | Purpose |
| :--- | :--- | :--- |
| `GET /status` | `GET` | Health, event/blob counts, active SSE clients, and P2P mesh status |
| `POST /events` | `POST` | Publish signed event (`/records` alias) |
| `GET /events` | `GET` | Query events with filtering (`collection`, `label`, `owner`, `since`, `until`, `cursor`) |
| `GET /events/{id}` | `GET` | Fetch event by ID (`?resolve=blob` to inline linked blob) |
| `GET /events/stream` | `GET` | Real-time Server-Sent Events stream |
| `POST /up` | `POST` | Upload `.bin` binary file |
| `GET /down/{hash}` | `GET` | Download `.bin` binary file (`HEAD` supported) |
| `GET /blobs` | `GET` | List stored blobs |
| `GET /metrics` | `GET` | Prometheus telemetry metrics |
| `GET /agent.txt` | `GET` | Machine-readable contract for AI agents |
