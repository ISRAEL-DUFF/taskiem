# Moniepoint integration

Moniepoint's developer API is Monnify, its payment gateway. The connector is `connectors/moniepoint` (`moniepoint@1`), built from Monnify's public documentation on TeamApt's Confluence space (teamapt.atlassian.net/wiki/spaces/MON, read 6 October 2026). developers.monnify.com blocks automated readers; its "Going Live" page (also read 6 October 2026, as a search excerpt) gives the live host.

## Connection

| Field | |
| --- | --- |
| `api_key` | Monnify dashboard → Settings → API Keys and Webhooks (`MK_TEST_…` or `MK_PROD_…`) |
| `secret_key` | Same page. Also the webhook key |
| `contract_code` | Needed for reserved accounts and checkout |
| `wallet_account_number` | The disbursement wallet to pay from (Monnify's "wallet account number"); a step can name another |
| `environment` | `sandbox` uses `sandbox.monnify.com`; anything else is live (`api.monnify.com`) |

**Tokens.** Monnify issues an hour-long bearer token from `POST /api/v1/auth/login` against `Basic base64(apiKey:secretKey)`. The connector caches one token per host and key pair, renews it a minute before it expires, and logs in again once if Monnify answers 401. A refused login fails the step (check the keys); a login that could not be completed is retried, since nothing was sent.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /api/v2/disbursements/wallet-balance` | read | Kobo |
| `list_banks` | `GET /api/v1/banks` | read | Also the connection test |
| `resolve_account` | `GET /api/v1/disbursements/account/validate` | read | Run before paying a new account |
| `transfer` | `POST /api/v2/disbursements/single` | idempotent write | NGN from the wallet. Usually final at once; `async` answers `PENDING` |
| `get_transfer` | `GET /api/v2/disbursements/single/summary` | read | By reference |
| `create_reserved_account` | `POST /api/v2/bank-transfer/reserved-accounts` | idempotent write | Permanent account numbers for a customer; key is `account_reference` |
| `get_reserved_account` | `GET /api/v2/bank-transfer/reserved-accounts/{ref}` | read | |
| `init_transaction` | `POST /api/v1/merchant/transactions/init-transaction` | idempotent write | Checkout URL (40 minutes); key is `payment_reference` |
| `get_transaction` | `GET /api/v2/merchant/transactions/query` | read | By `payment_reference` or Monnify's `transaction_reference` |

**Amounts.** Taskiem amounts are kobo. Monnify takes and returns naira (`20`, `"100.00"`); the connector converts exactly, and an amount it cannot read exactly is an error, never a zero. Fees round up to the kobo, settlement amounts down.

**Never twice.** The engine's key is the transfer `reference`. Monnify refuses a reference it has seen (response code `D05`, "Supplied reference already exists"); the connector then fetches the transfer by reference and reports it, so a resend after a lost response returns the original transfer. If Monnify refuses the reference but lists nothing under it, the outcome stays unknown. `D07` (the same amount to the same account within two minutes) is answered the same way: the transfer under our reference if there is one, otherwise the step fails because nothing was sent. Code `99` ("re-query to ascertain status"), 500, 502 and 504 are unknown outcomes; 503 and 429 are retried. `D01`, `D03`, `D04` and `D06` ("treat as FAILED") fail the step with Monnify's message.

A transfer Monnify reports `FAILED` or `REVERSED` fails the step with its reason. `PENDING_AUTHORIZATION` (two-factor authorisation on the Monnify account) completes the step with `requires_authorization: true`; the transfer then waits for the OTP Monnify emails, so either turn two-factor authorisation off for API disbursements or approve it before the money moves. `EXPIRED` is documented only for batches, so it parks the step for a person.

Reserved accounts and checkouts are keyed the same way: a resend first asks Monnify for the account or transaction under the key and reports it if it exists, and a refusal that names the reference does the same. A repeated checkout reports the earlier transaction without its checkout URL (the status query does not return it).

## Webhooks

Monnify posts events to the URLs set under Developer → Webhook URLs (Transaction Completion, Refund Completion, Disbursement, Settlement). Point any or all of them at the trigger `event`: `/hooks/{tenant}/connectors/moniepoint@1/event?env=prod&connection=<name>`. Deliveries are verified as Monnify documents: `monnify-signature` is the hex HMAC-SHA512 of the raw body keyed with the secret key (the connector's test checks Monnify's own worked example). Monnify sends no event id; repeats are dropped on event type plus the transaction, refund, settlement or mandate reference.

| Events | Correlation (signal steps wait for `moniepoint@1:event`) |
| --- | --- |
| `SUCCESSFUL_DISBURSEMENT`, `FAILED_DISBURSEMENT`, `REVERSED_DISBURSEMENT` | the transfer's `reference` |
| `SUCCESSFUL_TRANSACTION` on a reserved account | the account's `account_reference` |
| `SUCCESSFUL_TRANSACTION` (checkout), `REJECTED_PAYMENT` | the `payment_reference` |
| `SUCCESSFUL_REFUND`, `FAILED_REFUND` | the refund reference |
| `SETTLEMENT` | the settlement reference |
| `MANDATE_UPDATE` | the external mandate reference |

Monnify resends an unacknowledged webhook up to 10 times, five minutes apart, and recommends confirming every payment with `get_transaction` before giving value.

## To confirm with Monnify before go-live

1. The live host: `api.monnify.com` comes from the Going Live page, which could only be read as a search excerpt.
2. The error envelope: whether refusals such as `D05` arrive with HTTP 400 or 200 and `requestSuccessful: false` (the connector handles both), and what the summary endpoint returns for an unknown reference (the connector treats 404, `D02` and "not found" messages as absent).
3. Reference limits for `reference`, `accountReference` and `paymentReference` (length and characters); the connector sends 36 characters of `a-z0-9_`.
4. What a repeated `accountReference` or `paymentReference` returns; the documentation does not say.
5. Whether `D07` can be returned for a resend of the same reference before `D05`.
6. Whether `REVERSED` and `EXPIRED` can be the status of a single transfer at initiation.
7. IP whitelisting: Monnify asks for production IPs to be whitelisted before disbursements go live (`D06` otherwise), and sends webhooks from 35.242.133.146.
8. A sandbox account with a disbursement wallet, a contract code and two-factor authorisation off for API transfers.
