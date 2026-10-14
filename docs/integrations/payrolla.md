# Payrolla integration guide

**For:** Payrolla engineering  
**From:** Taskiem  
**Status:** ready to build against the iswallet sandbox

Once a payroll is approved and **locked** in Payrolla, someone at the company presses **Send to Taskiem** and Payrolla sends it to Taskiem. A Taskiem approver then releases the money, Taskiem pays every employee from the company's wallet on iswallet, and it reports each outcome back to Payrolla.

Payrolla builds three things:

1. **Send** each locked payroll to a Taskiem webhook.
2. **Receive** one report per payroll on an endpoint of yours.
3. **Receive** a reversal notice when a bank payout Taskiem reported as paid is later reversed.

## How a payroll moves

1. The payroll is approved and locked in Payrolla, and **Send to Taskiem** sends it. Taskiem answers `202 Accepted` at once.
2. Taskiem reads the company wallet's balance and asks a **payroll approver** to release the money. The approver sees the total, the headcount, how many people are paid to banks, and the balance. **This is the only approval before money moves**: iswallet has no second check. Whoever wrote the workflow cannot approve it. If nobody approves within **24 hours**, or the approver rejects it, nothing is paid.
3. Taskiem pays each employee, five at a time:
   - **with an iswallet wallet:** a wallet-to-wallet transfer, final at once, no fee.
   - **without one:** a bank payout through iswallet. Taskiem waits up to 72 hours for iswallet to confirm it. The bank fee is charged to the company wallet on top of the salary.
4. Taskiem sends Payrolla **one report** with every employee's outcome and the payroll's totals. A rejected or expired approval also gets a report.
5. Taskiem then watches each bank payout it reported as paid for a **reversal**, for 35 days from when the payroll reached it. Each reversal becomes a reversal notice to Payrolla. Nothing more is paid.

**Guarantees.**

- No employee is paid twice for the same payroll, even if you send it twice or a server crashes mid-payment. Each payment is keyed on `payroll_id` + `employee_id`.
- Nothing is paid before approval.
- One failed payment does not stop the others; it is listed in the report.
- Watching for reversals starts only after the report is sent: it never delays or changes the report, and it never pays anyone.

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
| `period` | string | Shown to the approver and in payment narrations, e.g. `September 2026` |
| `funding_wallet_id` | string | Payrolla's company wallet on iswallet |
| `total_kobo` | integer | Sum of `net_pay_kobo`, shown to the approver |
| `employees` | array | Up to 5,000 per payroll |
| `employees[].employee_id` | string | Your id; comes back in the report |
| `employees[].net_pay_kobo` | integer | **Kobo**, at least 100 (₦1). ₦300,000.00 is `30000000` |
| `employees[].wallet_id` | string | The employee's iswallet wallet, under the Payrolla iswallet client. When present, the employee is paid by wallet transfer |
| `employees[].bank_code`, `account_number`, `account_name` | strings | Required when there is no `wallet_id`. Use iswallet's bank codes, and resolve the name with iswallet's name enquiry when the employee adds the account: a payout to a valid account number belonging to someone else cannot be recovered |

Every amount is an integer in kobo, never a decimal or a string.

**Size.** The body may be **up to 4 MB for Payrolla companies** (Taskiem's default is 1 MB; it is raised for each Payrolla company's tenant). Send a payroll as one `payroll_id`, however large: do not split it into several. Each `payroll_id` needs its own approval in Taskiem and gets its own report.

**Narrations.** Each payment's narration is `Salary <period> <payroll_id> <employee_id>`, on wallet transfers and bank payouts alike, cut to iswallet's 255-character limit. Keep ids short so the employee id is never cut off.

Account numbers and names are encrypted per employee inside Taskiem and can be erased on request (NDPA).

### Responses

| Status | Meaning | What to do |
| --- | --- | --- |
| `202` | Accepted. Body: `{"run_id": "…", "duplicate": false}` | Store `run_id`; nothing else to do |
| `202` with `"duplicate": true` | Taskiem already has this `payroll_id` | Nothing; you get the original `run_id` |
| `401` | Signature missing or wrong | Check the secret and that you signed the exact bytes sent |
| `404` | Wrong URL | Check the tenant id, path and `env` |
| `413` | Body over the limit (4 MB for Payrolla companies) | Contact us; do not split the payroll |
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

Each company's Taskiem tenant holds `payrolla_api_url` and the token (see Setting up, below); the token is stored encrypted. The same URL base, token and rules serve [reversal notices](#reversal-notices).

**Idempotency.** Taskiem may send the same report more than once if a response is lost. Every retry of one report carries the **same** `Idempotency-Key`. Store reports by that key, and answer a repeat with the same `2xx` as the first time.

