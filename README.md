# <img src="assets/icon.png" width="32" height="32" alt=""> Tunlease

**Debug a webhook on your laptop using its real, unchangeable callback URL — no redeploy, no new URL.**

Claim one path on an existing fixed endpoint; its live traffic reaches your
laptop while every other path keeps serving the real app. Ctrl+C to release.

```bash
tunle claim /webhooks/stripe/* --to 8080 --gateway staging.myapp.com
```

![A fixed callback URL returns the app's 404 until you claim its path, then reaches a server on your laptop — on the same URL](assets/demo.gif)

Unlike ngrok/localtunnel/bore, it does **not** give you a new URL. [How it compares](#how-it-compares).

[Concepts and URL map](docs/concepts.md) · [Task index](docs/tasks.md) · [Developer quick start](#quick-start-for-developers) · [Platform setup](docs/platform-deployment.md) · [Troubleshooting](docs/developer-guide.md#troubleshooting)

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

The safety model combines a path allowlist, exclusive leases with TTLs,
optional token authentication, audit logs, and fail-open routing. Fail-open
covers unmatched paths and unavailable developer tunnels while the gateway is
healthy; gateway availability still requires normal platform HA or bypass
planning. See the [routing and failure contract](docs/concepts.md#routing-and-failure-contract).

## How it compares

Similar in spirit to [ngrok](https://ngrok.com/),
[localtunnel](https://github.com/localtunnel/localtunnel), and
[bore](https://github.com/ekzhang/bore) — but built for a different job. Those
tools give you a **new** URL for a local port. Tunlease keeps a third party's
**existing fixed** URL and reroutes just one path on it to your machine.

| | Tunlease | ngrok | localtunnel | bore |
|---|:---:|:---:|:---:|:---:|
| Expose a local port | ✅ | ✅ | ✅ | ✅ |
| Keep a third party's existing fixed URL | ✅ | ❌ | ❌ | ❌ |
| Claim one path, leave the rest untouched | ✅ | ❌ | ❌ | ❌ |
| Fail open to the real app | ✅ | ❌ | ❌ | ❌ |
| Exclusive lease, many developers, one URL | ✅ | ❌ | ❌ | ❌ |
| Self-hostable | ✅ | ❌ | ✅ | ✅ |

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
| `tunle claim` (also `list` / `release`) | Developer machine | Claim a path, hold the lease, reverse tunnel, heartbeat |
| `tunle gateway` | In front of the app | API, lease registry, reverse-tunnel server, path demux, and fail-open to the app |
| `tunle serve` | Single host in front of one app | Convenience wrapper for `gateway` with `fail_open_url` set to `--app` |

The gateway sits in front of the app and does everything on the server side. It
serves its control plane under `control_prefix` (default `/_tunlease`); every
other path is third-party traffic — tunnelled to the developer when a claim
matches, otherwise proxied to `fail_open_url` (the original app), otherwise 404.
There is no separate sidecar process.

- **Single host, one app** — run `tunle serve --app http://localhost:3000`. One
  process fronts the app: claimed paths tunnel to a developer, everything else
  fails open to the app. No Kubernetes.
- **Shared platform** — run `tunle gateway` with `fail_open_url` pointed at the
  app's Service and deploy it in front of the app (Ingress → gateway). This is
  the model the Helm chart deploys.

The gateway does not call the Kubernetes API, so Kubernetes is not required — it
is just the recommended target for the platform model.

## Embedding the tunnel client

Go applications can embed the same claim, lease, reconnect, and tunnel engine used by the standalone CLI. Their users do not need the `tunle` binary.

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
make e2e     # Redis + gateway + app + real CLI
```

## Documentation

Choose the shortest path for your role:

- **Anyone learning the model:** [Concepts and URL map](docs/concepts.md) — canonical topology, vocabulary, routing/failure matrix, HTTP trust boundary, and deployment limits ([繁中](docs/concepts.zh-TW.md))
- **Team new to Tunlease:** [Adoption guide](docs/adoption-guide.md) — go from fit check to images, deployment, developer claims, and end-to-end verification ([繁中](docs/adoption-guide.zh-TW.md))
- **Developer receiving callbacks:** [Developer guide](docs/developer-guide.md) — installation, configuration, CLI usage, and troubleshooting ([繁中](docs/developer-guide.zh-TW.md))
- **Platform team self-hosting the gateway:** [Platform deployment guide → Install the gateway](docs/platform-deployment.md#install-the-gateway) — prerequisites (images, Ingress, DNS/TLS), Helm install, and security ([繁中](docs/platform-deployment.zh-TW.md#安裝-gateway))
- **Service owner fronting an app:** [Platform deployment guide → Front the app with the gateway](docs/platform-deployment.md#front-the-app-with-the-gateway) — put the gateway in front of the app and point `fail_open_url` at the app's Service ([繁中](docs/platform-deployment.zh-TW.md#用-gateway-前置-app))
- **Contributor understanding the system:** [Architecture](docs/architecture.md) — control/data planes, routing, lifecycle, and recovery ([繁中](docs/architecture.zh-TW.md))
- **Go application author:** [Embedding the Go client](docs/go-client.md) — module setup, lifecycle API, errors, and testing ([繁中](docs/go-client.zh-TW.md))

## Status

The v0.1 product baseline is complete. The gateway, CLI, and reusable Go client are all functional: the public-endpoint-to-localhost tunnel, fail-open behavior, the in-memory and optional Redis registry, installation and self-update, and cross-platform (Linux/macOS/Windows) binaries are all in place.

A single gateway replica with the in-memory registry is the supported data-plane
deployment. A gateway restart drops leases; after the gateway becomes reachable,
the CLI claims again and rebuilds the tunnel. Redis can preserve lease records,
but tunnel sessions are process-local, so Redis alone does not make multiple
gateway replicas safe.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the local
setup and the `make preflight` quality gate. Please report security issues
privately per [SECURITY.md](SECURITY.md). Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE)
