# PGDock integration plan

Status: **planned**. This plan runs in parallel with Taskiem's own phases. It never blocks them, and they never wait for it.

Source: *PGDock × Taskiem: How the Two Platforms Work Together* (7 October 2026, @EaziDeFi), called **the joint doc** below. This plan turns the joint doc into tracked work. It records:

- what each side has to build;
- what has to change in the joint doc itself;
- every open question.

It was checked against both codebases on 7 October 2026: Taskiem at `claude/friendly-cannon-0ak3hh`, and PGDock's published contracts (`docs/webhooks.md`, `docs/cli.md` and `api/openapi.yaml` in `ISRAEL-DUFF/PgDock`).

Taskiem builds only from PGDock's **published** contracts: its docs, its OpenAPI file, and changelogs. This is the same rule as for any provider (the [clean-room policy](clean-room-policy.md) applies in spirit). If an integration needs something PGDock does not publish, PGDock publishes it first. That is the joint doc's "third-party test".

## 1. Principles adopted

These are the joint doc's integration principles, adopted as written. Decision [0018](decisions/0018-pgdock-integration-principles.md) records them.

1. **Each product stands alone.** Neither product requires the other to work.
2. **Public surfaces only.** No shared tables, queues, service accounts or private endpoints.
3. **The third-party test.** Anything Taskiem does with PGDock, an outside tool must be able to do too.
4. **Separate tenancy and security.** Each product keeps its own organisations, users and secrets. Linking accounts is an explicit, scoped, revocable consent.
5. **Versioned contracts.** PGDock's deprecation policy applies to every PGDock API this plan depends on. Each team runs the other's integration tests in CI.
6. **Independent outages.** An outage in one product degrades the integration but never takes the other down.

## 2. What exists today

### PGDock (from its published contracts)

| Capability | What PGDock publishes | Use in this plan |
| --- | --- | --- |
| Transactional webhooks | Outbox written in the same transaction as the change: a rolled-back change sends nothing, a committed one always sends. Ordered per webhook. At-least-once delivery. Retries for 24 h, then dead letters kept 30 days. Paused after 50 failures in a row. Delivery rate capped per plan (Personal 60/min, Team 300/min). | The row-changed trigger (P1) |
| Webhook payload | `id`, `webhook`, `project`, `table`, `type` (`INSERT`, `UPDATE`, `DELETE`, `TEST`), `record`, `old_record`, `committed_at`. Above 256 KB, `truncated: true` and `primary_key` are sent instead of the records. | Trigger output schema |
| Webhook signing | `PGDock-Signature: t=<unix>,v1=<hex HMAC-SHA256 of "t.body">`, 5-minute window. `PGDock-Event-Id` for de-duplication. `PGDock-Webhook` names the webhook. Optional static headers. | Verify and dedup |
| Webhook management API | `POST/GET /api/v1/projects/{id}/webhooks`, `GET/PATCH/DELETE …/{webhook_id}`, plus `…/test`, `…/rotate-secret`, `…/deliveries` and `…/replay` | Creating and deleting the webhook behind a trigger |
| API tokens | Belong to a person and act in one organisation. Scopes `read`, `write`, `admin`. Can be restricted to some projects. Expire after 90 days by default, a year at most. Never do more than the person can, and stop working when the person leaves or the organisation is suspended. | The connection credential (P1) |
| Device login (CLI) | `/api/v1/auth/device/token` | Not the consent flow P2 needs |
| SQL endpoint | `POST /api/v1/projects/{id}/sql`. Runs as the project's role, with a statement timeout and at most 1,000 rows. `read_only` flag; several statements allowed when not read-only. **No bound parameters.** SQL errors come back with status 200 in `error`. | Stopgap reads only (see P1-T4) |
| Rows endpoint | `GET /api/v1/projects/{id}/tables/{schema}/{table}/rows`. Structured `filter` (eq, neq, lt, lte, gt, gte, contains, is_null, not_null, in), `order`, keyset pagination, up to 1,000 rows. **Read only.** | Query rows (P1), and fetching a truncated event's row |
| Scheduled jobs | SQL or a signed HTTP call on a cron, signed with `PGDock-Job`. Missed runs are not caught up. | A job can trigger a Taskiem workflow through a Taskiem webhook trigger |
| Errors | `{code, message}` | Error mapping (P1) |
| V3 and V4 | Billing (V3 §3), message providers, data API, auth and storage (V4). **Not published in the repository.** | P2–P4 depend on these |

