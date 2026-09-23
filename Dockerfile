# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder

ARG VERSION=1.0.0
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 \
    GOOS="${TARGETOS:-$(go env GOOS)}" \
    GOARCH="${TARGETARCH:-$(go env GOARCH)}" \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/originless .

FROM alpine:latest

ARG VERSION=1.0.0
LABEL org.opencontainers.image.title="Originless" \
      org.opencontainers.image.version="${VERSION}"

# Kubo is Alpine's package name for the IPFS implementation and provides
# the `ipfs` command-line executable.
RUN apk add --no-cache kubo ca-certificates \
    && ipfs --version \
    && addgroup -S originless \
    && adduser -S -G originless -h /app originless \
    && mkdir -p /data/ipfs \
    && chown -R originless:originless /app /data

WORKDIR /app
COPY --from=builder /out/originless /usr/local/bin/originless
COPY docker-entrypoint.sh /app/entrypoint.sh

ENV IPFS_PATH=/data/ipfs \
    PORT=3232 \
    IPFS_API_URL=http://127.0.0.1:5001

VOLUME ["/data"]

# Originless web UI, IPFS swarm transport, and the local IPFS RPC API.
EXPOSE 3232/tcp 4001/tcp 4001/udp 5001/tcp

STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:3232/healthz || exit 1

USER originless
ENTRYPOINT ["/app/entrypoint.sh"]
