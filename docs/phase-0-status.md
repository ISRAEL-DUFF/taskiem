# Phase 0 status

Phase 0 (build plan) runs about 4 weeks. Gate G0 must pass before Phase 1 starts. Last updated 2026-10-05.

> Everything here that needs people (signatures, owners' confirmations, credentials, hardware, reviews, partners) is tracked in one place: [What needs people](needs-people.md).

## Scope

| Item | Status | Where |
| --- | --- | --- |
| Clean-room policy | Drafted; needs counsel review and signatures | [clean-room-policy.md](clean-room-policy.md) |
| Contributor IP assignment agreements | **Needs a person**: counsel to draft, every contributor to sign | — |
| CI licence scanner with allow-list | Done; Go and npm, blocks CI | `tools/licencecheck`, [decision 0007](decisions/0007-licence-policy.md) |
| Decision log | Started, 8 entries | [decisions/](decisions/README.md) |
| Naming and trademark search | **Needs a person**: search "Taskiem" in NG (and GH, KE if targeted) | [decision 0006](decisions/0006-working-name.md) |
| Monorepo, Go and TS linting, unit test runners | Done | `Makefile`, `.golangci.yml`, `eslint.config.js` |
| SBOM generation and signed builds | Done in CI on `main` (SPDX via Syft, keyless cosign); runs on the first push to `main` | `.github/workflows/ci.yml` |
| Schema v0 with RLS from day one | Done; 13 tables, forced RLS, dispatch functions, fencing, audit chain; 12 integration tests | `engine/db` |
| `wd/v1` JSON Schema | Done, frozen for Phase 1 | `schemas/`, [contracts](contracts/wd-v1.md) |
| `connector/v1` manifest schema | Done, frozen for Phase 1 | `schemas/`, [contracts](contracts/connector-v1.md) |
| Action-class contract | Done; one open question for Phase 1 week 6 | [contracts](contracts/action-classes.md), `engine/effects` |
| Idempotency key contract | Done, with cross-implementation test vectors | [contracts](contracts/idempotency.md), `engine/effects` |
| Engine spike and load test | Done | [RESULTS.md](../engine/spike/RESULTS.md) |
| Dogfood selection | Three drafts written; **needs product owners** to confirm payloads, endpoints, roles | [flows/dogfood](../flows/dogfood/README.md) |

## Gate G0

- [ ] Clean-room policy signed by every contributor. *Waiting on people: policy text is ready.*
- [x] Licence scanner blocking non-allow-listed dependencies in CI. *Verified to fail on a GPL-3.0 package.*
- [x] Engine spike sustains at least 200 steps/s on one Postgres instance. *1,302 steps/s peak on a 4-vCPU host; 500 steps/s sustained at p95 dispatch 4–5 ms.*
- [x] Spike profile names bottlenecks and projects to 500 steps/s. *Thundering herd (fixed), orchestrator claim cost (inline decide recommended); projected about 1,200 steps/s with allowances on 8 vCPU.*
- [ ] Three dogfood workflows written as WD drafts with expected behaviour. *Drafts done and validated; to be ticked when Payrolla, iSpend, and ops owners confirm the assumptions.*

## Carried into Phase 1

- Inline decide and one claimer per process (decision 0002 amendment).
- Incremental decision state for long histories (needed for payroll-sized `foreach`).
- Decide on `effect.duplicates: tolerable` for notification writes (action-class contract).
- WD semantic rules 5–8 and 11 need the CEL type checker (wd-v1 contract).
- Docker image and Compose stack: written and syntax-checked, but not yet built or run (no Docker daemon in the environment where Phase 0 was built).
