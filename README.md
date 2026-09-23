# Originless

Originless is a container-first Go service with an embedded IPFS node. It serves
a web dashboard on port `3232`, accepts IPFS uploads, and provides signed event
APIs.

## Run with Docker

```bash
docker run --rm --name originless \
  -p 3232:3232 \
  ghcr.io/besoeasy/originless:latest
```

Open [http://localhost:3232](http://localhost:3232) after the container starts.
The container is removed when it stops.

For local Podman development, use:

```bash
./podman.sh
```

See [`api.md`](api.md) for the HTTP API.
