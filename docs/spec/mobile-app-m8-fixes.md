# Phone app M8: the audit's findings and their fixes

The audit after [M8](mobile-app-m8.md) (12 November 2026) compared each
part's screens with the web's, request by request and line by line. Request
paths, methods, bodies and keys matched throughout; these are what did not.
Each is fixed with its test unless it says otherwise.

## Bugs

| # | Where | What | Fix |
|---|---|---|---|
| 1 | Bills: Paid outside Taskiem, Query | The modal's button stayed pressable while sending (a second /declare); a failure showed behind the modal | Busy and its error inside the modal |
| 2 | Stock: orders to suppliers | Not read again after an order opened from it, nor on the Stock screen's pull | Read again on return and with the screen |
| 3 | Suppliers: On Taskiem | Any failure reading the links said "not linked with a supplier yet" | Only "not available" (503) is none; else the error |
| 4 | Reference cards, a statement's rows | Stale after the invoice or bill opened from them was paid | Read again on return |
| 5 | An order: Confirm it | Offered items to receive into where stock is not kept on Taskiem | Only with `keeps_stock` |
| 6 | An invoice's lines | "Added after the invoice was sent." never showed (the amendment is its id, a string) | Tested as the web does |
| 7 | Invoice settings | Saving one card threw away unsaved edits in the other | Each card refills its own |
| 8 | An invoice: Void, Undo | Their errors showed at the top, off-screen; Keep it left them | Each error in its own card, cleared on Keep it |
| 9 | A money entry: Match it | "Loading invoices…" stayed beside an error | Only while loading |
| 10 | A table's json cells | Shown as Dart's map text; a change sent a string | JSON both ways |
| 11 | The products table | Price and cost (kobo) drawn as naira; other price columns not as money | The shop's columns, as the web |
| 12 | A table's date-and-time cells | The raw ISO text | When-words |
| 13 | A table's number boxes | No minus on iOS | A signed keypad |
| 14 | A table's decimal numbers | Commas inside the decimals | Commas in the whole part only |
| 15 | Pay riders | One failed read hid both parts | Each read on its own |
| 16 | A rider's bank code | Digits only; codes may have letters | Letters and digits |
| 17 | Money: Choose dates | Kept the dates from before | Filled from the period chosen |
| 18 | Upload statement | Choosing the file again dropped the matched columns | Kept, as the web |

## Gaps

| # | Where | What | Fix |
|---|---|---|---|
| 19 | Statement | No Chat | Chat opens the conversation |
| 20 | Bills, empty | No Linked businesses | Added |
| 21 | Confirm it; On Taskiem | "Stock" and "Find them" were words, not links | Links |
| 22 | An invoice's payments | A school's receipts (number, voided, PDF) | Shown, the PDF in the browser |
| 23 | Settle the query | Its question "How do you settle it?" | Added |
| 24 | A draft saved from its screen | Two copies of the invoice's screen | Back to the one it came from |
| 25 | A table | Export CSV, import CSV, delete the table; the notes for a table Taskiem keeps and tables not available; sorting by a column; product photos | Export (share sheet), import (phone's files), delete after its name, the notes, sorting; photos counted in words |
| 26 | The account-number card | Balance and withdrawals | Said to be on the web, with the address |
| 27 | Account boxes | The names already used, suggested | Offered as choices under each box |
| 28 | The Money tab | Still "coming" | Money |

## Words

| # | Where | Fix |
|---|---|---|
| 29 | An invoice's empty payments | "… matched to this invoice on To match." |
| 30 | A queried invoice | "… in your conversation with them below." |
| 31 | The editor's hidden-details hint | Not for a linked customer |
| 32 | Confirming it is you, with no method | The server's words and Account, as the web |

## Where it stands

Every row above is fixed, each with a widget test (`bills_page_test`,
`purchases_page_test`, `statement_page_test`, `invoices_page_test`,
`money_sheets_test`, `table_tool_test`, `riders_page_test`,
`money_page_test`, `ref_screens_test`, `conversation_test`). The full suite
passes (837 tests).

Two differences stay, on purpose:

- A product's photos show as a count in the table's rows ("2 photos"); the
  web's thumbnails need the photo screen, and photos are added from
  Products.
- A reference card in Taskiem's conversation opens its document's screen,
  not its tool's sheet as the web does; the screen does all the sheet does.

The bottom bar's Orders and Updates tabs still say they are coming: they
are not M8's.
