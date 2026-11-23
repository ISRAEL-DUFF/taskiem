# What Taskiem needs from Payrolla's API

Status: request to the Payrolla team, 22 November 2026. Tracked as partner item P7 ([partner tracker](../partner-tracker.md)). Plan: [build plan](../spec/build-plan-saved-tools-payments.md) P4.

**Answered, 23 November 2026.** Payrolla shipped all of it in three phases ([A](payrolla-reference/05-phase-a-response.md), [B](payrolla-reference/06-phase-b-response.md), [C](payrolla-reference/07-phase-c-response.md)), deciding one thing differently: approving and paying are confirmed with Payrolla's own one-time code, not Taskiem's signed passkey approval (section 4 below). What Taskiem builds on it: [decision 0074](../decisions/0074-payroll-run-from-taskiem.md) and P4b. Our answers to Payrolla's questions: [08-taskiem-answers](payrolla-reference/08-taskiem-answers.md). This request is kept as it was sent.

## Why

Payroll in Taskiem is Payrolla. Taskiem does not calculate, approve or pay a payroll itself (Run payroll is deleted; [decision 0044](../decisions/0044-rails-and-products.md) and its note of 22 November 2026). But a business should not feel it is using two products: it should run its payroll in Taskiem, in Taskiem's own screens and chat, while Payrolla does the work behind them.

So Payrolla is the **engine** and Taskiem the **front**:

- **Payrolla** keeps employees, their pay and bank details, calculates each run, keeps the approvals, pays every employee once, and issues payslips. It is the one system that can pay a salary.
- **Taskiem** shows all of it in its own look (the app, the web, and the chat on WhatsApp, Telegram and Slack), and sends what people do back to Payrolla through its API.
- **Not an embed.** Taskiem does not show Payrolla's screens in a frame (R6's embed stays unbuilt).

Today's Connect API ([reference](payrolla-reference/02-taskiem-integration-guide.md)) is almost all reads: Taskiem can show payroll but not run it. These are the additions that would let it.

## Principles for every request

- **One payer.** Only Payrolla moves salary money. Nothing here asks Payrolla to let Taskiem pay.
- **Idempotent writes.** Every write takes an `Idempotency-Key` header and gives the same answer to the same key for at least 24 hours, so a retry from Taskiem never makes two of anything.
- **Events for every change.** Each write that changes something sends the event it would send had the change been made in Payrolla, so Taskiem's view and Payrolla's agree whichever side acted.
- **Who did it.** Every write carries the Taskiem person acting (`X-Acting-User`: an opaque id Taskiem gives, with their name for Payrolla's audit), recorded in Payrolla's own history beside the change.
- **Sensitive values sealed.** Bank account numbers and ID numbers go to Payrolla and are never read back in full: reads give the bank and the last four digits.
- **Scopes.** Each group below is its own scope, so a company owner chooses what Taskiem may do.

## The requests

In the order Taskiem would build on them. **Must** blocks running payroll from Taskiem; **should** makes it feel whole; **could** can wait.

### 1. Connecting a company in one step (must)

Today an owner makes a key in Payrolla and pastes it into Taskiem.

- **A connect flow:** from Taskiem, "Connect Payrolla" sends the owner to Payrolla to sign in and choose the scopes, and returns to Taskiem with a code Taskiem exchanges for the company's credentials (OAuth 2.0 authorization code with PKCE, or Payrolla's own equivalent). Refresh, revoke, and a `company.disconnected` event.
- **Making a company from Taskiem** (should): a business with no Payrolla company yet gets one made from Taskiem with its name, address, RC number and the owner as its first admin, so "Set up payroll" never leaves Taskiem.

### 2. Employees (must; scope `employees:write`)

