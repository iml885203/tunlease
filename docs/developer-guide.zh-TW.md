# 開發者指南

[English](developer-guide.md) · [繁體中文](developer-guide.zh-TW.md)

Tunlease 可以暫時把 staging 固定 callback path 轉到你的電腦。本指南假設平台團隊已部署 `tunlease-gateway`、在目標 endpoint 加入 `tunlease-sidecar`，並設定可認領的 path。你不需要 Kubernetes 權限。

開始前，向 Tunlease 維護者取得：

- Gateway URL。
- 只有 gateway 啟用 client authentication 時才需要個人 token。
- 你可以認領的 path prefix。

CLI 不會自動建立或搜尋 token。Gateway 使用預設的無認證設定時不需要 token。

## 安裝與更新

```bash
# macOS 與 Linux
curl -fsSL https://tunlease.example.com/install/install.sh | bash
tunle --version
```

```powershell
# Windows PowerShell（amd64）
irm https://tunlease.example.com/install/install.ps1 | iex
tunle --version
```

macOS/Linux installer 會偵測 OS 與 amd64/arm64，安裝到 `~/.local/bin/tunle`，驗證 SHA-256，並把上一版保留為 `.prev`。

Windows installer 會下載原生 amd64 executable 到 `%LOCALAPPDATA%\tunlease`、驗證 SHA-256、把上一版保留為 `tunle.exe.prev`，並將目錄加入 user `PATH`。tunnel transport 是 Tunlease 專用的。

```bash
tunle update
```

`tunle update` 會驗證 checksum 並保留前一版。Windows 請重新執行 PowerShell installer 更新。

## 設定

macOS/Linux 建立 `~/.tunlease.yaml` 並限制權限。只要填平台團隊給你的 gateway 網域即可——
client 會自動附加 control plane 的路徑前綴，scheme 也預設為 `https`，可以省略（若 gateway
沒有 TLS，例如 localhost，就明確加上 `http://`）：

```yaml
gateway: myapp.example.com
token: YOUR_PERSONAL_TOKEN
```

```bash
chmod 600 ~/.tunlease.yaml
```

Windows PowerShell：

```powershell
@"
gateway: myapp.example.com
token: YOUR_PERSONAL_TOKEN
"@ | Set-Content (Join-Path $HOME ".tunlease.yaml")
```

設定優先順序是 flag、環境變數、設定檔：

| 設定 | Flag | 環境變數 | YAML |
|---|---|---|---|
| Gateway URL | `--gateway` | `TUNLEASE_GATEWAY` | `gateway` |
| 可選的個人 token | `--token` | `TUNLEASE_TOKEN` | `token` |

啟用認證時，不要把 token commit 到 repo，也不要放進共用文件。

## 開啟 tunnel

先啟動本機服務，再認領最小範圍的 path：

```bash
# 服務已在 localhost:8080 listening。
tunle claim --to 8080 /webhooks/provider/callback/*
```

- Path 必須以 `/` 開頭；CLI 會正規化成以 `/*` 結尾的 prefix pattern。
- Wildcard 只能放在最後；`/a/*/b` 不合法。
- 互相重疊的 prefix 不能同時 claim；衝突時會顯示目前 owner 與到期時間。
- `claim` 會留在 foreground 維持 tunnel 與 heartbeat；Ctrl+C 會釋放租約。Process 意外結束後，server 會在 TTL 到期時清除租約。
- Claim 期間，該 path 下真正的 staging callback 都會到本機；其他 path 不受影響。

多個 path 可以共用同一個 local port：

```bash
tunle claim --to 8080 \
  /webhooks/provider/debit/* \
  /webhooks/provider/credit/*
```

## 查看與釋放

```bash
tunle list
tunle list --all
tunle release /webhooks/provider/callback/*
tunle release --to 8080
```

本機 claim metadata 存在 `~/.tunlease/state.json`，其中沒有 token。即使檔案遺失，`release PATH` 仍可查詢 server 並釋放目前 identity 擁有的租約。

## 疑難排解

`gateway is required`
: 檢查 `~/.tunlease.yaml`、`TUNLEASE_GATEWAY` 或 `--gateway` flag。

`valid bearer token required`
: Gateway 已啟用認證；請透過 `token`、`TUNLEASE_TOKEN` 或 `--token` 設定維護者提供的 token。

`path is not allowed`
: Path 不在平台 allowlist；請改用核准的 provider prefix，或請維護者調整 allowlist。

`path already claimed`
: Path 與現有租約重疊。執行 `tunle list --all` 查看 owner 與到期時間，不要接管其他開發者的測試。

Callback 仍然進到 staging app
: 確認 `claim` process 還在、local port 正確、本機服務有回應，而且 `tunle list` 看得到租約。Tunnel 無法連線時 sidecar 會刻意 fail-open 回原始 app。

無法透過 TLS 連上 gateway
: scheme 預設為 `https`，client 不會自動退回 `http`。若 gateway 沒有 TLS（例如 localhost），請改用明確的 `http://` gateway URL。

使用其他 release URL
: 設定 `TUNLEASE_BASE_URL`，或在 `tunle update` 加上 `--base-url`。
