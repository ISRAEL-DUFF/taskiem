# Contract: Action classes

Every connector action and every writing `http` step declares one class. The engine's retry behaviour depends only on the class (spec 4.4); `engine/effects` holds the reference implementation.

| Class | Retry on `retryable` error | On `unknown_outcome` | Before re-sending after a crash with an open `EffectIntent` |
| --- | --- | --- | --- |
| `read` | Retry freely | Retry freely | No intent is written for reads |
| `idempotent_write` | Retry with the same key | Retry with the same key | Re-send with the same key |
| `reconcilable_write` | Retry with the same key | Run `reconcile`; re-send only on `not_found` | Run `reconcile` first |
| `unsafe_write` | Retry only if the error proves nothing was sent (connection refused, DNS failure, 4xx before body) | Never retry; park in `needs_reconciliation` and alert | Park in `needs_reconciliation` |

## Error classes

Handlers return errors wrapped as one of:

- `retryable`: timeouts before send, 5xx, 429 (honouring `Retry-After`).
- `fatal`: validation errors, 4xx auth or permission. Never retried.
- `unknown_outcome`: the request may have reached the provider (connection dropped after send, timeout after send).

An unclassified error is treated as `unknown_outcome`, the safe default.

## Reconcile results

A reconcile action returns `found` (with the provider's record, which becomes the step's output), `not_found` (safe to re-send; `attempt_group` is incremented), or `indeterminate` (park in `needs_reconciliation`).

## Open question for Phase 1

Notifications (SMS, Slack) are `unsafe_write`, but for them a duplicate is harmless and a missing message is not (found while drafting the dogfood ops alert). Candidate: a per-step opt-in `effect.duplicates: tolerable` that lets an `unsafe_write` retry an unknown outcome, refused by policy on any step a tenant marks as moving money. Decide before Phase 1 week 6.
