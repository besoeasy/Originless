#!/usr/bin/env bash
# Originless load script — keeps uploading random events + blobs until stopped.
# Uses only standard linux tools for HTTP: curl (uploads), plus
# openssl/jq/sha utilities for Ed25519 signing.
# Usage: ./test.sh  (then Ctrl-C to stop)
set -u

for cmd in curl openssl jq od head date; do
  command -v "$cmd" >/dev/null || { echo "missing required tool: $cmd" >&2; exit 1; }
done

read -rp "Originless node URL [http://127.0.0.1:3232]: " NODE
NODE="${NODE:-http://127.0.0.1:3232}"
NODE="${NODE%/}" # strip trailing slash

echo "==> checking $NODE/status ..."
if ! curl -s -m 10 "$NODE/status" | jq -e '.status == "success"' >/dev/null; then
  echo "node unreachable or unhealthy: $NODE/status" >&2
  exit 1
fi
echo "==> node OK"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"; echo; echo "stopped. temp cleaned."; exit 0' INT TERM EXIT

# One Ed25519 identity for the whole run (server only verifies signatures).
openssl genpkey -algorithm ed25519 -out "$TMP/priv.pem" 2>/dev/null
PUBHEX="$(openssl pkey -in "$TMP/priv.pem" -pubout -outform DER | tail -c 32 | od -An -tx1 | tr -d ' \n')"
OWNER="ed25519:$PUBHEX"
echo "==> owner: $OWNER"

COLS=(chat saves demo agents notes)
LAST_BLOB="" # track one hash so every 5th event can link it (eviction-protected)
i=0
ok_events=0
ok_blobs=0

rand_str() { # $1 = length; alnum only (avoids Go canonical escaping edge cases)
  head -c 64 /dev/urandom | base64 | tr -dc 'a-z0-9' | head -c "$1"
}

# publish() — sign + POST /events. $1=collection $2=canonical-data-json $3=labels_csv
#                 $4=labels_json-array $5=blob hash ("" for none) $6=optional path
#                 to attach as the `blob` part (event part always goes first).
# Prints the raw server response. With a blob attached the response is the event
# publish result (the hash lives in the top-level blob field, signed).
publish() {
  local col="$1" data_canon="$2" labels_csv="$3" labels_json="$4" blob="${5:-}" attach="${6:-}"
  local created expires idin sighex
  created="$(date +%s)"
  expires="$((created + 2592000))" # 30d, within 1y max TTL
  idin="${OWNER}:${col}:${created}:${expires}:${data_canon}:${blob}:${labels_csv}"
  printf '%s' "$idin" | openssl dgst -sha256 -binary > "$TMP/id.hash"
  openssl pkeyutl -sign -inkey "$TMP/priv.pem" -rawin -in "$TMP/id.hash" -out "$TMP/sig.raw" 2>/dev/null || return 1
  sighex="$(od -An -tx1 "$TMP/sig.raw" | tr -d ' \n')"
  jq -n --arg o "$OWNER" --arg c "$col" --argjson cr "$created" --argjson ex "$expires" \
    --argjson d "$data_canon" --argjson l "$labels_json" --arg s "$sighex" --arg b "$blob" \
    '{owner:$o,collection:$c,created_at:$cr,expires_at:$ex,data:$d,labels:$l,sig:$s} + (if $b == "" then {} else {blob:$b} end)' > "$TMP/ev.json"
  if [ -n "$attach" ] && [ -f "$attach" ]; then
    curl -s -m 60 -X POST "$NODE/events" \
      -F "event=@$TMP/ev.json;type=application/json" \
      -F "blob=@$attach;type=application/octet-stream"
  else
    curl -s -m 60 -X POST "$NODE/events" -H "Content-Type: application/json" -d @"$TMP/ev.json"
  fi
}

i=0
while true; do
  i=$((i + 1))

  # ---- blob (bytes) + the signed event that owns them, in ONE request ----
  # The `blob` part carries the raw bytes; the top-level blob field inside
  # the signed event is their SHA-256 (server re-hashes, mismatch -> 400).
  SIZE="$((1024 + RANDOM % 16384))"
  openssl rand -out "$TMP/load.blob" "$SIZE" 2>/dev/null
  BHASH="$(sha256sum "$TMP/load.blob" | cut -d' ' -f1)"
  MSG="$(rand_str 6)"
  COL="${COLS[$((RANDOM % ${#COLS[@]}))]}"
  D_CANON="$(jq -c -S -n --arg m "$MSG" '{note:$m}')"
  PUB_RESP="$(publish "$COL" "$D_CANON" "topic:$COL,client:testsh" \
    '["topic:'"$COL"'","client:testsh"]' "$BHASH" "$TMP/load.blob")"
  if printf '%s' "$PUB_RESP" | jq -e '.status == "success"' >/dev/null 2>&1; then
    ok_blobs=$((ok_blobs + 1)); LAST_BLOB="$BHASH"
    echo "[$i] blob+ev ok  col=$COL size=$SIZE hash=${BHASH:0:12}... (events=$ok_events blobs=$ok_blobs)"
  else
    echo "[$i] blob+ev FAIL size=$SIZE resp=$(printf '%s' "$PUB_RESP" | head -c 200)"
  fi

  # ---- random signed event (label-shifted, occasionally links LAST_BLOB) ----
  COL="${COLS[$((RANDOM % ${#COLS[@]}))]}"
  MSG="$(rand_str 6)"
  RND="$(rand_str 12)"
  SCORE="$((RANDOM % 10000))"
  LEVEL="$((1 + RANDOM % 50))"
  if [ -n "$LAST_BLOB" ] && [ $((i % 6)) -eq 0 ]; then
    # every 6th event links the last blob via the top-level blob field:
    # as long as this event lives, the janitor treats the blob as
    # referenced (link-only, no bytes re-attached).
    D_CANON="$(jq -c -S -n --arg m "$MSG" '{note:$m}')"
    LBL_CSV="topic:$COL,client:testsh"
    LBL_JSON='["topic:'"$COL"'","client:testsh"]'
    PUB_RESP="$(publish "$COL" "$D_CANON" "$LBL_CSV" "$LBL_JSON" "$LAST_BLOB")"
  else
    D_CANON="$(jq -c -S -n --arg m "$MSG" --arg r "$RND" --argjson s "$SCORE" --argjson l "$LEVEL" \
      '{msg:$m,rand:$r,score:$s,lvl:$l}')"
    RUN="$(rand_str 4)"
    LBL_CSV="topic:$COL,client:testsh,run:$RUN"
    LBL_JSON='["topic:'"$COL"'","client:testsh","run:'"$RUN"'"]'
    PUB_RESP="$(publish "$COL" "$D_CANON" "$LBL_CSV" "$LBL_JSON" "")"
  fi
  if printf '%s' "$PUB_RESP" | jq -e '.status == "success"' >/dev/null 2>&1; then
    ok_events=$((ok_events + 1))
    EID="$(printf '%s' "$PUB_RESP" | jq -r '.id[0:12]')"
    echo "[$i] event ok col=$COL id=$EID... (events=$ok_events blobs=$ok_blobs)"
  else
    echo "[$i] event FAIL resp=$(printf '%s' "$PUB_RESP" | head -c 200)"
  fi

  sleep 1
done
