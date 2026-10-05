# Payrolla integration guide

**For:** Payrolla engineering  
**From:** Taskiem  
**Status:** ready to build against the iswallet sandbox

When a payroll is approved in Payrolla, Payrolla sends it to Taskiem. A Taskiem approver then releases the money, Taskiem pays every employee from Payrolla's company wallet on iswallet, and it reports each outcome back to Payrolla.

Payrolla builds two things:

1. **Send** each approved payroll to a Taskiem webhook.
2. **Receive** one report per payroll on an endpoint of yours.

## How a payroll moves

1. Payrolla sends the approved payroll to Taskiem. Taskiem answers `202 Accepted` at once.
2. Taskiem reads the company wallet's balance and asks a **payroll approver** to release the money. The approver sees the total, the headcount, how many people are paid to banks, and the balance. **This is the only approval before money moves**: iswallet has no second check. Whoever wrote the workflow cannot approve it. If nobody approves within **24 hours**, or the approver rejects it, nothing is paid.
3. Taskiem pays each employee, five at a time:
   - **with an iswallet wallet:** a wallet-to-wallet transfer, final at once, no fee.
   - **without one:** a bank payout through iswallet. Taskiem waits up to 72 hours for iswallet to confirm it. The bank fee is charged to the company wallet on top of the salary.
4. Taskiem sends Payrolla **one report** with every employee's outcome. A rejected or expired approval also gets a report.

**Guarantees.**

- No employee is paid twice for the same payroll, even if you send it twice or a server crashes mid-payment. Each payment is keyed on `payroll_id` + `employee_id`.
- Nothing is paid before approval.
- One failed payment does not stop the others; it is listed in the report.

## 1. Sending a payroll

### Endpoint

```
POST https://<taskiem-host>/hooks/<tenant-id>/payrolla/payroll-approved?env=<dev|prod>
Content-Type: application/json
X-Taskiem-Signature: sha256=<hex HMAC-SHA256 of the raw body>
```

We send you the exact URL for each environment, and a **signing secret** per environment. Use `env=dev` against the iswallet sandbox and `env=prod` in production. Keep the secret out of source code and logs.

### Signature

`X-Taskiem-Signature` is `sha256=` followed by the lower-case hex HMAC-SHA256 of the **exact bytes** you send, keyed with the signing secret. Sign the bytes you send, not a re-serialised copy.

Node.js:

```js
import crypto from "node:crypto";

const body = JSON.stringify(payroll);
const signature = "sha256=" + crypto.createHmac("sha256", process.env.TASKIEM_SIGNING_SECRET).update(body).digest("hex");

const res = await fetch(TASKIEM_PAYROLL_URL, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-Taskiem-Signature": signature },
  body,
});
```

Python:

```python
import hashlib, hmac, json, os, requests

body = json.dumps(payroll, separators=(",", ":")).encode()
sig = "sha256=" + hmac.new(os.environ["TASKIEM_SIGNING_SECRET"].encode(), body, hashlib.sha256).hexdigest()
res = requests.post(TASKIEM_PAYROLL_URL, data=body,
                    headers={"Content-Type": "application/json", "X-Taskiem-Signature": sig}, timeout=10)
```

Shell, for testing:

```sh
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')"
curl -sS -X POST "$URL" -H 'Content-Type: application/json' -H "X-Taskiem-Signature: $SIG" --data-binary "$BODY"
```

### Payload

```json
{
  "payroll_id": "PR-2026-09",
  "period": "September 2026",
  "funding_wallet_id": "6f1e…",
  "total_kobo": 135000000,
  "employees": [
    { "employee_id": "E1", "net_pay_kobo": 30000000, "wallet_id": "9c2a…" },
    { "employee_id": "E2", "net_pay_kobo": 35000000,
      "bank_code": "044", "account_number": "0690000031", "account_name": "Bola Ade" }
  ]
}
```

| Field | Type | Rules |
| --- | --- | --- |
| `payroll_id` | string | **Unique per payroll, forever.** Taskiem treats a repeat as the same payroll (see below) |
| `period` | string | Shown to the approver and in transfer narrations, e.g. `September 2026` |
| `funding_wallet_id` | string | Payrolla's company wallet on iswallet |
| `total_kobo` | integer | Sum of `net_pay_kobo`, shown to the approver |
| `employees` | array | Up to 5,000 per payroll |
| `employees[].employee_id` | string | Your id; comes back in the report |
| `employees[].net_pay_kobo` | integer | **Kobo**, at least 100 (₦1). ₦300,000.00 is `30000000` |
| `employees[].wallet_id` | string | The employee's iswallet wallet, under the Payrolla iswallet client. When present, the employee is paid by wallet transfer |
| `employees[].bank_code`, `account_number`, `account_name` | strings | Required when there is no `wallet_id`. Use iswallet's bank codes, and resolve the name with iswallet's name enquiry when the employee adds the account: a payout to a valid account number belonging to someone else cannot be recovered |

