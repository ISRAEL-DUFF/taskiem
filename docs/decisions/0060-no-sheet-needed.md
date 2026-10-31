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
   - `customers` and `staff` are defined once and used by templates, chat words and the AI builder; the storefront's `products` and `services`, and the tables fronts' kinds make ([0059](0059-fronts.md)), live in the same registry. There is no `debtors` table (point 6).
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
6. **The debtor book is invoices.**
   - Money owed is kept in one place, invoices, which already have a balance, part payments, matching, receipts and oldest-first attribution. A debtors table would be a second ledger with a second "paid", so there is none.
   - `<name> owes <amount>` makes an invoice of a new kind, `credit`, in a new status, **recorded**: open and owed everywhere (matching, splitting, credit, statements, lists), never messaged, due after the business's credit terms or a given date, always taking part payments.
   - A customer with no phone or email is a record of their own (`invoice_customers`), keyed `book:<id>` on their invoices, so attribution and statements group by it; a phone given later is added to it. A customer recorded with a phone, or chosen in the name lookup from an invoice sent to them, is that phone's customer, as the invoice is: a notebook line and an invoice sent to the same number are one balance.
   - `<name> paid <amount>` is a payment counted towards that customer's open invoices oldest first, any extra held as credit. *Cash* posts cash to the money log. *Transfer* is asked where the money went: into a connected account nothing is posted, since the rail's report is the record; into any other account it is entered now under that account's channel name, and the statement line for it is paired with the entry when that channel's statement is imported (new: today an import would record it again). Nothing is counted twice and nothing is lost.
   - `who owes me?` answers from open invoices, recorded and sent alike, as one balance per customer. `send <name> her balance` sends the recorded lines together in one message with one way to pay the total: the customer's own account number, or a customer payment link issued against the customer and matched to their open invoices oldest first, which is new. A credit sale from the stock words makes a credit invoice.
   - There is one reminder: `chase-overdue-invoices` gains one message per customer (on for new workflows, off for running ones) and the "who owes me" phrases (never the bare word "owe"), and replaces the two debtor-reminder sheet templates; `debtor-reminder-table` is retired. It reminds only what is overdue; credit lines fall due after the business's credit terms, which the owner chooses when they record their first notebook debt (no default is applied until they do), and no terms means no reminders for them. A line's due date is fixed when it is recorded. A change of terms applies to new lines unless the owner chooses to apply it to open ones too. Only a person who may change the invoice settings business-wide is asked.
   - `notify_customer` learns recorded lines: it sends where there is an address and answers `sent: false` where there is none, so a cash receipt on a notebook debt is not a failed run.
   - The plan's §6.7 lists the claims earlier versions made about today's code that were false, for the reviewer to acknowledge.
   - A debtor's name is kept in a lower-cased search form beside the sealed one, as a recorded exception to the personal-data rules with its bounds written into the threat model.
7. **Questions over rows become a checked query spec, never free reading.**
   - The model, where used, gets the question and the table and column names and types. It never gets a row.
   - It returns a spec that is checked against the person's tables and columns, with types, operators, values, a limit of 20 and at most one count or sum, before one read-only query runs.
   - Code writes the answer, with personal values masked and the reading of the question shown.
8. **The model routes table commands in every language**, under the existing rule: it returns an intent and an argument, never `yes`, `no` or `cancel`, so every write waits for the person's own reply.

## Consequences

- "Remind my customers who owe me" works for a phone-only trader with no sheet and no database in sight, through invoices, with nothing new to make. This is the strategy's [§3.3](../strategy/chat-first-nigeria.md#33-xenibase-under-the-agent) goal.
- Records live in one place, the business's Xenibase project, whether they arrive from chat, a file, the web or a workflow.
- More of chat writes data. Each write path has a **yes**, an idempotency key and the guard on the values shown, and destructive changes need the PIN.
- The model sees more kinds of request but no more data. Its output is a routing or a spec that code checks, and it runs with the person's permissions.
- Sheet templates are offered to fewer businesses. Their twins carry the library's tests and their own `wd-test/v1` cases.
