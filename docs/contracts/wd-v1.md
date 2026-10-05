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
8. **Parallel output.** `{ "<branch name>": { <id>: output } }`. With `join: any`, the first branch to finish wins and the others are cancelled (and compensated if they committed effects).
9. **Writes must be classified.** `http` steps with `POST`, `PUT`, `PATCH`, or `DELETE` must declare `class` other than `read` (schema-enforced). `effect` is only meaningful on writes.
10. **Expression roots.** `trigger`, `steps`, `run`, `env`, `secrets`, and inside a foreach `item` and `index`. `secrets` may appear only in `input`, `config.headers`, `config.url`, and `config.body` of the step that uses them, never in `when`, `subject`, `transform` output, or `effect`, so a secret can never be copied into history.
11. **Limits.** At most 500 steps per scope; foreach `max_items` defaults to 10,000; `settings.timeout` may not exceed the plan maximum (spec 9.4).

Rules 1–4, 9, and 10 are checked in Phase 0. Rules 5–8 and 11 need the CEL type checker and plan data and are completed in Phase 1.

## Durations

`<n><unit>` repeated, units `ms`, `s`, `m`, `h`, `d`; for example `90s`, `1h30m`, `7d`.
