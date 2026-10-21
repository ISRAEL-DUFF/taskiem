# Moniepoint POS terminals integration

Moniepoint's POS API delivers the transactions a business's terminals make and lets software ask a terminal to take a payment. The connector is `connectors/moniepointpos` (`moniepoint_pos@1`), built from Moniepoint's public POS developer documentation on TeamApt's Confluence space (teamapt.atlassian.net/wiki/spaces/EI: *Webhooks*, *Push Payment Request (API Reference)*, *API Client Credentials*, *POS as a Platform Documentation*; read 21 October 2026). The dashboard's menu names below also follow a third-party setup guide (BrandDrive, "How to connect Moniepoint POS Terminal", read the same day). docs.pos.moniepoint.com, Moniepoint's own docs host, refuses automated readers.

This is a **different product from Monnify**, Moniepoint's online payment gateway (the [`moniepoint@1` connector](moniepoint.md)). A shop with terminals has a Moniepoint Business account and connects it here; a business taking payments online through Monnify connects there. Why there are two, and what the terminals feed is for: [POS terminals](../strategy/pos-terminals.md), [decision 0040](../decisions/0040-pos-terminals.md).

## Connection

| Field | |
| --- | --- |
| `client_id` | Moniepoint Business → Settings → POS Apps Developer → API Clients. Covers every terminal assigned to the business |
| `client_secret` | Shown once when the client is created |
| `webhook_secret` | Optional; needed to receive transactions. POS Apps Developer → Webhooks → *Get API Secret*, after the subscription below is created |
| `environment` | `sandbox` for a client created in Moniepoint's Sandbox environment (one per business); anything else is live |

**What the owner does in Moniepoint Business**, as the connection screen says it:

1. Settings → POS Terminal Configuration → POS Terminal Features: turn on **ERP Integration** and apply.
2. Settings → POS Apps Developer → **API Clients**: create a client, name it, choose the environment (Sandbox to try things, Production for real terminals), and copy the id and the secret. The secret cannot be shown again; if it leaks, delete the client and create another.
3. POS Apps Developer → **Webhooks** → New Subscription: paste Taskiem's address for the connection (below), tick the transaction types, submit, then **Get API Secret** and copy it.
4. Terminals must run app version **1.11.0 or later** for webhooks (1.7.2 for payment requests).

Taskiem checks the client id and secret with `check_access` before the connection is saved.

