# Contract: WebAssembly connector ABI `taskiem-connector/v1`

A tenant's own connector is a [connector/v1 manifest](connector-v1.md) and a WebAssembly module that implements its actions (spec 6). The engine (`engine/wasmconn`, on wazero) runs every call in a fresh instance: memory capped, the call's deadline enforced, no filesystem, no environment, and no network except through the host function below. Go authors use [`sdk/connectorsdk`](../connector-sdk.md), which implements this ABI; the contract is here for other languages.

## Module

- A WASI preview 1 **reactor**: if it exports `_initialize`, the host calls it once per instance before anything else. It may import `wasi_snapshot_preview1` functions (clocks, random; there are no files, arguments or environment variables) and the four `taskiem` functions below. Importing anything else refuses the upload.
- It exports its memory and `taskiem_execute_v1(input_len: i32) -> i64`.
- At most 32 MiB; linear memory at most 128 MiB (2048 pages). A module that declares more refuses the upload.

## One call

1. The host instantiates the module and calls `taskiem_execute_v1(n)`, where `n` is the length of the request JSON.
2. The module allocates `n` bytes and calls `taskiem.input_read(ptr)`; the host copies the request there.
3. The module does its work, making requests with `taskiem.http_request`.
4. It returns `(ptr << 32) | len` of the result JSON in its memory (at most 1 MiB). The host reads it and closes the instance.

### Request

```json
{
  "action": "create_payment",
  "base_url": "https://ledger.example.com",
  "input": { "amount": 5000, "account_number": "0123456789", "reference": "tsk_..." },
  "credentials": { "api_key": "..." },
  "idempotency_key": "tsk_...",
  "attempt": 1,
  "key_first_sent": "2026-10-06T09:14:00Z"
}
```

`base_url` is the manifest's, or the deployment's override (sandboxes). The idempotency key is also in `input` at the field the manifest's `idempotency.field` names, as for built-in connectors. `key_first_sent` is present for writes: when a request carrying this key was first about to be sent (for providers whose duplicate protection expires).

### Result

`{"output": <any JSON>}`, or `{"error": {"kind": KIND, "message": "..."}}` with KIND one of:

| Kind | Means | Engine |
| --- | --- | --- |
| `retryable` | Nothing happened; the same request may be sent again | Retries |
| `fatal` | It will fail the same way again | Fails the step (`on_error`) |
| `unknown_outcome` | It may have happened | By action class: retry with the same key, reconcile, or park |
| `not_sent` | Provably never left | Retries, even an unsafe write |
| `indeterminate` | Only a person can settle it | Parks the step |
| `not_found` | (Reconcile actions) the provider has no record of the effect | Sends again under a new key |

Messages are masked for personal data before they are recorded.

**The host decides whether anything was sent.** All of a module's I/O goes through it, so `not_sent` after a request left becomes `unknown_outcome`, and `unknown_outcome` when no request left becomes `not_sent`. A trap (panic, out of memory) or the deadline is `unknown_outcome` once a request has left; before that a deadline is `not_sent` and a trap is `fatal`.

## Host functions (module `taskiem`)

| Function | Signature | Does |
| --- | --- | --- |
| `input_read` | `(ptr: i32)` | Copies the request JSON to `ptr` |
| `http_request` | `(ptr: i32, len: i32) -> i32` | Sends the request JSON at `ptr`; returns the length of the response JSON |
| `http_response_read` | `(ptr: i32)` | Copies the last response JSON to `ptr` |
| `log` | `(ptr: i32, len: i32)` | Appends a line to the step's log (masked; 16 KiB per call). Standard output and error go there too |

The host never calls into the module while a host function runs.

### HTTP

Request: `{"method": "POST", "url": "https://...", "headers": {"Name": "value"}, "body": "<base64>"}`. Response: `{"status": 201, "headers": {"name": "value"}, "body": "<base64>"}` (header names lower-case, first value), or `{"error": {"kind": "not_sent" | "unknown_outcome" | "fatal", "message": "..."}}` for a transport failure, a host the egress guard refuses (`fatal`), or a body over 4 MiB. Any HTTP status is a response: the module judges it. At most 20 requests per call.

Requests go through the tenant's egress guard to the hosts the manifest declares (`egress_hosts`, else `base_url`'s host): never private, loopback or metadata addresses.

## Upload and versions

`POST /v1/tenant-connectors` (multipart fields `manifest` and `module`; permission `connector.manage`; audited with the module's SHA-256), or `taskiem connector push`. The manifest's `id` must start with `x_`, so a tenant connector can never shadow a built-in. A version never changes once uploaded; raise the version to change it. New runs use the newest enabled version of each major (`x_ledger@1`), as for built-ins, and `POST /v1/tenant-connectors/{id}/{version}/disable` withdraws one (the previous version of that major takes over).
