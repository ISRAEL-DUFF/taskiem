# Contracts frozen for Phase 1

| Contract | Machine-readable | Reference implementation |
| --- | --- | --- |
| [Workflow Definition `wd/v1`](wd-v1.md) | `schemas/wd-v1.schema.json` | `engine/wd` |
| [Connector manifest `connector/v1`](connector-v1.md) | `schemas/connector-v1.schema.json` | `engine/connector` |
| [Action classes](action-classes.md) | `class` in connector and `http` steps | `engine/effects` |
| [Idempotency keys](idempotency.md) | `idempotency` in manifests, `effect` in steps | `engine/effects` |
| [Workflow tests `wd-test/v1`](wd-test-v1.md) (Phase 2) | `*.test.json` files | `engine/wdtest` |
| [WebAssembly connector ABI `taskiem-connector/v1`](connector-wasm-v1.md) (Phase 2) | module imports and exports | `engine/wasmconn`, `sdk/connectorsdk` |

"Frozen" means Phase 1 builds against these as written. A change during Phase 1 needs a decision-log entry; a breaking change after Phase 1 needs a new schema version and a migration tool (build plan, "Release cadence").

Changes recorded during Phase 1:

- `connector/v1` rule 13, `auth.oauth2` (an addition; nothing existing changed): [decision 0034](../decisions/0034-oauth-connections.md).
- `wd/v1` trigger type `storefront` (an addition; nothing existing changed), and its `booking` action (S4, also an addition): [decision 0036](../decisions/0036-storefronts.md), [storefronts](../storefronts.md).
- `wd/v1` trigger type `form` and step type `form` (S7, additions; nothing existing changed), with the `forms` mock in `wd-test/v1`: [decision 0037](../decisions/0037-forms.md), [forms](../spec/forms.md).
