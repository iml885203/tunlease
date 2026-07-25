# Developer guide

[English](developer-guide.md) · [繁體中文](developer-guide.zh-TW.md)

## Install

```bash
# macOS or Linux with Homebrew
brew install iml885203/tap/tunlease
```

```powershell
# Windows with Scoop
scoop bucket add tunlease https://github.com/iml885203/scoop-bucket
scoop install tunlease
```

Homebrew installs shell completions for bash, zsh, and fish. The CLI can also
print a completion script directly with `tul completion SHELL`.
Common short flags are `-p` for `--to`, `-g` for `--gateway`, `-t` for
`--token`, `-k` for `--insecure`, `-d` for `--detach`, and `-a` for `--all`.
Use `-o` for `--output`.

## Console output

Interactive terminals use color to distinguish successful lifecycle events,
connection state, warnings, errors, and HTTP status classes. Command behavior,
exit status, and stdout/stderr destinations remain unchanged. Color is disabled
automatically for pipes, redirected output, and detached claim logs. Set
`NO_COLOR=1` to disable it explicitly. Top-level errors are printed once.
Gateway errors include their stable error code and one recovery action when
there is a clear next step.

For automation, client commands accept `--output json` (`-o json`). `list`
returns one JSON document; foreground `claim` and multi-path `release` use
newline-delimited JSON events. Every document includes `schema_version: 1` and
a stable `type`. Errors are written to stderr with a stable `code`; successful
commands exit 0 and errors exit 1, as in text mode. JSON output never contains
ANSI escapes. For example:

```json
{"schema_version":1,"type":"connected","paths":["/callback"],"target":"localhost:8080","local_port":8080}
{"schema_version":1,"type":"request","method":"POST","path":"/callback","status":200,"duration_ms":42}
{"schema_version":1,"type":"error","code":"path_not_allowed","message":"path is outside the allowlist","action":"Ask the gateway operator for an allowed path."}
```

Error codes are gateway API codes when available, `partial_release` when only
some `release --to` operations succeed, and `command_failed` otherwise. Fields
may be added within schema version 1; existing fields keep their meaning.
Consumers must ignore unknown fields and event types so version 1 can grow
additively.

| `type` | Command / stream | Required fields | Optional fields |
|---|---|---|---|
| `warning` | `claim` stderr | `code`, `message` | — |
| `connected` | `claim` stdout | `paths`, `target`, `local_port` | `expires_at`, `background`, `log_path`, `release_command` |
| `disconnected` | foreground `claim` stdout | `state` (`retrying`) | — |
| `reconnected` | foreground `claim` stdout | `paths`, `target`, `local_port` | `expires_at` |
| `local_error` | foreground `claim` stdout | `code`, `message`, `target`, `local_port` | — |
| `request` | foreground `claim` stdout | `method`, `path`, `status`, `duration_ms` | — |
| `expired` | foreground or detached `claim` stdout/log | `paths` | `expired_at` |
| `released` | `claim` or `release` stdout | `paths` | `local_port`, `already_absent` |
| `claim_list` | `list` stdout | `claims`; every item has `paths`, `owner`, `started_at`, `mine`, `status` | items with `mine: true` have `target` and `local_port`; finite claims have `expires_at` |
| `release_summary` | `release` stdout | `released`, `failed`, `gateway`; exactly one selector: `local_port` or `paths` | `already_absent` |
| `error` | failed command stderr | `code`, `message` | `action`; partial release also has `released`, `already_absent`, `failed`, structured `failures`, `local_port`, and `gateway` |

`claim -d --output json` writes one `connected` record to the parent stdout.
Its `log_path` contains JSONL from the child; child stdout and stderr share that
file. Foreground lifecycle and request events use stdout. Preflight warnings
and terminal errors use stderr.

## Connect

Ask the platform team for the callback host, an allowed path, and optionally a
personal token. Start the local service, then:

```bash
export TUNLEASE_GATEWAY=callbacks.staging.example.com
export TUNLEASE_TOKEN=YOUR_TOKEN
tul claim '/webhooks/provider/callback/*' --to 8080
```

Omit a wildcard to claim only one exact path. `/callback` and `/callback/` are
equivalent. `/callback/*` matches exactly one child segment, while
`/callback/**` matches the callback path and all descendants at any depth.

The gateway URL is the bare host; the client adds fixed `/_tunlease`. HTTPS is
the default. Use an explicit `http://` only for local development.

You may put `gateway`, `token`, `insecure`, and `default_scheme` in
`~/.tunlease.yaml`. Prefer installing the correct CA; `--insecure` disables WSS
server verification and is only for trusted development networks.

The file is optional; flags and environment variables work without it. When it
exists, every client command parses it strictly and reports malformed YAML,
unknown keys, invalid value types, and an invalid `default_scheme` with the
file location before connecting.

See the [provider recipes](webhook-recipes.md) for Stripe, GitHub, Slack, and
OAuth examples.

