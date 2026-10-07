# Internal security self-review (Phase 2, before G2)

Date: 2026-10-07. This review runs ahead of the external penetration test ([pen-test-scope.md](pen-test-scope.md)) and tests the [threat model](threat-model.md). Four reviewers read the code, each covering one area:

- authentication and sessions;
- tenancy and authorization;
- untrusted input, egress and sandboxes;
- secrets, personal data and audit.

Two findings were also confirmed against a scratch database: the cross-tenant SSO domain and the global passkey wipe. The rest come from reading the code end to end.

The **Status** column is updated as fixes land. A finding is closed only when its fix has a regression test. The test is named in the fix's commit.

It does not replace people-task I3 (an independent internal review, [needs-people.md](../needs-people.md)). It gives that reviewer, and the external testers, a starting list.

## Findings

### Critical and high

| # | Area | Finding | Status |
| --- | --- | --- | --- |
| S1 | Tenancy | A tenant could attach a domain to another tenant's SSO connection. Foreign-key checks ignore row-level security, so the domain would pass verification and the attacker would be added to that tenant on first sign-in (JIT membership). | Fixing |
| S2 | Identity | Adding a passkey or TOTP needed only a session. A stolen session could enrol its own second factor and pass approval step-up. Separately, password sign-in into a tenant where the user is not an admin could be used to enrol a passkey that also signs in to tenants where they are. | Fixing |
| S3 | Identity, tenancy | Any tenant admin could attach any existing user by email, without consent, and then delete that user's passkeys in every tenant. | Fixing |
| S4 | Identity | TOTP step-up codes could be guessed without limit. | Fixing |
| S5 | Secrets | Secret values could reach run history through transport error text: a URL holding an API key in its query, and Slack webhook URLs in alert errors. | Fixing |
| S6 | Governance | An API key acts as `key:<id>`, so its creator counted as a different person. One admin could author a policy and approve it with their own key, or start a payout run with a key and approve it themselves. | Fixing |
| S7 | Governance | Git-led sync could put a repository one person controls in charge of a gated environment, which bypassed four-eyes publishing, policy approval and promotion. | Fixing |
| S8 | Ingest | Connector deliveries verified with one environment's secret could wake runs waiting in another, so a staging test key could forge a production payment confirmation. | Fixing |
| S9 | Ingest | A cron that never fires (30 February) was stored with a zero next-fire time and refired forever, starving every tenant's schedules. | Fixing |

### Medium

| # | Area | Finding | Status |
| --- | --- | --- | --- |
| S10 | Identity | Open redirect after SSO (`return_to=/%5Cevil.com`) | Fixing |
| S11 | Identity | Sign-in CSRF: SSO state was not bound to the browser, and password sign-in accepted non-JSON bodies | Fixing |
| S12 | Governance | API keys outlived their creator's roles and membership, and ignored the creator's environment limit | Fixing |
| S13 | Governance | Delegations stayed valid after the delegator lost the role | Fixing |
| S14 | Governance | Business roles that decide approvals could be granted by anyone with `member.manage` | Fixing |
| S15 | Governance | Several handlers ignored an API key's environment limit | Fixing |
| S16 | Git | A stored Git token could be sent to a new `api_url`. Old push deliveries could be replayed to roll an environment back. | Fixing |
| S17 | Secrets | Platform secrets (Git credentials, webhook secrets) were readable by workflows. The secrets API could write the reserved `_identity` and `_alerts` namespaces. | Fixing |
| S18 | Sandboxes | Code-step CPU and memory limits were set by the workflow's author, with no ceiling. JavaScript `fetch` had no call cap. | Fixing |
| S19 | Ingest | Connector deliveries with an empty or unsigned deduplication key could be replayed | Fixing |
| S20 | Ingest | A tenant connector could answer webhooks with HTML on the platform's origin. The edge sent no security headers. | Fixing |
| S21 | Audit | The application's database role could insert audit rows and move the chain head directly | Fixing |
| S22 | Privacy | Personal data inside free text (provider error messages) is not sealed | Open: needs a design for redacting tainted substrings without false positives |
| S23 | Secrets | Key rotation does not re-wrap personal-data subject keys. The pseudonymisation key is tied to tenant key v1. | Open: planned with BYOK (spec 14.1) |

