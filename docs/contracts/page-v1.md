# Contract: Page `page/v1`

A page is a screen a business makes for itself from parts Taskiem draws ([decision 0072](../decisions/0072-pages.md), [the plan](../spec/pages.md), [the guide](../pages.md)). The structure is defined by `schemas/page-v1.schema.json`; this page adds the rules a JSON Schema cannot express. `engine/pages` enforces both: `Parse` what the document says alone, `Check` what needs the business's tables. Every problem is a sentence an owner can act on, with the path it is about (`/blocks/2/source/table`), and never says "block", "schema", "query" or "expression".

## Structure in brief

```json
{ "page": "page/v1", "name": "tickets", "title": "Tickets", "icon": "tables", "about": "Every ticket, newest first",
  "roles": ["support", "manager"],
  "filters": [ { "id": "q", "type": "search" },
               { "id": "st", "type": "choice", "column": "status", "options": [ { "value": "open" }, { "value": "done", "label": "Done" } ] },
               { "id": "when", "type": "dates", "column": "opened" },
               { "id": "mine", "type": "mine", "column": "email", "label": "Mine" } ],
  "blocks": [
    { "id": "head", "type": "heading", "title": "Support desk", "text": "What is open now" },
    { "id": "open", "type": "figure", "label": "Open", "value": "count", "tone": "warn", "lead": true,
      "source": { "table": "tickets", "filters": [ { "column": "status", "op": "eq", "value": "open" } ] } },
    { "id": "owed", "type": "figure", "label": "Owed", "value": "sum", "of": "amount_kobo", "naira": true,
      "source": { "table": "tickets" } },
    { "id": "week", "type": "figure", "label": "Opened this week", "value": "count",
      "change": { "column": "opened", "period": "week" }, "source": { "table": "tickets" } },
    { "id": "list", "type": "list", "title": "Tickets",
      "source": { "table": "tickets", "order": [ { "column": "created_at", "desc": true } ], "uses": ["q", "st", "when", "mine"] },
      "item": { "title": "=row.title", "sub": "=row.customer", "state": { "text": "=row.status", "tones": { "open": "warn", "done": "good" } } },
      "group": { "column": "status", "order": ["open", "doing", "done"] },
      "detail": { "title": "=row.title", "lines": [ { "label": "Customer", "value": "=row.customer" },
                                                     { "label": "Amount", "value": "=row.amount_kobo", "naira": true } ] },
      "empty": "No tickets" },
    { "id": "all", "type": "table", "source": { "table": "tickets" },
      "columns": [ { "column": "title" }, { "column": "amount_kobo", "label": "Amount", "naira": true } ] } ] }
```

