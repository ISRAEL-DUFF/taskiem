# 0045 — Tally is folded into Taskiem: invoices, a money log and one-time accounts, no separate product

Date: 2026-10-23 · Status: **Proposed** (build plan: [invoices and the money log](../spec/invoices-and-money-log.md))

## Context

Tally ([overview](../strategy/tally-overview.md)) is a reconciliation product built before Taskiem for Nigerian small businesses that collect through several channels. It pulls every channel into one ledger, matches payments to invoices and sales, quantifies leakage (short or late settlements, fees above the agreed rate, duplicates, money that never arrived), issues invoices with payment links and dedicated account numbers, and was meant to underwrite working capital from the resulting record. Its Phases 1 and 2 are built; it has no users and no banking partner, and lending is no longer a goal.

Taskiem now builds products on payment rails as templates ([decision 0044](0044-rails-and-products.md)): rails declare capabilities, including `collect_link` and `virtual_account`, and normalised events (`payment.received`, `virtual_account.credited`, `payout.settled`), and R4 added a trigger that starts on those events whichever rail is connected. Taskiem also has data tables, storefront orders, POS records in data tables ([0040](0040-pos-terminals.md), [0041](0041-opay-business.md)), forms, messages to people outside a business ([0039](0039-reaching-people-outside.md)), scheduled triggers and exactly-once steps.

Reviewing Tally against Taskiem on 2026-10-23 found two kinds of feature:

- **Processes**, which are workflows: send an invoice, wait for payment, chase it, tell the owner, record an expense, alert on a broken source. Taskiem can run all of them; most need only templates.
- **A record with rules**, which a workflow cannot be: an entry for every movement of money, never edited; matches between payments and invoices that can be undone without losing history; an invoice that is marked paid only by a payment. Steps of a workflow do not commit together, and data tables can be edited by anyone with `table.edit`, so a record built only from workflow writes is as good as the least careful workflow or person.

Without lending, the record does not have to satisfy a lender, so Tally's double-entry ledger and its database trigger are more than is needed. It does have to be one the owner can believe.

Separately, a customer paying an invoice by bank transfer needs an account number tied to that invoice. Most rails (iswallet, Flutterwave, Paystack, OPay and others) offer **dynamic accounts**: a temporary account number for one payment, which expires after 15 minutes to an hour. Today's `virtual_account` capability is a permanent account per customer, with no amount and no expiry.

## Decision

