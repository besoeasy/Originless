#!/bin/sh
set -eu

export IPFS_PATH="${IPFS_PATH:-/data}"

truthy() {
  v=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  case "$v" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

falsey() {
  v=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  case "$v" in
    0|false|no|off) return 0 ;;
    *) return 1 ;;
  esac
}

csv_foreach() {
  # csv_foreach "a,b,c" → prints each trimmed token on its own line
  printf '%s' "$1" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | sed '/^$/d'
}

json_string_array_from_csv() {
  printf '%s' "$1" | awk -F',' '{
    printf "["
    for (i = 1; i <= NF; i++) {
      gsub(/^[ \t]+|[ \t]+$/, "", $i)
      if ($i == "") next
      if (n++) printf ","
      printf "\"%s\"", $i
    }
    printf "]"
  }'
}

host_from_url() {
  # http://node-a:3232 → node-a   | https://originless.gupt.app/ → originless.gupt.app
  printf '%s' "$1" | sed -E 's#^[a-zA-Z][a-zA-Z0-9+.-]*://##' | sed 's#[/?].*##' | sed 's#:[0-9]*$##'
}

wait_ipfs_api() {
  # Must hit the HTTP API — `ipfs id` works offline and races the daemon for repo.lock.
  i=0
  while [ "$i" -lt 90 ]; do
    if ! kill -0 "$IPFS_PID" 2>/dev/null; then
      echo "[entrypoint] IPFS daemon exited before API became ready"
      return 1
    fi
    if wget -qO- --post-data='' "http://127.0.0.1:5001/api/v0/version" >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  return 1
}

# Dial other Originless nodes by reading their /status peer id, then swarm-connect.
# IPFS_PEER_NODES=http://node-a:3232,https://originless.example
connect_peer_nodes() {
  [ -n "${IPFS_PEER_NODES:-}" ] || return 0
  csv_foreach "$IPFS_PEER_NODES" | while read -r base; do
    base=$(printf '%s' "$base" | sed 's#/*$##')
    host=$(host_from_url "$base")
    [ -n "$host" ] || continue

    peer=""
    j=0
    while [ "$j" -lt 30 ]; do
      body=$(wget -qO- "$base/status" 2>/dev/null || true)
      peer=$(printf '%s' "$body" | sed -n 's/.*"node":{"id":"\([^"]*\)".*/\1/p' | head -1)
      if [ -z "$peer" ]; then
        peer=$(printf '%s' "$body" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
      fi
      if [ -n "$peer" ]; then
        break
      fi
      j=$((j + 1))
      sleep 2
    done
    [ -n "$peer" ] || continue

    echo "[entrypoint] swarm connect → $host ($peer)"
    # Daemon is up: CLI talks to the API (no repo.lock contention).
    connected=0
    k=0
    while [ "$k" -lt 20 ]; do
      if ipfs swarm connect "/dns4/${host}/tcp/4001/p2p/${peer}" >/dev/null 2>&1 \
        || ipfs swarm connect "/dns4/${host}/udp/4001/quic-v1/p2p/${peer}" >/dev/null 2>&1; then
        connected=1
        break
      fi
      case "$host" in
        *[!0-9.]* ) ;;
        *)
          if ipfs swarm connect "/ip4/${host}/tcp/4001/p2p/${peer}" >/dev/null 2>&1 \
            || ipfs swarm connect "/ip4/${host}/udp/4001/quic-v1/p2p/${peer}" >/dev/null 2>&1; then
            connected=1
            break
          fi
          ;;
      esac
      k=$((k + 1))
      sleep 3
    done
    if [ "$connected" -eq 1 ]; then
      echo "[entrypoint] connected to $host"
    else
      echo "[entrypoint] warning: could not dial $host (will rely on DHT/bootstrap)"
    fi
  done
}

connect_swarm_multiaddrs() {
  [ -n "${IPFS_SWARM_CONNECT:-}" ] || return 0
  csv_foreach "$IPFS_SWARM_CONNECT" | while read -r addr; do
    echo "[entrypoint] swarm connect → $addr"
    ipfs swarm connect "$addr" >/dev/null 2>&1 || true
  done
}

# IPFS_PROFILE: lowpower (default, Umbrel/home-friendly), default, or server.
# Connectivity still comes from bootstrap + published 4001 + swarm peering —
# not from raising ConnMgr via the default/server profiles.
profile=$(printf '%s' "${IPFS_PROFILE:-lowpower}" | tr '[:upper:]' '[:lower:]')
case "$profile" in
  lowpower|server|default|local-discovery|test|badgerds|flatfs|randomports) ;;
  *) profile=lowpower ;;
esac

