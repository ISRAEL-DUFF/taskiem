# Business links: two businesses on Taskiem trading with each other as records, not messages

Status: **Draft** 30 October 2026, for review · Decision: [0061](../decisions/0061-business-links.md) (proposed) · Builds on: [invoices](../invoices.md) ([0045](../decisions/0045-tally-in-taskiem.md)), [matching](../matching.md) and customers' own account numbers ([0046](../decisions/0046-balans-in-taskiem.md)), [stock: suppliers and purchase orders](../stock.md#suppliers-and-purchase-orders) ([0057](../decisions/0057-stock-by-chat.md)), [storefronts](../storefronts.md) (and the fronts plan, in review on PR 135), [reaching people outside](../decisions/0039-reaching-people-outside.md), [dispatch](dispatch.md), [the money log](../money-log.md), [embedding: partners and sub-tenants](../embedding.md) ([0015](../decisions/0015-embedding-tenancy.md), for the pattern of entering another tenant), [governance](../governance.md) (approvals)

## 1. Summary

Dangote Stores sells provisions in bulk. Forty shops buy from it every week, and some of them are on Taskiem. Today Taskiem cannot tell. Ada's Kitchen sends Dangote a purchase order, and it arrives as a WhatsApp message with two buttons, not as an order in Dangote's business. Dangote sends Ada an invoice, and it arrives as a link by SMS, not as a bill she can approve and pay from her money log. Ada pays, and the money lands in Dangote's matching as a transfer from a phone number. Each side retypes what the other already recorded. Tenancy is doing its job, every business kept apart, but it keeps two businesses that trade with each other apart too.

A **link** is a relationship between two businesses on Taskiem, made with both sides' consent, over which documents travel as records:

- Dangote's invoice to Ada becomes a **bill** in Ada's business, owed and payable there, with part payments.
- Ada's purchase order becomes an **order** in Dangote's business, and its acceptance, dispatch and delivery flow back to her purchase order.
- Ada's payment, from Taskiem or from her bank, is matched on both sides at once: her bill paid, his invoice paid, one transfer, nothing in To match on either side.
- A question about a line is a **thread** both can see, in the web app and in chat.
- Dangote can share a **price list**; Ada's reorders from her low list go straight into his orders at his prices.
- Each side keeps its own records under its own tenancy. Nothing is shared in one place; the link carries copies, and each copy says which side's is the truth for what.

And everyone who is not on Taskiem keeps getting what they get today, by WhatsApp, SMS and email, because Dangote cannot know which of his forty customers will ever sign up.

Two ways to link: look a business up and ask, like a connection request, or be offered the link the first time one business sends the other a document. Both sides must say yes.

Eight milestones, L0 to L7. L0 and L1 give Dangote and Ada a bill that is paid once and matched twice.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Link | A relationship between two businesses on Taskiem, requested by one and accepted by the other, under which documents between them are mirrored. Either side can end it |
| Linked business | The other side of a link. Owner-facing word: **linked business**; never "partner", which is a platform partner ([0015](../decisions/0015-embedding-tenancy.md)) |
| Mirror | A record in one business made from a record in the linked business: a bill from an invoice, an order from a purchase order, a status from a status. Each business owns its copy under its own row-level security; a mirror names its source by the link and the source's reference |
| Truth | Which side's copy decides a fact. The issuer's invoice is the truth for what is owed; the payer's business is the truth for what it paid; the seller's order is the truth for accepted, dispatched and delivered; the buyer's purchase order for received |
| Bill | The buyer's copy of a linked supplier's invoice: a record of money owed to a business, with the lines, the due date, what is paid and the way to pay. New; today what a business owes a supplier lives on purchase orders as received-and-not-paid |
| Thread | A conversation between the two businesses attached to one document, visible to both in the web app and in chat |
| Price list | What a seller shares with a linked buyer: items from a front's list with the buyer's prices, minimums and terms |

## 3. Where we are

