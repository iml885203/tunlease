# Releasing Tunlease

This runbook is for maintainers. Users only need the installation instructions
in the README.

## Publish a release

1. Start from a clean `main` branch with CI passing.
2. Choose a semantic version such as `v0.2.0`.
3. Confirm user-facing documentation and its paired `*.zh-TW.md` files agree.
4. Create and push an annotated tag:

   ```bash
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin v0.2.0
   ```

The tag workflow validates the version, runs tests and lint, publishes
multi-architecture gateway images to GHCR, and creates a GitHub Release with
CLI binaries and SHA-256 files.

Verify the completed release:

```bash
gh run list --limit 5
gh release view v0.2.0
docker pull ghcr.io/iml885203/tunlease-gateway:v0.2.0
```

Do not move or replace a published version tag. Publish a new patch release
instead.

## Update the Homebrew tap

The tap checks the latest GitHub Release every six hours and opens a formula
update pull request. Review its macOS and Linux test run, then merge it.

If automation is unavailable, update it manually after the GitHub Release
succeeds:

1. Open the tap checkout managed by Homebrew:

   ```bash
   brew tap iml885203/tap
   cd "$(brew --repository iml885203/tap)"
   git pull --ff-only
   ```

2. Calculate the immutable source archive's checksum without leaving it in the
   tap checkout:

   ```bash
   curl -fsSL \
     https://github.com/iml885203/tunlease/archive/refs/tags/v0.2.0.tar.gz |
     shasum -a 256
   ```

3. Update the `url` and `sha256` in `Formula/tunlease.rb`.
4. From the same tap checkout, run:

   ```bash
   brew style --formula iml885203/tap/tunlease
   brew audit --strict --new --online iml885203/tap/tunlease
   brew uninstall --force tunlease 2>/dev/null || true
   HOMEBREW_NO_INSTALL_FROM_API=1 \
     brew install --build-from-source iml885203/tap/tunlease
   brew test iml885203/tap/tunlease
   ```

5. Push the tap change and require its macOS and Linux workflow to pass.

The tap builds from source. It does not depend on the prebuilt release
binaries.

## Update the Scoop bucket

The `iml885203/scoop-bucket` repository checks the latest release every six
hours and opens a manifest update pull request. Its Windows workflow installs
the manifest with Scoop and runs the CLI. Review that run before merging.

The manifest uses the Windows binary and SHA-256 file published by the release
workflow. If automation is unavailable, update its `version`, download URL, and
hash together.

## Homebrew Core readiness

The formula is technically prepared for Homebrew Core:

- public MIT-licensed source;
- stable semantic-version tags and immutable source archives;
- source build with a build-only Go dependency;
- a functional test that starts the gateway and checks its health endpoint;
- no self-update command;
- macOS and Linux formula CI in the tap.

Before proposing it to `Homebrew/homebrew-core`, recheck the current
[formula requirements](https://docs.brew.sh/Acceptable-Formulae),
[package acceptance policy](https://docs.brew.sh/Package-Acceptance-Policy),
and [contribution process](https://docs.brew.sh/How-To-Open-a-Homebrew-Pull-Request).
Homebrew normally rejects projects younger than 30 days and owner-submitted
projects without sufficient notability. These are adoption gates, not missing
Tunlease features. Keep the tap as the supported installation path until the
project qualifies.
