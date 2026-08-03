# Releasing Tunlease

[English](releasing.md) · [繁體中文](releasing.zh-TW.md)

This runbook is for maintainers. Users only need the installation instructions
in the README.

## Release

1. Confirm `main` contains the intended changes and paired
   English/Traditional Chinese documentation.
2. In GitHub Actions, run **Release** from `main`.
3. Select `patch`, `minor`, or `major`.

After its checks pass, the workflow creates the version and handles the GitHub
Release, gateway images, Helm defaults, Homebrew, Scoop, and public relay
deployment.

Gateway image publishing and CLI release publishing run independently after
the version tag is created. Public relay deployment starts as soon as the
versioned gateway image is available in GHCR; it does not wait for CLI assets,
Homebrew, Scoop, or Helm defaults. Package-manager updates wait for the GitHub
Release because they consume its release metadata or binaries.

## Required release credentials

Package updates use the private `iml885203-package-sync` GitHub App. Install
the App only on `iml885203/homebrew-tap` and `iml885203/scoop-bucket` with
`Actions: Read and write` and `Contents: Read`.

Configure these repository settings:

| Setting | Kind | Purpose |
|---|---|---|
| `PACKAGE_SYNC_APP_CLIENT_ID` | Variable | GitHub App client ID |
| `PACKAGE_SYNC_APP_PRIVATE_KEY` | Secret | GitHub App private key |
| `PUBLIC_RELAY_INFRA_TOKEN` | Secret | Fine-grained token for `iml885203/tunlease-public-relay-infra` with `Actions: Read and write` and `Contents: Read` |

## Verify or recover

A release is complete when the **Release** workflow is green. To inspect its
artifacts:

```bash
VERSION=vX.Y.Z
gh release view "$VERSION"
docker pull "ghcr.io/iml885203/tunlease-gateway:$VERSION"
```

To retry one failed downstream step without creating another version, run
**Sync Homebrew**, **Sync Scoop**, or **Deploy public relay** with the existing
`vX.Y.Z`.

Never move or replace a published version tag. Publish a patch release instead.
