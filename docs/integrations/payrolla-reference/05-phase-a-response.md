# Payrolla's answer to "What Taskiem needs from Payrolla's API" (P7)

> **Update:** Phase B is now live as well: approving, locking and paying from Taskiem, and bank account changes held for a second approver. Where this document says "Phase B", see `06-phase-b-response.md`. Bank account changes are no longer refused; they're held.

Payrolla → Taskiem · in reply to your request of 22 November 2026

Thank you for the clear request. We agree with the model: **Payrolla is the engine, Taskiem is the front.** Taskiem runs payroll in its own screens and chat, Payrolla does the work behind them, and only Payrolla pays a salary. We're building it in three phases.

**The first phase is live now:** Taskiem can manage employees and prepare payroll runs through the API. This document covers:
- where each of your nine requests stands;
- the few places we've decided differently, and why;
- exactly how to use what's live.

The full reference is the OpenAPI file at `{{PAYROLLA_API}}/connect/openapi.yaml`. It always matches what's deployed.

## Your principles

| Principle | Status |
|---|---|
| **One payer** | ✅ Agreed. Nothing in the API pays anyone. |
| **Idempotent writes** | ✅ Live. Every write takes `Idempotency-Key`. The same key and value return the first answer for 24 hours, marked `Idempotent-Replayed: true`. A different request under a used value is refused (`IDEMPOTENCY_KEY_REUSED`). Server errors (5xx) aren't kept, so you can retry them with the same value. |
| **Events for every change** | ✅ Live. A change through the API sends the same events as the same change made in Payrolla, and the reverse. |
| **Who did it** | ✅ Live. Every write takes `X-Acting-User` (your id for the person) and `X-Acting-User-Name`. Both are recorded in Payrolla's history beside the change, together with the connected app. |
| **Sensitive values sealed** | ✅ Bank account numbers and ID numbers never come back. Reads give the bank code, the bank's own name for the account, and the last four digits. |
| **A scope per group** | ✅ The new scopes are `employees:write`, `payroll:write` and `payslips:read`. Salaries and breakdowns come with `payroll:read`. |

## Your nine requests

| # | Request | Status |
|---|---|---|
| 1 | Connecting in one step (OAuth) | **Phase C.** For now the owner creates the key in Payrolla and enters it in Taskiem once. |
| 1 | Making a company from Taskiem | **Phase C** |
| 2 | Employees: create, change, terminate, link by your id | ✅ **Live**, with two limits: bank and phone changes (below) |
| 2 | Bank account checks | ✅ **Live.** Every account is checked with the bank, and the bank's name for it is returned. |
| 2 | A bank change held for approval | **Phase B.** Until then, bank accounts of existing employees can't be changed through the API. |
| 3 | Runs: create, breakdown, adjustments, exclusions, cancel | ✅ **Live** |
| 4 | Approving from Taskiem | **Phase B**, confirmed with a Payrolla code rather than a Taskiem-signed approval (below) |
| 5 | Locking, paying on the pay date, retrying failed payments | **Phase B.** Payrolla pays automatically on the pay date once a run is locked. |
| 5 | Funding account and balance | ✅ **Live** |
| 6 | Payslips as data, and an employee's payslips | ✅ **Live** |
| 7 | Employee self-service | **Phase C.** Leave isn't planned: Payrolla doesn't keep leave. |
| 8 | Settings and reports | **Phase C:** statutory settings, and the PAYE and pension schedules. Pay groups and a bank file aren't planned: payouts go through the wallet provider, not a bank upload. |
| 9 | New events | ✅ `run.adjusted` and `run.excluded` are **live**, and `employee.updated` now also reports bank and pay changes. The rest come with the features they describe (B and C). |

Phase B and C dates: `[DATE B]`, `[DATE C]`.

## Where we've decided differently, and why

**1. Approvals are confirmed with a Payrolla code, not a Taskiem-signed approval.**

Under your design, Payrolla would accept an approval signed with Taskiem's key, after a passkey check Taskiem runs. Payrolla could verify the signature, but not that a passkey was really used, or by whom. Whoever held Taskiem's key for a company could then approve that company's payroll.

So in Phase B the approval flow works like this, still entirely in Taskiem's screens:
1. Taskiem asks Payrolla to start an approval for a run.
2. Payrolla sends the approver a one-time code, on something they set up in Payrolla: their authenticator app, their verified email, or a phone their owner confirmed. The message names the run, for example "approve November payroll, ₦9.2m, 48 people".
3. The approver types the code into Taskiem, and Taskiem sends it to Payrolla with the approval.

Codes last 5 minutes, allow 5 attempts, and work once, for that one action on that one run. Locking works the same way. This also answers your security questions:
- **No signing keys:** there are no Taskiem signing keys to register or rotate.
- **No question for auditors:** the factor is Payrolla's own.
- **Replays:** not possible.

We may offer the signed-passkey route later, as something an owner opts into per company.

