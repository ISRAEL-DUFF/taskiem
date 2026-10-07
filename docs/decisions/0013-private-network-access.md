# 0013 — Reaching databases and servers on private networks

Date: 2026-10-07 · Status: Proposed (design; not built)

## Problem

The MySQL, PostgreSQL and SFTP connectors dial through the egress guard. The guard refuses private, loopback, link-local and metadata addresses (spec 14.2) and pins the address it checked. That rule is what keeps tenant workflows away from the cluster network and the cloud metadata service. It also means that a database at `10.20.0.5` inside a customer's VPC cannot be reached at all, and that is where most fintechs keep their ledgers.

The design must let a tenant reach its own private hosts without letting any tenant reach anyone else's, or the platform's own network.

## Decision

Three mechanisms, offered in this order. All three keep the guard's rule that a tenant's traffic never leaves the worker towards a private address on the worker's own network.

### 1. SSH tunnel through the customer's bastion (build first)

A connection to MySQL, PostgreSQL or SFTP gains an optional `tunnel` block:

```json
{ "host": "10.20.0.5", "port": "5432",
  "tunnel": { "host": "bastion.acme.ng", "port": "22", "user": "taskiem",
              "private_key": "<secret>", "host_key": "SHA256:…" } }
```

- **The guard vets the bastion.** The bastion must be a public address on the tenant's allow-list, like any other host, and the guard pins the address it checked. The worker never opens a socket to `10.20.0.5`. The bastion opens it, on the customer's network, under the customer's own firewall rules.
- **Host key pinned.** No credential is sent before the bastion's key matches `host_key`, as the SFTP connector already does. There is no trust-on-first-use.
- **One destination.** The tunnel opens a single `direct-tcpip` channel to the connection's `host:port` and nothing else. The SSH session allows no shell, no agent forwarding and no other channels. Customers should restrict the bastion user to that destination on their side (`permitopen`).
- **Credentials** are the connection's sealed fields, exactly as now. The private key is envelope-encrypted and decrypted only in the worker, for the call.
- **No new dependency.** `golang.org/x/crypto/ssh` is already used by the SFTP connector.
- **Lifetime.** A tunnel lives for one call, like the connection it carries. Pooling across calls is a later optimisation, and only within one tenant and connection.

This is the common pattern for hosted ETL and automation tools reaching private databases (Fivetran, Airbyte and Retool all offer an SSH tunnel option). It needs nothing from Taskiem's operators and works on every cloud.

### 2. Outbound relay inside the customer's network (later)

This option is for customers who will not expose SSH. A small `taskiem relay` process runs in their network and dials out to Taskiem over TLS, so no inbound port is opened. Its credential is a per-tenant relay key. Workers send connections for that tenant through the relay, and the relay enforces its own local allow-list of `host:port`.

This needs a relay endpoint on the edge and a multiplexing protocol (HTTP/2 or WebSocket streams). Design it after the tunnel ships and partners say they need it.

### 3. Operator-allowed private ranges (self-hosted, single tenant only)

`TASKIEM_EGRESS_PRIVATE_CIDRS=10.20.0.0/16` lets the guard dial those ranges for every tenant. It is for a self-hosted deployment that serves one organisation inside its own network. Metadata ranges (`169.254.0.0/16`, `fd00:ec2::/32`) and loopback stay refused whatever the setting says.

- The setting is refused at start-up unless `TASKIEM_SINGLE_TENANT=true`.
- The setting is logged at start-up.
- It is never offered on the hosted platform.

## Why

- The guard's invariant ("a tenant never makes the worker connect to a private address on the worker's network") holds in all three cases. The tunnel and the relay move the private hop to the customer's side. The operator setting is limited to deployments where there is only one tenant to protect.
- Allowing private CIDRs per tenant on a shared platform was rejected. Tenants' private ranges overlap with each other and with the cluster's (everyone uses `10.0.0.0/8`), so per-tenant ranges cannot tell a tenant's database from another tenant's database or from the platform's own services.
- VPC peering and PrivateLink per customer are infrastructure work for each partner. They stay an option for large partners and need no code beyond mechanism 3.

## Tests the tunnel must pass

- A connection whose tunnel host is private, or not on the allow-list, is refused before any dial.
- A wrong host key fails before authentication, and the private key is never sent.
- The SSH session opens exactly one `direct-tcpip` channel, to the connection's host and port. Requests for other channels, a shell or agent forwarding are refused or never made.
- A tunnel never outlives its call. The bastion sees the session close when the call ends, times out or is cancelled.
- MySQL, PostgreSQL and SFTP all work through the same tunnel code, tested against an in-process SSH server.

## Not decided

- Whether `tunnel` is a field of each connection or a named, reusable tunnel shared by several connections in a tenant. The per-connection field is simpler and is the starting point.
