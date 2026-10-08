# Connector SDK

When Taskiem has no connector for a provider you use, you can write one: for your own organisation, or for every Taskiem customer through the public catalogue. A connector is a **manifest** (what the provider offers: actions, their classes, idempotency, personal fields, webhooks) and a **WebAssembly module** that does the calls. Taskiem runs each call isolated: a fresh instance with capped memory and the step's deadline, no files, and no network except to the hosts the manifest names, through the tenant's egress guard. A bug in a connector can fail its own steps; it cannot crash the engine or see another tenant.

| | Your own connector | A catalogue connector |
| --- | --- | --- |
| Id | `x_<name>` (`x_ledger`) | `p_<publisher>_<name>` (`p_acme_ledger`), in your verified namespace |
| Who can use it | Your organisation (and, for partners, sub-tenants you share it with) | Any organisation that installs it |
| How it gets there | `taskiem connector push`, or Connections → Your connectors | A signed package, automated checks, a review by Taskiem, then you publish ([connector submissions](connector-submissions.md)) |
| Versions | Newest enabled version of each major | Pinned per installing organisation; upgrades are explicit |

The example in [`examples/wasm-connector`](../examples/wasm-connector) is a complete one: a balance read, a payment the ledger deduplicates by reference, a lookup by reference that settles unknown outcomes, recorded fixtures and conformance cases. `taskiem connector init` starts from it.

## 1. Start

```sh
taskiem connector init ./ledger                         # your own: x_ledger
taskiem connector init --publisher acme ./ledger        # for the catalogue: p_acme_ledger
```

`init` copies the example under the new id: `main.go` (handlers), `main_test.go` (a native test), `manifest.yaml`, and `testdata/` with fixtures and conformance cases. Outside a Go module it writes a `go.mod` and tells you to fetch the SDK (`go get github.com/israel-duff/taskiem/sdk/connectorsdk@<version>`).

## 2. Write the manifest

The format is [connector/v1](contracts/connector-v1.md), as for built-in connectors. Rules that matter most:

- **Classify every write honestly** ([action classes](contracts/action-classes.md)). The class, not your code, decides whether Taskiem may send a request again after a timeout. A payment the provider deduplicates by a reference you send is `idempotent_write` with an `idempotency` block naming that field; one you can look up afterwards is `reconcilable_write` with a `reconcile` action; anything else is `unsafe_write`, and Taskiem parks it for a person rather than risk paying twice.
- **Declare your hosts.** `base_url` (https), and `egress_hosts` when the connector reaches more than one host. Nothing else is reachable.
- **Declare personal fields** under `pii`, in inputs and outputs, so Taskiem seals them in run history.
- Webhook triggers must verify their deliveries (a signature or secret scheme; `none` is refused).

`taskiem connector validate` applies the strict lint the catalogue uses, and you should hold your own connectors to it too:

```sh
taskiem connector validate manifest.yaml connector.wasm
```

| Check | Error when |
| --- | --- |
| Identity | The id is not `x_...`, or (catalogue) not `p_<publisher>_<name>` at most 63 characters |
| Classes | A `read` action is named or titled like a change (`create_`, `send_`, `transfer`, `pay`, `refund`, `delete_`...): reads are retried freely |
| Hosts | No host; `base_url` not https; a host that is an IP address, `localhost`, or a bare name; (catalogue) a wildcard or a private suffix (`.local`, `.internal`, `.test`...) |
| Personal data | An input or output field whose name holds personal data (`name`, `email`, `phone`, `bvn`, `nin`, `account_number`, `address`, `date_of_birth`...) is not under `pii` |
| Triggers | A trigger does not verify its deliveries |
| Module | It imports anything but WASI and the four host functions, does not export `taskiem_execute_v1`, imports memory, starts with more memory than the cap, or is over 32 MiB |

Warnings (shown, not blocking): an `unsafe_write` (it will park on timeouts), no `auth.test` action, an idempotency field the input schema does not declare, a missing description for your own connector.

## 3. Write the handlers (Go)

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

- `r.Input` is the step's input, `r.Credentials` the connection's fields, `r.BaseURL` the manifest's `base_url`. For writes, Taskiem's idempotency key is in `r.IdempotencyKey` and also in the input field your manifest names. **Send it to the provider**: the conformance kit checks that it arrives.
- Build every URL from `r.BaseURL` (or a host in `egress_hosts`). `tc.Do` and `tc.DoJSON` are the only way out. `DoJSON` classifies statuses as built-in connectors do: 429 and 503 retryable, other 5xx unknown outcome, other 4xx fatal; `tc.StatusOf(err)` gives the status for your own handling (a 409 that means "already done", a 404 that means "not found").
- Return errors wrapped with `tc.Retryable`, `tc.Fatal`, `tc.UnknownOutcome`, `tc.NotSent` or `tc.Indeterminate`, or `tc.NotFound` from a reconcile action. An unwrapped error is an unknown outcome. Whatever you return, Taskiem knows whether a request left, and corrects "not sent" and "unknown" accordingly.
- `tc.Log` writes to the step's log; personal data is masked.

Test the handlers natively with `go test`: set `tc.TestHTTP` to play the provider and call `tc.Run` (see `examples/wasm-connector/main_test.go`).

