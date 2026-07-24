#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
CLAIM_PID=""
LOCAL_PID=""

cleanup() {
  [ -n "$CLAIM_PID" ] && kill "$CLAIM_PID" 2>/dev/null || true
  [ -n "$LOCAL_PID" ] && kill "$LOCAL_PID" 2>/dev/null || true
  docker compose -f "$ROOT/compose.yaml" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT

wait_for() {
  local url=$1
  for _ in $(seq 1 60); do curl -fsS "$url" >/dev/null 2>&1 && return 0; sleep 1; done
  echo "timeout waiting for $url" >&2; return 1
}

expect() {
  local want=$1 got=$2 label=$3
  if [ "$got" != "$want" ]; then echo "FAIL $label: want '$want', got '$got'" >&2; exit 1; fi
  echo "OK: $label"
}

cd "$ROOT"
docker compose up -d --build
wait_for http://127.0.0.1:28080/health

before=$(curl -fsS http://127.0.0.1:28080/test/callback)
expect "app:/test/callback" "$before" "unclaimed path goes to app"

go build -o "$TMP/tunlease" ./cmd/cli
go build -o "$TMP/testapp" ./cmd/testapp
"$TMP/testapp" --listen 127.0.0.1:18500 --label local >"$TMP/local.log" 2>&1 & LOCAL_PID=$!
TUNLEASE_GATEWAY=http://127.0.0.1:18300 TUNLEASE_TOKEN=e2e-token TUNLEASE_STATE_FILE="$TMP/state.json" \
  "$TMP/tunlease" claim --to 18500 /test/callback >"$TMP/claim.log" 2>&1 & CLAIM_PID=$!

for _ in $(seq 1 30); do
  got=$(curl -fsS http://127.0.0.1:28080/test/callback 2>/dev/null || true)
  [ "$got" = "local:/test/callback" ] && break
  sleep 1
done
expect "local:/test/callback" "$got" "claimed path crosses reverse tunnel to localhost"

kill -9 "$CLAIM_PID" 2>/dev/null || true; wait "$CLAIM_PID" 2>/dev/null || true; CLAIM_PID=""
sleep 1
fallback=$(curl -fsS http://127.0.0.1:28080/test/callback)
expect "app:/test/callback" "$fallback" "dead tunnel falls back to app immediately"
curl -fsS http://127.0.0.1:29090/metrics | grep -q 'devproxy_sidecar_requests_total{route="fallback"} 1'
echo "OK: fallback metric incremented"

sleep 9
expired=$(curl -fsS http://127.0.0.1:28080/test/callback)
expect "app:/test/callback" "$expired" "Redis TTL expiry removes route"

# Claim again, then remove the control plane. Every request must still succeed;
# old route is retained briefly but tunnel failure falls back, then stale table clears.
TUNLEASE_GATEWAY=http://127.0.0.1:18300 TUNLEASE_TOKEN=e2e-token TUNLEASE_STATE_FILE="$TMP/state2.json" \
  "$TMP/tunlease" claim --to 18500 /test/stale >"$TMP/claim2.log" 2>&1 & CLAIM_PID=$!
for _ in $(seq 1 30); do
  got=$(curl -fsS http://127.0.0.1:28080/test/stale 2>/dev/null || true); [ "$got" = "local:/test/stale" ] && break; sleep 1
done
expect "local:/test/stale" "$got" "second claim active"
docker compose stop gateway >/dev/null
for _ in $(seq 1 8); do
  got=$(curl -fsS http://127.0.0.1:28080/test/stale)
  expect "app:/test/stale" "$got" "gateway outage remains fail-open"
  sleep 1
done
echo "ALL COMPOSE E2E CHECKS PASSED"
