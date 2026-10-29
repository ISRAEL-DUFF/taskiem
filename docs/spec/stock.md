# Stock by chat: the shop's stock from a phone, sales that move it, suppliers and purchase orders

Status: **Draft** 28 October 2026, for review · Decision: [0057](../decisions/0057-stock-by-chat.md) · Builds on: [storefronts](../storefronts.md) (the catalogue and its stock), [data tables](../data-tables.md) (kept tables), [money log](../money-log.md) (cash, POS sales, expenses), [reaching people outside](../decisions/0039-reaching-people-outside.md), [languages and voice notes](../decisions/0029-languages-and-voice-notes.md), [leaks](../leaks.md), [money reports](../money-reports.md) · Stories: [user stories](../strategy/user-stories.md) 45–48, 56

## 1. Summary

A shop keeps its stock from WhatsApp: `add 50 cartons of Indomie at 8,500` puts an item in the catalogue table or tops it up, `sold 3 bags of rice 45,000 cash` records the sale in the money log and lowers the stock, `how many bags of rice do I have?` answers from the table, every morning the owner gets the items at or below their reorder level, a purchase order is drafted to the supplier and sent on WhatsApp after the owner approves, `received 50 cartons of Indomie` raises the stock and closes the order, and a stock count posts the differences. Every change is a movement Taskiem keeps in its own record, beside the owner's table, so a count can always be explained, even one the owner changed in the table by hand. Nothing needs a storefront or a laptop.

Chat is the first door; everything is also on the web, on a **Stock** page in the Storefront section, through the same engine path, so the two never disagree.

