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

- **Webhooks:** on the Connections page, each prod connection of a payment service shows its address under **Webhook for the money log**, with where to paste it in the service's dashboard. Paystack takes one address per account: paste the payments one; transfers still arrive because Taskiem checks Paystack every few minutes.
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

## Money

**Money** in the web app's sidebar is for anyone who may see invoices (`invoice.read`); assigning, closing, uploading and entering need `invoice.manage`. It has four tabs:

- **Leaks:** what Taskiem found the business may be losing ([leaks](leaks.md)), with the number open, their amount and the total recovered at the top. Each leak says what it is in words, with its amount, the day it was opened and a link to its invoice when it has one. It can be assigned to a member (people who cannot list members can assign it to themselves) and closed as **recovered** (with the amount got back), **written off** or **not a leak**, with an optional note. The filter shows open, closed or all; a closed leak says how it was closed, by whom and the amount recovered, and one whose cause went away (the money was matched, the invoice paid) says it closed by itself.
- **Upload statement:** a bank statement as a CSV for an account Taskiem has no connection to, named like `gtbank/current`. A file in the common format (`date`, `description`, `amount` with money out negative, and optionally `reference` and `balance`) goes straight in. A file with the bank's own column names shows a form to match them once, guessed from their names: one amount column, or money in and money out apart. The match is remembered for the account. A file with any line Taskiem cannot read adds nothing and lists each line and why. Afterwards the page says how many lines were added and how many skipped as already in the log, so the same days can be uploaded again safely.
- **Cash and expenses:** an expense (with an optional vendor and category), cash in or cash out, with its amount, date and description, and optionally the account it moved through (cash when empty). Each entry is recorded under the person's name, once per form even if it is sent twice.
- **Fees:** the fee agreed with each connection (`paystack/main`): a percent of the amount, a flat fee, or both, with an optional cap. A fee above the agreed one becomes a leak.

## Not yet recorded

- Payouts a service reports as settled at once, with no event after.
- Refunds.
- Matching payments to invoices and orders (T5).
