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
`tul completion SHELL` 直接輸出 completion script。
常用短旗標為：`-p` 對應 `--to`、`-g` 對應 `--gateway`、`-t` 對應
`--token`、`-k` 對應 `--insecure`、`-d` 對應 `--detach`，以及 `-a`
對應 `--all`。`-o` 對應 `--output`。

## Console output

互動式 terminal 會用顏色區分成功的 lifecycle event、連線狀態、warning、
error 與各類 HTTP status；command 行為、exit status 及 stdout/stderr 目的地
維持不變。Pipe、redirect output 與 detached claim log 會自動停用顏色，也可
設定 `NO_COLOR=1` 明確停用。Top-level error 只會印一次。Gateway error
會包含穩定的 error code；若有明確下一步，也會附上一個 recovery action。

Automation 可在 client command 使用 `--output json`（`-o json`）。`list`
會回傳一份 JSON document；foreground `claim` 與 multi-path `release` 使用
newline-delimited JSON events。每份資料都包含 `schema_version: 1` 與穩定的
`type`。Error 會寫入 stderr 並包含穩定的 `code`；成功維持 exit 0，錯誤維持
exit 1，與 text mode 相同。JSON output 不包含 ANSI escape。例如：

```json
{"schema_version":1,"type":"connected","paths":["/callback"],"target":"localhost:8080","local_port":8080}
{"schema_version":1,"type":"request","method":"POST","path":"/callback","status":200,"duration_ms":42}
{"schema_version":1,"type":"error","code":"path_not_allowed","message":"path is outside the allowlist","action":"Ask the gateway operator for an allowed path."}
```

有 gateway API code 時直接沿用；`release --to` 只有部分成功時使用
`partial_release`，其餘錯誤使用 `command_failed`。Schema version 1 可新增
field，但既有 field 的意義不變。Consumer 必須忽略未知 field 與 event type，
讓 version 1 可以 additive growth。

| `type` | Command / stream | 必要 fields | 可選 fields |
|---|---|---|---|
| `warning` | `claim` stderr | `code`, `message` | — |
| `connected` | `claim` stdout | `paths`, `target`, `local_port` | `expires_at`, `background`, `log_path`, `release_command` |
| `disconnected` | foreground `claim` stdout | `state`（`retrying`） | — |
| `reconnected` | foreground `claim` stdout | `paths`, `target`, `local_port` | `expires_at` |
| `local_error` | foreground `claim` stdout | `code`, `message`, `target`, `local_port` | — |
| `request` | foreground `claim` stdout | `method`, `path`, `status`, `duration_ms` | — |
| `released` | `claim` 或 `release` stdout | `paths` | `local_port` |
| `claim_list` | `list` stdout | `claims`；每筆 item 必有 `paths`、`owner`、`started_at`、`mine`、`status` | `mine: true` 的 item 有 `target` 與 `local_port`；finite claim 有 `expires_at` |
| `release_summary` | `release --to` stdout | `released`, `failed`, `local_port`, `gateway` | — |
| `error` | 失敗 command stderr | `code`, `message` | `action`；partial release 另含 `released`、`failed`、結構化 `failures`、`local_port` 與 `gateway` |

`claim -d --output json` 會向 parent stdout 寫入一筆 `connected` record；
`log_path` 是 child 的 JSONL log，child stdout 與 stderr 會合併到同一檔案。
Foreground lifecycle 與 request event 使用 stdout；preflight warning 與
terminal error 使用 stderr。

## 連線

向平台團隊取得 callback host、允許的 path，以及可選的個人 token。先啟動本機
service，再執行：

```bash
export TUNLEASE_GATEWAY=callbacks.staging.example.com
export TUNLEASE_TOKEN=YOUR_TOKEN
tul claim '/webhooks/provider/callback/*' --to 8080
```

不加 wildcard 時只 claim 一條 exact path。`/callback` 與 `/callback/` 等同；
`/callback/*` 只符合一層子 path segment，`/callback/**` 則符合 callback path
本身與任意深度的所有子路徑。

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
claim，讓本機 service 可以稍後啟動。Ready 訊息會直接顯示完整 routing 關係，
不暴露內部 claim ID：

```text
Connected: /webhooks/provider/callback/* → localhost:8080
Requests will appear below. Press Ctrl+C to stop forwarding and release the path.
```

Gateway 若限制 claim duration，第一行會包含 `until HH:MM:SS`。Detached claim
使用相同的 route 與 deadline 表達，再顯示 log path 及 `tul release` command。

連線期間每次本機連線失敗都會印在 foreground，或寫入 detached claim log，
並省略重複的底層 dial 細節。每個 forwarded request 也會顯示一行包含 method、
path、response status 與 duration 的精簡 activity：

```text
→ POST /webhooks/provider/callback  200  42ms
```

Activity 不包含 query string、headers 或 body，避免把 secret 與 webhook
payload 複製到 terminal output。Detached claim 會把相同 activity 寫入
`~/.tunlease/claim-PORT.log`。
Gateway connection 中斷時，text mode 會先顯示一次
`Connection lost; retrying…`；空窗期間 request 走 origin。Replacement
成功後才顯示 `Reconnected`。
process 擁有兩者：Ctrl+C 或連線結束會立即釋放。暫時斷網時會自動重連；
claim ID 會改變。

```bash
tul list
tul list --all
tul release '/webhooks/provider/callback/*'
tul release --to 8080
```

`tul list` 會標示 path、forwarding target 或 owner、status 與開始時間。只有
由本機建立的 claim 才會顯示 local port。
`release --to` 會嘗試該 port 上、屬於目前 gateway 的所有本機記錄 claim；
每筆成功後立即保存 state。若後續項目失敗，command 會回報
`partial_release` summary，並只留下失敗項目供安全重試。

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
