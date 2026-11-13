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

### Wake-ups per statement (migration 00258)

The task trigger now runs once per statement, not per row: it reads the statement's new rows (a transition table) and sends one `NOTIFY taskiem_tasks` per distinct queue, with the same payload (the queue's name). A task not due within a second (a retry's backoff) sends none; an idle worker polls every second, which is how it finds such a task anyway. Tests: `TestTaskInsertsNotifyOncePerQueue` (20 tasks in one statement run the trigger once and send one wake-up; a mixed insert sends one per queue) and `TestFutureTasksDoNotNotify`, beside `TestListenWakesOnlyForWantedPayloads` and the runtime tests.

What this does **not** do: the engine inserts each task with its own statement, and Postgres already delivers identical payloads once per transaction, so a transaction that schedules a task still notifies once and still takes the notify lock at commit. Ramp at 20, 40 and 60 runs/s, 30 s per stage, 4 tenants, 2 workers, the two builds interleaved twice (host CPU 76% to 98% busy, load average 3 to 16 on 4 CPUs, other agents' builds alongside):

| Build | Round | Runs/s achieved at 20 / 40 / 60 | Dispatch ≤ 50 ms at 20 / 40 / 60 | Trigger calls per worker step | `Lock:object` share at 20 / 40 / 60 |
| --- | --- | --- | --- | --- | --- |
| per row | 1 | 19.9 / 33.2 / 34.2 | 85.1% / 43.1% / 42.2% | 0.99 | 3% / 8% / 6% |
| per statement | 1 | 19.9 / 37.8 / 35.0 | 98.0% / 44.9% / 41.6% | 0.98 | 2% / 3% / 20% |
| per row | 2 | 19.9 / 37.1 / 32.5 | 97.4% / 42.2% / 38.5% | 0.97 | — / 3% / 3% |
| per statement | 2 | 19.9 / 29.5 / 30.4 | 96.4% / 35.8% / 37.1% | 0.98 | 2% / 3% / 9% |

No difference beyond the noise, as expected: about one trigger call per worker step either way, and the ceiling stays at 30 to 38 runs/s. At 40 runs/s and above the largest waits in these runs were `Lock:transactionid` (23% to 33%, the run row and `connector_rate_buckets`) and WAL writes, more than the notify lock. The change removes per-row work for any batch insert and the wake-ups for retries; it is not the fix for the commit ceiling.

### Wake-ups after the commit (decision 0067, migration 00427)

A transaction that schedules a task no longer notifies. The process sends the wake-up after the commit, at most once every 10 ms for a queue, and wakes its own workers in memory ([plan](spec/task-wake-ups.md), `engine/runtime/wake.go`). `TASKIEM_WAKE_IN_TX=true` returns a process to the old behaviour, which is how the two were compared: one binary, the setting changed.

**The lock on its own** (PostgreSQL 16, a bare insert, `pgbench`, 12 November 2026; [the plan's §2](spec/task-wake-ups.md#2-the-problem-measured)): with `NOTIFY` in the transaction, 8 clients and 64 clients both committed about 3,000 times a second; without it, 64 clients committed about 20,000 times; with the wake-ups sent by one other connection 100 times a second, about 19,000.

**The ramp.** One binary with `TASKIEM_WAKE_IN_TX` changed, the two settings interleaved, 4 tenants, 2 workers, three worker steps a run, 30 s a stage, 12 November 2026. A different machine from the rounds above: 4 CPUs (Xeon, 2.1 GHz), a disk that flushes in about 0.3 ms, the load tool, six server processes and Postgres all on it, load average 1 to 13. Two rounds at 20, 40, 60 and 80 runs/s, then three more at 50 and 60.

| Wake-ups | Runs/s achieved at 60 / 80 | Dispatch ≤ 50 ms at 50 | Dispatch ≤ 50 ms at 60 | Run p95 at 60 |
| --- | --- | --- | --- | --- |
| in the transaction | 59.3, 59.3 / 55.7, 56.7 | 92.5%, 93.0%, 90.8% | 66.7%, 75.1%, 85.0%, 94.6%, 84.0% | 440 to 738 ms |
| after the commit | 59.5, 58.7 / 57.5, 58.4 | 93.9%, 92.3%, 94.3% | 82.6%, 54.5%, 89.4%, 93.3%, 84.9% | 453 to 902 ms |

What this shows, and what it does not:

- **No difference on this machine, beyond the noise.** At 60 runs/s dispatch within 50 ms ranged from 67% to 95% with wake-ups in the transaction and from 55% to 93% with them after the commit, round to round, for the same binary. At 50 runs/s both sat between 91% and 94%. At 20 and 40 runs/s every objective was met either way.
- **The ceiling did not move, and this machine cannot show whether it moves.** Both settings levelled off at 56 to 58 runs/s with the CPUs 96% to 98% busy: this machine runs out of CPU, not of commits. Its disk flushes in about 0.3 ms, so the notify lock allowed about 3,000 notifying commits a second, far above the 240 steps a second offered. The collapse recorded above was on a machine with a slow, contended disk, where one notifying commit per flush is a low ceiling. That case is the reason for the change and is **not reproduced here**; it has to be measured on a host with a slow flush or on the target hardware (W5 of the plan).
- **What is shown is narrower:** the lock on its own (above), that the engine's commits no longer take it (`TestEngineCommitsDoNotNotify`, `TestWorkerCommitsDoNotNotify`), and that sending the wake-up after the commit costs nothing measurable here.
- Every accepted run ended in every pass, about 46,000 runs in all. Wait-event samples were not taken.

An earlier pass of this ramp, the same day, appeared to favour the change at 60 runs/s (59% and 67% against 85% and 94%). It was taken on a build in which only the transaction that starts a run had stopped notifying (the worker's transactions go through `InTenantTxRetry`, which the first build missed), and the five rounds above did not repeat it. It was noise.

### A slow disk (13 November 2026)

The collapse above 40 runs/s was recorded on a machine with a slow, contended disk, and the review machine's disk is too fast to show it. So the disk was slowed: `tools/loadtest/slowsync/slowsync.c` is a small library, loaded into Postgres with `LD_PRELOAD`, that adds a fixed delay to every `fdatasync` and `fsync`. With 10 ms added to each flush, on PostgreSQL 16:

| Transaction (`pgbench`, 32 clients) | No delay | +5 ms | +10 ms |
| --- | --- | --- | --- |
| Insert a row | 15,500 commits/s | 2,730 | 1,460 |
| Insert a row and `pg_notify` | 3,220 | 166 | 90 |

One notifying commit per flush, as the plan said: about 100 a second at 10 ms.

**The ramp at +10 ms**, one binary with `TASKIEM_WAKE_IN_TX` changed, 4 tenants, 2 workers, three worker steps a run, 30 s a stage, two rounds interleaved (both rounds shown):

| Runs/s offered | In the transaction: achieved | accepted | dispatch ≤ 50 ms | After the commit: achieved | accepted | dispatch ≤ 50 ms |
| --- | --- | --- | --- | --- | --- | --- |
| 10 | 9.9, 9.9 | all | 98.5%, 100% | 9.9, 9.9 | all | 100%, 100% |
| 20 | 16.9, 17.0 | all | 1.1%, 0.8% | 19.8, 19.8 | all | 100%, 100% |
| 30 | 17.1, 17.4 | all | 0.4%, 0.4% | 29.7, 29.7 | all | 99.7%, 99.1% |
| 40 | 13.8, 13.3 | 702 and 689 of 1,201 | 0.3%, 0.3% | 39.5, 39.5 | all | 98.4%, 97.9% |
| 60 | 10.6, 10.1 | 377 and 359 of 1,801 | 0.4%, 0.5% | 56.0, 54.8 | all | 50.8%, 51.1% |

- **With wake-ups in the transaction the engine tops out near 17 runs/s and then falls as more is offered**, refusing webhooks, with the CPUs 40% to 69% busy. This is the collapse, reproduced: the limit is commits, not CPU.
- **With wake-ups after the commit it holds 40 runs/s inside every objective** and reaches about 55 before the four CPUs are spent (96%), which is this machine's ceiling with a fast disk too.
- No run moved money twice in any pass.

This is the measurement decision [0067](decisions/0067-wake-ups-leave-the-transaction.md) was waiting for, on a disk made slow rather than a slow disk. The target hardware is still owed its own figures (P4-R5).

## Failure tests

`tools/loadtest -mode chaos -scenario …` drives the same cluster:

| Scenario | What it does | What it checks |
| --- | --- | --- |
| `kill-worker` | 60 transfer runs and 30 SMS runs; the fake provider holds each call 6 s after recording its effect; `kill -9` the first worker while calls are in flight; a supervisor-style restart 2 s later | Every transfer run completes having moved money exactly once (one reference per run; references the provider saw twice are counted); every SMS run completes or parks in `needs_reconciliation`, none sends twice; how long until the last run settles; `taskiem_lease_expiries_total` |
| `restart-orchestrator` | 10 runs/s for 60 s; `kill -9` the orchestrator at 20 s and restart it 3 s later; `SIGTERM` and restart at 38 s | Runs ended per second throughout, the longest stretch with none, statuses, duplicates, runs decided by the sweep, the SLIs over the window |
| `replica-lag` | Points the API's `TASKIEM_DATABASE_READ_URL` at a second database whose heartbeat stands an hour behind (a replica that stopped replaying), bound 5 s | The run list still shows every run (served by the primary), `taskiem_db_replica_in_use` 0 and reads counted on the primary; then the stand-in "catches up" (its heartbeat kept current) and the API uses it again (it has no runs, so the list comes back empty: proof of where the read ran); then it falls behind and reads return to the primary |

**Run on 13 November 2026**, on the review machine (4 CPUs, PostgreSQL 16, the load tool, six processes and Postgres together), for the first time:

| Scenario | Result |
| --- | --- |
| `kill-worker` | The worker was killed holding 16 leases with 32 provider calls in flight, and restarted 2.4 s later. All 60 transfer runs completed and each moved money exactly once; 10 references were sent again and deduplicated by the provider. All 30 SMS were sent once: 24 runs completed and 6 parked in `needs_reconciliation` after sending, which is the rule for a write that cannot be checked. 16 lease expiries recovered; the last run settled 59 s after the kill (the lease is 60 s) |
| `restart-orchestrator` | Killed at 20 s and back at 23.3 s; stopped and back at 38.3 s. All 600 runs completed, none moved money twice, 214 were decided by the sweep, and no second passed without a run ending |
| `replica-lag` | With the stand-in an hour behind, the run list came from the primary; caught up, from the stand-in; behind again, from the primary |
| A standby promoted, at once | A primary with a synchronous standby, 15 runs/s. The primary was stopped with `-m immediate` after 307 runs and the standby promoted 0.2 s later, the processes on a two-host `target_session_attrs=read-write` URL (decision 0024). 1,348 of 1,351 webhooks accepted (three answered 503), every accepted run completed, no transfer sent twice |
| A standby promoted after 15 s | The same, with 15 s between losing the primary and the promotion. 1,123 of 1,351 accepted: the 228 that arrived while there was no primary were answered 503. Every accepted run completed, none sent twice; run duration p95 119 ms, p99 533 ms |

What these do not cover: a promotion on real hosts with a real HA manager (the two servers here shared a machine and the promotion was by hand), a standby that is not synchronous (where an acknowledged commit can be lost), and a network partition as opposed to a dead primary. A webhook that arrives while there is no primary is refused, not held: the sender must send it again, as payment providers do.

## What remains

- **Commit serialisation by `NOTIFY`.** Resolved in code (decision [0067](decisions/0067-wake-ups-leave-the-transaction.md), above): transactions that schedule tasks no longer notify, so their commits can share a WAL flush. Measured on 13 November with the disk slowed to a 10 ms flush (above): about 17 runs/s and falling with the wake-up in the transaction, 40 runs/s inside every objective with it after the commit. The target hardware's own figures are still owed.
- **No back-pressure at the edge.** Webhooks were accepted (202) even while runs waited minutes to start. Durable acceptance is by design ("zero accepted events lost"), but the edge could shed or slow down (429 with `Retry-After`) when the backlog of ready tasks is large. A decision for the product, not a performance fix.
- **The claim statement.** Still the most expensive single statement (2 to 3 ms here, about 2 calls per step after fix 1). Each call walks the queue's tenants, one index probe each. Since migration 00259 only tenants with a task due now have their worker pool, cap and leased tasks looked up (with 2,000 tenants backing off and 20 with work due, a claim took 20 to 25 ms instead of 107 to 134 ms here); the probe per tenant remains. Worth a closer look with real tenant counts.
- **The run row.** `FOR UPDATE` on the run row in decide waits on the worker's and the orchestrator's transactions for the same run (transactionid waits of 6 to 25% at and above the knee); expected, but it lengthens commits.
- **Profiles.** The CPU and heap profiles of the baseline ramp were not captured (the profiler was turned on with `TASKIEM_PPROF=on`, which the server then read as false; the tool now sets `true`, and the server now accepts `on` and refuses values it does not understand). The database-side evidence above stands without them; take profiles on the next run.
- **Run lists for large tenants.** Resolved: `GET /v1/runs` names the tenant in its query (row-level security alone gives the planner `tenant_id = ANY (...)`, which cannot read an index in order) and migration 00260 adds `runs (tenant_id, started_at DESC)`, built `CONCURRENTLY`. With 30,000 runs for one tenant among 300,000, the first page went from a bitmap scan of all 30,000 and a sort (23 to 33 ms) to an index scan that reads 50 rows (0.08 ms) on this machine. Not yet measured through the API at that size.
- **Retention purge of timers.** Resolved: `taskiem_purge_run` deletes a run's timers by `run_id` and deleting the run checks the timers' foreign key, and `timers` had no index on `run_id`, so each purged run scanned every timer kept for every retained run (fired timers stay until their run is purged), twice. Migration 00354 adds `timers (run_id)`, built `CONCURRENTLY`. With 200,000 timers the delete went from a sequential scan (11 ms) to an index scan (0.5 ms) on the review machine (`TestTimersAreIndexedByRun` pins the index).
- **Failure tests and failover**: the three scenarios and a standby promotion were run on the review machine on 13 November 2026 (above) and passed. Still owed: the same on the target hosts with their HA manager, an asynchronous standby, and a network partition (P4-R5).
- **Target hardware**: everything here again on production-like hardware with no neighbours, before quoting capacity.

## The Chat page's live stream

`GET /v1/chat/stream` (server-sent events, `api/chat_stream.go`) tells a person's open pages what changed in their conversations: a message, a read mark, a reaction, a deletion, a setting, someone typing. It is built so that it does **not** use `NOTIFY`, for the reason above: chat writes go to the same database as the runs, and a `NOTIFY` from every message would queue on the commit lock. Instead each open stream asks, once a second, what changed since it last looked, from an index on `link_messages (tenant_id, updated_at)` (migration 00306; a trigger keeps `updated_at` for reads, hides and reactions): one short transaction, a lookup when nothing has changed and a few rows when something has. A look-back of ten seconds and a record of the events already sent make a change that commits a little after it was written arrive once. A business may hold twelve streams open (a thirteenth is answered 429 with `Retry-After`, and that tab polls as before) and a server 2,000.

Measured with `TASKIEM_STREAM_LOAD=<businesses> go test ./api -run TestChatStreamLoad -v` (twelve streams per business, plus a pair talking; the server, the test and Postgres on one machine, so the figures show direction):

| Streams open | Transactions per second | Per stream | Send to arrival, median | 95th percentile |
| --- | --- | --- | --- | --- |
| 482 | 495 | 1.03 | 0.30 s | 1.00 s |
| 1,202 | 1,175 | 0.98 | 0.30 s | 1.00 s |

What the stream sends: `ready`, `ping` (every ten seconds of silence, so a page can tell a held-up stream from a quiet one and reopen it), `message`, `gone`, `conversation`, `typing`, and `taskiem` (the person's own conversation with Taskiem, from the same lookup, with its own cursor). A page that reconnects with `Last-Event-ID` is caught up from that id, but never from further back than an hour. A batch is at most 200 changes; a full batch is followed at once by the next, without waiting. Read marks are folded into the `message` event of the message they concern, so a page sees one event for a message and its tick turning blue.

Metrics: `taskiem_chat_streams_open` (gauge), `taskiem_chat_stream_polls_total`, `taskiem_chat_stream_poll_duration_seconds`, `taskiem_chat_stream_events_total{event}` and `taskiem_chat_stream_refused_total{cap}` (`business` or `server`). `/v1/chat/stream` is left out of the availability and latency objectives, as the run stream is: a stream open for half an hour is not a slow request.

The cost is one transaction per stream per second, linear in the streams and independent of the messages; the 95th percentile is the polling interval. At the plan's two seconds for the median this has a wide margin. What to watch on the target hardware: the transactions per second from open streams against the runs' own, and the number of streams (tabs left open on a business's side); if either grows, the interval can lengthen for streams that have been idle, or one poller per business can serve all of its streams (each stream then only filters), neither of which changes the page. A longer-term option is a table of changes written with each message, read by sequence.

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

# A slow disk: add 10 ms to every flush of a Postgres of your own, then compare
# wake-ups in the transaction and after the commit (decision 0067).
gcc -shared -fPIC -O2 -o /tmp/slowsync.so tools/loadtest/slowsync/slowsync.c -ldl
SLOWSYNC_US=10000 LD_PRELOAD=/tmp/slowsync.so pg_ctl -D "$PGDATA" -o '-p 5544' start
go run ./tools/loadtest -mode cluster -bin /tmp/taskiem -tenants 4 -workers 2 \
  -rates 10,20,30,40,60 -stage 30s -env TASKIEM_WAKE_IN_TX=true -out /tmp/slow-intx

# CPU profile of one process, then the top functions.
go tool pprof -top /tmp/taskiem /tmp/load/worker1-cpu-30.pprof
```

Flags: `-provider-latency` (default 20 ms), `-api-rate` (default 5/s), `-drain` (how long to wait for a stage's runs, default 2 min), `-stop-after-breach`, `-env KEY=VALUE,...` for extra server settings (for example `TASKIEM_DATABASE_POOL`). The tool lifts the per-tenant ingest limits so they do not shape the load. Record the machine's load next to every result; on a shared machine, interleave the builds you compare and repeat them. Leave `TASKIEM_PPROF` off in production unless you are taking a profile, and never expose the metrics port publicly.
