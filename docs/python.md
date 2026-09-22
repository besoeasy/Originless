# Python Integration Guide

Originless requires no proprietary SDK or API keys. You can integrate directly using standard Python libraries or popular packages (`requests`, `cryptography` / `pynacl`).

---

## Prerequisites

```bash
pip install requests cryptography
```

---

## 1. Key Generation & Event Signing

Originless uses standard **Ed25519** signatures. The message format for signature verification is:
```text
owner:collection:created_at:expires_at:canonical(data):blob:labels
```

Here is a clean helper function for signing:

```python
import hashlib
import json
import time
from cryptography.hazmat.primitives.asymmetric import ed25519

def generate_keypair():
    """Generates an Ed25519 private key and its formatted owner string."""
    private_key = ed25519.Ed25519PrivateKey.generate()
    public_bytes = private_key.public_key().public_bytes_raw()
    owner = "ed25519:" + public_bytes.hex()
    return private_key, owner

def sign_event(private_key, owner, collection, data, labels=None, ttl_seconds=86400 * 30, blob=""):
    """Creates and cryptographically signs an Originless event payload."""
    if labels is None:
        labels = []
    
    created_at = int(time.time())
    expires_at = created_at + ttl_seconds
    
    # Canonical JSON (sorted keys, compact delimiters)
    canonical_data = json.dumps(data, sort_keys=True, separators=(",", ":"))
    
    # Message to sign
    sorted_labels = sorted(labels)
    msg = f"{owner}:{collection}:{created_at}:{expires_at}:{canonical_data}:{blob}:{','.join(sorted_labels)}"
    msg_hash = hashlib.sha256(msg.encode("utf-8")).digest()
    
    # Ed25519 signature
    sig = private_key.sign(msg_hash).hex()
    
    event = {
        "owner": owner,
        "collection": collection,
        "created_at": created_at,
        "expires_at": expires_at,
        "data": data,
        "labels": sorted_labels,
        "sig": sig,
    }
    if blob:
        event["blob"] = blob
        
    return event
```

---

## 2. Publishing an Event

```python
import requests

BASE_URL = "http://localhost:3232"

priv_key, owner = generate_keypair()

# Prepare event data
payload = {
    "title": "Autonomous Agent Report",
    "status": "completed",
    "metrics": {"duration_ms": 412, "items_processed": 50}
}

event = sign_event(
    private_key=priv_key,
    owner=owner,
    collection="reports",
    data=payload,
    labels=["agent:alpha", "status:ok"],
    ttl_seconds=86400 * 7 # 7 days
)

# Publish
resp = requests.post(f"{BASE_URL}/events", json=event)
print(f"Status: {resp.status_code}")
print(resp.json())
# Output: {'status': 'success', 'id': '...', 'stored_at': '...'}
```

---

## 3. Uploading an Event with Attached Binary Blob (Atomic Multipart)

To store binary files (e.g. tar archives, models, SQLite databases, binary dumps), hash the file first with SHA-256, sign an event linking to it, and send both in a single atomic request:

```python
import hashlib
import json
import requests

BASE_URL = "http://localhost:3232"
priv_key, owner = generate_keypair()

# 1. Read binary bytes and compute SHA-256
with open("dataset.tar.gz", "rb") as f:
    blob_bytes = f.read()

blob_hash = hashlib.sha256(blob_bytes).hexdigest()

# 2. Sign event with top-level blob pointer
event = sign_event(
    private_key=priv_key,
    owner=owner,
    collection="datasets",
    data={"filename": "dataset.tar.gz", "size": len(blob_bytes)},
    labels=["type:dataset"],
    blob=blob_hash
)

# 3. Post as multipart/form-data (event part FIRST)
files = [
    ("event", (None, json.dumps(event), "application/json")),
    ("blob", ("dataset.tar.gz", blob_bytes, "application/octet-stream"))
]

resp = requests.post(f"{BASE_URL}/events", files=files)
print(resp.json())
```

---

## 4. Querying Events & Downloading Blobs

```python
import hashlib
import requests

BASE_URL = "http://localhost:3232"

# 1. Query records
res = requests.get(f"{BASE_URL}/events", params={
    "collection": "reports",
    "label": "status:ok",
    "limit": 10
})
events = res.json().get("events", [])
for ev in events:
    print(f"[{ev['collection']}] {ev['id'][:12]}... : {ev['data']}")

# 2. Query event with linked blob metadata
event_id = events[0]["id"]
detail_res = requests.get(f"{BASE_URL}/events/{event_id}", params={"resolve": "blob"})
detail = detail_res.json()
print("Attached blob:", detail.get("blob"))

# 3. Download content-addressed blob by hash
blob_hash = detail.get("blob", {}).get("hash")
if blob_hash:
    blob_res = requests.get(f"{BASE_URL}/blob/{blob_hash}")
    # Verify integrity locally
    assert hashlib.sha256(blob_res.content).hexdigest() == blob_hash
    with open("downloaded_dataset.tar.gz", "wb") as f:
        f.write(blob_res.content)
    print("Downloaded and verified blob successfully.")
```

---

## 5. Streaming Live Events (Server-Sent Events)

Stream events live in Python without WebSockets or background daemons:

```python
import json
import requests

BASE_URL = "http://localhost:3232"

# Open SSE stream with chunked decoding
stream_url = f"{BASE_URL}/events/stream?collection=reports"
with requests.get(stream_url, stream=True) as resp:
    for line in resp.iter_lines():
        if line:
            decoded = line.decode("utf-8")
            if decoded.startswith("data: "):
                event = json.loads(decoded[6:])
                print(f"Live event received: {event['id']} -> {event['data']}")
```
