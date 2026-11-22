# What a business buys, and what it does with it

Status: **Settled** 22 November 2026: the founder took every suggested answer (§10). Drafted 21 November 2026; revised 22 November 2026 after review, then to separate stock from products, then for a system not yet in production, then after a second review · Decision: [0070](../decisions/0070-stock-apart-from-products.md) · Builds on: [stock](stock.md) (the stock record, suppliers, purchase orders, receiving), [business links](business-links.md) (orders between businesses, item links, bills), [invoices and the money log](invoices-and-money-log.md) (expenses with a vendor and a category), [money reports](../money-reports.md)

## 1. Summary

A business buys many things it does not sell. A fashion house buys fabric it sews with and a sewing machine it keeps. It buys snacks for the shop, gifts for staff, a client's own fabric for a job, and diesel for the generator. Today every purchase is treated as stock to sell: each line must be one of the business's products, what arrives raises that product's count, and paying for it is an expense filed as `stock`.

Two things are wrong with that, and this plan fixes both.

1. **Stock and products are one thing today.** Stock is a count on a product, so anything a business wants to count must be a product, in the catalogue it sells from (at most 500 items). This plan separates them (§4):
   - a **stock item** is something the business counts: fabric, thread, bags of rice, finished dresses;
   - a **product** is something it sells, on its storefront, price lists and in chats;
   - a product may be **counted by** a stock item, so selling it lowers the count, as today, by however many of it one sale uses;
   - a stock item with no product is a **material**, or anything counted and never sold.
2. **Every purchase must be stock.** No fixed list of kinds of purchase fits every business, so this plan does not make one (§5):
   - **a purchase is just something bought** by default. It records what it was, how many, from whom, what it cost, and a **label** in the business's own words. It is spending in the money log, and nothing else happens;
   - **two things the business may choose to do with a line**, neither required:
     1. **Add to stock:** tie the line to a stock item, so the count goes up and the cost is set;
     2. **Keep track of it:** put each one on **Things we have**, the list of what the business keeps, such as a machine, a generator or a laptop;
   - **said once and remembered**, per supplier item.

The supplier sees nothing different.

**Taskiem is not in production yet**, so this plan changes the model outright: no live data to carry over, no old and new paths kept side by side, and nothing kept only for compatibility. Demo and test data are made again from their seeds.

