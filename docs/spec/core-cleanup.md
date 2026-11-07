# Business records out of the core: the engine keeps the engine, the business keeps its records

Status: **Plan** (proposed 7 November 2026) · Decision: [0064](../decisions/0064-business-records-out-of-the-core.md) (proposed) · Builds on: [data tables](../data-tables.md) ([0033](../decisions/0033-data-tables-on-xenibase.md)), [kept tables](../data-tables.md#kept-tables) ([0045](../decisions/0045-tally-in-taskiem.md) point 6), [known tables](no-sheet-needed.md#54-known-tables) ([0060](../decisions/0060-no-sheet-needed.md)), the definer pattern ([0061](../decisions/0061-business-links.md)), [social posting](social-posting.md) (the first plan written to this rule) · Rules: [CONTRIBUTING: migrations](../../CONTRIBUTING.md#migrations)

## 1. Summary

Taskiem's core schema holds 329 tables in 260 forward-only migrations. About 130 are the engine and the platform: tenancy, runs, steps, signals, connections, approvals, audit, billing, operators, the channel plumbing, the catalogue, data-table definitions. The other 190 or so, about 60% of the schema and most of the Go code since October, are business records: students and fees, orders and bookings, bills and price lists, stock movements, deliveries, invoices, form submissions, the money log. Each was put there for a stated reason, always the same one: a record with rules, which a workflow cannot be, and which a data table the owner can edit cannot keep safely.

That reason is real. The cost is also real. Every such table is a module of the monolith with its own API, chat commands and pages against core rows; the business cannot see, query or export the record except through what that module exposes; every table is a permanent commitment on every installation; and where the business did need the record in its own tables, the engine writes a **kept copy** beside the core truth, so the same fact is stored twice (`money_log`, `money_matches`, `school_students`, `school_classes`, `school_term_fees`, `stock_movements`). The social posting plan was the first written the other way: one table, the business's own, nothing in the core. This plan makes that the rule and works the existing features back to it.

**The rule.** A table a business could have made itself is the business's own, in its data project. A record with rules is a **kept table** in the business's project: written only by Taskiem, through one writer, exactly once; read, queried and exported by the business like any table. The core keeps only what the engine needs to run: references, workflow state, coordination that must be transactional with runs (holds, counters, leases, outboxes), and the platform's own records. Nothing else.

**The one thing to decide first (§5).** A kept table in Xenibase cannot be written in the same transaction as the engine's state, is a minute behind, and is subject to the project's rate limits and pauses. For lists that is fine. For ledgers and orders it is the reason they are in the core today. So the plan asks for one decision before any move: whether Taskiem may **host** a data table itself, rows in a per-organisation schema in Taskiem's own Postgres under the same Tables API, so that a kept record can be transactional with the engine and sealed with the tenant's key, while staying the business's own, listed, exported and readable by every workflow. With it, every record below has a home with the properties its decision asked for. Without it, ledgers stay in the core and only lists move, which is a third of the gain.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Core | Taskiem's own Postgres schema, `engine/db/migrations`, run on every installation, under row-level security and the audit chain |
| Data table | A table the business owns in its data project, listed on the Tables page, read and written by workflows, exported ([data tables](../data-tables.md)) |
| Kept table | A data table only Taskiem writes; the business reads, queries and exports it; its definition cannot be changed by hand ([kept tables](../data-tables.md#kept-tables)) |
| Hosted table | Proposed (§5): a data table whose rows are in Taskiem's own Postgres, in the organisation's schema, behind the same Tables API as a Xenibase one |
| List | Rows a person would keep in a sheet: students, suppliers, riders, price lists, a gallery. Edited by people and workflows |
| Record with rules | Rows that must be true: a ledger entry never edited, an order whose status moves one way, a receipt numbered once, a payment matched exactly once. Written only by Taskiem |
| Engine state | What the engine needs transactionally with runs: a hold on a slot with an expiry, a counter, a lease, an outbox row, a run's reference to a record. Stays in the core |
| Twin | A core table and a kept copy of the same fact, as the money log is today |
| Module | A feature's Go packages, API routes, chat commands and web pages (`engine/school`, `api/school*.go`, the Schools pages) |

## 3. Where we are

The core schema's tables, sorted by what they hold (counts from `engine/db/migrations`, 7 November 2026):

| Area | Core tables | Lists | Records with rules | Engine state | Twin today |
| --- | --- | --- | --- | --- | --- |
| Schools | 41 | students, classes, families, guardians, terms, sessions, enrolments, fee items, discounts, options | term invoices, receipts, receipt counters, student accounts, reminders, promises, clearance overrides, daily closes | account jobs, pending postings, invoicing runs | students, classes, term fees |
| Business links | 36 | price lists, items, supplier prices, trade items, settings, areas | links, offers, bills, bill payments, link orders, conversations, messages, reactions, stars, credit terms, notices | mirrors due, orders due, starts, typing, chat sent, blind requests | none |
| Storefronts and fronts | 17 | catalogue mapping, services, blocks, fronts, domains, reviews | orders, order payments, tickets, bookings | holds, snapshots, inbox, actions, changes, media | none |
| Money log and matching | 16 | fee rates, statement formats, payers, customers | entries, matches, suggestions, leaks, reversals, lookbacks, statement waiting, digests | match state | money log, matches |
| Stock | 9 | suppliers, settings, counts, low-stock | movements, sales, purchase orders, purchase payments | none | movements |
| Dispatch | 7 | riders, settings | deliveries, payouts, remittances, remittance parts | presence | none |
| Invoices and debtor book | 9 | invoice customers, customer links | invoices, payments, amendments, accounts, requests | paid starts, payment starts | none |
| Forms | 9 | endpoints, links | submissions, files, uploads | codes, once, requests, inbox, file deletions | none |
| Profile, agents, reach | 11 | profile, gallery, reach contacts, pages, channels | agent sessions, verifications | reach codes, targets, request links | none |
| Everything else | ~130 | | | the engine and the platform | |

The reasons given, in the decisions, for the core:

- **A record with rules, which a workflow cannot be** (0045): steps of a workflow do not commit together; a data table can be edited by anyone with `table.edit`; a record built only from workflow writes is as good as the least careful workflow or person.
- **Sealed personal data** (0036, amended): orders hold a customer's name and phone sealed with the tenant key, a status link's key, payments and held slots, which a data table the owner can edit cannot keep safely.
- **Transactions with the engine**: a booking hold with an expiry, a receipt number, a match that is undone without losing history, a payout paired with a credit, all need one commit with the run or the webhook that caused them.
- **Availability**: a page view or a webhook must not depend on an external project's rate limit or a paused project (0036 rejected reading the catalogue live from Xenibase).

Kept tables (0045 point 6) answered the first reason: only Taskiem writes them. They were then used as **copies**, not as the record, because of the other three. This plan answers the other three.

## 4. What stays as it is

- **The engine and the platform**, every table of it. Tenancy, runs, steps, signals, timers, connections, secrets, approvals, policies, audit, anchors, billing, plans, limits, operators, the catalogue, worker pools, SSO, SCIM, passkeys, status page, data-table definitions.
- **The channel plumbing**: WhatsApp, Telegram, Slack and USSD bindings, inboxes, outboxes, templates, OTPs, notices, chat sessions and history. They are the platform's, however many tables they take; a message is not a business record.
- **Every API route, chat command and page.** The cleanup changes where rows live, not what people see. `GET /v1/invoices` keeps answering; it reads the business's table instead of a core one.
- **Decision 0033**: Xenibase stays the home of ordinary data tables and of a business's own project. Hosted tables (§5) are an addition behind the same API, not a replacement.
- **Migrations are forward-only.** A core table leaves by a migration that stops writing it, a copy to the new home, a switch of readers, and a later migration that drops it, never by editing an old migration.

## 5. The decision to take first: hosted tables

Today a data table's rows live only in the organisation's Xenibase project. Proposed: a second backend for the same thing, chosen per table by Taskiem and invisible to the business.

| | Xenibase table (today) | Hosted table (proposed) |
| --- | --- | --- |
| Rows live in | The organisation's Xenibase project | A per-organisation schema in Taskiem's Postgres (`org_<id>.<name>`), under row-level security, in the engine's backups |
| Same API | `GET/POST /v1/tables`, rows, search, export, the Tables page, `read_rows` and `query_rows` steps, chat | Identical: `data_tables` resolves to the backend per table |
| Transactions with the engine | No (a minute behind, outbox needed) | Yes: one commit with the run, the webhook or the definer |
| Sealed columns | No: `personal` columns are plain in the project | Yes: `personal` columns sealed with the tenant key as core sealed columns are today, masked on read as today |
| Availability | The project's limits and pauses | The engine's |
| Who writes a kept one | Taskiem, async, through the Xenibase API | Taskiem, in the transaction |
| Bring-your-own project | The business's own Xenibase; it may edit rows there | Not applicable: hosted rows are Taskiem's to keep; the business reads and exports |
| Export and portability | Already the business's | `GET /v1/tables/{name}/export` as today; a business leaving takes a dump of its schema |

Rule for choosing: a **list** goes to the business's Xenibase project (or wherever its data project is), as today. A **record with rules** is a **kept hosted table**: the business's own in every way that matters (listed, queried by workflows, searched, exported, deleted with the business), and Taskiem's to write, transactionally. Nothing else is needed for the moves below.

What it costs: one backend behind `engine/datatables` (schema per organisation, the same column types, RLS policies, the sealing of `personal` columns, export), about the size of the Xenibase backend; the Tables page showing where a table lives; one migration making the schemas. What it buys: every record below has a home outside the core with the properties its decision asked for, and no more twins.

If the decision is **no**, the plan still moves the lists (H2, parts of H3 to H8) and stops the growth (H0, H1); the records stay in the core, and this document says which.

## 6. The method, for every move

Each area is moved the same way, so the first teaches the rest:

1. **Classify** each table: list, record with rules, engine state, platform. Lists and records leave; engine state stays and is renamed into a module schema (`school.account_jobs`, not `public.school_account_jobs`), so the core's public schema is the engine's alone.
2. **Define** the destination tables as known tables in `templates/tables/` (lists) or kept definitions in the module (records), with `personal` columns marked. The names already taken by kept copies keep their names: the copy becomes the record.
3. **Write to both** for one release: the module writes the new home in the transaction (hosted) or through the outbox (Xenibase) and keeps writing the core table. A reconciler compares them nightly and reports differences as a platform alert; none for a week is the gate.
4. **Switch readers**: the API, chat, pages and workflow steps read the new home. The twin's sweeper stops. The core table becomes write-only.
5. **Stop writing** the core table; a migration drops it a release later, after the next backup rotation.
6. **Erasure and retention** follow the record: a person's erasure (`money_log_erasures`, `form_file_deletions` are the shapes) runs against the new home; retention is the business's `max_retention_days` as for every table.
7. **Tests**: the area's workflow tests and browser tests unchanged (the API is the contract), plus the reconciler's week.

A CI check from H0 holds the line meanwhile.

## 7. Milestones

### H0: the rule, the allow-list and the check (S)

- CONTRIBUTING gains the rule: a table a business could have made itself is the business's own; a record with rules is a kept table; only engine state and the platform's own records go in the core, each in its module's schema.
- `engine/db/core_tables.txt`: the allow-list of the core's tables, as `dispatch_columns.txt` lists columns today. A migration that creates a table not on the list fails CI unless the migration's header names the decision that allows it (`-- decision: 0064`). The list starts as every table that exists, so nothing breaks, and shrinks with each milestone.
- Decision 0064 accepted or amended with §5's answer.

**Done when:** CI refuses a new unlisted table; the social posting plan builds under the rule with nothing to add to the list.

### H1: hosted tables (L; only if §5 is yes)

The second backend behind `engine/datatables`: per-organisation schemas, RLS, the column types, `personal` sealing, search, export, the rows API, `read_rows` and `query_rows`, `EnsureKept` on it, the Tables page saying where each table lives, a limit on hosted rows per plan beside `max_data_tables`.

**Done when:** a hosted table behaves as a Xenibase one through every API and step in the data-tables test suite; a `personal` column is sealed at rest and masked as today; a kept hosted table is written inside an engine transaction that rolls back together.

### H2: lists that are plainly the business's (M)

Business profile and gallery, reach contacts, riders, suppliers, price lists and supplier prices, school families and guardians, storefront reviews and blocks. Each becomes a known table in the business's project; the module reads and writes it through `datatables`; chat and pages unchanged.

**Done when:** the Tables page lists them, a workflow reads a rider or a supplier with `read_rows`, exports work, and the core tables are dropped.

### H3: invoices and the debtor book (M; needs H1 for the records)

`customer_invoices`, payments, amendments and accounts become kept hosted tables (`invoices`, `invoice_payments`, …); `invoice_customers` a list. Paid and payment starts stay as engine state under `invoices.*`.

**Done when:** every invoice route and chat command answers from the new tables; the matching tests pass; a customer's erasure clears the new home.

### H4: the money log, matching and customer accounts (M; needs H1)

The kept copy `money_log` becomes the record: `money_entries` and the matching tables are written as kept hosted tables in the same transaction as the rail's webhook; the sweeper and the twin go. Match state stays as engine state.

**Done when:** no row of the money log exists twice; a webhook's entry and its match commit together or not at all; the leak and digest reports read the new home; the money-log erasure path runs against it.

### H5: stock (M; needs H1)

`stock_movements` (the kept copy) becomes the record; sales, purchase orders, payments, counts and low-stock become kept hosted tables; suppliers and settings lists (H2).

**Done when:** the stock chat commands and the kept-copy sweeper tests pass without the twin.

### H6: storefront orders, bookings and tickets (L; needs H1)

Orders, order payments, bookings and tickets become kept hosted tables with their customer columns sealed; holds, snapshots, inbox, actions and changes stay as engine state under `storefront.*`; catalogue and services are already the business's tables.

**Done when:** a storefront order placed on the public host, paid and delivered reads from the new tables at every step; the status link works; the browser tests pass.

### H7: schools (L; needs H1)

The largest area: students, classes and term fees are already kept copies and become the record; term invoices, receipts, student accounts, reminders, promises, clearance and daily closes become kept hosted tables; families and guardians lists (H2); account jobs, pending postings and invoicing runs stay as engine state under `school.*`.

**Done when:** the schools walkthrough and the demo school seed run against the new tables; the receipt counter is still gapless; no twin remains.

### H8: business links and dispatch (L; needs H1, H3, H6)

Bills become the buyer's kept table and link orders the seller's; conversations and messages kept; price lists are H2; the mirror queue, orders due and definers stay as engine state under `links.*`. Deliveries, payouts and remittances kept; presence engine state.

**Done when:** the business-links suite passes with the mirrors in kept tables; the reconciler compares digests across the new homes; the dispatch flows read from the new tables.

### H9: the core alone (S)

The allow-list is the engine and the platform only; every module's engine state is in its own schema; `engine/db/migrations` carries a note at the top naming the rule and the list.

**Done when:** a new table in `public` needs a decision, and the list has no business record on it.

## 8. What each move must keep

| Property | How it is kept |
| --- | --- |
| Rules on records | Only Taskiem's writer writes a kept table (as today); hosted ones in the engine's transaction; a rule the writer enforces is a function in the module, with the same tests |
| Sealed personal data | Hosted `personal` columns sealed with the tenant key; Xenibase ones stay unsealed and so take no sealed record |
| Exactly once | Hosted: the transaction. Xenibase: the outbox and `Idempotency-Key`, as the mirrors and kept copies do today |
| Audit | Every write to a kept record is audited in the business's chain as the module's writes are today; nothing changes because the table moved |
| Row-level security | Hosted schemas carry the same policies as the core; a session never sees another organisation's schema |
| Erasure and retention | Follow the record (§6 point 6) |
| Export | Every moved table exports as every table does; the business owns more than before |
| Limits | Hosted rows count against a plan limit; kept records are not held back by it (a receipt is written whatever the plan), only lists are |
| Backups and the VM kit | Hosted schemas are in the engine's database, so the VM's Postgres backup and the recovery sheet cover them with no change |

## 9. Risks and open questions

| Risk | What we do |
| --- | --- |
| §5 is answered no | Lists still move (H2 and the list parts of H3 to H8); records stay; the allow-list still stops growth. Say so in 0064 |
| A move loses rows | Double-write and the nightly reconciler for a week before readers switch; the core table is dropped a release after it stops being written |
| Performance of hosted tables with many organisations | One schema per organisation is the pattern Postgres handles well into the tens of thousands; measured in H1 on the load test |
| Modules keep reaching into each other's tables | Module schemas make that visible in a query; the review rule is that a module reads another's records through its package, not its tables |
| The migration numbering and forward-only rule | Every step is a new migration; nothing old is edited |
| The team builds new features meanwhile | H0 comes first, so every plan from now on (social posting is the first) is written to the rule, and the list only shrinks |

Open: whether hosted tables should also be offered to businesses as ordinary tables (a business with no Xenibase project could have tables at once), which 0033 did not consider; whether chat history and link conversations are "the platform's" or "the business's record" (proposed: the platform's, they are messages); the per-plan limit for hosted rows.
