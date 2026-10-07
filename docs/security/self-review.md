# Internal security self-review (Phase 2, before G2)

Date: 2026-10-07. This review runs ahead of the external penetration test ([pen-test-scope.md](pen-test-scope.md)) and tests the [threat model](threat-model.md). Four reviewers read the code, each covering one area:

- authentication and sessions;
- tenancy and authorization;
- untrusted input, egress and sandboxes;
- secrets, personal data and audit.

Two findings were also confirmed against a scratch database: the cross-tenant SSO domain and the global passkey wipe. The rest come from reading the code end to end.

Every finding marked Fixed has a regression test, named in its commit:

- `Harden ingest, sandboxes, egress, audit and alerts`
- `Governance hardening: keys act as their owners, Git-led needs a second person`
- `Identity hardening` (S1–S4, S10, S11, S24–S26).

The whole Go suite, the TypeScript suites and the 9 browser specs pass with all three applied.

It does not replace people-task I3 (an independent internal review, [needs-people.md](../needs-people.md)). It gives that reviewer, and the external testers, a starting list.

## Findings

### Critical and high

| # | Area | Finding | Status |
| --- | --- | --- | --- |
| S1 | Tenancy | A tenant could attach a domain to another tenant's SSO connection. Foreign-key checks ignore row-level security, so the domain would pass verification and the attacker would be added to that tenant on first sign-in (JIT membership). | Fixed |
| S2 | Identity | Adding a passkey or TOTP needed only a session. A stolen session could enrol its own second factor and pass approval step-up. Separately, password sign-in into a tenant where the user is not an admin could be used to enrol a passkey that also signs in to tenants where they are. | Fixed |
| S3 | Identity, tenancy | Any tenant admin could attach any existing user by email, without consent, and then delete that user's passkeys in every tenant. | Fixed |
| S4 | Identity | TOTP step-up codes could be guessed without limit. | Fixed |
| S5 | Secrets | Secret values could reach run history through transport error text: a URL holding an API key in its query, and Slack webhook URLs in alert errors. | Fixed |
| S6 | Governance | An API key acts as `key:<id>`, so its creator counted as a different person. One admin could author a policy and approve it with their own key, or start a payout run with a key and approve it themselves. | Fixed |
| S7 | Governance | Git-led sync could put a repository one person controls in charge of a gated environment, which bypassed four-eyes publishing, policy approval and promotion. | Fixed |
| S8 | Ingest | Connector deliveries verified with one environment's secret could wake runs waiting in another, so a staging test key could forge a production payment confirmation. | Fixed |
| S9 | Ingest | A cron that never fires (30 February) was stored with a zero next-fire time and refired forever, starving every tenant's schedules. | Fixed |

### Medium

