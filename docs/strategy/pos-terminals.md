# POS terminals: the problem, and what we are building to solve it

Date: 21 October 2026 · Status: **building** (Moniepoint first; OPay once its API questions are answered)

## 1. The problem

Most Nigerian shops, stores, salons, pharmacies, restaurants and market traders are paid through a POS terminal: Moniepoint and OPay above all, then PalmPay and the banks' own. The terminal is the till. Cash is now the minority of what a shop takes, and "transfer" means a transfer the terminal confirms. The business owner's day is therefore in the terminal, and the terminal is where the day is hardest to see.

What an owner with one or more terminals cannot do today without standing next to each one:

- **Know the day's takings per terminal at closing time.** The Moniepoint Business app shows a list; it does not add the day up per terminal, per shop, per kind, and it does not come to the owner. A trader with three counters, or two shops, scrolls three lists or asks three people.
- **Learn at once when a payment did not go through.** A declined, failed or reversed payment looks like a sale to a busy cashier; the customer leaves with the goods. The owner finds out in the evening, or never.
- **Do an end-of-day cash-up that holds.** "What does the sales book say, and what did the terminal take?" is the question every shop asks at closing, from memory and from the app's list. The difference is what pays for shortages and quiet theft.
- **Keep records the accountant, the bank or the lender will accept.** A month of terminal transactions, by terminal and by day, exported and kept, not screenshots.
- **See the big ones.** An owner who is not in the shop wants to know about the ₦250,000 sale when it happens, not from the evening's story.

Taskiem's promise is exactly this kind of work: tell it what to do in your own words and it runs every day without being reminded. Until now it could not keep that promise for a POS business, for a reason that was easy to miss from the outside:

- The **Moniepoint connector is Monnify**, Moniepoint's online payment gateway. A shop with terminals has a *Moniepoint Business* account, a different product with different credentials. Monnify never sees a terminal transaction.
- The **OPay connector is OPay Checkout**, the online cashier. Same story.
- Neither provider has a public endpoint that lists a business's past terminal transactions, so there was nothing to poll either.

So an owner who connected "Moniepoint" to Taskiem got a connector that could pay suppliers from a Monnify wallet and never heard about a single sale in the shop.

## 2. What the providers offer (read 21 October 2026)

**Moniepoint** has a POS API, separate from Monnify, that does what we need:

- The owner turns on *ERP integration* in Moniepoint Business (Settings → POS Terminal Configuration), then under *POS Apps Developer* creates an API client (an id and a secret, for pushing payment requests to a terminal and checking them) and a *webhook subscription*: a URL, the transaction types to send, and a secret shown once. The subscription covers **every terminal assigned to the business**.
- Moniepoint then posts one signed event per terminal transaction: purchases, transfers to the terminal, card transfers, withdrawals, bill payments, collections, pay codes, airtime. Each carries the terminal's serial number, the amount in kobo, Moniepoint's transaction reference, the status, the terminal's answer, the time and a narration. The signature is an HMAC-SHA256 over the delivery's id, timestamp and body, keyed with the subscription's secret. Retries carry the same event id. Terminals need app version 1.11.0 or later.
- There is also a push-payment request (software asks a terminal to charge an amount) and a status lookup by the merchant's reference.
- There is **no endpoint that lists past transactions**: history has to come from the dashboard's statement export.

