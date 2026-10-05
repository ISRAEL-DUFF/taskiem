# 0011 — Code steps in QuickJS on wazero, via a vendored fork of fastschema/qjs

Date: 2026-10-05 · Status: Accepted

## Decision

Code steps run in QuickJS compiled to WebAssembly on wazero (spec 7), through `github.com/fastschema/qjs` v0.0.6 (MIT), vendored as `third_party/qjs` with three changes (`third_party/qjs/FORK.md`):

1. A hard WebAssembly memory cap (`WithMemoryLimitPages`), reserved up front so growth never copies.
2. No host directory mounted: scripts have no filesystem.
3. Context cancellation always interrupts running code.

TypeScript is stripped and modules bundled with esbuild (MIT). Each run gets a fresh runtime; the compiled QuickJS module is shared. The sandbox deletes QuickJS's `std`, `os`, `print`, and `scriptArgs` globals before user code runs, and exposes only `host.log`, `host.secret` (declared secrets), `host.now` (the run's logical time), and `host.fetch` (egress-guarded, tenant allow-list).

## Why

- QuickJS's own memory limit did not bound repeated string concatenation in testing (587 MB resident against a 16 MB limit). The wazero cap does: measured peak is about 75 MB plus the cap.
- Upstream mounts the process's working directory into the guest by default.
- Upstream's `MaxExecutionTime` is not applied; the time limit is the run's context deadline, which interrupts tight loops (verified) at the cost of a library panic on teardown that the sandbox recovers.
- Building QuickJS to WASI ourselves is planned with the Python sandbox (Phase 2); vendoring a small MIT library now keeps the clean-room position (it is not a competitor product) and the exit path open.

## Measured

Cold run of a trivial script after the module is compiled: under 10 ms. First compilation of the module: about 0.7 s, paid once per process at worker start.

## Provenance

QuickJS (Bellard, MIT) and wazero (Apache 2.0) are public projects; the host API is our own design from spec 7.3.