Other languages work too (Rust, TinyGo, AssemblyScript): implement the [ABI](contracts/connector-wasm-v1.md).

## 4. Record fixtures and conformance cases

The conformance kit replays recorded provider exchanges against your **compiled module, in the same sandbox Taskiem runs it in**, with no network but a replay server. Fixtures use the format built-in connectors use (`testdata/fixtures/<name>.json`):

```json
{
  "request": { "method": "POST", "path": "/v1/payments", "headers": { "Authorization": "Bearer test-key" },
               "body": { "amount": 5000, "account_number": "0123456789", "reference": "tsk_7w2x..." } },
  "response": { "status": 201, "body": { "id": "pay_1", "reference": "tsk_7w2x...", "status": "pending" } }
}
```

A request matches on method and path, and on the query parameters, headers and JSON body you give (others are free). A case (`testdata/conformance/<name>.json`) is one action call:

```json
{
  "action": "create_payment",
  "input": { "amount": 5000, "account_number": "0123456789" },
  "credentials": { "api_key": "test-key" },
  "idempotency_key": "tsk_7w2x...",
  "exchanges": ["payment_duplicate", "payment_found"],
  "expect": { "output": { "id": "pay_1", "status": "completed" } }
}
```

- `exchanges` are fixture names, or inline `{request, response}` objects, in the order the call must make them; an unexpected request, a mismatch or a request that never came fails the case.
- `expect` is an `output` (every field given must match; others may be present) or an `error` kind: `retryable`, `fatal`, `unknown_outcome`, `not_sent`, `indeterminate`, `not_found`.
- For a write with an `idempotency` block, `idempotency_key` is placed in the input field the manifest names, as the engine does (derived from the case name when left out).
- Use values that are not anyone's real data. Record from the provider's sandbox, then replace keys, names and numbers.

```sh
taskiem connector build ./ledger      # GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared
taskiem connector test -v ./ledger
```

`test` fails when a case fails, and reports what a reviewer will ask about:

- **Coverage**: actions no case calls (the catalogue needs at least one case per action).
- **Key not sent**: an idempotent write whose key never reached the provider in a passing case. The class promises a deduplication the module does not do.
- **Reads that write**: a read action that sent other than GET or HEAD. Often fine (a search by POST); a reviewer checks.
- **Drift**: an output that departs from the declared schema (a type, an enum value, a required field), exactly as the [drift monitor](#6-use-it) would see it in production.
- A request to any host but the replayed provider is refused, so a module that ignores `base_url` fails.

Cover success, the provider's refusals (4xx), unknown outcomes (5xx, timeouts) and, for reconcilable writes, the lookup both finding and not finding the effect.

## 5. Ship it

**Your own connector:**

```sh
taskiem connector check manifest.yaml connector.wasm
TASKIEM_API_KEY=... taskiem connector push manifest.yaml connector.wasm
```

`check` loads the pair exactly as an upload would, offline. `push` needs an API key with `connector.manage` (owners and admins have it). Uploads are audited with the module's SHA-256. In the web app, Connections → Your connectors lists versions and takes uploads. A version never changes once uploaded: raise `version` to change anything. New runs use the newest enabled version of each major (`x_ledger@1`); disabling a version hands its major back to the previous one.

**For the catalogue**, package and sign it, then submit: see [connector submissions](connector-submissions.md).

```sh
taskiem connector keygen -o publisher.key
taskiem connector package --key publisher.key --licence Apache-2.0 --contact dev@acme.example --original ./ledger
taskiem connector submit ./ledger/p_acme_ledger-1.0.0.tcpkg
```

## 6. Use it

Add a connection for it (Connections → New connection), then use it like any connector: `"connector": "x_ledger@1"` (or `p_acme_ledger@1` once installed from the catalogue) in a definition, or the step palette, or `connector("x_ledger@1", "get_balance", ...)` in workflow code. Provider webhooks declared under `triggers` work as for built-ins.

Declare output schemas carefully: Taskiem compares every output with them, and a field that changes type, a value outside an `enum`, or a missing `required` field shows under Connections → Contract drift, for every connector, built-in, your own or installed.

## The package format

`taskiem connector package` writes one JSON document, `taskiem-connector-package/v1` (`engine/connpkg`):

| Field | |
| --- | --- |
| `format`, `id`, `version` | The format and the connector version |
| `manifest` | The manifest as written |
| `module` | The WebAssembly module, base64 |
| `licence`, `source_url` | An SPDX identifier from the [accepted list](connector-submissions.md#licences); where the source can be read |
| `conformance` | The suite, every exchange inline |
| `attestation` | `original_work` (you wrote it or may publish it, and copied no other product's connector; the [clean-room policy](clean-room-policy.md) applies) and a `contact` address |
| `created_at` | When it was packaged |
| `digest` | Hex SHA-256 over all of the above (the module by its own SHA-256), in a fixed JSON form |
| `key_id`, `signature` | The publisher key's id (first 8 bytes of the SHA-256 of the public key, hex) and an Ed25519 signature over `taskiem-connector-package/v1\n<id>\n<version>\n<digest>\n`, as audit anchors are signed |

`taskiem connector verify --public-key KEY PACKAGE` checks the digest and the signature. The digest is what a reviewer approves and what an installing organisation pins.
