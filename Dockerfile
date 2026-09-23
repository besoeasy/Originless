FROM alpine:latest

# Kubo is Alpine's package name for the IPFS implementation and provides
# the `ipfs` command-line executable.
RUN apk add --no-cache kubo \
    && ipfs --version

ENV IPFS_PATH=/data/ipfs
RUN mkdir -p "$IPFS_PATH"

VOLUME ["/data"]

# IPFS swarm transport and local RPC/API ports.
EXPOSE 4001/tcp 4001/udp 5001/tcp 5001/udp

STOPSIGNAL SIGTERM
ENTRYPOINT ["ipfs"]
CMD ["daemon", "--init", "--init-profile=lowpower"]
