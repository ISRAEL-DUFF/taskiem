# Mono integration

Mono is a Nigerian open-banking and payments platform. The connector is `connectors/mono` (`mono@1`), built from Mono's public documentation at docs.mono.co (API reference under docs.mono.co/api, guides under docs.mono.co/docs; read 6 October 2026). It covers Connect (account linking and account data), one-time DirectPay payments and recurring Direct Debit mandates. Mono's Lookup, Prove, Disburse and WhatsApp (Owo) products are not covered.

## Connection

| Field | |
| --- | --- |
| `secret_key` | The app's secret key (`test_sk_...` or `live_sk_...`), sent as `mono-sec-key`. Sandbox and live share `api.withmono.com`; the key decides the environment |
| `webhook_secret` | The webhook secret Mono generates when you add a webhook URL to the app. Needed only for the `event` trigger |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `initiate_account_linking` | `POST /v2/accounts/initiate` (scope `auth`) | unsafe write | Returns `mono_url` for the customer. The account id arrives as `mono.events.account_connected` |
| `initiate_reauthorisation` | `POST /v2/accounts/initiate` (scope `reauth`) | unsafe write | |
| `exchange_token` | `POST /v2/accounts/auth` | unsafe write | Widget flow: code to account id |
| `unlink_account` | `POST /v2/accounts/{id}/unlink` | unsafe write | |
| `get_account` | `GET /v2/accounts/{id}` | read | `realtime` sends `x-realtime: true`. Only the BVN's last four digits |
| `get_balance` | `GET /v2/accounts/{id}/balance` | read | Kobo |
| `get_identity` | `GET /v2/accounts/{id}/identity` | read | Full BVN, phone, date of birth, address |
| `list_transactions` | `GET /v2/accounts/{id}/transactions` | read | One page per call; `has_more` |
| `get_statement` | `GET /v2/accounts/{id}/statement` | read | 1 to 12 months; JSON, or a PDF job |
| `get_statement_pdf` | `GET /v2/accounts/{id}/statement/jobs/{job}` | read | Poll until `BUILT` |
| `request_income` | `GET /v2/accounts/{id}/income` | read | Report arrives as `mono.events.account_income` |
| `get_income_records` | `GET /v2/accounts/{id}/income-records` | read | Earlier reports, as Mono returns them |
| `request_creditworthiness` | `POST /v2/accounts/{id}/creditworthiness` | read | Report arrives as `mono.events.account_credit_worthiness` |
| `list_banks` | `GET /v3/banks/list` | read | Also the connection test |
| `initiate_payment` | `POST /v2/payments/initiate` (`onetime-debit`) | reconcilable write | Reconciled with `verify_payment` |
| `verify_payment` | `GET /v2/payments/verify/{reference}` | read | DirectPay payments and mandate debits |
| `create_customer` | `POST /v2/customers` | unsafe write | Needed before a mandate |
| `create_mandate` | `POST /v3/payments/mandates` | reconcilable write | Reconciled with `get_mandate` (by reference) |
| `initiate_mandate` | `POST /v2/payments/initiate` (`recurring-debit`) | reconcilable write | Mono-hosted authorisation link |
| `get_mandate` | `GET /v3/payments/mandates/{id or reference}` | read | |
| `pause_mandate`, `reinstate_mandate`, `cancel_mandate` | `PATCH /v3/payments/mandates/{id}/{action}` | unsafe write | "Already paused/cancelled" counts as done |
| `check_mandate_balance` | `GET /v3/payments/mandates/{id}/balance-inquiry` | read | Billed by Mono (NGN 50, or NGN 10 with `amount`) |
| `debit_mandate` | `POST /v3/payments/mandates/{id}/debit` | reconcilable write | Reconciled with `verify_payment` |
| `get_debit` | `GET /v3/payments/mandates/{id}/debits/{reference}` | read | |

Income and creditworthiness requests start a billed analysis but change nothing, so they are reads; their results come only by webhook.

**Amounts.** Mono works in kobo, as Taskiem does; amounts pass through unchanged. An input amount that is not a whole number of kobo is refused, and a provider amount that is not one is an error, never a zero, except mandate balance enquiries (Mono's example carries fractions of a kobo), which round down. Fees round up. DirectPay payments, hosted mandates and debits have Mono's NGN 200 (20000 kobo) minimum.

**Never twice.** The engine's key is the `reference` of payments, mandates and debits: 22 characters of base64url, because Mono caps payment references at 24 characters and requires at least 10. These actions are reconcilable writes: after an unknown outcome the engine looks the reference up before sending again. If Mono refuses a reference it has seen, the connector reports the payment, mandate or debit already made under it. A debit Mono refuses (insufficient funds, code 51; mandate not ready; the same-day lockout after repeated failures) fails the step with Mono's response code: no money moved. Codes 01, 09 and 99 mean the bank has not answered; the step parks for a person (or wait for the debit event). A debit that reconciles is reported in `verify_payment`'s shape; check its `status`.

**Personal data.** Inputs carrying names, emails, phone numbers, addresses, BVNs and account numbers are declared PII. Outputs (`get_identity`, account names and numbers) are not declared, because the manifest contract only declares input fields; BVNs, phone numbers, account numbers and emails in outputs are caught by detection, names and addresses are not (see below).

## Webhooks

Mono sends every event for an app to the one URL set on the app, so there is one trigger, `event`: register `/hooks/{tenant}/connectors/mono@1/event?env=prod&connection=<name>`. Deliveries are verified as Mono documents: the `mono-webhook-secret` header must equal the connection's `webhook_secret`. Repeats are dropped on `event_id`, which Mono keeps across its retries.

| Events | Correlation |
| --- | --- |
| `mono.events.account_connected`, `account_updated`, `account_unlinked`, `account_income`, `account_credit_worthiness` | the account id |
| `direct_debit.payment_successful`, `_failed`, `_cancelled`, `_abandoned` | the payment `reference` |
| `events.mandates.created`, `approved`, `rejected`, `ready`, `expired` | the mandate id |
| `events.mandate.action.pause`, `.cancel`, `.reinstate` | the mandate id |
| `events.mandates.debit.processing`, `.successful`, `.failed`, `debit_attempt.successful`, `debit.partial_debit_successful` | the debit `reference` |
| `mono.transaction.dispute_initiated`, `reversal_completed` | the transaction `reference` |

Mono retries any delivery not answered with HTTP 200 for up to 48 hours (25 attempts).

## To confirm with Mono before go-live

1. Which characters references may contain: the connector sends base64url (`A-Z a-z 0-9 - _`).
2. The exact refusal (HTTP status, message, response code) for a reused reference on `/v2/payments/initiate`, `/v3/payments/mandates` and the debit endpoint; the connector treats 409, codes 26 and 94, and messages mentioning a unique or duplicate reference as "already sent".
3. Whether `/v2/payments/verify/{reference}` finds every mandate debit by its reference (the reconcile path for `debit_mandate` relies on it), and how it reports a reference it does not know (the connector reads 404 or "not found").
4. The HTTP status of failed debits and whether a 2xx can carry `status: failed`; the connector treats the latter as an unknown outcome.
5. Whether a Connect code can be exchanged twice (`exchange_token` parks on an unknown outcome).
6. The exact "already active" wording for `reinstate_mandate`.
7. Whether income and creditworthiness webhooks carry `event_id` (the documented examples do not; the connector falls back to the event, account and timestamp).
8. Whether Mono accepts HTTP 202, which Taskiem's ingest answers with; Mono documents retrying anything but 200 (see the engine note in the delivery report).
