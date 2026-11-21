# What a business buys, and what it does with it

Status: **Draft** 21 November 2026, for review and discussion · Decision: to come (0070) · Builds on: [stock](stock.md) (suppliers, purchase orders, receiving), [business links](business-links.md) (orders between businesses, item links, bills), [invoices and the money log](invoices-and-money-log.md) (expenses with a vendor and a category), [money reports](../money-reports.md)

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
| Label | The business's own word for what a purchase was for: "Machines", "Staff welfare", "Fuel", "Client job: Mrs Ade". A business has its own labels. Taskiem has none built in |
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
- **Materials** such as fabric, thread or flour are products like any other. They are hidden, never put up for sale, and counted in and out.
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
- **Things we have** is a page under Money, on the web and in the app. It lists everything with its cost and when it came, and can be grouped by label.
- A row can be marked **sold**, **broken**, **given away** or **gone**. It stays in the history and leaves the totals.
- **Add something we have** puts on the list anything bought before Taskiem or outside it.

### 4.4 Labels

- **The business's own.** A label is made by typing it. Taskiem offers the business's past labels first, then a suggestion from the item's name and the supplier's category.
- **Renamed or merged** from Money settings. A merge moves past spending to the label that is kept.
- **No label** is allowed, and shows as **Not labelled**, so buying is never blocked by naming.
- **Money reports** group spending by label. Lines added to stock are shown as **Bought to sell or make with**, and tracked lines as **Bought to keep**. Neither is a running cost.
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
- **Changed later** from the Suppliers page, where item links are already listed. A change applies to orders received after it, and nothing already received moves.
- **One order line can differ.** On an order, the business can treat a line differently from its link. For example, one phone it usually sells is kept for the till. That changes only the line.
- **A suggestion, never a decision.** Taskiem pre-selects a treatment and a label when it can guess:
  - the business already has a product of that name: Add to stock;
  - the business has tracked this kind of thing before: Keep track of it;
  - otherwise: Just record it.

  The business always confirms the first time.
- **In chat.** `order 1 sewing machine from Singer Ltd` asks the same thing as a choice, in the business's language. A typed or spoken answer also works: `keep track of it, machines`, `add to stock`, `just record it`.
- **Purchases by hand.** A purchase order to a supplier not on Taskiem asks the same for each new line. An expense recorded by hand, such as `paid 15,000 diesel`, is a line that is just recorded, labelled Diesel.

## 6. What does not change

- **The supplier.** It sees one order, as today. The buyer's treatment and labels are never sent through the door.
- **Receiving.** All came, Not all came and the day's end work for every line. Only what happens after recording differs.
- **Bills and paying.** A bill from a linked supplier is paid as today. Its expense entry is split by label (§7).
- **Approval limits** apply to the whole order, as today.

## 7. Data

Migration numbers start at 00491.

- **Purchase lines** gain:
  - `treatment` (`record` | `stock` | `track`);
  - `label`.

  Old lines become `stock` with the label **Stock bought**, which is what they were.
- **Item links** (`item_links`) gain `treatment` and `label`. `item_id` becomes nullable: it is required when `treatment = 'stock'` and empty otherwise.
- **`spend_labels`**, new, the business's own labels: `id`, `tenant_id`, `name` (unique per business, case-insensitive), `created_at`, and `merged_into` for a merged label. Money-log expense categories already entered are copied in as labels.
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
- **The expense.** Paying a purchase writes one money-log expense per label on the order, each naming the supplier and the order. For example: ₦180,000 Machines, ₦12,000 Packaging, ₦40,000 Stock bought. A part payment is split in proportion to the lines.

## 8. Screens

The web and the app change together. App screens are built in the app's own look ([mobile-app-look.md](mobile-app-look.md)).

- **The treatment question and label:** in the order sheets of a supplier's price list, their shop, a supplier-drafted order and a purchase order by hand.
- **Order and bill cards:** each line shows its label and a small mark when it went to stock or Things we have. This is on the buyer's side only.
- **Things we have:** a page under Money, with a row's detail page (status, serial, warranty, note) and **Add something we have**.
- **Labels:** in Money settings, to rename and merge.
- **Suppliers:** the item links list shows each item's treatment and label, and both can be changed there.
- **Low list and stock value:** unchanged, because only stock lines reach them.

## 9. Questions for the founder

| # | Question | Suggested answer |
| --- | --- | --- |
| Q1 | Things we have: only a list, or also value going down over time (depreciation) for accounts? | The list only, for now. Depreciation later, with an accountant's view |
| Q2 | Materials such as fabric: is it enough that they are products never for sale, counted by hand? Or should making something use them up (a dress uses 3 yards)? | Counted by hand for now. Using up materials when something is made is its own later plan |
| Q3 | Should a new business start with a few suggested labels, or none at all? | None built in. Suggestions come from its own past spending and the item's name |
| Q4 | Should Taskiem suggest a treatment and label from the item's name, the supplier's category and the business's past purchases? | Yes, pre-selected and confirmed by the business the first time, never decided alone |
| Q5 | Should a label be required? | No. Unlabelled spending shows as Not labelled, and the business can label it later |
| Q6 | When something on Things we have is sold, should the sale be recorded as money in? | Yes, when marked sold with a price: money in, labelled like the thing |
| Q7 | Personal buying: should an owner buying on their own account (You) be able to move a purchase into the business? | Not in this plan. A business buys as the business |

## 10. Milestones

| # | What | Size |
| --- | --- | --- |
| U1 | **Server:** `treatment` and `label` on purchase lines and item links; `spend_labels`; receiving by treatment; `kept_things`; the expense split by label; OpenAPI and tests | M |
| U2 | **The question and its memory:** in every order path (price list, their shop, chat, supplier-drafted, by hand), on the web and in the app, with suggestions; changed from Suppliers; labels renamed and merged | M |
| U3 | **Things we have:** the page, its detail, add by hand, statuses (with a sale as money in); spending by label in money reports; web and app | S |

## 11. Accept

- A fashion business orders a sewing machine from a linked supplier and chooses **Keep track of it**, labelled Machines:
  - when it arrives, no stock moves and a row appears on Things we have;
  - paying records an expense labelled Machines, naming the supplier;
  - the next order of the same machine asks nothing.
- It orders snacks for the shop and chooses **Just record it**, labelled Staff welfare: no stock moves, nothing is tracked, and paying records the expense under Staff welfare.
- It orders fabric and chooses **Add to stock**, a hidden product: the count rises and its cost is set, as today, and it is not on the storefront.
- An order mixes fabric (to stock), nylon bags (recorded, Packaging) and a machine (tracked, Machines):
  - one bill;
  - three expense entries that add up to it;
  - stock moved only for the fabric;
  - one row on Things we have.
- Not all came on the machine line records the short count and disputes it, as for any line, and adds no row for what did not come.
- The supplier's side of each of these orders is the same as today.
- A treatment changed on the Suppliers page applies only to orders received after the change. One line treated differently on an order does not change the link.
