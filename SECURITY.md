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

Tunlease is intended for controlled, authorized development and staging
environments. Exposing a gateway to untrusted networks without authentication
configured is outside the intended threat model.
