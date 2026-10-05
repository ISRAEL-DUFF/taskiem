# Phase 1 status

Phase 1 (build plan) is about 20 weeks: engine, connectors, sandbox, approvals, UI, then dogfooding and a 4-week soak. Started 2026-10-05, while gate G0's people items were still open (clean-room signatures, trademark search, dogfood confirmation; see [Phase 0 status](phase-0-status.md)). Last updated 2026-10-05 (milestone 4, engineering part).

## Milestones

| # | Weeks | Milestone | Status |
| --- | --- | --- | --- |
| 1 | 1–6 | Engine core passes the determinism and crash-recovery suites | **Engine core built; both suites pass at G1 scale** (below) |
| 2 | 4–10 | Connector SDK, first five connectors, sandbox | **Done** (below): Paystack, Dojah, Termii, Postgres connectors plus http steps; JavaScript/TypeScript sandbox; egress guard; encrypted secrets and connections |
| 3 | 7–13 | Canvas, inspector, approvals, audit log | **Done** (below): HTTP API with identity and RBAC, approvals with separation of duties, audit log with an independent verifier, triggers and ingest, single-binary roles with telemetry, Docker Compose, and the web app. All three dogfood workflows run end to end in tests |
| 4 | 13–16 | Dogfood workflows live; bug-fix and load test | **Engineering part done** (below): load test of the real engine, database-crash suite, the bugs they found fixed. **Going live needs people and production**: product owners to confirm the drafts, holdco production access, provider sandbox and live credentials, target hardware |
| 5 | 17–20 | Production soak | Not started (follows milestone 4) |

## Milestone 1: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Expressions | CEL with cost limits, deterministic, JSON-safe; secret references deferred to the worker | `engine/expr` |
| Definitions | Typed WD model; validator enforces "read only settled steps" and secret-only expressions | `engine/wd`, decisions 0010 |
| Orchestrator | Pure `decide()`: connector, http, transform, wait, signal, approval (timeouts, escalation), branch, foreach (concurrency caps), retries with deterministic backoff, `on_error`, compensation, run timeout | `engine/decide` |
| Runtime | Inline decide in every appending transaction; tasks, timers, signal waits and buffering; trigger dedup; approvals; cancel | `engine/runtime` (store) |
| Worker | One claimer per process; EffectIntent before writes; derived keys; open-intent handling; reconcile; parking; fencing and heartbeats; call deadlines | `engine/runtime` (worker), decision 0009 |
| Scheduler | Timers, lease recovery, undecided-run sweep, partitions | `engine/runtime` (scheduler) |
| Executors | Connector actions (spec 6.3) and http steps with transport error classification | `engine/connector`, `engine/runtime` |
| Schema | Migration 00007: event origins, signal waits, buffered signals | `engine/db/migrations` |

## Milestone 2: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Connectors | Paystack (balance, transfer with duplicate-reference handling, verify, webhook HMAC-SHA512), Dojah (BVN, NIN, balance), Termii (SMS as `unsafe_write`, balance), Postgres (read-only `query`, `execute`); HTTP is the `http` step. Each ships recorded fixtures replayed in CI | `connectors/`, `connectors/builtin` |
| Connector SDK | Runtime interface (spec 6.3), registry, classified JSON HTTP helper, generic webhook verification, guarded TCP dialer for non-HTTP connectors | `engine/connector` |
| Sandbox | QuickJS on wazero with hard memory cap, wall-clock limit, no filesystem or stdlib, fresh runtime per run; TypeScript via esbuild; `host.log/secret/now/fetch`; sandbox worker queue | `engine/sandbox`, `third_party/qjs`, decision 0011 |
| Egress | Per-call allow-lists (connectors: manifest hosts; http steps and sandbox fetch: tenant rules per environment); SSRF blocking incl. metadata, IPv4-mapped, DNS rebinding; guarded redirects; logged | `engine/egress` |
| Secrets | Envelope encryption: AES-256-GCM per value, tenant KEKs wrapped by a KMS root key (local or OpenBao transit, tested against OpenBao 2.4.1), rotation re-wraps data keys, audited | `engine/secrets` |
| Configuration | Connections with encrypted credentials, tenant variables snapshotted into each run, egress rules | migration 00008, `engine/runtime/config.go` |
| Dogfood | The iSpend top-up reconciliation flow runs end to end against real Postgres and fake Paystack/iSpend servers | `e2e/` |

## Milestone 3: what exists