### Low and informational

| # | Finding | Status |
| --- | --- | --- |
| S24 | Suspended tenants' and users' sessions and API keys kept working | Fixing |
| S25 | Sign-in and ingest rate-limiter maps grew without bound. Rate limiting was per IP only. | Fixing |
| S26 | User enumeration through adding members, and unverified SSO domain claims blocking the real owner | Fixing |
| S27 | Race that could leave a tenant without an owner | Fixing |
| S28 | Runs could pin a deprecated version in an ungated production environment | Fixing |
| S29 | The compliance chain report checked anchor signatures but not the chain against them. The anchorer signed the stored head without recomputing it. | Fixing |
| S30 | Retention purges were not audited | Fixing |
| S31 | Egress blocklist missed 6to4, Teredo and IPv4-compatible IPv6 ranges | Fixing |
| S32 | The PostgreSQL connector did not verify server certificates by default | Fixing |
| S33 | The encryption context binds a secret to its id but not to its environment and name | Open, low: needs database write access to exploit |
| S34 | Tenant code (flow compile, Python checks) runs in the API process with no concurrency limit. Several in-memory caches never evict. | Open, low |
| S35 | Passkey step-up is not bound to the specific approval. The session token is also returned in the sign-in body. No HSTS header. | Open, informational |

## Controls the review confirmed

These hold today. Testers should still try them.

- **Row-level security.** Forced on every tenant table. The tenant scope is set per transaction and is empty by default. Every runtime pool connects as `taskiem_app`. Cross-tenant functions return only routing columns, and callers re-scope.
- **Ids from requests.** Ids in URLs and bodies resolve under RLS. No cross-tenant IDOR was found.
- **Tokens and passwords.**
  - Sessions and API keys are 256-bit random tokens stored as SHA-256.
  - Passwords use Argon2id, with constant-time comparison and a dummy hash for unknown emails.
  - Cookies are `HttpOnly`, `Secure` and `SameSite=Lax`, and cookie sessions must send the CSRF header.
- **OIDC.**
  - The issuer must match exactly, and endpoints must use HTTPS.
  - Only asymmetric algorithms are accepted.
  - `iss`, `aud`, `azp`, `exp`, `iat`, `nbf` and the nonce are checked, and PKCE (S256) is used.
  - A verified email is required.
- **SAML.** Signatures are validated by gosaml2. IdP-initiated responses are refused, and request ids are single-use.
- **WebAuthn.** Challenges are single-use and expire. The origin must match exactly. User presence and user verification are both required. The signature counter is updated with compare-and-set.
- **Approvals.**
  - Only people vote.
  - Each person has one voice per level.
  - Earlier levels' approvers are excluded from later levels.
  - Step-up is checked last, and TOTP codes cannot be replayed.
  - Publishing refuses API keys, the requester and the author.
- **Webhooks.**
  - Every signature scheme compares in constant time and fails closed.
  - Timestamped schemes enforce a time window.
  - Bodies are size-limited.
  - Signature headers never reach runs.
- **Egress.**
  - The vetted IP is pinned.
  - Every resolved address is checked.
  - Redirects are re-vetted.
  - Environment proxies are ignored.
  - Every connector, including the MySQL, PostgreSQL and SFTP clients, dials through the guard.
- **Sandboxes.**
  - Each run gets a fresh WebAssembly instance, with no filesystem and a hard memory cap.
  - A run can see only the secrets its step declares.
  - Imports are allow-listed for tenant connectors.
  - CEL has a cost limit and uses RE2 for regular expressions.
- **Encryption.**
  - AES-256-GCM with a fresh data key per value.
  - Ciphertext is bound to its id and tenant.
  - Tenant keys are stored only wrapped by the KMS key.
  - Shredding a subject's key is one-way.
- **Audit.**
  - Audit rows cannot be updated or deleted.
  - Anchors are Ed25519-signed and append-only.
  - Exports verify offline.
  - Revealing personal data needs a permission and is audited.
