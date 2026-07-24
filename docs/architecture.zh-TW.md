# 架構

[English](architecture.md) · [繁體中文](architecture.zh-TW.md)

## 系統概觀

```mermaid
flowchart LR
    ThirdParty[第三方系統]

    subgraph Cluster[共用環境]
        Ingress[既有 Ingress<br/>固定 public endpoint]

        subgraph Workload[Provider endpoint Pod]
            Sidecar[tunlease-sidecar<br/>path router]
            App[原始應用程式<br/>固定 endpoint workload]
        end

        subgraph GatewayPod[tunlease-gateway Pod]
            API[Claim 與 route API]
            Registry[租約 registry<br/>staging 使用 memory]
            TunnelServer[專用 tunnel server]
        end
    end

    subgraph Developer[開發者電腦]
        CLI[tunle CLI]
        LocalApp[本機服務]
    end

    ThirdParty -->|固定 URL| Ingress
    Ingress --> Sidecar
    Sidecar -->|未認領或失敗| App
    Sidecar -->|已認領 path| TunnelServer
    CLI -->|claim 與 heartbeat| API
    API <--> Registry
    Sidecar -.->|每 3 秒取得 route table| API
    CLI ==>|反向 WebSocket tunnel| TunnelServer
    TunnelServer -->|轉送 request| LocalApp

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class CLI,Sidecar,API,Registry,TunnelServer tunlease;
```

藍色節點由 Tunlease 擁有並發布；其他節點是第三方系統或環境中既有的基礎設施與應用程式。Control plane 包含 claim、heartbeat、lease 與 route-table polling；data plane 則是 request 經 sidecar 前往原始 app 或反向 tunnel 的路徑。Sidecar 不依賴 Kubernetes API。

## Request routing

```mermaid
flowchart TD
    Request[Request 到達 tunlease-sidecar]
    Match{是否符合 active route?}
    Tunnel{Tunnel 是否在 timeout 內回應?}
    Local[轉到本機服務]
    App[轉到原始 app]

    Request --> Match
    Match -->|否| App
    Match -->|是| Tunnel
    Tunnel -->|是| Local
    Tunnel -->|否：fail open| App

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class Request,Match,Tunnel tunlease;
```

Sidecar 使用 longest-prefix matching。Route table 超過最大 stale 時間仍無法更新時，會清空 route table，讓所有 request 回到原始 app。

## Claim 生命週期

```mermaid
sequenceDiagram
    participant Dev as 開發者
    participant CLI as tunle CLI
    participant GW as tunlease-gateway
    participant SC as tunlease-sidecar
    participant Local as 本機服務

    Dev->>CLI: tunle claim --to 8080 /path/*
    CLI->>GW: POST /api/v1/claims
    GW-->>CLI: claim ID、TTL、heartbeat、fingerprint
    CLI->>GW: 開啟反向 tunnel
    loop Claim 存活期間
        CLI->>GW: Heartbeat
        SC->>GW: Poll /api/v1/routes
    end
    SC->>Local: 經 tunnel 轉送符合的 callback
    Dev->>CLI: Ctrl+C
    CLI->>GW: DELETE claim
```

## Gateway Pod 替換

使用 memory registry 時，替換 gateway Pod 會移除所有 lease 與 tunnel。這是目前單 replica 部署的預期行為：

1. Sidecar 無法連到舊 tunnel，先 fail-open 回原始 app。
2. CLI reconnect 後發現 lease 不存在，建立新 claim。
3. CLI 建立新 tunnel，sidecar 取得新 route。
4. 符合的 callback 恢復轉送到本機服務。

這個行為已在 staging 環境驗證；callback 約 17 秒內恢復，過程沒有回傳 gateway 產生的 5xx。

## 部署邊界

核心 protocol 不要求 Kubernetes。Kubernetes 只是目前的 packaging 與 lifecycle 模型：

- Helm 部署共用 gateway、Service、Ingress 與 config Secret。
- 目標應用程式在自己有版本控制的 Deployment 中加入 `tunlease-sidecar`。
- Sidecar 接手應用程式原本的 Service port；應用程式移到 Pod-local port。
- Public host、path 與第三方設定完全不變。