**OPay** has a POS integration API (create an order for a terminal, query it) and a webhook under Developer Tool → "POS & Others" in the OPay Business dashboard, whose notifications carry the terminal serial, the amount, the fee and the settlement amount. Two things are not public: the signing and encryption of its envelope (OPay's own Odoo integration uses RSA keys), and whether the webhook fires for every walk-up sale on a terminal or only for orders created through the API. Both are questions for an OPay contact ([needs people, P16](../needs-people.md)).

The full references are in the [Moniepoint POS integration guide](../integrations/moniepoint_pos.md) and [decision 0040](../decisions/0040-pos-terminals.md).

## 3. What we are building

Four pieces, shipped together, Moniepoint first.

### 3.1 A connector for the terminals: `moniepoint_pos@1`

A new connector, not an extension of the Monnify one, because it is a different account, different credentials and a different kind of customer. It receives the terminals' transactions as verified events (the `transaction` trigger), can ask a terminal to take a payment (`request_payment`, keyed by a merchant reference so a retry never asks twice) and can check what became of the request (`get_transaction`). Its connection screen tells the owner, step by step, where in Moniepoint Business to turn on ERP integration, create the client and create the webhook subscription, and checks the client id and secret against Moniepoint before saving. Details, limits and the questions still open with Moniepoint: [integration guide](../integrations/moniepoint_pos.md).

### 3.2 The tables: the shop's POS record, in naira

The events land in the business's own [data tables](../data-tables.md), which are the source of truth for everything else, and which the owner, the accountant and any later workflow can read, search and export:

| Table | What a row is | Columns |
| --- | --- | --- |
| `pos_sales` | One event Moniepoint sent about one terminal transaction | `reference`, `terminal` (serial), `kind` (card purchase, transfer to terminal, withdrawal…), `status`, `approved`, `amount` (naira), `happened_at`, `sale_date` (the Lagos day), `customer`, `narration`, `response` (the terminal's answer), `event` |
| `pos_terminals` | One terminal, with the name the shop calls it | `serial`, `name`, `branch` |
| `pos_cashups` | One end-of-day check of one terminal | `cashup_date`, `terminal`, `book_total`, `pos_total`, `difference`, `sales`, `note` |

Three choices worth knowing. Amounts are **naira** in the tables (the connector speaks kobo, as the provider does) because owners read these tables and no owner thinks in kobo. A transaction that Moniepoint reports twice (pending, then completed) is **two rows**, and every reader takes the latest row per reference, so the record is an honest log and never loses a status change. And terminals are named through `pos_terminals`, which starts empty: fill it in once and every summary says "Counter 1" instead of a serial number.

### 3.3 The templates: what the owner actually asked for

| Template | What it does |
| --- | --- |
| [`pos-sales-record`](../templates.md) | Every terminal transaction becomes a row in `pos_sales`. Silent. The record the others read |
| `pos-daily-summary` | Every evening at the time chosen: today's approved sales, count and total, per terminal and per kind, the biggest sale, how many payments did not go through, to the owner on WhatsApp or Telegram |
| `pos-sale-alert` | The moment a terminal takes an approved sale at or above the amount chosen, or a payment is declined, failed or reversed, tell the owner. "Do not release goods for it" |
| `pos-cashup` | At closing, from the web app or by chat: name the terminal and what the sales book says; Taskiem adds up the terminal's day, tells the owner the difference and what to look for, and keeps the check in `pos_cashups` |

Each is read back as plain steps, passes the publishing checks, dry-runs and round-trips through flow code like every template in the library, and none asks the owner for an id, a kobo amount or a pattern.

### 3.4 Templates now make their tables

Until now a template that used a table said "make it under Tables first". The POS templates would have failed that test on the first owner, so templates now **declare the tables they work on**, with their columns, and Taskiem makes any that do not exist when the template is used (the person may point a template at a table of their own instead). The Templates page says which tables a template keeps its records in; the API answers what became of each. This serves every table-backed template, not only these.

## 4. The owner's path, today

1. Sign up on the web, confirm the email, link WhatsApp (as today).
2. **Connections → Moniepoint POS terminals.** The guided screen walks through Moniepoint Business: turn on ERP integration, create the API client (copy the id and secret), create the webhook subscription with Taskiem's address and the transaction types, copy the webhook secret. Taskiem checks the client against Moniepoint before saving. This is the one visit to a provider's dashboard, and the only step the [no-technical-steps rule](../spec/no-technical-steps.md) allows only because it is guided and checked.
3. **Templates → Record every Moniepoint POS sale.** Taskiem makes `pos_sales` and `pos_terminals`. Publish.
4. The first sale on any terminal appears in Tables within seconds. Fill in `pos_terminals` with the names of the counters (optional).
5. Add the evening summary, the alert, and the cash-up, each in under a minute.

By chat: "every evening tell me how much I sold on my POS" finds the summary template by its words; the chat flow (N6) proposes the tables and asks for the settings one at a time. Connecting Moniepoint still needs the web screen, which is the chat-first gap already listed in the [strategy](chat-first-nigeria.md#32-three-product-gaps-that-keep-the-chat-from-being-chat-first).

## 5. What this does not do yet, and what comes next

- **History.** The record starts the day the webhook is created; Moniepoint offers no API for earlier transactions. The data tables' CSV import takes a statement the owner exports, but Moniepoint exports Excel or PDF and the email trigger keeps only images and PDFs, so a statement-import path (Excel attachments, or a PDF reader) is the next piece for businesses that want last month too.
- **OPay terminals.** Built once OPay answers the two questions ([P16](../needs-people.md)). The tables and templates are designed so an OPay connector lands in the same `pos_sales` with a different `event`, and the summary, alert and cash-up need no change.
- **Settlement.** Terminal proceeds settle into the Moniepoint Business account; settlement is not among the POS events. "Did the money reach the account?" waits either for Moniepoint to add settlement events or for open-banking coverage of Moniepoint MFB (unconfirmed).
- **Who was at the terminal.** The event names the terminal, not the cashier. Cash-ups are per terminal; per-cashier attribution needs a terminal login that the event does not carry.
- **Card purchases in the webhook.** Moniepoint's documentation lists a purchase event type and a third-party guide names only withdrawals and transfers when subscribing; a sandbox run or a written answer from Moniepoint settles it ([P15](../needs-people.md)).
- **Connect from chat** and **sign up from WhatsApp**, so that a trader never opens a laptop: the two chat-first milestones ahead of the pilot.

## 6. How we will know it worked

In the Phase 5 pilot, for shops with terminals: the share that connect their terminals without a support call; events arriving within a minute of the sale; the evening summary delivered by closing time every day; cash-up differences that the owner can explain; fewer than 1 in 10 asking for help with set-up (the same bar as [G-N](../spec/no-technical-steps.md#gate-how-we-know-it-worked-g-n)).

## 7. Claims

Everything above that describes Moniepoint's or OPay's APIs comes from their public documentation, read on 21 October 2026 and cited in the integration guide; nothing was tested against a live account yet. Where the documentation is silent or inconsistent, the guide says so and the connector errs on the side of not acting twice.
