# Taskiem

A workflow automation engine that regulated businesses can trust with money movement: durable, replayable runs with exactly-once external effects, compliance controls built in, and first-class African integrations. Built in Go on PostgreSQL and hosted in Nigeria.

*Taskiem is a working name, pending a trademark search.*

## Status: Phase 1 (Internal MVP)

See [Phase 1 status](docs/phase-1-status.md). The engine core passes the determinism and crash-recovery suites at G1 scale. Phase 0's remaining people items are in [Phase 0 status](docs/phase-0-status.md).

| Start here | |
| --- | --- |
| [Architecture specification](docs/spec/architecture.md) | What we are building and why |
| [Phased build plan](docs/spec/build-plan.md) | Phases, gates, staffing |
| [Contracts](docs/contracts/README.md) | `wd/v1`, `connector/v1`, action classes, idempotency keys |
| [Decision log](docs/decisions/README.md) | Every design decision, and where its ideas came from |
| [Clean-room policy](docs/clean-room-policy.md) | Read and sign before your first commit |
| [Contributing](CONTRIBUTING.md) | Layout, local checks, rules CI enforces |

## Quick start

Requires Go 1.26+, Node 22.13+ with pnpm 10, and PostgreSQL 16 (or Docker).

```sh
make db-up                              # Postgres in Docker on :5432
go run ./cmd/taskiem migrate --dsn postgres://postgres:postgres@127.0.0.1:5432/taskiem?sslmode=disable
go run ./cmd/taskiem validate flows/dogfood/*.wd.json connectors/*/manifest.yaml
make check                              # lint, tests, contract validation, licence gate, TypeScript
make test-db                            # integration tests: RLS, fencing, audit chain
make spike                              # Phase 0 engine load test at 500 steps/s
```
