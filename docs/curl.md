# cURL & Shell Guide

Originless is designed to be operated entirely with standard Linux CLI utilities (`curl`, `openssl`, `jq`, `sha256sum`). There are no proprietary CLI tools, no API tokens, and no account registrations.

---

## 1. Node Health & Telemetry

Check node availability, memory vitals, storage stats, and P2P mesh status:

```bash
BASE="http://localhost:3232"

# Inspect JSON status
curl -s "$BASE/status" | jq .

# Inspect Prometheus metrics
curl -s "$BASE/metrics"
```

---

## 2. Publishing Signed JSON Events

Events are verified using standard **Ed25519** signatures. You hold the private key; Originless verifies the signature and validates TTL.

### Step 1: Generate an Ed25519 Keypair (Once)
```bash
# Generate private key
openssl genpkey -algorithm ed25519 -out key.pem

# Extract raw 32-byte public key as hex
PUBHEX=$(openssl pkey -in key.pem -pubout -outform DER | tail -c 32 | od -An -tx1 | tr -d ' \n')
OWNER="ed25519:$PUBHEX"
echo "Your Owner ID: $OWNER"
```

### Step 2: Sign and Publish an Event
```bash
COL="notes"
CREATED=$(date +%s)
EXPIRES=$((CREATED + 86400 * 30)) # 30 days TTL (max 1 year)
DATA='{"title":"Meeting Notes","text":"Zero-auth storage works!"}'
CANONICAL=$(printf '%s' "$DATA" | jq -c -S .)
BLOB="" # empty string for blob-less events
LABELS_CSV="env:prod,topic:meeting"
LABELS_JSON='["env:prod","topic:meeting"]'

# Format ID message: owner:collection:created:expires:canonical(data):blob:labels
ID_INPUT="${OWNER}:${COL}:${CREATED}:${EXPIRES}:${CANONICAL}:${BLOB}:${LABELS_CSV}"

# Compute SHA-256 and sign with Ed25519
printf '%s' "$ID_INPUT" | openssl dgst -sha256 -binary > id.hash
openssl pkeyutl -sign -inkey key.pem -rawin -in id.hash -out sig.raw
SIG=$(od -An -tx1 sig.raw | tr -d ' \n')

# Build event payload
jq -n \
  --arg o "$OWNER" \
  --arg c "$COL" \
  --argjson cr "$CREATED" \
  --argjson ex "$EXPIRES" \
  --argjson d "$CANONICAL" \
  --argjson l "$LABELS_JSON" \
  --arg s "$SIG" \
  '{owner:$o, collection:$c, created_at:$cr, expires_at:$ex, data:$d, labels:$l, sig:$s}' > event.json

# Post to node
curl -X POST "$BASE/events" \
  -H "Content-Type: application/json" \
  -d @event.json
```

**Response (`201 Created` or `200 Duplicate`):**
```json
{
  "status": "success",
  "id": "e4a28f...",
  "stored_at": "2026-09-22 20:00:00"
}
```

---

## 3. Uploading Binary Blobs & Events (Atomic Multipart)

Blobs are stored under the **SHA-256** of their binary contents. You publish the event and the binary bytes together in a single atomic request (with the `event` part **first**):

```bash
# 1. Prepare your binary file
tar -czf backup.tar.gz ./my-data
FILE="backup.tar.gz"

# 2. Compute the content-address hash
BHASH=$(sha256sum "$FILE" | cut -d' ' -f1)

# 3. Create and sign an event with top-level "blob": "<sha256>"
COL="backups"
CREATED=$(date +%s)
EXPIRES=$((CREATED + 86400 * 30))
DATA=$(jq -c -S -n --arg name "$FILE" --arg size "$(wc -c < "$FILE" | tr -d ' ')" '{name:$name, size:($size|tonumber)}')
CANONICAL="$DATA"
LABELS_CSV="archive:backup"
LABELS_JSON='["archive:backup"]'

# Note: the blob hash is included in the signed message
ID_INPUT="${OWNER}:${COL}:${CREATED}:${EXPIRES}:${CANONICAL}:${BHASH}:${LABELS_CSV}"
printf '%s' "$ID_INPUT" | openssl dgst -sha256 -binary > id.hash
openssl pkeyutl -sign -inkey key.pem -rawin -in id.hash -out sig.raw
SIG=$(od -An -tx1 sig.raw | tr -d ' \n')

jq -n \
  --arg o "$OWNER" \
  --arg c "$COL" \
  --argjson cr "$CREATED" \
  --argjson ex "$EXPIRES" \
  --argjson d "$CANONICAL" \
  --arg b "$BHASH" \
  --argjson l "$LABELS_JSON" \
  --arg s "$SIG" \
  '{owner:$o, collection:$c, created_at:$cr, expires_at:$ex, data:$d, blob:$b, labels:$l, sig:$s}' > event.json

# 4. Upload event + raw binary bytes in one request
curl -X POST "$BASE/events" \
  -F "event=@event.json;type=application/json" \
  -F "blob=@$FILE;type=application/octet-stream"
```

The server cryptographically validates the signature before staging any bytes, hashes the incoming bytes, and commits both atomically.

---

## 4. Downloading Blobs

Stored bytes can be downloaded from any node by their SHA-256 hash:

```bash
# Download and save with remote filename
curl -OJ "$BASE/blob/$BHASH"

# Check caching and ETag headers
curl -I "$BASE/blob/$BHASH"

# List stored blobs
curl -s "$BASE/blobs?limit=20" | jq .
```

---

## 5. Querying Events

Query events using query parameters:

```bash
# Filter by collection
curl -s "$BASE/events?collection=notes&limit=10" | jq .

# Filter by label
curl -s "$BASE/events?label=env:prod" | jq .

# Filter by owner
curl -s "$BASE/events?owner=$OWNER" | jq .

# Filter by attached blob
curl -s "$BASE/events?blob=$BHASH" | jq .

# Full-text search across payload data
curl -s "$BASE/events?search=Meeting" | jq .

# Keyset pagination (using next_cursor)
curl -s "$BASE/events?cursor=1758420000:e4a28f...&limit=25" | jq .

# Fetch single event by ID (with ?resolve=blob to inline blob metadata)
curl -s "$BASE/events/<EVENT_ID>?resolve=blob" | jq .
```

---

## 6. Real-Time Streaming (Server-Sent Events)

Stream new events in real time without WebSockets or polling:

```bash
# Follow all new events live
curl -N "$BASE/events/stream"

# Follow new events in a specific collection
curl -N "$BASE/events/stream?collection=notes"

# Follow new events matching a label
curl -N "$BASE/events/stream?label=env:prod"
```
