# Contributing to Taskiem

Before your first commit:

1. Read and sign the [clean-room policy](docs/clean-room-policy.md), and sign an IP assignment agreement.
2. Read the [architecture spec](docs/spec/architecture.md) and the [build plan](docs/spec/build-plan.md).

## Repository layout

| Path | Contents |
| --- | --- |
| `cmd/taskiem` | The single binary: `migrate`, `validate`, and (Phase 1) `serve --role` |
| `engine/` | Engine packages: database and migrations, effects contract, the Phase 0 spike |
| `connectors/` | First-party connector manifests and (from Phase 1) Go handlers |
| `schemas/` | Frozen contracts: `wd/v1` and `connector/v1` JSON Schemas |
| `flows/` | Workflow definitions, including the Phase 0 dogfood drafts |
| `sdk/` | TypeScript SDK (`@taskiem/sdk`) |
| `web/` | Web app (Phase 1) |
| `deploy/` | Docker Compose for local and single-node installs |
| `tools/` | Repository tooling, such as the licence checker |
| `docs/` | Spec, build plan, contracts, decision log, clean-room policy |

## Local checks

```sh
make check          # lint, unit tests, schema validation, licence check
make db-up          # start Postgres via Docker Compose (or point TASKIEM_TEST_DATABASE_URL at any Postgres 16)
make test-db        # integration tests against TASKIEM_TEST_DATABASE_URL
```

Run `make check` before pushing; CI runs the same targets.

## Rules that CI enforces

- Every dependency must pass the licence gate (`make licences`).
- Migrations are forward-only. Never edit a migration that has been merged; add a new one.
- Every tenant-owned table has `tenant_id` and an RLS policy; the integration tests check this.
- Contracts in `schemas/` change only through a new schema version and a decision-log entry.
