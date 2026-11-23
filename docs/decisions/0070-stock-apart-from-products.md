# 0070 — Stock apart from products, and a purchase is just something bought

Date: 2026-11-22 · Status: **Accepted** (22 November 2026; not built) · Plan: [What a business buys, and what it does with it](../spec/what-a-business-buys.md) · Amends: [0057](0057-stock-by-chat.md) point 1

## Context

[0057](0057-stock-by-chat.md) made the catalogue table the stock: its `stock` column is the live count, in the owner's own table, with `unit`, `cost_price`, levels and `supplier` beside it. Its alternatives turned down "a stock ledger of Taskiem's own, the table a copy", so that the owner's table stays theirs to see, export and correct.

Opening Taskiem to everyone showed two things that model cannot hold:

- **A business buys many things it does not sell.** Today every purchase line must be one of its products, what arrives raises that product's count, and paying is filed as `stock`. A sewing machine, diesel or a client's fabric becomes a product it would never sell.
- **A business counts things it never sells.** Fabric, thread and flour must be products to be counted, so they take catalogue places (at most 500) and every page that sells has to hide them.

Taskiem is not in production. No business, staging included, relies on the stock it keeps today, so the model can change outright.

## Decision

1. **Stock items, apart from products.**
   - A **stock item** is what a business counts. Taskiem keeps it in its own database, and its count is the sum of its movements.
   - A **product** is still a catalogue row: what the business sells.
   - A product may be **counted by** one stock item, using a whole number of it per sale (`product_stock.uses`). Several products may draw on one stock item.
   - A stock item with no product is a **material**. Stock items are not in the catalogue and do not count against its 500. A business may have up to 5,000.
2. **Counts leave the catalogue.** This amends 0057 point 1.
   - The catalogue loses `stock`, `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier`.
   - Counts change only through Taskiem: the Stock page, chat, `stock@1`, sales, orders and receiving. Each change is a movement with its reason, made in one transaction with the count.
   - The owner's project keeps a read-only copy of stock items, as it does of movements and the money log.
   - Public pages read how many are left through one narrow function that reveals nothing else about a stock item.
3. **A purchase is just something bought.**
   - By default a line is recorded: what, how many, from whom, the cost, and a **label** in the business's own words. That is spending in the money log, and nothing more.
   - The business may instead **add it to stock** (a stock item) or **keep track of it** on **Things we have**.
   - It says which once per supplier item, and Taskiem remembers. Taskiem may suggest, and never decides.
4. **Labels are the business's own.**
   - None are built in, and a label is never required.
   - Every expense carries its label's id, and renaming or merging a label never rewrites the money log.
   - In profit, things bought to keep are left out.
5. **The founder's answers** to the plan's questions ([§10](../spec/what-a-business-buys.md#10-settled-by-the-founder)), all as suggested:
   - Things we have is a list only, with no depreciation.
   - Materials are counted in and out by hand; recipes are a later plan.
   - Suggestions are confirmed by the business the first time.
   - Selling something on Things we have is money in, labelled **Sale of things we had**.
   - An owner's own purchases stay their own.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Keep stock on products and mark materials "never for sale" | Materials would still take catalogue places, and every selling page would have to hide them; stock and selling stay tangled |
| A fixed list of purchase kinds (stock, supplies, equipment) | No list fits every business: gifts for staff, a client's fabric and snacks fall outside any three |
| Keep counts in the owner's table as a copy Taskiem writes | Two places hold a count, so a hand edit and a sale can disagree; the send-and-retry of every change stays |
| Move existing stock per business, old and new paths side by side | Nothing is in production to move; the dual paths would be built only to be taken out |

## Consequences

- **The owner's own table holds no count.** The stock design's "the owner's table is theirs to correct" no longer holds for counts. A correction is an **Adjust**, with its reason, on the Stock page or in chat. Workflows that read or set the old columns are listed for their owners when S1 ships, with what to use instead.
- **Movements are Taskiem's alone.** The send-and-retry of catalogue changes and the outside-change check go.
- **Purchase lines become their own table,** and item links are made again around the supplier's item.
- **Every test, seed and fixture that kept stock on catalogue rows is rewritten.** The demo world's stock is made again.
- **The work:**
  - the split first: S1a, S1b and S2;
  - then purchases: U1, U2a, U2b and U3.

## Provenance

The founder's questions on 21 November 2026 led to the plan: a sewing machine bought by a fashion business, then whether every purchase should become a product. Its drafts were reviewed twice, and on 22 November 2026 the founder took every suggested answer.
