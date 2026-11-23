# 0072 — Pages: a business's own screens, as their own document

Date: 2026-11-22 · Status: **Accepted** (23 November 2026; the plan's choices were settled 19 November 2026; G0 and G1 built) · Plan: [Pages](../spec/pages.md) · Build plan: [G0, G1](../spec/build-plan-saved-tools-payments.md#stage-6-cards-the-reader-and-the-first-pages)

## Context

Taskiem has screens for what it built: orders, stock, money, staff. A business also keeps tables of its own (tickets, expenses, leave, generators) and has no screen for them but the table itself, which is the record, not a view chosen for a job. Taskiem cannot build a screen for every business's tables. The [pages plan](../spec/pages.md) lets the business make one from a small set of parts Taskiem draws, fed by what it already has. Its seven questions were settled with the founder on 19 November 2026 ([§16](../spec/pages.md#16-for-the-founder-to-decide)). This record writes down what the code depends on.

## Decision

1. **A page is its own document, `page/v1`**, not part of `wd/v1` (§16, 1): a page draws from many tables (and later tools) and runs nothing, so tying it to one workflow would be wrong for most pages. Its schema is `schemas/page-v1.schema.json`, its contract [page/v1](../contracts/page-v1.md), its validator `engine/pages`. A page has a short `name` (its address and, later, `/name` in the chat), a `title`, an `icon`, `roles`, `filters` and `blocks`.
2. **The parts are Taskiem's and fixed.** G1 draws a heading, a figure (a count, or a column added up, optionally this day, week or month's beside the one before), a list (grouped by a column too), a table, and a row's detail as a card; the founder put grouped lists and figures with a change in the first release, and the board and the chart later (§16, 5). The list's row and the detail are the same shapes as a made tool's `list` and `card` answers (`item`, `state`, `lines`), named once (`shapes/v1`). There is no free placing, no code, no style of a business's own.
3. **A page shows what its maker chose, to those its roles let open it** (§16, 2). It reads with the business's connection, as a made tool's answer does, not with the rights of whoever opens it: reading with the viewer's rights would make most pages empty for the staff they are made for. In exchange:
   - **who may open it** is the published version's `roles`: owners always; none named, everyone in the business; an API key acts as its owner (the rule of decision 0070's `roles`);
   - **a personal column is masked** (a phone to its last four digits) unless the viewer also holds `table.read`; the mask is applied before any expression sees the row, and a search by such a viewer never reads a personal column;
   - **publishing says what it shows** ("Tickets: Title, Customer, Status") and to whom, and is audited with both;
   - **making or changing a page needs `table.read`** besides `page.edit`: its maker chooses what of the tables others will see.
4. **Everything is worked out on the server.** `GET /v1/pages/{name}/view` returns each part with its figures and rows already read, filtered and masked for the viewer; the web and the app draw from it and never read a table for a page themselves, so the rules of 3 are held in one place. Reads go through the checked query of decision 0060 (names, types and operators checked against the table at publish and at each view); a total is added up over at most 5,000 rows and says when it stopped there. Filters (a search, a choice, dates, Mine) are values passed to a condition, never to a model.
5. **Draft and published, like a workflow.** A page is changed as one draft and shown to others once published; every published version is kept (`page_versions`). Publishing refuses a page with any problem; a part whose table or column has gone since shows "This part needs fixing" to its editors and is left out for everyone else.
6. **Permissions:** `page.edit` (make, change, pin, delete) and `page.publish`, held by owners and admins and given to other roles in the role editor. Opening a page needs neither.
7. **Where a page shows:** a Pages area on the web and in the app; up to **five pinned** to the navigation, under **Your pages**, apart from Taskiem's own areas (the founder chose to allow pinning, over pull request #211's none). A page's name is never one of Taskiem's own command words in any language that is on, nor a tool's command, nor another page's.
8. **Plan limit `max_pages`**: 10 on the lowest plans (shop, shop plus, starter), 50 on the middle (growth), none above; drafts count; a business over it after moving to a lower plan keeps its pages and cannot add more (§16, 6). It is not a downgrade blocker.

## What G0 and G1 build

- `schemas/page-v1.schema.json`, `engine/pages` (parse and check with problems in plain words that never say "block", "schema" or "query"; what to read for each part; rows shaped and masked; Show as a page), the [contract](../contracts/page-v1.md).
- `pages` and `page_versions` under forced row-level security, and `tenant_limits.max_pages` (migration 00530).
- The API (`/v1/pages`: list, make from a document or a table, get, save the draft, publish, pin, delete, validate, view, a part's next rows or another order), in OpenAPI, the app's client and the [guide](../pages.md).
- The web's Pages area, the page, the editor (no technical words) and **Show as a page** on a table; the app's Pages list and page. Editing stays on the web.

## Left to later phases

The G1 items not in the first build followed on 23 November 2026 ([§18](../spec/pages.md#18-as-built-g0-and-g1)): the versions list with bring-back, a page as a file, the app's ordering of a table, Taskiem's orders, invoices, deliveries, customers and staff as record views, the change notices that update an open page, a page as a tab under an area, and the SDK. G2 (actions: tools, signals a workflow declares for pages, approvals; tools as sources; the status block; alerts; changing a row in place; **Pin as a page**) and G3 (the tile, `/name`, the short form on WhatsApp, Telegram and Slack) were built the same day ([§18a](../spec/pages.md#18a-as-built-g2-and-g3)): a page still runs nothing itself; a button is the tool, the signal or the approval, held to their own rules as the person who pressed. Left: making G2's parts in the editor, a page opened from a notification; board and chart (G4); starters and **Describe it** (G5).

## Alternatives considered

- **A page as a `views` block inside a workflow** (pull request #211). One page reads many tables and runs nothing; the parts that pull request had right were taken (§15a). Rejected for the structure.
- **Reading with the viewer's rights.** Most pages would be empty for the staff they are for, and a business would hand out `table.read` to make them work, which shows every table. Rejected; the masking and the publish statement of 3 are the price.
- **Drawing on the client from the table's rows.** Would need every viewer to hold `table.read` and would put the masking in two apps. Rejected for 4.
- **Free layout or a business's own styles.** Out of reach of an owner on a phone and of two renderers. Rejected (§15).
