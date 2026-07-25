# 嵌入 Go client

[English](go-client.md) · [繁體中文](go-client.zh-TW.md)

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
```

```go
client, err := tunnelclient.New(tunnelclient.Config{
    Gateway: "callbacks.staging.example.com",
    Token: token,
})
if err != nil {
    return err
}

session, err := client.Start(ctx, []string{
    "/webhooks/provider/callback/*",
}, 8080)
if err != nil {
    return err
}
defer session.Close()

for event := range session.Events() {
    switch event.Type {
    case tunnelclient.EventTunnelReconnected:
        log.Printf("reconnected as %s", event.Claim.ID)
    case tunnelclient.EventLocalTargetError:
        log.Printf("local target unavailable: %v", event.Err)
    }
}
return session.Err()
```

`Start` 只在 gateway 已擁有 paths 且 reverse tunnel 可路由後返回；可傳 1–8
條 path，每條最多 512 bytes。Session 持有兩者，直到 `Close` 或 context
cancellation。重連會取得新 claim ID，
可由 `session.Claim()` 讀取。
Dispatch 後若無法連到 local port，gateway 會回
`502 claimed tunnel target unavailable`，session 也會送出 best-effort
`EventLocalTargetError`；claim 會保持 connected。

`Config` 支援 `Gateway`、`Token`、`DefaultScheme`、`Insecure` 與 custom
`HTTPClient`。Gateway 必須是無 path 的 host 或 URL；package 會加入固定
`/_tunlease`。`Insecure` 停用外層 TLS verification；設定 custom client 時忽略。

使用 `List` 查看 active session，`Release` 依 ID 終止。Current process
擁有的 session 應優先呼叫 `Session.Close`。Gateway error 可轉成
`*tunnelclient.APIError`，例如 `path_claimed`、`path_not_allowed` 與
`claim_limit_reached`。

Integration test 應啟動 local HTTP server、建立 session、呼叫固定 public URL、
驗證 local response、關閉 session，再驗證同一 request 回到 origin。
