# Example workflows

Workflows that exercise connectors and patterns but are not dogfood acceptance tests.

- `topup-reconciliation-postgres-paystack.wd.json`: the first iSpend draft, written before we knew iSpend tops up through iswallet. Every 15 minutes it reads stale pending top-ups from a database with the Postgres connector, verifies each with Paystack, and completes, flags, or fails it through an idempotent HTTP API. Kept as the end-to-end example of a scheduled `foreach` over a database query (`e2e.TestTopupReconciliationExample`).
- `ussd-airtime-topup.wd.json`: a USSD menu ([USSD](../../docs/ussd.md)). A caller dials `*384*123#`, chooses their own or another number and an amount, and confirms; the run sends the airtime through Africa's Talking and the caller gets an SMS when it is done or has failed.
- `month-end-pay-and-tell.wd.json`: templates as steps ([decision 0047](../../docs/decisions/0047-composable-templates.md)). On the 25th it runs payroll with the Run payroll template, then pays the month's suppliers with the Pay a list template (the batch named after the month payroll paid), then tells the owner how both went, reading each template's outputs. Both templates' approvals, payments and waits run in this one run.
