# iswallet integration

iswallet is iSpend's wallet platform and a payment provider for the group's products (iSpend; Payrolla pays its customers' staff through it itself, and Taskiem connects to Payrolla as a product, [decision 0044](../decisions/0044-rails-and-products.md)). Through its capabilities (`pay_bank`, `pay_wallet`, `balance`, `resolve_account`, `get_payout`) it is one of the rails templates such as [Run payroll](../templates.md) run on. The connector is `connectors/iswallet` (`iswallet@1`). It follows iswallet engineering's answers of 5 October 2026, kept verbatim in [iswallet-answers-2026-10-05.md](iswallet-answers-2026-10-05.md); section numbers below refer to that document. Their answers to Xenibase of 12 and 13 October 2026 ([iswallet-answers-2026-10-12-pgdock.md](iswallet-answers-2026-10-12-pgdock.md), cited as "Xenibase item N") describe the same API and brought a lookup by our key, the rate-limit headers and event details, all used below.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /v1/wallets/{id}/balance` | read | Returns every currency with its own `scale`; spend from `available`, not `total` |
| `transfer` | `POST /v1/transfers` | idempotent write | Wallet to wallet, same client and currency. Final on 200, no fee, does not use the daily limit. Compensated by a transfer the other way |
| `payout` | `POST /v1/outflows` | idempotent write | NGN bank payout. Returns `pending` with an `outflow_id`; the outcome arrives as an `outflow_event`. Debits amount + fee. **Not compensable** once confirmed (§B6) |
| `get_payout` | `GET /v1/outflows/{id}` | read | The authoritative state of a payout, with the wallet it was debited from |
| `name_enquiry` | `POST /v1/name-enquiry` | read | Run before paying a new account |
| `list_banks` | `GET /v1/banks` | read | Also the connection test |
| `issue_virtual_account` | `POST /v1/wallets/{id}/virtual-accounts/batch` (one account) | idempotent write | A customer's permanent account number on a wallet, its `reference` our key; iswallet answers the same account for the same reference (`existing`). A business wallet needs its director's BVN first. The `virtual_account` capability |
| `list_transactions` | `GET /v1/wallets/{id}/transactions` | read | A wallet's transactions in a window, every page (up to 2,000). The `list_credits` capability keeps credits into customers' numbers |

The idempotency key travels in the `Idempotency-Key` header (`tsk_` + 32 characters). Account numbers and names are sealed as personal data.

## Live and test

Connecting iswallet asks **Live (real money)** or **Test (practice)**, as Paystack's does; the connection keeps the choice, its calls go to that host, and a test connection is marked "Test: no real money" on Connections ([connector/v1](../contracts/connector-v1.md) rule 17). Live is iswallet's production, `https://ispend.name.ng/iwallet` (`TASKIEM_ISWALLET_URL` moves it); test is its sandbox, `https://synledger.name.ng/iwallet` (`TASKIEM_ISWALLET_TEST_URL`). Both serve the `/v1/…` API under that address (iswallet, 26 October 2026). Live and sandbox keys are separate, so paste the key from the same environment. Connections made before modes keep going where they went: `TASKIEM_ISWALLET_URL` when set, else the sandbox.

## Connection fields

| Field | Secret | Meaning |
| --- | --- | --- |
| `api_key` | yes | The client's API key (client scope, §A2) |
| `webhook_secret` | yes | The subscription's signing secret, for the webhook triggers |
| `wallet_ids` | no | Optional. Comma-separated wallet ids this connection may use; see [below](#one-client-shared-by-many-customers) |
| `shared_client` | no | `true` when the key also reaches other customers' wallets; such a connection is refused without `wallet_ids` |

## One client shared by many customers

iswallet keys are client-scoped: a key reaches every wallet of its client (§A2). When one client serves several customers, as a partner's one client may serve every company it works for, the key in one company's connection could move another company's money. Taskiem guards this in the connector ([decision 0031](../decisions/0031-shared-provider-clients.md)):

- **The allow-list.** With `wallet_ids` set, `transfer` (`from_wallet_id`), `payout` (`wallet_id`) and `get_balance` (`wallet_id`) naming any other wallet fail the step as fatal with `WALLET_NOT_ALLOWED`, before any request is sent; `get_payout` withholds a payout debited from another wallet (also `WALLET_NOT_ALLOWED`). A transfer's destination is not limited, so a company can pay its employees' wallets. A compensating transfer back from an employee's wallet is refused unless that wallet is listed too; list it, or settle such a reversal by hand.
- **Shared clients must list wallets.** `shared_client: true` with no `wallet_ids` is refused when the connection is saved or its credentials replaced (`WALLET_ALLOW_LIST_MISSING`), and, if one is stored anyway, every wallet call refuses with the same code. **A shared client's key is always provisioned with both fields.**
- **The partner** provisions each company's sub-tenant connection through the partner API with its own key, `shared_client: "true"` and that company's wallets:

  ```http
  POST /v1/partner/sub-tenants/{sub}/connections
  {"environment": "prod", "connector": "iswallet@1", "name": "main",
   "credentials": {"api_key": "<partner client key>", "shared_client": "true", "wallet_ids": "6f1e…,7a02…"}}
  ```

  When a company opens or closes a wallet, replace the connection's credentials with the new list.
- **Events.** iswallet subscriptions are per client (§E5), so every company's events reach one subscription. Point it at the **partner's own** tenant: an iswallet connection there holds `webhook_secret` (and, optionally, `wallet_ids` listing the partner's own wallets, such as a funding wallet). Each verified event is delivered there and then into every one of the partner's sub-tenants whose connection lists the envelope's `wallet_id`, as if it had arrived at that sub-tenant's own URL: the same dedup, the same `outflow_id` correlation, the sub-tenant's own subscribed workflows. Events for a wallet no sub-tenant (and no connection of the partner's) lists are recorded, alerted (`event_unrouted`) and listed by `GET /v1/partner/event-routes?status=unrouted` ([embedding](../embedding.md#13-routing-provider-events-to-sub-tenants)).
- `wallet.transfer.completed` fires once per side with each side's `wallet_id`, so a transfer between two companies reaches both, each its own side. The two share a `txn_id`, so the dedup key includes the wallet (Xenibase item 22).

Wallet-scoped keys and per-wallet subscriptions at iswallet would add a second layer; they have been asked for (P4-W1 in [needs people](../needs-people.md#phase-4)) and the guard does not depend on them.

## Customers' account numbers

Through `virtual_account` a business gives each customer (each student, for a school) a permanent account number on one of its wallets (`account`), and through `list_credits` reads back what was paid into them: each credit's `provider_reference` is its `credit_id`, `amount_credited` what landed, `fee_charged` the provider's fee when disclosed, and their sum what the payer sent. Deposits arrive net of the provider's fee (1.4%, capped at ₦2,000). iswallet's partner names the accounts; the name asked for is sent but may not be shown. The credit webhook (`wallet.credit.posted`) is **not** mapped to `virtual_account.credited` yet: which of its fields name the account is not documented, so until iswallet says, credits reach the money log only by reading them back. The facts and open questions are in [schools rail facts](../spec/schools-rail-facts.md#iswallet).

## Webhook triggers

Deliveries are verified with `hmac_sha256_timestamped`: HMAC-SHA256 over `<X-iSpend-Timestamp>.<raw body>`, refused outside 5 minutes. The secret is the subscription's, stored in the connection as `webhook_secret`, so use one iswallet subscription per Taskiem connection (for a client shared by a partner's customers, one subscription at the partner: [above](#one-client-shared-by-many-customers)).

| Trigger | Events | Correlation (for signal steps) |
| --- | --- | --- |
| `outflow_event` | `wallet.outflow.confirmed`, `.failed`, `.reversed` | `outflow_id` |
| `credit_event` | `wallet.credit.posted`, `.reversed`, `.rejected` | `wallet_id` |
| `transfer_event` | `wallet.transfer.completed`, `.failed` | `wallet_id` |

Repeats are dropped by movement, as iswallet advises (Xenibase items 22 and 24): `credit_event` and `transfer_event` on the event type, `data.txn_id` and the envelope's `wallet_id` (so both sides of a transfer are kept, and two events for one movement are one), falling back to the envelope `id` when an event carries no `txn_id`; `outflow_event` on the envelope `id`. iswallet makes 10 delivery attempts over about 8 to 11 hours and then stops; deliveries missed past that are listed by `GET /v1/webhooks/subscriptions/{id}/deliveries` and re-sent one at a time with `POST .../deliveries/{delivery_id}/retry`. Order is not guaranteed, so workflows act on each event's content, not its arrival order. Signal steps wait for `iswallet@1:<trigger>`, for example `iswallet@1:outflow_event` correlated on the payout's `outflow_id`. Register the URL Taskiem shows on the workflow's Endpoints tab (`/hooks/{tenant}/connectors/iswallet@1/outflow_event?env=prod&connection=<name>`).

## How the connector keeps "never twice, never lost"

iswallet's replay cache lasts 24 hours, after which a repeated key returns a 500 even though nothing moves (§0.2). Since 12 October iswallet also answers `GET /v1/requests/{idempotency_key}` (Xenibase item 25): the request recorded under our key, with its kind, `resource_id`, status and whether that status is final, or `404 IDEMPOTENCY_KEY_NOT_FOUND`, which iswallet guarantees means nothing was posted (the key is written in the same transaction as the first ledger entry). So:

- **Within 24 hours of a key's first use**, a lost response or a 500 is an unknown outcome: Taskiem resends the same key and iswallet replays the original answer. Pinned by `e2e.TestCapabilityPayoutsOnIswalletEndToEnd`, which loses a payout's response after the money moved.
- **After 23 hours** the connector looks the key up before sending anything. A recorded payout is answered from the record (its `outflow_id` and status; the bank's outcome follows as for any payout); a recorded transfer likewise, or, if not yet final, the step waits and asks again. A key iswallet has no record of is sent again, safely. A key recorded for a different kind of request parks the step for an operator. Workflows also cap write retries at 6 hours.
- **`IDEMPOTENCY_KEY_REUSED`** parks the step too: the key was used before, so the payment may have happened.
- A write whose retries run out after any unknown outcome **parks rather than fails**, because "failed" would invite someone to pay again (engine-wide since this integration).
- `RATE_LIMITED`, `CUSTODY_UNAVAILABLE`, `SUBACCOUNT_SYNCING`, `LIQUIDITY_EXHAUSTED` and 503 are retried; on a 429 the next attempt waits until `X-RateLimit-Reset` (iswallet sends no `Retry-After`, Xenibase item 27); limits, insufficient funds, `OUTFLOW_REJECTED` and other 4xx fail the step with iswallet's code and request id.
- A payout iswallet marks `unresolved` sends no event; the wait times out (72 hours in Run payroll's `await_settlement`), and the payment is reported as not confirmed. `reversed` can arrive days after `confirmed`; the ops alert workflow listens for it, and Run payroll's `await_reversal` watches each confirmed payout for 35 days and records a reversal.

## Workflows using it

- `templates/library/run-payroll.json` on iswallet: bank payouts by capability, waiting on `outflow_event`'s normalised payout events for confirmation and then for reversals.
- `flows/dogfood/ops-payout-failure-alert.wd.json`: `outflow_event` (failed, reversed).
- `flows/dogfood/ispend-credit-exceptions.wd.json`: `credit_event` (rejected, reversed).

## Limits to plan for (§0.3, §A3)

- A new wallet is TIER_1: ₦50,000 per transaction **and per day** for bank payouts. Upgrade a funding wallet's KYC tier before paying a payroll from it. Wallet-to-wallet transfers are bound by the per-transaction limit only.
- 100 requests a minute per API key, with no bulk endpoint: about 600 people an hour for a payroll, everything included. iswallet will raise the limit per client on request. Taskiem does not yet pace calls to a connector's rate limit; 429s wait for the window to reopen, and a resent idempotent request is answered from the replay cache without counting against the limit.

## Waiting on iswallet (their §H)

1. ~~Lookup by idempotency key~~: shipped 12 October and used above.
2. A typed duplicate error instead of the 500: now iswallet's top item.
3. `Retry-After` on 429 (on their list; `X-RateLimit-Reset` is used meanwhile), and a higher rate limit for large payrolls.
4. Bulk payouts with per-item outcomes.
5. Structured metadata on transfers (today a payroll's ids go in the narration).
6. A parked-credit signal for client keys (for an iSpend alert).

## Waiting on us

Client names for the API keys (`ispend`, and one per product that pays through iswallet), the largest payroll expected (Payrolla's beta was 2 people and ₦3,000, which a TIER_1 wallet covers), sandbox provisioning parameters (employee wallets, funding; iswallet provisions a sandbox client in one call), Taskiem's webhook URLs per product, and our egress IPs (iswallet has no IP allow-list today; it wants them for incident forensics).

## Sandbox (Xenibase item 32)

Sandbox-only simulators drive the real paths, so their webhooks match production's: `POST /v1/sandbox/simulate/deposit` (a bank transfer into a virtual account), `/simulate/outflow` (a payout ending confirmed, failed or reversed) and `/simulate/reversal` (a credit clawed back). Failed debits are the real refusals: overdraw for `INSUFFICIENT_FUNDS`, freeze the wallet for `WALLET_INACTIVE`, exceed the tier for the limit codes. A delayed webhook and an ambiguous timeout cannot be simulated at iswallet; Taskiem's suites exercise a lost response (`e2e.TestCapabilityPayoutsOnIswalletEndToEnd`) and the lookup after the replay window (`connectors/iswallet` tests). The sandbox is not reset and `owner_ref` is globally unique, so use a prefix per environment.