**2. Payrolla's own rules.** These are the rules as Payrolla has them today:
- **One approver per run.** There's no two-approver rule for payroll runs. When the company turns on separation of duties, whoever prepared a run can't approve it.
- **Runs prepared through the API** count as prepared by "Taskiem (connected app)", not by the person in Taskiem. So with separation of duties on, any of the company's approvers in Payrolla can approve them.
- **No unlocking.** Locking freezes the payslips and posts them to the books. A locked run is corrected with a correction run, in Payrolla.

**3. Not available yet**, each refused with a clear error code:
- **Changing an existing employee's bank account straight away.** A silent change of where a salary goes is the classic payroll fraud, and nothing in a run's review would show it. Changes are held for a second approver; see `06-phase-b-response.md`. Accounts given when an employee is created apply at once.
- **Changing a phone number** (`PHONE_CHANGE_NOT_AVAILABLE`). The phone is how the employee's wallet and payslips reach them.
- **Job title.** Payrolla doesn't keep one. A field a request doesn't accept is refused as `UNKNOWN_FIELD`, never silently ignored.
- **Terminating with a last day.** Terminating takes effect immediately.
- **Adjustments** are a bonus or a deduction, each with a reason. Repaying an advance is a deduction. Payrolla recovers its own salary advances and loans itself.

## Using what's live

| | |
|---|---|
| API base | `{{PAYROLLA_API}}` |
| Reference | `{{PAYROLLA_API}}/connect/openapi.yaml` |
| Auth | `Authorization: Bearer pyk_live_…`, one key per company |

**Responses.** Every response is wrapped: `{"data": …}` on success, `{"error": {"code", "message"}}` on failure. Amounts are integers in kobo.

**On every write:**

```http
Idempotency-Key: 2c1f3e7a-…          # unique per change; reuse only to retry it
X-Acting-User: tsk_user_8812         # your id for the person
X-Acting-User-Name: Ngozi Eze
```

### Employees (`employees:write`; reads `employees:read`)

**Add one**, with a bank account checked against the bank:

```http
POST /connect/employees
{ "first_name": "Zainab", "last_name": "Bello", "phone": "+2348011112201", "email": "zainab@acme.ng",
  "start_date": "2026-12-01", "department": "Operations", "external_ref": "tsk_staff_7",
  "pay":  { "gross_kobo": 45000000, "pensionable": true },
  "bank": { "bank_code": "058", "account_number": "0123456789" } }
```
```json
{ "data": { "employee": {
    "id": "e3f0…", "first_name": "Zainab", "last_name": "Bello", "status": "ACTIVE",
    "external_ref": "tsk_staff_7", "pay_method": "BANK",
    "bank": { "bank_code": "058", "account_name": "ZAINAB A. BELLO", "last4": "6789" },
    "pay":  { "gross_kobo": 45000000, "basic_kobo": 0, "pensionable": true, "effective_from": "…" } } } }
```

- **`pay`:** monthly gross, optionally split into `basic_kobo`, `housing_kobo` and `transport_kobo`. `pensionable` defaults to true.
- **Who sees pay:** `pay` comes back only to keys with `payroll:read`.
- **Contact details:** a phone or an email is required, so the employee can receive their payslip.
- **`external_ref`:** your id for the person, unique in the company (`EXTERNAL_REF_TAKEN`).
- **Finding someone:** `GET /connect/employees?external_ref=tsk_staff_7`. `external_id` works too.

**Change someone:** `PATCH /connect/employees/{id}` with only the fields that change. Unsent fields, including those set in Payrolla such as pension details, are kept.

- **Fields you can send:** `first_name`, `last_name`, `email`, `department`, `cost_center`, `external_ref` and `pay`.
- **Pay changes:** a pay change starts from `pay.effective_from`, which defaults to today.
- **Events:** `employee.updated` follows, with `changed_fields`.

**End someone's employment:** `POST /connect/employees/{id}/terminate`.

**Reads:** `GET /connect/employees` and `/connect/employees/{id}` carry the masked bank, plus pay for keys with `payroll:read`.

### Runs (`payroll:write`; reads `payroll:read`)

**Make a run** and it's calculated straight away:

```http
POST /connect/runs
{ "period": "2026-12" }
```

- **Periods:** companies paid weekly or bi-weekly send `period_start` and `period_end`.
- **Pay date:** `pay_date` defaults to the period's last day.
- **Events:** `run.computed` follows.
- **Failed calculations:** if a run can't be calculated (for example, the company's statutory settings aren't complete), it's still made, as DRAFT, and the answer carries `compute_error`. Fix the cause and call `POST /connect/runs/{id}/compute`. Retrying the same request returns the same run and never makes another.
- **One run per period:** a second regular run for the same period is refused (`DUPLICATE_RUN`).

