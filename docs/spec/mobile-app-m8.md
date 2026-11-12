# Phone app M8: money

The plan for M8 of the [phone app](mobile-app.md) (§10, §3.3): what is
built, in which order, and what each part copies from the web. Written 12
November 2026, after M7 and its audit's fixes.

**Done when** (§10): an order is invoiced, sent, paid and matched, and a
bill paid with a passkey, from the app alone.

**Passkeys come with M4.** M4 (notifications, links and passkeys) was moved
after M8 (12 November 2026). Until then the app approves a step-up with a
code from an authenticator app (the approval sheet does already); a rule
that takes only a passkey says so and is decided on a device with one. So
M8 is done when a bill is paid with an approval's code; with a passkey once
M4 brings passkeys to the app.

## What is there already

The tools panel and each tool's sheet; Your orders (purchases) and a
purchase's view; approvals with a code (`approve.dart`); orders, deliveries
and their screens (M7). Saying "coming to the app soon" today: the Invoices,
Bills, Money and Tables rows and their **Open …**; Add invoice, Add cash or
expense; a card's **View invoice** and **Pay**; **Invoice them** and
**Statement** in a business's conversation; a reference card's Open of an
invoice, bill or purchase; the alerts' **Reorder** and **Match it**; the
briefing's **Owed to you**; an order's **Its invoice** and a buyer's
**Their statement**; paying riders and the riders' page.

## What M8 builds

| # | Part | Copies (web) | Notes |
| --- | --- | --- | --- |
| W | **The money tools' rows, in their sheets**: an invoice (its lines, what is due, send, remind, record a payment), Add invoice and **Invoice them**; a bill (Pay, its lines); a money entry (match it, what it is) and Add cash or expense; a table's rows (add, change, remove) | `ChatMoney.tsx` `InvoiceView`, `InvoiceAdd`, `MoneyEntryView`, `MoneyAdd`; `ChatBuyer.tsx` `BillView`; `ChatToolForms.tsx` `TableTool` | The sheets the chat works in every day |
| I | **Invoices**: the list and its filters; an invoice: hero, lines, payments (record, undo), discount, amend, void, send (to the chat, or a PDF through the phone's share sheet), a query settled; a new invoice and its editor; the invoice settings (credit and days to pay) | `Invoices.tsx` `Invoices`, `InvoicePage`, `InvoiceNew`, `InvoiceEdit`, `InvoiceEditor`, `Amend`, `Payments`, `SettleQuery`, `InvoiceSettingsPage` | |
| H | **Matching money**: payments waiting for an invoice, matched, split by hand; a customer's matching and an account number given to them | `Invoices.tsx` `InvoiceMatch`, `WaitingCard`, `HandSplit`, `SplitForm`, `CustomerMatching`, `GiveAccount`; the alerts' **Match it** | |
| B | **Bills**: the list; a bill: what is due, **Pay from Taskiem** (sent for approval, then approved with a code), paid outside Taskiem, queried | `Bills.tsx` | |
| P | **Purchases and suppliers**: a purchase's page (the buyer's side of an order: accept the change, cancel, all came, not all came, paid); the Stock screen's purchases card (receive, pay); suppliers (linked or not, the second-person limit, where to deliver, what each is owed); a supplier's prices; the alerts' **Reorder** | `TradeOrders.tsx` `PurchasePage`, `ConfirmProposed`, `BuyerActions`; `StockPurchases.tsx`; `StorefrontSuppliers.tsx` | |
| S | **A linked business's statement**: what is owed each way, from **Statement** in its conversation, Their statement and the terms card | `Statement.tsx` | |
| M | **Money**: today in and out, the bars and sources; leaks; statements (bank statements read, lines waiting); entries; fees; the briefing's **Owed to you** | `Money.tsx`, `MoneyParts.tsx` | |
| T | **Tables**: the list and a table's rows (search, add, change, remove after yes) | `Tables.tsx` `Tables`, `TablePage`, `Rows`, `RowForm` | A table's columns and a new table stay on the web, as the services table's set-up did (M6) |
| R | **Riders**: the riders' page (add, change, their areas) and Pay (cash owed, what each is due, payouts) | `StorefrontRiders.tsx`; `Dispatch.tsx` `RidersPayPage` | Left from M7 |
| X | **Every "coming soon" of M8 opens its screen**: cards, reference cards, Taskiem's alerts and briefing, a business's tiles, the order screen's links; **Messages about it** on a purchase and a bill | | |

## Order of the work

W first (the sheets the chat uses), then I and H (an order invoiced, sent,
paid and matched), B (a bill paid), P, S, M, T, R, and X last (or as each
screen lands). Each part: the web read first, and the server's routes
(`api/`) for every request; widget tests for what each button sends; large
text and touch targets (`expectLargeText`, `expectPressable`); committed on
its own. After the last part, an audit as M5's to M7's.

## Where it stands

| Part | Done |
|---|---|
| W | The money rows in their sheets: an invoice (record a payment, send, remind, void), Add invoice and **Invoice them**; a bill (Pay, query, paid outside); a money entry (match it) and Add cash or expense; a table's rows |
| T | Tables: the list and a table's rows (search, pages, add, change, remove after yes) |
| I | Invoices: the list and its filters; an invoice (what is due, send and send again, its page opened or shared, void with a reason, amend, a payment undone, the customer shown, a query settled, its messages with a linked customer); a new invoice and a draft changed; the settings and credit terms |
| H | Matching money: To match (each payment and what it may be for, matched or turned down, matched by hand to an invoice or order, split by hand or across the customer's invoices, held for a second person), an invoice's customer (what they owe, credit, account numbers given, payers forgotten); the alerts' **Match it**. A school family's claims are said and matched on the web |

The server makes no PDF of an invoice: after Send the app shares the
invoice's page link through the phone's share sheet instead, as the web
shows the link.
