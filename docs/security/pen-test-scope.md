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
5. **Authentication.** Test password sign-in, passkeys, TOTP, OIDC and SAML sign-in, SCIM provisioning, API keys and sessions.
6. **Integrity of the audit chain.** Show whether history can be changed without detection, given access to the application database role.

## In scope

| Target | Detail |
| --- | --- |
| Web app and HTTP API | Everything under `/v1/*` and the web app (built from `web/`). An OpenAPI-style route list is in `api/server.go`. |
| Webhook edge | `/hooks/*`, the connector triggers for each built-in connector, and the Git push hooks under `/git-hooks/*` |
| SSO and provisioning | OIDC and SAML sign-in against a test IdP that the testers control, and SCIM 2.0 at `/scim/v2` |
| Sandboxes | `code` steps in all three languages, tenant WASM connectors (upload and run), and flow-code compile at `/v1/code/compile` |
| Egress | HTTP steps, connectors, and alert webhooks, tested as SSRF from a tenant's point of view |
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
