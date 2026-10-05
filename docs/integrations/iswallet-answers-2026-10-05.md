# iswallet → Taskiem: answers to the connector questions

**For:** Taskiem engineering
**From:** iswallet Engineering
**Date:** 5 October 2026
**Against:** *iswallet questions for the Taskiem connector*

Answers are under each question ID. Everything here was checked against the running code
today, not against a spec. Where the code and this document disagree, the code is right —
tell us.

**Read §0 first.** One of your `must` items is not built, and one of your assumptions
about duplicate protection is true for 24 hours and then stops being true. Both affect the
guarantee you say your design rests on, so they are not buried in the list.

**Docs:** OpenAPI at `https://synledger.name.ng/iwallet/openapi.json`, Swagger UI at
`/docs`. Use it — with two warnings. It was regenerated on 24 September, so routes
added since are absent; and **`POST /v1/outflows` is missing from it entirely**, which
is probably the first endpoint you went looking for. It is documented in §B1 below and
is the authoritative description until we regenerate. (`POST /v1/multispend` is also
listed under the wrong path, `/v1/wallets/multispend`; irrelevant to you, but it tells
you how much to trust the spec over this document.)

---

## 0. The three things that change your design

### 0.1 D1/D2 — there is no lookup by your reference. This is the one real blocker.

You cannot today ask iswallet "did the transfer with key `tsk_…` happen?". No endpoint
takes a client idempotency key. Your crash-recovery step has no server to ask.

We are not going to dress this up. It is already the top item on our build list and it is
being worked on now; see §D1 for what exists in the meantime, why the near-miss workaround
is a trap rather than a workaround, and why the fix is weeks rather than months.

The good news is narrower than it sounds but real: **every webhook we send you carries your
own idempotency key** in `data.idempotency_key` (§E3). So correlation *after the fact* is
solved. What is missing is only the synchronous question, in only one window: you sent the
request and never saw the response.

### 0.2 C2 — duplicate protection is two mechanisms with different lifetimes

This matters because your retry policy will meet the seam.

| Window | What happens on a repeat | What you see |
|---|---|---|
| **Within 24 hours** | the original response is replayed from a cache | the original 202 and `outflow_id` — exactly what you want |
| **After 24 hours** | the cache has expired; the request re-executes and is refused by a permanent database constraint | **`500 INTERNAL_ERROR`** |

Money is safe in both. Beyond 24 hours the second payout is refused **before any provider
call**, so nothing moves twice. We pinned that today rather than asserting it —
`TestOutflow_E2E_ReusedClientKeyDebitsOnceButReportsNothingUseful` initiates a payout,
replays the key, and asserts the wallet moved once; with the constraint dropped the test
fails by paying twice.

But the **response is a lie**. A 500 means "unknown, try again" in your taxonomy and in
ours, and here it means "already done, stop". A connector that trusts its own retry policy
will retry that forever. There is no error code naming the duplicate.

Both halves of this are ours to fix and both are in §H. Until they are: **a retry more
than 24 hours after the original is not safe to automate.** Age your pending transfers and
escalate the old ones to a human.

### 0.3 The limits will stop your Payrolla pilot on day one, and you did not ask

A wallet's payout ceiling comes from its **owner type** and its KYC **tier**, and a new
wallet is TIER_1. For the business and client owner types — which is what Payrolla's
company funding wallet will be:

| Tier | Per transaction | Per day (cumulative) |
|---|---|---|
| TIER_1 | ₦50,000 | ₦50,000 |
| TIER_2 | ₦500,000 | ₦1,000,000 |
| TIER_3 | ₦5,000,000 | ₦10,000,000 |

**A new company wallet can pay one ₦50,000 salary per day.** Even at TIER_3 a run is
capped at ₦10m/day in bank payouts. Upgrade via `POST /v1/wallets/{id}/kyc` before the
pilot, and tell us the payroll size you need — these are seeded defaults carrying an
`[OPEN] confirm with compliance` note, not ratified policy, and for a group-internal
product we can revisit them.

Two asymmetries you need, because neither is intuitive and neither is documented anywhere
else:

- **Bank payouts** (`/v1/outflows`) check the per-transaction limit *and* consume the daily
  cumulative counter.
- **Wallet-to-wallet transfers** (`/v1/transfers`) check both but **do not consume** the
  daily counter. So paying employees into their iswallet wallets is bounded only by the
  per-transaction limit; paying them to bank accounts is bounded by both.

Which rail Payrolla uses therefore changes the ceiling by orders of magnitude.

**One defect we found while answering you, and have now fixed.** The customer owner type
— `client_end_user`, which every iSpend end-user wallet uses — had **no limit rows at
all**, and a missing row reads as "no limit" rather than "no money". An owner-type rename
a couple of dozen migrations ago left the published PSB floors stranded on the old name,
so those wallets were uncapped. A configured per-client override could not rescue them
either: the narrowing comparison was against a base of zero, and nothing is less than
zero, so a client that had asked us for a ₦20,000 ceiling had none.

Both halves are fixed and pinned by tests; the end-user caps are now the published PSB
floors:

| Tier | Per transaction | Per day (cumulative) |
|---|---|---|
| TIER_1 | ₦50,000 | ₦50,000 |
| TIER_2 | ₦200,000 | ₦300,000 |
| TIER_3 | ₦1,000,000 | ₦5,000,000 |

