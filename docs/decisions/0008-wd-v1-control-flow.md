# 0008 — Nested sub-flows for branch, parallel and foreach

Date: 2026-10-05 · Status: Accepted

## Decision

In `wd/v1`, the top level and every sub-flow are a list of steps forming a DAG through `needs`. Control flow is explicit:

- `branch` has `paths` (each a `when` and its own `steps`) and an optional `default`.
- `parallel` has `branches` (each its own `steps`), a `join` of `all` or `any`, and a concurrency cap.
- `foreach` maps its own `steps` over `items`, with `item` and `index` in scope and a concurrency cap.

Cycles are invalid. Step ids are unique across the whole WD, including nested ones, so `steps.<id>` is unambiguous.

## Why

Implicit loops through graph edges make replay and visual layout ambiguous. Nested sub-flows keep each control structure a single node on the canvas and a single scope in `decide()`.

## Provenance

Structured control flow is standard programming-language design; the JSON shape is our own.
