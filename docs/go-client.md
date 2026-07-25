# Embedding the Go client

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

`Start` returns only after the gateway owns the paths and the reverse tunnel is
routable. It accepts 1–8 paths, each at most 512 bytes. The session owns both
until `Close` or context cancellation. A
reconnect receives a new claim ID, available through `session.Claim()`.
When the gateway limits claim duration, `session.Claim().ExpiresAt` contains
the deadline and `session.Err()` returns an API error with code
`claim_expired` after the terminal expiry handshake.
If a dispatched request cannot connect to the local port, the gateway returns
`502 claimed tunnel target unavailable` and the session emits the best-effort
`EventLocalTargetError`; the claim remains connected.

`Config` supports `Gateway`, `Token`, `DefaultScheme`, `Insecure`, and a custom
`HTTPClient`. Gateway must be a host or URL without a path; the package appends
fixed `/_tunlease`. `Insecure` disables outer TLS verification and is ignored
when a custom client is supplied. Unlike the CLI, the Go package does not
persist an automatic identity; provide a stable random `Token` when connecting
to a gateway that enables dynamic client identity.

Use `List` to inspect active sessions and `Release` to terminate one by ID.
Prefer `Session.Close` for a session owned by the current process. Errors from
the gateway may be inspected as `*tunnelclient.APIError`, including
`path_claimed`, `path_not_allowed`, `claim_limit_reached`,
`owner_claim_limit_reached`, and `claim_expired`.

Integration tests should start a local HTTP server, establish a session, call
the fixed public URL, verify the local response, close the session, and verify
the same request reaches the origin.