So iSpend customer wallets are **more** restricted than the business table above, not
less, and a TIER_1 customer cannot withdraw more than ₦50,000 in a day. The
`EXCEEDS_SINGLE_TXN_LIMIT` and `EXCEEDS_DAILY_LIMIT` branches in your connector are live
on both rails and both wallet kinds — build them properly rather than treating them as
theoretical.

One scope note so you are not surprised later: **no owner type has limits in any currency
except NGN.** USDT and the other non-naira balances are uncapped for everyone pending
compliance numbers. Irrelevant to Payrolla and iSpend as specified, relevant the moment
either touches a non-NGN balance.

---

## A. Access and environments

### A1 — Base URLs

- **Sandbox:** `https://synledger.name.ng/iwallet`
- **Production:** issued at go-live. Not yet published.

### A2 — Authentication

`Authorization: Bearer <key>`. A plain static bearer token — no signing, no OAuth, no
token exchange, no expiry.

Keys have a **scope**, and the scope is the whole access model:

- `client` — sees only wallets whose `client_id` is this client's. Cross-client access is a
  403. This is the scope you use.
- `internal` — sees everything, plus the `/v1/admin/*` surface. We hold these.

**One integration per product means one client per product**, which is what you asked for:
a `payrolla` client and an `ispend` client, each with its own key, its own rate-limit
bucket, its own webhook subscriptions, and no visibility into the other's wallets. We issue
them; say the word and name the two clients.

A key is shown **once**, at issuance. Rotation is `POST
/v1/admin/clients/{client_id}/api-keys/{key_id}/rotate`, which we run for you.

### A3 — Rate limits

| Scope | Limit |
|---|---|
| `client` | **100 requests/minute** |
| `internal` | 1,000 requests/minute |

Fixed window, per API key. Every response carries:

```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 94
X-RateLimit-Reset: 1760000460      ← unix seconds, when the window resets
```

Over the limit: **`429`** with `code: "RATE_LIMITED"`.

**There is no `Retry-After`.** Use `X-RateLimit-Reset` — it is on every response, including
the 429, and it is the exact instant the window rolls. `Retry-After` is on the list in §H.

Two things in your favour:

- The window is **per key**, so Payrolla's budget and iSpend's are separate.
- **A replayed idempotent request does not consume the limit.** The cache replays before
  the rate limiter runs, so your retries are free. Deliberate; keep retrying.

One caveat to size against: the counter is **in-process**, so it is per instance rather
than cluster-wide. Treat 100/min as the floor you are guaranteed, not the ceiling you will
observe, and do not build against the slack.

**Do the arithmetic for Payrolla before you build.** One payout per employee at 100/min is
600 employees/hour, and that is the whole budget — nothing left for balance reads or status
polls. There is no bulk payout endpoint (§B5). If a run is larger than a few hundred
people, tell us the size and we will raise the limit for that client; that is a
configuration change, not a build.

### A4 — IP allow-listing

**No.** There is no IP allow-list, in either direction, and nothing in the code reads the
caller's address for authorization. Send us your egress IPs anyway — they are useful for
incident forensics and they are what we would configure first if you want this added.

---

## B. Moving money

### B1 — Endpoints

**Payrolla can pay either way, and this is the most important design choice in your
connector**, because the two rails differ in finality, cost, speed and limits:

| | Wallet → wallet | Wallet → bank account |
|---|---|---|
| Endpoint | `POST /v1/transfers` | `POST /v1/outflows` |
| Response | **`200`, final** | `202`, pending |
| Settlement | synchronous, in one DB transaction | asynchronous, NIP |
| Fee | none | provider + platform fee, **surcharged** |
| Reversible | only by a compensating transfer | no (§B6) |
| Daily limit consumed | **no** | yes |
| Constraint | both wallets must be the **same client** and the **same currency** | NGN only |

So employees with iswallet wallets under the Payrolla client are paid by transfer —
instant, free, and final before your call returns. Employees paid to bank accounts go
through `/v1/outflows` and you wait for a webhook.

**Wallet → wallet transfer**

```http
POST /v1/transfers
Authorization: Bearer <key>
Idempotency-Key: tsk_a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6

{ "from_wallet_id": "6f1e…", "to_wallet_id": "9c2a…",
  "amount": 25000000, "currency": "NGN", "narration": "Sept salary" }
```

```json
{ "transfer_id": "…", "txn_id": "…", "from_wallet_id": "6f1e…", "to_wallet_id": "9c2a…",
  "amount": 25000000, "currency": "NGN", "status": "completed",
  "completed_at": "2026-10-05T09:14:02Z" }
```

`status` is always `completed` on a 200. There is no pending state and no webhook to wait
for. If you got a 200, the money is in the employee's wallet and the ledger is balanced.

(₦250,000 in both examples here is above the TIER_1 per-transaction cap, so copied
verbatim against a fresh wallet they return `422 EXCEEDS_SINGLE_TXN_LIMIT`. Deliberate —
it is the first thing a real payroll run meets. See §0.3.)

**Payout to a bank account**

```http
POST /v1/outflows
Authorization: Bearer <key>
Idempotency-Key: tsk_a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6

{ "wallet_id": "6f1e…", "amount": 25000000, "currency": "NGN",
  "narration": "Sept salary",
  "destination": { "bank_code": "044", "account_number": "0690000031",
                   "account_name": "Ada Obi" },
  "metadata": { "run_id": "payroll-2026-09", "employee_id": "emp-417" } }
```

