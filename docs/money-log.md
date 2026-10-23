# The money log

The money log is a business's record of every movement of money on its connected payment services: payments received, credits to its accounts, payouts and what became of them, and sales on its POS terminals. Taskiem writes each movement once and nobody changes it, following [decision 0045](decisions/0045-tally-in-taskiem.md). It is what invoices, matching and summaries will be built on ([build plan](spec/invoices-and-money-log.md), T2).

## What is recorded

Every event a connected payment service sends Taskiem in its prod connections, whether or not a workflow listens to it:

| Event | Entry |
| --- | --- |
| `payment.received` | Money in, a payment, settled |
| `payment.returned` | Money in that the service sent back to the payer (paid late, or not the amount asked), reversed |
| `virtual_account.credited` | Money in to one of the business's accounts, settled, with the payer's name |
| `payout.settled`, `payout.failed`, `payout.reversed` | Money out, a payout, with its outcome |
| `pos.sale` | A sale on a POS terminal (Moniepoint, OPay Business): approved, pending, declined or reversed, with its fee where the service gives one |

Payments that Taskiem finds by asking the payment service (an invoice paid into its one-time account number) are recorded too, as the same entry the service's own event would make.

Sandbox and staging connections are not recorded: the log is the business's real money.

## How each movement is recorded once

A movement is known by where it happened (the connection, such as `paystack/main`), what was heard (the event) and the service's reference. The same event sent twice, sent and also found by polling, or found by asking, is one entry. A later status is a new entry that names the first in `corrects`: a payout that settled and was then reversed has two entries.

If Taskiem cannot record an event, it answers the payment service with an error, so the service sends it again.

## Hearing from each connection

- **Webhooks:** the payment service sends its events to Taskiem's webhook URL for the connection, set in the service's dashboard.
- **Polling:** for Paystack, Taskiem also asks for new payments and transfers every few minutes, for every connected account, so a business that never set the webhook up is still recorded. The first look starts when the connection is made; nothing earlier is replayed.

## Where to see it

The log is copied to a data table named `money_log`, a [kept table](data-tables.md#kept-tables): it can be read, searched and exported like any table, and read by workflows and templates, but no person or workflow can add, change or delete its rows through Taskiem. Copying happens within a minute of the entry being recorded; a business without data tables yet keeps its entries until it has them.

| Column | Holds |
| --- | --- |
| `entry_id` | The entry's id |
| `occurred_at`, `recorded_at` | When it happened at the service (when the service says), and when Taskiem recorded it |
| `direction` | `in` or `out` |
| `kind` | `payment`, `account_credit`, `payout`, `pos_sale` (and, later, `refund`, `statement`, `cash`, `expense`, `correction`) |
| `amount_kobo`, `fee_kobo`, `currency` | Amounts in kobo |
| `status` | `settled`, `pending`, `failed` or `reversed` |
| `channel` | The connection, such as `paystack/main` |
| `reference` | The payment service's reference |
| `payer` | The payer's name and account, where the service gives them (personal data) |
| `source` | `event` (the service told Taskiem) or `lookup` (Taskiem asked) |
| `note`, `corrects` | A short explanation, and the entry a later status refers to |

To read a movement's current status, take its latest entry by `recorded_at`.

The log's own copy in Taskiem's database is the record of reference: the `money_log` table is a copy for reading. A business that brings its own Xenibase project can still edit the table there, outside Taskiem; the copy in Taskiem is unaffected.

## Not yet recorded

- Payouts a service reports as settled at once, with no event after.
- Refunds, statement uploads, cash and expenses (T6).
- Matching payments to invoices and orders (T5).
