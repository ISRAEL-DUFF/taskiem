# Remita integration

Remita is SystemSpecs' Nigerian payments platform. The connector is `connectors/remita` (`remita@1`), built from Remita's public API documentation at api.remita.net, the published "Remita APIs" Postman collection (read 6 October 2026): Authentication (new and first-generation APIs), Accept Payments > Invoice Generation (APIs, Fields and Definitions, Response Codes), and Funds Transfer > Rest Interface and Response Codes.

Two Remita platforms are involved:

- **Invoices (RRR)** are first-generation "echannel" APIs, authenticated with the merchant id and an SHA-512 `apiHash`.
- **Funds Transfer** is on the RemitaConnect gateway, authenticated with a `secretKey` header. Remita requires the calling IPs to be whitelisted in production.

## Connection

| Field | |
| --- | --- |
| `secret_key` | RemitaConnect secret key (API Credentials page). Funds Transfer |
| `merchant_id`, `api_key` | Remita merchant id and API key (API Keys and Webhooks). Invoices |
| `service_type_id` | Default service type for invoices; a step can name another |
| `source_account_number`, `source_bank_code`, `source_account_name` | Optional: sent with transfers as the account to debit (Remita's examples include them; its field table does not) |
| `environment` | `demo` uses Remita's demo hosts; anything else is live |

Hosts: Funds Transfer uses `api-gateway.remita.net` live and `api-demo.systemspecsng.com/services/connect-gateway` in demo. Invoices use `demo.remita.net` (cancellation: `remitademo.net`) in demo. **Remita's public documentation does not name the live invoice host**, so invoice actions on a live connection are refused with that explanation until it is confirmed and added.

**Signing.** Invoice calls send `Authorization: remitaConsumerKey=<merchantId>,remitaConsumerToken=<apiHash>`, the apiHash being the lowercase hex SHA-512 of: `merchantId + serviceTypeId + orderId + amount + apiKey` (generate), `rrr + apiKey + merchantId` (status by RRR, cancel), `orderId + apiKey + merchantId` (status by order). Status calls also carry the hash in the path; cancel carries it in the body. Tests pin each concatenation.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `list_banks` | `GET /api/v1/interbank/transaction/bank/list` | read | Also the connection test |
| `resolve_account` | `POST /api/v1/interbank/name/enquiry` | read | Returns the name only (Remita also returns the BVN; it is dropped) |
| `transfer` | `POST /api/v1/interbank/fund/transfer` | idempotent write | Single payment; usually pending |
| `get_transfer` | `GET /api/v1/interbank/query-transaction/{paymentIdentifier}` | read | |
| `bulk_transfer` | `POST /api/v1/interbank/bulk/fund-transfer` | idempotent write | One debit, many credits |
| `get_bulk_transfer` | `GET /api/v1/interbank/bulk/query-transaction/{batch}` and `…/bulk/transaction/detail/{batch}` | read | Batch totals and each credit's state (first page) |
| `generate_invoice` | `POST /echannelsvc/merchant/api/paymentinit` | idempotent write | Standard, split (`line_items`) and custom-field invoices |
| `get_invoice` | `GET /echannelsvc/{merchantId}/{rrr}/{hash}/status.reg` or `…/{orderId}/{hash}/orderstatus.reg` | read | `00`/`01` paid, `02`/`012`/`046`/`059` failed, else pending |
| `cancel_invoice` | `POST /echannelsvc/v2/api/deactivate.json` | unsafe write | Unpaid invoices only |

**Amounts.** Taskiem amounts are kobo; Remita takes and returns naira. Whole naira are sent without decimals (`"21000"`, as in Remita's examples), otherwise with two decimals (`"21000.50"`); the invoice hash uses exactly the text sent. Responses are converted exactly; an amount that cannot be read exactly is an error (unknown outcome), never a zero. Split-invoice line items must add up to the invoice and exactly one must bear the fee, as Remita requires; the connector checks before sending.

**Transfer outcomes.** A single payment's own code is `data.responseCode`: `00` successful, `01`/`09` pending, a code in Remita's failure list failed (the step fails with Remita's `responseMessage`; nothing moved), and any code Remita does not list pending, as Remita advises ("use the Check Status APIs"). `transaction_state`, `debit_completed` and `reversed` are reported as given. Bulk batches report Remita's batch state; a batch whose debit failed fails the step; each credit's state is mapped to successful (`SUCCESS`, `CREDIT_SUCCESS`), failed (`FAILED`, `DEBIT_FAILED`, `CREDIT_FAILED`) or pending.

**Never twice.** The engine's key is the `paymentIdentifier` (transfers), `batchPaymentIdentifier` (bulk; each item's identifier is derived from it, so a resend carries the same items) and `orderId` (invoices): 32 lowercase hex characters. Remita refuses a repeated payment (`23` "Payment Reference Exist", `26` "Duplicate record", `94` "Duplicate transaction") and a repeated order (`028`, `055`); the connector then looks the payment, batch or invoice up by that reference and reports it. When Remita refuses a reference and reports nothing under it, the step parks for a person. An existing invoice for another amount fails the step. HTTP 5xx, and invoice codes `25`, `998`, `999`, are unknown outcomes: idempotent writes resend under the same key and land on the duplicate path.

## Payment notifications

Remita posts a JSON array of payments made in the merchant's favour to the URL configured on Remita. Register the trigger URL from the workflow's Endpoints tab (`/hooks/{tenant}/connectors/remita@1/invoice_payment?env=prod&connection=<name>`).

| Trigger | Event | Dedup | Correlation (signal steps wait for `remita@1:invoice_payment`) |
| --- | --- | --- | --- |
| `invoice_payment` | `payment_notification` | `body[0].rrr` | `body[0].orderId` (the invoice's `order_id`) |

**Deliveries are not verified.** Remita documents no signature, token or secret for these notifications, so the trigger is `verify: none`: a workflow must run `get_invoice` before acting on one. Remita sends an array; the trigger reads its first payment. Remita asks receivers to answer the text `Ok`; the engine answers 202 with JSON (see below). Remita documents no notifications for Funds Transfer; poll `get_transfer`/`get_bulk_transfer`.

## To confirm with Remita before go-live

1. The live host for the invoice (echannel) APIs, including cancellation. Not in the public documentation; live invoice calls are refused until it is added.
2. That `api-gateway.remita.net` (given as the production API Gateway in the Pension section) is the production base for Funds Transfer, with the same `/api/v1/interbank/...` paths.
3. Whether `query-transaction/{paymentIdentifier}` and the bulk queries take the merchant's identifier or the one Remita returns (Remita's sample response carries a different `paymentIdentifier` from its sample request). The connector queries by the merchant's; if Remita needs its own, a lost response leaves the step parked rather than paid twice.
4. Length and character limits of `paymentIdentifier`, `batchPaymentIdentifier` and `orderId` (the connector sends 32 lowercase hex characters).
5. The meaning of `channel` (the connector sends `"1"`, as in Remita's example) and whether the `source*` fields are required.
6. Whether a non-whole naira amount is accepted in invoices and how it enters the apiHash.
7. What `reversed: true` with a pending code means, and whether `transactionState: FAILED` with an unlisted code (Remita's own query sample: code `41`) is final. The connector reports it as pending.
8. How Remita treats a notification answered with something other than `Ok` (retries, and for how long), and whether notifications can be authenticated (IP list or shared secret).
9. Pagination parameters for bulk transaction details (only the first page is read; `more_transfers` says when there is more).

## Engine change that would help

A per-trigger acknowledgement body, e.g. `ack: { status: 200, body: "Ok", content_type: text/plain }`, so the hook answers providers like Remita in the form they document instead of 202 JSON.
