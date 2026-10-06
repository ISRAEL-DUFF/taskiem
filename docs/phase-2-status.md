# Phase 2 status

Phase 2 (build plan) is about 12 weeks: it makes the platform fit for the first external design partners. Started 2026-10-06, while Phase 1's go-live and soak (milestones 4 and 5) wait on people and production; see [Carried over from Phase 1](#carried-over-from-phase-1). Last updated 2026-10-06.

> Everything here that needs people (signatures, owners' confirmations, credentials, hardware, reviews, partners) is tracked in one place: [What needs people](needs-people.md).

## Milestones

The build plan lists Phase 2's deliverables without an order. This is the order they are built in: each milestone gives the next something to stand on (tests before Git-led deploys, policies before four-eyes publishing, the WASM runtime before third-party connectors).

| # | Weeks | Milestone | Deliverables (spec) | Status |
| --- | --- | --- | --- | --- |
| 1 | 1–3 | Engine and local tooling | `parallel` step (3.2); workflow tests with mocked connector outputs (10.5); CLI: `validate`, `test`, `diff`, `deploy`, `runs tail`, `dev` (10.4) | **Done** (below) |
| 2 | 2–6 | Code and Git | TypeScript SDK and compiler to WD; deterministic codegen from the canvas; three-way merge on parent digest (10.1, 10.2); GitHub and GitLab, platform-led and Git-led (10.3) | **Done** (below), except a published Taskiem GitHub App and Bitbucket |
| 3 | 3–8 | Governance and privacy | Policy objects: amount rules, multi-level approvers, escalation, delegation, step-up with passkey or TOTP; four-eyes on production publishing and policy edits (9.1); Nigerian-identifier PII detectors, redaction everywhere, `pii.reveal` (9.3); per-workflow retention (9.4); compliance reports and audit-chain anchoring (9.2, 9.6) | **Done** (below); emailed anchors arrived with milestone 5's alerts |
| 4 | 4–10 | Connectors | WASM runtime for third-party connectors; contract-drift monitor (6.3, 6.4); 13 African connectors and 7 global ones (6.5) | **Done** (below): WASM runtime, drift monitor, and 25 connectors (16 African, 9 global); live sandbox checks wait on provider accounts |
| 5 | 6–11 | Identity and operations | Passkeys by default for admins; SSO (OIDC, SAML); SCIM; custom roles (13.2, 13.3); staging environments, alerts to email and Slack, dashboards, live run view on the canvas (15.1); Python sandbox (7.1) | **Done** (below) |
| 6 | 11–12 | External readiness | First external penetration test (14.4); design partners onboarded | Needs people: [X1–X5](needs-people.md#external-readiness-phase-2-milestone-6-gate-g2) |

### What engineering cannot finish alone

- **Connectors** are built from each provider's public documentation (clean-room policy) and tested against recorded fixtures. The gate's nightly sandbox checks need a sandbox account per provider.
- **SSO and SCIM** need test identity providers registered by the organisation. A one-click **Taskiem GitHub App** needs the organisation to register it; until then tenants use a token or their own GitHub App.
- **Gate G2** needs design partners, an external penetration test, and a partner's compliance sign-off.

## Milestone 1: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Engine | `parallel` step: branches run side by side under `max_concurrency`. `join: all` fails once in-flight steps settle after a branch fails. `join: any` records the winning branch in history, cancels the losers' unfinished steps (new `StepCancelled` event: timers, signal waits, approvals and unclaimed tasks removed), waits for a loser write already under way, and compensates the losers' committed effects before completing | `engine/decide`, `engine/runtime`, migration 00014, [wd/v1 contract](contracts/wd-v1.md) |
| Worker | A worker never starts a cancelled write: it checks for cancellation under the run lock before recording intent | `engine/runtime/worker.go` |
| Fix | A failing `foreach` waited forever when another iteration had steps it would never start; failing control steps now wait only for steps in flight | `engine/decide` |
| Web app | `parallel` in the step palette, node summaries, and run timeline (`cancelled`) | `web/` |
| Workflow tests | `wd-test/v1`: trigger, mocked step outcomes (per attempt, by step or instance), signals, approval decisions; assertions on status, step states, outputs, what each step sent, and attempt counts. Runs the real orchestrator on a virtual clock; mocked errors are classified by the real action classes. The three dogfood workflows have 12 cases, run by `go test` | `engine/wdtest`, `flows/dogfood/tests/`, [contract](contracts/wd-test-v1.md) |
| CLI | `validate` (now the same checks as publishing, shared with the API), `test`, `diff`, `deploy`, `runs tail`, `dev` (private Postgres cluster, bootstrap, hot reload that tests before it deploys) | `cmd/taskiem`, `engine/wdcheck`, [CLI guide](cli.md) |
| API | Workflow listings carry the definition's id (`key`), which the CLI matches local files by | `api/workflows.go` |

`taskiem dev` uses the Postgres installed on the machine rather than downloading binaries at run time; `--dsn` covers machines without it. Recorded connector fixtures in `dev` are not wired yet: workflows that call providers need sandbox credentials or test mocks.

## Milestone 2: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| SDK | `@taskiem/sdk`: `workflow(...).step()/.next()`, helpers for every step type and trigger, typed helpers for every built-in connector action (generated from the manifests; a test fails when they are stale) | `sdk/src`, `tools/sdkgen`, [SDK guide](sdk.md) |
| Expressions | Arrow functions over `{ trigger, steps, env, run, secrets, item, index }` compile to CEL; constructs without a CEL meaning are refused with the field and the alternative. Recognises the SDK's `cel` helpers after bundlers rename them | `sdk/src/expr.ts` |
| Code generation | Definition to `.flow.ts`, deterministic and stable. An expression becomes an arrow function only when it compiles back to the identical CEL, so generated code always builds to its definition. All workflows in `flows/` have their code committed and are checked both ways (`go test`, `pnpm test`), and `tsc` checks the code | `sdk/src/codegen.ts`, `flows/**/*.flow.ts` |
| In the binary | esbuild bundles flow code with the embedded SDK and QuickJS runs it: `taskiem build` (`--check` for CI), `taskiem codegen`, `validate` and `dev` accept `.flow.ts`. Identical output to Node | `engine/flowcode`, `sdk/sdk.go`, `cmd/taskiem` |
| API and web app | `POST /v1/code/generate` and `/v1/code/compile` (sandboxed, SDK imports only, checked as publishing would); the editor's Code tab shows the workflow as code and applies edits | `api/code.go`, `web/src/pages/Editor.tsx` |
| Git | Per environment, GitHub or GitLab with a token or a GitHub App installation. Git-led: signed push webhooks queue a sync that checks every definition and runs every workflow test at the commit, and deploys only if all pass, in one transaction; versions record the commit, publishes are audited with it, and the workflows are read-only in Taskiem. Platform-led: publishing opens a pull or merge request with the definition and its code. Calls go through the egress guard; credentials are encrypted secrets | `engine/gitprovider` (with an in-memory fake GitHub and GitLab), `api/git.go`, migration 00015, Settings → Git, [guide](git.md) |
| Concurrent edits | A save names its parent version's digest. When another version landed since, the edits merge three ways by top-level field and top-level step (nested steps included): different parts merge, the same part changed twice is a conflict (409 listing each, with both sides). The editor shows a dialog to keep either side; `deploy` sends the version it compared with | `engine/wdmerge`, `api/workflows.go`, editor `MergeDialog`, `web/e2e/merge.spec.ts` |

Definitions are stored as `jsonb`, which reorders keys, so code generated from a stored version lists object keys in Postgres's order rather than the author's. Git sync will carry the author's file.

## Milestone 3: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Policies | Versioned approval policies: ordered rules (CEL over the subject) choosing sequential levels of role and count, step-up, constraints, timeout and escalation. Snapshotted into each run so decisions replay; publishing refuses a workflow naming a policy with no active version; Git-led repositories carry `policies/*.policy.json` | `engine/policy`, `engine/decide`, `engine/runtime/approvals.go`, `api/governance.go`, migration 00016, [guide](governance.md) |
| Approvals | Levels approved in order; distinct approvers across levels; maker-checker for the voter and anyone they act for; one voice per person per level | `engine/runtime/approvals.go` |
| Delegation | Time-boxed (at most 90 days), reasoned, revocable hand-over of approval roles; inbox shows whom you cover for; audited | `api/governance.go`, Approvals page |
| Step-up | TOTP (RFC 6238, verified against the RFC's vectors), enrolment under Account, single-use codes; asked for only after eligibility | `engine/totp`, Account page |
| Four-eyes | Owner settings: publishing needs a second publisher (publish requests), policy versions need a second person | `api/governance.go`, Settings, Approvals pages |
| Reports | Approvals by approver and amount band (with delegations, levels, step-up), failed and reconciled effects with operator resolutions, access to personal data, workflow changes with diffs, and the chain verified with its anchors; JSON or CSV, viewing audited | `api/reports.go`, Reports page, [compliance](compliance.md) |
| Anchoring | Daily Ed25519-signed chain heads, appended outside the database first and recorded append-only; `taskiem audit verify --anchors` catches a chain rewritten with every hash recomputed | `engine/audit/anchor.go`, migration 00017, `cmd/taskiem` |
| Retention | A tenant default for run data (a workflow's own setting still wins); crypto-shredding erasure was already in place | `engine/runtime/store.go`, Settings |
| PII detection | Conservative validators for Nigerian identifiers (phone, BVN, NIN, NUBAN by check digit or field name, Luhn-valid cards by scheme, email) seal undeclared personal data in step results and every payload before it is written; copies are sealed by taint. Code step logs, provider error messages and the process's own logs are masked | `engine/pii/detect.go`, `engine/runtime`, `cmd/taskiem/serve.go`, [privacy](privacy.md) |

Passkey step-up waits for passkey sign-in (milestone 5), which needs the same WebAuthn support.

## Milestone 4: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| WASM runtime | Tenants' own connectors: a connector/v1 manifest and a WebAssembly module (ABI `taskiem-connector/v1`) on wazero. A fresh instance per call, memory capped, the step's deadline, no files; HTTP only through the host, to the manifest's hosts, through the tenant's egress guard. The host, which sees every request, decides whether anything was sent, so a module cannot talk the engine into resending a write | `engine/wasmconn`, [ABI contract](contracts/connector-wasm-v1.md) |
| Tenant scoping | Connector lookups are per tenant everywhere (workers, run pins, PII sealing, validation, publishing, Git sync, webhooks, the palette): built-ins plus the tenant's own, whose ids start `x_` so they never shadow a built-in | `engine/connector` (`Registry.For`), migration 00018 |
| Go SDK | `sdk/connectorsdk`: handlers per action, `Do`/`DoJSON` with the built-ins' status classification, classified errors, native testing with `TestHTTP`; a complete example connector | `sdk/connectorsdk`, `examples/wasm-connector`, [guide](connector-sdk.md) |
| Upload | API (`connector.manage`, audited with the module's SHA-256; immutable versions; disable), CLI `connector build/check/push`, Connections → Your connectors | `api/tenantconnectors.go`, `cmd/taskiem`, `web/` |
| Contract drift | Every successful connector output is compared with the action's declared schema (types, enums, required fields, `$ref`s). Departures are recorded per tenant by connector version, action, path and kind (values never stored; an unexpected enum value only when short and not personal), counted in a metric, logged once, listed and acknowledged under Connections. Runs carry on with what the provider said | `engine/drift`, `engine/runtime/worker.go`, `api/drift.go`, migration 00019, [operations](operations.md#metrics) |

### Provider connectors: Flutterwave, Anchor, Lenco, Breet

The products use Flutterwave, Anchor, Lenco and Breet, so these come first, ahead of the spec's list (parts of which are out of date: Okra has shut down; Stitch serves South Africa; NIBSS needs a licensed partner). Each was built from the provider's own documentation, read 6 October 2026, with recorded fixtures and tests; the two hardest guarantees also run end to end through the engine (`e2e/providers_test.go`).

| Connector | API | Never twice | Webhooks | Guide |
| --- | --- | --- | --- | --- |
| `flutterwave@1` | v3 (stable; v4 is in beta) | Unique reference; a refused repeat is looked up by reference. A 503 is Flutterwave's timeout: an unknown outcome | `verif-hash` shared secret | [flutterwave.md](integrations/flutterwave.md) |
| `anchor@1` | v1 | Idempotency header and reference; a resend asks for the reference first, so the 24-hour replay window does not matter. Counterparty name checked before paying | base64 of hex HMAC-SHA1 | [anchor.md](integrations/anchor.md) |
| `lenco@1` | v1 (NGN) | Unique reference; a refused repeat is looked up by reference | HMAC-SHA512 keyed with SHA-256 of the token | [lenco.md](integrations/lenco.md) |
| `breet@1` | v1 | No idempotency key: withdrawals are reconcilable by `externalId`, never resent blind | `x-webhook-secret` shared secret | [breet.md](integrations/breet.md) |

Shared pieces: exact conversion between minor units and providers' decimal amounts (`connectors/internal/money`; an amount that cannot be read exactly is an error, never a zero), and new webhook schemes (`hmac_sha1`, `header_secret`, base64 and base64-of-hex encodings, SHA-256 key derivation) in the connector/v1 contract.

Open questions for each provider are listed at the end of its guide. The one that blocks a feature: **Breet's documentation gives bank withdrawal amounts both in local currency and in USD**, so `withdraw_to_bank` is refused until a connection records the unit Breet confirmed. Live checks need a sandbox account per provider.

### The rest of the library

Built on 6 October 2026 from each provider's public documentation, clean-room, with recorded-shape fixtures, tests and a guide listing what to confirm before go-live. Telegram was added at the product's request.

| Connector | Covers | Webhooks | Guide |
| --- | --- | --- | --- |
| `moniepoint@1` | Monnify (Moniepoint's developer API): transfers with status and name enquiry, reserved accounts, collections, balance; duplicate references looked up | HMAC-SHA512 | [moniepoint.md](integrations/moniepoint.md) |
| `interswitch@1` | Payouts with status, banks, name enquiry, Web Checkout status with an amount check | HMAC-SHA512 | [interswitch.md](integrations/interswitch.md) |
| `opay@1` | Cashier and bank-transfer collections, status, close, refunds; signed requests (payouts are not publicly documented) | In-body HMAC-SHA3-512, checked by the connector | [opay.md](integrations/opay.md) |
| `remita@1` | Funds transfer (single and bulk), invoices (RRR), banks, name enquiry | URL token, `Ok` reply, one event per payment | [remita.md](integrations/remita.md) |
| `mono@1` | Account linking and data, statements, identity, DirectPay, Direct Debit mandates (reconcilable) | Shared secret, 200 reply | [mono.md](integrations/mono.md) |
| `prembly@1` | IdentityPass KYC: BVN, NIN, phone, licence, passport, voter's card, CAC, bank account, face match, liveness | HMAC-SHA256 | [prembly.md](integrations/prembly.md) |
| `youverify@1` | KYC and KYB, address verification, AML screening | HMAC-SHA256, 200 reply | [youverify.md](integrations/youverify.md) |
| `africastalking@1` | SMS (bulk), fetch messages, airtime | URL token (Africa's Talking signs nothing) | [africastalking.md](integrations/africastalking.md) |
| `telegram@1` | Bot API: messages, documents, photos, edits, button answers, chat lookups, webhook registration | Secret-token header | [telegram.md](integrations/telegram.md) |
| `whatsapp@1` | Cloud API: text, templates, media, interactive messages, read receipts | HMAC-SHA256, GET verification, one event per batched item | [whatsapp.md](integrations/whatsapp.md) |
| `slack@1` | Messages, updates, ephemeral, reactions, channels, users by email, file uploads | Slack v0 signatures, URL verification; button clicks correlate on the clicked value | [slack.md](integrations/slack.md) |
| `gmail@1` | Send, search, read, labels, drafts; service account or OAuth refresh token | — | [gmail.md](integrations/gmail.md) |
| `googlesheets@1` | Read, append, update, clear, create, add sheet | — | [googlesheets.md](integrations/googlesheets.md) |
| `mysql@1` | Read-only queries and writes, typed parameters; MySQL's protocol written from Oracle's docs (the common Go driver is MPL-2.0) | — | [mysql.md](integrations/mysql.md) |
| `s3@1` | S3 and compatible stores (R2, MinIO, Spaces, Wasabi): objects, listing, copy, presigned URLs; SigV4 written in-house | — | [s3.md](integrations/s3.md) |
| `sftp@1` | Upload (atomic), download, list, stat, rename, delete, mkdir; host keys pinned before any credential is sent | — | [sftp.md](integrations/sftp.md) |

Engine features they needed, all in the connector/v1 contract: `slack_v0`, `query_secret`, `basic` and `connector` verify schemes; trigger `handshake` (Meta's GET challenge, Slack's URL verification), `split` (one event per item of a batched delivery) and `ack` (the reply a provider expects); form-encoded deliveries as fields; `parseJSON` in trigger expressions; and personal data declared in outputs (`output.<path>`), sealed when a step's result is written. One library added: `github.com/pkg/sftp` (BSD-2-Clause). Every open question and account is in [What needs people](needs-people.md#providers-phase-2-milestone-4-gate-g2).

## Milestone 5: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Custom roles | Named permission sets; no one creates, widens, grants or removes a role carrying a permission they lack | `api/roles.go`, migration 00020, [governance](governance.md#custom-roles) |
| Passkeys | WebAuthn implemented from the specification: sign-in, approval step-up (also satisfies `totp`), administrators held to passkeys once a public URL is set, resets audited | `engine/webauthn`, `api/passkeys.go`, migration 00021, [governance](governance.md#passkeys) |
| Single sign-on | OIDC (code flow with PKCE) and SAML 2.0 (SP-initiated); DNS-verified domains, JIT members, group-to-role mapping, enforcement with an owner break-glass | `engine/oidc`, `engine/saml`, `api/sso.go`, migration 00022, [governance](governance.md#single-sign-on) |
| Staging | Per-environment deployments: publishing reaches ungated environments; gated ones (prod on staging) take a version only by promotion, held to four-eyes; `taskiem promote` | `api/environments.go`, migration 00024, [guide](environments.md) |
| Alerts | Rules for failed, slow and reconciling runs, stuck approvals, drift, expiring credentials and audit anchors; email (SMTP), Slack and HMAC-signed webhooks with retries; anchors emailed outside the platform | `engine/alerts`, `api/alerts.go`, migration 00025, [guide](alerts.md) |
| Dashboards | Success rate, p50/p95 duration, runs per day, failing steps and connectors, runs needing reconciliation, approvals pending, per workflow; by environment and period | `api/dashboard.go`, `web/src/pages/Dashboard.tsx`, [guide](dashboard.md) |
| Live run view | Run history streamed as server-sent events (resumable, sealed); the run page's canvas lights each step as it goes | `api/stream.go`, `web/src/canvas/RunCanvas.tsx`, [guide](dashboard.md#live-run-view) |
| Python sandbox | CPython 3.12 for WASI on wazero, a fresh instance per run: `main(input, host)` with fetch (egress-guarded), declared secrets, logical time; standard library only; time, memory and output limits | `engine/sandbox/python.go`, `engine/sandbox/pywasm`, [guide](code-steps.md) |
| SCIM 2.0 | Users and Groups for Okta, Entra ID and others; administrators map groups to roles; deactivation removes access and signs out; owners are never deprovisioned | `api/scim.go`, migration 00023, [governance](governance.md#provisioning-scim-20) |


## Carried over from Phase 1

Kept pending while Phase 2 is built. Phase 1's milestones 4 and 5 and gate G1 stay open until these are done ([Phase 1 status](phase-1-status.md)):

1. Deploy Taskiem with a public URL and enter production iswallet credentials (Connections and Secrets pages).
2. Generate webhook URLs and signing secrets; register them with iswallet and Payrolla.
3. Payrolla implements the [integration guide](integrations/payrolla.md).
4. iSpend builds `POST /internal/wallet-credit-exceptions`, honouring `Idempotency-Key`.
5. Load and failover test on the target hardware; internal security review.
