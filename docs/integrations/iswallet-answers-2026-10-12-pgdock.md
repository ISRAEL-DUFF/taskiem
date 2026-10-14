> Kept verbatim for the team. iswallet wrote this to PGDock, not to Taskiem, and it was passed to us on 14 October 2026. What it changes for Taskiem's connector is in [iswallet](iswallet.md); what we still need from iswallet is in the [partner tracker](../partner-tracker.md). The FX, consent and billing questions in it are PGDock's to decide.

# iswallet → PGDock: answers to the wallet and FX integration request

**For:** PGDock engineering
**From:** iswallet Engineering
**Date:** 12 October 2026
**Against:** *PGDock × iSpend/iSwallet: wallet and FX integration request*

Answers under each numbered item. Everything was checked against the running code, not
against a spec; where the code and this document disagree, the code is right — tell us.

**Read §0 first.** Four of your items describe features that belong to a different
product, and one of them — how a merchant debits a customer — works so differently here
that your flow 3 needs rethinking rather than rewiring. There is also an FX question
underneath items 16–21 that is commercially larger than the API question you asked.

**Docs:** OpenAPI at `https://synledger.name.ng/iwallet/openapi.json`, Swagger UI at
`/docs`. It was regenerated on 24 September, so treat this document as authoritative where
they differ.

---

## 0. Four things to settle before you build

### 0.1 You are describing two products, and we are the other one

You already integrate iSpend's **merchant API** — customers, checkout sessions, mandates,
mandate charges, refunds. iswallet is a different system with a different model. These do
not exist here, at all:

| What you asked for | In iswallet |
|---|---|
| Mandates / standing permission (item 10) | **Not built.** No mandate object, no consent record, no revocation |
| Checkout session or push approval for a top-up (item 6) | **Not built.** No hosted page, no approval flow |
| Hold funds now, capture later (item 14) | **Not built.** Every debit is immediate and final |
| Refund tied to the original charge (item 15) | Partially — the money moves, but nothing links it to the debit |

This is not a roadmap gap we are about to close. iswallet is a ledger and a wallet API; the
consent and checkout surface lives in the merchant product. If your flow genuinely needs
mandates and hosted approval, the honest answer is that you should charge through the
merchant API you already have and use iswallet only for the balance — or accept the model
in §0.2, which is how our other tenants do exactly what you are describing.

### 0.2 There is no customer consent primitive. Your key is the authorisation.

This is the single most important answer in the document.

A **client-scoped API key can move money between any two wallets it owns, for any amount,
at any time, without the customer being asked.** PGDock creates the wallets, so PGDock's
key owns them, so PGDock can debit them. That is the whole permission model.

So the flow for item 10 is: `POST /v1/transfers`, customer wallet → your operating wallet.
No mandate to create, nothing to revoke, no approval step, and no API call that would let a
customer refuse.

Two consequences you should decide on deliberately rather than inherit:

1. **The consent burden is entirely yours.** Whatever authorises a monthly debit — your
   terms of service, a tick box at signup — lives in PGDock and is enforced by PGDock.
   iswallet will not check it and cannot. If a customer disputes a charge, our records show
   that your key moved the money and nothing about why they agreed to it.
2. **Revocation is yours to implement too.** There is no "mandate revoked" state for us to
   webhook you about (item 22). The nearest primitive we have is freezing the wallet
   (item 5), which stops *all* movement, not just yours.

If that is not acceptable for your compliance posture, say so now — it is a reason to bill
through the merchant API instead, and better discovered in this document than in month
three.

### 0.3 Your USD→NGN rate is the official rate, and it carries no spread

Items 16–21 asked about the API. The commercial facts underneath matter more:

- **The fiat→fiat spread is currently `0` basis points.** USD→NGN comes back with
  `spread_bps: 0` and `effective_rate == market_rate`. There is no FX margin on your
  conversions today. If you expected to show a customer "rate + our spread", there is no
  spread to show, and if *we* expected to earn one, we are not.
- **The source is openexchangerates**, which publishes the **official** USD/NGN rate. In
  Nigeria that has historically diverged materially from the rate at which naira actually
  buys dollars. Your customers fund their wallets with naira earned at market reality; you
  would be billing them at the official rate. Depending on which way the gap runs, that is
  either you under-collecting or your customers overpaying, every month, by a margin
  nobody chose.
- **We can override the pair.** There is an operator endpoint that pins USD/NGN to a rate
  our desk sets, which is how we would price this deliberately rather than inherit a feed.
- **Updated 13 October 2026 — there is now a second answer to this.** Converting
  **NGN→USDT** prices off P2P market rates rather than the official one, which removes the
  divergence problem entirely; it carries a **150 bps** spread where this pair carries zero.
  That trade is laid out in §J.1, and it is the same decision this section asks for, with
  better options.

None of that is a code question — it is a pricing decision for your team and ours, and it
should be made before you launch rather than after the first month-end. We would rather
raise it now than have you discover it in a reconciliation.

### 0.4 One thing in your plan we would change

Your model — NGN wallet, USD invoice, convert at charge time — is the right one, and it is
what we would recommend. But **do not expect to debit "at a quoted rate"** (item 17). There
is no mechanism that locks a rate into a wallet debit. You will:

1. Ask for the rate, compute the naira amount yourself,
2. Record the rate and the naira amount on your invoice,
3. Debit that naira amount as a plain transfer.

The rate is *your* pricing input, not a parameter we honour. That is simpler than it
sounds and it puts the number on your invoice beyond dispute — but it does mean the rate on
the invoice is yours to defend, not ours.

---

## A. Wallets

### 1 — Creating a wallet

Separate call, and there is **no customer object**. The wallet *is* the customer record.

```http
POST /v1/wallets
Authorization: Bearer <key>
Idempotency-Key: pgdock-org-4471-wallet

{ "owner_type": "client_end_business", "owner_ref": "pgdock:org:4471",
  "currency": "NGN", "client_id": "pgdock" }
```