Every amount is an integer in kobo, never a decimal or a string. The whole body must be under 1 MB.

Account numbers and names are encrypted per employee inside Taskiem and can be erased on request (NDPA).

### Responses

| Status | Meaning | What to do |
| --- | --- | --- |
| `202` | Accepted. Body: `{"run_id": "…", "duplicate": false}` | Store `run_id`; nothing else to do |
| `202` with `"duplicate": true` | Taskiem already has this `payroll_id` | Nothing; you get the original `run_id` |
| `401` | Signature missing or wrong | Check the secret and that you signed the exact bytes sent |
| `404` | Wrong URL | Check the tenant id, path and `env` |
| `413` | Body over 1 MB | Contact us |
| `422` | The payload breaks a rule above. Body: `{"error": "…", "problems": ["…"]}` | Fix the payload. Nothing was recorded |
| `429` | Too many requests | Retry after `Retry-After` seconds |
| `503` | Not recorded | Retry after `Retry-After` seconds |
| network error or timeout | Unknown | Retry |

**Retrying is always safe**: send the same body with the same `payroll_id` until you get a `202`. Use a 10-second timeout and back off (1 s, 2 s, 4 s … up to a minute).

**Corrections.** A `payroll_id` can be used once. To correct a payroll before it is approved in Taskiem, ask the Taskiem approver to reject it, then send the corrected payroll under a **new** `payroll_id` (for example `PR-2026-09-r2`). Resending the old id with different contents is ignored: you get back the original run.

## 2. Receiving the report

Build one endpoint on your side:

```
POST {payrolla_api_url}/payrolls/{payroll_id}/disbursement-report
Authorization: Bearer <token you issue to Taskiem>
Idempotency-Key: tsk_…
Content-Type: application/json
```

Tell us `payrolla_api_url` for each environment and issue Taskiem a token. We store both encrypted.

**Idempotency.** Taskiem may send the same report more than once if a response is lost. Every retry of one report carries the **same** `Idempotency-Key`. Store reports by that key, and answer a repeat with the same `2xx` as the first time.

**Your response.** Answer `2xx` within 30 seconds once the report is stored. Taskiem retries `429`, `503`, `500`, `502`, `504`, timeouts and network errors up to 5 times over about a minute. Any other `4xx` stops the retries and leaves the payroll failed for Taskiem's operators to follow up, so use it only for a request you will never accept.

### Completed payroll

`status` is `completed` once every payment has finished, paid or failed. `results` has **one entry per employee, in payroll order**. Each entry always has `employee.employee_id`, plus exactly one of these outcomes:

| Entry contains | Outcome |
| --- | --- |
| `to_wallet` with `status: "completed"` | **Paid** to the employee's wallet. `txn_id` is iswallet's ledger reference |
| `to_bank` and `settle` with `settle.event: "wallet.outflow.confirmed"` | **Paid** to the bank account. `to_bank.outflow_id` is iswallet's reference; `to_bank.fee` was charged to the company wallet |
| `to_bank` and `settle` with `settle.event: "wallet.outflow.failed"` | **Failed** at the bank; iswallet returned the money to the company wallet |
| `wallet_failed` or `bank_failed` | **Failed**: iswallet refused the payment. `error.message` carries iswallet's code, e.g. `EXCEEDS_SINGLE_TXN_LIMIT`, `INSUFFICIENT_FUNDS`, `OUTFLOW_REJECTED` |
| `bank_unconfirmed` | **Unknown after 72 hours**: iswallet never confirmed the bank payout. Do not pay this employee again until Taskiem's operators confirm what happened |

`paid` and `failed` count the entries. An example, from the integration test (one wallet payment, one bank payment, one more wallet payment, and one payment refused for exceeding a limit):

