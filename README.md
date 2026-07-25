# <img src="assets/icon.png" width="32" height="32" alt=""> Tunlease

**Debug a webhook on your laptop using its real, unchangeable callback URL — no redeploy, no new URL.**

Claim one path on an existing fixed endpoint; its live traffic reaches your
laptop while every other path keeps serving the real app. Ctrl+C to release.

```bash
tunle claim /webhooks/stripe/* --to 8080 --gateway staging.myapp.com
```

![A fixed callback URL returns the app's 404 until you claim its path, then reaches a server on your laptop — on the same URL](assets/demo.gif)

Instead of publishing another endpoint, it keeps the callback URL already in
use. [How it compares](#how-it-compares).

[Developer quick start](#quick-start-for-developers) · [Platform setup](docs/platform-deployment.md) · [Architecture](docs/architecture.md) · [Troubleshooting](docs/developer-guide.md#troubleshooting)

[English](README.md) · [繁體中文](README.zh-TW.md)

## How it works

```mermaid
flowchart LR
    TP["Third party<br/>(e.g. Stripe)"] -->|"calls the fixed URL"| GW[tunlease gateway]

    subgraph Shared["Shared environment"]
        GW[tunlease gateway]
        App[Original app]
        GW -->|"every other path<br/>(fail-open)"| App
    end

    subgraph Developer["Developer machine"]
        CLI[tunle CLI]
        Local[Your local service]
        CLI -->|"reverse tunnel"| Local
    end

    GW -->|"claimed path"| CLI

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class GW,CLI tunlease;
```

The gateway receives the fixed host's traffic and forks by path: a **claimed**
path with a connected tunnel reaches your laptop; every other path is proxied
to the configured original app. Blue nodes are Tunlease's; the rest already
exist.

The safety model combines a path allowlist, exclusive connected tunnels,
optional token authentication, audit logs, and origin fallback. Fallback
covers requests that have no matching connected session before dispatch.
Gateway, Ingress, and origin outages require separate infrastructure or bypass
planning. See the [routing and failure contract](docs/architecture.md#routing-and-failure-contract).

## How it compares

Similar in spirit to [ngrok](https://ngrok.com/),
[localtunnel](https://github.com/localtunnel/localtunnel), and
[bore](https://github.com/ekzhang/bore) — but built around a different
workflow. General-purpose tunnels publish a public endpoint for a local
service. Tunlease instead keeps a third party's **existing fixed** callback URL
and lets developers temporarily claim individual paths on it.

This table compares the tools' built-in workflow, not every setup that can be
assembled with custom domains, routing policies, or an external reverse proxy.

| | Tunlease | ngrok | localtunnel | bore |
|---|---|---|---|---|
| Primary endpoint model | Existing host + claimed HTTP path | Public or custom URL | Assigned URL | Public TCP port |
| Session-scoped, exclusive path claim | Built in | No equivalent claim workflow | No | No |
| Unclaimed paths automatically use the original app | Built in | Requires routing policy | Requires external proxy | Requires external proxy |
| Multiple developers claim separate paths on one host | Built in | Requires manual routing | No | No |
| Current supported relay is open source and self-hostable | Yes | No | Yes | Yes |

## Quick start for developers

Once your platform team has routed the existing callback host through a gateway
and given you its URL, allowed prefix, and optional token, install the CLI and
claim. If the gateway is not set up yet, see the
[platform deployment guide](docs/platform-deployment.md).

```bash
# macOS and Linux
# Replace YOUR_TUNLEASE_HOST with your team's published distribution host.
curl -fsSL https://YOUR_TUNLEASE_HOST/install/install.sh | bash
```

```powershell
# Windows PowerShell (amd64)
# Replace YOUR_TUNLEASE_HOST with your team's published distribution host.
irm https://YOUR_TUNLEASE_HOST/install/install.ps1 | iex
```

Then claim a path in one command — point it at the gateway with `--gateway`
(just the domain your platform team gave you) and forward the path to a local
port with `--to`. Ctrl+C releases it. The scheme defaults to `https`, so you can
omit it; use an explicit `http://` for a gateway without TLS (e.g. localhost).

```bash
tunle claim /webhooks/provider/callback/* --to 8080 --gateway myapp.example.com
```

Claims receive real staging callbacks, including their data and credentials.
Start your local service first, claim the narrowest path you need, and make
callback handling idempotent: provider retries and mid-request tunnel failures
can produce duplicate delivery.

To avoid repeating `--gateway`, set it once as an environment variable:

```bash
export TUNLEASE_GATEWAY=myapp.example.com
tunle claim /webhooks/provider/callback/* --to 8080
```

Or, if you prefer a file, put it in `~/.tunlease.yaml` (a `token:` line goes
here too if the gateway requires authentication):

```yaml
gateway: myapp.example.com
```

Everything else uses the same short form:

```bash
tunle list                                    # your active claims
tunle list --all                              # every claim on the gateway
tunle release /webhooks/provider/callback/*   # release a path
tunle release --to 8080                        # release everything on a port
tunle update                                  # self-update the binary
tunle --version
```

See the [developer guide](docs/developer-guide.md) for full usage and troubleshooting.

Windows amd64 uses Tunlease's purpose-built tunnel transport. The PowerShell installer verifies the published SHA-256 checksum and preserves the previous executable as `tunle.exe.prev`.

## Components and deployment model

It is all one `tunle` binary; a subcommand selects the role:

| Command | Runs on | Responsibility |
|---|---|---|
| `tunle claim` (also `list` / `release`) | Developer machine | Connect a path to a local service |
| `tunle gateway` | In front of the app | Own active paths, terminate tunnels, route requests, and proxy to the original app |

The gateway sits in front of the app and does everything on the server side. It
serves its control plane under the fixed `/_tunlease` prefix; every
other path is third-party traffic — tunnelled to the developer when a claim
matches, otherwise proxied to the required `fail_open_url` (the original app).
There is no separate sidecar process.

- **Any host, one app origin** — run `tunle gateway` with `fail_open_url` pointed at the
  app's Service and deploy it in front of the app (Ingress → gateway). This is
  the model the Helm chart deploys. Kubernetes is optional.

The gateway does not call the Kubernetes API, so Kubernetes is not required — it
is just the recommended target for the platform model.

## Embedding the tunnel client

Go applications can embed the same connected-path and reconnect engine used by the standalone CLI. Their users do not need the `tunle` binary.

```bash
go get github.com/iml885203/tunlease/pkg/tunnelclient@latest
```

See [Embedding the Go client](docs/go-client.md) for authentication, a complete lifecycle example, API behavior, errors, upgrades, and integration testing.

## Local development

Go and Docker Compose are required. Helm and kubectl are only needed to validate deployment manifests.

Enable the formatting/vet pre-commit hook once per clone:

```bash
git config core.hooksPath .githooks
```

```bash
make build   # Build the tunle binary into bin/
make test    # Run the Go test suite
make lint    # Run the pinned golangci-lint container
make preflight # Build, vet, race-test, lint, and reject formatting drift
make e2e     # gateway + origin app + local app + real CLI
```

## Documentation

Choose the shortest path for your role:

- **Developer receiving callbacks:** [Developer guide](docs/developer-guide.md) — installation, configuration, CLI usage, and troubleshooting ([繁中](docs/developer-guide.zh-TW.md))
- **Platform and service owners:** [Platform deployment guide](docs/platform-deployment.md) — whole-host routing, required origin, Helm, rollout, and security ([繁中](docs/platform-deployment.zh-TW.md))
- **Contributor understanding the system:** [Architecture](docs/architecture.md) — control/data planes, routing, lifecycle, and recovery ([繁中](docs/architecture.zh-TW.md))
- **Go application author:** [Embedding the Go client](docs/go-client.md) — module setup, lifecycle API, errors, and testing ([繁中](docs/go-client.zh-TW.md))

## Status

The gateway, CLI, and reusable Go client are functional. The supported
deployment is deliberately one gateway replica: active paths and their
WebSocket sessions live in the same process. A restart disconnects them; active
clients reconnect and register their paths again when the gateway returns.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the local
setup and the `make preflight` quality gate. Please report security issues
privately per [SECURITY.md](SECURITY.md). Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE)
