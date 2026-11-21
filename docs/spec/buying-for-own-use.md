# Buying for the business's own use: stock, supplies and equipment

Status: **Draft** 21 November 2026, for review and discussion · Decision: to come (0070) · Builds on: [stock](stock.md) (suppliers, purchase orders, receiving), [business links](business-links.md) (orders between businesses, item links, bills), [invoices and the money log](invoices-and-money-log.md) (expenses with a vendor and a category), [money reports](../money-reports.md)

## 1. Summary

A business on Taskiem buys many things it does not sell. A fashion house buys fabric and thread it sews with, which is stock. It also buys a sewing machine, which it keeps and uses for years. And it buys packaging, data and diesel, which it uses up. Today every purchase is treated as stock to sell: each line must be "one of your items", what arrives raises that item's count, and paying for it is an expense filed as `stock`.

This plan lets each line of a purchase be one of three things:

1. **Stock:** goods to sell, or materials used to make what is sold. As today.
2. **Supplies:** things used up in running the business. No stock moves; paying for them is an expense under a category.
3. **Equipment:** things the business keeps and uses. No stock moves; each one is recorded on a list of what the business owns, and paying for it is an expense under Equipment.

The business says which, once, the first time it buys a given item from a given supplier. Taskiem remembers the answer, as it already remembers item links. The supplier sees nothing different.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Use | What a purchase line is to the buyer: `stock`, `supplies` or `equipment` |
| Stock item | A row of the catalogue with a count, as in [stock](stock.md). It may be for sale, or a material that is never for sale |
| Supplies | Goods used up in running the business: packaging, cleaning products, stationery, fuel, data |
| Equipment | Goods the business keeps: machines, generators, phones, laptops, furniture, vehicles |
| What we own | The list of the business's equipment, one row per thing received |
| Item link | The buyer's memory of what a seller's item is to it ([business links §9](business-links.md#9-l4-price-lists-item-links-and-mentions)). Today it always points at a stock item; this plan lets it point at a use instead |

## 3. Where we are

- **Purchase orders** (`purchase_orders.lines`) hold `{item_id, item, unit, quantity, cost_kobo, received}`. Receiving (`stock.ReceiveTx`) refuses a line whose `item_id` is not one of the buyer's stock items, raises each item's count, and sets its cost price to the order's.
- **Orders to a linked supplier.** Every way a business buys from another business on Taskiem ends in such an order, and each asks the buyer which of its own items each line is (`say which of your items … is`):
  - from the supplier's price list;
  - from their shop, at storefront prices;
  - by chat, for example `order 20 bags of rice from Dangote`;
  - confirming an order the supplier drafted for us (`links.ConfirmProposedTx`).
- **Paying.** A purchase paid is a money-log expense naming the supplier, with category `stock` (`engine/stock/purchases.go`). A bill from a linked supplier, paid from Taskiem, is a payout that serves as the expense.
- **What goes wrong.** To buy a sewing machine, the fashion house must make "Sewing machine" a stock item:
  - its stock value grows by the machine's price;
  - the machine can appear on the morning low list and in reorder suggestions;
  - it can be put on the storefront;
  - money reports count it as goods bought to sell.

## 4. The three uses

### 4.1 Stock (unchanged)

- What arrives raises the item's count and sets its cost price, as today.
- An item that is a **material**, such as fabric, thread or flour, is still a stock item. It is simply never put up for sale. This needs a flag the catalogue may already have (hidden or not for sale; to be checked, §9 Q2).
- Paying is an expense under `stock`, as today.

### 4.2 Supplies

- What arrives moves no stock. Receiving still records what came (All came, Not all came) on the order, so a short delivery is still disputed and paid for correctly.
- Paying is an expense with the supplier as vendor and the line's **expense category**, such as Packaging, Fuel or Cleaning. An order with lines in several categories splits its expense by line.
- Supplies are not counted. A business that wants to count something it uses up, such as packaging, makes it a stock item instead.

### 4.3 Equipment

- What arrives moves no stock. Each thing received becomes a row in **What we own**:
  - what it is;
  - from whom;
  - when it came;
  - what it cost;
  - the order it came on;
  - an optional note, serial number and warranty end date.
- A quantity of 3 makes 3 rows, so each machine can be told apart later.
- Paying is an expense under **Equipment**, with the supplier as vendor.
- **What we own** is a page under Money (web and app). It lists everything, its total cost, and when each came. A row can be marked **sold**, **broken** or **gone**, which keeps it in the history but out of the total.
- Value going down over time (depreciation) is not in this plan (§9 Q1).

## 5. Saying the use, once

The question appears where the buyer is now asked which of its items a line is. It becomes one question with three answers.

> **What is "Singer heavy-duty sewing machine" to you?**
> - **Stock**: we sell it, or make what we sell with it. *Which item?* (pick one, or make a new one)
> - **Supplies**: we use it up. *Which category?*
> - **Equipment**: we keep and use it.

- **Remembered per supplier item.** The item link gains a `use` and, for supplies, a `category`. The next order of the same item from the same supplier asks nothing.
- **Changed later** from the Suppliers page, where item links are already listed. A change applies to orders received after it. Nothing already received is moved.
- **A suggestion, never a decision.** Taskiem pre-selects an answer when it can guess:
  - the buyer already has a stock item of that name: Stock;
  - the seller's item category, for example Machines or Electronics: Equipment;
  - words such as "nylon bags" or "diesel": Supplies.

  The buyer always confirms the first time.
- **In chat.** `order 1 sewing machine from Singer Ltd` asks the same question as a choice, in the business's language. A voice note or text answer works too: `equipment`, `we use it`, `stock`.
- **Purchases by hand.** A purchase order to a supplier not on Taskiem asks the same question for each new line. An expense recorded by hand, such as `paid 15,000 diesel`, is already supplies and is unchanged.

## 6. What does not change

- **The seller.** It sees one order, as today. The buyer's use is never sent through the door.
- **Receiving.** All came, Not all came and the day's end work for every line, whatever its use. Only what happens after recording differs: stock moves for stock lines only.
- **Bills and paying.** A bill from a linked supplier is paid as today. Its expense entry is split by the lines' uses (§7).
- **Approval limits** apply to the whole order, as today.

## 7. Data

Migration numbers start at 00491.

- **The purchase line** gains `use` (`stock` | `supplies` | `equipment`, default `stock`) and `category` (for supplies). Old lines are all `stock`, which is what they were.
- **Item links** (`item_links`) gain `use` and `category`. `item_id` becomes nullable: it is required when `use = 'stock'` and empty otherwise.
- **Receiving** (`stock.ReceiveTx`) moves stock and sets cost only for stock lines, and writes an `owned_items` row per unit for equipment lines. It stays in the same transaction and keeps the same receipt key, so a repeated receipt makes nothing twice.
- **`owned_items`**, new, in the business's scope with row-level security like the other business tables:
  - `id`, `tenant_id`, `name`, `supplier_id`, `purchase_order_id`;
  - `cost_kobo`, `received_at`;
  - `serial`, `warranty_until`, `note`;
  - `status` (`in_use`, `sold`, `broken`, `gone`), `status_at`, `created_by`.
- **The expense.** Paying a purchase writes one money-log expense per use and category on the order, each naming the supplier and the order. For example: ₦180,000 `equipment`, ₦12,000 `packaging`, ₦40,000 `stock`. A part payment is split in proportion to the lines.
- **Money reports** group expenses by category already. `equipment` is shown apart, as **Bought to keep**, because it is not a running cost.

## 8. Screens

The web and the app change together. App screens are built in the app's own look ([mobile-app-look.md](mobile-app-look.md)).

- **The use question:** in the order sheets of a supplier's price list, their shop, a supplier-drafted order, and a purchase order by hand.
- **Order and bill cards:** each line shows a small mark (Stock, Supplies or Equipment) on the buyer's side only.
- **What we own:** a new page under Money, with a row's detail page (status, serial, warranty, note) and **Add something we own** for things bought before Taskiem or bought outside it.
- **Suppliers:** the item links list shows each item's use, and it can be changed there.
- **Low list and stock value:** unchanged, because only stock lines reach them.

## 9. Questions for the founder

| # | Question | Suggested answer |
| --- | --- | --- |
| Q1 | Equipment: only a list of what the business owns, or also value going down over time (depreciation) for accounts? | The list only, for now. Depreciation later, with the accountant's view |
| Q2 | Materials such as fabric: is it enough that they are stock items never put up for sale? Or should making something use them up (a dress uses 3 yards)? | Stock items never for sale, counted in and out by hand. Using up materials by recipe is a later plan |
| Q3 | Expense categories for supplies: a fixed starting set, or the business's own? | A starting set (Packaging, Fuel and power, Cleaning, Office, Data and airtime, Repairs, Other) that the business can add to |
| Q4 | Should Taskiem suggest the use from the seller's item category and the item's name? | Yes, as a pre-selected answer the buyer confirms, never decided alone |
| Q5 | Can one item link be both stock and equipment? For example, a phone shop that sells phones and also keeps one for the till | No: one use per supplier item. The rare case is changed on that order's line, which does not change the link |
| Q6 | Personal buying: should an owner buying on their own account (You) be able to move a purchase into the business? | Not in this plan. A business buys as the business |

## 10. Milestones

| # | What | Size |
| --- | --- | --- |
| U1 | **Server:** `use` and `category` on purchase lines and item links; receiving by use; `owned_items`; the split expense; OpenAPI and tests | M |
| U2 | **The question and its memory:** in every order path (price list, their shop, chat, supplier-drafted, by hand), on the web and in the app, with the suggestion; changed from Suppliers | M |
| U3 | **What we own:** the page, its detail, add by hand, statuses; Bought to keep in money reports; web and app | S |

## 11. Accept

- A fashion business orders a sewing machine from a linked supplier and says Equipment:
  - when it arrives, no stock moves and a row appears in What we own;
  - paying records an expense under Equipment naming the supplier;
  - the next order of the same machine asks nothing.
- The same business orders fabric and says Stock (a material): the count rises and its cost is set, as today, and it is not on the storefront.
- An order mixes fabric (stock), nylon bags (supplies, Packaging) and a machine (equipment):
  - one bill;
  - three expense entries that add up to it;
  - stock moved only for the fabric.
- Not all came on the machine line records the short count and disputes it, as for any line, and makes no owned row for what did not come.
- The supplier's side of each of these orders is the same as today.
- An item link changed from Equipment to Stock applies only to orders received after the change.
