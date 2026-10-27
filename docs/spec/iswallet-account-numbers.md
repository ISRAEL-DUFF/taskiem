# iswallet account numbers: the plan

Status: **Draft** 27 October 2026, extended the same day with workstream W (Taskiem as a collection rail, [decision 0052](../decisions/0052-taskiem-as-a-collection-rail.md)) · Source: iswallet's guide of 27 October ([kept verbatim](../integrations/iswallet-answers-2026-10-27-account-numbers.md)) · Connector: [iswallet](../integrations/iswallet.md) · Rail facts: [schools](schools-rail-facts.md#iswallet) · Decisions: [0046](../decisions/0046-balans-in-taskiem.md) (students' own account numbers), [0045](../decisions/0045-tally-in-taskiem.md) (customers' accounts, the money log)

## 1. Summary

iswallet's guide of 27 October answers what the rail facts (B0) had to leave open: the credit webhook's fields, gross against net, idempotent issuing, naming, the batch, and the school model of one wallet with one permanent account per student. Nothing in it changes Taskiem's design: the record, the matching rule, the one payment path and the schools' batch jobs stay as built. What changes is the connector and what sits around it:

1. iswallet credits reach the money log **from the webhook, in seconds**, instead of only from the 48-hour look-back, up to 15 minutes late.
2. Single issuing uses the documented single-account call; the batch stays for rolls; one-time accounts are refused on iswallet.
3. Taskiem can **create a school's wallet** from the Schools settings instead of asking the bursar to paste a wallet id.
4. Every iswallet call keeps its `X-Request-ID`, and the two failure kinds the guide distinguishes (final against unknown) are told apart.
5. A connection check that proves the key works and names the partners that can issue.
6. **Taskiem subscribes the webhook itself**, one subscription per connection and environment, the signing secret kept as a platform secret: there is no iswallet dashboard to paste a URL into (iswallet, 27 October, [addendum](../integrations/iswallet-answers-2026-10-27-account-numbers.md#addendum-later-the-same-day-the-webhook-endpoint)).

Two workstreams: **I** (I1 to I7, the connector, one PR) and **W** (Taskiem as a collection rail through its own iswallet client, [decision 0052](../decisions/0052-taskiem-as-a-collection-rail.md), one PR per milestone). Things that need people, not code, are in §6.

## 2. What the guide settles

| Question in the rail facts | Answer (27 October) | Effect |
| --- | --- | --- |
| Which fields of `wallet.credit.posted` name the account | `data.virtual_account.reference` and `.account_number`; `wallet_id` at the top level | The webhook can be mapped to `virtual_account.credited` (I1) |
| Gross or net | `data.amount` is what the payer sent; `data.amount_credited` what landed after the partner's fee, passed to the customer by default | Count `amount` (§7.4 of the schools plan), settle `amount_credited`, fee is the difference |
| Is single issuing idempotent | Yes, on `reference`; the batch too, per item | The key per student holds; retries are free; the single call is preferred for one account (I2) |
| Naming | `account_name` is what the payer's bank shows at resolution | The rail-facts worry closes; the 31-character cap stays until a limit is stated |
| One wallet, many accounts | Supported, no practical cap, every credit carries its reference | B4's design holds; an earlier version overwrote silently, which is why a reference is always sent |
| One-time accounts | Not needed; not idempotent; expiry advisory | Refused on iswallet (I2); invoices on iswallet collect through the customer's permanent account |
| The batch | 500 a call, per-item `created`/`existing`/`failed`, permanent only, one partner per batch | The batch jobs' page size stays below 500; per-item results are already read |
| KYC before issuing | `email` on every wallet; a director's BVN on the two business types; `422 KYC_REQUIRED` names the field | The readiness note names the field (I5) |
| Failure kinds | `502 PROVIDER_REJECTED` is final, nothing created; `500 INTERNAL_ERROR` is unknown, the account may exist | Map to fatal and unknown outcome (I4) |
| Wallet creation | Flat identity fields, never `kyc_profile`; `Idempotency-Key` required; `owner_type`; the response's `account_number` is **not payable**, read `internal_reference` | The wallet action (I3) |
| The webhook endpoint | Ours: given to iswallet in `endpoint_url` on `POST /v1/webhooks/subscriptions`, changed with `PATCH`; one subscription per environment, each with its own secret returned once; https and publicly resolvable (private or loopback refused, `400 INVALID_ENDPOINT_URL`); answer 2xx within 30 s and work afterwards; 10 retries then dead-letter; `GET …/subscriptions/{id}/deliveries` shows each attempt | Remote registration under decision 0021 (I7); ingest already answers 202 at once |
| Signature | `HMAC-SHA256(secret, timestamp + "." + rawBody)`, `X-iSpend-Signature`, 300 s tolerance; retries carry `X-iSpend-Idempotency-Key` | Already the manifest's scheme; add the idempotency header to dedup (I1) |

## 3. Design

### 3.1 The credit event (I1)

`credit_event` already receives `wallet.credit.posted`, `.reversed` and `.rejected` with the right signature scheme and routes by `wallet_id`. It gains the capability mapping:

- `virtual_account.credited` from `wallet.credit.posted` when `data.virtual_account.reference` is present: `reference`, `account_number`, `credit_id` (the transaction id iswallet sends; the guide's example omits it, the OpenAPI names `txn_id` and `provider_reference`: `provider_reference` when present, else `txn_id`, the same id the look-back uses so a credit found both ways counts once), `amount_kobo` = `data.amount`, `settled_kobo` = `data.amount_credited`, `fee_kobo` = their difference, `currency`, `occurred_at`. Payer name and account are not in the example; mapped as optional if the OpenAPI shows them (`counterparty`, as the transaction list has).
- Dedup on `X-iSpend-Idempotency-Key` as well as the movement key already used, so a redelivery is one event.
- `wallet.credit.reversed` maps to the reversal the money log already handles (B4), keyed by the same `credit_id`.
- The look-back stays as the safety net: a credit the webhook already recorded is found and counted once by its `credit_id`.

### 3.2 Issuing (I2)

- `issue_virtual_account` (one account) calls `POST /v1/wallets/{id}/virtual-account` with `type: permanent`, `reference`, `account_name`, optional `provider`; the batch action keeps `POST …/virtual-accounts/batch` for the schools' jobs (page size stays under 500). Both idempotent on the reference; output unchanged (`created`/`existing` from the status).
- The capability's `requires` and `ignores` are unchanged (`account` is the wallet; email, phone, BVN and NIN belong to the wallet, not the account).
- `collect_transfer` (a one-time account per invoice) is **not** offered on iswallet: the manifest maps no dynamic account, and an invoice to a customer who has a permanent account on an iswallet wallet collects through that account (the permanent-account rule, B1). The invoice page says so.

### 3.3 The school's wallet (I3)

- A new action `create_wallet`: `owner_type` (`client_end_user`, `client_end_user_business`, `client_end_business`), `owner_ref` (Taskiem's key, `tenant:<id>` for a business's own wallet), `currency`, flat identity fields (`email` or `business_email`, `bvn`, names) and the required `Idempotency-Key` from the action's idempotency key; output `wallet_id`, `internal_reference`, `tier`. The response's `account_number` is dropped on purpose and documented as not payable.
- Schools > Settings > Account numbers: beside "paste the wallet id", **Create the school's wallet** on the chosen iswallet connection, asking for the owner type (default `client_end_user_business`, with the guide's words on where the money sits) and the director's BVN and the business email, both sealed as PII and sent once. The wallet id lands in `issuing_account`. Audited.
- For businesses that are not schools, the same action is available to workflows and from the invoices' customer-account form's "Issue one with" when the connection has no wallet yet.

### 3.4 Request ids and failure kinds (I4)

- Every iswallet response's `X-Request-ID` is kept on the connector call record and shown on a failed step and in the money log's held credits, so a report to iswallet carries it.
- `502 PROVIDER_REJECTED` is fatal with iswallet's reason; `500 INTERNAL_ERROR` is an unknown outcome (the step parks for a person, as the engine does today; for an issue, the next attempt with the same reference is safe because issuing is idempotent, so the batch jobs retry it rather than park).

### 3.5 The connection check (I5)

- A read action `list_providers` (`GET /v1/providers`) and a readiness check on an iswallet connection: the key works, and these partners can issue permanent accounts for NGN. Shown on the connection page and used by the Schools settings before a batch is planned.
- Readiness notes: a business wallet without its director's BVN gets "iswallet needs the director's BVN before it can issue account numbers" from the `KYC_REQUIRED` message's field.

### 3.6 Subscribing ourselves (I7)

iswallet has no dashboard where a business pastes a URL: the subscription is made over the API, and its signing secret is returned once. This is the case [decision 0021](../decisions/0021-remote-trigger-registration.md) was built for (Xenibase), so iswallet's three webhook triggers (`credit_event`, `outflow_event`, `transfer_event`) declare `registration: remote` and the connector supplies a `Registrar`:

- **Create**: `POST /v1/webhooks/subscriptions` with `endpoint_url` (the ingest URL Taskiem registers, which names the subscription row: `…/hooks/…?env=&connection=&subscription=`) and the trigger's `event_types` exactly (no wildcards); the `signing_secret` goes to the tenant's vault under the platform environment `_remote`, never returned by the API. **Update**: `PATCH` the same subscription when the hooks URL or the events change. **Find by name**: iswallet subscriptions carry no name, so the reconciler finds one by its `endpoint_url`, which carries the row id, before creating (crash safety by name, 0021 point 4). **Delete** on undeploy. **Rotate**: not documented; absent until iswallet says.
- **Drift**: the 10-minute check reads the subscription and its last deliveries (`GET …/subscriptions/{id}/deliveries`), records `healthy`, `failing` (recent non-2xx answers from us) or `missing`, and shows iswallet's status code and our response body for the last failed attempt on the connection page, so "we never got the webhook" is answered without asking iswallet.
- **One subscription per connection and environment** (prod and staging each get their own secret), as the page already says; a partner's shared client keeps one subscription at the partner, routed by `wallet_id` as today (decision 0031).
- The manual path (paste `webhook_secret`) stays for a connection whose subscription was made outside Taskiem; `webhook_secret` becomes optional, and the connection page says which of the two is in force.
- Taskiem's side already answers `202` at once and does the work afterwards; the hooks host must be https and public: `TASKIEM_HOOKS_URL` on staging and production, which the readiness check (I5) names when missing.

### 3.7 Taskiem as a collection rail (workstream W)

iswallet, Payrolla and Taskiem are one company's products, and one iswallet client may hold wallets for many unrelated businesses. So Taskiem holds a client of its own and gives any business a wallet under it ([decision 0052](../decisions/0052-taskiem-as-a-collection-rail.md)): a business with no Paystack, Flutterwave, Moniepoint or OPay account receives money from day one, and never sees iswallet.

- **The platform client (W1).** `TASKIEM_ISWALLET_CLIENT_KEY` and `_TEST_CLIENT_KEY` as platform secrets on the `api` and `scheduler` roles; the platform's one subscription per environment registered by I7 at the platform's own URL (`/hooks/platform/iswallet?env=`), verified with its secret, and each event delivered into the tenant whose `iswallet/taskiem` connection lists the envelope's `wallet_id` (decision 0031's routing, with the platform in the partner's place); an event for a wallet no tenant lists is recorded and alerted to operators. A business's `iswallet/taskiem` connection carries no key: calls use the platform's, and the allow-list is its wallet (so `WALLET_NOT_ALLOWED` protects every other business, as today).
- **Turning it on (W2).** Settings > Payments (and the Schools issuing setting, and the Start page for a business with no rail): **Receive money with Taskiem**. It asks what the business is (holding other people's money, such as fees, or only its own), explains segregated against pooled in plain words with the sweep's lag, takes the business email and, for a business wallet, the director's BVN (sealed PII, sent once, never shown again), then `create_wallet` (I3) with `owner_ref = tenant:<id>`, the chosen `owner_type` and an idempotency key per tenant, so a retry never makes a second wallet. The connection `iswallet/taskiem` is made with the wallet listed. Readiness names what is missing (`KYC_REQUIRED`'s field). An owner may change segregated or pooled only by asking (it is not reversible at iswallet without them).
- **Collection (W3).** The wallet's own number (its single unreferenced account, `virtual_account` without a reference) is the business's "pay me" number: on the Money page, in chat (`my account number`), on the storefront's pay-by-transfer, counted by the catch-all matching. An invoice sent on this rail gives its customer a permanent number on first send (`register_account`, I2's path), so the invoice's page and the WhatsApp **Pay by transfer** show that number instead of a one-time one; the page's wording changes to "pay to your own account number, any time".
- **Balance and withdrawal (W4).** The wallet's balance (`get_balance`, `available`) on the Money page and in the daily summary; **Withdraw to my bank**: the business's bank account resolved by `name_enquiry` and remembered, an approval policy on by default (the owner approves, four-eyes above a threshold the owner sets), the payout through the existing `payout` action with its outcome from `outflow_event`, in the money log as any payout. A template `withdraw-to-bank` (weekly, or when the balance passes an amount).
- **Fees and plans (W5).** The partner fee passed to the payer by default, shown on receipts as sent against landed; billing meters credits and payouts on the platform rail; which plans include it and any Taskiem margin are settings the founder sets (`deploy/plans.yaml`).
- **Operations and docs (W6).** Runbook: provisioning the platform client live and test, rotating its key, the subscription's health, unrouted events; the guide for business owners ("Receive money with Taskiem"); the Schools guide's issuing section; decision 0052 accepted.

## 4. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| I1 | The credit webhook mapped: `virtual_account.credited` and the reversal, dedup on the idempotency header, the look-back as the net | S | A signed `wallet.credit.posted` fixture counts on a student's invoice within the tick, once, with gross counted and the fee shown; the look-back finds it and adds nothing; a reversal reopens the invoice and voids the receipt |
| I2 | Issuing on the single-account call; the batch kept; no one-time accounts on iswallet | S | Conformance fixtures for both calls; an invoice to an iswallet customer offers their permanent account, never a one-time one |
| I3 | `create_wallet` and "Create the school's wallet" | M | A school's wallet is created from settings with the BVN sent once and sealed; a retry creates no second wallet; the wallet-level `account_number` appears nowhere |
| I4 | Request ids kept and shown; final against unknown failures | S | A failed step and a held credit show the id; a `502` fails fatally with the reason; a `500` parks, and an issue retries safely |
| I5 | `list_providers` and the connection check; the BVN readiness note | S | The connection page says the key works and which partners issue; a business wallet without a BVN shows the note |
| I6 | Docs: the iswallet page, the rail facts (iswallet rows closed), the partner tracker (what iswallet still needs), the schools open items (iswallet rows closed), the Schools guide (creating the wallet) | S | `docsite -check` passes; the open items no longer say the webhook is unmapped |
| I7 | iswallet's triggers registered remotely (decision 0021): create, update, find by endpoint, delete, drift with the deliveries log; the secret a platform secret; the manual secret optional | M | Connecting iswallet on staging makes the subscription with no pasting; a signed delivery to the registered URL starts the credit; the connection page shows the subscription's health and the last failed attempt; undeploy removes it |

Order: I1, I2, I4, I5, I7, I3, I6. One PR.

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| W1 | The platform client: secrets, the platform subscription and its routing into tenants, `iswallet/taskiem` connections with no key and one wallet each | M | A credit to a tenant's wallet arrives at the platform URL and lands in that tenant's money log and nowhere else; a wallet no tenant lists is alerted; a tenant's step naming another wallet is refused before any request |
| W2 | Turning it on: the choice of segregated or pooled, email and BVN sealed and sent once, the wallet created idempotently, readiness | M | A new business with no rail turns it on from the Start page and has a wallet and a connection; a retry makes no second wallet; the BVN is never readable again; the Schools setting offers "Taskiem" |
| W3 | Collection: the business's own number, a permanent number per invoiced customer, the invoice page and chat wording on this rail | M | An invoice sent on this rail is payable by transfer to the customer's own number from the page and from WhatsApp; a transfer to the business's own number reaches To match |
| W4 | Balance and withdrawal: the balance shown, Withdraw to my bank under approval, the template | M | An owner withdraws to a resolved bank account with an approval, the payout and its outcome are in the money log, the balance follows |
| W5 | Fees and plans: metering, plan gating, the fee shown | S | Billing counts credits and payouts on the platform rail; a plan without the rail says so on the Start page |
| W6 | Operations and docs; decision 0052 accepted | S | Runbook, owner's guide, Schools guide; `docsite -check` passes |

Order: W1, W2, W3, W4, W5, W6, after the I workstream has merged (W1 and W2 build on I3, I5 and I7). One PR per milestone.

## 5. Testing

| Level | What | Where |
| --- | --- | --- |
| Conformance | The single and batch issue calls, `create_wallet`, `list_providers` against fixtures written from the guide; the credit webhook fixture with its signature | `engine/conntest`, `connectors/iswallet/testdata` |
| Capability | `virtual_account.credited` from the fixture: gross, settled, fee, reference, account number, credit id; the reversal | `connectors/iswallet` |
| Exactly once | The webhook and the look-back reporting the same credit count once; a redelivery with the same idempotency header is one event | `engine/lookback`, `engine/ingest` |
| Schools | A student's account issued on the single call; a batch job over the batch call with `existing` items; the wallet created from settings | `engine/schools`, `api` |
| Remote registration | Create, find by endpoint, update, delete and drift against a fake iswallet (`engine/remote` tests as Xenibase's); the secret never in an API answer | `engine/remote`, `connectors/iswallet` |
| Web | Settings: create the wallet, the readiness line, the BVN note; the connection page's subscription health | Playwright |
| The platform rail | A credit routed into the right tenant and only it; turning the rail on from scratch; an invoice paid to a customer's permanent number; a withdrawal under approval; a plan without the rail | `api`, Playwright |

## 6. Needs people, not code

| Item | Who | Status |
| --- | --- | --- |
| A public https hooks host on staging and production (`TASKIEM_HOOKS_URL`), so Taskiem's own subscriptions are accepted (private or loopback addresses are refused) | Founder (operations) | Open; nothing to give iswallet by hand once I7 is built |
| Which `owner_type` the schools are. `client_end_user_business` means per-student credits reach the school's segregated account by iswallet's sweep (alarm at 10 minutes) while the balance is right at once; the alternative, one unreferenced number per school, loses per-student attribution | Founder, with iswallet | Open; the plan assumes `client_end_user_business` and eventual segregation |
| Expected enrolment volume, so iswallet can say whether 500 a call needs paging on our side (our jobs page below that already) | Founder | Open |
| Sandbox key and base URL on the staging connection, then the pilot checklist's issuing step run for real | Founder, then engineering | Open |
| The `account_name` length limit and whether iswallet adds a fee of its own on deposits | iswallet | Still unanswered; not blocking |
| Taskiem's own iswallet client, live and test, provisioned by iswallet; its keys into the platform secrets | Founder (iSpend) | Open; needed for W1 |
| Whether a business may move from pooled to segregated later, and the sweep's behaviour at volume | iswallet | Open; W2 says the choice is made once until answered |
| Which plans include the platform rail, and whether Taskiem charges a margin over the partner fee | Founder | Open; W5 |

## 7. Out of scope

FX and crypto (the guide's own scope); payouts beyond the business withdrawing its own balance (payroll stays Payrolla's and the payout templates'); one-time accounts on iswallet; changing where a school's money is held (iswallet's side); a ledger (0046).
