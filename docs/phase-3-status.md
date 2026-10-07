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
| C | C1 | Sub-tenants (`parent_id`), `embed_apps`, end-user token minting, partner admin API with webhooks and a dual audit trail, headless mode | 13.1, 13.4, 5.3 | In progress |
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
