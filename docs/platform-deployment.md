# Platform deployment

[English](platform-deployment.md) · [繁體中文](platform-deployment.zh-TW.md)

## Supported topology

Route the existing callback host at `/` to one gateway replica, then point
`fail_open_url` at the original app's internal origin. This is a replacement
for the host's existing `/` route, not an additional competing Ingress.

Before rollout, verify:

- the origin URL cannot resolve back through the gateway;
- `/_tunlease` does not belong to the app;
- the Ingress supports WebSocket upgrade and long idle timeouts;
- the public host has trusted TLS;
- the origin still serves every unclaimed path.

The gateway refuses to start without an HTTP(S) origin. It supports one replica
only; active sessions are in memory and process-local.

## Helm

Build and publish the gateway image to a registry the cluster can pull from,
then use private values such as:

```yaml
image:
  repository: YOUR_REGISTRY/tunlease-gateway
  tag: IMAGE_VERSION
ingress:
  host: callbacks.staging.example.com
  path: /
config:
  failOpenURL: http://myapp-origin.default.svc
  maxClaims: 64
  whitelist:
    - /webhooks/provider/
auth:
  tokens:
    - owner: alice
      token: ALICE_TOKEN
```

```bash
helm upgrade --install tunlease charts/tunlease \
  --namespace tunlease --create-namespace \
  -f values.private.yaml
```

The gateway YAML has only five fields: `listen`, required `fail_open_url`,
`max_claims`, `whitelist`, and `tokens`. Unknown fields fail startup so removed
configuration cannot be silently ignored. `/_tunlease` and one replica are
fixed, not values.

Empty `whitelist` permits every valid path. Empty tokens disable authentication;
all clients then share the `anonymous` owner and can list or release each
other's tunnels. Use one secret token per owner outside a trusted network.

## Verify and operate

```bash
curl -fsS https://callbacks.staging.example.com/_tunlease/healthz
curl -fsS https://callbacks.staging.example.com/webhooks/provider/example
kubectl -n tunlease logs deployment/tunlease-gateway --since=10m
```

Verify the complete sequence: unclaimed request reaches origin; an active
`tunle claim` reaches localhost; Ctrl+C returns the same URL to origin.
`healthz` proves only that the process answers HTTP.

A rollout disconnects all clients. They reconnect when the new gateway is
reachable; requests use the origin during the gap. Use a recreate-style rollout
or otherwise ensure there is never more than one routable gateway replica.
Rollback must restore the host's previous `/` route as well as the workload.

HTTPS/WSS protects the client-to-TLS-terminator hop. If TLS terminates before
the Pod, the cluster hop is inside the trust boundary; use re-encryption or mTLS
when that network is not trusted. A failure after tunnel dispatch is never
replayed to origin because localhost may already have processed it.
