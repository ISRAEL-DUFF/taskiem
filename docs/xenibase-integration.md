# Xenibase integration plan

Status: **phases 1 to 3 done, phase 4 open** (17 October 2026). Xenibase was called PGDock when this plan was written (7 October 2026); its connector id changed from `pgdock@1` to `xenibase@1` with the rename. The trigger, reads, remote registration and, on Xenibase's V4 data API, the write actions are built ([Xenibase connector](integrations/xenibase.md)). Xenibase answered the asks below in `docs/integrations/taskiem-reply.md` of its repository and in its decisions log ("Taskiem integration asks"); the data tables feature is designed in [decision 0033](decisions/0033-data-tables-on-xenibase.md) and under way. This plan runs in parallel with Taskiem's own phases. It never blocks them, and they never wait for it.

Source: *Xenibase × Taskiem: How the Two Platforms Work Together* (7 October 2026, @EaziDeFi), called **the joint doc** below. This plan turns the joint doc into tracked work. It records:

- what each side has to build;
- what has to change in the joint doc itself;
- every open question.

It was checked against both codebases on 7 October 2026: Taskiem at `claude/friendly-cannon-0ak3hh`, and Xenibase's published contracts (`docs/webhooks.md`, `docs/cli.md` and `api/openapi.yaml` in `ISRAEL-DUFF/Xenibase`).

Taskiem builds only from Xenibase's **published** contracts: its docs, its OpenAPI file, and changelogs. This is the same rule as for any provider (the [clean-room policy](clean-room-policy.md) applies in spirit). If an integration needs something Xenibase does not publish, Xenibase publishes it first. That is the joint doc's "third-party test".

## 1. Principles adopted

These are the joint doc's integration principles, adopted as written. Decision [0018](decisions/0018-pgdock-integration-principles.md) records them.

1. **Each product stands alone.** Neither product requires the other to work.
2. **Public surfaces only.** No shared tables, queues, service accounts or private endpoints.
3. **The third-party test.** Anything Taskiem does with Xenibase, an outside tool must be able to do too.
4. **Separate tenancy and security.** Each product keeps its own organisations, users and secrets. Linking accounts is an explicit, scoped, revocable consent.
5. **Versioned contracts.** Xenibase's deprecation policy applies to every Xenibase API this plan depends on. Each team runs the other's integration tests in CI.
6. **Independent outages.** An outage in one product degrades the integration but never takes the other down.

## 2. What exists today

### Xenibase (from its published contracts)

| Capability | What Xenibase publishes | Use in this plan |
| --- | --- | --- |
| Transactional webhooks | Outbox written in the same transaction as the change: a rolled-back change sends nothing, a committed one always sends. Ordered per webhook. At-least-once delivery. Retries for 24 h, then dead letters kept 30 days. Paused after 50 failures in a row. Delivery rate capped per plan (Personal 60/min, Team 300/min). | The row-changed trigger (P1) |
| Webhook payload | `id`, `webhook`, `project`, `table`, `type` (`INSERT`, `UPDATE`, `DELETE`, `TEST`), `record`, `old_record`, `committed_at`. Above 256 KB, `truncated: true` and `primary_key` are sent instead of the records. | Trigger output schema |
| Webhook signing | `Xenibase-Signature: t=<unix>,v1=<hex HMAC-SHA256 of "t.body">`, 5-minute window. `Xenibase-Event-Id` for de-duplication. `Xenibase-Webhook` names the webhook. Optional static headers. | Verify and dedup |
| Webhook management API | `POST/GET /api/v1/projects/{id}/webhooks`, `GET/PATCH/DELETE …/{webhook_id}`, plus `…/test`, `…/rotate-secret`, `…/deliveries` and `…/replay` | Creating and deleting the webhook behind a trigger |
| API tokens | Belong to a person and act in one organisation. Scopes `read`, `write`, `admin`. Can be restricted to some projects. Expire after 90 days by default, a year at most. Never do more than the person can, and stop working when the person leaves or the organisation is suspended. | The connection credential (P1) |
| Device login (CLI) | `/api/v1/auth/device/token` | Not the consent flow P2 needs |
| SQL endpoint | `POST /api/v1/projects/{id}/sql`. Runs as the project's role, with a statement timeout and at most 1,000 rows. `read_only` flag; several statements allowed when not read-only. **No bound parameters.** SQL errors come back with status 200 in `error`. | Stopgap reads only (see P1-T4) |
| Rows endpoint | `GET /api/v1/projects/{id}/tables/{schema}/{table}/rows`. Structured `filter` (eq, neq, lt, lte, gt, gte, contains, is_null, not_null, in), `order`, keyset pagination, up to 1,000 rows. **Read only.** | Query rows (P1), and fetching a truncated event's row |
| Scheduled jobs | SQL or a signed HTTP call on a cron, signed with `Xenibase-Job`. Missed runs are not caught up. | A job can trigger a Taskiem workflow through a Taskiem webhook trigger |
| Errors | `{code, message}` | Error mapping (P1) |
| V3 and V4 | Billing (V3 §3), message providers, data API, auth and storage (V4). **Not published in the repository.** | P2–P4 depend on these |

