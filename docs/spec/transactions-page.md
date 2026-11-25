# A Transactions page, on the web and in the app

The founder's decision 4 of 25 November 2026: every entry of the business's
money log on a page of its own, on the web (**Money › Transactions**,
`/money/transactions`, in the side menu under Money) and in the app
(**Money › Go to › Transactions**), each entry with a page of its own.

Before this, the money log was listed only by the chat's Money tool
(`GET /v1/money/entries`, every entry newest first), and the Money pages
showed the period's sums, the money to match, and "Cash and expenses",
which is a form for entries typed by hand. A person could not see the
payments themselves, one by one, nor ask "what came in from Paystack last
week" or "where is the ₦4,500 Bisi sent".

## What it shows

- **One row per movement of money.** The log writes a new entry for each
  later status of a movement (a transfer pending, then settled; a payout
  settled, then reversed), naming the first in `corrects`. A row is the
  movement with its latest status, the first entry's time, and its amount
  (the first entry's when a later one has none), exactly as the Money
  page's summary and the money log export count it
  (`engine/moneyreport`, `latest`). So the totals of the page for a period
  equal Money's for the same period, and a reversed payout is one row
  marked Reversed, not two rows that look like two payments.
- **Newest first, by day**, by when the money moved (as the rail or the
  person said; else when Taskiem heard), 50 at a time, **Show more** for
  the next 50.
- **Filters**: a period (Today, This week, This month, Last month,
  Everything, Choose dates; This month to start), money in or out, one
  account (the channels the log has money on: Paystack, the Taskiem
  wallet, cash, a bank statement's account), and matched or not
  (matched: counted towards an invoice, order or sale; not matched:
  settled naira money in counted towards nothing yet).
- **A search** over what was typed or the rail said: the description,
  vendor, label, note, reference, account, and what it counts towards
  (an invoice's number, an order's or sale's reference); and an amount in
  naira ("4,500", "₦4500.50") matched exactly. Who paid, as the bank said,
  is sealed (personal data, `pii`), so it is not searched; a cash entry's
  "Cash from Bisi" or an expense's vendor is.
- **Totals for the filter**, over every row it finds, not only the page:
  money in and out settled, what is pending, the fees, how many, and how
  many are in another currency (in no naira sum).
- **Each row**: what it is (the words typed, the vendor, else its kind:
  Payment, Paid out, Cash, Expense…), the amount signed (+ in, − out), the
  account, the reference, what it counts towards, the time, and a status:
  Matched, To match, Not matched, Received or Paid, Pending, Failed,
  Reversed.
- **One transaction on its own**: the amount on the page's hero card with
  its status; what it is, who paid or was paid (masked as `B•••` and
  `••6789` until someone with `pii.reveal` presses **Show who paid**,
  which is audited), the fee, the account, the reference, the
  description, the label, the note, what is not counted yet, its
  currency when not naira, when it moved and when and how Taskiem heard
  of it; **What it paid for** (each invoice, order or sale and how much
  towards each, opening the invoice or order); **What happened** (each
  status, when there is more than one); and **Match** when matching left
  it for a person and they may match.
- **The period's CSV**: the existing money log export
  (`GET /v1/money/exports/log`) for the period chosen. It holds every
  movement of the period whatever the filters, as before; it is not
  offered for Everything (an export is at most 400 days).

## The API

Permission as before: `invoice.read`.

- `GET /v1/money/entries?movements=true` lists movements, with
  `direction` (in, out), `channel`, `from` and `to` (Lagos days, to
  included), `matched` (yes, no), `q` (100 characters at most), `limit`
  (50 unless given, 200 at most) and `cursor` (the `next` the last page
  gave). The first page (no cursor) also carries `totals` and `channels`.
  Each row is the list's entry as before, plus `movement` (the first
  entry's id) and `statuses` (how many entries); its `id` is the entry
  that counts (the first settled money in), which matching by hand takes.
  Without `movements=true` the list is what it was (the chat's tool), and
  the new filters are refused there (400).
- `GET /v1/money/entries/{id}` (new) is one movement, found by any of its
  entries: the row's fields with `recorded_at`, `note`, `source`,
  `history` and `counts`, and `payer` and `payer_account` masked unless
  `reveal=true` (needs `pii.reveal`; audited as `pii.reveal`
  `money_entry`).

No migration: the list reads `money_entries` and the tables that count
it, as the export does. It works out the movements of the whole log each
time (as the summary does); for a business's log that is quick, and an
index or a kept table can come if one log grows large.

## As built (25 November 2026)

- **Server**: `engine/moneyreport/movements.go` (`Movements`,
  `SumMovements`, `MovementChannels`, `MovementOf`, `ParseAmount`,
  cursors), `api/money_transactions.go`, the route in `api/money.go`, the
  switch in `api/money_entries.go`; OpenAPI (`MoneyTotals`,
  `getMoneyEntry`) and the app's client (`listMoneyEntries`,
  `getMoneyEntry` in `mobile/api.txt`). Tests:
  `engine/moneyreport/movements_db_test.go` (one row per movement, the
  filters, the search, paging, the totals equal to the summary's, one
  movement found by its reversal) and `api/money_transactions_test.go`
  (filters and refusals, paging, masking and reveal, another business,
  permissions).
- **Web**: `pages/Transactions.tsx` (`MoneyTransactions`,
  `MoneyTransaction`), `lib/transactions.ts` with its tests; the route
  `/money/transactions` and `/money/transactions/:id`; **Transactions**
  in the side menu under Money (its own icon; Money is not lit with it),
  in Money's tabs, and a row in Money's "To look at" taking Money's
  period. The list leads with the totals on a pine hero card; rows are
  `Items`/`Item` by day; the filters are the period bar, a search box and
  three selects; the download is the log export. The transaction's page
  leads with its amount on the hero card, then the details as a `kv`
  list, then what it paid for and what happened as rows. Tests:
  `e2e/transactions.spec.ts` (filters, search, the page, axe in both
  themes at 390 px, the side menu), both pages in
  `e2e/accessibility.spec.ts` and `plainwords.test.ts`.
- **App**: `lib/chat/transactions_page.dart` (`MoneyTransactionsPage`,
  `MoneyTransactionPage`), opened from Money › Go to › Transactions with
  Money's period. The list as Things we have is: the period's chips under
  the head, the totals on a top card, a search box, In or out as chips,
  Account and Matched as rows that open a choice, then the rows by day,
  **Show more**, and the download in the head (the phone's share sheet).
  The transaction's page: the amount on a top card with its status pill,
  Details as lines, What it paid for (opening the invoice or order) and
  What happened as rows, Show who paid, and Match as a callout. A new
  icon `b-swap`. Pictures `transactions` and `transaction` in
  `test/looks`; Money's pictures changed for the new row. Tests:
  `test/transactions_page_test.dart`.

## Not done

- The CSV is the period's whole money log; a CSV of only the filtered
  rows is not offered.
- Who paid, as the bank said, is not searched (it is sealed); nor is the
  customer an invoice was for.
- Entries entered by hand say "Entered by hand", not who entered them.
