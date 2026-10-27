# iswallet account numbers: the plan

Status: workstreams I and W **built** 27 October 2026 ([§8](#8-built-workstream-i), [§9](#9-built-workstream-w)); what W left out is planned as workstream N ([§10](#10-next-what-w-left-out-workstream-n)). Drafted 27 October 2026, extended the same day with workstream W (Taskiem as a collection rail, [decision 0052](../decisions/0052-taskiem-as-a-collection-rail.md)) · Source: iswallet's guide of 27 October ([kept verbatim](../integrations/iswallet-answers-2026-10-27-account-numbers.md)) · Connector: [iswallet](../integrations/iswallet.md) · Rail facts: [schools](schools-rail-facts.md#iswallet) · Decisions: [0046](../decisions/0046-balans-in-taskiem.md) (students' own account numbers), [0045](../decisions/0045-tally-in-taskiem.md) (customers' accounts, the money log)

## 1. Summary

iswallet's guide of 27 October answers what the rail facts (B0) had to leave open: the credit webhook's fields, gross against net, idempotent issuing, naming, the batch, and the school model of one wallet with one permanent account per student. Nothing in it changes Taskiem's design: the record, the matching rule, the one payment path and the schools' batch jobs stay as built. What changes is the connector and what sits around it:

1. iswallet credits reach the money log **from the webhook, in seconds**, instead of only from the 48-hour look-back, up to 15 minutes late.
2. Single issuing uses the documented single-account call; the batch stays for rolls; **one-time accounts serve invoices**, the default for a customer billed once, with their caveats handled.
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
| One-time accounts | Not needed *for schools*; offered as `type: dynamic` with `amount` required; not idempotent; expiry advisory | Mapped to the invoices' one-time path (I2): the default for a customer billed once; Taskiem claims the issue first, treats an ambiguous failure as two numbers, enforces the window itself |
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
- `collect_transfer` (a one-time account per invoice, the default for invoices on every rail) maps to `POST /v1/wallets/{id}/virtual-account` with `type: dynamic`, `amount` (the amount due, in kobo), `reference` (Taskiem's one-time key, with its prefix) and `account_name`; output the number, bank, name and `expires_at`. Because the call is not idempotent, the action is marked unsafe: the invoice path records the attempt before calling and an ambiguous failure (`500`, a timeout) parks for a person rather than retrying, as it does for Flutterwave; `502` is final and the page offers to ask again. Expiry is advisory at iswallet, so Taskiem's own "valid until" governs, and a late transfer the provider accepts is counted, as today. A customer who has a permanent number on the business's wallet pays into that instead, as the permanent-account rule already says.
- The credit webhook tells the two apart by the reference: a one-time reference carries Taskiem's prefix and maps to `payment.received` for its invoice (as Paystack's `charge.success` without a dedicated account does); a customer's key maps to `virtual_account.credited` (I1).

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
- **Collection (W3).** The wallet's own number (its single unreferenced account, `virtual_account` without a reference) is the business's "pay me" number: on the Money page, in chat (`my account number`), on the storefront's pay-by-transfer, counted by the catch-all matching. Invoices on this rail work as on every rail: a **one-time number per invoice by default** (I2), since many customers are billed once; a permanent number per customer when the business gives one (opt-in, for regulars) and always for a school's students. The invoice page and the WhatsApp **Pay by transfer** show whichever applies.
- **Balance and withdrawal (W4).** The wallet's balance (`get_balance`, `available`) on the Money page and in the daily summary; **Withdraw to my bank**: the business's bank account resolved by `name_enquiry` and remembered, an approval policy on by default (the owner approves, four-eyes above a threshold the owner sets), the payout through the existing `payout` action with its outcome from `outflow_event`, in the money log as any payout. A template `withdraw-to-bank` (weekly, or when the balance passes an amount).
- **Fees and plans (W5).** The partner fee passed to the payer by default, shown on receipts as sent against landed; billing meters credits and payouts on the platform rail; which plans include it and any Taskiem margin are settings the founder sets (`deploy/plans.yaml`).
- **Operations and docs (W6).** Runbook: provisioning the platform client live and test, rotating its key, the subscription's health, unrouted events; the guide for business owners ("Receive money with Taskiem"); the Schools guide's issuing section; decision 0052 accepted.

## 4. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| I1 | The credit webhook mapped: `virtual_account.credited` and the reversal, dedup on the idempotency header, the look-back as the net | S | A signed `wallet.credit.posted` fixture counts on a student's invoice within the tick, once, with gross counted and the fee shown; the look-back finds it and adds nothing; a reversal reopens the invoice and voids the receipt |
| I2 | Issuing on the single-account call; the batch kept; one-time accounts for invoices with their caveats | S | Conformance fixtures for all three calls; an invoice on an iswallet connection offers a one-time number with Taskiem's own expiry, or the customer's permanent number when they have one; an ambiguous failure parks and never issues twice by retry |
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
| W3 | Collection: the business's own number; invoices with one-time numbers by default and permanent ones opt-in; the page and chat wording on this rail | M | An invoice sent on this rail is payable by transfer to a one-time number from the page and from WhatsApp, or to the customer's own number when they have one; a transfer to the business's own number reaches To match |
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
| Taskiem's own iswallet client, live and test, provisioned by iswallet; its keys into the platform secrets, then `taskiem iswallet subscribe` per mode ([runbook](../runbooks/iswallet-platform.md)) | Founder (iSpend), then operations | Open; the code is built (§9) and waits on the keys |
| Whether a business may move from pooled to segregated later, and the sweep's behaviour at volume | iswallet | Open; W2 says the choice is made once until answered |
| Which plans include the platform rail, and whether Taskiem charges a margin over the partner fee | Founder | Open; W5 |
| What iswallet charges per payout, so the sweep leaves enough in the wallet (N2) | iswallet | Open; a platform setting, ₦100 by default, until answered |

## 7. Out of scope

FX and crypto (the guide's own scope); payouts beyond the business withdrawing its own balance (payroll stays Payrolla's and the payout templates'); changing where a school's money is held (iswallet's side); a ledger (0046).

## 8. Built: workstream I

Built 27 October 2026, in one PR. What differs from §3:

| Item | What exists | Code |
| --- | --- | --- |
| I1 | `wallet.credit.posted` with `data.virtual_account.reference` is `virtual_account.credited` (gross `amount`, `amount_credited` settled, the fee their difference); `credit_id` is `data.provider_reference` only, not `txn_id` as a fallback: the look-back reads `provider_reference`, so a credit counted under `txn_id` and then read back would be counted twice. An event without one is left to the look-back. `wallet.credit.reversed` is `payment.returned` naming the credit by its `provider_reference` (a one-time number's by its reference). Dedup: the movement (`txn_id`, else `provider_reference`), then `X-iSpend-Idempotency-Key`, then the envelope id. `credit_event` correlates on the account's reference | `connectors/iswallet/manifest.yaml` |
| I2 | `issue_virtual_account` on the single call; `issue_virtual_accounts` (the batch, up to 500) as its own action; the schools' jobs issue one at a time through `virtual_account`, idempotent per reference, as before. `collect_transfer` maps to `create_dynamic_account`: unsafe; its key is its reference, `tot_` and the invoice's key, so the payment id is recorded before iswallet is asked; it opens on the connection's one wallet (`wallet_ids` must list exactly one), since the capability takes no wallet; window 1 to 1,440 minutes, default 60 | `connectors/iswallet` |
| I3 | `create_wallet`; **Create the school's wallet** in Schools settings (`POST /v1/settings/schools/wallet`, owners): owner type, business email and the director's BVN sent once and kept nowhere in Taskiem (the audit holds neither), keyed by tenant and connection, the wallet then the issuing account. The invoices' "Issue one with" is not changed | `api/schools_wallet.go`, `web/src/pages/SchoolAccountParts.tsx` |
| I4 | `X-Request-ID` on every write's output (`request_id`) and in every failure's message, so a failed step shows it; `502 PROVIDER_REJECTED` fatal; `500` unknown. Held credits show none: a credit comes by webhook or look-back, not from a request of ours | `connectors/iswallet/iswallet.go`, `engine/connector/httpx.go` (`DoJSONHeader`) |
| I5 | `list_providers`, also the connection test; **Check** on Connections (`POST /v1/connections/{id}/check`) says whether the saved details work and which partners can issue in naira; `KYC_REQUIRED` reads "iswallet needs the director's BVN (or the wallet's email) before it can issue account numbers" wherever it surfaces | `api/admin.go`, `web/src/pages/Connections.tsx` |
| I7 | A new `registration: connection` (connector/v1 rule 8): one subscription per connection and trigger, made when the connection is, repaired by Check, removed with the connection before its credentials go; deliveries reach every subscribed workflow, the money log and a partner's routing; one without `subscription=` (made by hand) is verified with `webhook_secret`. The registrar's health reads the deliveries log and shows iswallet's record of our answer. A subscription found after a crash is deleted and made again (no rotation at iswallet). A shared client's connection makes none | migration 00232, `engine/remote`, `engine/ingest/hooks.go`, `api/remote.go`, `connectors/iswallet/registrar.go` |
| I6 | The iswallet page, rail facts, the schools and invoices specs, the schools open items, the partner tracker, Schools and its guide, embedding, the connector contract | `docs/` |
| Tests | Conformance fixtures for the single issue, KYC, 502, 500, one-time numbers (and without one wallet), the credit, a one-time payment, a reversal and the guide's own example; the webhook and look-back mapping one credit to one key; the batch, providers, wallet creation, request ids and dedup; the saved-connection check; a connection subscribing itself end to end (three subscriptions, a signed credit recorded once, a wrong signature refused, a failing delivery shown, removal); a shared client making none; creating the school's wallet; Playwright: the wallet created from settings | `connectors/iswallet/*_test.go`, `api/connection_check_test.go`, `api/iswallet_subscriptions_test.go`, `api/schools_wallet_test.go`, `web/e2e/accounts.spec.ts` |

## 9. Built: workstream W

Built 27 October 2026, in the same PR as workstream I (asked for as one). What differs from §3.7 and the milestones:

| Item | What exists | Code |
| --- | --- | --- |
| W1 | `TASKIEM_ISWALLET_CLIENT_KEY` and `_TEST_CLIENT_KEY` on every process. A connection Taskiem makes under its client (`connections.platform = 'taskiem'`, migration 00233) stores only its wallet; the vault hands it the platform key for its mode as a shared client, refuses to replace its credentials, and the API refuses to delete it (`provisioned_by = 'taskiem:receive_money'`) or to name another connection `taskiem`. One subscription per mode (made by `taskiem iswallet subscribe`, its secret printed once for `TASKIEM_ISWALLET_WEBHOOK_SECRET`) delivers to `/hooks/platform/iswallet@1?mode=`; each event goes to the trigger that sends its type, then to the business whose Taskiem connection holds the wallet in that mode (`taskiem_platform_route_event`), never to a business that lists the wallet on a connection of its own. An unknown wallet is acknowledged, counted (`app_event_drops`) and logged at error level for the operators; there is no operator alert channel beyond the logs yet. The egress guard lets a connector reach several loopback ports, so the browser tests fake live and test | `engine/secrets/connections.go`, `engine/ingest/platform.go`, `cmd/taskiem/iswalletcmd.go` |
| W2 | **Settings > Payments** (`GET`/`POST /v1/settings/receive-money`, owners turn it on): whose money it holds (pooled by default, segregated for a school), the business email and the director's BVN, sent to iswallet once and kept nowhere (not sealed, as §3.7 planned: nothing needs them again); one wallet per business, keyed by the tenant. Invoices are pointed at it unless they name a rail of their own; a school's issuing too. Get started offers it to a business with no rail; the plan feature `receive_money` gates it (402) and Get started says so | migration 00234, `api/receive_money.go`, `web/src/pages/ReceiveMoney.tsx` |
| W3 | The business's own number: a permanent account on its wallet whose reference (`biz_…`) names no customer, so its credits reach To match. Invoices on the rail get one-time numbers as on every rail; an invoice whose customer has a number of their own shows it on the page and in WhatsApp's Pay by transfer, a one-time number still on offer (migration 00235). "my account number" and "balance" in chat. Invoices no longer ask a rail with no `get_payment` (iswallet) every 30 seconds: its webhook says when it is paid. **Not built**: pay-by-transfer on the storefront, which takes payment through Paystack only (planned: N3, §10.3) | `engine/publicweb/invoicepay.go`, `api/invoice_chat.go` |
| W4 | The balance on Payments, on Money's card, in the daily summary and in chat. **Withdraw to my bank**: the bank account named by `name_enquiry` and kept; the approval rule `taskiem_withdrawals` (an owner; two owners from an amount the owner sets); the `withdraw-to-bank` template, made and published for the business on its Taskiem connection; `POST /v1/receive-money/withdraw` starts a run once per key, which waits for approval, pays through `pay_bank` and waits for the outcome, all in the money log. **Not built**: the weekly or above-a-balance sweep; the template runs on demand (planned: N2, §10.2) | `templates/library/withdraw-to-bank.json` |
| W5 | The daily usage snapshot counts credits and payouts on the rail (`rail_credits`, `rail_credit_kobo`, `rail_payouts`, `rail_payout_kobo`). `receive_money` is on every plan for now. A school receipt says what the payer sent against what reached the school after the bank's fee; invoice receipts (templates) do not yet (planned: N1, §10.1) | `engine/billing/snapshot.go`, `engine/schools/receipts.go`, `deploy/plans.yaml` |
| W6 | The runbook, the owner's guide, the Schools guides, the iswallet page, billing and the CLI; decision 0052 accepted | `docs/runbooks/iswallet-platform.md`, `docs/guide/receiving-money.md` |
| Tests | Routing into the right business only (another business under the client, one listing the wallet itself, the other mode), the vault's hand-out and refusals; turning it on end to end with a fake iswallet (one wallet whatever the retries, the BVN sent once and nowhere in the database, the connection locked, invoices pointed at it, the balance), a credit through the platform URL into the right money log, withdrawals (the name, the rule, one workflow, one run per key, the account sealed in the run); the own number on the invoice page through the database; Playwright from Get started to a withdrawal | `engine/ingest/platform_test.go`, `api/receive_money_test.go`, `engine/publicweb/invoice_db_test.go`, `web/e2e/receive-money.spec.ts` |

### After review (27 October 2026)

A review of the built PR found and fixed:

- **Withdrawals**: the bank account is written into the withdrawal workflow itself (a new version for a new account) and shown in the approval, so neither a run's starter nor anyone starting the workflow another way can send the money elsewhere. Four-eyes holds: with four-eyes on approval rules the rule is written pending, with four-eyes on publishing the new account waits for a second person, and a gated prod waits for promotion; the Payments page says what is waiting, and the account and rule before stay in force. Two owners from an amount needs two owners. A payout that fails tells the owner.
- **Access**: every receive-money route is the prod wallet's (`tenantWide`); reading the setting needs `invoice.read`; a shared client's connection (a partner's, or a business's under Taskiem's client) can create no wallet and no subscription; deleting a connection checks the environment and whether Taskiem keeps it before releasing its subscriptions; a partner cannot name a connection `taskiem`; the platform webhook counts its rate after verifying; the business's own reference is random; Taskiem's calls for one business take at most 20 a minute of the shared key.
- **Money**: a one-time number's payment read back by the look-back is the webhook's `payment.received` entry, not a second account credit (`list_credits` marks it); the business's own wallet is read back by the look-back; customers' and students' numbers on the Taskiem wallet need no wallet chosen (the server fills it, whatever is sent); the balance's ledger figure is iswallet's `total`; a school receipt shows the fee only when the transfer was for it alone; polling skips iswallet's one-time numbers without starving other rails.
- **Turning it on**: the key is the business's and what it sent (a corrected email or BVN is a new request); two presses at once make one wallet; iswallet not answering is a 502 saying asking again is safe; invoices are pointed at the wallet unless they already name a rail that can take payments.

Still open, by choice or for later: a business's own workflows calling iswallet on the shared key are limited only by iswallet; anyone who may publish workflows can publish one that pays out from the wallet without the approval rule, as on any rail the business connects; Schools settings do not run the connection check before planning a batch (I5); metering counts a payout or credit later reversed; WhatsApp gives a customer with their own number that number only (the invoice page still offers a one-time one); the connection page does not say which subscription is in force for a connection whose secret was pasted.


## 10. Next: what W left out (workstream N)

Planned 27 October 2026. Three pieces §9 records as not built: the fee on invoice receipts (N1), the sweep to the business's bank (N2) and pay by transfer on the storefront (N3). Two decisions are the founder's, taken the same day:

- **A sweep needs no approval.** Its money goes only to the bank account written into the business's withdrawal workflows, which changes only under the rules in §9's review (and four-eyes where it is on). An owner turns it on, chooses when it runs, and is told after each sweep.
- **A storefront order paid by transfer is refunded by hand.** Only Paystack refunds through its API; on iswallet, Flutterwave and OPay a refund is a payout to the customer's account, which Taskiem may not have. The owner sends the money back from their bank and marks the order refunded; a refund through a payout under approval is for later.

### 10.1 The fee on invoice receipts (N1)

A school's receipt says what the payer sent against what reached the school (`engine/schools/receipts.go`); an invoice's receipt cannot, because the fee never reaches it:

- iswallet's one-time numbers, the default for invoices, map to `payment.received` with the amount only. The credit webhook carries `amount` and `amount_credited` for them as for permanent numbers, and the look-back's `fee_charged` likewise: `payment.received` gains `fee_kobo` (capabilities schema, the iswallet manifest's event and `list_credits` mappings) and `moneylog.FromEvent` keeps it, as it does for `virtual_account.credited`.
- The invoice triggers' bodies carry no fee. `PaymentTrigger`'s `payment` gains `fee_kobo` and `received_kobo` (what reached the business), read from the money log entry the part counted (`customer_invoice_payments.entry_id`), shown only when the part took the whole entry, the schools' rule. `PaidTrigger` gains the same two figures summed over such parts.
- `notify_customer` fills only `{link}`, `{number}` and `{due}`. It gains `{fee_line}`: "You sent ₦X; ₦Y reached {business} after the bank's fee of ₦Z." from a new message (`invoice.receipt.fee_line`, in every language file), or nothing when there was no fee or the payment was split. `invoice-paid-receipt`, `resend-paid-receipt` and `zoho-mail-paid-receipt` use it; a business's workflow made from them gets it through the template update, not by itself.

### 10.2 The sweep to the bank (N2)

Settings > Payments gains **Send my money to my bank automatically**: off (the default); **every week** on a day and at a time; or **whenever the wallet holds more than** an amount, checked every hour. Either way the owner may keep an amount in the wallet. It needs the bank account saved under Withdraw to your bank.

- **Settings**: `receive_money` gains `sweep_mode` (`off`, `weekly`, `above`), `sweep_day`, `sweep_time`, `sweep_above_kobo`, `sweep_keep_kobo` and `sweep_workflow_id`; `PUT /v1/settings/receive-money/sweep`, owners, audited.
- **The workflow**: a new template, `sweep-to-bank`, made and published for the business as the withdrawal workflow is (four-eyes publishing holds it as it holds a new account). A schedule trigger (the week's day and time, or hourly at a minute derived from the business's id, so businesses do not all ask the shared key in the same minute); a `balance` step on the Taskiem connection; a payout of what the wallet holds less the amount kept and less the payout fee's allowance, only when that is at least ₦1,000 (and, for `above`, only when the wallet holds more than the amount); then the outcome, as in `withdraw-to-bank`; the owner told after each sweep, paid or not.
- **No approval step.** The approval rule cannot say "no approval" (a rule has at least one level), and the withdrawal workflow pays only when approved, so the sweep is its own workflow, not a call into the withdrawal one. The bank account is written into it as into the withdrawal workflow (`withdrawDefinition`'s substitution), and saving a new account writes a new version of both in the same transaction. The amount comes from the balance, never from input: anyone starting it by hand (any workflow can be run from the API by someone with run.start) moves at most what the rule would, to the same account, earlier. The payout's key is the sweep's scheduled time (the run's id when started by hand), so a timetable slot never pays twice.
- **The payout fee**: what iswallet charges per payout is unknown (§6); until it is, the allowance is a platform setting (`TASKIEM_ISWALLET_PAYOUT_FEE_KOBO`, default ₦100). A withdrawal awaiting approval when a sweep empties the wallet fails on payout and tells the owner, as any payout without the money does.
- **Words**: the Payments page and the owner's guide say plainly that sweeps are not approved and go only to the saved account; decision 0052's As built gains the rule.

### 10.3 Pay by transfer on the storefront (N3)

Storefront checkout is Paystack by name: `PaystackPayments` calls `paystack@1`'s actions, `ForCheckout` matches only `paystack/…` channels, `CheckPayments` verifies with `verify_charge`, and the forms' settings offer only a Paystack connection. [Rails and products](rails-and-products.md) already plans moving it onto `collect_link`; N3 does that and adds `collect_transfer`, so an order is payable by a one-time number on any rail that issues them (Paystack, Flutterwave, OPay, iswallet; a business receiving money with Taskiem gets its Taskiem connection by default).

- **Issuing**: `customer_invoice_accounts` holds an order or an invoice (a nullable `order_id`, exactly one of the two), so issuing, the hourly and per-item caps, the payment id recorded before asking, the 30-second `get_payment` polling, expiry and an unknown outcome are the invoices' code (`engine/invoicing/accounts.go`), not a second copy. The customer asks for the number from the order's status page (`POST /o/{ref}/pay`, queued as the invoice page's request is: the public role holds no rail keys); the number's window is what is left of the order's hold, and asking for one extends a hold to at least 60 minutes for an order, kept at 30 for a booking's slot.
- **Paid**: the matcher's exact lookup on `customer_invoice_accounts` sends an order's payment to `Orders.CountPayment` and `Paid`, as `ForCheckout` does for a card payment; `ForCheckout` and `checkoutChannel` take any rail's channel. A payment to the number of an order that has expired goes to To match naming the order; the owner takes the order or refunds by hand. Late and wrong amounts are each rail's to return or report, as for invoices (`on_late`, `on_wrong_amount`).
- **The status page**: the number, bank, name, amount and minutes left, as the invoice page shows them (`taskiem_public_order_payment`), beside the card button when the rail has `collect_link`. The page's "held for 30 minutes" says the order's real hold (it is 24 hours after the business accepts).
- **Refunds by hand**: an order paid by transfer that becomes `refund_due` asks the owner to send the amount back from their own bank, naming the sender's details when the rail's credit gave them, and to press **Mark refunded** (`POST /v1/storefront/orders/{id}/refunded`, audited, with a note); the customer is told it was refunded by transfer. The order workflow's refund approval no longer says "through Paystack" for these orders. A card payment refunds through Paystack as today.
- **Settings**: the forms' payment connection lists any connection with `collect_link` or `collect_transfer`; `at_order` still needs a verified business on a plan that includes it.

### 10.4 Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| N1 | The fee on invoice receipts | S | A one-time number's payment through the webhook and through the look-back records its fee once; an invoice paid by it alone sends a receipt saying sent, received and fee; a split payment's receipt says none; the three receipt templates use `{fee_line}` |
| N2 | The sweep to the bank, without approval | M | Weekly and above-an-amount sweeps pay the saved account what the wallet holds less what is kept and the fee allowance, once per slot, with no approval, and tell the owner; a new bank account versions the sweep with the withdrawal; four-eyes publishing holds a change; nothing is swept below ₦1,000 |
| N3 | Pay by transfer on the storefront; refunds by hand | L | A customer pays an order and a booking by transfer to a one-time number from the status page on iswallet and on Paystack; it is paid within the tick and once; an expired order's payment reaches To match; a transfer-paid order is marked refunded by the owner and the customer told; card payments unchanged |

Order: N1, N2, N3. Tests as §5: conformance fixtures for the fee on a one-time number; `api` and engine tests for the receipt's figures, the sweep's amounts, keys, versioning and four-eyes, an order's number issued, paid and refunded by hand; Playwright for the sweep's settings and paying an order by transfer, with axe in both themes.
