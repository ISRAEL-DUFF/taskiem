# Dogfood workflows

These are the three Phase 1 acceptance workflows required by gate G0. All the group's products move money through **iswallet**; the Paystack-based first drafts were rewritten on 5 October 2026 once iswallet's answers arrived. Each owner confirms the payloads, endpoints, and roles against the real product before go-live. Assumptions to confirm are listed per workflow.

All three validate against `schemas/wd-v1.schema.json` and the semantic rules in `docs/contracts/wd-v1.md` (`taskiem validate flows/dogfood/*.wd.json`). Their behaviour is pinned by workflow tests in `tests/` ([format](../../docs/contracts/wd-test-v1.md)): run `taskiem test flows`.

## 1. Payrolla salary disbursement — `payrolla-salary-disbursement.wd.json`

**Provider.** iswallet ([integration](../../docs/integrations/iswallet.md)). Payrolla pays from its company wallet on iswallet.

**Trigger.** Payrolla posts `payroll_id`, `period`, `funding_wallet_id`, `total_kobo`, and `employees[]` to the webhook once a payroll is approved and locked in Payrolla and someone presses **Send to Taskiem**, signed with HMAC-SHA256. Each Payrolla customer company is its own Taskiem sub-tenant, with its own webhook URL, signing secret, token, funding wallet and approvers. Each employee has either an iswallet `wallet_id` or bank details (`bank_code`, `account_number`, `account_name`). Duplicate deliveries of the same `payroll_id` return the original run.

**Expected behaviour.**

1. Summarise the payroll and read the funding wallet's available NGN balance.
2. Pause for maker-checker approval by a `payroll_approver`, showing the total, headcount, how many are paid to banks, and the balance. Rejection or a 24-hour timeout ends the run with nothing paid.
3. Pay each employee, at most 5 at a time: by wallet transfer when they have an iswallet wallet (instant, final, free), otherwise by bank payout, then wait up to 72 hours for iswallet's confirmation. The idempotency seed is `payroll_id:employee_id`.
4. Post the results back to Payrolla: how many were paid and how many failed, totals (amount paid and bank fees in kobo, counts by outcome), and one entry per employee in payroll order, carrying the employee id and net pay and either the payment or a failure record with iswallet's error. A rejected or expired approval is reported too. The contract with Payrolla is [docs/integrations/payrolla.md](../../docs/integrations/payrolla.md).
5. Watch every confirmed bank payout for a reversal (`wallet.outflow.reversed`, correlated on its `outflow_id`) until 35 days after the payroll arrived (`payrolla_reversal_watch_days`, 1 to 60), and post a reversal notice to `{payrolla_api_url}/payrolls/{payroll_id}/reversal-notice` for each one. Then the run ends.

