# 0046 — Balans is rebuilt in Taskiem as a Schools area: students with their own account numbers, term fees as invoices, no ledger

Date: 2026-10-23 · Status: **Accepted** 2026-10-23 (build plan: [schools](../spec/schools.md))

## Context

Balans ([overview](../strategy/balans-overview.md)) is the founder's fee collection and reconciliation product for Nigerian private schools. Its one idea is that **every student has their own bank account number**: a parent transfers to it, and the payment lands on that child's fees with no bursar in the loop. Around that it has an exception queue for money it cannot place, receipts, a parent page reached from a link, a three-step reminder ladder with promises to pay and payment plans, a two-person rule for cash and discounts above a threshold, clearance worked out from what was paid, a gate screen with no amounts, a daily close and an audit trail. It runs against a seeded demo school only: it has no live schools, and invoicing, student import, fee editing, settings, automated WhatsApp and anything beyond exact matching are unbuilt.

Taskiem already has most of what Balans needs, from Tally ([decision 0045](0045-tally-in-taskiem.md)): invoices paid only by matched payments, the `virtual_account` capability and `virtual_account.credited` event, a money log written once per movement, matching with suggestions, undo and automatic matching, receipts on the `invoice` trigger, reminders, approvals with four-eyes and step-up, audit, row-level security per tenant, messages to people outside a business ([0039](0039-reaching-people-outside.md)), and leaks and summaries for the owner.

Reviewing Balans against Taskiem on 2026-10-23 found the same split 0045 found for Tally: **processes**, which are workflows (issue account numbers to a roster, invoice a term, chase, receipt, close the day, tell the proprietor), and **a record with rules**, which a workflow cannot be (who a student is and which account is theirs, what each child owes for a term, what has been paid, who is cleared). Balans exists because of a documented case in which "cleared" was a flag a person could set without money moving; a record built from workflow writes to editable data tables would reopen that hole.

Balans also kept a double-entry ledger enforced by the database, for a lender. 0045 dropped Tally's ledger for the same reason it applies here: without lending, a single-entry money log with corrections as new rows, invoices paid only by matches, and a balanced journal export answer what the school needs.

## Decision

1. **Balans is not continued as a separate product.** It is rebuilt inside Taskiem as a **Schools area** that any Taskiem business can turn on, beside Invoices and Money. Balans' code is the founder's own and may be reused directly where it fits (the reference format, account naming, matching features, receipt layout, reminder copy).
2. **Students, families, classes, terms and fee structures are a platform record**, changed by people with the right permissions and by workflows only through a `schools@1` platform connector, as invoices are through `invoices@1`.
3. **What a child owes for a term is an invoice.** A term's fees become one invoice per enrolled student, built from the class's fee structure, issued exactly once per student and term. Discounts and amendments are changes to that invoice, with a reason, audited. There is **no separate ledger**: a child's balance is their invoices less the payments matched to them, and the money log is the record of every movement (0045's reasoning, applied again).
4. **Clearance is derived, never set.** A student is cleared for a term when the term's invoices are paid (or an approved payment plan is up to date). An override is possible only with a reason, by a person with the permission, and is audited; there is no "cleared" field anyone can write.
5. **Every student can have a permanent account number from any connected rail that maps `virtual_account`** (iswallet, Flutterwave, Paystack, Moniepoint and others as their mappings are added from public documentation). The account belongs to the student, is issued once (idempotent, resumable, in batches), and **a credit to it is matched exactly** to that student's oldest open invoice, then the next; what is left is held as the family's credit. A credit whose account or reference does not agree with what Taskiem issued is held for a person, never posted. These rules are built for every business, not only schools: a customer's permanent account (0045's §8.1 rule, not yet built) is the same thing.
6. **Matching learns payers.** Confirming that money from a payer belongs to a family remembers that payer, so the next payment from them is suggested first, and matched automatically where the business has turned automatic matching on. One payment may be **split across several invoices** (siblings), oldest first. Both are general matching features.
7. **A child is credited what the parent sent.** Where a rail settles net of its fee, the amount counted towards the invoice is the gross (what landed plus the fee), and the fee is shown as a cost, not a shortfall for the family.
8. **Parents need no account.** A family gets a statement page on the public host reached from a link, with every child's fees, payments, account numbers, receipts, "I have paid" and a payment plan request. Links are sent in reminders and receipts on WhatsApp, Telegram or SMS (0039).
9. **Cash at the desk and discounts above the school's thresholds need a second person** through Taskiem's approval policies; until approved, nothing reaches the child's fees. Thresholds are the owner's setting.
10. **Gate checks are a role with one screen**: a name search answering **Cleared** or **See the bursar**, never an amount. Any other page is refused by the server, not hidden.
11. **Fees collected for someone else** (an exam body's fee) are pass-through lines: shown to parents as collected on that body's behalf, and kept out of the school's own income in summaries and exports.
12. **Processes are workflows and templates**: issuing account numbers to new students, invoicing a term, the reminder ladder, receipts, telling a parent about a plan, the daily close report and the proprietor's summary.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Keep Balans as its own app and connect it to Taskiem as a product (0044's route for finished products such as Payrolla) | Balans is far from finished and has no schools; finishing it would be a second product to run for features Taskiem mostly has |
| Everything as workflows over ordinary data tables | A person or workflow with `table.edit` could mark a child paid or cleared without money moving: the exact failure Balans was built to prevent |
| Balans' double-entry ledger as a Taskiem module | Its value was a record a lender would accept; without lending, invoices, matches and the money log answer the school's questions, and the journal export balances (0045) |
| A school-only matching engine | The permanent-account rule, payer memory and split payments are what every business with customers' accounts needs; one engine keeps one set of guarantees |
| A clearance field with an audit trail | An audited flag still lets a person clear a child who has not paid; deriving it removes the field |
| Balans' 72-hour parent token as the only way in | Kept as the default for links in messages, but the page is a family's own statement; expiry is a setting, and a printed receipt gets its own token so a parent can check it (Balans could not) |

## Consequences

- Rails gain `virtual_account` mappings from their public documentation (iswallet, Paystack, Flutterwave first), with lookups so credits can be read back over an overlapping window when a webhook is lost.
- Matching gains the permanent-account rule, split payments across invoices, payer memory and gross counting; these close items in the [open items](../spec/invoices-open-items.md) for every business.
- New platform pieces: the schools record and `schools@1`, term invoicing, the family statement page on the public host, receipts as numbered records with a verification page, clearance and the gate role, and a Schools area in the web app.
- New permissions (`school.read`, `school.manage`, `school.approve`, `school.gate`) and a built-in **gate** role.
- The phase plans gain a workstream, B0 to B9, in the build plan.

## Provenance

From a discussion on 2026-10-23: the founder shared the Balans overview, said Balans is their own product, mostly built but far from finished, and asked for it to be rebuilt in Taskiem as workflows. The founder accepted the recommendation to keep the record in the platform and the processes as workflows, and settled four questions the same day: Balans is rebuilt inside Taskiem as a vertical; account numbers come from any money service that can issue them (iswallet, Flutterwave, Paystack and others); no ledger, for the reason it was dropped from Tally; and it is an area any Taskiem business can turn on.
