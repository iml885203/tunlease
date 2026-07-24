# 導入 Tunlease

[English](adoption-guide.md) · [繁體中文](adoption-guide.zh-TW.md)

當第三方持續呼叫團隊無法修改的固定 callback URL，而開發者需要把 staging 的
特定 path 暫時轉到 localhost 時，請從這份 checklist 開始。它串起各角色的
指南，不取代其中的詳細內容。

## 1. Tunlease 適合你嗎？

- [ ] 你有第三方固定 endpoint、開發者需要暫時認領個別 callback path，而且
  未認領或轉送失敗的 traffic 必須繼續交給原始 app。

Tunlease 是專注於 callback 開發的 tunnel，不是通用 VPN 或 proxy。它的安全模型
包含 path allowlist、互斥且有 TTL 的 lease、可選的 token authentication、
structured audit log，以及 fail-open 回原始 app。系統與失敗路徑請看
[架構](architecture.zh-TW.md)，protocol 與安全細節請看
[v1 規格](spec-v1.zh-TW.md)。

## 2. 取得 image

- [ ] Build `tunlease-gateway` 與 `tunlease-sidecar`，並 push 到你的 cluster
  可以 pull 的 registry。

手動 GitLab CI job `build-images-self-hosted` 使用 `docker buildx` build 兩個
Dockerfile target，產出 `linux/amd64` 與 `linux/arm64`，接著直接 push
`$TARGET_REGISTRY/tunlease-gateway:$TAG` 與
`$TARGET_REGISTRY/tunlease-sidecar:$TAG`。`TARGET_REGISTRY` 預設為
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
skopeo copy --all \
  "docker://<ACCESSIBLE_SOURCE_REGISTRY>/tunlease-sidecar:$TUNLEASE_TAG" \
  "docker://<YOUR_REGISTRY>/tunlease-sidecar:$TUNLEASE_TAG"
```

Gateway 與 sidecar 請使用相同 tag。部署前提與 image override 請看
[平台部署指南](platform-deployment.zh-TW.md)。

## 3. 部署 Gateway

- [ ] 依照
  [平台部署 → 安裝 Gateway](platform-deployment.zh-TW.md#安裝-gateway)。

API 與反向 tunnel 共用同一個 HTTP Ingress path；tunnel 是 WebSocket
connection。Ingress 或 load balancer 必須保留 WebSocket upgrade，read/send
timeout 至少設為 3600 秒。不需要 UDP listener 或 forwarding。

## 4. 加入 Sidecar

- [ ] 在擁有固定 endpoint 的 workload 旁，依照
  [平台部署 → 加入 Sidecar](platform-deployment.zh-TW.md#加入-sidecar)。

Sidecar 會把已認領的 path 經 gateway 轉送，其餘 traffic 則 fail-open 回 app。
Gateway 與 sidecar config 中的 `sidecar_token` 必須相同。

## 5. 開發者認領 path

- [ ] 選擇使用 [`tunlease` CLI](developer-guide.zh-TW.md)，或在 Go
  應用程式直接嵌入 [`pkg/tunnelclient`](go-client.zh-TW.md)。

兩種方式都會建立並維持相同的 claim、lease、heartbeat 與反向 tunnel
lifecycle。請先啟動本機服務，再認領核准範圍內最小的 path。

## 6. 驗證完整流程

- [ ] 認領測試 path，確認 `tunlease list` 看得到它。
- [ ] 透過第三方看到的固定 URL 呼叫該 path，確認 request 到達 localhost。
- [ ] Release claim，再確認該 path 回到原始 app。
