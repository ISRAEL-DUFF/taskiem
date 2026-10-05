# iswallet integration

iswallet is iSpend's wallet platform and the payment provider for Payrolla and iSpend. The connector is `connectors/iswallet` (`iswallet@1`). It follows iswallet engineering's answers of 5 October 2026, kept verbatim in [iswallet-answers-2026-10-05.md](iswallet-answers-2026-10-05.md); section numbers below refer to that document.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /v1/wallets/{id}/balance` | read | Returns every currency with its own `scale`; spend from `available`, not `total` |
| `transfer` | `POST /v1/transfers` | idempotent write | Wallet to wallet, same client and currency. Final on 200, no fee, does not use the daily limit. Compensated by a transfer the other way |
| `payout` | `POST /v1/outflows` | idempotent write | NGN bank payout. Returns `pending` with an `outflow_id`; the outcome arrives as an `outflow_event`. Debits amount + fee. **Not compensable** once confirmed (§B6) |
| `get_payout` | `GET /v1/outflows/{id}` | read | The authoritative state of a payout |
| `name_enquiry` | `POST /v1/name-enquiry` | read | Run before paying a new account |
| `list_banks` | `GET /v1/banks` | read | Also the connection test |

The idempotency key travels in the `Idempotency-Key` header (`tsk_` + 32 characters). Account numbers and names are sealed as personal data.

## Webhook triggers

Deliveries are verified with `hmac_sha256_timestamped`: HMAC-SHA256 over `<X-iSpend-Timestamp>.<raw body>`, refused outside 5 minutes. The secret is the subscription's, stored in the connection as `webhook_secret`, so use one iswallet subscription per Taskiem connection.

| Trigger | Events | Correlation (for signal steps) |
| --- | --- | --- |
| `outflow_event` | `wallet.outflow.confirmed`, `.failed`, `.reversed` | `outflow_id` |
| `credit_event` | `wallet.credit.posted`, `.reversed`, `.rejected` | `wallet_id` |
| `transfer_event` | `wallet.transfer.completed`, `.failed` | `wallet_id` |

Repeats are dropped on the envelope `id`. Signal steps wait for `iswallet@1:<trigger>`, for example `iswallet@1:outflow_event` correlated on the payout's `outflow_id`. Register the URL Taskiem shows on the workflow's Endpoints tab (`/hooks/{tenant}/connectors/iswallet@1/outflow_event?env=prod&connection=<name>`).

## How the connector keeps "never twice, never lost"

iswallet has no lookup by our key yet (§0.1), and its replay cache lasts 24 hours, after which a repeated key returns a 500 even though nothing moves (§0.2). So:

- **Within 24 hours of a key's first use**, a lost response or a 500 is an unknown outcome: Taskiem resends the same key and iswallet replays the original answer. Pinned by `e2e.TestPayrollaDisbursementEndToEnd`, which loses a transfer's response after the money moved.
- **After 23 hours** the connector refuses to resend at all and parks the step in `needs_reconciliation` (`effects.ErrIndeterminate`); an operator checks the wallet's ledger and resolves it. Workflows also cap write retries at 6 hours.
- **`IDEMPOTENCY_KEY_REUSED`** parks the step too: the key was used before, so the payment may have happened.
- A write whose retries run out after any unknown outcome **parks rather than fails**, because "failed" would invite someone to pay again (engine-wide since this integration).
- `RATE_LIMITED`, `CUSTODY_UNAVAILABLE`, `SUBACCOUNT_SYNCING`, `LIQUIDITY_EXHAUSTED` and 503 are retried; limits, insufficient funds, `OUTFLOW_REJECTED` and other 4xx fail the step with iswallet's code and request id.
- A payout iswallet marks `unresolved` sends no event; the waiting step times out (72 hours in the Payrolla flow) and the run fails for an operator. `reversed` can arrive days after `confirmed`; the ops alert workflow listens for it.

## Workflows using it

- `flows/dogfood/payrolla-salary-disbursement.wd.json`: transfers and payouts, waiting on `outflow_event`.
- `flows/dogfood/ops-payout-failure-alert.wd.json`: `outflow_event` (failed, reversed).
- `flows/dogfood/ispend-credit-exceptions.wd.json`: `credit_event` (rejected, reversed).

## Limits to plan for (§0.3, §A3)

- A new wallet is TIER_1: ₦50,000 per transaction **and per day** for bank payouts. Upgrade the Payrolla funding wallet's KYC tier before the pilot. Wallet-to-wallet transfers are bound by the per-transaction limit only.
- 100 requests a minute per API key, with no bulk endpoint: about 600 employees an hour for Payrolla, everything included. iswallet will raise the limit per client on request. Taskiem does not yet pace calls to a connector's rate limit; 429s are retried with backoff.

## Waiting on iswallet (their §H)

1. Lookup by idempotency key (removes the 24-hour seam).
2. A typed duplicate error instead of the 500.
3. `Retry-After` on 429, and a higher rate limit for Payrolla.
4. Bulk payouts with per-item outcomes.
5. Structured metadata on transfers (today Payrolla's ids go in the narration).
6. A parked-credit signal for client keys (for an iSpend alert).

## Waiting on us

Client names for the two API keys (`payrolla`, `ispend`), Payrolla's largest payroll and the tier it needs, sandbox provisioning parameters (employee wallets, funding), Taskiem's egress IPs and webhook URLs per product.
