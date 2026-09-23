#!/bin/sh
set -u

if [ ! -f "${IPFS_PATH:-/data/ipfs}/config" ]; then
	ipfs init --profile=lowpower
fi
ipfs config --json Routing.Type '"dhtclient"'

ipfs daemon &
ipfs_pid=$!

stop_ipfs() {
	if kill -0 "$ipfs_pid" 2>/dev/null; then
		kill -TERM "$ipfs_pid" 2>/dev/null || true
	fi
}

trap stop_ipfs TERM INT

/usr/local/bin/originless &
app_pid=$!

wait "$app_pid"
app_status=$?

stop_ipfs
wait "$ipfs_pid" 2>/dev/null || true
exit "$app_status"
