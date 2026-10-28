# Contributing to Taskiem

Before your first commit:

1. Read and sign the [clean-room policy](docs/clean-room-policy.md), and sign an IP assignment agreement.
2. Read the [architecture spec](docs/spec/architecture.md) and the [build plan](docs/spec/build-plan.md).

## Repository layout

| Path | Contents |
| --- | --- |
| `cmd/taskiem` | The single binary: `serve --role` (every role), `migrate`, `validate`, and the operator, connector and workflow commands ([CLI](docs/cli.md)) |
| `cmd/taskiem-shim` | The container-step supervisor copied into sandbox Pods ([container steps](docs/container-steps.md)) |
| `api/` | The HTTP API; `api/openapi/openapi.yaml` is its OpenAPI 3.1 document |
| `engine/` | The engine and its services: database and migrations (`engine/db`), the runtime, connectors, sandboxes, the AI layer, SLOs, and the product areas; `engine/spike` is the Phase 0 spike, kept for `make spike` |
| `connectors/` | First-party connectors: manifests and Go handlers |
| `schemas/` | Contracts: the `wd/v1` and `connector/v1` JSON Schemas |
| `flows/` | Dogfood workflows (`.flow.ts` and the `.wd.json` it compiles to) with their tests |
| `templates/` | The template library |
| `evals/` | The AI evaluation suites and their baselines |
| `e2e/` | Go end-to-end tests of the engine and first-party connectors against fake providers |
| `sdk/` | TypeScript SDK (`@taskiem/sdk`) |
| `web/` | The web app; its browser tests are in `web/e2e` |
| `deploy/` | Docker Compose, the Helm chart and the manifests generated from it, the one-VM kit, Prometheus rules and the Grafana dashboard |
| `third_party/qjs` | Taskiem's fork of the QuickJS bindings (a Go module of its own) |
| `tools/` | Repository tooling: the licence checker, docs site builder, SDK generator, load test and AI evaluation runner |
| `docs/` | Spec, build plan, contracts, decision log, clean-room policy, the guides the docs site publishes, and the engineering atlas (`docs/atlas`) |

## Local checks

```sh
make db-up          # start Postgres via Docker Compose (or point TASKIEM_TEST_DATABASE_URL at any Postgres 16)
make check          # lint, every Go test (the database ones included), schema validation, licence gate, TypeScript
make test-db        # every Go test again, uncached
make e2e            # the browser tests against the real binary (needs psql and Chromium)
make db-down        # stop the database
```

The Makefile exports `TASKIEM_TEST_DATABASE_URL` (default `postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable`), so `make check` runs the database tests and fails if no database answers. `make check TASKIEM_TEST_DATABASE_URL=` skips them.

`make lint` needs golangci-lint **v2.15.0**, the version CI pins; an older binary may refuse the configuration (one built with an older Go stops with "lower than the targeted Go version").

Run `make check` before pushing. It covers the main steps of CI's `go` and `typescript` jobs and the licence gate. CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) also runs, on every pull request:

- the public pages again under each theme, the secrets tests against OpenBao's transit engine, the Prometheus rule tests with `promtool`, the MySQL connector against a MySQL 8.4 server, the docs site build, and `taskiem migrate` on a clean database (`go` job); lint and tests of the QuickJS fork in `third_party/qjs`, a module of its own;
- the race detector over `engine/runtime`, `engine/decide` and `engine/effects` (`race` job), and the database crash suite (`chaos` job, not yet a gate);
- the website stylesheet check (`pnpm site:css`, then `git diff`) (`typescript` job);
- the browser suite, as `make e2e` (`web` job);
- the image, built once, then the Compose stack and the whole one-VM deployment on it (`docker` and `vps` jobs);
- the Helm chart and the manifests generated from it (`make chart`, then `git diff`), and the VM Compose file (`deploy` job);
- the AI evaluation report (`ai-eval` job; the gate itself is `TestBaselinesHold` in `go test`);
- the forward-only migration check below (`migrations` job);
- every reference in the engineering atlas resolves (`node docs/atlas/atlas.mjs check`), and a change to its pages comes with the rebuilt `docs/atlas/site/index.html` (`atlas` job).