```json
{ "outflow_id": "8d3f…", "operation_id": "8d3f…", "wallet_id": "6f1e…",
  "amount": 25000000, "currency": "NGN", "status": "pending",
  "fee": 5350, "net_amount": 25000000,
  "provider_reference": "tsk_a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
  "destination": { … }, "created_at": "2026-10-05T09:14:02Z" }
```

**The `fee` in that example is illustrative.** It is computed server-side from the live
schedule for the provider that will execute, so it varies by amount, provider and
client. Never hardcode it; read it from the response, or `GET /v1/fees` if you need it
before the call.

Four more things in that response that will bite if you skim it:

1. **`outflow_id` is the only durable handle you get.** Persist it before you do anything
   else. Until §D1 ships it is your *only* way back to this payout (§0.1).
2. **The fee is a surcharge, not a deduction.** The beneficiary receives `amount`; the
   wallet is debited `amount + fee`. `net_amount` is the beneficiary's figure, equal to
   `amount`. Fund `amount + fee`.
3. **Do not send `fee_amount`.** If you do, it is read as an *assertion* and a mismatch is
   refused with `FEE_MISMATCH` rather than silently overridden. Omit it and accept the
   computed fee; read `GET /v1/fees` if you need to quote beforehand.
4. **`provider_reference` may be empty** on an otherwise successful 202. That is the
   ambiguous case — see §B4.

`metadata` is a free-form JSON object on outflows and is echoed back on reads. **Transfers
have no metadata field** — only `narration`, max 255 characters. If Payrolla needs
structured run/employee identifiers on wallet-to-wallet salary payments, today they go in
the narration string. Structured transfer metadata is in §H.

**Wallet balance**

`GET /v1/wallets/{wallet_id}/balance`

```json
{ "wallet_id": "6f1e…", "currency": "NGN",
  "balance":  { "total": 500000000, "available": 500000000, "pending": 0 },
  "balances": [ { "currency": "NGN", "total": 500000000, "available": 500000000,
                  "pending": 0, "scale": 2 } ],
  "scale": 2, "as_of": "2026-10-05T09:14:02Z" }
```

Read **`balances[]`**, not `balance` — the top-level object is the wallet's canonical
currency only and exists for older integrators. Use **`available`**: `total` includes money
committed to a pending outflow. A currency with no balance is **absent from the array**,
not zero. Each entry carries its own `scale`, so B2 is answerable at runtime.

Two more you will want:

- `GET /v1/banks` — bank codes for the `destination.bank_code` field.
- `POST /v1/name-enquiry` — NIP name resolution. Call it before a payout to a new account;
  a wrong-but-valid account number is not recoverable (§B6).

### B2 — Amount units and currency

**Integer minor units. Always. Every amount field, in and out.** Never decimal, never a
string, never major units.

NGN is scale 2, so **kobo**: ₦250,000.00 is `25000000`.

Bank payouts are **NGN only** — the providers are Nigerian. Wallets are multi-currency
(USD, EUR, GBP, GHS at scale 2; USDT/USDC at 6; ETH at 18), so a balance read can return
non-NGN rows, but you cannot pay those out to a bank. Read `scale` per currency from
`balances[]` rather than hardcoding; that is the field that stops the 1,000× class of bug.

### B3 — Transfer statuses

Two separate state machines, because there are two rails.

**Transfers** (`/v1/transfers`): `completed`. That is the whole set. Synchronous and final.

**Outflows** (`/v1/outflows`), as returned by `GET /v1/outflows/{id}`:

| `status` | Meaning | Final? | What you do |
|---|---|---|---|
| `pending` | accepted; with the provider | **no** | wait for the webhook |
| `confirmed` | the beneficiary's bank was credited | **yes** | done |
| `failed` | definitively failed; the wallet was made whole | **yes** | the money is back; retry under a *new* key if you still want to pay |
| `reversed` | was confirmed, then recalled by the provider (NIP return, chargeback) | **yes** | money is back *after* you were told it succeeded — see below |
| `unresolved` | we waited past our own deadline and still do not know | **no** | **do not retry.** A human resolves it |
| `resolved` | an operator resolved an `unresolved` payout by hand | **yes** | read the outcome from the ledger or ask us |

Two that will surprise a state machine built from the usual four:

- **`reversed` arrives after a terminal success.** Your `confirmed` handler must not assume
  nothing further can happen to that payout. For Payrolla this means an employee can be
  marked paid and then unpaid, days later. You get `wallet.outflow.reversed` (§E1).
- **`unresolved` is not failure and is not success.** It is our honest "we do not know",
  set when the provider never gave us an answer within the operation's deadline. Retrying
  it is the one action that can actually double-pay, because the original may still be
  live. Alert instead.

### B4 — Error classification

Envelope, on every error:

```json
{ "error": {
    "code": "INSUFFICIENT_FUNDS",
    "message": "insufficient available balance",
    "request_id": "req_1f7b30eeae50",
    "doc_url": "https://docs.ispend.io/errors/INSUFFICIENT_FUNDS"
}}
```

`request_id` is **inside `error`**, and also on the `X-Request-ID` response header. There
is no top-level `request_id`. The header is the one always present. Log it — it is what we
search on.

**Nothing happened. Safe to retry with the same key.**

| Status | Code |
|---|---|
| 429 | `RATE_LIMITED` |
| 500 | `CUSTODY_UNAVAILABLE` — we could not verify the funding account. Refused before any money moved |
| 503 / connection failure | nothing reached the handler |

**Rejected for good. Do not retry; the request is wrong, or the state is wrong.**

