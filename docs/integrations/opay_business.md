# OPay Business integration

OPay Business is OPay's offline-payments product for a business with POS terminals and wallets. The connector is `connectors/opaybusiness` (`opay_business@1`), built from OPay's public documentation at documentation.opayweb.com (*API Basics*, *Authentication*, *POS Integration (API)*, *General Payment Webhook*, *Query Balance*, *Query Order Details*, *TN Integration API*, *Digital Wallets*; read 22 October 2026) and, for the one thing the pages leave out, the webhook envelope's signature, OPay's own public Odoo integration (github.com/Opay-Digital-Services-LTD/odoo_pos_opay, `services/opay_auth.py`, read the same day).

This is a **different product from OPay Checkout** (the [`opay@1` connector](opay.md)): Checkout is online card and transfer payments with a merchant id and HMAC keys; OPay Business is the business account behind the terminals, with a client auth key and RSA keys. Why we want it and what it is for: [POS terminals](../strategy/pos-terminals.md); the design: [decision 0041](../decisions/0041-opay-business.md).

## What OPay offers, and what is not self-serve

OPay's offline APIs are enabled per business: OPay's own onboarding says a sales representative selects the endpoints, a Super Admin sets the keys and the webhook URL under Developer Tools, and OPay runs a joint test before go-live. A shop cannot paste a key and go, as with Moniepoint; the pilot needs an OPay relationship ([needs people, P16](../needs-people.md)).

## Connection

| Field | |
| --- | --- |
| `head_merchant_id` | The Business ID (`headMerchantId`) |
| `client_auth_key` | The 32-character key OPay gives each business; a request header, and the first thing checked on every notification |
| `opay_public_key` | OPay's RSA public key (PEM, or the Base64 DER the dashboard shows): verifies OPay's answers and notifications, encrypts requests |
| `merchant_private_key` | The business's RSA private key (unencrypted PEM): signs requests, decrypts answers and notifications. **Taskiem makes it** (`generate: rsa_key_pair`): the connection screen makes a 1024-bit key pair in the browser, keeps the private key as this value and shows the public key once, to paste into OPay's dashboard as the merchant public key |
| `merchant_id` | Optional: the Branch ID when OPay set the shop up under a head business; else the Business ID is used |
| `sub_scene` | Optional: the `subSceneEnum` OPay assigns when it enables payment requests to terminals; only `request_payment` needs it |

`CheckCredentials` refuses keys that cannot be read before anything is saved; `get_balance` is the connection test. Every field is Developer Tools in the OPay Business dashboard (Integration → Developer Tools), reached by a Super Admin.

## The envelope

Every request is `{paramContent, sign}` with the headers `clientAuthKey`, `version: V1.0.1`, `bodyFormat: JSON` and `timestamp` (milliseconds, valid five minutes): `paramContent` is the plaintext JSON (keys sorted, compact) encrypted with OPay's public key in PKCS #1 v1.5 blocks and Base64-encoded; `sign` is SHA256withRSA by the business's private key over `paramContent` followed by the timestamp. Every answer is `{code, message, sign, timestamp, data}`: `sign` is OPay's over the other fields sorted as `name=value` joined with `&` (nulls left out), checked with OPay's public key **before the code is believed**, even a refusal; `data` is decrypted with the business's private key. The algorithms are what the documentation's samples and OPay's own integration use (`crypto.go`).

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `POST /openApi/merchant/queryMerchantBalance` | read | The connection test; `available_balance` in kobo (OPay gives kobo here) |
| `get_transaction` | `POST /openApi/order/queryDetail` (`orderType: PAY`) | read | Any transaction by OPay's `orderNo`; the shape of a notification |
| `get_terminal` | `POST /openApi/tn/queryBySn` | read | The dedicated account (TN) bound to a terminal's serial, with status |
| `list_terminal_transactions` | `POST /openApi/tn/queryTransactionRecords` | read | A terminal's successful inbound transactions between two Nigerian days at most seven apart; pages of 100; `all` fetches up to 1,000; looks the TN up when not given |
| `request_payment` | `POST /openApi/order/checkout/createOrder` (`sceneEnum: CASH_API`) | idempotent write | Asks a terminal for `amount` kobo; key is `out_order_no`; needs `sub_scene` |
| `get_payment_request` | `POST /openApi/order/checkout/qryOrderDetail` | read | A terminal order by `out_order_no` or `order_no`, with `pay_type` and `sn` |

**Amounts.** OPay's offline APIs give naira with two decimals as text (`"300.00"`) for transactions and kobo for the balance; the connector reports kobo everywhere and refuses an amount it cannot convert exactly (an error, never a zero). `request_payment` sends naira as OPay takes it.

**Never twice.** The engine's key is `out_order_no`. OPay refuses one it has seen with code 00005; the connector then looks the order up by `outOrderNo` and reports it, so a resend after a lost response returns the original order. Refused as a duplicate yet not found: the outcome stays unknown. 10000 ("system busy"), 11004 and 11005 are unknown outcomes for the create (OPay may have made the order; the engine resends under the same key) and retries for reads; A_1001 (expired) is retried; other codes refuse the step. A made order is read back for its status; when the read fails the order is reported `INITIAL` with OPay's `orderNo`.

