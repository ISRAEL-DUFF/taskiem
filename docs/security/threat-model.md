# Threat model

What Taskiem protects, from whom, where the trust boundaries are, and which control holds at each one. It is written for the external penetration test (spec 14.4, gate G2), whose scope is in [pen-test-scope.md](pen-test-scope.md), and for anyone changing a boundary. The internal self-review that tested this model is in [self-review.md](self-review.md).

Keep this document current. A change that adds an entry point, a role, a store of secrets or a new way to run tenant code updates the tables below in the same pull request.

## What we protect

| Asset | Why it matters | Where it lives |
| --- | --- | --- |
| Provider credentials (bank, wallet, payments, KYC API keys; OAuth refresh tokens; database passwords) | Each one can move money or read customer identity data | `secrets` and `connections`, envelope-encrypted (AES-256-GCM data keys under a tenant key, under the KMS root key) |
| Money-moving decisions | A forged approval, a replayed webhook, a duplicated transfer or a skipped four-eyes check moves money wrongly | Run history (`run_events`), approvals, policies, idempotency keys |
| Personal data (BVN, NIN, account numbers, names, phone numbers) | NDPA obligations; identity theft | Sealed fields in run inputs and outputs (`engine/pii`), approval subjects, reports |
| Workflow definitions and policies | Whoever controls them controls what the platform does with the credentials | `workflows`, `workflow_versions`, `policies`, Git repositories |
| Audit chain | The evidence regulators and partners rely on | `audit_log` (hash chain), signed anchors on disk and sent by email |
| Tenant isolation | One tenant's data or credentials reaching another is the worst outcome for a multi-tenant platform | Row-level security on every tenant table; the tenant in the session |
| Availability of the engine | Stalled payouts and missed salary runs | Postgres, worker queues |

## Who attacks

| Actor | Starting position | Wants |
| --- | --- | --- |
| Internet attacker | Can reach the public URL, `/hooks/*` and `/git-hooks/*` | An account, webhook forgery, SSRF into the cluster, denial of service |
| Malicious or compromised tenant member | Holds a valid session, possibly a custom role, in one tenant | More rights in the tenant, another tenant's data, raw secrets, an approval they should not give, a publish that skips four-eyes |
| Malicious tenant code | A `code` step (JavaScript, TypeScript or Python), a tenant WASM connector, or flow code compiled at publish | Escape the sandbox, read other tenants' data or secrets, reach internal addresses, exhaust the host |
| Compromised provider or identity provider | Sends webhooks, API responses, SAML assertions or OIDC tokens | Forge events or logins, inject content that is shown to people or fed into expressions |
| Compromised Git repository or Git host | Can push to a connected repository and send push hooks | Deploy a workflow without the platform's approval flow |
| Insider with infrastructure access | Database, cluster or KMS access | Read secrets, rewrite history without detection |

Out of scope for the model: a compromised KMS root key together with the database (accepted: that is the root of trust), a compromised build pipeline (covered by release signing, spec 14.3, once releases are cut), and physical attacks.

## Trust boundaries and entry points

```
 browser ──HTTPS──▶ ingress ──▶ api (8080) ──┐
 providers ─HTTPS─▶ ingress ──▶ edge (8081) ─┤──▶ Postgres (RLS, taskiem_app)
 Git hosts ─HTTPS─▶ ingress ──▶ edge ────────┤
 IdPs ◀─redirects─ browser                   │
                       orchestrator, scheduler ┤
 providers ◀─egress guard── worker ───────────┘──▶ OpenBao transit (root key)
                              └─ sandboxes: QuickJS, CPython, tenant WASM (wazero)
```

| # | Boundary | Entry points | Main controls | Code |
| --- | --- | --- | --- | --- |
| B1 | Internet → API | `/v1/*`: sign-in, passkeys, SSO start and callbacks, signup (off by default), and every authenticated route | Session cookies (`HttpOnly`, `Secure`, `SameSite=Lax`); cookie sessions must send the `X-Taskiem-Request` header on every non-GET request, and there is no CORS; per-IP sign-in rate limit; Argon2id password hashes; API keys stored hashed; permissions checked per route (`need(...)`); step-up (passkey or TOTP) for approvals; administrators held to passkeys once a public URL is set | `api/server.go`, `api/auth.go`, `api/passkeys.go`, `engine/webauthn`, `engine/totp` |
| B2 | Identity provider → API | OIDC callback, SAML ACS, SCIM | OIDC: state, nonce and PKCE; issuer, audience, expiry and signature checked. SAML: signed assertion required (gosaml2), IdP-initiated responses refused, `InResponseTo` bound to a single-use RelayState, audience, recipient and validity window checked. SCIM: per-tenant bearer token; owners never deprovisioned; JIT users only in DNS-verified domains | `api/sso.go`, `engine/oidc`, `engine/saml`, `api/scim.go` |
| B3 | Provider → edge | `/hooks/{tenant}/{path}` and connector triggers | Signature verified before parsing (HMAC, Slack v0, query secret, basic, connector-specific), compared in constant time; body size limit; deduplication; handshakes answer only the documented challenge | `engine/ingest`, `engine/connector/webhook.go` |
| B4 | Git host → edge | `/git-hooks/*` | Per-repository webhook secret; a push only triggers a sync, which reads the repository with the tenant's token and goes through the same validation, policy and four-eyes rules as publishing in the UI | `api/git.go` |
| B5 | Worker → internet | HTTP steps, connectors, alert webhooks, Slack | Egress guard: per-tenant host allow-list, private, loopback, link-local and metadata ranges refused, the vetted IP pinned for the connection, redirects re-checked | `engine/egress` |
| B6 | Tenant code → host | `code` steps, tenant WASM connectors, flow code at compile time | WebAssembly only (wazero, no host filesystem or sockets); memory, time and output limits; fetch only through the egress guard; secrets only those the step declares; a fresh instance per run | `engine/sandbox`, `engine/wasmconn`, `engine/flowcode` |
| B7 | Tenant → tenant | Every table holding tenant data | Postgres row-level security, forced, on every tenant table; the app connects as `taskiem_app` and sets the tenant per transaction; services that run across tenants (scheduler, ingest) scope each query explicitly | `engine/db/migrations`, `api.Server.tx` |
| B8 | Application → secrets | Secret reads in the worker | Envelope encryption, ciphertext bound to tenant and name; decrypted only in worker memory for the call; never in logs, events or responses (log attributes redacted) | `engine/secrets`, `cmd/taskiem` (`redactAttr`) |
| B9 | Application → audit | Every privileged action | Append-only hash chain; heads signed and written outside the database and emailed; offline verification of exports | `engine/audit` |

