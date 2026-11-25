# Payrolla × Taskiem: Phase B is live (approving, locking and paying from Taskiem)

Payrolla → Taskiem · follows our answer to your P7 request (`05-phase-a-response.md`)

Taskiem can now take a payroll all the way through in its own screens:
- prepare it (Phase A);
- have a person approve it;
- lock it, which commits the payment;
- follow it to the pay date, when Payrolla pays everyone.

It can also handle bank account changes, which Payrolla holds for a second approver. As agreed, Payrolla still does every payment, and the key alone never approves or pays: each of those steps is confirmed by the person doing it, with a code from Payrolla.

The reference is `{{PAYROLLA_API}}/connect/openapi.yaml`. Every write still takes `Idempotency-Key`, `X-Acting-User` and `X-Acting-User-Name`, as in Phase A.

## What the owner switches on

The new scope is **`payroll:approve`**, labelled "Approve and pay" in Payrolla → Settings → Connected apps. Keys can't gain scopes, so the owner issues a new key with it and revokes the old one.

Under that app, the owner also sees **People it can act as** and can remove anyone from it.

## 1. Linking a Taskiem person to their Payrolla user (once)

Taskiem acts as a Payrolla user only after that user has proved it's them.

```http
GET /connect/approvers
```

This lists who may approve or pay, with:
- `can_approve` (approve, reject and lock);
- `can_pay` (pay now and retry);
- `can_confirm` (they have a phone or authenticator for codes);
- `linked_as`, if one of your people is already linked to them.

```http
POST /connect/approvers/{user_id}/link              (X-Acting-User: tsk_user_8812)
→ 202 { "challenge": { "challenge_id": "…", "channel": "SMS", "destination_hint": "•••• 4567", "expires_at": "…" } }

POST /connect/approvers/{user_id}/link/confirm      { "challenge_id": "…", "code": "482910" }
→ 200 { "approver": { … "linked_as": "tsk_user_8812" } }
```

**How the code reaches them:**
- **Who gets it:** the code ("link your Payrolla account to Taskiem") goes only to that user: their authenticator app, their phone confirmed by their owner, or their verified email.
- **Authenticator:** with `channel: "TOTP"`, nothing is sent; they read the code from their app.
- **Changes:** `DELETE /connect/approvers/{user_id}/link` unlinks them. A Taskiem person can be linked to one Payrolla user at a time.

## 2. Approving, locking and paying

Every action that commits money takes two calls:

**1. Ask for a code** for one action on one run:

```http
POST /connect/runs/{id}/confirmations    { "action": "approve" | "lock" | "pay_now" | "retry_failed" }
→ 202 { "challenge": { … } }
```

Payrolla checks the action is possible before it sends anything: the run's state, the person's role, and separation of duties. The message says exactly what is being confirmed, for example:

> Your Payrolla code to lock November 2026 payroll and pay ₦9,200,000 to 48 people on 28 Nov 2026 is 482910. It expires in 5 minutes. Never share it.

**2. Do it with the code** the person types into Taskiem:

```http
POST /connect/runs/{id}/approve | /lock | /pay-now | /retry-failed     { "challenge_id": "…", "code": "482910" }
→ 200 { "run": { … }, "payment": { "status": "SCHEDULED", "scheduled_for": "2026-11-28", "total_kobo": …, "paid": 0, … } }
```

**Codes:**
- **Lifetime:** 5 minutes, 5 attempts, and one use.
- **Scope:** a code works only for the action and run it was asked for.
- **Volume:** a person can be sent up to 5 codes in 10 minutes, which covers linking, approving and locking in one sitting.

**Rejecting** a run needs a linked person but no code, because it moves no money: `POST /connect/runs/{id}/reject {"note": "…"}`.

### Locking commits the payment

- **Pay date:** locking schedules the payment, and **Payrolla pays everyone automatically on the pay date.** There's no separate "disburse" step to call. If the pay date has already come, it pays at once.
- **Refused locks:** a run Payrolla couldn't pay, for example someone with no bank account or wallet, is refused before it's locked (`NO_PAYOUT_DESTINATION`). It can still be fixed at that point.
- **Current accounts:** each person is paid into the account they have on the pay date. An approved bank change after locking is honoured.
- **Short wallet:** if the wallet doesn't cover the payroll on the pay date, it waits. We tell the owner and approvers, you get `run.awaiting_funds` with `short_by_kobo`, and it pays as soon as the wallet is topped up. Use `GET /connect/funding` to warn earlier.
- **`pay_now`** sends a locked run's payment early.
- **`retry_failed`** resends failed payments to each person's current account (not returned ones).
- **Status:** `GET /connect/runs/{id}/payment` shows where a payment is: `SCHEDULED`, `AWAITING_FUNDS`, `PAYING`, `COMPLETED` or `PARTIALLY_COMPLETED`, with counts.

### Payrolla's rules still apply

- **Who may do what:** only Owners and Approvers approve, reject and lock. Paying now and retrying need someone allowed to pay.
- **Separation of duties** (when the company has it on): whoever prepared a run can't approve it. That includes the Taskiem person who made it with `POST /connect/runs`, acting as their linked Payrolla user, whether in Taskiem or in Payrolla itself.
- **History:** every decision is recorded as made by the person, "in Taskiem", with your acting user id.

