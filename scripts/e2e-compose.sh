#!/usr/bin/env bash
# End-to-end test over docker compose, all-HTTP topology (mirrors e2e-local.sh):
#   compose brings up gateway (:18300) + app (testapp on :8081).
# The gateway serves its control plane under /_tunlease and demuxes every other
# path itself: a claimed path is tunnelled to the developer, anything else falls
# open to fail_open_url (http://app:8081).
#
# Scenarios:
#   1. before any claim, a third-party path falls open to the app
#   2. after `tunle claim`, that path reaches the developer's local server
#   3. after the tunnel dies, its paths are released and fall open again
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

# Third-party traffic hits the gateway root; control plane is under /_tunlease.
GATEWAY=http://127.0.0.1:18300

cd "$ROOT"
docker compose up -d --build
wait_for "$GATEWAY/_tunlease/healthz"

# 1. Unclaimed path falls open to the app (fail_open_url).
before=$(curl -fsS "$GATEWAY/test/callback")
expect "app:/test/callback" "$before" "unclaimed path falls open to app"

go build -o "$TMP/tunlease" ./cmd/tunlease
go build -o "$TMP/testapp" ./cmd/testapp
"$TMP/testapp" --listen 127.0.0.1:18500 --label local >"$TMP/local.log" 2>&1 & LOCAL_PID=$!

# CLI's gateway base URL; the client auto-adds the /_tunlease control prefix.
TUNLEASE_GATEWAY="$GATEWAY" TUNLEASE_TOKEN=e2e-token TUNLEASE_STATE_FILE="$TMP/state.json" \
  "$TMP/tunlease" claim --to 18500 /test/callback >"$TMP/claim.log" 2>&1 & CLAIM_PID=$!

# 2. Claimed path crosses the reverse tunnel to the developer's local server.
for _ in $(seq 1 30); do
  got=$(curl -fsS "$GATEWAY/test/callback" 2>/dev/null || true)
  [ "$got" = "local:/test/callback" ] && break
  sleep 1
done
expect "local:/test/callback" "$got" "claimed path crosses reverse tunnel to localhost"

# 3. Kill the tunnel; connection closure releases the path.
kill -9 "$CLAIM_PID" 2>/dev/null || true; wait "$CLAIM_PID" 2>/dev/null || true; CLAIM_PID=""
for _ in $(seq 1 15); do
  got=$(curl -fsS "$GATEWAY/test/callback" 2>/dev/null || true)
  [ "$got" = "app:/test/callback" ] && break
  sleep 1
done
expect "app:/test/callback" "$got" "closed tunnel falls open to app again"

echo "ALL COMPOSE E2E CHECKS PASSED"
