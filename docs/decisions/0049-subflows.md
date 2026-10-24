# 0049. Subflows: another workflow as a child run

**Status:** Accepted (2026-10-24)

## Context

wd/v1 has had a `subflow` step since Phase 1, validated but failing at run time as unsupported, kept for Phase 3. [0047](0047-composable-templates.md) decided what it is for, against template steps: a template step pulls another workflow's steps into this run, so one run holds the approvals, payments, waits and exactly-once rules; a subflow runs another workflow of the business as its own run, for a workflow that is deployed, owned and changed on its own, and whose history should be its own. [0048](0048-callable-workflows.md) then made the business's own workflows usable as template steps. What was left is the child run: the onboarding workflow started once for each new customer, the month-end close that runs the payroll workflow and waits for it, the report workflow kicked off and left to run.

## Decision

1. **The step's config is the workflow's id, an optional version and `wait`.** `config.workflow` is the workflow's UUID, as a template step names a workflow (0048); `config.version` pins a version, and without it the version published when the step starts runs, so the child workflow changes on its own schedule; `wait` (the default) makes the step wait for the child to end. `input` is the child's run inputs, checked against its inputs schema when the version is saved and when the step starts.
2. **The child starts in the parent's transaction.** Decide schedules the step with the child's inputs (`KindSubflow`); the runtime starts the child run in the same transaction that records the step, so a parent whose step is recorded always has its child and a child always has its step. What keeps the run from starting (no such workflow, no published version, a trigger that does not start on demand, a plan limit, a suspended tenant, too deep a chain) fails the step with kind `subflow` rather than the parent's decision.
3. **The child reports as a signal.** A waiting step is a signal wait (event `subflow`, correlated by the parent run and step instance) that the child delivers when it ends, in the transaction that ends it, with `{run_id, status, outputs, error}`: its declared outputs (0048) evaluated at completion, or its failure. The step's output is that report; a child that failed or was cancelled fails the step. A step that does not wait gets `{run_id, status: "started"}` at once. The wait is registered before the child starts, so a child that completes inside the parent's own decision (a workflow of transforms) reports into that decision's next round rather than into a nested one.
4. **The child carries the parent's seed.** Its idempotency seed is the parent's seed (or run id) and the step instance, as 0047 asked: a parent resumed from a failure replays the step's recorded signal, and a child that does run again under the same seed sends the same keys.
5. **A parent that ends cancels the children it waits for.** Completed, failed, cancelled or timed out, it has no use for their results and should not leave money moving for it; children it did not wait for are independent and finish. Cancelling a child fails the parent's step.
6. **Bounds.** A workflow may not run itself; runs nest five deep at most. The child runs in the parent's environment and counts against the business's quotas as any run.
7. **Lineage.** `runs.parent_run_id` (already there for forks) with `parent_step` and `parent_waits` tell a child from a fork (which has `forked_from_seq`). The run page shows what started a run and the runs it started; the builder lists the startable workflows and a rule says when to use a subflow rather than a template step.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Start the child from a worker task | A task retried after a crash could start two children, or none if its record was lost; the transaction gives one child per recorded step with nothing to reconcile |
| Poll the child's status from the parent | A timer per waiting step, latency, and a parent that polls a child that was deleted; the child reporting as it ends is one event with no polling |
| Leave children running when the parent is cancelled | A payroll child moving money for a month-end run that someone cancelled is the case to avoid; a fire-and-forget child is marked as such (`wait: false`) and is left alone |
| A version pinned at save, as template steps are | The point of a subflow over a template step is a workflow that changes on its own; the published version at start is what the business stands behind, and `version` pins it when that is wanted |

## Consequences

- wd/v1's `subflow` step config changes from `{workflow: wf_..., version}` (required, never executable) to `{workflow: <uuid>, version?, wait?}` ([contract](../contracts/wd-v1.md#subflow-steps)); the SDK's `subflow()` with it.
- `history` gains `KindSubflow`, `SubflowEvent` and `RunCompletedPayload.Outputs`; decide evaluates a workflow's outputs when a run completes, for any caller.
- `runtime.Start` is split so a run can be started inside a transaction (`startIn`); `deliverSignal` likewise (`deliverSignalTx`), which also skips deciding a run whose decision is under way in the same call.
- `runtime.ForkInfo` gains `parent_step` and `subflows`; `lockRun` tells a fork by `forked_from_seq`, not `parent_run_id`.
- Workflow tests gain `subflows` mocks; the read-back says "Run your workflow: X, and wait for it to finish".
- `ai` is the only step type left unsupported at run time.

## Provenance

From the review of 0047 and 0048 as built (2026-10-24): the founder asked for the one item left, the subflow step.