**Your response.** Answer `2xx` within 30 seconds once the report is stored. Taskiem retries `429`, `503`, `500`, `502`, `504`, timeouts and network errors up to 5 times over about a minute. Any other `4xx` stops the retries and leaves the payroll failed for Taskiem's operators to follow up, so use it only for a request you will never accept.

### Completed payroll

`status` is `completed` once every payment has finished, paid or failed. `results` has **one entry per employee, in payroll order**. Each entry always has `employee.employee_id` and `employee.net_pay_kobo` (the amount from your payroll), plus exactly one of these outcomes:

| Entry contains | Outcome | Counted in `totals` as |
| --- | --- | --- |
| `to_wallet` with `status: "completed"` | **Paid** to the employee's wallet. `txn_id` is iswallet's ledger reference | `paid_to_wallet` |
| `to_bank` and `settle` with `settle.event: "wallet.outflow.confirmed"` | **Paid** to the bank account. `to_bank.outflow_id` is iswallet's reference; `to_bank.fee` was charged to the company wallet | `paid_to_bank` |
| `to_bank` and `settle` with any other `settle.event` (`wallet.outflow.failed`, or rarely `wallet.outflow.reversed`) | **Failed** at the bank; iswallet returned the money to the company wallet | `failed_at_bank` |
| `wallet_failed` or `bank_failed` | **Failed**: iswallet refused the payment. `error.message` carries iswallet's code, e.g. `EXCEEDS_SINGLE_TXN_LIMIT`, `INSUFFICIENT_FUNDS`, `OUTFLOW_REJECTED` | `refused` |
| `bank_unconfirmed` | **Unknown after 72 hours**: iswallet never confirmed the bank payout. Do not pay this employee again until Taskiem's operators confirm what happened | `unconfirmed` |

**`settle` is the source of truth for a bank payout.** `to_bank` is iswallet's first answer to the payout request, so `to_bank.status` is always `"pending"`, even for a payout that was then confirmed. Read the outcome from `settle.event`, never from `to_bank.status`. `to_bank.fee` is the bank fee iswallet charged on top of the salary; a payout that failed at the bank has its fee returned with it.

`paid` and `failed` count the entries. `totals` adds up the payroll:

| Field | Meaning |
| --- | --- |
| `paid_kobo` | Total paid to employees, in kobo: the `net_pay_kobo` of every paid entry |
| `bank_fees_kobo` | Total bank fees charged to the company wallet, in kobo: the `to_bank.fee` of every confirmed bank payout. Wallet transfers are free |
| `paid_to_wallet`, `paid_to_bank`, `refused`, `failed_at_bank`, `unconfirmed` | How many entries had each outcome (table above). The first two add up to `paid`, the other three to `failed` |

The company wallet was debited `paid_kobo + bank_fees_kobo` for this payroll. New fields may be added to the report over time; ignore fields you do not know.

An example, from the integration test (one wallet payment, one bank payment, one more wallet payment, and one payment refused for exceeding a limit):

```json
{
  "failed": 1,
  "paid": 3,
  "payroll_id": "PR-2026-09",
  "results": [
    {
      "employee": {
        "employee_id": "E1",
        "net_pay_kobo": 30000000
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
        "employee_id": "E2",
        "net_pay_kobo": 35000000
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
        "employee_id": "E3",
        "net_pay_kobo": 25000000
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
        "employee_id": "E4",
        "net_pay_kobo": 45000000
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
  "status": "completed",
  "totals": {
    "bank_fees_kobo": 5350,
    "failed_at_bank": 0,
    "paid_kobo": 90000000,
    "paid_to_bank": 1,
    "paid_to_wallet": 2,
    "refused": 1,
    "unconfirmed": 0
  }
}
```

Failed payments are not retried by Taskiem. To pay those employees, send a new payroll containing only them, under a new `payroll_id`.

### Rejected payroll

If the approver rejects the payroll, or nobody approves it within 24 hours, nothing is paid and you get:

```json
{ "payroll_id": "PR-2026-09", "status": "rejected", "reason": "rejected_by_approver",
  "paid": 0, "failed": 0, "results": [],
  "totals": { "paid_kobo": 0, "bank_fees_kobo": 0, "paid_to_wallet": 0, "paid_to_bank": 0,
              "refused": 0, "failed_at_bank": 0, "unconfirmed": 0 } }
```

`reason` is `rejected_by_approver` or `approval_timed_out`.

### Timing

- A wallet-only payroll reports within minutes of approval.
- A payroll with bank payouts reports once the last payout settles, normally within minutes, at most 72 hours.
- Rarely, iswallet does not answer a payment request clearly, and Taskiem cannot tell whether that payment went through. Taskiem then holds the payroll for an operator to check iswallet's ledger, and the report waits until they have. This protects against paying anyone twice.
- Reversal notices can come at any time in the 35 days after Taskiem received the payroll, always after the report.