```json
{ "wallet_id": "6f1e…", "owner_type": "client_end_business",
  "owner_ref": "pgdock:org:4471", "currency": "NGN",
  "tier": "TIER_1", "status": "active", "created_at": "2026-10-12T09:14:02Z" }
```

- **`owner_ref` is yours and must be namespaced.** Uniqueness is on
  `(owner_ref, currency)` **globally**, not per tenant, so `org:4471` could collide with
  another integrator. `pgdock:org:<id>` is the right shape.
- **`409 WALLET_ALREADY_EXISTS`** carries the existing wallet under
  `error.original_response`. Treat it as success — it is your recovery path if you lose a
  response.
- **One wallet per customer, holding every currency.** Not one per currency. A wallet
  acquires a balance in a currency the first time it receives one.
- **`owner_type`**: your customers are organisations, so `client_end_business` — a business
  holding its own money, settled from our pool. (The alternative, `client_end_user_business`,
  puts each customer's balance in its own account at the partner bank. It is for businesses
  holding funds *on behalf of others*; it costs a director's BVN before the first deposit
  and a reconciliation line for the wallet's whole life. You do not need it.)

### 2 — Reading a balance

`GET /v1/wallets/{wallet_id}/balance`

```json
{ "wallet_id": "6f1e…", "currency": "NGN",
  "balance":  { "total": 5000000, "available": 5000000, "pending": 0 },
  "balances": [ { "currency": "NGN", "total": 5000000, "available": 5000000,
                  "pending": 0, "scale": 2 } ],
  "scale": 2, "as_of": "2026-10-12T09:14:02Z" }
```

- **Read `balances[]`.** The top-level `balance` is the canonical currency only and exists
  for older integrators.
- **Use `available`** for "can this customer cover the charge".
- **`pending` is not a merchant hold.** It is money committed to an in-flight *bank
  payout*. Nothing you do can create or release it, and it will be `0` for a wallet that
  only receives deposits and pays you. See item 14.
- A currency with no balance is **absent from the array**, not zero.

### 3 — Listing transactions

`GET /v1/wallets/{wallet_id}/transactions?from=&to=&type=&status=&limit=&cursor=`

RFC3339 bounds, cursor-paged, `limit` 1–100 (default 20). This is your per-customer view.

For nightly reconciliation across **all** your wallets at once, use the export instead:

`GET /v1/ledger/export?from=&to=&format=csv` — scoped to your client, `json` or `csv`,
**max 10,000 rows**, so keep each call inside a 90-day window (a day at a time for you).

### 4 — Currencies

A wallet can hold NGN, USD, EUR, GBP, GHS, USDT, USDC and the major crypto set. One wallet,
many balances.

**Your plan is the right one** and we would not change it: an NGN wallet, USD pricing,
converted at charge time. The alternative — holding USD balances and debiting USD — only
moves the conversion earlier (the customer still funds in naira, so something still has to
convert) and leaves your customers carrying FX risk between top-up and charge. Keep the
money in naira and let the invoice carry the rate.

One caveat if you ever reconsider: **only NGN has limit rows configured.** Balances in USD
and everything else are uncapped, pending compliance numbers. See item 28.

**Updated 13 October 2026:** a wallet can now hold **USDT that is genuinely backed and
withdrawable to an address you control**, which is a real third option rather than a listed
currency code. It also changes the rate problem in §0.3. See §J.

### 5 — Freezing and closing

Four statuses, set with `PATCH /v1/wallets/{id}` using your own key — you control your
customers' wallets, we do not need to be involved:

| Status | Effect on your calls |
|---|---|
| `active` | normal |
| `frozen` | every debit refused with `422 WALLET_INACTIVE`; credits still land |
| `suspended` | same refusal; intended for compliance holds rather than your own dunning |
| `closed` | **terminal** — no transition out of it, ever |

Allowed moves: active ↔ frozen, active/frozen → suspended, suspended → active, anything →
closed. `closed` has no exit, so do not use it for "customer cancelled, might come back".

A customer cannot freeze their own wallet — they have no credentials with us. Anything
customer-facing is yours to build.

---

## B. Funding the wallet

### 6 — Top-up from iSwallet: not supported

There is no checkout session, no hosted page, no push approval, and **no transfer between
wallets of different tenants**. A customer's own personal iSpend wallet and the wallet you
created for them are in different client scopes, and a client key cannot move money across
that boundary.

So of your two funding routes, **only bank transfer exists** (item 7). If customer-approved
top-up from an existing iSpend balance matters to your product, it has to go through the
merchant API you already integrate, and arrive here as a bank transfer or an operator
action on our side.

We would rather say this plainly than describe a workaround that is really us doing it by
hand.

### 7 — Bank transfer into a virtual account

```http
POST /v1/wallets/{wallet_id}/virtual-account
{ "is_permanent": true, "client_reference": "pgdock:org:4471" }
```

```json
{ "wallet_id": "6f1e…", "provider_code": "flutterwave",
  "account_number": "9901234567", "account_name": "PGDOCK / ACME LTD",
  "bank_name": "Wema Bank", "is_permanent": true,
  "reference": "pgdock:org:4471", "status": "active" }
```

- **Permanent NUBAN**, one per `client_reference`. Re-issuing the same reference returns the
  existing account, so a re-run of your provisioning job is safe.
- **Read it back** with `GET /v1/wallets/{id}/virtual-accounts` — you do not have to treat
  your stored copy as the only one.
- **Account name and bank are the provider's**, not ours to format. Expect a derived name
  and a NUBAN at whichever partner bank issued it.
- **Credit speed**: seconds after the provider's webhook reaches us. There is no settlement
  delay on our side — the credit posts on receipt.
- **Wrong amount**: credited as sent. A virtual account is an address, not an invoice; it
  has no expected amount to be wrong about. Reconciling an underpayment to an invoice is
  yours.
- **Duplicate transfer**: two transfers are two deposits and two credits, each with its own
  event. We deduplicate *redeliveries of the same deposit*, never two genuine payments.

**One thing you must model: deposits arrive net of the provider's fee.** Flutterwave takes
**1.4%, capped ₦2,000**, and settles the remainder — ₦50,000 sent credits ₦49,300. See
item 8.

### 8 — Limits and fees

**Limits** come from the wallet's KYC tier. For `client_end_business`:

| Tier | Per transaction | Per day |
|---|---|---|
| TIER_1 (default) | ₦50,000 | ₦50,000 |
| TIER_2 | ₦500,000 | ₦1,000,000 |
| TIER_3 | ₦5,000,000 | ₦10,000,000 |

**A new wallet can only move ₦50,000 a day.** Upgrade with `POST /v1/wallets/{id}/kyc`
before a customer tries to fund a real subscription. These are seeded defaults carrying an
unratified-pending-compliance note, so tell us the ceiling you need.

Note the limits apply to **outflows** (bank payouts) and are checked on transfers; they do
not cap *deposits*. A customer can fund more than their tier allows and then be unable to
withdraw it, which is worth surfacing in your UI.

**Fees:**

- **Inbound (deposit):** the provider's 1.4%/₦2,000 collection fee. Default policy is
  pass-through — the customer bears it and receives the net. We can switch PGDock to
  `absorb`, where the customer is credited the full amount and the fee lands on us; that is
  a commercial conversation, not a code change.
- **Your debit (transfer):** no fee. Wallet-to-wallet movement is free.
- **Withdrawal (item 9):** provider cost plus platform fee, surcharged — the beneficiary
  receives the full amount and the wallet is debited amount + fee. Read `GET /v1/fees`.

### 9 — Withdrawing to a bank

`POST /v1/outflows`, which **you** call on the customer's behalf — there is no
customer-facing withdrawal. So PGDock handles refunds and withdrawals, through our API.

```http
POST /v1/outflows
Idempotency-Key: pgdock-refund-9912

{ "wallet_id": "6f1e…", "amount": 2500000, "currency": "NGN",
  "narration": "Balance withdrawal",
  "destination": { "bank_code": "058", "account_number": "0123456789",
                   "account_name": "Acme Ltd" } }
```

Returns `202` with an `outflow_id`. **Persist it before doing anything else** — it is your
only handle on the payout (see item 25). Asynchronous: poll `GET /v1/outflows/{id}` or wait
for `wallet.outflow.confirmed`.

`POST /v1/name-enquiry` resolves an account name before you pay. Use it: a payout to a
valid-but-wrong account number is indistinguishable from a correct one, and it is not
recoverable.

---

## C. Debiting the wallet

### 10 — How PGDock debits

**See §0.2.** Mechanically:

```http
POST /v1/transfers
Idempotency-Key: pgdock:invoice:2026-10:org:4471

{ "from_wallet_id": "<customer wallet>", "to_wallet_id": "<your operating wallet>",
  "amount": 7350000, "currency": "NGN",
  "narration": "PGDock Oct 2026 inv INV-4471-10" }
```

```json
{ "transfer_id": "…", "txn_id": "…", "amount": 7350000, "currency": "NGN",
  "status": "completed", "completed_at": "2026-10-12T09:14:02Z" }
```

Your operating wallet id comes from `GET /v1/platform/account` — it is provisioned per
tenant, and you should **not** create one yourself. There is no permission to grant and
none to revoke.

`narration` is capped at 255 characters and is the only place your invoice number can live:
**transfers have no structured metadata field.** That is a real gap for you and it is on our
list; until then, put the invoice id in the narration and keep the authoritative link in
your own records.

### 11 — Idempotency

Yes, on the `Idempotency-Key` **header**. Required — a missing one is
`400 MISSING_IDEMPOTENCY_KEY`, not a silently unprotected charge.

Two mechanisms with different lifetimes, and the seam matters:

| When you retry | What happens |
|---|---|
| **Within 24 hours** | the original response is replayed — same body, same `transfer_id` |
| **After 24 hours** | the replay cache has expired; a permanent database constraint refuses the second charge, but you get **`500 INTERNAL_ERROR`** |

**Money is safe in both.** The second debit never posts. But past 24 hours the response is
indistinguishable from a transient fault, so a connector that trusts its own retry policy
will retry forever. Both halves are on our fix list; until then, **do not automate a retry
more than 24 hours after the original** — age your pending charges and escalate the old
ones.

Same key with a *different body* is `422 IDEMPOTENCY_KEY_REUSED`, non-retryable. Serialise
your request deterministically so a retry does not trip it.

### 12 — Failure responses

Envelope on every error, with `request_id` inside `error` **and** on the `X-Request-ID`
header (log the header — it is what we search on):

```json
{ "error": { "code": "INSUFFICIENT_FUNDS", "message": "…",
             "request_id": "req_1f7b30eeae50", "doc_url": "…" }}
```

**Final — do not retry:**

| Code | Status | Means |
|---|---|---|
| `INSUFFICIENT_FUNDS` | 422 | the customer cannot cover it. Dun them, do not retry |
| `WALLET_INACTIVE` | 422 | frozen, suspended or closed |
| `CURRENCY_MISMATCH` | 422 | the wallet holds no balance in that currency |
| `EXCEEDS_SINGLE_TXN_LIMIT` / `EXCEEDS_DAILY_LIMIT` | 422 | tier ceiling — item 8 |
| `IDEMPOTENCY_KEY_REUSED` | 422 | your key generation is wrong |
| `WALLET_NOT_FOUND` | 404 | |
| `MISSING_IDEMPOTENCY_KEY`, `INVALID_REQUEST` | 400 | |
| 403 | | the wallet belongs to another tenant |

**Safe to retry with the same key:**

| Code | Status |
|---|---|
| `RATE_LIMITED` | 429 |
| connection failure / 503 | — |

**Ambiguous — retry with the same key, within 24 hours:** any other `500 INTERNAL_ERROR`.

There is no "iSpend unavailable" code; that surfaces as a connection failure or a 5xx.

### 13 — Synchronous or asynchronous

**A transfer is synchronous and final.** `200` means the money has moved and the ledger is
balanced. There is no pending state, no webhook to wait for, and no maximum pending
duration — the question does not arise.

(Bank payouts, item 9, are the asynchronous ones. Do not generalise from them.)

### 14 — Holds and capture

**Not available.** There is no authorise-then-capture primitive, and `pending` on the
balance is not one (item 2).

Two workable shapes, both yours to choose:

- **Debit the exact amount.** Your plan fee is known in advance and usage is in arrears, so
  you already know the number at charge time. This is what we would do.
- **Debit, then refund the difference.** A real debit followed by a reverse transfer
  (item 15). It moves money twice and shows twice on the customer's statement, so only
  reach for it if an estimate genuinely has to be taken first.

What you cannot do is reserve funds and have us guarantee they will still be there. Between
your check and your charge, the customer can withdraw.

### 15 — Refunds and credits back

A refund is a **transfer in the opposite direction**, under its own idempotency key:
operating wallet → customer wallet. Any amount, so partial refunds are just a smaller
number. Approved and supported — several tenants do exactly this.

For a goodwill credit funded by PGDock rather than returned from a charge, there is also
`POST /v1/platform/credits` (operating account → one of your customers), which books it as
a platform credit rather than an ordinary transfer. Use it when the money is yours rather
than theirs coming back.

**The gap:** nothing links a refund to the original debit. No `original_transfer_id`, no
structured metadata — only the 255-character narration. Keep the association in your own
records and put the original invoice id in the narration. Structured transfer metadata is
on our list.

---

## D. FX

### 16 — Asking for a quote

```http
GET /v1/rates?from=USD&to=NGN&amount=4900
```

`amount` is minor units of `from` — `4900` is $49.00.

```json
{ "rate": {
    "from": "USD", "to": "NGN",
    "market_rate": "1512.40000000",
    "spread_bps": 0,
    "effective_rate": "1512.40000000",
    "amount": 4900,
    "converted_amount": 7410760,
    "fetched_at": "2026-10-12T09:00:00Z",
    "expires_at": "2026-10-12T09:15:00Z",
    "sources": ["openexchangerates"],
    "derived": false
}}
```

- **`converted_amount` is minor units of `to`** — `7410760` is ₦74,107.60. Both currencies'
  scales are applied for you; do not multiply the rate yourself.
- **`derived: true`** means we priced the pair the other way round and inverted it. Check
  it before concluding our number disagrees with a market that quotes the other direction.
- Rates are exact decimal strings, not floats. Parse them as decimals.

### 17 — Quote validity and locking

**There is no quote id on this endpoint, and you cannot pass a rate into a debit.**

`expires_at` tells you when we will next refresh, not how long a price is held for you —
nothing is reserved. The only locked quote in the system is the convert quote
(`POST /v1/convert/quotes`, 60 seconds, with an id), and that exists to convert a customer's
*own* balance between two currencies, which is not what you are doing.

So the flow in §0.4: quote, compute, record the rate on your invoice, debit the naira
amount. The rate on the invoice is yours.

### 18 — Spread

`spread_bps` is returned as its own field and `effective_rate` already includes it, so you
can show a customer either or both.

**For fiat→fiat it is currently `0`.** USD→NGN carries no spread today and
`effective_rate == market_rate`. See §0.3 — this is worth a decision rather than an
assumption.

(For reference, the configured spreads elsewhere are 50 bps between stablecoins and 150 bps
across fiat/crypto. Fiat-to-fiat being zero looks like an omission rather than a policy.)

### 19 — Source and history

**Source:** openexchangerates for fiat pairs, polled periodically; `sources` names it on
every quote. Crypto and stablecoin pairs come from a weighted blend of exchange feeds.
An operator can pin a pair to a desk rate, which overrides the feed.

**History: readable as of 12 October**, with a client key.

```http
GET /v1/rates/history?from=USD&to=NGN&since=2026-09-01&until=2026-09-30&limit=100
```

```json
{ "from": "USD", "to": "NGN", "count": 2,
  "history": [
    { "from": "USD", "to": "NGN", "market_rate": "1540.00000000",
      "source": "openexchangerates", "recorded_at": "2026-09-13T12:00:00Z" },
    { "from": "USD", "to": "NGN", "market_rate": "1525.75000000",
      "source": "openexchangerates", "recorded_at": "2026-09-12T12:00:00Z" } ],
  "note": "market rates as published by the source; the spread in force at the time is not recorded" }
```

Newest first. `since`/`until` are optional and inclusive, RFC3339 **or** a plain
`YYYY-MM-DD` (midnight UTC). `limit` is 1–500, default 100. The window parameters are
`since`/`until` because `from`/`to` already name the currency pair.

One caveat, and it is in the payload as well as here: **these are MARKET rates.** The
spread in force at the time is not recorded, so this tells you what the market did, not
what a customer was charged. For that, keep the quote — which is why we would keep storing
it on the invoice even now the endpoint exists. A quote on an invoice is evidence; a rate
looked up afterwards is a reconstruction.

(If a `+01:00` offset is sent unencoded it arrives as a space and is refused — percent-encode
it as `%2B`, or send UTC with a trailing `Z`.)

Record `effective_rate`, `market_rate`, `spread_bps`, `fetched_at` and `sources` on the
invoice anyway — the whole quote, not just the rate. That is the number the customer was
charged; this endpoint is the market context around it.

### 20 — Both directions, and which rate on a refund

Both directions quote — `NGN→USD` works, flagged `derived: true` where we price the inverse.

**Which rate applies to a refund is your decision, and we would use the original.** The
customer paid ₦74,107.60; refunding that exact naira figure returns what they gave you and
leaves no FX gain or loss for either side. Refunding "$49 at today's rate" means one of you
profits from the move, and over a year of refunds that is a position nobody intended to
take.

Since your debit is denominated in naira anyway, refunding the naira amount is also the
simpler implementation: no quote, no second rate to defend.

### 21 — Maximums and unavailability

**No maximum per quote** — it is a price lookup, not a reservation.

**And it does not fail when the market is quiet, which is the part to plan for.** If every
rate source is down or stale, we return the **most recent rate we have, however old**,
rather than erroring. That is deliberate — failing a conversion because a feed hiccuped is
usually worse — but it means **a successful 200 does not mean a fresh rate**.

**Check `fetched_at` yourself** and decide your own staleness tolerance. For a monthly
invoice we would refuse to charge on a rate older than a few hours and alert instead. A
pair we have never priced is `404 RATE_NOT_FOUND`.

---

## E. Webhooks and reconciliation

### 22 — Events

Self-service registration: `POST /v1/webhooks/subscriptions` with `endpoint_url` and an
`event_types` array. You receive only what you list. The signing secret is returned **once**,
in the 201.

Relevant to you:

| Event | Fires when |
|---|---|
| `wallet.credit.posted` | money credited — **this is your "top-up received"** |
| `wallet.credit.reversed` | a credit we confirmed was clawed back by the bank |
| `wallet.credit.rejected` | an inbound transfer was refused and refunded to the sender |
| `wallet.debit.posted` | any debit posted |
| `wallet.transfer.completed` | a transfer settled — **fires twice**, once per side |
| `wallet.transfer.failed` | a transfer was refused |
| `wallet.outflow.confirmed` / `.failed` / `.reversed` | a bank payout (item 9) |

Not available, because the underlying concepts are not: **mandate revoked**, **wallet
frozen**, **top-up approved**. Freezing is your own action, so you already know.

**The body is an envelope** — the fields are one level down under `data`:

```json
{ "id": "evt_8f21…", "idempotency_key": "…", "event_type": "wallet.credit.posted",
  "schema_version": "v1", "occurred_at": "2026-10-12T09:14:02Z",
  "wallet_id": "6f1e…",
  "data": { "wallet_id": "6f1e…", "amount": 4930000, "currency": "NGN",
            "balance_after": 4930000, "txn_id": "…", "operation_id": "…",
            "source_type": "va_deposit", "source_ref": "…",
            "amount_credited": 4930000 } }
```

**Corrected 12 October:** a bank transfer into a virtual account is **`va_deposit`**. This
document first said `pull_inflow`; that is the pull-charge API, which you do not use. The
set is `va_deposit` (customer funding), `pull_inflow` (a pull charge) and `wallet_transfer`
(your own debits and refunds). Filter on `va_deposit` if you only want customer deposits.

**`amount_credited` is present on a virtual-account deposit** and is the field to book:
`amount` is the payer's gross, `amount_credited` is what the wallet received after the
provider's collection fee (item 8). Your fallback to `amount` would over-book every deposit
by the fee, so treat a missing `amount_credited` as an error worth alerting on rather than
a default.

**`wallet.transfer.completed` arrives twice per transfer** — one for the sender's wallet,
one for the receiver's, with different `wallet_id`. Both are yours, since both wallets are
yours. Dedupe on `(txn_id, wallet_id)`, not `txn_id` alone, or you will drop a real event.

Your plan to re-read the transaction before recording anything is right, and we would keep
it.

### 23 — Signature

Confirmed, and it matches what you have:

- **Algorithm:** HMAC-SHA256, hex-encoded.
- **Header:** `X-iSpend-Signature: sha256=<hex>`.
- **Signed string:** `"<timestamp>.<raw body>"` — `X-iSpend-Timestamp`, a literal `.`, then
  the **raw bytes**. Verify before parsing; re-serialised JSON will not match.
- **Skew:** reject if `abs(now − timestamp) > 300` seconds.
- Constant-time compare.
- **Secret:** per subscription, returned once at creation. A lost secret means deleting the
  subscription and making another.

Also sent: `X-iSpend-Idempotency-Key`, the per-delivery key.

### 24 — Delivery guarantees

- **10 attempts**, then dead-lettered and we stop.
- Backoff `min(30s × 2ⁿ, 6h)` plus jitter; the ten attempts span roughly **8 to 11 hours**.
- Success is any `2xx`; a 30s timeout counts as failure.
- **Duplicates are possible** — dedupe on the envelope `id`, or better on `data.txn_id`,
  which also protects you if we ever emit two events for one movement.
- **Ordering is not guaranteed.** Concurrent delivery and retries both reorder. Drive state
  from each event's content, not from arrival sequence; `GET /v1/wallets/{id}/balance` is
  always authoritative.
- **Replay:** `GET /v1/webhooks/subscriptions/{id}/deliveries` lists them and
  `POST /v1/webhooks/subscriptions/{id}/deliveries/{delivery_id}/retry` re-fires one. That
  is your recovery path if your endpoint was down past the retry window.

### 25 — Reconciliation endpoints

**By date range:** yes — `GET /v1/wallets/{id}/transactions` per customer, or
`GET /v1/ledger/export` across all of them (item 3). Hourly and nightly are both fine
within the rate limit.

**By reference: yes, as of 12 October.** `GET /v1/requests/{idempotency_key}` resolves
whatever was recorded under a key of yours.

```http
GET /v1/requests/{idempotency_key}
Authorization: Bearer <key>
```

```json
{ "idempotency_key": "tsk_a1b2c3…", "kind": "outflow",
  "resource_id": "8d3f…", "status": "pending", "terminal": false,
  "wallet_id": "6f1e…", "amount": 25000000, "currency": "NGN",
  "created_at": "2026-10-12T09:14:02Z" }
```

| Field | Use |
|---|---|
| `kind` | `outflow`, `inflow`, `transfer`, `convert`, `spend`, `multi_spend` or `bill_payment` — says which status vocabulary applies and which endpoint `resource_id` belongs to |
| `resource_id` | the id the original create call returned; fetch the full record with it |
| `status` | that kind's own status |
| `terminal` | **false means wait, not retry** — the outcome can still change |

**`404 IDEMPOTENCY_KEY_NOT_FOUND` means nothing was posted and the request is safe to send
again.** That is a guarantee, not a hope: every kind records the key in the *same database
transaction* as its first ledger entry, so a row exists and money moved, or it does not and
nothing did. There is no window where the debit is committed and the key is not yet visible
— which is exactly the window that makes the provider-reference lookup untrustworthy.

All seven kinds are searched. A kind left out would 404 for money that moved, so the test
suite reads the key columns out of the schema and fails if a table is added without being
covered. (It found one we had missed while we were building this.)

Scoped to your client: another tenant's key reads as not-found.

For your monthly debit this is the answer to "the call timed out, did the customer get
charged?" — and because a transfer is synchronous, a 200 from it means the charge landed
and a 404 means it did not.

**The older endpoint is still the wrong one**, and the reasoning is worth keeping: `GET /v1/transactions/by-provider-reference/{ref}` takes the
*provider's* reference, which exists only after a successful provider call — so for the
ambiguous case you would most want it for, it returns a confident `404` that means "no
record" and "possibly live" at the same time. **Do not build crash recovery on it.**

Persist your key and the returned `transfer_id` together anyway, writing the key *before*
the call — it saves a lookup on the happy path. But you no longer depend on it: if you lost
the response, ask `GET /v1/requests/{key}`.

---

## F. Operations, limits, compliance

### 26 — Authentication and keys

`Authorization: Bearer <key>`. A static token — no signing, no OAuth, no expiry.

**Scoping is coarser than you asked for.** There are exactly two scopes: `client` (yours —
sees only your wallets, 403 on anything else) and `internal` (ours). **There is no
read-only or charge-only key.** Any key that can read your wallets can also move money in
them.

If separation of duties matters, the lever available today is **separate client ids** — a
different client per purpose, each with its own key and its own wallets — but wallets do not
cross that boundary, so it only helps if the duties also separate the wallets. For PGDock
they do not. Treat your key as a payment credential.

Rotation: `POST /v1/admin/clients/{client_id}/api-keys/{key_id}/rotate`, which we run for
you. A key is shown once, at issuance.

### 27 — Rate limits

**100 requests/minute** for a client key, fixed window. Every response carries:

```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 94
X-RateLimit-Reset: 1760000460      ← unix seconds
```

Over: `429` with `code: "RATE_LIMITED"`. **There is no `Retry-After`** — use
`X-RateLimit-Reset`, which is on every response including the 429. It is on our list.

Two things in your favour: the limit is **per key**, and **a replayed idempotent request
does not consume it** — the cache replays before the limiter runs, so retries are free.

Your stated volume (hundreds of organisations, one debit each per month) is nowhere near
this. Spread your monthly billing run across the hour rather than firing it all at `:00`
and you will not notice the limit.

### 28 — Regulatory scope and KYC

Wallets, virtual accounts and payouts sit behind our licensed partners; iswallet is the
ledger and the API over them. KYC tiers and limits are enforced per wallet (item 8).

**A business wallet needs a director's BVN before a virtual account can be issued** — so
before it can be funded at all. Plan for collecting it at organisation onboarding, not at
first charge. Debits and withdrawals additionally respect the tier ceilings.

Where PGDock sits in the regulatory picture — whether you are an agent, a merchant of
record, or neither — is not a question engineering should answer. Put it to our compliance
team before launch; we will make the introduction.

### 29 — Tax documents

iswallet does not issue tax documents and does not compute VAT. Nothing in the system
produces a customer-facing receipt.

What exists is the transaction record and the export (item 3), which is the evidence a tax
document would be built from. Whether VAT applies to our fees and what must appear on whose
document is a finance and compliance question, and we would rather connect you to the
people who can answer it than guess in an engineering document.

### 30 — Settlement

**Instantly, in naira, in your operating wallet.** When you debit a customer, the money
moves from their wallet to yours in a single database transaction — there is no settlement
cycle, no T+1, and no separate account to wait for. `GET /v1/platform/account` shows the
balance.

You hold **naira**. Getting it to your bank is a payout you initiate: `POST /v1/outflows`
from the operating wallet, which is asynchronous and carries the outbound fee (item 8).
Converting to dollars is a separate decision and not something we do for you today.

So the FX exposure is yours and it is real: you price in dollars, hold naira, and choose
when to convert. Worth modelling before launch.

### 31 — Finance reports

`GET /v1/ledger/export?from=&to=&format=csv` is the one report, scoped to your client,
10,000 rows per call. It is a transaction listing, not a settlement file.

There is **no FX gain/loss report and no fee report**. For FX we would have nothing to tell
you — you do your own conversion (item 30), so the position and the P&L are both yours.
Fees are visible per transaction on the export.

If a daily settlement file in a particular shape matters, tell us the columns; it is a
thin layer over the export rather than new machinery.

---

## G. Sandbox and go-live

### 32 — Sandbox

Base URL `https://synledger.name.ng/iwallet`. Credentials are issued per client; say the
word and we will provision yours.

One call sets up a working environment:

```http
POST /v1/admin/clients/pgdock/sandbox/provision
{ "funding_amount_kobo": 1000000000, "employee_wallet_count": 10 }
```

It returns your keys (once), a funded operating wallet, and pre-created customer wallets.
We run it — it needs an internal key.

Simulators, all sandbox-only (`403 SCOPE_INSUFFICIENT` in production):

| Simulate | Endpoint |
|---|---|
| A bank transfer into a virtual account | `POST /v1/sandbox/simulate/deposit` |
| A payout ending confirmed / failed / reversed | `POST /v1/sandbox/simulate/outflow` |
| A credit clawed back by the bank | `POST /v1/sandbox/simulate/reversal` |
| Platform FX inventory (if you ever convert) | `POST /v1/sandbox/simulate/liquidity` |
| A crypto balance in a wallet | `POST /v1/sandbox/simulate/crypto-deposit` |

The deposit simulator drives the **real** deposit path, so the webhook you get in sandbox is
the one production sends.

**What you cannot simulate**, from your list:

- **A failed debit** — not directly, but you do not need a simulator: debit more than the
  balance for `INSUFFICIENT_FUNDS`, freeze the wallet for `WALLET_INACTIVE`, or exceed the
  tier for the limit codes. Those are the real refusals, not stubs.
- **A revoked mandate** — there are no mandates (§0.1).
- **A delayed webhook** — no injection point. Simulate it on your side; it is your retry
  logic being tested, not ours.
- **An ambiguous timeout** — the case your reconciliation most needs to be right about.
  A proxy that drops our response is the only way to exercise it, and it is worth doing.

One trap: **sandbox is not routinely reset and `owner_ref` is globally unique.** Use a
distinct prefix per environment or a rebuilt dev database will collide with your last run —
and `409 WALLET_ALREADY_EXISTS` returns the existing wallet, so the collision looks like
success.

### 33 — Go-live

Not an engineering answer. Business verification, the security review and production key
issuance are owned by the iSpend commercial and compliance teams, and the lead time depends
on them rather than on us. We will put you in touch; flag the compliance question in
item 28 at the same time, since the two run together.

### 34 — Contacts

Also not ours to assign unilaterally — we will come back with a named technical contact and
an escalation path rather than invent one here. Send us your webhook URL and egress IPs
meanwhile (we do not operate an allowlist today, but they are useful for incident
forensics).

---

## H. What we owe you

In the order we would fix them for your integration:

1. ~~**Lookup by idempotency key**~~ — **shipped 12 October**, before you started building
   (item 25). It was the top item for three integrators; you were the third to ask.
2. **A typed duplicate error** instead of the 24-hour `500` (item 11). Now the top item.
3. ~~**Rate history readable by a client key**~~ — **shipped 12 October** (item 19).
4. **Structured metadata on transfers** (items 10, 15) — so invoice and refund links stop
   living in a narration string.
5. **`Retry-After` on 429** (item 27).
6. **A decision on the USD/NGN rate and spread** (§0.3, and now §J.1) — commercial, not code,
   and the one we would like an answer on before you launch.
7. **Enforcing the order of a conversion and a withdrawal inside the backing window**
   (§J.4) — found by testing your path end to end, and a decision we owe you rather than a
   bug we are hiding.

## J. Addendum, 13 October 2026 — you can hold dollars now, and it changes §0.3

Everything above described a naira wallet with a USD price converted at charge time, and
§0.3 warned you about the rate that conversion uses. Since then we have built the other
half: a wallet can hold **USDT that is really backed, really reconciled, and withdrawable to
an address you control.** That is new, it is not what you asked for, and it changes the answer
to the question you asked that mattered most commercially.

**This section is the worked example and the error list you asked for.** It has run end to
end against the real code, through the same HTTP API you would call.

### J.1 Why this matters to you, in one paragraph

§0.3 told you that USD→NGN comes from openexchangerates — **the official rate** — with a
**zero** spread, and that in Nigeria the official rate has historically diverged from the rate
at which naira actually buys dollars. Converting **NGN→USDT instead** prices off a weighted
average of Binance P2P, Luno/Quidax and CoinGecko: **market reality, not the official rate.**
The under-collection risk in §0.3 goes away.

It is not free. The fiat→crypto spread is **150 basis points**, where fiat→fiat was zero. So
the choice in front of you is a real one:

| | Price USD→NGN (§0.3) | Convert NGN→USDT (this section) |
|---|---|---|
| Rate source | openexchangerates, **official** | P2P + local + global, **market** |
| Spread | **0 bps** | **150 bps** |
| What you hold | naira | dollars (USDT) |
| Pay a dollar cost with it | convert later, somehow | withdraw USDT to your provider |

**That is a pricing decision for your team and ours, and it should be made before launch
rather than after a month-end.** We would rather raise it now, again, than have you find it
in a reconciliation.

### J.2 The worked example — five calls, real numbers

An organisation's naira wallet funded with ₦500,000. PGDock converts ₦200,000 and pays a
cloud bill with the proceeds.

**1. Quote the conversion.**

```http
POST /v1/convert/quotes
{ "source_wallet_id": "<the org's NGN wallet>", "target_currency": "USDT", "amount": 20000000 }
```

Every field, as it comes back:

```json
{ "quote_id": "cvq_c4c1dfbb-9cbc-498c-b253-ad0a98cb68ea",
  "source_wallet_id": "78885ab1-9a34-4f58-b458-5cea2a1db504",
  "source_currency": "NGN", "debit_amount": 20000000,
  "target_currency": "USDT", "credit_amount": 145324000,
  "amount_side": "source", "fx_rate": "0.00072662",
  "fees": { "platform_fee": 0, "total_debit": 20000000 },
  "expires_at": "2026-10-13T15:56:11.217833Z",
  "created_at": "2026-10-13T15:55:11.217852Z" }
```

₦200,000 buys **145.324000 USDT** — **₦1,376.24 per USDT** effective, against a mid of
**₦1,355.58**. As a price that is 1.52% rather than the 1.50% headline, because 150 bps comes
off the rate and you are reading its reciprocal; quote the per-USDT figure on an invoice, not
the basis points.

`fx_rate` is USDT **per kobo** at 8 decimal places, as a decimal string rather than a float, so
it survives a parse unchanged. Amounts are **minor units throughout**: kobo for NGN (scale 2)
and 1e-6 USDT (scale 6). `platform_fee` is a fee you can ask us to add on top; it is zero
unless you set it. The quote holds for **60 seconds** and moves nothing.

**2. Execute it.**

```http
POST /v1/convert
{ "quote_id": "cvq_…", "client_idempotency_key": "pgdock-inv-2026-10-0417" }
```

The organisation's NGN balance falls to ₦300,000 and its **USDT balance is 145.324000**. Both
balances are on the **same wallet** — there is no second wallet to create and no
`target_wallet_id` to pass; the target is derived from the source wallet's owner, which is why
converting into someone else's wallet is not expressible.

**Idempotent on your key**: the same `client_idempotency_key` returns the original conversion
rather than converting twice.

**3. Read the balance** (unchanged from item 2): `GET /v1/wallets/{id}/balance` reports every
currency the wallet holds, each with its own `scale`.

**4. Withdraw the USDT to your provider's address.**

```http
POST /v1/wallets/{id}/withdraw
{ "chain_code": "tron", "currency_code": "USDT", "amount": 145324000,
  "to_address": "T…", "client_idempotency_key": "pgdock-wd-2026-10-0417" }
```

→ **`202 Accepted`** with an `operation_id`. This is **asynchronous**: the balance is held
immediately, the coins leave when our provider confirms, and `wallet.debit.posted` follows.
The address is validated for the chain before anything moves — a Tron address must start with
`T` and be 34 characters, and a mismatch is a `422 INVALID_ADDRESS` with nothing debited.

**5. The confirmation** arrives as a webhook (§E, item 22) and the operation reaches
`confirmed_success`.

### J.3 Error codes on the new path

Every one is a typed code with a stable meaning. The two columns that matter operationally
are whether to retry and what fixes it.

| Code | HTTP | Retry? | What it means |
|---|---|---|---|
| `INVALID_PAIR` | 422 | no | source and target currency are the same |
| `QUOTE_EXPIRED` | 422 | **re-quote** | the 60 seconds elapsed |
| `QUOTE_ALREADY_USED` | 409 | no | that quote was already executed; read the conversion |
| `INSUFFICIENT_FUNDS` | 422 | no | the wallet's available balance will not cover the debit |
| `INSUFFICIENT_LIQUIDITY` | 422 | **later** | we cannot back the currency you are converting into. **This is also what you get if we have not enabled the currency at all** — see J.4 |
| `SHORT_LIMIT_REACHED` | 422 | **later** | we permit an unbacked position in this currency and it is at its configured ceiling. Distinct from the line above because the remedy is ours and different |
| `FX_UNAVAILABLE` | 503 | **yes** | no rate for the pair right now |
| `INVALID_ADDRESS` | 422 | no | the destination does not match the chain |
| `INVALID_REQUEST` | 400 | no | a malformed or incomplete body |

A `500` means a defect on our side. **If you ever see one on this path, tell us** — one of
these codes was missing until 13 October and fell through to a `500`, which told the caller to
retry something that would never succeed. We would rather hear about the next one from you
than from a log.

### J.4 What we will not pretend is settled

- **A conversion into a currency we have not switched on is refused**, with
  `INSUFFICIENT_LIQUIDITY`, every time. Enabling USDT for live conversions is a decision on
  our side with a licensing question behind it (how much of a dollar position we may carry
  against naira), and it has to be answered before your first charge, not during it.
- **There is a window where we owe you dollars we have not yet bought.** When a conversion
  completes we buy the matching dollars at our provider immediately afterwards, usually within
  the minute. In that window the position is unbacked — deliberately, because the alternative
  is refusing your conversion — and we watch it: the position is reported every five minutes,
  reconciled nightly, and a purchase that cannot complete is escalated by name.
- **If you withdraw inside that window, today the withdrawal is accepted and settles against a
  balance we have not acquired yet.** We found this by testing your exact path end to end
  rather than the two halves separately. Nothing is lost and nothing is miscounted — the
  accounts show it plainly and the nightly walk now flags it — but the honest statement is
  that the ordering is not yet enforced, and whether it should be refused up front or left to
  settle is a decision we owe you. **If your charge converts and withdraws in the same minute,
  tell us, because that is the case that makes it urgent.**
- **No limits are configured on USDT balances.** Item 28 said this about every non-naira
  balance and it is still true.
- **The withdrawal is to an address you give us, and it is final.** There is no recall. A
  wrong address is a lost payment, and that is a property of the chain rather than of us.

### J.5 Added to what we need from you

- **Which model** — naira wallet with a USD price (§0.4), or naira wallet converting to USDT
  (this section)? They are not exclusive, and the second is only worth the 150 bps if you
  actually need dollars to spend.
- **A decision on the 150 bps**, the same way §0.3 asked for one on the zero.
- **Whether a single charge converts and withdraws in the same minute** (J.4).

## I. What we need from you

- Confirmation that §0.2 — your key debits without customer consent — is acceptable, or
  a decision to bill through the merchant API instead.
- Your position on §0.3: which rate, and whether you expect a spread.
- The tier you need on customer wallets (item 8), and your expected monthly charge size.
- Your webhook URL per environment, and egress IPs.
- Whether you want deposits `pass_through` or `absorb` (item 8).

---

*Sections 0–I checked against the running code on 12 October 2026; §J on 13 October 2026,
against an end-to-end test of the path it describes. Where this document and the code disagree,
the code is right — tell us, and we will fix whichever is wrong.*
