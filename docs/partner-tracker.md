# Partner tracker: what is still needed

Last updated 23 October 2026, after Payrolla's integration pack; staging is up at https://staging.ispend.name.ng, so partner sandbox work can start. One place for what is outstanding with each partner, and what each one is waiting on from us. Every item has an id that matches the request page or guide where it was asked, so replies can be ticked off here.

The request pages sent to partners:

| Partner | Page | Last sent |
| --- | --- | --- |
| Payrolla | [Payrolla integration: our answers, what's left, and the guide](https://claude.ai/artifact/QqY5U7DNXwAYVXq2hWZGDt) | 14 October 2026, answering Payrolla's reply. Payrolla's integration pack came back on 23 October ([kept here](integrations/payrolla-reference/01-reply-email.md)) |
| iswallet (and iSpend) | [What Taskiem needs from iswallet to go live](https://claude.ai/artifact/QkzDpSN1V75TozwWs73Tpo) | 9 October 2026, plus section 3a on 14 October |
| Xenibase | [What Taskiem needs from Xenibase](https://claude.ai/artifact/KjzURqhiLmdUsBttbXk7to) | 9 October 2026 |

Keys, tokens and signing secrets never travel in email, chat, tickets or these pages: the holder enters them on Taskiem's Connections or Secrets page, or we agree a one-time password-manager share.

## Payrolla

**Changed by [decision 0044](decisions/0044-rails-and-products.md).** Payrolla already pays its customers' staff itself, through iswallet, and has webhooks and embeddable screens, so Taskiem connects to it as a product instead of paying for it: the webhook Taskiem received, the disbursement report and the reversal notice are all withdrawn, before Payrolla builds the notice endpoint (R0-1). The old guide is [archived](integrations/archive/payrolla.md). Payroll for a business without a payroll product is Taskiem's [Run payroll](templates.md) template. What is needed from Payrolla now is its own API, events and embed, for the Payrolla connector (R5) and its screens inside Taskiem (R6).

| Id | Needed from Payrolla | When | Status |
| --- | --- | --- | --- |
| N1 | Pilot company's payroll: headcount, total net value, how many paid to bank accounts, run dates; and Payrolla's largest payroll across customers. iswallet needs these for the wallet tier and rate limit | Now | [x] 14 October: Payrolla is in beta with no production customer yet. The sandbox pilot (Chuks Electronics) pays 2 people, both to bank accounts, ₦3,000 net in all; that is also Payrolla's largest payroll. Run dates not fixed. A new TIER_1 wallet (₦50,000 per payout and per day) and 100 requests a minute cover it, so no tier or rate-limit raise is needed until a real customer is onboarded |
| N2 | Peter Chuks's full email; approvers so far: Peter Chuks (eazidefi@gmail.com), John Chuks (eazidefi+approver@gmail.com) | Now | [x] 14 October |
| N3 | Each onboarded company's iswallet wallets it pays from (at least its funding wallet), for its connection's `wallet_ids`; for sandbox, the test company's wallets | Now | [x] Withdrawn (decision 0044): Taskiem no longer pays from Payrolla's wallets |
| N4 | Who at Payrolla receives Taskiem's partner API key | Now | [x] Withdrawn: no partner API key is needed to connect a product |
| N5 | Build `POST {payrolla_api_url}/payrolls/{payroll_id}/reversal-notice` to the payload in the guide | Now | [x] Withdrawn: tell Payrolla not to build it (R0-1) |
| N6 | Pilot company's production `funding_wallet_id` (R6), start date, on-call contact for payroll days (T2) | Before production | [x] Withdrawn (decision 0044) |
| N7 | Confirm 35 days of watching for reversals is enough (settable per company, 1–60) | Confirm | [x] Withdrawn: Payrolla watches its own payouts |
| E1 | Embedding the builder: after the payroll pilot | Later | Deferred by Payrolla |
| P1 | Payrolla's API (R0-2): how Taskiem connects (API key or OAuth), the sandbox, rate limits, and reading payrolls, payslips and approvals | Now | [x] 23 October: the Connect API, one key per company (API key, no OAuth), 120 requests a minute, no approve API (an `approval_url` instead), 7-day payslip links ([reference](integrations/payrolla-reference/02-taskiem-integration-guide.md)); built as `payrolla@1` (R5) |
| P2 | Payrolla's webhooks (R0-2, R0-4): the events (payroll approved, paid, payment failed or returned), their signature, and whether they are set per company or once for all | Now | [x] 23 October: 15 event types, signed `t=<unix>,v1=<HMAC-SHA256>` with 5 minutes' tolerance, subscriptions per company made with the company's key, retries for about two days and 30 days of replay ([webhooks](integrations/payrolla-reference/api/webhooks.md)) |
| P3 | Payrolla's embeddable screens (R0-3): their URLs, how a Payrolla user is signed in inside a frame, `frame-ancestors` allowing Taskiem's origins, and postMessage events | Now | [x] 23 October: a server-minted launch token and Payrolla's SDK, `frame-ancestors` from the owner's allowed origins, `EMBED_*` messages ([embedding](integrations/payrolla-reference/api/embedding.md)); for R6 |
| P4 | A Payrolla sandbox account for Taskiem's tests | Now | [~] 23 October: a test company in production (test employees, an unfunded wallet, so nothing can be paid). Payrolla asks who on our team needs access; each person makes their own key. 26 October: Payrolla's sandbox is `synledger.name.ng/payrolla/api/v1` (where that test company is) and production is `ispend.name.ng/payrolla/api/v1`; connections choose live or test |
| P5 | Taskiem's exact production origins, for each owner's embedding settings and Payrolla's customer guide | Before R6 | [ ] Owed by us |
| P6 | The pilot: Chuks Electronics, proposed from 23 October, after the go-live checklist ([checklist](integrations/payrolla-reference/04-go-live-checklist.md)) | Before the pilot | [ ] The connector is built (R5); the checklist's Taskiem items, the test company's keys and a public hooks URL are open |
| Q1 | Payrolla asked which workflows launch first, so the pilot has the scopes on | Answer | [ ] Proposed: the R7 templates (approval link, summary, payslips to staff, returned salaries), which need `payroll:read`, `employees:read`, `webhooks:manage` and, for payslips, `payslips:links` |
| Q2 | Payrolla asked who records employees' consent to WhatsApp messages | Answer | [ ] Proposed: Taskiem. `notify@1` messages someone on WhatsApp only after they ask for updates there with the business's link; everyone else gets SMS |
| Q3 | Payrolla asked how many API calls per company per day to expect (limit 120 a minute per key) | Answer | [ ] Proposed: a few per event (an employee look-up, a payslip link, a run's lines) and two each morning for the scheduled checks; well inside the limit |

For the record, from Payrolla's reply about the withdrawn integration: report base URL `https://synledger.name.ng/payrolla/api/v1/integrations/taskiem` (sandbox; the reply said `/payroller`, but staging serves Payrolla under `/payrolla`, checked 15 October: `/payrolla/health` answers ok and the integration path answers 401 without a token) and `https://ispend.name.ng/payroller/api/v1/integrations/taskiem` (production, as the reply gave it; confirm whether it moved to `/payrolla` too), one bearer token per company issued in Payrolla and entered by the company's account holder; re-sends use `{run_id}-r2`, `-r3`; payrolls are sent once locked.

## iswallet (and iSpend)

The connector is built on iswallet's answers of 5 October ([iswallet](integrations/iswallet.md), [answers](integrations/iswallet-answers-2026-10-05.md)). Their answers to Xenibase of 12–13 October ([kept here](integrations/iswallet-answers-2026-10-12-pgdock.md)) were passed to us on 14 October: they do not answer our request page, but they settle H1, P4 and most of W4, and the connector now uses the lookup by key, `X-RateLimit-Reset` and per-side transfer dedup. Their FX, consent and billing questions are Xenibase's.

| Id | Needed from iswallet | When | Status |
| --- | --- | --- | --- |
| W1 | Sandbox clients provisioned: `taskiem` (Taskiem's own tests of the iswallet rail, e.g. Run payroll: a wallet funded with ₦1m and 5 recipient wallets) and `ispend` (a funded wallet with a virtual account, for the credit-exceptions workflow). The `payrolla` client is withdrawn (decision 0044) | Now | [ ] They provision in one call (`POST /v1/admin/clients/{client}/sandbox/provision`); asked on the 23 October page. iSpend's virtual account needs a director's BVN on a business wallet |
| W2 | Sandbox client keys, delivered securely: the holder types each into Taskiem staging's Connections page | Now | [ ] |
| W3 | Sandbox webhook subscriptions for our URLs (one per product), signing secrets delivered securely | Now | [x] Not needed from iswallet: Taskiem subscribes each connection itself (`POST /v1/webhooks/subscriptions`) and keeps the secret, once the hooks host is public https (`TASKIEM_HOOKS_URL`; [iswallet account numbers](spec/iswallet-account-numbers.md) I7) |
| W4 | How to force each sandbox outcome: payout confirmed, failed, reversed after confirmation, unresolved; credit rejected and reversed | Now | [~] Simulators for deposit, payout confirmed/failed/reversed and credit clawback; failed debits are real refusals. Still open: payout `unresolved`, credit `rejected` |
| H1 | Lookup by our idempotency key | Before production | [x] Shipped 12 October (`GET /v1/requests/{key}`); used by the connector |
| H2 | Typed duplicate error instead of the 500, and the replay window | Before production | [ ] Now their top item; no longer blocks us (the lookup comes first) |
| H3 | `Retry-After` on 429; higher rate limit for `payrolla` | Before production | [~] No `Retry-After` yet (on their list); we use `X-RateLimit-Reset`. No limit raise needed during Payrolla's beta (N1); ask again with the first real customer's headcount |
| H6 | Error catalogue per endpoint, with retryability | Before production | [~] Debit codes given to Xenibase match our handling; a full catalogue per endpoint still open |
| H8 | `POST /v1/outflows` in the OpenAPI spec; multispend path fix | Before production | [ ] |
| H4, H5, H7, H9 | Bulk payouts, structured transfer metadata, parked-credit signal, non-NGN limits | Later | [ ] H9 (non-NGN limits) dropped from the 23 October page: nothing on Taskiem needs it yet |
| P1 | KYC tier raised for the pilot company's wallet, limits confirmed by compliance | Before production | [ ] Not needed for the beta pilot (N1 fits TIER_1); needed for the first real customer |
| P2, P3 | Production client keys (`taskiem`, `ispend`); production webhook subscriptions | Before production | [~] Keys asked. Subscriptions: Taskiem makes them when the production connection is made |
| A1 | The open questions of the 27 October guide: the `account_name` length limit; whether iswallet adds a fee of its own on deposits; whether a business may move from pooled to segregated later; the sweep at volume; whether the reversal event carries the credit's `provider_reference` and `virtual_account` (Taskiem maps it so) | Before production | [ ] Not blocking; [iswallet account numbers](spec/iswallet-account-numbers.md) §6 |
| P4 | Allow-list our egress IPs, if they allow-list callers | Before production | [x] Not needed: no allow-list today. They want our IPs for incident forensics |
| P5 | On-call contact for payout incidents | Before production | [ ] They will name a technical contact and escalation path |
| M1, M2 | Wallet-scoped keys (M1, still asked as later); per-wallet webhook subscriptions (M2, dropped: Taskiem routes by `wallet_id`) | Later | [ ] |
| M3 | Confirm every outflow, credit and transfer event carries the `wallet_id` it concerns, and which side for transfers | Before production | [~] Credit and transfer confirmed (envelope `wallet_id`; one transfer event per side). Outflow events to confirm |
| M4 | KYC tier per company wallet as each Payrolla company is onboarded | — | [x] Withdrawn (decision 0044); replaced by P1, the tier process for any business paying staff through iswallet |
| Q1–Q4 | `wallet.credit.reversed`/`rejected` examples; when H1 and H2 ship; reversal timing after confirmation; maintenance windows | Answer | [~] H1 shipped, H2 next. The rest open |

| Id | Needed from iSpend engineering | When | Status |
| --- | --- | --- | --- |
| S1 | Build `POST {ispend_api_url}/internal/wallet-credit-exceptions` honouring `Idempotency-Key` | Now | [ ] |
| S2 | `ispend_api_url` (sandbox and production) and a bearer token, delivered securely | Now | [ ] |
| S3 | Confirm the credit-exception workflow | Now | [ ] |

## Xenibase

Xenibase (formerly PGDock). Phase 1 is built from its published contracts ([plan](xenibase-integration.md), [guide](integrations/xenibase.md)), including the write actions on the V4 data API. Xenibase's reply of October 2026 (`docs/integrations/taskiem-reply.md` in its repository) answers the items below; ticked on 17 October 2026.

| Id | Needed from Xenibase | When | Status |
| --- | --- | --- | --- |
| S1 | Sandbox organisation, project-restricted token (read and write), cloud API base URL | Now | [ ] |
| S2 | Signed sample deliveries (insert, update, delete, test, truncated) | Now | [x] `docs/integrations/fixtures/webhooks` in the Xenibase repository, checked in its CI |
| S3 | A contact for contract questions | Now | [ ] |
| P1-G1 | Parameterised SQL or row-write endpoints | Needed for write actions | [x] The V4 data API; no SQL endpoint will be added |
| P1-G3 | Webhook management API frozen as a versioned contract | Needed | [x] `docs/webhooks.md`, with `description` and `metadata` |
| P1-G4 | Error code list with retryability | Needed | [x] `docs/errors.md` |
| P1-G2 | `Idempotency-Key` on writes | Wanted | [x] On every data API write, 24 hours |
| C1 | Publish the V3 and V4 contracts (data API, auth, storage, billing events) | Needed for phases 2–4 | [~] Data API, auth, storage and paused projects published; no billing events yet |
| Q7–Q28 | Thirteen questions on the request page, section 3 | Answer | [x] Answered; recorded in the plan's section 5 |
| P2-G1, P3-G1/G2, P4 | OAuth consent, data API, project creation by token, contract tests in their CI | Later | [~] Data API and project creation by token exist; OAuth consent and the dedicated-instance role bootstrap are built on the Xenibase branch `claude/nigeria-workflow-automation-strategy-uoddh0`; contract tests in their CI once the `xenibase@1` suite is sent |

## What partners are waiting on from us

| Item | For | Blocked by |
| --- | --- | --- |
| ~~A Taskiem staging deployment with a public URL~~ | Everyone | Done: https://staging.ispend.name.ng. Webhook URLs are `https://staging.ispend.name.ng/hooks/<tenant id>/connectors/<connector>@1/<trigger>?env=prod&connection=<name>`, shown exactly on each workflow's Endpoints tab |
| The names of Taskiem people who need Payrolla's test company (P4), and our embedding origins (P5) | Payrolla's recordings and the pilot | Nothing; send with the next Payrolla message |
| Webhook URLs per product and environment | iswallet W3, P3; Payrolla's events (P2) | Staging: a tenant and a deployed workflow on staging for each, then its Endpoints tab. Production later |
| Fixed egress IPs | Xenibase (if they allow-list); iswallet for forensics only | Cloud provider and NAT (P4-C1, P4-C5) |
| Telling iswallet that Taskiem no longer pays for Payrolla, and Run payroll's expected figures | iswallet H3, P1 | Nothing; send with the next iswallet message |
| A sending domain for invitation emails | Company people joining their sub-tenants | P4-O1 |
| Running the sandbox tests with each partner | iswallet W4, Payrolla P4, Xenibase S1 | The partners' sandbox items above |
