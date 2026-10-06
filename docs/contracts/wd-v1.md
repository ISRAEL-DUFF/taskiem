# Contract: Workflow Definition `wd/v1`

The structure is defined by `schemas/wd-v1.schema.json`. This document adds the semantic rules a JSON Schema cannot express. `engine/wd.Validate` enforces both.

## Structure in brief

- Top level: `schema`, `id`, `version`, `name`, `trigger`, `steps`, optional `description`, `inputs`, `types`, `settings`. Canvas layout is **not** part of the WD (spec 3.5).
- A **scope** is a list of steps: the top-level `steps`, or the `steps` of a branch path, branch default, parallel branch, foreach body, or `on_error` handler.
- `connector` steps put `connector`, `action`, `input`, `effect`, and `compensate` at the step level. Every other type keeps its settings in `config`; `code`, `subflow`, and `ai` also take `input`.

## Semantic rules

1. **Unique ids.** Step ids are unique across the whole WD, nested scopes included. Branch path and parallel branch names are unique within their step.
2. **`needs` stay in scope.** A step may only `need` steps in its own scope. A nested step implicitly runs after its parent starts; it does not list the parent.
3. **No cycles.** `needs` within each scope form a DAG.
4. **Ready rule.** A step is ready when every step it needs has completed or been skipped. If its `when` is false, or any needed step was skipped, it is **skipped**. A skipped step has no output.
5. **Expression references.** `steps.<id>` may reference a step that is a transitive `need` of the current step, or of one of its enclosing control steps. Referencing a step that may not have run yet is a validation error.
6. **Foreach scope.** Inside a `foreach` body, `item` and `index` are in scope, and `steps.<id>` for a body step refers to the current iteration. Outside, `steps.<foreach_id>.output` is the list of per-iteration outputs, in item order, each an object keyed by body step id.
7. **Branch output.** `steps.<branch_id>.output` is `{ "path": "<name or default>", "steps": { <id>: output } }`. Paths are tested in order; the first true `when` wins.
8. **Parallel output.** `{ "<branch name>": { <id>: output } }`. With `join: any`, the first branch to finish wins, the output holds only that branch, and the others are cancelled (and compensated if they committed effects).
9. **Writes must be classified.** `http` steps with `POST`, `PUT`, `PATCH`, or `DELETE` must declare `class` other than `read` (schema-enforced). `effect` is only meaningful on writes.
10. **Expression roots.** `trigger`, `steps`, `run`, `env`, `secrets`, and inside a foreach `item` and `index`. `secrets` may appear only in `input`, `config.headers`, `config.url`, `config.query`, and `config.body` of the step that uses them, never in `when`, `subject`, `transform` output, or `effect`, so a secret can never be copied into history. An expression that reads `secrets` may read nothing else (`='Bearer ' + secrets.token` is fine; `=secrets.base + trigger.body.path` is not): decide resolves every other expression and leaves secret-only ones for the worker, so secret values meet run data only inside the provider call.
11. **Limits.** At most 500 steps per scope; foreach `max_items` defaults to 10,000; `settings.timeout` may not exceed the plan maximum (spec 9.4).

Rules 1–5, 9, and 10 are enforced by `engine/wd.Validate`. Rules 6–8 are runtime semantics implemented by `engine/decide`. Rule 11's plan maximum is enforced at publish time once plans exist (Phase 4).

## Runtime semantics (engine/decide)

- **Step instances.** Events name step instances: the step id, prefixed inside a `foreach` by each enclosing iteration (`pay_all[3].pay_employee`, `outer[1].inner[2].x`). Branch and `on_error` steps keep their plain ids.
- **Skipping.** A step whose `when` is false is skipped. A step whose need was skipped, or failed and was handled by `on_error`, is skipped too.
- **Failure.** A step fails when its retries are exhausted, its error is fatal, or an expression it evaluates fails. Its scope then starts nothing new and waits for in-flight steps. With `on_error`, the step's on_error flow runs (with `steps.<id>.error` in scope) and the step counts as handled: its dependents are skipped and the run carries on. A handled step's on_error outputs take its place in the enclosing scope's results, so a `foreach` iteration whose payment failed reports its on_error record. Without it, the failure propagates to the enclosing control step, and at top level the run compensates (reverse completion order) and fails.
- **Default retry policy.** Connector and http steps without `retry` get `{max: 3, backoff: exponential, initial: 2s, max_delay: 5m}`; code steps get no retries. Jitter (up to 20%) is derived from the run, step instance, and attempt, so replays agree.
- **Parallel.** Branches start together, at most `max_concurrency` at a time (default: all), in definition order. Branch steps keep their plain ids, as in a `branch`. With `join: all` (the default) the step completes when every branch has; once a branch fails, no branch starts anything new, steps already in flight settle, and the step fails. With `join: any` the first branch to complete wins (definition order breaks a tie within one decision) and is recorded in a second `StepStarted` (`{winner}`), so replays agree. Every losing step that has begun and not finished gets a `StepCancelled` event: its timers, signal waits, open approval, and any task no worker holds are removed. A worker holding a cancelled task drops it unless a write was already under way (its `EffectIntent` recorded, checked under the run lock); the parallel step waits for such writes to settle. Then the losers' completed steps that declare `compensate` are compensated, newest first, and the parallel step completes. It fails only when every branch has failed.
- **Approvals.** With `role`/`count`, that many distinct people holding the role approve (maker-checker applies). With `policy`, the policy version active when the run started (snapshotted into `RunStarted`) routes the approval: the first rule matching the `subject` sets the levels, step-up and constraints, recorded in `ApprovalRequested`; the policy's `timeout`/`on_timeout` apply when the step sets none. No active version, or no matching rule, fails the step with kind `policy`. See [governance](../governance.md).
- **Outputs.** `transform` runs inside decide; `wait` outputs `{fired_at}`; `signal` outputs the signal's payload; `approval` outputs `{decision, decided_by}` (`rejected` with `reason: timeout` by default on timeout); `branch` outputs `{path, steps}`; `foreach` outputs a list, one object per iteration keyed by body step id.
- **Not yet executable.** `subflow` and `ai` validate but fail at run time with kind `unsupported` until Phase 3.

## Durations

`<n><unit>` repeated, units `ms`, `s`, `m`, `h`, `d`; for example `90s`, `1h30m`, `7d`.
