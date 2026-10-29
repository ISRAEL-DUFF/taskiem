# 0057 — Stock by chat: the catalogue table is the stock, every change is a kept movement, sales and purchases move it, suppliers are people outside the business

Date: 2026-10-28 · Status: **Proposed** · Plan: [stock by chat](../spec/stock.md)

## Context

A storefront's catalogue is one of the business's own data tables, and mapping an integer `stock` column makes orders refuse what is sold out and lower the count when accepted ([storefronts](../storefronts.md#stock)). That is the whole of stock today: a count the owner edits in the table or with `stock <item> <n>` in chat, with no record of why it changed, no sales but storefront orders, no reorder levels but a template over a Google Sheet, and no suppliers.

The user-story catalogue's everyday-owner stories for a shop (45 to 48, 56) are different in kind from a count: *keep my stock list by chat*, *text me every morning what is low*, *draft a purchase order to my supplier when stock runs low and send it after I approve*, *reduce stock when a sale is recorded and let me ask how many bags of rice I have*, *let me record sales by voice note during the day and work out the total at night*. The shops Taskiem is for sell at a counter and on a POS terminal far more than on a storefront; the storefront's stock has to be the shop's stock.

Taskiem has the pieces: the owner's own tables in their Xenibase project, **kept tables** that only Taskiem writes ([data tables](../data-tables.md#kept-tables)), the money log with cash and POS sales ([money log](../money-log.md)), approvals, people outside the business reached in chat ([decision 0039](0039-reaching-people-outside.md)), voice notes transcribed as if typed ([0029](0029-languages-and-voice-notes.md)), leaks, and the daily summary.

## Decision

1. **The catalogue table stays the stock.** Its `stock` column is the live count, in the owner's own table where they can see and export it; stock by chat adds columns it maps (`unit`, `cost_price`, `reorder_level`, `target_level`, `supplier`) and never a second list of items. Counts are whole numbers of an item's unit.
2. **Every change is a movement Taskiem keeps.** The table is in the owner's Xenibase project and changed over its API, so no transaction spans it and Taskiem's database; movements take the money log's shape. Each movement (item, change, the count after, the reason: order, sale, received, adjustment, count, outside; who, when, and the order, sale or purchase it came from) is recorded in Taskiem's database in the transaction of what caused it, under an idempotency key. The change to the table is sent after that, under the same key, and only while the table still holds the count that was read, as orders do today, so a retried step never moves stock twice. The kept table `stock_movements` is a copy for reading. A count changed in the table outside Taskiem is found when the catalogue copy refreshes and recorded as an `outside` movement, so a count can always be explained.
3. **Sales move stock and money together.** `sold 3 bags of rice 45,000 cash` records the sale, three movements and a cash takings entry in the money log in one transaction in Taskiem's database; a sale paid by transfer or on the terminal is a target for matching, matched once to the money the rail reported, so the shop's takings and its stock agree. Sentences are read by word lists and a parser, never by the model. A storefront order accepted lowers stock as today, now with a movement. Voice notes work where the beta is on.
4. **The low list comes from the table, every morning**, from each item's reorder level, and the Google Sheet template is kept for businesses that keep stock in a sheet.
5. **Suppliers are people outside the business.** A purchase order is drafted from the low list (quantities up to each item's target, the last cost paid), the owner approves it under the business's policy, and it goes to the supplier in chat with **Confirm** and **Change** (Taskiem receives no SMS, so a supplier on SMS gets a link to WhatsApp and the owner marks the order confirmed until they move); receiving the goods (`received 50 cartons of Indomie`) raises stock with a movement and closes the order; what was paid is an expense in the money log, or a payout under approval when paid through Taskiem.
6. **Counts and shrinkage are first-class.** A stock count by chat posts each difference as an adjustment with a reason; the daily close and the monthly email show takings against stock movements and the shrinkage at cost; leaks, valued at cost, open for a sale of more than the stock, a negative count, and a count difference above the owner's limit. An item low for a week with no order and a purchase order not received are owner alerts, not leaks, since leaks are money.
7. **Nothing here needs a storefront, and nothing needs turning on**: a shop with no public page gets the products table from `add 50 cartons of Indomie at 8,500` or from the Stock page, and has stock from then on, within the catalogue's limit (at most 500 items, or the plan's `storefront_items` when lower; no higher limit without a storefront); chat and the web app's Stock page are two doors to one engine path.

## Alternatives considered

| Option | Why not |
| --- | --- |
| A stock ledger of Taskiem's own, the table a copy | The owner's table is theirs to see, export and correct; Taskiem's record of movements beside it gives the audit without taking the list away (0045's shape: the business's table, Taskiem's log of reference with a kept copy) |
| Movements only in a kept table in the owner's project, written with the count | The table is reached over Xenibase's API, so the two writes cannot share a transaction, and a table with `table.edit` can be changed outside Taskiem (0045's reason for keeping the money log in Taskiem) |
| Stock as a column only, as today, with chat commands to set it | A count with no history cannot say why it moved; the stories ask for sales, reorders and purchases, which are movements |
| Reading sales from the POS terminal's events as the only sales record | A `pos.sale` carries an amount, a terminal and a reference, not items (POS terminals strategy §3); the sale by chat names the items and the terminal event is what it matches |
| Purchase orders by email or PDF | Suppliers in this market are on WhatsApp; a message with Confirm is what they answer |

## Consequences

- A `stock@1` platform connector, movements and sales recorded in Taskiem's database with `stock_movements` as a kept copy, sales as a target for matching, chat commands for stock, sales, counts and purchases in the five languages, a Suppliers page beside Riders, purchase orders under approval, templates for the morning low list and reorders, leaks and summary lines.
- The storefront's order acceptance writes a movement.
- The POS terminals strategy's "who was at the terminal" and "what was sold" questions get their first answer from the sale by chat.

## Provenance

From the founder on 28 October 2026, choosing stock by chat as the next piece after dispatch, from the story catalogue's stock, orders and deliveries section.
