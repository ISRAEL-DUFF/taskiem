# 0010 — Expressions that read secrets read nothing else

Date: 2026-10-05 · Status: Accepted

## Decision

A WD expression that references `secrets` may not reference any other variable. Decide resolves every other expression into the `StepScheduled` payload and leaves secret-only expressions as strings; the worker resolves them at call time.

## Why

Decide must be pure and its outputs are stored, so it can never see secret values. Allowing mixed expressions (`=secrets.base + trigger.body.path`) would force the worker to rebuild the whole expression context or partially evaluate CEL. Restricting secret expressions keeps secret values out of history by construction and makes the worker's job a lookup and a template.

## Provenance

Our own rule.
