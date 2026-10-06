# Breet integration

Breet turns crypto deposits into naira or cedi: deposit addresses per customer, optional automatic settlement to a bank, and withdrawals to wallets and banks. The connector is `connectors/breet` (`breet@1`), built from Breet's public documentation and OpenAPI description at docs.breet.io (read 6 October 2026).

## Connection

| Field | |
| --- | --- |
| `app_id`, `app_secret` | From the Breet dashboard (Settings → For Developer) |
| `pin` | The withdrawal PIN set on the dashboard; required for withdrawals. Wrong PINs freeze the account |
| `environment` | `sandbox` sends `X-Breet-Env: sandbox`; anything else is production |
| `webhook_secret` | Breet's webhook secret, sent as `x-webhook-secret` |
| `bank_withdrawal_unit` | `local` or `usd`: see below. Bank withdrawals are refused until it is set |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `list_assets` | `GET /trades/assets` | read | Asset ids for `generate_address`; also the connection test |
| `get_balances` | `GET /users/fetch-integration` | read | Fiat balances only; the response's secrets and owner details are dropped |
| `generate_address` | `POST /trades/sell/assets/{id}/generate-address` | unsafe write | One address per label and asset; a repeat returns the existing one |
| `get_deposit` | `GET /trades/transactions/{id}` | read | |
| `list_banks`, `verify_bank_account` | `GET /payments/banks`, `POST /payments/banks/validate` | read | |
| `add_bank` | `POST /payments/banks/add` | unsafe write | At most three; a repeat returns the saved one |
| `withdraw_crypto` | `POST /payments/withdraw/address` | reconcilable write | USD (cents) or crypto units |
| `withdraw_to_bank` | `POST /payments/withdraw/bank/{saved bank id}` | reconcilable write | See below |
| `get_withdrawal` | `GET /payments/withdrawal/{id or externalId}` | read | Also the reconcile action |

**Never twice.** Breet has no idempotency key. The engine's key is sent as `externalId`, which Breet can look up, so withdrawals are reconcilable: after a lost response the engine asks Breet for the withdrawal by `externalId` and records it, and sends again (under a new key) only when Breet has no record. Breet's own guard refuses the same amount to the same address within a minute; the connector then looks for its own withdrawal and fails the step if there is none (the refusal was for someone else's). `rejected` and `reversed` withdrawals, which Breet refunds, fail the step.

**Bank withdrawal amounts.** Breet's documentation disagrees: the API reference and the withdrawals guide say the amount is in NGN or GHS, and the off-ramp guide says it is in USD. A factor of about 1,500 rides on it, so the connector does not choose. Confirm the unit with Breet for your account and set `bank_withdrawal_unit` on the connection; the step's `currency` must then match it (NGN or GHS for `local`, USD for `usd`), and its `amount` is in that currency's minor units.

**Amounts.** USD values and crypto quantities are passed through as exact decimal text (`amount_usd`, `crypto_received`); withdrawal amounts are minor units, fees rounded up, balances rounded down to what can be spent.

## Webhooks

A Breet integration has one webhook URL for every event, so there is one trigger, `event`. Set the URL from the workflow's Endpoints tab on the Breet dashboard (`/hooks/{tenant}/connectors/breet@1/event?env=prod&connection=<name>`). Breet does not sign deliveries: it sends the shared secret in `x-webhook-secret`, compared in constant time; Breet also publishes source IPs, which can be allowed at the load balancer. Delivery is at least once and unordered: repeats are dropped on `eventId`, and the docs advise re-fetching before acting on money, which `get_deposit` and `get_withdrawal` do.

| Events | Correlation (signal steps wait for `breet@1:event`) |
| --- | --- |
| `trade.pending`, `.completed`, `.flagged`, `.deleted` | the deposit address |
| `withdrawal.pending`, `.processing`, `.completed`, `.reversed`, `.rejected` | the withdrawal id |
| `trade.address.created` | the address |

Credit customers on `trade.completed` only; `trade.flagged` is held below the asset's minimum.

## To confirm with Breet before go-live

1. **The unit of bank withdrawal amounts** (local currency or USD).
2. Whether a repeated `externalId` is refused.
3. Whether the one-minute duplicate guard applies to bank withdrawals.
4. Whether sandbox and production use separate credentials and webhook secrets.
