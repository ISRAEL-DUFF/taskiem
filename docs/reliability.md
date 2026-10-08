# Reliability

Gate G4 needs 99.9% availability for 60 days before general availability ([build plan](spec/build-plan.md#phase-4--public-launch-about-16-weeks), spec 15.3). This page is how Taskiem measures that, alerts on it, tells customers, and handles incidents. Design: [decision 0023](decisions/0023-slos-and-alerting.md). What needs people (the on-call rota, the paging tool, the status page domain, who owns the 60-day measurement) is in [what needs people](needs-people.md#phase-4).

| Piece | Where |
| --- | --- |
| SLO definitions (source of truth) | `engine/slo/slo.go` |
| Prometheus recording rules and alerts | `deploy/prometheus/taskiem-rules.yaml`, or the chart's `prometheusRule` |
| Rule unit tests | `deploy/prometheus/taskiem-rules.test.yaml` (`promtool test rules`) |
| Grafana dashboard | `deploy/grafana/taskiem-slo-dashboard.json`, or the chart's `grafanaDashboard` |
| Synthetic canary | `engine/canary`, `taskiem canary` |
| Status page | `engine/status`, `/status`, `taskiem status` |
| Graceful shutdown | `cmd/taskiem/serve.go`, `engine/runtime` |
| Load and failure tests, the knee, bottlenecks fixed and left | [performance](performance.md), `tools/loadtest -mode cluster` and `-mode chaos` |

The rules, their tests and the dashboard are generated: change `engine/slo/slo.go`, then run `go generate ./engine/slo`. A test fails if the committed files are stale, if an alert has no runbook section below, or if a rule reads a metric the engine does not export. With `promtool` on the path (or `TASKIEM_PROMTOOL`), `go test ./engine/slo` also runs `promtool check rules` and the rule tests.

## Service-level objectives

Every objective is over a rolling 30 days. The SLI is good events over all events.

| SLO | Target | Good events | All events | Metric |
| --- | --- | --- | --- | --- |
| `api_availability` | 99.9% | API requests answered without a 5xx | API requests, except `/healthz`, `/readyz`, the status page and run streams (`/v1/runs/{run}/stream`) | `taskiem_http_requests_total` |
| `api_latency` | 99% | Those requests answered within 1 s | The same requests | `taskiem_http_request_duration_seconds` |
| `webhook_ingest` | 99.9% | Webhook and connector-event deliveries answered without a 5xx | Deliveries to `/hooks` | `taskiem_ingest_deliveries_total` |
| `run_start` | 99% | Runs whose first worker step is claimed within 5 s of the trigger being accepted | Runs whose first step runs on a worker | `taskiem_run_start_seconds` |
| `step_dispatch` | 95% | Connector and sandbox steps claimed within 50 ms of becoming ready (spec 15.3: p95 under 50 ms) | Claimed connector and sandbox steps | `taskiem_step_dispatch_delay_seconds` |
| `scheduler_timeliness` | 99% | Timers and schedules fired within 5 s of being due | Timers and schedules fired | `taskiem_scheduler_lateness_seconds` |

Spec 15.3 sets the first and the dispatch latency; the others are ours. "Zero accepted events lost" (spec 15.3) is a design property (an accepted delivery is a committed row), not a ratio, and is checked by the chaos and load tests, not here.

**How each is measured.**

- **Clocks.** Dispatch delay, run start and timer lateness are measured on the database clock (`clock_timestamp()` against the row's own time), so clock skew between pods and Postgres does not count. Schedule lateness uses the scheduler's clock against the stored due time.
- **4xx are not failures.** A bad signature, a payload over the limit or a tenant at its rate limit is the sender's problem. 5xx are ours.
- **Run start** is observed once per run, by the worker that first claims the first step the run scheduled. Runs that start with a transform or a wait are not observed.
- **Dispatch delay** counts from the moment a task is ready (`available_at`). A task held back because its tenant is at its `worker_concurrency` cap counts as delay: that is by design (spec 16.2), so a tenant saturating its plan can burn this budget. Check the backlog panels before acting on a dispatch alert.
- **BYOK.** A webhook answered 503 because a tenant revoked its own key (`key_unavailable`) counts against `webhook_ingest`. It shows in the logs and in `taskiem_tenant_key_checks_failed_total`.
- **What the pods cannot see.** These SLIs come from the pods. A request that never reaches a pod (the load balancer down, every pod gone) is not counted. Two things cover that: the [synthetic canary](#synthetic-canary), which goes through the public URL like a customer, and, where the cloud provides them, the load balancer's own request metrics. For the G4 measurement use both.

### Alerting: multi-window, multi-burn-rate

Each SLO has one alert name with four conditions (the Google SRE workbook's recommended set). A condition fires when both of its windows burn the budget faster than the rate. The long window shows the problem is real; the short window makes the alert stop soon after it is fixed.

| Severity | Long window | Short window | Burn rate | Budget spent when it fires |
| --- | --- | --- | --- | --- |
| page | 1 h | 5 min | 14.4 | 2% in an hour |
| page | 6 h | 30 min | 6 | 5% in six hours |
| ticket | 1 d | 2 h | 3 | 10% in a day |
| ticket | 3 d | 6 h | 1 | 10% in three days |

For the 95% dispatch objective the 1-hour page needs more than 72% of steps to be slow; the 6-hour page catches a sustained 30%.

Recording rules: `taskiem:slo_errors:ratio_rate<window>{slo}` for 5m, 30m, 1h, 2h, 6h, 1d and 3d; `taskiem:slo_target:ratio`; `taskiem:slo_errors:ratio_avg30d` (the average of hourly ratios: quiet hours weigh as much as busy ones); and `taskiem:slo_error_budget_remaining:ratio` (1 is untouched, 0 is spent, below 0 is over).

### Measuring G4

G4's availability criterion is `api_availability` and `webhook_ingest`, each at or above 99.9% over 60 consecutive days, plus the canary. Take the SLI over each 30-day half from `1 - taskiem:slo_errors:ratio_avg30d`, or for one 60-day figure:

```promql
1 - avg_over_time(taskiem:slo_errors:ratio_rate1h{slo="api_availability"}[60d])
```

Keep Prometheus retention (or a long-term store) above 60 days. Record any period excluded from the window (declared maintenance) in the status page; maintenance does not pause the measurement unless the decision owner says so beforehand. The owner of this measurement is a needs-people item (P4-R4).

## Error budget policy

The budget is the 0.1% (or 1%, 5%) an objective allows over 30 days. For `api_availability` that is 43 minutes of full outage a month.

| Budget left | What happens |
| --- | --- |
| Over 50% | Normal. Ship as usual. |
| 25% to 50% | Risky changes (migrations that rewrite tables, queue or lease changes, dependency upgrades) need a second reviewer from on-call and a rollback plan in the pull request. |
| Under 25% | Feature releases to production pause. Only fixes for reliability, security and data correctness go out. The weekly review picks the top reliability work. |
| Spent (0% or below) | Release freeze, except fixes that reduce risk. A postmortem is written for the incidents that spent it. The freeze lifts when the budget is back above 25% or the engineering lead and on-call agree in writing. |

Planned maintenance spends budget like any outage. During the G4 window a spent budget restarts the 60-day count unless the decision owner records otherwise.

## Alerts

| Alert | Severity | Fires when | Runbook |
| --- | --- | --- | --- |
| `TaskiemAPIAvailabilityBudgetBurn` | page or ticket | API 5xx burn the 99.9% budget | [runbook](#runbook-taskiemapiavailabilitybudgetburn) |
| `TaskiemAPILatencyBudgetBurn` | page or ticket | Too many API requests take over 1 s | [runbook](#runbook-taskiemapilatencybudgetburn) |
| `TaskiemWebhookIngestBudgetBurn` | page or ticket | `/hooks` answers 5xx | [runbook](#runbook-taskiemwebhookingestbudgetburn) |
| `TaskiemRunStartBudgetBurn` | page or ticket | Runs take over 5 s to reach a worker | [runbook](#runbook-taskiemrunstartbudgetburn) |
| `TaskiemStepDispatchBudgetBurn` | page or ticket | Steps wait over 50 ms to be claimed | [runbook](#runbook-taskiemstepdispatchbudgetburn) |
| `TaskiemSchedulerTimelinessBudgetBurn` | page or ticket | Timers and schedules fire over 5 s late | [runbook](#runbook-taskiemschedulertimelinessbudgetburn) |
| `TaskiemCanaryFailing` | page | 5 canary probes failed in 15 min and none passed | [runbook](#runbook-taskiemcanaryfailing) |
| `TaskiemCanaryStopped` | ticket | No canary probes for 15 min | [runbook](#runbook-taskiemcanarystopped) |
| `TaskiemSchedulerStalled` | page | No scheduler tick for 2 min, or no scheduler | [runbook](#runbook-taskiemschedulerstalled) |
| `TaskiemQueueBacklog` | page | A connector or sandbox task has waited over 10 min | [runbook](#runbook-taskiemqueuebacklog) |
| `TaskiemLeaseExpiries` | ticket | Over 10 leases expired in 15 min | [runbook](#runbook-taskiemleaseexpiries) |
| `TaskiemTargetDown` | ticket | A pod's metrics cannot be scraped for 10 min | [runbook](#runbook-taskiemtargetdown) |

Every alert carries `severity` and a `runbook_url` to its section below. Route `severity="page"` to the paging tool and `severity="ticket"` to the team's tracker or channel in Alertmanager:

```yaml
route:
  receiver: tickets
  routes:
    - matchers: [severity="page"]
      receiver: pager        # the paging tool (P4-R2)
receivers:
  - name: pager    # e.g. pagerduty_configs / opsgenie_configs / webhook_configs
  - name: tickets  # e.g. slack_configs or email_configs
```

These are platform alerts for operators. Tenants' own alerts (failed runs, approvals, limits) are separate ([alerts](alerts.md)).

## Severity levels

| Level | Meaning | Examples | Response |
| --- | --- | --- | --- |
| SEV1 | Most customers cannot use Taskiem, or data is at risk | API or ingest down; runs not executing anywhere; a tenant can see another's data; payments sent twice | Page now. Incident lead within 15 min. Status page within 15 min, updates every 30 min. Postmortem required |
| SEV2 | A major part is degraded, or many customers are affected | One queue stalled; schedules late; webhooks failing for a provider; error budget burning at page rate | Page. Status page within 30 min, updates every hour. Postmortem required |
| SEV3 | A minor part is degraded or a few customers are affected | One integration failing; slow dashboard; a ticket-rate burn | Working hours. Status page if customers can notice. Postmortem if the cause is ours and could recur |
| SEV4 | No customer impact yet | Canary stopped; one pod not scraped | Ticket |

When unsure, pick the higher level; lower it later.

## On-call

Who is on the rota, the paging tool and the hand-off day are needs-people items (P4-R1, P4-R2). The expectations:

- One primary and one secondary, weekly, handing over on a fixed weekday with a short note: open incidents, budget left, anything risky shipping that week.
- Acknowledge a page within 5 minutes and start work within 15. If the primary does not acknowledge in 10 minutes, the page goes to the secondary, then the engineering lead.
- On-call carries a laptop and network access, can reach the cluster, Prometheus, Grafana and the database, and can run `taskiem status`.
- On-call may stop a release, roll back, scale, or declare an incident without asking. Anything that deletes data needs a second person.
- Time spent paged out of hours is taken back. More than two out-of-hours pages in a week is itself a reliability problem for the weekly review.
- Each week: review alerts that fired (was each one actionable?), the budget, and open postmortem actions.

## Incident lifecycle

1. **Detect.** An alert, the canary, a customer report or a team member. Anyone may declare an incident.
2. **Declare.** The first responder becomes incident lead until they hand over. Open a channel named `inc-<date>-<short-name>`, set the severity, and post the first status update (SEV1 and SEV2 always):

   ```sh
   taskiem status open --title "Webhooks delayed" --components webhooks --impact partial_outage \
     --message "We are investigating delays receiving webhooks. Runs started by webhooks may start late."
   ```

   Or `POST /v1/status/admin/incidents` with an operator token ([status page](#status-page)).
3. **Mitigate first.** Restore service before finding the root cause: roll back the last release, scale workers, fail over the database, disable a misbehaving tenant's schedules. Write down each action and its time in the channel.
4. **Communicate.** Update the status page at the cadence for the severity, even if nothing changed: `taskiem status update ID --status identified --message "..."`, then `monitoring` once a fix is out. For SEV1, also email affected customers' owners.
5. **Resolve.** When the SLIs and the canary are back to normal for 15 minutes: `taskiem status resolve ID --message "..."`.
6. **Review.** A postmortem within five working days for SEV1 and SEV2, blameless, using the template below. Actions get owners and dates and are tracked to the end.

Roles in a larger incident: the incident lead (decides, keeps the timeline), operations (hands on keyboard), communications (status page and customers). One person may hold several in a small team.

### Communication templates

Keep updates short, factual and free of internal names, tenant names and guesses about the cause.

| Stage | Template |
| --- | --- |
| Investigating | We are investigating reports of {symptom} affecting {component}. {What customers may see}. Next update within {30/60} minutes. |
| Identified | We have identified the cause of {symptom} and are working on a fix. {Workaround, if any}. Next update within {N} minutes. |
| Monitoring | A fix is in place and {component} is recovering. Runs delayed during the incident {are being processed / will retry automatically}. We are monitoring. |
| Resolved | This incident is resolved. From {start} to {end} UTC, {impact}. {What customers need to do, or "No action is needed."} We will publish a summary within five working days. |
| Maintenance (scheduled) | On {date} from {start} to {end} UTC we will {work}. {Impact, e.g. "new runs may start up to a minute late; nothing is lost"}. |
| Maintenance (completed) | The maintenance is complete and all systems are operating normally. |

Accepted webhooks and runs are never lost (spec 15.3); say so when it is true and relevant, because it is the customer's first worry.

### Postmortem template

```markdown
# Postmortem: <title> (<date>)

Severity: SEV<n> · Incident lead: <name> · Status page: <link>
Duration: <start> to <end> UTC (<minutes>) · Error budget spent: <SLO>: <percent>

## Summary
Two or three sentences: what happened, who was affected, how it ended.

## Impact
Customers and runs affected, requests failed, runs delayed (by how long), any data effect. Numbers from the SLO dashboard.

## Timeline (UTC)
- hh:mm detected by <alert / customer / canary>
- hh:mm incident declared, SEV<n>
- hh:mm <each action and what it changed>
- hh:mm resolved

## Root cause
What failed and why, down to the condition that let it happen. No names; systems and decisions.

## What went well / what went badly / where we got lucky

## Actions
| Action | Type (prevent / detect / mitigate) | Owner | Due |
| --- | --- | --- | --- |
```

## Status page

Every `api` pod serves a public status page. It needs no sign-in and shows no tenant data: components, their state, operator-declared incidents and maintenance, and (optionally) the canary's results.

| Path | What |
| --- | --- |
| `GET /status` | The page as HTML: no script, no cookies, no external assets; refreshes every minute |
| `GET /status.json` | The same as JSON, readable from any origin (CORS `*`, no credentials) |
| `GET /status/feed.atom` | An Atom feed, one entry per update, newest first |

**Components:** `api` (API and web app), `webhooks` (ingest), `runs` (starting runs and executing steps), `scheduler` (schedules, timers). An incident may also name `integration:<name>` (for example `integration:paystack`); it appears while the incident is open. A component's state is the worst impact among the open incidents naming it: operational, maintenance, degraded, partial outage, major outage. A maintenance window shows from its start to its end without anyone posting, and earlier as upcoming.

**The canary's signal** (`TASKIEM_STATUS_CANARY`, on): with the canary running, three failed probes in a row (the newest within 10 minutes) mark `runs` degraded, or `webhooks` when the webhook was not accepted, labelled "automatic check". A declared incident on the component wins. One passing probe clears it.

**Caching.** Each pod computes the page at most every 15 seconds, and answers carry `Cache-Control: public, max-age=30, stale-while-revalidate=30, stale-if-error=86400` and an ETag, so a CDN in front can serve it during a flood and keep serving the last copy if Taskiem is down. If the database cannot be read, a pod serves the last page it has, marked stale.

**Declaring.** Operators use the CLI (with database access), the [operator console](operator-console.md) (signed in with a passkey, which it asks for again for every declaration and update), or the admin API, which stays for automation. Both record who posted each update in append-only tables (migration 00123: no update or delete, even by the superuser); `taskiem status list` and `GET /v1/status/admin/incidents` show them. The public page never does.

| CLI | Admin API |
| --- | --- |
| `taskiem status open --title T --components C[,C] [--impact degraded\|partial_outage\|major_outage] [--status investigating\|identified\|monitoring] --message M` | `POST /v1/status/admin/incidents` `{"title", "components", "impact", "status", "message"}` |
| `taskiem status maintenance --title T --components C --from TIME --to TIME --message M` | the same with `"kind": "maintenance", "starts_at", "ends_at"` |
| `taskiem status update ID --status S [--impact I] --message M` | `POST /v1/status/admin/incidents/{id}/updates` `{"status", "impact", "message"}` |
| `taskiem status resolve ID --message M` | an update with `resolved` (or `completed` for maintenance) |
| `taskiem status show`, `taskiem status list` | `GET /v1/status/admin/incidents` |

A closed incident takes no more updates; open a new one. The admin API is off until operators have tokens: `taskiem status token NAME` prints a token for that person and a `NAME:<sha256>` entry for `TASKIEM_STATUS_TOKENS` (comma-separated) on the `api` role. Only the hash is configured. Tokens are not tenant credentials and no tenant session or API key works there. Wrong tokens are rate limited per address.

| Variable | Default | Notes |
| --- | --- | --- |
| `TASKIEM_STATUS_PAGE` | on | Serve `/status`, `/status.json` and the feed on the `api` role |
| `TASKIEM_STATUS_CANARY` | on | Show canary results and let failures mark components degraded |
| `TASKIEM_STATUS_TOKENS` | — | `name:sha256hex,...`: operators allowed on `/v1/status/admin` |

**An external provider is better for the public page.** A status page served by the system it reports on goes down with it. Use a hosted status page on its own domain (P4-R3) for customers, and treat this one as the self-hosted fallback and the source: a hosted provider can poll `/status.json` or the Atom feed, and the CDN cache above keeps the last state visible during an outage. Self-hosted and dedicated installs can use this page as it is.

## Synthetic canary

The canary does what a customer does. Every minute it sends a signed webhook to a canary workflow in an internal tenant through the public hooks URL, follows the run through the public API until a worker has run its code step and the run has finished, and checks the output (`n * 2 + 1`, from a random `n`). It records accept and completion times (`taskiem_canary_duration_seconds`), results (`taskiem_canary_probes_total{result}`: `ok`, or where it failed: `accept`, `complete`, `output`) and the last success (`taskiem_canary_last_success_timestamp_seconds`). Run in the scheduler role it also stores each result (a week's worth) for the status page.

Set it up once:

1. Create an internal tenant for it: `taskiem bootstrap --tenant "Taskiem canary" --email canary@<your domain>`. With billing on, put it on a plan without payment: `taskiem billing grant TENANT_ID enterprise`.
2. Sign in as that owner, or create an owner API key, and run `taskiem canary setup --url https://<api> --key <owner key> [--hooks-url https://<hooks>/hooks]`. It creates and publishes the canary workflow (`wf_taskiemcanary`), stores a random webhook key, creates an API key that can only read runs (365 days), and prints:

   ```
   TASKIEM_CANARY_HOOK_URL=https://<hooks>/hooks/<tenant>/taskiem-canary?env=prod
   TASKIEM_CANARY_SECRET=...
   TASKIEM_CANARY_API_URL=https://<api>
   TASKIEM_CANARY_API_KEY=tsk_key_...
   ```

3. Put them in the scheduler role's Secret. The scheduler probes every `TASKIEM_CANARY_INTERVAL` (`1m`, 15s to 1h), each probe allowed `TASKIEM_CANARY_TIMEOUT` (`1m`).

Running setup again issues a new webhook key and API key (the old webhook key stops working at once): do it before the key expires, and after anyone may have seen the values. `taskiem canary probe [--count N]` runs probes now and exits non-zero on a failure (useful after a deploy). `taskiem canary run` probes forever with metrics on `TASKIEM_METRICS_LISTEN`: run it outside the cluster (another region or provider) to see what customers see when the cluster's own network is the problem.

The canary tenant is a normal tenant under row-level security: its webhook key is in its own vault, its API key reads runs only, and its runs are purged after a day.

## Graceful shutdown

Spec 15.4: rolling upgrades never stop running workflows, and workers finish or release leases before shutdown. On SIGTERM every role:

1. **Flips readiness.** `/readyz` answers 503 `draining` on the API (8080), the edge (8081) and the metrics port (9090); `taskiem_draining` becomes 1. Liveness (`/healthz`) stays 200.
2. **Keeps serving for `TASKIEM_SHUTDOWN_DELAY`** (default 0; the chart sets 10 s), so the Service and ingress take the pod out before it stops listening. Without this, requests routed in the meantime fail.
3. **Stops taking work.** HTTP servers stop accepting and finish open requests (up to 20 s). Long-lived run streams end. Workers claim nothing new; schedulers stop ticking.
4. **Drains.** Workers let in-flight steps finish for up to `TASKIEM_WORKER_DRAIN` (30 s). Steps still running then are cancelled.
5. **Releases leases.** Each worker and scheduler hands back the task, timer and orchestration leases it still holds (`taskiem_release_leases`, migration 00122; `taskiem_leases_released_total`), so another pod takes them at once instead of after the lease expires (60 s for tasks). The lease epoch is kept, so anything the stopping process still tries to write is fenced out. A write step cut off mid-call is reconciled by the next worker before anything is re-sent (decision 0009).

The pod's grace period must cover the delay, the drain and a few seconds to release leases: the chart's `shutdown.*` values enforce it (`gracePeriodSeconds` ≥ `delaySeconds` + `workerDrainSeconds` + 10). Tested by `TestShutdownFlipsReadinessFirst` (`cmd/taskiem`) and `TestWorkerDrainsThenReleasesLeases` (`engine/runtime`).

## Runbooks

Start every page the same way: open the SLO dashboard, check the canary, check what changed (deploys, migrations, config, provider status pages), and declare an incident if customers can notice.

### Runbook: TaskiemAPIAvailabilityBudgetBurn

API requests are failing with 5xx.

1. Which routes? `sum by (route, code) (rate(taskiem_http_requests_total{code=~"5.."}[5m]))`. One route points at a feature; all routes point at the database, the KMS or the pods.
2. Pods: are API pods ready and not restarting (`kubectl get pods -l app.kubernetes.io/component=api`)? OOM kills: raise memory or find the leak.
3. Database: `/readyz` failing means the pool cannot reach Postgres. Check the primary, connections (`max_connections` against the pools), locks and replication.
4. KMS: 503 `key_unavailable` everywhere means OpenBao (the platform KMS) is sealed or unreachable; for one tenant it is that tenant's own key ([BYOK](byok.md#when-the-key-is-unavailable)).
5. A deploy in the last hour: roll back first (`helm rollback`), investigate after.

### Runbook: TaskiemAPILatencyBudgetBurn

API requests are slow.

1. Which routes? `histogram_quantile(0.99, sum by (le, route) (rate(taskiem_http_request_duration_seconds_bucket[5m])))`.
2. Database: slow queries (`pg_stat_statements`), lock waits, CPU, a vacuum or migration running.
3. CPU throttling on API pods: raise limits or replicas.
4. AI building (`/v1/ai/*`) and code compilation are slow by nature; if only they are slow and within their own timeouts, open a ticket to exclude them from this SLI rather than paging.

### Runbook: TaskiemWebhookIngestBudgetBurn

Deliveries to `/hooks` get 5xx. Providers retry, but some give up.

1. `sum by (kind, result) (rate(taskiem_ingest_deliveries_total[5m]))`: 503 with nothing recorded means the database is unavailable to the edge; 500 means a bug.
2. Edge pods ready? Ingress routing `/hooks/` to the edge Service?
3. One tenant with `key_unavailable`: its own key (BYOK) is revoked; it is theirs to fix, and it still burns this budget.
4. After recovery, nothing accepted was lost; deliveries refused with 5xx are redelivered by most providers. Note which providers do not, for the customer update.

### Runbook: TaskiemRunStartBudgetBurn

Runs take more than 5 seconds to reach a worker.

1. Queues: `taskiem_queue_ready` and `taskiem_queue_oldest_ready_seconds`. A deep queue: scale workers (`roles.worker.replicas` or the autoscaler).
2. Held-back runs: `taskiem_queued_runs{reason="tenant"}`. Runs over a tenant's limits wait by design; one tenant can dominate. `taskiem tenants limits TENANT_ID` shows its usage.
3. Workers claiming nothing while tasks are ready: check worker logs for `claim failed` and the database.
4. Orchestrator: runs whose events are undecided wait for the sweep; check orchestrator pods and `taskiem_runs_swept_total`.

### Runbook: TaskiemStepDispatchBudgetBurn

Steps wait more than 50 ms to be claimed.

1. `histogram_quantile(0.95, sum by (le, queue) (rate(taskiem_step_dispatch_delay_seconds_bucket[5m])))`: which queue?
2. Saturation: `taskiem_queue_leased` at the workers' total concurrency means more workers are needed.
3. Tenant caps: tasks held by a tenant's `worker_concurrency` count as delay. If the backlog is one tenant at its plan cap, it is expected; open a ticket, do not scale for it.
4. Notifications: workers wake on `LISTEN`; if the delay is about 1 s, wake-ups are being lost (connection churn, a pooler in transaction mode in front of Postgres).

### Runbook: TaskiemSchedulerTimelinessBudgetBurn

Timers and schedules fire late.

1. `histogram_quantile(0.99, sum by (le, kind) (rate(taskiem_scheduler_lateness_seconds_bucket[5m])))`: timers or schedules?
2. Is a scheduler running and ticking (`taskiem_scheduler_last_tick_timestamp_seconds`)? Its logs for `scheduler tick failed`.
3. A large backlog of due timers after an outage drains at 100 per tick; it catches up on its own. Several schedulers are safe (claims use `SKIP LOCKED`) if it does not.
4. The database: slow claims, lock waits.

### Runbook: TaskiemCanaryFailing

The end-to-end probe has not completed a run in 15 minutes. Customers' runs are probably affected.

1. Where it fails: `sum by (result) (increase(taskiem_canary_probes_total[15m]))` and the scheduler's log lines `canary probe failed` (stage and detail).
   - `accept`: the webhook was not answered 202. `http_401` means the canary's webhook key changed (someone ran setup again without updating the Secret); 5xx or `unreachable` means ingest is down: see the ingest runbook.
   - `complete`: the run did not finish. `timeout` with runs stuck: workers (the sandbox queue) or the orchestrator; `run_failed`: the run's events say why (`taskiem runs tail RUN_ID` with the canary tenant's key).
   - `output`: the run finished with the wrong result. Treat as a correctness incident (SEV1) until shown otherwise.
2. Run `taskiem canary probe` from your machine with the same settings to tell an in-cluster problem from a public one.
3. If the canary itself is broken (its tenant suspended, its key expired) and customers are fine, fix it and lower the incident to SEV4.

### Runbook: TaskiemCanaryStopped

No canary probes for 15 minutes. Not a customer problem by itself, but the availability signal is gone.

1. Is a scheduler running? The canary runs in the scheduler role (`canary on` at start).
2. Were `TASKIEM_CANARY_*` removed from the Secret, or did the scheduler fail to start on a bad value (its log says which)?
3. If the canary was turned off on purpose, silence this alert for that period.

### Runbook: TaskiemSchedulerStalled

No scheduler has finished a tick for 2 minutes. Timers, schedules, lease recovery, the sweep and admission of queued runs have stopped.

1. `kubectl get pods -l app.kubernetes.io/component=scheduler`: running? Crash-looping? Its volume (archives, anchors) mounted?
2. Logs: `scheduler tick failed` every tick means the database or a migration problem; the error says which.
3. Restart the scheduler pod. A second replica is safe if one is stuck.
4. After recovery, overdue timers fire and lease recovery frees stuck tasks within a minute; nothing is lost.

### Runbook: TaskiemQueueBacklog

A connector or sandbox task has waited over 10 minutes.

1. Workers running for that queue (`TASKIEM_WORKER_QUEUES`)? A queue with no worker never drains.
2. `taskiem_queue_leased`: workers busy (scale them) or idle (they cannot claim: logs, database)?
3. One tenant's backlog at its `worker_concurrency` cap ages its own tasks; that is the plan working. Tell the tenant, or raise the cap with `taskiem tenants limits`.
4. Sandbox queue only: Python steps need memory; OOM-killed workers show as restarts and `TaskiemLeaseExpiries`.

### Runbook: TaskiemLeaseExpiries

Leases expire instead of being completed or released: processes die mid-step.

1. Restarts and OOM kills on worker pods; raise memory limits or lower `TASKIEM_WORKER_QUEUES` concurrency.
2. Pods killed before draining: the grace period is shorter than delay + drain (the chart refuses this; a manual manifest may not), or nodes are being drained without respecting the PodDisruptionBudget.
3. A step that hangs past its lease without heartbeats: its logs; the step's `timeout`.

### Runbook: TaskiemTargetDown

Prometheus cannot scrape a pod.

1. Is the pod running? If it is gone, the alert clears when Prometheus notices.
2. NetworkPolicy: with `networkPolicy.enabled`, metrics are only open to `networkPolicy.monitoringNamespace`.
3. A pod that serves traffic but cannot be scraped hides its errors from the SLIs: fix it quickly, and check the canary and load balancer metrics for that period.
