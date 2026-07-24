# Tunlease v1 protocol 與實作規格

[English](spec-v1.md) · [繁體中文](spec-v1.zh-TW.md)

本文件定義 CLI、gateway、sidecar 三個 deliverable，並把架構轉成可執行規格。第 2 節是 compatible client 使用的 public contract。

這份規格最初使用 DevProxy 名稱，現在專案名為 `tunlease`；metrics 為了 dashboard 相容性保留 `devproxy_` prefix。

## 1. 範圍與閱讀方式

- 單一 monorepo 包含 `cmd/cli`、`cmd/gateway`、`cmd/sidecar`、共用 package、Helm chart、sidecar patch，以及發布跨平台 CLI 與 gateway/sidecar image 的 CI。
- 嵌入 Tunlease 的應用程式修改不在範圍內。Gateway 必須透過 HTTP API 與 tunnel protocol 提供全部能力，不能依賴 bundled CLI 的 private behavior。
- 第 2 節是 frozen v1 API shape。未來相容修改必須是 additive；不相容修改需先提案。
- 本文件定義建置與驗收要求；與早期架構筆記衝突時以本文件為準。

## 2. Protocol v1

### 2.1 概觀

開發者不需要 Kubernetes 權限。CLI 經既有 Web Ingress 對 gateway 建立專用 reverse WebSocket tunnel；gateway registry 管理 path lease。固定 endpoint app 旁的 sidecar 輪詢 route table：已認領 path 進 tunnel，其他 path 與所有失敗都回原始 app。

| 元件 | 位置 | 職責 |
|---|---|---|
| CLI | 開發者電腦 | Claim/release/list、tunnel lifecycle、heartbeat、Ctrl+C cleanup |
| Gateway | 共用 Deployment | Registry API、lease ownership、port allocation、reverse-tunnel server |
| Sidecar | 固定 endpoint Pod | Longest-prefix routing 與 fail-open proxy |

### 2.2 共通規則

- Control plane 服務於可設定的 `control_prefix`（預設 `/_tunlease`）。API 位於 `<control_prefix>/api/v1/`、WebSocket tunnel upgrade 位於 `<control_prefix>/tunnel`、healthz 位於 `<control_prefix>/healthz`。前綴之外的第三方流量以 path 分流：命中 active claim 者 tunnel，否則 fail open 到 `fail_open_url`（未設定時回傳 `404`）。Client 會自動附加這個前綴，scheme 也預設為 `https`，所以使用者只需設定 gateway host。
- 未設定 token 時停用 client authentication。啟用時，API 與 tunnel 都使用 `Authorization: Bearer <token>`；每個 static token 對應一個 owner，建立 tunnel 還必須提供該 owner 的 claim ID。未認證的 client 共用 `anonymous` owner。
- Error 使用 `{"error":"<machine_code>","detail":"<human message>",...}`；client 必須忽略未知 JSON field。
- 時間使用 RFC 3339 UTC，lease expiry 以 server clock 判定。

### 2.3 Registry API

| Endpoint | 用途 | 成功 | 主要錯誤 |
|---|---|---|---|
| `POST /api/v1/claims` | Claim paths；body：`{"paths":["/api/callback/*"],"local":"localhost:5000"}` | `201`，包含 `claim_id`、`owner`、`paths`、`expires_at`、`ttl_seconds`、`heartbeat_seconds`、`tunnel_fingerprint` | `409 path_claimed`、`403 path_not_allowed`、`401`、`503 claim_limit_reached` |
| `POST /api/v1/claims/{id}/heartbeat` | 延長 lease 並更新 tunnel identity | `200`，包含 `expires_at` 與 `tunnel_fingerprint` | `404 claim_expired`；client 必須重新 claim 並建立 tunnel |
| `DELETE /api/v1/claims/{id}` | Idempotent release | `204` | `401`；僅 owner 或 admin 可 release |
| `GET /api/v1/claims` | 列出 active lease | `200`，包含 `claims` | `401` |
| `GET /api/v1/routes` | Sidecar route table，支援 `ETag`／`If-None-Match` | `200`，包含 `version` 與 routes；或 `304` | 可要求專用 sidecar token |
| `GET /healthz` | Liveness/readiness | `200` | — |

Route 包含 `path_prefix`、`claim_id`、`owner`、`expires_at`。Claim 的 `local` 只供顯示；CLI 固定把實際 target 指向 `127.0.0.1:<--to>`。

### 2.4 Tunnel establishment

