# 導入 Tunlease

[English](adoption-guide.md) · [繁體中文](adoption-guide.zh-TW.md)

當第三方持續呼叫團隊無法修改的固定 callback URL，而開發者需要把 staging 的
特定 path 暫時轉到 localhost 時，請從這份 checklist 開始。它串起各角色的
指南，不取代其中的詳細內容。

改動 production-like routing 前，請先讀[核心概念](concepts.zh-TW.md)的 canonical
topology、URL map、詞彙與 failure contract。

## 1. Tunlease 適合你嗎？

- [ ] 你有第三方固定 endpoint、開發者需要暫時認領個別 callback path，而且
  未認領或轉送失敗的 traffic 必須繼續交給原始 app。

Tunlease 是專注於 callback 開發的 tunnel，不是通用 VPN 或 proxy。它的安全模型
包含 path allowlist、互斥且有 TTL 的 lease、可選的 token authentication、
structured audit log，以及 fail-open 回原始 app。系統、失敗路徑與 protocol
細節請看[架構](architecture.zh-TW.md)。

除非下列條件全部成立，否則不應導入：

- 平台 owner 能把既有 staging callback host 導入 gateway，並讓原 app 保留可獨立連線的 internal origin。
- 保留的 `/_tunlease` namespace 不會和 app 衝突。
- 團隊接受 gateway 會前置該 host 的全部流量，而 fail-open 保護的是 tunnel selection，
  不是 gateway/Ingress outage。
- 允許真實 staging data 到達已授權 developer laptop。

| 責任 | 常見 owner |
|---|---|
| DNS、TLS、Ingress cutover、HA/bypass、rollback | Platform team |
| Origin URL、callback allowlist、idempotency、synthetic test | Service owner |
| Token、audit access、data-handling policy | Platform/security owner |
| Local service、最窄 claim、cleanup | Developer |

## 2. 取得 image

- [ ] Build `tunlease-gateway`，並 push 到你的 cluster 可以 pull 的 registry。

本 repo 不發布共用 image 或 installer；developer quick start 可執行前，平台團隊
必須先發布 artifact。

手動 GitLab CI job `build-images-self-hosted` 使用 `docker buildx` build gateway
Dockerfile target，產出 `linux/amd64` 與 `linux/arm64`，接著直接 push
`$TARGET_REGISTRY/tunlease-gateway:$TAG`。`TARGET_REGISTRY` 預設為
`$CI_REGISTRY_IMAGE`；registry host 與 credential 則分別透過
`REGISTRY_HOST`、`REGISTRY_USER`、`REGISTRY_PASSWORD` 提供。

外部團隊可以把該 job 複製到自己的 pipeline，將 `TARGET_REGISTRY` 與三個
registry login variable 改成自己的 registry。Build 會使用 Go 的
`TARGETOS`／`TARGETARCH` 支援，直接 push multi-arch manifest。

本 repo **沒有公開、可連線的共用 image source**。請從 source 使用複製後的 job
build；或先取得團隊可存取的來源，再使用以下 mirror 範本。所有角括號內的值都必須
自行替換：

```bash
export TUNLEASE_TAG="<COMMIT_SHA_OR_RELEASE_TAG>"

skopeo copy --all \
  "docker://<ACCESSIBLE_SOURCE_REGISTRY>/tunlease-gateway:$TUNLEASE_TAG" \
  "docker://<YOUR_REGISTRY>/tunlease-gateway:$TUNLEASE_TAG"
```

部署前提與 image override 請看
[平台部署指南](platform-deployment.zh-TW.md)。

## 3. 部署 Gateway

- [ ] 依照
  [平台部署 → 安裝 Gateway](platform-deployment.zh-TW.md#安裝-gateway)。

API 與反向 tunnel 共用同一個 HTTP Ingress path；tunnel 是 WebSocket
connection。Ingress 或 load balancer 必須保留 WebSocket upgrade，read/send
timeout 至少設為 3600 秒。不需要 UDP listener 或 forwarding。

把既有 callback host 的 `/` 不經 rewrite 導向 gateway。只掛 `/tunlease`
無法攔截 `/webhooks/...`。

## 4. 用 Gateway 前置 App

- [ ] 依照
  [平台部署 → 用 Gateway 前置 App](platform-deployment.zh-TW.md#用-gateway-前置-app)，
  讓固定 endpoint 的 Ingress 導向 gateway。

Gateway 會把已認領的 path 經 tunnel 轉送，其餘 traffic 則 fail-open 回 app。
把 `fail_open_url` 設為 app 的 Service。

## 5. 開發者認領 path

- [ ] 選擇使用 [`tunle` CLI](developer-guide.zh-TW.md)，或在 Go
  應用程式直接嵌入 [`pkg/tunnelclient`](go-client.zh-TW.md)。

兩種方式都會建立並維持相同的 claim、lease、heartbeat 與反向 tunnel
lifecycle。請先啟動本機服務，再認領核准範圍內最小的 path。

## 6. 驗證完整流程

- [ ] 認領測試 path，確認 `tunle list` 看得到它。
- [ ] 透過第三方看到的固定 URL 呼叫該 path，確認 request 到達 localhost。
- [ ] Release claim，再確認該 path 回到原始 app。
- [ ] 只中斷 developer tunnel，確認 dispatch 前的 traffic 會到 origin。
- [ ] 另外演練平台的 gateway-outage HA/bypass 與 rollback。
- [ ] Onboard developer 前驗證真實 provider signature 與 duplicate-delivery/idempotency case。
