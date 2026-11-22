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
- **A source** is a data table by name, with `filters` (conditions every row meets: `column`, `op`, `value`), `order` (up to two columns) and `uses` (the page's filters that narrow it).
- **Values** shown (`item`, `state.text`, `detail`) are words, or an expression after `=` over `row` (the row, its values written as people read them: dates in Lagos, Yes and No) and `filters` (the page's filter values). `state.tones` gives a tone to each of a state's words.
- **Shapes (`shapes/v1`):** a list's `item` and `state`, and a detail's `title` and `lines`, are a made tool's `list` and `card` answers' shapes ([made tools §7](../spec/made-tools.md#7-the-answer)); a page's detail may have twelve lines where a card in the chat has six.

## Rules

1. **Names.** `name` is unique in the business, never one of Taskiem's own command words in any language that is on (`wdcheck.ReservedCommand`, the list a tool's command is held to), and never a tool's command. A part's and a filter's `id` are unique in the page.
2. **Limits.** 12 parts; at most 4 figures side by side (a fifth needs another part between); one `lead`; 4 filters; 5 conditions on a source; 2 orders; 6 columns in a table; 12 lines on a detail; 20 groups in order and 20 choices.
3. **Sources are checked against the table** through the checked query of decision 0060 (`datatables.CheckQuery`), at publish and again at every view: the table is the business's; every column is the table's (or `id`, `created_at`, `updated_at`); each operator fits its column's type (`eq`, `neq`, `lt`, `lte`, `gt`, `gte`, `contains`, `is_null`, `not_null`; comparisons for numbers and dates, `contains` for words); each value fits its column. Text is matched without regard to case.
4. **A figure.** `sum` adds up a number column (`integer` or `number`); `naira` draws it as naira from kobo. `change` names a `date` or `datetime` column and a `period` (`day`, `week` from Monday, `month`, in Lagos): the figure counts (or adds up) only the rows in this period and says the change on the one before. A total is added up over at most 5,000 rows and says so when it stopped there.
5. **A list's groups** are a column's values (not a personal or json column), in `group.order` first (matched without regard to case), then as they come; a row with none is under "Other".
6. **Filters.** `search` looks in the `columns` it names (words, emails or phones) or every text column; `choice` names a text column and its `options`; `dates` a `date` or `datetime` column, set to `today`, `week`, `month` or `YYYY-MM-DD..YYYY-MM-DD` (up to a year); `mine` an `email` column, matched to the viewer's email. A part may use a filter only when it fits the part's table.
7. **Expressions** read `row.<column>` for a column the table has, and `filters.<id>`; nothing else (no steps, secrets, environment). `outputs` and `item` come with tools as sources (G2).
8. **What a viewer is shown.** A page reads with the business's connection and shows what it was made to show to those its `roles` let open it (owners always; none named, everyone in the business). A column marked personal (phones and emails by default) is masked for a viewer without `table.read`, before any expression reads the row; a search by such a viewer leaves personal columns out.
9. **A part that cannot be drawn** (its table or a column gone since it was published) is refused at publish, and at view is shown as "This part needs fixing" to those who may edit the page and left out for everyone else.

Rules 1 (but uniqueness), 2, 7 and the structure are `pages.Parse`; 1's uniqueness and 3 to 6 are `pages.Check`; 8 and 9 are the API's view (`api/pages.go`) with `pages.ReadFor` and `pages.Shape`.

## Changes

The format is new with G0 (decision 0072). Parts named in the plan and not in this version (buttons, status, board, chart, and tools or Taskiem's own records as sources) are additions to it when their phases come; a document without them is unchanged.
