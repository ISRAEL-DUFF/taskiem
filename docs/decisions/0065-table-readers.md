# 0065 — Per-table readers: a data table's rows for some roles only

Date: 2026-11-07 · Status: **Accepted** 2026-11-07 (built: [data tables](../data-tables.md#who-may-read-a-table), [tickets §8.4](../spec/tickets.md#84-staff-it-and-hr-staff-it-desk-staff-hr-desk), [guide](../guide/tickets.md#it-and-hr-requests-from-your-staff)) · Amends: [0033](0033-data-tables-on-xenibase.md) (who reads a table's rows), [0064](0064-ticket-kit.md) (where HR requests are kept, and who answers them)

## Context

`table.read` is all or nothing (decision 0033): whoever holds it reads every data table's rows, and viewers, operators, auditors, builders and admins hold it. The HR help desk (`staff-hr-desk`, decision 0064) kept its requests (pay, health, complaints) in the shared `tickets` and `ticket_messages` tables beside customers' tickets, so every one of those people could read them, and the shared reply form, open to every member, could answer them. A business must be able to keep some records for some people.

## Decision

1. **A table may name readers:** membership roles, built-in, custom or business ones (`data_tables.readers`, migration 00420; null, the default, is every `table.read` holder; an empty list, meaning nobody, is refused by the API and by a check constraint). With readers, a person reads the table's rows only when they hold one of the roles or are the owner, on top of `table.read`, and changes them only so and with `table.edit`. An API key reads as its owner's roles now.
2. **Every path a person reaches rows by is checked in one place**: the API's `tableRowsFor`, which the rows API, the query, the export, the import, chat's find, add, change and delete, chat's file import, chat's questions, and the storefront's and stock's member paths all go through. The table's schema changes (title, columns, deleting it) need a reader too. A request without a person (a public storefront page, a background job) is Taskiem itself and is not limited.
3. **Others still see that the table exists**: its name, columns and readers are listed to every `table.read` holder, with `can_read: false`, so they know whom to ask; its row count is not given. Chat says who may read it; the model answering questions over the tables is not even told of it.
4. **Readers change with `role.manage`** (and `table.read`), audited as `table.readers.set` and `table.readers.clear` with the roles before and after. Readers say what a role grants, as a custom role's permissions do, which is `role.manage`'s; `member.manage` decides who holds a role, and is kept apart so the two together are needed to let a person in. Only someone who may read the table now may change its readers, so setting them never lets the setter into rows they could not read. A new table may name readers with `table.edit` alone, since it holds nothing yet; templates make their tables so. A kept table's readers do not change.
5. **Workflows are not limited.** They reach rows through the `data_tables` connection as before, which is how the HR desk works. So whoever can edit and publish workflows can write one that reads a restricted table: `workflow.edit` and `workflow.publish` are trusted permissions (threat model).
6. **Known tables may declare readers**, fixed role names: `hr_tickets` and `hr_ticket_messages` (readers `hr`, `hr_lead`), and `hr_confidential_tickets` and `hr_confidential_messages` (`hr_lead`), shaped as `tickets` and `ticket_messages`. A table of that name the business made before is left as it is, and the readers the template wants are reported for someone with `role.manage` to set.
7. **The HR desk keeps its own tables.** `ticket-open` takes `desk: hr`: the request goes to the HR tables (references `HR-1042`), or, when its department's role is `hr_lead`, to the confidential ones (`HC-1042`). `ticket-answer` finds a request's tables by its reference's prefix, `ticket-follow-up` follows one desk (`customers`, `hr` or `hr_confidential`) and escalates HR requests to `hr_lead` alone. Two members' forms answer them: `hr-ticket-reply` (form roles `hr`, `hr_lead`; `HR-` numbers only) and `hr-confidential-reply` (`hr_lead`; `HC-` numbers only). The shared `ticket-reply` takes only `T-` numbers and reads only `tickets`.

## Alternatives considered

- **A permission per table** (`table.read:hr_tickets`). Permissions are a fixed list the role editor shows; a table is the business's own, made and named at any time.
- **Hiding a restricted table altogether.** People would not know it is there or whom to ask, and a table of the same name could not be explained; its name and columns are not the secret, its rows are.
- **`member.manage` to change readers.** Whoever gives roles could then also widen what a role reads; with `role.manage` the two stay separate, as for custom roles.
- **Roles from template parameters.** A parameter can fill one string, and a form's `roles` list element, but the HR desk's roles come from its `departments` lines, read at run time. The roles are fixed (`hr`, `hr_lead`); a business using others changes the tables' readers on the Tables page.
- **Confidential requests as one table holding tickets and messages.** Every piece of the kit works on a tickets table and a messages table; a second pair of the same shape lets them all be reused unchanged, choosing the pair by the reference.
- **Keeping confidential text out of the shared HR tables** (a placeholder row in `hr_tickets`). `hr` would still see who raised a confidential matter and when.
- **One reply form checking the respondent's roles.** `trigger.form` carries the member's id, not their roles; two forms with fixed `roles` are checked by the form host before a run starts.

## Consequences

- `data_tables.readers` (migration 00420) and its column grants; `taskiem_dispatch`'s table-wide `SELECT` covers it (`dispatch_columns.txt`). RLS is unchanged.
- `PUT` and `DELETE /v1/tables/{id}/readers`; tables answer `readers` and `can_read`; a new table and a template's table may name `readers`; 403 `table_restricted` on the row and schema routes.
- HR requests made before this change stay in `tickets` with `T-` references: the HR desk's new requests go to the HR tables, and an owner moves the old ones by hand if they want them restricted.
- The follow-up is published once per desk; the HR desk's needs `desk: hr`, and `hr_confidential` for confidential requests.
