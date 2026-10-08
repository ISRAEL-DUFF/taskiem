# Penetration test: scope and rules of engagement

This is the scope for Taskiem's first external penetration test (spec 14.4). The test is gate G2 item 2, and finding a firm is people-task X1 in [needs-people.md](../needs-people.md). Everything a firm needs to quote and start is here or linked from here.

Read with:

- [threat-model.md](threat-model.md): the assets, actors and boundaries to test against.
- [self-review.md](self-review.md): what the internal review already found and fixed. Testers should try to break those fixes too.

## Objectives

In order of priority:

1. **Isolation between tenants.** Show whether one tenant can read or change another tenant's data, secrets, runs or audit trail.
2. **Money movement without authority.** Show whether a payout or transfer can start, or be approved, without the authority the workflow's policy requires. Routes to test include:
   - forged or replayed webhooks;
   - self-approval;
   - approval without step-up;
   - publishing around four-eyes;
   - promoting a version into production without approval;
   - pushing a definition through Git.
3. **Secret and personal-data disclosure.** Show whether provider credentials, sealed personal data or session tokens can be recovered through:
   - the API;
   - the run history and live stream;
   - logs, errors and alerts;
   - exports and reports.
4. **Escape from tenant code.** Show whether code steps (JavaScript, TypeScript, Python), tenant WASM connectors or flow code compiled at publish can:
   - reach the host;
   - reach other tenants;
   - reach internal network addresses;
   - deny service to other tenants.
5. **Authentication.** Test password sign-in, passkeys, TOTP, OIDC and SAML sign-in, SCIM provisioning, API keys and sessions. Step-up challenges (`POST /v1/me/step-up/options`) name an operation and target; try spending one on another approval, decision or change. Browser sign-ins get a cookie and no token in the body (`"bearer": true` for API clients).
6. **Integrity of the audit chain.** Show whether history can be changed without detection, given access to the application database role.

## In scope

| Target | Detail |
| --- | --- |
| Web app and HTTP API | Everything under `/v1/*` and the web app (built from `web/`). An OpenAPI-style route list is in `api/server.go`. |
| Webhook edge | `/hooks/*`, the connector triggers for each built-in connector, and the Git push hooks under `/git-hooks/*` |
| SSO and provisioning | OIDC and SAML sign-in against a test IdP that the testers control, and SCIM 2.0 at `/scim/v2` |
| Sandboxes | `code` steps in all three languages, tenant WASM connectors (upload and run), and flow-code compile at `/v1/code/compile` |
| Egress | HTTP steps, connectors, and alert webhooks, tested as SSRF from a tenant's point of view |
| Customer keys (BYOK, B17) | `/v1/keys`: changing keys without step-up or with a step-up made for something else (rotate, bring, replace credentials and remove need a passkey asked for that operation, or an authenticator code), a tenant-supplied KMS address as SSRF, recovering or moving another tenant's key credentials, keeping access after a customer revokes its key (beyond the documented cache bound), and making a revocation lose data or send a payment twice ([BYOK](../byok.md)). Use the testers' own OpenBao or cloud KMS |
| Operator console (B19) | `/v1/ops/*` and `/ops`: reaching it with a tenant's session, API key or passkey, or reaching tenant routes with an operator's session; creating or escalating an operator account through the API; writing without a passkey step-up, or with one made for another operation, target or operator; enrolment links replayed; SSO from another issuer or subject; reading tenant data beyond plan, subscription state, limits, usage and pool; acting without a trace in the platform audit chain. Testers get one operator account, enrolled through `taskiem operators add`, and one on the reviewer list ([operator console](../operator-console.md)) |
| Billing (B14) | `/v1/billing/*` and the payment webhooks `/v1/billing/webhooks/{paystack,flutterwave}`, against the providers' test modes only: forged, replayed or edited webhooks; a smaller payment or another currency settling an invoice; paying one invoice twice (two checkouts, a card charge pending at the bank and the dunning retry) and whether the second is applied or only recorded for a refund (`billing.payment.unapplied`); raising a plan or limits without a verified payment ([billing](../billing.md)) |
| Self-serve signup (B1) | `/v1/signup` with signup turned on: the per-address limit across replicas, throwaway domains, names carrying links, and what an organisation that has not confirmed its email can make the platform send to strangers (invitations, API keys and email alert channels are held back; alert tests are paced) ([onboarding](../onboarding.md)) |
| Connector catalogue (B18) | `/v1/catalogue/*` as a publisher (the testers get a verified namespace): packages that exhaust the checker (cases, hosts, module time and memory; bounded per submission), a verified publisher renaming itself or squatting a namespace, a version changed after review, reading another tenant's submissions, installs or modules ([connector submissions](../connector-submissions.md)) |
| Remote triggers and voice notes (B16, B10) | PGDock deliveries to `/hooks/{tenant}/connectors/pgdock@1/row_changed?subscription=` (forged, replayed, aimed at another tenant's or workflow's subscription), against a PGDock test organisation; WhatsApp voice notes from a bound test number (oversized, overlong, malformed Ogg, a media URL elsewhere) with a self-hosted transcription server |
| Deployment | The Helm chart's defaults (`deploy/helm/taskiem`): pod security, network policy, the metrics port, the migration job. White-box review of the chart is welcome. |
| Source code | The full repository, for white-box testing. Testers get read access. |

