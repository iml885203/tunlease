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

`Start` returns only after the gateway owns the paths and the reverse tunnel is
routable. It accepts 1–8 paths, each at most 512 bytes. A path without `/*`
or `/**` matches only that exact path (with or without its trailing slash).
A trailing `/*` matches exactly one child segment; `/**` matches the path
itself and all descendants at any depth. The session owns the paths until
`Close` or context cancellation. A
reconnect receives a new claim ID, available through `session.Claim()`.
`EventTunnelDisconnected` is emitted once when retrying begins;
`EventTunnelReconnected` follows only after the replacement is ready.
When the gateway limits claim duration, `session.Claim().ExpiresAt` contains
the deadline and `session.Err()` returns an API error with code
`claim_expired` after the terminal expiry handshake.
An explicit remote release similarly returns `claim_released`. These values are
terminal reasons for embedding applications; the CLI treats both as successful
lifecycle completion.
If a dispatched request cannot connect to the local port, the gateway returns
`502 This path is claimed, but its local service is unavailable.` and directs
the owner to the terminal without exposing local details. The session emits
the best-effort `EventLocalTargetError`; the claim remains connected.
Each completed request also emits a best-effort `EventRequestActivity` with its
method, path, response status, and duration. The path excludes the query string;
headers and bodies are never included.

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

## Reusing claim CLI semantics

Applications built with Cobra can reuse Tunlease's claim flags and validation
without adopting `tul`'s foreground or detached process lifecycle:

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

This binds the full `tul claim` flag set: `-p/--to`, `-g/--gateway`,
`-t/--token`, `-k/--insecure`, `-d/--detach`, and `-o/--output`.
`ClaimFlags.Options` applies the same port, path, and output validation as
`tul`, reads the corresponding `TUNLEASE_*` environment variables when flags
are absent, and does not widen exact paths into wildcards.

`ClaimUse`, `ClaimShort`, and `ClaimExample` provide the canonical command help.
Embedding commands whose release command is not `tul release` should use
`BindClaimFlagsWithReleaseCommand`.
`BindListFlags` and `BindReleaseFlags` provide the corresponding `tul list` and
`tul release` contracts. The embedding application remains responsible for
implementing foreground and detached lifecycle behavior and rendering its
selected output format.
