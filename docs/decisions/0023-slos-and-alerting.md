# 0023 — SLOs as code: burn-rate alerts from one list, a black-box canary, a self-hosted status page, readiness before drain

Date: 2026-10-07 · Status: Accepted (on-call rota, paging tool, status page domain and the G4 measurement owner pending P4-R1 to P4-R4)

## Context

Gate G4 needs 99.9% availability for 60 days before general availability, and spec 15.3 sets two numbers: 99.9% monthly availability for the API and webhook ingest, and step dispatch latency under 50 ms at p95. Before Phase 4 the engine exported request, ingest and queue metrics but nothing defined what "available" meant, no alert fired on it, nothing checked the whole path the way a customer uses it, customers had nowhere to look during an incident, and a pod stopped listening the moment it got SIGTERM, before load balancers had taken it out. Two numbers were not measurable at all: dispatch latency (only the age of the oldest ready task was exported) and how late timers fire.

## Decision

1. **One list of SLOs in Go generates everything.** `engine/slo` holds six objectives (API availability 99.9%, API latency 99% under 1 s, webhook ingest 99.9%, run start 99% under 5 s, step dispatch 95% under 50 ms, scheduler timeliness 99% under 5 s), each a ratio of good to all events over existing counters and histograms. `go generate ./engine/slo` writes the Prometheus recording rules, the alerts, `promtool` unit tests and the Grafana dashboard; the same files ship in the Helm chart. Tests keep the committed files current, require a runbook section for every alert, and require every metric a rule reads to be one the engine exports.
2. **Alerts are multi-window, multi-burn-rate.** Each SLO gets one alert name with the SRE workbook's four conditions: 14.4× over 1 h and 5 min, 6× over 6 h and 30 min (page); 3× over 1 d and 2 h, 1× over 3 d and 6 h (ticket). A handful of symptom alerts cover what ratios cannot see: the canary failing, the scheduler stalled, a queue nobody serves, leases expiring, a pod not scraped.
3. **Latencies are measured on the database clock.** Claiming a task returns how long it waited ready (`clock_timestamp() - available_at`, migration 00122); timer lateness and run start come from row and event times read in the same transaction. Pod clock skew never counts against a 50 ms objective.
4. **The canary is a black box with minimal keys.** It signs a real webhook to a canary workflow in an internal tenant through the public hooks URL and reads the run back through the public API with a key that can only read runs. It runs in the scheduler role, or anywhere as `taskiem canary run`. It is set up through the public API like any customer workflow, with no special engine path.
5. **The status page is self-hosted, public and append-only.** The `api` role serves `/status`, `/status.json` and an Atom feed from operator-declared incidents and maintenance windows. Declarations go through definer functions into tables that refuse updates and deletes even from the superuser (migration 00123), and each update records who posted it: that is the audit trail. Operators use the CLI (database access) or an admin API with operator tokens configured as SHA-256 hashes. Three consecutive canary failures mark a component degraded automatically, labelled as such. Answers are cacheable (15 s in process, `max-age=30` and `stale-if-error` for a CDN).
6. **Readiness flips before anything stops.** On SIGTERM `/readyz` answers 503 at once, the process keeps serving for `TASKIEM_SHUTDOWN_DELAY`, then servers stop, workers drain for `TASKIEM_WORKER_DRAIN`, and workers and schedulers release every lease they still hold (`taskiem_release_leases`). The chart sets the delay and refuses a grace period too short for delay, drain and release.

## Alternatives considered

- **Hand-written rule files, or a generator like Sloth or Pyrra.** Hand-written rules drift from the metrics and the runbooks; an external generator is another tool in the build and still needs our tests. Generating from Go keeps the definitions next to the metrics and lets ordinary tests check them.
- **Availability from the load balancer only.** It sees requests that never reach a pod, but its metrics differ per cloud and it cannot see dispatch or scheduling. We measure in the pods, add the canary for the outside view, and document adding the load balancer's metrics for G4.
- **A hosted status page only.** Better for customers (it survives our outage) and still recommended (P4-R3), but self-hosted and dedicated installs need one too, and a hosted page needs a source. The self-hosted page is the source and the fallback; a CDN can keep its last state up.
- **Operator sign-in for the admin API.** There is no operator identity in the API today (P4-6 reviewers use the CLI). Hashed per-person tokens are enough for a handful of operators and leave room for operator SSO later.
- **A tenant-scoped audit log for status changes.** Status changes belong to no tenant. An append-only table with the actor on every row is simpler and as tamper-evident for this purpose as the hash chain would be without anchoring.
- **Leaving leases to expire on shutdown.** Correct (fencing makes it safe) but every deploy then delays cut-off steps by up to a minute, which a 5-second run-start objective cannot absorb.

## Consequences

- The claim function returns one more column; callers naming columns are unaffected, `SELECT *` callers (two tests) were changed.
- Dispatch delay includes time a task waits because its tenant is at its `worker_concurrency` cap. A saturated tenant can burn the dispatch budget; the runbook says to check before scaling. Excluding capped tenants would need the claim to know why each task waited.
- The status page shares the API's fate unless fronted by a CDN or mirrored by a hosted provider; documented.
- Webhook 503s caused by a tenant revoking its own key (BYOK) burn the ingest budget. Rare, visible in logs, accepted for now.
- `TASKIEM_SHUTDOWN_DELAY` defaults to 0 outside Kubernetes so local stops stay instant; the chart sets 10 s.
