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
    case tunnelclient.EventTunnelDisconnected:
        log.Print("connection lost; retrying")
    case tunnelclient.EventTunnelReconnected:
        log.Printf("reconnected as %s", event.Claim.ID)
    case tunnelclient.EventLocalTargetError:
        log.Printf("local target unavailable: %v", event.Err)
    case tunnelclient.EventRequestActivity:
        log.Printf("%s %s %d %s",
            event.Method, event.Path, event.Status, event.Duration)
    }
}
return session.Err()
```

`Start` 只在 gateway 已擁有 paths 且 reverse tunnel 可路由後返回；可傳 1–8
條 path，每條最多 512 bytes。不含 `/*` 或 `/**` 的 path 只符合 exact path
（結尾 slash 有無皆可）；結尾 `/*` 只符合一層子 path segment，`/**` 則符合
該 path 本身與任意深度的所有子路徑。Session 持有 paths，直到 `Close` 或
context cancellation。重連會取得新 claim ID，
可由 `session.Claim()` 讀取。
開始 retry 時會送出一次 `EventTunnelDisconnected`；replacement ready 後才
送出 `EventTunnelReconnected`。
Gateway 限制 claim duration 時，`session.Claim().ExpiresAt` 會包含 deadline；
terminal expiry handshake 後，`session.Err()` 會回傳 code 為
`claim_expired` 的 API error。
Explicit remote release 同樣會回傳 `claim_released`。對 embedding application
而言兩者都是 terminal reason；CLI 會把兩者視為成功的 lifecycle completion。
Dispatch 後若無法連到 local port，gateway 會回
`502 This path is claimed, but its local service is unavailable.`，只引導
owner 查看 terminal，不暴露本機細節。Session 也會送出 best-effort
`EventLocalTargetError`；claim 會保持 connected。
每個完成的 request 也會送出 best-effort `EventRequestActivity`，包含 method、
path、response status 與 duration。Path 不含 query string，也不會包含 headers
或 body。

`Config` 支援 `Gateway`、`Token`、`DefaultScheme`、`Insecure` 與 custom
`HTTPClient`。Gateway 必須是無 path 的 host 或 URL；package 會加入固定
`/_tunlease`。`Insecure` 停用外層 TLS verification；設定 custom client 時忽略。
Go package 不像 CLI 會保存 automatic identity；連到啟用 dynamic client
identity 的 gateway 時，請提供穩定的隨機 `Token`。

使用 `List` 查看 active session，`Release` 依 ID 終止。Current process
擁有的 session 應優先呼叫 `Session.Close`。Gateway error 可轉成
`*tunnelclient.APIError`，例如 `path_claimed`、`path_not_allowed` 與
`claim_limit_reached`、`owner_claim_limit_reached` 與 `claim_expired`。

Integration test 應啟動 local HTTP server、建立 session、呼叫固定 public URL、
驗證 local response、關閉 session，再驗證同一 request 回到 origin。

## 重用 claim CLI 語意

使用 Cobra 建置的應用程式可以重用 Tunlease 的 claim flags 與驗證，而不必
採用 `tul` 的 foreground 或 detached process lifecycle：

```go
var flags tunnelcli.ClaimFlags
cmd := &cobra.Command{
    Use:  "claim PATH [PATH...] --to PORT",
    Args: cobra.MinimumNArgs(1),
    RunE: func(cmd *cobra.Command, paths []string) error {
        options, err := flags.Options(paths)
        if err != nil {
            return err
        }
        return applicationClaim(options)
    },
}
tunnelcli.BindClaimFlags(cmd, &flags)
```

這會綁定 `-p/--to`、`-g/--gateway`、`-t/--token` 與 `-k/--insecure`。
`ClaimFlags.Options` 會套用與 `tul` 相同的 port 和 path 驗證，且不會把
exact path 擴大成 wildcard。Process lifecycle 與輸出 flags 仍由嵌入應用程式負責。
