# Pages: a business's own screens

Status: **plan, settled; G0 and G1 built 22 November 2026** ([As built](#18-as-built-g0-and-g1); [decision 0072](../decisions/0072-pages.md), proposed). Written 19 November 2026 with the founder, who took every recommendation in §16 the same day. The founder chose to build [made tools](made-tools.md) first and to have this written now so it is ready ([made tools](made-tools.md) §14, 6). §16 lists what the founder decided. Its phases are G0 to G5 in [the build plan](build-plan-saved-tools-payments.md).

Builds on: [made tools](made-tools.md) (the answer's shapes, buttons, care), [data tables](../data-tables.md), [forms](../forms.md), [chat tools](chat-tools.md), [no technical steps](no-technical-steps.md), [saved objects](saved-objects.md).

## 1. Summary

Taskiem has screens for what it built: orders, stock, money, staff. A business also makes workflows and tables of its own: tickets, expenses, leave, generators, a waiting list. Taskiem cannot build a screen for each.

A **page** is a screen the business makes for itself, out of a small set of **blocks** Taskiem draws: a figure, a list, a table, a board, a chart, buttons. Each block is fed by something the business already has: a data table, or a made tool that only reads. A button on a page is a made tool.

- "Tickets": three figures (open, waiting, closed this week), the open tickets as a list, **Assign to me** and **Close** on each row, **New ticket** at the top.
- "Expenses this month": the total, a chart by week, the rows, **Log an expense**.
- "Generators": each generator with its last service date, and **Log a service**.

A page is not code and not a canvas. The owner picks blocks and says what feeds them; Taskiem lays them out for a phone first. That is what lets the web and the phone app both draw every page, and keeps it within reach of an owner who has never built anything.

Pages sit beside the conversation, not in place of it: every page has a short form in the chat (§9), so a business on WhatsApp alone still gets its figures and its list.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Page | One screen of a business, with a name, an icon and blocks |
| Block | One part of a page, of a kind Taskiem draws (§4) |
| Source | What feeds a block: a data table, or a made tool that only reads (§5) |
| Filter | A control at the top of a page (a search, a choice, dates) that narrows its blocks (§6) |
| Action | A button on a page or a row: it opens a made tool's form (§7) |
| Pinned | Shown in the business's navigation and tools panel |
| Draft, published | As for workflows: a page is changed as a draft and shown to others once published |

## 3. What exists, and what is missing

| Today | Where | What a page needs from it |
| --- | --- | --- |
| Data tables: a business's own rows, columns with types, a personal flag; a page of rows by filters and order, with a count | [data tables](../data-tables.md), `POST /v1/tables/{id}/rows/query` | The first source. Reads as the table's API does, with `table.read`'s masking rule (§8) |
| One screen for each table: its rows, search, add, edit | `web/src/pages` (`TablePage`), the Tables area | Stays, as the record. A page is a view chosen for a job, not a replacement |
| Made tools' answers: `text`, `card`, `list`, with buttons | [made tools](made-tools.md) §7, §10 (plan; M1, M2) | The list block **is** the `list` answer; the detail **is** the `card`; an action **is** a button |
| Care: whether a tool only reads, worked out from its steps | Made tools §8 | Only a tool that "answers at once" may feed a block |
| The cards that fold to a row and open | `ChatRefCard.tsx`, `ref_card.dart` (built) | A row's detail |
| Forms | [forms](../forms.md) | An action's form is the tool's form, as in the chat |
| Saved objects of a made-up kind (Generator, Car) | [saved objects](saved-objects.md) (plan; S1) | A later source (§5) |
| Nothing lets a business compose a screen | | The whole of this plan |

## 4. The blocks

A fixed set. Each is drawn by Taskiem on the web and in the app, light and dark, at 360 px.

| Block | Shows | Fed by | Phase |
| --- | --- | --- | --- |
| **Heading** | A title and a line of plain words | Nothing | G1 |
| **Figure** | One number with a label, and a tone (plain, good, warn, bad); an amount in kobo is drawn as naira. It may show its change on the period before ("up 12% on last week"). Up to four sit in a row; one may be marked as the page's lead and is drawn large | A table's or a record view's count or sum under a filter; or one output of a tool | G1 (count, sum), G2 (tool) |
| **List** | Rows, each a title, a line under it and a state; a press opens the row's detail. May be **grouped** by a choice column, as headed sections (Open, Doing, Done) | A table; or a tool's output of type `list` | G1, G2 |
| **Table** | Rows in columns the owner picks, sortable by a press on a column | A table | G1 |
| **Detail** | One row as a card: a title, up to twelve labelled lines, its actions. Opens as a sheet from a row | The row pressed | G1 |
| **Buttons** | Up to three actions for the whole page (**New ticket**) | Made tools, signals, approvals (§7) | G2 |
| **Status** | A run's state in plain words: queued, working, waiting for whom, done, failed | A run named by the row or by the page's address | G2 |
| **Board** | A list in columns by a choice (Open, Doing, Done); a card moved to another column runs a tool | A table or a tool, with a tool for the move | G4 |
| **Chart** | Bars or a line: one measure over time or by a group | A tool's output of type `list` with a label and a number | G4 |

The list block's `item`, its `state` and its row buttons, and the detail's `lines`, are the same shapes as made tools' `list` and `card` answers, with the same expressions. A `list` answer can be put on a page unchanged ("Pin this as a page").

**Layout.** One column on a phone. On a wide screen a block is full width or half; figures share a row. There is no free placing. The order of blocks is the order on a phone.

**Limits:** 12 blocks on a page; 4 figures in a row; 25 rows shown, then **More**; 3 actions for the page; 2 on a row; 6 columns in a board.

## 5. Sources

**A data table.** The block names the table, the columns it shows, a filter and an order, or one **count** or **sum**. It is read through the checked query of decision 0060 (point 7): names and types checked against the business's tables at publish and at each read, operators from a fixed list. A count is one read. A sum is read in pages of 1,000 rows, up to 5,000, since the data API has no sum; a figure says so when it stopped there. Fast, and counts no run.

**Taskiem's own records, read only.** `records`: orders, invoices, deliveries, customers or staff, each a view Taskiem defines with a fixed list of the columns a page may show, filtered and ordered as a table is. An area the business has off is refused. A page can list today's deliveries; it cannot change one except through an action (§7), so the rules those screens hold are kept.

**A made tool that only reads.** The block names the tool and gives its inputs (fixed values, or the page's filters). Taskiem starts it and draws its outputs. This is how a page shows anything a workflow can work out: a sum, rows joined from two tables, figures from a connected app.

- Only a tool whose care is "answers at once" (made tools §8) may be a source, so opening a page can never change anything. The validator refuses any other.
- Its run is a run: it counts against the business's limits (§16, 3). What it returned is kept for a short while (proposed 60 seconds, for the same viewer and inputs), so a page reopened does not run it again; **Refresh** runs it now.
- A page shows when each such block was worked out ("Updated 2 minutes ago").
- Limits: 3 tool sources on a page; a source that has not answered in 10 seconds shows "Still working" and fills in when it does.

**Saved objects of one kind** (G5, with saved objects S1): the business's objects of a made-up kind as a list ("Generators"), with dates coming up.

**Not a source:** anything of Taskiem's own beyond those views (money, stock levels, costs), and other systems. A workflow that calls a web address puts what it needs in a table or an output first, so every page is masked and bounded the same way.

**Expressions** on a page run in the API when it draws the page, over three names only: `row` (a table's row), `outputs` and `item` (a tool's), and `filters`. They cannot read steps, secrets or the environment (made tools §7).

## 6. Filters

A page may have up to four controls at its top: a **search**, a **choice** (from a list the owner gives, or a table column's values), a **date range** (Today, This week, This month, or two dates), and **Mine** (rows whose person column is the viewer).

A filter narrows every block that says it uses it: a table source by its column, a tool source as one of its inputs. The page remembers a viewer's last filters on that device.

## 7. Actions

An action is on the page's **Buttons**, on a row, or on a detail, and is one of three things:

| Action | What happens |
| --- | --- |
| **Call a tool** (`tool`, `inputs`) | The tool's form opens filled in, with its care. The rest of this section |
| **Signal a run** (`signal: { run, name, input }`) | The named signal is sent to that run, as a run's own buttons send them today (a delivery's **Confirm** is one). Only a signal the run's workflow declares as one a page may send; it asks **Yes** first |
| **Approve** (`approve: { run, step }`) | The approval sheet, with its PIN and rules, unchanged |

`when` on an action hides it when false (`"when": "=row.status == 'delivered'"`). A signal to a run that has moved on says so.

For a tool: pressing it opens that tool's form in a sheet, filled in from the row (`"inputs": { "ref": "=row.ref" }`), exactly as a button in the chat does (made tools §10):

- the tool's care applies: **yes**, or step-up where it moves money or the business's policies ask;
- it runs as the person who pressed, with their roles; someone who may not use the tool sees the button greyed with why;
- its answer shows in the sheet and in the person's Taskiem chat, as any tool's does;
- the blocks it could have changed are read again when it finishes.

**Changing a row in place** (a tick, a choice) on a table source needs `table.edit` and is offered only on a **Table** block the owner marks "can be changed here" (G2). Everything else is done by a tool, so the business's own rules are kept.

## 8. Who may see a page, and what it shows them

- **A page has `roles`**, as a tool does: who may open it. Left out: everyone in the business.
- **A page shows what its maker chose, to those who may open it.** Like a tool's answer (made tools §8), a block reads with the business's connections, not the viewer's rights. A page over the salaries table shows what it was built to show to everyone allowed to open it.
- **So publishing says so, in plain words:** "Managers will be able to open **Payroll overview**. It shows: name, role, net pay." A change to who may open it, or to what it shows, is called out to the approver where the business has four-eyes.
- **Personal data:** a column marked personal is masked on a page (a phone to its last four digits) unless the viewer also holds `table.read`. A tool's outputs are masked as its answers are.
- **The AI never sees a page's rows.** The builder is given tables' and tools' names and types only.
- **Not for customers or linked businesses** in this plan (§15).

## 9. Where a page shows

- **Pages**, an area on the web and in the app: the business's pages as tiles. Up to five may be **pinned** to the navigation (settled by the founder, 19 November 2026; pull request #211 had proposed none). A pinned page is drawn under a **Your pages** heading, apart from Taskiem's own areas, so what Taskiem built and what the business made are never confused.
- **As a tab under an area** (`"place": { "area": "orders" }`): after Taskiem's own tabs on that area's page, while the area is on.
- **As an alert** (`"alert": true` on a figure that counts): when the count is above zero the alerts list shows the page's name and the count, and opens it, as the hand-built Confirm alert does.
- **In the tools panel** of the Taskiem chat, a pinned page is a tile; pressing it opens the page as a sheet over the chat.
- **`/` and its name** in the Taskiem chat: `/tickets` opens the page (a page's name can never be a word of Taskiem's own or a tool's command; the same reserved list).
- **On WhatsApp, Telegram and Slack** a page cannot be drawn. Its name answers with a **short form**: its figures on a line each, the first five rows of its first list, and a link to open it. "tickets" answers "Open 7 · Waiting 2 · Closed this week 14", five rows, **Open the page**. Actions are reached as tools there, by their own commands.
- **From a notification:** `notify.tell` may carry a button that opens a page with filters set ("Disk alerts" filtered to db-2).

**Kept up to date.** A page is read when it is opened and on pull to refresh. A block over a table, a record view or a run is also told when they change, through the change notices the chat tools already use (`api/chat_changes.go`), so a **Confirm** pressed on the web shows on the phone without a reload. Tool sources are not: they show when they were worked out, and **Refresh**.

## 10. Making a page

No technical words on this path ([no technical steps](no-technical-steps.md)).

1. **From a table:** **Show as a page** on a table makes a page with a count, the table as a list, and its detail. The quickest start.
2. **From a tool's answer:** **Pin as a page** on a `list` or `card` answer.
3. **From a starter:** Tickets desk, Expenses this month, Leave, Stock count, Deliveries today, Waiting list, Service log. A starter brings its table and tools with it.
4. **Describe it:** "a page of open tickets with how many are waiting, and a button to add one". The builder drafts the page and names what it would create (G5).
5. **By hand:** **Add a block**, pick its kind, pick what feeds it from the business's tables and tools, pick what each row shows. A live preview at phone width beside it.

Blocks are moved by dragging (web) or **Move up / Move down** (app, and for keyboards). A page is saved as a draft and **published**; earlier versions are kept and can be brought back.

## 11. The definition

A page is a JSON document, `page/v1`, kept and versioned like a workflow's definition, with its own schema (`schemas/page-v1.schema.json`) and contract. It is not part of `wd/v1`: a page runs nothing.

```json
{ "page": "page/v1", "name": "tickets", "title": "Tickets", "icon": "ticket",
  "roles": ["support", "manager"],
  "filters": [ { "id": "mine", "type": "mine", "label": "Mine" },
               { "id": "q", "type": "search" } ],
  "blocks": [
    { "id": "open", "type": "figure", "label": "Open",
      "source": { "table": "tickets", "filters": [ { "column": "status", "op": "eq", "value": "open" } ] },
      "value": "count", "tone": "warn" },
    { "id": "list", "type": "list", "title": "Open tickets",
      "source": { "table": "tickets", "filters": [ { "column": "status", "op": "eq", "value": "open" } ],
                  "order": [ { "column": "created_at", "desc": true } ], "uses": ["mine", "q"] },
      "item": { "title": "=row.title", "sub": "=row.customer", "state": { "text": "=row.priority", "tone": "plain" } },
      "detail": { "title": "=row.title", "lines": [ { "label": "Customer", "value": "=row.customer" },
                                                     { "label": "Opened", "value": "=row.created_at" } ] },
      "actions": [ { "label": "Assign to me", "tool": "assign-ticket", "inputs": { "ref": "=row.id" } } ],
      "empty": "No open tickets" },
    { "id": "new", "type": "buttons", "actions": [ { "label": "New ticket", "tool": "new-ticket" } ] } ] }
```

**The shapes are defined once.** The list's `item`, the detail's `lines`, `state`, buttons and the card are one named part of the schema, `shapes/v1`, used by a made tool's answer and by every block here, with one renderer on the web and one in the app.

**The validator refuses:** a record view for an area that is off; a signal the run's workflow does not declare for pages; a block kind it does not know; a source naming a table, column or tool that does not exist; a tool source whose care is not "answers at once"; an action naming a tool that does not exist (a warning at draft, refused at publish); an expression reading anything but `row`, `outputs`, `item`, `filters`; more than the limits; a name that is a reserved word or another page's or tool's.

**When what it stands on changes.** A table column dropped, or a tool unpublished, leaves a page with a block that cannot be drawn. That block shows "This part needs fixing" to those who may edit the page and is left out for others; the owner is told, and dropping a column or unpublishing a tool first lists the pages that use it.

## 12. Storage and API

- **`pages`**: tenant, `name` (unique in the tenant), title, icon, roles, pinned and its order, the published version. **`page_versions`**: the document, who, when, draft or published. Both under forced row-level security.
- **`page_source_cache`**: a tool source's outputs for a page, block, viewer and inputs, with when it was worked out; cleared after its time and when an action from that page finishes.
- **API:** `/v1/pages` (list, make, change, publish, delete, versions); `GET /v1/pages/{name}/view?filters` returns the page as drawn: each block with its rows, figures and buttons already worked out, masked for this viewer; `GET /v1/pages/{name}/blocks/{id}?cursor` for **More**; `POST /v1/pages/{name}/refresh`; `POST /v1/pages/validate`.
- The web and the app both draw from `view`: neither reads a table or starts a tool for a page itself, so the rules of §8 are held in one place.
- **Permissions:** `page.edit` and `page.publish` (owners, admins, and roles given them); opening needs the page's roles.
- OpenAPI, the app's generated client and the SDK as always. Export and import of a page as a file, with the workflow export.
- **Plan limit:** `max_pages`: 10 on the lowest plan, 50 on the middle, none above, beside `max_data_tables`. Drafts count. A business over the limit after moving to a lower plan keeps its pages and cannot add more.

## 13. Security and abuse

| Risk | What stops it |
| --- | --- |
| A page that changes something when opened | Only tables and tools that only read may be sources; checked at publish and again at each view |
| A page showing more than was meant | Publishing lists who may open it and what it shows; four-eyes where the business has it; personal columns masked without `table.read` (§8) |
| A page used to walk round a tool's roles | An action is the tool, with its roles and care, as the person who pressed |
| A page that uses up the month's runs | Tool sources are kept for a short while; 3 on a page; counted and shown on the page's settings ("about 40 runs a day") |
| Code or a script on a page | None can be given: blocks are Taskiem's, expressions read four names |
| A viewer's words steering a model | Filters are values passed to a filter or a tool's input, never to a model |
| Rows reaching the AI builder | It is given names and types only; a test reads what is sent |
| A stale page acted on | An action's tool checks the state itself (made tools §7); the page reads again after an action |

## 14. Phases

| Phase | What | Needs | Done when |
| --- | --- | --- | --- |
| G0 | The decision record; `page/v1`, its schema, contract and validator; storage; the API without drawing | The founder's answers (§16) | A page document validates or is refused for the right reason |
| G1 | Pages from tables and record views: heading, figure (count, sum), list (grouped too), table, detail, filters; the change notices; the Pages area, pinning, a tab under an area, roles, draft and publish; **Show as a page**; web and app | G0 | An owner turns the tickets table into a page with a count and a list in under two minutes, and staff open it on a phone |
| G2 | Actions: tools, signals and approvals on the page and on rows; tools as sources, figures from a tool, the status block, a page as an alert, changing a row in place, **Pin as a page**; the deliveries fixture | G1, M2 | "Tickets" has **New ticket** and **Assign to me**; "Expenses this month" shows a total a workflow worked out; **Confirm** on the fixture page moves a delivery from a phone and shows on the web without a reload |
| G3 | The chat: the tile, `/name`, the short form on WhatsApp, Telegram and Slack, a page opened from a notification | G2, M1 | "tickets" on WhatsApp answers with the figures, five rows and a link |
| G4 | Board and chart | G2 | A ticket dragged from Open to Doing runs its tool; expenses show by week |
| G5 | Starters, **Describe it**, saved objects of a kind as a source | G2, M6, S1 | "Describe it" makes a working page from one sentence |

G1 stands on tables alone, but is started only once made tools' cards and buttons (M2) are under way (§16, 7). G2 onward needs M2.

## 15. What this is not

- **Not a website builder.** Storefronts and public pages are their own things. A page is for the business's own people.
- **Not for customers or linked businesses** in this plan. What a customer sees is the storefront, the conversation and customers' tools.
- **Not a free canvas**, and not themes or styles of a business's own. Taskiem lays out and draws.
- **Not code.** No script, no frame, no drawing of a business's own, on the web or in the app.
- **Not Taskiem's own screens rebuilt.** Orders, Stock and Money stay as built.
- **Not a report writer.** A chart shows one measure. Reports with many are for an export or a connected tool.
- **Not for embedded builders** in this plan.

## 15a. Taken from pull request #211

That pull request specified pages as a `views` block inside a workflow. The founder settled on a page as its own document (§16, 1), so its structure was not taken. These parts were, on 19 November 2026: sums through the checked query (§5); Taskiem's own records as read-only views (§5); signals and approvals as actions, and `when` (§7); a page as a tab under an area and as an alert (§9); the change notices (§9); one definition of the shapes (§11); the status block (§4); the deliveries page as the acceptance fixture (§17). On the one disagreement, pinning to the navigation, the founder chose to allow it.

## 16. For the founder to decide

**Settled 19 November 2026.** The founder took each recommendation below, with these changes: grouped lists and figures that show a change are in the first release in place of a board and a chart (5); the limit is 10 pages on the lowest plan, 50 on the middle and none above, drafts counted, and pages never switched off by a move to a lower plan (6); pages start from the build plan's stage 4, after made tools' cards and buttons, not beside M1 (7).

| # | Question | Recommendation |
| --- | --- | --- |
| 1 | Is a page its own document (`page/v1`), or a part of a workflow? | **Its own.** A page draws from many tables and tools and runs nothing; tying it to one workflow would be wrong for most pages |
| 2 | Does a page read with the viewer's rights, or show what its maker chose? | **What its maker chose**, to those its `roles` allow, as a tool's answer does, with personal columns masked without `table.read` and publishing saying plainly what is shown (§8). Reading with the viewer's rights would make most pages empty for the staff they are made for |
| 3 | Does a tool source's run count against the run limits? | **Settled 19 November 2026, with made tools' question 9: yes**, as any run, kept for 60 seconds a viewer and shown as an estimate on the page's settings. Watch the first businesses before changing it |
| 4 | Pages for customers or linked businesses? | **No**, not in this plan |
| 5 | Board and chart: in the first release or later? | **Later** (G4). Figures, lists and buttons cover most needs; a board and a chart are the two costliest to draw well in the app |
| 6 | The limit on the lowest plan | **10 pages**, 12 blocks each |
| 7 | When is it built? | **After made tools:** G0 and G1 once M2 is under way, G2 and G3 after, G4 and G5 last ([the build plan](build-plan-saved-tools-payments.md) §4) |

## 17. Tests, when built

- **Go:** the validator's refusals; a tool that writes refused as a source; expressions limited to their four names; masking of personal columns with and without `table.read`; a page's roles; an action refused for someone outside the tool's roles; the cache kept and cleared; a dropped column marking the block; limits; another tenant's page, table or tool never reached (isolation test); migrations down and up.
- **The acceptance fixture:** a page declared to equal the hand-built deliveries page (today's deliveries from the record view, the amount owed to riders as a sum, **Confirm** as a signal on each row) is compared with that page in the browser tests and the app's picture tests. The hand-built page is not replaced; the fixture proves the blocks are enough.
- **Web end-to-end and app:** making a page from a table; each block at 360 and 390 px in both themes with axe; filters; a row's detail; an action's sheet through to its answer and the page read again; **Move up / Move down** by keyboard; the app's picture test for each block.
- **Chat:** the short form on WhatsApp; a reserved word refused as a page's name.
- **Plain words:** no "block", "source", "schema" or "query" on the owner's path: "part", "what it shows", "from".

## 18. As built (G0 and G1)

Built 22 November 2026: [decision 0072](../decisions/0072-pages.md) (proposed, for the founder to accept), [the contract](../contracts/page-v1.md) and `schemas/page-v1.schema.json`, `engine/pages` (problems in plain words; sources checked through `datatables.CheckQuery`; what each part reads; rows shaped and masked; **Show as a page**), migration 00530 (`pages`, `page_versions`, `tenant_limits.max_pages`), `page.edit` and `page.publish`, the API of §12 but for what is listed below (`api/pages.go`, in OpenAPI and the app's client), the [guide](../pages.md), the web's Pages area, page and editor, and the app's Pages and page.

- **Parts:** heading, figure (count; sum, over at most 5,000 rows; `change` by a date column for this day, week or month beside the one before; `lead` drawn on pine / the top card), list (grouped by a column, a state with a tone per word), table (up to six columns, ordered by a press on one on the web), and a row's card (`detail` of a list or table, up to twelve lines). Filters: search, choice, dates, Mine (an email column, matched to the viewer's email; a phone column is not matched yet). A list or table applies every filter that fits its table (the editor sets `uses`); figures stay whole (`uses` on a figure is refused at Parse, and ignored when drawing).
- **§8 as built:** a page is read with the business's connection; who may open it is its published `roles` (owners always; none, everyone) and anyone with `page.edit`; a personal column is masked for a viewer without `table.read` before an expression reads the row, and a search by such a viewer leaves personal columns out; making and changing a page needs `table.read` besides `page.edit`; publishing says, and audits, who may open it and what it shows.
- **Nothing narrows, adds up or ranks by a personal column for a viewer without `table.read`** (review of G0/G1): a dates or choice filter on a personal column, a sum of one and a `change` by one are refused at Check (as a group by one is), so they cannot be published, and a published page with one draws without that part; besides, `ReadFor` leaves such a filter out for that viewer and the view does not draw it. A table is not put in order by a personal column for them (400). We refuse sums rather than draw them masked: a masked total is no figure, and a total over a narrowed set is what leaked. More's cursor is Taskiem's own (`pages.Cursor`), opaque and taken back only with the order it was given for; a part whose own order is by a personal column goes on, for such a viewer, by a skip of the rows shown (its first 1,000 rows), never by the data API's cursor, which may carry the value a row was ranked by. A maker may still order a part by a personal column (a list of people by name): its viewers see the order, never the values.
- **Four-eyes:** where the business has four-eyes publishing (`publishNeedsApproval`, as a workflow's publish), another person with `page.publish` publishes a page: the person who last saved its draft (`draft_by`, or their API key) is refused (403), as is a caller that is no person. We refuse rather than route through the workflow's publish requests, which are a workflow version's; a page's approval queue would be its own piece of work.
- **Pins** are counted under a per-business advisory lock, and `pin_order` is unique among pinned pages; only a change of pin is audited. **Deleting** a page that was ever published needs `page.publish`; `page.edit` alone deletes a draft never published.
- **Counts:** a list's or table's count is exact (`count: "exact"`). The data API offers `estimated`, but what it estimates under filters is not documented, and a count beside the rows must match them; left exact until it is.
- **The draft** is kept on the page (one per page); each published version is a row of `page_versions`. A draft may be saved with problems; publishing refuses any.
- **Limits:** `max_pages` 10 (shop, shop plus, starter), 50 (growth), none (business, enterprise); not a downgrade blocker. Five pinned.
- **Where:** the web's menu has **Pages** and, for pinned pages, **Your pages**; a table's page has **Show as a page**. The app has Pages from Today's **Your pages**, the Taskiem chat's `/pages`, and `/pages` links; editing is on the web.

**Left from G1, and why:** Taskiem's own records as sources (`records`: orders, invoices, deliveries) and a page as a tab under an area wait for a record view per area, a piece of design of its own; the change notices (`api/chat_changes.go`) that update an open page, since pages are read on open and on pull to refresh until then; `versions` and bringing one back (versions are kept; there is no screen or call to list them yet); the app's table part ordered by a press (the web has it); export and import of a page with the workflow export; the SDK. The end-to-end test draws the staff member's page from a stand-in for `view` because the e2e server has no Xenibase; `api/pages_test.go` draws it for real against the fake.
