#!/usr/bin/env bash
# Build Originless locally and run its test container in the foreground.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PODMAN_BIN="${PODMAN:-podman}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
	cat <<'EOF'
Originless Podman test runner

Builds the local image and runs it in the foreground with --rm. Press Ctrl-C
to stop it; the container is removed automatically. No host or named volume
is mounted.

Environment variables:
  IMAGE_NAME      Image tag to build (default: originless:latest)
  CONTAINER_NAME  Container name (default: originless)
  PORT            Host port for Originless (default: 3232)
  VERSION         Version build argument (default: dev)
  PODMAN          Podman executable (default: podman)

Examples:
  ./podman.sh
  PORT=8080 ./podman.sh
EOF
	exit 0
fi

if ! command -v "$PODMAN_BIN" >/dev/null 2>&1; then
	echo "missing required executable: $PODMAN_BIN" >&2
	exit 1
fi

IMAGE_NAME="${IMAGE_NAME:-originless:latest}"
CONTAINER_NAME="${CONTAINER_NAME:-originless}"
PORT="${PORT:-3232}"
VERSION="${VERSION:-dev}"

if [[ ! "$PORT" =~ ^[0-9]+$ ]] || (( PORT < 1 || PORT > 65535 )); then
	echo "invalid PORT: $PORT (must be between 1 and 65535)" >&2
	exit 1
fi

echo "==> building $IMAGE_NAME (version $VERSION) with $PODMAN_BIN..."
"$PODMAN_BIN" build \
	--build-arg "VERSION=$VERSION" \
	--tag "$IMAGE_NAME" \
	--file "$SCRIPT_DIR/deploy/Dockerfile" \
	"$SCRIPT_DIR"

echo "==> starting $CONTAINER_NAME on http://127.0.0.1:$PORT"
echo "==> press Ctrl-C to stop and remove the container"
exec "$PODMAN_BIN" run \
	--rm \
	--name "$CONTAINER_NAME" \
	--publish "${PORT}:3232/tcp" \
	"$IMAGE_NAME"
