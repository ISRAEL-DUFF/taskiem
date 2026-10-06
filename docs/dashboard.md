# Dashboard

**Dashboard** in the web app (`GET /v1/dashboard`, `run.read`) shows how a tenant's workflows are doing, per environment, over the last day, 7, 30 or 90 days. Everything is derived from run history, so it agrees with the run inspector.

| Figure | Meaning |
| --- | --- |
| Success rate | Completed runs ÷ (completed + failed), for runs started in the period. Cancelled and unfinished runs are left out |
| Runs | Started in the period; "in progress" are queued, running or waiting |
| Duration p50 / p95 | Of completed runs, start to end |
| Needs reconciliation | Runs waiting, right now, for someone to confirm a call whose outcome was unknown |
| Approvals pending | Open approval steps right now, and when the oldest was requested |
| Runs per day | Completed, failed and other, by the day each run started, in the viewer's time zone |
| Failing steps / connectors | Failed attempts, retried ones included: a step or provider that keeps needing retries shows here before it fails runs |
| Workflows | Runs, success rate, failures and p95 for each workflow |

Query parameters: `environment`, `workflow`, `days` (1–90, default 7), `tz` (an IANA zone, default `Africa/Lagos`). An API key limited to one environment sees that environment only. To be told rather than having to look, set up [alerts](alerts.md).
