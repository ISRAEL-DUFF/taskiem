# Dogfood workflows (Phase 0 drafts)

These are the three Phase 1 acceptance workflows required by gate G0. They are **drafts written from the build plan's examples**; each owner must confirm the payloads, endpoints, and roles against the real product before Phase 1 starts. Assumptions to confirm are listed per workflow.

All three validate against `schemas/wd-v1.schema.json` and the semantic rules in `docs/contracts/wd-v1.md` (`go test ./engine/wd/...`).

## 1. Payrolla salary disbursement — `payrolla-salary-disbursement.wd.json`

**Trigger.** Payrolla posts `payroll_id`, `period`, `total_kobo`, and `employees[]` (id, net pay in kobo, Paystack recipient code) to the webhook once a payroll is approved in Payrolla. Duplicate deliveries of the same `payroll_id` return the original run.

**Expected behaviour.**

1. Summarise the payroll and read the Paystack NGN balance (read-only, retried freely).
2. Pause for maker-checker approval by a `payroll_approver`, showing the total, headcount, and balance. Rejection or a 24h timeout ends the run with no transfers.
3. On approval, pay each employee with a Paystack transfer, at most 5 at a time. The idempotency seed is `payroll_id:employee_id`, so re-running or forking the run never pays an employee twice for the same payroll.
4. For transfers Paystack reports as `pending`, wait up to 24h for the `transfer.success`, `transfer.failed`, or `transfer.reversed` webhook.
5. Post the per-employee results back to Payrolla.

**Must never happen.** An employee paid twice for one payroll; a transfer before approval; an approver who triggered the payroll approving it.

**Assumptions to confirm.** Payrolla's webhook payload and HMAC secret; a report endpoint on Payrolla's API; that Paystack transfer OTP is disabled for the API integration (otherwise transfers return `otp`); the role name.

## 2. iSpend wallet top-up reconciliation — `ispend-topup-reconciliation.wd.json`

**Trigger.** Every 15 minutes (Africa/Lagos). Runs never overlap: `concurrency_key` is constant and the run timeout (14m) is shorter than the interval.

**Expected behaviour.**

1. Read up to 500 top-ups still `pending` after 15 minutes from iSpend's database through a read-only connection.
2. For each (10 at a time), verify the charge with Paystack.
3. Paid in full → mark the top-up complete in iSpend, which credits the wallet. Paid a different amount → flag it for a human. Failed, abandoned, or reversed → mark it failed. Still pending → leave it for the next run.

**Must never happen.** A wallet credited twice for one Paystack reference (every write uses the reference as its idempotency seed and iSpend's endpoint honours `Idempotency-Key`); a wallet credited for an amount that differs from what was paid.

**Assumptions to confirm.** Table and column names; that iSpend exposes idempotent `complete`, `flag`, and `fail` endpoints; a read-only database user for Taskiem.

## 3. Ops transfer-failure alert — `ops-transfer-failure-alert.wd.json`

**Trigger.** Paystack `transfer.failed` or `transfer.reversed` webhook, deduplicated on reference plus event.

**Expected behaviour.** Build one message, then in parallel send it by SMS to the on-call number (Termii) and post it to the ops Slack channel.

**Must never happen.** A failed transfer with no alert.

**Open finding for Phase 1.** Termii SMS and Slack incoming webhooks have no idempotency key or status API, so both are `unsafe_write`: an unknown outcome parks the step in `needs_reconciliation` rather than retrying. That is right for money and wrong for alerts, where a duplicate is harmless but a missing alert is not. Phase 1 should decide whether to add a per-step opt-in (for example `effect.duplicates: tolerable`) that lets an `unsafe_write` retry an unknown outcome. Recorded in `docs/contracts/action-classes.md`.

**Assumptions to confirm.** On-call phone and Slack webhook held as tenant variables and secrets; Termii sender and channel.
