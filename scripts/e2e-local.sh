#!/usr/bin/env bash
# 本機端到端驗收：
#   1. gateway 起來，CLI claim 後 curl gateway（以 path demux）→ 到達本機 server
#   2. 白名單外 403、同 path 衝突 409
#   3. kill -9 CLI → WebSocket 關閉 → claim 立即消失
set -euo pipefail
cd "$(dirname "$0")/.."

PORT_API=18300
PORT_LOCAL=18500
PORT_ORIGIN=18501
TOKEN=e2e-secret
TMP=$(mktemp -d)
PIDS=()
cleanup() { kill "${PIDS[@]}" 2>/dev/null || true; rm -rf "$TMP"; }
trap cleanup EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }

make build >/dev/null

cat > "$TMP/config.yaml" <<EOF
listen: ":$PORT_API"
fail_open_url: "http://127.0.0.1:$PORT_ORIGIN"
max_claims: 64
whitelist: ["/test/"]
tokens:
  - {owner: e2e, token: "$TOKEN"}
EOF

./bin/tul gateway --config "$TMP/config.yaml" > "$TMP/gateway.log" 2>&1 &
PIDS+=($!)

# Origin server：未 claim 的 path 會送到這裡。
python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'hello-from-origin')
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', $PORT_ORIGIN), H).serve_forever()
" &
PIDS+=($!)

# 本機目標 server：回固定字串
python3 -c "
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'hello-from-local')
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', $PORT_LOCAL), H).serve_forever()
" &
PIDS+=($!)

for i in $(seq 1 20); do
  curl -sf "http://127.0.0.1:$PORT_API/_tunlease/healthz" >/dev/null && break
  [ "$i" = 20 ] && fail "gateway did not become healthy"
  sleep 0.3
done

# The CLI receives only the whole-host gateway URL and appends /_tunlease.
export TUNLEASE_GATEWAY="http://127.0.0.1:$PORT_API" TUNLEASE_TOKEN="$TOKEN"

# --- 白名單外 → 403 ---
if ./bin/tul claim --to $PORT_LOCAL /outside/cb 2> "$TMP/deny.log"; then
  fail "claim outside whitelist should fail"
fi
grep -qi "allowlist\|not allowed" "$TMP/deny.log" || fail "403 message missing: $(cat "$TMP/deny.log")"
echo "OK: whitelist 403"

# --- claim + tunnel ---
./bin/tul claim --to $PORT_LOCAL /test/cb > "$TMP/claim.log" 2>&1 &
CLAIM_PID=$!
PIDS+=($CLAIM_PID)

for i in $(seq 1 30); do
  BODY=$(curl -sf --max-time 2 "http://127.0.0.1:$PORT_API/test/cb" 2>/dev/null || true)
  [ "$BODY" = "hello-from-local" ] && break
  [ "$i" = 30 ] && { cat "$TMP/claim.log" "$TMP/gateway.log" >&2; fail "tunnel did not deliver traffic"; }
  sleep 0.5
done
echo "OK: claim → tunnel → local server"

# --- 衝突 → 409（另一個 owner 視角：同 token 也該擋，前綴互蓋）---
if ./bin/tul claim --to $PORT_LOCAL /test/cb/deeper 2> "$TMP/conflict.log"; then
  fail "overlapping claim should fail"
fi
grep -q "already claimed by e2e" "$TMP/conflict.log" || fail "409 message missing: $(cat "$TMP/conflict.log")"
echo "OK: overlap 409 with claimed_by"

# --- list 有 (you) 標記 ---
./bin/tul list | grep -q "(you)" || fail "list missing (you) marker"
echo "OK: list shows own claim"

# --- kill -9 → connection close → claim 消失 ---
kill -9 "$CLAIM_PID"
for _ in $(seq 1 20); do
  CLAIMS=$(curl -sf -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT_API/_tunlease/api/v1/claims")
  echo "$CLAIMS" | grep -q '"claims":\[\]' && break
  sleep 0.25
done
echo "$CLAIMS" | grep -q '"claims":\[\]' || fail "claim survived closed tunnel: $CLAIMS"
echo "OK: kill -9 → connection close → claim gone"

# --- release 後 state 清乾淨（release 殘留 state 條目）---
./bin/tul release /test/cb > /dev/null 2>&1 || true

echo "ALL E2E CHECKS PASSED"