| Status | Codes |
|---|---|
| 400 | `MISSING_IDEMPOTENCY_KEY`, `IDEMPOTENCY_KEY_TOO_LONG`, `INVALID_IDEMPOTENCY_KEY`, `INVALID_REQUEST`, `INVALID_NARRATION`, `INVALID_DESTINATION` |
| 403 | wallet belongs to another client |
| 404 | `WALLET_NOT_FOUND`, `OUTFLOW_NOT_FOUND` |
| 422 | `INSUFFICIENT_FUNDS`, `WALLET_INACTIVE`, `CURRENCY_MISMATCH`, `EXCEEDS_SINGLE_TXN_LIMIT`, `EXCEEDS_DAILY_LIMIT`, `CLIENT_LIMIT_EXCEEDED`, `KYC_REQUIRED`, `FEE_MISMATCH`, `FEE_NOT_CONFIGURED`, `PROVIDER_NOT_CONFIGURED`, `CLIENT_FEE_EXCEEDS_CAP`, `OUTFLOW_REJECTED`, `IDEMPOTENCY_KEY_REUSED` |

Notes on four of those:

- **`OUTFLOW_REJECTED`** is the provider refusing definitively — bad account number, dead
  bank. The wallet has already been made whole by the time you see it. A retry under the
  same key will not help and a retry under a new key will fail the same way until the
  destination changes.
- **`IDEMPOTENCY_KEY_REUSED`** means you sent the same key with a *different body*. We
  fingerprint the body against the key; replaying would hand you a success describing an
  operation you did not ask for. **Non-retryable** — it is a key-generation bug. Note it
  fires on a changed body, not on a repeat, so a retry that re-serialises the same
  request with map ordering differences will trip it. Serialise deterministically.
- **`EXCEEDS_DAILY_LIMIT`** is the one you will hit in the Payrolla pilot. See §0.3.
- **`SUBACCOUNT_SYNCING`** and **`LIQUIDITY_EXHAUSTED`** (both 422) are ours, not yours,
  and are **retryable after a delay** — the wallet's money is there, we just cannot execute
  from it this instant. Back off a minute. `LIQUIDITY_EXHAUSTED` also pages us.

**We don't know yet.**

- **`202` with `status: "pending"` and an empty `provider_reference`.** This is the
  ambiguous outcome and it does **not** look like an error. When the provider call times
  out or 5xxs, we deliberately keep the payout pending rather than reverse a transfer that
  may be live, and we return 202. So a 202 means *"accepted by us; outcome with the
  provider unknown"* — never *"the provider accepted it"*. Wait for the webhook.
- **Any other `500 INTERNAL_ERROR`.** Ambiguous by construction. Retry with the **same
  key** — within 24 hours that is safe and gets you the original answer (§0.2).

### B5 — Bulk transfer

**No.** There is no bulk transfer or bulk payout endpoint. One HTTP call per payment, at
100/min (§A3).

Bulk endpoints exist for *wallets* (`POST /v1/wallets/bulk`) and *virtual accounts*
(`POST /v1/wallets/{id}/virtual-accounts/batch`), both with per-item results, so the shape
you want is established — it just does not exist for money yet. It is in §H, and your
Payrolla volume is the argument for its priority. Tell us the largest run you expect.

(`POST /v1/multispend` is not this. It spends from several wallets *of one owner* into one
destination — the opposite fan-out. Its OpenAPI entry also has the wrong path; see §0.3.)

### B6 — Reversal or refund by API

**No.** There is no endpoint that reverses a completed payout or transfer.

This is a real constraint on your compensating-action design, so here is exactly what you
have:

- **Wallet → wallet:** compensate with a **second transfer in the opposite direction**,
  under its own key. Both wallets must be the same client's, which for Payrolla's own
  wallets they are. This is the supported pattern; another tenant uses it for corrections.
- **Wallet → bank, still `pending`:** nothing to do and nothing you can do. Wait. It will
  land as `confirmed` or `failed`; `failed` makes the wallet whole automatically.
- **Wallet → bank, `confirmed`:** **the money has left the group.** There is no API, and
  there is no out-of-band fix either — recovering it means the beneficiary's bank's recall
  process. `reversed` happens only when the provider initiates it.

So in Taskiem's terms: **a confirmed bank payout is not a compensable step.** Order your
workflows so that anything that can fail happens *before* the payout, not after. If a later
step can fail and you need the whole unit undone, the payout cannot be in that unit.

This is also why `POST /v1/name-enquiry` matters: a payout to a valid account number
belonging to the wrong person is indistinguishable from a successful payment, forever.

### B7 — Confirmation steps (OTP, PIN, second approval)

**None, on any API path.** No OTP, no transaction PIN, no second approval, no maker-checker
on `/v1/outflows` or `/v1/transfers`. A valid bearer key moves money in one call. Nothing
to disable.

Which puts the whole control burden on key custody and on your side's authorisation. Two
asks, not conditions:

- Separate keys per product (§A2), so a leak is bounded to one.
- Whatever approves a payroll run on Taskiem's side is the *only* approval in the chain.
  There is no second pair of eyes downstream.

(Maker-checker exists for *ledger corrections* in our admin console, under dual control. It
is not on the payment path and is not available to a client key.)

---

## C. Duplicate protection

### C1 — Idempotency key

**`Idempotency-Key`, an HTTP header.** Required on every POST that moves money — a missing
header is `400 MISSING_IDEMPOTENCY_KEY`, not a silently-unprotected call.

There is no body field for it on outflows or transfers. (`/v1/convert` additionally
accepts `client_idempotency_key` in the body; irrelevant to you.)