- `POST /connect/employees`: create, with name, phone, email, job title, start date, pay (amount, per month or other periods Payrolla supports), and bank (bank code, account number; sealed). Answers the employee with the bank's name for the account once Payrolla has checked it.
- `PATCH /connect/employees/{id}`: change any of those; a bank change answers whether Payrolla holds it for approval (see 4) and sends `employee.updated` with `changed_fields`.
- `POST /connect/employees/{id}/terminate`: leaving, with the last day.
- **Bank account checks:** an endpoint, or a field on the answer, saying the bank's name for the account (Payrolla already checks names before paying).
- **Linking:** `external_id` on an employee (Taskiem's staff member id), settable on create and update, and found by (`GET /connect/employees?external_id=`), so Taskiem's staff and Payrolla's employees stay one list.

### 3. Runs (must; scope `payroll:write`)

- `POST /connect/runs`: a run for a period (`2026-11`), optionally a pay date; Payrolla calculates it as it does now and sends `run.computed`.
- `GET /connect/runs/{id}/lines` with each line's breakdown (gross, each deduction and addition by name, net), not only net and status.
- `POST /connect/runs/{id}/lines/{line}/adjustments`: a bonus, a deduction or an advance repaid, with an amount and a reason; the run recalculates and sends `run.computed` again.
- `DELETE` of an adjustment; leaving someone out of this run (`POST …/lines/{line}/exclude`, with a reason).
- `POST /connect/runs/{id}/cancel` before it is approved.

### 4. Approving from Taskiem (must; scope `payroll:approve`)

Today there is no approve API by design: the approver confirms in Payrolla with their own factor. For payroll to be run from Taskiem, Payrolla would accept **Taskiem's confirmation** as the approval:

- Taskiem asks the approver for a **passkey** (Taskiem's step-up, the same as for its own payments), bound to this run, its total and its line count.
- Taskiem sends `POST /connect/runs/{id}/approve` (or `/reject` with a reason) carrying a **signed approval**: the run id, its total and line count as the approver saw them, the approver (`X-Acting-User`, and the Payrolla user they are linked to), the time, and that a passkey was used; signed with a key Taskiem holds for the company's connection (Payrolla keeps Taskiem's public key, rotatable).
- Payrolla checks the signature, that the totals still match the run (refused if it was recalculated since), and that the person is one of its approvers for the company, then records the approval as its own, with "approved in Taskiem with a passkey" in its history.
- **Approvers are linked** to Payrolla users: `GET /connect/approvers` (who may approve, with their email) and a link between a Payrolla user and Taskiem's person (by verified email, confirmed once by the person).
- Payrolla's own rules hold: two approvers where it asks for two, no one approving a run they prepared.
- The same for **a bank account change held for approval** (2): `POST /connect/employees/{id}/bank-change/approve` with a signed approval.

### 5. Locking and paying (must; scope `payroll:approve`)

- `POST /connect/runs/{id}/lock` (and the reverse while allowed); Payrolla pays on its pay date as now.
- `POST /connect/runs/{id}/pay-now` where Payrolla allows paying before the date.
- **The funding account:** `GET /connect/funding` (the wallet or account the company pays from and its balance), so Taskiem can say "short by ₦120,000" before the pay date (today's payday check reads another connection's balance).
- **Paying the failed ones again:** `POST /connect/runs/{id}/retry-failed` for lines whose payout failed (not reversed), once their bank details are fixed.

### 6. Payslips (should; scope `payslips:read`)

- `GET /connect/payslips/{id}` with its contents (period, gross, each line, net, employer's details), so Taskiem shows a payslip in its own screens and sends it to the employee in the chat as Taskiem's own card, not only as a 7-day link.
- `GET /connect/employees/{id}/payslips`: an employee's past payslips, for "my payslips" on the staff member's side of Taskiem.
- The existing link stays for sending outside Taskiem.

### 7. What an employee does themselves (could; scope `self_service`)

- The employee's own view through Taskiem: their payslips, their pay and their bank account (masked), and a request to change the bank account that Payrolla holds for the company's approval (4).
- Leave and its balance, if Payrolla keeps it.

### 8. Settings and reports (could)

- Pay groups, pay dates, statutory deductions turned on (PAYE, pension, NHF): read, so Taskiem can show them; change later.
- Reports Payrolla makes (pension schedule, PAYE schedule, the bank file): a list and a download link.

### 9. Events to add

- `run.adjusted` (a line's adjustment added or removed), `run.excluded`, `employee.bank_change_requested`, `employee.bank_change_approved`, `company.disconnected`, `approval.recorded` (with whether it came from Taskiem).
- The webhook payloads keep leaving out bank numbers and breakdowns, as now; Taskiem reads those with its scopes.

## Security questions for both teams

- **Taskiem's signing key for approvals:** one per company connection, kept in Taskiem's vault, its public key registered with Payrolla at connect; rotation without a gap.
- **Taskiem's step-up as Payrolla's second factor:** Payrolla's go-live checklist and its auditors accept a passkey made in Taskiem as the approver's factor.
- **Replays:** the signed approval carries a nonce and an expiry (five minutes), and Payrolla refuses one it has seen.
- **Who sees what:** Taskiem shows salaries only to those a company's owner allows (Taskiem's own permission for payroll), whatever the key's scopes.

## What Taskiem builds before any of this

With today's read API (P4a in the build plan): Payroll in Taskiem's own screens showing Payrolla's runs and each line's status, the approval card in the chat with Payrolla's approval link, payslip links to each employee's own chat, the payday and lock reminders, returned salaries, and staff linked to Payrolla employees (by phone or email, confirmed by the owner). Each request above, once Payrolla ships it, turns a link into Payrolla into an action in Taskiem.
