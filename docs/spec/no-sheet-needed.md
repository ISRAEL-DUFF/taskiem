# No sheet needed: records kept in chat, and the templates that use them

Status: **Building**: R1 to R4 built 30 October 2026, R5 in part (§8.1 to §8.5); the rest planned, 30 October 2026 (revised the same day after a review against the code) · Decision: [0060](../decisions/0060-no-sheet-needed.md) (proposed) · Builds on: [data tables](../data-tables.md) ([0033](../decisions/0033-data-tables-on-xenibase.md)), [templates](../templates.md), [WhatsApp: data tables](../whatsapp.md#data-tables), [chat as the way in](chat-way-in.md) ([0058](../decisions/0058-chat-as-a-way-in.md)), [stock](stock.md) ([0057](../decisions/0057-stock-by-chat.md)), [the money log](../money-log.md) ([0045](../decisions/0045-tally-in-taskiem.md)), [languages](../languages.md) ([0029](../decisions/0029-languages-and-voice-notes.md)) · Strategy: [chat-first](../strategy/chat-first-nigeria.md#33-xenibase-under-the-agent) §3.3, the "Xenibase underneath" row of §4

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
  - Make a table, from a known shape (customers, staff) or from the owner's own words.
  - Add a column.
  - Change or remove a row.
  - Keep a debtor book in plain words: `Ada owes 5,000`, `Ada paid 2,000`, `who owes me?`. The book is invoices underneath, not a table: a notebook line is a credit invoice, and what Taskiem already does for invoices (balances, part payments, matching, receipts) does the book's work, with a new status, customer records and a reminder per customer added to invoices (§6.4).
  - Ask questions over any table.
  - The model routes all of these in every language, but only to the same commands, and it never sees a row.

Seven milestones, R1 to R7. R1 takes every sheet template off the table for a business with no sheet; R5 makes "remind my customers who owe me" work from a first chat, over invoices.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Sheet template | A template whose connectors include `googlesheets@1` |
| Twin | The template a business with no sheet is offered instead: the same job over a data table, or over a record Taskiem already keeps (the money log, stock) |
| Known table | A table shape Taskiem knows by name and purpose (`customers`, `staff`, and the storefront's tables), defined once and used by templates, chat words and the AI builder. The table is the business's own, an ordinary data table, not a [kept](../data-tables.md#kept-tables) one |
| Debtor book | The chat words `owes`, `paid` and `who owes me?` over [invoices](../invoices.md): a notebook line is an invoice of kind `credit` in a new status, **recorded** (open and owed, never messaged), with the phone optional. There is no debtors table |
| Query spec | What a question over rows becomes before anything is read: a table, filters, an order, a limit and at most one count or sum of a column, every name checked against the table's columns |

## 3. Where we are

| Exists | Where |
| --- | --- |
| Data tables: one Xenibase project per business, made on the first table; columns typed (`text`, `integer`, `number`, `boolean`, `date`, `datetime`, `phone`, `email`, `json`); rows through the data API with idempotency keys; CSV import and **Make a table from a file**; kept tables; limits | [data tables](../data-tables.md), `api/datatables*.go`, `engine/datatables` |
| In chat: `tables`, `add to <table>` (a Flow where set up, else column by column, added after **yes**, personal values sealed and masked), `find <table> <text>` (five rows) | `api/whatsapp_tables.go`, [WhatsApp: data tables](../whatsapp.md#data-tables) |
| Templates that declare their tables (`tables`), made after the draft is saved when missing (`created`, `exists`, `skipped`, `failed`) | [templates: tables a template makes](../templates.md#tables-a-template-makes), `api/templates.go` `templateTables` |
| Eight sheet templates: `daily-sales-summary`, `debtor-reminder-sms`, `debtor-reminder-whatsapp`, `new-customer-welcome`, `salary-payout-approval`, `sales-ledger-sheet`, `stock-reorder-alert`, `transfer-received-notify` | `templates/library/` |
| Replacements: `salary-payout-approval` is `superseded_by` `run-payroll` (a staff table); `stock-reorder-alert` is `replaced_where` `keeps_stock` by `low-stock-alert`; `daily-sales-summary` is `replaced_where` `records_sales` by `sales-day-total` | `templates/templates.go` (`ReplacedWhere`, `For`), `api/templates.go` `templateHolds` |
| One table twin: `debtor-reminder-table` (SMS over a `debtors` table with `name`, `phone`, `balance`); it declares no `tables`, so the table must already exist. Invoices nearly do its job: `chase-overdue-invoices` reminds customers with overdue invoices, one message per invoice, by SMS and email, and is offered only for goals that say "invoice" | `templates/library/debtor-reminder-table.json`, `chase-overdue-invoices.json` |
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
- **`debtor-reminder-table` declares no table.** It and `bank-alert-email` "still expect their table to exist" ([templates](../templates.md#tables-a-template-makes)). It is also a second debtor ledger beside invoices (§6.4), so it is retired rather than fixed.

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
| `debtor-reminder-sms` | `chase-overdue-invoices` with **One message per customer** (§6.4): what each customer owes over invoices, credit lines included; `debtor-reminder-table` is retired (`superseded_by` it) | Changed |
| `debtor-reminder-whatsapp` | `chase-overdue-invoices`, which sends on WhatsApp where the customer asked the business for updates, as invoices do, and by SMS otherwise | Changed |
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
- templates (`tables: [{"known": "customers"}]`, a new `known` field on `templates.Table`, or a parameter with the table picker defaulting to it);
- the chat words (§6.4);
- the AI builder's context, which lists them as tables it may propose by name;
- making a table in chat (§6.1);
- the storefront's kinds ([fronts](fronts.md) §6): `products` and `services`, which the storefront makes today in its own code, move into the registry, and the kinds' new tables (`menu`, `hire_items`, `spaces`, `events`, `classes`) are defined here when fronts build them, so there is one registry, not one for templates and one for the storefront.

| Known table | Columns |
| --- | --- |
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
- `bank-alert-email` declares its table.

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

**The book is invoices, not a table.** Taskiem already keeps money owed as [invoices](../invoices.md): a customer sealed, an amount and a balance, a draft that is saved but not sent, part payments with cash posted and matched, a receipt, overdue after a due date, a list by status, a trigger on every payment counted, chasing by SMS and email (`chase-overdue-invoices`, one message per invoice), and, from the schools work, attribution of a payment to a customer's open invoices oldest first with any extra held as credit ([matching: customers' own account numbers](../matching.md#customers-own-account-numbers)). A `debtors` table with a balance column would build all of that again, in a second place, with a second notion of "paid" that matching cannot see; the two ledgers would disagree the first time a customer paid through an account number. So there is no `debtors` table. The debtor book is a set of chat words over invoices. What it needs from invoices is new, and listed here so none of it is taken for granted:

- **A kind and a status.** A notebook line is an invoice of kind `credit` in a new status, **recorded**: open and owed, but never messaged.
  - Drafts are not open: matching counts a payment only towards sent, part-paid or overdue invoices (`docs/matching.md`), and a draft takes no payment. A credit line saved as a draft would be skipped by `Ada paid` and by `who owes me?`.
  - So **recorded** is open in every place that asks: oldest-first attribution, splitting, suggestions, credit held and taken, the statement, `who owes me?` and the `list` action.
  - **Recorded** moves to part-paid and paid by payments, as sent does. It moves to sent only when the owner sends it (`send`, below). It becomes overdue only with a due date.
  - A credit invoice always takes part payments, whatever **Customers may pay part** says, since a notebook debt is paid in bits.
- **A customer without a phone or email.** Invoices require one or the other today, and group a customer by `customer.key`, else phone, else email (`docs/invoices.md`).
  - A credit line's customer gets a key on their first line, `book:<id>`, from a new `invoice_customers` record holding the name (sealed, plus a lower-case search form of it, see Q8), and the phone and email once known.
  - Every invoice to that customer carries the key, so attribution, credit and statements group by it as they do for a school's `student:42`.
  - A phone given later (`Ada's number is 0803…`) is set on the record and on the customer's open invoices, so receipts and reminders can reach her.
  - Two Adas are two records, picked by number when the name is ambiguous.
- **One reminder, per customer.** `chase-overdue-invoices` sends by SMS and email, one message per overdue invoice, and is retrieved only for goals that say "invoice" (`requires`). Rather than a second reminder template beside it, which would send a customer two messages for one debt when both ran, the one template grows:
  - `invoices@1` `list` gains `group: customer` (one row per customer with what they owe, their lines and their oldest due date), and the template gains a **One message per customer** setting, on by default for new workflows and off for the ones already running, so nobody's reminders change under them.
  - Its retrieval words gain "remind my customers who owe me", "who owes me", "debtors" and their kin in the five languages, and its `requires` is widened so the goal need not say "invoice".
    - The widening is by **phrases**, never the bare word "owe". `requires` already takes a phrase whose words must come in order with only stop words between: "who owe me", "customers who owe", "people who owe me", "my debtors".
    - So "pay the suppliers I owe" never retrieves the customer reminder. Eval goals cover both directions.
  - Per customer it sends on WhatsApp where the customer asked the business for updates (as invoices do) and by SMS otherwise, with the customer payment link (above) or their account number where a rail is connected, and the reminder cadence it has today, seeded per customer and reminder number so a second run sends no duplicate.
  - So the two debtor-reminder sheet templates are replaced by `chase-overdue-invoices`, and `debtor-reminder-table` is retired. A business that made a reminder workflow from the old per-invoice version keeps it as it is until it turns the setting on.
- **When a credit line is due.** A credit line has no due date unless given, and there is no default-terms setting today. Invoices > Settings gains **Credit lines are due after** (days), and a line said `by Friday` takes that date. The terms are the owner's, not Taskiem's: the first `owes` in a business asks once, *How long do you give customers to pay?* with **7 days**, **14 days**, **30 days** and **No fixed time**, and keeps the answer as the setting. Until it is answered no credit line has a due date, so nothing is reminded by a default nobody chose. What the choices should be, and whether one is suggested first, is a founder's call (Q9).
  - **A line's due date is set when it is recorded,** from the terms then in force (or the date given). It is never moved by a later change of terms.
  - **Changing the terms** (in chat, `credit terms 14 days`, or on Invoices > Settings) applies to new lines. Chat then offers *Apply to Ada's and 11 other open lines too?*, which dates the open recorded lines from the day each was recorded and is audited as one change.
  - **Lines recorded before any terms** stay undated, and so are never reminded, until the owner applies the terms to them in this way. The first answer offers it at once when such lines exist.
  - **Who is asked.** Saving the terms is a change to the invoice settings, which needs `invoice.manage` across the whole business (`PUT /v1/invoice-settings` is tenant-wide, `api/invoices.go`). So the question is asked only of a person who can save it.
    - Anyone else who records a debt gets the line undated, with *An owner sets how long customers have to pay; until then this debt has no due date*.
    - The owner is told once, in their next chat, that terms are waiting to be set.
  - **One rule for reminders.** `chase-overdue-invoices` reminds only what is overdue, in both modes. A credit line becomes overdue after its terms like any invoice, so every customer who owes is reminded once their line is due.
  - Emptying the setting means credit lines are never overdue and never reminded, and the setting says so beside the box (*No reminders for notebook debts*). The per-customer mode does not list lines with no due date.

| Words | Does |
| --- | --- |
| `Ada owes 5,000` (`Ada go pay 5k`, and the other languages) | A new invoice of kind `credit` to the customer Ada for ₦5,000, **recorded** and not sent: nothing is sent, the due date is the credit terms' (or none), and the phone is asked once and may be skipped. The read-back shows Ada's balance after it (*Ada: owes 5,000, total 8,000*) and waits for **yes**. If there is more than one Ada, a numbered pick |
| `Ada paid 2,000` (`Ada don pay 2k`) | A payment of ₦2,000 counted towards Ada's open invoices, oldest first, as a school's payment is counted towards a student's; what is left over is held as Ada's credit and taken by her next invoice. With *cash* it is a cash posting into the money log, matched to those invoices. With *transfer*, what happens turns on **where the money went**, not on whether a rail is connected: a business with Paystack still takes transfers into its ordinary bank account, which Paystack never sees and which the bank-alert template and statement imports exist for. Chat asks *Into which account?* with the connected accounts listed (the business's wallet or collection account, Ada's own account number where she has one) and **Another account** last. Into a connected account, nothing is posted, since the rail's report is the record and a typed entry beside it would count the money twice: Ada's balance changes when the transfer arrives and is matched (her payer memory and her account number make it a strong match), and chat says so. Into any other account, it is posted as an entered transfer (channel `transfer`, the account named) and matched at once, so the money is in the log and her balance moves now. A later statement import of that account carries the same transfer, and **pairing it with the entered one is new work**. Today `ImportStatement` skips only lines already imported on the same channel (`engine/moneylog/statement.go`), so the statement line would be a second payment.
- **The account is a channel name.** A statement is imported under a channel such as `gtbank/current` (`money_statement_formats`), so an entered transfer names its account the same way, or pairing has nothing to pair on: *Into which account?* lists the business's connected accounts, then the channels it has imported statements for, then **A new account**, which asks for a name once (checked as a channel name is) and remembers it for next time. Free text is never taken as the account.
- The import gains a pairing step. A money-in line on the channel the entered transfer named, with the same amount and a date within three days of it, is recorded as the bank's confirmation of that entry, not as new money.
- Exactly one candidate pairs at once. Several candidates, or one with a different amount, are listed after the import as *Is this the transfer you recorded for Ada?*, with **Same money** and **Different money**.
- An entered transfer that no statement ever confirms stays as the owner's word, as entered cash does. A business with no connected rail skips the question. Without *cash* or *transfer* the owner is asked *How did Ada pay?* |
| `who owes me?` (`who dey owe me`) | Customers with open invoices, grouped, largest balance first, with the total: credit lines and sent invoices alike, so one question answers the whole debt. Ten at a time, phones masked |
| `Ada` alone, while the book is open | Ada's statement: her balance, her open invoices and the last payment |
| `send Ada her balance` | New work: moves Ada's recorded lines to sent together and sends **one** message listing them with their total and one way to pay it: her own account number where she has one, else a **customer payment link**. That link is new too: today a payment link belongs to one invoice. A customer link is issued against the customer's key (`book:<id>`, or the key or phone their invoices carry) for the total owed, kept with the customer like a registered account number is, and a payment through it is matched exactly to the customer's open invoices oldest first, as a payment into their own account number is ([matching](../matching.md#customers-own-account-numbers)); it is `invoices@1` `customer_link` in the connector, and the link's page shows the lines and takes part payments. No invoice is folded into another or voided, so each line keeps its history. Needs a phone or an email for Ada |
| `remind Ada`, and the reminder template | `chase-overdue-invoices` with one message per customer (above). `remind Ada` sends hers now, once a day at most |

Rules:
- **Shown before written.** Each change is shown and written after **yes**, through the invoice functions, which already take an idempotency key; two messages at once make two invoices, or two payments, and never lose one.
- **A credit invoice differs from a sent one** in its status (recorded), its due date (the credit terms', or given), its part payments (always allowed) and its customer (a `book:` key, possibly with no phone or email). Everything else, the balance, the payments, the receipt once a phone is known, the money log and the audit, is the invoice's as today. The per-payment invoice trigger still fires for cash on a recorded line, so a receipt workflow reaches `notify_customer` for an invoice that was never sent. **That is new work.** Today `NotifyCustomer` refuses any invoice with no `sent_at` (`engine/invoicing/invoicing.go`, "has not been sent"), so every cash payment on a credit line would fail `invoice-paid-receipt`'s step. The template's `on_error` branch catches it, but it records "the thank-you could not go" each time.
- `notify_customer` accepts a recorded invoice. With a phone or an email it sends, as for a sent one. With neither, it sends nothing and answers `sent: false` with `reason: no_address`, not an error.
- **Two refusals learn recorded, by name.** "Open everywhere" is two checks in code, each of which refuses today: `NotifyCustomer` refuses an invoice with no `sent_at` (`engine/invoicing/invoicing.go:966`), and the count-payment path refuses an invoice whose status is draft (`invoicing.go:1064`), which `Ada paid 2,000 cash` would hit before any receipt is attempted. R5 changes both, and its test for a cash payment on a recorded line asserts the payment was counted and then that the receipt answered `sent: false`.
- `invoice-paid-receipt` and the other receipt templates treat `sent: false` as done, not failed. Their tests gain a recorded line with no address.
- **Who is Ada.** A name is looked up among the `invoice_customers` records: the exact name, else the one the words start, else one containing them, with a numbered pick when several match, as items are found in chat. A customer of a sent invoice with a phone is offered too, and choosing them puts the key on their record.
- **Who may use the words.** `who owes me?` and a statement need `invoice.read`; `owes`, `paid` and `send` need `invoice.manage`, as `invoice` and `cash in` do. Without it, the reply names the role.
- **No clash with supplier payments.** In English the word order decides. A supplier payment starts with *paid* (`paid Dangote 425,000 transfer`, [stock](../stock.md#suppliers-and-purchase-orders), matched by `paidRe`). A customer's payment starts with the name (`Ada paid 2,000`). In a language whose word order does not decide, a name that is both a supplier and a customer is asked about.
- **Credit sales** (Q3): `sold 3 bags of rice 45,000 credit to Ada` lowers stock and makes a credit invoice with the sale's lines, in the sale's one transaction, with no money until she pays. A sale on credit is a bill.
- **Amounts** are parsed by the money words' parser (`parseKobo`, `api/whatsapp_money.go`: `5k`, `5,000`, `₦5000`), in kobo, as invoices keep them.
- **The web app.** Invoices gains a **Credit** filter, the **Recorded** status, the credit terms setting, and the customer statement (a customer's open invoices, payments and balance), which the schools family page is already a form of.

### 6.5 Questions over rows: R6

- **What can be asked.** Any table: *who owes me more than 10k?*, *how many customers in Ikeja?*, *total balance*, *staff not active*.
- **How a question becomes a query spec.**
  - Word patterns that need no model: `who owes me` (invoices, §6.4), `how many <table>`, `total <column>`.
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

### 6.7 Corrections to earlier versions of this plan

Earlier versions of this plan stated the following as how the code works today. They were false. Each is corrected above and listed here so a reviewer can acknowledge it:

| # | Claim | What the code does | Where corrected |
| --- | --- | --- | --- |
| C1 | A later statement import that carries an entered transfer "is matched to the entry, not counted again" | `ImportStatement` skips only lines already imported on the same channel. A statement line for money already entered by hand is recorded as a second payment (`engine/moneylog/statement.go`) | §6.4 (`Ada paid … transfer`): pairing is new work in R5 |
| C2 | For an invoice with no address, `notify_customer` "answers `sent: false`", and the receipt templates "already tolerate that answer" | `NotifyCustomer` refuses any invoice that was never sent ("has not been sent", `engine/invoicing/invoicing.go:965`). A recorded credit line is never sent, so the receipt step fails, and `invoice-paid-receipt`'s `on_error` records a failure for each payment. Before that, counting the payment itself refuses a draft (`invoicing.go:1063`), so `Ada paid 2,000 cash` would fail before any receipt is tried. Both refusals learn the recorded status in R5 | §6.4 (rules): `notify_customer` on recorded lines is new work in R5 |
| C3 | (Earlier still) `chase-overdue-invoices` "already sends by WhatsApp" and is the debtor reminder as it is | It sends by SMS and email, one message per invoice, and is retrieved only for goals that say "invoice" | §6.4: per-customer mode, WhatsApp, and widened `requires` are new work |
| C4 | (Earlier still) A credit line saved as a draft is counted by matching and `who owes me?` | Matching counts payments only towards sent, part-paid and overdue invoices; drafts take no payment | §6.4: the **recorded** status |
| C5 | (First version) `updated_at` guards a chat write against a newer change | `updated_at` is a default set when the row is made, and nothing changes it afterwards | §6.3: the guard compares the values shown |

Two leftovers that contradicted the design were also fixed:
- R5's "done when" named `remind-who-owes-me`, a template the design no longer has.
- Empty credit terms promised reminders that `chase-overdue-invoices`, which lists only overdue invoices, would never send.

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
| R1 | **Templates without a sheet**: `no_sheets` (before the stock check) and `replaced_where` lists; the builder's retrieval honours conditions and ranks twins; known tables; the column check on existing tables; `customer-welcome-table` and `payment-received-tell-owner`; the three twins that already exist wired in; `bank-alert-email` declares its table; chat template builds and the builder's template drafts make their tables; provisioning from chat (§9.1); the prompt's sentence; eval goals. The two debtor sheet templates stay offered until R5's per-customer reminder exists | M | A business with no Google connection, from a first chat, says "welcome my new customers", gets `customer-welcome-table`, and has its `customers` table made on **yes**; no surface offers it a sheet template that has a twin |
| R2 | **Bring your sheet**: files on WhatsApp and Telegram (documents newly read on both); the import moved out of its handler; the file typing ported to Go; Make a new table or Add to; the Google Sheet copied once | M | A CSV and an `.xlsx` sent on WhatsApp become a table; the same file sent twice writes once; a 6,000-row file is refused with the reason |
| R3 | **Tables made and changed in chat**: `new table` (known or own words), add column, rename; drop column and delete table per Q1 | M | A first table made in chat on a business with no project is ready and announced; the Tables page shows what chat made |
| R4 | **Rows changed in chat**: change and delete from `find`, the guard on the values shown, forms for personal columns, audit | M | A change made on the web between the show and the **yes** is not overwritten |
| R5 | **The debtor book over invoices**: the `credit` kind and the **recorded** status counted as open everywhere; `invoice_customers` and `book:` keys; part payments always on for credit; the credit terms setting; owes, paid (oldest first; cash posted; a transfer by the account it went into: into a connected account counted when the rail reports it, into any other entered now and paired with the statement line later), who owes me, the statement, `send … her balance`; the customer payment link (`invoices@1` `customer_link`, matched like an account number); `invoices@1` `list` grouped by customer and `chase-overdue-invoices` with **One message per customer**, replacing the two debtor sheet templates and retiring `debtor-reminder-table`; the searchable-name exception written into the threat model; `notify_customer` on recorded lines; statement pairing for entered transfers; credit sales (Q3); the web app's filter, status, setting and statement; five languages | L | `Ada owes 5,000`, `Ada paid 2,000 cash`, `who owes me?` give one credit invoice, one cash payment matched to it, a balance of 3,000 and a list of one; `Ada paid 1,000 transfer` into the connected account changes nothing until the transfer arrives and is matched, then lowers the balance once; into another account it is entered and matched at once, and the statement line for it pairs with the entry instead of counting again; a cash payment on a recorded line with no phone completes its receipt workflow with `sent: false`; two `owes` at once make two lines; `send Ada her balance` sends one message for 3,000 that an account number pays and matching clears line by line; "remind my customers who owe me" from a first chat gets `chase-overdue-invoices` with **One message per customer**, which sends Ada one message once her line is due, and "pay the suppliers I owe" does not retrieve it |
| R6 | **Questions and the model's routing**: word patterns, query specs, checks, answers, rate limit; table intents in `intent.Allowed`; eval cases | M | "how many customers in Ikeja?" in Pidgin answers with a count and its reading; a spec naming a column the table lacks reads nothing |
| R7 | **Docs and the decision**: data tables, WhatsApp, Telegram, templates, the owners' guide (*Keep your records in chat*), the strategy, decision 0060 accepted with As built | S | The docs-site check passes; the strategy's "Xenibase underneath" row says what is built |

### 8.1 As built (R1)

Built 30 October 2026:
- **Replacements.** `replaced_where` takes one object or a list, and writes one back as an object, so a template that did not change pins the same JSON. The conditions are `no_sheets` and `no_sheets_stock` (`no_sheets` where Taskiem's stock is on: the twins of `daily-sales-summary` and `stock-reorder-alert` are stock's, `sales-day-total` and `low-stock-alert`, which do nothing without it). `templateHolds` works out `no_sheets` from the active Google Sheets connections before it looks at stock.
- **Offering.** The gallery and the Start page leave a replaced template out and list a twin just before the template it replaces where both are offered. The AI builder's retrieval (`Library.MatchFor`) takes the business's conditions from `startAIBuild`, puts the twin in the place of a template replaced there, and ranks the twin first where both are offered unless the goal says "sheet".
- **Twins.** New: `customer-welcome-table` (started by a row added to `customers`, so chat's `add to customers`, an import and a website through the rows API all welcome) and `payment-received-tell-owner` (the twin of both `transfer-received-notify` and `sales-ledger-sheet`, since the money log already keeps every payment). Wired: `sales-day-total`, `low-stock-alert`. `templates_test.go` checks that every template using Google Sheets has a twin, except the two debtor reminders, which wait for R5.
- **Known tables.** `templates/tables/customers.json` and `staff.json`; a template's table names one with `known`, resolved when the template is read and not kept, so `run-payroll`, which now names `staff`, pins the same JSON as before. The products and services tables move into the registry with fronts (0059), not here.
- **Tables made.** `chatTemplateBuild` and the builder's drafts from a template carry the template's tables (`builder.TemplateTableSpecs`, keeping its columns' personal markings); `tablesUsed` counts a `row_changed` trigger's tables. A table that exists already is left as it is and what it lacks is said: `missing` in the API's `tables` answer, a warning on the builder's proposal, `wa.build.table_lacks` in chat. `bank-alert-email` declares its table.
- **Provisioning from chat.** The first table a business makes from chat is preceded by `wa.build.tables_setting_up`. Keeping the agreed tables in the chat session for 24 hours with **Try again** (§9.1) came with R3 (§8.2), for a table made on its own; a draft's tables that fail still get the web link.
- **The prompt.** Rule 12 says a goal naming a sheet uses Google Sheets and any other goal about a list uses a data table.
- **Evaluation.** Two cases (`sme-welcome-added-customer`, `sme-ping-me-on-payment-no-sheet`). The offline suite runs without a business's conditions, so it sees both templates of a pair and the twin first; the baseline was regenerated (template starts 262 → 259 of 315, trigger accuracy 0.607 → 0.575, connector recall and step recall up). The shift is the twin-first ranking on goals that matched a sheet template loosely; no case that passed the gate fails now.

### 8.2 As built (R3)

Built 30 October 2026 (`api/chat_tables_make.go`):
- **`new table`** (and `make a table`, `new list`, `keep a list of …`; `new_table` in the word lists, drafts outside English) offers the known tables the business does not have, then **Something else**. A known one is read back from its registry entry. Something else asks the name (checked as a table name, refused when taken), then the columns: comma-separated, each named in snake case, titled as typed and typed from its words (`typeFromWords`: phone, email, amount/price/balance/naira/cost/fee/total as a number, date/day/due as a date, paid/done/active as yes or no, quantity/count as a whole number, else words). The read-back lists the types in plain words; sending the list again changes it; **yes** makes it through `chatMakeTable`.
- **Provisioning.** *Setting up your records* first; any failure other than a name taken, an invalid shape or a plan limit keeps the agreed table in the session for 24 hours with **Try again** (§9.1).
- **`add column <name> to <table>`** asks the type as a numbered list unless the words say it, then adds after **yes** as the Tables page does: the column limit, Xenibase's schema change, the record, `table.column.add` in the audit. **`rename <table> to <title>`** changes the title (audited as `table.update`).
- **Dropping a column and deleting a table** answer with the Tables page's link. Q1 (chat with the name typed back and the PIN) is the founder's to decide; until then chat takes the plan's fallback, which needs nothing new. The PIN path would be a WhatsApp Flow per decision, as approvals have.
- **Not built:** the model proposing columns from a description (§6.1, optional), and the Telegram Mini App row form (§9.2), which R4 needs more than R3.
- Tests: `TestChatMakesAndChangesTables`, `TestChatTableWaitsForItsProject`, the word-list cases in `engine/lang`.

### 8.3 As built (R4)

Built 30 October 2026 (`api/chat_tables_rows.go`):
- **Numbered rows.** `find <table> <text>` numbers the rows it shows and keeps their ids in the session (`chatData.Found`, 30 minutes). `more` shows the next five: the same search on ids below the smallest shown, since `read_rows` pages by cursor and the chat keeps no cursor. Any other message lets the list go and is read as itself.
- **`change n`** reads the row again by its id and shows it with its details numbered, personal ones masked; then which detail, then its new value, parsed and checked as `add to` checks one (`skip` empties an optional detail). The read-back shows each detail changed as *was → will be*; another detail's number before the **yes** adds it. **`delete n`** reads the row again and shows it for **yes**. **no** keeps the list.
- **The guard.** The values read when the row was shown are kept in the session, personal ones sealed. The write is `update_rows` or `delete_rows` by `filters` (the connector takes a key or filters, not both, so the id is a filter) with `max_affected: 1`: the id, and for each column changed (a removal: every column) its value as read, `is_null` for an empty one, `is` for a yes or no, `eq` otherwise. `affected: 0` means the row changed or went: nothing was written, and the row is shown again as it is now and asked again (or said to be gone). A value Xenibase compares differently from how it was read (a JSON object's key order, a number with more digits than a float holds) can only make the guard refuse, never let a write through. The test makes a change on the web between the show and the **yes** and checks it is not overwritten, and that a change to another column is kept; with the guard reduced to the id, the same test fails.
- **The guard's values travel in the URL.** A write by filter sends its conditions as the data API's `where` query parameters, so a personal value being guarded is in the request's query string, over TLS, to the business's own project, which holds it already. Asked of Xenibase with Q7: the same conditions accepted in a write's body, which keeps them out of access logs.
- **Idempotency and audit.** Each write's key is `wa-` and a nonce taken at each read-back, so a **yes** delivered twice writes once (the test delivers the same message twice). Audited as `table.row.update` (`columns`: how many) and `table.row.delete`, with the channel, the table and the row's id, never a value. Three writes per number per 20 seconds.
- **Personal values.** On WhatsApp with forms set up, a personal detail's new value comes in a one-field `taskiem_inputs` form (`column` in the form's record), checked and sealed there. Telegram's Mini App row form (§9.2) is not built, so on Telegram and Slack the value is asked in chat, as `add to` asks one there; it is sealed when received and masked in every message.
- **Words.** English (`change`, `edit`, `correct`, `update`; `delete`, `remove`; `more`, `next`) while the list is kept; the hints are translated. The model's routing of these in every language is R6's.
- Tests: `TestChatChangesAndRemovesRows`, `TestChatChangesPersonalValueInForm`, `TestTelegramChangesRows`; `TestWhatsAppTables` updated for the numbered lines.

### 8.4 As built (R2)

Built 30 October 2026 (`api/chat_tables_files.go`, `engine/datatables/files.go`):
- **Documents.** WhatsApp's webhook reads `document` messages (id, type, file name, caption) and Telegram's updates read any non-image document (`file_name` too); both reach the conversation as one kind of message. A Slack DM's file (`file_share`) is answered with the Tables page's link (Q6).
- **Reading.** `datatables.ReadFile`: a CSV, or an `.xlsx` read with `archive/zip` and `encoding/xml` alone (Q5's second option): the first sheet through the workbook's relationships, shared and inline strings, rich text, numbers to 15 significant digits, date-formatted numbers (built-in and custom formats, the 1904 system) as dates. Formulas are never evaluated: the value Excel saved is read, and a cell starting like a formula stays the text it is. Caps: 500 parts, 32 MB a part and 64 MB together, counted as read rather than taken from the zip's directory; 200 columns; 2 MB a file, refused before download where the channel reports the size; 5,000 rows, refused with the reason. The test has a zip bomb (40 MB of text packed small) and a 6,000-row file.
- **Typing.** `datatables.TableFromFile` is the web's `tableFromCSV` ported: numbers (commas and ₦ allowed, no leading zero), Nigerian phone numbers, `YYYY-MM-DD` dates, else words; phone and email columns personal. In chat, number cells have their commas and ₦ taken off before the check. The web keeps its own.
- **Choosing.** **New table** (named from the file, `_2` and on when taken), **Add to table** (offered for the table the file is named after, or the one with at least half the headings as columns), `add to <table>`, **Not now**. Unmatched headings are asked one by one by number, **0** leaving one out; a file with none of the table's columns adds nothing.
- **One import.** The API's import is split into `importRows` (the batches, keys and replay), `importRecords` (the rows checked, numbered by line) and `headerColumns` (headings to columns); the API's answers are unchanged. The import id is `importIDFor(table, "", file)`: the same file to the same table writes once, and the chat says *already added*.
- **Never kept.** The session holds the file's channel id, name and SHA-256, and the headings; the file is fetched again for the **yes** and refused if it differs. Rows are audited as counts (`table.import` with `file`: csv, xlsx or sheet), never values. Five files a minute per number.
- **A Google Sheet, once.** `copy my sheet <link>` reads the title (`get_spreadsheet`) and the first tab's `A1:GR5002` (`get_values`) with the business's Google Sheets connection in prod, then goes on as a file does; it is read again for the **yes** and refused if it changed. The answer after the rows are in says it was a copy. The API's lookup was split so chat can call it (`connectorAction`). **Copy into a table** after a sheet template is picked is not built.
- Tests: `TestChatBringsAFileIntoATable`, `TestTelegramBringsAFileIntoATable`, `TestChatCopiesASheetOnce`, `TestSlackFileGoesToTheWeb`; `TestReadXLSX`, `TestReadCSVFile`, `TestTableFromFile`; the parsers' document cases.

### 8.5 As built (R5, in part)

Built 30 October 2026, in slices. So far (`engine/invoicing/book.go`, `api/chat_debtors.go`, migration 00273):
- **The model.** `customer_invoices` gains `kind` (`invoice`, `credit`) and `recorded_at`; the status trigger gives **recorded** to a line recorded and not sent (draft needs neither), part-paid, overdue and paid as for a sent one. The payment trigger refuses only drafts, the daily overdue check and the invoice page (and its pay requests) take recorded lines, and matching's candidates, a customer's open invoices oldest first (`OpenInvoicesOf`, by due date, else when sent or recorded) and the overdue `list` count them. `NotifyCustomer` sends for a recorded line and answers `sent: false`, `reason: no_address` where there is no phone or email (the C2 refusal); the count-payment refusal of drafts never met recorded lines. Credit lines always take part payments.
- **Customers of the book.** `invoice_customers`: the name sealed under a subject of the customer's own (`customer_book`, keyed by `book:<id>`, so two Adas are erased apart), its search form beside it, the phone sealed once known. A trigger on `subject_keys` replaces the search form with `(erased)` when the subject is shredded (Q8's bound, tested). Lookup: the exact name, else names starting with the words, else containing them, nine at most.
- **Credit terms.** `credit_days` and `credit_terms_at` on the invoice settings, set only by `SetCreditTerms` (the web's settings save leaves them); `CreditDue` dates a line from the terms in force when it is recorded; `ApplyCreditTerms` re-dates the open recorded lines from the day each was recorded, audited as one change. The API shows them read-only.
- **Chat.** `Ada owes 5,000` (`go pay`, `dey owe`), `Ada paid 2,000 cash|transfer` (`don pay`), `who owes me?` (`debtors`, `who dey owe me`), `balance Ada`, `Ada's number is …`, `credit terms 14 days|none`. A new customer's phone is asked once (sealed in the session) and may be skipped; the first debt asks the terms (7, 14, 30 days, no fixed time; Q9's choices are the plan's until the founder says otherwise). Cash is a money log entry (`cash`, entered) counted with `Matcher.CountCustomer`, as a school's desk cash is; a transfer lists the connected rails Taskiem reads (`list_credits`; nothing is posted into one) and the statement channels, or takes a new channel name checked as one (`moneylog.ValidChannel`), and is entered under it. Every write is keyed by the read-back's nonce.
- **One reminder, per customer.** `invoices@1` `list` takes `group: customer` (overdue only): one row per customer with something overdue, an opaque customer id, the number of their oldest overdue invoice or debt, how many and what they owe on them; a line with no due date is never listed. `notify_customer` gains `{customer_due}` and `{customer_lines}`. `chase-overdue-invoices` gains **One message per customer** (`per_customer`, default on for new workflows; running ones keep their pinned definition), reaching each customer through their oldest overdue line, with their customer payment link (`{customer_link}`, below), seeded per customer and reminder. Its `requires` gains "customers who owe", "people who owe me", "my debtors" and "debtors who owe"; "who owe me" was not added, because retrieval drops stop words and words under three letters, which would leave the bare word "owe" (a test now checks that "remind me what I owe my landlord" and "pay the suppliers I owe" do not retrieve it). `debtor-reminder-sms` and `debtor-reminder-whatsapp` are replaced by it where `no_sheets`; `debtor-reminder-table` is superseded by it. The builder baseline was regenerated (316 cases with `sme-remind-who-owe-me-no-sheet`): goals that used to match the retired table template loosely no longer do, and `topup-reconciliation`'s offline draft, which had borrowed that template's loop, now has none.
- **The customer payment link and `send Ada her balance`** (R5c; `engine/invoicing/customerlink.go`, `engine/publicweb/customer.go`, migration 00274). A link is kept per customer (`invoice_customer_links`, one per `customer_id`, its token sealed under the customer's subject as an invoice's is) and served at `/c/{token}` by the invoice page's template with a line per open invoice, oldest first, and the invoice page's payment section (**Pay what you owe**, any part from ₦1,000). Its account numbers and card links are rows of `customer_invoice_accounts` with a third owner, `customer_link_id`, beside an invoice and an order, and its requests the invoices' queue, so issuing (`IssueForCustomer`, on the rail of the oldest open line), the limits, the lookups and expiry are the invoices'. A payment through one is matched exactly (`matching` `exact`, or the checker through `Invoices.LinkReceived` and `Matcher.LinkPaid`) with `CountCustomer`, marked `how link` in `money_entry_customers`; either path first, it counts once. `SendBalance` sends one SMS (and email) naming the open lines, the total and the customer's own account number, else the link, and sends the recorded lines with it; chat reads it back with the address masked. A recorded line now takes the customer's credit when it is recorded (found while building: §6.4 says credit is "taken by her next invoice", and `RecordCredit` did not), and the read-back's total counts it. `notify_customer` gains `{customer_link}` (the customer's link, else the invoice's), which the per-customer reminder now carries, and `invoices@1` gains `customer_link` (the link and what the customer owes in all, for any of their open lines). `get` lists `recorded` among its statuses, which R5a missed.
- **Statement pairing** (R5e; `engine/moneylog/pairing.go`, migration 00275). The import plans each line before recording any: a money-in line with exactly one unconfirmed transfer entered by hand on its channel, of its amount, within three Lagos days, is recorded correcting that entry (`corrects`, so reports count the movement once) and marked matched; with several, or with one of another amount when it is the only line near the only entry near it, it waits in `money_statement_waiting` (out of the log, its key kept so a re-upload skips it) until **Same money** or **Different money** (`GET /v1/money/statement-waiting`, `POST …/{id}`, and **Transfers to confirm** on **Upload statement**). An entry a waiting line names is not paired by another line meanwhile. "One of another amount" is bounded to a one-to-one nearness so that one transfer entered by hand does not hold a whole statement.
- **Credit sales** (Q3; migration 00276, `stock.SaleIn.OnCredit`, `api/whatsapp_stock.go`). A stock sale gains `paid_how`/`status` `credit` and `invoice_id`; `RecordSale` calls `OnCredit` inside its transaction before the sale row, and the chat's `credit to Ada` gives it `RecordCredit` with the sale's lines (or one line for one price given for all), under the sale's key. The evening total and the web show **On credit**; the web's sale form does not offer it, since it names no customer.
- **Not yet:** sending the reminder on WhatsApp where the customer asked for updates, the web app's filter, status, setting and statement, and the other three languages' words (R5f). A customer of a sent invoice is not yet offered when a name is looked up.
- Tests: `TestDebtorBook`, `TestCustomerLinkAndBalance` (engine), `TestCustomerLinkPaysOldestFirst` (matching), `TestCustomerPageThroughTheDatabase` (public host), `TestStatementPairsEnteredTransfers` (money log), `TestMoneyAPI`, `TestCreditSaleByChat`, `TestChatDebtorBook`.

R1 comes first and stands alone. It carries provisioning from chat, since it is the first to make tables from a first chat. R3 comes next, and R2 needs it (**Make a new table** is R3's table-making). R4 can run beside R2. R5 needs nothing from R3 and R4, since it writes invoices, not rows. R6 needs R3 to R5 for its intents.

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
| Q2 | Does `Ada paid 2,000 cash` record cash in the money log? | Yes: it is a cash posting matched to Ada's open invoices, as a school's cash is, for a person with `invoice.manage`. A *transfer* is asked *Into which account?*: into a connected account nothing is posted, since the rail reports it; into any other account it is entered as a transfer now, and a later statement line for it is paired with the entry (new work, §6.4). Without either word the owner is asked *How did Ada pay?* |
| Q3 | Credit sales: should `sold 3 bags of rice 45,000 credit to Ada` lower stock and make a credit invoice, with no money until she pays? | Yes, in R5: the sale's one transaction ([stock K2](stock.md)) gains the invoice, and a sale on credit is a bill |
| Q4 | Should storefront orders add their customer to `customers`? | Not in this plan: orders keep their customer sealed in Taskiem. A later twin can copy them with consent |
| Q5 | Excel: is a parsing dependency allowed? | Use one with a permissive licence (the `licences` CI check decides), or read `.xlsx` with the standard library's `archive/zip` and `encoding/xml` for the first sheet's values only. The second needs no review |
| Q6 | Slack files: ask every workspace to reinstall the app for `files:read`? | Not now. Add the scope with the next reason to reinstall, and read files from then on |
| Q8 | May a debtor's name be kept in a searchable form (lower-case, in Taskiem's database under row-level security) so `Ada owes` finds her without unsealing every customer? | Yes, as a recorded exception to the personal-data rules, not a convention: a customer's name is a sealed category (`x-pii: name`), and suppliers and stock items, the precedent, are a business's trading names and products. R5 writes the exception into the [threat model](../security/threat-model.md) and the personal-data notes with its bounds: the search form is lower-cased and trimmed, held only on `invoice_customers` under row-level security, read only by the name lookup, never in run outputs, chat echoes, audit details, exports or anything sent to the model, erased with the customer, and the sealed name stays the one shown |
| Q9 | Credit terms: which choices the first `owes` offers (7, 14, 30 days, no fixed time), and whether one is suggested first? | The owner picks; no default is applied until they do. The founder sets the choices, since a reminder after seven days to a regular customer may be harsher than a trader would choose |
| Q7 | Ask Xenibase for a trigger that keeps `updated_at` on every write (or a row version)? And for a write by filter to take its conditions in the body, not the URL? | Yes to both, as nice-to-haves. The guard on the values shown works without either; a version would make the guard cover every column, and conditions in the body would keep guarded personal values out of URLs (§8.3) |

## 11. Testing

- **Templates.** The library tests run over every new twin, plus `wd-test/v1` cases. Retrieval tests: with and without a Google Sheets connection, for the eight goals (no sheet template is in the builder's two matches without one).
- **Chat.** Conversation tests on WhatsApp and Telegram for every command above, including:
  - a change written after a web edit to the same column (refused and shown again), and after an edit to another column (written, the other edit kept);
  - two `owes` for the same customer at once (two credit invoices, one balance);
  - `who owes me?` listing a credit line and a sent invoice of the same customer as one balance, and a recorded line counted as open by matching, splitting and the statement;
  - two customers named Ada kept apart by their `book:` keys;
  - `Ada paid` attributed oldest first with the extra held as credit, and a credit invoice retired by a payment into an account number;
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
