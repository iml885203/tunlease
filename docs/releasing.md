# Releasing Tunlease

This runbook is for maintainers. Users only need the installation instructions
in the README.

## Publish a release

1. Confirm `main` contains the intended changes and paired English/Traditional
   Chinese documentation.
2. In GitHub Actions, run the **Release** workflow from `main`.
3. Select `patch`, `minor`, or `major`.

The workflow calculates the next stable semantic version and runs formatting,
vet, build, race tests, pinned lint, and Helm validation before it creates an
annotated tag. It then publishes multi-architecture gateway images to GHCR and
creates a GitHub Release with CLI binaries and SHA-256 files. After publishing,
it dispatches the Homebrew tap and Scoop bucket updaters in parallel, waits for
their tests and merges, and verifies that both package definitions match the
new version. As the final release step, it dispatches the matching immutable
gateway image to the public relay infrastructure and waits for that deployment
and its health check to succeed. A failed preflight never creates a version
tag.

Configure the `HOMEBREW_TAP_TOKEN` repository secret before releasing. Use a
fine-grained personal access token restricted to `iml885203/homebrew-tap` with
repository permissions `Actions: Read and write` and `Contents: Read`. The tap
updater uses its own repository-scoped `GITHUB_TOKEN` to update and merge the
formula; the cross-repository token only dispatches and observes that workflow.
The **Sync Homebrew** workflow can be run manually with an existing release
version to verify the token or recover without publishing another release.

Configure the `SCOOP_BUCKET_TOKEN` repository secret the same way, restricted
to `iml885203/scoop-bucket` with repository permissions
`Actions: Read and write` and `Contents: Read`. The **Sync Scoop** workflow can
also be run manually with an existing release version for verification or
recovery.

Configure the `PUBLIC_RELAY_INFRA_TOKEN` repository secret with a fine-grained
personal access token restricted to
`iml885203/tunlease-public-relay-infra`. It needs repository permissions
`Actions: Read` and `Contents: Read and write`: Contents write sends the
authenticated `repository_dispatch`, and Actions read lets the release
workflow wait for and verify the resulting deployment. The infrastructure
workflow rejects dispatches whose source repository or stable semantic version
payload is unexpected.

Pushing a valid annotated tag manually remains a recovery path; tag CI uses
the same reusable artifact workflow.

Verify the completed release:

```bash
VERSION=vX.Y.Z # use the version just published
gh run list --limit 5
gh release view "$VERSION"
docker pull "ghcr.io/iml885203/tunlease-gateway:$VERSION"
```

Do not move or replace a published version tag. Publish a new patch release
instead.

## Update the Homebrew tap

The release workflow immediately dispatches the tap updater after artifacts
are published. The tap also checks the latest GitHub Release every six hours as
a fallback. The updater opens a formula update pull request, waits for its
macOS and Linux tests, and merges the PR only after both pass.

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
   VERSION=vX.Y.Z # use the version just published
   curl -fsSL \
     "https://github.com/iml885203/tunlease/archive/refs/tags/$VERSION.tar.gz" |
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

The release workflow immediately dispatches the
`iml885203/scoop-bucket` updater after artifacts are published. The bucket also
checks the latest release every six hours as a fallback. Its Windows workflow
installs the manifest with Scoop and runs the CLI; the updater waits and merges
the PR only after that test succeeds. The release workflow then verifies that
the merged manifest matches the new version.

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