### Reversal notices

A bank payout iswallet confirmed can, rarely, be **reversed** days later by the receiving bank (a NIP return or a recall). The money comes back to the company wallet, and the employee who was reported as paid has not been paid. For each such reversal Taskiem calls:

```
POST {payrolla_api_url}/payrolls/{payroll_id}/reversal-notice
Authorization: Bearer <token you issue to Taskiem>
Idempotency-Key: tsk_…
Content-Type: application/json
```

The token, the `Idempotency-Key` rules and the response rules are those of the report: store the notice by its key and answer a repeat with the same `2xx`. Each notice has its own key, different from the report's. Taskiem retries `429`, `5xx`, timeouts and network errors up to 10 times over about four hours; any other `4xx` stops the retries and is shown to Taskiem's operators.

```json
{
  "payroll_id": "PR-2026-09",
  "employee_id": "E2",
  "outflow_id": "out_2",
  "amount_kobo": 35000000,
  "fee_kobo": 5350,
  "reversed_at": "2026-10-09T08:30:00Z",
  "event_id": "evt_out_2_wallet.outflow.reversed",
  "iswallet": {
    "amount": 35000000,
    "currency": "NGN",
    "fee": 5350,
    "idempotency_key": "tsk_dptbusyh5cmrw2o3x4jyp2wrcjsp3cct",
    "operation_id": "out_2",
    "outflow_id": "out_2",
    "provider_reference": "tsk_dptbusyh5cmrw2o3x4jyp2wrcjsp3cct",
    "reversed_at": "2026-10-09T08:30:00Z",
    "total_reversed": 35005350,
    "wallet_id": "w_company"
  }
}
```

| Field | Meaning |
| --- | --- |
| `payroll_id`, `employee_id` | The payroll and employee of the reversed payout, as in the report |
| `outflow_id` | iswallet's payout reference, the report's `to_bank.outflow_id` |
| `amount_kobo`, `fee_kobo` | The salary and the bank fee, in kobo, from iswallet's event (from the report when iswallet leaves them out) |
| `reversed_at` | When iswallet says the payout was reversed |
| `event_id` | iswallet's event id |
| `iswallet` | iswallet's event data as it was sent, including `total_reversed` |

**What to do.** Mark the employee unpaid for that payroll. To pay them, send a new payroll containing only them under a new `payroll_id`, after checking their bank details: a reversal often means the account is closed or wrong.

**How long Taskiem watches.** From the report until 35 days after Taskiem received the payroll: every confirmed payout is watched for at least 30 days after iswallet confirmed it. A reversal after that is not sent to Payrolla. The period can be changed for a company (1 to 60 days, counted from when the payroll reached Taskiem) with the `payrolla_reversal_watch_days` variable; tell us if you need a longer one.

A reversal that is reported before the report is sent (iswallet does not promise to deliver events in order) shows in the report itself: that entry's `settle.event` is `wallet.outflow.reversed` and it counts as failed. No notice follows for it.

## 3. Setting up

