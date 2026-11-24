# Your pages

A **page** is a screen your business makes for itself: "Tickets" with how many are open and the open ones as a list, **New ticket** at its top and **Assign to me** on each row, "Expenses this month", "Generators". You pick the parts and what feeds them from your [data tables](data-tables.md), your orders, invoices, deliveries, customers and staff, or your [tools](tools.md) that only read; Taskiem lays them out for a phone first and draws them on the web and in the phone app. A page is not code and runs nothing ([decision 0072](decisions/0072-pages.md), [the plan](spec/pages.md), [the contract](contracts/page-v1.md)).

## The quickest start: Show as a page

On a table's page, **Show as a page** makes a page from it in one tap: a figure of how many rows there are, one of how many were added this week beside last week, the rows as a list (grouped by a status column when the table has one, each row opening as a card of its columns), and a search. It is a draft until you publish it.

## What a page is made of

| Part | Shows |
| --- | --- |
| **Heading** | A title and a line of words |
| **Figure** | One number: how many rows, or a column added up (an amount in kobo drawn as naira), with its colour; optionally only this day, week or month's, with the change on the one before ("up 12% on last week"). Up to four sit side by side; one may be the page's main figure, drawn large |
| **List** | Rows, each a title, a line under it and a state; may be put in groups by a column (Open, Doing, Done). A press opens the row as a card |
| **Table** | Rows in up to six columns you pick; a press on a column (in the app, **Order**) puts them in its order. A yes or no column you mark **can be ticked here** is a tick for those who may change the table's rows, on the web and in the app |
| **Buttons** | Up to three buttons for the whole page (**New ticket**) |
| **Status** | Where a run of one of your workflows is, in plain words: queued, working, waiting for whom, done, failed. The run the page's address names (`?run=`), else the latest. The run itself, its times and who it waits for show to those who may read runs. A list's or a table's rows can each show **where their own run is** too (below) |

