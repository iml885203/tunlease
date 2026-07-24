# Architecture

[English](architecture.md) · [繁體中文](architecture.zh-TW.md)

## System overview

```mermaid
flowchart LR
    ThirdParty[Third-party system]

    subgraph Cluster[Shared environment]
        Ingress[Existing Ingress<br/>fixed public endpoint]

        subgraph Workload[Provider endpoint Pod]
            Sidecar[tunlease-sidecar<br/>path router]
            App[Original application<br/>fixed-endpoint workload]
        end

        subgraph GatewayPod[tunlease-gateway Pod]
            API[Claim and route API]
            Registry[Lease registry<br/>memory in staging]
            TunnelServer[Purpose-built tunnel server]
        end
    end

    subgraph Developer[Developer machine]
        CLI[tunlease CLI]
        LocalApp[Local service]
    end

    ThirdParty -->|fixed URL| Ingress
    Ingress --> Sidecar
    Sidecar -->|unclaimed path or failure| App
    Sidecar -->|claimed path| TunnelServer
    CLI -->|claim and heartbeat| API
    API <--> Registry
    Sidecar -.->|poll route table every 3s| API
    CLI ==>|reverse WebSocket tunnel| TunnelServer
    TunnelServer -->|forward request| LocalApp

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class CLI,Sidecar,API,Registry,TunnelServer tunlease;
```

Blue nodes are owned and shipped by tunlease; neutral nodes are third-party systems or existing infrastructure and applications. The control plane consists of claims, heartbeats, leases, and route-table polling. The data plane is the request path through the sidecar to either the original app or the reverse tunnel. The sidecar never depends on Kubernetes APIs.

## Request routing

```mermaid
flowchart TD
    Request[Request reaches tunlease-sidecar]
    Match{Active route matches<br/>the request path?}
    Tunnel{Tunnel responds<br/>within timeout?}
    Local[Forward to local service]
    App[Forward to original app]

    Request --> Match
    Match -->|No| App
    Match -->|Yes| Tunnel
    Tunnel -->|Yes| Local
    Tunnel -->|No: fail open| App

    classDef tunlease fill:#dbeafe,stroke:#2563eb,color:#1e3a8a,stroke-width:2px;
    class Request,Match,Tunnel tunlease;
```

The sidecar uses longest-prefix matching. If the route table is unavailable for more than its maximum stale period, it clears the table, making every request take the original-app path.

## Claim lifecycle

```mermaid
sequenceDiagram
    participant Dev as Developer
    participant CLI as tunlease CLI
    participant GW as tunlease-gateway
    participant SC as tunlease-sidecar
    participant Local as Local service

    Dev->>CLI: tunlease claim --to 8080 /path/*
    CLI->>GW: POST /api/v1/claims
    GW-->>CLI: claim ID, remote port, TTL, heartbeat
    CLI->>GW: Open reverse tunnel
    loop While the claim is active
        CLI->>GW: Heartbeat
        SC->>GW: Poll /api/v1/routes
    end
    SC->>Local: Forward matching callback through tunnel
    Dev->>CLI: Ctrl+C
    CLI->>GW: DELETE claim
```

## Gateway Pod replacement

With the in-memory registry, replacing the gateway Pod removes all leases and tunnels. This is intentional for the current single-replica deployment:

1. The sidecar cannot reach the old tunnel and fails open to the original app.
2. The CLI reconnects, discovers that its lease no longer exists, and creates a new claim.
3. The CLI builds a new tunnel and the sidecar receives the new route.
4. Matching callbacks resume forwarding to the local service.

This behavior has been verified in a staging environment; callback forwarding recovered in approximately 17 seconds without returning gateway-generated 5xx responses.

## Deployment boundary

The core protocol does not require Kubernetes. Kubernetes provides the current packaging and lifecycle model:

- Helm deploys the shared gateway, Service, Ingress, and configuration Secret.
- The target application's version-controlled Deployment adds `tunlease-sidecar`.
- The sidecar takes the application's original Service port; the application moves to a Pod-local port.
- Public hosts, paths, and third-party configuration remain unchanged.
