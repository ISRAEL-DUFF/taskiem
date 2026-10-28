# Stock by chat: the shop's stock from a phone, sales that move it, suppliers and purchase orders

Status: **Draft** 28 October 2026, for review · Decision: [0056](../decisions/0056-stock-by-chat.md) · Builds on: [storefronts](../storefronts.md) (the catalogue and its stock), [data tables](../data-tables.md) (kept tables), [money log](../money-log.md) (cash, POS sales, expenses), [reaching people outside](../decisions/0039-reaching-people-outside.md), [languages and voice notes](../decisions/0029-languages-and-voice-notes.md), [leaks](../leaks.md), [money reports](../money-reports.md) · Stories: [user stories](../strategy/user-stories.md) 45–48, 56

## 1. Summary

A shop keeps its stock from WhatsApp: `add 50 cartons of Indomie at 8,500` puts an item in the catalogue table or tops it up, `sold 3 bags of rice 45,000 cash` records the sale in the money log and lowers the stock, `how many bags of rice do I have?` answers from the table, every morning the owner gets the items at or below their reorder level, a purchase order is drafted to the supplier and sent on WhatsApp after the owner approves, `received 50 cartons of Indomie` raises the stock and closes the order, and a stock count posts the differences. Every change is a movement Taskiem keeps beside the owner's table, so a count can always be explained. Nothing needs a storefront or a laptop.

Chat is the first door; everything is also on the web, on a **Stock** page in the Storefront section, through the same engine path, so the two never disagree.

