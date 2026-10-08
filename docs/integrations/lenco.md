# Lenco integration

Lenco is a Nigerian business bank. The connector is `connectors/lenco` (`lenco@1`), built on Lenco's API v1 from its public reference at lenco-api.readme.io (read 6 October 2026). v2 is Lenco Pay, a different product whose examples are all in Zambian kwacha.

## Connection

| Field | |
| --- | --- |
| `api_token` | From Lenco support (support@lenco.ng). Also the webhook key |
| `account_id` | Optional: the 36-character id of the account to pay from (`list_accounts` shows them); a step can name another |
| `environment` | `sandbox` uses `sandbox.lenco.co`; anything else is live (`api.lenco.co`) |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `list_accounts` | `GET /accounts` | read | Balances in kobo |
| `get_balance` | `GET /account/{id}/balance` | read | |
| `list_banks` | `GET /banks` | read | Six-digit bank codes; also the connection test |
| `resolve_account` | `GET /resolve` | read | Run before paying a new account |
| `transfer` | `POST /transfer` | idempotent write | NGN to a bank account or a saved recipient. Usually returns `pending` |
| `get_transfer` | `GET /transfer/by-reference/{ref}` | read | |
| `create_virtual_account` | `POST /virtual-accounts` | unsafe write | Dynamic, or static with a BVN |

**Amounts.** Taskiem amounts are kobo. Lenco takes and returns naira as decimal strings (`"2000.00"`); the connector converts exactly, and an amount it cannot read exactly is an error, never a zero. Fees round up to the kobo. Virtual-account `amount`/`min_amount` are sent as naira numbers: Lenco's reference types them as numbers and does not state the unit, so check them in the sandbox before relying on them.

**Never twice.** The engine's key is the transfer `reference`. Lenco refuses a reference it has seen ("Duplicate client reference", code 04); the connector then fetches the transfer by reference and reports it, so a resend after a lost response returns the original transfer. A transfer Lenco reports `failed` fails the step with Lenco's `reasonForFailure` (nothing moved). `declined` is not explained in Lenco's documentation, so it parks the step for a person.

## Webhooks

Lenco sends every event to the one URL registered with its support, so there is one trigger, `event`: register the URL from the workflow's Endpoints tab (`/hooks/{tenant}/connectors/lenco@1/event?env=prod&connection=<name>`). Deliveries are verified as Lenco documents: `X-Lenco-Signature` is HMAC-SHA512 of the body keyed with the hex SHA-256 of the API token. Lenco sends no event id, so repeats are dropped on event and transaction id.

| Events | Correlation (signal steps wait for `lenco@1:event`) |
| --- | --- |
| `transaction.successful`, `transaction.failed` | the transfer's `reference` |
| `virtual-account.transaction`, `.transaction.settled`, `.rejected-transaction` | the virtual account's `account_reference` |

Lenco retries unacknowledged webhooks hourly for 24 hours and advises polling as a fallback; `get_transfer` is that poll.

## To confirm with Lenco before go-live

1. Whether `/transfer` (queued) or `/transactions` (synchronous) is preferred; the connector uses `/transfer`.
2. What `declined` means and whether it is final.
3. Reference length limits and uniqueness scope (the connector sends 36 characters).
4. Whether the webhook signature covers the raw body (the PHP example) or re-serialised JSON (the Node example); the connector verifies the raw body.
5. The unit of virtual-account `amount`/`minAmount`.