### C2 — Repeat behaviour and retention

**See §0.2 — this is the answer that matters most and it has a seam in it.**

Short form: within 24 hours you get **the original response replayed**, same status, same
body, same `outflow_id`. After 24 hours you get **`500 INTERNAL_ERROR`** and no money
moves. Retention of the replay cache is **24 hours**; the underlying no-double-pay
constraint is **permanent**.

There is one more case worth naming because it is the nastier one. The permanent constraint
is on `(provider_code, idempotency_key)` — scoped to the provider that executed. We route
payouts across providers by cost and liquidity, so in principle a repeat beyond 24 hours
that routes to a *different* provider than the original would not collide. We have never
seen it and the window requires a >24h retry, but it is the reason the fix in §H is a real
lookup rather than a longer cache.

### C3 — Key format

- **Maximum length: 255 characters.**
- **Allowed: anything except ASCII control characters** (`< 0x20`, `0x7f`). No charset
  restriction beyond that.
- `tsk_` + 32 lower-case alphanumerics is well inside both. 

A useful side effect we checked for you: **all three of our bank providers pass a
reference in your format through to the bank unchanged.** Each sanitises references that
contain illegal characters or exceed the provider's length cap; yours needs neither, so
your key appears verbatim as the provider's own reference. That is what makes §D1's
near-miss workaround *almost* work — and see §D1 for why you must not use it.

### C4 — Key scope

Two different scopes for the two mechanisms, which is worth knowing precisely:

- **The 24-hour replay cache** is keyed on `(API key id, Idempotency-Key)`. Scoped to your
  key. Your Payrolla and iSpend keys cannot collide with each other or with any other
  tenant.
- **The permanent no-double-pay constraint** is keyed on `(provider_code,
  idempotency_key)` — **not** scoped to a client. Two tenants using the same key string on
  the same provider would collide.

With a `tsk_`-prefixed 32-character random suffix, collision is not a practical concern.
We mention it because it is a latent cross-tenant coupling and you asked precisely the
right question to find it.

---

## D. Checking a transfer

### D1 — Lookup by our reference

**Not built.** No endpoint accepts a client idempotency key. See §0.1.

**What exists, and the one you must not use:**

| Endpoint | Takes | Use it? |
|---|---|---|
| `GET /v1/outflows/{id}` | our `outflow_id` from the 202 | **yes** — this is your status source |
| `GET /v1/wallets/{id}/transactions?status=&from=&to=` | wallet + filters, cursor-paged | yes, for reconciliation sweeps |
| `GET /v1/wallets/{id}/transactions/{txn_id}` | ledger txn id | yes |
| `GET /v1/transactions/by-provider-reference/{reference}` | the **provider's** reference | **no — see below** |

`GET /v1/outflows/{id}`:

```json
{ "outflow_id": "8d3f…", "operation_id": "8d3f…", "wallet_id": "6f1e…",
  "amount": 25000000, "currency": "NGN", "status": "confirmed",
  "provider_reference": "tsk_a1b2…", "fee": 5350,
  "confirmed_at": "2026-10-05T09:15:40Z", "created_at": "2026-10-05T09:14:02Z" }
```

**Why the provider-reference lookup is a trap, not a workaround.** Because your key passes
through to the provider unchanged (§C3), `GET /v1/transactions/by-provider-reference/tsk_…`
will often find your payout. It is tempting and it is exactly wrong for your use case: that
endpoint matches on the reference the *provider* confirmed back to us, and we only record
it **after a successful provider call**. In the ambiguous case — the timeout, the crash, the
one you are recovering from — there is no recorded reference, and the endpoint returns a
confident **404**.

A 404 that means "never happened" in most cases and "live, but not yet recorded" in exactly
the case you are asking about is worse than no endpoint at all. Do not build your
crash-recovery decision on it.

**What we suggest until the real lookup ships:**

1. **Persist your key and the `outflow_id` together, writing the key *before* the HTTP
   call** and the `outflow_id` on response. You then have a handle for everything except a
   crash in the request window itself.
2. **For that window, retry with the same key within 24 hours.** The replay cache will hand
   you the original `outflow_id` (§0.2). This is the actual answer today and it covers the
   realistic crash — a process that dies and restarts in minutes.
3. **Beyond 24 hours, escalate to a human.** Do not automate it: you will get a 500 you
   cannot distinguish from a transient fault, and the correct action is a ledger check.
4. **Rely on webhooks for correlation.** Every event carries your key (§E3), so a payout
   you lost the handle to will still announce itself with your own identifier on it.

### D2 — Clear "not found"

For `GET /v1/outflows/{id}`: **yes, unambiguously.** `404` with `code:
"OUTFLOW_NOT_FOUND"`. An id we never issued cannot be confused with anything else.

For the provider-reference lookup: **no**, and this is the trap in §D1. `404` with
`TRANSACTION_NOT_FOUND` conflates "never existed" with "exists but has no provider
reference yet".

Since the reference you would want to query by is the one we do not have a lookup for, the
honest summary is: there is no endpoint today that can safely answer "this reference was
never received".

### D3 — Visibility delay

**None, for `GET /v1/outflows/{id}`.** The operation row and the Phase-1 ledger debit are
written in the **same database transaction**, which commits before the 202 is returned. One
primary, no read replica in the path. If you have the `outflow_id`, it is readable
immediately — including in the ambiguous 202 case, where the payout is already there as
`pending`.