| # | Area | Finding | Status |
| --- | --- | --- | --- |
| S10 | Identity | Open redirect after SSO (`return_to=/%5Cevil.com`) | Fixed |
| S11 | Identity | Sign-in CSRF: SSO state was not bound to the browser, and password sign-in accepted non-JSON bodies | Fixed |
| S12 | Governance | API keys outlived their creator's roles and membership, and ignored the creator's environment limit | Fixed |
| S13 | Governance | Delegations stayed valid after the delegator lost the role | Fixed |
| S14 | Governance | Business roles that decide approvals could be granted by anyone with `member.manage` | Fixed |
| S15 | Governance | Several handlers ignored an API key's environment limit | Fixed |
| S16 | Git | A stored Git token could be sent to a new `api_url`. Old push deliveries could be replayed to roll an environment back. | Fixed |
| S17 | Secrets | Platform secrets (Git credentials, webhook secrets) were readable by workflows. The secrets API could write the reserved `_identity` and `_alerts` namespaces. | Fixed |
| S18 | Sandboxes | Code-step CPU and memory limits were set by the workflow's author, with no ceiling. JavaScript `fetch` had no call cap. | Fixed |
| S19 | Ingest | Connector deliveries with an empty or unsigned deduplication key could be replayed | Fixed |
| S20 | Ingest | A tenant connector could answer webhooks with HTML on the platform's origin. The edge sent no security headers. | Fixed |
| S21 | Audit | The application's database role could insert audit rows and move the chain head directly | Fixed |
| S22 | Privacy | Personal data inside free text (provider error messages) is not sealed | Open: needs a design for redacting tainted substrings without false positives |
| S23 | Secrets | Key rotation does not re-wrap personal-data subject keys. The pseudonymisation key is tied to tenant key v1. | Fixed (P4-5, [addendum](#addendum-2026-10-07-bring-your-own-key-phase-4-p4-5)) |

### Low and informational

| # | Finding | Status |
| --- | --- | --- |
| S24 | Suspended tenants' and users' sessions and API keys kept working | Fixed |
| S25 | Sign-in and ingest rate-limiter maps grew without bound. Rate limiting was per IP only. | Fixed |
| S26 | User enumeration through adding members, and unverified SSO domain claims blocking the real owner | Fixed |
| S27 | Race that could leave a tenant without an owner | Fixed |
| S28 | Runs could pin a deprecated version in an ungated production environment | Fixed |
| S29 | The compliance chain report checked anchor signatures but not the chain against them. The anchorer signed the stored head without recomputing it. | Fixed |
| S30 | Retention purges were not audited | Fixed |
| S31 | Egress blocklist missed 6to4, Teredo and IPv4-compatible IPv6 ranges | Fixed |
| S32 | The PostgreSQL connector did not verify server certificates by default | Fixed |
| S33 | The encryption context binds a secret to its id but not to its environment and name | Fixed (P4-5, [addendum](#addendum-2026-10-07-bring-your-own-key-phase-4-p4-5)) |
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
  - Ciphertext is bound to its tenant, environment, name and id (since P4-5; see the addendum).
  - Tenant keys are stored only wrapped by the KMS key.
  - Shredding a subject's key is one-way.
- **Audit.**
  - Audit rows cannot be updated or deleted.
  - Anchors are Ed25519-signed and append-only.
  - Exports verify offline.
  - Revealing personal data needs a permission and is audited.

## What the fixes left open

These are known residuals, carried to the next round:

- **Invitations.** ~~Admins cannot yet list or cancel pending invitations, and invitees cannot decline one. Someone with an account but no membership anywhere cannot sign in to accept.~~ Closed: `GET`/`DELETE /v1/invitations`, `POST /v1/me/invitations/{tenant}/decline`, and an invitee session limited to the invitation routes (`TestInviteeSession` checks it reaches no tenant data). Two notes remain:
  - **Invitee oracle.** `POST /v1/members` answers alike whether or not the email had an account, but what follows differs: someone new is listed as a member at once, someone with an account as a pending invitation (`GET /v1/invitations`, and before it, by their absence from `GET /v1/members`). An admin with `member.manage` can therefore learn whether an email has a Taskiem account. Accepted: it needs `member.manage` in some tenant, reveals existence only (not which tenants), and is the same signal the members list gave before invitations were listed. Closing it would mean inviting new people too, rather than creating their account.
  - **Invitee sessions** sign in someone who belongs nowhere, so the sign-in limiter, the dummy-hash timing and the passkey rules apply to them as to members; they last as long as ordinary sessions (12 hours) and end on sign-out, on accepting (replaced by a tenant session) and when the person is disabled.
- **Git-led connections.**
  - A connection set up before four-eyes was turned on, or before its environment was gated, is not re-reviewed.
  - On an ungated environment with four-eyes off, one `git.manage` holder can still connect a repository in Git-led mode. That follows the tenant's own settings.
  - Pending connection requests are not shown in the web app yet. They are available through the API.
- **Signals.** Signals are matched by environment, not by connection. Signal steps do not name a connection in wd/v1.
- **SAML sign-in** now needs HTTPS or localhost. The browser-binding cookie must be `SameSite=None`, and browsers accept that only on a `Secure` cookie.
- **PostgreSQL connections.** Connections with no `sslmode` now verify the server certificate. A server whose certificate chains to a private CA needs `sslmode` set explicitly.

## Addendum 2026-10-07: embedding (Phase 3, C1)

The embedding foundations ([embedding.md](../embedding.md), decision 0015, threat-model boundary B11) were reviewed against the same four areas before they were committed. Two findings were fixed before commit, each with a regression test in `api/embed_test.go`:

| # | Area | Finding | Status |
| --- | --- | --- | --- |
| E1 | Authorization | An end-user token kept the permissions it was minted with until it expired (up to an hour), even after the partner narrowed the app's end-user permissions | Fixed: permissions are intersected with the app's current list on every request |
| E2 | Untrusted input | A definition that was JSON but not an object skipped the allowed-connector check at save (it could not be read for connectors) | Fixed: refused with 400 |

Open, carried to the next round:

- ~~A suspended sub-tenant's schedules and webhook triggers keep firing; only its tokens and sessions stop.~~ Closed: a suspended tenant does no new work ([governance](../governance.md#suspended-tenants)).
- With four-eyes publishing on in a sub-tenant, an end user's publish is now a request that the partner (as its key's owner) or a sub-tenant member decides ([embedding](../embedding.md#end-users-and-four-eyes)). The partner mints its end users' tokens, so one partner person could both ask (as an end user) and approve (with the key): four-eyes in a sub-tenant separates the partner's people from its end users, not two partner people from each other.
- CORS preflights for `/v1/embed/{app}` look the app's origins up in the database without authentication (one indexed read; no per-address limit yet).
- The partner's run counts across its sub-tenants (`taskiem_partner_usage`) are read without an audit entry: counts only, no sub-tenant data.

## Addendum 2026-10-07: bring your own key (Phase 4, P4-5)

Bring your own key ([BYOK](../byok.md), [decision 0019](../decisions/0019-bring-your-own-key.md), new boundary B17) changes the secrets boundary (B8). The work closed two open findings, each with a regression test:

| # | Status | How | Test |
| --- | --- | --- | --- |
| S23 | Fixed | Rotation queues a background re-wrap of every data key, subject key and the pseudonymisation key. The pseudonymisation key is now a key of its own (`tenant_pseudonym_keys`), started from version 1's material so subject ids do not change. Old versions are retired once unused and destroyed after a grace period | `TestBYOKLifecycle` (subject id unchanged and envelopes readable after version 1 is destroyed), `TestTenantsKeysCLI` |
| S33 | Fixed | Associated data is the tenant, environment, name (or `connection:<id>`) and id (`aad_version` 2). The re-wrap job re-seals older rows in place | `TestBYOKLifecycle` (a renamed row and a row moved to another environment no longer decrypt) |

The BYOK code was reviewed against the same four areas before commit:

| # | Area | Finding | Status |
| --- | --- | --- | --- |
| K1 | Secrets | A code step (no retries by default) resumed after a key outage through a plain retry would have failed at once, its retry budget spent by the outage | Fixed before commit: resumptions (`key_restored`, `key_restored_reconcile`) are exempt from the budget and backoff in `decide` (`TestKeyUnavailableParksAndResumes`) |
| K2 | Secrets | Marking a completed write whose result could not be sealed as "parked, nothing sent" would make the resume send it again | Avoided by design: only failures before the call park as `key_unavailable` (secrets, credentials, client); a result that cannot be sealed takes the crash path (reconcile, or park for a person) |
| K3 | Egress | A tenant-supplied KMS address is an SSRF vector | Fixed before commit: egress guard, HTTPS only, no redirects, private ranges only with `TASKIEM_BYOK_ALLOW_PRIVATE` (`TestVaultTransit`) |
| K4 | Secrets | Customer key credentials copied between tenants' rows by someone with database write access | Fixed before commit: tenant and row id are inside the plaintext the platform KMS authenticates (`TestBYOKLifecycle`) |
| K5 | Authorization | Key operations need only an owner's session: no step-up (passkey or TOTP) | Open, medium. Owners only, every operation audited, leaving still needs the customer's key |
| K6 | Secrets | Rows sealed under the first encryption context can still be renamed by someone with database write access until the re-wrap job reaches them | Open, low: the job starts at once after migration 00100, in batches of 500 per tenant per pass |
| K7 | Secrets | Unwrapped tenant keys stay in process memory until the cache entry expires and the garbage collector reuses it (Go does not zero memory) | Accepted, informational: the same as every key in a running process |
| K8 | Availability | A short KMS blip parks steps until the next key job pass (at most `TASKIEM_KEY_CHECK_INTERVAL`, a minute by default) | Accepted: parking is safe, and **Check now** resumes at once |