Wallet and bank payments carry `Salary <period> <payroll_id> <employee_id>` as their narration (cut to iswallet's 255 characters); bank payouts also carry both ids in `metadata`.

**Must never happen.** An employee paid twice for one payroll; a payment before approval; an approver who triggered or wrote the workflow approving it; the reversal watch delaying or changing the report, or paying anyone.

**When a payment fails.** A payment iswallet refuses (a limit, insufficient funds, a rejected account), a bank payout iswallet reports as failed, or one it does not confirm within 72 hours, is recorded as failed for that employee and does not stop the others; Payrolla gets every failure in the report and decides what to do. A payment whose outcome is unknown is different: the run waits in `needs_reconciliation` until an operator checks iswallet's ledger, because reporting it as failed could lead to paying twice. A confirmed bank payout cannot be reversed by API.

**Watching for reversals.** The watch starts only after the report has been sent (it needs the `report` step), and its steps are a signal wait, a timer and a POST to Payrolla: nothing in it calls iswallet. Each confirmed payout's reversal races the deadline in its own `parallel` (join `any`); the notice comes after the race, so the deadline can never cancel a notice that is being retried. A notice Payrolla refuses with a 4xx is recorded as `notice_failed` and the other payouts stay watched. A reversal for an outflow no run waits for wakes nothing (it is buffered for seven days and dropped). The payouts are watched in lanes of 1,000 (`watch[0..4].watch_lane`), because a `foreach` runs at most 1,000 iterations at once and a payroll has up to 5,000 employees. The run's timeout is 70 days, beyond the longest watch.

A watching run is cheap: one signal wait and one timer per confirmed payout, no worker and no polling; it is decided again only when a reversal or the deadline arrives. What people see:

| Where | What it shows for a watching run |
| --- | --- |
| Run list | Status `running` with no duration until the watch ends (the engine has no separate status for a run that only waits; see [Phase 4 status](../../docs/phase-4-status.md#payrolla-answers-reversal-notices-and-totals)) |
| Run page | `watching_for_reversals` completed, named "Watching for reversals (report sent)", with a note, `watch_until`, how many payouts are watched and their outflow ids; under `watch`, one `reversal_event` waiting for `iswallet@1:outflow_event` and one `watch_ends` timer per payout. `report` shows when the report went |
| Dashboard | Counted as active, and in the success rate only once it ends. Completed-run durations (p50, p95) for this workflow include the watch, so they do not measure how long paying takes: read the `report` step's time on the run page for that |
| Limits | Holds one place under a plan's `max_running_runs` until the watch ends |

## 2. iSpend credit exceptions — `ispend-credit-exceptions.wd.json`

**Why this workflow.** iSpend customers top up by bank transfer to an iswallet virtual account, and iswallet credits the wallet within seconds, so there is nothing pending to chase. What goes wrong silently is the exception: a top-up iswallet refuses and refunds to the sender (`wallet.credit.rejected`), where the customer's money left their bank and no credit arrives, and a credit iswallet claws back after posting it (`wallet.credit.reversed`), where iSpend may already have issued a receipt.

**Trigger.** iswallet's `credit_event` webhook for those two events, on the iSpend connection. Redeliveries are dropped on the event id; ordinary `wallet.credit.posted` events are ignored.

**Expected behaviour.** Describe the exception, then in parallel tell iSpend (`POST {ispend_api_url}/internal/wallet-credit-exceptions` with the event id, kind, wallet, amount and iswallet's data, sent with an `Idempotency-Key`) and post it to the ops Slack channel.

**Must never happen.** A rejected or reversed credit that iSpend never hears about.

**Assumptions to confirm with iSpend.** The endpoint and its payload (iSpend builds it; it must honour `Idempotency-Key`); what iSpend does with it (void the receipt, update the wallet view, tell the customer). The exact fields iswallet sends on `wallet.credit.reversed` (the answers list "credit identifiers + reversal reference"; the whole `data` object is forwarded, and the amount is 0 when absent).

**Later.** A daily reconciliation of iswallet's transaction list against iSpend's records, and an alert on parked credits once iswallet exposes a count to client keys (their §H item 7).

## 3. Ops payout-failure alert — `ops-payout-failure-alert.wd.json`

**Trigger.** iswallet's `wallet.outflow.failed` or `wallet.outflow.reversed` webhook (a reversal can come days after a payout was confirmed). Redeliveries are dropped on the event id.

**Expected behaviour.** Build one message, then send it by SMS to the on-call number (Termii) and post it to the ops Slack channel.

**Must never happen.** A failed or reversed payout with no alert.

**Open finding for Phase 1.** Termii SMS and Slack incoming webhooks have no idempotency key or status API, so both are `unsafe_write`: an unknown outcome parks the step in `needs_reconciliation` rather than retrying. That is right for money and wrong for alerts, where a duplicate is harmless but a missing alert is not. Recorded in `docs/contracts/action-classes.md`.

**Assumptions to confirm.** On-call phone and Slack webhook held as tenant variables and secrets; Termii sender and channel.
