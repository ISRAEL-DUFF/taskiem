# Money reports

What a business's money came to: every morning in chat, every month by email, and at any time on the web app's Money page, with the period's [money log](money-log.md), invoices and an accounting journal to download. This follows [decision 0045](decisions/0045-tally-in-taskiem.md) and the [build plan](spec/invoices-and-money-log.md) (§9.2, §9.3, T7). Days and months are Lagos days and months.

**Each movement is counted once, at its latest status.** A movement can have several entries in the log (a payout that settled, then was reversed); the reports take its latest entry, with the first entry's amount when a later one gives none. A movement belongs to the period in which it happened (its first entry's time). So a payout reversed the next day shows as reversed, not as money out, and a pending payment that settled shows as settled.

## The daily summary in chat

Every morning from 08:00 Lagos, each **owner** of a business who has linked WhatsApp or Telegram gets yesterday's money, on WhatsApp if they linked both:

> [Ada Foods] Yesterday: ₦300,000 in (₦20,000 pending) — Paystack ₦250,000, Moniepoint POS ₦50,000; ₦12,000 out; 1 invoice paid (₦250,000); 1 leak opened (₦7,777).
>
> Money page: https://app.taskiem.com/money

- **In** is money in that settled, by channel, the largest first (at most five named); **pending** is money in still on its way. **Out** is money out that settled. Invoices paid are those paid in full that day, with what was paid on them; leaks are those [opened](leaks.md) that day.
- **Nothing to say, nothing sent.** A day with no movement, no invoice paid and no leak opened sends no message.
- **Once.** Each business's summary for a day is taken once (`money_digests`, migration 00205) in the transaction that queues it, so a restart or a second scheduler never sends it twice. It goes through the chat [outbox](whatsapp.md#delivery-and-retries) (kind `money_daily`, naming the summary and the owner) and is worded when sent, in the owner's language, from the log as it then stands.
- **Only owners.** Members with other roles, including those who can read invoices, get no summary; they see the Money page.
- **WhatsApp's 24-hour window.** Inside it the summary is a text message; outside it, it goes as the approved alert template (`taskiem_alert`): "[Ada Foods] Yesterday: ₦300,000 in … Details: <link>", the line cut at 200 characters, counted against the business's template allowance like other alerts. Telegram has no window. A summary that could not go out within a day is dropped, as is one for a person who is no longer an owner.

There is no setting to turn the summary off yet: it would be a "Money" row in Settings > Notifications, which the web app does not show yet.

## The monthly email

On the 1st of each month from 09:00 Lagos (or on the 2nd or 3rd, if Taskiem's scheduler was down on the 1st), each owner with an email gets last month's money:

- Subject: **[Ada Foods] money in October 2026**.
- Money in and out, settled and pending; the fees and the effective fee rate; each channel's money in, out and fees; invoices paid; leaks opened, leaks open now and the total recovered.
- A link to the Money page, where the month's log, invoices and journal download as spreadsheets (CSV). The email carries no attachments.

A month with no movement, no invoice paid and no leak opened sends no email. The email is sent once per business and month in the same way as the daily summary; when no owner's email could be sent, it is tried again at the next look. It needs the deployment's email (`TASKIEM_SMTP_URL` and `TASKIEM_ALERT_FROM`, as for alerts); without it no monthly email is sent and the scheduler says so when it starts.

## The Overview

**Money > Overview** in the web app (`invoice.read`) shows the period you pick (today, yesterday, this week from Monday, this month, last month, or dates you choose): the totals (money in and out, settled and pending, fees and the effective fee rate, invoices paid, leaks opened, open now and recovered), each channel's money, and the three downloads below for the same period; the invoices download names customers only for people with `pii.reveal` who tick **with customers' names**. It reads `GET /v1/money/summary` with `?from=2026-10-01&to=2026-10-31` (Lagos days, both included; at most 400 days) or `?month=2026-10`, and today with neither.

The **effective fee rate** is the fees taken over the settled money in they were taken on, so channels that report no fee do not lower it.

## The exports

`GET /v1/money/exports/{log|invoices|journal}` (`invoice.read`), with the same period, downloads a CSV file named like `journal-2026-10-01-to-2026-10-31.csv`. Amounts are in naira with two decimals (`25000.00`), dates are Lagos days (`2026-10-23`).

### The money log

One line per movement, at its latest status; money in positive, money out negative.

| Column | |
| --- | --- |
| `date` | When it happened |
| `entry_id` | The movement (its first entry) |
| `direction` | `in` or `out` |
| `kind` | `payment`, `account_credit`, `pos_sale`, `payout`, `refund`, `fee`, `statement`, `cash`, `expense` or `correction` |
| `status` | Its latest: `settled`, `pending`, `failed` or `reversed` |
| `amount` | Positive in, negative out |
| `fee` | The fee taken on it, if known |
| `currency` | `NGN` |
| `channel` | `paystack/main`, `moniepoint_pos/shop`, `cash`, … |
| `reference` | The rail's reference |
| `description` | What was typed or the statement said |
| `vendor`, `category` | For expenses |
| `source` | `event` (the rail told Taskiem), `lookup` (Taskiem asked), `upload` (a statement) or `entered` (typed in) |
| `invoice` | The invoice numbers it is matched to, if any |

### Invoices

The invoices **sent** in the period, whatever they are now.

| Column | |
| --- | --- |
| `number` | `INV-0104` |
| `status` | `sent`, `part_paid`, `paid`, `overdue`, `void` |
| `customer` | Empty, unless someone with `pii.reveal` asks with `?reveal=true` (audited). Phones and emails are never exported |
| `sent`, `due` | Days |
| `total`, `paid`, `still_due` | `still_due` is 0 for a void invoice |
| `paid_on`, `voided_on` | Days, when so |

### The accounting journal

The period's **settled** movements as a double-entry journal, in the shape bookkeeping tools import: `date`, `journal_id` (the movement), `account`, `debit`, `credit`, `description`, `reference`. Each movement is two lines whose debit and credit balance, and two more when a fee was taken:

| Movement | Debit | Credit |
| --- | --- | --- |
| Money in matched to an invoice | `Bank: <channel>` | `Accounts receivable` |
| A POS sale | `Bank: <channel>` | `Sales` |
| Cash put in | `Cash` | `Owner's cash movements` |
| Other money in | `Bank: <channel>` | `Money in to identify` |
| A payout | `Payouts` | `Bank: <channel>` |
| An expense | `Expenses: <Category>` | `Bank: <channel>` (or `Cash`) |
| A refund | `Refunds` | `Bank: <channel>` |
| Cash taken out | `Owner's cash movements` | `Cash` |
| A statement line going out | `Money in to identify` | `Bank: <channel>` |
| The fee on any of these | `Bank and payment fees` | `Bank: <channel>` |

The channel `cash` is the account `Cash`; any other is `Bank: ` and the channel. A movement matched to an invoice is described as "Invoice INV-0104".

### Formulas are not run

A text cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return, which a spreadsheet would run as a formula, is written with a leading apostrophe (`'=SUM(A1)`), as table exports do: statement lines, references and descriptions are not Taskiem's words. Amounts are numbers and are left as they are.
