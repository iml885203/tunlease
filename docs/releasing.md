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

## Required secrets

Configure these fine-grained personal access tokens as repository secrets:

| Secret | Restricted repository | Repository permissions |
|---|---|---|
| `HOMEBREW_TAP_TOKEN` | `iml885203/homebrew-tap` | `Actions: Read and write`, `Contents: Read` |
| `SCOOP_BUCKET_TOKEN` | `iml885203/scoop-bucket` | `Actions: Read and write`, `Contents: Read` |
| `PUBLIC_RELAY_INFRA_TOKEN` | `iml885203/tunlease-public-relay-infra` | `Actions: Read and write`, `Contents: Read` |

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
