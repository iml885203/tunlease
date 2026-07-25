# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately**. Do not open a public
issue for a suspected vulnerability.

Use GitHub's private vulnerability reporting for this repository:
**[Report a vulnerability](https://github.com/iml885203/tunlease/security/advisories/new)**
(Security → Advisories → *Report a vulnerability*).

Please include:

- a description of the issue and its impact,
- the affected component (gateway, CLI, or the `tunnelclient` package),
- steps to reproduce or a proof of concept, and
- any relevant version, commit, or configuration details.

You can expect an initial acknowledgement within a few days. Once a fix is
ready, a patched release and — where appropriate — a GitHub Security Advisory
will be published.

## Scope

Tunlease routes a single claimed path on a fixed endpoint to a developer's
machine and **fails open** to the original application for everything else.
Security-relevant areas include:

- the exclusive-lease and path-allowlist enforcement in the gateway,
- authentication on the tunnel WebSocket upgrade and the claim API,
- the TLS-pinned tunnel between client and gateway, and
- the fail-open behaviour that must never expose unclaimed paths to a client.

## Authentication and ownership

You never set an owner yourself — the gateway assigns one automatically, and it
governs who may **release** a claim.

- **Gateway with no tokens** (the default): authentication is off. Anyone who
  can reach it can claim, and every claim shares the `anonymous` owner — so any
  client can also list or release any other's claim. Fine on a trusted network.
- **Gateway with tokens configured**: each token maps to an owner name in the
  gateway config. A client sends `Authorization: Bearer <token>`; the gateway
  looks it up server-side (a client can't assert an owner it has no token for),
  and a claim is then owned by that token's owner and only that owner can
  heartbeat or release it. Any authenticated owner can list all claims and see
  their owner, paths, and expiry.

Configure per-developer tokens for any gateway reachable outside a trusted
network. See the [platform deployment guide](docs/platform-deployment.md) for
token setup.

## Threat model

Tunlease is intended for controlled, authorized development and staging
environments. Exposing a gateway to untrusted networks without authentication
configured is outside the intended threat model.

Authentication is not denial-of-service protection. An Internet-reachable
deployment also needs edge rate limits, request/header/body limits, connection
quotas, trusted TLS, and ordinary gateway/Ingress availability controls.

`--insecure` disables outer TLS server authentication. The inner tunnel
fingerprint is delivered through that outer connection, so pinning does not
protect against a full man-in-the-middle in this mode. Prefer a trusted
internal CA and use `--insecure` only on a trusted network.

Real staging callback bodies and credentials are delivered to developer
machines. Operators must apply data-classification, authorization, local
logging/retention, and endpoint-access policies appropriate to that data.

Fail-open is performed only by a reachable gateway when no usable tunnel exists
before dispatch. It is not protection from gateway, Ingress, DNS,
load-balancer, or origin outage, and Tunlease does not promise to replay a
request after tunnel dispatch has begun. See the
[failure contract](docs/concepts.md#routing-and-failure-contract).
