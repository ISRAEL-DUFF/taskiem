# Invoices and the money log: specification and build plan

Status: **Proposed** · Decision: [0045](../decisions/0045-tally-in-taskiem.md) · Workstream **T0 to T8**, one pull request per milestone · Source: [Tally overview](../strategy/tally-overview.md)

What a small business needed from Tally, built in Taskiem: invoices that settle themselves, an account number per invoice that the customer asks for when they are ready to pay, one record of every naira that moved, payments matched to what they pay for, and the leaks and summaries that come from that record. Lending is out of scope.

Contents:

1. [Summary](#1-summary)
2. [Words used here](#2-words-used-here)
3. [Where we are](#3-where-we-are)
4. [One-time accounts: `collect_transfer`](#4-one-time-accounts-collect_transfer)
5. [Invoices](#5-invoices)
6. [Paying an invoice](#6-paying-an-invoice)
7. [The money log](#7-the-money-log)
8. [Matching](#8-matching)
9. [Leaks, summaries and exports](#9-leaks-summaries-and-exports)
10. [What comes from Tally](#10-what-comes-from-tally)
11. [Milestones](#11-milestones)
12. [Order and timing](#12-order-and-timing)
13. [Testing](#13-testing)
14. [Risks and open questions](#14-risks-and-open-questions)
15. [Out of scope](#15-out-of-scope)

---

## 1. Summary

| Tally feature | In Taskiem | Kind |
| --- | --- | --- |
| Invoices with a lifecycle, paid only by reconciliation | The invoice record and `invoices@1` (§5) | Platform |
| A dedicated account number per invoice | `collect_transfer` one-time accounts, asked for on the invoice page or in chat (§4, §6) | Capability, platform |
| Payment links | `collect_link`, on the same invoice page (§6) | Capability (built) |
| Overdue nudges, invoice sent and paid messages | Templates on scheduled triggers and `notify@1` (§5.4) | Template |
| Ingestion, dedup, raw payload kept | Rails' events and lookups, written to the money log (§7) | Platform (mostly built) |
| One ledger for every channel | The money log, a kept table (§7) | Platform |
| Statement upload for channels with no API | A form whose file is parsed into the log (§7.4) | Template, platform parser |
| Auto-matching, suggestions, confirm, reject, undo | Matching (§8) | Platform |
| Exceptions in naira | Checks on the log (§9.1) | Templates on the log |
| Dashboard, settled versus pending, take-rate | A daily summary in chat and a Money page (§9.2) | Template, web |
| Ledger, journal and VAT exports | Monthly exports (§9.3) | Template |
| Payables | Money-out rows from a form or chat (§7.4) | Template |
| Roles, audit, tenant isolation, kobo amounts | Already platform guarantees | Built |
| Working capital, analyst queue, BVN capture | Dropped (0045) | — |

## 2. Words used here

| Word | Meaning |
| --- | --- |
| **One-time account** | A bank account number a rail issues for one payment, which stops accepting new payments after a time (a dynamic or temporary virtual account) |
| **Permanent account** | An account number a rail issues to one customer for good (`virtual_account`) |
| **Invoice page** | The public page of one invoice, at `/i/<token>` on the form host |
| **Money log** | The kept table holding one row for every movement of the business's money (§7) |
| **Kept table** | A data table Taskiem writes and nobody can change or delete rows of through Taskiem (§7.1) |
| **Match** | A row saying which part of a payment pays which invoice, order or other thing (§8) |
| **Leak** | A shortfall the log can show: an unmatched payment, a fee above the agreed one, a duplicate (§9.1) |

## 3. Where we are

| Piece | State | Where |
| --- | --- | --- |
| `collect_link`, `get_payment`, `virtual_account` capabilities and `payment.received`, `virtual_account.credited` events | Built (R1) | `schemas/capabilities-v1.json` |
| A trigger on rails' normalised payment events, and `await_payment` | Built (R4) | [wd-v1](../contracts/wd-v1.md) |
| `payment-link-per-order`, `virtual-account-per-customer` (Moniepoint only), `transfer-received-notify` | Built (R4); credits to customers' accounts are told and logged, not matched | `templates/library/` |
| `invoice-follow-up` | Built earlier, Paystack only, invoices kept in another tool | `templates/library/invoice-follow-up.json` |
| Storefront orders: a platform record, an inbox from the public host, a status page, Paystack checkout | Built (S3) | [storefronts](../storefronts.md) |
| POS sales in data tables, inserted never merged | Built (0040, 0041) | `pos_sales` |
| Data tables | Built; every row editable with `table.edit` | [data tables](../data-tables.md) |
| Messages to customers on WhatsApp and Telegram after one tap; their vocabulary is STOP and START | Built (0039) | [messages](../messages.md) |
| One-time accounts, invoices, the money log, matching | **Missing** | This plan |

## 4. One-time accounts: `collect_transfer`

### 4.1 The capability

| | |
| --- | --- |
| Input | `amount_kobo`, `currency` (default NGN), `customer {name, email, phone}`, `description`, `expires_minutes` (wanted; the rail may shorten or lengthen it) |
| Output | `account_number` (`x-pii`), `bank_name`, `account_name` (`x-pii`), `reference`, `amount_kobo`, `expires_at` (as the rail set it) |
| Class | idempotent or reconcilable; unsafe allowed only when declared, as for `virtual_account` |
| Event | `virtual_account.credited`, carrying `reference`, the amount received and the payer as the rail reports them |
| Lookup | `get_payment` by `reference`, for rails whose event can be missed |

As with every capability, the rail's reference is derived from the step's idempotency key, so a retried request returns the same account instead of opening a second (§6.4).

### 4.2 First rails

iswallet, Flutterwave, Paystack and OPay first (OPay's temporary account is already noted in [rails and products §3.1](rails-and-products.md#31-what-each-rail-can-do-today)), then the rest as their documentation allows. For each, T0 records from the rail's **public documentation** (clean-room policy):

| Fact | Why it matters |
| --- | --- |
| The lifetimes it allows, and its default | `expires_at` and the countdown |
| What happens to a transfer that lands after expiry: refused and returned, credited, or held | §6.3 case 1 |
| Whether it refuses an amount that differs from the one asked, or credits any amount | §6.3 case 2 |
| Its fee per account issued or per payment, and any rate limit on issuing | The cap in §6.4 |
| The event or webhook for a credit, and the field carrying our reference | Exact matching (§8.1) |
| A lookup by reference | Recovering a missed event |

## 5. Invoices

### 5.1 The record

A platform record like a storefront order, in Taskiem's database, listed and exported from the web app.

| Field | Notes |
| --- | --- |
| `number` | Per business, `INV-0001` upwards, with an optional prefix the business sets |
| `customer` | Name, phone, email: sealed under the customer's personal-data subject |
| `lines[]` | Description, quantity, unit price in kobo; or a single amount |
| `total_kobo`, `currency` | NGN first |
| `due_on` | Optional |
| `paid_kobo` | Sum of the invoice's matches (§8); never written directly |
| `status` | §5.2 |
| `token` | For the invoice page; 24 random bytes, kept as a hash |
| `accounts[]` | Every one-time account issued for it (§6) |
| `connection` | The rail connection that takes its payments |

### 5.2 Statuses

| Status | Set by |
| --- | --- |
| `draft` | Creating it |
| `sent` | `invoices@1` `send`, or the web app's Send |
| `part_paid` | Matches summing to less than the total |
| `paid` | Matches summing to the total or more; more is also flagged as an overpayment (§9.1) |
| `overdue` | A sent or part-paid invoice past `due_on` (a daily check) |
| `void` | The owner, with a reason; a void invoice takes no new account numbers, and money that still arrives for it is flagged |

`part_paid` and `paid` come only from matches; a match undone moves the invoice back.

### 5.3 `invoices@1`

A platform connector, no connection needed, acting only for the organisation running the workflow: `create`, `send` (by SMS, email and, when the customer is reachable there, WhatsApp or Telegram, with the invoice page link), `get`, `void`, `notify_customer`, and the signal `invoice.paid` (correlated by number) for a run waiting on it. There is no action that sets a status.

### 5.4 Templates

| Template | Does |
| --- | --- |
| `send-invoice` | From chat ("invoice Ada 25,000 for 5 bags"), a form or the web: create and send, tell the owner when paid |
| `chase-overdue-invoices` | Daily: a reminder to each overdue customer, at most every three days, then the owner after three reminders. Supersedes `invoice-follow-up` |
| `invoice-paid-receipt` | On `invoice.paid`: a receipt to the customer, a note to the owner |

## 6. Paying an invoice

### 6.1 On the invoice page

The page (form host, the business's theme, [0042](../decisions/0042-public-page-themes.md)) shows the business, the lines, what is due and the status. It offers:

- **Pay by bank transfer.** The page writes a request to the inbox (the public host has no rail keys). The `api` role issues the account within a second with `collect_transfer` on the invoice's connection, for the amount still due, and the page shows the account number, bank, account name, amount and a countdown to `expires_at`. Reloading shows the same account while it is valid.
- **Get a new account number**, once the shown one has expired.
- **Pay with card or USSD**, where the connection offers `collect_link`: a payment page made once per amount due.
- After payment, the page says **Paid** (or how much is still due) as soon as the match is made. The page asks the server every few seconds while an account is shown.

### 6.2 In chat

When `send` reaches the customer on WhatsApp or Telegram ([0039](../decisions/0039-reaching-people-outside.md)), the message carries the amount due and two buttons: **Pay by transfer** and **Open invoice**. Pay by transfer answers with an account number, bank, account name, amount and "valid until 14:35"; when it has expired, **New account number** issues another. These buttons are the only addition to an outsider's vocabulary (0045, point 7). The invoice page also offers the one-tap link to get updates on WhatsApp or Telegram.

### 6.3 Cases

1. **A transfer lands after expiry.** Wherever the rail still credits it, it carries the account's reference and is matched to the invoice like any other. The countdown is never used to refuse or ignore money.
2. **The amount differs.** Less: the invoice is part-paid and the page and chat offer an account for the rest. More: paid, and the excess is flagged for the owner (refund or keep as credit).
3. **The customer paid the old account after taking a new one.** Both accounts belong to the invoice; both payments count, and any excess is case 2.
4. **The invoice is already paid or void.** No account is issued; the page and chat say so. Money that still arrives is matched (paid) or flagged (void).
5. **Many requests.** At most five accounts per invoice an hour and twenty in all; the current valid account is shown again rather than a new one issued.
6. **The rail's answer is lost.** The issue step is retried with the same idempotency key, so the rail returns the same account. A rail whose `collect_transfer` is unsafe parks the request for the operator and the page says to try again shortly.

### 6.4 Idempotency

The key for an account is derived from the invoice and a counter (`invoice:<id>:acct:<n>`), where `n` advances only when the previous account has expired or been used. A double tap or a retried request therefore returns the same account.

## 7. The money log

### 7.1 Kept tables

A data table can be marked **kept** by Taskiem. In a kept table:

- rows are written only by Taskiem's own writers (the money log, matching);
- the table page, the rows API, chat and workflows can read, search and export, but not add, change or delete rows; `table.edit` does not change that;
- the columns cannot be changed or the table deleted while the feature that keeps it is on;
- a business that brings its own Xenibase project could still edit rows there, outside Taskiem; the table's page says so.

### 7.2 `money_log`

One row per movement, inserted once, never updated:

| Column | Notes |
| --- | --- |
| `entry_id` | Taskiem's id, unique (the writer checks before inserting, as data tables offer no unique constraint) |
| `occurred_at`, `recorded_at` | When it happened at the rail; when Taskiem wrote it |
| `direction` | `in` or `out` |
| `kind` | `payment`, `account_credit`, `payout`, `refund`, `pos_sale`, `fee`, `statement`, `cash`, `expense`, `correction` |
| `amount_kobo`, `fee_kobo`, `currency` | Integer minor units |
| `status` | `settled`, `pending`, `failed`, `reversed`, as the rail reported at the time; a later status is a new row naming the first |
| `channel` | The connection (`paystack/main`) or `upload`, `cash` |
| `reference` | The rail's reference, or ours |
| `payer` | Name and account as the rail reports them; `personal` |
| `source` | `event`, `lookup`, `upload`, `entered` |
| `corrects` | The `entry_id` a correction or later status refers to |

Readers take the latest row per movement, as `pos_sales` readers do.

### 7.3 Where rows come from

- **Every connected rail's normalised events** (`payment.received`, `virtual_account.credited`, `payout.settled`, `.failed`, `.reversed`), whether or not a workflow listens. Each rail connection gets the log's own intake: a remote subscription where the rail allows it (as Xenibase does), polling where it offers lookups (as storefront payments poll Paystack), otherwise the URL on the connection page.
- **POS sales** from `moniepoint_pos@1` and `opay_business@1`, alongside the `pos_sales` rows they already write.
- **Payouts Taskiem makes** (Run payroll, Pay a list, refunds) through the same events.

### 7.4 Rows people add

- **Statement upload.** A form takes a bank or provider statement (CSV first; the formats of Nigeria's common banks from Tally's parsers); each line becomes a `statement` row, skipped when a row with the same channel, reference and amount exists.
- **Cash and expenses.** From a form or chat ("spent 12,000 on diesel"): `cash` or `expense` rows with a vendor and category, marked `entered` and attributed to the person.

## 8. Matching

### 8.1 Exact

A payment is matched without asking anyone when its reference is one Taskiem issued:

- a one-time account's reference: that invoice;
- a payment link's `payment_id`: that invoice or order;
- a customer's permanent account (`virtual_account`): that customer's oldest open invoice, then the next, with the rest flagged as credit.

### 8.2 Suggested

Any other money in (a bank transfer with "PAYMENT" as its narration, a POS sale, a statement line) gets up to three suggestions among open invoices and orders, ranked by Tally's features (amount, payer name against customer name, days since the invoice), each with its reason ("same amount, payer 'ADA OBI' matches the customer, two days after INV-0104"). Above a threshold the owner can turn on automatic matching; below it a person confirms in the web app or with one tap in chat. A rejected suggestion is not offered again.

### 8.3 The matches table

`money_matches`, a kept table: `match_id`, `entry_id`, `target` (`invoice:INV-0104`, `order:AK-7QX4KD`), `amount_kobo`, `how` (`exact`, `suggested`, `manual`), `by`, `reasons`, and `undoes` for an undo. An invoice's `paid_kobo` is the sum of its matches not undone.

## 9. Leaks, summaries and exports

### 9.1 Leaks

| Leak | First version |
| --- | --- |
| Money in with nothing to match, after a day | Yes |
| Invoice overdue | Yes |
| Duplicate (the same reference and amount twice on one channel) | Yes |
| Overpayment, and money for a void invoice | Yes |
| Fee above the agreed one, against a rate the owner sets per connection | Yes |
| Short or late settlement | Later: needs each rail's settlement report as a capability |
| Customer debited, business never credited | Later: rail by rail, where a lookup shows it |

Each leak is shown in naira, can be assigned, and is closed as recovered (with the amount), written off or not a leak. The total recovered is shown on the Money page.

### 9.2 Summaries

- **Daily in chat** to the owner: money in, settled and pending, by channel, invoices paid, leaks opened.
- **A Money page** in the web app: the period's money in and out, settled versus pending, by channel, the effective fee rate, open leaks and the amount recovered.

### 9.3 Exports

Monthly by email, and on demand from the Money page: the log, invoices, an accounting journal (Tally's format) and a VAT summary for VAT-registered businesses.

## 10. What comes from Tally

| From Tally | Used for |
| --- | --- |
| The eight exception definitions | §9.1 (five first) |
| The matching features and scoring | §8.2 |
| Fee and take-rate calculations | §9.1, §9.2 |
| Statement parsers | §7.4 |
| Journal and VAT export formats | §9.3 |
| Its invoice lifecycle and user stories | §5, and the acceptance cases of T3 to T6 |

Tally's code is the team's own and may be copied in; it is adapted to Taskiem's conventions (kobo, sealed personal data, the egress guard), with its tests.

## 11. Milestones

Sizes: S up to a week, M one to two weeks, L two to four weeks, for one engineer.

### T0: decisions and rail facts (people, alongside T1)

| Item | Owner | Blocks |
| --- | --- | --- |
| Accept decision 0045, including the amendment to 0039 | Founders | T4 |
| For iswallet, Flutterwave, Paystack and OPay, the facts of §4.2 from their public documentation | Engineering | T1 |
| Which bank statement formats to support first | Founders | T6 |
| Whether VAT exports are needed at launch | Founders | T7 |

### T1: `collect_transfer` (M)

The capability in `schemas/capabilities-v1.json`, its mapping for the first four rails, conformance cases from recorded fixtures, the SDK's types, and `GET /v1/capabilities` listing it.

**Done when:** each of the four rails passes conformance; a retried issue returns the same account; a credit to the account produces `virtual_account.credited` with its reference.

### T2: kept tables and the money log (L)

Kept tables in data tables; `money_log` and its writer; the log's intake per rail connection (remote subscription, polling or URL); POS rows; payouts Taskiem makes.

**Done when:** every event from a connected rail writes exactly one row whether or not a workflow listens; a replayed or duplicated event writes none; no person or workflow can change a row through Taskiem (tests on the page, the API, chat and the tables connector).

### T3: invoices (L)

The record, numbering, statuses, `invoices@1`, the Invoices pages (list, new, detail, void), and the `send-invoice` template.

**Done when:** an owner creates and sends an invoice from the web and from chat; no action or page can set `paid`; a voided invoice takes no new account.

### T4: paying an invoice (L; needs T1, T3)

The invoice page, the inbox request and issuing, the countdown and new numbers, `collect_link` on the page, the chat buttons and the 0039 amendment, the caps of §6.3.

**Done when:** a customer pays an invoice by transfer from the page and from WhatsApp on each T1 rail in sandbox; a transfer after expiry still pays the invoice; a sixth request in an hour shows the current account.

### T5: matching (M; needs T2, T3)

Exact matching, suggestions with reasons, confirm, reject and undo in the web app and chat, `money_matches`, invoice statuses from matches, the permanent-account rule.

**Done when:** a one-time account payment marks its invoice paid with no person involved; an unreferenced transfer gets a suggestion with its reason; an undo moves the invoice back and leaves both rows.

### T6: leaks, statements, cash and expenses (M; needs T5)

The first five leaks, their lifecycle and the amount recovered; statement upload for the first formats; cash and expense entry; `chase-overdue-invoices` and `invoice-paid-receipt`.

### T7: summaries and exports (M)

The daily summary, the Money page, monthly exports (log, invoices, journal, VAT).

### T8: templates and AI (S)

Start page entries, the AI builder's knowledge of invoices and the log (an `invoice` request uses `invoices@1`, never a data table with a status column), eval cases, `invoice-follow-up` superseded.

## 12. Order and timing

| Order | Milestone | Why here |
| --- | --- | --- |
| 0 | T0 | Rail facts decide T1's mappings and §6.3 |
| 1 | T1 | Small, and everything about paying depends on it |
| 2 | T3 | The record the page and matching need |
| 3 | T4 | The visible feature: an invoice the customer pays by transfer |
| 4 | T2 | The log; can run beside T3 and T4 with a second engineer |
| 5 | T5 | Needs the log and invoices |
| 6 | T6, T7 | Built on the log and matches |
| 7 | T8 | Last, over what exists |

About 12 to 16 weeks for one engineer; 8 to 10 for two, one on T1, T3, T4 and one on T2, T5.

## 13. Testing

| Level | What | Where |
| --- | --- | --- |
| Capability conformance | `collect_transfer` on each rail's fixtures; the event carries the reference | `engine/conntest`, `connectors/<rail>/testdata` |
| Exactly once | A lost answer when issuing returns the same account; an event delivered twice writes one log row and one match | `engine`, `e2e/` with rail fakes |
| Kept tables | No write through the page, API, chat or the tables connector; corrections as new rows | `api`, `engine/datatables` |
| Invoices | Statuses only from matches; void; numbering under concurrency | `api` |
| Paying | Expired account still pays; underpayment, overpayment, two accounts, paid and void invoices; caps | `e2e/`, Playwright for the page |
| Chat | Buttons work only for the contact's own open invoice of that business; other text gets the fixed answer | `engine/reach`, `api` |
| Matching | Exact, suggested with reasons, reject remembered, undo | `engine` |
| Templates | Every new template on every capable rail | `templates/templates_test.go` |

## 14. Risks and open questions

| Risk or question | Mitigation or owner |
| --- | --- |
| A rail refuses transfers after expiry and returns them slowly; the customer thinks they paid | The page and chat say the time clearly, and a refused late transfer is the rail's to return; T0 records each rail's behaviour and the help text names it |
| A rail charges for each account issued | The caps of §6.3; T0 records the fee; the owner is told which rail is cheaper per payment |
| WhatsApp needs an approved template for an invoice with buttons outside the 24-hour window | A utility template submitted in T4; until approved, the message goes by SMS with the page link |
| Kept tables in a business's own Xenibase project can be edited there | Said on the table's page; the guarantee is against edits through Taskiem (0045) |
| Matching a permanent-account payment to the wrong invoice | Oldest first is the rule; an undo moves it, and the owner can match by hand |
| Who bears the account fee: the business, or added to the invoice | Founders, T0 |
| Whether a business can turn on automatic matching of suggestions, and at what threshold | Founders, before T5 |

## 15. Out of scope

Working capital and anything a lender needs, double-entry accounting, tax filing, an accounting system or ERP (exports only), holding customers' money, and multi-currency invoices beyond NGN at first.
