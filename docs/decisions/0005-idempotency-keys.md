# 0005 — Derived and per-connector encoded idempotency keys

Date: 2026-10-05 · Status: Accepted

## Decision

Key material is `tenant_id ‖ seed ‖ step_id ‖ attempt_group`, where the seed is the WD's `effect.idempotency_seed` or the run id. The engine hashes it with SHA-256 and the connector manifest encodes the hash to fit the provider (charset, length, prefix). The contract and reference implementation are `docs/contracts/idempotency.md` and `engine/effects`.

## Why

Provider references have strict formats (Paystack: 16–50 characters of `[a-z0-9_-]`). A raw composite key would be rejected or truncated. Hashing gives a fixed length; per-connector encoding gives a valid charset. Deriving rather than generating keeps keys identical across retries and replays.

## Provenance

Idempotency keys are documented practice for payment APIs (for example Paystack's own transfer reference docs). The derivation scheme is our own.
