# Contracts frozen for Phase 1

| Contract | Machine-readable | Reference implementation |
| --- | --- | --- |
| [Workflow Definition `wd/v1`](wd-v1.md) | `schemas/wd-v1.schema.json` | `engine/wd` |
| [Connector manifest `connector/v1`](connector-v1.md) | `schemas/connector-v1.schema.json` | `engine/connector` |
| [Action classes](action-classes.md) | `class` in connector and `http` steps | `engine/effects` |
| [Idempotency keys](idempotency.md) | `idempotency` in manifests, `effect` in steps | `engine/effects` |

"Frozen" means Phase 1 builds against these as written. A change during Phase 1 needs a decision-log entry; a breaking change after Phase 1 needs a new schema version and a migration tool (build plan, "Release cadence").
