# Tunlease v1 protocol and implementation specification

[English](spec-v1.md) · [繁體中文](spec-v1.zh-TW.md)

This document defines the three deliverables—CLI, gateway, and sidecar—and turns the original architecture into an executable specification. Section 2 is the public contract for compatible clients.

This specification originated under the name DevProxy. The project is now named `tunlease`; metrics retain the `devproxy_` prefix for dashboard compatibility.

## 1. Scope and reading guide

- Deliver one monorepo containing `cmd/cli`, `cmd/gateway`, `cmd/sidecar`, shared packages, a Helm chart, a sidecar patch, and CI that publishes cross-platform CLI binaries and gateway/sidecar images.
- Changes to embedding applications are out of scope. The gateway must expose every required capability through the HTTP API and tunnel protocol; it must not depend on private CLI behavior.
- Section 2 is the frozen v1 API shape. Future compatible changes must be additive. Propose any incompatible change before implementation.
- This document defines what to build and verify. It takes precedence over earlier architecture notes when they conflict.

## 2. Protocol v1

### 2.1 Overview

The developer needs no Kubernetes access. The CLI initiates a purpose-built reverse WebSocket tunnel to the gateway through an existing web Ingress. The gateway registry owns path leases. A sidecar beside the fixed-endpoint app polls the route table: claimed paths enter the tunnel; every other path and every failure goes to the app.

| Component | Location | Responsibility |
|---|---|---|
| CLI | Developer machine | Claim/release/list, tunnel lifecycle, heartbeat, and Ctrl+C cleanup |
| Gateway | Shared Deployment | Registry API, lease ownership, port allocation, and reverse-tunnel server |
| Sidecar | Fixed-endpoint Pod | Longest-prefix routing and fail-open proxying |

### 2.2 Common rules

- APIs live under `/api/v1/`; WebSocket tunnel upgrades use `/tunnel` on the same host.
- Client authentication is disabled when no tokens are configured. When enabled, the API and tunnel use `Authorization: Bearer <token>`; each static token maps to an owner, and tunnel establishment also requires the owned claim ID. Unauthenticated clients share the `anonymous` owner.
- Errors use `{"error":"<machine_code>","detail":"<human message>",...}`. Clients must ignore unknown JSON fields.
- Times use RFC 3339 UTC. Lease expiry is determined by the server clock.

### 2.3 Registry API

| Endpoint | Purpose | Success | Main errors |
|---|---|---|---|
| `POST /api/v1/claims` | Claim paths. Body: `{"paths":["/api/callback/*"],"local":"localhost:5000"}` | `201` with `claim_id`, `owner`, `paths`, `remote_port`, `expires_at`, `ttl_seconds`, `heartbeat_seconds`, and `tunnel_fingerprint` | `409 path_claimed`, `403 path_not_allowed`, `401` |
| `POST /api/v1/claims/{id}/heartbeat` | Renew a lease and refresh tunnel identity | `200` with `expires_at` and `tunnel_fingerprint` | `404 claim_expired`; the client must claim and establish a tunnel again |
| `DELETE /api/v1/claims/{id}` | Idempotently release a lease | `204` | `401`; only its owner or an admin may release it |
| `GET /api/v1/claims` | List active leases | `200` with `claims` | `401` |
| `GET /api/v1/routes` | Sidecar route table with `ETag`/`If-None-Match` | `200` with `version` and routes, or `304` | May require a dedicated sidecar token |
| `GET /healthz` | Liveness/readiness | `200` | — |

A route contains `path_prefix`, `tunnel_addr`, `claim_id`, `owner`, and `expires_at`. The `local` field on a claim is informational; the CLI fixes the actual forwarding target to `127.0.0.1:<--to>`.

### 2.4 Tunnel establishment

1. The client creates a claim and receives `remote_port`.
2. It connects to `wss://<host>/tunnel` with `X-Tunlease-Claim` and, when authentication is enabled, the bearer token. TLS 1.3 runs inside WebSocket and the client pins the certificate fingerprint returned by the claim response.
3. The gateway must verify that the requested remote port belongs to a live lease owned by the current authenticated or anonymous identity.
4. `tunnel_addr` is the gateway's `pod_ip:remote_port`; sidecars connect directly rather than through the Service. V1 therefore uses one gateway replica.
5. The tunnel sends a 25-second yamux keepalive. Ingress read/send timeouts must be at least 3600 seconds.