**See every line with its breakdown:** `GET /connect/runs/{id}/lines`. Each line has:
- `gross_kobo`, `total_earnings_kobo`, `total_deductions_kobo` and `net_kobo`;
- payment status, reference and reason;
- `items`: each earning, deduction and employer contribution by name, for example `BASIC`, `PAYE` and `PENSION_EE`.

**Change a run before it's approved.** Each change recalculates at once and returns the run and its lines:

| Change | Request |
|---|---|
| Bonus or deduction | `POST /connect/runs/{id}/lines/{employeeId}/adjustments` `{ "type": "BONUS", "amount_kobo": 10000000, "reason": "Q4 target" }` |
| Remove it | `DELETE /connect/runs/{id}/adjustments/{adjustmentId}` |
| Leave someone out | `POST /connect/runs/{id}/lines/{employeeId}/exclude` `{ "reason": "unpaid leave" }` |
| Put them back | `DELETE /connect/runs/{id}/exclusions/{exclusionId}` |
| Recalculate | `POST /connect/runs/{id}/compute` |
| Cancel | `POST /connect/runs/{id}/cancel`, before approval only. An approved run is cancelled in Payrolla. |

- **Amounts:** adjustment amounts are gross, so tax and pension apply as usual.
- **Lists:** `GET /connect/runs/{id}/adjustments` and `/exclusions` list the current ones.
- **Events:** `run.adjusted` and `run.excluded` follow each change, then `run.computed`.
- **After approval:** changes are refused (`INVALID_STATE`).

**Approving, for now:** send the approver the `approval_url` from `run.computed`. They approve in Payrolla. In Phase B this becomes an action in Taskiem.

### Payslips (`payslips:read`)

- `GET /connect/payslips/{id}` returns a final payslip as data:
  - the period and pay date;
  - the employee, with the name as it was when the run was locked;
  - the employer, with name and RC number;
  - gross, each item, net, and payment status.

  Use it to show the payslip as Taskiem's own card. Before the run is locked, it returns `PAYSLIP_NOT_FINAL`; read the run's lines instead.
- `GET /connect/employees/{id}/payslips` lists an employee's final payslips, most recent first.
- The 7-day payslip link (`POST /connect/payslips/{id}/link`) stays, for sending outside Taskiem.

### Funding (`payroll:read`)

`GET /connect/funding` returns `{ "has_wallet", "available_kobo", "currency": "NGN" }`. Compare it with a run's `total_net_kobo` to warn "short by ₦120,000" before the pay date.

### New and changed events

| Event | `data` |
|---|---|
| `run.adjusted` | `run`, `change` (`added` or `removed`), `adjustment` (`id`, `employee_id`, `type`, `amount_kobo`, `reason`) |
| `run.excluded` | `run`, `change`, `exclusion_id`, `employee_id`, `reason` |
| `employee.updated` | Now also sent when a bank account or pay changes: `changed_fields` includes `bank` or `pay`, without the values. Read them through the API. |

Payloads still never carry bank numbers or pay. A `"*"` subscription receives the new events automatically.

### Errors you'll meet on writes

| Code | Meaning |
|---|---|
| `IDEMPOTENCY_KEY_REQUIRED`, `ACTING_USER_REQUIRED` | A required header is missing |
| `IDEMPOTENCY_KEY_REUSED` (422) | That value was used for a different request |
| `IDEMPOTENCY_IN_PROGRESS` (409) | The first request with that value is still running; retry shortly |
| `UNKNOWN_FIELD` (400) | A field this request doesn't accept, named in the message |
| `SCOPE_REQUIRED` (403) | The key lacks the scope; the owner issues a key with it |
| `PHONE_CHANGE_NOT_AVAILABLE` (422) | See above |
| `EXTERNAL_REF_TAKEN`, `ALREADY_EXCLUDED`, `DUPLICATE_RUN` (409) | Already exists |
| `INVALID_STATE` (409) | The run has moved on (approved, locked or cancelled) |
| `PAYSLIP_NOT_FINAL` (409) | The run isn't locked yet |

## What the owner does

In Payrolla → Settings → Connected apps, the owner connects Taskiem and ticks what it may do. The new options are:
- **Manage team:** add people, change their details and pay, end employment.
- **Prepare payroll:** runs, bonuses, deductions, exclusions.
- **Read payslips:** payslip contents.

A key can't gain scopes, so an existing connection needs a new key to use these. In Payrolla's history, every change Taskiem makes shows as "Taskiem (connected app)", with your acting user beside it.

## Questions for you

1. **Approvals:** does the Phase B flow work for your screens: start an approval, the approver enters the code from Payrolla, you submit it? Tell us now if you need anything else from it, such as how the code is sent or what the message says.
2. **Pay date:** should automatic payment on the pay date need anything from Taskiem, or is a locked run enough?
3. **OAuth (Phase C):** please send your redirect URIs, and whether you need refresh tokens or long-lived connections.
4. **`X-Acting-User`:** is your user id stable for the life of a person? We record it as given.

[YOUR NAME]
Payrolla
