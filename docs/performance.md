# Performance

How Taskiem behaves under load, where it first misses an SLO, what was fixed, and what is left (Phase 4 P4-4). These are **development-machine numbers, not production numbers**: one shared 4-CPU container, with three other agents building and testing on it at the same time. Read every figure with the machine load printed next to it. What they are good for is finding where the engine queues and which queries it waits on; absolute capacity must be measured again on the target hardware ([needs people](needs-people.md#phase-4), P4-R5).

## Method

`tools/loadtest -mode cluster` runs the built binary's roles as separate processes, the way production does:

| Process | Role |
| --- | --- |
| `taskiem serve --role api` | API (reads alongside the load) |
| `taskiem serve --role edge` | Webhooks (`/hooks`) |
| `taskiem serve --role orchestrator` | The decide sweep (every 100 ms) |
| `taskiem serve --role scheduler` | Timers, lease recovery, admission |
| `taskiem serve --role worker` × 2 | `connector` and `sandbox` queues |

It creates a fresh database, tenants with the binary's own `bootstrap` command, and through the API a Paystack and a Termii connection per tenant and two published workflows:

- **load**: a webhook starts it; a TypeScript code step (sandbox), Paystack `check_balance` (a read), Paystack `transfer` (an idempotent write, keyed by the engine's reference), then a transform. Three steps run on workers.
- **sms**: one Termii `send_sms`, an unsafe write (no idempotency key, no lookup). Used by the failure tests.

The connectors are the real built-in ones, pointed at a fake provider on loopback (`TASKIEM_PAYSTACK_URL`, `TASKIEM_TERMII_URL`). The fake adds 20 ms to every call, records every transfer by reference and run, deduplicates a reused reference as Paystack does, and counts SMS per run, so a duplicated or lost effect shows.

Each **stage** offers webhooks open loop (arrivals on a clock, not waiting for answers) at one rate for 40 or 45 s, with 5 API reads a second (the run list and run detail, each with its own tenant's key), then waits for every accepted run to end. Every webhook body is different: equal bodies are deduplicated by the edge (the first ramp, `ramp1` below, made that mistake and is discarded).

**SLIs come from the processes' own `/metrics`**, scraped from all six before and after each stage and summed exactly as the recording rules in `engine/slo` do (`api_availability`, `api_latency`, `webhook_ingest`, `run_start`, `step_dispatch`); latency percentiles are interpolated within the histogram buckets. Client-side timings (webhook accept, API calls) and run durations from the database are reported beside them.

**Database side.** `pg_stat_statements` needs `shared_preload_libraries`, and the shared server cannot be restarted for it. Instead, for the test database only:

- `pg_stat_activity` is sampled every 100 ms: active backends, what they wait on (`wait_event`), and which statement; lock waits by statement.
- `track_functions = all` is set on the test database, so `pg_stat_user_functions` gives calls and time for the engine's SQL functions (claim, finish, append, heartbeat).
- `pg_stat_user_tables` deltas show sequential scans (a missing index) and rows changed.
- Commits for the test database, the CPU time of each Taskiem process and of the Postgres backends serving the test database, host CPU busy and the load average before and after.

**Profiles.** `TASKIEM_PPROF=true` serves Go's profiler on the metrics port (`cmd/taskiem/pprof.go`, off by default, never on a public port); `-profile-from RATE` takes CPU and heap profiles of every process at that rate and above.

## Environment and its noise

| | |
| --- | --- |
| Machine | Shared container, 4 vCPU, 15 GB RAM, Linux 6.18 |
| Neighbours | Three other agents building and testing Go and the web app on the same CPUs throughout |
| Postgres | 16.14, stock settings (`shared_buffers` 128 MB, `synchronous_commit` on, `max_connections` 100), shared with the other agents' test databases |
| Go | 1.26.0 |
| Load generator | Same machine |

The load average during the runs was 4 to 28 on 4 CPUs, and host CPU 40% to 100% busy, mostly from the neighbours: Taskiem's six processes used under 2.2 cores at most. The same binary at the same rate gave a dispatch SLI of 46% in one run and 97% in the next (`base`, rounds 1 and 2 below). Comparisons were therefore interleaved (A, B, C, A, B, C) and repeated, and fixes are judged mainly on measures that do not depend on CPU contention: calls per step and lock waits.

## Results: the original code

Ramp `base1`, 4 tenants, 2 workers, 45 s per stage, before any fix.

| Runs/s offered | Worker steps/s | Runs/s achieved | Webhook ingest | Run start ≤ 5 s | Dispatch ≤ 50 ms | API ≤ 1 s | Run p95 | Host CPU | Load avg (1 min) | SLOs breached |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 5 | 15 | 5.0 | 100% | 100% (p95 60 ms) | 97.6% (p95 41 ms) | 100% (p95 22 ms) | 257 ms | 96% | 9.4 → 11.8 | none |
| 10 | 30 | 10.0 | 100% | 100% (p95 49 ms) | 97.9% (p95 42 ms) | 100% (p95 17 ms) | 266 ms | 95% | 11.3 → 14.8 | none |
| 20 | 60 | 19.8 | 100% | 100% (p95 118 ms) | **82.8%** (p95 98 ms) | 100% (p95 59 ms) | 715 ms | 98% | 13.8 → 17.1 | step_dispatch |
| 30 | 90 | 29.7 | 100% | 100% (p95 275 ms) | 74.3% (p95 193 ms) | 100% (p95 47 ms) | 915 ms | 98% | 16.0 → 17.6 | step_dispatch |
| 45 | 135 | 19.3 | 100% | **38.1%** (p95 27 s) | 21.7% (p95 23 s) | 100% (p95 159 ms) | 24 s | 99% | 16.5 → 22.8 | run_start, step_dispatch |
| 60 | 180 | 7.8 | 100% | 0% | 18.7% | 100% (p95 285 ms) | > 2 min (1,412 runs unfinished at the 2-minute limit) | 100% | 24.6 → 26.9 | run_start, step_dispatch |
| 80 | 240 | 7.9 | 100% | 0% | 20.8% | 100% (p95 228 ms) | > 2 min (2,297 unfinished) | 100% | 26.4 → 28.7 | run_start, step_dispatch |

Every webhook was accepted (202) at every rate, no run failed, and no transfer was made twice. Client-side accept p95 grew from 46 ms to 1.5 s.

**The knee.** The first SLO to break is step dispatch (95% of steps claimed within 50 ms of becoming ready), at **20 runs/s (60 worker steps/s)** on this machine; run start (99% within 5 s) follows at 45 runs/s. Beyond about 40 runs/s the engine does not plateau, it collapses: achieved throughput falls from 30 to 8 runs/s while the Taskiem processes use less CPU (the workers fall from 0.8 to 0.45 cores each). They are waiting on Postgres, not working.

**Where the time went, at and above the knee** (share of active backend samples):

| Rate | Waiting on locks | Top statements | Top lock waits |
| --- | --- | --- | --- |
| 20 | 12% | claim 37%, `commit` 37%, orchestrator claim 10%, append event 5% | `commit` (Lock:object), the run row (`FOR UPDATE`) |
| 30 | 16% | `commit` 44%, claim 32% | `commit`, the run row |
| 45 | 39% (Lock:object 24%, transactionid 15%) | `commit` 53%, claim 19%, **`INSERT INTO tenant_usage`** 7% | `commit` 339, `tenant_usage` 103, the run row 93 |
| 60 | 40% | `commit` 45%, `tenant_usage` 17%, claim 17% | `tenant_usage` 704, `commit` 645, the run row 262 |

No table was read by large sequential scans (the hot tables are small and indexed), and no N+1 pattern showed: the engine's statements are few per step (about 10 commits per worker step, including claims, heartbeats, the orchestrator sweep and ingest).

Three things stand out:

1. **Claims.** `taskiem_claim_tasks` was the most expensive statement (2 to 3 ms a call on this machine) and called about 4 times per worker step at low rates. Each worker process runs one claimer per queue, and every new task's `NOTIFY` woke all of them, whatever the task's queue: a sandbox claimer woke and claimed for nothing on every connector task, and the other way round.
2. **The usage row.** Every accepted run adds one to its tenant's row in `tenant_usage` for the day, and the row stays locked until the run's transaction commits. One tenant's webhooks were accepted one at a time behind that row, and the commit itself was slow (next point).
3. **Commits.** `commit` waits on `Lock:object` and on WAL writes. A transaction that inserts a task sends `NOTIFY`, and Postgres serialises committing transactions that have notified (a global lock held from before the commit until after its WAL flush), so they cannot share a WAL flush. On a machine whose disk flush is slow and whose CPUs are saturated, this caps commits and is the main reason for the collapse above 40 runs/s.

## What was fixed

| # | Bottleneck | Fix | Behaviour |
| --- | --- | --- | --- |
| 1 | Claimers woken for other queues' tasks | The worker's listener ignores notifications whose payload (the new task's queue, as `taskiem_notify_task` sends it) is another queue's (`engine/runtime/listen.go`, `worker.go`) | Unchanged: a claimer still wakes for its own queue, on reconnect, and polls every second |
| 2 | One locked usage row per tenant per day | The day's count is kept in 16 shards; each start adds to a random one (migration **00161**, `countStart` in `engine/runtime/store.go`); every reader sums the rows (quotas, the limits page, partner caps already did; billing snapshots now do) | Unchanged counts and quotas; the down migration folds the shards back |

Tests: `TestListenWakesOnlyForWantedPayloads` (another queue's notification does not wake, its own does), `TestUsageShardsCountEveryStart` (40 concurrent starts all counted across shards, the limits page sees the sum, the quota still refuses at the limit), and the existing quota, billing, HA and replica tests.

**Before and after, fix 1.** Five runs a second for 40 s (wake-ups dominate at low rates), interleaved, twice each:

| Binary | Claims per worker step | Time in claims | Dispatch ≤ 50 ms |
| --- | --- | --- | --- |
| original, round 1 | 3.92 | 4,084 ms | 98.8% |
| original, round 2 | 3.94 | 3,721 ms | 99.8% |
| fix 1, round 1 | **2.00** | **2,249 ms** | 99.3% |
| fix 1, round 2 | **1.97** | **2,066 ms** | 99.8% |

Claims per step halved and the database time spent claiming fell by 45%; at this rate dispatch is within the SLO either way.

**Before and after, fixes 1 and 2 together.** Ramp at 20, 30 and 40 runs/s, 40 s per stage, three binaries interleaved and repeated:

| Binary | Round | Dispatch ≤ 50 ms at 20 / 30 / 40 | Run start ≤ 5 s at 40 | Runs/s achieved at 40 | Lock waits (share of active samples) at 20 / 30 | `tenant_usage` lock-wait samples, all stages |
| --- | --- | --- | --- | --- | --- | --- |
| original | 1 | 46.0% / 30.7% / 30.2% | 27.6% | 24.4 | 24% / 26% | 31 |
| original | 2 | 97.4% / 92.1% / 40.2% | 75.3% | 34.2 | 4% / 11% | 0 |
| fix 1 | 1 | 50.3% / 34.0% / 35.9% | 22.7% | 29.4 | — | 28 |
| fix 1 | 2 | 97.7% / 99.7% / 35.9% | 28.1% | 32.3 | — | 37 |
| fixes 1 and 2 | 1 | 97.9% / 94.6% / 39.4% | 75.1% | 33.8 | 3.5% / 3.7% | 1 |
| fixes 1 and 2 | 2 | 99.5% / 90.7% / 50.4% | **100%** (p95 3.9 s) | **37.0** | 2% / 15% | 1 |

The fixed build had the best throughput at 40 runs/s in both rounds and almost no waits on the usage row, but the round-to-round spread (46% to 97% for the same binary) is larger than the differences, so these end-to-end figures show direction, not size. A focused test with one tenant at 40 webhooks a second (every start on the same tenant) did not separate the builds either: lock-wait samples on the usage statement were 48 and 3 before, 2 and 90 after, with the second "after" run taken while the load average jumped from 8 to 21. A clean measurement of fix 2 on its own (a `pgbench` script of the ingest transaction, one row against 16 shards, with and without `NOTIFY`) was being set up when the shared database server went down with the machine (see below); it should be the first thing run on the target hardware.

**The knee after the fixes:** dispatch stays within its SLO at 20 runs/s in both rounds and breaks at 30 to 40 runs/s; run start stays within its SLO up to 40 runs/s in one round of two. On this machine that is a modest move (from 20 to between 20 and 30 runs/s for dispatch), within the noise.

## Failure tests

`tools/loadtest -mode chaos -scenario …` drives the same cluster:

| Scenario | What it does | What it checks |
| --- | --- | --- |
| `kill-worker` | 60 transfer runs and 30 SMS runs; the fake provider holds each call 6 s after recording its effect; `kill -9` the first worker while calls are in flight; a supervisor-style restart 2 s later | Every transfer run completes having moved money exactly once (one reference per run; references the provider saw twice are counted); every SMS run completes or parks in `needs_reconciliation`, none sends twice; how long until the last run settles; `taskiem_lease_expiries_total` |
| `restart-orchestrator` | 10 runs/s for 60 s; `kill -9` the orchestrator at 20 s and restart it 3 s later; `SIGTERM` and restart at 38 s | Runs ended per second throughout, the longest stretch with none, statuses, duplicates, runs decided by the sweep, the SLIs over the window |
| `replica-lag` | Points the API's `TASKIEM_DATABASE_READ_URL` at a second database whose heartbeat stands an hour behind (a replica that stopped replaying), bound 5 s | The run list still shows every run (served by the primary), `taskiem_db_replica_in_use` 0 and reads counted on the primary; then the stand-in "catches up" (its heartbeat kept current) and the API uses it again (it has no runs, so the list comes back empty: proof of where the read ran); then it falls behind and reads return to the primary |

**Not run in this round.** The scenarios are built and compile against the cluster harness, but the machine restarted before they ran, the shared Postgres did not come back, and starting it again was refused by the agent harness. Creating a scratch primary and standby (to promote the standby and point the processes at a multi-host `target_session_attrs=read-write` URL, decision 0024) was also refused (`initdb` as the `postgres` user). So this round has **no measured failover stall, no measured lease recovery and no measured orchestrator restart**. What exists from earlier work: the engine's own tests for connection drops and failover retries (`TestWorkerSurvivesConnectionDrops`), replica fallback (`TestReplicaFallsBack`), the Phase 1 database-crash suite (1,000 payment runs, 4 crashes, no duplicates or losses: [Phase 1 status](phase-1-status.md)), and graceful worker shutdown (`engine/runtime/shutdown_test.go`). Across all the ramps the fake provider recorded about 37,000 transfers and no run moved money twice.

## What remains

- **Commit serialisation by `NOTIFY`.** Above about 40 runs/s on this machine, transactions that insert tasks queue on Postgres's notify lock and the WAL flush, and throughput collapses instead of levelling off. Options, each needing a design decision: send `NOTIFY` from fewer transactions (for example only when the claimers of that queue may be idle), batch wake-ups through a single notifier, or rely on polling at a shorter interval when busy. Measure first on the target hardware, where a disk flush is far faster.
- **No back-pressure at the edge.** Webhooks were accepted (202) even while runs waited minutes to start. Durable acceptance is by design ("zero accepted events lost"), but the edge could shed or slow down (429 with `Retry-After`) when the backlog of ready tasks is large. A decision for the product, not a performance fix.
- **The claim statement.** Still the most expensive single statement (2 to 3 ms here, about 2 calls per step after fix 1). Each call walks the queue's tenants and looks up each tenant's worker pool. Worth a closer look with real tenant counts.
- **The run row.** `FOR UPDATE` on the run row in decide waits on the worker's and the orchestrator's transactions for the same run (transactionid waits of 6 to 25% at and above the knee); expected, but it lengthens commits.
- **Profiles.** The CPU and heap profiles of the baseline ramp were not captured (the profiler was turned on with `TASKIEM_PPROF=on`, which the server reads as false; the tool now sets `true`). The database-side evidence above stands without them; take profiles on the next run.
- **Run lists for large tenants.** `GET /v1/runs` orders a tenant's runs by `started_at` with no index on `(tenant_id, started_at)`; it was fast at these sizes (p95 under 300 ms at every rate), but was not measured with hundreds of thousands of runs per tenant.
- **Failure tests and failover**: run the three scenarios and a real standby promotion on a host where Postgres can be started and replicated (P4-R5).
- **Target hardware**: everything here again on production-like hardware with no neighbours, before quoting capacity.

## How to rerun

```sh
GOFLAGS=-p=2 go build -o /tmp/taskiem ./cmd/taskiem
export TASKIEM_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable'

# Rate stages; profiles from 30 runs/s up; logs, profiles and summary.md in -out.
go run ./tools/loadtest -mode cluster -bin /tmp/taskiem -tenants 4 -workers 2 \
  -rates 5,10,20,30,45,60 -stage 45s -profile-from 30 -out /tmp/load

# Failure scenarios.
go run ./tools/loadtest -mode chaos -bin /tmp/taskiem -tenants 4 -scenario kill-worker -out /tmp/chaos
go run ./tools/loadtest -mode chaos -bin /tmp/taskiem -tenants 4 -scenario restart-orchestrator -out /tmp/chaos
go run ./tools/loadtest -mode chaos -bin /tmp/taskiem -tenants 4 -scenario replica-lag -out /tmp/chaos

# CPU profile of one process, then the top functions.
go tool pprof -top /tmp/taskiem /tmp/load/worker1-cpu-30.pprof
```

Flags: `-provider-latency` (default 20 ms), `-api-rate` (default 5/s), `-drain` (how long to wait for a stage's runs, default 2 min), `-stop-after-breach`, `-env KEY=VALUE,...` for extra server settings (for example `TASKIEM_DATABASE_POOL`). The tool lifts the per-tenant ingest limits so they do not shape the load. Record the machine's load next to every result; on a shared machine, interleave the builds you compare and repeat them. Leave `TASKIEM_PPROF` off in production unless you are taking a profile, and never expose the metrics port publicly.
