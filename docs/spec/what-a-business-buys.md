# What a business buys, and what it does with it

Status: **Draft** 21 November 2026, for review and discussion; revised 22 November 2026 after review, and again the same day to separate stock from products · Decision: to come (0070) · Builds on: [stock](stock.md) (the stock record, suppliers, purchase orders, receiving), [business links](business-links.md) (orders between businesses, item links, bills), [invoices and the money log](invoices-and-money-log.md) (expenses with a vendor and a category), [money reports](../money-reports.md)

## 1. Summary

A business buys many things it does not sell. A fashion house buys fabric it sews with and a sewing machine it keeps. It buys snacks for the shop, gifts for staff, a client's own fabric for a job, and diesel for the generator. Today every purchase is treated as stock to sell: each line must be one of the business's products, what arrives raises that product's count, and paying for it is an expense filed as `stock`.

Two things are wrong with that, and this plan fixes both.

1. **Stock and products are one thing today.** Stock is a count on a product, so anything a business wants to count must be a product, in the catalogue it sells from (at most 500 items). This plan separates them (§4):
   - a **stock item** is something the business counts: fabric, thread, bags of rice, finished dresses;
   - a **product** is something it sells, on its storefront, price lists and in chats;
   - a product may be **counted by** a stock item, so selling it lowers the count, as today;
   - a stock item with no product is a **material**, or anything counted and never sold.
2. **Every purchase must be stock.** No fixed list of kinds of purchase fits every business, so this plan does not make one (§5):
   - **a purchase is just something bought** by default. It records what it was, how many, from whom, what it cost, and a **label** in the business's own words. It is spending in the money log, and nothing else happens;
   - **two things the business may choose to do with a line**, neither required:
     1. **Add to stock:** tie the line to a stock item, so the count goes up and the cost is set;
     2. **Keep track of it:** put each one on **Things we have**, the list of what the business keeps, such as a machine, a generator or a laptop;
   - **said once and remembered**, per supplier item.

The supplier sees nothing different.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Stock item | Something the business counts: a name, a unit, a count, a cost, low and target levels, a usual supplier. Kept by Taskiem |
| Product | Something the business sells: a row of its catalogue, with a price, photos and options, as today |
| Counted by | A product's link to the stock item whose count selling it lowers. A product without one is not counted (services, dishes, things on hire), as a product without a `stock` value is today |
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
- **Counted by.** A catalogue row may be counted by one stock item (`stock_items.product_id`, unique per business).
  - **One to one.** A stock item counts at most one row, and a row is counted by at most one stock item (§10 Q8).
  - **Variants.** Each variant row with a count is counted by its own stock item, as each has its own count today. The stock items of one product's variants are grouped by it on the Stock page.
- **The catalogue's `stock` column** becomes a copy of its stock item's count, written by Taskiem as it writes counts today: recorded first, sent after, under the movement's key.
  - The storefront, the public page and Their shop read it unchanged.
  - **An owner's change** to the column by hand is still seen on the refresh and recorded as an `outside` movement, now on the row's stock item. A business can therefore still change counts in its own table.
  - A row with no stock item has no count. Its `stock` column is left empty, as for a product that is not counted today.
- **Size.** Stock items are not in the catalogue, so they do not count against its 500. A business may have up to **5,000** stock items (§10 Q9).

### 4.2 What points where after the split