Transfers likewise: the 200 returns after the ledger transaction commits, so a balance read
issued afterwards reflects it.

---

## E. Webhooks

### E1 — Events

Registration is self-service: `POST /v1/webhooks/subscriptions` with `endpoint_url` and an
`event_types` array. You are delivered only the types you list.

Events relevant to the connector, all confirmed as actually emitted:

| Event | Fires when | Payload fields (under `data`) |
|---|---|---|
| `wallet.outflow.confirmed` | a bank payout reached the beneficiary | `outflow_id`, `operation_id`, `wallet_id`, `amount`, `fee`, `currency`, **`idempotency_key`**, `provider_reference`, `confirmed_at` |
| `wallet.outflow.failed` | definitively failed; wallet made whole | `outflow_id`, `operation_id`, `wallet_id`, `amount`, `fee`, `currency`, **`idempotency_key`**, failure reason |
| `wallet.outflow.reversed` | a **confirmed** payout was recalled | `outflow_id`, `operation_id`, `wallet_id`, `amount`, `fee`, `total_reversed`, `currency`, **`idempotency_key`**, `provider_reference`, `reversed_at` |
| `wallet.credit.posted` | money credited to a wallet — **this is your "wallet funded"** | `wallet_id`, `amount`, `currency`, `balance_after`, `txn_id`, `operation_id`, `source_type`, `source_ref` |
| `wallet.credit.reversed` | a credit we already confirmed was clawed back | credit identifiers + reversal reference |
| `wallet.credit.rejected` | a transfer *into* a virtual account was refused and refunded to the sender | credit identifiers |
| `wallet.debit.posted` | any debit posted to a wallet | `wallet_id`, `amount`, `currency`, `txn_id` |
| `wallet.transfer.completed` | a wallet-to-wallet transfer settled — **fires twice**, once for each side | transfer identifiers, `from_wallet_id`, `to_wallet_id` |
| `wallet.transfer.failed` | a transfer was refused | identifiers + `failure_reason` |
| `wallet.inflow.confirmed` | a pull-charge inflow succeeded | inflow identifiers |

Two notes:

- **`wallet.transfer.completed` is delivered twice per transfer** — one event for the
  sender's wallet, one for the receiver's — with different `wallet_id`. For an
  internal-to-internal salary payment both are your own wallets, so you get both. Dedupe on
  `(txn_id, wallet_id)`, not `txn_id` alone, or you will treat the second as a redelivery
  and drop a real event.
- **There is no `topup.failed`.** The nearest is `wallet.credit.rejected` (§F3).

**The delivery body is an envelope.** The fields above are one level down, under `data`:

```json
{
  "id": "evt_8f21…",
  "idempotency_key": "wallet.outflow.confirmed:<txn_id>:<wallet_id>:<subscription_id>",
  "event_type": "wallet.outflow.confirmed",
  "schema_version": "v1",
  "occurred_at": "2026-10-05T09:15:40Z",
  "wallet_id": "6f1e…",
  "data": { "outflow_id": "8d3f…", "idempotency_key": "tsk_a1b2…", … }
}
```

Note the two different `idempotency_key` fields: the **envelope's** is ours, per delivery;
the one inside **`data`** is **yours**. That is not a naming we are proud of. See §E3.

### E2 — Signature

- **Algorithm:** HMAC-SHA256, hex-encoded.
- **Header:** `X-iSpend-Signature: sha256=<hex>`.
- **Signed string:** `"<timestamp>.<raw request body>"` — the timestamp from
  `X-iSpend-Timestamp`, a literal `.`, then the **raw bytes** of the body. Read and verify
  before parsing; re-serialised JSON will not match.
- **Replay window:** reject if `abs(now − X-iSpend-Timestamp) > 300` seconds.
- **Compare in constant time.**
- **Secret:** generated per subscription and returned **once**, in the `201` from
  `POST /v1/webhooks/subscriptions`. It is never shown again — a lost secret means deleting
  the subscription and creating another.

Also sent: `X-iSpend-Idempotency-Key`, the per-delivery key (same value as the envelope's
`idempotency_key`).

### E3 — Correlating and deduping

**Link to your transfer:** `data.idempotency_key` — **your own key**, verbatim, on all
three `wallet.outflow.*` events. Also `data.outflow_id` if you kept it.

This is the field that makes the §D1 gap survivable: you can always recognise your own
payout in an event, even if you lost the `outflow_id`. Credit and transfer events do not
carry it (they carry `txn_id` and `source_ref` instead).

**Dedupe:** the envelope's **`id`** is the stable event identifier, constant across every
retry of that event.

Prefer deduping on the **ledger fact** — `data.txn_id`, or `(txn_id, wallet_id)` for
transfer events (§E1) — over the delivery key. It is strictly stronger: it also protects
you if we ever emit two events for one movement, which is the failure mode a delivery-key
dedupe cannot see.

### E4 — Retries and ordering

- **10 attempts**, then the delivery is **dead-lettered** and we stop.
- Backoff: `min(30s × 2ⁿ, 6h)` plus up to 25% jitter. Attempt 1 is immediate; the ten
  attempts span roughly **8 to 11 hours** — the 6h cap is never reached inside ten
  attempts, so the last gap is about 4¼ hours, not 6.
- Success is any `2xx`. Anything else, including a timeout at 30s, is a failure.
- A dead-lettered delivery can be replayed by us, and by you:
  `POST /v1/webhooks/subscriptions/{id}/deliveries/{delivery_id}/retry`. List them with
  `GET /v1/webhooks/subscriptions/{id}/deliveries`.

