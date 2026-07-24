# Adopting Tunlease

[English](adoption-guide.md) · [繁體中文](adoption-guide.zh-TW.md)

Use this checklist when a third party calls a fixed callback URL that your team
cannot change and developers need to route selected staging paths to localhost.
It connects the role-specific guides without replacing them.

## 1. Is Tunlease for you?

- [ ] You have a fixed third-party-facing endpoint, developers need to claim
  individual callback paths temporarily, and unclaimed or failed traffic must
  continue to the original application.

Tunlease is a focused callback-development tunnel, not a general VPN or proxy.
Its safety model combines a path allowlist, exclusive leases with TTL,
optional token authentication, structured audit logs, and fail-open routing to
the original application. See [Architecture](architecture.md) for the system,
failure paths, and protocol details.

## 2. Get the images

- [ ] Build and push `tunlease-gateway` to a registry your cluster can pull from.

The manual GitLab CI job `build-images-self-hosted` builds the gateway Dockerfile
target with `docker buildx` for `linux/amd64` and `linux/arm64`, then pushes
`$TARGET_REGISTRY/tunlease-gateway:$TAG`. `TARGET_REGISTRY` defaults to
`$CI_REGISTRY_IMAGE`; its host and credentials are supplied separately through
`REGISTRY_HOST`, `REGISTRY_USER`, and `REGISTRY_PASSWORD`.

An external team can copy that job into its pipeline and set
`TARGET_REGISTRY` and the three registry login variables for its own registry.
The build uses Go's `TARGETOS`/`TARGETARCH` support and pushes the multi-arch
manifests directly.

This repository does **not** ship a public, reachable shared image source.
Build from the source with the copied job, or use the following mirror template
only after your team has arranged an accessible source. Every angle-bracket
value must be replaced:

```bash
export TUNLEASE_TAG="<COMMIT_SHA_OR_RELEASE_TAG>"

skopeo copy --all \
  "docker://<ACCESSIBLE_SOURCE_REGISTRY>/tunlease-gateway:$TUNLEASE_TAG" \
  "docker://<YOUR_REGISTRY>/tunlease-gateway:$TUNLEASE_TAG"
```

Deployment prerequisites and image overrides are covered in the
[Platform deployment guide](platform-deployment.md).

## 3. Deploy the gateway

- [ ] Follow
  [Platform deployment → Install the gateway](platform-deployment.md#install-the-gateway).

The API and reverse tunnel use the same HTTP Ingress path; the tunnel is a
WebSocket connection. Configure the Ingress or load balancer to preserve
WebSocket upgrades and use read/send timeouts of at least 3600 seconds. No UDP
listener or forwarding is needed.

## 4. Front the app with the gateway

- [ ] Follow
  [Platform deployment → Front the app with the gateway](platform-deployment.md#front-the-app-with-the-gateway)
  so the fixed endpoint's Ingress routes to the gateway.

The gateway routes claimed paths through the tunnel and fails open to the
application for everything else. Set `fail_open_url` to the app's Service.

## 5. Developers claim paths

- [ ] Choose the [`tunle` CLI](developer-guide.md), or embed
  [`pkg/tunnelclient`](go-client.md) directly in a Go application.

Both options create and maintain the same claim, lease, heartbeat, and reverse
tunnel lifecycle. Start the local service first and claim the narrowest
approved path.

## 6. Verify end-to-end

- [ ] Claim a test path and confirm it appears in `tunle list`.
- [ ] Call that path through the fixed third-party-facing URL and confirm the
  request reaches localhost.
- [ ] Release the claim, then confirm the path returns to the original
  application.