```json
{
  "failed": 1,
  "paid": 3,
  "payroll_id": "PR-2026-09",
  "results": [
    {
      "employee": {
        "employee_id": "E1"
      },
      "to_wallet": {
        "amount": 30000000,
        "completed_at": "2026-10-05T19:53:41Z",
        "currency": "NGN",
        "status": "completed",
        "transfer_id": "tr_1",
        "txn_id": "txn_1"
      }
    },
    {
      "employee": {
        "employee_id": "E2"
      },
      "settle": {
        "body": {
          "data": {
            "amount": 35000000,
            "currency": "NGN",
            "fee": 5350,
            "idempotency_key": "tsk_jt6o3sh2emxr52dxgn32dpwxczada3rp",
            "operation_id": "out_2",
            "outflow_id": "out_2",
            "provider_reference": "tsk_jt6o3sh2emxr52dxgn32dpwxczada3rp",
            "wallet_id": "w_company"
          },
          "event_type": "wallet.outflow.confirmed",
          "id": "evt_out_2_wallet.outflow.confirmed",
          "occurred_at": "2026-10-05T19:53:41Z",
          "schema_version": "v1",
          "wallet_id": "w_company"
        },
        "event": "wallet.outflow.confirmed"
      },
      "to_bank": {
        "amount": 35000000,
        "fee": 5350,
        "idempotency_key": "tsk_jt6o3sh2emxr52dxgn32dpwxczada3rp",
        "net_amount": 35000000,
        "outflow_id": "out_2",
        "provider_reference": "tsk_jt6o3sh2emxr52dxgn32dpwxczada3rp",
        "status": "pending"
      }
    },
    {
      "employee": {
        "employee_id": "E3"
      },
      "to_wallet": {
        "amount": 25000000,
        "completed_at": "2026-10-05T19:53:41Z",
        "currency": "NGN",
        "status": "completed",
        "transfer_id": "tr_3",
        "txn_id": "txn_3"
      }
    },
    {
      "employee": {
        "employee_id": "E4"
      },
      "wallet_failed": {
        "employee_id": "E4",
        "error": {
          "kind": "fatal",
          "message": "iswallet 422 EXCEEDS_SINGLE_TXN_LIMIT: over the per-transaction limit (request req_l): fatal"
        },
        "rail": "wallet"
      }
    }
  ],
  "status": "completed"
}
```

Failed payments are not retried by Taskiem. To pay those employees, send a new payroll containing only them, under a new `payroll_id`.

### Rejected payroll

If the approver rejects the payroll, or nobody approves it within 24 hours, nothing is paid and you get:

```json
{ "payroll_id": "PR-2026-09", "status": "rejected", "reason": "rejected_by_approver",
  "paid": 0, "failed": 0, "results": [] }
```

`reason` is `rejected_by_approver` or `approval_timed_out`.

### Timing

- A wallet-only payroll reports within minutes of approval.
- A payroll with bank payouts reports once the last payout settles, normally within minutes, at most 72 hours.
- Rarely, iswallet does not answer a payment request clearly, and Taskiem cannot tell whether that payment went through. Taskiem then holds the payroll for an operator to check iswallet's ledger, and the report waits until they have. This protects against paying anyone twice.

### After the report

A bank payout iswallet confirmed can, rarely, be **reversed** days later by the receiving bank. Taskiem alerts its operators when that happens, and they will tell you. An automatic reversal notice to Payrolla is not built yet; tell us if you need it.

## 3. Setting up

**Taskiem gives Payrolla:**

- the webhook URL for `dev` and `prod`;
- a signing secret for each.

**Payrolla gives Taskiem:**

- the report base URL (`payrolla_api_url`) for each environment;
- a bearer token for Taskiem to call it;
- the company `funding_wallet_id` in each environment;
- the names of the people who will approve payrolls in Taskiem (they get the `payroll_approver` role).

**From iswallet:** Payrolla's iswallet client, with a sandbox provisioned with a funded company wallet and test employee wallets. Employees paid by wallet transfer must have wallets under the same Payrolla client, in NGN.

## 4. Testing in the sandbox

Use `env=dev`. In iswallet's sandbox, the bank code decides a payout's fate: **`000`** confirms at once and **`999`** fails at once.

| Test | Send | Expect |
| --- | --- | --- |
| Happy path | 2 employees with wallets, 1 with bank code `000`; approve in Taskiem | Report `completed`, `paid: 3`, `failed: 0` |
| Bank failure | 1 employee with bank code `999` | Report entry with `settle.event: "wallet.outflow.failed"`, `failed: 1` |
| Refused payment | 1 employee whose `wallet_id` belongs to another iswallet client | `wallet_failed` entry; the others are paid |
| Duplicate | Send the same payroll twice | Second `202` has `"duplicate": true`; one report; everyone paid once |
| Rejection | Reject in Taskiem | Report `status: "rejected"` |
| Bad signature | Change one byte after signing | `401` |
| Bad payload | Omit `net_pay_kobo` | `422` with the problem listed |
| Report retry | Answer the first report with `503` | The same report arrives again with the same `Idempotency-Key` |

## Questions

Ask Taskiem engineering. When something looks wrong, quote the `run_id` from the `202`: Taskiem's operators can see every step of that payroll.