Each Payrolla customer company is its own Taskiem tenant (a sub-tenant of Payrolla's), so every value below is **per company**. Payrolla sets each company up by API ([below](#one-sub-tenant-per-company)), and each company's own people set its secrets.

**Taskiem gives Payrolla, per company:**

- the tenant id and the webhook URL for `dev` and `prod`;
- a signing secret for each environment.

**Per company, in that company's Taskiem tenant:**

- the report base URL (`payrolla_api_url`) for each environment, which Payrolla tells us;
- the bearer token Taskiem uses to call Payrolla for that company: Payrolla issues it to the company's account holder, who enters it as `payrolla_api_token` on Taskiem's **Secrets** page, so nobody else sees it;
- the company's own funding wallet (`funding_wallet_id`, sent in each payroll);
- the approvers: the company's own people who release payrolls in Taskiem (they get the `payroll_approver` role). Nobody at Payrolla or Taskiem approves a company's payroll.

**From iswallet:** Payrolla's iswallet client, with a sandbox provisioned with a funded company wallet and test employee wallets. Employees paid by wallet transfer must have wallets under the same Payrolla client, in NGN.

## One sub-tenant per company

Each Payrolla customer company gets its own space in Taskiem, a **sub-tenant**: its own people, approvers, webhook URL and signing secret, report token and payrolls, kept apart from every other company and from Payrolla's own staff. Payrolla sets one up by API when the company turns Taskiem on; nobody at Taskiem has to do anything. The calls use Payrolla's partner API key from Payrolla's servers ([partner guide](../embedding.md#14-people-in-a-sub-tenant)).

**Once, in Payrolla's own Taskiem tenant:** keep the payroll workflow there and share it: `PUT /v1/partner/workflows/{wf}/share`.

**For each company that turns Taskiem on:**

1. **Create its sub-tenant**: `POST /v1/partner/sub-tenants` with `{"name": "Company name"}`. Keep the `id`.
2. **Install the payroll workflow** into it: `POST /v1/partner/sub-tenants/{id}/workflows` with the workflow's id as `package`, `"environments": ["dev", "prod"]`, the report base URL for each environment as `variables` (`{"dev": {"payrolla_api_url": "…"}, "prod": {"payrolla_api_url": "…"}}`), and the approver role as `roles` (`[{"name": "payroll_approver", "permissions": ["approval.decide", "run.read", "workflow.read"]}]`). The answer gives the company's **webhook URL for each environment**: store them with the company and send its payrolls there. The company's funding wallet comes in each payroll's `funding_wallet_id`, so it needs no setting.
3. **Connect iswallet** for it: `POST /v1/partner/sub-tenants/{id}/connections` with `{"environment": "prod", "connector": "iswallet", "credentials": {"api_key": "…", "shared_client": "true", "wallet_ids": "<that company's wallet ids>"}}` (and `dev` with the sandbox key and wallets). The wallet list is required: see below. This needs one of Payrolla's embed apps to allow `iswallet`; a headless app is enough.
4. **Invite the account holder**: `POST /v1/partner/sub-tenants/{id}/invitations` with their email and `"roles": ["owner"]`. Taskiem emails them a link to create a passkey and join. Payrolla never sees the link or their sign-in.
5. **Invite the payroll approvers** the same way, with `"roles": ["payroll_approver"]`. The account holder can also invite them from Taskiem's Members page.

**What the company's account holder does in Taskiem**, once, after joining:

- on the Secrets page, sets `payrolla_api_token` (the bearer token Payrolla issues for that company) in each environment, and adds Payrolla's report host to the egress allow-list;
- on the workflow's Endpoints page, presses **New signing secret** for each environment and enters it in Payrolla's settings for the company. Taskiem shows it once; Payrolla's servers never receive it from Taskiem.

`GET /v1/partner/sub-tenants/{id}/installs` shows the webhook URLs again, and the install answer's `secrets` list shows which of these the company has set (never their values). A new version of the payroll workflow, installed the same way, arrives in each company as a draft: nothing changes in production until someone there publishes it.

**Payroll size.** A sub-tenant accepts webhook bodies up to 1 MiB by default, and a 5,000-employee payroll is close to that. Ask Taskiem's operators to raise Payrolla's limit, then raise each company's with `PUT /v1/partner/sub-tenants/{id}/limits` (`{"max_payload_bytes": 2097152}`).

**One Taskiem sub-tenant per company.** Payrolla's one iswallet key reaches every company's wallets, so each company's iswallet connection is provisioned with `"shared_client": "true"` and that company's own `wallet_ids`: Taskiem then refuses any payment from another company's wallet before it reaches iswallet. Payrolla's iswallet webhook subscription points at Payrolla's own tenant, and Taskiem delivers each event to the company whose connection lists its wallet. See [iswallet: one client shared by many customers](iswallet.md#one-client-shared-by-many-customers).

## 4. Testing in the sandbox

Use `env=dev`. In iswallet's sandbox, the bank code decides a payout's fate: **`000`** confirms at once and **`999`** fails at once.

| Test | Send | Expect |
| --- | --- | --- |
| Happy path | 2 employees with wallets, 1 with bank code `000`; approve in Taskiem | Report `completed`, `paid: 3`, `failed: 0`; `totals.paid_kobo` is the three salaries, `totals.bank_fees_kobo` the one payout's fee |
| Reversal | After the happy path's report, reverse the bank payout with iswallet's sandbox control (`POST /v1/sandbox/simulate/outflow` with `outcome: "reversed"`; Taskiem can do it for you) | One reversal notice for that employee; nobody is paid again |
| Bank failure | 1 employee with bank code `999` | Report entry with `settle.event: "wallet.outflow.failed"`, `failed: 1` |
| Refused payment | 1 employee whose `wallet_id` belongs to another iswallet client | `wallet_failed` entry; the others are paid |
| Duplicate | Send the same payroll twice | Second `202` has `"duplicate": true`; one report; everyone paid once |
| Rejection | Reject in Taskiem | Report `status: "rejected"` |
| Bad signature | Change one byte after signing | `401` |
| Bad payload | Omit `net_pay_kobo` | `422` with the problem listed |
| Report retry | Answer the first report with `503` | The same report arrives again with the same `Idempotency-Key` |

## Questions

Ask Taskiem engineering. When something looks wrong, quote the `run_id` from the `202`: Taskiem's operators can see every step of that payroll.