**Availability.** Stock is a feature of the catalogue, not an area to turn on: a business has it the moment it has items with a count, from chat or from the Stock page, which makes the products table when there is none. The Start page offers **Keep my stock** to a business with no items, as it offers **Make my page**. Its only switches are settings with defaults (reorder levels, the evening total's time, the leak limits). On every plan unless billing says otherwise.

Five milestones, K1 to K5, one PR each.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Item | A row of the catalogue table: name, price, `stock`, and the columns this plan maps: `unit` (bag, carton, piece), `cost_price` (the last price paid), `reorder_level`, `target_level`, `supplier` |
| Movement | One change to an item's stock that Taskiem kept: the change, the count after, the reason, who, when, and what it came from |
| Sale | Items sold at the counter, recorded by chat or voice note: items and quantities, the amount, how it was paid (cash, transfer, POS) |
| Supplier | A person or business the shop buys from: a contact under decision 0039 with the items they supply |
| Purchase order | Items and quantities asked of a supplier, with the expected cost; sent after approval, received in full or in part |
| Count | The owner's physical count of some or all items; each difference from the table becomes an adjustment |

## 3. Where we are

| Exists | Where |
| --- | --- |
| The catalogue as the owner's table; the `stock` column; orders refuse what is sold out and lower it on acceptance, once; `add product`, `price`, `stock <item> <n>` in chat | [storefronts](../storefronts.md#stock), [from chat](../storefronts.md#from-chat) |
| Kept tables Taskiem alone writes, 409 to everyone else, kept copies | [data tables](../data-tables.md#kept-tables) |
| The money log: cash in, POS sales from Moniepoint and OPay terminals, transfers, expenses with a vendor and category | [money log](../money-log.md), [POS terminals](../strategy/pos-terminals.md) |
| People outside the business reached in chat with signed buttons | decision 0039 |
| Voice notes transcribed and read as if typed (a beta behind two switches) | decision 0029 |
| Approvals, leaks, the daily summary and close, the monthly email | [governance](../governance.md), [leaks](../leaks.md), [money reports](../money-reports.md) |
| `stock-reorder-alert` over a Google Sheet | [templates](../templates.md) |

## 4. The stock record (K1)

- **Columns**: the catalogue mapping gains `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier` (optional, each; an owner's own table keeps its own column names, mapped as `stock` is). `add 50 cartons of Indomie at 8,500` on a business with no catalogue makes the products table with them, as `add product` does today.
- **Movements**: a kept table `stock_movements` (`item_id`, `item`, `change`, `after`, `reason` one of `order`, `sale`, `received`, `adjustment`, `count`, `by`, `at`, `reference` naming the order, sale, purchase or count), written by Taskiem alone in the same transaction as the table change, once per change under an idempotency key; the table is changed only while it still holds the count that was read (the storefront's rule, lifted into `engine/stock`). The storefront's `decide` writes an `order` movement.
- `stock@1` platform connector: `list_items` (low, by supplier, by name), `get_item`, `adjust` (change, reason, note), `receive`, `count`, `movements` (item, since). Permissions: `table.edit` on the catalogue's table for changes, `table.read` to see, as the catalogue has today.
- **Chat**: `add 50 cartons of Indomie at 8,500` (an item, or stock on an item, with the cost price); `stock indomie`, `how many bags of rice do I have?` (the count, the unit, the last movement); `stock` (the low list); `adjust rice -2 spoilt`. Items are found as `price` and `hide` find them today, with a numbered list when several match. Each change is shown and written on **yes**.
- **Web**: a **Stock** page (Storefront > Stock): items with stock, unit, cost price, reorder level, target and supplier, edited inline (each edit a movement with a reason, written through `engine/stock`); the low list at the top; each item's movements, and a movements list filtered by item, reason and day; **Add items** making the products table when there is none. The catalogue page shows the stock columns too.

## 5. Sales (K2)

- `sold 3 bags of rice 45,000 cash` (also `transfer`, `pos`; several items in one line: `sold 2 cartons indomie, 1 bag rice 60,000 transfer`): shown, then on **yes** one transaction records a **cash** takings entry in the money log (the shape cash in has today, with the items in its note), or for `transfer` and `pos` a sale waiting to be matched to the money the rail reports (a transfer into the business's account, a terminal's `pos.sale`), and a `sale` movement per item. A sale with no amount uses the catalogue price. A sale of more than the stock is recorded and raises a leak (§7), never refused: the shelf is right and the table is wrong.
- **Voice notes**: where the beta is on, a note is transcribed and read as the same sentence, and the reply says what was heard before **yes**.
- **The day's total**: at the business's closing time (the storefront's hours, else 21:00 Lagos), the owner is told today's sales: the total, by cash, transfer and POS, how many sales, the top items, and the items that went below their reorder level; in the daily summary the next morning the same line sits beside the money log's takings.
- **POS sales without items**: a terminal's sale is money, not items. When the business has stock, a `pos.sale` with no sale by chat within the day is listed in the evening ("3 terminal sales, ₦21,500, with no items recorded") so the owner can say what was sold or leave it; nothing is guessed.
- **Web**: **Record a sale** on the Stock page (items, quantities, amount, how paid), today's sales and the evening total, and the terminal sales with no items beside them with **Say what was sold**.
- `stock@1` `record_sale` for workflows; the storefront's orders unchanged.

## 6. Reorders, suppliers and purchase orders (K3)

- **The low list**: every morning (the business's time), the items at or below their reorder level with the suggested quantity (target level less stock, else the last received quantity), from the table; a template `low-stock-alert` replaces the sheet-based one for businesses with a catalogue (the sheet one stays, `superseded_by` only where the business has stock in a table).
- **Suppliers**: people outside the business (decision 0039) with the items they supply and their last prices, on **Suppliers** (beside Riders) and by chat `add supplier Dangote Depot 0803… for rice, indomie`.
- **Purchase orders**: from the low list, the owner (or the template) drafts an order per supplier: items, quantities, the last cost price, the total; it is an approval under the business's policy (the owner approves their own small ones; four-eyes above a limit the owner sets); approved, it goes to the supplier in chat ("[Ada Stores] Order: 50 cartons Indomie, 20 bags rice. Confirm?") with **Confirm** and **Change** (a counter-offer by text), or by SMS with the words. The order's status: drafted, sent, confirmed, part received, received, cancelled.
- **Receiving**: `received 50 cartons of Indomie` (against the open order from that supplier, else a plain receipt) raises the stock with a `received` movement, updates the cost price, closes the order or leaves it part received; `received` from the web too.
- **Paying**: a purchase paid in cash or by transfer is an **expense** in the money log naming the supplier and the order (recorded with the receipt, or when the owner says `paid Dangote 425,000 transfer`); a purchase paid through Taskiem is a payout to the supplier's bank account under approval, matched to the order. Supplier credit (buy now, pay later) shows as owed to the supplier in the summary.
- **Web**: **Suppliers** (beside Riders) and **Purchase orders** on the Stock page: draft from the low list, approve, send, receive in part or full, mark paid.
- `stock@1`: `low_items`, `draft_purchase`, `send_purchase`, `receive`, `list_purchases`.

## 7. Counts, shrinkage and leaks (K4)

- **A count by chat**: `count rice 12, indomie 40` (or `count` to be asked item by item, or **Count** on the Stock page: a sheet to fill item by item, or a CSV): each difference from the table becomes a `count` movement with the owner's reason, shown first and written on **yes**; a count of everything closes a counting period.
- **Shrinkage**: the daily close and the monthly email show, per period, stock received against sold and counted out, in units and at cost; a line per item with the largest loss.
- **Leaks** (as money leaks, closed with a reason): a sale of more than the stock; a negative count; a count difference above the owner's limit; an item below its reorder level for more than a week with no order; a purchase order confirmed and not received after its expected days.

## 8. Templates, chat and docs (K5)

- Templates: `low-stock-alert`, `reorder-from-supplier` (the low list to drafted orders every morning, sent after approval), `daily-sales-summary` (the evening total), `stock-count-reminder` (weekly).
- Chat words in the five languages (drafts for review): sold, received, count, adjust, how many, stock, supplier.
- Docs: `docs/stock.md` (the guide), the storefront page's stock section, the money log (sales, expenses to suppliers), the owner's guide; the AI builder learns `stock@1` ("tell me what's low", "order from my supplier when rice is low") with eval cases and a table warning for a proposed "stock" table with a quantity column that the catalogue already is.

## 9. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| K1 | The stock record: the mapped columns, `stock_movements`, `engine/stock` with the once-only rule lifted from orders, `stock@1` reads and adjustments, `add`/`stock`/`how many`/`adjust` in chat, the Stock page with inline edits and movements, **Keep my stock** on the Start page | M | `add 50 cartons of Indomie at 8,500` makes or tops up the item with a movement; an order accepted writes an `order` movement; a retried adjustment moves stock once; the count is always the sum of its movements |
| K2 | Sales: `sold …` by chat and voice note, the money log entry and the movements in one transaction, transfer and POS sales matched to the rail's money, the evening total, terminal sales with no items listed | M | A cash sale lowers stock and is in the money log; a POS sale by chat matches the terminal's event once; the evening message totals the day by cash, transfer and POS |
| K3 | Reorders, suppliers and purchase orders: reorder levels, the morning low list, suppliers, orders drafted, approved and sent with Confirm, received in part or full, paid as an expense or a payout | M | A low item becomes a drafted order, approved, sent, confirmed by the supplier's tap, received with the stock raised and the cost updated, and paid as an expense |
| K4 | Counts, shrinkage and leaks: counts by chat and CSV, adjustments with reasons, shrinkage in the close and the monthly email, the five leaks | S | A count posts the differences with reasons; the close shows shrinkage at cost; a sale beyond stock raises a leak |
| K5 | Templates, chat words, docs, the AI builder; the sheet template superseded where a table has stock | S | The four templates publish from the Start page; the builder answers "order from my supplier when rice is low" with `stock@1`; the guides are published |

Order: K1 to K5, one PR each.

## 10. Testing

| Level | What | Where |
| --- | --- | --- |
| Exactly once | Two sales of the last bag at the same moment; a retried `receive`; an order and a sale on the same item in one second: the count equals the sum of movements, every time | `engine/stock`, `engine/db` |
| Kept table | Nobody but Taskiem writes `stock_movements` (409 and the workflow refusal), the copy agrees | `engine/datatables`, `api` |
| Money | A cash sale is one money log entry; a POS sale by chat matches one `pos.sale` once; a purchase paid is one expense naming the order | `engine/moneylog`, `api` |
| Chat | The sentences in the five languages, forgiving numbers and units, the numbered list on ambiguity, voice notes with the fake transcriber | `api`, `engine/plain` |
| Suppliers | A Confirm works once from the supplier's contact only; Change carries the counter-offer back | `api` |
| Web | The Stock page: items and inline edits as movements, the low list, Record a sale, Suppliers, purchase orders, Count by sheet and CSV, movements; the Start page's offer | Playwright |

## 11. Risks and open questions

| Risk or question | Mitigation or owner |
| --- | --- |
| Owners who sell units the catalogue does not list (a half bag, a cup of rice) | `unit` per item and fractional quantities allowed (`sold 2.5 bags`); a shop that sells by weight is out of scope for K1 |
| A sale by chat that names an item wrongly | The reply shows what was understood before **yes**, and the numbered list resolves ambiguity; a wrong sale is corrected with an adjustment, never deleted |
| Terminal sales carry no items | Listed in the evening for the owner to say; nothing guessed (POS terminals strategy §5) |
| Suppliers who answer by phone, not in chat | The order's status can be set by the owner (`confirmed`); the message is the record |
| Cost prices and shrinkage at cost need a cost per item | The last price received; items with none show shrinkage in units only |

## 12. Out of scope

Barcodes and scanning; multiple shops or stores per business (one stock per business); batches and expiry dates (pharmacies: section M later); selling by weight; supplier catalogues and price lists (story 55 is the wholesaler's side); accounting valuation methods beyond last cost.
