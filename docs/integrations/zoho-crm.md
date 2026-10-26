# Zoho CRM integration

The connector is `connectors/zohocrm` (`zoho_crm@1`). It works with Zoho CRM's API v8:

- adds and updates leads, contacts, deals and any other module's records, without duplicates;
- reads and searches them;
- adds notes;
- starts workflows when records are added, changed or deleted.

Built from Zoho's public developer documentation, read 26 October 2026; the manifest header lists the pages. It is K6 of [business connectors](../spec/business-connectors.md#73-zoho_crm1).

## Connecting (one click)

A business connects with **Connect Zoho CRM**, through the same Zoho client as [Zoho Books](zoho-books.md#connecting-one-click) (`client: zoho`). Every call goes to the account's API domain (`<api_domain>/crm/v8`), held to Zoho's hosts (rule 19). Tokens are refreshed and kept as for Books (`connectors/internal/zoho`).

The scopes are `ZohoCRM.modules.ALL`, `ZohoCRM.settings.modules.READ` and `ZohoCRM.notifications.ALL`.

## Actions

Records are CRM's own: fields by their API names (`Last_Name`, `Email`, `Phone`, `Company`, `Deal_Name`, `Stage`, `Amount`, and a lookup as `{"id": …}`), modules by theirs (`Leads`, `Contacts`, `Deals`, the business's own).

| Action | Class | What it does |
| --- | --- | --- |
| `list_modules` | read | The modules the API reaches; the connection's check |
| `upsert_record` | idempotent write | CRM's upsert: matched by the module's unique fields (Leads and Contacts by Email, Deals by Deal_Name, Accounts by Account_Name, or `duplicate_check_fields`) and updated, or added |
| `update_record` | idempotent write | Sets fields of a record by its id |
| `get_record` | read | A record |
| `search_records` | read | By email, phone, a word, or CRM's criteria |
| `add_note` | idempotent write | A note on a record; the same title and text already there is answered rather than added twice |

**Never twice.** CRM's upsert makes a retry update the record it added, rather than add a second. `run_workflows: false` keeps CRM's own workflow rules from running for the change.

**Errors.** CRM answers `{"code", "details", "message", "status": "error"}`, for a whole request or for each record of a write:

| Answer | What happens |
| --- | --- |
| 401, `INVALID_TOKEN` | The token is refreshed once, then the connection is expired |
| 429 | Retried |
| `INVALID_URL_PATTERN`, `INVALID_MODULE`, 404 | `not_found` |
| Other refusals (`MANDATORY_NOT_FOUND`, `INVALID_DATA`, `DUPLICATE_DATA`) | Fatal, with the field named |
| 5xx | Unknown outcome |

## Triggers

### `record_changes` (CRM's notification watches)

A workflow on `record_changes` names the module it watches (`options.module`) and the events it wants: `insert`, `update` or `delete`. Taskiem then asks CRM to watch the module (`POST /actions/watch`, registration remote: decision 0021):

- **The address.** CRM sends each notification to the workflow's own address.
- **The token.** It carries a token of Taskiem's in the body. CRM signs nothing, so the token is compared in constant time (`body_token`).
- **Renewal.** A watch lasts a week at most. Taskiem asks for six days and renews it a day before it lapses (`PATCH /actions/watch` with a new expiry). The reconciler plans that from the expiry CRM answers.
- **After a crash.** A watch found that way, whose token Taskiem never kept, is stopped and made again.
- **Stopping.** Undeploying stops the watch.

What a run gets:

- `trigger.item`: `id` (the record), `module`, `operation`;
- `trigger.body`: CRM's notification: `ids`, `server_time`, `channel_id`.

One notification naming several records starts one run for each. Read the record with `get_record`.

- **Once each:** by the record, the operation and CRM's time.
- **Correlation:** the record, so a run waiting on a deal wakes when it changes.

## Templates

- **Record paid invoices in Zoho CRM** (`zoho-crm-paid-invoices`): when an invoice is paid in full, the customer is upserted as a contact (by email, or by phone without one) and the invoice as a deal, closed won, for its amount, named by the invoice number.

## Needs a person

- A Zoho CRM organisation for a live check: connect, upsert twice, search, a note, a watch made, a notification through ingest, and a renewal (K0-5).

## To confirm with Zoho

1. That `PATCH /actions/watch` with a new `channel_expiry` renews the watch (its page says only that the details sent are kept).
2. The shape of `GET /actions/watch` (each watch's `channel_id` and its events' `channel_expiry`) and `DELETE /actions/watch?channel_ids=`.
3. The notification's `operation` values (`insert`, `update`, `delete`).
4. That `GET /{module}/{id}/Notes` lists a record's notes in v8.
5. CRM's API credits per edition, against the calls a busy workflow makes.
