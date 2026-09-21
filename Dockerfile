FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /originless .

FROM alpine:latest

ENV STORAGE_MAX=100GB
ENV PIN_EXPIRY_DAYS=30
ENV ENABLE_GATEWAY=false
ENV IPFS_ROUTING=dhtclient
ENV IPFS_PROFILE=lowpower

RUN apk add --no-cache ca-certificates gcompat kubo wget && \
  adduser -D -h /app originless

WORKDIR /app

COPY --from=builder /originless /app/originless
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && \
  mkdir -p /tmp/originless /data && \
  chown -R originless:originless /app /tmp/originless /data

USER originless

EXPOSE 3232 4001/tcp 4001/udp

VOLUME ["/data"]

STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=5 CMD wget -qO- http://127.0.0.1:3232/status || exit 1

ENTRYPOINT ["/app/docker-entrypoint.sh"]