| Area | Deliverable | Where |
| --- | --- | --- |
| Engine completions | `concurrency_key` and `max_concurrency` with queued runs admitted in order (`RunAdmitted`); operator resolution of parked steps (`completed`, `failed`, `retry`), audited; retention from run end with archive-then-purge and empty-partition drops; declared PII sealed per data subject before write, taint-sealing of copies, erasure by key destruction | `engine/runtime`, `engine/pii`, `engine/secrets`, migrations 00009–00010 |
| API | REST API (`/v1`): password sign-in (argon2id, rate-limited), hashed session tokens, scoped and environment-limited API keys, CSRF header for cookie sessions, built-in roles (spec 13.3); workflows, immutable versions, canvas layout, validation and publish; idempotent run start checked against the inputs schema; run history sealed unless revealed (audited); cancel; resolve; connections, secrets (write-only), variables, egress; members and keys; audit list, verify and export; erasure | `api/`, migration 00011 |
| Approvals | Inbox per role; votes recorded in one transaction with the decision: role required, maker-checker (whoever started the run or wrote or published the version cannot approve), distinct approvers, `count` approvals approve and any rejection rejects; what the approver was shown is stored with the vote | `engine/runtime/approvals.go`, `api/approvals.go` |
| Audit | Every security-relevant action appended to the per-tenant hash chain; `taskiem audit verify` recomputes an export independently of Postgres, byte for byte, and catches edits and truncation | `engine/audit`, `cmd/taskiem` |
| Triggers | Webhooks (HMAC or bearer; dedup expression or body hash; inputs schema; per-tenant ceiling with 429), connector webhooks verified by manifest (Paystack HMAC-SHA512) delivering deduplicated signals and starting subscribed workflows, cron schedules in Africa/Lagos deduplicated per scheduled time; registered from the published version in the publishing transaction | `engine/ingest`, migration 00012, decision 0012 |
| Operations | `taskiem serve --role api\|edge\|orchestrator\|scheduler\|worker\|all` configured from the environment, graceful shutdown with a worker drain window; Prometheus metrics (requests, ingest, steps, queue depth and age, timers, lease expiries, sweeps); OpenTelemetry traces over OTLP; `taskiem bootstrap`; Docker image with the web app; Compose stack with OpenBao transit, verified end to end | `cmd/taskiem`, `engine/telemetry`, `deploy/`, `Dockerfile`, [operations](operations.md) |
| Web app | Sign-in; workflow list; canvas editor (React Flow) with step palette, dependency editing, connector input forms generated from manifest schemas, trigger and run settings, JSON view, published endpoints; run list and inspector; approvals inbox; connections; secrets, variables, egress; audit log with verification and export; members and API keys | `web/` |

