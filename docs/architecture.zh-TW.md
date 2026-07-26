# 架構

[English](architecture.md) · [繁體中文](architecture.zh-TW.md)

Tunlease 只有四個核心概念：

- **Gateway**：位於單一 app 前方的 whole-host proxy。
- **Unclaimed target**：由 `fail_open_url` 指定的原始 app，或由
  `unclaimed_status` 指定的固定錯誤。
- **Tunnel session**：開發者的一條 WSS 長連線。
- **Claimed paths**：該連線互斥擁有的 path prefix。

WebSocket 本身就是 claim：handshake 成功便擁有 paths，連線結束便釋放。
Operator 也可設定最長 claim duration；這只是 live session 的時間上限，不是
durable lease。

## Topology 與 URL

既有 callback host 的 `/` 必須送到 gateway。`/_tunlease` 固定保留給 health、
list/release 與 tunnel WebSocket；其餘都是 data traffic。若有設定 origin，
它必須使用不會繞回 public gateway 的獨立內部 URL。

```mermaid
flowchart LR
    ThirdParty["第三方"] --> Gateway["Gateway"]
    subgraph Shared["共用環境"]
      Gateway --> Origin["原始 app"]
    end
    subgraph Developer["開發者電腦"]
      CLI["tul CLI"] --> Local["本機服務"]
    end
    Gateway --> CLI
```

## Session lifecycle

`GET /_tunlease/tunnel` 升級成 WebSocket。認證、path 驗證、互斥 ownership、
tunnel 建立與 readiness 都在這次 handshake 完成。Gateway 與 client 會進行有
timeout 的 ready/ack；未完成的連線會釋放 paths。`Start` 只會在 data channel
可路由後返回。

網路斷線會移除 server record。Client 以舊 claim ID 作為 replacement context
重試，成功後取得新 ID；空窗期間流量送 origin。明確 release 是 terminal，
不可自動重連；gateway 會送 release control frame，只有 client ACK 後才回成功。

設定 `max_claim_duration` 時，handshake 會包含 `expires_at`。期限到達後，
gateway 送出 terminal expiry control frame、等待 client ACK、關閉 session，
並釋放 paths。

剩餘 HTTP API：

- `GET /_tunlease/api/v1/claims`
- `DELETE /_tunlease/api/v1/claims/{id}`
- `GET /_tunlease/healthz`

## Gateway 與 client 相容性

每次 tunnel handshake 都會交換 `X-Tunlease-Protocol`；其整數值是 wire
protocol 的 major version。Protocol major 相同的 gateway 與 client release
保證雙向相容；minor 與 patch release 不同不會妨礙核心的 claim、forwarding、
reconnect、list 與 release lifecycle。

較新的 gateway 遇到較舊 protocol 會回 `client_upgrade_required`；較新的
client 遇到較舊 gateway 則收到 `gateway_upgrade_required`。CLI 會將這些 code
轉成升級 `tul` 或聯絡 gateway operator 的指示。v1 過渡期間，缺少 header
視為 protocol 1，讓已發布的 v1.0 client 與 gateway 保持相容。不同 protocol
major 不預期能互通。

## 路由與失敗契約

| 狀態 | 結果 |
|---|---|
| Path 符合 connected session | 經 yamux 送 localhost |
| 沒有符合的 connected session | Proxy 到 origin，或回設定的固定錯誤 |
| Dispatch 後 tunnel/local 失敗 | 回 `502 This path is claimed, but its local service is unavailable.`；不 replay |
| Dispatched tunnel 到 `tunnel_idle_timeout` 都沒有 read/write activity | 關閉該 request 並回 `504`；不 replay |
| Gateway 或設定的 origin unavailable | 平台 outage |

Path 必須以 `/` 開頭。不含 wildcard 的 path 是 exact；結尾的 slash 會被忽略，
因此 `/callback` 與 `/callback/` 等同。結尾 `/*` 只符合一層子 path segment；
`/**` 則符合該 path 本身與任意深度的所有子路徑。其他位置不支援 wildcard。
每條最多 512 bytes，一個 session 最多 8 條，重疊的 claim 範圍互斥。Gateway
傳送 HTTP method、path/query、headers、body 與 response；app 仍須處理
provider retry 與重複 delivery。

一般 HTTP request 會以直接送到 app 的 request 作為基準驗證：fail-open 與
claimed tunnel traffic 都會保留 method、原始 escaped request URI 與 query
順序、public `Host`、end-to-end headers（包含重複值與 cookies）、body bytes、
response status、response headers（包含重複的 `Set-Cookie`）、response body
與 response trailers。Request body 會在抵達時持續以 stream 傳送，response
也會在 upstream 每次 flush 後立即傳送，不會等完整內容才一次 buffer；client
cancellation 會傳到已選定的 target。由 proxy 管理的欄位可以不同：hop-by-hop
headers 會被
移除、`X-Forwarded-For` 會記錄 proxy hop、`Content-Length` 等 framing headers
可能重新產生，內部 hop 使用的 HTTP protocol version 也不是 app contract。
HTTP request trailers 不屬於 v1 forwarding contract；provider 必須把簽章資料
放在一般 headers 或 body。

Proxied response 完成後，gateway 會透過獨立 yamux stream，將 best-effort
activity event 送給 owner client。內容只有 method、不含 query 的 path、
response status 與 duration；activity reporting 不會阻塞 response，也不會
解析 tunneled HTTP stream。

## 部署邊界

v1 只支援單一 gateway process 與 in-memory state。WebSocket session 是
process-local，因此多 replica 不安全。Gateway 重啟會產生 reconnect gap，
不保留 active claim。Transport security 由外層 HTTPS/WSS 提供，tunnel
內沒有第二層 TLS。