**Statuses** (API Basics): `SUCCESS` (money credited), `FAIL`, `CLOSE` (timed out), `CANCEL`, `PENDING` (only from a query); `INITIAL` appears on new terminal orders.

## The general payment webhook

A Super Admin adds Taskiem's address under OPay Business → Integration → Developer Tools → **POS & Others** → Add Webhook URL: `/hooks/{tenant}/connectors/opay_business@1/payment?env=prod&connection=<name>`. OPay then posts "real-time payment notifications for all types of transactions through OPay" to it (General Payment Webhook), one per transaction, with the headers `X-Opay-Tranid` (the transaction) and `merchantId`, and expects `{"code": "00000"}` within five seconds; unanswered deliveries are retried after five seconds and then at growing intervals.

| Trigger | Event | Dedup | Correlation |
| --- | --- | --- | --- |
| `payment` | `payment` (one event; the status is in the body) | the `X-Opay-Tranid` header (retries repeat it); the verified body's hash when it is missing | none: the body is encrypted when the key would be read |

**Deliveries are verified, then decrypted.** The body is OPay's envelope (`clientAuthKey`, `version`, `bodyFormat`, `timestamp`, `paramContent`, `sign`), as the POS Integration page documents for this same URL. The trigger declares `verify: {scheme: connector}` with `secret_field: opay_public_key`: the connector's verifier refuses, with 401, anything that is not a complete envelope of version V1.0.1/JSON, a timestamp more than five minutes from now, or a `sign` that is not OPay's SHA256withRSA over `paramContent` + `timestamp`. Its **enricher** then runs for each workflow the event starts: it checks the envelope's `clientAuthKey` against the connection's in constant time and decrypts `paramContent` with the business's private key, so **a run sees OPay's plain notification** as `trigger.body` (a top-level `data` wrapper is unwrapped): `status`, `orderNo`, `payNo`, `outOrderNo`, `headMerchantId`, `merchantId`, `amount`, `fee`, `feePattern`, `payMethod`, `settlementAmount`, `settlementFee`, `currency`, `sn` (the terminal), `remark`, `senderName`, `senderBank`, `senderAccount` (masked), the recipient, `transactionTime` and `completedTime` (millisecond epochs), `additionalInformation` (JSON text with `rrn`). A delivery whose client auth key or key material is another business's fails to start the run, and OPay retries.

Because the connection's public key is the trigger's "secret", a connection without OPay's public key refuses every delivery.

## Templates

[`pos-opay-sales-record`](../templates.md) keeps every notification in the `pos_sales` table (the same table as Moniepoint's, with `fee` and `settled` filled); `pos-opay-nightly-sync` and `pos-opay-backfill` use `list_terminal_transactions` to add a terminal's successful payments the notifications missed, tonight or for past days; `pos-opay-settlement-check` is the evening gross-against-net message with the balance. The evening summary, the alert and the cash-up over `pos_sales` need no change.

**Money log.** Each decrypted notification is also the normalised event `pos.sale` (reference `payNo`, else `orderNo`; `amount_kobo`, `fee_kobo` and `settled_kobo` from the naira text; `status` approved, pending, declined or reversed; `sn`, the time, how the customer paid, `senderName`), so sales reach Taskiem's money log as well as `pos_sales`. The mapping reads the plain notification, so the delivery must be decrypted before it is normalised; the envelope as received is no event.

## To confirm with OPay before go-live

1. **That the general payment webhook fires for walk-up terminal sales** (a card tapped on a terminal with no API order), not only for orders created through the API, and for transfers into a terminal's TN account. OPay's words are "all types of transactions"; a test business settles it.
2. **That the general webhook's body is the envelope** the POS page documents (the General Payment Webhook page shows the decrypted notification only). If OPay posts plain JSON, the trigger would move to a URL secret and confirmation by `get_transaction` before anything is acted on.
3. **The signature over a notification**: `paramContent` + `timestamp`, as OPay's own integration verifies it, rather than the sorted `name=value` form answers use.
4. **The key size OPay accepts** for the merchant public key: the documentation generates 1024-bit keys, which is what Taskiem makes; whether 2048 is accepted.
5. **The `subSceneEnum`** for terminal payment requests, assigned per business.
6. **The amount unit of `usableAmount`** ("in Kobo" per the Query Balance page) against the naira text everywhere else.
7. **`outOrderNo` limits** (length and characters): the connector sends 35 lowercase letters and digits.
8. **Whether `transactionType` and `payType`** are in the general webhook's notification as they are in the POS page's (the general page hides them behind "show more parameters"); the templates do not depend on them.
9. **Rate limits and the TN history's reach back in time** (the seven-day window per call is documented; how far back it goes is not).
10. **A test business** with a terminal, a client auth key, OPay's public key and the webhook pointed at staging ([needs people, P16](../needs-people.md)).
