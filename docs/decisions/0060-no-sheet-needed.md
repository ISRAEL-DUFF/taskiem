# 0060 — No sheet needed: data tables are the default record, and they are kept from chat

Date: 2026-10-30 · Status: Proposed · Plan: [no sheet needed](../spec/no-sheet-needed.md)

## Context

A trader can now sign up, connect services and publish workflows without leaving WhatsApp ([0058](0058-chat-as-a-way-in.md)). The workflows they reach for first still need a Google Sheet. Eight templates read or write one.

The AI builder's retrieval offers a sheet template first for "remind my customers who owe me", whatever the business has connected. Neither a template picked in chat nor an AI draft made from a template makes the tables the template declares.

Data tables exist for this ([0033](0033-data-tables-on-xenibase.md)), but in chat a person can only list tables, add a row and find rows. Making or changing a table, correcting or removing a row, and asking "who owes me?" all need the web app. The owner of a business made in chat may have no web sign-in.

## Decision

1. **A business without Google Sheets is offered table twins, not sheet templates, everywhere.**
   - A new condition `no_sheets` holds when the business has no `googlesheets@1` connection.
   - `replaced_where` may list several conditions.
   - The gallery, the Start page, the AI builder's retrieval and the sign-up's starters all apply the conditions, and the builder ranks a twin above its sheet template.
   - Sheet templates stay for businesses that connect Google, and workflows made from them keep working.
2. **Known tables.**
   - `debtors`, `customers` and `staff` are defined once and used by templates, chat words and the AI builder; the storefront's `products` and `services`, and the tables fronts' kinds make ([0059](0059-fronts.md)), live in the same registry.
   - They are the business's own ordinary tables, never kept. One that exists is never changed.
3. **Templates make their tables wherever they are used.**
   - A chat template build makes the tables it declares, and so does an AI draft made from a template.
   - An existing table of the name is never changed, but is checked for the columns the template needs.
4. **A list kept elsewhere comes in as a table, once.**
   - It can be a CSV or `.xlsx` file sent on WhatsApp or Telegram, or a Google Sheet read once through the business's connection. Slack files wait for the `files:read` scope.
   - The import is the existing import, with the same limits and idempotency.
   - There is no sync.
5. **Tables are made and changed in chat.**
   - Rows are changed and removed in chat, each after the person's **yes**.
   - A write names the row's id and the values the person was shown, so a change made elsewhere since then is never overwritten. `updated_at` cannot do this, because nothing changes it after a row is made.
   - Dropping a column or deleting a table needs the name typed back and the chat PIN where one is set, which today means WhatsApp. Otherwise it goes to the web.
   - Kept tables stay closed to every change, in chat too.
6. **The debtor book.**
   - `<name> owes <amount>`, `<name> paid <amount>` and `who owes me?` act on the `debtors` table, or the table the owner names as the book.
   - It is the notebook, not a second ledger: money a customer should be sent something to pay is an invoice, the book offers to raise one, and `who owes me?` answers with both.
   - A payment said with *cash* is also recorded once in the money log, for a person allowed to record money (`invoice.manage`).
   - A *transfer* is not, because the rail records it already.
7. **Questions over rows become a checked query spec, never free reading.**
   - The model, where used, gets the question and the table and column names and types. It never gets a row.
   - It returns a spec that is checked against the person's tables and columns, with types, operators, values, a limit of 20 and at most one count or sum, before one read-only query runs.
   - Code writes the answer, with personal values masked and the reading of the question shown.
8. **The model routes table commands in every language**, under the existing rule: it returns an intent and an argument, never `yes`, `no` or `cancel`, so every write waits for the person's own reply.

## Consequences

- "Remind my customers who owe me" works for a phone-only trader with no sheet and no database in sight. This is the strategy's [§3.3](../strategy/chat-first-nigeria.md#33-xenibase-under-the-agent) goal.
- Records live in one place, the business's Xenibase project, whether they arrive from chat, a file, the web or a workflow.
- More of chat writes data. Each write path has a **yes**, an idempotency key and the guard on the values shown, and destructive changes need the PIN.
- The model sees more kinds of request but no more data. Its output is a routing or a spec that code checks, and it runs with the person's permissions.
- Sheet templates are offered to fewer businesses. Their twins carry the library's tests and their own `wd-test/v1` cases.
