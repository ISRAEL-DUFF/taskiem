# 0002 — PostgreSQL as the task queue in v1

Date: 2026-10-05 · Status: Accepted, pending the G0 load test

## Decision

Tasks live in a Postgres table. Workers claim with `SELECT … FOR UPDATE SKIP LOCKED` through the `claim_tasks` dispatch function, hold a time-limited lease, and carry a `lease_epoch` fencing token on every write. `LISTEN/NOTIFY` wakes idle workers; a 1 s poll is the fallback.

## Why

Events and tasks commit in one transaction, so a scheduled step can never be lost or duplicated by a crash between two systems. One fewer moving part to run in-region.

## Exit path

The queue sits behind an interface. If load tests show one primary cannot hold the targets (500 steps/s, p95 dispatch under 50 ms), it moves to NATS JetStream without changing `decide()`.

## Evidence

See `engine/spike/RESULTS.md` for the Phase 0 spike numbers and the projection to the G1 target.

## Provenance

`SKIP LOCKED` queues are a documented PostgreSQL pattern (PostgreSQL docs, `SELECT … FOR UPDATE SKIP LOCKED`, since 9.5). Fencing tokens are from Kleppmann, "How to do distributed locking", 2016.

## Amendment — 2026-10-05, after the G0 spike

The spike sustained 500 steps/s at p95 dispatch 4–5 ms and peaked at 1,302 steps/s on a 4-vCPU host (`engine/spike/RESULTS.md`). The decision stands, with two changes to how the engine is built:

1. **One claimer per process.** Each worker or orchestrator process holds a single `LISTEN` connection and a single claim loop that feeds its executors. One claimer per executor caused a thundering herd costing 7× the commits.
2. **Inline decide.** `decide()` runs inside the transaction that appends a step's result, as ingest already does for `RunStarted`. The orchestrator role sweeps runs left undecided. This cut commits per step by 43% and raised peak throughput by 34%.