### Taskiem

| Capability | Status | Use in this plan |
| --- | --- | --- |
| Connector framework (connector/v1) | Built: manifests, action classes, idempotency, reconcile, triggers with verify schemes, dedup, PII declarations by path | The `xenibase@1` connector |
| `hmac_sha256_timestamped` verify scheme | Built, with `signature_format: t_v1` for Xenibase's `t=<unix>,v1=<hex>` header (several `v1` values accepted) | Xenibase signatures (P1-T3) |
| Encrypted credential vault, with each use audited | Built | Xenibase tokens |
| Outbound traffic guard | Built | Calls to Xenibase |
| Plan limits and billing | Built (Phase 4) | Combined plan (P4) |
| A connector registering its own webhook with a provider at publish, and removing it at unpublish | **Built** (P1-T1, [decision 0021](decisions/0021-remote-trigger-registration.md)): `registration: remote` triggers, intent recorded at deploy, a reconciler, health on the connection and workflow, `DELETE /v1/workflows/{wf}/deployments/{env}` | The `xenibase@1` row-changed trigger |
| A generic OAuth authorization-code connection flow | **Not built.** Google uses pasted refresh tokens or service accounts. | Needed by P2 |
| `database_change` trigger type | Reserved in the wd/v1 schema, not implemented. Spec 8 describes it as logical replication or `LISTEN` on a customer database. | Kept for that; the Xenibase trigger is a `connector_event` (Q11, default taken) |
| Data tables, forms | **Do not exist** in Taskiem's spec or code | See correction C1 |

## 3. Corrections to the joint doc

The joint doc should be updated as follows. Each correction has an owner and a status.

