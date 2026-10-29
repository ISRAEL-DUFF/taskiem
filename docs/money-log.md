# The money log

The money log is a business's record of every movement of money on its connected payment services: payments received, credits to its accounts, payouts and what became of them, refunds to customers, and sales on its POS terminals. Taskiem writes each movement once and nobody changes it, following [decision 0045](decisions/0045-tally-in-taskiem.md). It is what invoices, matching and summaries will be built on ([build plan](spec/invoices-and-money-log.md), T2).

## What is recorded

Every event a connected payment service sends Taskiem in its prod connections, whether or not a workflow listens to it:

| Event | Entry |
| --- | --- |
| `payment.received` | Money in, a payment, settled |
| `payment.returned` | Money in that the service sent back to the payer (paid late, or not the amount asked), reversed |
| `virtual_account.credited` | Money in to a customer's account number (a student's, for a school), settled: what the payer sent, the service's fee where it reports one, and the payer's name and account where given. The entry's reference is the account's; each payment into it is its own entry (known by the service's id for the credit) |
| `payout.settled`, `payout.failed`, `payout.reversed` | Money out, a payout, with its outcome |
| `refund.pending`, `refund.processed`, `refund.failed` | Money out, a refund of a payment to the customer: pending, settled (it went out) or failed (the money stayed). The entry's reference is the payment refunded; its note names it, with the service's own refund reference when the event gives one. Paystack sends these |
| `pos.sale` | A sale on a POS terminal (Moniepoint, OPay Business): approved, pending, declined or reversed, with its fee where the service gives one |

Payments that Taskiem finds by asking the payment service (an invoice paid into its one-time account number) are recorded too, as the same entry the service's own event would make.

A payout that a workflow makes (pay a bank account, a wallet or a mobile money number) and that the service answers as settled there and then is recorded when the step finishes, as the same entry the service's `payout.settled` event would make, so the event, if it comes later, adds nothing. A payout answered pending is recorded when its event comes; one answered failed moved no money and is not recorded.

Sandbox and staging connections are not recorded: the log is the business's real money.

## How each movement is recorded once

A movement is known by where it happened (the connection, such as `paystack/main`), what was heard (the event) and the service's reference. A credit to a customer's account number is known by the account's reference and the service's id for that credit, since one account takes many payments. The same event sent twice, sent and also found by polling, or found by asking, is one entry. A later status is a new entry that names the first in `corrects`: a payout that settled and was then reversed has two entries.

If Taskiem cannot record an event, it answers the payment service with an error, so the service sends it again.

## Hearing from each connection

- **Webhooks:** on the Connections page, each prod connection of a payment service shows its address under **Webhook for the money log**, with where to paste it in the service's dashboard. Paystack takes one address per account: paste the payments one, which also carries refunds; transfers still arrive because Taskiem checks Paystack every few minutes.
- **Polling:** for Paystack, Taskiem also asks for new payments and transfers every few minutes, for every connected account, so a business that never set the webhook up is still recorded. The first look starts when the connection is made; nothing earlier is replayed. Refunds are not polled: they arrive only through the webhook.

## Accounts paid

Taskiem remembers the bank accounts a business has paid, so an approval to pay one it has never paid can say so. A changed account number on a supplier's invoice, or a new staff member's account slipped into payroll, is a common way money goes astray; the warning asks the person approving to check before the money leaves.

- **What is remembered:** each account a workflow's payout step (`pay_bank`, and each account in a `pay_many` batch) sent money to in prod, whether the service answered settled or pending, with the bank's code. One the service answered failed moved no money and is not remembered, nor is an account in a batch whose own transfer failed. Sandbox and staging payouts are not remembered.
- **How:** not the account number. Each account is kept as a pseudonym made from its digits with the business's own key, the way personal data is sealed, so the table means nothing without that key and another business's payments say nothing about this one's (`payout_accounts`).
- **The warning:** an approval in prod whose subject names an account (`account_number`, else `nuban` or `account`) that the business has not paid shows **First payment to this account**: a chip on its card on Approvals, the line under it in Home's Needs you, and a note in the confirm sheet. Where both the approval and the remembered payment have a bank's code, they must agree: the same ten digits at another bank are another account. `GET /v1/approvals` gives it as `first_payment`.
- **Not yet:** payouts made before this was added are not remembered, so the first approval to each account after it says first payment; payments made outside Taskiem (the bank's own app) are not known; WhatsApp and Telegram approvals do not show the warning yet.

## Where to see it

The log is copied to a data table named `money_log`, a [kept table](data-tables.md#kept-tables): it can be read, searched and exported like any table, and read by workflows and templates, but no person or workflow can add, change or delete its rows through Taskiem. Copying happens within a minute of the entry being recorded; a business without data tables yet keeps its entries until it has them.

| Column | Holds |
| --- | --- |
| `entry_id` | The entry's id |
| `occurred_at`, `recorded_at` | When it happened at the service (when the service says), and when Taskiem recorded it |
| `direction` | `in` or `out` |
| `kind` | `payment`, `account_credit`, `payout`, `refund`, `pos_sale`, `statement`, `cash`, `expense` (and, later, `correction`) |
| `amount_kobo`, `fee_kobo`, `currency` | Amounts in kobo |
| `status` | `settled`, `pending`, `failed` or `reversed` |
| `channel` | The connection, such as `paystack/main` |
| `reference` | The payment service's reference |
| `payer` | The payer's name and account, where the service gives them (personal data) |
| `source` | `event` (the service told Taskiem), `lookup` (Taskiem asked), `step` (a workflow's payout the service answered as settled at once), `upload` (a statement) or `entered` (typed in) |
| `note`, `corrects` | A short explanation, and the entry a later status refers to |

To read a movement's current status, take its latest entry by `recorded_at`.

The log's own copy in Taskiem's database is the record of reference: the `money_log` table is a copy for reading. A business that brings its own Xenibase project can still edit the table there, outside Taskiem; the copy in Taskiem is unaffected.

## Statements, cash and expenses

**Statement upload.** A bank statement is uploaded as a CSV in one common format: the columns `date`, `description`, `amount` (money in positive, money out negative, in naira), and optionally `reference` and `balance`. A statement in a bank's own columns is uploaded once its columns are matched to these; the match is remembered for that account, so the next upload needs nothing. Banks that put money in and money out in separate columns are matched as such. Dates are read day first (23/10/2026, 23-Oct-2026) or as 2026-10-23; amounts as banks write them (1,234.50, (1,234.50), 1,234.50 DR). A file with any line Taskiem cannot read adds nothing, and says which lines and why. A line already in the log (the same account, date, amount and reference) is skipped, so uploading overlapping statements is safe. Each line becomes a `statement` entry on the account named at upload, such as `gtbank/current`, attributed to the person who uploaded it. Statement lines are matched to invoices like any money in ([matching](matching.md)).

**Cash and expenses.** Cash in, cash out and expenses are entered with an amount, a date, what it was for, and for an expense the vendor and a category; each is recorded once, attributed to the person. Over the API: `POST /v1/money/statements` and `POST /v1/money/entries` (with an `Idempotency-Key`).

### From chat

Members with `invoice.manage` can record cash and expenses from WhatsApp or Telegram: `spent 12,000 on diesel at Total`, `expense 5000 fuel`, `cash in 20,000 till`, `cash out 3,000 float`. Taskiem shows the entry and records it on **yes**, once ([WhatsApp commands](whatsapp.md#for-people-using-taskiem-on-whatsapp)). In Pidgin, Yoruba, Hausa or Igbo, where that language is on, its own words work too, followed at once by the amount (`I spend 12,000 for diesel`, `mo ná 12,000 fún diesel`, `kuɗi ya shigo 20,000`, `ego pụrụ 3,000`); they are unreviewed drafts ([the words](whatsapp.md#invoices-and-money-in-other-languages)). Categories come from English words only.

The money that came in and the money that went out are also checked for [leaks](leaks.md).

**From stock.** A sale paid in cash ([stock](stock.md#sales)) is a `cash` entry in, recorded in the sale's transaction and naming it; a sale by transfer or on the terminal is matched to the money the rail reports. A payment to a supplier for a purchase order is an `expense` with the supplier as its vendor, the category `stock` and the order in its description.

## Money

**Money** in the web app's sidebar is for anyone who may see invoices (`invoice.read`); assigning, closing, uploading and entering need `invoice.manage`. It has five tabs:

- **Overview** (the landing tab, at `/money`): the period's money and the exports; see [Overview and exports](#overview-and-exports) below. The leaks moved to `/money/leaks`; old links to `/money?status=…` still lead there.
- **Leaks:** what Taskiem found the business may be losing ([leaks](leaks.md)), with the number open, their amount and the total recovered at the top. Each leak says what it is in words, with its amount, the day it was opened and a link to its invoice when it has one. It can be assigned to a member (people who cannot list members can assign it to themselves) and closed as **recovered** (with the amount got back), **written off** or **not a leak**, with an optional note. The filter shows open, closed or all; a closed leak says how it was closed, by whom and the amount recovered, and one whose cause went away (the money was matched, the invoice paid) says it closed by itself.
- **Upload statement:** a bank statement as a CSV for an account Taskiem has no connection to, named like `gtbank/current`. A file in the common format (`date`, `description`, `amount` with money out negative, and optionally `reference` and `balance`) goes straight in. A file with the bank's own column names shows a form to match them once, guessed from their names: one amount column, or money in and money out apart. The match is remembered for the account. A file with any line Taskiem cannot read adds nothing and lists each line and why. Afterwards the page says how many lines were added and how many skipped as already in the log, so the same days can be uploaded again safely.
- **Cash and expenses:** an expense (with an optional vendor and category), cash in or cash out, with its amount, date and description, and optionally the account it moved through (cash when empty). Each entry is recorded under the person's name, once per form even if it is sent twice.
- **Fees:** the fee agreed with each connection (`paystack/main`): a percent of the amount, a flat fee, or both, with an optional cap. A fee above the agreed one becomes a leak.

### Overview and exports

The **Overview** sums up a period: today (the default), yesterday, this week (from Monday), this month, last month, or two dates chosen by hand, at most 400 days. Days are Lagos days, and the last day is included. It shows:

- **Money in** and **money out**, settled, with what is still pending when there is any.
- **Fees**, with the effective rate: the fees over the money they were taken on, as a percent. It is left out when no fee was reported.
- **Invoices paid** in the period: how many, and what was paid on them.
- **Leaks:** how many were opened in the period, how many are open now with their amount, and the total recovered so far, with a link to the Leaks tab.
- **By channel:** each account (`paystack/main`, `gtbank/current`, cash) with its money in, pending, out, fees, fee rate and the number of movements.

Under the figures, **Exports** download the period as CSV files: the **money log**, the **invoices** sent, and an **accounting journal**, double-entry lines a bookkeeping tool can import, whose debits equal credits. Someone who may see personal data (`pii.reveal`) can tick **Invoices with customers' names** to add each customer's name to the invoices export; that access is audited. The files and their columns are described in [money reports](money-reports.md).

## Not yet recorded

- Refunds on services other than Paystack: Flutterwave's documentation shows a refund webhook body without an event name, and OPay's callbacks are not read yet.
- Two partial refunds of one payment that reach the same status are one entry: Paystack's refund events name the payment, and its own refund reference is empty while a refund is pending.
- A refund does not change what an invoice shows as paid; a refunded invoice payment stays matched.
- Payouts made in one batch (`pay_many`) and answered settled at once, and payouts settled when a workflow looks them up with no event after.
- Matching payments to invoices and orders (T5).
