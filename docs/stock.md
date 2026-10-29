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
| `sale` | A sale recorded (K2) |
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

## In chat

On WhatsApp and Telegram (each change shown first, written on **yes**):

| Send | What happens |
| --- | --- |
| `add 50 cartons of Indomie at 8,500` | Adds the item with 50 received and the cost price, or tops up the one there (`add 20 bags rice`, `add 6 Indomie Chicken`; the price after `at`, `@` or `for` is the cost of one) |
| `stock indomie`, `how many bags of rice do I have?` | The count, in its unit, whether it is low, and the last movement |
| `stock` | The items at or below their reorder level, with how many to order |
| `adjust rice -2 spoilt` | An adjustment with its reason (`+3 found` adds) |
| `stock rice 12` | A count |

Items are found as `price` finds them: the exact name, else the one the words start, else one containing them, a unit's plural or singular alike; several give a numbered list. Seeing stock needs `table.read`; changing it, `table.edit`; starting to keep stock where nothing does, `storefront.manage` too. The words are English for now; the other languages' come with sales (K2).

## On the web

**Storefront > Stock**: the low items first, each item with its count, levels, cost and supplier, **Record stock** (goods came in; some were spoilt, lost or found, with why; I counted), **Change** (name, unit, prices, levels, supplier) and **History**; then every change, by item and reason. **Add items** adds one with how many there are.

## API

| Route | What it does |
| --- | --- |
| `GET /v1/stock` | Whether the catalogue keeps stock, and its items from the table itself, low ones flagged with how many to order; movements stuck on their way |
| `POST /v1/stock/setup` | Keep my stock (`storefront.manage` and `table.edit`) |
| `POST /v1/stock/items` | An item and how many there are, with an `Idempotency-Key`; 409 `item_exists` with its id |
| `PATCH /v1/stock/items/{id}` | An item's name, prices, unit, levels or supplier; its stock changes by movements only |
| `GET /v1/stock/movements` | Movements, the latest to land first, by `item`, `reason`, `since`, `until` |
| `POST /v1/stock/movements` | Goods received, an adjustment or a count, with an `Idempotency-Key`; 202 while it has not reached the table |

## In workflows: `stock@1`

A platform connector acting on the run's own business, needing no connection:

| Action | What it does |
| --- | --- |
| `list_items` | Items and their stock; `low` for those at or below their reorder level (each with `suggest`), by `supplier` or `name` |
| `get_item` | One item by its row id |
| `adjust` | A change with a note |
| `receive` | Goods received, naming where from (`reference`) |
| `count` | A count |
| `movements` | Movements, by item, reason and time |

Changes carry the step's idempotency key: a retried step moves stock once.

## For developers

`engine/stock` records and sends movements (`RecordTx`, `Apply`, `Outside`, `OrderLines`), reads items (`Items`), and sweeps (`Sweeper`: pending movements, and the kept copy). The table is reached through `storefront.XenibaseStock`, whose `Change` is the storefront's compare-and-set. The movements are migration 00256; `taskiem_stock_due_tenants` (a definer) finds the businesses with work for the sweep, and `storefronts.keeps_stock` makes `taskiem_storefronts_due` refresh a catalogue keeping stock whether or not its page is published.
