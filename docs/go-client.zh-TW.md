# 嵌入 Tunlease Go client

[English](go-client.md) · [繁體中文](go-client.zh-TW.md)

`tunnelclient` package 讓 Go 應用程式直接管理 Tunlease session。它與獨立 CLI 共用 claim、lease heartbeat、重新連線、TLS pinning WebSocket 與反向 tunnel engine。最後仍是一個 application binary，使用者不需要另外安裝 `tunlease` CLI。

## 加入 dependency

用 `go get` 加入 package：

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
```

如果 repository 是 private，請先確認本機 Git credentials 有讀取權限，並讓 Go 直接取得 module（而非透過 public proxy）：

```bash
go env -w GOPRIVATE=github.com/iml885203/tunlease
```

把 `go.mod` 與 `go.sum` 的異動一起 commit。為了讓 build 可以重現，請保留 `go get` 選定的版本，不要依賴本機 `replace` directive。

## 啟動並管理 session

```go
package callbacks

import (
    "context"
    "errors"
    "fmt"

    "github.com/iml885203/tunlease/pkg/tunnelclient"
)

func ServeTunnel(ctx context.Context, gateway string, token string, localPort int) error {
    client, err := tunnelclient.New(tunnelclient.Config{
        Gateway: gateway,
        Token:   token,
    })
    if err != nil {
        return fmt.Errorf("configure Tunlease: %w", err)
    }

    session, err := client.Start(ctx, []string{
        "/webhooks/provider/callback/*",
    }, localPort)
    if err != nil {
        return fmt.Errorf("start Tunlease tunnel: %w", err)
    }
    defer func() { _ = session.Close() }()

    for {
        select {
        case <-ctx.Done():
            return session.Close()
        case <-session.Done():
            return session.Err()
        case event, ok := <-session.Events():
            if !ok {
                return session.Err()
            }
            switch event.Type {
            case tunnelclient.EventHeartbeatWarning:
                // Session 仍可使用，並會自動重試。
            case tunnelclient.EventLeaseReclaimed:
                // Claim ID 已改變；用 session.Claim() 讀取最新狀態。
            case tunnelclient.EventTunnelReconnected:
                // Gateway tunnel identity 改變，且已完成重新連線。
            }
        }
    }
}

func IsConflict(err error) bool {
    var apiErr *tunnelclient.APIError
    return errors.As(err, &apiErr) && apiErr.Code == "path_claimed"
}
```

`Start` 會正規化 path，並只在 gateway 接受 claim 且初始反向 tunnel 已連線後回傳。設定失敗時只回傳 error，不會留下 session。啟動後，session 會自行維持 heartbeat，並處理暫時性的 tunnel 斷線。

Caller 負責 context 與 session lifecycle：

- Host application 關閉時取消 context。
- 呼叫 `Close`，等待 tunnel 關閉並盡力釋放 claim。
- 監看 `Done`，結束後用 `Err` 取得 terminal failure。
- 需要目前 claim ID、path、owner、port 或 expiry 時呼叫 `Claim`。
- `Events` 提供 non-terminal lifecycle notification；application correctness 應以 `Claim`、`Done` 與 `Err` 為準。

## Credentials 與設定

Package 不會讀取 `~/.tunlease.yaml`、environment variables 或 CLI state。嵌入它的應用程式必須把 gateway URL 傳給 `tunnelclient.New`。Gateway 未設定 client token 時，`Token` 可以留空。

若 gateway 啟用認證，應用程式必須從自己的安全設定來源取得個人 token。不要把 token 寫進 log，也不要透過 local status API 暴露。應用程式需要自訂 HTTP transport 時，可以傳入 `Config.HTTPClient`。

## 列出與釋放 claim

```go
claims, err := client.List(ctx)
if err != nil {
    return err
}

if err := client.Release(ctx, claims[0].ID); err != nil {
    return err
}
```

目前 process 擁有的 session 應優先使用 `Session.Close`；只有 administrative flow 手上僅有 claim ID 時才直接呼叫 `Release`。

## 升級

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
go mod tidy
go test ./...
```

跨 major version 升級前先閱讀 release notes。開始發布 versioned module 後，public Go API 會遵守 semantic versioning。

## Integration test

有效的 integration test 應驗證真實 data path，而不只呼叫 claim API：

1. 在可用的 localhost port 啟動 HTTP server。
2. 啟動 session，把專用測試 path claim 到該 port。
3. 使用該 path 呼叫固定 public endpoint。
4. 確認本機 server 收到 request，且 response 成功回到 caller。
5. 關閉 session，再確認相同 public request 會 fallback 到原始 application。

請使用 automation 專用 path，避免測試攔截其他開發者的 callback。
