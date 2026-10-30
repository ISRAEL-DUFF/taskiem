# No sheet needed: records kept in chat, and the templates that use them

Status: **Draft** 30 October 2026, for review (revised the same day after a review against the code) · Decision: [0060](../decisions/0060-no-sheet-needed.md) (proposed) · Builds on: [data tables](../data-tables.md) ([0033](../decisions/0033-data-tables-on-xenibase.md)), [templates](../templates.md), [WhatsApp: data tables](../whatsapp.md#data-tables), [chat as the way in](chat-way-in.md) ([0058](../decisions/0058-chat-as-a-way-in.md)), [stock](stock.md) ([0057](../decisions/0057-stock-by-chat.md)), [the money log](../money-log.md) ([0045](../decisions/0045-tally-in-taskiem.md)), [languages](../languages.md) ([0029](../decisions/0029-languages-and-voice-notes.md)) · Strategy: [chat-first](../strategy/chat-first-nigeria.md#33-xenibase-under-the-agent) §3.3, the "Xenibase underneath" row of §4

## 1. Summary

A trader can now sign up, connect services and publish workflows without leaving WhatsApp ([chat as the way in](chat-way-in.md)). But the workflows they reach for first still need a Google Sheet. Eight templates read or write one, among them both debtor reminders, the new-customer welcome and the daily sales summary. A trader in Onitsha has no sheet. They have a notebook, and a phone.

Data tables were built for this ([decision 0033](../decisions/0033-data-tables-on-xenibase.md)): records a business keeps in its own Xenibase project, read and written by workflows through the `data_tables` connection. In chat today a person can list tables, add a row and find rows. Making a table, changing one, correcting or removing a row, and asking a question such as "who owes me?" all need the web app.

This plan closes both gaps:

- **Part A: templates without a sheet.**
  - Every sheet template gets a table twin, or a feature that already does its work.
  - A business with no Google Sheets connection is offered only the twin, on every surface, including the AI builder.
  - A twin makes its tables when it is used, from the web or from chat.
  - An owner who does have a sheet can send it, or point at it, and it becomes a table.
- **Part B: tables fully in chat.**
  - Make a table, from a known shape (debtors, customers, staff) or from the owner's own words.
  - Add a column.
  - Change or remove a row.
  - Keep a debtor book in plain words: `Ada owes 5,000`, `Ada paid 2,000`, `who owes me?`.
  - Ask questions over any table.
  - The model routes all of these in every language, but only to the same commands, and it never sees a row.

Seven milestones, R1 to R7. R1 alone makes "remind my customers who owe me" work with no sheet from a first chat.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Sheet template | A template whose connectors include `googlesheets@1` |
| Twin | The template a business with no sheet is offered instead: the same job over a data table, or over a record Taskiem already keeps (the money log, stock) |
| Known table | A table shape Taskiem knows by name and purpose (`debtors`, `customers`, `staff`), defined once and used by templates, chat words and the AI builder. The table is the business's own, an ordinary data table, not a [kept](../data-tables.md#kept-tables) one |
| Debtor book | The `debtors` known table used by its chat words |
| Query spec | What a question over rows becomes before anything is read: a table, filters, an order, a limit and at most one count or sum of a column, every name checked against the table's columns |

## 3. Where we are

| Exists | Where |
| --- | --- |
| Data tables: one Xenibase project per business, made on the first table; columns typed (`text`, `integer`, `number`, `boolean`, `date`, `datetime`, `phone`, `email`, `json`); rows through the data API with idempotency keys; CSV import and **Make a table from a file**; kept tables; limits | [data tables](../data-tables.md), `api/datatables*.go`, `engine/datatables` |
| In chat: `tables`, `add to <table>` (a Flow where set up, else column by column, added after **yes**, personal values sealed and masked), `find <table> <text>` (five rows) | `api/whatsapp_tables.go`, [WhatsApp: data tables](../whatsapp.md#data-tables) |
| Templates that declare their tables (`tables`), made after the draft is saved when missing (`created`, `exists`, `skipped`, `failed`) | [templates: tables a template makes](../templates.md#tables-a-template-makes), `api/templates.go` `templateTables` |
| Eight sheet templates: `daily-sales-summary`, `debtor-reminder-sms`, `debtor-reminder-whatsapp`, `new-customer-welcome`, `salary-payout-approval`, `sales-ledger-sheet`, `stock-reorder-alert`, `transfer-received-notify` | `templates/library/` |
| Replacements: `salary-payout-approval` is `superseded_by` `run-payroll` (a staff table); `stock-reorder-alert` is `replaced_where` `keeps_stock` by `low-stock-alert`; `daily-sales-summary` is `replaced_where` `records_sales` by `sales-day-total` | `templates/templates.go` (`ReplacedWhere`, `For`), `api/templates.go` `templateHolds` |
| One table twin: `debtor-reminder-table` (SMS over a `debtors` table with `name`, `phone`, `balance`); it declares no `tables`, so the table must already exist | `templates/library/debtor-reminder-table.json` |
| The AI builder is told to prefer data tables over sheets, and proposes a missing table to be made on save | `engine/ai/builder/prompt.go` rule 12 |
| Building from chat: a picked template or a goal becomes a draft, the tables it proposes are made after **yes** (N6), then its settings, then **Publish it now?** | `api/whatsapp_build.go`, `api/chat_publish.go` `waBuildFinish` |
| Voice notes and photos reach the conversation. WhatsApp document messages are dropped by the webhook. Telegram turns only an image sent as a document into a photo and drops other documents, and its file type has no file name. The Slack app has no `files:read` scope | `engine/whatsapp/webhook.go`, `engine/telegram/update.go`, `connectors/slack/manifest.yaml` |
| Rows: every table has `id`, `created_at` and `updated_at`, but `updated_at` is only a default set when the row is made, with no trigger, and no write changes it | `engine/datatables/schema.go` |
| The connector's reads: `read_rows` returns typed rows with `id` and `updated_at` and can count the matching rows (`count: exact`); `query_rows` has no count; no action sums | `connectors/xenibase/manifest.yaml` |
| Chat's money words (`cash in`, `expense`) need `invoice.manage`; an entered money-log entry has a channel (`cash` by default) and a description, but no payer | `api/whatsapp_money.go`, `engine/moneylog/statement.go` (`Entered`) |
| Chat makes a table with `chatMakeTable` (the same checks and limits as the Tables page's `newDataTable`, which needs an HTTP request) | `api/whatsapp_storefront.go`, `api/datatables.go` |
| The model routes members' messages in languages other than English to a fixed set of intents with a short argument, never yes, no or cancel; the table intents are not among them | `engine/ai/intent` (`Allowed`) |
| The chat PIN, for approvals whose policy accepts it. It is entered in a WhatsApp Flow, so it exists on WhatsApp only: Telegram's PIN is not built and Slack has none | [WhatsApp: approval PIN](../whatsapp.md#approval-pin) |

### 3.1 Gaps found while writing this

- **The builder still reaches for a sheet first.**
  - `replaced_where` is applied to the template gallery only (`api/templates.go:144`).
  - The AI builder's retrieval (`engine/ai/builder/builder.go:579`, top two matches) applies no conditions.
  - For "remind my customers who owe me" it offers `debtor-reminder-sms` first and `debtor-reminder-table` second, whatever the business has connected.
  - Prompt rule 12 says to prefer tables, but the template with the highest score arrives with its connectors' schemas.
- **Templates picked in chat do not get their tables made.**
  - `chatTemplateBuild` (`api/chat_signup.go:751`) builds the proposal with `Tables: []builder.TableSpec{}`.
  - So a template declaring tables, picked from the sign-up's starters, saves a draft whose table does not exist. The starters are the only templates chat offers by name: there is no `templates` command.
- **An AI build from a template doesn't make the template's tables either.**
  - The proposal's `tables` are only those the model describes (`engine/ai/builder/builder.go:401`), and the template context it is given lists none.
  - "Remind my customers who owe me" in chat is an AI build (`waStartBuild`), not a template pick.
- **`debtor-reminder-table` declares no table.** It and `bank-alert-email` "still expect their table to exist" ([templates](../templates.md#tables-a-template-makes)).

## 4. What stays as it is

- **Sheet templates stay.**
  - A business that connects Google Sheets is offered them as today.
  - Workflows made from them keep working; nothing migrates a workflow from a sheet to a table on its own.
- **Data tables stay as they are.** Xenibase, the data API, idempotency keys, kept tables and limits do not change. No new storage is added.
- **Permissions stay as they are.**
  - `table.read` to list, find and ask.
  - `table.edit` to make or change tables and rows.
  - Kept tables are changed only by Taskiem's own writers, in chat too.
- **Personal data is handled as today.** Phone and email columns, and any column marked personal, are masked in chat and logs.
- **The model's limits are unchanged.** It routes and drafts. It never confirms, approves, publishes or changes a row, and it is never sent a row.
- **The web app's Tables page is unchanged**, and stays the place for bulk work: imports over 5,000 rows, a column's type, a table's deletion where chat does not allow it (§9.3).
- **Rows written elsewhere.** The guard on a chat write compares the values the person saw (§6.3), so it works whoever changed the row: the web, a workflow, an import, or Xenibase directly.

## 5. Part A: templates without a sheet

### 5.1 The twins

| Sheet template | Without a sheet | New? |
| --- | --- | --- |
| `debtor-reminder-sms` | `debtor-reminder-table`, now declaring the `debtors` known table | Changed |
| `debtor-reminder-whatsapp` | `debtor-reminder-table-whatsapp`: the same over `debtors`, sent as the approved WhatsApp reminder template | New |
| `new-customer-welcome` | `customer-welcome-table`, started by a new row in `customers` (§5.3) | New |
| `daily-sales-summary` | `sales-day-total` (sales recorded by chat or on the Stock page) | Existing |
| `stock-reorder-alert` | `low-stock-alert` (Taskiem's stock) | Existing |
| `sales-ledger-sheet` | `payment-received-tell-owner`: the money log already records every payment, so the twin only tells the owner | New |
| `transfer-received-notify` | `payment-received-tell-owner` | New |
| `salary-payout-approval` | `run-payroll` (already its `superseded_by`) | Existing |

Each new twin:
- passes every check the library's tests make: publishing checks, exactly its declared connectors, policy, dry run, plain read-back and the flow-code round trip;
- comes with `wd-test/v1` cases of its own;
- declares its known tables, so using it makes them.

### 5.2 Offering the twin

- **A new condition, `no_sheets`.**
  - It holds for a business with no `googlesheets@1` connection in any environment.
  - It joins `keeps_stock` and `records_sales` in `templates.ReplaceWhen`.
  - `templateHolds` computes it first. Today it returns an empty set when stock is off (`api/templates.go:432`), which would hide the condition on such a deployment.
- **More than one condition per template.**
  - `replaced_where` becomes a list of `{by, when}`, while a single object is still read as today.
  - So `daily-sales-summary` is replaced where `records_sales` holds, and also where `no_sheets` holds.
  - The library's load check still requires each `by` to be offered and each `when` to be known.
- **The same rule everywhere a template is offered:**
  - the gallery and the Start page, as today;
  - the AI builder's retrieval: `Library.Match` takes the business's conditions, the API passes `templateHolds`, and a replaced template is never in the two matches;
  - the sign-up's starters, which are hard-coded today and name no sheet template, so nothing changes there until they are chosen from the library.
- **What it means for a business with Sheets connected.** It sees both the sheet template and its twin. The gallery lists a twin just before the template it replaces. When both match a goal, the builder's retrieval ranks the twin above the sheet template unless the goal names a sheet or a spreadsheet. A business that connects Google later starts seeing the sheet templates again.
- **The builder's prompt.** Rule 12 gains one sentence: a goal that names a sheet or spreadsheet uses Google Sheets, and any other goal about a list uses a data table.

### 5.3 Twins that start from the table

`customer-welcome-table` starts when a customer is added to the customers table (`xenibase@1` `row_changed`, `INSERT`, on `customers`). That covers:
- `add to customers` in chat;
- an import;
- a website that adds the customer through the rows API (`POST /v1/tables/{id}/rows`), in place of the sheet template's webhook.

A template has one trigger, so there is no second way in to choose. Storefront orders do not add customers (Q4). A business that keeps its customers by chat gets the welcome text with nothing to wire.

### 5.4 Known tables

Known tables are defined once in `templates/tables/` as JSON, so the definition is used by:
- templates (`tables: [{"known": "debtors"}]`, a new `known` field on `templates.Table`, or a parameter with the table picker defaulting to it);
- the chat words (§6.4);
- the AI builder's context, which lists them as tables it may propose by name;
- making a table in chat (§6.1).

| Known table | Columns |
| --- | --- |
| `debtors` | `name` text (required) · `phone` phone · `balance` number (naira) · `due_on` date · `note` text |
| `customers` | `name` text (required) · `phone` phone · `email` email · `area` text · `marketing_opt_in` boolean · `email_status` text. Personal: name, phone and email |
| `staff` | `name`, `phone`, `email`, `net_pay`, `bank_code`, `account_number`, `account_name`, `active`: the shape `run-payroll` declares today, moved here unchanged |

Rules for known tables:
- **Existing tables are left alone.** A business that already has a table of that name keeps it as it is.
  - Today `templateTables` reports such a table as `exists` without looking at its columns (`api/templates.go:242`).
  - This plan adds the check, as new work: a table that lacks a column the template needs is reported as `exists` with `missing: [...]`, in plain words in chat and on the web, and the draft is saved either way.
- **The marketing templates keep their own shape.** The Mailchimp, Brevo and Resend templates declare a `customers` shape keyed on `email`, which is required, with `phone` as text. A chat-first business's customers often have no email, so the known `customers` table does not require it.
  - The marketing templates are not moved onto the known table and are unchanged.
  - Where a business has a `customers` table from one of them, it is used as it is (an email is then asked as required by `add to customers`), and the column check above reports what a twin lacks.
- **A known table is not kept.** The owner can change it in the web app or in chat like any other.

### 5.5 Tables made from chat builds

- `chatTemplateBuild` carries `tpl.TablesFor(params)` into the proposal's `tables`, so `waBuildFinish` makes them after **yes**, as it does for an AI build.
- **The builder does the same for a draft made from a template.** When its proposal names the template it was instantiated from (`Proposal.Template`), the template's `TablesFor` over the filled parameters is merged into `tables`, skipping tables that exist and ones the model already described. This is the path a goal typed in chat takes.
- Each table's result is said in the chat: made, already there (with any missing column), or not made and why. Making one may provision the business's project (a minute or two). Chat then says so and carries on once it is ready (§9.1, part of R1, since R1 is the first to make tables from a first chat).
- `debtor-reminder-table` and `bank-alert-email` declare their tables.

### 5.6 Bring your sheet

An owner who already keeps a list somewhere can bring it into a table:
- **A file sent in chat.**
  - A CSV or Excel file (`.csv`, `.xlsx`, first sheet only).
  - **WhatsApp:** a document message. This is new: the webhook drops documents today.
  - **Telegram:** a document. This is new: `update.go` gains non-image documents with their file name, read with `getFile`.
  - **Slack:** not in this plan. Reading a DM's file needs the `files:read` scope, which means asking every workspace to reinstall the app (a people item, §10 Q6). Until then a Slack DM answers with the web link.
  - Taskiem asks: **Make a new table**, **Add to <table>**, or **Not now**.
    - **Make a new table** is named from the file, with its columns typed from its values. That typing lives in the web app today (`web/src/lib/tables`) and is ported to Go, with the web keeping its own.
    - **Add to <table>** matches headers by name or title, and asks about any header that matches none by number.
  - The rows are written after **yes**, shown as the count and the first three rows, with personal values masked.
  - **The same import code as `POST /v1/tables/{id}/import`.** The import is moved out of its HTTP handler into a function both call. It has the same limits (5,000 rows, batches of 500).
  - **Sending a file twice writes once.** The import id is derived, as the API derives it when no key is sent, from the file and the table, never the message. A retried delivery, or the same file sent twice, writes nothing twice within Xenibase's 24 hours.
- **A Google Sheet, once.**
  - For a business with a Google Sheets connection, `copy my sheet` (or **Copy into a table** after a sheet template is picked) asks for the sheet's link.
  - It reads the range once with `googlesheets@1` `get_values`, then goes on exactly as a file does.
  - Nothing is synced afterwards, and the chat says so.

Rules for files:
- Files are fetched with the channel's media download, never from a link the message carries.
- Size limits are 2 MB and 5,000 rows.
- `.xlsx` is unzipped with caps on entry count and expanded size.
- The file is not kept after the import. Its rows are audited as counts, never values.

## 6. Part B: tables fully in chat

Everything here works on WhatsApp, Telegram and Slack DMs through the one conversation (`converse`). Forms are WhatsApp Flows where they are set up, with Telegram's Mini App added for a row's fields (§9.2). Otherwise questions come one at a time, as `add to` does today.

### 6.1 Making a table: R3

- **`new table`** (or `make a table`, `keep a list of …`) offers the known tables not yet made, plus **Something else**.
- **Something else.**
  - Taskiem asks what the list is called, then *what do you write down for each one?*, for example "name, phone, what they bought, date".
  - Each comma-separated item becomes a column, named in snake case, titled as typed, and typed from its words: *phone* or *number to call* is phone; *email* is email; *amount*, *price*, *balance* or *naira* is number; *date* or *day* is date; *paid*, *done* or *active* is boolean; anything else is text.
  - The table is read back as a numbered list with the types in plain words ("Phone: a phone number"), then **Yes**, **Change 2** or **Cancel**.
  - Where the model is configured, it may propose columns for a description instead. The proposal is read back the same way and checked by the same validation; the model gets no tables, rows or other context.
- **It is made through `chatMakeTable`**, which chat already uses for a draft's tables and the storefront's catalogue, with the same limits, reserved names and audit as the Tables page. It needs `table.edit`.
- **The first table provisions the project** (§9.1). Chat answers at once, *Setting up your records. I'll message you when it's ready.*, and then reports what was made.

### 6.2 Changing a table: R3

- **Allowed in chat.**
  - `add column <name> to <table>` (or **Add a column** under `tables` → a table) asks the type in plain words, then **yes**.
  - A required column cannot be added to a table with rows, as on the web.
  - `rename <table>` changes the title. The name never changes.
- **Dropping a column and deleting a table** destroy data that cannot be got back from Taskiem. See Q1:
  - **Recommended:** allowed in chat only with the table's or column's name typed back, plus the chat PIN where one is set. Without a PIN, chat sends the web link.
  - The PIN exists on WhatsApp only (§3), so on Telegram and Slack these two always go to the web, until a PIN is built there.
  - A kept table is refused, with the existing words.

### 6.3 Changing and removing rows: R4

- **Finding a row.** `find <table> <text>` numbers its rows. **More** pages on, five at a time.
- **`change 2`**:
  - shows the row, masked;
  - asks which column (a numbered list) and then the new value, checked by `datatables.CheckValue`;
  - shows the change as *Balance: 5,000 → 3,000* and writes it after **yes**.
  - Several columns can be changed before the **yes**.
- **`delete 2`** shows the row and removes it after **yes**.
- **Concurrency: compare the values that were shown.** `updated_at` cannot guard a write, because nothing changes it after the row is made (§3).
  - An update names the row by `id` **and** by the value read of every column it changes, with `max_affected: 1`. A value read as empty uses `is null`.
  - A delete names the row by `id` and every value shown.
  - If the answer is `affected: 0`, the row changed since it was shown. Chat shows it again and asks again.
  - No change is written over one that someone else made to the same columns since. A change to other columns is kept, since the update sets only its own.
  - A real version would be better: a Xenibase trigger keeping `updated_at`, if its schema API allows one. That is asked of Xenibase (Q7), and the guard above needs nothing from it.
- **Idempotency.** Each write carries an idempotency key from the conversation and the message, as `add to` does.
- **Audit.** Writes are audited as `table.row.update` and `table.row.delete`, with the channel, counts and row ids, never values.
- **Restrictions.**
  - Kept tables refuse with the words they use today.
  - A personal column's new value is asked in a form where there is one. Its value is sealed in the session until the write, as for `add to`.

### 6.4 The debtor book: R5

| Words | Does |
| --- | --- |
| `Ada owes 5,000` (`Ada go pay 5k`, and the other languages) | Adds 5,000 to Ada's balance, or adds Ada with that balance. If there is more than one Ada, a numbered pick. A new debtor is asked for a phone number, which can be skipped |
| `Ada paid 2,000` (`Ada don pay 2k`) | Lowers Ada's balance by 2,000, never below zero. With *cash* or *transfer*, also records the money in the money log (see the rules below) |
| `who owes me?` (`who dey owe me`) | Debtors with a balance, largest first, with the total. Ten at a time, phones masked |
| `Ada` alone, while the debtor book is open | Ada's balance and when it last changed |

Rules:
- **Shown before written.** Each change is shown, for example *Ada: owes 5,000 → 3,000*, and written after **yes**. The rules of §6.3 apply: `id` plus the balance that was shown, so two messages at once never lose one of the changes, and an idempotency key from the message.
- **Cash payments in the money log.**
  - A payment said with *cash* also records a cash entry in the money log, once (keyed by the message), after the balance, with *Ada (debtor book)* in its description. An entered entry has no payer field.
  - This needs `invoice.manage`, as `cash in` does. Without it only the balance changes, and the reply says so.
  - If the money-log write fails, the balance change stands, and the owner is told the money was not recorded, with a `cash in` to resend.
  - **A payment said with *transfer* records nothing in the money log.** A transfer to a connected rail is already recorded there by the rail. Matching links payments to invoices and orders, not to typed entries, so a typed entry beside it would count the money twice.
- **No clash with supplier payments.** In English the word order decides. A supplier payment starts with *paid* (`paid Dangote 425,000 transfer`, [stock](../stock.md#suppliers-and-purchase-orders), matched by `paidRe`). A debtor's payment starts with the name (`Ada paid 2,000`).
  - In a language whose word order does not decide, a name that is both a supplier and a debtor is asked about.
- **Which table is the book.**
  - The words act on the `debtors` table, or on the table the owner chose in `debtor book <table>`.
  - Where there is none, the first `owes` offers to make it.
- **Who may use the words.** `who owes me?` needs `table.read`, and the changes need `table.edit`.
- **Amounts** are parsed by the money words' parser (`parseKobo`, `api/whatsapp_money.go`: `5k`, `5,000`, `₦5000`), which returns kobo. The `balance` column is in naira, so the book divides by 100 when it writes, and writes kobo to the money log.

### 6.5 Questions over rows: R6

- **What can be asked.** Any table: *who owes me more than 10k?*, *how many customers in Ikeja?*, *total balance*, *staff not active*.
- **How a question becomes a query spec.**
  - Known tables have word patterns that need no model: `who owes me`, `how many <table>`, `total <column>`.
  - Anything else goes to the model, which is given only the question, the table names and the column names and types, never a row.
  - The model returns a query spec:

    ```json
    {"table": "customers", "filters": [{"column": "area", "op": "eq", "value": "Ikeja"}], "order": [], "limit": 10, "aggregate": {"fn": "count"}}
    ```
- **Checks before anything is read.**
  - The table exists and is readable by the person.
  - Every column is the table's.
  - Each op is one of the connector's that fits the column's type.
  - Each value passes `CheckValue` for that type.
  - `limit` is at most 20.
  - `aggregate` is `count`, or `sum` of a number or integer column.
  - A spec that fails is answered *I couldn't answer that from your tables* with the `find` hint. Nothing is read.
- **How it runs.** Through the same connection and reads purpose (`tables.chat`) as `find`:
  - A count is one `read_rows` call with `count: exact` and `limit: 1`. `query_rows` has no count.
  - Rows are one `read_rows` call.
  - A sum has no action to run it, so code adds the column over the matching rows, paging up to 5,000. Past that the answer says *over the first 5,000 rows*.
- **How the answer is written.** Code writes it, never the model: the number, or the rows numbered like `find`'s, with personal values masked.
  - The answer is followed by the reading of the question, *Customers where area is Ikeja: 14*, so a wrong reading is visible. A value in the reading from a personal column is masked as the column's values are.
- **Limits.**
  - The model's questions are limited per chat per hour (20 by default).
  - They are counted against the business's AI usage like members' routing.
  - Without a model configured, only the word patterns work. Chat says so once and offers `find`.

### 6.6 The model's routing: R6

- **New intents for the model.** `tables`, `add`, `find`, and the new `new_table`, `change_row`, `owes`, `paid` and `ask` join `intent.Allowed`.
  - The argument is the table, row or text, bounded as today.
  - The model still never returns `yes`, `no` or `cancel`, so every write waits for the person's own **yes**, matched by the word lists.
- **Word lists in five languages** gain `new table`, `change`, `delete`, `owes`, `paid` and `who owes me`, marked as drafts outside English, as before.
- **The evaluation suite** gains cases in each language for the table intents and for questions over rows: the spec expected, and specs that must be refused.

## 7. Security

| Threat | Answer |
| --- | --- |
| A message makes the model read or change data it should not | The model only routes or returns a query spec. Specs are read-only, checked against the person's tables and columns, and run with the person's permissions. Every write waits for the person's **yes** |
| Rows leaving to the model provider | The model never gets rows: only the question, and table and column names and types. Answers are written by code |
| A forged or replayed message changes a row | Writes go through the channel's verified webhook (as today). They are keyed by the message, so a replay writes nothing twice, and guarded by the values that were shown (§6.3) |
| A hostile file (formula cells, a zip bomb, a huge sheet) | Size, row and entry caps. `.xlsx` is unzipped with expanded-size limits. Cells are values only, checked against the column, and never evaluated. A cell that starts like a formula is stored as text. The protection against a spreadsheet running it is on export, where it already is (an exported cell gets a leading `'`). The file is not kept |
| Destroying data by chat | Dropping a column and deleting a table need the name typed back and the PIN (Q1), which means WhatsApp only. Kept tables are refused. Single rows need a **yes** each |
| Personal values in chat history | Masked when shown. New personal values come by form where there is one, sealed until written, as `add to` does |
| A known table colliding with an owner's table | A known table that exists is never changed. Templates report missing columns instead (new: §5.4) |

## 8. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| R1 | **Templates without a sheet**: `no_sheets` (before the stock check) and `replaced_where` lists; the builder's retrieval honours conditions and ranks twins; known tables; the column check on existing tables; the three new twins; `debtor-reminder-table` and `bank-alert-email` declare tables; chat template builds and the builder's template drafts make their tables; provisioning from chat (§9.1); the prompt's sentence; eval goals | M | A business with no Google connection, from a first chat, says "remind my customers who owe me", gets `debtor-reminder-table`, and has its `debtors` table made on **yes**; no surface offers a sheet template to it |
| R2 | **Bring your sheet**: files on WhatsApp and Telegram (documents newly read on both); the import moved out of its handler; the file typing ported to Go; Make a new table or Add to; the Google Sheet copied once | M | A CSV and an `.xlsx` sent on WhatsApp become a table; the same file sent twice writes once; a 6,000-row file is refused with the reason |
| R3 | **Tables made and changed in chat**: `new table` (known or own words), add column, rename; drop column and delete table per Q1 | M | A first table made in chat on a business with no project is ready and announced; the Tables page shows what chat made |
| R4 | **Rows changed in chat**: change and delete from `find`, the guard on the values shown, forms for personal columns, audit | M | A change made on the web between the show and the **yes** is not overwritten |
| R5 | **The debtor book**: owes, paid (with the money log), who owes me, in five languages | M | `Ada owes 5,000`, `Ada paid 2,000 cash`, `who owes me?` give a balance of 3,000, one cash entry (for a person with `invoice.manage`), and a list of one; `Ada paid 1,000 transfer` changes only the balance; two `owes` at once both land |
| R6 | **Questions and the model's routing**: word patterns, query specs, checks, answers, rate limit; table intents in `intent.Allowed`; eval cases | M | "how many customers in Ikeja?" in Pidgin answers with a count and its reading; a spec naming a column the table lacks reads nothing |
| R7 | **Docs and the decision**: data tables, WhatsApp, Telegram, templates, the owners' guide (*Keep your records in chat*), the strategy, decision 0060 accepted with As built | S | The docs-site check passes; the strategy's "Xenibase underneath" row says what is built |

R1 comes first and stands alone. It carries provisioning from chat, since it is the first to make tables from a first chat. R3 comes next, and R2 needs it (**Make a new table** is R3's table-making). R4 can run beside R2. R5 needs R4's guard. R6 needs R3 to R5 for its intents.

## 9. Details

### 9.1 Provisioning from chat

The first table a business makes provisions its Xenibase project.
- **How it works today.** `datatables.Service.EnsureProject` (`engine/datatables/project.go`) does it inside the call that makes the table. Each step is recorded, so a later call resumes it. It can take a minute or two, and another call meanwhile gets `409 tables_provisioning`.
- **Chat already makes a draft's tables that way.** `waBuildFinish` does it inline, after **yes**.
- **What this plan adds.**
  - The *Setting up your records* line is sent before the first table, so the person is not left with silence.
  - The agreed tables are kept in the chat session for 24 hours, so a message that arrives during provisioning, or a provisioning failure, loses nothing. The next message, or **Try again**, resumes it.
  - If provisioning fails, the person is told once, with the reason in plain words. Today a failure is only logged as an error (`project.go`); this plan adds a metric and an operator alert on repeated failures.

### 9.2 Telegram and Slack

- **Telegram.** The Mini App built for connect forms (C2) gains a row form: the table's columns as fields, checked by its signed `initData`, as the connect page is. Documents are newly parsed (§5.6) and read with `getFile`. There is no PIN (§6.2).
- **Slack.** Rows are asked one column at a time. Files are not read until the app has `files:read` (Q6). There is no PIN.

### 9.3 What chat still sends to the web

- A column's type.
- An import over 5,000 rows.
- The table page's search over all columns.
- Bringing your own Xenibase project.
- Dropping a column or deleting a table without a PIN, which means always on Telegram and Slack (Q1).
- A file sent in a Slack DM (Q6).

Each is a line with the web link, which opens with the chat's sign-in link where the person has no password ([no technical steps](no-technical-steps.md) N6).

## 10. Open questions

| # | Question | Recommendation |
| --- | --- | --- |
| Q1 | May chat drop a column or delete a table? | Yes, with the name typed back and the chat PIN where set; else the web link. The owner of a chat-born business may have no web sign-in at all, and a refusal there is a dead end |
| Q2 | Does `Ada paid 2,000 cash` record cash in the money log? | Yes, for a person with `invoice.manage`: the money log is the record of money. A *transfer* records nothing there, since the rail already does. Without either word only the balance changes, and the owner is asked *How did Ada pay?*, which can be skipped |
| Q3 | Credit sales: should `sold 3 bags of rice 45,000 credit to Ada` lower stock and add to Ada's balance, with no money-log entry until she pays? | Yes, but as its own small milestone after R5, since it touches the sale's single transaction ([stock K2](stock.md)) |
| Q4 | Should storefront orders add their customer to `customers`? | Not in this plan: orders keep their customer sealed in Taskiem. A later twin can copy them with consent |
| Q5 | Excel: is a parsing dependency allowed? | Use one with a permissive licence (the `licences` CI check decides), or read `.xlsx` with the standard library's `archive/zip` and `encoding/xml` for the first sheet's values only. The second needs no review |
| Q6 | Slack files: ask every workspace to reinstall the app for `files:read`? | Not now. Add the scope with the next reason to reinstall, and read files from then on |
| Q7 | Ask Xenibase for a trigger that keeps `updated_at` on every write (or a row version)? | Yes, as a nice-to-have. The guard on the values shown works without it, and a version would make the guard cover every column |

## 11. Testing

- **Templates.** The library tests run over every new twin, plus `wd-test/v1` cases. Retrieval tests: with and without a Google Sheets connection, for the eight goals (no sheet template is in the builder's two matches without one).
- **Chat.** Conversation tests on WhatsApp and Telegram for every command above, including:
  - a change written after a web edit to the same column (refused and shown again), and after an edit to another column (written, the other edit kept);
  - two `owes` for the same debtor at once (both land);
  - a replayed delivery (written once);
  - a kept table (refused);
  - a person without `table.edit` (refused with the role named).
- **Query specs.** Table tests for the checks: an unknown table or column, an op that does not fit the type, a value that fails `CheckValue`, a limit over 20, an aggregate on text. Fuzzing of the spec parser.
- **Files.** Fixtures for CSV and `.xlsx`, including a formula cell (stored as text), a zip bomb and a 6,000-row file; the same file sent in two messages writes once.
- **The model.** Eval cases per language for routing and specs. A model answer outside the schema reads nothing.
- **End to end.** One Playwright run: a table made in chat appears on the Tables page with its rows.

## 12. Out of scope

- Keeping a Google Sheet in sync with a table, in either direction.
- Moving an existing workflow from a sheet to a table on its own. The workflow page offers the twin as a new draft instead (the `upgrade` mechanism), later.
- Changing many rows at once by chat ("mark everyone in Ikeja paid").
- Questions that join tables, or that the model answers in its own words.
- A column's type changed in place.
