FROM golang:1.24-alpine AS builder

ARG VERSION=dev
WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/besoeasy/originless/modules.Version=${VERSION}" -o /originless .

FROM alpine:latest

RUN apk add --no-cache ca-certificates wget && \
  adduser -D -h /app originless

WORKDIR /app

COPY --from=builder /originless /app/originless

RUN mkdir -p /data && \
  chown -R originless:originless /app /data

USER originless

EXPOSE 3232/tcp 3232/udp 3234/udp

VOLUME ["/data"]

STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=5 CMD wget -qO- http://127.0.0.1:3232/status || exit 1

ENTRYPOINT ["/app/originless"]