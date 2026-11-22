# What a business buys, and what it does with it

Status: **Draft** 21 November 2026, for review and discussion; revised 22 November 2026 after review · Decision: to come (0070) · Builds on: [stock](stock.md) (suppliers, purchase orders, receiving), [business links](business-links.md) (orders between businesses, item links, bills), [invoices and the money log](invoices-and-money-log.md) (expenses with a vendor and a category), [money reports](../money-reports.md)

## 1. Summary

A business buys many things it does not sell. A fashion house buys fabric it sews with and a sewing machine it keeps. It buys snacks for the shop, gifts for staff, a client's own fabric for a job, and diesel for the generator. Today every purchase is treated as stock: each line must be "one of your items", what arrives raises that item's count, and paying for it is an expense filed as `stock`.

No fixed list of kinds of purchase fits every business, so this plan does not make one. Instead:

- **A purchase is just something bought.** It records what it was, how many, from whom, what it cost, and a **label** in the business's own words. That is the default. It is spending in the money log, and nothing else happens.
- **Two things the business may choose to do with a line**, and neither is required:
  1. **Add to stock:** tie the line to one of its products, so the count goes up and the cost price is set, as today. For goods it sells, and for materials it wants to count.
  2. **Keep track of it:** put each one on **Things we have**, a list of what the business has and wants to follow. For a machine, a generator or a laptop.
- **Said once, remembered.** The first time the business buys an item from a supplier, it says how to treat it. Taskiem remembers this, as it already remembers item links.