### Taskiem

| Capability | Status | Use in this plan |
| --- | --- | --- |
| Connector framework (connector/v1) | Built: manifests, action classes, idempotency, reconcile, triggers with verify schemes, dedup, PII declarations by path | The `pgdock@1` connector |
| `hmac_sha256_timestamped` verify scheme | Built | PGDock signatures, if the header format matches (P1-T2) |
| Encrypted credential vault, with each use audited | Built | PGDock tokens |
| Outbound traffic guard | Built | Calls to PGDock |
| Plan limits and billing | Built (Phase 4) | Combined plan (P4) |
| A connector registering its own webhook with a provider at publish, and removing it at unpublish | **Not built.** Triggers are registered inside Taskiem; providers are configured by hand today. | Needed by P1-T1 |
| A generic OAuth authorization-code connection flow | **Not built.** Google uses pasted refresh tokens or service accounts. | Needed by P2 |
| `database_change` trigger type | Reserved in the wd/v1 schema, not implemented. Spec 8 describes it as logical replication or `LISTEN` on a customer database. | Decide how it relates to PGDock's trigger (Q11) |
| Data tables, forms | **Do not exist** in Taskiem's spec or code | See correction C1 |

## 3. Corrections to the joint doc

The joint doc should be updated as follows. Each correction has an owner and a status.

| # | Joint doc says | Correction | Owner | Status |
| --- | --- | --- | --- | --- |
| C1 | "The data tables feature in Taskiem's V2 spec can be PGDock projects" | Taskiem has no data tables feature, and no "V2 spec": it has one architecture spec in phases. Data tables would be a **new Taskiem feature**. Reword as "Taskiem could add data tables backed by PGDock projects" and link Q9. | Joint doc author | Open |
| C2 | "Taskiem's forms and white-label embedding need end-user sign-in. PGDock auth (V4) provides it" | Embedding is built and needs no end-user sign-in: the partner signs its users in and mints short-lived Taskiem tokens server-side ([embedding](embedding.md)). Taskiem has no public forms feature; its only forms are WhatsApp Flows and canvas input forms. Reword so PGDock auth applies only to a **future** public-forms feature (Q10), and drop the claim for embedding. | Joint doc author | Open |
| C3 | "Both bill in naira through Flutterwave and iSpend" | Taskiem's billing, built in Phase 4, uses **Paystack and Flutterwave** ([billing](billing.md)). iSpend appears in Taskiem only as a dogfood workflow integration. Correct the table and the shared-payment-module row, and link Q3. | Joint doc author | Open |
| C4 | "Shared building blocks … built once and reused" (organisations and roles, metering and price books, payment provider, message provider, SSRF-safe delivery) | Taskiem has already built each of these: RBAC with custom roles, `tenant_limits` and plans, `billing.PaymentProvider`, the WhatsApp and SMS layer with its outbox, and the egress guard. The real choice is whether PGDock adopts Taskiem's designs, Taskiem adopts PGDock's, or they only align contracts (Q1). Reword from "build once" to "align or adopt", and list what exists on each side. | Joint doc author | Open |
| C5 | "WHT handling" is a benefit of the shared payment interface | Taskiem's billing has no withholding-tax handling. Either list it as a gap for Taskiem (Q17) or drop it from the benefits. | Joint doc author | Open |
| C6 | "A paused Free-tier PGDock project returns 'resuming'" | PGDock's published contracts have no paused or resuming state for projects; there are suspended organisations and paused webhooks. Confirm it is a V3 feature and its exact error code (Q13), or remove it. | PGDock team | Open |
| C7 | "Taking Taskiem offline for an hour loses no events" (done-when) | This holds because PGDock retries for 24 h and keeps dead letters for 30 days, not because of Taskiem. Restate it as: "within PGDock's 24 h retry window, nothing is lost; beyond it, dead letters can be replayed for 30 days". Also note that at Personal-plan delivery rates (60/min) a backlog drains slowly. | Joint doc author | Open |
| C8 | "PGDock delivers signed events in commit order" (as a property of the trigger) | PGDock delivers in order, but Taskiem starts one run per event and runs them concurrently, so **runs do not complete in commit order**. Add a note, and link Q12 for workflows that need ordering, which would use a per-key concurrency setting. | Joint doc author | Open |
| C9 | Stopgap actions "through a parameterised SQL call through the management API" | The published SQL endpoint has **no bound parameters**, allows several statements, and returns SQL errors with status 200. Taskiem will not build write actions by putting values into SQL text. The stopgap needs PGDock to add a parameterised endpoint (P1-G1) or row-write endpoints. Until then P1 ships reads only (query rows through the rows endpoint), plus the trigger. | Joint doc author, PGDock team | Open |
| C10 | "Writes carry an idempotency key … so Taskiem's retries never write twice" | PGDock has no idempotency-key mechanism to carry one. Options: PGDock honours an `Idempotency-Key` header on writes (P1-G2); or Taskiem uses natural keys (upsert on a conflict column is idempotent, a plain insert is not). Until then, insert is an `unsafe_write`, so an uncertain outcome is held for a person rather than retried. | PGDock team | Open |
| C11 | Execution history on PGDock dedicated instances | Taskiem's own database needs things a managed service may not allow: creating roles, roles that own `SECURITY DEFINER` functions, forced row-level security, `LISTEN/NOTIFY`, partitioning, and a migration user that owns the schema. Restate this as a feasibility check (Q5, P4-T6) rather than a capability. | Both | Open |

