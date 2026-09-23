# Originless API

Base URL: `http://localhost:3232`

The application is served by the Originless container. Uploads are added to the
embedded IPFS node with pinning disabled.

## Routes

| Method | Route | Description |
| --- | --- | --- |
| `GET` | `/` | HTML home page |
| `GET` | `/stats` | IPFS repository statistics as JSON |
| `GET` | `/healthz` | Container health status as JSON |
| `POST` | `/up` | Upload one file, or automatically upload multiple files/a folder |
| `POST` | `/upf` | Upload a folder and return the folder root CID |
| `GET` | `/down/{cid}` | Download content available in the local IPFS repository |
| `POST` | `/events` | Publish a signed event |
| `GET` | `/events` | Query signed events |
| `GET` | `/events/{id}` | Retrieve one signed event |
| `GET` | `/events/stream` | Stream matching events over SSE |

## `GET /stats`

Returns the Kubo repository statistics:

```json
{
  "NumObjects": 12,
  "RepoPath": "/data/ipfs",
  "SizeStat": {
    "RepoSize": 2048,
    "StorageMax": 10000000000
  },
  "Version": "fs-repo@18"
}
```

## `GET /healthz`

```json
{
  "status": "ok"
}
```

## Events

Events are immutable JSON documents signed by the client with Ed25519. Events
are limited to 8 KiB and a maximum TTL of one year.

The signed message is:

```text
owner:collection:created_at:expires_at:canonical_data:blob:labels_csv
```

`canonical_data` is compact JSON with recursively sorted object keys. The event
ID is the lowercase SHA-256 hex digest of that message, and `sig` is the
Ed25519 signature of the 32-byte digest encoded as 128 hex characters.

### `POST /events`

Send an `application/json` event body:

```json
{
  "owner": "ed25519:<64-hex-public-key>",
  "collection": "chat",
  "created_at": 1758420000,
  "expires_at": 1789956000,
  "data": {"user": "alice", "message": "Hello world!"},
  "labels": ["room:lobby"],
  "sig": "<128-hex-signature>"
}
```

The optional `blob` field is a SHA-256 hex reference. A new event returns
`201 Created`; replaying the same signed event returns `200 OK` with
`duplicate: true`.

### `GET /events`

Query live events with `collection`, `label`, `owner`, `since`, `until`,
`search`, `blob`, `limit`, and `cursor`. Results are newest first. The default
limit is 50 and the maximum is 100. Pass the returned `next_cursor` unchanged
to fetch the next page.

### `GET /events/{id}`

Returns the event JSON by its server-computed ID. Expired events are hidden
unless `include_expired=true` is supplied.

### `GET /events/stream`

Streams newly published matching events as Server-Sent Events. Filters include
`collection` and `label` (the other event query filters are also accepted).

Events are currently held in memory and are lost when the app process stops.

## `POST /up`

Send `multipart/form-data` with one or more file parts. A single file is added
as a file; multiple files, relative filenames, or directory parts are treated
as a folder.

```bash
curl -F "file=@hello.txt" http://localhost:3232/up
```

## `POST /upf`

Send the folder contents as multipart file parts. Include relative paths in
the multipart `filename` values to preserve the directory structure.

```bash
curl \
  -F "file=@folder/one.txt;filename=folder/one.txt" \
  -F "file=@folder/nested/two.txt;filename=folder/nested/two.txt" \
  http://localhost:3232/upf
```

## `GET /down/{cid}`

Serves any UnixFS content currently available in the local IPFS repository.
The route calls Kubo with offline mode enabled, so it will not fetch a missing
CID from the public network. If the content is not local, the route returns
`404` or `502` when the IPFS node cannot provide it.

The response is returned as a generic binary attachment; the original filename
and extension are not stored in the CID.

```bash
curl -OJ http://localhost:3232/down/<cid>
```

Because uploads are unpinned, content may disappear after IPFS garbage
collection. This route does not perform copyright or content-type detection.

## Upload response

Successful uploads return:

```json
{
  "cid": "bafy...",
  "size": 2048,
  "bytes": 2048,
  "name": "hello.txt",
  "extension": ".txt",
  "mime": "text/plain; charset=utf-8",
  "files": 1
}
```

- `cid` is the IPFS CID of the uploaded file or folder root.
- `size` is the size reported by IPFS for the returned root.
- `bytes` is the total number of uploaded file bytes.
- `extension` is the lowercase extension from the submitted filename.
- `mime` is derived from that extension; unknown extensions use `application/octet-stream`.

Uploads use `multipart/form-data` and are limited to 1 GiB of file data.
