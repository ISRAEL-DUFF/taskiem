# 0010 — Expressions that read secrets read nothing else

Date: 2026-10-05 · Status: Accepted

## Decision

A WD expression that references `secrets` may not reference any other variable. Decide resolves every other expression into the `StepScheduled` payload and leaves secret-only expressions as strings; the worker resolves them at call time.

## Why

Decide must be pure and its outputs are stored, so it can never see secret values. Allowing mixed expressions (`=secrets.base + trigger.body.path`) would force the worker to rebuild the whole expression context or partially evaluate CEL. Restricting secret expressions keeps secret values out of history by construction and makes the worker's job a lookup and a template.

## Provenance

Our own rule.

## Amendment — 2026-11-13, data is never evaluated at the worker

The worker used to find the expressions decide had left by searching the scheduled input for strings that start with `=` and read `secrets`. An expression decide had already evaluated can yield exactly such a string: a webhook field, a form answer or a chat message whose whole value is `=secrets.api_token`. The worker then looked that secret up and sent it wherever the step sent the field. When the step also held a genuine secret expression, every string of the input starting with `=` was evaluated.

Now a place is an expression only because the workflow's author wrote one there:

- Decide records where it left secret expressions: `secret_paths` in the `StepScheduled` payload, a list of JSON pointers (RFC 6901) into `input`. It is written from the definition alone, so no value of a run can add to it. A step with no secret expression has no such field, and its payload is byte for byte what it was.
- The worker evaluates those places and no others. Every other string is data and is sent as it is, whatever it starts with. A listed place that does not hold an expression reading secrets fails the step; it is never guessed at.
- A step expanded from a capability passes its input through the rail's mapping. Decide evaluates the mapping twice more with a different marker in place of each kept expression. A place of the action's input is a secret's only where the two results hold the two markers: the mapping carried that field there whole. A value of the run is the same in both results, so it cannot match both.
- A payload written before this amendment has no list. The worker then takes the places from the step as authored (`decide.AuthoredSecretPaths`): a place counts only when the definition holds a secret expression there and the payload holds the very same string. Resolving keeps the shape of what the author wrote, so a value of the run cannot sit at such a place. Tasks in flight keep their genuine secrets; the input is never searched again. No migration.
- Replay (`decide.Verify`) accepts a recorded `StepScheduled` that differs from the replayed one only in having no `secret_paths`. Any other difference still fails.

No change to wd/v1. Tests: `engine/runtime/secretdata_test.go`, `engine/decide/secretpaths_test.go`, `engine/expr/kept_test.go`.