1. Client 建立 claim 並取得 `claim_id` 與 `tunnel_fingerprint`。
2. Client 使用 `X-Tunlease-Claim` 連到 `wss://<host>/tunnel`；啟用認證時另帶 bearer token。TLS 1.3 跑在 WebSocket 內，client pin claim response 回傳的 fingerprint。
3. Gateway 必須驗證 claim 是 active，且屬於目前 authenticated 或 anonymous identity。
4. 第三方流量以 HTTP 進入 gateway 單一 listener，依 path 分流到對應 tunnel；沒有 per-claim TCP port。因此 v1 使用單一 gateway replica。
5. Tunnel 每 25 秒送 yamux keepalive；Ingress read/send timeout 至少 3600 秒。

### 2.5 Lease 與 path 語意

- 預設 TTL 300 秒、heartbeat 30 秒；client 使用 server response，不自行 hard-code。
- 只支援以 `/*` 結尾的 prefix pattern；sidecar 選擇 longest matching prefix。
- 若新 path 與 active path 任一 prefix 包含另一方，回 `409`，並附 owner 與 expiry。
- Allowlist 是 opt-in：空 allowlist 允許任意 path；有設定 prefix 時，每個 claim 的 path 都必須位於其中一個 prefix 下。
- Claim、release、expiry 都產生包含 owner、time、paths、claim ID 的 structured audit event。

### 2.6 Sidecar 行為

- 每三秒以 ETag polling `/api/v1/routes`。
- Polling 失敗時最多保留舊 table 60 秒，之後清空並把流量送到 app。
- Tunnel dialing 或 response header 超過一秒時，立即 replay request 到 app 並增加 fallback metric。
- 保留 method、headers、body 與 streaming；只有 tunnel traffic 加上 `X-DevProxy-Claim: <claim_id>`。
- 提供 `devproxy_sidecar_requests_total{route="app|tunnel|fallback"}`、route-table age 與 route-fetch error metrics。

嵌入式 client 只需依賴上述 claim endpoints、tunnel protocol 與 lease semantics。Tunnel 以一個 claim multiplex 多條 TCP stream，刻意不提供 arbitrary target、SOCKS、UDP、shell 或 file transfer。

## 3. 實作選擇

| 區域 | 選擇 |
|---|---|
| Shared | Go 1.25.7 以上、單一 monorepo、`CGO_ENABLED=0` |
| CLI | Cobra、coder/websocket、yamux；flag → `TUNLEASE_*` env → `~/.tunlease.yaml` |
| Gateway | coder/websocket、yamux、TLS 1.3、`net/http`、memory 或 Redis registry |
| Sidecar | `httputil.ReverseProxy`、Prometheus client、slog；不使用 Envoy |
| Deployment | Multi-stage/distroless image、gateway Helm chart、sidecar patch |
| Release | GitLab CI、Pages installer/checksum、tag Generic Packages、自動更新 |

刻意排除 Envoy/Istio、frp、web framework 與 SQLite，以維持較小的 dependency surface。

## 4. Delivery 與驗收

### Phase 1 — protocol foundation

- [x] Memory registry API、專用 tunnel server、port allocation、owner validation。
- [x] CLI claim/list/release/heartbeat、自動 re-claim、Ctrl+C cleanup。
- [x] Conflict 包含 owner/expiry；allowlist violation 回 403。

### Phase 2 — sidecar 與 Redis

- [x] ETag polling、longest-prefix routing、全部 fail-open 規則與 metrics。
- [x] Redis-backed lease 與 audit log。
- [x] Compose E2E：claim 前到 app、claim 中到 localhost、release/TTL 後回 app。
- [x] Gateway outage 清除 stale route，且 app traffic 不失敗。
- [x] Tunnel outage fallback 到 app 並增加 metric。

### Phase 3 — staging 與發布

- [x] Gateway Helm chart、Ingress、values、sidecar patch。
- [x] Binary distribution、checksum、image、自動更新。
- [x] Staging 環境 public endpoint → 固定 endpoint workload sidecar → gateway → localhost。
- [x] Tunnel idle 10 分鐘後仍可用。
- [x] Memory gateway Pod replacement 時 fail-open，之後 CLI 自動 re-claim/recover。
- [x] Linux/macOS amd64/arm64 binary、checksum、shell installer。
- [x] Native Windows amd64 binary、checksum、PowerShell installer。
- [x] macOS arm64 install/update。
- [ ] Linux host claim/callback/release。

## 5. Security baseline

- Registry、route table 或 tunnel 失敗時 fail-open 到 app。
- 只能 claim 平台核准的 prefix。
- API 與 tunnel 都要驗證；audit claim、release、expiry。OIDC 是 v2 議題。
- 清楚警告 claim 會接收真實第三方 staging traffic；v1 不做 read-only mirroring。

## 6. 不在範圍內

- 由嵌入應用程式維護的 client implementation。
- OIDC、read-only mirroring、多 replica gateway port coordination。
- 移除採用端 application 既有的 tunnel mechanism。

若 Ingress behavior 讓第 2 節無法實作，必須提出 protocol change，不能靜默做不同步 workaround。
