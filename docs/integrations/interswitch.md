# Interswitch integration

The connector is `connectors/interswitch` (`interswitch@1`), built from Interswitch's public documentation at docs.interswitchgroup.com (read 6 October 2026): Authentication, Payouts (with Receiving Institutions, Payout Channels and Error Codes), Web Checkout, Payment Response Codes and Webhooks. It uses the Payouts service rather than the older Quickteller Service v5 transfer API, which needs a terminal id and a MAC and documents no live host.

## Connection

| Field | |
| --- | --- |
| `client_id`, `client_secret` | Quickteller Business → Developer tools |
| `wallet_id`, `wallet_pin` | The prefunded payout wallet (Quickteller Business → Wallets) and its PIN; needed for `transfer` |
| `merchant_code` | The `MX…` code, for `get_payment` |
| `webhook_secret` | The secret key shown when configuring webhooks |
| `environment` | `sandbox` uses `passport-sandbox`, `payouts-sandbox` and `sandbox.interswitchng.com`; anything else is live (`passport`, `payouts`, `webpay.interswitchng.com`) |

**Tokens.** Payout calls carry a bearer token from Interswitch Passport's client-credentials grant (`POST /passport/oauth/token`, `Basic base64(clientId:secret)`). The connector caches one token per Passport host and credential pair until a minute before `expires_in`, and asks again once if Interswitch answers 401. Refused credentials fail the step; a Passport outage is retried, since no payment was attempted.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `list_banks` | `GET /api/v1/payouts/receiving-institutions` | read | Interswitch, CBN and NIP codes; any of them is accepted as `bank_code`. Also the connection test |
| `resolve_account` | `POST /api/v1/payouts/customer-lookup` | read | Run before paying a new account |
| `transfer` | `POST /api/v1/payouts` | idempotent write | NGN bank transfer from the wallet, lookup and payout in one call (`singleCall`). Usually `PROCESSING` |
| `get_transfer` | `GET /api/v1/payouts/{reference}` | read | |
| `get_payment` | `GET /collections/api/v1/gettransaction.json` (webpay host) | read | Web Checkout requery by `txn_ref` and expected amount |

**Amounts.** Taskiem amounts are kobo. Payouts take and return naira decimals; Web Checkout reports kobo. The connector converts exactly, and an amount it cannot read exactly is an error, never a zero. Fees round up to the kobo.

**Never twice.** The engine's key is the payout's `transactionReference`. Interswitch does not document what a repeated reference does, so before any resend the connector asks for the payout by reference and reports it if it exists; it sends again only when Interswitch says there is none (404), and stops if it cannot tell. A refusal mentioning a duplicate (or a 409) is answered the same way, and if nothing is listed under the reference the outcome stays unknown. 500 and 504 are unknown outcomes; 503 and 429 are retried.

Payout statuses are `PROCESSING`, `SUCCESSFUL` and `FAILED`. `FAILED` fails the step with Interswitch's description (Interswitch reverses any debit), as do refusals with the documented failure codes 13, 20, 51, 52, 53, 59 and 61. Code `09` ("not yet final, call status endpoint") is `PROCESSING`. Any other status is treated as an unknown outcome.

**Confirming a checkout payment.** `get_payment` returns `status` (`successful` for 00 and 11, `partial` for 10, `pending` for 09, S0 and Z0, `failed` otherwise) and `amount_matches`. Give value only when both say so; Interswitch requires the amount check. Z25 ("Transaction not Found") is not found. Interswitch keeps three months of transactions on this endpoint.

## Webhooks

Configure one URL under Quickteller Business → Developer tools → Webhooks and choose the event types: `/hooks/{tenant}/connectors/interswitch@1/event?env=prod&connection=<name>`. Deliveries are verified as Interswitch documents: `X-Interswitch-Signature` is the hex HMAC-SHA512 of the raw body keyed with the webhook secret. One transaction is reported several times under the same `uuid` (created, updated, completed), so repeats are dropped on event, `uuid` and `timestamp`.

| Events | Correlation (signal steps wait for `interswitch@1:event`) |
| --- | --- |
| `TRANSACTION.CREATED`, `.UPDATED`, `.COMPLETED` | the checkout's `txn_ref` (`data.merchantReference`), else the `uuid` |
| `SUBSCRIPTION.*`, `LINK.TRANSACTION_*`, `INVOICE.TRANSACTION_*` | the `uuid` (the invoice, link or subscription reference) |

Interswitch retries an unacknowledged webhook up to five times. A `TRANSACTION.COMPLETED` may arrive without the earlier events and may be a failure; confirm with `get_payment`.

## To confirm with Interswitch before go-live

1. What a repeated payout `transactionReference` returns, and what `GET /api/v1/payouts/{reference}` returns for an unknown reference (the connector treats only 404 as absent).
2. The customer-lookup response: it is not documented. The connector reads `recipientName` (the name payout records use), then `recipient.recipientName`, then `accountName`, and fails the step if none is present.
3. Payout webhooks: the dashboard offers a Payout event type, but no payout event names or payloads are documented, so the trigger lists none and payouts are confirmed with `get_transfer`.
4. Whether `sourceAccountName` and `sourceAccountNumber` are required for payouts (the sample sends them; the connector sends them when the step gives them).
5. Reference limits (length and characters); the connector sends 36 characters of `a-z0-9_`.
6. The unit of the `amount` query parameter of `gettransaction.json` (the connector sends kobo, as checkout amounts are in minor units), and whether that endpoint now requires a bearer token (the v1 sample sends none; v2 does).
7. The batch payout sandbox host: the batch page names `isw-payout-service.k8.isw.la`, the single-payout page `payouts-sandbox.interswitchng.com` (used here).
8. A sandbox client with a funded payout wallet, its PIN, and a merchant code with Web Checkout enabled.