1. **Tally is not continued as a separate product.** What merchants need from it is built in Taskiem; working capital, the credit analyst queue, BVN capture (the rails do their own checks) and Tally's operator tooling (Taskiem's operator console covers it) are dropped. Tally's code is the team's own and may be reused directly: the exception definitions, matching features, fee and take-rate calculations, statement parsers and export formats.
2. **Invoices are a platform record**, as storefront orders are: numbered per business, with the statuses draft, sent, part-paid, paid, overdue and void, readable from a public invoice page, and changed by workflows only through an `invoices@1` platform connector. **An invoice becomes part-paid or paid only from matched payments**, never by hand or by a workflow writing a status.
3. **A one-time account capability, `collect_transfer`**, joins the capability set: an amount, a reference Taskiem derives, a customer and a wanted lifetime in; the account number, bank, account name, reference and the time it actually expires out. Payments into it arrive as `virtual_account.credited` with that reference. Rails map it in their manifests like any capability, starting with iswallet, Flutterwave, Paystack and OPay.
4. **The customer gets the account number when they ask for it, on the invoice page or in chat.** The invoice page has a **Pay by bank transfer** button that issues a one-time account and shows it with a countdown, and **Get a new account number** when it has expired. The same is offered in WhatsApp and Telegram to a customer who received the invoice there. Every account issued for an invoice stays tied to it. **Who pays the rail's fee for the account is the business's setting**, changeable on one invoice: the business (the default), or the customer, who then sees the fee as its own line.
5. **A payment matches by its reference, never by the clock.** Money that lands in an expired account, after the customer took a new one, or in two accounts for one invoice still counts towards that invoice. Paying more than is due is flagged as an overpayment for the owner. The countdown only tells the customer how long to use the number.
6. **A money log, kept by Taskiem in the business's data tables.** Every movement on a business's connected rails (payments received, account credits, payouts and their outcomes, refunds), its POS sales and its uploaded statements is written once as a row of a **kept table**: a data table that Taskiem writes and that people and workflows can read, search and export but not change or delete. Corrections are new rows that refer to the old. Matches between payments and invoices or orders are rows in a second kept table; undoing a match adds a row. Payments Taskiem issued the reference for match exactly; others get suggestions with their reasons, and **a business can turn on automatic matching** of the strongest suggestions (off by default). This keeps 0040's reasons for data tables (the owner can export, search and reuse the record, and templates read it like any table) while giving the record the one rule it needs.
7. **Outsiders may ask for an invoice's account number in chat.** This amends 0039 point 7: besides STOP and START, a contact who was sent an invoice on a channel may use that invoice's buttons there (**Pay by transfer**, **New account number**). Nothing else is added to their vocabulary, and a button works only for an invoice of the business that sent it, to that contact, while it is open.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Keep building Tally as a separate product and connect it to Taskiem (0044's product route) | No users, no banking partner and no lending goal: it would be a second product to run for features Taskiem mostly has |
| Everything as templates over ordinary data tables | The record could be edited by any person or workflow with `table.edit`, so "what did I really earn" would be only as good as the least careful edit. An invoice status written by a workflow could say paid without a payment |
| Tally's double-entry ledger as a Taskiem module | Its value was a record a lender would accept. Without lending, a single-entry log with corrections as new rows answers the owner's questions at a fraction of the cost |
| A Taskiem-owned money table outside data tables | Rejected in 0040 for POS records and for the same reasons here: the owner could not export, search or reuse it, and templates could not read it |
| A permanent account per invoice | Rails limit or charge for permanent accounts, and most offer only temporary ones for one-off payments; a business would collect thousands of dead accounts |
| Match by amount and time window inside the expiry only | A transfer started before expiry can land after it; matching by the clock would lose real payments |
| Let outsiders type "send me an account number" | Free text from outsiders invites abuse and support load; buttons tied to an invoice they were sent are enough |

## Consequences

- `schemas/capabilities-v1.json` gains `collect_transfer`; four rails gain its mapping and conformance cases.
- Data tables gain **kept tables**, written only by Taskiem's own writers. A business that brings its own Xenibase project can still edit them there, outside Taskiem; the guarantee is against edits through Taskiem, which is what the owner needs without a lender.
- A money log needs events from every connected rail even when no workflow listens: each rail connection gets its own intake for the log (a remote subscription where the rail allows it, polling where it offers lookups, otherwise the URL on the connection page).
- New platform pieces: the invoice record and `invoices@1`, the invoice page on the public host (which has no rail keys, so it asks the `api` role for an account through an inbox, as storefront orders do), the matching service and its review screen.
- The template `invoice-follow-up` (Paystack only) is superseded by rail-agnostic invoice templates.
- The phase plans gain a workstream, T0 to T8, in the build plan.

## Provenance

From a discussion on 2026-10-23: the founder judged that Taskiem can do what Tally was meant to do, confirmed that lending is no longer a goal and that Tally has no users or banking partner, described the dynamic account flow (a button on the invoice, a countdown, a new number when it expires), and asked for the account number to be sendable on WhatsApp and Telegram. The same day the founder settled four questions: who pays the account fee is configurable, a business can turn on automatic matching, statements come in one common format, and VAT exports are not needed at launch. Rails offering dynamic accounts, as the founder listed them: iswallet, Flutterwave, Paystack, and most other money rails; each rail's behaviour is to be confirmed from its public documentation (T0).
