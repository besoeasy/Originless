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
List your pinned CIDs.

**Request**

```bash
curl -H "daku: <token>" http://localhost:3232/pin/list
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

If `ALLOWED_USERS` is not set, **any** valid Daku user can access the pin management endpoints (though they only see their own pins).
