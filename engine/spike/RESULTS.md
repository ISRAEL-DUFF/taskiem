# Phase 0 engine spike — results

Date: 2026-10-05. The spike is throwaway code (`engine/spike`). It runs the real schema v0, RLS, dispatch functions and fencing, with a no-op step body, so it measures the cost of the queue and orchestration path alone.

## Setup

| Item | Value |
| --- | --- |
| Host | Shared cloud container, 4 vCPU, 15 GB RAM; load generator on the same host |
| Postgres | 16.14, stock config (`shared_buffers` 128 MB), `fsync` and `synchronous_commit` on, no replica |
| Go | 1.26.0, pgx v5 |
| Workload | 20 tenants, linear 5-step workflows, every step a task through `taskiem_claim_tasks` |
| Process shape | 1 process: 1 task claimer feeding 16 executors, 1 run claimer feeding 8 deciders, 64 connections |

Reproduce with `make spike` (or `go run ./engine/spike/cmd/loadtest -h` for the flags).

## Results

**Open-loop (sustained arrival rate)**: the G1 latency criterion.

| Variant | Offered | Achieved | Dispatch p50 / p95 / p99 | Decide p95 | Commits / step | Host CPU busy |
| --- | --- | --- | --- | --- | --- | --- |
| Separate orchestrator | 500 steps/s | 499 | 2.8 / 4.3 / 5.4 ms | 5.8 ms | 5.8 | 63% |
| Separate orchestrator | 800 steps/s | 786 | 6.0 / 10.5 / 12.7 ms | 13.7 ms | 5.2 | 81% |
| **Inline decide** | 500 steps/s | 498 | 3.5 / 4.9 / 6.0 ms | 2.5 ms | 3.3 | 51% |

**Closed-loop (all runs started at once)**: peak throughput. Dispatch latency here is dominated by backlog and is not meaningful.

| Variant | Runs × steps | Throughput | Commits / step | Host CPU busy |
| --- | --- | --- | --- | --- |
| One claimer per executor (first attempt) | 2,000 × 5 | 499 steps/s | 32.9 | 92% |
| Separate orchestrator | 4,000 × 5 | 971 steps/s | 4.7 | 80% |
| **Inline decide** | 4,000 × 5 | 1,302 steps/s | 3.0 | 85% |

No task was ever fenced out in these runs (leases never expired), and every run completed.

## G0 criteria

- **At least 200 steps/s on one Postgres instance**: met: 971 steps/s with the separate orchestrator design, 1,302 steps/s with inline decide.
- **Profile names the bottlenecks, with a projection to 500 steps/s**: met, on this host. 500 steps/s is already sustained at p95 dispatch 4–5 ms, against a G1 limit of 50 ms. Details below.

## Bottlenecks found

1. **Thundering herd (fixed in the spike).** The first version gave every executor its own `LISTEN` connection and claim loop. Each task notification woke all 16 workers and all 8 orchestrators; most of their claims came back empty, giving 33 commits per step and a CPU-bound host at 499 steps/s. One listener and claimer per process, claiming only as many tasks as it has free executors, cut this to 4.7 commits per step and doubled throughput. **Phase 1 worker and orchestrator processes must use this shape.**
2. **The orchestrator claim is the most expensive statement.** With `pg_stat_statements` at 500 steps/s, `taskiem_claim_runs_to_orchestrate` and its inner query took about 33% of all SQL time. Each step also updated the `runs` row three times (event seq, orchestrator lease, `decided_seq`), and every one of those updates churns the `runs_pending_decide` partial index.
3. **Postgres CPU, not the Go client, is the ceiling.** The load generator used 0.6–0.7 cores at every rate; its profile is mostly network syscalls and scheduler time. Postgres used the rest of the host.

## Recommendation for Phase 1

Run `decide()` **inline in the transaction that appends a step's result**, the same way ingest already runs the first decision (spec 2.2). The worker already holds the run row lock from the append, so this needs no extra lock. The `orchestrator` role remains as a sweeper for runs left undecided: crash leftovers, timer and signal events, and events appended by other paths. It also stays the place `decide()` runs when a decision is expensive. Measured gain: 43% fewer commits per step, 34% more peak throughput, and decide latency p95 down from 5.8 ms to 2.5 ms.

This changes how the work is split, not what the spec guarantees. `decide()` stays a pure function and events plus tasks still commit in one transaction. It is recorded as an amendment to decision 0002.

## Projection to the G1 target (500 steps/s, p95 dispatch under 50 ms)

On this host, Postgres spends about 2.1 ms of CPU per step with inline decide (about 2.7 cores at 1,302 steps/s). On the planned Cloud v1 primary, assumed to be 8 or more dedicated vCPU:

| Factor | Effect | Allowance |
| --- | --- | --- |
| CPU ceiling at 2.1 ms/step on 8 vCPU | about 3,800 steps/s | — |
| Real payloads (KB-sized JSON, PII envelopes) instead of `{"ok":true}` | more WAL and TOAST work per event | ×2 cost |
| Synchronous standby in-region (spec 2.3) | about 1 ms more commit latency; throughput holds through group commit with enough concurrent writers | more connections, not CPU |
| Longer histories: `decide()` reads the whole history each time | O(steps) per decision; fine at 5 steps, not at foreach over 5,000 items | needs history snapshots (below) |
| Inspector reads, ingest, timers on the same primary | competing load | ×1.5 cost |

With those allowances, the CPU ceiling is about 1,200 steps/s, which is 2.4 times the 500 steps/s target. The Postgres-as-queue choice (decision 0002) stands; the JetStream exit path is not needed for G1.

## Not yet measured (Phase 1 load tests)

- Target hardware with a synchronous standby. The G1 number must come from there.
- **Long histories.** A `foreach` over thousands of items makes full-history reads in `decide()` quadratic. Phase 1 needs an incremental decision state (a snapshot of the derived state, kept in the run row or a side table, rebuilt by replay in tests) before the Payrolla dogfood workflow runs at real payroll sizes.
- Lease expiry and fencing under load (kill -9 of workers mid-step). The fencing paths are covered by integration tests; the chaos suite comes in Phase 1.
- Vacuum and bloat on `tasks` and `runs` under sustained load over hours.
- `LISTEN/NOTIFY` with many worker processes. Postgres serialises `NOTIFY` at commit; at high process counts, consider notifying only on empty-to-non-empty queue transitions.