## Lifecycle

A successful `claim` means path ownership and the data tunnel are both ready.
Before claiming, the CLI probes the local port. An unavailable port emits a
warning but does not block the claim, so the local service may start afterward.
The ready message shows the complete routing relationship without exposing
internal claim IDs:

```text
Connected: /webhooks/provider/callback/* → localhost:8080
Requests will appear below. Press Ctrl+C to stop forwarding and release the path.
```

Gateways with a finite claim duration include `until HH:MM:SS` in the first
line. Detached claims use the same route and deadline wording, then show the log
path and the `tul release` command.
Reaching that advertised deadline is a successful lifecycle completion:
the CLI prints `Claim expired …; tunnel closed.` and exits 0. Likewise, a
foreground claim stopped by another `tul release` prints `Claim released;
tunnel closed.` and exits 0. The Go client still exposes `claim_expired` and
`claim_released` through `Session.Err()` as terminal reasons.

While connected, each failed local connection is printed in the foreground or
written to the detached claim log. Connection errors omit redundant dial
internals. Every forwarded request also emits a compact activity line with its
method, path, response status, and duration:

```text
→ POST /webhooks/provider/callback  200  42ms
```

Activity lines deliberately omit the query string, headers, and body so secrets
and webhook payloads are not copied into terminal output. Detached claims write
the same activity lines to `~/.tunlease/claim-PORT.log`.
If the gateway connection drops, text mode prints
`Connection lost; retrying…` once before retrying. Requests use the origin
during that gap. A successful replacement prints `Reconnected`.
The foreground process owns them: Ctrl+C or connection close releases them
immediately. On transient network loss it reconnects automatically and the
claim ID changes.

```bash
tul list
tul list --all
tul release '/webhooks/provider/callback/*'
tul release --to 8080
```

`tul list` labels the path, forwarding target or owner, status, and start time.
It shows local ports only for claims created by this machine.
`release --to` attempts every locally recorded claim on that port for the
selected gateway. Each successful release is persisted immediately; if later
releases fail, the command reports a `partial_release` summary and leaves only
the failed entries for a safe retry.
`release --to PORT` is idempotent, including stale local entries: a gateway
`claim_not_found` response removes the stale entry and exits 0. `release PATH`
also treats stale state and a list/delete race as already absent. If the recorded
tunnel process is still alive, the CLI instead returns `release_pending`
and retains its state so a reconnect cannot be mistaken for a completed release.
Path release returns that code directly; `release --to` includes it as a
failure inside `partial_release`. Retry the same release command. If the local
entry is gone and the gateway disables claim listing, the CLI cannot discover
or release an unknown path. `list` and path-based `release` return
`claim_list_unavailable`; manage locally recorded claims with
`release --to PORT` on the machine that created them.

`-d` is shorthand for `--detach`; it starts a background process. Always use
`release` in automation cleanup. Treat command exit status as the interface;
human-readable output is not a stable serialization format. Local metadata in
`~/.tunlease/state.json` includes random per-gateway client identities used by
gateways that enable dynamic identity. Keep the file private; these values let
another process list or release that identity's claims but do not grant cloud
or account access.

Claim the narrowest path, never `/`, and assume callbacks contain real staging
credentials and personal data. Make local handlers idempotent: provider retries
or a tunnel failure after dispatch can duplicate delivery.

## Troubleshooting

- **`path_claimed`** — another connected session owns an overlapping prefix.
- **`path_not_allowed`** — ask for an allowlisted prefix.
- **`claim_limit_reached`** — the gateway reached `max_claims`.
- **`owner_claim_limit_reached`** — this client identity reached
  `max_claims_per_owner`.
- **Claim expired** — the gateway's advertised `max_claim_duration` ended the
  session and released its paths normally.
- **`claim_list_unavailable`** — this gateway does not expose path lookup; use
  `release --to PORT` on the machine that created the claim.
- **`release_pending`** — a tunnel process is reconnecting, so release is
  not confirmed yet; retry the same release command.
- **Origin receives the request** — confirm the claim process is connected,
  the path matches, and the local port accepts HTTP.
- **`502 This path is claimed, but its local service is unavailable.`** — the
  client could not connect to the configured localhost port. The public
  response directs the tunnel owner to the terminal without exposing local
  details; start the service and inspect the foreground output or
  `~/.tunlease/claim-PORT.log`.
- **TLS error** — install the internal CA; use `--insecure` only as a temporary
  trusted-network diagnostic.
- **Gateway path rejected** — pass only a host or origin URL, without
  `/_tunlease`; the prefix is automatic.

Upgrade with Homebrew:

```bash
brew upgrade tunlease
```

Upgrade with Scoop:

```powershell
scoop update tunlease
```

For a direct installation, run the installer again. Set `TUNLEASE_BASE_URL`
only when installing from a release mirror.
