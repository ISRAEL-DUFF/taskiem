# Taskiem fork of github.com/fastschema/qjs v0.0.6

Upstream: https://github.com/fastschema/qjs (MIT, see LICENSE). Vendored because the sandbox needs two guarantees the upstream configuration does not expose (decision 0011):

1. **Hard memory cap.** `MemoryLimitPages` sets wazero's `WithMemoryLimitPages` on the shared runtime configuration. QuickJS's own `JS_SetMemoryLimit` does not bound every allocation pattern (repeated string concatenation reached 587 MB RSS against a 16 MB limit in testing).
2. **No filesystem.** No host directory is mounted (upstream mounts `Option.CWD`, defaulting to the process working directory).

It reserves the capped memory up front (`WithMemoryCapacityFromMax`), so growth never copies, and always enables `WithCloseOnContextDone`, so cancelling a run's context interrupts running JavaScript.

Removed from the copy: upstream tests and test data. Everything else, including the prebuilt `qjs.wasm` and its C sources in `qjswasm/`, is unchanged. To update: copy the new upstream release over this directory and re-apply the three edits in `runtime.go` marked "Taskiem fork".
