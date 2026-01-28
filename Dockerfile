FROM node:slim

ENV STORAGE_MAX=200GB
ENV FILE_LIMIT=5GB
ENV IPFS_PATH=/data
ENV NODE_ENV=production

# Install dependencies (curl, tar, etc.)
RUN apt-get update && \
    apt-get install -y curl tar && \
    rm -rf /var/lib/apt/lists/*

# Install IPFS (Kubo)
RUN curl -fsSL "https://dist.ipfs.tech/kubo/v0.39.0/kubo_v0.39.0_linux-$(dpkg --print-architecture).tar.gz" | \
    tar -xz -C /tmp && \
    mv /tmp/kubo/ipfs /usr/local/bin/ipfs && \
    rm -rf /tmp/kubo

WORKDIR /app

COPY package.json package-lock.json* ./

RUN npm install --omit=dev

COPY . .

# Create temp uploads directory, data directory, and set ownership
# Using 'node' user which exists in node image
RUN mkdir -p /tmp/originless /data && chown -R node:node /app /tmp/originless /data

# Switch to non-root user
USER node

EXPOSE 3232 4001/tcp 4001/udp

# Declare volume for IPFS data persistence
VOLUME ["/data"]

# Stop signal and grace period for clean shutdown
STOPSIGNAL SIGTERM
LABEL com.docker.compose.stop-grace-period="15s"

# Health check - wait 7m for IPFS to connect to peers
HEALTHCHECK --interval=30s --timeout=10s --start-period=7m --retries=5 \
  CMD curl -f http://localhost:3232/health || exit 1

CMD ["sh", "-c", "\
  if [ ! -f \"$IPFS_PATH/config\" ]; then ipfs init --profile=lowpower; fi && \
  ipfs config Datastore.StorageMax ${STORAGE_MAX} && \
  ipfs config --json Routing.Type '\"dhtclient\"' && \
  ipfs config --json Provide.DHT.Interval '\"24h\"' && \
  ipfs daemon --enable-gc --routing=dhtclient & \
  until curl -s http://127.0.0.1:5001/api/v0/id > /dev/null; do \
    echo 'Waiting for IPFS daemon...'; sleep 3; \
  done && \
  exec node app.js"]