Each figure, list and table reads one table (or your orders or invoices, below), with conditions every row it shows meets ("status is open") and an order. At the top of a page up to four **filters** narrow the parts that use them: a **search**, a **choice** from values you list, **dates** (today, this week, this month, or two days) and **Mine** (rows whose email or phone number column is the person's own: their phone is the number they sign in by). A list or table shows 25 rows, then **More**. A page has at most 12 parts.

## Your orders, invoices, deliveries, customers and staff

A part can show your **orders**, **invoices**, **deliveries**, **customers** or **staff** as Taskiem keeps them, read only: in **Add a part**, pick **Orders (Taskiem's)** and the like where you pick a table. Each has a fixed set of columns:

| | Columns |
| --- | --- |
| **Orders** | Number, Customer, Phone, Email, Status, Kind, Came from (shop, app or business), Total, Paid, Pickup or delivery, Delivery area, Delivery (its latest delivery), Placed, Paid on |
| **Invoices** | Number, Customer, Phone, Email, Status, Kind, Total, Paid, Owed, Due, Sent, Paid on |
| **Deliveries** | Order, Customer, Phone, Rider, Status, Delivery area, Rider's fee, Owed to the rider (delivered, not confirmed yet), Paid at the door, Offered, Delivered, Confirmed |
| **Customers** | Customer, Phone, Email, Invoices, Owed, Added (the customers of your book, under Money) |
| **Staff** | Name, Email, Roles, Joined |

They are counted, added up (Total, Paid, Owed, in naira), listed and narrowed as a table is: "orders received this week", "invoices overdue", the sum of what is owed. A row opens the order or invoice on its own screen.

- **Who sees them:** besides the page's roles, a person needs what that screen needs: orders and deliveries `storefront.manage` or `order.handle`, invoices and customers `invoice.read`, staff `member.manage`. Others see "You cannot see orders." where the part would be; the rest of the page shows.
- **The customer** (name, phone, email) is shown masked to everyone, as the Orders and Invoices lists show it (B•••, •••5678); to see a customer, open the order or invoice. So a page never searches, picks, groups, adds up or puts rows in order by the customer. A delivery's rider is shown by name, but never searched, picked or put in order by: it is kept sealed, and opened only for the rows shown.
- **A part over an area you turned off** (Settings, areas) cannot be published, and shows that it is turned off until you turn the area back on.

## Buttons

A button on a page (in a **Buttons** part) or on each row of a list or table (shown on the row and on its card) is one of three things:

- **A tool**: pressing it opens one of your [tools](tools.md) with its form filled in from the row (`"inputs": {"ref": "=row.id"}`), exactly as a button on a tool's answer does: it runs as the person who pressed, held to the tool's roles and its care (yes, or a passkey where it moves money); someone who may not use it sees it greyed with why. A value the person sees masked (a phone, for someone without `table.read`) is asked, never filled in. A **once** button is spent by the first run it starts.
- **A signal to a run**: tells a run of one of your workflows something it waits for (**Confirm** on a delivery), after **Yes**. Only to a run of the workflow the button names, waiting at a step that says a page may send it (`"pages": true` on the signal step), held to that workflow's roles; to no other run. A run that has moved on says so. Sending one needs the right to start runs (`run.start`).
- **An approval**: opens the approval sheet of a run waiting at that step, with its PIN and rules, when it waits for the person.

**When** hides a button for the rows it does not fit (`"when": "=row.status == 'delivered'"`; in the editor, **Show it only when**). Pressing a button reads the row again on the server: nothing the screen sends is taken for a row's value.

**Where each row's run is:** a list or a table can show, on each row and its card, where a run of one of your workflows is for that row (`"status": {"workflow": "Confirm deliveries", "to": "=row.id"}`): the run waiting for the row, or that last did (**Confirm** on a delivery waits for that delivery), or the run whose number a column holds (`"run": "=row.run"`). Its words (Waiting, Done, Not started…) show to everyone the page opens to; the run itself and who it waits for only to those who may read runs.

## Parts a tool works out

A figure or a list can be fed by one of your tools that **answers at once** (only reads): "Expenses this month" shows a total a workflow worked out. The part names the tool, its values (fixed words, or the page's filters: `"=filters.month"`) and the answer it shows (one of the tool's outputs: a number or words for a figure, a list for a list, its rows read as `item`). Its run is a run like any other, counted on your plan, as the person who opened the page; what it answered is kept for a minute (so opening the page again does not run it again) and shows when it was worked out ("Worked out 3 minutes ago"). **Refresh** works it out again; so does pressing any button on the page. A tool still running shows **Still working** and fills in when it answers. At most three parts a page are worked out by tools. A tool that changes anything, or that asks before it runs, cannot feed a page. **What a tool works out is for those who may use the tool:** someone the page opens to but the tool's roles do not (or who does not use tools) sees "Only managers can see this." in the part's place, here and in the chat. So a page cannot open to more people than its tools: publishing refuses a page whose roles are wider than a tool part's.

**Pin as a page:** a tool's list or card answer (in its sheet, on the web) has **Pin as a page**, which makes a draft page fed by that tool with the values it was given: a list keeps the answer's rows, words and buttons; a card becomes a figure for each of its first four lines that shows one of the tool's answers. The page opens to the tool's roles, and a value that is personal (a phone, an email, an account, a saved person or place) is not kept on it; a value is kept as the very words given, never read as an expression.

## A page as an alert, and as a tab

- **An alert:** a figure that counts (a table's or a view's rows), marked **alert**, shows in the alerts at the end of your Taskiem chat while it is above zero: the page's name and the count, with **Open the page**. Only to those who may open the page.
- **A tab under an area** (`"place": {"area": "orders"}`): the published page shows as a tab after Taskiem's own on that area's screens (Online shop and Orders, Money, Schools on the web; a **Your pages** section on the app's Money and Orders), while that area is on. **Linked businesses** has tabs too (Linked businesses, Your business in Explore, then your pages placed there); in the app they are chips at its top.
- **From a notification:** a Tell me message (`notify.tell`) can carry a button that opens a page with its filters set (`{"label": "See db-2", "page": "disk-alerts", "filters": {"host": "db-2"}}`). It shows, on WhatsApp and Telegram, only to people the page's roles let open it; pressed, it answers with the page's short form with those filters and its link, which opens the page with them in the app and on the web. Anyone else pressing it is told they cannot open the page.

## In the chat

- **In your Taskiem chat** (web and app), a pinned page is a tile in the tools panel, under **Your pages**; "/" and a word finds any page you may open, and "/" and a page's name (`/tickets`) opens it over the chat, with its buttons.
- **On WhatsApp, Telegram and Slack** a page cannot be drawn: its name (`tickets`, or `/tickets`) answers with its **short form**: its figures on a line ("Open 7 · Waiting 2"), the first five rows of its first list, how many more, and **Open the page** with its link. Taskiem's own words always win; a page's name is never one of them nor a tool's. Only for a published page whose roles let you open it, masked as on the page; anyone else is answered as if there were none. Its buttons are reached as tools there, by their own words.

## Kept up to date

An open page keeps itself up to date: when a row of a table it shows is changed in Taskiem (on the web, in the app, in the chat, by a workflow), or an order or invoice changes, the parts that show it are read again within a few seconds, on every screen it is open on, without a reload. A list keeps the order you put it in. Pulling down (or **Refresh**) still reads the whole page. Rows written to a table from outside Taskiem show on the next pull. A page published again is read again whole; pinning it or saving a draft does not disturb anyone who has it open. Someone who loses the right to open the page, or to see orders or invoices, stops being told within half a minute. A person may keep five pages up to date at once (a business, forty); past that, pull to refresh.

## Who sees what

- **Who may open it** is the page's roles: owners always; none chosen, everyone in the business. Publishing says so in plain words, with what it shows: "Support will be able to open Tickets. It shows: Title, Customer, Status."
- **A page shows what its maker chose** to those allowed to open it. Like a tool's answer, it reads with the business's connection, not with the rights of whoever opens it: a page over salaries shows them to everyone who may open it. Choose who may open it with that in mind.
- **Personal columns** (phones, emails, and any column marked personal) are masked (•••5678) for someone without `table.read`, and a search by them never looks in those columns, nor can they put a table in order by one. A page never adds up, compares by, groups by, or offers a choice or dates filter on a personal column: the editor does not offer it and publishing refuses it, since narrowing by one a day at a time would show what is masked. Figures count every row their own conditions pick; the filters at the top narrow lists and tables only.
- **Making and changing pages** needs `page.edit` and `table.read`; **publishing** needs `page.publish`, and so does deleting a page that was ever published (`page.edit` alone deletes a draft never published). Owners and admins have both; give them to other roles in the role editor.
- **Four-eyes:** where the business has four-eyes publishing (Settings, governance), another person with `page.publish` publishes a page: the person who last saved its draft cannot.

## Making and changing one

On the web, **Pages** lists your pages, pinned first. **New page** starts from a table, from your orders, invoices, deliveries, customers or staff, or from nothing. The editor has no technical words: **Add a part** (a count, a total, a list, a table, a heading, a figure or a list a tool works out, **Buttons**, **Where a run is**), pick its table, what each row shows (a row's title can be two columns, "AC-12 · Musa"), the groups and the conditions, **Buttons on each row** (opens a tool's form with each value from the row, a filter, words or asked; tells a run something; opens a run's approval; and **Show it only when**), **Where each row's run is**, a count as **an alert**, a yes/no column that **can be ticked here**, **Also a tab under** one of Taskiem's areas, **Move up** and **Move down**, and a live preview at phone width. **About the page** also says about how many runs its tools start a day ("About 40 runs a day from this page: 20 views a day, 2 tools each") beside your plan's limit, and warns when that is more. Changes are kept as a **draft** (Save draft); people keep seeing the published page until you **Publish**. Up to five pages can be **pinned**, shown in the menu under **Your pages**.

**Earlier versions**, in the editor, lists each time the page was published: who published it, when, and what changed ("Added Owed. Now opened by owners and support."). **Bring back** makes a version your draft, in place of your changes not published yet: people keep seeing the published page until you publish it, and publishing it is checked as any change is (a column gone since is said, and with four-eyes publishing another person publishes it).

**Save as a file** keeps the page as a file (`tickets.page.json`) of what it shows and how, never your rows: a condition naming one person's phone or email is left out, and the editor says so. On **Pages**, **Make from a file** makes a page from one, as a draft: if the name is taken it gets the next free one (tickets-2), and Taskiem says which tables or columns it names that your business does not have, so you can make them or change the page before publishing it.

In the phone app, **Pages** lists the pages you may open and opens each one: figures, lists, tables and cards, with its filters and pull to refresh. A table has **Order** at its head to put its rows in one of its columns' order (picked again, the other way round); a personal column is not offered to someone who may not read the table. Making and changing pages is on the web.

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
| `POST /v1/pages` | Make a page as a draft: `{"document": …}`, `{"from_table": "<table id>"}` or `{"from_tool": {"command", "inputs"}}` (Pin as a page) |
| `GET /v1/pages/{name}` | A page; its draft, the draft's problems and what it shows, for `page.edit` holders |
| `PUT /v1/pages/{name}/draft` | Save the draft |
| `POST /v1/pages/{name}/publish` | Publish the draft, once it has no problem left |
| `PUT /v1/pages/{name}/pin` | `{"pinned": true}` |
| `DELETE /v1/pages/{name}` | Delete the page and its versions (a published one needs `page.publish`) |
| `POST /v1/pages/validate` | A document's problems, nothing saved |
| `GET /v1/pages/{name}/view?filters=` | The page as drawn for you: every part's figures and rows worked out on the server, masked for you (`draft=1` for the draft) |
| `GET /v1/pages/{name}/blocks/{id}?cursor=` | More rows of a list or table, or a table in a column's order (`order`, `desc=1`) |
| `GET /v1/pages/{name}/live` | Server-sent events while the page is open: `changed` names the parts to read again (no data), at most once in two seconds |
| `POST /v1/pages/{name}/act` | A button pressed: `{"block", "row", "action", "yes"}`; gives the tool to open with its values (start it with the `button`), sends a signal after `yes`, or gives the approval to open |
| `POST /v1/pages/{name}/refresh` | What tools worked out on the page for you is let go, so it is worked out again |
| `GET /v1/page-choices` | Your tools (whether each only reads, its answers and values) and workflows (the signals a page may send them, their approvals) by name, for the editor (`page.edit`) |
| `GET /v1/page-records` | Your orders, invoices, deliveries, customers and staff as a page may show them: their columns, whether their area is on and whether you may see them (`page.edit`) |
| `GET /v1/pages/{name}/versions` | Every published version, newest first: who, when, who it let open the page, what changed (`page.edit`) |
| `POST /v1/pages/{name}/versions/{v}/bring-back` | That version becomes the draft (never published by this) |
| `GET /v1/pages/{name}/export` | The page as a file: its document, no rows, nothing personal (`draft=1` for the draft) |
| `POST /v1/pages/import` | `{"document": …}` read from a file, made a new page's draft; `missing` says what the business lacks |

The web and the app both draw from `view`: neither reads a table for a page itself. Reads are recorded in the connection reads (`GET /v1/secrets/reads`, purpose `pages.view`). Making, saving, publishing, pinning and deleting are audited (`page.create`, `page.draft`, `page.publish` with who may open it and what it shows, `page.pin` when it changes, `page.delete`, `page.bring_back`, `page.export`, `page.import`), never with a row's values.

In code, `@taskiem/sdk` (`sdk/src/page.ts`) writes a page: `page({ name, title }, figure(…), list(…, { actions: [toolAction(…), signalAction(…)] }))`, its values words, CEL or arrow functions over `row`, `item`, `outputs` and `filters`.

## Not yet

Boards and charts, starters and **Describe it** come in later phases ([the plan](spec/pages.md#14-phases)).
