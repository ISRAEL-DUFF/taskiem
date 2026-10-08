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

~~Soft ingest limits that queue runs (spec 8.3) — Phase 1 enforces the hard per-tenant ceiling (429 with `Retry-After`) only.~~ Done 2026-10-07, see the amendment below. Polling, database-change, email, WhatsApp, and USSD triggers are later phases.

## Amendment 2026-10-07: soft ingest limits and plan caps

Spec 8.3 and 16 are now enforced per tenant (migration 00034, [operations](../operations.md#plan-limits)).

1. **Two rates.** Each tenant has a soft ingest rate and a hard ceiling (platform defaults 20/s burst 100 and 50/s burst 200, `TASKIEM_DEFAULT_*`; operators override per tenant). Above the ceiling a delivery gets 429 with `Retry-After`, as before. Between the two it is verified, deduplicated and recorded like any other and answered 202 with `"queued": true`: the run it starts is stored `queued` (`queue_reason = 'tenant'`) with its `RunStarted`, and nothing is decided until it is admitted. The rate limiters live in each edge process; admission does not.
2. **Admission is in the database.** Each scheduler tick lists the tenants with held runs (a `SECURITY DEFINER` routing function), and for each one claims its token bucket row (`tenant_admission`, `FOR UPDATE SKIP LOCKED`), refills it at the tenant's soft rate and starts its oldest held runs (`RunAdmitted`, then the first decision), up to its running-runs cap. A held run that then finds its workflow's concurrency taken waits on as an ordinary queued run. Tenants have separate buckets and every tick visits every tenant with a backlog, so one tenant's flood does not delay another's admissions; a restart loses nothing.
3. **Order.** While a tenant has held runs, its new starts (manual and scheduled too) queue behind them, so the backlog drains first in, first out.
4. **Signals are never held.** A connector event above the soft rate still delivers its signal to waiting runs at once; only the runs it starts are queued.
5. **Deduplication first.** The receipt is checked before any limit, so a provider's retry of a queued delivery returns the original run and is not counted again; a start refused by a limit records nothing (no receipt), so the retry after the limit lifts starts it. An accepted delivery is never dropped.
6. **Refusals.** A full backlog (`max_queued_runs`, default 10,000) answers 429 `backlog_full` with `Retry-After`. Run quotas (`runs_per_day`, `runs_per_month`, UTC, off by default because spec 16 prices plans flat) answer 429 `quota_exceeded` with `Retry-After` until the period ends, for webhooks, connector events and API starts alike; a schedule fire beyond a quota is skipped, logged and counted, and the schedule moves on. Every limit reached is recorded per tenant and day and can alert the tenant (alert rule kind `limit`).
7. **Why 429 for an exhausted quota, not 402.** Webhook providers retry a 429 (honouring `Retry-After`) and disable endpoints that keep answering other 4xx codes; a 402 would risk losing the endpoint while the tenant sorts out its plan. The `code` field (`quota_exceeded`, `backlog_full`, `rate_limited`, `limit_exceeded`) tells clients which limit it was.
8. **Workers.** Spec 16.2's per-tenant throughput is enforced where tasks are claimed: `taskiem_claim_tasks` takes tasks round-robin across tenants and never gives a tenant more than its `worker_concurrency` in flight on a queue (default 32), so one tenant's backlog cannot hold every worker slot.

## Amendment 2026-10-07: remotely registered triggers

A connector trigger may now be registered at its provider by Taskiem ([decision 0021](0021-remote-trigger-registration.md)): the publishing transaction records the subscription a deployed version wants (point 1 still holds: nothing is sent from it), and a reconciler creates, updates, checks and deletes it at the provider. Such a trigger is delivered to an ingest URL naming its subscription and verified with the secret the provider returned, kept in the vault, instead of a connection field (point 4). `DELETE /v1/workflows/{wf}/deployments/{env}` removes a workflow's triggers from an environment.
