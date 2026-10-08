# 0028 — Explicit egress proxy: the guard vets, then CONNECTs to the vetted IP

Date: 2026-10-08 · Status: Accepted (amends 0024, point 8)

## Context

Decision 0024 gives fixed egress addresses by NAT and ignores `HTTP(S)_PROXY` for tenant traffic, because a forward proxy given a host name resolves it again after the guard checked it. That reopens DNS rebinding to private and metadata addresses (spec 14.2). It left one case open: a customer's dedicated deployment whose network lets nothing out except through its own proxy.

## Decision

1. **One explicit setting.** `TASKIEM_EGRESS_PROXY` names an HTTP `CONNECT` proxy as `http://host:port` or `https://host:port`, with the port written out and nothing else. Credentials come from `TASKIEM_EGRESS_PROXY_USER` and `TASKIEM_EGRESS_PROXY_PASSWORD`, never from the URL. Anything else stops the process at start: a bare `host:port`, a missing port, another scheme, a path, credentials in the URL, a password without a user, or credentials without a proxy. `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are still never read for tenant traffic.
2. **The guard stays in charge.** For every connection it checks the allow-list, resolves the name once, and refuses if any address is private, loopback, link-local, metadata or otherwise not public, as without a proxy. Only then does it open a tunnel: `CONNECT <vetted IP>:<port>`. A name is never sent to the proxy (the code refuses to), so the proxy has nothing to resolve.
3. **TLS is end to end.** The tunnel is a plain byte stream. The HTTP client verifies the destination's certificate against the original host name from the URL, not the IP dialled.
4. **Redirects are re-vetted.** Each redirect is a new request through the guard: allow-list, resolution and the address checks again, then a new tunnel.
5. **Scope.** Every connection made through the egress guard: connector calls, HTTP steps, sandbox fetches, alert and partner webhooks, tenants' databases and SFTP, customer KMS calls (BYOK), and the container-step proxy's upstream connections. An operator's loopback exception (a fake provider on the same machine) is dialled directly. Platform calls that use Go's default transport (billing providers, the platform KMS, Git hosts, a self-hosted model) are unchanged and still follow the standard proxy variables.
6. **Failures.** A proxy that cannot be reached, or rejects the credentials, means nothing was sent (the step may retry). A proxy that answers `403` refused the destination: fatal, like a guard denial. Only TCP goes through the proxy.

## Alternatives considered

- **Honour `HTTPS_PROXY`.** Rejected, as in 0024: the proxy would resolve names itself, and the variable is often set for unrelated tools.
- **Send the host name and trust the proxy to block private ranges.** Rejected: it moves the SSRF control out of Taskiem to a box we do not test, and DNS can answer differently there.
- **SOCKS5.** Rejected for now: HTTP `CONNECT` is what corporate proxies offer, and one protocol is less to get wrong.

## Consequences

- A proxy that allows `CONNECT` only to port 443 blocks plain HTTP providers and tenants' databases on other ports. The operator must allow the ports their tenants need.
- A proxy that allows only listed host names cannot work, because it sees only IPs. Such a proxy needs IP rules, or the deployment uses NAT.
- With BYOK private addresses allowed (`TASKIEM_BYOK_ALLOW_PRIVATE`), calls to the customer's private KMS also go through the proxy, which must reach it.
- Credentials to an `http://` proxy cross the network in the clear. Keep the proxy on the private network, or use `https://`.
