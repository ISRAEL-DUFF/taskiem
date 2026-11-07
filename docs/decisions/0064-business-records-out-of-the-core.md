# 0064 — Business records out of the core: a table a business could have made itself is the business's own, and Taskiem may host it

Date: 2026-11-07 · Status: **Proposed** · Plan: [core cleanup](../spec/core-cleanup.md)

## Context

Taskiem's core schema has 329 tables. About 190 are business records: students, fees and receipts; orders, bookings and tickets; bills, price lists and link orders; stock movements and purchases; deliveries and payouts; invoices; form submissions; the money log. Each was put in the core for a stated reason: a record with rules, which a workflow cannot be, and which a data table the owner can edit cannot keep safely ([0036](0036-storefronts.md), [0045](0045-tally-in-taskiem.md)); sealed personal data; a transaction with the engine; availability independent of the business's external project.

[0045](0045-tally-in-taskiem.md) point 6 made **kept tables** so a business's record could live in its own project with only Taskiem writing it. They were then used as copies beside the core truth, because the other three reasons remained. The founder, on 7 November 2026, asked that features not bloat the core with tables meant for workflows; the [social posting plan](../spec/social-posting.md) was the first written that way.

## Decision

1. **The rule.** A table a business could have made itself is the business's own, in its data project. A record with rules is a kept table, written only by Taskiem, exactly once, and read, queried and exported by the business like any table. The core keeps only engine state (holds, counters, leases, outboxes, references to records) and the platform's own records, each module's state in that module's schema. CONTRIBUTING states it; CI holds it with an allow-list of the core's tables that only shrinks; a new core table needs a decision.
2. **Taskiem may host a data table.** A second backend behind the same Tables API: rows in a per-organisation schema in Taskiem's Postgres, under row-level security, in the engine's backups, with `personal` columns sealed by the tenant key, written in the engine's transaction. Xenibase stays the home of ordinary tables and of a business's own project ([0033](0033-data-tables-on-xenibase.md)); a kept record whose rules need the transaction or the seal is hosted. The business sees one Tables page and one API either way.
3. **Lists move to the business's project; records with rules move to kept hosted tables; engine state stays and is renamed into module schemas.** In the order the plan sets: lists first, then invoices, the money log, stock, storefront orders, schools, links and dispatch, each by double-write, a reconciler's week, a reader switch, then the drop.
4. **The API, chat and pages are the contract.** Nothing a person sees changes because a row moved.
5. **Where the copy exists today, the copy becomes the record** (`money_log`, `money_matches`, `school_students`, `school_classes`, `school_term_fees`, `stock_movements`); no fact is stored twice.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Leave the core as it is and only stop the growth | The twins stay, the business still cannot see its own records except through each module, and every installation carries every feature's tables |
| Move records into Xenibase kept tables as they are | No transaction with the engine, a minute behind, personal data unsealed, the project's limits and pauses on a webhook's path: the reasons the records went into the core in the first place would hold again |
| Module schemas only, in the core | Separates the engine from the modules, which is worth doing, but the rows are still Taskiem's, not the business's: not listed, not queried by workflows, not exported |
| One schema for all hosted rows with a tenant column | Fewer schemas, but the business's export is a filtered dump and the seal and RLS are per row; a schema per organisation is the shape the engine already uses for Xenibase projects and what a business takes when it leaves |

## Consequences

- One new backend in `engine/datatables`, about the size of the Xenibase one, and a plan limit for hosted rows.
- Eight areas moved over several releases, each with a double-write period; the core's allow-list shrinks from 329 to about 130 plus module state.
- Every plan from now on names which of its tables are the business's and which are engine state, as social posting does, and nothing new joins the core without a decision.
- 0033's open question, a business with no Xenibase project, gets an answer for free once hosted tables exist: it is for a later decision whether to offer them as ordinary tables.
