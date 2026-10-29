# Money reports

What a business's money came to: every morning in chat, every month by email, and at any time on the web app's Money page, with the period's [money log](money-log.md), invoices and an accounting journal to download. This follows [decision 0045](decisions/0045-tally-in-taskiem.md) and the [build plan](spec/invoices-and-money-log.md) (§9.2, §9.3, T7). Days and months are Lagos days and months.

**Each movement is counted once, at its latest status.** A movement can have several entries in the log (a payout that settled, then was reversed); the reports take its latest entry, with the first entry's amount when a later one gives none. A movement belongs to the period in which it happened (its first entry's time). So a payout reversed the next day shows as reversed, not as money out, and a pending payment that settled shows as settled.

## The daily summary in chat

Every morning from 08:00 Lagos, everyone who [gets it](#who-gets-them) and has linked WhatsApp or Telegram gets yesterday's money, on WhatsApp if they linked both:

> [Ada Foods] Yesterday: ₦300,000 in (₦20,000 pending) — Paystack ₦250,000, Moniepoint POS ₦50,000; ₦12,000 out; 1 invoice paid (₦250,000); 1 leak opened (₦7,777).
>
> Money page: https://app.taskiem.com/money

- **In** is money in that settled, by channel, the largest first (at most five named); **pending** is money in still on its way. **Out** is money out that settled. Invoices paid are those paid in full that day, with what was paid on them; leaks are those [opened](leaks.md) that day.
- **Riders.** A business with [riders](dispatch.md#money) gets a line for each rider who delivered, took cash, handed cash in or was rated that day: "Musa: 4 delivered, ₦8,500 paid at the door, ₦3,000 cash, owes ₦3,000, rated 4.5 of 5".
- **Sales.** A business [keeping stock](stock.md#sales) gets a line for the sales it recorded that day: "Sales you recorded: ₦310,000 from 3 sales (Cash ₦30,000 · Transfer ₦280,000)." Its evening total of the day's sales is sent after closing, through the same outbox (kind `sales_evening`).
- **Nothing to say, nothing sent.** A day with no movement, no invoice paid and no leak opened sends no message.
- **Once.** Each business's summary for a day is taken once (`money_digests`, migration 00205) in the transaction that queues it, so a restart or a second scheduler never sends it twice. Who gets it is decided then: someone who turns the daily summary on later that day gets the next day's, not that day's. It goes through the chat [outbox](whatsapp.md#delivery-and-retries) (kind `money_daily`, naming the summary and the person) and is worded when sent, in the person's language, from the log as it then stands.
- **WhatsApp's 24-hour window.** Inside it the summary is a text message; outside it, it goes as the `taskiem_daily_summary` [template](whatsapp.md#templates), "[Ada Foods] Yesterday's money, 23 Oct 2026: ₦300,000 in … Tap Open for the Money page." with an **Open** button, once Meta has approved it in the number's account; until then as the alert template (`taskiem_alert`): "[Ada Foods] Yesterday: ₦300,000 in … Details: <link>", the line cut at 200 characters. Either is counted against the business's template allowance like other alerts. Telegram has no window. A summary that could not go out within a day is dropped, as is one for a person who has since turned it off or can no longer read invoices.

## The monthly email

On the 1st of each month from 09:00 Lagos (or on the 2nd or 3rd, if Taskiem's scheduler was down on the 1st), everyone who [gets it](#who-gets-them) and has an email gets last month's money:

- Subject: **[Ada Foods] money in October 2026**.
- Money in and out, settled and pending; the fees and the effective fee rate; each channel's money in, out and fees; invoices paid; leaks opened, leaks open now and the total recovered.
- A link to the Money page, where the month's log, invoices and journal download as spreadsheets (CSV). The email carries no attachments.

A month with no movement, no invoice paid and no leak opened sends no email. The email is sent once per business and month in the same way as the daily summary, to those who get it when the month is taken; when no one's email could be sent, it is tried again at the next look. It needs the deployment's email (`TASKIEM_SMTP_URL` and `TASKIEM_ALERT_FROM`, as for alerts); without it no monthly email is sent and the scheduler says so when it starts.

## Who gets them

Each person chooses under **Settings > Notifications**, in the **Money summaries** row: **Daily money summary (chat)** and **Monthly money email**, each on its own (`PUT /v1/me/notification-settings` with `{"settings": [{"group": "money", "chat": false, "email": true}]}`: `chat` is the daily summary, `email` the monthly email; see [notifications](alerts.md#notifications-with-nothing-set-up)).

- **Owners** get both unless they turn them off.
- **Anyone else who can read invoices** (`invoice.read`: the admin, operator, auditor and viewer roles, and custom roles that include it) gets neither unless they turn them on.
- **People who cannot read invoices** do not see the row and never get either, whatever was chosen before: a person who loses `invoice.read` stops getting them, and a daily summary already queued for them is dropped. Taskiem checks when it takes the day or month, and again when the chat message goes.
- Only active members: a person whose account is disabled gets nothing.

The choices are per business (a person in two businesses chooses for each), kept with the other notification settings (`notification_settings`, group `money`, migration 00206).

## The Overview

**Money > Overview** in the web app (`invoice.read`) shows the period you pick (Today, This week from Monday and This month are one press away; yesterday, last month and dates you choose are under **Other**): money in, money out and what was kept, with bars of money in and out for each day (each hour for a day) that show a day's figures when pressed, and a table of them under **See the figures**; where the money in came from, each account's share; what to look at (payments not matched, the leaks, the fees and the effective fee rate, invoices paid, money collected for others); each channel's money; and the three downloads below for the same period. The invoices download names customers only for people with `pii.reveal` who tick **with customers' names**. It reads `GET /v1/money/summary` with `?from=2026-10-01&to=2026-10-31` (Lagos days, both included; at most 400 days) or `?month=2026-10`, and today with neither.

**Home** shows the same summary for today on its money card: money in, how many payments, a line of the money in through the day (from 6 am, or the first payment if earlier, to now), money out, and the payments waiting to be matched (`GET /v1/money/unmatched`).

**The period in stretches.** The summary's `buckets` split the period for the charts: an hour each for a period of one day (`unit: "hour"`, 24 of them), a Lagos day each for up to 92 days (`unit: "day"`), and none for longer (`unit: ""`). Each has its settled money `in_settled_kobo` and `out_settled_kobo` and the number of settled `payments` in. A movement counts in the stretch of its first entry, as it counts in the period, so the stretches add up to the period's settled totals.

**Riders.** For a business with [riders](dispatch.md), the summary's `riders` lists each rider with deliveries, cash or a rating in the period: `delivered`, `door_transfer_kobo` (paid by transfer at the door), `cash_kobo`, `remitted_kobo` (handed in, or taken off their pay), `fees_kobo`, the `rating` that period and what they `owed_kobo` now. The Overview shows them under **Riders**.

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
| `source` | `event` (the rail told Taskiem), `lookup` (Taskiem asked), `step` (a workflow's payout the rail answered as settled at once), `upload` (a statement) or `entered` (typed in) |
| `invoice` | The invoice numbers it is matched to, if any |
| `order` | The storefront orders it is matched to, if any |

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
| `pass_through` | What of the total is collected for others ([pass-through](#pass-through)) |
| `pass_through_for` | For whom, separated by `; ` (`WAEC; NECO`) |
| `pass_through_paid` | How much of what was paid is theirs (the business's own lines are paid first) |
| `amended` | The net of the [amendments](invoices.md#amending-a-sent-invoice) made after it was sent (negative for a discount) |
| `student`, `class`, `term` | A school's term invoice: the student's admission number (no name), their class and the term ([Schools](schools.md#invoicing-a-term)); empty otherwise |

### The accounting journal

The period's **settled** movements as a double-entry journal, in the shape bookkeeping tools import: `date`, `journal_id` (the movement), `account`, `debit`, `credit`, `description`, `reference`. Each movement is two lines whose debit and credit balance, and two more when a fee was taken:

| Movement | Debit | Credit |
| --- | --- | --- |
| Money in matched to an invoice | `Bank: <channel>` | `Accounts receivable`, and `Collected for <body>` for its pass-through share |
| Money in matched to a storefront order | `Bank: <channel>` | `Sales` |
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

### Pass-through

An invoice line can be collected on someone else's behalf (`pass_through_for`, such as a school's WAEC fee, [decision 0046](decisions/0046-balans-in-taskiem.md) point 11): that money is owed to them, never the business's income. Every payment counted towards such an invoice is split between the business's own lines and the pass-through lines by one rule, **own lines first**: a payment counts towards the business's own lines until they are paid in full (after discounts), and only then towards the pass-through lines, in the order they appear on the invoice. A ₦120,000 invoice of ₦100,000 tuition and ₦20,000 for WAEC paid as ₦90,000 then ₦30,000 is ₦90,000 own, then ₦10,000 own and ₦20,000 WAEC's. The rule works in whole kobo with nothing to round, and never reports money as held for someone else while the business's own fees are still unpaid. Payments undone are left out, and the shares are worked out again from the payments that still count.

- **The Overview and `GET /v1/money/summary`**: `collected_for_others_kobo` is the pass-through share of the period's settled money in, `collected_for` the same by body, and `own_in_settled_kobo` the rest of the settled money in, the business's own.
- **The invoices export**: the `pass_through`, `pass_through_for` and `pass_through_paid` columns above.
- **The journal**: a movement's pass-through share is credited to `Collected for <body>` (`Collected for WAEC`) instead of `Accounts receivable`; the bank's debit is the same, so debits still equal credits.

### Formulas are not run

A text cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return, which a spreadsheet would run as a formula, is written with a leading apostrophe (`'=SUM(A1)`), as table exports do: statement lines, references and descriptions are not Taskiem's words. Amounts are numbers and are left as they are.
