# 0064 — The core stays the engine's: lists in the business's project, records in their module's schema with a kept copy, a line CI holds

Date: 2026-11-07 · Status: **Proposed** · Plan: [core cleanup](../spec/core-cleanup.md)

## Context

Taskiem's core schema has 329 tables. About 190 are business records: students, fees and receipts; orders, bookings and tickets; bills, price lists and link orders; stock movements and purchases; deliveries and payouts; invoices; form submissions; the money log. Each was put in the core for a stated reason: a record with rules, which a workflow cannot be, and which a data table the owner can edit cannot keep safely ([0036](0036-storefronts.md), [0045](0045-tally-in-taskiem.md)). [0045](0045-tally-in-taskiem.md) point 6 made **kept tables** so a business could read such a record in its own project, and three modules copy their records into them with a sweeper each.

The founder, on 7 November 2026, asked that features not bloat the core with tables meant for workflows, and that the same fact not be kept twice. The [social posting plan](../spec/social-posting.md) was the first written that way. Three ways of moving the records out were then worked through and set aside (plan §10): the records share a ledger (invoices, customer accounts, the money log, matching) that no area can leave alone, and the kept copy turned out to be a projection the business needs, not a second record.

## Decision

1. **The rule.** A table a business could keep in a sheet is a data table in its project, never a core table. A record with rules is a core table in its module's schema, written only by its module, and exposed as a kept copy in the business's project whenever a workflow, a template, chat search or the builder would read it. Engine state (holds, counters, leases, outboxes, keys, queues, tokens) is a core table in its module's schema and is never shown as a table. The public schema is the engine's and the platform's.
2. **A line CI holds.** `engine/db/core_tables.txt` lists the core's tables; a migration that creates a table not on the list fails CI unless its header names the decision that allows it. The list is edited only by the migration that creates or drops a table, so every new core table is a line in review.
3. **One way to keep a copy.** A shared package, `engine/keptcopy`, makes a kept copy from a module's declaration of the table, the core rows and the row function: the sweeper, the ensure, the idempotent batch write, the erasure walk, a rebuild from the core, a lag alert and the tests, once. The three mirrors that exist move onto it; every later module uses it. A copy is derived: the core row is the truth, and any copy can be rebuilt.
4. **Module schemas.** Each business module's tables move into the module's own schema by a rename migration when the area is next worked on, never as a release of their own; row-level security and grants carried over; the API, chat and pages unchanged. A module reads another module's records through its package, not its tables; the exceptions that exist today are recorded and retired when touched.
5. **Nothing moves out of the core.** [0033](0033-data-tables-on-xenibase.md) and [0045](0045-tally-in-taskiem.md) stand as written. Records with rules stay on the base they share, and a copy is made for the records a business asks to read, not before.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Move records with rules into the business's project, the engine keeping a stub (id, version, status, coordination) and writing the record through an outbox, with sealed columns and batch writes asked of Xenibase | Sound on paper. Costs an outbox, a reconciler and eventual consistency in every module, for records nobody has asked to own; and schools, storefronts, stock and dispatch share the invoices, accounts and money-log tables, so one area cannot move without the ledger or without pointing back into it |
| Taskiem hosts the table itself: a per-organisation schema in its own Postgres behind the Tables API | Reverses the alternative 0033 rejected by name and makes Taskiem a second table backend, for properties the kept copy already gives |
| Serve core records as read-only tables on demand, no copy | Removes the sweeper but loses what the copy is for: a table in the business's own project it can join, trigger a workflow from, export and take with it |
| Leave everything as it is | The core keeps growing a module per feature with nothing a reviewer can point to, and each new copy is written by hand |

## Consequences

- A day for the rule and the check; a medium piece of work for `engine/keptcopy` with the three mirrors moved onto it; a small rename migration per area as each is touched.
- The three existing mirrors (946 lines) become declarations and row functions of about forty lines each; the erasure list is derived, not written.
- Every plan from now on names which of its tables are lists, records or engine state, as social posting does, and nothing new joins the core without a line in the list.
- The copy becomes cheap enough that a module makes one the first time a workflow asks, in one small pull request.
