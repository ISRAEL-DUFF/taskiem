# Xenibase (formerly PGDock) integration

The connector is `connectors/pgdock` (`pgdock@1`; the id stays, workflows pin it, the manifest's name is Xenibase). Xenibase is a managed Postgres platform run by the same founders as Taskiem, but the connector is built like any other ([decision 0018](../decisions/0018-pgdock-integration-principles.md)): only from what Xenibase publishes. The trigger and the management API reads were built from `docs/webhooks.md` (webhooks, payload, signature, delivery), `docs/cli.md` (API tokens) and `api/openapi.yaml` (the paths and shapes below), read 7 October 2026; the data API actions from `docs/backend-services.md` (Keys, Reading data, Writing data, Retrying writes safely: Idempotency-Key, Functions), `docs/errors.md` (the Data API table), `api/openapi.yaml` (`/projects/{id}/services`) and `docs/integrations/taskiem-reply.md` (P1-G1 and P1-G2: writes go through the data API, which honours an idempotency key; there will be no parameterised SQL endpoint), read 17 October 2026. Nothing private is used: an outside tool could do everything here. The plan and its open questions are in [the Xenibase integration plan](../pgdock-integration.md); data tables are built on these actions ([decision 0033](../decisions/0033-data-tables-on-xenibase.md)).

What it does: **start a workflow when rows change** (Taskiem creates and removes the Xenibase webhook itself), **read rows** through either API, and **write rows and call functions** through the project's data API, every write under an idempotency key.

## Connection

Make an API token in Xenibase under **Account → API tokens** (or `xenibase tokens create --name taskiem --scopes read,write --project <project> --expires 90d`):

- **Restrict it to the project** Taskiem works with. It then cannot see or touch anything else in the organisation.
- Scopes **read** (query rows, the connection test) and **write** (Taskiem creates and deletes the project's webhooks). Taskiem never needs **admin**.
- It expires (90 days by default, a year at most). Xenibase emails you a week before; replace the connection's token before then.

For writes, functions and `read_rows`, the project's **backend services** must be on (Project → Settings → API → Enable backend services) and the connection needs the project's **secret key** and **data API URL**, both on that API page. The secret key is shown once, when it is made; make a second one and move the connection to it before revoking the first.

| Field | |
| --- | --- |
| `token` | The API token (`xb_…` or `pgd_…`). Sent as `Authorization: Bearer <token>` to the management API |
| `project_id` | The project's id (Xenibase shows it on the project page) |
| `org_id` | Optional. When set, the connection test checks the token acts in this organisation |
| `base_url` | Optional: an `https://` Xenibase server, for a self-hosted Xenibase. Empty: the platform's Xenibase, `TASKIEM_PGDOCK_URL` |
| `secret_key` | Optional, secret: the project's secret key (`xb_sec_…`). Sent as `apikey` to the data API, where it runs as the service role (row-level security does not apply). Needed by `read_rows`, the writes and `call_function` |
| `api_url` | Optional: the project's data API URL, `https://<ref>.<api-domain>` (the `url` of `GET /api/v1/projects/{id}/services`). Needed with `secret_key` |

**Which server.** `TASKIEM_PGDOCK_URL` is the platform's Xenibase; a connection may name its own server (a self-hosted Xenibase, plan question Q20: Xenibase is installed per operator, so each connection sets its own management URL and API domain). Any `https://` server is accepted, and the egress guard still refuses private, loopback and metadata addresses (decision [0013](../decisions/0013-private-network-access.md) covers private databases). Management API tokens are not accepted by the data API, and the secret key is not accepted by the management API: the two credentials do different work.

**The connection test** (`test_connection`) reads the token (`GET /api/v1/me`, whose `token` names its organisation, scopes, projects and expiry), the organisation (`GET /api/v1/orgs/{org}`) and its projects (`GET /api/v1/projects?org=`), and answers with the organisation's and projects' names. It fails if the connection's project is not one the token reaches, or the token acts in another organisation than `org_id`; it warns when the token is not restricted to projects, lacks the write scope, has admin, or expires within 14 days. It then reads the project's backend services (`GET /api/v1/projects/{id}/services`) and warns, without failing, when they are off, when the connection's `api_url` is not the project's `url` (answered as `api_url`), or when the connection has no `secret_key`.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `test_connection` | `/me`, `/orgs/{org}`, `/projects`, `/projects/{id}/services` | read | The connection test |
| `query_rows` | `GET /api/v1/projects/{id}/tables/{schema}/{table}/rows` | read | `filters` (`{column, op, value}` or `{column, op: in, values}`; ops `eq`, `neq`, `lt`, `lte`, `gt`, `gte`, `contains`, `is_null`, `not_null`, `in`), `order` (`{column, desc}`, in priority order), `limit` (1 to 1,000, default 50), `after` (the previous page's `next`) |
| `get_row` | the same, filtered on the key | read | `key`: the primary key columns and values (a truncated event's `primary_key`). `found` false when there is no such row |
| `read_rows` | `POST /data/v1/{table}/query` | read | `table` (or `schema.table`), `select`, `filters` (the same form, plus ops `like`, `ilike`, `is` with `null`, `true` or `false`, `contained_by`, `search`), `order`, `limit` (1 to 1,000, default 100), `cursor` (the previous page's `next_cursor`), `count` (`exact` or `estimated`). Answers `rows` (typed values), `next_cursor`, `count` |
| `insert_rows` | `POST /data/v1/{table}` | idempotent_write | `rows` (1 to 1,000 objects) or `row`; `select`, `return` (`representation` or `minimal`). Answers `affected`, `rows`, `replayed` |
| `upsert_rows` | `POST /data/v1/{table}?on_conflict=` | idempotent_write | As `insert_rows`, with `on_conflict` (the columns of a unique constraint) and `resolution` (`merge`, the default: conflicting rows take the sent columns; `ignore`: they are left alone) |
| `update_rows` | `PATCH /data/v1/{table}/{key}` or `?where=` | idempotent_write | `set` (the columns to change) and exactly one of `key` (one row's primary key value) or `filters` (non-empty; `not_null` is not available here); `max_affected` (default 1,000, at most 100,000); `select`, `return`. Answers `affected`, `rows`, `replayed` |
| `delete_rows` | `DELETE /data/v1/{table}/{key}` or `?where=` | idempotent_write | `key` or `filters`, `max_affected`. Answers `affected`, `replayed`; deleted rows are not brought back |
| `call_function` | `POST /data/v1/rpc/{function}` | idempotent_write | `function` (or `schema.function`), `args` (named arguments). Answers `data` (a scalar, or a list for a set or table) and `replayed` |

`query_rows` answers with `rows` (objects keyed by column), `columns` (`name`, `type`), `next` (empty on the last page), `order` and `key_columns`. The management API returns every value as text (`"7"`, `"250000"`, `null` for NULL): convert with `int()` or `double()` in expressions. The data API (`read_rows` and the rows a write answers with) returns values typed, as JSON. Pages are keyset pages on the primary key.

**Never SQL text.** `query_rows` filters go as the rows endpoint's structured `filter` parameter, which Xenibase compiles to a parameterised `WHERE` clause; the connector never uses that endpoint's raw `where` parameter or the SQL endpoint, and the fake Xenibase in its tests refuses both. The data API binds values too: `read_rows` sends the query body (`where` as `{column, op, value}` conditions under `and`, with `not` for `not_null`), and an update or delete by filter sends `where=column:op:value` parameters, one per filter, percent-encoded whole so a colon or a comma in a value is a value; an `in` list is comma-joined with double quotes around a value holding a comma or a quote. The connector checks every column name is a name (or a JSON path such as `meta->tag`) and every operator is one Xenibase lists, before anything is sent.

**Writes are refused before they are sent** when they would change every row: `update_rows` and `delete_rows` need `key` or `filters` (never both, never an empty list), and Xenibase refuses the rest with `filter_required`. `max_affected` is Xenibase's guard: when more rows would change it answers `too_many_rows` and changes nothing.

**The idempotency key.** Every write is an `idempotent_write`: the engine derives a key per step attempt (`tsk-` and 22 base64url characters, 132 bits) and the connector sends it as `Idempotency-Key`. Xenibase keeps the first answer for 24 hours: a retry after an unknown outcome (a timeout, a lost answer, a 5xx) gets that answer with `Idempotent-Replayed: true`, which the action reports as `replayed: true`, and writes nothing. The same key with a different request is `422 idempotency_key_reused`, a fatal conflict, which the engine's derivation never produces for the same step. A refused write records no key, so a corrected retry under the same key runs. A key first sent more than 24 hours earlier is not sent again: the step fails with the earlier attempt named as possibly applied, since Xenibase would run the write a second time. Keys belong to the secret key's caller; the fake in the tests keeps them per project and key.

Rows in the inputs and outputs are personal data until shown otherwise: they are sealed (`pii` on `output.rows`, `output.row`, `input.rows`, `input.row`, `input.set` and a function's `output.data`).

## Trigger

The trigger is `row_changed`, a connector event (`connector_event`). wd/v1's reserved `database_change` type is kept for watching any Postgres directly (logical replication); a Xenibase project is watched through Xenibase's published webhooks instead (plan question Q11, decided this way for now).

```json
"trigger": {"type": "connector_event", "config": {
  "connector": "pgdock@1", "trigger": "row_changed", "connection": "shop",
  "events": ["INSERT", "UPDATE"],
  "options": {"tables": ["orders", "billing.invoices"], "columns": ["status"], "refetch_truncated": false}}}
```

| | |
| --- | --- |
| `options.tables` | Required: `schema.table`, or `table` in `public` |
| `options.columns` | Optional: an `UPDATE` starts the workflow only when one of these columns changed |
| `options.refetch_truncated` | Optional, default false: see [Large rows](#large-rows) |
| `events` | Any of `INSERT`, `UPDATE`, `DELETE` (default: all three), and `TEST` to run on Xenibase's test events |

**Taskiem registers it** ([decision 0021](../decisions/0021-remote-trigger-registration.md)). There is no URL to copy and no secret to paste. When the workflow is published, Taskiem creates a Xenibase webhook (`POST /api/v1/projects/{id}/webhooks`) for the tables, events and columns, pointed at its ingest URL, and keeps the signing secret Xenibase returns once in the vault. A new version updates the webhook (`PATCH`); a version without the trigger, or taking the workflow out of an environment (`DELETE /v1/workflows/{wf}/deployments/{env}`), deletes it. Each environment the workflow is deployed to has its own webhook, through that environment's Xenibase connection; an environment without one shows the subscription as failed until a connection is added. The webhook is named `taskiem-<subscription id>`, which is how Taskiem finds one it created just before a crash, and how you can tell Taskiem's webhooks apart in Xenibase. Don't rename them.

**Status.** The workflow's Endpoints tab, the publish answer and the Xenibase connection show each webhook's state: `ok`, `pending`, `failed` (Xenibase refused, with the reason: a token without the write scope, an expired token), or Xenibase's view, checked every 10 minutes: `failing` (deliveries failing), `paused` (paused by hand, or after 50 failures in a row), `broken` (Xenibase's triggers on the table were dropped), `missing` (someone deleted the webhook). **Publish again to repair it**: even publishing the same version re-applies the webhook, which resumes a paused one, reinstalls a broken one and recreates a missing one. Taskiem never resumes a webhook on its own: pausing it in Xenibase is a way to stop the workflow.

**Each delivery** is checked before anything else:

| | |
| --- | --- |
| **Signature** | `PGDock-Signature: t=<unix>,v1=<hex>`: HMAC-SHA256 of `"<t>.<raw body>"` with the webhook's secret, compared in constant time; refused when `t` is more than five minutes from now. Every `v1` value is tried, so a rotation that signs with the old and new secret for a while is accepted (Q22: `rotate-secret` with `overlap_seconds` does that) |
| **Event** | The payload's `type` |
| **Dedup** | `PGDock-Event-Id` (the payload's `id` if the header is missing). Xenibase delivers at least once; a repeat is acknowledged and starts nothing |
| **Routing** | Only the workflow the webhook was created for starts |

The run's trigger is `{type: "connector_event", connector, trigger, event, body}`, with Xenibase's payload as `body`: `id`, `webhook`, `project`, `table` (`public.orders`), `type`, `record` (null for a `DELETE`), `old_record` (null for an `INSERT`) and `committed_at`. An `UPDATE`'s `old_record` is the whole row before the change (Q24).

**Test events.** Xenibase's **Send test event** (`POST …/webhooks/{id}/test`) posts `type: "TEST"`. Taskiem verifies it, answers 202 with `"test": true`, records the time on the subscription (shown as `last_test_at`) and starts nothing, unless the workflow lists `TEST` in its events.

**Ordering.** Xenibase delivers each webhook's events in commit order, but Taskiem starts one run per event and runs them side by side, so **runs do not finish in commit order**. If a workflow must handle a row's changes one at a time, set `settings.concurrency_key` to the row, for example `=trigger.body.table + ':' + string(trigger.body.record != null ? trigger.body.record.id : trigger.body.old_record.id)`: runs with the same key run one at a time and the others wait, queued in the order they started. `max_concurrency: 1` takes the whole workflow one change at a time (plan question Q12).

**Rolled back, retried, replayed.** Xenibase writes each change to an outbox in the same transaction, so a rolled-back change never starts a workflow and a committed one always does. If Taskiem is down, Xenibase retries for 24 hours (10 s doubling to 2 h), then keeps the event as a dead letter for 30 days, which you can replay from Xenibase (`pgdock webhooks replay <p> <webhook> --all`); a replay or a retry Taskiem already took is taken once. A failing event holds back the ones behind it for that webhook, and your Xenibase plan caps deliveries per minute (Personal 60, Team 300), so a backlog drains at that rate. Whether webhooks Taskiem creates count against that cap is Xenibase's to say (Q15).

### Large rows

A change whose rows serialise to more than 256 KB arrives without `record` and `old_record`, with `"truncated": true` and the row's `primary_key`. By default the run gets it like that: check `trigger.body.truncated` and fetch the row with `get_row` (`key: trigger.body.primary_key`) if you need it. With `options.refetch_truncated: true`, Taskiem fetches the row through the rows endpoint before the run starts and puts it in `record`, adding `refetched: true`; the row is as it is now, which may be later than the change. If it cannot (the row was deleted since, the event was a `DELETE`, or `primary_key` is not an object of key columns) the run gets the event with `refetched: false` and `refetch_error`; if Xenibase is busy (429, 503, a timeout) the delivery is refused and Xenibase sends it again. Which is the better default, and whether `primary_key` is always an object Xenibase's rows endpoint can filter on, are open (Q14, Q23).

## Errors

**The data API** answers errors as `{"error": {code, message, details, request_id}}`, with the SQLSTATE in `details.pg_code`, and publishes every code with whether a retry can help (`docs/errors.md`, the Data API table). The connector maps them by code and status:

| Data API | Class | Engine |
| --- | --- | --- |
| 429 `rate_limited`, `spend_cap_rate_limited` | `rate_limited` | Retried, waiting at least `Retry-After` |
| 429 `plan_limit_reached` | `quota` | Fatal (the month's requests are used up; Xenibase marks it not retryable) |
| 503 `project_resuming`, `database_unavailable`, `retry`, `starting`, `idempotency_unavailable` | `unavailable` | Retried, waiting at least `Retry-After` |
| 504 `statement_timeout` | `timeout` | Unknown outcome: an idempotent write is retried with the same key, which replays or runs it |
| 500, 502 and other 5xx | `server` | Unknown outcome, as above |
| 401 `key_required`, `invalid_key`, `invalid_token` | `auth` | Fatal |
| 403 `permission_denied`, `rls_required`, `secret_key_in_browser`, `origin_not_allowed`, `project_suspended` | `permission` | Fatal |
| 403 `read_only` | `conflict` | Fatal |
| 404 `unknown_table`, `unknown_function`, `not_found`, `project_not_found`, `unknown_host` | `not_found` | Fatal |
| 409 `unique_violation`, `foreign_key_violation` | `conflict` | Fatal (use `upsert_rows` to merge on a unique constraint) |
| 422 `idempotency_key_reused` | `conflict` | Fatal |
| 400 (`filter_required`, `too_many_rows`, `unknown_column`, `invalid_filter`, `database_error`, …), 405, 413, 422 (`not_null_violation`, `check_violation`) | `validation` | Fatal |

The error message carries the request id (`pgdock conflict (http 409, unique_violation): … (request req_…)`) for Xenibase's support. Codes the connector does not know map by their status, as Xenibase's deprecation policy asks (`4xx` no retry, `429` and `503` retry).

**The management API** answers errors as `{code, message}`, with `sql_error` (`code` a SQLSTATE) for database errors. The connector maps a SQLSTATE when there is one, the few codes the OpenAPI file names, and otherwise the HTTP status, conservatively:

| Management API | Class | Engine |
| --- | --- | --- |
| 429 | `rate_limited` | Retried, waiting at least `Retry-After` |
| 503 | `unavailable` | Retried, waiting at least `Retry-After` |
| 500, 502 | `server` | Unknown outcome (reads are retried) |
| 504 | `timeout` | Unknown outcome |
| 401 | `auth` (token wrong, expired or revoked) | Fatal |
| 403, `reauth_required` | `permission` | Fatal |
| 404 | `not_found` | Fatal (`get_row` answers `found: false` instead) |
| 409 | `conflict` | Fatal |
| an error with `quota` | `quota` | Fatal |
| 400, 413, 422, other 4xx | `validation` | Fatal |
| SQLSTATE `40001`, `40P01` | `conflict` | Retried (nothing was committed) |
| SQLSTATE `57014` | `timeout` | Retried |
| SQLSTATE `08…`, `53…`, `57P01`–`57P03` | `unavailable` | Retried |
| SQLSTATE `23…` (`23505` unique) | `conflict` | Fatal |
| SQLSTATE `42501` | `permission` | Fatal |
| SQLSTATE `22…`, other `42…` | `validation` | Fatal |

The error message names the class, status, code and SQLSTATE (`pgdock rate_limited (http 429, rate_limited): …`). The SQL endpoint, which returns database errors with status 200, is not used. A paused Free project answers `503 project_resuming` with `Retry-After: 10` on both APIs (Q13), which maps as unavailable and is retried.

## Tests

`connectors/pgdock/pgdocktest` is a fake Xenibase built from the same published documents: a transactional outbox (`Begin`, `Insert`, `Update`, `Delete`, `Commit` or `Rollback`), delivery in commit order that holds a webhook's queue at its first failure, retries, dead letters and replay, pausing after 50 failures, truncation above 256 KB, test events, redelivery of an event already delivered, the webhook management API (including `rotate-secret`), the rows endpoint with filters, order and keyset pages, token scopes and project restriction, `GET /projects/{id}/services`, and injected errors with `Retry-After`. Its data API (`data.go`), served under `/edge/{ref}` of the same server with per-project secret keys (`AddSecretKey`, `APIURL`), holds typed JSON rows: reads with `where`, `or`, the query body's `and`, `or` and `not`, `order`, `select`, keyset cursors and `count`; inserts with an identity key, `on_conflict` with both resolutions, `unique_violation`; updates and deletes by key or filter with `filter_required`, `max_affected` and `too_many_rows`, `return=minimal` and `select`; `rpc` for registered Go functions (`AddFunction`); the wrapped error shape; and the idempotency semantics as documented (the first answer kept per key and request, `Idempotent-Replayed: true`, `422 idempotency_key_reused`, no key after a refusal). Writes through it go through the same change path as `Tx`, so webhooks fire and the outbox keeps its order; `Rows` and `Changes` read a table back, and a `LoseAnswer` failure commits a write and loses its answer (502). `connectors/pgdock/testdata/events` holds deliveries shaped as Xenibase publishes them, signed with a fixture secret.

`connectors/pgdock/pgdock_test.go` covers each action against the fake, the filter translator (`TestFilterTranslator`), idempotent replay and key reuse (`TestIdempotentReplay`), the refusals made before anything is sent, and the error mapping of both APIs (`TestErrorMapping`, `TestDataErrorMapping`).

`engine/remote/remote_test.go` runs the plan's acceptance test against it (P1-T8, `TestNewRowSendsWhatsAppOnce`): an `orders` insert starts a workflow that sends a WhatsApp message through a fake Graph API and writes `status: notified` back to the row by key through the data API; a rolled-back insert starts nothing; events retried after Taskiem was unavailable, and events Xenibase delivers twice, start one run each; and a write-back whose answer was lost after the commit is retried by the engine under the same key and replayed by Xenibase, so the row changes exactly once. It also covers adoption after a crash, a redeploy, drift (broken, paused, missing) and its repair, a refusal, and removal. Xenibase is to run these against its own builds (plan item P4-T5).

## What Xenibase does not publish

These were open when the trigger was built (7 October 2026); Xenibase's reply of 17 October (`docs/integrations/taskiem-reply.md`) answers most of them, and [the plan](../pgdock-integration.md#5-open-questions) tracks each. The connector keeps the conservative reading until the trigger is revisited:

- the cloud address (Q20) and how an API token is sent: the OpenAPI file has no security scheme; `Authorization: Bearer` is assumed, as for its metrics endpoint (Q25);
- which scope deleting a webhook needs, and whether it needs a typed confirmation like other destructive actions (Q26);
- whether webhook names are unique in a project, which finding Taskiem's webhook by name relies on (Q27);
- whether the payload's `project` is the project's id or another identifier (the published example, `p_k2f9a7bq3d`, is not a UUID like the API's ids) (Q28);
- the error codes (P1-G4), the rotation overlap (Q22), `primary_key`'s shape (Q23), `old_record`'s columns (Q24), and delivery rates for integrators' webhooks (Q15).

## Writes

Through the data API (P1-G1, P1-G2: answered, see [Actions](#actions)). What the data API docs leave open, and what the connector assumes:

- the shape of an `in` value in the query body (a JSON list is sent) and how a double quote inside a quoted `in` value of a `where` parameter is escaped (a backslash is used; the connector quotes a value holding a comma or a quote);
- what `PATCH` or `DELETE` by a key that matches no row answers (`affected: 0` is assumed, not 404);
- `old_record` of a data API update is the whole row, as for any update (Q24, answered: `to_jsonb(OLD)`).

## Template

`pgdock-new-row-whatsapp` ([templates](../templates.md)): a row inserted into a table sends the number in it an approved WhatsApp template.