| # | Joint doc says | Correction | Owner | Status |
| --- | --- | --- | --- | --- |
| C1 | "The data tables feature in Taskiem's V2 spec can be Xenibase projects" | Taskiem has no data tables feature, and no "V2 spec": it has one architecture spec in phases. Data tables would be a **new Taskiem feature**. Reword as "Taskiem could add data tables backed by Xenibase projects" and link Q9. | Joint doc author | Open |
| C2 | "Taskiem's forms and white-label embedding need end-user sign-in. Xenibase auth (V4) provides it" | Embedding is built and needs no end-user sign-in: the partner signs its users in and mints short-lived Taskiem tokens server-side ([embedding](embedding.md)). Taskiem has no public forms feature; its only forms are WhatsApp Flows and canvas input forms. Reword so Xenibase auth applies only to a **future** public-forms feature (Q10), and drop the claim for embedding. | Joint doc author | Open |
| C3 | "Both bill in naira through Flutterwave and iSpend" | Taskiem's billing, built in Phase 4, uses **Paystack and Flutterwave** ([billing](billing.md)). iSpend appears in Taskiem only as a dogfood workflow integration. Correct the table and the shared-payment-module row, and link Q3. | Joint doc author | Open |
| C4 | "Shared building blocks … built once and reused" (organisations and roles, metering and price books, payment provider, message provider, SSRF-safe delivery) | Taskiem has already built each of these: RBAC with custom roles, `tenant_limits` and plans, `billing.PaymentProvider`, the WhatsApp and SMS layer with its outbox, and the egress guard. The real choice is whether Xenibase adopts Taskiem's designs, Taskiem adopts Xenibase's, or they only align contracts (Q1). Reword from "build once" to "align or adopt", and list what exists on each side. | Joint doc author | Open |
| C5 | "WHT handling" is a benefit of the shared payment interface | Taskiem's billing has no withholding-tax handling. Either list it as a gap for Taskiem (Q17) or drop it from the benefits. | Joint doc author | Open |
| C6 | "A paused Free-tier Xenibase project returns 'resuming'" | Xenibase's published contracts have no paused or resuming state for projects; there are suspended organisations and paused webhooks. Confirm it is a V3 feature and its exact error code (Q13), or remove it. | Xenibase team | Open |
| C7 | "Taking Taskiem offline for an hour loses no events" (done-when) | This holds because Xenibase retries for 24 h and keeps dead letters for 30 days, not because of Taskiem. Restate it as: "within Xenibase's 24 h retry window, nothing is lost; beyond it, dead letters can be replayed for 30 days". Also note that at Personal-plan delivery rates (60/min) a backlog drains slowly. | Joint doc author | Open |
| C8 | "Xenibase delivers signed events in commit order" (as a property of the trigger) | Xenibase delivers in order, but Taskiem starts one run per event and runs them concurrently, so **runs do not complete in commit order**. Add a note, and link Q12 for workflows that need ordering, which would use a per-key concurrency setting. | Joint doc author | Open |
| C9 | Stopgap actions "through a parameterised SQL call through the management API" | The published SQL endpoint has **no bound parameters**, allows several statements, and returns SQL errors with status 200. Taskiem will not build write actions by putting values into SQL text. The stopgap needs Xenibase to add a parameterised endpoint (P1-G1) or row-write endpoints. Until then P1 ships reads only (query rows through the rows endpoint), plus the trigger. | Joint doc author, Xenibase team | Open |
| C10 | "Writes carry an idempotency key … so Taskiem's retries never write twice" | Xenibase has no idempotency-key mechanism to carry one. Options: Xenibase honours an `Idempotency-Key` header on writes (P1-G2); or Taskiem uses natural keys (upsert on a conflict column is idempotent, a plain insert is not). Until then, insert is an `unsafe_write`, so an uncertain outcome is held for a person rather than retried. | Xenibase team | Open |
| C11 | Execution history on Xenibase dedicated instances | Taskiem's own database needs things a managed service may not allow: creating roles, roles that own `SECURITY DEFINER` functions, forced row-level security, `LISTEN/NOTIFY`, partitioning, and a migration user that owns the schema. Restate this as a feasibility check (Q5, P4-T6) rather than a capability. | Both | Open |

## 4. Phases and work items

