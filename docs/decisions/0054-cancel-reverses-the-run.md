# 0054 — Cancelling a run reverses its completed steps, unless the person chooses not to

Date: 2026-10-28 · Status: **Accepted** (built 28 October 2026) · Spec: [4.10 Replay, fork, and cancel](../spec/architecture.md#410-replay-fork-and-cancel)

## Context

The spec says cancelling a run "runs compensation if configured". It did not: `CancelRun` appended `RunCancelled` and ended the run at once. Cancelling a half-done disbursement left the transfers it had made in place, while a failed run of the same workflow would have reversed them, newest first. An operator could easily take a cancelled run for an undone one.

## Decision

1. **Cancel reverses by default.** `POST /v1/runs/{run}/cancel` (and the embed route, and a cancelled or moved storefront booking) records `CancelRequested`. Decide then:
   - withdraws every step that has started but not finished (`StepCancelled`), so nothing new starts and waiting approvals, timers, signals and forms are closed;
   - waits for writes already under way (an intent recorded, no outcome yet), or parked for an operator, to settle; a write that completes is reversed with the rest;
   - runs the compensations of the completed steps, newest first, exactly as a failed run does (`CompensationStarted` … `CompensationCompleted`);
   - ends the run with `RunCancelled`, naming who cancelled it and the steps it reversed.
   The run stays `running` (or `needs_reconciliation` while a write or a compensation is parked) until then. A second request changes nothing.
2. **Cancel without reversing is an explicit choice**: `{"reverse": false}` ends the run at once and leaves completed steps as they are, as before. The console offers both, with "Cancel and reverse" first. A queued run, which has done nothing, is always cancelled at once.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Keep cancel as it was and correct the spec | The person cancelling a disbursement half way almost always wants the money back; asking them to find and reverse each transfer by hand is how money is lost |
| Reverse only in the console, not the API | The API is how most businesses' own systems cancel runs; the same request should mean the same thing everywhere |

## Consequences

- An API client that cancels and reads the status in the answer may see `running`; the run ends when its compensations have run, and its events stream says so.
- A new event type, `CancelRequested` (migration 00236). Histories without it replay as before.
- Reversing is only as good as each step's `compensate`: a step with none is left as it is, as on failure.
