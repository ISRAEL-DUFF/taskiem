# Partner tracker: what is still needed

Last updated 14 October 2026, after iswallet's answers to PGDock. One place for what is outstanding with each partner, and what each one is waiting on from us. Every item has an id that matches the request page or guide where it was asked, so replies can be ticked off here.

The request pages sent to partners:

| Partner | Page | Last sent |
| --- | --- | --- |
| Payrolla | [Payrolla integration: our answers, what's left, and the guide](https://claude.ai/artifact/QqY5U7DNXwAYVXq2hWZGDt) | 14 October 2026, answering Payrolla's reply |
| iswallet (and iSpend) | [What Taskiem needs from iswallet to go live](https://claude.ai/artifact/QkzDpSN1V75TozwWs73Tpo) | 9 October 2026, plus section 3a on 14 October |
| PGDock | [What Taskiem needs from PGDock](https://claude.ai/artifact/KjzURqhiLmdUsBttbXk7to) | 9 October 2026 |

Keys, tokens and signing secrets never travel in email, chat, tickets or these pages: the holder enters them on Taskiem's Connections or Secrets page, or we agree a one-time password-manager share.

## Payrolla

Payrolla's side is built and tested against a stub of our webhook. Its reply of October 2026 agreed one Taskiem sub-tenant per customer company; we built onboarding by API, the shared-client guard, reversal notices and report totals ([guide](integrations/payrolla.md), decisions 0030 and 0031).

| Id | Needed from Payrolla | When | Status |
| --- | --- | --- | --- |
| N1 | Pilot company's payroll: headcount, total net value, how many paid to bank accounts, run dates; and Payrolla's largest payroll across customers. iswallet needs these for the wallet tier and rate limit | Now | [x] 14 October: Payrolla is in beta with no production customer yet. The sandbox pilot (Chuks Electronics) pays 2 people, both to bank accounts, ₦3,000 net in all; that is also Payrolla's largest payroll. Run dates not fixed. A new TIER_1 wallet (₦50,000 per payout and per day) and 100 requests a minute cover it, so no tier or rate-limit raise is needed until a real customer is onboarded |
| N2 | Peter Chuks's full email; approvers so far: Peter Chuks (eazidefi@gmail.com), John Chuks (eazidefi+approver@gmail.com) | Now | [x] 14 October |
| N3 | Each onboarded company's iswallet wallets it pays from (at least its funding wallet), for its connection's `wallet_ids`; for sandbox, the test company's wallets | Now | [ ] |
| N4 | Who at Payrolla receives Taskiem's partner API key | Now | [ ] |
| N5 | Build `POST {payrolla_api_url}/payrolls/{payroll_id}/reversal-notice` to the payload in the guide | Now | [ ] |
| N6 | Pilot company's production `funding_wallet_id` (R6), start date, on-call contact for payroll days (T2) | Before production | [ ] The sandbox pilot company is Chuks Electronics; the first production customer is not chosen yet |
| N7 | Confirm 35 days of watching for reversals is enough (settable per company, 1–60) | Confirm | [ ] |
| E1 | Embedding the builder: after the payroll pilot | Later | Deferred by Payrolla |

Known from Payrolla's reply: report base URL `https://synledger.name.ng/payrolla/api/v1/integrations/taskiem` (sandbox; the reply said `/payroller`, but staging serves Payrolla under `/payrolla`, checked 15 October: `/payrolla/health` answers ok and the integration path answers 401 without a token) and `https://ispend.name.ng/payroller/api/v1/integrations/taskiem` (production, as the reply gave it; confirm whether it moved to `/payrolla` too), one bearer token per company issued in Payrolla and entered by the company's account holder; re-sends use `{run_id}-r2`, `-r3`; payrolls are sent once locked.

## iswallet (and iSpend)

The connector is built on iswallet's answers of 5 October ([iswallet](integrations/iswallet.md), [answers](integrations/iswallet-answers-2026-10-05.md)). Their answers to PGDock of 12–13 October ([kept here](integrations/iswallet-answers-2026-10-12-pgdock.md)) were passed to us on 14 October: they do not answer our request page, but they settle H1, P4 and most of W4, and the connector now uses the lookup by key, `X-RateLimit-Reset` and per-side transfer dedup. Their FX, consent and billing questions are PGDock's.

| Id | Needed from iswallet | When | Status |
| --- | --- | --- | --- |
| W1 | Sandbox clients `payrolla` and `ispend` provisioned (Payrolla: ₦10m funded company wallet and 10 employee wallets; iSpend: a funded wallet with a virtual account) | Now | [ ] They provision in one call (`POST /v1/admin/clients/{client}/sandbox/provision`); we have to ask. iSpend's virtual account needs a director's BVN on a business wallet |
| W2 | Sandbox client keys, delivered securely | Now | [ ] |
| W3 | Sandbox webhook subscriptions for our URLs (one per product), signing secrets delivered securely | Now, once our staging is up | [ ] |
| W4 | How to force each sandbox outcome: payout confirmed, failed, reversed after confirmation, unresolved; credit rejected and reversed | Now | [~] Simulators for deposit, payout confirmed/failed/reversed and credit clawback; failed debits are real refusals. Still open: payout `unresolved`, credit `rejected` |
| H1 | Lookup by our idempotency key | Before production | [x] Shipped 12 October (`GET /v1/requests/{key}`); used by the connector |
| H2 | Typed duplicate error instead of the 500, and the replay window | Before production | [ ] Now their top item; no longer blocks us (the lookup comes first) |
| H3 | `Retry-After` on 429; higher rate limit for `payrolla` | Before production | [~] No `Retry-After` yet (on their list); we use `X-RateLimit-Reset`. No limit raise needed during Payrolla's beta (N1); ask again with the first real customer's headcount |
| H6 | Error catalogue per endpoint, with retryability | Before production | [~] Debit codes given to PGDock match our handling; a full catalogue per endpoint still open |
| H8 | `POST /v1/outflows` in the OpenAPI spec; multispend path fix | Before production | [ ] |
| H4, H5, H7, H9 | Bulk payouts, structured transfer metadata, parked-credit signal, non-NGN limits | Later | [ ] |
| P1 | KYC tier raised for the pilot company's wallet, limits confirmed by compliance | Before production | [ ] Not needed for the beta pilot (N1 fits TIER_1); needed for the first real customer |
| P2, P3 | Production client keys; production webhook subscriptions and secrets | Before production | [ ] |
| P4 | Allow-list our egress IPs, if they allow-list callers | Before production | [x] Not needed: no allow-list today. They want our IPs for incident forensics |
| P5 | On-call contact for payout incidents | Before production | [ ] They will name a technical contact and escalation path |
| M1, M2 | Wallet-scoped keys; per-wallet webhook subscriptions (nice to have; our guard does not depend on them) | Later | [ ] |
| M3 | Confirm every outflow, credit and transfer event carries the `wallet_id` it concerns, and which side for transfers | Before production | [~] Credit and transfer confirmed (envelope `wallet_id`; one transfer event per side). Outflow events to confirm |
| M4 | KYC tier per company wallet as each Payrolla company is onboarded: process and lead time | Before production | [ ] |
| Q1–Q4 | `wallet.credit.reversed`/`rejected` examples; when H1 and H2 ship; reversal timing after confirmation; maintenance windows | Answer | [~] H1 shipped, H2 next. The rest open |

| Id | Needed from iSpend engineering | When | Status |
| --- | --- | --- | --- |
| S1 | Build `POST {ispend_api_url}/internal/wallet-credit-exceptions` honouring `Idempotency-Key` | Now | [ ] |
| S2 | `ispend_api_url` (sandbox and production) and a bearer token, delivered securely | Now | [ ] |
| S3 | Confirm the credit-exception workflow | Now | [ ] |

## PGDock

Phase 1 is built from PGDock's published contracts ([plan](pgdock-integration.md), [guide](integrations/pgdock.md)). Write actions wait on P1-G1.

| Id | Needed from PGDock | When | Status |
| --- | --- | --- | --- |
| S1 | Sandbox organisation, project-restricted token (read and write), cloud API base URL | Now | [ ] |
| S2 | Signed sample deliveries (insert, update, delete, test, truncated) | Now | [ ] |
| S3 | A contact for contract questions | Now | [ ] |
| P1-G1 | Parameterised SQL or row-write endpoints | Needed for write actions | [ ] |
| P1-G3 | Webhook management API frozen as a versioned contract | Needed | [ ] |
| P1-G4 | Error code list with retryability | Needed | [ ] |
| P1-G2 | `Idempotency-Key` on writes | Wanted | [ ] |
| C1 | Publish the V3 and V4 contracts (data API, auth, storage, billing events) | Needed for phases 2–4 | [ ] |
| Q7–Q28 | Thirteen questions on the request page, section 3 | Answer | [ ] |
| P2-G1, P3-G1/G2, P4 | OAuth consent, data API, project creation by token, contract tests in their CI | Later | [ ] |

## What partners are waiting on from us

| Item | For | Blocked by |
| --- | --- | --- |
| A Taskiem staging deployment with a public URL | Everyone: webhook URLs for Payrolla, iswallet subscriptions, PGDock testing | Infrastructure ([needs people](needs-people.md) D5, P4-C1) |
| Payrolla's partner tenant and partner API key | Payrolla onboarding (N4) | Staging |
| Webhook URLs per product and environment | iswallet W3, P3; Payrolla per company | Staging, then production |
| Fixed egress IPs | PGDock (if they allow-list); iswallet for forensics only | Cloud provider and NAT (P4-C1, P4-C5) |
| Telling iswallet Payrolla's figures: beta only, 2 people and ₦3,000, so no tier or limit raise yet | iswallet H3, P1 | Nothing: N1 answered; send with the next iswallet message |
| A sending domain for invitation emails | Company people joining their sub-tenants | P4-O1 |
| Running the sandbox tests with each partner | Payrolla T1, iswallet W4, PGDock S1 | Staging and the sandbox items above |