### 2.5 Lease and path semantics

- Default TTL is 300 seconds and heartbeat interval is 30 seconds. Clients use values returned by the server rather than hard-coding them.
- Only prefix patterns ending in `/*` are supported. The sidecar chooses the longest matching prefix.
- A new path conflicts with an active path if either prefix contains the other; return `409` with owner and expiry.
- The allowlist is opt-in: an empty allowlist permits any path; when prefixes are configured, every claimed path must be under one of them.
- Claim, release, and expiry emit structured audit events with owner, time, paths, and claim ID.

### 2.6 Sidecar behavior

- Poll `/api/v1/routes` every three seconds with ETag support.
- If polling fails, keep the previous table for at most 60 seconds, then clear it and send all traffic to the app.
- If tunnel dialing or response headers exceed one second, immediately replay the request to the app and increment the fallback metric.
- Preserve method, headers, body, and streaming. Add `X-DevProxy-Claim: <claim_id>` only for tunnel traffic.
- Expose `devproxy_sidecar_requests_total{route="app|tunnel|fallback"}`, route-table age, and route-fetch errors.

An embedding client only needs the claim endpoints, tunnel protocol, and lease semantics above. The protocol supports multiplexed TCP streams for one claim and deliberately has no arbitrary target, SOCKS, UDP, shell, or file-transfer mode.

## 3. Implementation choices

| Area | Choice |
|---|---|
| Shared | Go 1.25.7 or newer, one monorepo, `CGO_ENABLED=0` |
| CLI | Cobra, coder/websocket, and yamux; flag → `TUNLEASE_*` env → `~/.tunlease.yaml` |
| Gateway | coder/websocket, yamux, TLS 1.3, `net/http`, memory or Redis registry |
| Sidecar | `httputil.ReverseProxy`, Prometheus client, and slog; no Envoy |
| Deployment | Multi-stage/distroless images, gateway Helm chart, sidecar patch |
| Release | GitLab CI, Pages installer/checksums, Generic Packages on tags, and self-update |

Intentionally excluded: Envoy/Istio, frp, a web framework, and SQLite. The smaller dependency surface is easier to operate and hand over.

## 4. Delivery phases and acceptance

### Phase 1 — protocol foundation

- [x] In-memory registry API, purpose-built tunnel server, port allocation, and owner validation.
- [x] CLI claim, list, release, heartbeat, automatic re-claim, and Ctrl+C cleanup.
- [x] Conflict errors include owner/expiry; allowlist violations return 403.

### Phase 2 — sidecar and Redis

- [x] ETag polling, longest-prefix routing, all fail-open rules, and metrics.
- [x] Redis-backed leases and audit logs.
- [x] Compose E2E: app before claim, localhost during claim, app after release/TTL.
- [x] Gateway outage clears stale routes without failed app traffic.
- [x] Tunnel outage falls back to the app and increments the fallback metric.

### Phase 3 — staging and distribution

- [x] Gateway Helm chart, Ingress, values, and sidecar patch.
- [x] Binary distribution, checksums, images, and CLI self-update.
- [x] Public endpoint → fixed-endpoint workload sidecar → gateway → localhost in a staging environment.
- [x] Tunnel remains usable after ten idle minutes.
- [x] Replacing the in-memory gateway Pod causes fail-open app traffic, followed by automatic CLI re-claim and tunnel recovery.
- [x] CI publishes Linux/macOS amd64/arm64 binaries, checksums, and a shell installer.
- [x] CI publishes the native Windows amd64 binary, checksum, and PowerShell installer.
- [x] macOS arm64 install/update verified.
- [ ] Run claim/callback/release on a Linux host.

## 5. Security baseline

- Fail open to the app on registry, route-table, or tunnel failure.
- Only platform-approved prefixes may be claimed.
- Optionally authenticate APIs and tunnels; audit claim, release, and expiry. OIDC is a v2 concern.
- Warn clearly that a claim receives real third-party staging traffic. Read-only mirroring is out of scope for v1.

## 6. Out of scope

- Client implementations maintained by embedding applications.
- OIDC, read-only mirroring, and multi-replica gateway port coordination.
- Retirement of any pre-existing tunnel mechanism in the adopting application.

If Ingress behavior makes Section 2 impossible to implement, propose a protocol change instead of silently diverging. An unsynchronized workaround is an integration bug for future clients.
