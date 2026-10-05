# 0009 — Call deadlines for writes

Date: 2026-10-05 · Status: Accepted

## Decision

Every write's provider call has a deadline of `EffectIntent.recorded_at + call_timeout` (default 30 s). A worker does not start a call after the deadline, and cancels it at the deadline. Reconcile accepts a provider's `not_found` only after the deadline of the intent being reconciled; before that, the task is requeued for the deadline without spending a retry.

## Why

Fencing (decision 0002) stops a worker whose lease expired from *recording* a result, but cannot stop it from *calling the provider*. For a `reconcilable_write` against a provider that does not deduplicate, a stalled worker could send its request after a second worker had reconciled `not_found` and re-sent under a new key: one logical payment, two transfers. The Phase 1 crash suite reproduces this (duplicate bank transfers at 15% injected stalls when the guard is removed) and shows zero duplicates with it.

## Limits

The guard bounds when a worker can *start* a call. A request already in flight at the deadline that the provider processes late can still race a reconcile; `call_timeout` should exceed the provider's own processing time, and worker and database clocks must agree within a small skew (NTP).

## Provenance

Fencing tokens and lease timing hazards: Kleppmann, "How to do distributed locking" (2016). The deadline rule is our own.
