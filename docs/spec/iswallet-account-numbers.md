# iswallet account numbers: the plan

Status: **Draft** 27 October 2026, for the founder to extend · Source: iswallet's guide of 27 October ([kept verbatim](../integrations/iswallet-answers-2026-10-27-account-numbers.md)) · Connector: [iswallet](../integrations/iswallet.md) · Rail facts: [schools](schools-rail-facts.md#iswallet) · Decisions: [0046](../decisions/0046-balans-in-taskiem.md) (students' own account numbers), [0045](../decisions/0045-tally-in-taskiem.md) (customers' accounts, the money log)

## 1. Summary

iswallet's guide of 27 October answers what the rail facts (B0) had to leave open: the credit webhook's fields, gross against net, idempotent issuing, naming, the batch, and the school model of one wallet with one permanent account per student. Nothing in it changes Taskiem's design: the record, the matching rule, the one payment path and the schools' batch jobs stay as built. What changes is the connector and what sits around it:

1. iswallet credits reach the money log **from the webhook, in seconds**, instead of only from the 48-hour look-back, up to 15 minutes late.
2. Single issuing uses the documented single-account call; the batch stays for rolls; one-time accounts are refused on iswallet.
3. Taskiem can **create a school's wallet** from the Schools settings instead of asking the bursar to paste a wallet id.
4. Every iswallet call keeps its `X-Request-ID`, and the two failure kinds the guide distinguishes (final against unknown) are told apart.
5. A connection check that proves the key works and names the partners that can issue.

One PR, milestones I1 to I5 below, each small. Things that need people, not code, are in §6.

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

## 4. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| I1 | The credit webhook mapped: `virtual_account.credited` and the reversal, dedup on the idempotency header, the look-back as the net | S | A signed `wallet.credit.posted` fixture counts on a student's invoice within the tick, once, with gross counted and the fee shown; the look-back finds it and adds nothing; a reversal reopens the invoice and voids the receipt |
| I2 | Issuing on the single-account call; the batch kept; no one-time accounts on iswallet | S | Conformance fixtures for both calls; an invoice to an iswallet customer offers their permanent account, never a one-time one |
| I3 | `create_wallet` and "Create the school's wallet" | M | A school's wallet is created from settings with the BVN sent once and sealed; a retry creates no second wallet; the wallet-level `account_number` appears nowhere |
| I4 | Request ids kept and shown; final against unknown failures | S | A failed step and a held credit show the id; a `502` fails fatally with the reason; a `500` parks, and an issue retries safely |
| I5 | `list_providers` and the connection check; the BVN readiness note | S | The connection page says the key works and which partners issue; a business wallet without a BVN shows the note |
| I6 | Docs: the iswallet page, the rail facts (iswallet rows closed), the partner tracker (what iswallet still needs), the schools open items (iswallet rows closed), the Schools guide (creating the wallet) | S | `docsite -check` passes; the open items no longer say the webhook is unmapped |

Order: I1, I2, I4, I5, I3, I6. One PR.

## 5. Testing

| Level | What | Where |
| --- | --- | --- |
| Conformance | The single and batch issue calls, `create_wallet`, `list_providers` against fixtures written from the guide; the credit webhook fixture with its signature | `engine/conntest`, `connectors/iswallet/testdata` |
| Capability | `virtual_account.credited` from the fixture: gross, settled, fee, reference, account number, credit id; the reversal | `connectors/iswallet` |
| Exactly once | The webhook and the look-back reporting the same credit count once; a redelivery with the same idempotency header is one event | `engine/lookback`, `engine/ingest` |
| Schools | A student's account issued on the single call; a batch job over the batch call with `existing` items; the wallet created from settings | `engine/schools`, `api` |
| Web | Settings: create the wallet, the readiness line, the BVN note | Playwright |

## 6. Needs people, not code

| Item | Who | Status |
| --- | --- | --- |
| The webhook URL for sandbox and production, given to iswallet (`<edge>/hooks/…` as the connection page shows) | Founder | Open |
| Which `owner_type` the schools are. `client_end_user_business` means per-student credits reach the school's segregated account by iswallet's sweep (alarm at 10 minutes) while the balance is right at once; the alternative, one unreferenced number per school, loses per-student attribution | Founder, with iswallet | Open; the plan assumes `client_end_user_business` and eventual segregation |
| Expected enrolment volume, so iswallet can say whether 500 a call needs paging on our side (our jobs page below that already) | Founder | Open |
| Sandbox key and base URL on the staging connection, then the pilot checklist's issuing step run for real | Founder, then engineering | Open |
| The `account_name` length limit and whether iswallet adds a fee of its own on deposits | iswallet | Still unanswered; not blocking |

## 7. Out of scope

Payouts, FX and crypto (the guide's own scope); one-time accounts on iswallet; changing where a school's money is held (iswallet's side); a ledger (0046).
