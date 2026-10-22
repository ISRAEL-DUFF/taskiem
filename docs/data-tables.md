# Data tables

Records a business keeps (customers, debtors, staff, stock) that its workflows read and write, without a spreadsheet or a database of its own. A data table is a Postgres table in a Xenibase project that Taskiem creates and manages for the organisation ([decision 0033](decisions/0033-data-tables-on-xenibase.md)); workflows reach it through the `xenibase@1` connection named `data_tables` ([Xenibase integration](integrations/xenibase.md)), so nothing in the engine knows what a data table is.

Permissions: `table.read` sees tables and their rows; `table.edit` creates and changes tables, columns and rows. Owners, admins and builders have both; operators, auditors and viewers have `table.read`.

## What a table is

A table has a name, a title, a description and columns. Taskiem keeps that definition in its own database; the rows live only in the Xenibase project. In Postgres the table is `public.<name>` and every table has:

- `id`, a `bigint` identity and the primary key (Taskiem's API writes rows without one; an import may carry one to update rows);
- `created_at` and `updated_at`, `timestamptz` set when the row is made;
- the declared columns.

Names (of tables and columns) are lower-case letters, digits and underscores, starting with a letter, at most 63 characters; `id`, `created_at`, `updated_at` and anything starting `xb_` or `pg_` are reserved. A table's name is unique in the organisation.

## Creating one

On the Tables page, "New table" asks for the name and the columns. Through the API:

```sh
curl -X POST $TASKIEM/v1/tables -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "name": "debtors", "title": "Debtors",
  "columns": [
    {"name": "name", "title": "Name", "type": "text", "required": true},
    {"name": "phone", "type": "phone"},
    {"name": "balance", "title": "Balance (naira)", "type": "number"},
    {"name": "paid", "type": "boolean"}
  ]}'
```

The first table an organisation creates makes its Xenibase project, which takes a minute or two (see [how provisioning works](#how-provisioning-works)); a request that arrives while that runs gets 409 `tables_provisioning` with `Retry-After`. Every table change goes through Xenibase's schema API: Taskiem asks for a preview of the change and applies it with the preview's hash, so what runs is what Xenibase showed, and Taskiem never writes SQL text.

## Columns and types

| Type | Holds | In Postgres |
| --- | --- | --- |
| `text` | words and names | `text` |
| `integer` | a whole number (a count, an amount in kobo) | `bigint` |
| `number` | a number with decimals | `numeric` |
| `boolean` | yes or no | `boolean` |
| `date` | a day, `2026-11-01` | `date` |
| `datetime` | a date and time, RFC 3339 | `timestamptz` |
| `phone` | a phone number with the country code, `2348012345678` | `text` |
| `email` | an email address | `text` |
| `json` | structured data | `jsonb` |

A column may be `required` (`NOT NULL`; a required column cannot be added to a table that already has rows) and `personal` (see [personal data](#personal-data)); `phone` and `email` columns are personal unless the table says otherwise. Columns are added with `POST /v1/tables/{id}/columns` and dropped with `DELETE /v1/tables/{id}/columns/{name}`, which drops the column's data and asks for the column's name typed as `confirm`, as Xenibase's own editor does. The title and description change with `PATCH /v1/tables/{id}`; a column's name and type do not change (drop it and add another).

## The page

Tables lists the organisation's tables with their columns and an estimated row count. A table's page shows its rows fifty at a time with a search (`column:text`, or text alone on the first text column), inline add, edit and delete, CSV import and export, the columns with their types, and "Use in a workflow": the connection name, the table name and the connector actions a step uses. An owner sees the data project's state at the bottom of the Tables page, with "use my own Xenibase project" ([below](#bringing-your-own-project)).

## Rows by API

Rows go through the connector's actions on the `data_tables` connection, exactly as a workflow step's would, after Taskiem checks the values against the columns' types (a `phone` must look like a phone number, an `integer` be whole, a `date` be `YYYY-MM-DD`, a required column be present) and before anything is sent. Every connection read is in the secret reads (`GET /v1/secrets/reads`, purpose `tables.api`) like a step's.

| Call | Does |
| --- | --- |
| `GET /v1/tables/{id}/rows?limit&cursor&q` | a page of rows, newest first |
| `POST /v1/tables/{id}/rows/query` | a page by the connector's structured `filters` and `order`, with an optional `count` |
| `POST /v1/tables/{id}/rows` | inserts `row` (one) or `rows` (up to 1,000) |
| `PATCH /v1/tables/{id}/rows/{rowID}` | `{"set": {...}}` on one row |
| `DELETE /v1/tables/{id}/rows/{rowID}` | one row |

Every write carries an `Idempotency-Key` to Xenibase's data API: the request's own header when it sends one, a fresh one otherwise. Send the same key again after a lost answer and Xenibase answers the first attempt (`replayed: true`) instead of writing twice; it keeps keys for 24 hours.

## CSV

`POST /v1/tables/{id}/import` takes a multipart form with a `file` field: a CSV whose header row names the table's columns, by name or title in any case and spacing (`Phone Number` is `phone_number`; `created_at` and `updated_at` are ignored), and a `mapping` field for the headers that match none (`{"Notes": ""}` leaves one out, `{"Tel": "phone"}` puts it in a column); the web app asks about each such header. **Make a table from a file** on the Tables page makes a table with a column per header, typed from its values (numbers, phone numbers, dates, else text), then imports the file into it. It is up to 5,000 rows per call, written in batches of 500. With an `id` column the rows are upserted on `id` (so an export can be changed and imported back); without one they are inserted. Each batch carries its own idempotency key from the import id and the batch's index. The import id comes from the request's `Idempotency-Key` header, or without one from the file itself (and always the table), so the same import sent again after a lost answer or a failed batch writes nothing twice: the batches already in are answered from Xenibase's replay (`replayed_batches`; within its 24 hours). To import the same file again on purpose, send a new `Idempotency-Key`. The audit entry (`table.import`) records counts only.

`GET /v1/tables/{id}/export` answers `text/csv`: `id`, the columns, `created_at` and `updated_at`, by id, up to 100,000 rows. A text, phone, email or JSON cell that a spreadsheet would run as a formula (one starting with `=`, `+`, `-`, `@`, a tab or a carriage return) is written with a leading `'`, which spreadsheets hide and keep as text; an import takes it off again, so an export imports back unchanged. If Xenibase fails partway, the response is aborted, so the download fails instead of looking complete. Each export is audited as `table.export` with the number of rows and whether it completed; adding, changing and deleting rows through the API are audited as `table.row.insert`, `table.row.update` and `table.row.delete` (counts and row ids, never values).

## Using a table in a workflow

A data table is an ordinary Xenibase table to a workflow: the connector is `xenibase@1`, the connection `data_tables`, and `table` the table's name. Reads use `read_rows` (typed values, filters, order, pages); writes use `insert_rows`, `upsert_rows`, `update_rows` and `delete_rows`, each with the engine's idempotency key, updates and deletes by `key` or `filters` with a `max_affected`.

```json
{"id": "read_debtors", "type": "connector", "connector": "xenibase@1", "action": "read_rows", "connection": "data_tables",
 "input": {"table": "debtors", "filters": [{"column": "balance", "op": "gt", "value": 0}], "order": [{"column": "name"}], "limit": 1000}}
```

```json
{"id": "mark_paid", "type": "connector", "connector": "xenibase@1", "action": "update_rows", "connection": "data_tables",
 "needs": ["read_debtors"], "input": {"table": "debtors", "key": "=item.id", "set": {"paid": true}}}
```

A template may declare the tables it works on, and Taskiem makes the missing ones when the template is used ([templates](templates.md#tables-a-template-makes)): the `pos-*` templates make `pos_sales`, `pos_terminals` and `pos_cashups`, the record of a business's [POS terminals](strategy/pos-terminals.md). The template [`debtor-reminder-table`](templates.md) is the weekly debtor reminder over a `debtors` table (`name`, `phone`, `balance` in naira) instead of a sheet. The AI builder's context lists the organisation's tables with their columns and types (never rows), so a goal such as "remind my customers who owe me" names the table rather than a spreadsheet, and a goal that needs a table that does not exist proposes it, to be made when the draft is saved ([AI](ai.md)); `wdtext` reads such a step as any Xenibase step ("Read rows (data API) (Xenibase)"), since it never prints a step's inputs.

### The row-changed trigger

A workflow can start when a row of a table is added, changed or deleted: the `xenibase@1` `row_changed` trigger on the `data_tables` connection, with `options.tables` naming the table. Taskiem registers the webhook in the project when the workflow is published and removes it when it leaves the environment ([decision 0021](decisions/0021-remote-trigger-registration.md)), so an organisation on the platform's Xenibase gets webhooks within the project's plan rate.

```json
{"type": "connector_event", "config": {"connector": "xenibase@1", "trigger": "row_changed", "connection": "data_tables", "events": ["INSERT"], "options": {"tables": ["orders"]}}}
```

## On WhatsApp

A person whose number is linked can send `tables` for the list, `add to <table>` to add one row (a form where Flows are set up, else column by column; the row is shown with personal values masked and added only after **yes**) and `find <table> <text>` for up to five matching rows. The same permissions apply, the values are checked by the same code, the connection reads are recorded under the purpose `tables.chat`, and the insert carries an idempotency key from the conversation. Creating or changing tables, and editing or deleting rows, stay here. See [WhatsApp: data tables](whatsapp.md#data-tables).

## Personal data

Columns of type `phone` and `email`, and any column flagged `personal`, are personal data. The table page and the rows API return them as they are to people with `table.read` (the page is the record), and the connector declares rows as personal data, so they are masked in run outputs, in chat and in logs; they never appear in audit details, and the AI builder sees names and types, never rows. Keep the `table.read` permission to the people who should see the records.

## Limits

| Limit | Default | At the limit |
| --- | --- | --- |
| `max_data_tables` | 20 (starter 10, growth 50, business and above no limit) | creating another table gets 429 `limit_exceeded` |
| `max_data_table_columns` | 50 | a table with more columns is refused, and adding one gets 429 |

Rows, requests and size are the Xenibase project's own quotas: the data API's `plan_limit_reached` (429 `quota_exceeded` here) and the project's storage limit (`read_only`, 409).

## Deleting a table

`DELETE /v1/tables/{id}` with `{"confirm": "<the table's name>"}` drops the Postgres table and every row (Xenibase keeps the project's backups on its own schedule), then forgets the table; it is audited as `table.delete`. Decision 0033 has an organisation's project deleted with the organisation; Taskiem has no organisation deletion yet, so until it does an operator deletes the project in Xenibase.

## For operators

### Configuration

| Variable | Notes |
| --- | --- |
| `TASKIEM_XENIBASE_URL` | The platform's Xenibase (`https://…`). Also the default server for Xenibase connections that name none |
| `TASKIEM_XENIBASE_TOKEN` | An API token on that Xenibase: unrestricted (not limited to projects), with the `read`, `write` and `admin` scopes (project creation needs an unrestricted `write` token; enabling services, minting keys and dropping tables by token need `admin`). It stays in the `api` and `scheduler` processes, is never stored in a tenant's vault, never logged and never used by a workflow. Xenibase tokens last a year at most: rotate it before it expires, since the tokens Taskiem mints from it never outlive it. When it has less than a year left, organisations' tokens last only as long, and renewing them fails, with an alert, until it is replaced |
| `TASKIEM_XENIBASE_ORG` | The Xenibase organisation Taskiem makes projects in. The platform pays for them as part of the plan (decision 0033, Q9); raise its project quota as organisations grow |
| `TASKIEM_XENIBASE_REGION` | Optional: the region projects are made in (Xenibase's home region by default) |

Without the first three, creating a table answers 503 `tables_unavailable`, `GET /v1/tables/status` says `available: false`, and the Tables page says so; an organisation that brought its own project still works. Set them on the `api` role (provisioning runs in the request that creates the first table) and the `scheduler` role (token renewal).

### How provisioning works

The first table an organisation creates runs, in the request, with the platform token:

1. records the intent (`data_table_projects`, status `creating`, the project name `taskiem-<tenant id>`) and takes a lease, so a second request at the same time answers 409 `tables_provisioning`;
2. looks for a project of that name in the organisation (a project made by an attempt whose record was lost is adopted, never made twice), else `POST /api/v1/projects` (shared tier, the configured region) and waits for the create operation (`GET /api/v1/operations/{id}`, up to five minutes);
3. turns backend services on (`POST /projects/{id}/services`) and keeps the secret key from that one answer; services already on from an earlier attempt get a fresh secret key (`POST /services/keys`) instead;
4. mints a project-restricted token named `taskiem-data-tables` with the `read`, `write` and `admin` scopes for 365 days, or for as many whole days as the platform's token has left when that is less (`GET /api/v1/me`, then `POST /api/v1/tokens`: Xenibase refuses a token that outlives the one making it);
5. stores the `xenibase` connection `data_tables` in `prod` with `token`, `project_id`, `org_id`, `base_url`, `secret_key` and `api_url`, marked `provisioned_by = taskiem:data_tables` (the Connections page shows it as "Data tables"), and records the project as ready, audited as `table.project.provision`.

Each step is recorded before the next, so a process that dies half way leaves a record the next request resumes from the same step. A Xenibase refusal is recorded (`GET /v1/tables/status` shows `error`): an organisation quota answers 409 `xenibase_quota` with the quota's name, `no_capacity` and other temporary conditions 503 `xenibase_unavailable` with `Retry-After`, and a create operation that failed leaves the project `failed` until an operator looks at it in Xenibase. The per-call timeout is 20 seconds, through the egress guard to the platform's host only.

### Token renewal

Xenibase tokens expire. The `scheduler` role renews, every six hours, the token of every platform-provisioned project whose token expires within 14 days: a new one minted with the platform token replaces the connection's credentials (`connection.credentials_replace` in the audit log, with `table.project.token_renewed`). A renewal that fails is logged at error and raised to the organisation as an alert (`data_tables_token`, once a day) so its owners know the table will stop working when the token does; the usual cause is a platform token that expired or lost the `admin` scope. Own projects are never renewed.

### Bringing your own project

Settings > Data tables (the "Data project" section of the Tables page, for `connection.manage` holders) takes an existing `xenibase@1` connection of the organisation's own in `prod`: a token restricted to the project with `read`, `write` and `admin` (drops by token need `admin`), the project's secret key and its data API URL. Taskiem runs `test_connection`, refuses a connection without a secret key or data API URL, and copies the credentials into the `data_tables` connection (the original stays), so workflows and the API find the project under the one name; `PUT /v1/tables/project` does the same with `{"connection_id": …}`, audited as `table.project.set`. Once any table exists the project cannot change, with one exception: a connection to the same project (the same `project_id`) of the organisation's own may be set again, which is how its credentials are renewed (audited with `renewed_credentials: true`). For an own project Taskiem provisions nothing, renews nothing and deletes nothing: when its token nears expiry, or after it has expired, the organisation makes a new connection to the project and sets it here.

### What happens on delete

Deleting a table drops the Postgres table through the schema API (`drop_table` with `confirm`; a table already gone in Xenibase is forgotten without error). Deleting a column drops it the same way. When organisation deletion is added, a platform-provisioned project goes with the organisation (decision 0033) and an own project is left as it is; today an operator deletes the project in Xenibase.