**Availability.** Stock is a feature of the catalogue, not an area to turn on: a business has it the moment it has items with a count, from chat or from the Stock page, which makes the products table when there is none. The Start page offers **Keep my stock** to a business with no items, as it offers **Make my page**. Its only switches are settings with defaults (reorder levels, the evening total's time, the leak limits). On every plan unless billing says otherwise.

Five milestones, K1 to K5, built in order on one branch and landing together in one PR (#117), as dispatch did.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Item | A row of the catalogue table: name, price, `stock` (a whole number of the item's unit), and the columns this plan maps: `unit` (bag, carton, piece), `cost_price` (the last price paid), `reorder_level`, `target_level`, `supplier` |
| Movement | One change to an item's stock that Taskiem kept: the change, the count after, the reason, who, when, and what it came from. A change found in the table that Taskiem did not make is a movement too (`outside`) |
| Sale | Items sold at the counter, recorded by chat or voice note: items and quantities, the amount, how it was paid (cash, transfer, POS). A record Taskiem keeps, which money is matched to |
| Supplier | A person or business the shop buys from: a contact under decision 0039 with the items they supply |
| Purchase order | Items and quantities asked of a supplier, with the expected cost; sent after approval, received in full or in part |
| Count | The owner's physical count of some or all items; each difference from the table becomes an adjustment |

## 3. Where we are

| Exists | Where |
| --- | --- |
| The catalogue as the owner's table in their Xenibase project, changed over Xenibase's API; the `stock` column; orders refuse what is sold out and lower it on acceptance, once (the change recorded on the order before it is sent, under an Idempotency-Key, and made only while the table still holds the count read); at most 500 items, and the plan's `storefront_items`; `add product`, `price`, `stock <item> <n>` (which sets the count, with no record of why) in chat | [storefronts](../storefronts.md#stock), [from chat](../storefronts.md#from-chat) |
| The catalogue copy Taskiem keeps of the mapped columns, refreshed every 15 minutes and on publishing, which catches rows changed in Xenibase, in workflows or from chat | [the catalogue copy](../storefronts.md#the-catalogue-copy) |
| Kept tables Taskiem alone writes, 409 to everyone else; the money log's record of reference is in Taskiem's database, and its kept table is a copy made within a minute | [data tables](../data-tables.md#kept-tables), [money log](../money-log.md) |
| The money log: cash in, POS sales from Moniepoint and OPay terminals (a `pos.sale` is an amount, a terminal and a reference, no items), transfers, expenses with a vendor and category; matching money in to invoices and orders | [money log](../money-log.md), [POS terminals](../strategy/pos-terminals.md#3-what-we-are-building), [matching](../matching.md) |
| People outside the business reached in chat with signed buttons; riders on SMS get a link that moves them to WhatsApp, since Taskiem receives no SMS | decision 0039, [dispatch](../spec/dispatch.md) |
| Chat commands matched from word lists in five languages; the model only routes a few bounded intents and never reads amounts. Voice notes transcribed and read as if typed (a beta behind two switches) | decision 0029, `engine/lang` |
| Approvals, leaks, the daily summary and close, the monthly email | [governance](../governance.md), [leaks](../leaks.md), [money reports](../money-reports.md) |
| `stock-reorder-alert` and `daily-sales-summary` over a Google Sheet | [templates](../templates.md) |

## 4. The stock record (K1)

- **Columns**: the catalogue mapping gains `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier` (optional, each; an owner's own table keeps its own column names, mapped as `stock` is). `add 50 cartons of Indomie at 8,500` on a business with no catalogue makes the products table with them, as `add product` does today.
- **Movements**: the catalogue table is in the owner's Xenibase project and is changed over its API, so no transaction spans it and Taskiem's database. Movements therefore follow the money log's shape (record of reference in Taskiem, the table a copy) and the storefront's order rule (recorded before it is sent):
  - **The record.** A table `stock_movements` in Taskiem's database (`item_id`, `item`, `change`, `after`, `reason` one of `order`, `sale`, `received`, `adjustment`, `count`, `outside`, `by`, `at`, `reference` naming the order, sale, purchase or count, and the idempotency key). A movement is written in the transaction of what caused it: the order's decision, the sale, the receipt, the count.
  - **The change to the table** is sent after that commit, under the movement's key, and made only while the table still holds the count that was read (the storefront's rule, lifted into `engine/stock`). On a conflict the count is read again and the change re-sent under the same key until it lands; `after` is set when it does. A retried step never moves stock twice.
  - **The kept table** `stock_movements` in the owner's project is a copy for reading, made within a minute, as `money_log` is.
  - **Changes Taskiem did not make.** The owner's table is theirs: anyone with `table.edit` can change a count on its page or in Xenibase. When the catalogue copy refreshes (every 15 minutes, and before each change Taskiem sends), a count that differs from the last one Taskiem knew is recorded as an `outside` movement ("changed in the table"). So the count always equals the sum of its movements, outside ones included, and an owner who edits by hand still sees why it moved.
  - The storefront's `decide` writes an `order` movement in the order's transaction; `stock <item> <n>` in chat writes a `count` movement instead of setting the column bare.
- **Whole units.** Quantities are whole numbers of an item's `unit`, as the integer `stock` column is today; a shop that sells half bags lists the half bag as its own item. Fractions and selling by weight are out of scope (§12).
- **Size.** The stock list is the catalogue, so its limits hold for a shop with no storefront too: at most 500 items, or the plan's `storefront_items` when lower. A shop keeping stock without a storefront gets no higher limit (decided 29 October 2026). The limit is said when reached, as adding products says it today.
- `stock@1` platform connector: `list_items` (low, by supplier, by name), `get_item`, `adjust` (change, reason, note), `receive`, `count`, `movements` (item, since). Permissions: `table.edit` on the catalogue's table for changes, `table.read` to see, as the catalogue has today.
- **Chat**: `add 50 cartons of Indomie at 8,500` (an item, or stock on an item, with the cost price); `stock indomie`, `how many bags of rice do I have?` (the count, the unit, the last movement); `stock` (the low list); `adjust rice -2 spoilt`. Items are found as `price` and `hide` find them today, with a numbered list when several match. Each change is shown and written on **yes**.
- **Web**: a **Stock** page (Storefront > Stock): items with stock, unit, cost price, reorder level, target and supplier, edited inline (each edit a movement with a reason, written through `engine/stock`); the low list at the top; each item's movements, and a movements list filtered by item, reason and day; **Add items** making the products table when there is none. The catalogue page shows the stock columns too.
- **As built (K1):**
  - Migration 00256: `stock_movements` (a change, `delta`, or a count, `set_to`; `pending`, `done` or `void`; `count_before`, `count_after`, `change`, `short`), what was asked and why never changed after insert (a trigger, and column grants); `storefronts.keeps_stock`, so `taskiem_storefronts_due` refreshes a catalogue keeping stock whether or not its page is published; `taskiem_stock_due_tenants` for the sweep.
  - `engine/stock`: `RecordTx`, `Apply` (the claim reads the count, records what it sends, and the change is sent under a key of the movement and the count read; a moved count is read again up to five times, then the sweep sends it, waiting a minute more after each attempt up to an hour), `Outside` (on each catalogue refresh; and in each claim), `OrderLines` (the storefront's `decide` lowers stock through it when set: lines of one row are one movement); the outside check compares the database's clock only, and skips an item with a change of Taskiem's on its way or landed since the read. The kept copy `stock_movements` is made by the sweep (`stock.Mirror`), and its name is reserved.
  - A business with no storefront gets a draft one under its name to hold the catalogue (Keep my stock, or the first `add` in chat). The make-my-page questions are unchanged.
  - The mapping gains `unit`, `cost_price`, `reorder_level`, `target_level`, `supplier`; the products table chat and Keep my stock make has them all. None reaches the public page; an item added for stock without a selling price is not available on it.
  - Chat: `add <n> [<unit> of] <item> [at <price>]` (a unit is the word before "of", or a known unit word), `stock <item>`, `how many …`, `stock`, `adjust <item> <±n> <why>`, and `stock <item> <n>` as a count. English words in K1; `table.read` to see, `table.edit` to change, `storefront.manage` too to start keeping stock.
  - The web page lists items as rows with **Record stock** (received, adjusted with a reason, counted), **Change** and **History** rather than editing inline; Get started offers **Keep my stock** while no item keeps stock.
  - API: `GET /v1/stock`, `POST /v1/stock/setup`, `POST /v1/stock/items`, `PATCH /v1/stock/items/{id}`, `GET`/`POST /v1/stock/movements`. `stock@1`: `list_items`, `get_item`, `adjust`, `receive`, `count`, `movements`.

## 5. Sales (K2)

- `sold 3 bags of rice 45,000 cash` (also `transfer`, `pos`; several items in one line: `sold 2 cartons indomie, 1 bag rice 60,000 transfer`): shown, then on **yes** one transaction in Taskiem's database records:
  - the **sale** (`stock_sales`: items, quantities, amount, how paid, who, when; status `recorded`, or `waiting` for its money);
  - a `sale` movement per item;
  - for cash, a **cash** takings entry in the money log (the shape cash in has today, with the sale named in its note).

  The changes to the table follow as in §4. A sale with no amount uses the catalogue price. A sale of more than the stock is recorded and raises a leak (§7), never refused: the shelf is right and the table is wrong.
- **Matching transfer and POS sales.** A sale paid by `transfer` or `pos` waits for its money: matching gains the sale as a target beside invoices and orders, and a transfer into the business's account or a terminal's `pos_sale` entry of that amount on that day is matched to it once (the reference when the owner gives it, else amount and time, the nearest first). Matched, the sale is `recorded`; still waiting at the evening total, it is listed there.
- **Reading the sentence.** Sales are read by a parser, not the model: the verb and the payment words from `engine/lang`'s word lists, numbers and amounts read forgivingly as the storefront reads prices (`45,000`, `45k`, `N45000`), units from each item's `unit` (singular or plural), items found as `price` finds them, with a numbered list when several match. The model layer's bounded intents are unchanged and never read quantities or amounts; a sentence the parser cannot read is answered with an example. The sale words come in all five languages in K2 (drafts for review), since a Pidgin or Yoruba voice note is the case the story names; K5 adds the rest.
- **Voice notes**: where the beta is on, a note is transcribed and the text goes through the same parser, and the reply says what was heard before **yes**.
- **The day's total**: at the business's closing time (the storefront's hours, else 21:00 Lagos), the owner is told today's sales: the total, by cash, transfer and POS, how many sales, the top items, and the items that went below their reorder level; in the daily summary the next morning the same line sits beside the money log's takings.
- **POS sales without items**: a terminal's sale is money, not items. When the business has stock, a `pos.sale` matched to no sale by chat within the day is listed in the evening ("3 terminal sales, ₦21,500, with no items recorded") so the owner can say what was sold or leave it; nothing is guessed.
- **Web**: **Record a sale** on the Stock page (items, quantities, amount, how paid), today's sales and the evening total, and the terminal sales with no items beside them with **Say what was sold**.
- `stock@1` `record_sale` for workflows; the storefront's orders unchanged.
- **As built (K2):**
  - Migration 00257: `stock_sales` (lines as JSON, the amount, how paid, `recorded`/`waiting`/`matched`, the money entry it takes, unique, and how it was matched); a sale changes only from waiting to matched (a trigger). `taskiem_stock_due_tenants` also returns businesses with waiting sales; `money_digests` and the outbox gain the kind `sales_evening`.
  - `engine/stock`: `RecordSale` (sale, movements and the cash entry in one transaction, then the movements are sent), `MatchSales` (in the sweep: the sale's reference in the money's reference first, else the amount and the nearest time within 12 hours; `money_match_state` set to matched so the entry raises no unmatched leak), `DayTx` (a Lagos day added up, with the terminal's sales no sale names), `Evening` (queued once a day after closing, quiet with no sales, written at send time). A sale beyond the stock is recorded with the item in `short`; its leak comes in K4.
  - Chat: `sold …` in English, and after the sale word of Pidgin, Yoruba, Hausa and Igbo (`engine/lang` intent `sold`; the rest of the sentence is read as in English). The money summary gains a sales line.
  - Web: **Today's sales** as a pine card with **Record a sale**, the terminal's sales with no items with **Say what was sold**, and the day's sales as rows.
  - API: `POST /v1/stock/sales` (`entry_id` for a terminal's sale), `GET /v1/stock/sales?day=`.

## 6. Reorders, suppliers and purchase orders (K3)

- **The low list**: every morning (the business's time), the items at or below their reorder level with the suggested quantity (target level less stock, else the last received quantity), from the table; a template `low-stock-alert` replaces the sheet-based one for businesses with a catalogue (the sheet one stays, `superseded_by` only where the business has stock in a table).
- **Suppliers**: people outside the business (decision 0039) with the items they supply and their last prices, on **Suppliers** (beside Riders) and by chat `add supplier Dangote Depot 0803… for rice, indomie`.
- **Purchase orders**: from the low list, the owner (or the template) drafts an order per supplier: items, quantities, the last cost price, the total; it is an approval under the business's policy (the owner approves their own small ones; four-eyes above a limit the owner sets); approved, it goes to the supplier in chat ("[Ada Stores] Order: 50 cartons Indomie, 20 bags rice. Confirm?") with **Confirm** and **Change** (a counter-offer by text). Taskiem receives no SMS, so a supplier with no WhatsApp or Telegram gets the order by SMS with a link that moves them to WhatsApp, as riders do, and until they move the owner marks the order confirmed when the supplier answers by SMS or phone. The order's status: drafted, sent, confirmed, part received, received, cancelled.
- **Receiving**: `received 50 cartons of Indomie` (against the open order from that supplier, else a plain receipt) raises the stock with a `received` movement, updates the cost price, closes the order or leaves it part received; `received` from the web too.
- **Paying**: a purchase paid in cash or by transfer is an **expense** in the money log naming the supplier and the order (recorded with the receipt, or when the owner says `paid Dangote 425,000 transfer`); a purchase paid through Taskiem is a payout to the supplier's bank account under approval, matched to the order. Supplier credit (buy now, pay later) shows as owed to the supplier in the summary.
- **Web**: **Suppliers** (beside Riders) and **Purchase orders** on the Stock page: draft from the low list, approve, send, receive in part or full, mark paid.
- `stock@1`: `low_items`, `draft_purchase`, `send_purchase`, `receive`, `list_purchases`.

- **As built (K3):**
  - Migration 00258: `suppliers` (name plain, as the catalogue's supplier column holds it; the phone sealed and keyed as riders' are), `purchase_orders` (lines as JSON with what was received, the total, status, who drafted and approved, the supplier's change, the value received and paid), `purchase_payments` (one money log entry each), `stock_settings` (`purchase_limit_kobo`), `taskiem_supplier_orders` (a definer: the orders a sender may still confirm or change, across businesses, never their items), the outbox kind `supplier`.
  - `engine/stock`: `Purchases` (suppliers added with the tap-in link by SMS, `DraftLowTx`, `SendTx` with the limit: above it the drafter and workflows may not send, `ConfirmTx`, `CounterTx` with an owner alert, `CancelTx` until anything came, `ReceiveTx` with a received movement per item naming the order and the cost price set after, `PayTx` as an expense naming the supplier). The suggested quantity is up to the target level, else twice the reorder level (not the last received quantity).
  - Chat: `add supplier`, `suppliers`, `order from`, `received`, `paid <supplier>`, `send P-…`, `confirmed P-…`; the supplier's Confirm and Change (signed buttons), and CONFIRM / CHANGE P-… in words. `received` in the other four languages after their word. The morning summary lists what is low.
  - Web: **Orders to suppliers** on the Stock page, **Storefront > Suppliers** with the approval limit. API and `stock@1` as in [docs/stock.md](../stock.md).
  - Paying through Taskiem (a payout to the supplier's bank account under approval) comes with the templates in K5.

## 7. Counts, shrinkage and leaks (K4)

- **A count by chat**: `count rice 12, indomie 40` (or `count` to be asked item by item, or **Count** on the Stock page: a sheet to fill item by item, or a CSV): each difference from the table becomes a `count` movement with the owner's reason, shown first and written on **yes**; a count of everything closes a counting period.
- **Shrinkage**: the daily close and the monthly email show, per period, stock received against sold and counted out, in units and at cost; a line per item with the largest loss.
- **Leaks** are money ([leaks](../leaks.md)), so three are leaks, each valued at the item's cost price (in units only where it has none) and closed with a reason as other leaks are: a sale of more than the stock; a negative count; a count difference above the owner's limit.
- **Owner alerts**, not leaks, as dispatch did for quiet riders: an item below its reorder level for more than a week with no order; a purchase order confirmed and not received after its expected days.

- **As built (K4):**
  - Migration 00259: `stock_movements.cost_kobo` (the item's cost when it moved, where Taskiem knows it: sales, receipts, counts, the web's changes), `stock_counts` (lines with what Taskiem had and what was counted, full, the reason, what was missing at cost), `stock_low` (items low since when, for the alert), `stock_settings.count_limit_kobo`, `purchase_orders.expected_days` (3), the leak kinds `stock_short`, `stock_negative`, `stock_count`, and `taskiem_money_leak_tenants` looking at stock too.
  - `engine/stock`: `CountTx`/`RecordCount` (a count movement per difference, naming the count), `ShrinkTx` (received, sold, lost at cost, the cost when it moved else the last known), `MonthlyText` for the monthly email, `Watch` (hourly: items low a week with nothing on an open order, orders confirmed and late; each told once). The leaks are queries of `engine/leaks`, open until closed.
  - "The daily close" is the morning money summary: it says what was lost the day before. A CSV count is read into the sheet in the browser and sent as the sheet is.
  - Chat: `count …` and `count` item by item, and `count` in the other four languages after their word.
  - Web: **Counts and losses** on the Stock page (this month, the latest counts, **Count the shelf** with a CSV, the leak limit); the three leaks named on the Money page.

## 8. Templates, chat and docs (K5)

- Templates: `low-stock-alert`, `reorder-from-supplier` (the low list to drafted orders every morning, sent after approval), `sales-day-total` (the evening total from sales by chat), `stock-count-reminder` (weekly). The sheet-based `stock-reorder-alert` and `daily-sales-summary` stay, superseded only where the business keeps stock in a table or records sales by chat.
- Chat words in the five languages (drafts for review): each milestone brings its own words (sold in K2, received and supplier in K3, count in K4); K5 adds adjust, how many and stock, and reviews them all.
- Docs: `docs/stock.md` (the guide), the storefront page's stock section, the money log (sales, expenses to suppliers), the owner's guide; the AI builder learns `stock@1` ("tell me what's low", "order from my supplier when rice is low") with eval cases and a table warning for a proposed "stock" table with a quantity column that the catalogue already is.

- **As built (K5):**
  - Templates `low-stock-alert`, `reorder-from-supplier` (draft, one approval, send each; an order above the limit is reported not sent), `sales-day-total` (stock@1's new `sales_day`) and `stock-count-reminder`, each with its workflow tests. The sheet templates gain `replaced_where`: a business keeping stock in a table is not offered `stock-reorder-alert`, one recording sales not `daily-sales-summary`.
  - Chat words in the other four languages for an item's stock and what is low, and stock taken off with why (drafts for review, as the others are).
  - The AI builder: rule 24 (stock@1, the four templates, never notify a supplier), synonyms (inventory, shelf, restock, reorder, stocktake, sold), the `stock_as_table` warning for a proposed table of stock with a quantity, and five eval cases (`evals/builder/stock.jsonl`).
  - Docs: [docs/stock.md](../stock.md), the owner's guide, the storefront's and the money log's pages; decision 0057 accepted. Paying suppliers through Taskiem is not built (0057, as built).

## 9. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| K1 | The stock record: the mapped columns, `stock_movements`, `engine/stock` with the once-only rule lifted from orders, `stock@1` reads and adjustments, `add`/`stock`/`how many`/`adjust` in chat, the Stock page with inline edits and movements, **Keep my stock** on the Start page | M | `add 50 cartons of Indomie at 8,500` makes or tops up the item with a movement; an order accepted writes an `order` movement; a retried adjustment moves stock once; a count changed by hand in the table is recorded as an `outside` movement; the count is always the sum of its movements |
| K2 | Sales: `sold …` by chat and voice note in the five languages, the sale, its movements and the money log entry in one transaction, transfer and POS sales matched to the rail's money, the evening total, terminal sales with no items listed | M | A cash sale lowers stock and is in the money log; a POS sale by chat matches the terminal's event once; the evening message totals the day by cash, transfer and POS |
| K3 | Reorders, suppliers and purchase orders: reorder levels, the morning low list, suppliers, orders drafted, approved and sent with Confirm, received in part or full, paid as an expense or a payout | M | A low item becomes a drafted order, approved, sent, confirmed by the supplier's tap, received with the stock raised and the cost updated, and paid as an expense |
| K4 | Counts, shrinkage and leaks: counts by chat and CSV, adjustments with reasons, shrinkage in the close and the monthly email, the three leaks and two owner alerts | S | A count posts the differences with reasons; the close shows shrinkage at cost; a sale beyond stock raises a leak |
| K5 | Templates, the rest of the chat words, docs, the AI builder; the sheet templates superseded where a table has stock or sales are recorded by chat | S | The four templates publish from the Start page; the builder answers "order from my supplier when rice is low" with `stock@1`; the guides are published |

Order: K1 to K5, on one branch, in one PR (#117).

## 10. Testing

| Level | What | Where |
| --- | --- | --- |
| Exactly once | Two sales of the last bag at the same moment; a retried `receive`; an order and a sale on the same item in one second; the table's change failing and re-sent; a count changed by hand between a read and a write: the count equals the sum of movements, every time | `engine/stock`, `engine/db` |
| Kept table | Nobody but Taskiem writes `stock_movements` (409 and the workflow refusal), the copy agrees with the record | `engine/datatables`, `api` |
| Money | A cash sale is one money log entry in the sale's transaction; a POS sale by chat matches one `pos_sale` entry once; a purchase paid is one expense naming the order | `engine/moneylog`, `engine/matching`, `api` |
| Chat | The sentences in the five languages, forgiving numbers and units, the numbered list on ambiguity, an unreadable sentence answered with an example, voice notes with the fake transcriber | `api`, `engine/lang` |
| Suppliers | A Confirm works once from the supplier's contact only; Change carries the counter-offer back | `api` |
| Web | The Stock page: items and inline edits as movements, the low list, Record a sale, Suppliers, purchase orders, Count by sheet and CSV, movements; the Start page's offer | Playwright |

## 11. Risks and open questions

| Risk or question | Mitigation or owner |
| --- | --- |
| Owners who sell units the catalogue does not list (a half bag, a cup of rice) | `unit` per item, in whole numbers; a half bag or a cup is its own item. Fractions and selling by weight are out of scope |
| A shop with more items than the catalogue holds | Decided: at most 500 items, or the plan's `storefront_items` when lower, with or without a storefront (§4). The limit is said when reached |
| An owner changes counts in the table by hand | Recorded as `outside` movements when the copy refreshes; the Stock page shows them, so the hand edit is explained rather than lost |
| A sale by chat that names an item wrongly | The reply shows what was understood before **yes**, and the numbered list resolves ambiguity; a wrong sale is corrected with an adjustment, never deleted |
| Terminal sales carry no items | Listed in the evening for the owner to say; nothing guessed (a `pos.sale` is an amount, a terminal and a reference: POS terminals strategy §3) |
| Suppliers who answer by phone or SMS, not in chat | The order's status can be set by the owner (`confirmed`); the message is the record. Taskiem receives no SMS |
| Cost prices and shrinkage at cost need a cost per item | The last price received; items with none show shrinkage in units only |

## 12. Out of scope

Barcodes and scanning; multiple shops or stores per business (one stock per business); batches and expiry dates (pharmacies: section M later); fractional quantities and selling by weight; supplier catalogues and price lists (story 55 is the wholesaler's side); accounting valuation methods beyond last cost.
