#!/bin/sh
set -u

IPFS_REPO="${IPFS_PATH:-/data/ipfs}"
IPFS_API_URL="${IPFS_API_URL:-http://127.0.0.1:5001}"

if [ ! -f "$IPFS_REPO/config" ]; then
	ipfs init --profile=lowpower
fi
ipfs config --json Routing.Type '"dhtclient"'

ipfs daemon --enable-gc &
ipfs_pid=$!

cleanup() {
	kill -TERM "$ipfs_pid" 2>/dev/null || true
}

trap cleanup TERM INT

# Wait for Kubo's HTTP RPC before starting the application. Kubo rejects GET
# requests on its API, so use an empty POST to the version endpoint.
until curl -fsS -X POST "$IPFS_API_URL/api/v0/version" >/dev/null 2>&1; do
	if ! kill -0 "$ipfs_pid" 2>/dev/null; then
		echo "originless: ipfs daemon stopped before becoming ready" >&2
		exit 1
	fi
	sleep 5
done

echo "originless: ipfs daemon ready"

/usr/local/bin/originless &
app_pid=$!
wait "$app_pid"
app_status=$?

cleanup
wait "$ipfs_pid" 2>/dev/null || true
exit "$app_status"
