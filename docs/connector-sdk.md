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
- A provider that issues tokens to apps by OAuth 2.0 may declare `auth.oauth2` so people connect with one click instead of pasting a token ([below](#connecting-by-oauth)).

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

### Guiding the person who connects

Most people connecting a service are not developers, so each field can say what it is and where it is (no-technical-steps plan, N2). The web app shows them on a guided screen and checks a pasted value as it is typed and again, with the service, before it is saved (`POST /v1/connections` with `check: true`, or `POST /v1/connections/check`):

```yaml
auth:
  type: api_key
  fields:
    - key: secret_key
      label: Secret key
      secret: true
      help: Taskiem uses it to take and check payments for your business. Keep it private.
      where: Paystack dashboard → Settings → API Keys & Webhooks
      link: https://dashboard.paystack.com/#/settings/developers
      prefixes:
        - { prefix: sk_live_, mode: live, label: Live (real money) }
        - { prefix: sk_test_, mode: test, label: Test (practice) }
      refuse:
        - { prefix: pk_, message: "That is your public key. Copy the secret key from the same page: it starts with sk_." }
  test: { action: check_balance }
```

- `help` is one plain line; `where` says where the value is in the provider's dashboard, and `link` opens that page.
- `prefixes` are what a right value starts with. When they name modes, the person chooses one (**Live** or **Test**) instead of knowing a prefix, and a key for the other is refused in plain words. The connection records the mode, and pickers say when one holds a test key.
- `refuse` are values that are the wrong one, such as a public key, each with what to tell the person.
- `multiline` fields are pasted into a text area (a PEM key); a field with `generate: rsa_key_pair` offers to make an RSA key pair in the person's browser, keeps the private key as the value and shows the public key once to paste into the provider's dashboard (OPay Business).
- Optional fields are shown under **More options**.
- `auth.test` is the action the service is asked with before saving; a connector without one is saved after the field checks alone.

A connection nobody names is called `main` (then `main_2` …), the name templates, the AI builder and storefront payments use.

### Connecting by OAuth

A connector keeps its `auth.fields` (people can always paste credentials) and adds `auth.oauth2` when the provider supports the OAuth 2.0 authorization code grant with PKCE ([decision 0034](decisions/0034-oauth-connections.md); rule 13 of the [contract](contracts/connector-v1.md)):

```yaml
auth:
  type: api_key
  fields:
    - { key: token, label: API token, secret: true }
    - { key: project_id, label: Project ID }
    - { key: secret_key, label: Project secret key, secret: true, required: false }
  oauth2:
    authorize_url: "${server}/oauth/authorize"     # or an absolute https URL
    token_url: "${server}/api/v1/oauth/token"
    revoke_url: "${server}/api/v1/oauth/revoke"     # optional (RFC 7009)
    scopes: [read, write]
    pkce: true                                      # always; Taskiem sends an S256 challenge
    fields_from_token: { token: access_token, project_id: "project_ids[0]" }
```

- `authorize_url`, `token_url` and `revoke_url` are `https://` URLs, or start with `${server}/` to be resolved from the connector's `base_url` (the platform's server for that provider, such as `TASKIEM_XENIBASE_URL`) when a connection is linked. A connector whose URLs need a server is not linkable on a platform that names none; a connection's own server (a self-hosted instance) is never used for OAuth, since Taskiem is not registered there.
- `scopes` is what Taskiem asks for; the person may grant less.
- `fields_from_token` fills auth fields from the token answer: a field of the answer (`access_token`, `org_id`), one element of a list field (`project_ids[0]`: the grant must then name exactly one, or the person is asked to connect again and choose one), or `${server}` for the server the token came from. Every required field must be filled this way, or a linked connection would be missing it. Optional fields the token does not fill (Xenibase's secret key and data API URL) are added afterwards from the Connections page (`PUT /v1/connections/{id}/credentials`).

Two more keys serve providers whose tokens are short-lived and refreshed by the connector itself (Google):

- `authorize_params` adds query parameters to the consent URL (`{ access_type: offline, prompt: consent }`, without which Google issues no refresh token).
- `platform_client_fields` fills auth fields from Taskiem's own registration, `id` or `secret` (`{ client_id: id, client_secret: secret }`), since a refresh token only works with the client it was granted to. A connection that keeps a `refresh_token` from `fields_from_token` does not expire with its access token.

**Taskiem's own app.** Slack, Meta and Intuit send every business's events to the one URL of the app Taskiem registered with them, not to a URL per business ([decision 0051](decisions/0051-platform-apps.md), connector/v1 rule 18). Such a connector names the app in `auth.oauth2.client` (shared, as `zoho` is by every Zoho connector), says which auth field holds the business's account in `account_field` (filled from the token or, with `fields_from_callback`, from the callback's query), and declares its triggers `delivery: app` with an `account_key` expression. Taskiem verifies each delivery with the app's secret and delivers each event into every connection holding its account. A business that brought its own app keeps pasting its credentials and its trigger's own URL.

**Accounts on one of the provider's servers.** Zoho keeps each account in one data centre and names it on the callback (`accounts-server=https://accounts.zoho.eu`); the token answer names the API's (`api_domain`). Fill fields from both (`fields_from_callback`, `fields_from_token`), start `token_url` with `${connection.accounts_server}`, list `${connection.api_domain}` in `egress_hosts`, and list the provider's servers in `connection_hosts` (connector/v1 rule 19): Taskiem refuses a callback, and drops an egress host, that names anything else.

**Refresh tokens that rotate.** Some providers issue a new refresh token on every refresh and revoke the whole chain when an old one is used again (QuickBooks). A connector for one never refreshes on its own: it calls `req.RotateCredentials(ctx, fn)` ([decision 0051](decisions/0051-platform-apps.md)). Taskiem locks the connection's row, reads its credentials as stored now and calls `fn` with them; `fn` returns them unchanged when they are already fresh (another worker refreshed while this one waited), or refreshes and returns them with the provider's new fields merged in. What `fn` returns is stored, and audited as `connection.refresh` without values, before the lock is let go, so ten workers that find the token expired at once refresh once, and a restart reads the new refresh token. Keep the access token and its expiry in the credentials too: the next step then uses it without refreshing. `fn` wraps `connector.ErrCredentialsRejected` when the provider refuses the refresh token, which marks the connection expired. Where the credentials are not a stored connection's, `RotateCredentials` fails fatally with `connector.ErrNoRotate` rather than spend the token.

The flow is Taskiem's, not the connector's: `POST /v1/connections/oauth/start` records a ten-minute request and answers the consent URL (client id, redirect URI, scopes, state, PKCE challenge); the provider sends the browser back to `/v1/connections/oauth/callback`, which exchanges the code at `token_url` with the client secret and the verifier and saves the connection with `auth_type: oauth2` and the token's expiry. The state and the verifier never leave the server; the client secret never leaves the process. Taskiem's registration with the provider is per connector in the environment: `TASKIEM_OAUTH_<CONNECTOR>_CLIENT_ID` and `_CLIENT_SECRET` ([operations](operations.md#configuration)); without it, `GET /v1/connectors` says `oauth_linkable: false` and start answers 503 `oauth_unavailable`.

Revocation goes both ways. Deleting an OAuth connection (`DELETE /v1/connections/{id}`) also posts its token to `revoke_url`, best effort. When the provider refuses a connection's token as revoked or expired, the connector wraps `connector.ErrCredentialsRejected` in its error (the Xenibase connector does for `401 invalid_token`); the engine then marks an OAuth connection `expired` at that step, so runs stop using it and the Connections page says to connect again. Pasted credentials are left alone: the person replaces them.

### Events without a webhook set up

A webhook trigger usually needs someone to paste Taskiem's address into the provider's dashboard. A business that never does still gets its events when the trigger also declares `poll`: Taskiem asks the provider itself, every few minutes, in prod.

```yaml
triggers:
  charge_event:
    type: webhook
    verify: { scheme: hmac_sha512, header: x-paystack-signature, secret_field: secret_key }
    events: [charge.success]
    event_type: =body.event
    dedup: =body.data.reference + ':' + body.event
    poll: { action: list_transactions, since: from, items: =output.data, event: "='charge.success'", every: 5m }
```

- `action` is a read action. Its input field `since` names the time (RFC 3339) it lists records from; Taskiem keeps where it got to, starts at the first deployment (nothing earlier is replayed), and looks back ten minutes each time.
- `items` (over `output`) lists the records; `event` (over `item`) names each one's event.
- Each record becomes the delivery the provider would send, `{"event": …, "data": item}`, signed with the connection's own secret, and goes through the trigger's usual checks, dedup and subscribers. An event the webhook also delivers is taken once.
- Only a trigger verified by plain HMAC (`hmac_sha256`, `hmac_sha512`) can be polled.

The editor then says the address in the provider's dashboard is optional; with it, events arrive at once.

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
