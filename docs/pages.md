# Your pages

A **page** is a screen your business makes for itself: "Tickets" with how many are open and the open ones as a list, "Expenses this month", "Generators". You pick the parts and what feeds them from your [data tables](data-tables.md); Taskiem lays them out for a phone first and draws them on the web and in the phone app. A page is not code and runs nothing ([decision 0072](decisions/0072-pages.md), [the plan](spec/pages.md), [the contract](contracts/page-v1.md)).

## The quickest start: Show as a page

On a table's page, **Show as a page** makes a page from it in one tap: a figure of how many rows there are, one of how many were added this week beside last week, the rows as a list (grouped by a status column when the table has one, each row opening as a card of its columns), and a search. It is a draft until you publish it.

## What a page is made of

| Part | Shows |
| --- | --- |
| **Heading** | A title and a line of words |
| **Figure** | One number: how many rows, or a column added up (an amount in kobo drawn as naira), with its colour; optionally only this day, week or month's, with the change on the one before ("up 12% on last week"). Up to four sit side by side; one may be the page's main figure, drawn large |
| **List** | Rows, each a title, a line under it and a state; may be put in groups by a column (Open, Doing, Done). A press opens the row as a card |
| **Table** | Rows in up to six columns you pick; a press on a column puts them in its order |

Each figure, list and table reads one table, with conditions every row it shows meets ("status is open") and an order. At the top of a page up to four **filters** narrow the parts that use them: a **search**, a **choice** from values you list, **dates** (today, this week, this month, or two days) and **Mine** (rows whose email column is the person's own). A list or table shows 25 rows, then **More**. A page has at most 12 parts.

## Who sees what

- **Who may open it** is the page's roles: owners always; none chosen, everyone in the business. Publishing says so in plain words, with what it shows: "Support will be able to open Tickets. It shows: Title, Customer, Status."
- **A page shows what its maker chose** to those allowed to open it. Like a tool's answer, it reads with the business's connection, not with the rights of whoever opens it: a page over salaries shows them to everyone who may open it. Choose who may open it with that in mind.
- **Personal columns** (phones, emails, and any column marked personal) are masked (•••5678) for someone without `table.read`, and a search by them never looks in those columns, nor can they put a table in order by one. A page never adds up, compares by, groups by, or offers a choice or dates filter on a personal column: the editor does not offer it and publishing refuses it, since narrowing by one a day at a time would show what is masked. Figures count every row their own conditions pick; the filters at the top narrow lists and tables only.
- **Making and changing pages** needs `page.edit` and `table.read`; **publishing** needs `page.publish`, and so does deleting a page that was ever published (`page.edit` alone deletes a draft never published). Owners and admins have both; give them to other roles in the role editor.
- **Four-eyes:** where the business has four-eyes publishing (Settings, governance), another person with `page.publish` publishes a page: the person who last saved its draft cannot.

## Making and changing one

On the web, **Pages** lists your pages, pinned first. **New page** starts from a table; the editor has no technical words: **Add a part** (a count, a total, a list, a table, a heading), pick its table, what each row shows, the groups and the conditions, **Move up** and **Move down**, and a live preview at phone width. Changes are kept as a **draft** (Save draft); people keep seeing the published page until you **Publish**. Up to five pages can be **pinned**, shown in the menu under **Your pages**.

In the phone app, **Pages** lists the pages you may open and opens each one: figures, lists, tables and cards, with its filters and pull to refresh. Making and changing pages is on the web.

If a table or a column a page uses is gone, that part shows "This part needs fixing" to those who may edit the page and is left out for everyone else.

## Limits

| Limit | |
| --- | --- |
| Pages a business makes, drafts counted (`max_pages`) | 10 on the lowest plans, 50 on Growing Business, no limit above. A business over it after moving to a lower plan keeps its pages and cannot add more |
| Pinned pages | 5 |
| Parts on a page | 12; 4 figures side by side; 6 columns in a table; 12 lines on a row's card |
| Rows a list or table shows | 25, then **More** |
| A total | added up over the first 5,000 rows, and says so when it stopped there |

## The API

| Call | What |
| --- | --- |
| `GET /v1/pages` | The pages you may open, pinned first |
| `POST /v1/pages` | Make a page as a draft: `{"document": …}` or `{"from_table": "<table id>"}` |
| `GET /v1/pages/{name}` | A page; its draft, the draft's problems and what it shows, for `page.edit` holders |
| `PUT /v1/pages/{name}/draft` | Save the draft |
| `POST /v1/pages/{name}/publish` | Publish the draft, once it has no problem left |
| `PUT /v1/pages/{name}/pin` | `{"pinned": true}` |
| `DELETE /v1/pages/{name}` | Delete the page and its versions (a published one needs `page.publish`) |
| `POST /v1/pages/validate` | A document's problems, nothing saved |
| `GET /v1/pages/{name}/view?filters=` | The page as drawn for you: every part's figures and rows worked out on the server, masked for you (`draft=1` for the draft) |
| `GET /v1/pages/{name}/blocks/{id}?cursor=` | More rows of a list or table, or a table in a column's order (`order`, `desc=1`) |

The web and the app both draw from `view`: neither reads a table for a page itself. Reads are recorded in the connection reads (`GET /v1/secrets/reads`, purpose `pages.view`). Making, saving, publishing, pinning and deleting are audited (`page.create`, `page.draft`, `page.publish` with who may open it and what it shows, `page.pin` when it changes, `page.delete`), never with a row's values.

## Not yet

Buttons and actions on a page and its rows, tools feeding a page, a page as a tab under an area or as an alert, pages updating themselves when a row changes, pages in the chat (`/tickets`, the short form on WhatsApp), boards and charts come in later phases ([the plan](spec/pages.md#14-phases)).
