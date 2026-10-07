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
| `tools/` | Repository tooling, such as the licence checker and the docs site builder |
| `api/openapi/` | The public API's OpenAPI 3.1 document |
| `docs/` | Spec, build plan, contracts, decision log, clean-room policy, and the guides the docs site publishes |

## Local checks

```sh
make check          # lint, unit tests, schema validation, licence check
make db-up          # start Postgres via Docker Compose (or point TASKIEM_TEST_DATABASE_URL at any Postgres 16)
make test-db        # integration tests against TASKIEM_TEST_DATABASE_URL
```

Run `make check` before pushing; CI runs the same targets.

## Docs and the API reference

The public docs site is built from `docs/` and `api/openapi/openapi.yaml` by `tools/docsite` ([decision 0025](docs/decisions/0025-public-docs-and-api-reference.md)):

```sh
make docs-check     # every relative link and anchor in docs/, README.md and this file resolves
make docs           # check, then build the site into site/ (gitignored)
```

- **A new doc** goes either into the site's navigation or onto its internal list, both in `tools/docsite/pages.go`; a test fails until you choose.
- **Page URLs are stable**: `docs/templates.md` is `/templates`, `docs/integrations/slack.md` is `/integrations/slack`. The web app's help panels link to them (`web/src/lib/onboarding.ts`); a test fails if one is not a public page. Renaming a public doc breaks links people have saved.
- **A new or changed route** goes into `api/openapi/openapi.yaml` in the same change. `api/openapi_test.go` fails when a route is missing or extra, or when the permission, plan feature or authentication the document gives an operation differs from the router's source; and every answer the API tests receive is checked against the document's schemas.
- **Links** between docs are relative (`[billing](billing.md#plans)`). Anchors are GitHub's heading ids.

## Rules that CI enforces

- Every dependency must pass the licence gate (`make licences`).
- Migrations are forward-only. Never edit a migration that has been merged; add a new one.
- Every tenant-owned table has `tenant_id` and an RLS policy; the integration tests check this.
- Contracts in `schemas/` change only through a new schema version and a decision-log entry.