if [ ! -f "$IPFS_PATH/config" ]; then
  if [ "$profile" = "default" ]; then
    ipfs init
  else
    ipfs init --profile="$profile"
  fi
fi

ipfs config Datastore.StorageMax "${STORAGE_MAX:-100GB}"

# Allow dialing Docker/LAN peers (Compose IPFS_PEER_NODES). Still avoid
# announcing private addrs to the public DHT.
ipfs config --json Swarm.AddrFilters '[]'
ipfs config --json Addresses.NoAnnounce '[
  "/ip4/10.0.0.0/ipcidr/8",
  "/ip4/100.64.0.0/ipcidr/10",
  "/ip4/169.254.0.0/ipcidr/16",
  "/ip4/172.16.0.0/ipcidr/12",
  "/ip4/192.168.0.0/ipcidr/16",
  "/ip6/100::/ipcidr/64",
  "/ip6/2001:2::/ipcidr/48",
  "/ip6/fc00::/ipcidr/7",
  "/ip6/fe80::/ipcidr/10"
]'

# Re-apply swarm listen on every boot so Docker-published 4001 is actually bound.
ipfs config --json Addresses.Swarm '[
  "/ip4/0.0.0.0/tcp/4001",
  "/ip4/0.0.0.0/udp/4001/quic-v1",
  "/ip4/0.0.0.0/udp/4001/quic-v1/webtransport",
  "/ip6/::/tcp/4001",
  "/ip6/::/udp/4001/quic-v1",
  "/ip6/::/udp/4001/quic-v1/webtransport"
]'

# NAT traversal so other Kubo peers can Bitswap pins from this node.
ipfs config --json Swarm.EnableHolePunching true
ipfs config --json Swarm.RelayClient.Enabled true

# Routing mode (dhtclient | dht | auto). dhtclient is the default: announce + query
# without serving as a full DHT server.
routing=$(printf '%s' "${IPFS_ROUTING:-dhtclient}" | tr '[:upper:]' '[:lower:]')
case "$routing" in
  dht|dhtclient|dhtserver|auto|none) ;;
  *) routing=dhtclient ;;
esac
ipfs config --json Routing.Type "\"$routing\""

# Kubo 0.41 defaults Bootstrap to ["auto"] (remote autoconf). If that fetch times
# out at first boot, the node stays at 0 peers forever. Seed explicit defaults.
ipfs bootstrap rm --all >/dev/null 2>&1 || true
ipfs bootstrap add \
  /dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN \
  /dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa \
  /dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb \
  /dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt \
  /dnsaddr/va1.bootstrap.libp2p.io/p2p/12D3KooWKnDdG3iXw9eTFijk3YXExTvCzARjC9QzmkJBua8FLjtC \
  /dnsaddr/amigarage.bootstrap.libp2p.io/p2p/12D3KooWNMmZbdj45v8Nn8cwuptwANMNHYMxEBG1FfKE31sNaceS \
  >/dev/null 2>&1 || true

# Quiet Kubo telemetry nag in container logs (operators can re-enable).
export IPFS_TELEMETRY="${IPFS_TELEMETRY:-off}"

# Optional public multiaddrs when Docker publishes 4001 behind NAT/firewall.
# Comma-separated, e.g. /ip4/203.0.113.10/tcp/4001,/ip4/203.0.113.10/udp/4001/quic-v1
if [ -n "${SWARM_ANNOUNCE:-}" ]; then
  announce_json=$(json_string_array_from_csv "$SWARM_ANNOUNCE")
  if [ -n "$announce_json" ] && [ "$announce_json" != "[]" ]; then
    ipfs config --json Addresses.Announce "$announce_json"
  fi
fi

# Sticky Peering.Peers JSON array, e.g.
# [{"ID":"12D3KooW...","Addrs":["/ip4/10.0.0.2/tcp/4001"]}]
if [ -n "${IPFS_PEERING:-}" ]; then
  ipfs config --json Peering.Peers "$IPFS_PEERING"
fi

# Gateway is disabled to prevent serving abusive cleartext HTTP traffic.
# Content is pinned and distributed via libp2p swarm (port 4001).
# For HTTP retrieval, use Rainbow (https://github.com/ipfs/rainbow) on a separate domain.
ipfs config Addresses.Gateway '""'

# Drop a stale lock from a previous crashed container (single-process image).
rm -f "$IPFS_PATH/repo.lock" "$IPFS_PATH/api"

ipfs daemon --enable-gc --routing="$routing" &
export IPFS_PID=$!

if ! wait_ipfs_api; then
  echo "[entrypoint] fatal: IPFS API did not become ready"
  kill "$IPFS_PID" 2>/dev/null || true
  exit 1
fi

connect_swarm_multiaddrs
connect_peer_nodes

exec /app/originless
