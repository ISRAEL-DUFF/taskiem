# iswallet integration

iswallet is iSpend's wallet platform and the payment provider for Payrolla and iSpend. The connector is `connectors/iswallet` (`iswallet@1`). It follows iswallet engineering's answers of 5 October 2026, kept verbatim in [iswallet-answers-2026-10-05.md](iswallet-answers-2026-10-05.md); section numbers below refer to that document.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /v1/wallets/{id}/balance` | read | Returns every currency with its own `scale`; spend from `available`, not `total` |
| `transfer` | `POST /v1/transfers` | idempotent write | Wallet to wallet, same client and currency. Final on 200, no fee, does not use the daily limit. Compensated by a transfer the other way |
| `payout` | `POST /v1/outflows` | idempotent write | NGN bank payout. Returns `pending` with an `outflow_id`; the outcome arrives as an `outflow_event`. Debits amount + fee. **Not compensable** once confirmed (§B6) |
| `get_payout` | `GET /v1/outflows/{id}` | read | The authoritative state of a payout, with the wallet it was debited from |
| `name_enquiry` | `POST /v1/name-enquiry` | read | Run before paying a new account |
| `list_banks` | `GET /v1/banks` | read | Also the connection test |

The idempotency key travels in the `Idempotency-Key` header (`tsk_` + 32 characters). Account numbers and names are sealed as personal data.

## Connection fields

| Field | Secret | Meaning |
| --- | --- | --- |
| `api_key` | yes | The client's API key (client scope, §A2) |
| `webhook_secret` | yes | The subscription's signing secret, for the webhook triggers |
| `wallet_ids` | no | Optional. Comma-separated wallet ids this connection may use; see [below](#one-client-shared-by-many-customers) |
| `shared_client` | no | `true` when the key also reaches other customers' wallets; such a connection is refused without `wallet_ids` |

## One client shared by many customers

iswallet keys are client-scoped: a key reaches every wallet of its client (§A2). When one client serves several customers, as Payrolla's client serves every company on Payrolla, the key in one company's connection could move another company's money. Taskiem guards this in the connector ([decision 0031](../decisions/0031-shared-provider-clients.md)):

- **The allow-list.** With `wallet_ids` set, `transfer` (`from_wallet_id`), `payout` (`wallet_id`) and `get_balance` (`wallet_id`) naming any other wallet fail the step as fatal with `WALLET_NOT_ALLOWED`, before any request is sent; `get_payout` withholds a payout debited from another wallet (also `WALLET_NOT_ALLOWED`). A transfer's destination is not limited, so a company can pay its employees' wallets. A compensating transfer back from an employee's wallet is refused unless that wallet is listed too; list it, or settle such a reversal by hand.
- **Shared clients must list wallets.** `shared_client: true` with no `wallet_ids` is refused when the connection is saved or its credentials replaced (`WALLET_ALLOW_LIST_MISSING`), and, if one is stored anyway, every wallet call refuses with the same code. **A shared client's key is always provisioned with both fields.**
- **Payrolla** provisions each company's sub-tenant connection through the partner API with its own key, `shared_client: "true"` and that company's wallets:

  ```http
  POST /v1/partner/sub-tenants/{sub}/connections
  {"environment": "prod", "connector": "iswallet@1", "name": "main",
   "credentials": {"api_key": "<payrolla client key>", "shared_client": "true", "wallet_ids": "6f1e…,7a02…"}}
  ```

  When a company opens or closes a wallet, replace the connection's credentials with the new list.
- **Events.** iswallet subscriptions are per client (§E5), so every company's events reach one subscription. Point it at the **partner's own** tenant: an iswallet connection there holds `webhook_secret` (and, optionally, `wallet_ids` listing Payrolla's own wallets, such as the funding wallet). Each verified event is delivered there and then into every Payrolla sub-tenant whose connection lists the envelope's `wallet_id`, as if it had arrived at that sub-tenant's own URL: the same dedup on the envelope `id`, the same `outflow_id` correlation, the sub-tenant's own subscribed workflows. Events for a wallet no sub-tenant (and no Payrolla connection) lists are recorded, alerted (`event_unrouted`) and listed by `GET /v1/partner/event-routes?status=unrouted` ([embedding](../embedding.md#13-routing-provider-events-to-sub-tenants)).
- `wallet.transfer.completed` fires once per side with each side's `wallet_id`, so a transfer between two companies reaches both, each its own side.

Wallet-scoped keys and per-wallet subscriptions at iswallet would add a second layer; they have been asked for (P4-W1 in [needs people](../needs-people.md#phase-4)) and the guard does not depend on them.

## Webhook triggers

Deliveries are verified with `hmac_sha256_timestamped`: HMAC-SHA256 over `<X-iSpend-Timestamp>.<raw body>`, refused outside 5 minutes. The secret is the subscription's, stored in the connection as `webhook_secret`, so use one iswallet subscription per Taskiem connection (for a client shared by a partner's customers, one subscription at the partner: [above](#one-client-shared-by-many-customers)).

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
- A payout iswallet marks `unresolved` sends no event; the waiting step times out (72 hours in the Payrolla flow) and the run fails for an operator. `reversed` can arrive days after `confirmed`; the ops alert workflow listens for it, and the Payrolla flow keeps watching each confirmed payout's `outflow_id` after its report and sends Payrolla a reversal notice ([guide](payrolla.md#reversal-notices)).

## Workflows using it

- `flows/dogfood/payrolla-salary-disbursement.wd.json`: transfers and payouts, waiting on `outflow_event` for confirmation and, after the report, for reversals.
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
