# Developer guide

[English](developer-guide.md) · [繁體中文](developer-guide.zh-TW.md)

Use tunlease to temporarily route a fixed staging callback path to your machine. This guide assumes the platform team has already deployed `tunlease-gateway` in front of the target endpoint and configured an allowed path. You do not need Kubernetes access.

For the relationship between callback host, gateway URL, control prefix,
claim, lease, and tunnel, read the one-page
[concepts and URL map](concepts.md).

Before starting, ask a tunlease maintainer for:

- The gateway URL.
- A personal token only when the gateway has client authentication enabled.
- The path prefix you are allowed to claim.

The CLI does not create or discover personal tokens. With the default unauthenticated gateway configuration, no token is needed.

## Install and update

```bash
# macOS and Linux
# Replace YOUR_TUNLEASE_HOST with your team's published distribution host.
curl -fsSL https://YOUR_TUNLEASE_HOST/install/install.sh | bash
tunle --version
```

```powershell
# Windows PowerShell (amd64)
# Replace YOUR_TUNLEASE_HOST with your team's published distribution host.
irm https://YOUR_TUNLEASE_HOST/install/install.ps1 | iex
tunle --version
```

The repository does not publish a shared installer at the placeholder hostname;
your platform team must publish the artifacts and installer first. The installer
detects macOS/Linux and amd64/arm64 and installs to `~/.local/bin/tunle`. It
verifies SHA-256 and preserves the previous binary as `.prev`.

The Windows installer downloads the native amd64 executable to `%LOCALAPPDATA%\tunlease`, verifies SHA-256, preserves the previous binary as `tunle.exe.prev`, and adds the directory to the user `PATH`. The purpose-built tunnel transport is Tunlease's own.

```bash
tunle update
```

Updates verify the checksum and preserve the previous version as `tunle.prev`.
On Windows, rerun the PowerShell installer to update while preserving `tunle.exe.prev`.

## Configure

Create `~/.tunlease.yaml` and restrict its permissions on macOS/Linux. Use just
the gateway domain your platform team gave you — the client adds the control-plane
prefix automatically and defaults the scheme to `https`, so omit it (use an
explicit `http://` for a gateway without TLS, e.g. localhost):

```yaml
gateway: myapp.example.com
token: YOUR_PERSONAL_TOKEN
```

```bash
chmod 600 ~/.tunlease.yaml
```

On Windows PowerShell:

```powershell
@"
gateway: myapp.example.com
token: YOUR_PERSONAL_TOKEN
"@ | Set-Content (Join-Path $HOME ".tunlease.yaml")
```

Configuration precedence is flag, environment variable, then file:

| Setting | Flag | Environment | YAML |
|---|---|---|---|
| Gateway URL | `--gateway` | `TUNLEASE_GATEWAY` | `gateway` |
| Optional personal token | `--token` | `TUNLEASE_TOKEN` | `token` |
| Skip gateway TLS verification | `--insecure` | `TUNLEASE_INSECURE` | `insecure` |
| Default scheme when none given | — | `TUNLEASE_DEFAULT_SCHEME` | `default_scheme` |

When authentication is enabled, never commit a token or place it in shared documentation.

The gateway URL scheme is optional and defaults to `https`; write `http://`
explicitly for a gateway without TLS. `--insecure` skips verification of the
gateway's TLS certificate. Although the inner tunnel remains
fingerprint-pinned, that fingerprint is learned over the outer connection; a
full man-in-the-middle can replace it. Use `--insecure` only on a trusted
network and prefer installing the internal CA.

## Open a tunnel

Start the local service, then claim the narrowest useful path:

```bash
# A service is already listening on localhost:8080.
tunle claim --to 8080 /webhooks/provider/callback/*
```

- Paths must begin with `/`. The CLI normalizes them to prefix patterns ending in `/*`.
- A wildcard is only valid at the end; `/a/*/b` is invalid.
- Overlapping prefixes cannot be claimed simultaneously. A conflict reports the current owner and expiry time.
- `claim` stays in the foreground to maintain the tunnel and heartbeat. Ctrl+C releases the lease. If the process dies, the server removes it after its TTL.
- `--detach` runs the claim in the background and returns immediately (printing the claim id and a log path). Useful for scripts and agents that can't hold a blocking process. Stop it with `tunle release` (by path or `--to`).
- During a claim, all real staging callbacks under that path reach the local service. Unclaimed paths are unaffected.

See the [canonical path model](concepts.md#path-model) for segment boundaries,
query handling, overlap, case sensitivity, and proxy normalization.

Real payloads, credentials, and personal data under that staging path reach
your machine and may enter local logs. Use only authorized paths and follow
your team's data-handling policy. Keep the callback handler idempotent:
providers retry, and a failure after tunnel dispatch cannot be safely replayed
to the original app without risking duplicate side effects.

Multiple paths can share one local port:

```bash
tunle claim --to 8080 \
  /webhooks/provider/debit/* \
  /webhooks/provider/credit/*
```

## Inspect and release

```bash
tunle list
tunle list --all
tunle release /webhooks/provider/callback/*
tunle release --to 8080
```

Local claim metadata is stored in `~/.tunlease/state.json`; it does not contain the token. If that file is lost, `release PATH` can still query the server and release a lease owned by the current identity.

## Automation

Use `--detach` when an agent or script cannot keep a foreground process alive:

```bash
set -e
tunle claim --detach --to 8080 /webhooks/provider/callback/*
tunle list

# Run the callback-producing test here.

tunle release /webhooks/provider/callback/*
```

Always put release in the workflow's cleanup/finally step. Treat command exit
status—not human-readable text—as the success signal. After claiming, verify
the path appears in `tunle list` and send a synthetic callback before starting
destructive or stateful tests. Detached output reports the claim ID and log
path; preserve that log when diagnosing reconnects.

## Troubleshooting

`gateway is required`
: Check `~/.tunlease.yaml`, `TUNLEASE_GATEWAY`, or the `--gateway` flag.

`valid bearer token required`
: The gateway has authentication enabled. Configure `token`, `TUNLEASE_TOKEN`, or `--token` with a token issued by its maintainer.

`path is not allowed`
: The path is outside the platform allowlist. Use an allowed provider prefix or ask a maintainer to update the allowlist.

`path already claimed`
: The path overlaps another active lease. Run `tunle list --all` to see its owner and expiry; do not take over another developer's test.

`claim_limit_reached` (HTTP 503)
: The gateway is already holding its maximum number of active claims (`max_claims`, default 64). Wait for one to be released or expire, or ask the platform team to raise the limit.

Callbacks still reach the staging app
: Confirm that the `claim` process is running, the local port is correct, the local service responds, and `tunle list` shows the lease. The gateway intentionally fails open to the original app when the tunnel cannot be reached.

Cannot connect to the gateway over TLS
: The scheme defaults to `https` and the client does not silently retry over `http`. If the gateway has no TLS (e.g. localhost), use an explicit `http://` gateway URL.

`x509: certificate signed by unknown authority`
: Install the internal CA whenever possible. On a trusted network only, `--insecure` (or `TUNLEASE_INSECURE=1`) bypasses outer server authentication; inner pinning does not prevent a full MITM because its fingerprint arrives over that connection.

Use another release URL
: Set `TUNLEASE_BASE_URL`, or pass `--base-url` to `tunle update`.
