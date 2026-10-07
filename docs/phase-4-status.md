# Phase 4 status: public launch

Phase 4 turns a hand-held product into a self-serve cloud: Nigeria-hosted, highly available, with flat-priced plans anyone can sign up for ([build plan](spec/build-plan.md#phase-4--public-launch-about-16-weeks)). Weeks 1–6 build the production cloud, billing and onboarding; weeks 7–16 are a public beta that covers the 60-day availability window G4 requires.

Gate G4: 99.9% availability for 60 days before GA; signup to first successful run in under 15 minutes; **at least 10 paying external tenants across at least two tiers**; counsel IP review and trademark filing. Anything that needs people is in [What needs people](needs-people.md#phase-4); code goes ahead without waiting for it.

The PGDock integration runs as a separate parallel plan ([PGDock integration](pgdock-integration.md)). Phase 4 does not wait for it.

## Milestones

| Area | Milestone | Scope | Spec | Status |
| --- | --- | --- | --- | --- |
| Billing | P4-1 | Plans catalogue from a config file mapped onto every plan limit, features gated at the API, subscriptions (trial, active, past due with grace, degraded, cancelled, comped), plan changes with proration and downgrade blockers, immutable gapless invoices with VAT and pass-through overage, naira payments through Paystack (checkout, webhooks, saved cards, reconciliation) and Flutterwave, dunning, usage snapshots, billing API and page, operator CLI | 16, 13.1 | **Done** (code; [what exists](#p4-1-billing)); live payments need P4-B2, prices B1 |
| Onboarding | P4-2 | Self-serve signup hardened for the public (email confirmation, limits, abuse checks, trial), getting-started checklist, guided first workflow over the template gallery, in-product help, signup-to-first-run measurement | — | **Done** (code; [what exists](#p4-2-onboarding)); going public needs P4-O1–P4-O4 |
| Cloud | P4-3 | Nigeria-region production cloud: HA Postgres with synchronous standby, PITR, fixed egress IPs; workers split by queue, dedicated pools, read replicas | 2.3, 15.4 | Planned; needs people (infrastructure, D-items) |
| Reliability | P4-4 | 99.9% SLO with on-call, status page, incident process | 15.3 | Planned; needs people |
| Enterprise | P4-5 | BYOK (the `byok` plan feature exists; the key path does not), dedicated single-tenant deployments, white-label tier (built in Phase 3, C2) | 13.4, 14.1 | Planned |
| Ecosystem | P4-6 | Public connector SDK and a submission review process for third-party connectors | 6 | Planned |
| Docs | P4-7 | Public docs site, API reference, connector SDK guide | — | Planned |
| Trust | P4-8 | Bug bounty, security page, ISO 27001 and SOC 2 Type II preparation | 14.4 | Needs people |
| Legal | P4-9 | Counsel IP review, trademark registration, terms of service, DPA under the NDPA | — | Needs people |

## P4-1: billing

Done 7 October 2026. Operator and tenant guide: [billing](billing.md); design: [decision 0017](decisions/0017-plans-and-billing.md). Billing is off by default (`TASKIEM_BILLING`): every tenant is then on the internal `self_hosted` plan, as before.

| Piece | What exists | Code |
| --- | --- | --- |
| Plans | `deploy/plans.yaml`: Starter, Growth, Business (public) and Enterprise (granted) with **placeholder** naira prices pending B1, limits on `tenant_limits` keys (concurrency, workers, ingest, backlog, workflows, steps per run, retention, AI tokens, WhatsApp allowance), features (SSO, SCIM, custom roles, white-label, BYOK, Git, AI, embedded), partner caps, AI overage rate; strict validation; loaded into `plans` by the CLI or at start; retired, never deleted. Built-in `self_hosted` plan | `engine/billing/plans.go`, migration 00090 |
| Effective limits | Platform defaults ← plan ← operator overrides; sub-tenants on the partner's plan (`taskiem_tenant_plan` follows `parent_id`); partner run caps fall back to the plan's; new `max_retention_days` caps run history whatever a workflow asks | `engine/runtime/limits.go`, `engine/runtime/store.go`, migrations 00090, 00094 |
| Features | 402 `plan_feature_required` on creating SSO connections and domains, SCIM config, custom roles, Git connections, AI building (reads pass), the partner API, and white-label custom domains | `api/billing.go`, `api/server.go` |
| Subscriptions | Pure state machine (`Due`, `Schedule`) on a fake clock; trial at signup or first visit; past due with grace and dunning (card retries and emails on configured days); degraded refuses new runs only (402 `billing_degraded`), never running runs, approvals, signals, reconciliation or forks; cancel at period end and resume; comps by the operator | `engine/billing/subscription.go`, `engine/billing/job.go`, migration 00091 |
| Plan changes | Upgrade: new period now, proration credit for the unused part, effective on payment (at once with a saved card); downgrade at period end, refused with blockers and what to remove; monthly ↔ annual | `engine/billing/change.go` |
| Invoices | Gapless numbers per year (`TKM-2026-000001`), immutable once issued (trigger, even for the superuser), lines for the plan, WhatsApp overage per category and month at cost, AI overage, credits; VAT 7.5% (configurable) separately; JSON and a printable HTML page | migration 00092, `engine/billing/service.go`, `engine/billing/money.go`, `api/billing.go` |
| Payments | `Provider` interface; Paystack (initialize → hosted checkout, card and bank transfer; verify; `charge_authorization` for renewals; `charge.success` webhooks verified with the connector verification code) and Flutterwave (payments, verify by reference, tokenized charges, `verif-hash`); platform credentials from the environment; references carry the tenant; every webhook re-verified with the provider, amount and currency must match (else `mismatch`, audited); idempotent receipts; reconciliation of pending payments; abandoned after a day | `engine/billing/provider.go`, `paystack.go`, `flutterwave.go`, `change.go`, `job.go` |
| Usage snapshots | Hourly per tenant and UTC day: runs, steps, deployed workflows, running and stored runs, WhatsApp templates and overage, AI tokens, effective limits; partners see their sub-tenants summed per day | `engine/billing/snapshot.go`, migration 00093 |
| API and web | `GET /v1/billing/status`, `GET /v1/billing`, invoices, `POST /v1/billing/checkout`, `/plan`, `/cancel`, `/webhooks/{provider}`; `billing.manage` (owners); Settings > Billing page (plan cards, usage meters, daily usage, invoices, saved card, change plan with downgrade blockers) and a past-due banner for every member | `api/billing.go`, `web/src/pages/Billing.tsx`, `web/src/App.tsx` |
| Operator CLI | `taskiem billing plans [--file] [--load]`, `taskiem billing grant TENANT PLAN [--until]` (audited) | `cmd/taskiem/billingcmd.go` |
| Tests | Config validation; money (VAT rounding, credits, proration, calendar periods); state machine on a fake clock; plan limits, overrides, sub-tenant inheritance, billing off; trial → past due → degraded → recovered with an approval decided while degraded; card renewals and declines; Paystack checkout and webhooks against a fake Paystack (bad and missing signatures, duplicates, foreign references, amount mismatch); reconciliation and abandonment; upgrade proration, downgrade blockers, scheduled downgrade, cancel and resume, comps; invoice immutability and numbering; WhatsApp overage billed once; snapshots and partner aggregates (and their isolation); retention capped by plan; API gating, blockers, checkout, HTML invoice, degraded 402, permissions | `engine/billing/billing_test.go`, `api/billing_test.go`, `web/src/lib/billing.test.ts` |

What is left in billing:

- **Prices and tiers** (B1) and the real merchant accounts (P4-B2); VAT registration and finance sign-off on invoice content (P4-B3); terms of service (P4-B4).
- **Bank transfer by dedicated virtual accounts** (Paystack DVA): today bank transfers go through the hosted checkout's transfer channel, verified by webhook or reconciliation.
- **Plan caps not yet limits**: step throughput in steps per second (spec 16.1; approximated by `worker_concurrency`), environments per tier, container minutes (to map when the container-steps work adds `max_container_minutes_monthly`), and `max_subtenants` enforced at sub-tenant creation (today it only blocks downgrades; operators still set the partner's cap).
- **BYOK** itself (the plan feature exists).
- PDF invoices (HTML and JSON today), refunds and credit notes (manual today), proration of annual-to-monthly mid-period (scheduled at period end).

## P4-2: onboarding

Done 7 October 2026. Guide: [onboarding](onboarding.md); design: [decision 0022](decisions/0022-self-serve-onboarding.md). Signup stays off by default (`TASKIEM_ALLOW_SIGNUP`).

| Piece | What exists | Code |
| --- | --- | --- |
| Signup | Web page (`/signup`, linked from sign-in when on); JSON only; hidden field for bots; plain-address emails with a dotted domain; built-in throwaway-domain list plus `TASKIEM_SIGNUP_BLOCKED_DOMAINS`; names without links, domain-like words or addresses; password 12–1024 and not the email; per-address limit per day in the database (`TASKIEM_SIGNUP_PER_ADDRESS`, default 5, every replica) on top of the burst limiter; `409` race-safe on a duplicate email; owner signed in at once; trial started with billing on; outcomes counted | `api/onboarding.go`, `web/src/pages/Signup.tsx`, migration 00095 |
| Email confirmation | Single-use 24-hour link by email (selector and SHA-256 secret, five wrong secrets lock it, replaced on resend, three then one per 20 minutes), used by the signed-in person; audited `user.email.verify`; until then a self-serve tenant cannot invite members or create API keys (`403`); nothing held back where email cannot be sent | `api/onboarding.go`, `web/src/pages/Signup.tsx` (`/verify-email`), migration 00095 |
| Checklist | `GET /v1/onboarding`: confirm email, connect an app, create a workflow, publish, run once, invite a teammate, each derived from the tenant's data; hide (`workflow.edit`); in the navigation with progress for self-serve tenants | `api/onboarding.go`, `web/src/pages/Start.tsx`, `web/src/onboarding.tsx` |
| Guided first workflow | Starter templates first; one page per template: details, its connection (prod, the template's connection name) and variables, create and publish, a test run with an input sampled from the inputs schema and a live status; linked from each template's page | `web/src/pages/Start.tsx`, `web/src/lib/onboarding.ts` |
| In-product help | Help panels on Get started, Templates, Connections and Variables; links to `TASKIEM_DOCS_URL/<page>` when set | `web/src/onboarding.tsx`, `web/src/lib/onboarding.ts` |
| Measurement (G4) | `tenant_onboarding.first_run_at` stamped once in the transaction that completes a self-serve tenant's first run, and `taskiem_onboarding_first_run_seconds` (share at or under 900 s); shown to the tenant on Get started | `engine/runtime/store.go`, `engine/telemetry`, migration 00095 |
| Browser test fakes | A connector base URL naming a loopback IP lets that connector reach that port only (`egress.Guard.Loopback`), for fakes in browser tests | `engine/egress`, `cmd/taskiem/serve.go` |
| Tests | Signup refusals, JSON only, per-address limit shared by two replicas, signup off; confirmation by email, gates before and after, someone else's session, wrong secret, reuse, resend replaces; the whole G4 path through the API with a fake Termii (connection, variable, template, publish, run, stamp once, checklist, dismiss permission); other tenants' runs never count; loopback egress exceptions; checklist wording, starter order, durations, sample inputs, help links; Playwright: signup → template → fake Termii → publish → run → measured under 15 minutes, invite refused before confirming | `api/onboarding_test.go`, `engine/egress/egress_test.go`, `web/src/lib/onboarding.test.ts`, `web/e2e/onboarding.spec.ts` |

What is left in onboarding:

- **People**: the sending domain for signup email, the abuse policy, the docs site, and timed sessions with real new users (P4-O1–P4-O4 in [what needs people](needs-people.md#phase-4)).
- **Cleanup of unconfirmed tenants** (suspend or delete after N days) waits for the abuse policy.
- **WhatsApp as the entry point** (go-to-market step 2): signing up from WhatsApp, rather than linking a number after a web signup, is not built.
- A connection test before the first run (`auth.test` in manifests) is not called by the guide yet; a wrong key shows as a failed step on the test run.
