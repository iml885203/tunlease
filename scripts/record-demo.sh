#!/usr/bin/env bash
# Record assets/demo.gif. Sets up, off-camera:
#   - a local stand-in dev server (the claimed path reaches this)
# then runs vhs against assets/demo.tape using the public demo relay.
#
# Requires: vhs, python3, a Go toolchain.
set -euo pipefail
cd "$(dirname "$0")/.."

DOMAIN=tunlease.dotw.me
DEMO_PATH=/demo/testing/my-first-tunnel/
PORT_LOCAL=8080
TMP=$(mktemp -d /tmp/tunlease-vhs.XXXXXX)
BIN="$TMP/bin"
PIDS=()
cleanup() {
  HOME="$TMP/home" "$BIN/tul" release -p "$PORT_LOCAL" \
    -g "$DOMAIN" >/dev/null 2>&1 || true
  kill "${PIDS[@]}" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

# Build tul onto PATH for the recording.
mkdir -p "$BIN"
mkdir -p "$TMP/home"
go build -o "$BIN/tul" ./cmd/tunlease
export PATH="$BIN:$PATH"

# Local stand-in dev server the claimed path will reach.
python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(s): s.send_response(200); s.end_headers(); s.wfile.write(b'hello from my laptop\n')
    def log_message(s,*a): pass
HTTPServer(('127.0.0.1', $PORT_LOCAL), H).serve_forever()
" & PIDS+=($!)

for i in $(seq 1 20); do
  curl -fsS "https://$DOMAIN/_tunlease/healthz" >/dev/null && break
  [ "$i" = 20 ] && {
    echo "ERROR: https://$DOMAIN is not reachable" >&2
    exit 1
  }
  sleep 0.3
done
echo "Public relay reachable at https://$DOMAIN — recording…"

# Exercise the foreground tunnel while VHS is recording it. Retry briefly
# because the claim command needs a moment to finish its WebSocket handshake.
VERIFY_RESULT="$TMP/relay-verified"
(
  for _ in $(seq 1 30); do
    if curl -fsS "https://$DOMAIN$DEMO_PATH" |
      grep -q "hello from my laptop"; then
      touch "$VERIFY_RESULT"
      exit 0
    fi
    sleep 0.2
  done
  exit 1
) & PIDS+=($!)

HOME="$TMP/home" vhs assets/demo.tape
[ -f "$VERIFY_RESULT" ]
echo "Verified public relay traffic reached the local server."
echo "Recorded assets/demo.gif"