**Ordering: no guarantee, and you should assume it is violated.** Deliveries are claimed in
`next_attempt_at` order by concurrent workers, so even first attempts can interleave; and
once anything is retried it is reordered by construction. The realistic case for you:
`wallet.outflow.confirmed` for one payout arriving after `wallet.outflow.reversed` for
another, or a `reversed` arriving while you are still processing the `confirmed` it
supersedes.

Make your handlers order-independent and drive state from the event's own content, not from
arrival sequence. If you need the authoritative current state, `GET /v1/outflows/{id}` is
always right (§D3).

### E5 — Per-environment and per-product endpoints

**Yes, both, and they fall out of the model rather than needing a feature:**

- **Per product:** a subscription belongs to a `client_id`, and you have one client per
  product (§A2). Payrolla's subscription and iSpend's are separate rows with separate URLs,
  separate secrets and separate `event_types`. A wallet's events only ever fan out to its
  own client's subscriptions.
- **Per environment:** sandbox and production are separate deployments with separate
  databases. Subscriptions created in one do not exist in the other.
- **Several per client** is allowed too — useful for sending `wallet.outflow.*` to your
  payments service and `wallet.credit.posted` to reconciliation, as separate subscriptions
  with narrow `event_types`.

---

## F. Wallet top-ups (iSpend)

### F1 — How money enters, and whether it pends

**Two paths, and they behave completely differently.**

**1. Bank transfer to a virtual account** — the normal path, and the one iSpend uses.
`POST /v1/wallets/{id}/virtual-account` issues a NUBAN; a customer transfers to it; the
provider tells us; we credit and emit `wallet.credit.posted`. A wallet can hold **several**
virtual accounts, one per provider and per client reference, and re-issuing the same
reference returns the existing account rather than creating a second.

**There is no card top-up.** None. Do not build for it.

**2. Pull-charge collection** — `POST /v1/wallets/{id}/inflow` initiates a charge through a
provider. **This one is genuinely asynchronous** and sits `pending` until the provider
resolves it, exactly like an outflow.

**Now the part your workflow is built on.** For path 1: **we settle it ourselves, and there
is nothing for you to chase.** A deposit that reaches us is credited within seconds of the
provider's webhook. So **replace the "top-ups pending after 15 minutes" workflow** — you
would be polling a queue that is empty by construction.

But do not just delete the idea, because there *is* a stuck state and it is one you cannot
see:

**Parked credits.** When a credit arrives that we cannot attribute to a wallet — a payment
with a reference matching no virtual account, a reversal of a credit we never posted, a
settlement for a deposit we never saw — we **park** it rather than guess or drop it. Parked
money is real, sitting in the provider's account, belonging to someone, and waiting for an
operator. Your customer has paid and has no credit.

That is the condition worth alarming on, and it is **not visible to a client key** —
`GET /v1/admin/webhooks/parked` needs an internal key. So:

- The genuinely useful iSpend workflow is not "pending top-ups" but **"a customer says they
  paid and we have no credit"**, reconciled against our transaction list (§F2) — a
  deposit-level reconciliation, not a status poll.
- If you want to alarm on parked credits directly, tell us. Exposing a parked count per
  client is a small change and a far better signal than a 15-minute timer.

The other two non-settling states are `unresolved` (§B3), which needs a human, and a
dead-lettered webhook if **your** endpoint was down for 24 hours (§E4) — in which case the
money was credited and you simply never heard. Reconcile periodically regardless of
webhooks; that is the only defence against a dead-letter.

### F2 — Listing and looking up transactions

`GET /v1/wallets/{wallet_id}/transactions`, with `status`, `type`, `from`, `to` (RFC3339),
`limit` (1–100, default 20) and `cursor`. Cursor-paged. This is your reconciliation sweep.

For a single one: `GET /v1/wallets/{id}/transactions/{txn_id}`, or
`GET /v1/transactions/by-provider-reference/{reference}` for the provider's own reference
as it appears on a bank statement — tenant-scoped, and subject to the §D1 caveat about what
its 404 does and does not mean.

For bulk export: `GET /v1/ledger/export` across all your wallets by date range, max 10,000
rows — stay inside a 90-day window.

### F3 — "Wallet funded" and "top-up failed" webhooks

**Funded: yes** — `wallet.credit.posted`, with `balance_after` so you need no follow-up
read. `source_type` tells you where it came from; the complete set is `pull_inflow` (bank
transfer into a virtual account), `wallet_transfer` (wallet to wallet) and `va_deposit`
(a legacy deposit path). Filter on it if iSpend should react only to real customer deposits.

**Failed: not as such.** A bank transfer that reaches us does not fail — it is credited or
it is parked (§F1), and parking emits nothing to you. The two nearest events:

- **`wallet.credit.rejected`** — a transfer into a virtual account was refused by the
  provider and refunded to the sender. Nothing was credited and nothing will be; from the
  payer's side money left their account and no receipt will ever arrive. **Subscribe to
  this one** — it is the closest thing to "top-up failed" and it is invisible otherwise.
- **`wallet.credit.reversed`** — a credit we already confirmed was clawed back. You will
  have acted on the original: issued a receipt, released a service. Handle it.

---

## G. Sandbox testing

### G1 — Test wallets and funding

One call sets up everything, and it was built for this exact shape of integration:

```http
POST /v1/admin/clients/{client_id}/sandbox/provision
{ "funding_amount_kobo": 1000000000, "employee_wallet_count": 10 }
```

