# Decision log

One file per decision, numbered in order: `NNNN-short-title.md`. Each entry records what was decided, why, the alternatives considered, and, where the design resembles an existing product, where the idea came from. Entries are never rewritten: amendments are appended with a date, and a later decision supersedes an earlier one by number.

| # | Decision | Status |
| --- | --- | --- |
| [0001](0001-event-sourced-execution.md) | Event-sourced runs with a pure `decide()` | Accepted |
| [0002](0002-postgres-as-queue.md) | PostgreSQL as the task queue in v1 | Accepted; amended after the G0 spike |
| [0003](0003-cel-expressions.md) | CEL for workflow expressions | Accepted |
| [0004](0004-tenant-isolation-rls.md) | RLS with tenant scope arrays and a dispatch role | Accepted |
| [0005](0005-idempotency-keys.md) | Derived and per-connector encoded idempotency keys | Accepted |
| [0006](0006-working-name.md) | Working name Taskiem | Accepted, pending trademark search |
| [0007](0007-licence-policy.md) | Two-tier licence policy | Accepted |
| [0008](0008-wd-v1-control-flow.md) | Nested sub-flows for branch, parallel and foreach | Accepted |
| [0009](0009-write-call-deadlines.md) | Call deadlines for writes | Accepted |
| [0010](0010-secret-only-expressions.md) | Expressions that read secrets read nothing else | Accepted |
| [0011](0011-sandbox-quickjs-fork.md) | Code steps in QuickJS on wazero, via a vendored fork | Accepted |
| [0012](0012-trigger-registration.md) | Trigger registration, environments, and webhook secrets | Accepted |
| [0013](0013-private-network-access.md) | Private databases through an SSH tunnel first, a relay later, private ranges only single-tenant | Proposed |