- **Top level:** `page` (`"page/v1"`), `name` (`^[a-z][a-z0-9-]{1,23}$`), `title` (1 to 60 letters), optional `about` (200), `icon` (one of Taskiem's: tables, orders, money, invoices, members, box, tag, clock, check, bell, star, place, forms, reports, store, grid), `roles`, `filters`, `blocks`.
- **Parts (`blocks`):** `heading` (`title`, `text`), `figure` (`label`, `source`, `value` `count` or `sum` with `of`, `naira`, `tone`, `lead`, `change`), `list` (`title`, `size`, `source`, `item`, `group`, `detail`, `empty`), `table` (`title`, `size`, `source`, `columns`, `detail`, `empty`). A row's detail belongs to its list or table and opens as a sheet from the row.
- **A source** is a data table by name (`table`), or one of Taskiem's own record views (`records`: `orders`, `invoices`), never both, with `filters` (conditions every row meets: `column`, `op`, `value`), `order` (up to two columns) and, for a list or a table, `uses` (the page's filters that narrow it). A figure has no `uses`: it stays whole.
- **Values** shown (`item`, `state.text`, `detail`) are words, or an expression after `=` over `row` (the row, its values written as people read them: dates in Lagos, Yes and No) and `filters` (the page's filter values). `state.tones` gives a tone to each of a state's words.
- **Shapes (`shapes/v1`):** a list's `item` and `state`, and a detail's `title` and `lines`, are a made tool's `list` and `card` answers' shapes ([made tools §7](../spec/made-tools.md#7-the-answer)); a page's detail may have twelve lines where a card in the chat has six.

## Rules

1. **Names.** `name` is unique in the business, never one of Taskiem's own command words in any language that is on (`wdcheck.ReservedCommand`, the list a tool's command is held to), and never a tool's command. A part's and a filter's `id` are unique in the page.
2. **Limits.** 12 parts; at most 4 figures side by side (a fifth needs another part between); one `lead`; 4 filters; 5 conditions on a source; 2 orders; 6 columns in a table; 12 lines on a detail; 20 groups in order and 20 choices.
3. **Sources are checked against the table** through the checked query of decision 0060 (`datatables.CheckQuery`), at publish and again at every view: the table is the business's; every column is the table's (or `id`, `created_at`, `updated_at`); each operator fits its column's type (`eq`, `neq`, `lt`, `lte`, `gt`, `gte`, `contains`, `is_null`, `not_null`; comparisons for numbers and dates, `contains` for words); each value fits its column. Text is matched without regard to case.
4. **A figure.** It counts (or adds up) every row its own conditions pick: `uses` on a figure is refused. `sum` adds up a number column (`integer` or `number`) that is not personal; `naira` draws it as naira from kobo. `change` names a `date` or `datetime` column that is not personal and a `period` (`day`, `week` from Monday, `month`, in Lagos): the figure counts (or adds up) only the rows in this period and says the change on the one before. A total is added up over at most 5,000 rows and says so when it stopped there.
5. **A list's groups** are a column's values (not a personal or json column), in `group.order` first (matched without regard to case), then as they come; a row with none is under "Other".
6. **Filters.** `search` looks in the `columns` it names (words, emails or phones) or every text column; `choice` names a text column and its `options`; `dates` a `date` or `datetime` column (neither on a personal column: narrowing by one would show what the mask hides, a birth date found a day at a time), set to `today`, `week`, `month` or `YYYY-MM-DD..YYYY-MM-DD` (up to a year); `mine` an `email` column, matched to the viewer's email. A part may use a filter only when it fits the part's table.
7. **Expressions** read `row.<column>` for a column the table has, and `filters.<id>`; nothing else (no steps, secrets, environment). `outputs` and `item` come with tools as sources (G2).
8. **What a viewer is shown.** A page reads with the business's connection and shows what it was made to show to those its `roles` let open it (owners always; none named, everyone in the business). A column marked personal (phones and emails by default) is masked for a viewer without `table.read`, before any expression reads the row; a search by such a viewer leaves personal columns out; a table part is not put in order by a personal column for them; a choice or dates filter on one (a page published before rule 6 said so) is neither drawn nor applied for them. More's cursor is Taskiem's own, taken back only with the order it was given for; rows a part orders by a personal column go on for such a viewer by a skip of the rows shown (through the first 1,000), never by the data API's cursor, which may carry the value a row was ranked by.
9. **A part that cannot be drawn** (its table or a column gone since it was published) is refused at publish, and at view is shown as "This part needs fixing" to those who may edit the page and left out for everyone else.
10. **Taskiem's own records** (`"source": {"records": "orders"}`): a view Taskiem defines with a fixed list of columns a page may name, and nothing else. **orders:** `number`, `customer`, `phone`, `email` (personal), `status`, `kind`, `channel`, `total`, `paid` (kobo), `fulfilment`, `zone`, `delivery`, `placed_at`, `paid_at`. **invoices:** `number`, `customer`, `phone`, `email` (personal), `status`, `kind`, `total`, `paid`, `owed` (kobo), `due_on`, `issued_at`, `paid_at`. Both also `created_at`. A view is read as a table is (rules 3 to 6), but its personal columns are masked for every viewer and are never a condition, an order, a search's column, Mine's column, a group, a choice, dates or a sum. Check refuses a view Taskiem does not have and one whose area the business has turned off (orders: the Orders area; invoices: Money and invoices). The viewer needs the area's read permission (orders: `storefront.manage` or `order.handle`; invoices: `invoice.read`): without it the part is drawn as `unavailable` ("You cannot see orders."), as is a part whose area was turned off since publishing. A row carries `link`, the record's own screen. Read only.

Rules 1 (but uniqueness), 2, 7, a figure's `uses` and the structure are `pages.Parse`; 1's uniqueness, 3 to 6 and 10's refusals are `pages.Check`; 8, 9 and 10's drawing are the API's view (`api/pages.go`, `api/pages_records.go`) with `pages.ReadFor` and `pages.Shape`.

## Kept up to date

An open page may follow `GET /v1/pages/{name}/live` (server-sent events): `changed` `{"page", "blocks": [ids], "all"}` names the parts whose table, orders or invoices changed (every part, `all`, when the page itself did), at most once in two seconds, and carries no data; the client reads those parts again. Only for a page the viewer may open.

## The file

A page saved as a file (`GET /v1/pages/{name}/export`, `<name>.page.json`) is this document and nothing else, written with two spaces of indent: no rows, no ids of the business, and nothing that carries a personal value: a condition on a personal column with a value, every condition value of a part whose table the business no longer has, a choice filter on a personal column, a group order or state colours by one, and the words of an expression that reads one (each left out, and counted in the `Taskiem-Left-Out` header). Reading one back (`POST /v1/pages/import`) holds it to everything above: Parse refuses what is not a page, and Check against the business it is read into says what it names that the business lacks. A name already taken there becomes the next free one (`tickets-2`).

## Changes

The format is new with G0 (decision 0072). Parts named in the plan and not in this version (buttons, status, board, chart, and tools as sources) are additions to it when their phases come; a document without them is unchanged. `records` (orders and invoices) was added on 23 November 2026; deliveries, customers and staff are further views of it.