| Points at a stock item | Points at a product (unchanged) |
| --- | --- |
| Movements, counts, the low list, reorder suggestions, stock value | The storefront, fronts, the public page and Their shop |
| Purchase lines, receiving, cost prices | Sales lines and orders (they lower the product's stock item) |
| Item links and `supplier_items` (the buyer's memory, §6) | Price lists, deals and floor prices |
| The morning briefing's low items | Mentions with `#` in a chat |

- **Selling** a product counted by a stock item lowers that stock item. The rules are those of today: once, under the order's or sale's key, and a sale beyond the count is recorded with a leak, never refused.
- **The seller's side of a link order** finds its stock through its product, as today, then lowers the product's stock item.

### 4.3 Moving what exists

Every business keeping stock today has stock only on catalogue rows, so the move is mechanical:

- **One stock item for each catalogue row with a count**, variant rows included. Each one is counted by its row, and takes the row's name, unit, cost price, levels and supplier.
- **Every reference to a row moves to its stock item:**
  - movements;
  - counts;
  - sales lines' stock;
  - purchase lines;
  - item links.

  The row's id stays on each record (`product_id`), so nothing is lost.
- **Done per business, in one transaction, while its stock sweep is held.** A business that has not moved yet keeps working the old way, so the move can be stopped, checked and resumed.
- **Rolled out in three steps** (S1–S3, §11):
  1. the records and the move, writing both ways;
  2. every reader switched to stock items;
  3. the old paths taken out.

  Each step leaves every business's counts the same before and after, checked per business.

### 4.4 Screens and chat for stock

- **The Stock page** lists stock items, not products:
  - each shows its count, unit, cost, levels and supplier;
  - **Sold as** names its product, or says **Not sold** for a material;
  - a filter offers **All**, **Sold** and **Not sold**.
- **Add something to count** makes a stock item, asking **Do you sell it?**:
  - **Yes** also makes its product in the catalogue, as `add product` does;
  - **No** makes a material only.
- **A product's page** shows **Counted by**: its stock item, with its count, and a way to choose another or none.
- **Chat.** `add 20 yards of ankara fabric` names no product the business has, so it asks one question: **Do you sell it?** Yes adds a product counted by a new stock item; No adds a material. `stock`, `how many … do I have?`, `adjust` and counts work on stock items, materials included.
- **The low list and the briefing** include materials, by their low levels.

## 5. A purchase line

### 5.1 Just record it (the default)

- What arrives moves no stock. Receiving still records what came (All came, Not all came) on the order, so a short delivery is disputed and paid for correctly.
- Paying is an expense naming the supplier, the order and the line's **label**.
- This is right for most of what a business buys and does not sell. It also suits anything the business would rather not count.

### 5.2 Add to stock

- The line names a **stock item**, or one made there and then (asking **Do you sell it?** as in §4.4). When it arrives, the count rises and the cost is set, as today.
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
  - New entries carry the label's id (`label_id`), and its text as their category for anything reading categories today.
  - Reports find each entry's label when they are read: by `label_id`, or for an older entry by its category matched to a label's name, and then follow `merged_into` to the label that is kept.
  - So a rename or a merge changes how reports group spending, at once and for all of it, without touching the log.
- **No label** is allowed, and shows as **Not labelled**, so buying is never blocked by naming.
- **Money reports** group spending by label. Lines added to stock are shown as **Bought to sell or make with**, and tracked lines as **Bought to keep**. Neither is a running cost.
  - In profit for a period, **Bought to keep** is left out: it is something the business has, not money spent running it.
  - **Bought to sell or make with** counts as today's stock bought.
  - Just-recorded lines are running costs under their labels.
- **Expenses already entered by hand** keep their categories, which become labels.

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
- **The owner's own table** can still be edited by hand. A count changed there is recorded against its stock item.

## 8. Data

Migration numbers start at 00501.

- **`stock_items`**, new, in the business's scope with row-level security like the other business tables:
  - `id`, `tenant_id`, `name`, `unit`, `category`;
  - `count`, the sum of its movements;
  - `cost_kobo`, `low_level`, `target_level`, `supplier_id`;
  - `product_id`: the catalogue row it counts, unique per business, empty for a material;
  - `group_of`: the product a variant's stock item belongs to, for grouping on the Stock page;
  - `created_by`, `created_at`, `retired_at`.

  At most 5,000 per business.
- **`stock_movements`** gain `stock_item_id`, filled for every movement by the move (§4.3) and required after it. `item_id`, the catalogue row, stays for history.
- **The catalogue mapping** keeps `stock` as the column Taskiem writes its stock item's count to. `unit`, `cost_price`, `reorder_level`, `target_level` and `supplier` move to the stock item and are written back to the table as a copy while S2 runs, then stop being read from it.
- **The kept copy** in the owner's project gains a `stock_items` table, as `stock_movements` and `money_log` have one today, for reading.
- **Purchase lines** gain:
  - `treatment` (`record` | `stock` | `track`);
  - `stock_item_id`, for stock lines;
  - `label`.

  Old lines become `stock`, pointing at the stock item their row moved to (§4.3), labelled **Stock bought**.
- **Item links** (`item_links`) gain `treatment`, `stock_item_id` and `label`, and `item_id` becomes nullable.
  - `item_id` is in the table's primary key today, `(tenant_id, link_id, item_id)` (migration 00296), so a null cannot be stored as it stands.
  - The primary key moves to `(tenant_id, link_id, seller_item_id)`, already unique on the table.
  - A CHECK holds the stock item and the treatment together: `(treatment = 'stock') = (stock_item_id IS NOT NULL)`.
  - Existing links become `stock`, pointing at the stock item of their row, labelled **Stock bought**.
- **`supplier_items`**, new: the same memory for suppliers not on Taskiem (`tenant_id`, `supplier_id`, `name_key`, `treatment`, `stock_item_id`, `label_id`, `updated_by`, `updated_at`), unique on `(tenant_id, supplier_id, name_key)`, in the business's scope with row-level security.
- **`spend_labels`**, new, the business's own labels: `id`, `tenant_id`, `name` (unique per business, case-insensitive, up to 60 characters), `created_at`, and `merged_into` for a merged label (never a label that is itself merged, so a merge is one step). Money-log expense categories already entered are copied in as labels.
- **Money-log entries** gain a nullable `label_id`, set on new expenses only. Old entries are never changed (§5.4).
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

- **Stock:** the Stock page of stock items, **Add something to count**, and **Counted by** on a product's page (§4.4).
- **The treatment question and label:** in the order sheets of a supplier's price list, their shop, a supplier-drafted order and a purchase order by hand.
- **Order and bill cards:** each line shows its label and a small mark when it went to stock or Things we have. This is on the buyer's side only.
- **Things we have:** a page under Money, with a row's detail page (status, serial, warranty, note) and **Add something we have**.
- **Labels:** in Money settings, to rename and merge, for those who manage money.
- **Suppliers:** the item links list shows each item's treatment, stock item and label, all of which can be changed there; for a supplier not on Taskiem, its remembered items (`supplier_items`) the same way.

## 10. Questions for the founder

| # | Question | Suggested answer |
| --- | --- | --- |
| Q1 | Things we have: only a list, or also value going down over time (depreciation) for accounts? | The list only, for now. Depreciation later, with an accountant's view |
| Q2 | Should making something use up its materials (a dress uses 3 yards), or are materials counted in and out by hand? | By hand for now. Recipes that use up stock items when something is made are a later plan, which this split makes possible |
| Q3 | Should a new business start with a few suggested labels, or none at all? | None built in. Suggestions come from its own past spending and the item's name |
| Q4 | Should Taskiem suggest a treatment and label from the item's name, the supplier's category and the business's past purchases? | Yes, pre-selected and confirmed by the business the first time, never decided alone |
| Q5 | Should a label be required? | No. Unlabelled spending shows as Not labelled, and the business can label it later |
| Q6 | When something on Things we have is sold, should the sale be recorded as money in? | Yes, when marked sold with a price typed then: money in, labelled **Sale of things we had**, not the thing's own label, so selling a machine does not read as negative spending on machines |
| Q7 | Personal buying: should an owner buying on their own account (You) be able to move a purchase into the business? | Not in this plan. A business buys as the business |
| Q8 | Can one stock item be counted by several products? For example, rice sold by the bag and on two fronts, or one fabric sold as a bundle | Not in this plan: one to one, as counts are today. Several products drawing on one stock item, each using so many of it, belongs with the recipes of Q2 |
| Q9 | How many stock items may a business have? | 5,000, apart from the catalogue's 500, and raised by plan later if needed |
| Q10 | Should the owner's own table still let counts be changed by hand, now that the count is Taskiem's? | Yes, as today: a change there is recorded as an `outside` movement on its stock item |

## 11. Milestones

The split comes first, because Add to stock and every count after it point at stock items.

| # | What | Size |
| --- | --- | --- |
| S1 | **Stock items and the move:** `stock_items`; `stock_item_id` on movements, purchase lines and item links; the move per business with counts checked before and after; both ways written; the kept copy; tests on a copy of real shapes (sizes, outside changes, open orders) | L |
| S2 | **Every reader on stock items:** counts, the low list, reorder suggestions, receiving, sales and orders lowering a product's stock item, the briefing, stock value, `stock@1`, WhatsApp and Telegram; the Stock page, **Add something to count** and **Counted by**, on the web and in the app | L |
| S3 | **The old paths out:** stop reading counts and levels from the catalogue; the catalogue's `stock` is only a copy; docs | S |
| U1 | **Purchases on the server:** `treatment` and `label` on purchase lines and item links (the new primary key); `supplier_items`; `spend_labels` and `label_id` on new expenses; receiving by treatment; `kept_things`; the expense split by label, with bills that do not match; OpenAPI and tests | M |
| U2a | **The question and its memory, in the order sheets:** price list, their shop, supplier-drafted, by hand, on the web and in the app, with suggestions; changed from Suppliers; labels renamed and merged | M |
| U2b | **The question in chat:** `order … from …` and the questions' steps (buttons, labels, the stock item), on every channel | S |
| U3 | **Things we have:** the page, its detail, add by hand, statuses (with a sale as money in); spending by label in money reports; web and app | S |

## 12. Accept

**The split**

- A shop keeping stock today is moved:
  - every count is the same before and after;
  - its storefront, orders and sales work as before;
  - every movement, purchase line and item link points at a stock item.
- A product counted by a stock item, sold on the storefront, lowers that stock item once, and the catalogue's `stock` column shows the new count.
- An owner changes a count in their own table: the next refresh records an `outside` movement on the row's stock item.
- A fashion house adds "Ankara fabric" and answers **No** to Do you sell it?:
  - a stock item with no product;
  - nothing new in the catalogue, which still has its 500 places;
  - it is on the low list at its low level.
- A product with sizes moved: one stock item per size, grouped under the product on the Stock page.

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
