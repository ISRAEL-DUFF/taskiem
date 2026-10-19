# The public host

The public host serves pages anyone on the internet can open: [storefronts](spec/storefronts.md) and [hosted forms](spec/forms.md) (Phase 5, milestone S1). It is built and off by default. Until the storefront and form pages arrive (S2, S7), a verified host shows "Coming soon".

## What keeps it apart

| Concern | Control |
| --- | --- |
| A flood or a viral page slowing payments, chat or the console | Its own role (`taskiem serve --role public`), deployment, scaling and connection pool (`TASKIEM_PUBLIC_DATABASE_POOL`, 5), apart from `edge`, which takes webhooks |
| A bug reading tenant data | It connects as `taskiem_public`, which can read no table and execute only `taskiem_public_host` and the narrow functions later milestones add. A test fails the build if the role gains any other table privilege or privileged function |
| The platform's secrets | `--role public` needs only `TASKIEM_PUBLIC_DATABASE_URL`: no KMS, no `TASKIEM_DATABASE_URL`, no provider keys. The Helm chart gives its pods `public.existingSecret` alone |
| Cookies on a business's domain | None are set: any a handler tries to set is dropped and logged |
| Script injection and framing | `Content-Security-Policy` allows no inline script or style and no framing (`httpsec.PublicCSP`); a test fails any template that uses inline code. Embedding on verified sites comes with S6 |
| Floods of page views | A per-address limit (300 a minute, bursts of 100), generous because mobile networks put many phones behind one address; static files are not counted |
| Floods of made-up host names | Host lookups are cached in a bounded cache (10,000 hosts, 30 seconds; unknown hosts 10 seconds) |
| Certificates for names it does not serve | `/tls/ask?domain=` answers 200 only for a verified host. It is on the internal metrics port, never on the public listener |

## Setting it up

1. **A login of its own.** As the schema owner:

```sql
CREATE ROLE taskiem_public_login LOGIN PASSWORD '…' NOINHERIT;
GRANT taskiem_public TO taskiem_public_login;
```

   Then set `TASKIEM_PUBLIC_DATABASE_URL` to that login. The server sets `taskiem_public` on every connection, and warns at start if it shares the other roles' login.
2. **Run it.** Use `taskiem serve --role public`, or set `TASKIEM_PUBLIC_ENABLED=true` with `--role all`. It listens on `TASKIEM_PUBLIC_LISTEN` (`:8082`). Metrics, `/healthz`, `/readyz` and `/tls/ask` are on `TASKIEM_METRICS_LISTEN`.
3. **Kubernetes.** Set `public.enabled: true` and create the `public.existingSecret` Secret (default `taskiem-public`) holding `TASKIEM_PUBLIC_DATABASE_URL`. With `networkPolicy.enabled`, set `networkPolicy.tlsProxyNamespace` to the namespace of the proxy that issues certificates.
4. **In front of it**, a proxy that terminates TLS:
   - The preview domain uses one wildcard certificate (`*.<preview domain>`).
   - Businesses' own domains get certificates on demand, after asking `/tls/ask`. With Caddy, use `on_demand_tls { ask http://<public host>:9090/tls/ask }`.
   - Set `TASKIEM_TRUST_PROXY=true` behind it, so limits apply to visitors' addresses.

## Hosts

`public_hosts` maps a host name to the site it serves. A host serves only once verified. A suspended host, or one whose organisation is suspended, answers 410 "This page is unavailable". Suspension takes effect on every replica within 30 seconds, with no signal between them.

Rows are written when storefronts and forms are set up (S2, S6). Until then, operators can add one by hand to try the host:

```sql
INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at)
VALUES ('ada.shops.example.com', '<tenant id>', 'storefront', gen_random_uuid(), now());
```

## Building blocks for the next milestones

- **`engine/sender`:** SMS and email to customers and respondents through the platform's providers, from a durable outbox ([messages](messages.md)).

- **`engine/formguard`:** what every public form uses.
  - Signed one-time form tokens, with a minimum fill time, a maximum age and an idempotency key.
  - A honeypot field.
  - Optional proof-of-work (`pow.js`, under 1.5 KB).
  - Daily-rotating keyed hashes of client addresses, IPv6 by /64, so raw addresses are never stored.
  - Bounded per-address limiters.
- **`engine/publicnames`:** checks slugs, display names and domains.
  - Reserved platform words.
  - Protected names (banks, payment providers, agencies, Taskiem), including look-alikes: digits for letters, Cyrillic and Greek homoglyphs, accents, doubled letters.
  - Punycode and government domains refused.
