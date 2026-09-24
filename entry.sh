#!/bin/sh
set -u

IPFS_REPO="${IPFS_PATH:-/data/ipfs}"
READY_TIMEOUT="${IPFS_READY_TIMEOUT:-60}"
case "$READY_TIMEOUT" in
	'' | *[!0-9]*)
		READY_TIMEOUT=60
		;;
esac

app_pid=""
shutting_down=0

shutdown() {
	shutting_down=1
	kill -TERM "$ipfs_pid" 2>/dev/null || true
	if [ -n "$app_pid" ]; then
		kill -TERM "$app_pid" 2>/dev/null || true
	fi
}

if [ ! -f "$IPFS_REPO/config" ]; then
	ipfs init --profile=lowpower
fi
ipfs config --json Routing.Type '"dhtclient"'

ipfs daemon &
ipfs_pid=$!

trap shutdown TERM INT

# Readiness gate: don't serve traffic until the daemon answers, and fail
# loud instead of serving 502s forever if it never comes up.
ready=0
i=0
while [ "$i" -lt "$READY_TIMEOUT" ]; do
	if ipfs id >/dev/null 2>&1; then
		ready=1
		break
	fi
	if ! kill -0 "$ipfs_pid" 2>/dev/null; then
		break
	fi
	sleep 1
	i=$((i + 1))
done
if [ "$ready" -ne 1 ]; then
	echo "originless: ipfs daemon did not become ready; exiting" >&2
	shutdown
	wait "$ipfs_pid" 2>/dev/null || true
	exit 1
fi
echo "originless: ipfs daemon ready"

/usr/local/bin/originless &
app_pid=$!

# Supervise both processes: if either dies, shut down the other and exit
# so the container fails loud instead of serving a broken backend.
app_status=0
while true; do
	if ! kill -0 "$app_pid" 2>/dev/null; then
		wait "$app_pid"
		app_status=$?
		break
	fi
	if ! kill -0 "$ipfs_pid" 2>/dev/null; then
		if [ "$shutting_down" -eq 1 ]; then
			wait "$app_pid" 2>/dev/null
			app_status=$?
		else
			echo "originless: ipfs daemon died unexpectedly; exiting" >&2
			wait "$ipfs_pid" 2>/dev/null || true
			kill -TERM "$app_pid" 2>/dev/null || true
			wait "$app_pid" 2>/dev/null
			app_status=1
		fi
		break
	fi
	sleep 2
done

shutdown
wait "$ipfs_pid" 2>/dev/null || true
exit "$app_status"
