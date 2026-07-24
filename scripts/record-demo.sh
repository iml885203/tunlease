#!/usr/bin/env bash
# Record assets/demo.gif: start a local gateway + stand-in server, then run vhs
# against assets/demo.tape (which shows only the `tunle claim` command).
#
# Requires: vhs (brew install vhs), python3, and a Go toolchain.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT_GATEWAY=8080
PORT_LOCAL=3007
TMP=$(mktemp -d)
BIN="$TMP/bin"
PIDS=()
cleanup() { kill "${PIDS[@]}" 2>/dev/null || true; rm -rf "$TMP"; }
trap cleanup EXIT

# Build `tunle` into a temp dir and put it on PATH for the recording.
mkdir -p "$BIN"
go build -o "$BIN/tunle" ./cmd/tunlease
export PATH="$BIN:$PATH"

cat > "$TMP/demo.yaml" <<EOF
listen: ":$PORT_GATEWAY"
advertise_host: "127.0.0.1"
port_pool: {start: 42060, end: 42064}
ttl_seconds: 30
heartbeat_seconds: 10
whitelist: ["/webhooks/"]
EOF

# Local stand-in dev server the claimed path will reach.
python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(s): s.send_response(200); s.end_headers(); s.wfile.write(b'hello from my laptop\n')
    def log_message(s,*a): pass
HTTPServer(('127.0.0.1', $PORT_LOCAL), H).serve_forever()
" & PIDS+=($!)

# Single-process gateway+router in front of nothing in particular (demo only).
tunle serve --config "$TMP/demo.yaml" --listen ":$PORT_GATEWAY" --app "http://127.0.0.1:$PORT_LOCAL" >/dev/null 2>&1 &
PIDS+=($!)

for i in $(seq 1 20); do
  curl -sf "http://127.0.0.1:$PORT_GATEWAY/healthz" >/dev/null && break
  [ "$i" = 20 ] && { echo "gateway did not become healthy" >&2; exit 1; }
  sleep 0.3
done

vhs assets/demo.tape
echo "Recorded assets/demo.gif"
