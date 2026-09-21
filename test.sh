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

post_event() { # $1=collection $2=canonical-data-json $3=comma-labels $4=labels-json-array
  local col="$1" data_canon="$2" labels_csv="$3" labels_json="$4"
  local created expires idin sighex
  created="$(date +%s)"
  expires="$((created + 2592000))" # 30d, within 1y max TTL
  idin="${OWNER}:${col}:${created}:${expires}:${data_canon}:${labels_csv}"
  printf '%s' "$idin" | openssl dgst -sha256 -binary > "$TMP/id.bin"
  openssl pkeyutl -sign -inkey "$TMP/priv.pem" -rawin -in "$TMP/id.bin" -out "$TMP/sig.bin" 2>/dev/null || return 1
  sighex="$(od -An -tx1 "$TMP/sig.bin" | tr -d ' \n')"
  jq -n --arg o "$OWNER" --arg c "$col" --argjson cr "$created" --argjson ex "$expires" \
    --argjson d "$data_canon" --argjson l "$labels_json" --arg s "$sighex" \
    '{owner:$o,collection:$c,created_at:$cr,expires_at:$ex,data:$d,labels:$l,sig:$s}' > "$TMP/ev.json"
  curl -s -m 20 -X POST "$NODE/events" -H "Content-Type: application/json" -d @"$TMP/ev.json"
}

i=0
while true; do
  i=$((i + 1))

  # ---- random blob (1–17 KB of crypto-random bytes, passes .bin sniff) ----
  SIZE="$((1024 + RANDOM % 16384))"
  openssl rand -out "$TMP/load.bin" "$SIZE" 2>/dev/null
  BLOB_RESP="$(curl -s -m 60 -X POST "$NODE/up" \
    -F "file=@$TMP/load.bin;filename=load-$i.bin;type=application/octet-stream")"
  BHASH="$(printf '%s' "$BLOB_RESP" | jq -r '.hash // empty' 2>/dev/null)"
  if [ -n "$BHASH" ]; then
    ok_blobs=$((ok_blobs + 1)); LAST_BLOB="$BHASH"
    echo "[$i] blob ok  size=$SIZE hash=${BHASH:0:12}..."
  else
    echo "[$i] blob FAIL size=$SIZE resp=$(printf '%s' "$BLOB_RESP" | head -c 160)"
  fi

  # ---- random signed event ----
  COL="${COLS[$((RANDOM % ${#COLS[@]}))]}"
  MSG="$(rand_str 6)"
  RND="$(rand_str 12)"
  SCORE="$((RANDOM % 10000))"
  LEVEL="$((1 + RANDOM % 50))"
  if [ -n "$LAST_BLOB" ] && [ $((i % 5)) -eq 0 ]; then
    # every 5th event links the last blob -> janitor-exempt while event lives
    DATA_C="$(jq -c -S -n --arg b "$LAST_BLOB" --arg m "$MSG" '{_blob:$b,note:$m}')"
    LBL_CSV="topic:$COL,client:testsh"
    LBL_JSON='["topic:'"$COL"'","client:testsh"]'
  else
    DATA_C="$(jq -c -S -n --arg m "$MSG" --arg r "$RND" --argjson s "$SCORE" --argjson l "$LEVEL" \
      '{level:$l,msg:$m,rand:$r,score:$s}')"
    RUN="$(rand_str 4)"
    LBL_CSV="topic:$COL,client:testsh,run:$RUN"
    LBL_JSON='["topic:'"$COL"'","client:testsh","run:'"$RUN"'"]'
  fi
  EV_RESP="$(post_event "$COL" "$DATA_C" "$LBL_CSV" "$LBL_JSON")"
  if printf '%s' "$EV_RESP" | jq -e '.status == "success"' >/dev/null 2>&1; then
    ok_events=$((ok_events + 1))
    EID="$(printf '%s' "$EV_RESP" | jq -r '.id[0:12]')"
    echo "[$i] event ok col=$COL id=$EID... (events=$ok_events blobs=$ok_blobs)"
  else
    echo "[$i] event FAIL resp=$(printf '%s' "$EV_RESP" | head -c 200)"
  fi

  sleep 1
done
