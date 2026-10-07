# M-Pesa (Safaricom Daraja) integration

M-Pesa is Safaricom's mobile money service in Kenya; Daraja is its API platform. The connector is `connectors/mpesa` (`mpesa@1`), built clean-room from Safaricom's public Daraja documentation only (read 7 October 2026), with no SDK or other client consulted:

| Page | URL |
| --- | --- |
| Authorization | https://developer.safaricom.co.ke/dashboard/apis?api=Authorization |
| M-Pesa Express (STK push) | https://developer.safaricom.co.ke/dashboard/apis?api=MpesaExpressSimulate |
| M-Pesa Express query | https://developer.safaricom.co.ke/dashboard/apis?api=MpesaExpressQuery |
| Customer to Business (register URLs, callbacks) | https://developer.safaricom.co.ke/dashboard/apis?api=CustomerToBusiness |
| Business to Customer (B2C) | https://developer.safaricom.co.ke/dashboard/apis?api=BusinessToCustomer |
| Transaction Status | https://developer.safaricom.co.ke/dashboard/apis?api=TransactionStatus |
| Account Balance | https://developer.safaricom.co.ke/dashboard/apis?api=AccountBalance |
| Reversals | https://developer.safaricom.co.ke/dashboard/apis?api=Reversal |
| API list (hosts and paths) | https://developer.safaricom.co.ke/apis |

The portal renders these pages from its documentation service (`https://developer.safaricom.co.ke/api/graphql`, queries `getApis` and `populateApi`), which is where the text was read.

