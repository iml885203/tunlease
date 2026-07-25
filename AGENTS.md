# Working on Tunlease

This file is the repository entry point for coding agents and contributors.

## Authoritative documents

- `docs/developer-guide.md` — CLI workflow and automation.
- `docs/platform-deployment.md` — operator deployment and runbook.
- `docs/architecture.md` — implementation architecture and lifecycle.
- `docs/go-client.md` — public Go embedding API.
- `SECURITY.md` — security reporting and threat boundary.

English is the editing source of truth. Every user-facing semantic change must
update the paired `*.zh-TW.md` file in the same change. Do not translate command
names, configuration keys, paths, or code.

## Invariants

- The provider's existing callback URL does not change.
- The gateway receives `/` on that callback host. `/_tunlease` is reserved for
  its control plane; all other paths are data-plane traffic.
- `fail_open_url` must identify the original app without routing back through
  the gateway.
- A live WebSocket session owns the narrowest allowed paths; disconnect releases them.
- Fail-open covers no usable tunnel before dispatch while the gateway is
  reachable. It does not cover gateway/Ingress/origin outage or promise replay
  after dispatch.
- The current data plane uses one gateway replica with process-local state.
- Real staging data reaches developer machines.

## Configuration names

| Concept | Gateway YAML | Helm value | Client YAML |
|---|---|---|---|
| Public gateway | — | `ingress.host` | `gateway` |
| Control namespace | fixed `/_tunlease` | fixed `/_tunlease` | appended automatically |
| Original app | `fail_open_url` | `config.failOpenURL` | — |
| TLS verification bypass | — | — | `insecure` |

## Verification

Run the narrowest relevant checks, then before handoff run:

```bash
go test ./...
git diff --check
```

For chart changes, also render and lint the chart with Helm when available.
Check documentation links and ensure examples agree with
`charts/tunlease/values.yaml` and the rendered Ingress.
