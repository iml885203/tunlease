# <img src="assets/icon.png" width="32" height="32" alt=""> Tunlease

**在自己電腦上 debug webhook，用它真實、改不了的 callback URL——不重新部署、不換新 URL。**

在既有的固定 endpoint 上認領一條 path，它的即時流量就進到你的電腦，而其他 path
照常打到真正的 app。Ctrl+C 釋放。

```bash
tunle claim /webhooks/stripe/* --to 8080 --gateway staging.myapp.com
```

![claim 前固定 callback URL 回 app 的 404，`tunle claim` 後同一 URL 打到本機服務](assets/demo.gif)

它不另外發布 endpoint，而是保留已經在使用的 callback URL。
[與其他工具的比較](#與其他工具的比較)。

[開發者快速上手](#開發者快速上手) · [平台部署](docs/platform-deployment.zh-TW.md) · [架構](docs/architecture.zh-TW.md) · [疑難排解](docs/developer-guide.zh-TW.md#疑難排解)

[English](README.md) · [繁體中文](README.zh-TW.md)

## 運作方式

```mermaid
flowchart LR
    TP["第三方<br/>(例如 Stripe)"] -->|"呼叫固定 URL"| GW[tunlease gateway]

    subgraph Shared["共用環境"]
        GW[tunlease gateway]
        App[原始 app]
        GW -->|"其他所有 path<br/>(fail-open)"| App
    end

    subgraph Developer["開發者電腦"]
        CLI[tunle CLI]
        Local[你的本機服務]
        CLI -->|"反向 tunnel"| Local
    end

    GW -->|"已認領的 path"| CLI

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class GW,CLI tunlease;
```

Gateway 接收固定 host 的流量並依 path 分流：**已認領**且 tunnel 已連線的 path
到達你的電腦；其他 path proxy 到設定的原 app。藍色節點是 Tunlease 的；其餘本來就存在。

安全模型包含 path allowlist、互斥的 connected tunnel、可選 token 認證、audit log 與
origin fallback。Fallback 只涵蓋 dispatch 前沒有 matching connected session 的 request。
Gateway、Ingress 與 origin outage 需另外規劃 infrastructure 或 bypass。詳見
[路由與失敗契約](docs/architecture.zh-TW.md#路由與失敗契約)。

## 與其他工具的比較

精神上類似 [ngrok](https://ngrok.com/)、
[localtunnel](https://github.com/localtunnel/localtunnel)、
[bore](https://github.com/ekzhang/bore)——但工作流程不同。一般 tunnel 會替本機服務
發布一個 public endpoint；Tunlease 則保留第三方**既有的固定** callback URL，讓開發者
暫時 claim 其中個別 path。

下表比較的是工具內建的工作流程，不是透過 custom domain、routing policy 或外部
reverse proxy 能組合出的所有架構。

| | Tunlease | ngrok | localtunnel | bore |
|---|---|---|---|---|
| 主要 endpoint 模型 | 既有 host + 已 claim 的 HTTP path | Public 或 custom URL | 分配的 URL | Public TCP port |
| Session 範圍、互斥的 path claim | 內建 | 沒有對等的 claim workflow | 無 | 無 |
| 未 claim 的 path 自動回原 app | 內建 | 需要 routing policy | 需要外部 proxy | 需要外部 proxy |
| 多位開發者在同一 host claim 不同 path | 內建 | 需要手動設定 routing | 無 | 無 |
| 目前受支援的 relay 為開源且可自架 | 是 | 否 | 是 | 是 |

## 開發者快速上手

平台團隊把既有 callback host 導入 gateway，並提供 URL、允許的 prefix 與可選 token
後，安裝 CLI 再 claim。若 gateway 還沒架好，請先看
[平台部署指南](docs/platform-deployment.zh-TW.md)。

```bash
# macOS 與 Linux
# 請把 YOUR_TUNLEASE_HOST 換成團隊發布 installer 的 host。
curl -fsSL https://YOUR_TUNLEASE_HOST/install/install.sh | bash
```

```powershell
# Windows PowerShell（amd64）
# 請把 YOUR_TUNLEASE_HOST 換成團隊發布 installer 的 host。
irm https://YOUR_TUNLEASE_HOST/install/install.ps1 | iex
```

接著用一行命令認領 path——用 `--gateway` 指向 gateway（就填平台團隊給你的網域），
用 `--to` 把 path 轉到本機 port。Ctrl+C 釋放 path。scheme 預設為 `https`，可以省略；
若 gateway 沒有 TLS（例如 localhost）就明確加上 `http://`。control plane 的路徑前綴由
client 自動附加，所以不用自己在網址裡加 `/_tunlease`。

```bash
tunle claim /webhooks/provider/callback/* --to 8080 --gateway myapp.example.com
```

Claim 會收到真實 staging callback，包含其中的資料與 credential。請先啟動本機服務、
只 claim 所需的最窄 path，並確保 callback handler idempotent：provider retry
與 request 中途 tunnel failure 都可能造成重複 delivery。

若不想每次重打 `--gateway`，設成環境變數一次即可：

```bash
export TUNLEASE_GATEWAY=myapp.example.com
tunle claim /webhooks/provider/callback/* --to 8080
```

或者，若偏好用檔案，放進 `~/.tunlease.yaml`（gateway 需要認證時，`token:` 也寫這裡）：

```yaml
gateway: myapp.example.com
```

其餘指令都用同樣的簡短形式：

```bash
tunle list                                    # 你目前的 claim
tunle list --all                              # gateway 上所有 claim
tunle release /webhooks/provider/callback/*   # 釋放某個 path
tunle release --to 8080                        # 釋放某個 port 上的全部
tunle update                                  # 自我更新 binary
tunle --version
```

完整用法與問題排查請看[開發者指南](docs/developer-guide.zh-TW.md)。

Windows amd64 使用 Tunlease 專用的 tunnel transport。PowerShell installer 會驗證發布的 SHA-256 checksum，並把上一版保留為 `tunle.exe.prev`。

## 元件與部署模型

全部都是同一個 `tunle` binary，用 subcommand 切換角色：

| 命令 | 執行於 | 職責 |
|---|---|---|
| `tunle claim`（及 `list` / `release`） | 開發者電腦 | 把一條 path 連到本機服務 |
| `tunle gateway` | 前置於 app | 管理 active path、終止 tunnel、路由 request 並 proxy 到原 app |

Gateway 位於 app 前面，並包辦 server 端所有事情。它把 control plane 放在
固定的 `/_tunlease` 底下；其餘 path 都是第三方流量——符合 claim
時 tunnel 給開發者，否則 proxy 回必填的 `fail_open_url`（原始 app）。
沒有獨立的 sidecar process。

- **任何環境、單一 app origin** — 跑 `tunle gateway`，把 `fail_open_url` 指向 app 的 Service，並將
  gateway 部署在 app 前面（Ingress → gateway）。這是 Helm chart 部署的模型。

Gateway 不會呼叫 Kubernetes API，因此 Kubernetes 不是必要條件——它只是平台模型的建議部署目標。

## 嵌入 tunnel client

Go 應用程式可以直接嵌入相同的 path ownership、重新連線與 tunnel engine，使用者不需要另外安裝 `tunle` binary。

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
```

認證方式、完整 lifecycle 範例、API 行為、錯誤處理、升級與整合測試請看[嵌入 Go client](docs/go-client.zh-TW.md)。

## 本機開發

需要 Go 與 Docker Compose；只有驗證部署 manifest 時才需要 Helm 和 kubectl。

```bash
make build      # 建置 tunle binary 到 bin/
make test       # 執行 Go tests
make lint       # 執行固定版本的 golangci-lint container
make preflight  # build、vet、race test、lint 與格式檢查
make e2e        # gateway + origin app + local app + 真實 CLI
```

## 依角色閱讀

- **要接第三方 callback 的開發者：**[開發者指南](docs/developer-guide.zh-TW.md)——安裝、設定、CLI 與疑難排解（[English](docs/developer-guide.md)）
- **平台與 service owner：**[平台部署指南](docs/platform-deployment.zh-TW.md)——whole-host routing、必填 origin、Helm、rollout 與安全（[English](docs/platform-deployment.md)）
- **要理解或修改系統的貢獻者：**[架構](docs/architecture.zh-TW.md)——control/data plane、routing 與復原流程（[English](docs/architecture.md)）
- **要在 Go 應用程式嵌入 tunnel 的開發者：**[嵌入 Go client](docs/go-client.zh-TW.md)——module 設定、lifecycle API、錯誤與測試（[English](docs/go-client.md)）

## 目前狀態

Gateway、CLI 與可重用 Go client 皆可運作。目前刻意只支援單一 gateway
replica：active path 與 WebSocket session 在同一 process。Gateway 重啟會斷線；
恢復後，仍在執行的 client 會重新連線並登記 path。

## 參與貢獻

歡迎貢獻——本機設定與 `make preflight` 品質檢查請看 [CONTRIBUTING.md](CONTRIBUTING.md)。
安全性問題請依 [SECURITY.md](SECURITY.md) 私下回報。社群互動遵循
[行為準則](CODE_OF_CONDUCT.md)。

## 授權

[MIT](LICENSE)
