# Phase 3 status: differentiators

Phase 3 builds what the [build plan](spec/build-plan.md#phase-3--differentiators-about-16-weeks) calls the differentiators, in three workstreams that run in parallel:

- **A.** WhatsApp and USSD as a full interface.
- **B.** AI that builds and repairs workflows.
- **C.** Embedding Taskiem in other products.

Each workstream has its own milestones. Anything that needs people (accounts, approvals, testers, partners) is recorded in [What needs people](needs-people.md#phase-3), and code goes ahead without waiting for it.

## Milestones

| Workstream | Milestone | Scope | Spec | Status |
| --- | --- | --- | --- | --- |
| A | A1 | Number binding by OTP; `chat_sessions` state machine; template library; approvals by interactive buttons with signed decision tokens; step-up hand-off to a web passkey or TOTP; status and trigger commands with explicit confirmation; per-number rate limits | 11.1–11.3, 11.5, 9.1 | In progress |
| A | A2 | WhatsApp Flows forms for inputs and PIN step-up; shared platform number vs own number (embedded signup); template cost accounting | 11.1, 11.4, 16 | Planned |
| A | A3 | USSD fast path (menu steps inline at the edge, writes handed to the engine) and an aggregator connector | 8.4 | Planned |
| A | A4 | Pidgin, Yoruba, Hausa and Igbo intents and replies; voice-note transcription (beta, tested by native speakers) | 11.6 | Planned |
| B | B1 | Provider-agnostic model layer (Claude by default), redaction before prompts, per-tenant budgets, prompt and response audit; builder pipeline (retrieval → schema-constrained draft → validate → self-correct ×3 → dry run → generated tests → review as a draft version with the AI as co-author); the AI can never publish, approve, read secrets or write to a provider | 12.1, 12.3 | In progress |
| B | B2 | Repair pipeline: failure classification, shadow-sandbox fork with recorded inputs and mocked writes, diff and evidence, one-click publish and resume through the normal approval policy | 12.2 | Planned |
| B | B3 | Evaluation suite (200+ requests) with a runner that measures valid-on-first-try, test pass rate and policy violations, gating prompt and model changes | 12.4 | Planned |
| B | B4 | SME template library; chat-based building on WhatsApp (needs A1) | 11.1 | Planned |
| C | C1 | Sub-tenants (`parent_id`), `embed_apps`, end-user token minting, partner admin API with webhooks and a dual audit trail, headless mode | 13.1, 13.4, 5.3 | **Done** ([below](#c1-what-exists)) |
| C | C2 | Embedded builder web component and iframe, theming tokens, custom domains, white-label | 13.4 | Planned |
| C | C3 | Partner connector bridge (the partner's API as a pre-authenticated connector) | 13.4 | Planned |
| C | C4 | First embedded deployment inside a holdco product (Payrolla customer automations) | — | Needs people |
| — | X | Container steps (gVisor or Firecracker); mobile money (M-Pesa, MTN MoMo, Airtel Money); tax and statutory connectors | 7.5 | Planned |

## Exit gate G3

| Criterion | Engineering part | People part |
| --- | --- | --- |
| 50 real approvals through WhatsApp by design-partner users | A1–A2 | Meta business verification, templates approved, design partners using it |
| AI builder valid and test-passing on the first attempt for ≥ 70% of the evaluation suite | B1, B3 | Requests from dogfooding and design partners; a model API account |
| A failed production run repaired through the shadow sandbox and resumed | B2 | A real failure in a partner's production |
| One holdco product running embedded automations for its own customers | C1–C3 | C4 |
| No AI action able to publish, approve or read secrets, confirmed by a security review | B1–B2 (enforced in code and tested) | Independent review |

## C1: what exists

Embedding foundations, 2026-10-07. Partner guide: [embedding.md](embedding.md). Design: [decision 0015](decisions/0015-embedding-tenancy.md). Threat model: boundary B10.

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
