# Contributing to Tunlease

Thanks for your interest in contributing. This guide covers the local setup,
the quality gate, and how to propose changes.

## Development setup

You need Go (see the version in [`go.mod`](go.mod)). Docker is used for the
containerised lint target and the end-to-end suite.

```bash
git clone https://github.com/iml885203/tunlease.git
cd tunlease
go build ./...
```

Enable the repository Git hooks so formatting and vetting run before each
commit:

```bash
git config core.hooksPath .githooks
```

## Quality gate

Before opening a pull request, run the same checks CI enforces:

```bash
make preflight
```

`preflight` runs `gofmt` (check), `go build`, `go vet`, `go test -race ./...`,
and `golangci-lint`. Individual targets are also available — see the
[`Makefile`](Makefile) (`make test`, `make test-race`, `make vet`, `make lint`,
`make fmt`).

The end-to-end suite spins up the gateway, origin app, and a sample local app in containers
and drives the real CLI:

```bash
make e2e
```

## Pull requests

- Keep changes focused; one logical change per pull request.
- Match the surrounding code style. Comments are written in English and kept
  minimal.
- Add or update tests for behaviour changes.
- Make sure `make preflight` passes.
- Describe the motivation and the observable change in the PR description.

## Reporting bugs and requesting features

Open a [GitHub issue](https://github.com/iml885203/tunlease/issues). For
suspected security vulnerabilities, follow [SECURITY.md](SECURITY.md) instead of
filing a public issue.

## Releases

Maintainers should follow the [release runbook](docs/releasing.md). It covers
GitHub Releases, gateway images, the Homebrew tap, and Homebrew Core readiness.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