| Exists | Where |
| --- | --- |
| Every counterparty is outside the business: a customer is a sealed name, phone and email on an invoice; a supplier is a name and a sealed phone (`suppliers`), reached on WhatsApp through a tap-in link; a rider likewise | [invoices](../invoices.md), [stock](../stock.md#suppliers-and-purchase-orders), [0039](../decisions/0039-reaching-people-outside.md) |
| An invoice is sent as a link by SMS, email and, where the customer asked for updates, WhatsApp; its page takes card payments and transfers; part payments where allowed | [invoices](../invoices.md) |
| A purchase order is drafted (by hand, from the low list, or by `draft_purchase`), approved above a limit, sent to the supplier on chat with **Confirm** and **Change**, received as stock movements, and paid as an expense with the supplier as vendor. What is received and not paid is owed to the supplier | [stock](../stock.md#suppliers-and-purchase-orders), migration 00263 (`purchase_orders`, `purchase_payments`) |
| A storefront order is a customer's basket, accepted, paid, dispatched by riders and completed; its workflow is the business's | [storefronts](../storefronts.md), [dispatch](dispatch.md) |
| Matching: money in is matched to invoices and orders; a customer's own registered account number matches exactly, oldest invoice first, extra held as credit; payer memory; split payments | [matching](../matching.md) |
| A business pays out through its connection (`pay_bank`, `pay_many`), after an approval where the policy says so, with "First payment to this account" on an account never paid; each payout is a money-log entry | [money log](../money-log.md), `pay-a-list` |
| Another tenant is entered only through a definer that checks the relationship, audits in both tenants and narrows the scope to the other tenant for the rest of the transaction (`taskiem_partner_enter`); no session or key ever holds another tenant in scope | [0015](../decisions/0015-embedding-tenancy.md), migration 00045 |
| A business has a name, a storefront slug, a linked owner's number, and may have its own WhatsApp number | [storefronts](../storefronts.md), [WhatsApp](../whatsapp.md) |
| Chat reaches outsiders by a proven phone; its outbox carries invoices and purchase orders with signed buttons | [0039](../decisions/0039-reaching-people-outside.md), `api/whatsapp_purchases.go` |

Not built: any notion that a counterparty is itself a business on Taskiem; a bill; a document that exists on both sides; a payment matched on both sides; a thread between businesses; a price list; a purchase order that arrives as an order.

## 4. What stays as it is

- **Every counterparty not on Taskiem.** Invoices, purchase orders, order updates and reminders reach them exactly as today. A link changes what happens only between two linked businesses, and only for the documents addressed between them.
- **Tenancy.** A business's rows stay under its row-level security. No member of one business ever holds the other's scope. The link's writes into the other business go through one definer function, modelled on the partner one, that checks the link, audits in both businesses and narrows the scope for that write alone.
- **The documents themselves.** An invoice is still an invoice, with its statuses, payments, part payments and matching. A purchase order is still a purchase order, with its approval limit and stock movements. An order is still a storefront submission with its workflow. The link adds mirrors and pairs them; it does not add a second kind of invoice or order.
- **Money.** Each business's money log records what moved on its side: a payout for the payer, a credit for the payee. The link pairs the two entries; it never moves money itself. Paying out still takes the payer's approval policy, and "First payment to this account" still applies to a linked business's account the first time.
- **Consent.** No link exists until both sides accept, and no document crosses before that. A lookup never tells one business whether another exists until the other accepts.

## 5. The link

### 5.1 Finding each other

Two ways, both ending in a request that the other side accepts:

- **Look up and ask.** Under **Linked businesses** (a new page under Secrets & settings, and `link` in chat), a business searches by **business name**, by **phone number** (the number on a supplier's record, a customer's invoice, or typed), or by **storefront address** (`adas-kitchen.taskiem.ng`). The request carries the requester's business name and what it says they are to each other (**They supply us**, **They buy from us**, **Both**), and nothing else.
- **At the first document.** When a business sends an invoice, a purchase order or an order update to a phone that is a Taskiem business's (its owner's linked number, its own WhatsApp number, or a member's linked number), the message the outsider gets carries one more line: "Ada's Kitchen is on Taskiem too. Link the two businesses so your invoices and orders meet by themselves: **Link**." Tapping it is the request; the sender's side is asked to accept as the other side would be.

**What a lookup reveals: nothing until acceptance.** A search by name lists only businesses that turned on **Let other businesses find us by name** (off by default; on by default for a business with a published storefront, since its name is already public). A search by phone or storefront address lists nothing: it sends the request to the business behind it if there is one, and the requester is told "If a business on Taskiem uses that number, it has your request" either way, so a phone cannot be used to find out who is on Taskiem. The business asked sees the requester's name and what they claim to be, and may **Accept**, **Decline** (the requester is told only that there was no acceptance) or **Block** (no further requests from that business). Requests expire after 30 days. Accepting is an owner's or admin's act (`link.manage`, held by owners and admins), audited on both sides.

### 5.2 The link record

`business_links` is a platform table, not a tenant's: `id`, `a_tenant`, `b_tenant` (ordered so a pair is one row), `status` (`requested`, `active`, `paused`, `ended`, `blocked`), who requested and who accepted (person ids), `a_role` and `b_role` (`customer`, `supplier`, `both`, as each side sees the other), the settings each side keeps for the link (§5.4), timestamps. Each business reads its own links through a definer (`taskiem_links_of`) that returns the other side's name and the link's state, never the other side's rows. Ending a link stops new documents crossing; what crossed stays on both sides, as a supplier's invoice stays when they stop supplying.

### 5.3 Entering the other side

One definer, `taskiem_link_enter(link, action, detail)`, does what `taskiem_partner_enter` does for sub-tenants: checks the link is active and the caller's tenant is one of its sides, appends an audit entry in both businesses (`link.<action>`, naming the link and the document's reference), and sets the transaction's scope to the other side for the rest of the transaction. Every mirror write is one call followed by one insert or update into the other side's own tables, under their ordinary row-level security, and nothing else in the transaction. The API role may call it; the public host may not.

### 5.4 Per-link settings

Each side keeps its own settings for a link, changed on the Linked businesses page or in chat:

| Setting | Side | Default |
| --- | --- | --- |
| Accept their orders automatically, or ask me each time | seller | ask |
| Price list shared with them (§8) | seller | none |
| Credit terms for them: days to pay, and a limit on what they may owe before new orders wait | seller | the business's invoice terms; no limit |
| Pay their bills automatically up to an amount, after my approval policy; else ask me | buyer | ask |
| Where to take delivery, and who may collect | buyer | the storefront's address |
| Tell me when: an order comes from them, a bill arrives, a payment lands, a thread has a new message | both | all on, in chat and the bell |

## 6. L1: invoices become bills, paid once, matched twice

### 6.1 The bill

A **bill** is the buyer's copy of a linked supplier's invoice: `bills` in the buyer's tenant: `id`, `tenant_id`, `link_id`, `supplier` (the link's other side, named), `reference` (the supplier's invoice number, `INV-0042`), `lines` (the invoice's lines, as sent), `amount_due_kobo`, `paid_kobo`, `due_on`, `status` (`open`, `part_paid`, `paid`, `disputed`, `void`), `mirrored_at`, and the way to pay: the supplier's account number for this buyer (§6.3), and the supplier's payment link. A bill is made when a linked supplier sends an invoice whose customer is the buyer (by the link's identity: the buyer's phone, own number or key `link:<buyer>` on the invoice), through `taskiem_link_enter` and one insert, in the same transaction as the send. The invoice's number, lines and amounts are the truth; a change to the invoice (an amendment, a void) mirrors to the bill.

