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

## Upload response

Successful uploads return:

```json
{
  "cid": "bafy...",
  "size": 2048,
  "bytes": 2048,
  "name": "hello.txt",
  "files": 1,
  "pinned": false
}
```

- `cid` is the IPFS CID of the uploaded file or folder root.
- `size` is the size reported by IPFS for the returned root.
- `bytes` is the total number of uploaded file bytes.
- `pinned` is always `false` for uploads made through these endpoints.

Uploads use `multipart/form-data` and are limited to 1 GiB of file data.
