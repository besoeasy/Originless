# Originless API

Base URL (local): http://localhost:3232

## Overview

- Responses are JSON unless otherwise noted.
- Authentication for pin management uses [Daku](https://www.npmjs.com/package/daku).
- Send the token in the `daku: <token>` header.

## Endpoints

### POST /upload
Upload a file directly from your local system.

**Request**

```bash
curl -X POST -F "file=@yourfile.pdf" http://localhost:3232/upload
```

**Response**

```json
{
  "status": "success",
  "cid": "QmX...",
  "url": "https://dweb.link/ipfs/QmX...?filename=yourfile.pdf",
  "size": 12345,
  "type": "application/pdf",
  "filename": "yourfile.pdf"
}
```

---

### POST /remoteupload
Download and upload content from any URL to IPFS.

**Request**

```bash
curl -X POST http://localhost:3232/remoteupload \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/image.png"}'
```

**Response**

```json
{
  "status": "success",
  "cid": "QmX...",
  "url": "https://dweb.link/ipfs/QmX...",
  "filename": "image.png",
  "size": 12345,
  "type": "image/png",
  "sourceUrl": "https://example.com/image.png",
  "timing": {
    "download_ms": 1234,
    "upload_ms": 5678,
    "total_ms": 6912
  },
  "timestamp": "2026-01-07T03:18:00.000Z"
}
```

---

## Pin Management (Auth Required)

Authentication is handled via [Daku](https://www.npmjs.com/package/daku). Send your Daku token in the `daku: <token>` header.

### Why Daku?

Daku provides **decentralized, self-sovereign authentication** without passwords, accounts, or server-side sessions:

- **No accounts required** - Users control their own cryptographic keys
- **Stateless authentication** - No session storage or cookies needed on the server
- **Proof of work** - Built-in spam protection through computational proof
- **Cryptographic verification** - Each request is signed with your private key
- **Nostr-compatible** - Uses the same secp256k1 keypairs as Nostr
- **Self-sovereign identity** - You own and control your authentication credentials
- **Perfect for P2P** - No centralized auth servers or databases required

Each token is cryptographically signed and includes proof-of-work, making it impossible to forge and costly to spam.

### Admin Web UI

For a user-friendly interface, visit `http://localhost:3232/admin.html` in your browser.

The admin panel provides:
- Automatic token generation from your private key
- Pin management (add, list, remove)
- Detailed pin information (size, type, status, creation date)
- Token storage in localStorage for convenience

Simply paste your private key (from console logs if auto-generated) and the token is automatically generated for API requests.

### Command Line Usage

Generate a new Daku key pair locally with Node:

```bash
node -e "const { generateKeyPair } = require('daku'); const keys = generateKeyPair(); console.log('publicKey:', keys.publicKey); console.log('privateKey:', keys.privateKey);"
```

Use your Daku tooling to create a token from the private key and send it in the `daku` header for the requests below.

### POST /pin/add
Add one or more CIDs to your pin list.

**Request**

```bash
curl -X POST http://localhost:3232/pin/add \
  -H "daku: <token>" \
  -H "Content-Type: application/json" \
  -d '{"cids": ["QmHash1...", "QmHash2..."]}'
```

---

### GET /pin/list
List your pinned CIDs with detailed information.

**Request**

```bash
curl -H "daku: <token>" http://localhost:3232/pin/list
```

**Response**

```json
{
  "success": true,
  "pins": [
    {
      "id": 1,
      "cid": "QmHash...",
      "size": 12345,
      "type": "file",
      "status": "pinned",
      "author": "02abc...",
      "created_at": 1738276800,
      "updated_at": 1738276800
    }
  ]
}
```

---

### POST /pin/remove
Remove a CID from your pin list.

**Request**

```bash
curl -X POST http://localhost:3232/pin/remove \
  -H "daku: <token>" \
  -H "Content-Type: application/json" \
  -d '{"cid": "QmHash..."}'
```

---

## Access Control

You can restrict access to authenticated endpoints (Pin Management) by whitelisting specific Daku public keys.

Set the `ALLOWED_USERS` environment variable with a comma-separated list of allowed public keys:

```bash
docker run -d ... \
  -e ALLOWED_USERS="public_key_1,public_key_2" \
  ...
```

If `ALLOWED_USERS` is not set, Originless generates a Daku key pair at startup, logs it to the console, and enables pin management for that public key.