- **Confirmed by the founder** (§10 Q11, 22 November 2026): no business relies on stock it keeps, staging included. Whatever counts staging holds are made again from the seeds and are not carried over.
- **It reverses a decision.** [Stock §4](stock.md#4-the-stock-record-k1) keeps the count in the owner's own catalogue ("the owner's table is theirs"), changeable by hand there. Moving counts out is recorded as decision [0070](../decisions/0070-stock-apart-from-products.md), with what replaces each use of the catalogue's stock columns (§4.3).

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Stock item | Something the business counts: a name, a unit, a count, a cost, low and target levels, a usual supplier. Kept by Taskiem |
| Product | Something the business sells: a row of its catalogue, with a price, photos and options, as today |
| Counted by | A product's link to the stock item whose count selling it lowers, with how many of it one sale uses (1 by default). A product without one is not counted (services, dishes, things on hire), as a product without a `stock` value is today |
| Material | A stock item that no product is counted by: fabric, thread, flour, packaging the business counts |
| Purchase line | One line of a purchase: what, how many, the unit, the cost, the supplier, a label, and how it is treated |
| Label | The business's own word for what a purchase was for: "Machines", "Staff welfare", "Fuel", "Client job: Mrs Ade". Up to 60 characters (the money log's limit for a category). Taskiem has none built in |
| Treatment | What happens when a line arrives: **just record it** (the default), **add to stock** (a stock item), or **keep track of it** (Things we have) |
| Things we have | The business's list of things it keeps track of, one row per thing received or added |
| Item link | The buyer's memory of a supplier's item ([business links §9](business-links.md#9-l4-price-lists-item-links-and-mentions)). Today it points at a product; this plan points it at a stock item, or at a treatment and a label |

## 3. Where we are

- **Stock is a column of the catalogue** ([stock §4](stock.md#4-the-stock-record-k1)):
  - the catalogue is a table in the owner's own Xenibase project, changed over its API;
  - each row's `stock` column is the count, with `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier`;
  - a product with sizes and colours has one row per variant, each with its own count;
  - every change is a movement in Taskiem's database (`stock_movements`, keyed by the catalogue row's id), recorded first and then sent to the table under its key;
  - an owner who changes a count in the table by hand is seen on the next refresh and recorded as an `outside` movement;
  - the catalogue holds at most 500 items (`storefront.MaxItems`), stock or not.
- **Everything that counts points at catalogue rows:** movements, counts, sales lines, orders' stock, purchase lines (`purchase_orders.lines[].item_id`), item links (`item_links.item_id`), the low list, reorder suggestions, the morning briefing and stock value.
- **Purchase orders** hold `{item_id, item, unit, quantity, cost_kobo, received}`. Receiving (`stock.ReceiveTx`) refuses a line whose `item_id` is not one of the buyer's products, raises each one's count, and sets its cost price to the order's.
- **Orders to a linked supplier.** Every way a business buys from another business on Taskiem ends in such an order, and each asks which of the buyer's products each line is (`say which of your items … is`):
  - from the supplier's price list;
  - from their shop, at storefront prices;
  - by chat, for example `order 20 bags of rice from Dangote`;
  - confirming an order the supplier drafted for us (`links.ConfirmProposedTx`).
- **Paying.** A purchase paid is a money-log expense naming the supplier, with category `stock` (`engine/stock/purchases.go`). Expenses entered by hand already carry a free-text category (`moneylog.Entry.Category`).
- **What goes wrong.**
  - **Bought to use:** to buy a sewing machine, the fashion house must make "Sewing machine" a product. Its stock value grows, the machine can appear on the low list, in reorder suggestions and on the storefront, and reports count it as goods bought to sell.
  - **Materials:** to count fabric, it must make "Ankara fabric" a product too. That takes a catalogue place, so the pages that sell must keep hiding it.

## 4. Stock items, apart from products

### 4.1 The model

- **A stock item** is kept in Taskiem's database (`stock_items`), in the business's scope. Its count is the sum of its movements, as today, so it is the record of reference.
  - It has: a name, a unit, a count, a cost price, a low level, a target level, a usual supplier, a category, and its photo (optional).
  - It has no price and no page of its own: it is never shown to customers.
- **A product** stays a row of the catalogue, with everything a product has today. Nothing about selling changes.
- **Counted by** (`product_stock`): a catalogue row may be counted by one stock item, with **uses**, how many of it one sale of the row takes (a whole number, 1 by default).
  - A stock item may count several rows: rice sold by the bag on two fronts, or a "pack of 6" that uses 6 of a single item. Each sale lowers the stock item by its row's uses.
  - **Variants.** Each variant row with a count is counted by its own stock item, as each has its own count today. The stock items of one product's variants are grouped by it on the Stock page.
- **Counts leave the catalogue.** The catalogue no longer holds `stock`, `unit`, `cost_price`, `reorder_level`, `target_level` or `supplier`: they belong to the stock item.
  - The storefront, the public page, Their shop and price lists read **how many are left** from the stock item through `product_stock`: sold out when the stock item cannot cover one sale's uses.
  - **The read path.** Public pages read published snapshots today, so how many are left comes from one narrow function, `taskiem_product_left(tenant, product_ids[])`, which returns for each product only how many whole sales its stock item covers (or none, when it is not counted). It never returns a cost, a level, a supplier or the stock item itself. The public role and a linked buyer's reads call it; nothing else about stock items is readable to them.
  - Counts change only through Taskiem: the Stock page, chat, `stock@1`, sales, orders and receiving. Every change is a movement with its reason. The outside-change check of today (a count changed by hand in the table) goes, because the table no longer holds one.
  - **The owner's project** gets a read-only kept copy, `stock_items`, beside `stock_movements` and `money_log`, for reading and building on.
- **Size.** Stock items are not in the catalogue, so they do not count against its 500. A business may have up to **5,000** stock items (§10 Q9).
- **Uses are whole numbers**, and a stock item's **unit** is chosen so they stay whole: rice sold by the half kilo is counted in grams (a sale of "500 g" uses 500), fabric sold by the half yard in half yards. The Stock page and chat show the count in the unit it is kept in. Fractions of a use are not allowed.
- **Variants.** Adding a size or colour (the sizes forms, `make one product`) makes its stock item, counted at 0, with the product's unit, cost and levels; removing one retires its stock item (`retired_at`), keeping its movements. A product with 5 sizes and 4 colours has 20 stock items, grouped under it.
- **A product deleted from the catalogue.** `product_id` is a row of the owner's catalogue, which can be deleted there. The catalogue refresh drops a `product_stock` link whose row is gone; its stock item stays, as a material, with its count and history.

### 4.2 What points where after the split

| Points at a stock item | Points at a product (unchanged) |
| --- | --- |
| Movements, counts, the low list, reorder suggestions, stock value | The storefront, fronts, the public page and Their shop (reading how many are left through `product_stock`) |
| Purchase lines, receiving, cost prices | Sales lines and orders (they lower the product's stock item) |
| Item links and `supplier_items` (the buyer's memory, §6) | Price lists, deals and floor prices |
| The morning briefing's low items | Mentions with `#` in a chat |

- **Selling** a product counted by a stock item lowers that stock item by the sale's quantity times the row's uses. The rules are those of today: once, under the order's or sale's key, and a sale beyond the count is recorded with a leak, never refused.
- **Movements are made in Taskiem's database only.** Nothing is sent to a table first, so a movement and its count change in one transaction, and the send-and-retry of today's catalogue changes goes.
- **The seller's side of a link order** finds its stock through its product, as today, then lowers the product's stock item.

### 4.3 What replaces the catalogue's stock columns

| Was | Now |
| --- | --- |
| The owner changes a count by hand in the table | **Adjust** on the Stock page or in chat, a movement with its reason. A hand edit is not possible: the table holds no count |
| A workflow reads `stock` from the catalogue | It reads the kept copy `stock_items` (read-only), or calls `stock@1` |
| A workflow sets `stock` in the catalogue | It calls `stock@1` (`adjust`, `count`, `receive`), which records a movement |
| A connector or report built on the catalogue's `cost_price` | The kept copy's `cost_kobo` |

The owner's workflows that use the old columns are listed for them when S1 ships (a check over their published workflows), with what to use instead.

### 4.4 No move

There is no live stock to move. The new tables replace the catalogue's stock columns and the code that wrote to them, in one change. The demo world's seeds and the test fixtures make stock items directly, and every test that kept stock on catalogue rows is rewritten to the new model.

### 4.5 Screens and chat for stock

- **The Stock page** lists stock items, not products:
  - each shows its count, unit, cost, levels and supplier;
  - **Sold as** names its product, or says **Not sold** for a material;
  - a filter offers **All**, **Sold** and **Not sold**.
- **Add something to count** makes a stock item, asking **Do you sell it?**:
  - **Yes** also makes its product in the catalogue, as `add product` does;
  - **No** makes a material only.
- **A product's page** shows **Counted by**: its stock item and how many one sale uses, with the count, and a way to choose another or none.
- **Chat.** `add 20 yards of ankara fabric` names no product the business has, so it asks one question: **Do you sell it?** Yes adds a product counted by a new stock item; No adds a material. `stock`, `how many … do I have?`, `adjust` and counts work on stock items, materials included.
  - **`add` is already a word** of other commands: `add product …`, `add rider …`, `add <item> <price>`, and stock in (`stock in …`, `received …`). The words decide which: `add product` and a price keep today's meaning; a quantity and a unit with no price (`add 20 yards of …`) is counting something in; anything else is read by the model against the catalogue of actions ([chat menus §8](chat-menus.md)), which gains **Add something to count** as its own action. A test holds the cases apart.
- **The low list and the briefing** include materials, by their low levels.

## 5. A purchase line

### 5.1 Just record it (the default)

- What arrives moves no stock. Receiving still records what came (All came, Not all came) on the order, so a short delivery is disputed and paid for correctly.
- Paying is an expense naming the supplier, the order and the line's **label**.
- This is right for most of what a business buys and does not sell. It also suits anything the business would rather not count.

### 5.2 Add to stock

- The line names a **stock item**, or one made there and then (asking **Do you sell it?** as in §4.5). When it arrives, the count rises and the cost is set, as today.
- **Materials** are stock items with no product. They are counted in and out, are on the low list by their own levels, and never reach the catalogue or a customer.
- The label defaults to **Stock bought**, and the business can change it.

### 5.3 Keep track of it

- When it arrives, no stock moves. Each one becomes a row in **Things we have**:
  - its name;
  - from whom;
  - when it came;
  - what it cost;
  - the order it came on;
  - its label;
  - an optional note, serial number and warranty end date.
- A quantity of 3 makes 3 rows, so each one can be told apart later.
- **At most 50 rows from one line.** A tracked line of more than 50 asks first: "Put 500 nylon bags on Things we have, one row each?", with **Just record them** offered instead. A line over 200 cannot be tracked one by one; it is recorded, and one row can be added for the lot by hand.
- **Things we have** is a page under Money, on the web and in the app. It lists everything with its cost and when it came, and can be grouped by label.
- A row can be marked **sold**, **broken**, **given away** or **gone**. It stays in the history and leaves the totals.
- **Who may:** anyone who may see stock (`table.read`) sees Things we have. Anyone who may buy (purchase orders) may add rows and change their status. A sale with a price is money in, so it also needs permission to enter money.
- **Add something we have** puts on the list anything bought before Taskiem or outside it.

### 5.4 Labels

- **The business's own.** A label is made by typing it. Taskiem offers the business's past labels first, then a suggestion from the item's name and the supplier's category.
- **Renamed or merged** from Money settings, by those who manage money. Past spending is never rewritten: money-log entries are never changed (the `money_entry_kept` trigger, migration 00200, refuses any update but `mirrored_at`).
  - Every expense carries its label's id (`label_id`), and the label's name at the time as its category.
  - Reports find each entry's label by `label_id` when they are read, then follow `merged_into` to the label that is kept.
  - So a rename or a merge changes how reports group spending, at once and for all of it, without touching the log.
- **No label** is allowed, and shows as **Not labelled**, so buying is never blocked by naming.
- **Money reports** group spending by label. Lines added to stock are shown as **Bought to sell or make with**, and tracked lines as **Bought to keep**. Neither is a running cost.
  - In profit for a period, **Bought to keep** is left out: it is something the business has, not money spent running it.
  - **Bought to sell or make with** counts as today's stock bought.
  - Just-recorded lines are running costs under their labels.
- **Expenses entered by hand** (`paid 15,000 diesel`) take a label like any other.

## 6. Saying it once

The question appears where the buyer is now asked which of its products a line is.

> **"Singer heavy-duty sewing machine" from Singer Ltd**
> How should we treat it?
> - **Just record it**
> - **Add to stock**: *which stock item?* (pick one, or make a new one)
> - **Keep track of it**: put each one on Things we have
>
> Label: *Machines* (type or pick)

- **Remembered per supplier item.** The item link holds the treatment, the stock item (for stock) and the label. The next order of the same item from the same supplier asks nothing.
- **A supplier not on Taskiem** has no item links: they belong to a link between two businesses. A new small memory, `supplier_items` (supplier, the item's name as typed, treatment, stock item, label), keeps the same for purchases by hand. The name is matched without case or extra spaces, so "Diesel" and "diesel " are one item.
- **Changed later** from the Suppliers page, where item links are already listed. A change applies to orders received after it, and nothing already received moves.
- **One order line can differ.** On an order, the business can treat a line differently from its link. For example, one phone it usually sells is kept for the till. That changes only the line.
- **A suggestion, never a decision.** Taskiem pre-selects a treatment and a label when it can guess:
  - the business already has a stock item of that name: Add to stock;
  - the business has tracked this kind of thing before: Keep track of it;
  - otherwise: Just record it.

  The business always confirms the first time.
- **In chat.** `order 1 sewing machine from Singer Ltd` asks the same thing as a step of the chat's questions ([chat menus §7](chat-menus.md)), in the business's language:
  - the treatment as three buttons (WhatsApp's buttons, a list where there are more);
  - then the label, as the business's three most-used labels and **Another**, which asks for it to be typed;
  - for **Add to stock**, the stock item, as the item choices offered today, materials included.

  A typed or spoken answer also works: `keep track of it, machines`, `add to stock`, `just record it`.
- **Purchases by hand.** A purchase order to a supplier not on Taskiem asks the same for each line its memory does not know (`supplier_items`, above). An expense recorded by hand, such as `paid 15,000 diesel`, is a line that is just recorded, labelled Diesel.
- **After it arrived.** A line's treatment is locked once anything on it is received: undoing it would move stock back or remove rows someone may have changed. To correct one, the business changes the stock by hand (a count) or the row on Things we have (marked gone). Until something is received, the line can be changed on the order.

## 7. What does not change

- **The supplier.** It sees one order, as today. The buyer's treatment, labels and stock items are never sent through the door.
- **Selling.** The storefront, orders, sales, price lists, deals and Their shop work on products as today. Only where a count is kept moves.
- **Receiving.** All came, Not all came and the day's end work for every line. Only what happens after recording differs.
- **Bills and paying.** A bill from a linked supplier is paid as today. Its expense entry is split by label (§8), by the rules in §8 for a bill that does not match its order.
- **Approval limits** apply to the whole order, as today.
- **The owner's own catalogue** still holds the products. Counts are no longer in it (§4.1); a read-only copy of stock items sits beside it.

## 8. Data

Migration numbers start at 00501.

- **`stock_items`**, new, in the business's scope with row-level security like the other business tables:
  - `id`, `tenant_id`, `name`, `unit`, `category`;
  - `count`, the sum of its movements;
  - `cost_kobo`, `low_level`, `target_level`, `supplier_id`;
  - `group_of`: the product a variant's stock item belongs to, for grouping on the Stock page;
  - `created_by`, `created_at`, `retired_at`.

  At most 5,000 per business. `count` is changed only by a movement, in the movement's transaction (a trigger refuses any other change).
- **`product_stock`**, new: `tenant_id`, `product_id` (the catalogue row), `stock_item_id`, `uses` (a whole number, at least 1), unique on `(tenant_id, product_id)`.
- **`stock_movements`** point at `stock_item_id` instead of a catalogue row, and lose the fields of sending a change to a table (`pending`, the key sent, the retries).
- **The catalogue mapping** loses `stock`, `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier`.
- **The kept copy** in the owner's project gains a `stock_items` table, as `stock_movements` and `money_log` have one today, for reading.
- **`purchase_lines`**, a table of its own in place of the order's `lines` JSON, so a line can be treated, received, billed and labelled on its own:
  - `id`, `tenant_id`, `purchase_order_id`, `position`;
  - `name`, `unit`, `quantity`, `cost_kobo`, `received`;
  - `treatment` (`record` | `stock` | `track`), `stock_item_id` (for stock lines), `label_id`;
  - `seller_item_id`, for an order to a linked supplier.
- **Item links** (`item_links`) are made again as the buyer's memory of a supplier's item:
  - keyed by `(tenant_id, link_id, seller_item_id)`;
  - `treatment`, `stock_item_id`, `label_id`, `updated_by`, `updated_at`;
  - a CHECK holds the stock item and the treatment together: `(treatment = 'stock') = (stock_item_id IS NOT NULL)`.
- **`supplier_items`**, new: the same memory for suppliers not on Taskiem (`tenant_id`, `supplier_id`, `name_key`, `treatment`, `stock_item_id`, `label_id`, `updated_by`, `updated_at`), unique on `(tenant_id, supplier_id, name_key)`, in the business's scope with row-level security.
- **`spend_labels`**, new, the business's own labels: `id`, `tenant_id`, `name` (unique per business, case-insensitive, up to 60 characters), `created_at`, and `merged_into` for a merged label (never a label that is itself merged, so a merge is one step). 
- **Money-log entries** gain `label_id`: every expense carries one, or none for Not labelled. Its `category` is the label's name when the entry was made. Entries are still never changed (§5.4).
- **Receiving** (`stock.ReceiveTx`):
  - for stock lines, it moves the line's stock item and sets its cost;
  - for tracked lines, it writes a `kept_things` row per unit;
  - for recorded lines, it does nothing beyond the receipt.

  It stays in the same transaction and keeps the same receipt key, so a repeated receipt makes nothing twice.
- **`kept_things`**, new, in the business's scope with row-level security like the other business tables:
  - `id`, `tenant_id`, `name`, `label_id`;
  - `supplier_id`, `purchase_order_id` (empty when added by hand);
  - `cost_kobo`, `received_at`;
  - `serial`, `warranty_until`, `note`;
  - `status` (`have`, `sold`, `broken`, `given`, `gone`), `status_at`, `created_by`.
- **The expense.** Paying a purchase writes one money-log expense per label on the order, each naming the supplier and the order. For example: ₦180,000 Machines, ₦12,000 Packaging, ₦40,000 Stock bought.
  - **A part payment** is split in proportion to each label's share of the order, in whole kobo, rounded down; the kobo left over go to the label with the largest share (the first of them, on a tie). The entries always add up exactly to what was paid.
- **A bill that does not match its order.** A linked supplier's bill has its own lines, from their invoice (`bills.lines`), and need not match the order line for line:
  - a bill line is matched to the order line of the same seller item; its label and treatment are that line's, and the **bill's price** is the amount;
  - a bill line that matches no order line (an extra, a delivery charge) is **Not labelled**, for the business to label later;
  - an order line the bill does not have adds nothing to the expense;
  - a bill with no order behind it is split by the item links of its lines where they have one, and is otherwise Not labelled.

## 9. Screens

The web and the app change together. App screens are built in the app's own look ([mobile-app-look.md](mobile-app-look.md)).

- **Stock:** the Stock page of stock items, **Add something to count**, and **Counted by** on a product's page (§4.5).
- **The treatment question and label:** in the order sheets of a supplier's price list, their shop, a supplier-drafted order and a purchase order by hand.
- **Order and bill cards:** each line shows its label and a small mark when it went to stock or Things we have. This is on the buyer's side only.
- **Things we have:** a page under Money, with a row's detail page (status, serial, warranty, note) and **Add something we have**.
- **Labels:** in Money settings, to rename and merge, for those who manage money.
- **Suppliers:** the item links list shows each item's treatment, stock item and label, all of which can be changed there; for a supplier not on Taskiem, its remembered items (`supplier_items`) the same way.

## 10. Settled by the founder

The founder took every suggested answer on 22 November 2026. Each answer below is now the plan.

| # | Question | Answer |
| --- | --- | --- |
| Q1 | Things we have: only a list, or also value going down over time (depreciation) for accounts? | The list only, for now. Depreciation later, with an accountant's view |
| Q2 | Should making something use up its materials (a dress uses 3 yards), or are materials counted in and out by hand? | By hand for now. Recipes that use up stock items when something is made are a later plan, which this split makes possible |
| Q3 | Should a new business start with a few suggested labels, or none at all? | None built in. Suggestions come from its own past spending and the item's name |
| Q4 | Should Taskiem suggest a treatment and label from the item's name, the supplier's category and the business's past purchases? | Yes, pre-selected and confirmed by the business the first time, never decided alone |
| Q5 | Should a label be required? | No. Unlabelled spending shows as Not labelled, and the business can label it later |
| Q6 | When something on Things we have is sold, should the sale be recorded as money in? | Yes, when marked sold with a price typed then: money in, labelled **Sale of things we had**, not the thing's own label, so selling a machine does not read as negative spending on machines |
| Q7 | Personal buying: should an owner buying on their own account (You) be able to move a purchase into the business? | Not in this plan. A business buys as the business |
| Q8 | Can one stock item be counted by several products, each using so many of it? For example, rice sold by the bag on two fronts, or a pack of 6 | Yes: `product_stock` holds how many one sale uses, so it is in the model from the start. A product using several different stock items (a recipe) is Q2's later plan |
| Q9 | How many stock items may a business have? | 5,000, apart from the catalogue's 500, and raised by plan later if needed |
| Q10 | Should counts stay in the owner's own catalogue table, editable by hand? | No: counts leave the catalogue and change only through Taskiem, each with its reason. The owner's project keeps a read-only copy of stock items |
| Q11 | Is it safe to change the model outright, with nothing carried over? | Yes: no business, staging included, relies on stock it keeps today (confirmed). Recorded in decision 0070 |

## 11. Milestones

The split comes first, because Add to stock and every count after it point at stock items. With nothing in production, each milestone changes the model outright, with no old path kept.

| # | What | Size |
| --- | --- | --- |
| S1a | **Stock items and their reads:** `stock_items`, `product_stock` (with variants made and retired with their rows, and links dropped with deleted rows) and movements on stock items; the stock columns out of the catalogue mapping; counts, adjust, the low list, reorder suggestions, stock value, the briefing; `stock@1`; the kept copy; decision 0070; seeds, fixtures and their tests rewritten. Green on its own | M |
| S1b | **Everything that sells or buys through them:** sales and orders lowering through `product_stock` with uses; receiving onto stock items; `taskiem_product_left` and the storefront, public page, Their shop and price lists reading it; chat (`add … ` counting in, and the action); WhatsApp and Telegram; the list of the owner's workflows using old columns. Green on its own | M |
| S2 | **Stock items on the screens:** the Stock page, **Add something to count**, **Counted by** with uses, on the web and in the app | M |
| U1 | **Purchases on the server:** `purchase_lines`; item links made again; `supplier_items`; `spend_labels` and `label_id` on new expenses; receiving by treatment; `kept_things`; the expense split by label, with bills that do not match; OpenAPI and tests | M |
| U2a | **The question and its memory, in the order sheets:** price list, their shop, supplier-drafted, by hand, on the web and in the app, with suggestions; changed from Suppliers; labels renamed and merged | M |
| U2b | **The question in chat:** `order … from …` and the questions' steps (buttons, labels, the stock item), on every channel | S |
| U3 | **Things we have:** the page, its detail, add by hand, statuses (with a sale as money in); spending by label in money reports; web and app | S |

### As built: S1a

**Schema** (migration 00501):

- `stock_items` in the business's scope, with row-level security: the fields of §8, plus `updated_at` and `mirrored_at` for the kept copy. A trigger makes a new one start at 0, refuses more than 5,000 current ones per business (under a lock of the business's), and refuses any change to `count` but one made by a movement's own trigger.
- `product_stock` (`tenant_id`, `product_id`, `stock_item_id`, `uses` ≥ 1), unique on `(tenant_id, product_id)`, its stock item in the same business (a foreign key on both).
- `stock_movements` made again on `stock_item_id`. A movement has its count before, after and its change from the start; `pending`, `void`, the claims, attempts and errors are gone, and so is the `outside` reason. Inserting a movement sets its stock item's count in the same statement's transaction, and only from the count the movement starts from (`taskiem_stock_movement_moves`). After insert, only `mirrored_at` changes.
- `storefronts.keeps_stock` is gone. `taskiem_stock_tenants()` is the businesses with stock items; `taskiem_storefronts_due` refreshes a catalogue whose products are counted (so its deleted rows' links are dropped); `taskiem_stock_due_tenants` is what is left to copy, and sales waiting for their money. `definers.tsv` and `dispatch_columns.txt` follow.

**Engine.**

- `engine/productstock` is the one path from a product to its count: `CountsTx`, `ItemOfTx`, `ProductsOfTx`, `LinkTx`, `UnlinkTx`, `DropGoneTx`.
- `engine/stock` works on stock items:
  - `StockItem`, with `CreateItemTx`, `SetItemTx`, `RetireItemTx`, `StockItemsTx`;
  - `Item`, the list the Stock page, chat and `stock@1` read: a catalogue row with the stock item it is counted by, or a material. Its `id` is the product's row id, or a material's stock item id; `stock_item_id` and `material` say which;
  - `Low`, `Suggest` and `ValueKobo` (stock value at cost, on `GET /v1/stock` as `value_kobo`);
  - `RecordTx` makes the movement and its count in the caller's transaction: sales, orders, receiving, counts, adjustments and the evening total all write in one transaction. Selling or ordering a product that no stock item counts moves nothing (`ErrNotCounted`). Receiving, counting or adjusting one starts counting it, by a stock item of its name (`StartCountingTx`).
- `Apply`, `ApplyPending`, `Outside`, `ReadMark`, `SeenRows`, `LedgerTx`, the sweep's sending and `XenibaseStock`'s `Read`, `Change` and `Tracks` are gone. `storefront.XenibaseCatalogue` reads products for workflows and the live price check.
- `storefront.Columns` loses `stock`, `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier`, and a mapping naming one is refused. The products table that chat and Keep my stock make has none of them. Keep my stock makes a draft page and a products table, and does nothing more to an existing catalogue.
- A stock item's usual supplier is `supplier_id`. Setting one by name finds a current supplier of the business's, and refuses a name that is not one: add the supplier first. Renaming a supplier needs nothing more.

**Variants.**

- A new size or colour gets its stock item at 0, with the product's unit, cost, levels and supplier, `group_of` the product.
- A size taken off sale has its stock item retired, movements kept. One brought back brings its stock item back.
- `make one product` goes through the same path.

**Deleted rows.** The catalogue refresh (`SyncStorefront`), having read the whole table, drops the `product_stock` links of rows that are gone. Their stock items stay, as materials.

**Reads.**

- The low list, the morning summary's low line, the chat's low cards, the watch and `stock@1` all read the same list, so materials are in them.
- `stock@1` `list_items` and `get_item` give `stock_item_id` and `material`. `adjust`, `receive` and `count` take either kind of id.
- `sold …` in chat, `record_sale` and price lists refuse materials.
- The kept copy `stock_items` is made in the owner's project beside `stock_movements`, read-only (`datatables.KeptNames`). Each version of a stock item is written once, in place.

**API.** The JSON field names the web and app read are kept.

- `POST /v1/stock/items` takes `material: true` for a stock item with no product: the demo world's supplies are made this way.
- `PATCH /v1/stock/items/{id}` writes a product's own fields to its row, and the rest to its stock item.
- `GET /v1/stock` no longer has `stuck` nor an item's `waiting`.
- A movement's `status` is always `done`.
- OpenAPI and `mobile/lib/api/api.g.dart` follow. The web and app screens are unchanged (S2).

**The cut line to S1b.** Everything that sells, orders, receives or shows a count goes through `productstock` with today's behaviour: a product's count is its stock item's, and selling lowers it by the quantity, whatever `uses` says. The callers on this interim path are:

- the storefront's accepted orders (`stock.OrderLines`, through `storefront.Orders.Moves`) and an ended order put back (`ReturnOrderTx`);
- chat orders and link orders (the seller's side, as storefront orders of kind `chat` and `trade`);
- sales by chat, on the web and `stock@1` (`RecordSale`);
- purchases received, by hand, by `stock@1` and by a link (`ReceiveTx`, `AdjustShortTx`), whose lines still name a product's row id (or a material's id) and set the stock item's cost;
- the catalogue copy (`storefront.CatalogueItems`), so the storefront, the public page and Their shop show sold out, and price lists show stock;
- the live price check (`XenibaseCatalogue.Items`).

**Left for S1b:**

- `uses` above 1 when selling and when saying how many are left (always 1 now) (done: below);
- `taskiem_product_left` and the public pages reading it, in place of the catalogue copy's count (done: below);
- purchase lines on stock items (U1's `purchase_lines`), in place of a row id resolved through `productstock`;
- `add …` counting in with **Do you sell it?** in chat (done: below);
- the list of the owner's workflows that use the old columns (done: below).

### As built: S1b (workflows on the old columns)

A check, not a data migration; no migration.

- **What it reads.** The catalogue's table and its stored mapping (`storefronts.catalogue_columns` as saved: S1a refuses a mapping naming the old columns and leaves a stored one as it was). An old column's name is the mapping's own when it holds one, else the name Keep my stock gave it (`stock`, `unit`, `cost_price`, `reorder_level`, `target_level`, `supplier`). Then each workflow's published version, and its latest draft when newer.
- **What it finds** (`stock.OldColumnsIn`), in every step, those in containers and `on_error` too:
  - a `xenibase` step on the table (connection `data_tables` or none) naming the column in `select`, `filters` (and `any`) or `order`: **reads**; in `set`, `row`, `rows` or `on_conflict`: **sets** (or reads);
  - such a read with no `select` (every column), when a step fed by its output reads the column off a row (`.stock`, `['stock']`), in turn (a foreach over its rows, the steps inside);
  - a `row_changed` trigger on the table that watches the column, or whose row the workflow reads it off (`trigger.…`): **reads**.
  - Steps on `stock@1`, the kept copy `stock_items` or another table, and another project's connection, are not.
- **Instead** (§4.3): reading `stock` → `count` in the kept copy `stock_items` (read-only), or `stock@1` `get_item` / `list_items`; `cost_price` → `cost_kobo`; `reorder_level` → `low_level` or `low_items`; `unit`, `target_level`, `supplier` → the kept copy's. Setting `stock` → `stock@1` `adjust`, `count` or `receive`; any other → the item on the Stock page.
- **Where.** `GET /v1/stock/old-columns` (`workflow.read`) runs it: each use's workflow, version, published or draft, step, old column, the table's column, reads or sets, and instead. The storefront sweep runs it once per business after each start, after the catalogue refresh.
- **Told once.** Either way the owners are told, the first time anything is found: an alert `stock.old_columns` (`alerts.TellOwners`, one dedup key per business), in their notifications and on their chat as a setup problem, linking to Workflows, each use a line. Running it again tells nothing more.
- OpenAPI follows. The app does not call it, so `mobile/lib/api/api.g.dart` is unchanged; no screens.
- **Not caught:** a column named only inside an expression (`"select": "=…"`, `"set": "=…"`, `"rows": "=…"`), or a table named by one.

### As built: S1b (counting in by words)

**The words.** Read by a parser, never the model (`stock.ParseCountIn`, `waStockCommand`):

- a quantity first, after `add` or `stock in` (new): `add 20 yards of ankara fabric`, `stock in 5 cartons of Indomie at 8,500` (a cost, not a price). `received …` reads the same after its open-order check;
- naming a stock item or product the business has: counted in, shown for a Yes, as before;
- naming nothing it has: one question, **Do you sell it?**, with what it would add;
- `add product …`, `add service …`, `add item|dish … <price>`, `add rider …`, `add supplier …`, `add front …`, `add to …`: no quantity first, so their own commands, unchanged;
- anything else: the model, against the catalogue of actions.

**The answer** (`stock_new`, kept in `chat_sessions` with `waPendingTTL`, as the other one-question flows):

- **Yes**: a product in the catalogue counted by a new stock item, through `stockAdd`, the path the Stock page and the chat used before; not on the page until priced, as then;
- **No**: a material (`stockNew.Material`), nothing in the catalogue;
- either holds the quantity as a `received` movement, under the session's key; the first one also starts keeping stock, as before;
- another answer asks again; *cancel* leaves it.

**Every channel.** One reply with three buttons, Yes, I sell it / No, I don't / Cancel (`sellButtons`): buttons in the Taskiem chat (the Yes named for the question, as other Yeses), reply buttons on WhatsApp, buttons under the message on Telegram. Typed *yes* and *no*, in any language's words, do the same.

**The catalogue.** `stock.new`, **Add something to count**, under Stock: `add 20 yards of ankara fabric`, `table.edit` in the Stock part, asked step by step (what, and how many), and so a form in the app and on the web (M4's). It asks by itself (`asks`), so the model's action, or its form, is read as typed and gets the question.

**Reads.** Tested: a material made this way is in `how many`, `adjust`, `stock` (the low list) at its low level, and `low` on `GET /v1/stock`. Nothing needed changing.

**Words.** `wa.stock.ask_sell`, `ask_sell_first`, `sell_how`, `sell_reminder`, `added_material`, `button.yes_sell`, `button.no_sell` and `menu.stock.new*`; `wa.stock.confirm_new` and `confirm_first` are gone. The other four languages hold the English, marked not translated yet.

**Tests.** `TestParseCountIn` (the words); `TestCountingInWordsToldApart` (add product with a price, add item with a price, a quantity and a unit, `stock in`, `received`, something there, add rider); WhatsApp, the Taskiem chat, Telegram and the model, each to the end.

### As built: S1b (uses and how many are left)

**Schema** (migration 00502):

- `taskiem_product_left(tenant, product_ids[])`: for each product of the business counted by a stock item, `sales_left`, the stock item's count over the product's uses, rounded down, never below 0. A product counted by none is not in it. Nothing else: no cost, level, supplier or stock item. It answers for the business in scope, one actively linked to it, or one with a page online (published or closed).
- Granted to `taskiem_public` (the public page) and `taskiem_app` (the business itself, and a linked buyer's price lists). The dispatch role may read `product_stock.product_id`, `stock_item_id`, `uses` and `stock_items.id`, `count` for it. `definers.tsv`, `dispatch_columns.txt` and the public role's list (`engine/publicweb`) follow.
- `supplier_prices.left_count` goes: a linked buyer's copy of a supplier's prices holds no count.

**Uses.**

- A sale or an order of a product moves its stock item by the quantity times the product's uses (`stock.RecordTx`): the storefront's accepted orders, chat and link orders (`OrderLines`), and sales by chat, on the web and by `stock@1` (`RecordSale`). Once under the order's or sale's key, a sale beyond the count recorded short, never refused. An order put back (`ReturnOrderTx`) names the stock item and puts back what it took.
- `stock.Item` has `uses`. The check before a sale in chat compares the quantity times the uses (`Item.Beyond`). A product using more than one is sold with no unit ("2 Malt, pack of 6"); its default cost on a purchase line is the stock item's times its uses.
- `Move.Uses` says how many of the stock item one of a change is; a sale or an order takes the product's.

**Receiving.**

- A purchase line records `stock_item_id` and `uses` when the order is made (`stock.ResolveLinesTx` in `DraftTx`; a proposed order's lines when the buyer confirms them, `links.ConfirmProposedTx`). The seller's changes keep them.
- `ReceiveTx` moves the stock item named then by the quantity times the uses, at the line's cost over the uses (whole kobo, rounded down), and sets the stock item's cost to that. `AdjustShortTx` takes off the same way. A line naming a stock item directly moves it by the quantity. A line of a product counted by none when the order was made is read when it comes: what the product is counted by then, else it starts counting it. The line records the stock item then.
- A low item's line (`DraftLowTx`) is counted in its stock item (uses 1), and a stock item counting two products is ordered once.
- `purchase_lines` is U1's: the lines stay the order's JSON.

**Counted by.**

- `productstock.LinkTx` takes uses (1 to 1,000,000). `PUT /v1/stock/products/{id}/counted-by` with `{stock_item_id, uses}` counts a catalogue row by a stock item; `DELETE` counts it by none, its stock item kept. `table.edit`, all environments. Not for a material, nor a product sold in sizes and colours. Audited as `stock.counted_by`.
- `stock@1` has no action that sets an item's details: `list_items` and `get_item` give `uses`.
- OpenAPI and `mobile/lib/api/api.g.dart` follow. No screens (S2).

**How many are left.** No copy holds a count, so a sale writes no copy.

- The catalogue copy (`storefront_catalogue`) is written with no `stock`, its sizes none either (`SyncCatalogue`). `CopyCount`, `CopySizeCount` and the copy written after every movement are gone.
- It is read with how many are left: `storefront.Catalogue` through `productstock.LeftTx` (the owner's preview, Their shop in the app, chat, orders and the offer to linked businesses); the public page through `taskiem_product_left` (`publicweb.DBStorefronts`, as the public role); a product in sizes has each size's and theirs together, unknown when a size is not counted (`storefront.WithLeft`).
- A linked buyer's supplier prices (`links.SupplierPricesTx`, behind price lists, Their shop and ordering from a link) read `left` and sold out through `taskiem_product_left`, for a shared list and a storefront's offer alike. The offer copied to the buyer is what is shown, not how many are left, so a sale writes no copy and changes no offer's hash.
- The live price check and the refresh read counts as the business (`CatalogueItems`). The copy keeps the stock item's unit only where one sale uses one of it.

**Left for later:** `add …` counting in with **Do you sell it?** in chat, and the list of the owner's workflows that use the old columns (the other S1b parts); Counted by on the screens (S2); `purchase_lines` (U1).

### As built: S2 (web)

No server change. The app's screens are their own part of S2.

**The Stock page** (`StorefrontStock.tsx`) reads `GET /v1/stock/stock-items`:

- **What you count**: each stock item with its count and unit, levels, cost, supplier and what to order when low. **Sold as** names its products, with "(uses 6 bottles)" when one sale takes more than one, or says **Not sold**.
- A size's or colour's stock item sits under its product (`group_of`), after a line with the product's sizes, total and how many are low.
- The filter **All / Sold / Not sold** is the endpoint's `show`. The line above the list (how many, worth at cost, how many low) follows it; the low card is of everything.
- `GET /v1/stock` stays for whether stock is kept, sales and counts. Record a sale offers products only.
- **Record stock** and **History** name the stock item by its own id, which `POST /v1/stock/movements` and `?item=` take for both kinds.
- **Change** patches a material by its id, and anything sold through its first product (`sold_as[0]`), which reaches the same stock item. Name and price show only for a material (name) or a stock item sold as one product with uses 1 (both): a name or price there is the product's, and the server renames a product's stock item with it.
- **Add something to count** replaces Add items. **Do you sell it?** must be answered: Yes sends the product's fields with an optional selling price (unpriced, it is not on the page, as before); No sends `material: true` and no price.

**Chat tools.**

- A product's form (`ProductForm`) has **Counted by**: its stock item, how many one sale uses and the count. **Change** opens a picker over the stock items, with a search, **None**, and a stepper for uses; it saves with `PUT`, or `DELETE` for None. A product sold in sizes and colours gets the reason and no Change.
- Products and Sell leave materials out. Stock shows a material as Not sold and adds through **Add something to count** (`CountAdd`).

**Gone from the web** (S1a's leftovers): an item's `waiting`, `stuck` changes and their notice, a movement's `pending` status and error, the `outside` reason ("Changed in the table"), and the old Keep my stock words about adding columns to the table.

**Tests.** `lib/stock.test.ts` (Sold as, the rows' words, the grouping, which id a change takes, Counted by's line); `lib/tools.test.ts` (materials in the tools). `e2e/stock.spec.ts`:

- the page with a material **Not sold** and the filter, at 390 and 360 px in both themes;
- **Add something to count** with No and with Yes, and Change by the right id;
- **Counted by** set to uses 6 from the chat and shown on the Stock page, and none for a product in sizes.

### As built: S2 (app)

In the app's own look (`mobile-app-look.md`, Stock and supplies). No server change: it reads `GET /v1/stock/stock-items` (`listStockItems`) and writes through the calls there were.

- **The Stock page** (`chat/stock_page.dart`, the rows' words in `chat/stock_items.dart`). **What you count** lists the stock items, read once, all of them, and filtered on the phone by **All**, **Sold** and **Not sold** (so the tiles keep the whole stock): the count and unit, Low and a level bar, **Sold as** its products (`(6 a sale)` when one uses more than one) or **Not sold**, then cost, low level, order up to, supplier and what to order. A product's sizes and colours are one row (their count together, how many low) with each size's stock item under it. Running low, the Low tile and At cost read the same list (`low`, `value_kobo`).
- **Row actions** keep their ids: a stock item sold as a product acts through its first product's row id, a material through its own id (Record stock, Change, History, as before). Change has no selling price for a material.
- **Add something to count** is the head's +, in place of Add items: a form sheet asking **Do you sell it?** first; Yes sends the selling price, No `material: true`, both through `POST /v1/stock/items` with the quantity as goods received. The old add form is gone.
- **Counted by** on a product's sheet (`ProductForm`, `CountedByRow`): its stock item, how many a sale uses, the count; Change opens `CountedBySheet` (a stock item from the list, not a size's nor a retired one, and the uses) for `PUT …/counted-by`, or Count by none for `DELETE`. A product in sizes and colours says each is counted by its own. Choosing another resets the In stock stepper to the new count, so Save records no count.
- **S1a's leftovers gone** from the app: the `stuck` banner, a movement's `pending` and its error, the `outside` reason, an item's `waiting`, and Keep my stock's words about columns in the table.
- **Tests:** `test/stock_page_test.dart` (the words, the filter, Sold as and Not sold, sizes grouped, a material's and a product's actions, Do you sell it? both ways, large text and targets), `test/product_form_test.dart` (Counted by shown, changed with uses, none, in sizes); `test/looks/stock-*.png` drawn again.
- **Not done:** retired stock items (`retired=true`) are not shown; the products page's own list (Products tool) is unchanged.

### As built: U1 (schema)

Migration 00503; the rows are declared in `engine/stock/purchaselines.go`.

- **`purchase_lines`** in place of `purchase_orders.lines`, which goes: `position` (0 to 49, unique on the order: at most 50), `name`, `unit`, `quantity`, `cost_kobo`, `received`, `delivered` (a linked supplier's, mirrored), `treatment`, `stock_item_id` (exactly for a stock line), `uses` (1 unless stock), `label_id`, `seller_item_id`. A tracked line is at most 200. Once anything on a line is received, its treatment, stock item and uses are locked and it is not deleted (a trigger). The JSON line's `item_id` (the buyer's catalogue row) goes. `purchase_orders.change` and `.short` stay, by position.
- **`item_links`** made again, keyed `(tenant_id, link_id, seller_item_id)`, with `seller_item` (the supplier's name for it), `treatment`, `stock_item_id`, `uses`, `label_id`, `updated_by`, `updated_at`; the buyer's own `item_id` goes.
- **`supplier_items`**: the same, keyed `(tenant_id, supplier_id, name_key)`, with `name` as last typed; the table holds only keys made as `stock.NameKey` makes them.
- **`spend_labels`**: names unique per business without case, merged ones included; `merged_into` (a trigger keeps a merge one step); `created_by`. Never deleted.
- **`money_entries.label_id`**; `taskiem_money_entry_kept` now refuses any change but marking an entry copied. The kept `money_log` gains `label_id`.
- **`kept_things`**, with `purchase_line_id` and `unit_no` (unique together: receiving writes each unit once) and `sale_entry_id` (the money in of a sale).
- Composite foreign keys keep every reference in the business. None of the new tables is copied to the owner's project.

### As built: U1 (buying)

No migration: on 00503. `engine/stock/purchases_lines.go` (lines, memory, suggestions), `purchases_things.go` (Things we have); the API in `api/stock_purchases_u1.go`.

**Lines.** `stock.PurchaseLine` is a row of `purchase_lines` (`id`, `position`, `item`, `unit`, `quantity`, `cost_kobo`, `received`, `delivered`, `treatment`, `stock_item_id`, `uses`, `label_id`, `seller_item_id`) plus:

- `item_id`, how the order's steps name a line: the linked supplier's item, else a stock line's stock item, else the line's own id. Receiving takes it, the line's id, or a product counted by a stock line's stock item (chat's `received …`, `stock@1`);
- `unconfirmed`: the business has no memory of the item from this supplier, so the treatment and label are Taskiem's suggestion, for U2a's sheets to ask.

Every way of buying writes lines through `stock.PutNewLinesTx`: by hand, from the low list (stock lines), from a price list or their shop, chat and mentions, a workflow's `stock@1`/`link@1`, and an order a linked supplier drafts for us (`links.PlaceOrderTx`). The seller's steps change only quantity, price, received and delivered (`stock.SaveLinesTx`): a change accepted or countered drops the lines it zeroes and numbers the rest again; a tracked line grown past 200 is recorded. `change` and `short` stay JSON by position.

**Treatment on drafting** (`treatLinesTx`), per line:

1. said on it (`treatment`, `stock_item_id`, `uses`, `label_id`, `track_many`), checked; remembered when nothing was (the first time is the confirmation); a line that differs from a memory changes only itself;
2. else as remembered: `item_links` by `seller_item_id` for a linked supplier, else `supplier_items` by `NameKey`;
3. else suggested (`stock.SuggestTx`): a current stock item of that name, to stock; a name tracked before (a tracked line or a row of Things we have), track; else record. The label is the last one a line of that name had. A suggested track over 50, and any track over 200 from memory or suggestion, is recorded instead.

`item_id` on a line by hand, on a price-list line or in `confirm-proposed`'s `items` still names one of our items (a product or a material): the line goes into its stock item, with the product's uses (a product counted by none starts being counted, `stock.LineOf`). Chat and mentions suggest (not remember) the stock item of an own item of the same name. A line by hand into a stock item, to a linked supplier, gets the one `seller_item_id` remembered as going into it, so it reaches them as their own item.

**Confirming and changing.** `PATCH /v1/stock/purchases/{id}/lines/{position}` (`table.edit`): `{treatment, stock_item_id, uses, label_id, track_many, remember}`, until anything on the line is received (409 `line_locked`; the 00503 trigger too). The memory is written the first time, or with `remember`. `confirm-proposed` takes `lines` (a choice or null per line) or `items`, or nothing, and remembers every unconfirmed line as it then is. Sending does not confirm: nothing is remembered without the business saying so.

**Tracking many.** Over 50 without `track_many`: 409 `track_ask` with `line`, `item`, `quantity` (and `stock.TrackError`); over 200: 422 `track_too_many`.

**Memory.** `item_links` and `supplier_items` read as `stock.Remembered` (with the stock item's and label's names). `GET|PUT /v1/item-links/{link}` (PUT `{seller_item_id, seller_item?, item_id? | treatment…}`), `DELETE /v1/item-links/{link}/{seller_item_id}`; `GET|PUT|DELETE /v1/stock/suppliers/{id}/items` (`?name=` to forget). A change applies to orders drafted after it.

**Receiving** (`stock.ReceiveTx`, the same transaction and key): stock lines move their stock item by quantity × uses at the cost over uses and set its cost; tracked lines insert one `kept_things` row per unit that came, `unit_no` after those received, `ON CONFLICT DO NOTHING`; recorded lines nothing. A repeated receipt is found by its audit entry (`purchase.received` with its key, written in the receipt's transaction), so a receipt with no stock line is repeat-safe too. `AdjustShortTx` (a short count agreed after the day) moves stock lines back and marks the rows of units that did not come `gone`.

**Open orders.** `stock.OnOrderTx` is the stock items on open orders: the low list's drafting, the low-a-week watch, low cards in chat and `link@1` `low_orders` read it (`Item.OnOrder`). Low cards and `low_orders` find a linked supplier's item through the memory's stock item; a size's card finds their product through its size.

**The supplier** sees the same order: `WriteOrderTx` sends name, unit, quantity, price and the seller's own item (`seller_item_id`, so a bill line's `item_id` is still the order line's `seller_item_id`); never a treatment, stock item or label (tested).

**Things we have.** `GET /v1/things` (`table.read`; `all=true` with those no longer had; `group=label`, merged labels under the kept one), `GET /v1/things/{id}`, `POST /v1/things` and `PATCH /v1/things/{id}` (`table.edit`). Sold with `sale_price_kobo` needs `invoice.manage`: one money-log entry in (`thing-sale:<id>`, category **Sale of things we had**, no label), its id on the row.

**Web and app.** Types only: `PurchaseLine` and `ItemLink` follow; Prices and the order sheet find our item through the memory's stock item. The app's client is unchanged (it calls none of these). No screens (U2a).

**Not done:** labels made or suggested by name (the money side's); U2b's chat question (chat drafts as suggested).

### As built: U1 (money)

`engine/spend`; no schema change.

- **Labels.** `spend.FindOrMakeTx` finds a name without case (a merged label's name finds the label kept) or makes it. `ListTx`: labels not merged, most used first (money-log entries and purchase lines, merged labels' included). `Suggest`: the business's labels sharing a word with the item's name or the category, then its others, then one new name (the category, else the item name's last word, with a capital) when no label has it. `RenameTx` refuses another label's name and a merged label; `MergeTx` follows a merged `into` to the label kept and moves the labels merged into the one merged first.
- **API.** `GET /v1/money/labels` (`?item=`, `?category=`: `{labels, suggestions}`), `POST /v1/money/labels {name}` (201 made, 200 found), `PATCH /v1/money/labels/{id} {name}`, `POST /v1/money/labels/{id}/merge {into}`. Reading needs `invoice.read` or `table.read`, making `invoice.manage` or `table.edit`, renaming and merging `invoice.manage`. Made, renamed and merged are audited.
- **Paying an order** (`stock.PayTx` through `spend.RecordPurchaseTx`): one expense per label kept, in the order labels first appear on the lines, Not labelled lines together with no label; category the label's name then. A stock line with no label is given **Stock bought** (found or made) when the order is paid, and keeps it. Shares by `spend.Split` (rounded down, the kobo left to the largest share, the first on a tie). References `purchase:<key>`, then `/2`, `/3`; one `purchase_payments` row per entry.
- **A linked supplier's bill.** Its expense is the payout that pays it (one movement, paired with the supplier's credit), so it is split when read, not written: `spend.BillPartsTx`. The mirror writes `seller_item_id` and `purchase_id` on each bill line raised from a linked order's line (matched by item, unit and agreed price, in the seller's scope). A bill declared paid outside Taskiem has no entry, as before.
- **Entered by hand.** `POST /v1/money/entries` takes `label` (found or made) or `label_id` (the label kept), for an expense only; the category is then the label's name, as written. `paid 15,000 diesel` (and `spent …`, `expense …`, and the other languages' words) labels the expense from its description with a capital (Diesel), shown in the business's own spelling of a label it has, found or made at Yes. The chat's word map to fixed categories (fuel, rent …) is gone.
- **Report.** `GET /v1/money/spending` (`moneyreport.SpendingOf`): settled expenses and bill payouts by label kept, each split by treatment: a purchase's entry by its order's lines under the same label kept now (`spend.EntryPartsTx`; the whole order if no line has it any more), a payout by its bill's lines. Profit is own money in less fees and costs, leaving out Bought to keep.

### As built: U2a (web)

No server change. The words and requests in `web/src/lib/treat.ts`; the screens' parts in `web/src/pages/TreatLine.tsx`.

- **The question** (`TreatQuestion`): "**item** from **supplier**: How should we treat it?", Just record it / Add to stock (Which stock item?, from `GET /v1/stock/stock-items`, or **A new stock item…** asking **Do you sell it?**: Yes makes a product with an optional price, No a material, `POST /v1/stock/items`) / Keep track of it; **Label** typed, or picked from chips: a suggestion of the business's own, its labels by use, then new names (`GET /v1/money/labels?item=&category=`). A typed name is found or made (`POST /v1/money/labels`) when the order is drafted.
  - Not remembered: opens on a suggestion (a stock item of the name: to stock; a name on Things we have: track; else record; the first label suggested), or on the line's own when the server suggested it (`unconfirmed`).
  - Remembered: one line ("Keep track of it · Machines") with **Change for this order**, which opens the question with **Remember for next time** and **As remembered**. Received: locked ("It came, so it stays as it is").
  - 409 `track_ask` and 422 `track_too_many` show "Put 60 Nylon bags on Things we have, one row each?" with **Yes, one row each** and **Just record them**, and draft again.
- **Sheets.** A supplier's price list (`PriceLists.tsx`) and their shop's review (`ChatBuyer.tsx` `OrderSheet`) send each line's treatment, or nothing for one as remembered; a change with Remember is `PATCH …/lines/{position}` with `remember` after drafting. A product sold in sizes keeps "Which of your items" (`item_id`, its sizes matched by the server). A supplier-drafted order (`PurchaseView` in chat, `ConfirmProposed` on its page) asks each line and sends `confirm-proposed` `lines`. **New order** on the Stock page's orders (`OrderByHandModal`) is an order by hand to a supplier not on Taskiem, asking each line unless `GET /v1/stock/suppliers/{id}/items` remembers its name.
- **An order's page** (`/purchases/{id}`, now opened for every order): **How each is treated**, the unconfirmed lines asked and the others changed for this order, saved by `PATCH` per line. The Stock page's order row says "Say how to treat 2 items".
- **Cards** (buyer's side): each line of an order (its page, the chat card) and of a bill (its page, the chat card, through the bill line's `purchase_id` and `seller_item_id`) shows its label, or **Not labelled**, and **To stock** or **On Things we have**.
- **Suppliers**: **What you buy** lists what is remembered (item links, or a supplier not on Taskiem's items), with Change and Forget for `storefront.manage`.
- **Money > Labels** (`/money/labels`, the tab for `invoice.manage` only): the labels with how often each is used, **Rename** (refusing another label's name: merge instead) and **Merge** into another.
- **Tests:** `lib/treat.test.ts` (15); `e2e/buying.spec.ts`: the sewing machine kept track of as Machines and the next order asking nothing, 60 nylon bags asked first and just recorded, an order's lines asked, changed with Remember and locked, the Suppliers page's change and forget, a label renamed and merged, at 390 and 360 in both themes. `price-lists`, `chat-buyer` and `link-orders` follow the question.
- **Not done:** Things we have's page and U2b's chat question; a line's label merged since shows Not labelled on cards (the list reads labels kept).

### As built: U2a (app)

No server change. `mobile/api.txt` gains `setPurchaseLine`, `listItemLinks`, `putItemLink`, `deleteItemLink`, `listSupplierItems`, `putSupplierItem`, `deleteSupplierItem`, `listSpendLabels`, `makeSpendLabel`, `renameSpendLabel`, `mergeSpendLabel`. Look: `mobile-app-look.md`, Stock and supplies.

- **The question** (`chat/treat_line.dart`: `Treat`, `TreatChoices`, `TreatQuestion`, `LabelPickSheet`, `NewStockItemSheet`). Stock items from `listStockItems`, labels from `listSpendLabels`; a label typed is made (`POST /v1/money/labels`) when chosen or saved; a new stock item through `POST /v1/stock/items` (`material` for No).
- **Their price list and their shop** (`OrderSheet`'s review): each bag line is asked unless remembered (`item_links` by `seller_item_id`); unremembered lines are pre-selected on the phone (a stock item of the same name: Add to stock; a label sharing a word with the name) and sent with `treatment`, `stock_item_id`, `uses`, `label_id` (the first time is the confirmation); a remembered line sends nothing, or its fields when changed for this order, and with Remember for next time a `PUT /v1/item-links/{link}` first. The old "which of your items" choice, and making an item per line, are gone; keeping stock on Taskiem is asked only when a line goes to stock. Reorder finds their item by the last order's `seller_item_id`.
- **Supplier-drafted** (`PurchasePage`'s Confirm it, `PurchaseView`'s Confirm their quote): `unconfirmed` lines asked, others compact; `confirm-proposed` with `lines` (a line changed or unconfirmed with its fields, else null).
- **By hand** (and every open order, `PurchasePage`'s How each is treated): each line not yet received with the question; Confirm (unconfirmed) or Save sends `PATCH …/lines/{position}`, with `remember` from the switch; a line with anything received is shown locked.
- **Answers:** 409 `track_ask` asks "Put N item on Things we have, one row each?" and sends again with `track_many`, or as Just record them; 422 `track_too_many` sets the line to Just record it and says why; 409 `line_locked` says so and reads the order again.
- **Cards:** `LineMark` (label; To stock or Things we have) under each line of an order to a supplier and of a bill (a bill line matched to its order line by `purchase_id` and `seller_item_id`; unmatched, Not labelled).
- **Suppliers:** What you buy from them (`chat/supplier_items.dart`): item links for a linked supplier, `supplier_items` otherwise; changed (`PUT`, `track_ask` asked) and forgotten (`DELETE`), for `storefront.manage`.
- **Labels** (`chat/labels_page.dart`), Money › Settings for `invoice.manage`: by use, Rename (`PATCH`), Merge into another (`POST …/merge`).
- **Tests:** `test/purchases_page_test.dart` (asked and confirmed with a label picked, a label made and a material made, an order by hand changed with track_ask, line_locked, Suppliers' items changed and forgotten), `test/order_sheet_test.dart` (asked on placing, a remembered item sends nothing, changed and remembered), `test/purchase_view_test.dart`, `test/labels_page_test.dart`; `test/treat_fixture.dart`; looks `labels`, `purchase-confirm`, `purchase-lines`, and `money` drawn again.
- **Not done:** the question in chat's own order flows (`order … from …`, U2b); a suggested label not yet a label (from the server's `suggestions`) is offered in the picker but never pre-selected; uses other than 1 are kept, not asked.

## 12. Accept

**The split**

- Rice counted in grams: a "1 kg" product uses 1000 and a "500 g" one 500, and selling one of each lowers the stock item by 1500.
- Adding a size to a product makes its stock item at 0; removing it retires the stock item and keeps its movements.
- A product row deleted in the owner's catalogue: after the refresh, its stock item is a material with its count unchanged.
- The public page shows sold out from `taskiem_product_left`, and nothing else about the stock item is readable to the public role.
- `add product Jollof rice 2500` adds a product as today; `add 20 yards of ankara fabric` asks Do you sell it?

- A product counted by a stock item, sold on the storefront, lowers that stock item once, by the sale's quantity times its uses, in the sale's transaction.
- A "pack of 6" and a single, both counted by the same stock item: selling one pack lowers it by 6, and the storefront shows the pack sold out when fewer than 6 are left.
- The storefront shows sold out from the stock item, and the catalogue table holds no count.
- A fashion house adds "Ankara fabric" and answers **No** to Do you sell it?:
  - a stock item with no product;
  - nothing new in the catalogue, which still has its 500 places;
  - it is on the low list at its low level.
- A product with sizes: one stock item per size, grouped under the product on the Stock page.
- No count changes except through a movement with its reason.

**Purchases**

- A fashion business orders a sewing machine from a linked supplier and chooses **Keep track of it**, labelled Machines:
  - when it arrives, no stock moves and a row appears on Things we have;
  - paying records an expense labelled Machines, naming the supplier;
  - the next order of the same machine asks nothing.
- It orders snacks for the shop and chooses **Just record it**, labelled Staff welfare: no stock moves, nothing is tracked, and paying records the expense under Staff welfare.
- It orders fabric and chooses **Add to stock**, the material "Ankara fabric": the count rises and its cost is set, and nothing reaches the catalogue, a price list or a customer.
- An order mixes fabric (to stock), nylon bags (recorded, Packaging) and a machine (tracked, Machines):
  - one bill;
  - three expense entries that add up to it;
  - stock moved only for the fabric;
  - one row on Things we have.
- Not all came on the machine line records the short count and disputes it, as for any line, and adds no row for what did not come.
- The supplier's side of each of these orders is the same as today.
- A treatment changed on the Suppliers page applies only to orders received after the change. One line treated differently on an order does not change the link.
- A line's treatment cannot be changed once anything on it was received.
- Labels "Snacks" and "Staff welfare" merged into Staff welfare: the money report shows all past snack spending under Staff welfare, and no money-log entry was changed.
- A part payment of ₦100,000 on an order of ₦180,000 Machines and ₦12,000 Packaging and ₦40,000 Stock bought writes three entries that add up to exactly ₦100,000.
- A bill with a delivery charge not on the order: the charge is Not labelled, and the rest is split by the order's labels at the bill's prices.
- A purchase by hand from a supplier not on Taskiem: the second order of "Diesel" asks nothing.
- Tracking 500 nylon bags asks first and offers Just record them; 300 cannot be tracked one by one.