**Tokens.** `POST /v1/auth` with `{clientId, clientSecret}` answers a bearer `accessToken` with `expiresIn` seconds (8,699 in the documentation's example). The connector caches one token per host and credential pair, renews it a minute before it expires, and signs in again once if Moniepoint answers 401. A refused sign-in fails the step (check the id and secret); a sign-in that could not be completed is retried, since nothing was sent.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `check_access` | `POST /v1/auth` | read | The connection test; answers `ok` and `expires_in` |
| `request_payment` | `POST /v1/transactions` | idempotent write | Pushes a `PURCHASE` for `amount` kobo to `terminal_serial`, offering `payment_method` `CARD_PURCHASE`, `POS_TRANSFER` or `ANY`; key is `merchant_reference` |
| `get_transaction` | `GET /v1/transactions/merchants/{merchant_reference}` | read | `processing_status` `PENDING`, `PROCESSED` or `CANCELLED`; `response_code` and `response_message` once the terminal answers |

**Amounts.** Kobo, in and out, as the webhook documents ("smallest currency units") and as the push request's integer `amount` is read (to confirm, below).

**Never twice.** The engine's key is `merchant_reference`. Moniepoint refuses a reference it has seen with 400 "Transaction exists"; the connector then fetches the request by reference and reports it, so a resend after a lost response returns the original request instead of asking the terminal again. If Moniepoint refuses the reference but has nothing under it, the outcome stays unknown. A 202 with no body is the accepted request: the connector reads the request back and reports it `PENDING` (or, when the read fails, reports what was sent as pending: the push succeeded either way). Any other 4xx fails the step (the terminal was not asked); 5xx and timeouts are unknown outcomes, so the engine resends under the same reference and lands on the duplicate path. The outcome of a request arrives as a `transaction` event whose correlation is the `merchant_reference`, or from `get_transaction`.

## Webhooks

Moniepoint posts one event per terminal transaction to the subscription's URL. Register, as the URL, the trigger's address from the workflow's Endpoints tab: `/hooks/{tenant}/connectors/moniepoint_pos@1/transaction?env=prod&connection=<name>`. One subscription covers every terminal of the business; only the ticked transaction types are sent.

| Trigger | Events | Dedup | Correlation (signal steps wait for `moniepoint_pos@1:transaction`) |
| --- | --- | --- | --- |
| `transaction` | `V1_POS_PURCHASE_TRANSACTION`, `V1_POS_TRANSFER_TRANSACTION`, `V1_POS_CARD_TRANSFER_TRANSACTION`, `V1_POS_WITHDRAWAL_TRANSACTION`, `V1_POS_BILL_PAYMENT_TRANSACTION`, `V1_POS_COLLECTION_TRANSACTION`, `V1_POS_PAY_CODE_TRANSACTION`, `V1_POS_AIRTIME_TRANSACTION`, `V1_POS_BOOM_TRANSACTION`, `V1_TRANSFER_TRANSACTION` (`body.eventType`) | `body.eventId` (retries repeat it) | `body.data.merchantReference` when the transaction answers a payment request; empty for walk-up sales |

**Deliveries are verified.** Three headers come with each: `moniepoint-webhook-id`, `moniepoint-webhook-timestamp` (epoch milliseconds) and `moniepoint-webhook-signature`, the Base64 HMAC-SHA256, keyed with the subscription's secret, of `<id>__<timestamp>__<raw body>`. The trigger declares `verify: {scheme: connector}`, so the connector's own check runs before anything is recorded (the test reproduces the documentation's worked example); a delivery with a wrong or missing signature, id or timestamp is refused with 401. No timestamp tolerance is enforced: the documentation states none, replays are refused by the dedup on `eventId`, and Moniepoint retries unacknowledged deliveries with exponential backoff, so a late retry must still be accepted ([decision 0040](../decisions/0040-pos-terminals.md)).

**What a run sees** in `trigger.body.data`: `terminalSerial`, `amount` (kobo), `transactionReference` (unique per transaction), `merchantReference` (a payment request's, else empty or null), `retrievalReferenceNumber`, `transactionTime`, `transactionType` (`POS_TRANSFER`, `AIRTIME`…), `transactionStatus` (`PENDING`, `COMPLETED`, `APPROVED` are documented), `responseCode` and `responseMessage` (the terminal's answer), `narration`, `customFields` (a map, such as an invoice id) and `metaData` (a JSON string with the customer's name, bank and masked account for transfers). `transactionTime` appears in two shapes in the documentation (`2026/08/04 09:49:25`, Lagos time, and ISO 8601 with an offset); the POS templates read both.

## Templates

[`pos-sales-record`](../templates.md) keeps every event in the `pos_sales` table; `pos-daily-summary`, `pos-sale-alert` and `pos-cashup` are the evening summary, the big-sale and declined-payment alert, and the end-of-day cash-up over that table. All four make the tables they need when they are used.

## To confirm with Moniepoint before go-live

1. **Card purchases in the webhook.** The documentation lists `V1_POS_PURCHASE_TRANSACTION`; a third-party guide shows only Withdrawal, Transfer and POS Transfers as subscription choices. A sandbox transaction, or a written answer from pos-integrations@moniepoint.com, settles whether walk-up card sales are delivered.
2. **The unit of the push request's `amount`.** The webhook documents kobo; the push example (`11000`) does not say. The connector sends kobo.
3. **The sandbox host.** `channel.moniepoint.com` is the documented base URL; the connector's `sandbox` environment uses the development host the push-payment reference links to (`moniepoint-pos-backend-service.development.moniepoint.com`), to be confirmed for clients created in the Sandbox environment.
4. **`merchantReference` limits** (length and characters): the connector sends 36 characters of `a-z0-9_`.
5. **`transactionStatus` values** beyond `PENDING`, `COMPLETED` and `APPROVED` (declined, reversed, failed), and the response codes that mean them: the templates treat anything that is neither approved nor pending as not gone through.
6. **`transactionTime`'s format and time zone**, which the documentation shows two ways.
7. **Whether one business may create several webhook subscriptions** (one per Taskiem connection), and the longest URL accepted.
8. **The subscription management API** (POS as a Platform: API keys with `webhook:write`, `POST /v1/webhook-subscriptions` on `posapi.development.moniepoint.com`): with it Taskiem could create the subscription itself (`registration: remote`) and spare the owner step 3.
9. **Moniepoint's webhook source addresses**, if the deployment should also filter by IP.
10. **A sandbox client** for the nightly checks ([needs people, P15](../needs-people.md)).
