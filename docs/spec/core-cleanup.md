# Business records out of the core: the engine keeps the engine, the business keeps its records

Status: **Plan** (proposed 7 November 2026) · Decision: [0064](../decisions/0064-business-records-out-of-the-core.md) (proposed) · Builds on: [data tables](../data-tables.md) ([0033](../decisions/0033-data-tables-on-xenibase.md)), [kept tables](../data-tables.md#kept-tables) ([0045](../decisions/0045-tally-in-taskiem.md) point 6), [known tables](no-sheet-needed.md#54-known-tables) ([0060](../decisions/0060-no-sheet-needed.md)), the definer pattern ([0061](../decisions/0061-business-links.md)), [social posting](social-posting.md) (the first plan written to this rule) · Rules: [CONTRIBUTING: migrations](../../CONTRIBUTING.md#migrations)

## 1. Summary

Taskiem's core schema holds 329 tables in 260 forward-only migrations. About 130 are the engine and the platform: tenancy, runs, steps, signals, connections, approvals, audit, billing, operators, the channel plumbing, the catalogue, data-table definitions. The other 190 or so, about 60% of the schema and most of the Go code since October, are business records: students and fees, orders and bookings, bills and price lists, stock movements, deliveries, invoices, form submissions, the money log. Each was put there for a stated reason, always the same one: a record with rules, which a workflow cannot be, and which a data table the owner can edit cannot keep safely.

That reason is real. The cost is also real. Every such table is a module of the monolith with its own API, chat commands and pages against core rows; the business cannot see, query or export the record except through what that module exposes; every table is a permanent commitment on every installation; and where the business did need the record in its own tables, the engine writes a **kept copy** beside the core truth, so the same fact is stored twice (`money_log`, `money_matches`, `school_students`, `school_classes`, `school_term_fees`, `stock_movements`). The social posting plan was the first written the other way: one table, the business's own, nothing in the core. This plan makes that the rule and works the existing features back to it.

**The rule.** A table a business could have made itself is the business's own, in its data project. A record with rules is a **kept table** in the business's project: written only by Taskiem, through one writer, exactly once; read, queried and exported by the business like any table. The core keeps only what the engine needs to run: references, workflow state, coordination that must be transactional with runs (holds, counters, leases, outboxes), and the platform's own records. Nothing else.

**How the records get there (§5).** Three things kept ledgers and orders in the core even after kept tables existed: one commit with the engine, sealed personal columns, and availability that does not depend on an external project. None of them needs a new place to put rows. What must commit with the engine is coordination, not the record: a counter, a hold, a lease, an undo. That stays in the core as engine state, beside a **stub** that names the record; the record itself is a kept table in the business's project, written exactly once through the outbox. The two things the business's project cannot do today, an all-or-nothing write of several rows and a sealed column, are asks to Xenibase, which the same company owns, and the managed tier the platform provisions is the platform's to keep available. Decision 0033 stands: one backend, Xenibase, and Taskiem's database stays the execution store.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Core | Taskiem's own Postgres schema, `engine/db/migrations`, run on every installation, under row-level security and the audit chain |
| Data table | A table the business owns in its data project, listed on the Tables page, read and written by workflows, exported ([data tables](../data-tables.md)) |
| Kept table | A data table only Taskiem writes; the business reads, queries and exports it; its definition cannot be changed by hand ([kept tables](../data-tables.md#kept-tables)) |
| Stub | The engine's own row for a record: the tenant, the record's id in its kept table, a version, a status, and the coordination the engine needs (a hold's expiry, a counter's value). Never a business field; a pointer, not a copy |
| Ask | A feature Taskiem needs from Xenibase, recorded in the [integration plan](../xenibase-integration.md) as the earlier asks were (§5) |
| List | Rows a person would keep in a sheet: students, suppliers, riders, price lists, a gallery. Edited by people and workflows |
| Record with rules | Rows that must be true: a ledger entry never edited, an order whose status moves one way, a receipt numbered once, a payment matched exactly once. Written only by Taskiem |
| Engine state | What the engine needs transactionally with runs: a hold on a slot with an expiry, a counter, a lease, an outbox row, a stub. Stays in the core |
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
- **Decision 0033**, all of it: Xenibase is the one backend for data tables, the home of a business's own project, and Taskiem's database is the execution store and not a record store. This plan adds nothing beside it.
- **Migrations are forward-only.** A core table leaves by a migration that stops writing it, a copy to the new home, a switch of readers, and a later migration that drops it, never by editing an old migration.

## 5. How a record with rules lives in the business's project

A kept table today is a copy because three things could not be had from Xenibase. Each is answered below without a second place to put rows.

| What kept the record in the core | The answer |
| --- | --- |
| One commit with the engine | Split the record from its coordination. The receipt number, the hold on a slot, the lease, the undo marker and the run's reference are **engine state**: a stub in the module's schema, written in the engine's transaction. The receipt, the booking, the entry are the **record**: a kept table in the business's project, written through the outbox with the engine's idempotency key, exactly once. The stub carries the record's id and version once the write lands; until then the stub's status says so |
| Sealed personal data | An ask to Xenibase: a **sealed column**. The project stores the value encrypted under a key Taskiem supplies per project, shows it masked in Xenibase's own pages and exports, and returns it through the data API only to a call that carries the key. Taskiem keeps the key in the tenant's vault as it keeps the tenant key today. Until it ships, Taskiem seals the value itself before the write and unseals it on read; the business's own Xenibase pages then show the envelope, and the Tables page the value |
| Several rows that must land together | An ask to Xenibase: an **all-or-nothing batch write**, several operations on several tables of one project in one call, applied in one transaction, under one `Idempotency-Key`. An entry and its match, an order and its payment, a receipt and its account posting. Until it ships, the outbox writes them in order under one key and the stub's version makes a half-landed pair visible to the reconciler (§6) |
| Availability on a page view or a webhook | The engine decides from the stub, never from the record: whether a hold is live, what amount it was placed for, whether an order may move. The record is read where a person reads, the page, the chat, the export, the workflow step. The managed projects the platform provisions (0033 point 2) are on the platform's own tier, which the company sets so that they do not pause and their rate limit covers a webhook burst; a business that brings its own project takes its project's limits, as it does for every table today |

**Rule for the stub.** It holds what an engine decision needs before the record can be read and nothing else: identity, version, status, the coordination fields. A stub that grows a business field is a twin, and the review question for every stub column is "which engine decision reads this?".

**The asks.** Two features and one tier setting, each recorded in the integration plan under "Taskiem integration asks" as the data API's writes and idempotency were:

1. **Sealed columns**: a column type whose values the project encrypts under a per-project key the caller supplies; masked in Xenibase's UI, CSV and backups; returned by the data API only with the key; searchable by equality on a keyed hash. Needed by H3, H6, H7 and the forms submissions.
2. **Batch writes**: one data API call carrying several operations across the project's tables, applied in one transaction, refused whole if any is refused, under one idempotency key honoured for 24 hours. Needed by H4 first, then every record with more than one row.
3. **Managed tier terms**: projects Taskiem provisions never pause for quota, carry a rate limit sized for the engine's bursts, and are backed up on the engine's schedule. A setting of the platform's Xenibase organisation, not a feature.

Each move below says which ask it needs and what it does until the ask ships; the plan does not wait for Xenibase to move the lists.

## 6. The method, for every move

Each area is moved the same way, so the first teaches the rest:

1. **Classify** each table: list, record with rules, engine state, platform. Lists and records leave; engine state stays and is renamed into a module schema (`school.account_jobs`, not `public.school_account_jobs`), so the core's public schema is the engine's alone. A record's coordination fields are split out into its stub here (§5).
2. **Define** the destination tables as known tables in `templates/tables/` (lists) or kept definitions in the module (records), with `personal` columns marked. The names already taken by kept copies keep their names: the copy becomes the record.
3. **Write to both** for one release: the module writes the stub in its transaction and the record through the outbox, and keeps writing the core table. A reconciler compares them nightly and reports differences as a platform alert; none for a week is the gate.
4. **Switch readers**: the API, chat, pages and workflow steps read the new home. The twin's sweeper stops. The core table becomes write-only.
5. **Stop writing** the core table; a migration drops it a release later, after the next backup rotation.
6. **Erasure and retention** follow the record: a person's erasure (`money_log_erasures`, `form_file_deletions` are the shapes) runs against the new home; retention is the business's `max_retention_days` as for every table.
7. **Tests**: the area's workflow tests and browser tests unchanged (the API is the contract), plus the reconciler's week.

A CI check from H0 holds the line meanwhile.

## 7. Milestones

### H0: the rule, the allow-list and the check (S)

- CONTRIBUTING gains the rule: a table a business could have made itself is the business's own; a record with rules is a kept table; only engine state and the platform's own records go in the core, each in its module's schema.
- `engine/db/core_tables.txt`: the allow-list of the core's tables, as `dispatch_columns.txt` lists columns today. A migration that creates a table not on the list fails CI unless the migration's header names the decision that allows it (`-- decision: 0064`). The list starts as every table that exists, so nothing breaks, and shrinks with each milestone.
- Decision 0064 accepted; the three asks of §5 filed with Xenibase.

**Done when:** CI refuses a new unlisted table; the social posting plan builds under the rule with nothing to add to the list.

### H1: the stub pattern and the outbox (M)

A package the modules share: a stub table per record in the module's schema, the write of record and stub (stub in the transaction, record through the outbox under the engine's key, the record's id and version written back), the read path that decides from the stub and reads the record from the kept table, the nightly reconciler that compares a core table, its stubs and its kept table, and the sealing of `personal` columns on the way out until Xenibase's sealed columns ship. `engine/datatables` gains `WriteBatch`, which uses Xenibase's batch write where the project has it and ordered single writes otherwise, so a module is written once either way.

**Done when:** a record written through the pattern lands exactly once under a crash at every point between the stub's commit and the outbox's delivery; the reconciler reports a planted difference; a `personal` column written through it reads back masked through every surface it does today.

### H2: lists that are plainly the business's (M)

Business profile and gallery, reach contacts, riders, suppliers, price lists and supplier prices, school families and guardians, storefront reviews and blocks. Each becomes a known table in the business's project; the module reads and writes it through `datatables`; chat and pages unchanged.

**Done when:** the Tables page lists them, a workflow reads a rider or a supplier with `read_rows`, exports work, and the core tables are dropped.

### H3: invoices and the debtor book (M; needs H1; sealed columns for the customer fields)

`customer_invoices`, payments, amendments and accounts become kept tables (`invoices`, `invoice_payments`, …) with a stub per invoice; `invoice_customers` a list. Paid and payment starts stay as engine state under `invoices.*`.

**Done when:** every invoice route and chat command answers from the new tables; the matching tests pass; a customer's erasure clears the new home.

### H4: the money log, matching and customer accounts (M; needs H1; the first user of batch writes)

The kept copy `money_log` becomes the record: an entry and its match are one batch write from the rail's webhook, with the match state and the undo marker as the stub; the sweeper and the twin go.

**Done when:** no row of the money log exists twice; a webhook's entry and its match land together or not at all; the leak and digest reports read the new home; the money-log erasure path runs against it.

### H5: stock (M; needs H1)

`stock_movements` (the kept copy) becomes the record; sales, purchase orders, payments, counts and low-stock become kept tables; suppliers and settings lists (H2).

**Done when:** the stock chat commands and the kept-copy sweeper tests pass without the twin.

### H6: storefront orders, bookings and tickets (L; needs H1; sealed columns)

Orders, order payments, bookings and tickets become kept tables with their customer columns sealed; the hold with its expiry and amount, the status and the snapshot reference are the order's stub; inbox, actions and changes stay as engine state under `storefront.*`; catalogue and services are already the business's tables.

**Done when:** a storefront order placed on the public host, paid and delivered reads from the new tables at every step; the status link works; the browser tests pass.

### H7: schools (L; needs H1; sealed columns for guardians)

The largest area: students, classes and term fees are already kept copies and become the record; term invoices, receipts, student accounts, reminders, promises, clearance and daily closes become kept tables, the receipt counter and the posting lease their stubs; families and guardians lists (H2); account jobs, pending postings and invoicing runs stay as engine state under `school.*`.

**Done when:** the schools walkthrough and the demo school seed run against the new tables; the receipt counter is still gapless and every number has exactly one receipt row; no twin remains.

### H8: business links and dispatch (L; needs H1, H3, H6)

Bills become the buyer's kept table and link orders the seller's; conversations and messages kept; price lists are H2; the mirror queue, orders due and definers stay as engine state under `links.*`. Deliveries, payouts and remittances kept; presence engine state.

**Done when:** the business-links suite passes with the mirrors in kept tables; the reconciler compares digests across the new homes; the dispatch flows read from the new tables.

### H9: the core alone (S)

The allow-list is the engine and the platform only; every module's engine state is in its own schema; `engine/db/migrations` carries a note at the top naming the rule and the list.

**Done when:** a new table in `public` needs a decision, and the list has no business record on it.

## 8. What each move must keep

| Property | How it is kept |
| --- | --- |
| Rules on records | Only Taskiem's writer writes a kept table (as today); a rule the writer enforces is a function in the module, with the same tests; what must be decided atomically is decided on the stub in the engine's transaction |
| Sealed personal data | Xenibase's sealed columns once they ship; Taskiem's own envelope in the column until then, unsealed on read by Taskiem alone |
| Exactly once | The outbox and `Idempotency-Key`, as the mirrors and kept copies do today; batch writes for rows that must land together; the stub's version for the reconciler |
| Audit | Every write to a kept record is audited in the business's chain as the module's writes are today; nothing changes because the table moved |
| Row-level security | Stubs and engine state carry the core's policies; the record is in the business's own project, which no other organisation can reach |
| Erasure and retention | Follow the record (§6 point 6) |
| Export | Every moved table exports as every table does; the business owns more than before |
| Limits | A kept record is never held back by `max_data_tables` or the project's quota (a receipt is written whatever the plan); the managed tier's terms (§5) make that true, and a business on its own project is told when its quota holds a record |
| Backups | The record is in the project's backups, on the managed tier on the engine's schedule; the stubs in the engine's; the reconciler says when the two disagree |

## 9. Risks and open questions

| Risk | What we do |
| --- | --- |
| Xenibase's sealed columns or batch writes slip | Neither blocks a move: Taskiem seals the value itself and the outbox writes in order under one key until they ship (§5); the moves that carry personal data (H3, H6, H7) go in that order and switch to the native feature when it lands |
| A move loses rows | Double-write and the nightly reconciler for a week before readers switch; the core table is dropped a release after it stops being written |
| The record is a minute behind the engine | Every decision the engine takes reads the stub, never the record; a page that shows a record just written shows the stub's status until the write lands, as the mirrors do today; measured in H1 |
| A stub grows into a twin | The review question on every stub column is which engine decision reads it; the reconciler lists stub columns no decision reads |
| Modules keep reaching into each other's tables | Module schemas make that visible in a query; the review rule is that a module reads another's records through its package, not its tables |
| The migration numbering and forward-only rule | Every step is a new migration; nothing old is edited |
| The team builds new features meanwhile | H0 comes first, so every plan from now on (social posting is the first) is written to the rule, and the list only shrinks |

Open: whether chat history and link conversations are "the platform's" or "the business's record" (proposed: the platform's, they are messages); the managed tier's rate limit, to be sized from the engine's busiest webhook hour on staging; whether a business that brings its own project should be offered the managed tier for its kept records.
