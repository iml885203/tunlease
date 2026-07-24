#!/usr/bin/env bash
# Record assets/demo.gif. Sets up, off-camera:
#   - a local stand-in dev server (the claimed path reaches this)
#   - the tunle gateway on http://tunlease.demo (port 80), fail-open to the app
# then runs vhs against assets/demo.tape (which shows only `tunle claim`).
#
# Precondition you set up ONCE:
#   echo '127.0.0.1 tunlease.demo' | sudo tee -a /etc/hosts
#
# Run with ordinary permissions. Everything runs as you (so vhs is on PATH and
# assets/demo.gif is owned by you) EXCEPT the gateway, which binds privileged
# :80 under sudo (prompts once). The tape sets TUNLEASE_DEFAULT_SCHEME=http
# off-camera so `--gateway tunlease.demo` resolves to http://tunlease.demo.
#
# Requires: vhs, python3, a Go toolchain.
set -euo pipefail
cd "$(dirname "$0")/.."

DOMAIN=tunlease.demo
PORT_LOCAL=3007
TMP=$(mktemp -d)
BIN="$TMP/bin"
PIDS=()
GW_PID=""
cleanup() {
  kill "${PIDS[@]}" 2>/dev/null || true
  # The gateway runs under sudo (binds :80), so kill it with sudo too.
  [ -n "$GW_PID" ] && sudo kill "$GW_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

if ! grep -q "$DOMAIN" /etc/hosts 2>/dev/null; then
  echo "WARNING: '$DOMAIN' not in /etc/hosts. Add it first:" >&2
  echo "  echo '127.0.0.1 $DOMAIN' | sudo tee -a /etc/hosts" >&2
fi

# Build tunle onto PATH for the recording.
mkdir -p "$BIN"
go build -o "$BIN/tunle" ./cmd/tunlease
export PATH="$BIN:$PATH"

cat > "$TMP/demo.yaml" <<EOF
listen: ":80"
advertise_host: "127.0.0.1"
max_claims: 64
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

# The gateway binds privileged :80, so start ONLY it under sudo (prompts once).
# It fails open to the stand-in app for unclaimed paths.
echo "Starting the gateway on :80 (sudo — you may be prompted for your password)…"
sudo "$BIN/tunle" serve --config "$TMP/demo.yaml" --listen ":80" --app "http://127.0.0.1:$PORT_LOCAL" >/dev/null 2>&1 &
GW_PID=$!
sleep 1

for i in $(seq 1 20); do
  curl -sf "http://$DOMAIN/_tunlease/healthz" >/dev/null && break
  [ "$i" = 20 ] && { echo "ERROR: http://$DOMAIN not reachable — gateway on :80 failed to start." >&2; exit 1; }
  sleep 0.3
done
echo "Gateway reachable at http://$DOMAIN — recording…"

vhs assets/demo.tape
echo "Recorded assets/demo.gif"
