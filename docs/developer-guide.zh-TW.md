# 開發者指南

[English](developer-guide.md) · [繁體中文](developer-guide.zh-TW.md)

## 安裝

```bash
# 使用 Homebrew 的 macOS 或 Linux
brew install iml885203/tap/tunlease
```

```powershell
# 使用 Scoop 的 Windows
scoop bucket add tunlease https://github.com/iml885203/scoop-bucket
scoop install tunlease
```

Homebrew 會安裝 bash、zsh 與 fish shell completions。CLI 也可透過
`tunle completion SHELL` 直接輸出 completion script。

## 連線

向平台團隊取得 callback host、允許的 path，以及可選的個人 token。先啟動本機
service，再執行：

```bash
export TUNLEASE_GATEWAY=callbacks.staging.example.com
export TUNLEASE_TOKEN=YOUR_TOKEN
tunle claim '/webhooks/provider/callback/*' --to 8080
```

Gateway URL 只填 host；client 會加入固定的 `/_tunlease`。預設使用 HTTPS，
只有本機開發才明確填 `http://`。

也可在 `~/.tunlease.yaml` 設定 `gateway`、`token`、`insecure` 與
`default_scheme`。應優先安裝正確 CA；`--insecure` 會停用 WSS server
verification，只能用於可信的開發網路。

此檔案並非必要；沒有它仍可使用 flags 與 environment variables。檔案存在時，
每個 client command 都會 strict parse，並在連線前回報檔案位置、malformed
YAML、未知 key、錯誤 value type 或非法 `default_scheme`。

Stripe、GitHub、Slack 與 OAuth 範例請見
[provider recipes](webhook-recipes.zh-TW.md)。

## Lifecycle

`claim` 成功代表 path ownership 與 data tunnel 都已 ready。Foreground
CLI 會在 claim 前探測本機 port；port 尚未接受連線時會顯示 warning，但不阻止
claim，讓本機 service 可以稍後啟動。連線期間每次本機連線失敗都會印在
foreground，或寫入 detached claim log。
process 擁有兩者：Ctrl+C 或連線結束會立即釋放。暫時斷網時會自動重連；
claim ID 會改變，空窗期間 request 走 origin。

```bash
tunle list
tunle list --all
tunle release '/webhooks/provider/callback/*'
tunle release --to 8080
```

`-d` 是 `--detach` 的 shorthand，會啟動背景 process。Automation 必須在
cleanup 執行 `release`。
請以 command exit status 作為介面；human-readable output 不是穩定序列化格式。
`~/.tunlease/state.json` 會包含供啟用 dynamic identity 的 gateway 使用、每個
gateway 各自隨機產生的 client identity。請保護此檔案；這些值能讓其他 process
list 或 release 該身分的 claim，但不授予 cloud 或 account 權限。

只 claim 最窄的 path，絕不要 claim `/`，並把 callback 視為真實 staging
credential 與個資。Local handler 必須 idempotent：provider retry 或 dispatch
後 tunnel 失敗可能造成重複 delivery。

## 疑難排解

- **`path_claimed`**：另一條 connected session 擁有重疊 prefix。
- **`path_not_allowed`**：請平台團隊提供 allowlisted prefix。
- **`claim_limit_reached`**：gateway 已達 `max_claims`。
- **`owner_claim_limit_reached`**：這個 client identity 已達
  `max_claims_per_owner`。
- **`claim_expired`**：gateway 的 `max_claim_duration` 已終止 session 並釋放
  paths。
- **Request 到 origin**：確認 claim process 已連線、path 相符、本機 port 可回 HTTP。
- **`502 This path is claimed, but its local service is unavailable.`**：path
  已被 claim，但 client 無法連到設定的 localhost port。公開 response 只引導
  tunnel owner 查看 terminal，不暴露本機細節；啟動本機 service，並檢查
  foreground output 或 `~/.tunlease/claim-PORT.log`。
- **TLS error**：安裝 internal CA；`--insecure` 只作可信網路的暫時診斷。
- **Gateway path 被拒絕**：只傳 host 或 origin URL，不要附 `/_tunlease`。

使用 Homebrew 更新：

```bash
brew upgrade tunlease
```

使用 Scoop 更新：

```powershell
scoop update tunlease
```

直接安裝的使用者可重新執行 installer。只有從 release mirror 安裝時才設定
`TUNLEASE_BASE_URL`。
