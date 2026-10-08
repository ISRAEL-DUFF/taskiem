# Anchor integration

Anchor (getanchor.co) is banking as a service: deposit accounts, transfers and account numbers for businesses and their customers. The connector is `connectors/anchor` (`anchor@1`), built from Anchor's public documentation at docs.getanchor.co (read 6 October 2026).

## Connection

| Field | |
| --- | --- |
| `api_key` | Created in the Anchor dashboard (Developers → API keys), per environment |
| `account_id` | Optional: the deposit account to pay from (`…-anc_acc`); a step can name another |
| `environment` | `sandbox` uses `api.sandbox.getanchor.co`; anything else is live |
| `webhook_token` | The token set on the Anchor webhook, for verifying deliveries |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /accounts/balance/{id}` | read | Available, ledger, hold, pending |
| `list_banks` | `GET /banks` | read | NIP codes; also the connection test |
| `verify_account` | `GET /payments/verify-account/{bank}/{number}` | read | |
| `transfer` | `POST /counterparties`, then `POST /transfers` (NIPTransfer) | idempotent write | See below |
| `book_transfer` | `POST /transfers` (BookTransfer) | idempotent write | Between Anchor accounts; compensated by a transfer back |
| `get_transfer` | `GET /transfers/verify/{id}` | read | Anchor re-checks with the provider first |
| `get_transfer_by_reference` | `GET /transfers/by-reference/{ref}` | read | |

**Amounts** are kobo on both sides, as Anchor uses. NIP transfers start at 100 kobo.

**Name check.** `transfer` creates (or reuses: Anchor returns the existing record) the counterparty with Anchor's name verification, which looks the account up at the bank and keeps the bank's name whatever name was sent. The connector compares that name with `account_name` (every word of the shorter name must be in the longer, ignoring case, punctuation and order) and refuses to pay on a mismatch. `check_name: false` turns this off, for business names the bank abbreviates.

**Never twice.** The engine's key travels as `x-anchor-idempotent-key` (Anchor replays the first successful answer for 24 hours) and as the transfer `reference`. On any resend the connector first asks Anchor for the transfer by reference and reports it if it exists, so a resend after the replay window cannot pay twice. A 409 is answered the same way. A `FAILED` transfer fails the step with Anchor's `failureReason`. `COMPLETED` can later become `REVERSED`; listen for `nip.transfer.reversed`.

## Webhooks

Create an Anchor webhook (AtLeastOnce delivery) for the events below, with the URL from the workflow's Endpoints tab (`/hooks/{tenant}/connectors/anchor@1/transfer_event?env=prod&connection=<name>`) and a token, and put the token in the connection. Deliveries are verified as Anchor documents: `x-anchor-signature` is the base64 of the hex HMAC-SHA1 of the body, keyed with the token. Repeats are dropped on the event id. Anchor's events carry ids, not statuses: correlate on the transfer id (`transfer_id` in the action's output) and read the status with `get_transfer`.

| Trigger | Events | Correlation |
| --- | --- | --- |
| `transfer_event` | `nip.transfer.*`, `book.transfer.*` | the transfer id |
| `inflow_event` | `nip.inbound.received`, `nip.inbound.completed`, `payment.received`, `payment.settled`, `payin.received` | the receiving account id |

Give value for inflows on `nip.inbound.completed`; `nip.inbound.received` is still pending.

## To confirm with Anchor before go-live

1. The response to a repeated `reference` without the idempotency key, and to the by-reference lookup when nothing matches (the connector treats 404 as "not found").
2. Whether insufficient funds is ever a synchronous 4xx rather than a `FAILED` transfer.
3. The meaning of the status values beyond PENDING, COMPLETED, FAILED and REVERSED (the connector treats them as in progress).
4. The idempotency window (24 or 48 hours: the pages disagree; the connector does not depend on it).
5. How to create virtual account numbers now that the old endpoint is undocumented.