On a merge to main or a `v*` tag it also signs and pushes the image and a signed binary; a `v*` tag must name a commit on main.

## Docs and the API reference

The public site is built by `tools/docsite`: the product website at the root ([structure and copy](docs/strategy/website.md); the prose in `tools/docsite/assets/www/`, the lists from `templates/library`, `connectors/*/manifest.yaml` and `deploy/plans.yaml`) and, under `/docs/`, the docs site from `docs/` and `api/openapi/openapi.yaml` ([decision 0025](docs/decisions/0025-public-docs-and-api-reference.md)):

```sh
make docs-check     # every relative link and anchor in docs/, README.md and this file resolves
make docs           # check, then build the site into site/ (gitignored)
make docs SITE_APP=https://app.example.ng SITE_WHATSAPP=2348000000000 SITE_EMAIL=hello@example.ng
```

- **The website is the designer's pages** (`tools/docsite/assets/www/`, Tailwind markup) with its stylesheet compiled by `pnpm site:css` from `tools/docsite/tailwind/` and committed as `tools/docsite/assets/www.css`; CI fails if it is stale, so run `pnpm site:css` (or `make docs`) after changing a page. Prices come from `deploy/plans.yaml`; the claims register in [the website page](docs/strategy/website.md#claims-register) lists what the copy asserts that still has to be made true.
- **A new doc** goes either into the site's navigation or onto its internal list, both in `tools/docsite/pages.go`; a test fails until you choose.
- **Page URLs are stable**: `docs/templates.md` is `/docs/templates`, `docs/integrations/slack.md` is `/docs/integrations/slack` (`-site=false` builds the docs alone at `-base`). The web app's help panels link to them (`web/src/lib/onboarding.ts`); a test fails if one is not a public page. Renaming a public doc breaks links people have saved.
- **A new or changed route** goes into `api/openapi/openapi.yaml` in the same change. `api/openapi_test.go` fails when a route is missing or extra, or when the permission, plan feature or authentication the document gives an operation differs from the router's source; and every request body the API tests send, and every answer they receive, is checked against the document's schemas (a test sending an invalid body on purpose either gets a 400, 413, 415 or 422, which is not checked, or marks the request with `invalidBody`). Every operation has a full request and response schema: write it from the handler's Go types (shared shapes under `components/schemas`, `additionalProperties: false` where the handler answers a closed struct, `type: [x, 'null']` for pointers and slices that can be nil), list the error statuses it answers with a specific meaning, and make sure a test receives its success answer so the schema is checked.
- **Links** between docs are relative (`[billing](billing.md#plans)`). Anchors are GitHub's heading ids.

## Migrations

Migrations are goose SQL files in `engine/db/migrations`, applied in order by `taskiem migrate` and by every database test.

- **Forward-only.** Never edit, rename or delete a migration that has been merged; add a new one. CI's `migrations` job fails a pull request that changes a file under `engine/db/migrations/` that its base branch has (adding files is fine).
- **New indexes on large tables** are built with `CREATE INDEX CONCURRENTLY`, so writes to the table are not blocked while the index builds. `CONCURRENTLY` cannot run inside a transaction, so that migration starts with `-- +goose NO TRANSACTION` and holds only statements that may run outside one. A failed concurrent build leaves an invalid index behind: drop it before the migration runs again.

## Rules that CI enforces

- Every dependency, and every licence file of code copied into the repository, must pass the licence gate (`make licences`, `tools/licencecheck/policy.json`).
- Migrations are forward-only (above).
- Every tenant-owned table has `tenant_id` and an RLS policy; the database tests check this.

## Rules that review enforces

- Contracts in `schemas/` change only through a new schema version and a decision-log entry ([contracts](docs/contracts/README.md) describes the exceptions allowed during Phase 1). No check compares a schema with the version before it.
