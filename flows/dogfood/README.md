# Dogfood workflows

These are Phase 1 acceptance workflows required by gate G0. The group's products move money through **iswallet**; the Paystack-based first drafts were rewritten on 5 October 2026 once iswallet's answers arrived. Each owner confirms the payloads, endpoints, and roles against the real product before go-live. Assumptions to confirm are listed per workflow.

The third, Payrolla's salary disbursement, was retired by [decision 0044](../../docs/decisions/0044-rails-and-products.md): Payrolla pays its customers' staff itself, so Taskiem connects to it as a product rather than paying for it. Payroll for any business is the [Run payroll](../../templates/library/run-payroll.json) template, on any rail; its workflow tests ([`templates/tests/run-payroll.test.json`](../../templates/tests/run-payroll.test.json)) carry the old flow's cases.

Both validate against `schemas/wd-v1.schema.json` and the semantic rules in `docs/contracts/wd-v1.md` (`taskiem validate flows/dogfood/*.wd.json`). Their behaviour is pinned by workflow tests in `tests/` ([format](../../docs/contracts/wd-test-v1.md)): run `taskiem test flows`.

## 1. iSpend credit exceptions — `ispend-credit-exceptions.wd.json`

**Why this workflow.** iSpend customers top up by bank transfer to an iswallet virtual account, and iswallet credits the wallet within seconds, so there is nothing pending to chase. What goes wrong silently is the exception: a top-up iswallet refuses and refunds to the sender (`wallet.credit.rejected`), where the customer's money left their bank and no credit arrives, and a credit iswallet claws back after posting it (`wallet.credit.reversed`), where iSpend may already have issued a receipt.

**Trigger.** iswallet's `credit_event` webhook for those two events, on the iSpend connection. Redeliveries are dropped on the event id; ordinary `wallet.credit.posted` events are ignored.

**Expected behaviour.** Describe the exception, then in parallel tell iSpend (`POST {ispend_api_url}/internal/wallet-credit-exceptions` with the event id, kind, wallet, amount and iswallet's data, sent with an `Idempotency-Key`) and post it to the ops Slack channel.

**Must never happen.** A rejected or reversed credit that iSpend never hears about.

**Assumptions to confirm with iSpend.** The endpoint and its payload (iSpend builds it; it must honour `Idempotency-Key`); what iSpend does with it (void the receipt, update the wallet view, tell the customer). The exact fields iswallet sends on `wallet.credit.reversed` (the answers list "credit identifiers + reversal reference"; the whole `data` object is forwarded, and the amount is 0 when absent).

**Later.** A daily reconciliation of iswallet's transaction list against iSpend's records, and an alert on parked credits once iswallet exposes a count to client keys (their §H item 7).

## 2. Ops payout-failure alert — `ops-payout-failure-alert.wd.json`

**Trigger.** iswallet's `wallet.outflow.failed` or `wallet.outflow.reversed` webhook (a reversal can come days after a payout was confirmed). Redeliveries are dropped on the event id.

**Expected behaviour.** Build one message, then send it by SMS to the on-call number (Termii) and post it to the ops Slack channel.

**Must never happen.** A failed or reversed payout with no alert.

**Open finding for Phase 1.** Termii SMS and Slack incoming webhooks have no idempotency key or status API, so both are `unsafe_write`: an unknown outcome parks the step in `needs_reconciliation` rather than retrying. That is right for money and wrong for alerts, where a duplicate is harmless but a missing alert is not. Recorded in `docs/contracts/action-classes.md`.

**Assumptions to confirm.** On-call phone and Slack webhook held as tenant variables and secrets; Termii sender and channel.
