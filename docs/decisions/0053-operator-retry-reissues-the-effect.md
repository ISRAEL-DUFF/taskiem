# 0053 — An operator's retry of a parked write re-issues it under a new idempotency key

Date: 2026-10-28 · Status: **Accepted** (built 28 October 2026) · Contract: [idempotency keys](../contracts/idempotency.md#derivation)

## Context

A write whose outcome is unknown parks the run in `needs_reconciliation` until an operator checks with the provider and resolves the step: it happened (completed), it did not and should not be retried (failed), or it did not and should be sent again (retry). The idempotency contract says `attempt_group`, and with it the key, moves only when a reconcile answers `not_found` or a person explicitly re-issues the effect. The retry resolution did not move it: the next attempt reused the key of the call that parked.

Two things followed. A provider that keeps the answer for a reference (an `idempotent_write`) replayed the first call's failure, such as insufficient balance, or refused a reference it had seen, so the step parked again and the operator could not get the payment out without editing the workflow. And a step parked because its retries ran out (`retry.max` 0, the default for code steps, or a budget that was spent) did not run at all: decide counted the retry against the spent budget and failed the run.

Reusing the key does protect against one mistake: an operator who chooses retry when the provider did act gets the cached success instead of a second payment.

## Decision

1. **Retry re-issues the effect.** The operator's resolution is recorded with `reissue: true`; the next attempt sends under the next attempt group, a new key.
2. **Retry always sends.** A re-issue does not count against the step's retry budget or its `max_duration`, and is scheduled at once, as a step resumed after its key returned is.
3. **The console makes the operator confirm** they checked with the provider that the effect did not happen, and says plainly that the retry uses a new reference, so a payment that did go through would be paid twice. The resolution and its note are audited, as before.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Keep the same key and change the contract | Safe if the operator is wrong, but a cached provider failure keeps the step parked for good, which is the case operators meet most |
| Offer both "retry with the same key" and "re-issue with a new key" | One more choice to get wrong at the moment someone is resolving a stuck payment; an operator who is unsure should choose neither and check with the provider |

## Consequences

- The safety of a retry now rests on the operator's check, which was already required (a note recording what the provider shows) and audited.
- Histories written before this change carry no `reissue` flag and replay exactly as before.
