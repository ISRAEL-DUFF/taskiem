# 0074 — Payroll run from Taskiem on Payrolla's Connect API, confirmed with Payrolla's own code

Date: 2026-11-23 · Status: **Accepted (24 November 2026)** (P4b planned; the plan's open points settled by the founder the same day) · Plan: [Payroll in Taskiem, on Payrolla](../spec/chat-pay-people.md#payroll-in-taskiem-on-payrolla) · Build plan: [P4b](../spec/build-plan-saved-tools-payments.md#stage-4-the-rest-of-payments) · Amends: [0044](0044-rails-and-products.md), its note of 22 November 2026 (approving was a link into Payrolla) · Partner: Payrolla's replies ([Phase A](../integrations/payrolla-reference/05-phase-a-response.md), [Phase B](../integrations/payrolla-reference/06-phase-b-response.md), [Phase C](../integrations/payrolla-reference/07-phase-c-response.md)) and [our answers](../integrations/payrolla-reference/08-taskiem-answers.md)

## Context

Payroll in Taskiem is Payrolla's ([0044](0044-rails-and-products.md), note of 22 November 2026): Payrolla keeps the employees, works out each run, keeps the approvals and pays every salary. Taskiem is its front. P4a built that front on Payrolla's read API: the Payroll page, staff linked to Payrolla's employees, `payroll.read`, and pay and payslip messages to each employee's own chat ([Payrolla](../integrations/payrolla.md)). Everything that changed something was a link into Payrolla, because Payrolla had no API for it.

On 22 November 2026 Taskiem asked Payrolla for the rest ([the request](../integrations/payrolla-api-requests.md), partner item P7). Payrolla answered and shipped all of it in three phases, now live:

- **Phase A:** employees (add with a bank account checked with the bank, change, end), runs (make, see every line's breakdown, bonus or deduction, leave someone out, recalculate, cancel), payslips as data, an employee's payslips, and the funding wallet's balance. Every write takes `Idempotency-Key`, `X-Acting-User` and `X-Acting-User-Name`.
- **Phase B:** approving, rejecting and locking a run from Taskiem; paying now and paying the failed ones again; bank account changes held for a second approver; Taskiem people linked to their Payrolla users.
- **Phase C:** connecting by OAuth 2.0 with PKCE, with sign-up in the same step; an employee changing their own salary account; read-only settings and the statutory schedules.

Payrolla decided one thing differently. Taskiem asked it to accept **a Taskiem-signed approval after Taskiem's passkey**. Payrolla refused: it could check the signature but not that a passkey was used, or by whom, so whoever held Taskiem's key for a company could approve that company's payroll. Instead each step that commits money is confirmed with **a one-time code Payrolla sends to the person's own factor** (their authenticator app, their verified email, or a phone their owner confirmed), which the person types into Taskiem. The code lasts 5 minutes, allows 5 tries, works once, and only for that one action on that one run.

## Decision

1. **Taskiem runs payroll end to end in its own screens and chat, on Payrolla's Connect API.** In the web, the phone app and the chat, a business:
   - connects Payrolla in one step;
   - adds, changes and ends employees and their pay;
   - prepares this month's run, sees each person's breakdown, adds a bonus or a deduction, leaves someone out or puts them back;
   - approves or rejects it, locks it, pays it now, and pays the failed ones again;
   - approves bank account changes held for a second approver;
   - sees the payment's progress, the funding wallet and its shortfall, each payslip as Taskiem's own card, the payroll settings and the statutory schedules.

   An employee sees their own payslips and asks to change their own salary account. **Payrolla stays the only payer.** Nothing in Taskiem pays a salary, and no Payrolla screen is embedded. What is left in Payrolla's own screens: changing settings, unlocking by a correction run, cancelling an approved run, and changing a phone number.
2. **Committing money is confirmed with Payrolla's code, not Taskiem's passkey or a Taskiem-signed approval.** For approving, locking, paying now, paying the failed ones again, approving a held bank change, linking a Payrolla user, and an employee's own bank change:
   - Taskiem asks Payrolla for a code for that one action. Payrolla checks first that it is allowed (the run's state, the person's role, separation of duties) and refuses in words before anything is sent.
   - Payrolla sends the code. Its message names what is being confirmed ("lock November 2026 payroll and pay ₦9,200,000 to 48 people on 28 Nov 2026").
   - The person types the code into Taskiem: the web, the app, or the Taskiem thread on WhatsApp or Telegram. Slack answers with a link to the web.
   - Taskiem sends the code to Payrolla with the action, once. It never stores, logs or shows the code, and the model never sees it.

   Taskiem adds no passkey of its own to these steps: Payrolla's code is the factor, and it is Payrolla's. Rejecting moves no money, so it needs a linked person and no code.
3. **A Taskiem person acts as one Payrolla user, linked once, by a code.** Who may approve or pay is Payrolla's list (`GET /connect/approvers`). A person in Taskiem links themselves to their Payrolla user with one code, and an owner can unlink anyone. Taskiem keeps no copy of the link: Payrolla's `linked_as` is the record.
4. **Locking commits the payment.** There is no separate "pay" step: Payrolla pays everyone automatically on the pay date, or at once if the date has passed. Taskiem calls it **Lock and pay** and says plainly that a locked run cannot be unlocked. If the wallet is short on the pay date, the run waits for funds (`run.awaiting_funds`) and pays as soon as the wallet is topped up. Taskiem warns earlier, from the funding balance.
5. **Connecting is one step, by OAuth 2.0 with PKCE, through Taskiem's own OAuth machinery** ([0034](0034-oauth-connections.md), [0051](0051-platform-apps.md)):
   - A business not yet on Payrolla signs up in the same step, with its name, RC number and the owner's email filled in.
   - Refresh tokens rotate and work once, so they are refreshed under the connection's lock (`RotateCredentials`).
   - `company.disconnected` and a refused refresh token mark the connection expired, and owners see **Reconnect Payrolla**.
   - **Companies connected with a key keep working.** Their owner sees **Reconnect Payrolla** and moves over in one step. Approvers link again once each, and Taskiem sets the webhooks up again by itself.
6. **Payrolla's own rules are taken as they are,** and said to the business in plain words:
   - **One approver per run.** No two-approver rule, and no Taskiem approval policy on top.
   - **Separation of duties**, when the company has it on: whoever prepared a run may not approve it. The same goes for a bank change and whoever asked for it.
   - **No unlocking.** A locked run is corrected with a correction run, in Payrolla.
   - **No job title.** Payrolla keeps none.
   - **A phone number cannot be changed from Taskiem.**
   - **Ending someone's employment takes effect at once.** There is no last day.
   - **An advance repaid is a deduction.**
   - **Bonuses and deductions are gross,** so tax and pension apply to them.
   - **A bank change is held** until another Owner or Approver approves it, except where the person who asked is the company's only one.
7. **Who may do what in Taskiem** is Taskiem's permission, held as well as Payrolla's role, never instead of it:
   - `payroll.read` (exists): see runs, salaries, breakdowns, payslips, funding, settings and schedules.
   - `payroll.prepare` (new): add, change and end employees, ask for bank changes, and prepare, adjust, leave out, recalculate and cancel runs.
   - `payroll.approve` (new): approve, reject, lock, pay now and pay the failed ones again, and approve held bank changes, as their linked Payrolla user, who must be an Owner or Approver (and allowed to pay, to pay).

   All three are the owner's alone unless given to a role. An employee needs none to see their own payslips or change their own salary account: they must be the staff member linked to that employee.
8. **Every write to Payrolla carries an `Idempotency-Key` and the acting person.**
   - **The key** is fixed for one submission and reused only to retry it. Each attempt at a code gets a new one.
   - **`X-Acting-User`** is the person's Taskiem user id: the id of their own account, the same in every business they work in and never reused ([0068](0068-taskiem-for-everyone.md)). It is not a membership id.
   - **`X-Acting-User-Name`** is their name as the business knows them.
   - **An employee Taskiem adds** gets the same id as `external_ref` when they are a Taskiem person.
9. **Taskiem keeps as little as it can:**
   - **Kept:** the connection (its tokens or key sealed in the vault, its scopes and the company's id and name), the webhook subscription and its secret, and the staff-to-employee links P4a already keeps.
   - **Never kept:** no salary, breakdown, bank account, pension PIN or payslip. Screens fetch them from Payrolla when they are opened, and nothing is cached in the database.
   - **Chat:** a chat card keeps Payrolla's ids and the period. A message carries a run's total and headcount, never one person's pay.
   - **Audit:** records who did what to which run or employee, without amounts of pay.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Taskiem's passkey and a Taskiem-signed approval, as asked | Payrolla refused it, for a good reason: it can check a signature, not that a person used a passkey. Payrolla says it may offer it later as something an owner opts into per company |
| Taskiem's passkey **and** Payrolla's code on every approval | Two factors for one decision, from two products, and the second adds nothing Payrolla relies on. Settled by the founder, 24 November 2026: no passkey of Taskiem's own ([the plan's settled points](../spec/chat-pay-people.md#settled-by-the-founder-24-november-2026), 3) |
| Keep approving as a link into Payrolla (P4a) | A business would still use two products for the one thing that matters most. Kept only for a key connection without `payroll:approve`, until its owner reconnects |
| Embed Payrolla's screens (its "trust the app's sign-in" option included) | Already refused (0044's note of 22 November 2026). R6's embed stays unbuilt |
| Taskiem's own approval policies on top of Payrolla's (a second approver, a limit) | Two sets of rules about one payroll, which can disagree. Payrolla's are the payroll's, and Taskiem shows them |
| Keep a copy of runs, lines and payslips in Taskiem, for speed or search | A second copy of everyone's pay, to seal, keep right and delete. Payrolla answers in one call, and 120 calls a minute is far more than a Payroll page needs |
| Move key-connected companies over by force | Their keys work and Payrolla keeps them. Reconnect is one step, offered where it gains something |

## Consequences

- **P4b is planned as five slices**, each shipping on its own ([the plan](../spec/chat-pay-people.md#p4b-slice-by-slice)):
  1. connecting;
  2. staff and pay;
  3. this month's run;
  4. after locking;
  5. settings and schedules.
- **The connector `payrolla@1`** gains `auth.oauth2` (PKCE, HTTP Basic at the token URL, a platform app per mode, sign-up fields in `authorize_params`), the writes of Phases A to C, the new events, and Taskiem's own webhook subscription per connection.
- **`engine/access`** gains `payroll.prepare` and `payroll.approve`.
- **The Payrolla templates change.** The approval card becomes Taskiem's own card with **Approve**, and `payrolla-approve-by-chat` stays only for key connections without `payroll:approve`. The lock reminder offers **Lock and pay**. The payday check reads Payrolla's funding instead of another connection's balance.
- **Paying people's settled rules change for payroll only.** The first-payment warning on payroll approvals (P1) is not built: Taskiem never sees a salary account, and Payrolla holds every bank change for a second approver. A code typed in a chat thread is accepted for payroll, where a payment's PIN is never typed in a message. Both were confirmed by the founder on 24 November 2026 ([the plan's settled points](../spec/chat-pay-people.md#settled-by-the-founder-24-november-2026), 2 and 4).
- **The P7 request is answered.** Payrolla's open questions are answered in [08-taskiem-answers](../integrations/payrolla-reference/08-taskiem-answers.md), a draft for the founder to send.
- **The local copy of Payrolla's OpenAPI file is out of date** (`payrolla-reference/api/`, from 23 October). The current one is fetched before P4b-1, and the fake Payrolla the tests use follows it.

## Provenance

Payrolla's three replies (22 and 23 November 2026), kept unedited in `docs/integrations/payrolla-reference/` (05 to 07), answering Taskiem's request of 22 November 2026. The code-instead-of-signature design is Payrolla's. Taskiem adopted it because it keeps the factor with the payer.