The buyer sees bills on a **Bills** page (beside Invoices: a row per bill, supplier, amount, due, status, **Pay**) and in chat (`bills`, `bill INV-0042`, `pay INV-0042`, `pay Dangote`). A bill that is received-and-not-paid on a purchase order replaces that figure: a purchase order from a linked supplier is paid through its bill, so what is owed to a linked supplier is the sum of their open bills, and the Suppliers page says so.

### 6.2 Paying a bill from Taskiem

**Pay** on a bill, or `pay INV-0042` in chat, pays through the buyer's payments connection as a payout (`pay_bank` to the supplier's account number for this buyer, or to the account the supplier's invoice names): a whole bill, or an amount for a part payment where the invoice allows parts (a linked invoice allows them by default, as a credit line does). It is a payout like any other: the buyer's approval policy applies (an amount above the policy's line waits for a second person), "First payment to this account" applies the first time, and the payout is a money-log entry with the bill in its description. The bill shows `paying` until the rail confirms, then `paid` or `part_paid`.

A buyer may also pay outside Taskiem and say so (`paid Dangote 425,000 transfer`, as today for a purchase order): the bill is marked by the buyer, and the supplier's side is told "Ada's Kitchen says it paid ₦425,000 by transfer" and matches the money when it arrives, as §6.3 says.

### 6.3 One transfer, matched on both sides

When the link is made, the seller's side issues the buyer a **customer account number** on its collection connection where it can (customers' own account numbers, [matching](../matching.md#customers-own-account-numbers), registered to the customer key `link:<buyer tenant>`), with no one asked, and the bill carries it. Every payment the buyer makes into it, from Taskiem or from their bank app, is matched **exactly** on the seller's side to the buyer's oldest open invoice, as a student's fee is. The match is the event that closes the loop:

- the seller's invoice moves to part-paid or paid, as today;
- the link mirrors the payment to the buyer's bill (`taskiem_link_enter` and one update: `paid_kobo`, a payment row on the bill naming the seller's money-log entry by reference);
- where the buyer paid from Taskiem, the buyer's payout entry and the seller's credit entry are **paired** by the link and the payout's reference, so neither side sees the money in To match, and the buyer's expense for the bill is the payout, not a second entry;
- where the buyer paid from their bank, the seller's credit still matches exactly by the account number, and the buyer's bill is marked paid by the mirror; a buyer who had already marked it by hand sees the mark confirmed, not doubled.

A seller with no connection that issues account numbers falls back to its payment link and payer memory, and the bill says so; the buyer's payout from Taskiem still pairs by reference.

### 6.4 Accept

- Dangote sends Ada an invoice; a bill appears in Ada's business within the same transaction, with his lines and her account number. Ada pays it from Taskiem after her approval; Dangote's invoice is paid, Ada's bill is paid, both money logs have one entry each, paired, and neither To match has anything. Ada pays the next one from her bank app into the same number; the same result, with Ada's bill marked by the mirror.
- A part payment on each path; an amendment and a void mirrored; a bill for a customer not on Taskiem is never made, and the invoice goes as today.
- A member of Ada's business can never read Dangote's rows: the definer narrows the scope for one write and the audit shows both entries.

## 7. L2: purchase orders become orders, and the order's life flows back

- **Sending.** A purchase order to a linked supplier (drafted by hand, from the low list, or by `reorder-from-supplier`) is not sent as a chat message. Through the link it becomes an **order** in the seller's business: a storefront submission of a new kind, `trade`, with the buyer as the customer (named by the link, no sealed phone needed), the purchase order's lines (matched to the seller's items by the price list where one is shared, §8, else by name with the seller asked to confirm the match once), and the seller's prices. It arrives in the seller's Orders page and chat like any order, and starts the seller's order workflow (the plain one, or the delivered-by-riders one), or is accepted automatically where the seller set that for this link.
- **Flowing back.** The seller's accept, decline, "ready", dispatch (the rider's taps) and delivered mirror to the buyer's purchase order as `confirmed`, `cancelled`, a note, and **received** (the buyer's `received` movements per line, at the seller's quantities, with a **Not all came** correction the buyer can make within a day). The seller's counter-offer (a changed quantity or price) is the **Change** of today, carried as a mirrored note, with the buyer's **Accept the change** re-sending the order. Nothing a seller does changes a buyer's stock except delivered, and nothing a buyer does changes a seller's order except placing, accepting a change, and cancelling before acceptance.
- **The invoice.** When the seller accepts, or when the goods are delivered (the seller's choice per link), the seller's business raises the invoice for the order and it becomes the buyer's bill (L1), so an order placed from a low list on Monday is a bill paid on Friday with no one typing a number.
- **Orders the seller places for the buyer.** A sales rep who takes orders by phone drafts an order in the seller's business for a linked buyer; it mirrors to the buyer as a purchase order awaiting the buyer's **Confirm**, and goes no further until then.
- **Accept:** Ada's low list drafts an order to Dangote; it arrives as an order in Dangote's business, his rider delivers it, Ada's stock rises by what came, his invoice is her bill, she pays it, both are paid; a seller's change and a buyer's cancellation before acceptance; an order to an unlinked supplier goes by chat exactly as today.

## 8. L3: price lists and item links

- A seller shares a **price list** with a linked buyer: a front's list block (the catalogue, or a wholesale front's list under the fronts plan, in review on PR 135) with, per item, the buyer's price, a minimum quantity, and availability; one list may be shared with many links, and a link may override prices. The buyer sees it as **Dangote's prices** on the Suppliers page and in chat (`prices from Dangote`), and may order from it directly (`order 20 bags of rice from Dangote`), which drafts a purchase order at those prices.
- **Item links.** The buyer's stock items are linked to the seller's items (`item_links`: the buyer's item, the seller's item, the link), made when the buyer orders an item from the price list the first time, or from the Suppliers page. A linked item's purchase orders carry the seller's item id, so the seller's order needs no name matching, and the buyer's cost price follows the seller's price.
- **Price changes** mirror to the buyer's view at once; an open purchase order keeps the price it was placed at, and the seller may not change it after acceptance except through a change the buyer accepts.
- **Accept:** a price list shared and withdrawn; an order from it at the buyer's prices; an item linked once and reordered without matching; a price change that does not touch an open order.

## 9. L4: threads

- Every mirrored document (a bill, a linked order and its purchase order, a price list) has one **thread** between the two businesses: messages from either side's members, with the document's reference, visible on the document's page in both web apps and in both businesses' chat (`ask Dangote about INV-0042`, and replies in the same conversation, with the business's name in brackets as every shared-number message has). A thread message is a mirror too: written on the sender's side and copied through the definer; the sender is named as the business and the person's first name.
- **Disputes.** **Query this bill** on a bill marks it `disputed` on both sides with the thread opened; a disputed bill is not paid automatically and is not reminded; the seller's **Resolve** (an amendment, a credit note, or "as it stands") clears it.
- **Accept:** a message from Ada's chat appears on Dangote's bill page and in his chat within seconds; a disputed bill is skipped by auto-pay and reminders until resolved.

## 10. L5: statements, credit and reminders between linked businesses

- **The statement.** Each side sees the link's statement: for the seller, what the buyer owes across invoices with the oldest due; for the buyer, what it owes the supplier across bills. It is the customer statement the no-sheet-needed plan (in review on PR 135) adds to invoices, for a customer whose key is the link, so nothing new is kept.
- **Credit terms and limits.** The seller's per-link terms set the due date on each invoice to the buyer; the per-link limit holds new orders when the buyer's open balance would pass it, with the buyer told why and offered **Pay now**.
- **Reminders.** `chase-overdue-invoices`, one message per customer, reaches a linked buyer in its own business: the reminder lands as a notice on the bill and in the buyer owners' chat and bell, not as an SMS to a phone, and offers **Pay**. The buyer's side has the mirror: **bills due** in the morning summary and `bills due` in chat.
- **Accept:** a buyer over its limit sees new orders wait; a reminder arrives as a notice with Pay; a statement on each side adds up to the same number.

## 11. L6: automation

- **Triggers.** `link@1` events for workflows: `bill.received`, `bill.paid`, `order.received` (a linked buyer's order), `order.delivered`, `thread.message`, `link.accepted`. Each carries the link, the document's reference and the amounts, never the other side's contact details.
- **Actions.** `link@1`: `pay_bill` (through the payer's payout path, with the approval the policy requires), `place_order` (a purchase order to a linked supplier from items and quantities), `reply` (a thread message), `get_bill`, `get_order`, `price_of` (an item on a linked price list).
- **Templates.** `pay-bills-on-approval` (every bill from linked suppliers paid after one approval, up to an amount, else held), `reorder-from-linked-suppliers` (the low list ordered at linked prices, sent as orders), `tell-me-when-a-linked-customer-orders`, `linked-order-to-delivery` (accept, dispatch and invoice a linked buyer's order without a tap, within limits the owner sets).
- **The AI builder** knows links: "pay Dangote's bills every Friday after I approve" is `pay-bills-on-approval` with a day; "reorder from my suppliers on Taskiem" is the reorder template.
- **Accept:** the four templates pass the library's checks and their own tests; the triggers reach a workflow within the link's isolation.

## 12. Later, named now

Things a link makes possible that this plan does not build, so they are not designed out:

- **Taskiem-to-Taskiem settlement with no bank in between.** When both businesses hold wallets on Taskiem's own rail ([iswallet account numbers](iswallet-account-numbers.md)), a bill can be paid by an internal transfer, instant and without a bank fee. It is the same pairing with a different rail, and the strongest reason for a business to be on the rail.
- **Claiming the past.** When a link is accepted, the seller's earlier invoices to the buyer's phone can be offered to the buyer as bills, and the buyer's earlier purchase orders to the seller as orders, with each side's consent.
- **Many-to-many trade across links**: a distributor's price list visible to every linked retailer, a retailer's orders to many suppliers in one morning, which the reorder template already does one supplier at a time.
- **Invoice financing, escrow, ratings, a directory of businesses to link with.** Each is a product of its own on top of links, and none belongs here.

## 13. Security

| Concern | Control |
| --- | --- |
| Finding out who is on Taskiem | A name search lists only businesses that opted in; a phone or address lookup answers the same words whether or not a business is behind it, and only an acceptance reveals one |
| A link forced on a business | Nothing crosses before both sides accept; Decline tells the requester nothing; Block stops further requests; `link.manage` to accept, audited both sides; requests limited per business per day and per target |
| One business reading another's rows | No scope is ever held: every cross-business write is one `taskiem_link_enter` call (link active, caller a side, dual audit, scope narrowed for that transaction) and one insert or update; reads of the other side are never made, only mirrors kept on one's own side are read; a test asserts no query reads the other tenant outside the definer |
| A mirror that lies (a bill for an invoice that does not exist, an amount changed in transit) | A mirror is written in the same transaction as the source's change, by the source's code path, never from a message; it names the source by link and reference, and a reconciler compares mirrors with sources hourly and raises an alert on any difference |
| Money moved by the link | The link moves no money. A payout is the buyer's, through its own connection and approval policy, with "First payment to this account"; a credit is the seller's, matched by its own registration. The link only pairs the two entries, and the pairing is checked against the rail's reference |
| A seller changing an accepted order's price | Refused after acceptance; a change is a counter the buyer accepts, and the purchase order keeps the price it was placed at |
| Personal data across the link | A link carries business names, references, lines and amounts; never a member's phone, email or name beyond the first name on a thread message; a linked buyer's customer record on the seller's side is keyed `link:<tenant>`, with no sealed phone |
| A business that ends or blocks a link | Mirrors stay, nothing new crosses, open bills stay payable by the account number, and threads close; a blocked business cannot re-request |
| Plan abuse | Links per business, requests per day and mirrored documents per month are plan limits, counted as other limits are |

## 14. Testing

- **Isolation**: a test harness with two tenants that asserts, for every mirror write, one definer call, two audit entries and no other cross-tenant query (the query log is read in the test, as the replica tests do).
- **The loop**: L1's accepts with a fake rail on each side; the pairing by reference; part payments; a payment from outside Taskiem; an amendment and a void.
- **Orders**: L2's accepts through the storefront's order harness and the stock harness together, riders included.
- **Reconciler**: a mirror changed by hand is reported.
- **Chat**: `link`, `bills`, `pay`, `prices from`, `ask … about`, on WhatsApp and Telegram, with the business name in brackets.
- **Privacy**: a phone lookup answers the same words for a business and for no business; a name search lists only opted-in businesses; a declined request reveals nothing.
- **Web**: the Linked businesses and Bills pages pass axe in both themes and the plain-words test.

## 15. Needs a person

| Item | Who | Blocks |
| --- | --- | --- |
| The owner-facing words: "linked business", "bill", "price list", and whether the request reads as "connect" or "link", in the five languages | Founder, reviewers | L0 |
| Whether a business with a published storefront is findable by name by default | Founder | L0 |
| Which plans include links, and the limits per plan | Founder | L0 |
| Whether an invoice to a linked buyer is raised at acceptance or at delivery by default | Founder | L2 |
| Settlement on Taskiem's own rail for linked businesses (§12), and whether it is free of fees | Founder, iswallet | later |

## 16. Milestones

| Id | Milestone | Size |
| --- | --- | --- |
| L0 | **Links**: `business_links`, `taskiem_links_of`, `taskiem_link_enter` with dual audit, `link.manage`; lookup by name (opt-in), phone and address; the offer at the first document; accept, decline, block, end; the Linked businesses page and `link` in chat; the customer account number for the buyer issued at acceptance; the isolation test harness | M |
| L1 | **Bills**: `bills`, the mirror from a linked invoice (and amendments and voids), the Bills page and chat words, **Pay** through the payout path with approval and first-payment, paying outside Taskiem, exact matching on the seller's side with the mirror back, pairing of payout and credit by reference, the Suppliers page's owed figure from bills, the reconciler | L |
| L2 | **Orders**: a purchase order to a linked supplier as a `trade` order, the status flow back (confirmed, cancelled, received with the correction window), the seller's change and the buyer's acceptance, the invoice raised at acceptance or delivery, orders placed by the seller for the buyer | L |
| L3 | **Price lists and item links**: sharing a list per link, overrides, `prices from`, ordering from the list, `item_links`, cost prices that follow | M |
| L4 | **Threads and disputes**: thread messages mirrored, on both pages and in both chats, `ask … about`, Query this bill and Resolve | M |
| L5 | **Statements, credit and reminders**: the link's statement each side, terms and limits per link, the hold on orders over the limit, reminders as notices with Pay, bills due in the summary | M |
| L6 | **Automation**: `link@1` triggers and actions, the four templates, the builder's rules and evals | M |
| L7 | **Docs and the decision**: invoices, stock, storefronts, matching, the owners' guide ("Trade with businesses on Taskiem"), the atlas's business modules and identity pages, the threat model; decision 0061 accepted with As built | S |

In order, one PR each; L0 and L1 together if the first linked pair is waiting.

## 17. Risks and open questions

| Risk or question | What we do |
| --- | --- |
| Two businesses linked that are the same owner's (a shop and its kitchen as separate tenants) | Allowed; the link is a relationship between tenants, and the same owner on both sides accepts twice; the audit names them |
| The buyer's phone on the seller's invoice is a member's, not the owner's | The offer at the first document goes to the business behind any member's linked number; accepting still needs `link.manage` on that side |
| The reconciler finds a difference | It alerts the operators and both businesses' owners, names the document, and the source wins; the mirror is rewritten from it |
| A seller's order workflow expects a sealed customer phone (notify_customer) | A `trade` order's customer is the link; `notify_customer` on it writes to the buyer's thread and chat instead of SMS, and the order templates need no change |
| Received quantities disagree | The seller's delivered quantity is the default; the buyer's correction within a day opens the thread and marks the lines, and the invoice follows the agreed quantity |
| Name matching of order lines to the seller's items before a price list exists | Asked once per item on the seller's side ("Is Ada's 'rice 50kg' your 'Rice, 50 kg bag'?"), then remembered as an item link |

## 18. Out of scope

A shared document that both businesses edit (each keeps its copy); a marketplace or directory of businesses; settlement on Taskiem's rail (§12, named for later); invoice financing, escrow and ratings; links across deployments of Taskiem; a business linking with a partner's sub-tenant (a sub-tenant is a business and may link like any other, but the partner is not told); consumer customers, who are outsiders as today.