The supplier sees nothing different.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Purchase line | One line of a purchase: what, how many, the unit, the cost, the supplier, a label, and how it is treated |
| Label | The business's own word for what a purchase was for: "Machines", "Staff welfare", "Fuel", "Client job: Mrs Ade". A business has its own labels, up to 60 characters each (the money log's limit for a category). Taskiem has none built in |
| Treatment | What happens when a line arrives: **just record it** (the default), **add to stock** (one of the products), or **keep track of it** (Things we have) |
| Things we have | The business's list of things it keeps track of, one row per thing received or added |
| Item link | The buyer's memory of a supplier's item ([business links §9](business-links.md#9-l4-price-lists-item-links-and-mentions)). Today it always points at a product; this plan lets it hold a treatment and a label instead |

## 3. Where we are

- **Purchase orders** (`purchase_orders.lines`) hold `{item_id, item, unit, quantity, cost_kobo, received}`. Receiving (`stock.ReceiveTx`) refuses a line whose `item_id` is not one of the buyer's products, raises each one's count, and sets its cost price to the order's.
- **Orders to a linked supplier.** Every way a business buys from another business on Taskiem ends in such an order, and each asks which of the buyer's own items each line is (`say which of your items … is`):
  - from the supplier's price list;
  - from their shop, at storefront prices;
  - by chat, for example `order 20 bags of rice from Dangote`;
  - confirming an order the supplier drafted for us (`links.ConfirmProposedTx`).
- **Paying.** A purchase paid is a money-log expense naming the supplier, with category `stock` (`engine/stock/purchases.go`). Expenses entered by hand already carry a free-text category (`moneylog.Entry.Category`), so a label is not a new idea there.
- **What goes wrong.** To buy a sewing machine, the fashion house must make "Sewing machine" a product:
  - its stock value grows by the machine's price;
  - the machine can appear on the morning low list and in reorder suggestions;
  - it can be put on the storefront;
  - money reports count it as goods bought to sell.

## 4. A purchase line

### 4.1 Just record it (the default)

- What arrives moves no stock. Receiving still records what came (All came, Not all came) on the order, so a short delivery is disputed and paid for correctly.
- Paying is an expense naming the supplier, the order and the line's **label**.
- This is right for most of what a business buys that it does not sell. It also suits anything the business would rather not count.

### 4.2 Add to stock

- The line names one of the business's products, or one made there and then. When it arrives, the count rises and the cost price is set, as today.
- **Materials** such as fabric, thread or flour are products like any other, marked **never for sale**, and counted in and out.
  - Products have no such mark today: `stock.Item` has `Available`, which says whether one can be ordered now, not whether it is ever sold. U1 adds `for_sale` (true by default) to the product.
  - A product not for sale is never on the storefront, a price list or Their shop, and is not in reorder suggestions.
  - It is on the low list only when the business sets a low level for it (fabric running out matters; a machine part may not).
- The label defaults to **Stock bought**, and the business can change it.

### 4.3 Keep track of it

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

### 4.4 Labels

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

## 5. Saying it once

The question appears where the buyer is now asked which of its items a line is.

> **"Singer heavy-duty sewing machine" from Singer Ltd**
> How should we treat it?
> - **Just record it**
> - **Add to stock**: *which product?* (pick one, or make a new one)
> - **Keep track of it**: put each one on Things we have
>
> Label: *Machines* (type or pick)

- **Remembered per supplier item.** The item link holds the treatment, the product (for stock) and the label. The next order of the same item from the same supplier asks nothing.
- **A supplier not on Taskiem** has no item links: they belong to a link between two businesses. A new small memory, `supplier_items` (supplier, the item's name as typed, treatment, product, label), keeps the same for purchases by hand. The name is matched without case or extra spaces, so "Diesel" and "diesel " are one item.
- **Changed later** from the Suppliers page, where item links are already listed. A change applies to orders received after it, and nothing already received moves.
- **One order line can differ.** On an order, the business can treat a line differently from its link. For example, one phone it usually sells is kept for the till. That changes only the line.
- **A suggestion, never a decision.** Taskiem pre-selects a treatment and a label when it can guess:
  - the business already has a product of that name: Add to stock;
  - the business has tracked this kind of thing before: Keep track of it;
  - otherwise: Just record it.

  The business always confirms the first time.
- **In chat.** `order 1 sewing machine from Singer Ltd` asks the same thing as a step of the chat's questions ([chat menus §7](chat-menus.md)), in the business's language:
  - the treatment as three buttons (WhatsApp's buttons, a list where there are more);
  - then the label, as the business's three most-used labels and **Another**, which asks for it to be typed;
  - for **Add to stock**, the product, as the product choices offered today.

  A typed or spoken answer also works: `keep track of it, machines`, `add to stock`, `just record it`.
- **Purchases by hand.** A purchase order to a supplier not on Taskiem asks the same for each line its memory does not know (`supplier_items`, above). An expense recorded by hand, such as `paid 15,000 diesel`, is a line that is just recorded, labelled Diesel.
- **After it arrived.** A line's treatment is locked once anything on it is received: undoing it would move stock back or remove rows someone may have changed. To correct one, the business changes the stock by hand (a count) or the row on Things we have (marked gone). Until something is received, the line can be changed on the order.

## 6. What does not change

- **The supplier.** It sees one order, as today. The buyer's treatment and labels are never sent through the door.
- **Receiving.** All came, Not all came and the day's end work for every line. Only what happens after recording differs.
- **Bills and paying.** A bill from a linked supplier is paid as today. Its expense entry is split by label (§7), by the rules in §7 for a bill that does not match its order.
- **Approval limits** apply to the whole order, as today.

## 7. Data

Migration numbers start at 00501.

- **Purchase lines** gain:
  - `treatment` (`record` | `stock` | `track`);
  - `label`.

  Old lines become `stock` with the label **Stock bought**, which is what they were.
- **Item links** (`item_links`) gain `treatment` and `label`, and `item_id` becomes nullable: required when `treatment = 'stock'` and empty otherwise.
  - `item_id` is in the table's primary key today, `(tenant_id, link_id, item_id)` (migration 00296), so a null cannot be stored as it stands.
  - The primary key moves to `(tenant_id, link_id, seller_item_id)`, already unique on the table.
  - A CHECK holds `item_id` and `treatment` together: `(treatment = 'stock') = (item_id IS NOT NULL)`.
  - Existing links become `stock`, labelled **Stock bought**.
- **`supplier_items`**, new: the same memory for suppliers not on Taskiem (`tenant_id`, `supplier_id`, `name_key`, `treatment`, `item_id`, `label_id`, `updated_by`, `updated_at`), unique on `(tenant_id, supplier_id, name_key)`, in the business's scope with row-level security.
- **`spend_labels`**, new, the business's own labels: `id`, `tenant_id`, `name` (unique per business, case-insensitive, up to 60 characters), `created_at`, and `merged_into` for a merged label (never a label that is itself merged, so a merge is one step). Money-log expense categories already entered are copied in as labels.
- **Money-log entries** gain a nullable `label_id`, set on new expenses only. Old entries are never changed (§4.4).
- **Receiving** (`stock.ReceiveTx`):
  - for stock lines, it moves stock and sets the cost;
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

## 8. Screens

The web and the app change together. App screens are built in the app's own look ([mobile-app-look.md](mobile-app-look.md)).

- **The treatment question and label:** in the order sheets of a supplier's price list, their shop, a supplier-drafted order and a purchase order by hand.
- **Order and bill cards:** each line shows its label and a small mark when it went to stock or Things we have. This is on the buyer's side only.
- **Things we have:** a page under Money, with a row's detail page (status, serial, warranty, note) and **Add something we have**.
- **Labels:** in Money settings, to rename and merge, for those who manage money.
- **Suppliers:** the item links list shows each item's treatment and label, and both can be changed there; for a supplier not on Taskiem, its remembered items (`supplier_items`) the same way.
- **Low list and stock value:** unchanged, because only stock lines reach them.

## 9. Questions for the founder

| # | Question | Suggested answer |
| --- | --- | --- |
| Q1 | Things we have: only a list, or also value going down over time (depreciation) for accounts? | The list only, for now. Depreciation later, with an accountant's view |
| Q2 | Materials such as fabric: is it enough that they are products never for sale, counted by hand? Or should making something use them up (a dress uses 3 yards)? | Counted by hand for now. Using up materials when something is made is its own later plan |
| Q3 | Should a new business start with a few suggested labels, or none at all? | None built in. Suggestions come from its own past spending and the item's name |
| Q4 | Should Taskiem suggest a treatment and label from the item's name, the supplier's category and the business's past purchases? | Yes, pre-selected and confirmed by the business the first time, never decided alone |
| Q5 | Should a label be required? | No. Unlabelled spending shows as Not labelled, and the business can label it later |
| Q6 | When something on Things we have is sold, should the sale be recorded as money in? | Yes, when marked sold with a price typed then: money in, labelled **Sale of things we had**, not the thing's own label, so selling a machine does not read as negative spending on machines |
| Q7 | Personal buying: should an owner buying on their own account (You) be able to move a purchase into the business? | Not in this plan. A business buys as the business |

## 10. Milestones

| # | What | Size |
| --- | --- | --- |
| U1 | **Server:** `treatment` and `label` on purchase lines and item links (the new primary key); `supplier_items`; `for_sale` on products; `spend_labels` and `label_id` on new expenses; receiving by treatment; `kept_things`; the expense split by label, with bills that do not match; OpenAPI and tests | M |
| U2a | **The question and its memory, in the order sheets:** price list, their shop, supplier-drafted, by hand, on the web and in the app, with suggestions; changed from Suppliers; labels renamed and merged | M |
| U2b | **The question in chat:** `order … from …` and the questions' steps (buttons, labels, the product), on every channel | S |
| U3 | **Things we have:** the page, its detail, add by hand, statuses (with a sale as money in); spending by label in money reports; web and app | S |

## 11. Accept

- A fashion business orders a sewing machine from a linked supplier and chooses **Keep track of it**, labelled Machines:
  - when it arrives, no stock moves and a row appears on Things we have;
  - paying records an expense labelled Machines, naming the supplier;
  - the next order of the same machine asks nothing.
- It orders snacks for the shop and chooses **Just record it**, labelled Staff welfare: no stock moves, nothing is tracked, and paying records the expense under Staff welfare.
- It orders fabric and chooses **Add to stock**, a product never for sale: the count rises and its cost is set, as today, and it is not on the storefront, a price list or in reorder suggestions.
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