## 4. Phases and work items

These are the joint doc's four phases, with each item's owner, dependency and status. Item IDs are used in commits and in [What needs people](needs-people.md#pgdock-integration).

### Phase 1 — Connector on today's PGDock APIs (can start now)

**Taskiem team**

| ID | Item | Detail | Depends on | Status |
| --- | --- | --- | --- | --- |
| P1-T1 | **Remote trigger registration** (engine) | Connector triggers gain an optional lifecycle. On publish, the connector calls the provider to create the subscription (here, a PGDock webhook pointed at Taskiem's ingest URL) and stores the returned secret in the vault. On unpublish or redeploy it updates or deletes the subscription. The connection's status shows a broken or paused remote subscription. The lifecycle is generic, so Telegram, WhatsApp and the mobile-money callbacks can use it later. | — | Planned |
| P1-T2 | `pgdock@1` connection | API token, plus organisation and project (the token should be project-restricted). Scopes: `read` for the trigger and query, `write` for the webhook API and writes. A connection test that names the token's organisation and projects. Base URL per connection (self-hosted PGDock) or the platform default. | — | Planned |
| P1-T3 | Row-changed trigger | Table(s), events and optional changed columns, registered through P1-T1. Signature checked with `hmac_sha256_timestamped` against `PGDock-Signature`, adding a format if the existing one differs. De-duplicated on `PGDock-Event-Id`. `TEST` events delivered as tests. Truncated events refetched through the rows endpoint by `primary_key` before the run starts, or passed as truncated (Q14). PII declared by path when a table's columns are marked. | P1-T1, P1-T2 | Planned |
| P1-T4 | Query rows (read) | Through the rows endpoint: structured filters, order, limit (≤ 1,000), keyset pagination, as a `read` action. Never through the SQL endpoint with values in the text. | P1-T2 | Planned |
| P1-T5 | Write actions (insert, upsert, update, delete with a required filter and a maximum-affected guard; call a function) | Built only on a parameterised endpoint (P1-G1). Insert is `unsafe_write` until P1-G2; upsert on a conflict column is `idempotent_write`; update and delete need a filter and refuse when more rows than the guard would change. | P1-G1 (P1-G2 for idempotent insert) | Blocked |
| P1-T6 | Error mapping | Map PGDock `{code}` values to Taskiem's classes (validation, permission, conflict, rate limit, temporarily unavailable). Map SQL errors returned with status 200 by SQLSTATE: `23505` conflict, `40001` and `40P01` retryable, `57014` timeout, `42501` permission. Refusals with a `Retry-After` are retryable. | P1-G4 (error code list) | Planned |
| P1-T7 | Fake PGDock server and fixtures | Built from the published payloads and OpenAPI file: signing, ordering, retries, truncation, test events, the webhook API, the rows endpoint, errors. | — | Planned |
| P1-T8 | End-to-end acceptance test | The joint doc's "done when": a new `orders` row starts a workflow that sends a WhatsApp message (fake Graph API) and writes a status back to the row (needs P1-T5); a rolled-back insert starts nothing; deliveries retried after Taskiem was unavailable are taken once. | P1-T3, P1-T5 | Planned (write-back blocked on P1-G1) |
| P1-T9 | Docs and SDK | `docs/integrations/pgdock.md`, SDK helpers, a template ("new row → WhatsApp message"), and an entry in the evaluation suite | P1-T3 | Planned |

**PGDock team**

| ID | Item | Detail | Status |
| --- | --- | --- | --- |
| P1-G1 | Parameterised, single-statement SQL endpoint, or row-write endpoints | Bound parameters; one statement per call; a `max_rows_affected` guard; errors with a non-200 status or a stable error code. Without it, Taskiem ships no write actions (C9). | Needed |
| P1-G2 | `Idempotency-Key` on write endpoints | Replays within a window return the first answer (C10) | Wanted |
| P1-G3 | Webhook management API contract frozen | Field names, the secret returned once at creation, behaviour of `PATCH`, errors. Published as a versioned contract that Taskiem's tests run against. | Needed |
| P1-G4 | Error code list | Every `code` the webhook, rows and SQL endpoints can return, and which are retryable | Needed |
| P1-G5 | Test fixtures and a sandbox organisation | Signed sample payloads (including truncated and `TEST`), plus a sandbox for Taskiem's CI integration tests | Wanted |
| P1-G6 | Webhook delivery rates | Confirm whether a Taskiem-created webhook counts against the customer's per-plan delivery rate (Q15) | Question |

### Phase 2 — Account linking and shared modules (can run in parallel with phase 1)

| ID | Item | Owner | Detail | Status |
| --- | --- | --- | --- | --- |
| P2-G1 | OAuth-style authorization flow | PGDock | Authorization code with PKCE. The customer picks the organisation, projects and scopes. Issues a project-restricted token. Revocable from PGDock, with an audit entry on both sides. | Needed |
| P2-T1 | Generic OAuth connection flow | Taskiem | Authorization-code connections for any connector (PGDock first, Google next): redirect, state and PKCE, token storage and refresh in the vault, revocation, and the connect screen | Planned |
| P2-T2 | Revocation in both directions | Both | Revoking in either product stops the connection. Taskiem marks the connection broken when PGDock answers that the token was revoked. | Planned |
| P2-T3 | Shared payment module | PGDock owns, per the joint doc | Depends on Q1 and C3–C5: Taskiem's `billing.PaymentProvider` already exists | Decision needed |
| P2-T4 | Shared message module | Taskiem owns, per the joint doc | Taskiem's WhatsApp layer, outbox and templates exist. Decide whether PGDock calls Taskiem's public API, imports a versioned module, or reuses the design (Q1, Q16). | Decision needed |

### Phase 3 — Data API (waits for PGDock V4 data API)

| ID | Item | Owner | Detail | Status |
| --- | --- | --- | --- | --- |
| P3-G1 | Data API and SDK | PGDock | V4 data API: query, insert, upsert, update, delete, RPC | PGDock roadmap |
| P3-T1 | Rebuild actions on the data API | Taskiem | Retire the P1 stopgap actions; the connector's major version stays if the action contracts hold | Waiting |
| P3-T2 | **Data tables (new feature)** | Taskiem | Write a spec section first: what a data table is in a workflow, its permissions, PII, retention, and whether PGDock is the only backend (Q9). Then the table view and lifecycle on PGDock projects. | Spec needed |
| P3-G2 | Project creation by token, with quotas | PGDock | So data tables can create projects on the customer's behalf, within the customer's plan | PGDock roadmap |

### Phase 4 — Storage, auth, billing and operations (waits for PGDock V4)

| ID | Item | Owner | Detail | Status |
| --- | --- | --- | --- | --- |
| P4-T1 | Files on PGDock storage | Taskiem | A storage connector (buckets, signed URLs) and, if wanted, PGDock as the backend for workflow files (Q18) | Waiting |
| P4-T2 | Auth for public forms | Taskiem | Only if a public-forms feature is specified (C2, Q10) | Spec needed |
| P4-T3 | Combined plan and invoice | Both | Taskiem's plans config and billing can carry a bundle discount. Decide which product issues the invoice and how a combined subscription moves between the two (Q3, Q4). | Decision needed |
| P4-T4 | PGDock operations workflows | Taskiem builds and hosts; PGDock defines processes and events | Dunning, onboarding, withholding-tax credit-note chasing, and capacity, backup and incident alerts, built as workflows in a Taskiem tenant for PGDock. These are Taskiem's first external money-handling reference workflows. | Waiting on PGDock events |
| P4-T5 | Integration tests in each other's CI | Both | Each team runs the other's contract tests on every change to a contract the other depends on | Planned |
| P4-T6 | Execution store feasibility | Both | Check Taskiem's database requirements against PGDock dedicated instances (C11, Q5) before any decision | Planned |

## 5. Open questions

The joint doc's five questions come first (Q1–Q5). The rest came up while checking both codebases.

| # | Question | Who decides | Blocks |
| --- | --- | --- | --- |
| Q1 | Shared library or shared design for common building blocks? Given C4: does PGDock adopt Taskiem's modules, Taskiem PGDock's, or do they only align contracts? Who owns each module, and how are versions managed? | Both founders / tech leads | P2-T3, P2-T4 |
| Q2 | Single sign-on across both products, or only linked accounts with explicit consent? | Product | P2-G1, P2-T1 |
| Q3 | Bundle pricing: what discount, and which product's billing issues the bundle invoice? Taskiem's billing uses Paystack and Flutterwave (C3). | Leadership / finance | P4-T3 |
| Q4 | Data tables default: always PGDock, or PGDock as one backend among others? | Product | P3-T2 |
| Q5 | Execution store: should Taskiem run its own database on PGDock dedicated instances? This first needs the feasibility check (C11, P4-T6). | Both tech leads | P4-T6 |
| Q6 | Is the joint doc's data-tables idea wanted at all, given Taskiem has no such feature (C1)? If yes, who writes its spec section? | Product | P3-T2 |
| Q7 | Will PGDock add a parameterised SQL endpoint or row-write endpoints before the V4 data API (P1-G1)? Without one, P1 ships no write actions. | PGDock | P1-T5, P1-T8 write-back |
| Q8 | Will PGDock honour an `Idempotency-Key` on writes (P1-G2), or should Taskiem rely on upserts and treat inserts as unsafe? | PGDock | P1-T5 classes |
| Q9 | For data tables, does a table map to one PGDock project per tenant, one per workflow, or a schema in a shared project? Who pays for the project? | Product, both teams | P3-T2 |
| Q10 | Does Taskiem want a public forms feature (C2)? If yes, does it use PGDock auth, Taskiem's own end-user tokens, or WhatsApp OTP, which Taskiem already has? | Product | P4-T2 |
| Q11 | Should the PGDock trigger implement wd/v1's reserved `database_change` trigger type, or stay a connector trigger (`connector_event`), with `database_change` kept for direct logical replication on any Postgres? | Taskiem | P1-T3 shape |
| Q12 | Ordering: do some customer workflows need events processed in commit order? If yes, use `settings.concurrency_key` per table and primary key, or a strictly serial option (C8). | Product | P1-T3 docs |
| Q13 | Does PGDock have, or plan, a free-tier paused project with a "resuming" answer? Which error code and `Retry-After` (C6)? | PGDock | P1-T6 |
| Q14 | Truncated events (over 256 KB): should Taskiem refetch the row before starting the run (one more API call, and the row may have changed since) or pass the event as truncated and let the workflow fetch it? | Taskiem, with PGDock input | P1-T3 |
| Q15 | Do deliveries from Taskiem-created webhooks count against the customer's PGDock delivery-rate limit? Should Taskiem show that limit? | PGDock | P1-G6 |
| Q16 | For the shared message module: should PGDock's notifications (dunning, alerts) go through Taskiem's WhatsApp and SMS layer as a customer of Taskiem's public API, through a shared module, or separately? | Both | P2-T4, P4-T4 |
| Q17 | Should Taskiem's billing handle withholding tax (C5)? | Finance | Taskiem billing |
| Q18 | Should workflow files (attachments, generated documents, container-step outputs) move to PGDock storage, or only be reachable through a storage connector? | Product | P4-T1 |
| Q19 | Where does a customer manage a linked connection: in both products, or one canonical place with the other read-only? | Product | P2-T1, P2-T2 |
| Q20 | Self-hosted PGDock: should the connector support any PGDock server URL, or only PGDock's cloud? | Product | P1-T2 |
| Q21 | Which PGDock contracts count as "public" for the deprecation policy, given only the V2 docs and OpenAPI file are published (V3 and V4 are not in the repository)? | PGDock | All phases |

## 6. Updates this plan needs elsewhere

### In Taskiem

| Where | Update | Status |
| --- | --- | --- |
| `docs/decisions/0018-pgdock-integration-principles.md` | The principles in section 1 | Done with this plan |
| `docs/needs-people.md` | A "PGDock integration" section pointing to Q1–Q21 and the PGDock-side items | Done with this plan |
| `docs/phase-4-status.md` | A line saying the PGDock integration runs as a parallel plan, linking here | Done with this plan |
| `docs/spec/architecture.md` | If Q6 or Q10 is yes, a new section for data tables or public forms; if Q11 is answered, a note on the `database_change` trigger | After decisions |
| `docs/contracts/connector-v1.md` | The trigger lifecycle from P1-T1 (remote registration) | With P1-T1 |
| `docs/security/threat-model.md` | A boundary for PGDock ↔ Taskiem: tokens, webhook signatures, the scope of linked accounts, cross-product audit | With P1 |
| `docs/integrations/pgdock.md` | Connector guide | With P1 |

### In PGDock

| Where | Update |
| --- | --- |
| `docs/webhooks.md` | Note that integrators (Taskiem) create webhooks through the API with project-restricted tokens, and how deliveries count against rate limits (Q15) |
| `api/openapi.yaml` | A parameterised SQL endpoint or row-write endpoints (P1-G1); `Idempotency-Key` (P1-G2); the error code list (P1-G4) |
| Authorization | The OAuth-style consent flow (P2-G1) |
| CI | Run Taskiem's `pgdock@1` contract tests (P4-T5) |

### In the joint doc

Apply corrections C1–C11. Replace its open-questions section with a link to Q1–Q21 here, or copy them.

## 7. Risks

| Risk | Mitigation |
| --- | --- |
| Tight coupling creeps in (from the joint doc) | Public surfaces only; the third-party test in design reviews; contract tests in both CIs |
| One product's outage cascades (from the joint doc) | PGDock retries for 24 h; Taskiem's ingest deduplicates and queues; neither product calls the other on its critical path |
| Cross-product data exposure (from the joint doc) | Project-restricted tokens with the smallest scopes; consent; revocation from either side; audit entries in both |
| Roadmap dependency on PGDock V4 (from the joint doc) | Phase 1 ships the trigger and reads on today's APIs; writes wait for P1-G1 rather than for V4 |
| Bundle confusion and hidden coupling through shared code (from the joint doc) | Each product priced alone; shared modules versioned with owners (Q1) |
| **Unsafe SQL stopgap** (new) | No values put into SQL text, ever. Writes wait for bound parameters (C9). |
| **Duplicate writes without idempotency keys** (new) | Inserts are `unsafe_write` until P1-G2; upserts preferred (C10) |
| **Ordering assumptions** (new) | Documented (C8), with a per-key concurrency setting where order matters (Q12) |
| **Unpublished V3 and V4 contracts** (new) | Taskiem builds nothing on them until they are published (Q21) |
| **Remote webhook drift**: a customer deletes or pauses the PGDock webhook (new) | P1-T1 shows a broken or paused remote subscription on the connection and on the workflow; republishing reinstalls it |
