# Taskiem fork of github.com/fastschema/qjs v0.0.6

Upstream: https://github.com/fastschema/qjs (MIT, see LICENSE). Vendored because the sandbox needs guarantees the upstream configuration does not expose (decision 0011):

1. **Hard memory cap.** `MemoryLimitPages` sets wazero's `WithMemoryLimitPages` on the shared runtime configuration. QuickJS's own `JS_SetMemoryLimit` does not bound every allocation pattern (repeated string concatenation reached 587 MB RSS against a 16 MB limit in testing).
2. **No filesystem.** No host directory is mounted (upstream mounts `Option.CWD`, defaulting to the process working directory).
3. **A logical clock.** `Option.Walltime`, when set, is the module's wall clock, so `Date` in a code step is the run's logical time, as `host.now()` is (`options.go`).

4. **A cap per runtime.** `Option.MaxMemory`, when set, caps that runtime's linear memory through a wazero memory allocator (`capAllocator` in `runtime.go`), below the process-wide cap; `Runtime.MemoryExhausted` reports that a script asked for more. `JS_SetMemoryLimit` alone does not hold in this build: QuickJS adds `malloc_usable_size` of each allocation to its count, wasi-libc provides none, so it counts a fixed overhead per allocation (a 64 MB limit let 300 MB of 1 MiB arrays through).

It reserves the capped memory up front (`WithMemoryCapacityFromMax`), so growth never copies, and always enables `WithCloseOnContextDone`, so cancelling a run's context interrupts running JavaScript.

Removed from the copy: upstream tests and test data. Everything else, including the prebuilt `qjs.wasm` and its C sources in `qjswasm/`, is unchanged. To update: copy the new upstream release over this directory and re-apply the edits in `runtime.go` and `options.go` marked "Taskiem fork".
