#!/usr/bin/env bash
# Originless container runner — builds and runs originless using Podman in ephemeral (--rm) mode.
# Usage: ./podman.sh  (then Ctrl-C to stop)
set -euo pipefail

for cmd in podman; do
  command -v "$cmd" >/dev/null || { echo "missing required tool: $cmd" >&2; exit 1; }
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  cat <<'EOF'
Originless Podman Runner

Builds the originless container image and runs it in ephemeral (--rm) mode.

Usage:
  ./podman.sh [PODMAN_RUN_OPTIONS...]

Environment Variables:
  PORT            Host port to expose (default: 3232)
  IMAGE_NAME      Name of the container image (default: originless:latest)
  CONTAINER_NAME  Name of the container instance (default: originless)
  DATA_VOLUME     Named volume or host directory to mount at /data (optional)
  NETWORK_ID      P2P swarm network ID (optional, default: originless, set 'off' to disable)
  SKIP_BUILD      Set to 1 to skip image build and run immediately (default: 0)

Examples:
  ./podman.sh                             # Build and run interactively with --rm
  PORT=8080 ./podman.sh                   # Run on port 8080
  DATA_VOLUME=originless-data ./podman.sh # Persist data in a named volume
  ./podman.sh -d                          # Run in background (detached)
EOF
  exit 0
fi

IMAGE_NAME="${IMAGE_NAME:-originless:latest}"
CONTAINER_NAME="${CONTAINER_NAME:-originless}"
PORT="${PORT:-3232}"
VERSION="${VERSION:-$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")}"

# Build image unless SKIP_BUILD=1
if [ "${SKIP_BUILD:-0}" != "1" ]; then
  echo "==> building $IMAGE_NAME (version: $VERSION) with podman..."
  podman build \
    --build-arg VERSION="$VERSION" \
    -t "$IMAGE_NAME" \
    "$SCRIPT_DIR"
  echo "==> image build successful"
fi

RUN_ARGS=("--rm")

# Check if caller passed detached mode
IS_DETACHED=0
for arg in "$@"; do
  if [[ "$arg" == "-d" || "$arg" == "--detach" ]]; then
    IS_DETACHED=1
    break
  fi
done

# Allocate pseudo-TTY if interactive terminal and not detached
if [ "$IS_DETACHED" -eq 0 ] && [ -t 0 ] && [ -t 1 ]; then
  RUN_ARGS+=("-it")
fi

# Assign container name if not already provided in arguments
HAS_NAME=0
for arg in "$@"; do
  if [[ "$arg" == "--name" || "$arg" == --name=* ]]; then
    HAS_NAME=1
    break
  fi
done

if [ "$HAS_NAME" -eq 0 ]; then
  if podman container exists "$CONTAINER_NAME" 2>/dev/null; then
    echo "==> removing existing container '$CONTAINER_NAME'..."
    podman rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fi
  RUN_ARGS+=("--name" "$CONTAINER_NAME")
fi

# Map ports if not explicitly passed
HAS_PORT=0
for arg in "$@"; do
  if [[ "$arg" == "-p" || "$arg" == "--publish" || "$arg" == --publish=* ]]; then
    HAS_PORT=1
    break
  fi
done

if [ "$HAS_PORT" -eq 0 ]; then
  RUN_ARGS+=(
    "-p" "${PORT}:3232"
    "-p" "${PORT}:3232/udp"
    "-p" "3234:3234/udp"
  )
fi

# Optional volume mount
if [ -n "${DATA_VOLUME:-}" ]; then
  RUN_ARGS+=("-v" "${DATA_VOLUME}:/data")
fi

# Optional NETWORK_ID environment override
if [ -n "${NETWORK_ID:-}" ]; then
  RUN_ARGS+=("-e" "NETWORK_ID=${NETWORK_ID}")
fi

echo "==> starting $CONTAINER_NAME in --rm mode..."
echo "==> node available at http://127.0.0.1:$PORT (Ctrl-C to stop)"

exec podman run "${RUN_ARGS[@]}" "$@" "$IMAGE_NAME"
