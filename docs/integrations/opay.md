# OPay integration

OPay is a Nigerian payments company. The connector is `connectors/opay` (`opay@1`), built on OPay's merchant collection APIs from its public documentation at documentation.opaycheckout.com (read 6 October 2026): getting-started, payment-authentication, api-signature, cashier-create, bank-transfer, query-payment-status, cashier-close, payment-refund, payment-refund-status, payment-notifications-callbacks, callback-signature, error-codes and end-to-end-testing.

OPay's transfer (payout) APIs (to OPay wallets and to bank accounts), balance, bank list and account-name enquiry are **not** in OPay's public documentation: documentation.opayweb.com now covers only POS and digital-wallet products, and the older host that held the transfer API (doc.opayweb.com) no longer resolves. The connector therefore has no payout actions; see "To confirm with OPay" below.

## Connection

| Field | |
| --- | --- |
| `merchant_id` | Sent as the `MerchantId` header on every call |
| `public_key` | `OPAYPUB…`; authorises Cashier create (`Authorization: Bearer <public key>`) |
| `secret_key` | `OPAYPRV…`; signs every other call and verifies callbacks |
| `environment` | `sandbox` uses `testapi.opaycheckout.com`; anything else is live (`liveapi.opaycheckout.com`) |
| `callback_url` | Optional default for refunds (OPay requires a callback URL on every refund) |

Both keys are on the OPay merchant dashboard under API Keys & Webhooks.

**Signing.** Except for Cashier create, OPay authenticates a request with `Authorization: Bearer <signature>`, the lowercase hex HMAC-SHA512 of the JSON body with its keys in alphabetical order, keyed with the secret key. The connector serialises the body once (keys sorted), signs those bytes and sends the same bytes.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `create_cashier_payment` | `POST /api/v1/international/cashier/create` | idempotent write | Returns `cashier_url` for the payer. Public-key auth |
| `create_bank_transfer_payment` | `POST /api/v1/international/payment/create` (`payMethod: BankTransfer`) | idempotent write | Returns the `account_number`/`bank_name` to pay into and `expires_at` |
| `get_payment` | `POST /api/v1/international/cashier/status` | read | By `reference` or `order_no` |
| `close_payment` | `POST /api/v1/international/payment/close` | unsafe write | Cancels an unpaid payment; an unknown outcome parks |
| `refund_payment` | `POST /api/v1/international/payment/refund/create` | idempotent write | `refund_way` Original (default) or BankAccount |
| `get_refund` | `POST /api/v1/international/payment/refund/query` | read | |
| `verify_callback` | none (computed locally) | read | Checks a callback's `sha512`; a mismatch fails the step |

**Amounts.** OPay takes and returns integer kobo ("cent unit"), as Taskiem does. A response amount that is not a whole number is an error (unknown outcome), never a zero.

**Never twice.** The engine's key is the `reference` (`tsk` + 32 base32 characters, letters and digits only). OPay refuses a reference it has seen (code 02004); the connector then queries the payment (or refund) by reference and reports it, so a resend after a lost response returns the original. If the original belongs to a different amount (or, for refunds, a different original payment), the step fails rather than report someone else's payment. If OPay refuses the reference but its status API has nothing under it, the step parks for a person. A payment OPay reports FAIL or CLOSE, and a refund it reports FAIL, fails the step with OPay's `failureReason`/`errorMsg`.

**Errors.** 02000 (authentication), 02001 (bad parameters), 02002, 02003 and 02007 are refusals: the step fails. 50003 ("service not available, please try again"), codes OPay does not list, and HTTP 5xx are unknown outcomes: reads retry, idempotent writes resend under the same reference (and land on the duplicate path), `close_payment` parks. 02006/00012/02812 are "not found".

## Callbacks

OPay posts status changes to the payment's `callbackUrl`, or to the webhook URL set on the dashboard. Register the trigger's URL from the workflow's Endpoints tab (`/hooks/{tenant}/connectors/opay@1/payment?env=prod&connection=<name>`), either on the dashboard or as `callback_url` on each create step.

| Trigger | Event | Dedup | Correlation (signal steps wait for `opay@1:payment`) |
| --- | --- | --- | --- |
| `payment` | `transaction-status` (`body.type`) | `body.sha512` (changes with status and refund flag) | `body.payload.reference` |

**Deliveries are not verified by the engine.** OPay signs a callback with `sha512` *inside the body*: the hex HMAC-SHA3-512, keyed with the secret key, of `{Amount:"<amount>",Currency:"<currency>",Reference:"<reference>",Refunded:<t|f>,Status:"<status>",Timestamp:"<timestamp>",Token:"<token>",TransactionID:"<transactionId>"}` built from `payload`. None of the engine's webhook schemes can check that, so the trigger is declared `verify: none`. A workflow must not act on a callback alone: run `get_payment` (OPay's own advice) or `verify_callback` with `trigger.body.payload` and `trigger.body.sha512` first. The connector's implementation reproduces the signature in OPay's documented example exactly. OPay retries unacknowledged callbacks for 72 hours; the engine answers 2xx.

## To confirm with OPay before go-live

1. The transfer (payout) API: wallet and bank transfers, their status, balance, bank list and account-name enquiry, with signing and duplicate-reference behaviour. Not publicly documented; needed before payouts can be added.
2. Reference limits: maximum length and allowed characters (the connector sends 35 lowercase letters and digits).
3. Whether the duplicate-reference refusal (02004) also applies to refund references, and whether the status API finds payments created with `/payment/create` as well as Cashier orders.
4. Whether OPay verifies a request signature over the raw body bytes or over its own re-serialisation (the PHP sample signs a body with unescaped slashes and sends one with escaped slashes). The connector signs exactly what it sends; text containing `<`, `>` or `&` is sent JSON-escaped (`<`), which would fail authentication (harmlessly) if OPay re-serialises.
5. The unit of the callback's `amount` and `fee` ("in NGN" in the field table; the API uses kobo everywhere else). The connector does not read callback amounts; `get_payment` reports kobo.
6. Whether `refunded` in callbacks is a boolean (as in the example) or a string (as in the table). `verify_callback` accepts both.
7. OPay's callback source IPs, if the deployment should also filter by IP.

## Engine change that would help

A webhook verify scheme for a signature carried in the body over a templated string, e.g. `scheme: body_template_hmac`, `algorithm: sha3_512`, `signature: =body.sha512`, `template: '{Amount:"%s",...}'` with CEL expressions for each field and a boolean-to-`t`/`f` rule. With it the `payment` trigger could reject forged callbacks before they reach a workflow.