## 3. Bank account changes

Changing an existing employee's bank account is now possible through the API, and it's held.

```http
PATCH /connect/employees/{id}     { "bank": { "bank_code": "044", "account_number": "2222222222" } }
→ 200 { "employee": { "bank": { "last4": "6789", … }, "pending_bank_change": { "id": "…", "account_name": "ZAINAB A. BELLO", "last4": "2222", "status": "PENDING", … } } }
```

- **Until approved:** the employee keeps being paid into their current account until **an Owner or Approver other than whoever asked** approves the change.
- **"Whoever asked" includes your person:** a Taskiem person's request can't be approved by the Payrolla user they're linked to (`SAME_PERSON`).
- **One exception:** a company whose only Owner or Approver is the person who asked may approve their own request, with their code. That's recorded.
- **The employee is told twice:** by SMS and email when the change is asked for ("if you didn't ask for this, tell your employer"), and again when it takes effect.
- **The same rule applies in Payrolla's own screens.** It isn't special to Taskiem.
- **Approving:**

  ```http
  POST /connect/bank-changes/{id}/confirmations    → 202 { "challenge": … }
  POST /connect/bank-changes/{id}/approve          { "challenge_id": "…", "code": "…" }
  POST /connect/bank-changes/{id}/reject           { "note": "…" }
  ```
- **Reading:** `GET /connect/bank-changes` lists what's waiting, and employee reads carry `pending_bank_change`.
- **A tip:** include the bank account when you **create** an employee. A new employee's first account isn't held; adding one afterwards is.

## New events

| Event | When | `data` |
|---|---|---|
| `run.payment_scheduled` | A run was locked | `run`, `scheduled_for`, `total_net_kobo` |
| `run.awaiting_funds` | The pay date came and the wallet is short | `run`, `needed_kobo`, `available_kobo`, `short_by_kobo` |
| `approval.recorded` | A person approved, rejected or locked a run, or decided a bank change | `subject` (`run` or `bank_change`), `subject_id`, `action`, `by` (`user_id`, `name`), `via` (`payrolla` or `connect`), `app_name`, plus `run` or `employee_id` |
| `employee.bank_change_requested` | A new account is held for approval | `employee`, `change_id`, `requested_via` |
| `employee.bank_change_approved` / `_rejected` | It was decided | `employee`, `change_id` |

**What a lock now sends:** `run.locked`, then `approval.recorded`, then `run.payment_scheduled`. `run.disbursement_started` and the `payout.*` events follow on the pay date. A `"*"` subscription receives the new events automatically. As before, no event carries an account number or pay.

## Errors you'll meet

| Code | Meaning |
|---|---|
| `APPROVER_NOT_LINKED` (403) | The acting person isn't linked yet; link them first |
| `NOT_AN_APPROVER`, `CANNOT_PAY` (403) | Their Payrolla role doesn't allow it |
| `PAYMENT_CONFIRMATION_NOT_SET_UP` (403) | They have no factor to receive codes on; their owner adds their phone in Payrolla |
| `SOD_VIOLATION` (403) | They prepared this run, so someone else must approve it |
| `SAME_PERSON` (403) | They asked for this bank change, so someone else must approve it |
| `CODE_INVALID`, `CODE_EXPIRED`, `CODE_LOCKED` (403) | Wrong, expired or too many attempts; ask for a new code |
| `TOO_MANY_CODES` (429) | More than 5 codes in 10 minutes for this person |
| `ALREADY_LINKED` (409) | This Taskiem person is linked to someone else; unlink first |
| `AWAITING_FUNDS`, `ALREADY_PAYING`, `NOTHING_TO_RETRY` (409) | The payment can't be sent now |
| `INVALID_STATE` (409) | The run isn't at the step this action needs |
| `NO_PAYOUT_DESTINATION` (422) | On lock: someone has no account to be paid into |

## Your security questions, answered

- **Signing keys for approvals:** not needed. The confirmation is Payrolla's own code, sent to the approver's own factor.
- **Your passkey as Payrolla's second factor:** not needed for the same reason, so there's no question for auditors.
- **Replays:** codes are single-use, bound to one action on one run, and expire after 5 minutes. Requests are idempotent.
- **Who sees salaries:** the owner decides through scopes. Salaries and payslip breakdowns come only with `payroll:read`. Who sees them inside Taskiem is Taskiem's permission.

## What's next (Phase C)

One-step connect (OAuth) instead of copying the key, creating a company from Taskiem, employee self-service, and read-only settings and reports. Now live: see `07-phase-c-response.md`.

**Questions for you:**
1. **OAuth redirect URIs:** please send them, for production and for testing.
2. **Approvals screen:** does the two-step flow (ask for a code, then submit it) fit your screens and chat? For example, could the code be entered in the WhatsApp thread?
3. **Short-wallet alerts:** should we notify Taskiem in any other way, beyond the event?

[YOUR NAME]
Payrolla
