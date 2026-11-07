# The core stays the engine's: a rule, a line CI holds, and one way to keep a copy

Status: **Plan** (proposed 7 November 2026) · Decision: [0064](../decisions/0064-business-records-out-of-the-core.md) (proposed) · Builds on: [data tables](../data-tables.md) ([0033](../decisions/0033-data-tables-on-xenibase.md)), [kept tables](../data-tables.md#kept-tables) ([0045](../decisions/0045-tally-in-taskiem.md) point 6), [known tables](no-sheet-needed.md#54-known-tables) ([0060](../decisions/0060-no-sheet-needed.md)), [social posting](social-posting.md) (the first plan written to the rule) · Rules: [CONTRIBUTING: migrations](../../CONTRIBUTING.md#migrations)

## 1. Summary

Taskiem's core schema holds 329 tables in 260 forward-only migrations. About 130 are the engine and the platform: tenancy, runs, steps, signals, connections, approvals, audit, billing, operators, the channel plumbing, the catalogue, data-table definitions. The other 190 or so are business records: students and fees, orders and bookings, bills and price lists, stock movements, deliveries, invoices, form submissions, the money log. Each was put there for a stated reason, always the same one: a record with rules, which a workflow cannot be, and which a data table the owner can edit cannot keep safely ([0036](../decisions/0036-storefronts.md), [0045](../decisions/0045-tally-in-taskiem.md)).

This plan started as a plan to move those records out. It was turned over three times on 7 November 2026 and each turn made the case for moving weaker:

- A record with rules needs one commit with the engine for its counter, its hold, its lease and its undo. Splitting the record from that coordination (a stub in the core, the record outside) works on paper and costs an outbox, a reconciler and eventual consistency in every module.
- The records share a base. A school's term invoice is a `customer_invoices` row, its receipt hangs on a payment part, its student's account number is a `customer_accounts` row, and the money that pays a fee is a `money_entries` row matched by the matching module. Storefront orders, stock sales and dispatch sit on the same ledger. Moving one area either drags the ledger or leaves the area pointing back into it.
- The **kept copies** the plan first called twins are not two records. The core row is the truth, the copy is derived from it, only Taskiem writes it, and it can be rebuilt from the core at any time. What it gives the business nothing else gives: a table in its own project that it can join against its own lists, fire a workflow from with `row_changed`, export, and take with it when it leaves, on a project it brought itself. Its cost is a sweeper, a `mirrored_at` column and up to thirty seconds of lag.

So the records stay where they are, and [0033](../decisions/0033-data-tables-on-xenibase.md) and [0045](../decisions/0045-tally-in-taskiem.md) stand as written. What was wrong is narrower than "bloat" and narrower than "twins", and it is three things:

1. **Nothing holds the line.** The core grows a module per feature with no rule a reviewer can point to and no check that fails. The rule and an allow-list CI holds fix that.
2. **The copy is written fresh for every feature.** Schools, the money log and stock each have their own mirror (287, 448 and 211 lines), each with its own tick, its own ensure, its own column list and its own erasure walk. One shared package turns a copy from a week's work into an afternoon's, so the next feature neither skips the copy nor does it badly.
3. **The core's public schema does not say whose table is whose.** Module schemas (`school.receipts`, not `public.school_receipts`) make the engine's tables and each module's visible in a query, without moving a row.

No rows move. No outbox, no stubs, no second backend, nothing asked of Xenibase beyond what the managed tier already is.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Core | Taskiem's own Postgres schema, `engine/db/migrations`, run on every installation, under row-level security and the audit chain |
| Data table | A table the business owns in its data project, listed on the Tables page, read and written by workflows, exported ([data tables](../data-tables.md)) |
| Kept table | A data table only Taskiem writes; the business reads, queries and exports it; its definition cannot be changed by hand ([kept tables](../data-tables.md#kept-tables)) |
| Kept copy | A kept table that is a projection of a core record: the core row is the truth, the copy follows it, and it can be rebuilt from the core. `money_log`, `money_matches`, `school_students`, `school_classes`, `school_term_fees`, `stock_movements` today |
| List | Rows a person would keep in a sheet: customers, suppliers, riders, price lists, a gallery. Edited by people and workflows, in the business's project |
| Record with rules | Rows that must be true: a ledger entry never edited, an order whose status moves one way, a receipt numbered once, a payment matched exactly once. In the core, written only by Taskiem |
| Engine state | What the engine needs transactionally with runs: a hold with an expiry, a counter, a lease, an outbox row, an idempotency key. In the core, never shown as a table |
| Module | A feature's Go packages, API routes, chat commands and web pages, and from this plan its own Postgres schema (`school`, `storefront`, `links`, `money`, `stock`, `dispatch`, `invoices`, `forms`) |
| The base | The invoices, customer accounts, money log and matching modules, which schools, storefronts, stock and dispatch are built on |

## 3. Where we are

The core schema's tables, sorted by what they hold (counts from `engine/db/migrations`, 7 November 2026):

| Area | Core tables | Lists | Records with rules | Engine state | Kept copy today |
| --- | --- | --- | --- | --- | --- |
| Schools | 31 | classes, families, guardians, fee items, student options | sessions, terms, students, enrolments, discounts, receipts, promises, reminders, clearance, claims, plan requests, daily closes, promotions | settings, term invoice map, invoicing keys, student accounts, account jobs, pending postings, receipt counters, family links, inbox, messages, reminder keys, plan arrears | students, classes, term fees |
| Business links | 36 | price lists, items, supplier prices, trade items, settings, areas | links, offers, bills, bill payments, link orders, conversations, messages, reactions, stars, credit terms, notices | mirrors due, orders due, starts, typing, chat sent, blind requests | none (the mirrors are the other party's kept tables) |
| Storefronts and fronts | 17 | catalogue mapping, services, blocks, fronts, domains, reviews | orders, order payments, tickets, bookings | holds, snapshots, inbox, actions, changes, media | none |
| Money log and matching | 16 | fee rates, statement formats, payers, customers | entries, matches, suggestions, leaks, reversals, lookbacks, statement waiting, digests | match state | money log, matches |
| Stock | 9 | suppliers, settings, counts, low-stock | movements, sales, purchase orders, purchase payments | none | movements |
| Dispatch | 7 | riders, settings | deliveries, payouts, remittances, remittance parts | presence | none |
| Invoices and debtor book | 9 | invoice customers, customer links | invoices, payments, amendments, accounts, requests | paid starts, payment starts | none |
| Forms | 9 | endpoints, links | submissions, files, uploads | codes, once, requests, inbox, file deletions | none |
| Profile, agents, reach | 11 | profile, gallery, reach contacts, pages, channels | agent sessions, verifications | reach codes, targets, request links | none |
| Everything else | ~130 | | | the engine and the platform | |

How the three copies are made today: each module has a `mirror.go` with a 30-second tick, a `taskiem_<module>_unmirrored_tenants()` function, a `mirrored_at` column on the core table with a trigger that lets it be set once, `EnsureKept` for its tables, a column list, and a function from a core row to a kept row. The erasure path walks the kept tables by name (migration 00382). The three were written one after another and differ in small ways nobody chose.

## 4. What stays as it is

- **Every record with rules stays in the core**, written by its module, under the base it shares. Nothing moves to Xenibase, and Taskiem's database is the execution store and the record store it already is ([0033](../decisions/0033-data-tables-on-xenibase.md), alternatives).
- **The kept copies stay**, as the business's projection of its records, and become the pattern for every module whose records a workflow, a template or the builder would read.
- **Every API route, chat command and page.** This plan changes a schema name, a package and a file, never what people see.
- **Migrations are forward-only.** A table moves into a module schema by a migration that renames it; nothing old is edited.

## 5. The rule

A reviewer's rule, stated in CONTRIBUTING under migrations and held by CI (§7, C0):

1. **A table a business could keep in a sheet is a data table in its project**, a known table where the shape is Taskiem's to define ([0060](../decisions/0060-no-sheet-needed.md)). Never a core table.
2. **A record with rules is a core table in its module's schema**, written only by its module, and exposed as a kept copy in the business's project whenever a workflow, a template, chat search or the builder would want to read it. The copy is made with the shared package (§6), never by hand.
3. **Engine state is a core table in its module's schema** and is never shown as a table, copied or exported. Holds, counters, leases, outboxes, keys, queues, tokens.
4. **The public schema is the engine's and the platform's.** A module's tables live in the module's schema. A new table in `public` needs a decision.
5. **A module reads another module's records through its package, not its tables.** The sibling-credit join invoicing makes into `school_students` today is the one exception on record; the shared base is reached through the invoicing, moneylog and matching packages as it is now.

The question for every new table is which of 1, 2 or 3 it is, and the plan for every new feature says so, as the [social posting plan](social-posting.md) does for `posts`.

## 6. One way to keep a copy

A package `engine/keptcopy` the three mirrors move onto, and every later module uses. A module declares:

- the kept table's name, title and columns (`datatables.Column`, `Personal` marked);
- the core table and the key by which its rows are found, and a `mirrored_at` column on it;
- a function from one core row to one kept row (and `nil` for a row that leaves the copy, as a deleted class does);
- for the erasure path, which personal columns are a subject's.

The package gives the module, once, what each mirror has today in its own way: the `unmirrored_tenants` query and the index that makes it cheap; the tick, with a shorter tick after a write so a row is in the copy within seconds, not thirty; `EnsureKept` for the table on first use and a column added later; the batch write through the data API with the engine's idempotency key, as the mirrors already do; the set-once `mirrored_at` trigger from a template migration; the erasure walk, so migration 00382's list is derived from the declarations, not written by hand; the rebuild command (`taskiem keptcopy rebuild <tenant> <table>`) that drops and refills a copy from the core, which no mirror has today; a platform counter and alert when a copy falls behind by more than a minute; and the tests: a row written lands once, a row changed is copied again, an erased subject is gone from the copy, a rebuilt copy equals the core.

A module's mirror shrinks to its declarations and its row function, about forty lines, and the three existing ones behave the same way after the move, which the existing tests check.

## 7. Milestones

### C0: the rule and the allow-list (S)

CONTRIBUTING gains §5. `engine/db/core_tables.txt` lists the core's tables, as `dispatch_columns.txt` lists columns today; a migration that creates a table not on the list fails CI unless its header names the decision that allows it (`-- decision: 0064`). The list starts as every table that exists and is only ever edited by a migration that creates or drops a table, so the check is about the list changing in review, not about the list being short. A table listed under a module schema is listed by that name.

**Done when:** CI refuses a new unlisted table; the social posting plan's `posts` is a data table and adds nothing; decision 0064 accepted.

### C1: `engine/keptcopy` (M)

The package in §6, with the money log moved onto it first (the largest mirror and the one with the most tests), then schools and stock. The erasure path reads the declarations. The rebuild command exists and is in the VM kit's recovery sheet.

**Done when:** the three mirrors are declarations and row functions; their existing tests pass unchanged; a copy rebuilt from the core equals the copy the sweeper made; a copy more than a minute behind raises the platform alert on staging.

### C2: module schemas, as each area is touched (S each, over releases)

Each business module's tables move into the module's schema by a rename migration, with the row-level security policies and grants carried over and the module's queries updated: `school`, `storefront`, `links`, `money`, `stock`, `dispatch`, `invoices`, `forms`, `reach`. Done for an area when that area is next worked on, never as a release of its own, and the allow-list is updated in the same migration. The public schema is then the engine's, the platform's, and the channel plumbing's.

**Done when:** `SELECT schemaname, count(*) FROM pg_tables GROUP BY 1` reads as the areas table in §3; no module query names another module's schema except through the one recorded exception.

### C3: copies for the records that lack one (S each, when asked)

Orders, invoices, deliveries, bills and the rest get a kept copy through `engine/keptcopy` the first time a workflow, a template or a customer asks to read them, each an afternoon's declaration. Not before: a copy nobody reads is a sweeper for nothing.

**Done when:** the first such request is met in one pull request of under two hundred lines.

## 8. What each piece must keep

| Property | How it is kept |
| --- | --- |
| Rules on records | Unchanged: the module's writer, in its transaction, under the base's constraints |
| Sealed personal data | Unchanged in the core. In a copy, a `Personal` column is masked everywhere the data tables mask one; a copy never carries a sealed envelope's contents beyond what the mirrors carry today (a name), and never a phone, an email or a date of birth |
| Exactly once | The copy's batch write under the engine's idempotency key, as today; a row copied twice is the same row |
| Audit | Unchanged; a copy is derived and is not audited separately |
| Row-level security | Policies and grants are carried by the rename migration and checked by the existing isolation tests |
| Erasure | Derived from the copy declarations, so a new copy cannot be missed |
| Export and portability | The copy is in the business's project, so it exports and leaves with the business, as today |
| Rebuild | Any copy can be dropped and refilled from the core; the core is the truth |

## 9. Risks and open questions

| Risk | What we do |
| --- | --- |
| The allow-list becomes a formality that is always edited | The check fails on an unlisted table and the list is edited only by the migration that creates or drops one, so every new table is a visible line in review; the rule says what a reviewer asks |
| A rename migration breaks a query the tests do not cover | Each area's rename is done when the area is being worked on, with its tests run, and search for the old name across Go, SQL and the docs is part of the migration's review |
| A copy lags during a burst | The shorter tick after a write and the platform alert; a copy is a projection and a lag is visible, never a lost row |
| The sibling-credit join and any other cross-module table read | Recorded in CONTRIBUTING as the exceptions; each is turned into a package call when its module is next touched |

Open: whether chat history and link conversations are the platform's or the business's record (proposed: the platform's, they are messages); whether a business that brings its own Xenibase project should be offered a managed project for its kept copies, so a copy is never held by its own quota.

## 10. What was considered and set aside

Recorded so the next person does not retrace it:

- **Hosted tables**: a per-organisation schema in Taskiem's Postgres behind the Tables API, transactional and sealed. Reverses the alternative [0033](../decisions/0033-data-tables-on-xenibase.md) rejected by name and makes Taskiem a second table backend for properties the kept copy already gives.
- **The stub split**: coordination in the core, the record in the business's project through an outbox, sealed columns and batch writes asked of Xenibase. Sound, and the most expensive piece of the plan, for records nobody has asked to own; and the base the areas share means one area cannot move alone.
- **Served record tables instead of copies**: rows read from the core on demand, no copy. Removes the sweeper but loses the join in the business's own project, the `row_changed` trigger and portability, which are what the copy is for.
