# Stock: a shop's stock, kept by chat

A shop keeps its stock in Taskiem from WhatsApp or the web. Its catalogue table, in its own Xenibase project, holds the count. Every change to a count is a **movement** Taskiem records first, so a count can always be explained, even one changed in the table by hand. The plan is [stock by chat](spec/stock.md) ([decision 0057](decisions/0057-stock-by-chat.md)); this guide says what is built.

## Keeping stock

Stock is a feature of the catalogue, not an area to turn on. A business keeps stock once its catalogue maps a whole-number **stock** column:

- **Keep my stock**, on **Storefront > Stock** (and offered on Get started), or the first `add 50 cartons of Indomie at 8,500` in chat, sets it up. A business with no storefront gets a draft one under its name: it holds the catalogue, and is never shown until the owner publishes it. A business with no catalogue gets a **products** table with the columns below.
- A catalogue the business already has keeps its own columns: Keep my stock maps the stock columns it has (by name and type) and adds those it lacks (`stock` always; the others while the plan's column limit allows).
- The catalogue's limit holds with or without a page: at most 500 items, or the plan's `storefront_items` when lower.

| Column | Mapped as | What it holds |
| --- | --- | --- |
| `stock` | `stock` | How many are left, in whole units |
| `unit` | `unit` | The unit: bag, carton, piece (none: pieces) |
| `cost_price` | `cost_price` | What one cost, in naira (or kobo, as the price is) |
| `reorder_level` | `reorder_level` | At or below this, the item is low |
| `target_level` | `target_level` | What a low item is ordered back up to |
| `supplier` | `supplier` | Who the business buys it from |

None of these but `stock` is on the public page; a sold-out item shows as before. An item added for stock with no selling price is not shown on the page until it has one (`available` false).

## Movements

A movement is one change to one item's stock: the change, the count before and after, why (`reason`), who, when, and what it came from.

| Reason | Made by |
| --- | --- |
| `order` | A storefront order accepted (`decide`), one per catalogue row, naming the order's reference |
| `sale` | A sale recorded: `sold 3 bags of rice 45,000 cash`, **Record a sale**, `record_sale`, one per item, naming the sale's reference |
| `received` | Goods received: an item added with its quantity, `add 20 cartons of Indomie`, **Record stock** |
| `adjustment` | A correction with a reason: `adjust rice -2 spoilt` |
| `count` | A count: `stock rice 12`, **Record stock > I counted** |
| `outside` | A count changed in the table outside Taskiem, found when the catalogue is read |

How a movement reaches the table:

1. It is recorded in Taskiem's database in the transaction of what caused it, under an idempotency key: the same order, the same chat confirmation or the same `Idempotency-Key` is the same movement.
2. Taskiem reads the item's count and records, on the movement, the count it read and the count it will set. A decrease never takes a count below zero (nor further below it, when the table already held less); what it asked for beyond is kept as `short`.
3. The change is sent to the table only while the table still holds the count read, under a key of the movement and that count, so a retried send lands once. When the count moved first, it is read again (up to five times; then the sweep tries later).
4. Done, its `change` is what landed, and the public page's copy of the count is updated at once.

A movement that cannot reach the table (the project is down) stays pending and is sent again every minute, then less often, up to every hour; the Stock page says so. A movement for an item that is gone is void and moves nothing.

**An item's count is the sum of its movements.** The table is the owner's: anyone with `table.edit` may change a count on the table's page or in Xenibase. When the catalogue is read (every 15 minutes for a catalogue keeping stock, published or not, and before each change Taskiem sends), a count that differs from the sum of the item's movements, while no change of Taskiem's is on its way, is recorded as an `outside` movement ("Changed in the table"; the first count Taskiem sees is "First count seen in the table"). Times are compared on the database's clock, never the application's.

Movements are a record: what was asked and why never change after they are made, and done or void ones never change at all. They are copied, within a minute of landing, to a [kept table](data-tables.md#kept-tables) `stock_movements` in the business's project, for reading, exporting and workflows.

## Sales

A sale is items, how many, the amount and how it was paid: `cash`, `transfer` or `pos` (the terminal). Recording one writes, in one transaction of Taskiem's database:

- the sale (`stock_sales`, with a reference like `S-7QX4KD`), `recorded` when paid in cash, else `waiting` for its money;
- a `sale` movement per item, sent to the table as any movement is; a sale of more than the count is recorded, never refused, and the item is named in the sale's `short`;
- for cash, a `cash` entry in the [money log](money-log.md), naming the sale.

With no amount, the amount is the items' catalogue prices; an item with no price needs the amount.

**Matching.** A sale by transfer or on the terminal waits for its money. Every minute, a waiting sale is matched to money in of that amount (a transfer or payment for `transfer`, a terminal's `pos_sale` for `pos`) within 12 hours of it, the sale's reference in the money's reference first, then the nearest in time. Each money entry counts for one sale only, and once matched it is no longer an unmatched leak. Money a sale takes (its cash takings too) counts like an invoice's or an order's: it is not offered for an invoice or order, cannot be matched to one, and what the matcher had offered it for is closed. A sale naming an item on two lines moves its stock by both.

**Terminal sales with no items.** A terminal's sale is money, not items. Those of a day that no sale names are listed on the Stock page and in the evening total, so the owner can say what was sold (**Say what was sold**, or `sold … pos`): the sale takes that money, for its amount, and the items come off the stock. Nothing is guessed.

**The evening total.** After closing (the storefront's hours for the day, else 21:00 Lagos), the people who get the [money summary](money-reports.md#who-gets-them) are sent today's sales once: the total, by cash, transfer and terminal, what sold most, the sales still waiting for their money, the terminal's sales with no items, and what is low now. A day with no sales sends nothing. Outside WhatsApp's 24-hour window it goes as the alert template. The next morning's money summary carries a line for the sales recorded too.

## Suppliers and purchase orders

A **supplier** is a person or business outside yours ([decision 0039](decisions/0039-reaching-people-outside.md)), kept by name and mobile number (the number sealed, as a rider's is). Added, they get the business's tap-in link by SMS; one tap opens WhatsApp, and from then on their orders reach them there. The items they supply are the catalogue's items whose `supplier` column names them: adding a supplier with items sets it, and renaming one carries the new name to their items.

A **purchase order** is a supplier, lines (an item, how many, the price of one: the item's last cost price when drafted) and a total. It is:

1. **Drafted**, by hand or from the low list: **Order what is low** (`order from Dangote` in chat, `draft_purchase` in a workflow) drafts one order per supplier for the low items naming them and on no open order, each for how many to order (up to its order-up-to level, else twice its reorder level). A low item whose supplier Taskiem does not know is named, not guessed.
2. **Approved and sent** in one step. Above the business's limit (**Suppliers > Approving orders**), the person who drafted it may not send it, nor may a workflow: another owner or admin does. Sent, it goes to the supplier on the chat app they tapped in on, with **Confirm** and **Change**; outside WhatsApp's 24-hour window as the update template, which says to reply CONFIRM or CHANGE. A supplier not yet tapped in gets it by SMS with the link.
3. **Confirmed** by the supplier's tap or word (CONFIRM), which works from their own number only, or marked confirmed by the owner when they answered by phone. **Change** asks them to send `change P-7QX4KD` and what they can supply instead; it is kept on the order and the owners are told. Their words find their order across businesses through a definer function that answers only orders they may still answer.
4. **Received**, in part or in full (**Goods came in**, `received 10 bags of rice` in chat, `receive` with `purchase` in a workflow): a `received` movement per item naming the order, the item's cost price set to the order's, and the value received owed to the supplier until paid. Once every line has come, the order is received.
5. **Paid**, in cash or by transfer (**Record a payment**, `paid Dangote 425,000 transfer`): an `expense` in the [money log](money-log.md) with the supplier as its vendor and the order in its description. What is received and not yet paid is owed to the supplier, shown on **Suppliers** and in chat (`suppliers`).

An order can be cancelled until anything is received on it. A purchase order to a supplier that is also a business on Taskiem, and linked to yours, arrives as an order in their business rather than a message: planned in [business links](spec/business-links.md). The morning [money summary](money-reports.md#the-daily-summary-in-chat) lists what is low, with how many to order.

## Counts, shrinkage and leaks

A **count** is how many of each item are on the shelf: `count rice 12, indomie 40` (a reason after a colon: `count rice 12: rats`), `count` to be asked item by item (a number, *skip*, or *done*), or **Count the shelf** on the Stock page (a sheet to fill, or a CSV of names and counts it reads into the sheet). Shown first and written on **yes**: each item whose count differs gets a `count` movement naming the count (`C-7QX4KD`), with the reason as its note and the item's cost price. A count of every item holding a count is a full count, closing a counting period. `stock rice 12` is still a count of one item.

Every movement keeps the item's cost price when Taskiem knows it (sales, goods received, counts, changes from the web), so **shrinkage** is valued at cost: for a period, what came in, what was sold (sales and orders) and what was lost (counts and adjustments that took stock off), each item's, the largest loss first (`GET /v1/stock/shrinkage`, the Stock page's **Counts and losses**). The morning money summary says what was lost the day before, and the monthly email the month's.

Three things are **leaks**, valued at cost and closed with a reason as other [leaks](leaks.md) are: a sale of more than the stock, a count below zero in the table, and a count missing more than the business's limit (₦5,000 unless set on the Stock page; an item with no cost price always, in units). Two are **owner alerts**, not leaks: an item low for a week with nothing ordered, and an order confirmed and not received after its expected days (three by default). The stock watch looks every hour.

## Templates

Four templates start from the Stock record (Templates, or the Start page):

| Template | What it does |
| --- | --- |
| `low-stock-alert` | Every morning, the items at or below their reorder level, each with how many are left and how many to order |
| `reorder-from-supplier` | Every morning, an order to each supplier for their low items (on no open order), sent after one approval; above the business's limit an order is left for a second person |
| `sales-day-total` | Every evening, the day's sales recorded by chat or the Stock page, by cash, transfer and terminal, with what sold most |
| `stock-count-reminder` | Once a week, a reminder to count the shelf |

The sheet-based `stock-reorder-alert` and `daily-sales-summary` stay for businesses that keep stock or sales in a Google Sheet; a business keeping stock in a table is offered `low-stock-alert` instead of the first, and one recording sales `sales-day-total` instead of the second.

## In chat

On WhatsApp and Telegram (each change shown first, written on **yes**):

| Send | What happens |
| --- | --- |
| `add 50 cartons of Indomie at 8,500` | Adds the item with 50 received and the cost price, or tops up the one there (`add 20 bags rice`, `add 6 Indomie Chicken`; the price after `at`, `@` or `for` is the cost of one) |
| `stock indomie`, `how many bags of rice do I have?` | The count, in its unit, whether it is low, and the last movement |
| `stock` | The items at or below their reorder level, with how many to order |
| `adjust rice -2 spoilt` | An adjustment with its reason (`+3 found` adds) |
| `stock rice 12` | A count |
| `add supplier Dangote Depot 0803 123 4567 for rice, indomie` | A supplier, with the items they supply |
| `suppliers` | Each supplier, their open orders and what is owed to them |
| `order from Dangote` | Their low items as an order, sent on **yes** (or left for a second person above the limit: `send P-7QX4KD`) |
| `received 10 bags of rice` | Goods received on the open order from the item's supplier; with none, goods received as `add` |
| `paid Dangote 425,000 transfer` | A payment on the supplier's oldest order with something owed |
| `confirmed P-7QX4KD` | The supplier said yes by phone |
| `count rice 12, indomie 40: rats` | A count, with its reason; `count` alone asks item by item |
| `sold 3 bags of rice 45,000 cash` | A sale (`transfer`, `pos`; several items: `sold 2 cartons indomie, 1 bag rice 60,000 transfer`; no amount takes the prices) |

Items are found as `price` finds them: the exact name, else the one the words start, else one containing them, a unit's plural or singular alike; several give a numbered list. Seeing stock needs `table.read`; changing it, `table.edit`; starting to keep stock where nothing does, `storefront.manage` too. Recording a sale needs `table.edit`. The stock words are English; the sale words come in Pidgin, Yoruba, Hausa and Igbo too (`i don sell 3 bags rice 45,000 cash`), after the person's language is set (drafts, under review), and so do the words for goods received (`i don receive 10 bags rice`), a count (`i don count rice 12`), an item's stock or what is low (`check stock`, `mélòó ni rice`), and stock taken off with why (`comot rice 2 spoil`). Suppliers and their orders need `storefront.manage`; goods received, `table.edit`. A voice note is read the same way, where voice notes are on.

## On the web

**Storefront > Stock**: the low items first, each item with its count, levels, cost and supplier, **Record stock** (goods came in; some were spoilt, lost or found, with why; I counted), **Change** (name, unit, prices, levels, supplier) and **History**; then every change, by item and reason. **Add items** adds one with how many there are. Above them, **Today's sales** in a pine card (the total, by how paid, how many wait for their money) with **Record a sale** (items, how many, the price of one, how it was paid, the amount taken); the terminal's sales with no items, each with **Say what was sold**; and each sale of the day. **Orders to suppliers** lists the recent orders with **Approve and send**, **Mark confirmed**, **Goods came in**, **Record a payment** and **Cancel** as each fits, and **Order what is low**. **Counts and losses** shows this month's shrinkage, the latest counts, **Count the shelf** and the count's leak limit. **Storefront > Suppliers** lists the suppliers with where their orders reach them and what is owed, **Add a supplier** (with the items they supply), **Change**, **Send the link again**, **Retire**, and the approval limit.

## API

| Route | What it does |
| --- | --- |
| `GET /v1/stock` | Whether the catalogue keeps stock, and its items from the table itself, low ones flagged with how many to order; movements stuck on their way |
| `POST /v1/stock/setup` | Keep my stock (`storefront.manage` and `table.edit`) |
| `POST /v1/stock/items` | An item and how many there are, with an `Idempotency-Key`; 409 `item_exists` with its id |
| `PATCH /v1/stock/items/{id}` | An item's name, prices, unit, levels or supplier; its stock changes by movements only |
| `GET /v1/stock/movements` | Movements, the latest to land first, by `item`, `reason`, `since`, `until` |
| `POST /v1/stock/movements` | Goods received, an adjustment or a count, with an `Idempotency-Key`; 202 while it has not reached the table |
| `POST /v1/stock/sales` | A sale, with an `Idempotency-Key`: `lines` (`item_id`, `quantity`, `price_kobo`), `amount_kobo`, `paid_how`, `note`; `entry_id` gives a terminal's sale its items (409 when that money counts for something already) |
| `GET`/`POST /v1/stock/suppliers`, `PATCH /v1/stock/suppliers/{id}`, `POST .../retire`, `POST .../invite` | Suppliers (`storefront.manage`) |
| `GET`/`PUT /v1/stock/settings` | `purchase_limit_kobo` (above it, a second person sends an order) and `count_limit_kobo` (a count's loss above it is a leak); a PUT changes only what it names |
| `GET`/`POST /v1/stock/counts` | Counts; a count is `lines` (`item_id`, `count`) and a `reason`, with an `Idempotency-Key` |
| `GET /v1/stock/shrinkage?from=&to=` | A period's received, sold and lost, at cost (this month by default) |
| `GET`/`POST /v1/stock/purchases`, `POST /v1/stock/purchases/low` | Orders, drafted by hand or for what is low, with an `Idempotency-Key` |
| `POST /v1/stock/purchases/{id}/send`, `/confirm`, `/cancel`, `/receive`, `/pay` | An order's steps; `receive` and `pay` take an `Idempotency-Key` |
| `GET /v1/stock/sales?day=2026-10-29` | A day's sales (today in Lagos by default), added up by how paid, the top items, how many wait, and the terminal's sales with no items |

## In workflows: `stock@1`

A platform connector acting on the run's own business, needing no connection:

| Action | What it does |
| --- | --- |
| `list_items` | Items and their stock; `low` for those at or below their reorder level (each with `suggest`), by `supplier` or `name` |
| `get_item` | One item by its row id |
| `adjust` | A change with a note |
| `receive` | Goods received, naming where from (`reference`), or on a purchase order (`purchase`) |
| `count` | A count |
| `movements` | Movements, by item, reason and time |
| `low_items` | The low items, by supplier |
| `draft_purchase` | Orders for what is low (one per supplier, or one supplier's), or an order of lines to a supplier |
| `send_purchase` | A drafted order approved and sent; refused above the limit |
| `list_purchases` | Orders, open ones only with `open` |
| `record_sale` | A sale: lines by row id with quantity and price, the amount and how it was paid |
| `sales_day` | A day's sales added up (today when `day` is absent): the total, by how paid, how many, what sold most |

Changes carry the step's idempotency key: a retried step moves stock once.

## For developers

`engine/stock` records and sends movements (`RecordTx`, `Apply`, `Outside`, `OrderLines`), reads items (`Items`), and sweeps (`Sweeper`: pending movements, and the kept copy). The table is reached through `storefront.XenibaseStock`, whose `Change` is the storefront's compare-and-set. The movements are migration 00261; `taskiem_stock_due_tenants` (a definer) finds the businesses with work for the sweep, and `storefronts.keeps_stock` makes `taskiem_storefronts_due` refresh a catalogue keeping stock whether or not its page is published.

Sales are `engine/stock/sales.go` (`RecordSale`, `MatchSales`, `DayTx`), the sentence parser `salewords.go`, and the evening total `evening.go` (`Evening`, queued as `money_digests` kind `sales_evening` and sent by the WhatsApp/Telegram outbox, written at send time). Migration 00262 holds `stock_sales`; a sale's money entry is unique to it.

Suppliers and purchase orders are `engine/stock/purchases.go` (`Purchases`: suppliers, `DraftLowTx`, `SendTx`, `ConfirmTx`, `CounterTx`, `ReceiveTx`, `PayTx`) and migration 00263 (`suppliers`, `purchase_orders`, `purchase_payments`, `stock_settings`, `taskiem_supplier_orders`). The order goes through the chat outbox (kind `supplier`, the order and the supplier's contact); its buttons are signed for the business, the order and the contact (`api/whatsapp_purchases.go`).

Counts and shrinkage are `engine/stock/counts.go` (`CountTx`, `RecordCount`, `ShrinkTx`) and `watch.go` (`Watch`: `stock_low`, the two alerts); the leaks are three of `engine/leaks`' queries. Migration 00264 adds `stock_movements.cost_kobo`, `stock_counts`, `stock_low`, `stock_settings.count_limit_kobo`, `purchase_orders.expected_days` and the leak kinds.
