# Phase 3 status: differentiators

Phase 3 builds what the [build plan](spec/build-plan.md#phase-3--differentiators-about-16-weeks) calls the differentiators, in three workstreams that run in parallel:

- **A.** WhatsApp and USSD as a full interface.
- **B.** AI that builds and repairs workflows.
- **C.** Embedding Taskiem in other products.

Each workstream has its own milestones. Anything that needs people (accounts, approvals, testers, partners) is recorded in [What needs people](needs-people.md#phase-3), and code goes ahead without waiting for it.

## Milestones

| Workstream | Milestone | Scope | Spec | Status |
| --- | --- | --- | --- | --- |
| A | A1 | Number binding by OTP; `chat_sessions` state machine; template library; approvals by interactive buttons with signed decision tokens; step-up hand-off to a web passkey or TOTP; status and trigger commands with explicit confirmation; per-number rate limits | 11.1–11.3, 11.5, 9.1 | Done (code; [what exists](#a1-whatsapp-as-a-client-of-the-platform)); needs W1, W2 for production |
| A | A2 | WhatsApp Flows forms for inputs and PIN step-up; shared platform number vs own number (embedded signup); template cost accounting | 11.1, 11.4, 16 | Planned |
| A | A3 | USSD fast path (menu steps inline at the edge, writes handed to the engine) and an aggregator connector | 8.4 | Planned |
| A | A4 | Pidgin, Yoruba, Hausa and Igbo intents and replies; voice-note transcription (beta, tested by native speakers) | 11.6 | Planned |
| B | B1 | Provider-agnostic model layer (Claude by default), redaction before prompts, per-tenant budgets, prompt and response audit; builder pipeline (retrieval → schema-constrained draft → validate → self-correct ×3 → dry run → generated tests → review as a draft version with the AI as co-author); the AI can never publish, approve, read secrets or write to a provider | 12.1, 12.3 | Done ([what exists](#b1-what-exists)) |
| B | B2 | Repair pipeline: failure classification, shadow-sandbox fork with recorded inputs and mocked writes, diff and evidence, one-click publish and resume through the normal approval policy | 12.2 | Done ([what exists](#b2-what-exists)) |
| B | B3 | Evaluation suite (200+ requests) with a runner that measures valid-on-first-try, test pass rate and policy violations, gating prompt and model changes | 12.4 | Planned |
| B | B4 | SME template library; chat-based building on WhatsApp (needs A1) | 11.1 | Planned |
| C | C1 | Sub-tenants (`parent_id`), `embed_apps`, end-user token minting, partner admin API with webhooks and a dual audit trail, headless mode | 13.1, 13.4, 5.3 | **Done** ([below](#c1-what-exists)) |
| C | C2 | Embedded builder web component and iframe, theming tokens, custom domains, white-label | 13.4 | Planned |
| C | C3 | Partner connector bridge (the partner's API as a pre-authenticated connector) | 13.4 | Planned |
| C | C4 | First embedded deployment inside a holdco product (Payrolla customer automations) | — | Needs people |
| — | X | Container steps (gVisor or Firecracker); mobile money (M-Pesa, MTN MoMo, Airtel Money); tax and statutory connectors | 7.5 | Planned |

## A1: WhatsApp as a client of the platform

Done 7 October 2026. Setup, user guide and security model: [WhatsApp](whatsapp.md). Production needs the number and approved templates ([needs people](needs-people.md#phase-3) W1, W2).

| Piece | What exists | Code |
| --- | --- | --- |
| Platform number | `TASKIEM_WHATSAPP_*` from the Secret; Meta's webhook at `/channels/whatsapp` on the edge (handshake with the verify token, `X-Hub-Signature-256` before parsing, message ids claimed once); sends to the Graph API through the egress guard (base URL overridable for tests); every tenant message prefixed with the tenant's name (shared number) | `engine/whatsapp` (`graph.go`, `webhook.go`, `platform.go`), `api/whatsapp_chat.go`, `cmd/taskiem/serve.go` |
| Number binding (11.2) | Account > WhatsApp: proof of the account, a 6-digit code by the `taskiem_otp` authentication template, typed in the web app or sent back from the number; one number per account and per number; 10 minutes, five guesses, three codes an hour; unlinking; audited in each of the person's tenants with the number masked | `api/whatsapp.go`, migration 00035, `web/src/pages/Account.tsx` |
| Identity per message | A message resolves to (tenant, user, roles): the person bound to the number, in the tenant the number works in (`switch <organisation>`), with their current permissions; unbound numbers get a short reply, with a hook (`Server.WhatsAppPublic`) for public menus later | `api/whatsapp_chat.go` |
| `chat_sessions` (11.3) | idle, collecting_input, awaiting_confirmation, awaiting_approval_stepup; tenant data under forced RLS, read only in the conversation's current tenant; expiring within the 24-hour window; the window and current tenant per number in person-level tables reached only by functions | migrations 00035, 00036 |
| Templates | Eight templates (OTP, approval request, step-up link, run failed, run completed, needs reconciliation, approval waiting, generic alert) defined in code with category, body, variables and buttons; text inside the window, the template outside it, and on Meta's 131047 | `engine/whatsapp/templates.go`, [list for submission](whatsapp.md#templates) |
| Approvals by buttons (11.1, 9.1) | The notifier sends each open approval to eligible approvers with bound numbers (role or delegation, not makers, approval.decide), subject masked (account numbers last four); Approve/Reject carry signed single-use decision tokens; a tap is checked against the token and the sender's number, then decided by `VoteApproval` (policies, levels, distinct approvers, makers, delegation); refusals audited; `approvals` resends | `api/whatsapp_approvals.go`, `engine/whatsapp/token.go`, `engine/whatsapp/mask.go` |
| Step-up hand-off | A policy needing step-up gets a 10-minute single-use link (token in the fragment) to `/handoff`, where the same person, signed in to the same tenant, confirms that decision with a passkey or TOTP | `api/whatsapp_approvals.go`, `web/src/pages/Handoff.tsx` |
| Status and triggers (11.1, 11.5) | `status` / `what failed today?` (run.read; counts and latest failures, redacted); `run <workflow>` matched deterministically to workflows deployed in prod the person may start (run.start), required inputs collected field by field against the input schema (personal ones sealed while waiting), an explicit summary and **yes**, then `StartRun` with plan limits; the person hears how the run ended | `api/whatsapp_chat.go`, `engine/whatsapp/inputs.go` |
| Rate limits (11.5) | Per number: messages, run starts, unbound replies, codes | `api/whatsapp_chat.go` |
| Notifications | WhatsApp alert channel kind (members by email; text in the window, the kind's template outside) | `engine/alerts`, `api/alerts.go`, migration 00037 |
| Tests | Fake Graph API (`engine/whatsapp/whatsapptest`); binding (happy path, wrong code, expiry, attempt limit, number taken, pacing, by reply, unbinding, audit); signature, handshake and dedup; unbound reply; tenant switch; approvals end to end through the runtime (approve and reject, forgery, other key, replay, pair, expiry, wrong sender, self-approval, closed); step-up hand-off; status redaction; trigger with inputs and confirmation; flood limit; window and template fallback; a browser test of binding | `api/whatsapp_test.go`, `engine/whatsapp/whatsapp_test.go`, `web/e2e/whatsapp.spec.ts` |

Left for A2: WhatsApp Flows forms (inputs) and a Flows PIN for step-up; tenants' own numbers (a second `whatsapp.Platform` per phone number id, embedded signup, W3); template cost accounting against plan allowances; languages beyond English.

## Exit gate G3

| Criterion | Engineering part | People part |
| --- | --- | --- |
| 50 real approvals through WhatsApp by design-partner users | A1–A2 | Meta business verification, templates approved, design partners using it |
| AI builder valid and test-passing on the first attempt for ≥ 70% of the evaluation suite | B1, B3 | Requests from dogfooding and design partners; a model API account |
| A failed production run repaired through the shadow sandbox and resumed | B2 (done: the whole path runs against a fake model in `TestRepairDataPatchPublishAndResume` and `TestRepairFourEyes`) | A real failure in a partner's production, repaired with a real model (AI1) |
| One holdco product running embedded automations for its own customers | C1–C3 | C4 |
| No AI action able to publish, approve or read secrets, confirmed by a security review | B1–B2 (enforced in code and tested) | Independent review |

## B1: what exists

The model layer and the AI workflow builder ([AI](ai.md), [decision 0014](decisions/0014-ai-model-layer.md)). Everything runs against a fake model in tests; a real model needs an API account (AI1).

| Piece | Where | Notes |
| --- | --- | --- |
| Provider interface | `engine/ai` | System blocks with cache breakpoints, messages, JSON Schema output, max tokens, effort; returns text, JSON, usage, stop reason, model. `ai.Redacted` puts every prompt through `pii.Redact` |
| Claude provider | `engine/ai/anthropic.go` | Official Go SDK; `claude-opus-5-5` by default (`TASKIEM_AI_MODEL`); effort `high`; structured output envelope; cached system prompt and catalogue; streaming above 16k tokens; `fallbacks: "default"` (beta `server-side-fallback-2026-07-01`); refusals checked before content. Request shape tested against an httptest server |
| Self-hosted provider | `engine/ai/selfhosted.go` | OpenAI-compatible chat completions with `response_format: json_schema` (vLLM, llama.cpp, Ollama, TGI); which model to offer is AI1 |
| Fake provider | `engine/ai` (`Fake`) | Scripted or computed answers, records what it was sent |
| Builder pipeline | `engine/ai/builder` | Keyword retrieval over manifests, tenant context by name, similar workflows by structure; draft; publishing checks (`wdcheck`, injected) plus the `payment_write_without_approval` policy rule; up to 3 corrections; dry run in `wdtest` (generated happy path and model-proposed cases); proposal with summary, assumptions, problems, warnings, results, rounds, usage |
| Safety boundary | `engine/ai/imports_test.go`, `builder.ManifestOnly`, `api/ai_test.go` | Import graph excludes the vault, runtime, database, audit, ingest, sandboxes, API and connector handlers; the builder sees manifests only; `/v1/ai` routes pinned; no secret, credential or variable value reaches a prompt (seeded and checked) |
| API | `api/ai.go` | `GET /v1/ai/status`, `POST /v1/ai/build` (async, rate-limited, 503 when off, 429 over budget), `GET /v1/ai/builds/{id}`, `POST /v1/ai/builds/{id}/save` (draft only, `created_by` the person, `ai_build_id` the AI co-author), `GET /v1/ai/builds/{id}/interactions` |
| Storage and audit | migration 00040 | `ai_builds`, insert-only `ai_interactions` (redacted prompt and answer, model, usage, outcome), `workflow_versions.ai_build_id`, all under forced RLS; `ai.propose` and `ai.save` in the audit chain |
| Budgets | migration 00041, `engine/runtime/limits.go` | `ai_monthly_tokens` plan limit (default 2,000,000; `TASKIEM_DEFAULT_AI_MONTHLY_TOKENS`; `taskiem tenants limits --set ai_monthly_tokens=N`); use shown in `GET /v1/limits`; only AI building stops at the cap |
| Web | `web/src/pages/AIBuild.tsx` | **Build with AI** (workflow list) and **Change with AI** (editor): goal, progress, canvas preview, summary, warnings, dry run, Save as draft |
| Evaluation | `tools/aieval`, `evals/builder/seed.jsonl` | 25 seed requests from the dogfood flows and the catalogue; valid-on-first-try, test pass, policy violations, connector and step recall; `--provider anthropic`; `--min-first-try` gate |

Left for later milestones: B2 (now done, [below](#b2-what-exists)) built the repair pipeline on the same layer; B3 grows the suite to 200+ reviewed requests (AI2) and runs the gate in CI on prompt or model changes; B4 adds the SME template library and building over WhatsApp.
## B2: what exists

Self-repair of failed runs, 2026-10-07 ([AI: repairing failed runs](ai.md#repairing-failed-runs), [decision 0016](decisions/0016-repair-and-resume.md), threat model B12). Everything runs against a fake model in tests.

| Piece | Where | Notes |
| --- | --- | --- |
| Triggering | migration 00050 | Triggers queue `repair_jobs` (one per run and reason: `run_failed`, `needs_reconciliation`, `drift`) in the causing transaction; `Server.RunRepairs` works the queue in the API role where a model is configured (claims with `SKIP LOCKED`, stale jobs retried); per-tenant switch `ai_repair_settings` (on by default); jobs needing the model skipped when `ai_monthly_tokens` is spent; a drift job waits for its run to settle |
| Classification | `engine/ai/repair/classify.go` | Rules over error kinds, HTTP status, connector error classes, `MaybeApplied`, drift findings and whether the failing step reads the trigger; the model classifies only when the rules are unsure (in the same call as the patch) |
| Proposals per class | `engine/ai/repair`, `api/repair.go` | transient: retry from the failed step; credential: reconnect link, then retry; data, schema_drift, logic: a model patch (up to 3 attempts, failures fed back); unknown_outcome: the connector's read-only reconcile action run and its masked answer shown (`Worker.CheckReconcile`), resolved by a person |
| Shadow sandbox | `engine/shadow` | Fork of the failed run's opened recording into a wd-test case: recorded trigger, completed steps replayed from recorded outputs, everything else mocked from output schemas (all writes mocked), recorded approvals and signals; must complete with schema-valid connector inputs; flags completed writes the patch would change (publish-only); `wdtest.Result` now reports steps, inputs and errors |
| Tests for a patch | `api/repair.go`, migration 00052 | Regression test for the failing case from the redacted recording (must pass; notes whether it fails on the old version), the model's own test, and every stored `workflow_tests` case of the workflow; accepted repairs add their tests |
| Proposal | migration 00050 | `repair_proposals` (forced RLS): class, who classified, explanation, action, patched definition, `wddiff` diff, evidence, tests, attempts, tokens, status (`analysing`, `proposed`, `action`, `withheld`, `failed`, `awaiting_publish`, `awaiting_promotion`, `published`, `resumed`, `dismissed`); `ai_interactions.repair_id`; `workflow_versions.repair_id`; audit `ai.repair.propose` (actor type `ai`), `ai.repair.accept`, `ai.repair.dismiss`, `run.resume` |
| API | `api/repair.go` | List per run (`run.read`) and workflow (`workflow.read`), get, interactions (`audit.read`), accept (`run.resolve`, plus `workflow.publish` for a patch), dismiss (`run.resolve`), settings (`policy.manage` to change) |
| Publish and resume | `api/repair.go`, `api/governance.go`, `api/environments.go` | Accept creates the next version (`created_by` `system:ai-repair`), publishes through the normal path in the person's name (four-eyes: a publish request someone else approves; promotion gates: resume once deployed in the run's environment); resumes after a publish, a publish decision or a promotion; a rejected request dismisses the proposal |
| Resume (fork from step) | `engine/runtime/fork.go`, migration 00051 | `StartRequest.Fork`: a new run linked by `parent_run_id`, with the parent's trigger, variables and idempotency seed; completed tasks replayed from `run_replays` when the resolved input matches (writes with another input parked, never sent); signals redelivered, waits fired, approvals asked again; refused for uncertain writes or compensated runs; failed-for-good steps start a new attempt group |
| Safety | `engine/ai/imports_test.go`, `TestRepairRoutes` | The import rule now covers `engine/ai/repair` and also excludes `engine/shadow` and `engine/drift`; the model sees sealed history with placeholders; shadow feedback scrubbed of every opened personal value; routes pinned |
| Web | `web/src/pages/RepairPanel.tsx` | "Proposed fix" on a run's page: class, explanation, diff, shadow evidence and tests, Accept (publish and resume) / Retry / Dismiss, reconnect link, reconcile answer, waiting states |
| Tests | `engine/ai/repair/repair_test.go`, `engine/shadow/shadow_test.go`, `engine/runtime/fork_test.go`, `api/repair_test.go` | Classification table; model never asked for certain actions; redaction; three attempts; budget; shadow pass, fail, mismatch and schema checks; fork replays without re-sending, parks changed writes, refuses uncertain runs; API end to end per class (data with publish and resume counting provider executions, four-eyes, transient retry, credential, unknown outcome with reconcile, schema drift from a drift trigger with dedup), a bad patch withheld, budget exhaustion, permissions, tenant isolation and the off switch |

Left for later: alerts when a proposal appears (it shows on the run's page); an evaluation suite for repairs alongside B3's; resuming runs that compensated (a person starts a new run today); patches for workflows managed in a repository go through the repository (accept refuses them, as manual edits are refused).

Migrations 00050–00052 were numbered for B2 while C2 (00055–00059) and A2 (00060–00064) land in parallel; see the note under C1's known gaps.

## C1: what exists

Embedding foundations, 2026-10-07. Partner guide: [embedding.md](embedding.md). Design: [decision 0015](decisions/0015-embedding-tenancy.md). Threat model: boundary B11.

| Area | Deliverable | Where |
| --- | --- | --- |
| Partners | An operator makes a tenant a partner and sets partner-wide caps (`max_subtenants`, `subtenant_runs_per_day`, `subtenant_runs_per_month`); a sub-tenant cannot be one | `taskiem tenants partner`, `partners` (migration 00045) |
| Sub-tenants | `tenants.parent_id`, fixed at creation; created by the partner with a default workspace and `dev` and `prod`; own audit chain, KEK and limits; isolated from other sub-tenants and from the partner's own users (no membership puts one in scope) | Migration 00045, `api/partner.go` |
| Partner scope | `taskiem_partner_enter`: checks the parent, writes the access to both audit chains, narrows the transaction to one sub-tenant. The only path from a partner to sub-tenant data | Migration 00045, `partnerTx` in `api/partner.go` |
| Limit inheritance | A sub-tenant's limits default to its partner's effective limits and are capped at them (when set and when read); partner-wide caps checked at creation and run start | `runtime.Store.effective`, `checkPartnerQuota` (`engine/runtime/limits.go`) |
| Partner admin API | `/v1/partner`: sub-tenants (create, list, limits, suspend, resume), their workflows, runs (redacted outcomes) and usage; embed apps; token minting and revocation; webhook delivery log and retry. Partner API keys with `partner.read` or `partner.manage` only | `api/partner.go`, `api/embedapps.go` |
| Embed apps | Allowed origins, validated branding tokens, allowed connectors (and `http`, `code`, `ai` step types) and templates, end users' permissions, headless switch, webhook URL with a vault-held signing secret, status | `embed_apps` (migration 00046), `engine/embed` |
| End-user tokens | Opaque, SHA-256-hashed, 15 min default and 1 h maximum, bound to one app and optionally one origin; permissions within a fixed ceiling and the app's current list; revocable per end user; stop on suspension, app disable or partner disable. End users are `end_user:<app>/<id>` actors with no platform login | `end_users`, `end_user_tokens`, `taskiem_auth_end_user_token` (migration 00046), `api/embed.go` |
| Embed API and headless mode | `/v1/embed/{app}`: workflows (list, read, create, save, layout, validate, publish), connectors allowed, runs (start, list, read, stream, cancel); CORS only for the app's origins; definitions checked against the app's connectors at save, validate, publish and run start | `api/embed.go` |
| Partner webhooks | `run.completed`, `run.failed`, `workflow.published` (queued by triggers in the causing transaction), `usage.threshold` (80% and 100%, once per period); signed like alert webhooks, retried with backoff, logged; sent by the scheduler role | Migration 00047, `engine/embed/webhooks.go`, `alerts.Sign` |
| Tests | RLS and the partner path (`TestPartnerEnter`, `TestAuthTenantScope`); isolation and dual audit (`TestSubTenantIsolation`); tokens: expiry, audience, origin binding, revocation, permission subsets, forged and altered tokens, live narrowing (`TestEndUserTokens`); CORS (`TestEmbedCORS`); headless flow end to end and allowed connectors and templates (`TestHeadlessFlow`); limit inheritance and partner caps (`TestSubTenantLimits`); suspension (`TestSubTenantSuspension`); signed, retried webhooks (`TestPartnerWebhooks`); the operator CLI (`TestTenantsPartnerCLI`); definitions, origins and branding (`engine/embed`) | `api/embed_test.go`, `engine/db/db_test.go`, `engine/embed/embed_test.go`, `cmd/taskiem/tenantscmd_test.go` |

### Hooks left for C2–C4

- **C2 (web component, iframe, theming, custom domains, white-label).** The embed API is the component's whole backend. `GET /v1/embed/{app}/me` returns the app's branding tokens, validated so they can be applied as CSS custom properties. An iframe needs per-app `frame-ancestors` on its own pages (every page sends `frame-ancestors 'none'` today). `EventSource` cannot send the token header, so the component streams runs with `fetch`. Custom domains will add origins and a host-to-app mapping.
- **C3 (partner connector bridge).** `allowed_connectors` already gates what end users may use. Partner-provisioned, per-sub-tenant connections need a partner API route that writes into the sub-tenant's vault (its own KEK) through `partnerTx`.
- **C4 (Payrolla).** Needs people: [needs-people](needs-people.md#phase-3).

### Known gaps

- A suspended sub-tenant's schedules and webhook triggers keep firing; only its tokens and sessions stop.
- End users cannot hold `approval.decide` (decisions are recorded against platform users) or manage connections; with four-eyes publishing on, they cannot publish.
- Deleting a sub-tenant is an operator task; there is no API for it.
- Migrations 00045–00047 were numbered for C1 while A1 (00035–00039) and B1 (00040–00044) land in parallel; goose applies them in order on a fresh database, and an existing one migrated before A1 and B1 must apply those with goose's allow-missing option, or be migrated after all three merge.