These are the joint doc's four phases, with each item's owner, dependency and status. Item IDs are used in commits and in [What needs people](needs-people.md#xenibase-integration).

### Phase 1 — Connector on today's Xenibase APIs (can start now)

**Taskiem team**

| ID | Item | Detail | Depends on | Status |
| --- | --- | --- | --- | --- |
| P1-T1 | **Remote trigger registration** (engine) | Connector triggers gain an optional lifecycle. On publish, the connector calls the provider to create the subscription (here, a Xenibase webhook pointed at Taskiem's ingest URL) and stores the returned secret in the vault. On unpublish or redeploy it updates or deletes the subscription. The connection's status shows a broken or paused remote subscription. The lifecycle is generic, so Telegram, WhatsApp and the mobile-money callbacks can use it later. | — | Done |
| P1-T2 | `xenibase@1` connection | API token, plus organisation and project (the token should be project-restricted). Scopes: `read` for the trigger and query, `write` for the webhook API and writes. A connection test that names the token's organisation and projects. Base URL per connection (self-hosted Xenibase) or the platform default. | — | Done (base URL: Q20 open) |
| P1-T3 | Row-changed trigger | Table(s), events and optional changed columns, registered through P1-T1. Signature checked with `hmac_sha256_timestamped` against `Xenibase-Signature`, adding a format if the existing one differs. De-duplicated on `Xenibase-Event-Id`. `TEST` events delivered as tests. Truncated events refetched through the rows endpoint by `primary_key` before the run starts, or passed as truncated (Q14). PII declared by path when a table's columns are marked. | P1-T1, P1-T2 | Done (Q14, Q22–Q24 open; table PII marking not yet) |
| P1-T4 | Query rows (read) | Through the rows endpoint: structured filters, order, limit (≤ 1,000), keyset pagination, as a `read` action. Never through the SQL endpoint with values in the text. | P1-T2 | Done |
| P1-T5 | Write actions (insert, upsert, update, delete with a required filter and a maximum-affected guard; call a function) | Built on Xenibase's V4 data API with the project's secret key: every write is an `idempotent_write` carrying `Idempotency-Key`; update and delete need a key or a filter and refuse past `max_affected`. The connection gains `secret_key` and `api_url`. | P1-G1, P1-G2 (both answered by the data API) | Done (17 October 2026) |
| P1-T6 | Error mapping | Map Xenibase `{code}` values to Taskiem's classes (validation, permission, conflict, rate limit, temporarily unavailable). Map SQL errors returned with status 200 by SQLSTATE: `23505` conflict, `40001` and `40P01` retryable, `57014` timeout, `42501` permission. Refusals with a `Retry-After` are retryable. | P1-G4 (error code list) | Done, by HTTP status where no code is published (P1-G4) |
| P1-T7 | Fake Xenibase server and fixtures | Built from the published payloads and OpenAPI file: signing, ordering, retries, truncation, test events, the webhook API, the rows endpoint, errors. | — | Done |
| P1-T8 | End-to-end acceptance test | The joint doc's "done when": a new `orders` row starts a workflow that sends a WhatsApp message (fake Graph API) and writes a status back to the row; a rolled-back insert starts nothing; deliveries retried after Taskiem was unavailable are taken once. | P1-T3, P1-T5 | Done, with the write-back |
| P1-T9 | Docs and SDK | `docs/integrations/xenibase.md`, SDK helpers, a template ("new row → WhatsApp message"), and an entry in the evaluation suite | P1-T3 | Done |

**Xenibase team**

| ID | Item | Detail | Status |
| --- | --- | --- | --- |
| P1-G1 | Parameterised, single-statement SQL endpoint, or row-write endpoints | Answered: the V4 data API's row writes (bound values, one operation per call, `filter_required`, `max_affected`, non-2xx errors with stable codes). Xenibase will add no parameterised SQL endpoint. | Done (exists) |
| P1-G2 | `Idempotency-Key` on write endpoints | Done: on every data API write, 24-hour window, `Idempotent-Replayed: true` on a replay, `422 idempotency_key_reused` on a different request with the same key | Done |
| P1-G3 | Webhook management API contract frozen | Done: `docs/webhooks.md` ("Managing webhooks from another tool"), plus `description` and string `metadata` on webhooks, and a deprecation policy (`docs/errors.md`) | Done |
| P1-G4 | Error code list | Done: `docs/errors.md`, with the retryable codes marked; every `429` and retryable `503` carries `Retry-After`. Note: the management API's body is flat (`{code, message}`), the data API's is wrapped (`{"error": {code, message, details, request_id}}`) | Done |
| P1-G5 | Test fixtures and a sandbox organisation | Fixtures done (`docs/integrations/fixtures/webhooks`, checked in Xenibase's CI); the sandbox organisation and its credentials are a people item (partner tracker S1) | Fixtures done; sandbox needs people |
| P1-G6 | Webhook delivery rates | Answered: deliveries count against the organisation's per-minute rate, whoever created the webhook | Done |

### Phase 2 — Account linking and shared modules (can run in parallel with phase 1)

| ID | Item | Owner | Detail | Status |
| --- | --- | --- | --- | --- |
| P2-G1 | OAuth-style authorization flow | Xenibase | Authorization code with PKCE (S256, mandatory). The customer picks the organisation, projects and scopes on a consent page. The exchange issues an ordinary Xenibase API token (`created_via: oauth`, named after the app), so revocation, limits and the Account page are shared. Platform admins register clients. | Built (17 October 2026, Xenibase branch) |
| P2-T1 | Generic OAuth connection flow | Taskiem | Authorization-code connections for any connector declaring `auth.oauth2` (Xenibase first): state hashed and PKCE verifier sealed server-side, a 10-minute request, the token answer filling the connection's fields, revocation both ways (Disconnect revokes at the provider; a 401 `invalid_token` marks the connection expired), Connect Xenibase on the Connections page ([decision 0034](decisions/0034-oauth-connections.md)) | Done (17 October 2026) |
| P2-T2 | Revocation in both directions | Both | Revoking in either product stops the connection: Xenibase's Account page or `POST /oauth/revoke`, Taskiem's Disconnect; a revoked token makes the connection `expired` at its next use. | Done |
| P2-T3 | Shared payment module | Xenibase owns, per the joint doc | Depends on Q1 and C3–C5: Taskiem's `billing.PaymentProvider` already exists | Decision needed |
| P2-T4 | Shared message module | Taskiem owns, per the joint doc | Taskiem's WhatsApp layer, outbox and templates exist. Decide whether Xenibase calls Taskiem's public API, imports a versioned module, or reuses the design (Q1, Q16). | Decision needed |

### Phase 3 — Data API (waits for Xenibase V4 data API)

| ID | Item | Owner | Detail | Status |
| --- | --- | --- | --- | --- |
| P3-G1 | Data API and SDK | Xenibase | V4 data API: query, insert, upsert, update, delete, RPC, batches; SDKs for JavaScript, Dart and Go | Done (exists) |
| P3-T1 | Rebuild actions on the data API | Taskiem | There was no stopgap to retire: the writes were built on the data API from the start (P1-T5) and `read_rows` reads through it with typed values; `query_rows` on the management rows endpoint stays for tokens without a secret key. Major version unchanged (1.1.0). | Done |
| P3-T2 | **Data tables (new feature)** | Taskiem | Designed in [decision 0033](decisions/0033-data-tables-on-xenibase.md): one Xenibase project per organisation, provisioned by Taskiem from a platform-owned organisation (or the organisation's own project), tables through Xenibase's schema API, rows through the data API with idempotency keys, reached by workflows as a managed `xenibase@1` connection named `data_tables`; the table page, API, chat and builder context. Guide: [data tables](data-tables.md); on WhatsApp, `tables`, `add to <table>` and `find`. | Done (17 October 2026) |
| P3-G2 | Project creation by token, with quotas | Xenibase | Exists: `POST /api/v1/projects` with an unrestricted token with the `write` scope, within the organisation's quota (`409 quota_exceeded`); turning backend services on and minting keys need the `admin` scope | Done (exists) |

### Phase 4 — Storage, auth, billing and operations (waits for Xenibase V4)

| ID | Item | Owner | Detail | Status |
| --- | --- | --- | --- | --- |
| P4-T1 | Files on Xenibase storage | Taskiem | A storage connector (buckets, signed URLs) and, if wanted, Xenibase as the backend for workflow files (Q18) | Waiting |
| P4-T2 | Auth for public forms | Taskiem | Only if a public-forms feature is specified (C2, Q10) | Spec needed |
| P4-T3 | Combined plan and invoice | Both | Taskiem's plans config and billing can carry a bundle discount. Decide which product issues the invoice and how a combined subscription moves between the two (Q3, Q4). | Decision needed |
| P4-T4 | Xenibase operations workflows | Taskiem builds and hosts; Xenibase defines processes and events | Dunning, onboarding, withholding-tax credit-note chasing, and capacity, backup and incident alerts, built as workflows in a Taskiem tenant for Xenibase. These are Taskiem's first external money-handling reference workflows. | Waiting on Xenibase events |
| P4-T5 | Integration tests in each other's CI | Both | Each team runs the other's contract tests on every change to a contract the other depends on | Planned |
| P4-T6 | Execution store feasibility | Both | Answered by Xenibase: `SECURITY DEFINER`, forced RLS, `LISTEN/NOTIFY` (session pooler or direct), partitioning and an owning migration role all work on a dedicated instance; creating roles did not. Xenibase adds an operator-only action that creates integrator NOLOGIN roles on a dedicated project and grants them to the owner, so Taskiem's migrations (which create `taskiem_app` and `taskiem_dispatch`) can run there. | Feasible; bootstrap built on the Xenibase branch |

## 5. Open questions

The joint doc's five questions come first (Q1–Q5). The rest came up while checking both codebases.

| # | Question | Who decides | Blocks |
| --- | --- | --- | --- |
| Q1 | Shared library or shared design for common building blocks? Given C4: does Xenibase adopt Taskiem's modules, Taskiem Xenibase's, or do they only align contracts? Who owns each module, and how are versions managed? | Both founders / tech leads | P2-T3, P2-T4 |
| Q2 | Single sign-on across both products, or only linked accounts with explicit consent? | Product | P2-G1, P2-T1 |
| Q3 | Bundle pricing: what discount, and which product's billing issues the bundle invoice? Taskiem's billing uses Paystack and Flutterwave (C3). | Leadership / finance | P4-T3 |
| Q4 | Data tables default: always Xenibase, or Xenibase as one backend among others? **Decided (0033): Xenibase only; an organisation may bring its own Xenibase project.** | Product | P3-T2 |
| Q5 | Execution store: should Taskiem run its own database on Xenibase dedicated instances? This first needs the feasibility check (C11, P4-T6). | Both tech leads | P4-T6 |
| Q6 | Is the joint doc's data-tables idea wanted at all, given Taskiem has no such feature (C1)? If yes, who writes its spec section? **Yes: decision 0033 and the [data tables guide](data-tables.md).** | Product | P3-T2 |
| Q7 | Will Xenibase add a parameterised SQL endpoint or row-write endpoints before the V4 data API (P1-G1)? **Answered: no; the V4 data API is the write path, and it exists.** | Xenibase | Closed |
| Q8 | Will Xenibase honour an `Idempotency-Key` on writes (P1-G2)? **Answered: yes, on every data API write, 24 hours; every xenibase write is an `idempotent_write`.** | Xenibase | Closed |
| Q9 | For data tables, does a table map to one Xenibase project per tenant, one per workflow, or a schema in a shared project? Who pays for the project? **Decided (0033): one project per organisation, paid by the platform as part of the plan.** | Product, both teams | P3-T2 |
| Q10 | Does Taskiem want a public forms feature (C2)? If yes, does it use Xenibase auth, Taskiem's own end-user tokens, or WhatsApp OTP, which Taskiem already has? | Product | P4-T2 |
| Q11 | Should the Xenibase trigger implement wd/v1's reserved `database_change` trigger type, or stay a connector trigger (`connector_event`), with `database_change` kept for direct logical replication on any Postgres? | Taskiem | P1-T3 shape |
| Q12 | Ordering: do some customer workflows need events processed in commit order? If yes, use `settings.concurrency_key` per table and primary key, or a strictly serial option (C8). | Product | P1-T3 docs |
| Q13 | Does Xenibase have, or plan, a free-tier paused project with a "resuming" answer? **Answered: `503 project_resuming` (`Retry-After: 10`) and `503 project_restoring` (`Retry-After: 60`) on both APIs; retryable.** | Xenibase | Closed |
| Q14 | Truncated events (over 256 KB): should Taskiem refetch the row before starting the run (one more API call, and the row may have changed since) or pass the event as truncated and let the workflow fetch it? | Taskiem, with Xenibase input | P1-T3 |
| Q15 | Do deliveries from Taskiem-created webhooks count against the customer's Xenibase delivery-rate limit? **Answered: yes, against the organisation's per-minute rate; over it, events queue.** | Xenibase | Closed |
| Q16 | For the shared message module: should Xenibase's notifications (dunning, alerts) go through Taskiem's WhatsApp and SMS layer as a customer of Taskiem's public API, through a shared module, or separately? | Both | P2-T4, P4-T4 |
| Q17 | Should Taskiem's billing handle withholding tax (C5)? | Finance | Taskiem billing |
| Q18 | Should workflow files (attachments, generated documents, container-step outputs) move to Xenibase storage, or only be reachable through a storage connector? | Product | P4-T1 |
| Q19 | Where does a customer manage a linked connection: in both products, or one canonical place with the other read-only? | Product | P2-T1, P2-T2 |
| Q20 | Self-hosted Xenibase: should the connector support any Xenibase server URL, or only Xenibase's cloud? | Product | P1-T2 |
| Q21 | Which Xenibase contracts count as "public" for the deprecation policy, given only the V2 docs and OpenAPI file are published (V3 and V4 are not in the repository)? | Xenibase | All phases |
| Q22 | During `rotate-secret`, is there an overlap window? **Answered: `overlap_seconds` (up to a day); each delivery then carries one `v1=` per secret, which the verifier already handles.** | Xenibase | Closed |
| Q23 | Is `primary_key` enough to refetch a truncated event's row, and could it be sent on every event? **Answered: yes; every change event of a table with a primary key carries it.** | Xenibase | Closed |
| Q24 | Does an `UPDATE` event's `old_record` hold every column? **Answered: every column (`to_jsonb(OLD)`).** | Xenibase | Closed |
| Q25 | How does an API token authenticate? **Answered: `Authorization: Bearer xb_…`; token calls never need the CSRF header.** | Xenibase | Closed |
| Q26 | Which scope does deleting a webhook need, and does it need a typed confirmation? **Answered: every webhook call needs `write`; deletion needs no `confirm`.** | Xenibase | Closed |
| Q27 | Are webhook names unique within a project? **Answered: yes; `description` and string `metadata` (16 keys) exist, and Xenibase suggests finding a webhook by metadata since a person may rename it (a follow-up for the registrar).** | Xenibase | Closed |
| Q28 | Is the payload's `project` the project's id? **Answered: no, it is the database name; the new `project_id` field is the UUID.** | Xenibase | Closed |

## 6. Updates this plan needs elsewhere

### In Taskiem

| Where | Update | Status |
| --- | --- | --- |
| `docs/decisions/0018-pgdock-integration-principles.md` | The principles in section 1 | Done with this plan |
| `docs/needs-people.md` | A "Xenibase integration" section pointing to Q1–Q28 and the Xenibase-side items | Done with this plan |
| `docs/phase-4-status.md` | A line saying the Xenibase integration runs as a parallel plan, linking here | Done with this plan |
| `docs/spec/architecture.md` | If Q6 or Q10 is yes, a new section for data tables or public forms; if Q11 is answered, a note on the `database_change` trigger | After decisions |
| `docs/contracts/connector-v1.md` | The trigger lifecycle from P1-T1 (remote registration) | Done (rules 7–10; [decision 0021](decisions/0021-remote-trigger-registration.md)) |
| `docs/security/threat-model.md` | A boundary for Xenibase ↔ Taskiem: tokens, webhook signatures, the scope of linked accounts, cross-product audit | Done (B16); linked-account consent with P2 |
| `docs/integrations/xenibase.md` | Connector guide | Done |

### In Xenibase

| Where | Update |
| --- | --- |
| `docs/webhooks.md` | Note that integrators (Taskiem) create webhooks through the API with project-restricted tokens, and how deliveries count against rate limits (Q15); whether rotation signs with both secrets for a while (Q22); `primary_key`'s shape (Q23); whether webhook names are unique (Q27) |
| `api/openapi.yaml` (auth) | A security scheme for API tokens (Q25); the scope each webhook operation needs (Q26) |
| `api/openapi.yaml` | ~~A parameterised SQL endpoint or row-write endpoints (P1-G1); `Idempotency-Key` (P1-G2); the error code list (P1-G4)~~ Done through the data API and `docs/errors.md` |
| Authorization | The OAuth-style consent flow (P2-G1): built on the Xenibase branch (`docs/oauth-apps.md` there) |
| CI | Run Taskiem's `xenibase@1` contract tests (P4-T5) |

### In the joint doc

Apply corrections C1–C11. Replace its open-questions section with a link to Q1–Q28 here, or copy them.

## 7. Risks

| Risk | Mitigation |
| --- | --- |
| Tight coupling creeps in (from the joint doc) | Public surfaces only; the third-party test in design reviews; contract tests in both CIs |
| One product's outage cascades (from the joint doc) | Xenibase retries for 24 h; Taskiem's ingest deduplicates and queues; neither product calls the other on its critical path |
| Cross-product data exposure (from the joint doc) | Project-restricted tokens with the smallest scopes; consent; revocation from either side; audit entries in both |
| Roadmap dependency on Xenibase V4 (from the joint doc) | Phase 1 ships the trigger and reads on today's APIs; writes wait for P1-G1 rather than for V4 |
| Bundle confusion and hidden coupling through shared code (from the joint doc) | Each product priced alone; shared modules versioned with owners (Q1) |
| **Unsafe SQL stopgap** (closed) | No values put into SQL text, ever. Writes go through the data API's bound values (C9). |
| **Duplicate writes without idempotency keys** (closed) | Every data API write carries an idempotency key honoured for 24 hours (C10) |
| **Ordering assumptions** (new) | Documented (C8), with a per-key concurrency setting where order matters (Q12) |
| **Unpublished V3 and V4 contracts** (new) | Taskiem builds nothing on them until they are published (Q21) |
| **Remote webhook drift**: a customer deletes or pauses the Xenibase webhook (new) | P1-T1 shows a broken or paused remote subscription on the connection and on the workflow; republishing reinstalls it |