Returns a **client key and an internal key** (once only), a **funded company wallet**, and
**pre-created employee wallets** — so a Payrolla run is testable end to end on first
contact. Default funding is ₦10,000,000 (`1000000000` kobo). It needs an internal key, so
we run it; say how many employee wallets and what funding you want.

To fund a wallet afterwards: `POST /v1/sandbox/simulate/deposit` with the virtual account
number (or `wallet_id`) and an amount. It drives the **real deposit path**, not a stub, so
the `wallet.credit.posted` webhook you get in sandbox is the one you will get in
production.

The `/v1/sandbox/*` endpoints exist only where sandbox mode is on. In production they
return `403` with `code: "SCOPE_INSUFFICIENT"` — so a connector that leaves a
simulation call in a production path fails loudly rather than quietly doing nothing.

### G2 — Forcing pending, failed and reversed

Better than magic amounts — an explicit control:

```http
POST /v1/sandbox/simulate/outflow
{ "operation_id": "8d3f…", "outcome": "confirmed" | "failed" | "reversed" }
```

Create a payout normally (it stays `pending`, since the sandbox provider does not settle on
its own), then drive it to whichever terminal state you want. This reaches the same
resolution path as a real provider webhook, so the events you receive are the real ones.
Notably it is how you test the **`reversed`-after-`confirmed`** sequence from §B3, which is
the case most likely to be missing from a connector.

Also available:

- **`POST /v1/sandbox/simulate/reversal`** — reverses a previously simulated **credit**, so
  you can exercise `wallet.credit.reversed` and your receipt-voiding path.
- **Magic bank codes** on `destination.bank_code`: **`000`** confirms instantly, **`999`**
  fails instantly, **`998`** returns a provider decline (Lenco). These short-circuit before
  the provider call, so they work without a live provider sandbox.
- **`POST /v1/sandbox/simulate/subaccount`** — seeds a provider sub-account balance.

What is **not** available: a way to force an ambiguous timeout, or `unresolved`. Those are
the two states your retry logic most needs to be right about, and you will have to simulate
them on your side — a proxy that drops our response, or a test double. Worth doing: §B4's
"we don't know yet" row is the branch that double-pays if you get it wrong.

We do not routinely reset sandbox, and `owner_ref` uniqueness is enforced on
`(owner_ref, currency_code)` **globally**, not per client. Use a distinct `owner_ref`
prefix per environment and per product, or a rebuilt dev database that reuses refs will
collide with rows from your last run — and `409 WALLET_ALREADY_EXISTS` carries the
existing wallet, so the collision looks like success.

---

## H. What we owe you, in priority order

The first two are what we would consider blocking for a connector with your guarantee.

1. **Lookup by idempotency key.** The §0.1 gap. Worth saying why this is weeks and not
   months: all three provider adapters already support status queries keyed on *our*
   client key, so the capability exists below the API — what is missing is the endpoint
   and a lookup on the operation table, not new machinery.
2. **A typed duplicate error.** `409` or `422` with a code naming the duplicate and
   carrying the original `outflow_id`, instead of §0.2's `500`. Small, and it removes the
   infinite-retry failure mode. We will likely ship it with (1) and may simply extend the
   replay window past 24 hours at the same time.
3. **`Retry-After` on 429** (§A3), and a **per-client rate-limit override** — tell us
   Payrolla's payroll size and we will raise its limit before you need it.
4. **Bulk payout, with per-item outcomes** (§B5). Your volume is the argument for its
   priority; send us the numbers.
5. **Structured metadata on transfers** (§B1), so Payrolla's run and employee identifiers
   stop living in a narration string.
6. **The error catalogue**, per endpoint, with retryability — §B4 is the substance of it
   for your two endpoints.
7. **A parked-credit signal for client keys** (§F1), if you want the iSpend alerting
   workflow to be about something real.
8. **`POST /v1/outflows` in the OpenAPI spec**, and the multispend path fixed (see the
   docs note at the top).
9. **Limits for the non-NGN currencies** (§0.3) — with compliance, not with us.

Already done, off the list: **`client_end_user` limit rows** (§0.3), found and fixed while
writing this document.

**From you:** the two client names, your egress IPs, sandbox provisioning parameters
(§G1), the Payrolla payroll size and tier requirement (§0.3), and your webhook URLs per
product.

---

## I. Three questions in your document that changed our answers

Worth saying, because they are the questions that found real problems rather than real
documentation:

1. **C2's "how long are keys remembered"** is why §0.2 exists. The 24-hour seam and the
   500 behind it were not written down anywhere, and we pinned them with a test today
   rather than reason about them.
2. **C4's "is the key scoped per integration, per wallet, or global"** surfaced that our
   two duplicate-protection mechanisms have *different* scopes, one of them cross-tenant.
   Nobody had asked it that precisely before.
3. **D2's "does the lookup return a clear not found"** is the question that turns the
   provider-reference endpoint from a workaround into a trap. We would probably have
   offered it to you as the answer to D1 if you had not asked D2 in the same breath.

---

*Checked against the running code on 5 October 2026. §0.2's claim about money safety is
pinned by `TestOutflow_E2E_ReusedClientKeyDebitsOnceButReportsNothingUseful` and
`TestPayroll_E2E_ReusedKeyAtServiceLayerErrors`; §0.3's end-user limit gap by
`TestEndUserWallet_E2E_HasNoOutflowLimitAtAll`. Where this document and the code
disagree, the code is right — tell us.*
