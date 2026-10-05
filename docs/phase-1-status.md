# Phase 1 status

Phase 1 (build plan) is about 20 weeks: engine, connectors, sandbox, approvals, UI, then dogfooding and a 4-week soak. Started 2026-10-05, while gate G0's people items were still open (clean-room signatures, trademark search, dogfood confirmation; see [Phase 0 status](phase-0-status.md)). Last updated 2026-10-05.

## Milestones

| # | Weeks | Milestone | Status |
| --- | --- | --- | --- |
| 1 | 1–6 | Engine core passes the determinism and crash-recovery suites | **Engine core built; both suites pass at G1 scale** (below) |
| 2 | 4–10 | Connector SDK, first five connectors, sandbox | **Done** (below): Paystack, Dojah, Termii, Postgres connectors plus http steps; JavaScript/TypeScript sandbox; egress guard; encrypted secrets and connections |
| 3 | 7–13 | Canvas, inspector, approvals, audit log | Approval step and decision API in the engine; UI, policies, and audit wiring not started |
| 4 | 13–16 | Dogfood workflows live; bug-fix and load test | Not started |
| 5 | 17–20 | Production soak | Not started |

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

- The database primary killed mid-run (the testing plan lists it; the suite kills workers only).
- Real target hardware, real connectors (sandbox modes), and a synchronous standby.
- Recorded with the load test at 500 steps/s, not just correctness.

## Known gaps carried forward

- `parallel`, `subflow`, `ai` steps fail as unsupported (Phase 2/3 per the build plan).
- `concurrency_key` and plan caps are not enforced yet.
- Long histories: decide re-reads the full history each time; payroll-sized `foreach` needs incremental decision state (spike RESULTS.md).
- Resolving a parked step (`needs_reconciliation`) needs an operator API and events.
- PII envelopes (per-subject encryption) are not written yet; inputs and outputs are stored as plain JSONB.
- The egress guard is in-process; the standalone sidecar proxy with fixed egress IPs is deployment work (spec 14.2).
- Postgres connections to private networks are refused by the SSRF guard; reaching a private database needs a platform-level egress exception (not yet designed).
- Code steps: Python and uploaded WASM modules (Phase 2); `Date.now()` is the real clock, `host.now()` the run's logical time.
- Smile ID is not built (the plan allows Smile ID or Dojah).
- HTTP API, triggers beyond manual start (webhook, schedule, connector events), and the web UI.