Sandbox credentials are not available yet ([What needs people](../needs-people.md#phase-3), MM1), so the connector is tested against a fake Daraja written from the same pages (`connectors/mpesa/internal/fakedaraja`): tokens that expire and replace one another, the STK password check, a customer who answers later, duplicate `OriginatorConversationID`s, results posted to the callback URLs, queue timeouts and lost answers. The callback bodies in `testdata/callbacks.json` are Safaricom's documented samples.

## Connection

| Field | |
| --- | --- |
| `consumer_key`, `consumer_secret` | The Daraja app's keys (My Apps) |
| `environment` | `sandbox` uses `sandbox.safaricom.co.ke`; anything else `api.safaricom.co.ke` |
| `shortcode` | Business short code: the pay bill, or for a till the store or head office number used at go-live (`BusinessShortCode`) |
| `passkey` | Lipa na M-Pesa Online passkey (sandbox: the simulator's test data; production: emailed after go-live) |
| `b2c_shortcode` | B2C or "one account" short code (`PartyA` for B2C, balance and status); defaults to `shortcode` |
| `initiator_name` | The API operator's username on the M-Pesa organisation portal |
| `security_credential` | The operator's password **already encrypted** with Safaricom's certificate (the portal's test credentials page does this) — preferred |
| `initiator_password`, `certificate` | Instead: the plain password and Safaricom's public key certificate (PEM) for the environment; the connector encrypts per request as Daraja documents: RSA with PKCS #1 v1.5 padding (not OAEP), base64 |
| `hooks_url` | Taskiem's trigger base for this connector: `https://<taskiem>/hooks/<tenant>/connectors/mpesa@1`, with `?env=…&connection=…` when needed |
| `callback_token` | A long random value (`openssl rand -hex 32`); every callback URL carries it as `?token=` |

**Auth.** `GET /oauth/v1/generate?grant_type=client_credentials` with Basic `consumer_key:consumer_secret` returns a token valid 3,600 seconds. Daraja says each new token invalidates the previous one, so the connector caches one token per app and host, renews it a minute early, and when Daraja calls a token invalid (`404.001.03`, `400.003.01`, `401.002.01`: another process took a new one) fetches a new one once and repeats the call; Daraja refused before acting.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `check_credentials` | `GET /oauth/v1/generate` | read | Connection test: fetches a token only |
| `stk_push` | `POST /mpesa/stkpush/v1/processrequest` | unsafe write | Prompts the customer; outcome on `stk_callback` or `stk_query` |
| `stk_query` | `POST /mpesa/stkpushquery/v1/query` | read | `completed` (0), `cancelled` (1032), `failed` (other codes), `pending` ("being processed") |
| `register_c2b_urls` | `POST /mpesa/c2b/v2/registerurl` | unsafe write | Confirmation and validation URLs from `hooks_url`; "Urls are already registered" is reported as `already_registered` |
| `b2c_payment` | `POST /mpesa/b2c/v3/paymentrequest` | idempotent write | Key in `OriginatorConversationID`; outcome on `b2c_result` |
| `transaction_status` | `POST /mpesa/transactionstatus/v1/query` | read | By receipt number or `OriginatorConversationID`; answer on `status_result` |
| `account_balance` | `POST /mpesa/accountbalance/v1/query` | read | Answer on `balance_result` |
| `reversal` | `POST /mpesa/reversal/v1/request` | unsafe write | By receipt number; outcome on `reversal_result` |

**STK password.** `Password = base64(shortcode + passkey + timestamp)`, `Timestamp` `YYYYMMDDHHmmss`. The connector uses Kenyan time (EAT, UTC+3); Daraja does not say which zone, see the questions below.

**Classes, and never twice.**

- `b2c_payment` is an **idempotent write**: Daraja describes `OriginatorConversationID` as "unique … for every B2C request to avoid double disbursement" and refuses a repeat (`500.002.1001 Duplicate OriginatorConversationID`; its error table also lists result code `15` "Duplicate Detected"). The engine's key is sent there (32 lowercase hex characters). A resend after a lost answer gets the duplicate refusal, and the step reports `status: accepted, duplicate: true`: the first request's result is already on its way to `b2c_result`. Nothing is paid twice. A `b2c_result` with code `15` is the repeat being refused, not the payment's outcome; it is its own event (`b2c.duplicate`).
- `stk_push` and `reversal` are **unsafe writes**: Daraja takes no reference it deduplicates by (`AccountReference` is shown to the customer, not checked), and a lost answer leaves no id to look the request up by. An unknown outcome parks the step for a person instead of prompting or reversing twice. Refusals Daraja documents as "nothing happened, try again" are reported as *not sent*, so the engine retries: "Unable to lock subscriber" (a prompt already open on the phone), "System is busy", spike arrest and quota violations.
- Daraja's money results are all asynchronous, and its status API is asynchronous too, so no synchronous read can settle an unknown outcome; that is why B2C relies on Daraja's duplicate refusal rather than a reconcile action.

**Amounts.** Taskiem amounts are KES cents. Daraja takes whole shillings ("only whole numbers are supported"; minimum KES 1), sent as strings (`"Amount": "1500"`); an amount that is not whole shillings is refused before sending. Results report Daraja's amounts as given (shillings, sometimes with decimals: `"TransAmount": "5.00"`, `"Value": 1.0`).

**Phone numbers** are sent as `2547XXXXXXXX` (or `2541…`); `+` and spaces are removed; anything else is refused.

**Limits Daraja documents for STK push:** up to KES 250,000 per transaction, KES 500,000 a day per customer. `AccountReference` 12 characters, `TransactionDesc` 13, B2C `Remarks` 2–100.

## Callbacks and verification

Daraja sends every result to URLs given in the request (STK `CallBackURL`, `ResultURL`, `QueueTimeOutURL`) or registered once (C2B). The connector builds them from `hooks_url`: `<hooks_url>/<trigger>?token=<callback_token>`, plus `kind` and `ref` for asynchronous results.

| Trigger | Events | Dedup | Correlation (signal steps wait for `mpesa@1:<trigger>`) |
| --- | --- | --- | --- |
| `stk_callback` | `stk.completed`, `stk.failed` | `CheckoutRequestID` | `checkout_request_id` |
| `b2c_result` | `b2c.completed`, `b2c.failed`, `b2c.duplicate` | `ConversationID:ResultCode` | `originator_conversation_id` (the `ref` the connector put in the URL) |
| `status_result` | `status.completed`, `status.failed` | same | the `transaction_id` or `original_conversation_id` asked about |
| `balance_result` | `balance.completed`, `balance.failed` | same | the action's `correlation` output (Daraja's `OriginatorConversationID`) |
| `reversal_result` | `reversal.completed`, `reversal.failed` | same | the reversed `transaction_id` |
| `queue_timeout` | `timeout.<kind>` (`b2c`, `status`, `balance`, `reversal`) | the body | the request's `ref` |
| `c2b_confirmation` | `c2b.confirmation` | `TransID` | `BillRefNumber` (the account number), or `TransID` for till payments |
| `c2b_validation` | `c2b.validation` | `validation:TransID` | same |

**Daraja signs nothing**, and Safaricom publishes no source IP list on the pages read. Deliveries are therefore authenticated by the URL token (`query_secret`): a callback whose `token` does not match the connection's `callback_token` is refused with 401 before anything is recorded, and the token never reaches a run. Treat every callback as a report, not proof: before releasing value, confirm with `stk_query` (STK) or `transaction_status` (B2C, C2B, reversals), whose answers come back on `status_result`. The C2B triggers answer `{"ResultCode":"0","ResultDesc":"Accepted"}`, as Daraja documents.

**C2B validation.** Taskiem cannot answer a validation request with a rejection: trigger answers are fixed in the manifest and workflows run after the delivery is accepted, while M-Pesa waits about 8 seconds. Every validation request is accepted and delivered as an event; keep `ResponseType` in mind (what M-Pesa does when the URL is unreachable) and do not enable external validation for a short code that needs rejections (decision E5).

**URL rules** from the C2B page: production URLs must be HTTPS and publicly reachable; avoid the words M-PESA, Safaricom, exe, exec, cmd, SQL and query in them. Taskiem's trigger URLs contain `mpesa@1` (see question 1).

## Setting up

1. On the Daraja portal, create an app with the M-Pesa Express, C2B, B2C, Transaction Status, Account Balance and Reversal products as needed, and note its keys.
2. For B2C, balance, status and reversals: create an API operator on the M-Pesa organisation portal with the roles Daraja lists (ORG B2C API initiator, Balance Query ORG API, Transaction Status query ORG API, Org Reversals Initiator) and set its password; produce the security credential with the portal's test credentials page, or give the password and certificate.
3. Create the Taskiem connection with the fields above, then run `register_c2b_urls` once if you take pay bill or till notifications.
4. Go live through the portal's GO LIVE tab (short code, organisation name, an M-Pesa business administrator or manager username); Safaricom emails the production passkey and moves the app to production keys.

## To confirm before go-live

1. Whether Safaricom's URL keyword filter ("M-PESA") refuses `mpesa` in Taskiem's trigger paths (`/connectors/mpesa@1/...`). If it does, callbacks need a path without it (an alias route or an edge rewrite).
2. The time zone of `Timestamp` (the connector uses EAT, UTC+3).
3. The length limit of `OriginatorConversationID` (the response field is described as "fewer than 20 characters", yet Safaricom's own samples are 25–34; the connector sends 32 hex characters).
4. Whether a duplicate `OriginatorConversationID` is always refused synchronously (`500.002.1001`) or sometimes only in the result (`15`), and for how long M-Pesa remembers ids.
5. Whether the result callback's `OriginatorConversationID` always echoes the request's (B2C samples show a different value; the connector correlates on the `ref` it adds to the URL, which does not depend on it).
6. The body of `QueueTimeOutURL` notifications (undocumented).
7. The STK query's answer while the customer has not responded (the connector treats "being processed" as pending).
8. Whether `AccountBalance` and `TransactionStatus` accept `ref`/`kind` query parameters on result URLs in production (they do in the documentation's terms; untested without a sandbox).
9. Whether a published IP range for Daraja callbacks exists, to add an allow-list in front of the token.
10. "Security credentials are generated by encrypting the base64 encoded initiator password" (FAQ) versus "write the unencrypted password into a byte array" (algorithm steps): the connector follows the steps; confirm on the sandbox, or use `security_credential` from the portal.