## Out of scope

- Third-party providers' own APIs and sandboxes: Paystack, Flutterwave, banks, Meta, Slack, Google and the rest. Do not send traffic to them beyond what a workflow under test sends through its connectors.
- Denial of service by volume against shared infrastructure. Resource-exhaustion tests against the sandboxes and queues are in scope, up to the limits agreed for the test environment.
- Social engineering of staff, and physical testing.
- The KMS (OpenBao) itself, beyond how Taskiem authenticates to it.
- Findings that need a compromised KMS root key and database together. That combination is the accepted root of trust.

## Environment

A dedicated deployment, isolated from production and from design partners. It needs:

- The Helm chart in `split` mode, at a public URL with TLS. The image is built from the commit under test, and the commit SHA is recorded in the report.
- Postgres 16, and OpenBao with the transit engine. Neither is shared with anything else.
- Two tenants, **A** and **B**. Each tenant has:
  - an owner;
  - an administrator held to passkeys;
  - a publisher;
  - an approver;
  - a read-only member;
  - an API key with a custom role.
- Workflows seeded from `flows/dogfood` and `flows/examples`. Connectors point at the fake provider servers in each connector's tests or at providers' sandboxes. No live money moves.
- Credentials handed to testers through the password manager agreed with the firm, never by email or chat.
- Logs, metrics and traces available to the Taskiem team throughout the test, so findings can be confirmed from the server side.

## Rules of engagement

- Test window and contact people are agreed in writing before the start. Each side names someone reachable throughout the window.
- Stop and report at once on finding:
  - a way into tenant B from tenant A;
  - recovery of a provider credential;
  - code execution outside a sandbox.
  The finding is fixed or contained before testing continues in that area.
- Do not test against production, design partners' tenants, or any provider's live environment.
- Personal data in the environment is synthetic. If real data appears, stop and report it.

## Deliverables

- A report rating each finding by severity, with steps to reproduce, the affected commit, and evidence.
- A retest of every critical and high finding after the fixes. Gate G2 requires all critical and high findings to be fixed.
- A short statement suitable for sharing with design partners' risk teams (G2 item 3).

## What helps testers start fast

- [operations.md](../operations.md) describes every setting. [kubernetes.md](../kubernetes.md) covers the deployment.
- [governance.md](../governance.md) covers roles, approvals, four-eyes, passkeys, SSO and SCIM. [privacy.md](../privacy.md) covers personal data.
- The contracts are in `docs/contracts/`: wd/v1 for workflows, connector/v1, and wd-test/v1.
- `make up` runs the whole stack on one machine with Docker Compose, for local exploration before the window opens.

## Focus from the 2026-10-08 review

The [independent review of the Phase 4 work](self-review.md#addendum-2026-10-08-independent-review-of-the-phase-4-work) fixed R1 to R6. Testers should try to break those fixes, and look hardest at:

- **Money flows in billing.** Any way to pay once and get two periods or a higher plan, to be charged twice without the second payment showing as unapplied, or to reach `settle` with a body the provider did not verify.
- **What an unconfirmed signup can reach.** Every path from a fresh self-serve tenant to an email, SMS or WhatsApp message to someone outside it, sent by the platform.
- **The checker as a target.** Submissions built to hold the API's tenant-code slots, or to make the checker resolve or reach internal addresses.
- **Removing work without four eyes (R7, open).** What one person with `workflow.publish` can stop in a gated environment by undeploying.
- **The dispatch role and definer functions.** With the application role's database credentials, anything beyond routing columns of another tenant through `taskiem_dispatch`-owned functions.
