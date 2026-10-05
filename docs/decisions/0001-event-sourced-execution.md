# 0001 — Event-sourced runs with a pure `decide()`

Date: 2026-10-05 · Status: Accepted

## Decision

A run is its append-only event history (`run_events`). A pure function `decide(WD version, history) → []Command` determines what happens next. It reads no clock, randomness, or external state; time enters only through recorded events. Workers execute steps; the orchestrator writes new events and tasks in one transaction.

## Why

- Crash recovery is "reload history and decide again"; nothing is held only in memory.
- Replay is byte-for-byte reproducible, which the audit log, debugging, and AI repair all rely on.
- A transactional outbox (events plus tasks in one commit) removes dual-write bugs.

## Alternatives

- **State machine rows updated in place.** Simpler at first, but loses the history needed for replay and audit, and makes "what did the engine know when it decided" unanswerable.
- **Code-as-workflow with deterministic replay of user code.** Powerful, but forces determinism rules on every author. Our users edit a declarative definition instead, so only `decide()` must be deterministic.

## Provenance

Event sourcing, command/event separation, and the transactional outbox pattern are taken from public literature (Fowler, "Event Sourcing", 2005; Richardson, "Microservices Patterns", 2018, transactional outbox; Garcia-Molina and Salem, "Sagas", 1987). Durable-execution products use related ideas; no product source code was consulted.
