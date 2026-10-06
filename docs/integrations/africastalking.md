# Africa's Talking integration

The connector is `connectors/africastalking` (`africastalking@1`). It covers bulk SMS, the SMS inbox and airtime, with delivery-report, incoming-SMS and airtime-status callbacks. It was built only from Africa's Talking's public documentation, read 6 October 2026:

- developers.africastalking.com: SMS bulk and bulk (legacy), fetch messages, notifications, airtime sending, find transaction status, authentication.
- help.africastalking.com: messaging error codes, why messages fail, the airtime request flow, airtime failures, and callback security.

developers.africastalking.com shows automated readers a browser challenge, so its pages were read through search-engine extracts of them. The help centre was read directly. Everything the extracts did not show is listed under "To confirm".

## Set up

1. Sign up at account.africastalking.com and create a team and an **app** per country or use. The app's **username** is the `username` the API wants. In the sandbox the username is always `sandbox`.
2. In the app, open **Settings > API Key** and generate a key. Generate the sandbox key in the sandbox app.
3. For live SMS, register a **sender ID** or short code for each country (help centre, Dedicated Services). For airtime, ask Africa's Talking to enable it.

## Connection

| Field | |
| --- | --- |
| `username` | The app's username (`sandbox` in the sandbox) |
| `api_key` | Sent in the `apiKey` header |
| `sender_id` | Optional default sender ID or short code. Live bulk SMS requires one, from the step or here |
| `environment` | `sandbox` uses `api.sandbox.africastalking.com`; anything else is live (`api.africastalking.com`) |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `send_sms` | Live: `POST /version1/messaging/bulk` (JSON). Sandbox: `POST /version1/messaging` (form) | unsafe write | One message to many numbers in one call, with `sender_id` and `enqueue` |
| `fetch_messages` | `GET /version1/messaging?username&lastReceivedId` | read | The short-code inbox. Also the connection test |
| `send_airtime` | `POST /version1/airtime/send` (JSON) | unsafe write | `recipients: [{phone_number, currency_code, amount}]`. The amount is in hundredths (KES 100.50 is `10050`) and is sent as `"KES 100.50"`. Up to 1000 recipients |
| `get_airtime_status` | `GET /query/transaction/find` | read | Queued, Sent, Success or Failed |
| `get_balance` | `GET /version1/user` | read | The application balance, e.g. `KES 1785.5000`, plus `amount` in hundredths (rounded down) |

**SMS outcomes.** Each recipient gets a `statusCode`. `100` Processed, `101` Sent and `102` Queued count as accepted. The `4xx` codes (`401` RiskHold, `402` InvalidSenderId, `403` InvalidPhoneNumber, `404` UnsupportedNumberType, `405` InsufficientBalance, `406` UserInBlacklist, `407` CouldNotRoute, `409` DoNotDisturbRejection) are refusals. The step succeeds if any recipient was accepted, and the output lists every recipient with its `message_id` and cost. If none was accepted, the step fails with the reasons. If the refusals include `500`, `501` or `502` (Africa's Talking's or the gateway's error), the step parks for a person instead. "Sent" means handed to the telco, not delivered; delivery arrives as a delivery report.

**Never twice.** SMS has no idempotency key, so a lost response or a 5xx parks the step for a person. Airtime takes an `Idempotency-Key` header, and the connector sends the engine's key. However, Africa's Talking describes that key only in the context of its five-minute guard against repeated top-ups to the same number. After five minutes a resend would top up again, so airtime is also an unsafe write. An HTTP 429 refused the request, so nothing was sent, and it is retried even for sends. A 401 (bad key) is fatal.

## Callbacks

Set a long random `callback_token` on the connection (for example `openssl rand -hex 32`), then register each URL below, **with `&token=<callback_token>` appended**, in the dashboard (SMS > Callback URLs > Delivery Reports / Incoming Messages; Airtime > Callback URLs):

| Trigger | URL | Event | Dedup | Correlation |
| --- | --- | --- | --- | --- |
| `delivery_report` | `<public URL>/hooks/<tenant>/connectors/africastalking@1/delivery_report?env=prod` | `status`: Sent, Submitted, Buffered, Rejected, Success, Failed, AbsentSubscriber, Expired | `id:status` | `id`, the recipient's `message_id` from `send_sms` |
| `incoming_sms` | `.../africastalking@1/incoming_sms?env=prod` | `incoming_sms` | `id` | `from`, the sender's number |
| `airtime_status` | `.../africastalking@1/airtime_status?env=prod` | `status`: Success or Failed | `requestId:status` | `requestId`, the `request_id` from `send_airtime` |

Add `&connection=<name>` when the tenant has more than one Africa's Talking connection in the environment. The callbacks are form posts; Taskiem exposes their fields as `body.<field>` (`body.failureReason`, `body.networkCode`, `body.linkId`, ...).

**Callbacks are authenticated by a URL token.** Africa's Talking documents no signature on its callbacks, so each trigger uses the `query_secret` scheme: a delivery whose `token` parameter does not match the connection's `callback_token` is refused with 401, and the token is stripped before the event reaches a workflow. The token is only as secret as the URL: keep callback URLs out of logs and tickets, and rotate the token (edit the connection, then the dashboard URLs) if one leaks. Before acting on anything that moves money, still confirm it with a read: `get_airtime_status` for airtime.

## To confirm before go-live

1. **Callback authentication.** Ask Africa's Talking whether callbacks can carry a secret or come from fixed IP addresses. Taskiem would need a new verification scheme (for example a secret query parameter) to use either.
2. **JSON bulk SMS in the sandbox** was documented as "coming soon", so the sandbox uses the form endpoint. Once the JSON sandbox exists, test the live path there.
3. Whether the JSON bulk endpoint accepts premium `keyword` / `linkId`, and the most recipients per call. Neither is exposed yet.
4. **`get_balance` (`/version1/user`)**: the page describing it could not be read. Check the path and the `UserData.balance` response in the sandbox. Until then, the connection test uses `fetch_messages`.
5. `fetch_messages` response field names beyond those the incoming-SMS callback documents (`id`, `from`, `to`, `text`, `date`, `linkId`).
6. Airtime: what a repeat with the same `Idempotency-Key` returns; the exact response of `query/transaction/find` (the connector reads `status`); whether its sandbox host is `api.sandbox.africastalking.com`; and whether airtime callbacks are form or JSON (both work).
7. Voice calls are not included. The Make Call reference could not be read, Africa's Talking's help centre says the voice sandbox is not operational, and answering a call needs XML responses to Africa's Talking's callback, which Taskiem's ingest does not produce.
