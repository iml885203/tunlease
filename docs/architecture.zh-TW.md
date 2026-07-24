# 架構

[English](architecture.md) · [繁體中文](architecture.zh-TW.md)

## 系統概觀

```mermaid
flowchart LR
    ThirdParty[第三方系統]

    subgraph Cluster[共用環境]
        Ingress[既有 Ingress<br/>固定 public endpoint]

        subgraph GatewayPod[tunlease-gateway Pod]
            Router[HTTP path demux<br/>control_prefix + fail_open_url]
            API[Claim API]
            Registry[租約 registry<br/>staging 使用 memory]
            TunnelServer[專用 tunnel server]
        end

        App[原始應用程式<br/>固定 endpoint workload]
    end

    subgraph Developer[開發者電腦]
        CLI[tunle CLI]
        LocalApp[本機服務]
    end

    ThirdParty -->|固定 URL| Ingress
    Ingress --> Router
    Router -->|未認領或失敗| App
    Router -->|已認領 path| TunnelServer
    Router -->|control_prefix path| API
    CLI -->|claim 與 heartbeat| API
    API <--> Registry
    CLI ==>|反向 WebSocket tunnel| TunnelServer
    TunnelServer -->|轉送 request| LocalApp

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class CLI,Router,API,Registry,TunnelServer tunlease;
```

藍色節點由 Tunlease 擁有並發布；其他節點是第三方系統或環境中既有的基礎設施與應用程式。Gateway 位於 app 前面，並自行依 path 分流：走 `control_prefix`（預設 `/_tunlease`）的 request 進 claim API，其餘 path 則是第三方流量。Control plane 包含 claim、heartbeat 與 lease；data plane 則是 request 經 gateway 前往反向 tunnel，或在未認領 path 或 tunnel 失敗時回到原始 app（`fail_open_url`）的路徑。Gateway 不依賴 Kubernetes API。

## Request routing

```mermaid
flowchart TD
    Request[Request 到達 gateway]
    Control{Path 是否在<br/>control_prefix 底下?}
    API[交給 claim API 處理]
    Match{是否符合 active claim?}
    Tunnel{Tunnel 是否在 timeout 內回應?}
    Local[轉到本機服務]
    App[轉到原始 app<br/>fail_open_url]

    Request --> Control
    Control -->|是| API
    Control -->|否| Match
    Match -->|否| App
    Match -->|是| Tunnel
    Tunnel -->|是| Local
    Tunnel -->|否：fail open| App

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class Request,Control,Match,Tunnel tunlease;
```

Gateway 對 active claim 使用 longest-prefix matching。沒有符合 claim 的 path，或 claim 的 tunnel 無回應時，會走原始 app 路徑（`fail_open_url`）；若未設定 `fail_open_url` 則回傳 404。

## Claim 生命週期

```mermaid
sequenceDiagram
    participant Dev as 開發者
    participant CLI as tunle CLI
    participant GW as tunlease-gateway
    participant Local as 本機服務

    Dev->>CLI: tunle claim --to 8080 /path/*
    CLI->>GW: POST /api/v1/claims
    GW-->>CLI: claim ID、TTL、heartbeat、fingerprint
    CLI->>GW: 開啟反向 tunnel
    loop Claim 存活期間
        CLI->>GW: Heartbeat
    end
    GW->>Local: 經 tunnel 轉送符合的 callback
    Dev->>CLI: Ctrl+C
    CLI->>GW: DELETE claim
```

## Gateway Pod 替換

使用 memory registry 時，替換 gateway Pod 會移除所有 lease 與 tunnel。這是目前單 replica 部署的預期行為：

1. Gateway 無法再對到舊 claim，先 fail-open 回原始 app。
2. CLI reconnect 後發現 lease 不存在，建立新 claim。
3. CLI 建立新 tunnel，gateway 註冊新 claim。
4. 符合的 callback 恢復轉送到本機服務。

這個行為已在 staging 環境驗證；callback 約 17 秒內恢復，過程沒有回傳 gateway 產生的 5xx。

## 部署邊界

核心 protocol 不要求 Kubernetes。Kubernetes 只是目前的 packaging 與 lifecycle 模型：

- Helm 部署 gateway、Service、Ingress 與 config Secret。
- Gateway 部署在 app 前面：Ingress 把固定 endpoint 導向 gateway，gateway 的 `fail_open_url` 指向 app 的 Service。
- Public host、path 與第三方設定完全不變。
