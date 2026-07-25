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
print a completion script directly with `tunle completion SHELL`.

## Connect

Ask the platform team for the callback host, an allowed path, and optionally a
personal token. Start the local service, then:

```bash
export TUNLEASE_GATEWAY=callbacks.staging.example.com
export TUNLEASE_TOKEN=YOUR_TOKEN
tunle claim /webhooks/provider/callback/* --to 8080
```

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
The foreground process owns them: Ctrl+C or connection close releases them
immediately. On transient network loss it reconnects automatically; the claim
ID changes and requests use the origin during the gap.

```bash
tunle list
tunle list --all
tunle release /webhooks/provider/callback/*
tunle release --to 8080
```

`--detach` starts a background process. Always use `release` in automation
cleanup. Treat command exit status as the interface; human-readable output is
not a stable serialization format. Local metadata in
`~/.tunlease/state.json` contains no token.

Claim the narrowest path, never `/`, and assume callbacks contain real staging
credentials and personal data. Make local handlers idempotent: provider retries
or a tunnel failure after dispatch can duplicate delivery.

## Troubleshooting

- **`path_claimed`** — another connected session owns an overlapping prefix.
- **`path_not_allowed`** — ask for an allowlisted prefix.
- **`claim_limit_reached`** — the gateway reached `max_claims`.
- **Origin receives the request** — confirm the claim process is connected,
  the path matches, and the local port accepts HTTP.
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
