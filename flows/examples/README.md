# Example workflows

Workflows that exercise connectors and patterns but are not dogfood acceptance tests.

- `topup-reconciliation-postgres-paystack.wd.json`: the first iSpend draft, written before we knew iSpend tops up through iswallet. Every 15 minutes it reads stale pending top-ups from a database with the Postgres connector, verifies each with Paystack, and completes, flags, or fails it through an idempotent HTTP API. Kept as the end-to-end example of a scheduled `foreach` over a database query (`e2e.TestTopupReconciliationExample`).
- `ussd-airtime-topup.wd.json`: a USSD menu ([USSD](../../docs/ussd.md)). A caller dials `*384*123#`, chooses their own or another number and an amount, and confirms; the run sends the airtime through Africa's Talking and the caller gets an SMS when it is done or has failed.
