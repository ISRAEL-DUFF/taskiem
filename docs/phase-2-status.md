# Phase 2 status

Phase 2 (build plan) is about 12 weeks: it makes the platform fit for the first external design partners. Started 2026-10-06, while Phase 1's go-live and soak (milestones 4 and 5) wait on people and production; see [Carried over from Phase 1](#carried-over-from-phase-1). Last updated 2026-10-06.

## Milestones

The build plan lists Phase 2's deliverables without an order. This is the order they are built in: each milestone gives the next something to stand on (tests before Git-led deploys, policies before four-eyes publishing, the WASM runtime before third-party connectors).

| # | Weeks | Milestone | Deliverables (spec) | Status |
| --- | --- | --- | --- | --- |
| 1 | 1–3 | Engine and local tooling | `parallel` step (3.2); workflow tests with mocked connector outputs (10.5); CLI: `validate`, `test`, `diff`, `deploy`, `runs tail`, `dev` (10.4) | **Done** (below) |
| 2 | 2–6 | Code and Git | TypeScript SDK and compiler to WD; deterministic codegen from the canvas; three-way merge on parent digest (10.1, 10.2); GitHub and GitLab, platform-led and Git-led (10.3) | **In progress**: SDK, compiler, code generation, the editor's Code tab, and three-way merge done (below); Git next |
| 3 | 3–8 | Governance and privacy | Policy objects: amount rules, multi-level approvers, escalation, delegation, step-up with passkey or TOTP; four-eyes on production publishing and policy edits (9.1); Nigerian-identifier PII detectors, redaction everywhere, `pii.reveal` (9.3); per-workflow retention (9.4); compliance reports and audit-chain anchoring (9.2, 9.6) | Not started |
| 4 | 4–10 | Connectors | WASM runtime for third-party connectors; contract-drift monitor (6.3, 6.4); 13 African connectors and 7 global ones (6.5) | Not started |
| 5 | 6–11 | Identity and operations | Passkeys by default for admins; SSO (OIDC, SAML); SCIM; custom roles (13.2, 13.3); staging environments, alerts to email and Slack, dashboards, live run view on the canvas (15.1); Python sandbox (7.1) | Not started |
| 6 | 11–12 | External readiness | First external penetration test (14.4); design partners onboarded | Needs people |

### What engineering cannot finish alone

- **Connectors** are built from each provider's public documentation (clean-room policy) and tested against recorded fixtures. The gate's nightly sandbox checks need a sandbox account per provider.
- **SSO, SCIM, and Git apps** need a GitHub App, a GitLab application, and test identity providers registered by the organisation.
- **Gate G2** needs design partners, an external penetration test, and a partner's compliance sign-off.

## Milestone 1: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Engine | `parallel` step: branches run side by side under `max_concurrency`. `join: all` fails once in-flight steps settle after a branch fails. `join: any` records the winning branch in history, cancels the losers' unfinished steps (new `StepCancelled` event: timers, signal waits, approvals and unclaimed tasks removed), waits for a loser write already under way, and compensates the losers' committed effects before completing | `engine/decide`, `engine/runtime`, migration 00014, [wd/v1 contract](contracts/wd-v1.md) |
| Worker | A worker never starts a cancelled write: it checks for cancellation under the run lock before recording intent | `engine/runtime/worker.go` |
| Fix | A failing `foreach` waited forever when another iteration had steps it would never start; failing control steps now wait only for steps in flight | `engine/decide` |
| Web app | `parallel` in the step palette, node summaries, and run timeline (`cancelled`) | `web/` |
| Workflow tests | `wd-test/v1`: trigger, mocked step outcomes (per attempt, by step or instance), signals, approval decisions; assertions on status, step states, outputs, what each step sent, and attempt counts. Runs the real orchestrator on a virtual clock; mocked errors are classified by the real action classes. The three dogfood workflows have 12 cases, run by `go test` | `engine/wdtest`, `flows/dogfood/tests/`, [contract](contracts/wd-test-v1.md) |
| CLI | `validate` (now the same checks as publishing, shared with the API), `test`, `diff`, `deploy`, `runs tail`, `dev` (private Postgres cluster, bootstrap, hot reload that tests before it deploys) | `cmd/taskiem`, `engine/wdcheck`, [CLI guide](cli.md) |
| API | Workflow listings carry the definition's id (`key`), which the CLI matches local files by | `api/workflows.go` |

`taskiem dev` uses the Postgres installed on the machine rather than downloading binaries at run time; `--dsn` covers machines without it. Recorded connector fixtures in `dev` are not wired yet: workflows that call providers need sandbox credentials or test mocks.

## Milestone 2: what exists so far

| Area | Deliverable | Where |
| --- | --- | --- |
| SDK | `@taskiem/sdk`: `workflow(...).step()/.next()`, helpers for every step type and trigger, typed helpers for every built-in connector action (generated from the manifests; a test fails when they are stale) | `sdk/src`, `tools/sdkgen`, [SDK guide](sdk.md) |
| Expressions | Arrow functions over `{ trigger, steps, env, run, secrets, item, index }` compile to CEL; constructs without a CEL meaning are refused with the field and the alternative. Recognises the SDK's `cel` helpers after bundlers rename them | `sdk/src/expr.ts` |
| Code generation | Definition to `.flow.ts`, deterministic and stable. An expression becomes an arrow function only when it compiles back to the identical CEL, so generated code always builds to its definition. All workflows in `flows/` have their code committed and are checked both ways (`go test`, `pnpm test`), and `tsc` checks the code | `sdk/src/codegen.ts`, `flows/**/*.flow.ts` |
| In the binary | esbuild bundles flow code with the embedded SDK and QuickJS runs it: `taskiem build` (`--check` for CI), `taskiem codegen`, `validate` and `dev` accept `.flow.ts`. Identical output to Node | `engine/flowcode`, `sdk/sdk.go`, `cmd/taskiem` |
| API and web app | `POST /v1/code/generate` and `/v1/code/compile` (sandboxed, SDK imports only, checked as publishing would); the editor's Code tab shows the workflow as code and applies edits | `api/code.go`, `web/src/pages/Editor.tsx` |
| Concurrent edits | A save names its parent version's digest. When another version landed since, the edits merge three ways by top-level field and top-level step (nested steps included): different parts merge, the same part changed twice is a conflict (409 listing each, with both sides). The editor shows a dialog to keep either side; `deploy` sends the version it compared with | `engine/wdmerge`, `api/workflows.go`, editor `MergeDialog`, `web/e2e/merge.spec.ts` |

Definitions are stored as `jsonb`, which reorders keys, so code generated from a stored version lists object keys in Postgres's order rather than the author's. Git sync will carry the author's file.

## Carried over from Phase 1

Kept pending while Phase 2 is built. Phase 1's milestones 4 and 5 and gate G1 stay open until these are done ([Phase 1 status](phase-1-status.md)):

1. Deploy Taskiem with a public URL and enter production iswallet credentials (Connections and Secrets pages).
2. Generate webhook URLs and signing secrets; register them with iswallet and Payrolla.
3. Payrolla implements the [integration guide](integrations/payrolla.md).
4. iSpend builds `POST /internal/wallet-credit-exceptions`, honouring `Idempotency-Key`.
5. Load and failover test on the target hardware; internal security review.
