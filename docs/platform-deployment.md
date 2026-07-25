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

The gateway refuses to start unless exactly one unclaimed-request target is
configured: an HTTP(S) `fail_open_url`, or an `unclaimed_status` error between
400 and 599 for a relay without an original app. It supports one replica only;
active sessions are in memory and process-local.

## Helm

The chart uses the published gateway image by default. Create private values
for the existing host, original app, allowed paths, and optional tokens:

```yaml
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

Override `image.repository` and `image.tag` only when using your own build or
release mirror.

The gateway YAML fields are `listen`, one of `fail_open_url` or
`unclaimed_status`, `disable_claim_list`, `max_claims`,
`max_claims_per_owner`, `max_claim_duration`, `min_claim_path_segments`,
`dynamic_client_identity`, `whitelist`, and `tokens`. Unknown fields fail
startup so removed configuration cannot be silently ignored. `/_tunlease` and
one replica are fixed, not values.

Empty `whitelist` permits every valid path. Empty tokens disable authentication;
all clients then share the `anonymous` owner and can list or release each
other's tunnels. Set `disable_claim_list: true` on an anonymous public demo to
hide claim IDs while retaining release by an ID already recorded by the client.
Use one secret token per owner for a non-demo gateway outside a trusted network.

For a low-friction public demo, `dynamic_client_identity` lets the CLI create
and persist a random identity without asking the user for a token. The gateway
stores only its hash as the owner, filters `list` to that owner, and permits
only that owner to release the claim. This isolates cooperative clients; it is
not authentication because a malicious client can generate more identities.
Do not configure static `tokens` at the same time.

The following public-demo policy permits `/demo/testing/*` but rejects the
broader `/demo/*`, limits each identity to one tunnel, and expires every claim
after one minute:

```yaml
config:
  maxClaims: 20
  maxClaimsPerOwner: 1
  maxClaimDuration: 1m
  minClaimPathSegments: 2
  dynamicClientIdentity: true
  whitelist:
    - /demo/
```

Expiry is terminal: the gateway notifies the client, closes the tunnel, and
releases its paths. Reconnecting after network loss creates a new claim with a
new duration. Rate-limit repeated connections at the edge when operating an
untrusted public relay.

## Verify and operate

```bash
curl -fsS https://callbacks.staging.example.com/_tunlease/healthz
curl -fsS https://callbacks.staging.example.com/webhooks/provider/example
kubectl -n tunlease logs deployment/tunlease-gateway --since=10m
```

Verify the complete sequence: unclaimed request reaches origin; an active
`tul claim` reaches localhost; Ctrl+C returns the same URL to origin.
`healthz` proves only that the process answers HTTP.

A rollout disconnects all clients. They reconnect when the new gateway is
reachable; requests use the origin during the gap. Use a recreate-style rollout
or otherwise ensure there is never more than one routable gateway replica.
Rollback must restore the host's previous `/` route as well as the workload.

HTTPS/WSS protects the client-to-TLS-terminator hop. If TLS terminates before
the Pod, the cluster hop is inside the trust boundary; use re-encryption or mTLS
when that network is not trusted. A failure after tunnel dispatch is never
replayed to origin because localhost may already have processed it.
