# Dogfood workflows (Phase 0 drafts)

These are the three Phase 1 acceptance workflows required by gate G0. All the group's products move money through **iswallet**; the Paystack-based first drafts were rewritten on 5 October 2026 once iswallet's answers arrived. They are **drafts written from the build plan's examples**; each owner must confirm the payloads, endpoints, and roles against the real product before Phase 1 starts. Assumptions to confirm are listed per workflow.

All three validate against `schemas/wd-v1.schema.json` and the semantic rules in `docs/contracts/wd-v1.md` (`go test ./engine/wd/...`).

## 1. Payrolla salary disbursement — `payrolla-salary-disbursement.wd.json`

**Provider.** iswallet ([integration](../../docs/integrations/iswallet.md)). Payrolla pays from its company wallet on iswallet.

**Trigger.** Payrolla posts `payroll_id`, `period`, `funding_wallet_id`, `total_kobo`, and `employees[]` to the webhook once a payroll is approved in Payrolla, signed with HMAC-SHA256. Each employee has either an iswallet `wallet_id` or bank details (`bank_code`, `account_number`, `account_name`). Duplicate deliveries of the same `payroll_id` return the original run.

**Expected behaviour.**

1. Summarise the payroll and read the funding wallet's available NGN balance.
2. Pause for maker-checker approval by a `payroll_approver`, showing the total, headcount, how many are paid to banks, and the balance. Rejection or a 24-hour timeout ends the run with nothing paid.
3. Pay each employee, at most 5 at a time: by wallet transfer when they have an iswallet wallet (instant, final, free), otherwise by bank payout, then wait up to 72 hours for iswallet's confirmation. The idempotency seed is `payroll_id:employee_id`.
4. Post the per-employee results back to Payrolla.

**Must never happen.** An employee paid twice for one payroll; a payment before approval; an approver who triggered or wrote the workflow approving it.

**Known behaviour to agree with Payrolla.** A payment refused by iswallet (a limit, insufficient funds, a rejected account) stops new payments in that run and fails it, so someone decides before the rest is paid; payments already made stand. A confirmed bank payout cannot be reversed by API.

## 2. iSpend wallet top-up reconciliation — `ispend-topup-reconciliation.wd.json`

> **To be replaced.** This draft assumed Paystack top-ups that can stay pending. iSpend tops up through iswallet virtual accounts, which iswallet settles itself within seconds (answers §F1), so there is nothing pending to chase. The replacement workflow is being decided; candidates are in [the iswallet integration notes](../../docs/integrations/iswallet.md). The draft stays for its test coverage of the Postgres connector until then.

**Trigger.** Every 15 minutes (Africa/Lagos). Runs never overlap: `concurrency_key` is constant and the run timeout (14m) is shorter than the interval.

**Expected behaviour.**

1. Read up to 500 top-ups still `pending` after 15 minutes from iSpend's database through a read-only connection.
2. For each (10 at a time), verify the charge with Paystack.
3. Paid in full → mark the top-up complete in iSpend, which credits the wallet. Paid a different amount → flag it for a human. Failed, abandoned, or reversed → mark it failed. Still pending → leave it for the next run.

**Must never happen.** A wallet credited twice for one Paystack reference (every write uses the reference as its idempotency seed and iSpend's endpoint honours `Idempotency-Key`); a wallet credited for an amount that differs from what was paid.

**Assumptions to confirm.** Table and column names; that iSpend exposes idempotent `complete`, `flag`, and `fail` endpoints; a read-only database user for Taskiem.

## 3. Ops payout-failure alert — `ops-payout-failure-alert.wd.json`

**Trigger.** iswallet's `wallet.outflow.failed` or `wallet.outflow.reversed` webhook (a reversal can come days after a payout was confirmed). Redeliveries are dropped on the event id.

**Expected behaviour.** Build one message, then send it by SMS to the on-call number (Termii) and post it to the ops Slack channel.

**Must never happen.** A failed or reversed payout with no alert.

**Open finding for Phase 1.** Termii SMS and Slack incoming webhooks have no idempotency key or status API, so both are `unsafe_write`: an unknown outcome parks the step in `needs_reconciliation` rather than retrying. That is right for money and wrong for alerts, where a duplicate is harmless but a missing alert is not. Recorded in `docs/contracts/action-classes.md`.

**Assumptions to confirm.** On-call phone and Slack webhook held as tenant variables and secrets; Termii sender and channel.
