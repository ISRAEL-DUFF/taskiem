# 0064 — Business records out of the core: a table a business could have made itself is the business's own, and the engine keeps only the stub

Date: 2026-11-07 · Status: **Proposed** · Plan: [core cleanup](../spec/core-cleanup.md)

## Context

Taskiem's core schema has 329 tables. About 190 are business records: students, fees and receipts; orders, bookings and tickets; bills, price lists and link orders; stock movements and purchases; deliveries and payouts; invoices; form submissions; the money log. Each was put in the core for a stated reason: a record with rules, which a workflow cannot be, and which a data table the owner can edit cannot keep safely ([0036](0036-storefronts.md), [0045](0045-tally-in-taskiem.md)); sealed personal data; a transaction with the engine; availability independent of the business's external project.

[0045](0045-tally-in-taskiem.md) point 6 made **kept tables** so a business's record could live in its own project with only Taskiem writing it. They were then used as copies beside the core truth, because the other three reasons remained. The founder, on 7 November 2026, asked that features not bloat the core with tables meant for workflows; the [social posting plan](../spec/social-posting.md) was the first written that way. Xenibase and Taskiem are owned by the same company, so what Xenibase lacks is an ask, not a wall.

## Decision

1. **The rule.** A table a business could have made itself is the business's own, in its data project. A record with rules is a kept table, written only by Taskiem, exactly once, and read, queried and exported by the business like any table. The core keeps only engine state (holds, counters, leases, outboxes, stubs) and the platform's own records, each module's state in that module's schema. CONTRIBUTING states it; CI holds it with an allow-list of the core's tables that only shrinks; a new core table needs a decision.
2. **The record and its coordination are split.** What must commit with the engine is a **stub**: the record's id, version and status, and the fields an engine decision needs before the record can be read (a hold's expiry and amount, a counter's value, an undo marker). It is engine state in the module's schema. The record is a kept table in the business's project, written through the outbox under the engine's idempotency key. A stub carries no business field; a stub that does is a twin.
3. **What the business's project cannot do today is asked of Xenibase**, recorded in the [integration plan](../xenibase-integration.md) as the earlier asks were: sealed columns (encrypted under a per-project key the caller supplies, masked in Xenibase's own pages and exports, returned only with the key), batch writes (several operations on one project in one transaction under one idempotency key), and managed tier terms (the projects Taskiem provisions never pause and carry a rate limit sized for the engine). Until they ship, Taskiem seals the value itself and the outbox writes in order; no move waits on them.
4. **[0033](0033-data-tables-on-xenibase.md) stands whole.** Xenibase is the one backend for data tables; Taskiem's database is the execution store and gains no record store, in any shape.
5. **Lists move to the business's project; records with rules move to kept tables; engine state stays and is renamed into module schemas.** In the order the plan sets: lists first, then invoices, the money log, stock, storefront orders, schools, links and dispatch, each by double-write, a reconciler's week, a reader switch, then the drop. The API, chat and pages are the contract; nothing a person sees changes because a row moved. Where the copy exists today (`money_log`, `money_matches`, `school_students`, `school_classes`, `school_term_fees`, `stock_movements`), the copy becomes the record; no fact is stored twice.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Leave the core as it is and only stop the growth | The twins stay, the business still cannot see its own records except through each module, and every installation carries every feature's tables |
| Taskiem hosts the table itself: a per-organisation schema in its own Postgres behind the Tables API, transactional and sealed | Reverses 0033's rejected alternative, "rows in Taskiem's own database", and makes Taskiem a second table backend, the largest piece of work in the plan, for properties the company's own Xenibase can provide. It would move the records out of the core schema and back into Taskiem's database |
| Move records into Xenibase kept tables as they are, whole | No transaction with the engine for the counter, the hold or the undo; the stub is what makes the split safe |
| Module schemas only, in the core | Separates the engine from the modules, which is worth doing and the plan does it for engine state, but the rows are still Taskiem's, not the business's: not listed, not queried by workflows, not exported |

## Consequences

- Three asks filed with Xenibase; the plan's moves that carry personal data (invoices, storefront orders, schools) use Taskiem's own sealing until sealed columns ship, and the money log is the first user of batch writes.
- One shared package for the stub pattern, the outbox write of a record and the nightly reconciler, used by every module.
- Eight areas moved over several releases, each with a double-write period; the core's allow-list shrinks from 329 to about 130 plus module state.
- Every plan from now on names which of its tables are the business's and which are engine state, as social posting does, and nothing new joins the core without a decision.
