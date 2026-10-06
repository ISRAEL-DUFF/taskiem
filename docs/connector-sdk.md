# Writing your own connector

When Taskiem has no connector for a provider you use, your team can write one. A connector is a **manifest** (what the provider offers: actions, their classes, idempotency, personal fields, webhooks) and a **WebAssembly module** that does the calls. Taskiem runs each call isolated: a fresh instance with capped memory and the step's deadline, no files, and no network except to the hosts the manifest names, through your tenant's egress guard. A bug in your connector can fail its own steps; it cannot crash the engine or see another tenant.

The example in [`examples/wasm-connector`](../examples/wasm-connector) is a complete one: a balance read, an idempotent payment, and a lookup by reference that settles unknown outcomes.

## 1. Write the manifest

The format is [connector/v1](contracts/connector-v1.md), as for built-in connectors. Two rules for your own:

- The `id` starts with `x_` (`x_ledger`), so it never collides with a built-in.
- Classify every write honestly ([action classes](contracts/action-classes.md)). The class, not your code, decides whether Taskiem may send a request again after a timeout. A payment the provider deduplicates by a reference you send is `idempotent_write` with an `idempotency` block naming that field; one you can look up afterwards is `reconcilable_write` with a `reconcile` action; anything else is `unsafe_write`, and Taskiem parks it for a person rather than risk paying twice.

Declare personal fields under `pii` so Taskiem seals them in run history.

## 2. Write the handlers (Go)

```go
package main

import tc "github.com/israel-duff/taskiem/sdk/connectorsdk"

func init() {
	tc.Handle("get_balance", func(r *tc.Request) (any, error) {
		var out struct{ Available int64 `json:"available"` }
		err := tc.DoJSON("GET", r.BaseURL+"/v1/balance",
			map[string]string{"Authorization": "Bearer " + r.Credentials["api_key"]}, nil, &out)
		if err != nil {
			return nil, err
		}
		return map[string]any{"available": out.Available}, nil
	})
}

func main() {}
```

- `r.Input` is the step's input, `r.Credentials` the connection's fields, `r.BaseURL` the manifest's `base_url`. For writes, Taskiem's idempotency key is in `r.IdempotencyKey` and also in the input field your manifest names.
- `tc.Do` and `tc.DoJSON` are the only way out. `DoJSON` classifies statuses as built-in connectors do: 429 and 503 retryable, other 5xx unknown outcome, other 4xx fatal; `tc.StatusOf(err)` gives the status for your own handling (a 409 that means "already done", a 404 that means "not found").
- Return errors wrapped with `tc.Retryable`, `tc.Fatal`, `tc.UnknownOutcome`, `tc.NotSent` or `tc.Indeterminate`, or `tc.NotFound` from a reconcile action. An unwrapped error is an unknown outcome. Whatever you return, Taskiem knows whether a request left, and corrects "not sent" and "unknown" accordingly.
- `tc.Log` writes to the step's log; personal data is masked.

Test the handlers natively: set `tc.TestHTTP` to play the provider and call `tc.Run` (see `examples/wasm-connector/main_test.go`).

Other languages work too (Rust, TinyGo, AssemblyScript): implement the [ABI](contracts/connector-wasm-v1.md).

## 3. Build, check, upload

```sh
taskiem connector build ./my-connector          # GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared
taskiem connector check manifest.yaml connector.wasm
TASKIEM_API_KEY=... taskiem connector push manifest.yaml connector.wasm
```

`check` loads the pair exactly as an upload would, offline. `push` needs an API key with `connector.manage` (owners and admins have it). Uploads are audited with the module's SHA-256. In the web app, Connections → Your connectors lists versions and takes uploads.

A version never changes once uploaded: raise `version` to change anything. New runs use the newest enabled version of each major (`x_ledger@1`); disabling a version hands its major back to the previous one.

## 4. Use it

Add a connection for it (Connections → New connection), then use it like any connector: `"connector": "x_ledger@1"` in a definition, or the step palette, or `connector("x_ledger@1", "get_balance", ...)` in workflow code. Provider webhooks declared under `triggers` work as for built-ins.

Declare output schemas carefully: Taskiem compares every output with them, and a field that changes type, a value outside an `enum`, or a missing `required` field shows under Connections → Contract drift.
