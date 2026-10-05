# 0012 — Trigger registration, environments, and webhook secrets

Date: 2026-10-05 · Status: Accepted

## Decision

1. **Registered at publish.** Publishing a version replaces the workflow's rows in `triggers` in the same transaction. A webhook path is unique per tenant and environment; publishing a second workflow on a taken path fails with 409.
2. **Environments.** Webhook and connector-event triggers are registered in every environment; a delivery names its environment with `?env=` (default `prod`), so one provider configuration per environment is a URL change. Schedules fire in `prod` only; other environments start scheduled workflows manually.
3. **Webhook authentication.** `hmac` checks `X-Taskiem-Signature: sha256=<hex>` (HMAC-SHA256 of the raw body); `bearer` checks `Authorization: Bearer <token>`. The key is the environment secret named `webhook_<WD id>` (for example `webhook_wf_payrollaSalaryDisbursement`). A missing secret refuses every delivery. `mtls` and synchronous `respond` are rejected at publish until the edge proxy exists.
4. **Connector events.** Providers post to `/hooks/{tenant}/connectors/{connector}/{trigger}`; the delivery is verified with the connection's credential named by the manifest (`?connection=` picks one when several exist). It is delivered as the signal `<connector>:<trigger>` (for example `paystack@1:transfer_event`) with payload `{event, body}` to runs waiting on its correlation, and starts every workflow subscribed to the trigger and event. Signal delivery is deduplicated in the delivering transaction (`trigger_receipts`, run id nil), so provider retries never deliver twice.
5. **Expression roots.** A WD's webhook `dedup` sees the run's `trigger` root, like any WD expression. Manifest expressions (`event_type`, `dedup`, `correlation`) see `body`, `headers`, and `query`.
6. **Schedules.** Cron (five fields or descriptors) in the trigger's zone, default Africa/Lagos. Each fire starts a run deduplicated on (workflow, scheduled time) and then advances the schedule, so a crash between the two repeats the fire harmlessly. After downtime a schedule fires once for its latest missed time.

## Why

- Same-transaction registration means a published version and its listeners never disagree.
- `?env=` keeps the spec's `/hooks/{tenant}/{path}` shape while letting dev receive real provider sandboxes.
- A naming convention avoids a new secret type for Phase 1; the secret is still envelope-encrypted, write-only through the API, and audited.

## Not yet

Soft ingest limits that queue runs (spec 8.3) — Phase 1 enforces the hard per-tenant ceiling (429 with `Retry-After`) only. Polling, database-change, email, WhatsApp, and USSD triggers are later phases.
