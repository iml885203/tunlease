# Embedding the Tunlease Go client

[English](go-client.md) · [繁體中文](go-client.zh-TW.md)

The `tunnelclient` package lets a Go application own Tunlease sessions directly. It contains the same claim, lease heartbeat, reconnect, TLS-pinned WebSocket, and reverse-tunnel engine as the standalone CLI. The final application is one binary; its users do not install the `tunle` CLI.

## Add the dependency

Add the package with `go get`:

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
```

If the repository is private, make sure your Git credentials can read it and tell Go to fetch it directly rather than through the public proxy:

```bash
go env -w GOPRIVATE=github.com/iml885203/tunlease
```

Commit the resulting `go.mod` and `go.sum` changes. For reproducible builds, keep the version selected by `go get` rather than using a local `replace` directive.

## Start and own a session

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
                // The session remains active and retries automatically.
            case tunnelclient.EventLeaseReclaimed:
                // The claim ID changed; read session.Claim() for current state.
            case tunnelclient.EventTunnelReconnected:
                // The gateway tunnel identity changed and was reconnected.
            }
        }
    }
}

func IsConflict(err error) bool {
    var apiErr *tunnelclient.APIError
    return errors.As(err, &apiErr) && apiErr.Code == "path_claimed"
}
```

`Start` normalizes paths and returns only after the gateway accepts the claim and the initial reverse tunnel is connected. It returns an error without a session if setup fails. Once started, the session maintains heartbeats and reconnects transient tunnel failures internally.

The caller owns the context and session:

- Cancel the context when the host application is shutting down.
- Call `Close` to wait for tunnel shutdown and best-effort claim release.
- Watch `Done` and then read `Err` for terminal failures.
- Read `Claim` whenever the current claim ID, paths, owner, port, or expiry is needed.
- Consume `Events` for non-terminal lifecycle notifications; application correctness should use `Claim`, `Done`, and `Err` as authoritative state.

## Credentials and configuration

The package does not read `~/.tunlease.yaml`, environment variables, or CLI state. The embedding application must pass the gateway URL to `tunnelclient.New`. `Token` may be empty when the gateway has no client tokens configured.

For an authenticated gateway, obtain the personal token through the application's secure configuration path. Do not log it or expose it through a local status API.

`tunnelclient.Config` fields:

| Field | Meaning |
|---|---|
| `Gateway` | Gateway host or URL. Scheme is optional (see `DefaultScheme`); if it has no path, the control-plane prefix `/_tunlease` is appended automatically. |
| `Token` | Bearer token; empty when the gateway has no tokens configured. |
| `Insecure` | Skip verification of the gateway's outer TLS certificate (self-signed / internal gateway). The tunnel's inner TLS stays fingerprint-pinned. Ignored when `HTTPClient` is set. |
| `DefaultScheme` | Scheme used when `Gateway` has none. Defaults to `https`; set `http` for a gateway without TLS. |
| `HTTPClient` | Custom HTTP transport. When supplied, `Insecure` is ignored — configure TLS on the client yourself. |

## Listing and releasing claims

```go
claims, err := client.List(ctx)
if err != nil {
    return err
}

if err := client.Release(ctx, claims[0].ID); err != nil {
    return err
}
```

Prefer `Session.Close` for a session owned by the current process. Use `Release` for administrative flows where only a claim ID is available.

## Upgrade

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
go mod tidy
go test ./...
```

Review release notes before upgrading across a major version. The public Go API follows semantic versioning once versioned module releases are published.

## Integration test

A useful integration test exercises the real data path rather than only the claim API:

1. Start an HTTP server on an available localhost port.
2. Start a session claiming a dedicated test path to that port.
3. Call the fixed public endpoint with that path.
4. Verify the local server receives the request and its response reaches the caller.
5. Close the session and verify the same public request falls back to the original application.

Use a path reserved for automation so the test cannot intercept another developer's callback.
