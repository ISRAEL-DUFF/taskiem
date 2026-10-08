# PGDock integration

The connector is `connectors/pgdock` (`pgdock@1`). PGDock is a managed Postgres platform run by the same founders as Taskiem, but the connector is built like any other ([decision 0018](../decisions/0018-pgdock-integration-principles.md)): only from what PGDock publishes, read 7 October 2026: `docs/webhooks.md` (webhooks, payload, signature, delivery), `docs/cli.md` (API tokens) and `api/openapi.yaml` (the paths and shapes below). Nothing private is used: an outside tool could do everything here. The plan and its open questions are in [the PGDock integration plan](../pgdock-integration.md).

What it does today: **start a workflow when rows change** (Taskiem creates and removes the PGDock webhook itself) and **read rows**. Writing rows waits for PGDock to publish a parameterised endpoint (plan item P1-G1): PGDock's SQL endpoint takes no bound parameters, and Taskiem will not put values into SQL text.

## Connection

Make an API token in PGDock under **Account → API tokens** (or `pgdock tokens create --name taskiem --scopes read,write --project <project> --expires 90d`):

- **Restrict it to the project** Taskiem works with. It then cannot see or touch anything else in the organisation.
- Scopes **read** (query rows, the connection test) and **write** (Taskiem creates and deletes the project's webhooks). Taskiem never needs **admin**.
- It expires (90 days by default, a year at most). PGDock emails you a week before; replace the connection's token before then.

| Field | |
| --- | --- |
| `token` | The API token (`pgd_…`). Sent as `Authorization: Bearer <token>` |
| `project_id` | The project's id (PGDock shows it on the project page; `pgdock projects info <p> --json`) |
| `org_id` | Optional. When set, the connection test checks the token acts in this organisation |
| `base_url` | Optional: an `https://` PGDock server, for a self-hosted PGDock. Empty: the platform's PGDock, `TASKIEM_PGDOCK_URL` |

**Which server.** PGDock has not published its cloud address, so `TASKIEM_PGDOCK_URL` has no default: an operator sets it once PGDock publishes one, and until then each connection names its server. Whether connections may name any server (self-hosted PGDock) or only PGDock's cloud is open (plan question Q20); today any `https://` server is accepted, and the egress guard still refuses private, loopback and metadata addresses (decision [0013](../decisions/0013-private-network-access.md) covers private databases).

**The connection test** (`test_connection`) reads the token (`GET /api/v1/me`, whose `token` names its organisation, scopes, projects and expiry), the organisation (`GET /api/v1/orgs/{org}`) and its projects (`GET /api/v1/projects?org=`), and answers with the organisation's and projects' names. It fails if the connection's project is not one the token reaches, or the token acts in another organisation than `org_id`; it warns when the token is not restricted to projects, lacks the write scope, has admin, or expires within 14 days.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `test_connection` | `/me`, `/orgs/{org}`, `/projects` | read | The connection test |
| `query_rows` | `GET /api/v1/projects/{id}/tables/{schema}/{table}/rows` | read | `filters` (`{column, op, value}` or `{column, op: in, values}`; ops `eq`, `neq`, `lt`, `lte`, `gt`, `gte`, `contains`, `is_null`, `not_null`, `in`), `order` (`{column, desc}`, in priority order), `limit` (1 to 1,000, default 50), `after` (the previous page's `next`) |
| `get_row` | the same, filtered on the key | read | `key`: the primary key columns and values (a truncated event's `primary_key`). `found` false when there is no such row |

`query_rows` answers with `rows` (objects keyed by column), `columns` (`name`, `type`), `next` (empty on the last page), `order` and `key_columns`. PGDock returns every value as text (`"7"`, `"250000"`, `null` for NULL): convert with `int()` or `double()` in expressions. Pages are keyset pages on the primary key, so a table without one pages by offset (PGDock says deep offset paging is slow).

**Never SQL text.** Filters go as PGDock's structured `filter` parameter, which PGDock compiles to a parameterised `WHERE` clause. The connector never uses the raw `where` parameter or the SQL endpoint, and the fake PGDock in its tests refuses both. Rows in the output are personal data until shown otherwise: they are sealed (`pii: output.rows`, `output.row`).

## Trigger

The trigger is `row_changed`, a connector event (`connector_event`). wd/v1's reserved `database_change` type is kept for watching any Postgres directly (logical replication); a PGDock project is watched through PGDock's published webhooks instead (plan question Q11, decided this way for now).

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
| `events` | Any of `INSERT`, `UPDATE`, `DELETE` (default: all three), and `TEST` to run on PGDock's test events |

**Taskiem registers it** ([decision 0021](../decisions/0021-remote-trigger-registration.md)). There is no URL to copy and no secret to paste. When the workflow is published, Taskiem creates a PGDock webhook (`POST /api/v1/projects/{id}/webhooks`) for the tables, events and columns, pointed at its ingest URL, and keeps the signing secret PGDock returns once in the vault. A new version updates the webhook (`PATCH`); a version without the trigger, or taking the workflow out of an environment (`DELETE /v1/workflows/{wf}/deployments/{env}`), deletes it. Each environment the workflow is deployed to has its own webhook, through that environment's PGDock connection; an environment without one shows the subscription as failed until a connection is added. The webhook is named `taskiem-<subscription id>`, which is how Taskiem finds one it created just before a crash, and how you can tell Taskiem's webhooks apart in PGDock. Don't rename them.

**Status.** The workflow's Endpoints tab, the publish answer and the PGDock connection show each webhook's state: `ok`, `pending`, `failed` (PGDock refused, with the reason: a token without the write scope, an expired token), or PGDock's view, checked every 10 minutes: `failing` (deliveries failing), `paused` (paused by hand, or after 50 failures in a row), `broken` (PGDock's triggers on the table were dropped), `missing` (someone deleted the webhook). **Publish again to repair it**: even publishing the same version re-applies the webhook, which resumes a paused one, reinstalls a broken one and recreates a missing one. Taskiem never resumes a webhook on its own: pausing it in PGDock is a way to stop the workflow.

**Each delivery** is checked before anything else:

| | |
| --- | --- |
| **Signature** | `PGDock-Signature: t=<unix>,v1=<hex>`: HMAC-SHA256 of `"<t>.<raw body>"` with the webhook's secret, compared in constant time; refused when `t` is more than five minutes from now. Every `v1` value is tried, so a rotation that signs with the old and new secret for a while would be accepted (PGDock has not said whether it does: Q22) |
| **Event** | The payload's `type` |
| **Dedup** | `PGDock-Event-Id` (the payload's `id` if the header is missing). PGDock delivers at least once; a repeat is acknowledged and starts nothing |
| **Routing** | Only the workflow the webhook was created for starts |

The run's trigger is `{type: "connector_event", connector, trigger, event, body}`, with PGDock's payload as `body`: `id`, `webhook`, `project`, `table` (`public.orders`), `type`, `record` (null for a `DELETE`), `old_record` (null for an `INSERT`) and `committed_at`. Whether an `UPDATE`'s `old_record` holds every column or only the changed ones is not published (Q24).

**Test events.** PGDock's **Send test event** (`POST …/webhooks/{id}/test`) posts `type: "TEST"`. Taskiem verifies it, answers 202 with `"test": true`, records the time on the subscription (shown as `last_test_at`) and starts nothing, unless the workflow lists `TEST` in its events.

**Ordering.** PGDock delivers each webhook's events in commit order, but Taskiem starts one run per event and runs them side by side, so **runs do not finish in commit order**. If a workflow must handle a row's changes one at a time, set `settings.concurrency_key` to the row, for example `=trigger.body.table + ':' + string(trigger.body.record != null ? trigger.body.record.id : trigger.body.old_record.id)`: runs with the same key run one at a time and the others wait, queued in the order they started. `max_concurrency: 1` takes the whole workflow one change at a time (plan question Q12).

**Rolled back, retried, replayed.** PGDock writes each change to an outbox in the same transaction, so a rolled-back change never starts a workflow and a committed one always does. If Taskiem is down, PGDock retries for 24 hours (10 s doubling to 2 h), then keeps the event as a dead letter for 30 days, which you can replay from PGDock (`pgdock webhooks replay <p> <webhook> --all`); a replay or a retry Taskiem already took is taken once. A failing event holds back the ones behind it for that webhook, and your PGDock plan caps deliveries per minute (Personal 60, Team 300), so a backlog drains at that rate. Whether webhooks Taskiem creates count against that cap is PGDock's to say (Q15).

### Large rows

A change whose rows serialise to more than 256 KB arrives without `record` and `old_record`, with `"truncated": true` and the row's `primary_key`. By default the run gets it like that: check `trigger.body.truncated` and fetch the row with `get_row` (`key: trigger.body.primary_key`) if you need it. With `options.refetch_truncated: true`, Taskiem fetches the row through the rows endpoint before the run starts and puts it in `record`, adding `refetched: true`; the row is as it is now, which may be later than the change. If it cannot (the row was deleted since, the event was a `DELETE`, or `primary_key` is not an object of key columns) the run gets the event with `refetched: false` and `refetch_error`; if PGDock is busy (429, 503, a timeout) the delivery is refused and PGDock sends it again. Which is the better default, and whether `primary_key` is always an object PGDock's rows endpoint can filter on, are open (Q14, Q23).

## Errors

PGDock answers errors as `{code, message}`, with `sql_error` (`code` a SQLSTATE) for database errors. It has not published its list of codes (plan item P1-G4), so the connector maps a SQLSTATE when there is one, the few codes the OpenAPI file names, and otherwise the HTTP status, conservatively:

| PGDock | Class | Engine |
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

The error message names the class, status, code and SQLSTATE (`pgdock rate_limited (http 429, rate_limited): …`). The SQL endpoint, which returns database errors with status 200, is not used. A Free-tier "resuming" answer (plan C6, Q13) is not published, so it maps by its status like anything else.

## Tests

`connectors/pgdock/pgdocktest` is a fake PGDock built from the same published documents: a transactional outbox (`Begin`, `Insert`, `Update`, `Delete`, `Commit` or `Rollback`), delivery in commit order that holds a webhook's queue at its first failure, retries, dead letters and replay, pausing after 50 failures, truncation above 256 KB, test events, redelivery of an event already delivered, the webhook management API (including `rotate-secret`), the rows endpoint with filters, order and keyset pages, token scopes and project restriction, and injected errors with `Retry-After`. `connectors/pgdock/testdata/events` holds deliveries shaped as PGDock publishes them, signed with a fixture secret.

`engine/remote/remote_test.go` runs the plan's acceptance test against it (P1-T8, without the write-back that waits for P1-G1): an `orders` insert starts a workflow that sends a WhatsApp message through a fake Graph API; a rolled-back insert starts nothing; events retried after Taskiem was unavailable, and events PGDock delivers twice, start one run each. It also covers adoption after a crash, a redeploy, drift (broken, paused, missing) and its repair, a refusal, and removal. PGDock is to run these against its own builds (plan item P4-T5).

## What PGDock does not publish

The connector assumes the most conservative reading of each; each is a question in [the plan](../pgdock-integration.md#5-open-questions):

- the cloud address (Q20) and how an API token is sent: the OpenAPI file has no security scheme; `Authorization: Bearer` is assumed, as for its metrics endpoint (Q25);
- which scope deleting a webhook needs, and whether it needs a typed confirmation like other destructive actions (Q26);
- whether webhook names are unique in a project, which finding Taskiem's webhook by name relies on (Q27);
- whether the payload's `project` is the project's id or another identifier (the published example, `p_k2f9a7bq3d`, is not a UUID like the API's ids) (Q28);
- the error codes (P1-G4), the rotation overlap (Q22), `primary_key`'s shape (Q23), `old_record`'s columns (Q24), and delivery rates for integrators' webhooks (Q15).

## Writes

Not yet. Insert, upsert, update, delete and function calls wait for a parameterised, single-statement endpoint or row-write endpoints (P1-G1). When they come, an insert will be an `unsafe_write` until PGDock honours an idempotency key (P1-G2), upsert on a conflict column an `idempotent_write`, and update and delete will require a filter and refuse to change more rows than a stated maximum (P1-T5).

## Template

`pgdock-new-row-whatsapp` ([templates](../templates.md)): a row inserted into a table sends the number in it an approved WhatsApp template.