**Dogfood workflows, end to end in tests** (`e2e/`): the Payrolla disbursement runs through the API and the edge (signed webhook, duplicate delivery returns the same run, balance check, maker-checker approval in which the workflow's author is refused, three transfers with idempotency seeds, one settled later by a signed Paystack webhook, report posted to Payrolla, recipient codes sealed at rest, replay verified); the ops alert starts from a Paystack `transfer.failed` webhook, ignores the retry and `transfer.success`, and sends one SMS through Termii and one Slack post; the iSpend reconciliation runs as in milestone 2.

**Browser test** (`web/e2e`, CI job `web`): signs in, creates a workflow, adds a code step on the canvas, publishes, starts a run, sees it complete in the inspector, and verifies the audit chain, against the real binary and a fresh database.

## Suites (build plan, "Testing")

**Determinism.** `decide.Verify` replays a recorded history and checks every block of decide-written events, byte for byte in canonical JSON. It runs on every history the crash and determinism suites produce, and on every simulator test.

**Crash recovery** (`engine/runtime/suites_test.go`, `TASKIEM_CHAOS_RUNS`). Payment-shaped runs (an idempotent transfer, a reconcilable bank transfer against a provider that ignores keys, a foreach of three transfers) under 8% provider failures before execution, 8% unknown outcomes after execution, 4% worker crashes after intent and after the call, 1% stalls past the lease, and a worker process killed every 300 ms.

| Runs | Effects | Crashes | Stalls | Process kills | Duplicates | Lost | Replay mismatches |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 10,000 | 50,000 | 5,033 | 637 | 229 | **0** | **0** | **0** |
| 1,000 | 5,000 | 531 | 67 | 23 | 0 | 0 | 0 |
| 120 (CI default) | 600 | 59 | 9 | 10 | 0 | 0 | 0 |

The suite has teeth: with a provider that ignores keys on the idempotent transfer it reports duplicates immediately, and with the call-deadline guard removed it reports duplicate bank transfers under stalls.

**Unsafe writes.** Notifications with no key and no status API: under injected unknown outcomes, runs park in `needs_reconciliation` and no effect ever repeats.

### What G1's crash criterion still needs

- Real target hardware, real connectors (sandbox modes), and failover to a synchronous standby (the database-crash suite restarts one instance).
- Recorded with the load test at 500 steps/s, not just correctness.

## Milestone 4: what could be done before production

**Load test of the real engine** (`tools/loadtest`, `make load`): `StartRun` with its first decision, one-claimer workers, decide inline in every completion, the scheduler's sweep, all as in production, against a fresh database; a no-op connector action stands in for providers and timestamps each execution. Same host as the Phase 0 spike: shared 4-vCPU container, 15 GB, Postgres 16.14 stock (`shared_buffers` 128 MB, `synchronous_commit` on), load generator on the same host; 20 tenants, 5 sequential connector steps per run.

| Mode | Offered | Achieved | Dispatch p50 / p95 / p99 | Commits per step | Host CPU busy |
| --- | --- | --- | --- | --- | --- |
| Open loop, 60 s | 500 steps/s | 500 steps/s | 3.9 / **5.4** / 6.9 ms | 3.4 | 54% |
| Closed loop, 4,000 runs at once | — | 1,000 steps/s | (backlog) | 3.3 | 89% |

Dispatch latency is measured from the `StepScheduled` event to the moment a worker starts the step. G1 asks for 500 steps/s at p95 under 50 ms on the target hardware: met here with a wide margin, on weaker hardware. The real engine peaks at about 77% of the spike (1,302 steps/s): it loads definitions, re-reads history to decide, and seals and audits.

**Database-crash suite** (`TestDatabaseCrashSuite`, opt-in with `TASKIEM_CHAOS_PG_CRASH`, a command that stops Postgres with an immediate shutdown, as if the primary were killed, and starts it again): payment-shaped runs with light provider faults, Postgres crashed at progress points.

| Runs | Effects | Database crashes | Duplicates | Lost | Not completed | Replay mismatches |
| --- | --- | --- | --- | --- | --- | --- |
| 1,000 | 5,000 | 4 | **0** | **0** | 0 | 0 |
| 200 | 1,000 | 3 | 0 | 0 | 0 | 0 |

**Bugs found and fixed in this milestone**

- Workers never re-established their `LISTEN` connection after the database restarted, so they fell back to polling once a second for the life of the process. The listener now reconnects with backoff and wakes the claimer after reconnecting.
- The Docker image had not built since the vendored QuickJS fork (milestone 2): `go mod download` ran before `third_party/` was copied.
- Retention could not purge a run that had an approval (foreign key from `approvals`); purge now removes approvals and decisions with the run.
- Workers cancelled in-flight provider calls on shutdown, leaving outcomes unknown; they now drain for up to 30 s.
- `chi`'s `RealIP` middleware trusted any client's `X-Forwarded-For`, which would have let anyone dodge the sign-in rate limit; replaced by an opt-in, last-hop-only `TASKIEM_TRUST_PROXY`.

## G1 gate: where each item stands

| Item | Status |
| --- | --- |
| Three dogfood workflows in production for 4 weeks | Built and passing end to end against fake providers; needs owners' confirmation of the drafts, production access, and live or sandbox credentials |
| Chaos: 10,000 payment-shaped runs, zero duplicate and zero lost effects | Passed with worker crashes, stalls and process kills at 10,000 runs, and with database crashes at 1,000 runs (tables above); a synchronous-standby failover needs the target setup |
| 500 steps/s sustained, p95 dispatch under 50 ms | The real engine sustains 500 steps/s at p95 5.4 ms on a 4-vCPU development host; to be repeated on the target hardware |
| Audit chain verifies end to end with the CLI verifier | **Done**: `taskiem audit verify` on an export from `GET /v1/audit/export` |
| No open critical findings from an internal review | Needs the review |

## Known gaps carried forward

- `parallel`, `subflow`, `ai` steps fail as unsupported (Phase 2/3 per the build plan).
- Plan caps and soft ingest limits that queue runs (spec 8.3, 16); the hard per-tenant ingest ceiling exists.
- Long histories: decide re-reads the full history each time; payroll-sized `foreach` needs incremental decision state (spike RESULTS.md).
- The egress guard is in-process; the standalone sidecar proxy with fixed egress IPs is deployment work (spec 14.2).
- Postgres connections to private networks are refused by the SSRF guard; reaching a private database needs a platform-level egress exception (not yet designed).
- Code steps: Python and uploaded WASM modules (Phase 2); `Date.now()` is the real clock, `host.now()` the run's logical time.
- Smile ID is not built (the plan allows Smile ID or Dojah).
- Sign-in is password-only: no passkeys, SSO, step-up for approvals, or password reset yet; users are added by an admin.
- Workers read secrets through the vault directly; per-secret read auditing (`secret.read`) is not recorded per use.
- The web app edits nested steps as JSON, and has no live canvas view (Phase 2).
- Webhook `mtls` and synchronous `respond` are refused at publish until the edge proxy exists.
- The unsafe-write alert question from Phase 0 (`effect.duplicates: tolerable`) is still open; the ops alert parks on an unknown SMS or Slack outcome.
