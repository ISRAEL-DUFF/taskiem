# 0003 — CEL for workflow expressions

Date: 2026-10-05 · Status: Accepted

## Decision

Strings starting with `=` in a WD are CEL expressions evaluated with `cel-go` (Apache 2.0) under a cost limit. Roots: `trigger`, `steps`, `run`, `env`, `secrets`, and `item`/`index` inside a `foreach`.

## Why

CEL is side-effect free, not Turing-complete, and has bounded cost, so an expression cannot hang a worker or reach the network. It has a published spec and a maintained Go implementation.

## Alternatives

JavaScript snippets (unbounded, needs a sandbox per expression), JSONata (less familiar, weaker typing), a custom template language (more to build and secure).

## Provenance

CEL is a public specification (github.com/google/cel-spec). The `=` prefix convention is common in spreadsheets and was chosen independently.