## Threats and responses

STRIDE per boundary. **Status** is what the code does today. "Self-review" points to a finding in [self-review.md](self-review.md).

| Boundary | Threat | Response | Status |
| --- | --- | --- | --- |
| B1 | Credential stuffing or brute force on sign-in | Per-IP burst limit on sign-in; Argon2id; passkeys for administrators | In place. A per-account limit is a follow-up |
| B1 | CSRF on state-changing routes | Required `X-Taskiem-Request` header on cookie sessions; `SameSite=Lax`; no CORS. Sign-in and SSO callbacks bound to the browser (self-review) | In place |
| B1 | Session theft, and what a stolen session can do | `HttpOnly` and `Secure` cookies; sessions revoked on sign-out and deprovisioning; adding or removing a second factor needs an existing factor; step-up codes rate-limited (self-review) | In place |
| B1 | Privilege escalation through custom roles or API keys | Nobody grants a permission they lack; API keys cannot exceed their creator | In place |
| B1 | Approving your own request; approving without a second factor | Policies forbid the requester; step-up per decision; four-eyes on publishing and policy changes | In place |
| B2 | Forged SAML assertion (signature wrapping), replay | Signature validation by gosaml2/goxmldsig; single-use request ids; window checks | In place |
| B2 | Account takeover by linking an IdP identity to an existing account | JIT only within DNS-verified domains; SSO enforcement with an owner break-glass | In place |
| B3 | Forged or replayed webhook starting a payout | Signature before parsing; deduplication; workflows take money decisions only after the provider's own verification step | In place |
| B3 | Flooding `/hooks` | Body size limit; soft ingest limits per tenant | Body limit in place. Per-tenant ingest limits are carried gap A4 |
| B4 | A push deploying around four-eyes | Git-led publishing still passes validation and the environment's publish policy | In place |
| B5 | SSRF to cloud metadata or the cluster network | Egress guard on every outbound connection, with the IP pinned against DNS rebinding | In place. Private databases (MySQL, Postgres, SFTP inside a customer network) need a reviewed design: carried gap A4 |
| B6 | Sandbox escape | WebAssembly isolation; no host functions beyond fetch, logs and declared secrets | In place |
| B6 | Resource exhaustion by tenant code | Memory, time and output limits per instance; queue concurrency | In place |
| B7 | Cross-tenant read through a missing tenant filter | Forced RLS, so a missing `WHERE tenant_id` returns nothing rather than another tenant's rows | In place |
| B8 | Secrets in logs, errors, run history or AI prompts | Redaction of log attributes; secret values never placed in events | In place. Per-use `secret.read` auditing is carried gap A4 |
| B8 | Ciphertext swapped between tenants or names | Associated data binds tenant and name | In place |
| B9 | History rewritten by someone with database access | Hash chain plus anchors outside the database; anchors emailed | In place, provided anchors go to write-once storage (see [kubernetes.md](../kubernetes.md#storage)) |
| All | Personal data exposed in history, approvals or reports | Declared PII fields sealed (inputs and outputs); unsealing needs a permission and is audited | Nested input paths (`transfers[].account_name`) are carried gap A4 |

## Assumptions

- TLS terminates at the ingress. `TASKIEM_TRUST_PROXY` is on only behind a proxy that overwrites `X-Forwarded-For`.
- The database role used by the pods is `taskiem_app`, not the schema owner. Only migrations run as the owner.
- OpenBao (or the cloud KMS) is separate from the database and its token is scoped to the transit key.
- The scheduler's anchor directory is write-once storage, or anchors are also emailed outside the platform.
- Cluster network policies or the egress guard, not tenant goodwill, stop workers reaching internal services.
