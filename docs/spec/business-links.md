# Business links: two businesses on Taskiem trading with each other as records, not messages

Status: **In progress**: L0, L1 and L2's first slice built (§16.1 to §16.3) · Decision: [0061](../decisions/0061-business-links.md) (proposed) · Builds on: [invoices](../invoices.md) ([0045](../decisions/0045-tally-in-taskiem.md)), [matching](../matching.md) and customers' own account numbers ([0046](../decisions/0046-balans-in-taskiem.md)), [stock: suppliers and purchase orders](../stock.md#suppliers-and-purchase-orders) ([0057](../decisions/0057-stock-by-chat.md)), [storefronts](../storefronts.md) (and the fronts plan, in review on PR 135), [reaching people outside](../decisions/0039-reaching-people-outside.md), [dispatch](dispatch.md), [the money log](../money-log.md), [embedding: partners and sub-tenants](../embedding.md) ([0015](../decisions/0015-embedding-tenancy.md), for the pattern of entering another tenant), [governance](../governance.md) (approvals)

## 1. Summary

Dangote Stores sells provisions in bulk. Forty shops buy from it every week, and some of them are on Taskiem. Today Taskiem cannot tell. Ada's Kitchen sends Dangote a purchase order, and it arrives as a WhatsApp message with two buttons, not as an order in Dangote's business. Dangote sends Ada an invoice, and it arrives as a link by SMS, not as a bill she can approve and pay from her money log. Ada pays, and the money lands in Dangote's matching as a transfer from a phone number. Each side retypes what the other already recorded. Tenancy is doing its job, every business kept apart, but it keeps two businesses that trade with each other apart too.

A **link** is a relationship between two businesses on Taskiem, made with both sides' consent, over which documents travel as records:

- Dangote's invoice to Ada becomes a **bill** in Ada's business, owed and payable there, with part payments.
- Ada's purchase order becomes an **order** in Dangote's business, and its acceptance, dispatch and delivery flow back to her purchase order.
- Ada's payment, from Taskiem or from her bank, is matched on both sides at once: her bill paid, his invoice paid, one transfer, nothing in To match on either side.
- The two businesses have a **conversation**: a chat in the web app that looks and works like WhatsApp, one per linked business with its whole history, where every order, bill, delivery and payment is a card in the stream, and the same messages reach the owners' phones.
- Dangote can share a **price list**; Ada's reorders from her low list go straight into his orders at his prices.
- Each side keeps its own records under its own tenancy. Nothing is shared in one place; the link carries copies, and each copy says which side's is the truth for what.

And everyone who is not on Taskiem keeps getting what they get today, by WhatsApp, SMS and email, because Dangote cannot know which of his forty customers will ever sign up.

Three ways to link: find a business and ask, like a connection request, through the **search box on the Chat page**, which searches businesses on Taskiem as WhatsApp's searches contacts, or `link` in chat; be offered the link the first time one business sends the other a document; or scan the other business's code across the counter. Both sides must say yes.

In the web app's conversations, `@` suggests linked businesses as you type and `#` suggests items from their price lists, so one message, "Buy 400 bags of cement from @Dangote and 20 pieces of blocks from @Cherry", reads back as two orders and one **Yes** sends both.

Eight milestones, L0 to L7. L0 and L1 give Dangote and Ada a bill that is paid once and matched twice.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Link | A relationship between two businesses on Taskiem, requested by one and accepted by the other, under which documents between them are mirrored. Either side can end it |
| Linked business | The other side of a link. Owner-facing word: **linked business**; never "partner", which is a platform partner ([0015](../decisions/0015-embedding-tenancy.md)) |
| Mirror | A record in one business made from a record in the linked business: a bill from an invoice, an order from a purchase order, a status from a status. Each business owns its copy under its own row-level security; a mirror names its source by the link and the source's reference |
| Truth | Which side's copy decides a fact. Every mirrored field has one source: the issuer's invoice for what is owed; the seller's match for what is paid (`paid_kobo`); the seller's order for accepted, dispatched and `delivered`. What a side says for itself (`declared_paid_kobo`, `received`) is its own field beside the mirror, never mirrored and never compared |
| Bill | The buyer's copy of a linked supplier's invoice: a record of money owed to a business, with the lines, the due date, what is paid and the way to pay. New; today what a business owes a supplier lives on purchase orders as received-and-not-paid |
| Thread | The quoted replies to one document's card inside the conversation (§5.5); shown on the document's page in both web apps and in both businesses' chat, not a store of its own |
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

Three ways, each ending in a request that the other side accepts:

**On the web: the Chat page's search, as WhatsApp's.** The search box above the conversation list on the Chat page (§5.5) is where a business is found, because that is where a person looks for someone to talk to, and it works as WhatsApp's search does: one box, results in groups as you type. **Chats**: your linked businesses whose name matches, each opening its conversation. **Messages**: lines in your conversations that match, each opening the conversation at that message. **Businesses on Taskiem**: businesses you are not linked with that match, as WhatsApp lists contacts you have never chatted with (before L3, a Chats result opens the business's card instead of a conversation); tapping one opens its card with **Request link** and the role chooser (**They supply us**, **They buy from us**, **Both**), and once accepted it is a chat in the list. A business's card is its storefront's card where it has one (name, area, what it sells), else its name. **Request link** shows only to members with `link.manage` (owners and admins); an operator sees the card with "An owner or admin can link with this business" and no button. A business with a request pending shows under **Businesses on Taskiem** marked **Requested** and is not offered again; its card has **Cancel request**. A business that has asked us sits at the top of the list with **Accept**, **Decline** and **Block**. A phone number or address typed into the box is never a search: as you type, the only row it shows is **Send a link request to 0803…**, which acts on a tap alone, looks the same whether or not a business is behind the number, and is never sent by typing; the Chats group never matches a linked business by its phone, so the box cannot be used to confirm a number. There is no search box in the app's header and no search over the business's own invoices, orders or items in this plan; that would be a feature of its own.

**By what is sold, not only by name.** A business that needs cement does not know which supplier is on Taskiem; it knows what it wants and where it is. So the search takes words for things and places as well as names: `cement`, `rice 50kg`, `canopies for hire`, `catering`, `Ikeja`, and answers with businesses that sell or do that, near enough, that chose to be found. The index holds only what a business already shows the public or chose to share: its storefront card (name, what it sells, area), its published catalogue and fronts (items, categories, services, hire items, with the prices its page shows), the kind it gave at sign-up (food, provisions, fashion, phones, school, salon, services), and its area. Nothing from private tables, stock counts, private price lists, customers or members. A business with no storefront is found by name, kind and area only. A result shows the business's card, the items that matched with their public prices, the area, whether it is already linked, and **Request link**; linked businesses rank first, then businesses in the same area, then by how well the words match. This is a search, not a directory: there is no page to browse every business, no categories to wander, no ratings and no promoted listings (§18). The same **Request link** sits where the counterparty already is: on a supplier's row, on a customer's invoice under The customer's money, and on a repeat customer's order. **Linked businesses** (under Secrets & settings) lists the same links and pending requests with their settings, for the owner who manages them away from the chat.

**In chat.** `link Dangote` answers a numbered list of matches; `find cement in Ikeja` and `who sells canopies` answer the same list by what is sold; the number picks one, three buttons pick the role, and the request goes. `link 0803…` and `link adas-kitchen` send blind (below). `links` lists linked businesses and pending requests. A request arrives as a message with **Accept**, **Decline** and **Block**, signed as approvals are, so only that business's `link.manage` holders can answer. The same words and buttons on WhatsApp, Telegram and a Slack DM.

**At the first document.** When a business sends an invoice, a purchase order or an order update to a phone that is a Taskiem business's **own** (the number its owner linked, or the business's own WhatsApp number; never a member's number, since a member is a person who may be a consumer customer here and staff there), the message the outsider gets carries one more line: "Ada's Kitchen is on Taskiem too. Link the two businesses so your invoices and orders meet by themselves: **Link**." On WhatsApp it is a button; in an SMS it is a link to the business's standing page, which already serves tap-in links. Tapping it is the request.

**Across the counter.** Lagos trade is done face to face. The storefront's QR card gains a second code, **Link with us on Taskiem**, and invoices, receipts and purchase orders print a short link code (`taskiem.ng/l/<code>`, like a rider's tap-in link). Scanning or opening it from a signed-in business opens the request pre-filled; from a phone that is not signed in it offers to sign up first.

**What a lookup reveals: nothing until acceptance.** A search by name or by what is sold lists only businesses whose **Let other businesses find us** is on. The default is settled here, not left open: it is **on** for a business with a published storefront, because everything the search would show is already on its public page and the storefront's publish is the choice to be public, and **off** for every other business, which is asked once, in the sign-up's first chat or on the Linked businesses page, with what it wants to be found for. Turning it off takes a business out of both searches at once. A search by phone or storefront address lists nothing: it sends the request to the business behind it if there is one, and the requester is told "If a business on Taskiem uses that, it has your request" either way, so a phone cannot be used to find out who is on Taskiem. The business asked sees the requester's name and what they claim to be, and may **Accept**, **Decline** or **Block**. What the requester hears depends only on how they asked, never on the answer: a request made to a business listed by the search (by name or by what is sold) ends with "Not accepted" on decline or after 30 days, since the business's existence was already shown; a request made blind, by phone or address, gets no answer but acceptance, ever, so decline, expiry, a blocked business and no business at all look the same. **Block** takes that business's later requests and drops them silently, with the same words as the first: a blind one hears nothing, and one to a listed business hears "Not accepted" after the same 30 days, so a block is never told apart from a decline. Requests expire after 30 days. Accepting is an owner's or admin's act (`link.manage`, held by owners and admins), audited on both sides.

### 5.2 The link record

`business_links` is a platform table, not a tenant's: `id`, `a_tenant`, `b_tenant` (ordered so a pair is one row), `status` (`requested`, `active`, `paused`, `ended`, `blocked`), who requested and who accepted (person ids), `a_role` and `b_role` (`customer`, `supplier`, `both`, as each side sees the other), the settings each side keeps for the link (§5.4), timestamps. Each business reads its own links through a definer (`taskiem_links_of`) that returns the other side's name and the link's state, never the other side's rows. Ending a link stops new documents crossing; what crossed stays on both sides, as a supplier's invoice stays when they stop supplying.

### 5.3 Entering the other side

One definer, `taskiem_link_enter(link, action, detail)`, does what `taskiem_partner_enter` does for sub-tenants: checks the link is active and the caller's tenant is one of its sides, appends an audit entry in both businesses (`link.<action>`, naming the link and the document's reference), and sets the transaction's scope to the other side for the rest of the transaction. Every mirror write is one call followed by the writes for that one event into the other side's own tables (a bill's insert; a bill's payment row and its `paid_kobo`; the buyer's money-log entry marked paired), under their ordinary row-level security, and nothing else in the transaction. The caller's own rows are written before the call, in the same transaction, while the scope is still its own.

**Who may call it.** Both definers are owned by `taskiem_dispatch`, like every definer, and EXECUTE is granted to `taskiem_app`, the database role the api, worker and scheduler processes connect as (the matcher, the chat handlers and the reconciler in the api process; `link@1` workflow actions `pay_bill`, `place_order` and `reply` on workers), and not to `taskiem_public`, the role the public host and the mail role connect as. A public page never writes a mirror: it submits to the business it is serving, whose own code path mirrors.

**Two things need both sides, and each has its own path:**
- **Pairing a payout with a credit (§6.3)** is two transactions, one per side, joined by a **pairing token** Taskiem makes, not by either rail's reference, since the buyer pays out on its own connection and the seller collects on another and each provider issues its own id. The token is the bill's reference and a short code (`INV-0042/7QX4`), written into the payout's narration and kept on the buyer's payout entry. When a credit arrives into a link account, the seller's side finds the token **before matching**, so the match can go where the token says: first in what the rail passed on (the narration or payer remark, where the rail gives one); failing that, through a third, read-only definer, `taskiem_link_payout_token(link, amount_kobo, since)`, which answers the token of the one unpaired payout from the linked buyer of that amount within the last three days, and nothing when there is none or more than one (two equal monthly bills paid in the same week pair neither, and both payouts raise the alert below). With a token, the credit is matched to the invoice the token names and the seller enters the buyer's side through `taskiem_link_enter` to mark the bill's payment and the buyer's entry paired. Without one, the credit goes oldest-first as any unnamed payment, and nothing pairs. A payout the rail confirmed that is still unpaired after three days raises an alert for the buyer's owner: the money reached the seller, no bill was paired, and the seller may have counted it against an older invoice; the owner confirms the pair from the bill, and the seller's side is asked to re-aim the match where the two disagree. Neither transaction reads the other tenant's rows except through that definer, which returns a token or nothing.
- **The reconciler** reads through a second, read-only definer, `taskiem_link_digests(link)`, which returns one row per mirrored document from each side (the reference, the side, and a digest of the mirrored fields), never a document's content. The api role runs it hourly; a difference is rewritten through an ordinary mirror write from the source's side. The §14 isolation test allows exactly these three definers (`taskiem_link_enter`, `taskiem_link_digests`, `taskiem_link_payout_token`) and nothing else.

### 5.4 Per-link settings

Each side keeps its own settings for a link, changed on the Linked businesses page or in chat:

| Setting | Side | Default |
| --- | --- | --- |
| Accept their orders automatically, or ask me each time | seller | ask |
| Price list shared with them (§9) | seller | none |
| Credit terms for them: days to pay, and a limit on what they may owe before new orders wait | seller | the business's invoice terms; no limit |
| Pay their bills automatically up to an amount, after my approval policy; else ask me | buyer | ask |
| Where to take delivery, and who may collect | buyer | the storefront's address |
| Tell me when: an order comes from them, a bill arrives, a payment lands, a reply lands on one of our documents, a new message arrives in the conversation | both | all on, in chat and the bell |

### 5.5 The conversation: chat between linked businesses

Once linked, two businesses have a **conversation**, and it is where their trade starts and is talked about. On the phone it is the WhatsApp or Telegram the owner already uses. On the web it is **WhatsApp's look, feel and way of working, deliberately and in detail**, not an app-native chat panel in Taskiem's style: almost every business already knows WhatsApp, and a chat that works differently defeats the purpose of putting one in the app. This is a rule of the design, not a preference. What is WhatsApp's: the two-pane layout with conversations on the left and the open one on the right; bubbles on each side with the time in the corner; day dividers; the double tick for delivered and read; a quoted reply by swiping or by **Reply**; long-press or hover for reply, copy, forward and delete-for-me; the composer with the attachment clip, the camera and the microphone for a voice note that records on hold; search in a conversation; pin, mute and archive; unread counts; the typing line; link previews; and the words those things are called. What is Taskiem's: the colours and type ([screens](../brand/screens.md)), the business's card where a contact's profile would be, and the document cards. A person who can use WhatsApp Web can use this page with nothing to learn.

**The Chat page in the web app.** A list of conversations down the left, one per linked business, with the business's name, its last message and when, and an unread count; the open conversation on the right as a stream of bubbles, theirs on the left and yours on the right, grouped by day, with the composer at the bottom. A search box sits above the list, and it is the one search of this plan (§5.1): chats, messages and businesses on Taskiem to link with, as WhatsApp's search gives chats, messages and contacts. A conversation holds the whole history with that business from the link request onward and is searchable from the same box and from inside it. One more conversation sits in the list: **Taskiem**, the owner's own conversation with Taskiem that they have on WhatsApp today (`status`, `orders`, building by chat), now on the web with the same words and the same history, so the web app is a fourth channel on the one conversation engine (`converse`), beside WhatsApp, Telegram and Slack.

**What is in a conversation.** Everything that passed between the two businesses: typed messages, photos, files and voice notes, and every document as a **card** in the stream at the moment it happened. An order arrives as a card with **Accept**, **Change** and **Decline**; a bill as a card with **Pay** and **Query**; a delivery as a card with the rider's photo; a payment as a receipt card; a price list change as a card. Tapping a card opens the document. A quoted reply to a card is a reply about that document, which is what a thread is (§8): the document's page shows the replies to its card, and the conversation shows them in place.

**Who is talking.** A conversation is between the two businesses. Any member who holds `chat.trade` (a new permission, held by owners, admins and operators) can write; each message shows the business's name with the sender's first name under it, so Dangote's sales rep and Ada's manager can run the trade without either owner. Read marks are the business's, set when any member of the other side opens the conversation. A conversation can be muted or archived by either side, and never deleted by one side: it is the record of the trade, and its messages are mirrors like any other (§5.3), kept on both sides.

**Mentions.** In the composer, `@` suggests linked businesses as you type, and picking one puts a structured reference in the message, not a name to be matched later. In a conversation with a business that shares a price list, `#` suggests its items with the unit and the price, so a quantity is never guessed. A message in the Taskiem conversation that mentions businesses is read back as one draft per business, "Buy 400 bags of cement from @Dangote and 20 pieces of blocks from @Cherry" as two purchase orders at each one's prices and terms, and one **Yes** sends each into its own conversation as an order card; the purchase-order approval limit applies per draft. A mention of a business you have not linked offers the link instead of guessing. The model may route a free-form sentence to the order words and read quantities and items from it, as it routes today, and every order still waits for **Yes**.

**Starting a transaction.** On the web, the composer in a business's conversation, and **New order**, **New invoice** and **Pay** at the top of it; the ordinary pages work too (New invoice's customer picker lists linked businesses first, and the Suppliers page's **Order** sends as an order when the supplier is linked). In chat, the words the owner knows gain a linked name: `order 20 bags of rice from Dangote` drafts from his price list and sends as an order; `invoice Ada's Kitchen 25,000 for 5 cartons` makes the invoice with the linked business as the customer, no phone asked, and it lands as her bill; `pay Dangote 50,000` pays his open bills oldest first; `prices from Dangote`, `bills`, `bills due`, and `ask Dangote …` for a message with no document behind it. A name is looked up among linked businesses first, then suppliers and customers, with a numbered pick when two match.

**The same conversation on the phone.** On WhatsApp and Telegram the owner sees the conversation's messages prefixed with the business's name, as every shared-number message is: "[Dangote Stores] New order P-7QX4KD from Ada's Kitchen: 20 bags of rice, ₦1,000,000. **Accept** · **Change** · **Decline**." A quoted reply there goes into the same conversation and shows on the web; a mention typed by hand on the phone (`@Dangote`) resolves by name as today. A business with its own WhatsApp number carries its linked conversations on that number too. Who hears what follows each person's Tell me settings and the link's per-link switches (§5.4), so a sales rep can be the one who hears about orders and the owner the one who hears about bills.

## 6. L1: invoices become bills, paid once, matched twice

### 6.1 The bill

A **bill** is the buyer's copy of a linked supplier's invoice: `bills` in the buyer's tenant: `id`, `tenant_id`, `link_id`, `supplier` (the link's other side, named), `reference` (the supplier's invoice number, `INV-0042`), `lines` (the invoice's lines, as sent), `amount_due_kobo`, `paid_kobo`, `due_on`, `status` (`open`, `paying`, `part_paid`, `paid`, `disputed`, `void`; `paying` from the moment a payout from Taskiem is created for the bill, including while it waits for a second approver, during which a second **Pay** and auto-pay are refused, and the status returns to `open` or `part_paid` if the payout is rejected, cancelled or refused by the rail), `mirrored_at`, and the way to pay: the supplier's account number for this buyer (§6.3), and the supplier's payment link. A bill is made when a linked supplier sends an invoice whose customer **is the linked business**: the seller chose it as the customer (the picker lists linked businesses first), or the invoice carries the key `link:<buyer>`. An invoice to a bare phone number is never turned into a bill by the number alone, even when the number is the linked business's owner's, because a person can be a consumer customer of one business and staff of another: the seller is asked once, "Is this invoice for Ada's Kitchen?", and only yes makes the bill and sets the key. The bill is written through `taskiem_link_enter` in the same transaction as the send, after every write the send makes in the seller's own business (the invoice's status, its outbox message, its audit entry), since the definer narrows the scope to the buyer for the rest of the transaction. The buyer's side never refuses the seller's document: a mirror counts towards the **sender's** plan limit for mirrored documents, never the receiver's, and where the bill's write cannot be made (the link paused, the buyer's business suspended) the invoice is still sent, the bill waits as a pending mirror on the link, and the reconciler writes it when it can. The invoice's number, lines and amounts are the truth; a change to the invoice (an amendment, a void) mirrors to the bill.

The buyer sees bills on a **Bills** page (beside Invoices: a row per bill, supplier, amount, due, status, **Pay**) and in chat (`bills`, `bill INV-0042`, `pay INV-0042`, `pay Dangote`). A bill that is received-and-not-paid on a purchase order replaces that figure: a purchase order from a linked supplier is paid through its bill, so what is owed to a linked supplier is the sum of their open bills, and the Suppliers page says so.

### 6.2 Paying a bill from Taskiem

**Pay** on a bill, or `pay INV-0042` in chat, pays through the buyer's payments connection as a payout (`pay_bank` to the supplier's account number for this buyer, or to the account the supplier's invoice names): a whole bill, or an amount for a part payment where the invoice allows parts (a linked invoice allows them by default, as a credit line does). It is a payout like any other: the buyer's approval policy applies (an amount above the policy's line waits for a second person), "First payment to this account" applies the first time, and the payout is a money-log entry with the bill in its description. The bill shows `paying` from the moment the payout is created, through its approval, until the rail confirms, then `paid` or `part_paid`; a rejected, cancelled or refused payout returns it to `open` or `part_paid`.

A buyer may also pay outside Taskiem and say so (`paid Dangote 425,000 transfer`, as today for a purchase order). That is the buyer's own record and goes in the bill's own field, `declared_paid_kobo`, never in the mirrored `paid_kobo`: the bill shows "you say you paid ₦425,000; the supplier has counted ₦0 so far", the supplier's side is told "Ada's Kitchen says it paid ₦425,000 by transfer", and `paid_kobo` moves only when the supplier's match says so (§6.3). The two fields disagreeing is not drift for the reconciler; it is a question for the two businesses, and after seven days it opens the bill's thread by itself.

### 6.3 One transfer, matched on both sides

When the link is made, the seller's side issues the buyer a **customer account number** on its collection connection where it can (customers' own account numbers, [matching](../matching.md#customers-own-account-numbers), registered to the customer key `link:<buyer tenant>`), with no one asked, and the bill carries it. Every payment the buyer makes into it is matched **exactly** on the seller's side, with no one asked. Which invoice it pays depends on whether the payment names one: a payout from Taskiem that paid a chosen bill carries the pairing token (§5.3), the bill's invoice reference and a code, in the rail's narration, and is matched to **that invoice**, part or whole, with any excess to the buyer's oldest open invoice and then held as credit; a payment that names nothing, such as a transfer from a bank app, is matched to the buyer's **oldest open invoice** first, as a student's fee is, and a buyer who wants a bank-app transfer to go to one bill puts the bill's reference in the transfer's narration. **This is a change to matching**, planned in L1: today money into a customer's own account number goes to their oldest open invoice whatever the narration says ([matching](../matching.md#customers-own-account-numbers)); after L1 it goes first to the open invoice the narration names, for a customer with a `link:` key, and `docs/matching.md` says so. A second change beside it: an invoice whose bill is disputed (§8) carries a `disputed` flag mirrored from the bill, and oldest-first skips flagged invoices, so a disputed older invoice never absorbs money meant for another; money that names a flagged invoice still pays it. So `pay INV-0042` pays INV-0042 on both sides. The match is the event that closes the loop:

- the seller's invoice moves to part-paid or paid, as today;
- the link mirrors the payment to the buyer's bill (`taskiem_link_enter`, then a payment row on the bill naming the seller's money-log entry by reference and the bill's mirrored `paid_kobo`): the seller's match is the one source of `paid_kobo`;
- where the buyer paid from Taskiem, the buyer's payout entry and the seller's credit entry are **paired** by the link and the pairing token (§5.3), so neither side sees the money in To match, and the buyer's expense for the bill is the payout, not a second entry;
- where the buyer paid from their bank, the seller's credit still matches exactly by the account number, and the buyer's bill's `paid_kobo` is set by the mirror; a buyer who had already said so by hand (`declared_paid_kobo`) sees the two agree, and nothing is doubled.

A seller with no connection that issues account numbers falls back to its payment link and payer memory, and the bill says so; the buyer's payout from Taskiem still pairs by the token.

### 6.4 Accept

- Dangote sends Ada an invoice; a bill appears in Ada's business within the same transaction, with his lines and her account number. Ada pays it from Taskiem after her approval; Dangote's invoice is paid, Ada's bill is paid, both money logs have one entry each, paired, and neither To match has anything. Ada pays the next one from her bank app into the same number; the same result, with Ada's bill marked by the mirror.
- A part payment on each path; an amendment and a void mirrored; a bill for a customer not on Taskiem is never made, and the invoice goes as today.
- A member of Ada's business can never read Dangote's rows: the definer narrows the scope for one write and the audit shows both entries.

## 7. L2: purchase orders become orders, and the order's life flows back

- **Sending.** A purchase order to a linked supplier (drafted by hand, from the low list, or by `reorder-from-supplier`) is not sent as a chat message. Through the link it becomes an **order** in the seller's business: a storefront submission of a new kind, `trade`, with the buyer as the customer (named by the link, no sealed phone needed), the purchase order's lines (matched to the seller's items by the price list where one is shared, §9, else by name with the seller asked to confirm the match once), and the seller's prices. It arrives in the seller's Orders page and chat like any order, and starts the seller's order workflow (the plain one, or the delivered-by-riders one), or is accepted automatically where the seller set that for this link.
- **Flowing back.** The seller's accept, decline, "ready", dispatch (the rider's taps) and delivered mirror to the buyer's purchase order as `confirmed`, `cancelled`, a note, and **delivered**, with the seller's quantities per line in a mirrored `delivered` field. What the buyer **received** is the buyer's own field, never mirrored and never compared: it is proposed from `delivered`, and confirmed either by the buyer's tap or, when a day passes with no **Not all came**, by itself from the seller's figure. Only that confirmation makes the `received` stock movements, so by default the seller's delivered quantity does move the buyer's stock and set the billed quantity a day later, which is intended (a buyer who checks deliveries taps, one who does not gets the seller's count, as a paper delivery note works) and is named in §17. A **Not all came** within the day sets `received` to the buyer's count, opens the order's card as a dispute, and the invoice follows the agreed quantity. The seller's counter-offer (a changed quantity or price) is the **Change** of today, carried as a mirrored note, with the buyer's **Accept the change** re-sending the order. Nothing a seller does changes a buyer's stock except through `delivered` proposing `received`, and nothing a buyer does changes a seller's order except placing, accepting a change, and cancelling.
- **Cancelling.** Before the seller accepts, the buyer cancels freely and the order is cancelled on both sides. After acceptance, a cancel is a **request** the seller accepts or declines from the order's card, as a seller's change is one the buyer accepts; the seller's own cancel (decline, or cancel after acceptance) mirrors as `cancelled`. Once anything is received, neither side cancels; the rest is a change. This is narrower than today's rule for an ordinary purchase order, which the buyer may cancel until anything is received ([stock](../stock.md#suppliers-and-purchase-orders)), and `stock.md` notes the difference for linked suppliers.
- **The invoice.** When the seller accepts, or when the goods are delivered (the seller's choice per link), the seller's business raises the invoice for the order and it becomes the buyer's bill (L1), so an order placed from a low list on Monday is a bill paid on Friday with no one typing a number.
- **Orders the seller places for the buyer.** A sales rep who takes orders by phone drafts an order in the seller's business for a linked buyer; it mirrors to the buyer as a purchase order awaiting the buyer's **Confirm**, and goes no further until then.
- **Accept:** Ada's low list drafts an order to Dangote; it arrives as an order in Dangote's business, his rider delivers it, Ada's stock rises by what came, his invoice is her bill, she pays it, both are paid; a seller's change, a buyer's cancellation before acceptance, and a buyer's cancel request after acceptance accepted and declined; an order to an unlinked supplier goes by chat exactly as today.

## 8. L3: the conversation on the web, and disputes

- **The web channel.** The Chat page (§5.5) is a channel on `converse`: a conversation list, the message stream, the composer with mentions, document cards with their buttons, photos, files and voice notes, read marks, search, mute and archive, and the Taskiem conversation. Messages between linked businesses are mirrors written through the definer, so both sides keep the whole history under their own tenancy.
- **Threads are quoted replies.** A reply to a document's card is the document's thread: it shows on the document's page in both web apps and in both businesses' chat, and `ask Dangote about INV-0042` from the phone lands as a quoted reply too.
- **Disputes.** **Query this bill** on a bill's card marks the bill `disputed` and sets a `disputed` flag on the seller's invoice (a flag, since an invoice's status comes from its payments), with the reply opened; a disputed bill is not paid automatically, is not reminded, and its invoice is skipped by oldest-first matching (§6.3); the seller's **Resolve** (an amendment, a credit note, or "as it stands") clears both.
- **Accept:** a person who uses WhatsApp Web sends a message, replies to a bill's card by quoting it, records a voice note and archives a conversation on the Chat page without being told how; a message from Ada's web conversation appears in Dangote's web conversation and his WhatsApp within seconds, with his reply shown in both; a quoted reply to a bill's card shows on the bill's page; a disputed bill is skipped by auto-pay and reminders until resolved; a member without `chat.trade` can read but not write.

## 9. L4: price lists, item links and mentions

- A seller shares a **price list** with a linked buyer: a front's list block (the catalogue, or a wholesale front's list under the fronts plan, in review on PR 135) with, per item, the buyer's price, a minimum quantity, and availability; one list may be shared with many links, and a link may override prices. The buyer sees it as **Dangote's prices** on the Suppliers page and in chat (`prices from Dangote`), and may order from it directly (`order 20 bags of rice from Dangote`), which drafts a purchase order at those prices.
- **Item links.** The buyer's stock items are linked to the seller's items (`item_links`: the buyer's item, the seller's item, the link), made when the buyer orders an item from the price list the first time, or from the Suppliers page. A linked item's purchase orders carry the seller's item id, so the seller's order needs no name matching, and the buyer's cost price follows the seller's price.
- **Price changes** mirror to the buyer's view at once; an open purchase order keeps the price it was placed at, and the seller may not change it after acceptance except through a change the buyer accepts.
- **Mentions** (§5.5): `@` suggests linked businesses and `#` suggests a linked business's items with unit and price; a message naming several businesses reads back as one draft per business.
- **Accept:** `@Dangote` and `@Cherry` in one message read back as two orders and one **Yes** sends both (which needs L2's orders and this milestone's mentions); a mention of an unlinked business offers the link; `#rice` offers the item with its unit and price.
- **Accept:** a price list shared and withdrawn; an order from it at the buyer's prices; an item linked once and reordered without matching; a price change that does not touch an open order.

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
| Finding out who is on Taskiem | A name search lists only businesses whose findability is on (§5.1); a phone or address lookup answers the same words whether or not a business is behind it, and only an acceptance reveals one |
| A link forced on a business | Nothing crosses before both sides accept; a blind request hears nothing but acceptance, so decline, expiry, a block and no business at all are the same silence, while a request to a listed business hears "Not accepted" after a decline or 30 days; Block takes later requests and drops them with the same answer after the same time; `link.manage` to accept, audited both sides; requests limited per business per day and per target |
| One business reading another's rows | No scope is ever held, and three definers are the only crossing, all owned by `taskiem_dispatch` and executable by `taskiem_app` alone (§5.3). `taskiem_link_enter`: link active, caller a side, dual audit, the scope narrowed for the rest of the transaction, then the writes for that one event into the other side's own tables, which includes the pairing's update of the buyer's payout entry found by the pairing token. `taskiem_link_digests`: read-only, one row per mirrored document from each side with a digest of its mirrored fields and no content, for the reconciler. `taskiem_link_payout_token`: read-only, the token of the one unpaired payout of an amount within a window, or nothing, for pairing. No code path reads the other side's rows; a test asserts no query reaches the other tenant outside those three |
| A mirror that lies (a bill for an invoice that does not exist, an amount changed in transit) | A mirror is written in the same transaction as the source's change, by the source's code path, never from a message; it names the source by link and reference, and the reconciler compares each mirrored field's digest with its source's through `taskiem_link_digests` hourly and raises an alert on any difference. Fields each side owns (`declared_paid_kobo`, `received`) are never compared |
| Money moved by the link | The link moves no money. A payout is the buyer's, through its own connection and approval policy, with "First payment to this account"; a credit is the seller's, matched by its own registration. The link only pairs the two entries, by the pairing token Taskiem wrote into the payout's narration (§5.3), never by either rail's reference |
| A seller changing an accepted order's price | Refused after acceptance; a change is a counter the buyer accepts, and the purchase order keeps the price it was placed at |
| The seller asking about the buyer's payouts | `taskiem_link_payout_token` answers one question about the other side's money: whether the linked buyer made one unpaired payout of an amount in the last three days. It is called only by the matcher, once per credit that arrives into that buyer's link account, with that credit's amount, never from a request, a workflow or chat; it answers a token or nothing, never an amount, a time or a count; and each call is audited on the buyer's side (`link.payout_token`), so a seller cannot sweep amounts to learn what the buyer pays |
| Personal data across the link | A link carries business names, references, lines and amounts; never a member's phone, email or name beyond the first name on a thread message; a linked buyer's customer record on the seller's side is keyed `link:<tenant>`, with no sealed phone |
| A business that ends or blocks a link | Mirrors stay, nothing new crosses, open bills stay payable by the account number, and threads close; a blocked business's later requests are taken and dropped silently, with the same words and the same "Not accepted" after 30 days as any other (§5.1), so a block is never told apart from a decline |
| Plan abuse | Links per business, requests per day, mirrored documents per month and messages per day are plan limits, counted as other limits are; a mirrored document or message counts against the business that sent it, so one business's limit never refuses another's document (§6.1) |
| Files and voice notes in a conversation | Fetched through the channel's media download or uploaded on the web, size-capped, scanned where the deployment has a scanner (as hosted forms' files are), stripped of metadata, and kept under the business's private storage on each side |
| The Chat page's search leaking across tenants | Chats and messages come from the business's own rows; businesses on Taskiem come from an index built from published storefront content and the card of businesses whose findability is on; never private tables, stock, private price lists, customers or members; a phone or address typed into the box shows one row that sends a request on a tap alone and looks the same either way, and never a result |
| A mention that reaches a third business | A mention resolves only among the writer's own links; a business mentioned in a conversation with another is a reference the other side sees as a name, never a door into the third business |

## 14. Testing

- **Isolation**: a test harness with two tenants that asserts, for every mirror write, one `taskiem_link_enter` call, two audit entries and no other cross-tenant query, for the reconciler one `taskiem_link_digests` call returning digests only, and for a credit into a link account at most one `taskiem_link_payout_token` call returning a token or nothing, with that credit's amount, audited on the buyer's side; nothing else crosses (the query log is read in the test, as the replica tests do).
- **The loop**: L1's accepts with a fake rail on each side; a send to a buyer over its limit or with its business suspended still sends the invoice, and the bill is written later by the reconciler; the pairing by token, through the narration and through `taskiem_link_payout_token`, the token found before the match so `pay INV-0042` pays INV-0042 when the rail drops the narration, two equal payouts pairing neither, and the alert on a payout unpaired after three days; part payments; a payment from outside Taskiem; an amendment and a void.
- **Orders**: L2's accepts through the storefront's order harness and the stock harness together, riders included.
- **Reconciler**: a mirror changed by hand is reported.
- **Chat**: `link`, `bills`, `pay`, `prices from`, `ask … about`, on WhatsApp and Telegram, with the business name in brackets.
- **Privacy**: a phone lookup answers the same words for a business and for no business; a name search lists only businesses whose findability is on; a declined, expired and blocked request to a listed business all end with the same "Not accepted" after the same time.
- **Web**: the Linked businesses and Bills pages pass axe in both themes and the plain-words test.

## 15. Needs a person

| Item | Who | Blocks |
| --- | --- | --- |
| The owner-facing words: "linked business", "bill", "price list", and whether the request reads as "connect" or "link", in the five languages | Founder, reviewers | L0 |
| Which plans include links, and the limits per plan | Founder | L0 |
| Whether an invoice to a linked buyer is raised at acceptance or at delivery by default | Founder | L2 |
| Settlement on Taskiem's own rail for linked businesses (§12), and whether it is free of fees | Founder, iswallet | later |
| The Chat page's colours and type within WhatsApp's layout: the brand applies to colour and type only; layout, gestures, ticks and words follow WhatsApp (§5.5) | Founder, with the brand | L3 |
| Whether people on the other side are addressable (`@Dangote/Musa`) in the first cut, or businesses only | Founder | L4 (recommendation: businesses only) |

## 16. Milestones

| Id | Milestone | Size |
| --- | --- | --- |
| L0 | **Links and search**: `business_links`, `taskiem_links_of`, `taskiem_link_enter` with dual audit, `link.manage`; the Chat page's left pane: the list of linked businesses and pending requests with the search box (linked businesses, and businesses on Taskiem by name, what they sell and area, from an index of published storefront content; messages join in L3), where a tap opens the business's card (the link's state and settings, Accept, Decline, Block, Cancel request, and from L1 the documents between you) until L3 puts the conversation there, `find …` and `who sells …` in chat; lookup by phone and address; the offer at the first document; the link code on the QR card, invoices and receipts; accept, decline, block, end; the Linked businesses page and `link` in chat; the buyer's customer account number issued at acceptance; the isolation test harness | M |
| L1 | **Bills**: `bills`, the mirror from a linked invoice (and amendments and voids), the Bills page and chat words, **Pay** through the payout path with approval and first-payment, paying outside Taskiem, exact matching on the seller's side with the mirror back, pairing of payout and credit by the pairing token, matching's change to pay the invoice a payment names and to skip disputed invoices (`docs/matching.md` updated), the Suppliers page's owed figure from bills, the reconciler | L |
| L2 | **Orders**: a purchase order to a linked supplier as a `trade` order, the status flow back (confirmed, cancelled, received with the correction window), the cancel request after acceptance, the seller's change and the buyer's acceptance, the invoice raised at acceptance or delivery, orders placed by the seller for the buyer | L |
| L3 | **The conversation**: the web channel on `converse`; the Chat page's right pane: the stream, document cards with their buttons, photos, files and voice notes, read marks, search, mute and archive; the Taskiem conversation on the web; `chat.trade`; messages mirrored both sides and shown on the phone with the business's name; quoted replies as threads; disputes | L |
| L4 | **Mentions, price lists and item links**: `@` businesses and `#` items in the composer, one message read back as a draft per business, the unlinked-mention offer; sharing a price list per link, overrides, `prices from`, ordering from the list, `item_links`, cost prices that follow | M |
| L5 | **Statements, credit and reminders**: the link's statement each side, terms and limits per link, the hold on orders over the limit, reminders as notices with Pay, bills due in the summary | M |
| L6 | **Automation**: `link@1` triggers and actions, the four templates, the builder's rules and evals | M |
| L7 | **Docs and the decision**: invoices, stock, storefronts, matching, the owners' guide ("Trade with businesses on Taskiem"), the atlas's business modules and identity pages, the threat model; decision 0061 accepted with As built | S |

In order, one PR each; L0 and L1 together if the first linked pair is waiting. L3 can come before L2 if the first linked pair wants to talk before it orders.

### 16.1 As built (L0, first slice)

The link itself, its search and its words in chat; migration 00275, `engine/links`, `api/links.go`, `api/chat_links.go`.

- **The link record.** `business_links` is a platform table that no business reads: its policy for `taskiem_app` admits nothing, and each side reads it through `taskiem_links_of`. Its statuses are `requested`, `active`, `declined`, `blocked`, `cancelled` and `ended`; `paused` waits for a setting that needs it.
- **What each side sees.** `taskiem_link_view` decides it, in one place:
  - the side asked sees `asked_us` while the request waits;
  - the side that asked sees `requested` until the 30 days pass, whatever the answer, then `not_accepted`;
  - a request sent blind never shows from `business_links` to the side that asked: its own `link_blind_requests` row stands for it, with the address masked, until it is accepted;
  - a link that was made and then blocked reads `ended` to the other side.
- **The functions** (`definers.tsv`), all owned by `taskiem_dispatch` and granted to `taskiem_app` only:
  - `taskiem_links_of`;
  - `taskiem_link_search(q, limit)`;
  - `taskiem_link_request(target, phone, slug, role, actor)`;
  - `taskiem_link_answer(link, answer, actor)` for accept, decline, block, unblock, cancel and end;
  - `taskiem_link_enter`.
  The helpers they call (`taskiem_link_self`, `taskiem_link_view`, `taskiem_link_role`, `taskiem_link_findable`, `taskiem_link_audit_other`) are not granted to the application.
- **Search** (§5.1).
  - Every word must match the business's published card (name, tagline, area) or one of its published, available items (name or category). Up to three matching items come back with their public prices.
  - Linked businesses rank first, then those in the searcher's own storefront area, then by name.
  - A query holding a phone number finds nothing; the API answers it with the one `blind` row instead.
  - Not built in this slice: the business's kind from sign-up, and the area of a findable business with no storefront. Such a business is found by name only.
- **Findability.** `link_settings.findable` holds the business's choice. Left empty, it is on with a published storefront. The once-asked question in the sign-up chat is not built yet; `GET` and `PUT /v1/link-settings` set it.
- **Requests.**
  - **By search,** the target must be findable, or the request is refused as not found.
  - **Blind,** by phone, the number must be an owner's linked WhatsApp number or a business's own active WhatsApp number, and name exactly one business; by address, the business's published storefront address. Either way the caller gets back only whom to tell, never anything to show.
  - **Limits:** at most 20 requests a day through `business_links`, and 20 blind ones counted in the requester's own table.
  - **Asking again** within the 30 days tells no one. Asking a business that blocked us restarts our own clock and tells no one. Asking a business we blocked lifts our block.
  - **Unblocking** a link that was made returns it to ended. Unblocking a request makes it declined, so its requester, who saw it waiting while it was blocked, still sees it waiting until its 30 days pass.
- **Telling people.**
  - The other side's owners and admins are told after the request has been answered (`s.background`), in the app's bell and in chat with **Accept**, **Decline** and **Block** (`lk:<answer>:<link>`).
  - They are told in their own business's scope, from that business's own view of the link, so nothing the requester's transaction reads crosses.
  - An acceptance and the end of a link are told to the other side. A decline, a block and a cancel are never told.
- **Chat words.**
  - `links` lists the business's links.
  - `link Dangote` and `who sells cement` answer a numbered list; the number picks one, and three buttons give the role.
  - `link 0803…`, or `link <address>` when it names no business found, asks blind.
  - `find …` keeps its data-table meaning. Words that name no table search businesses, and a search that finds none gives the old table answer.
  - In this slice the words are English only; routing them in the other four languages is still to do.
- **Audit.** `link.request`, `link.accept`, `link.decline`, `link.block`, `link.unblock`, `link.cancel` and `link.end` go in the actor's chain. Each goes in the other side's chain too where that side may know of it (`link.requested` for a request). `taskiem_link_enter` writes both chains, with the other side's entry naming `link:<business>/<actor>`.
- **Tests.**
  - `engine/db/links_test.go`:
    - search and findability;
    - a decline and a block that read the same to the requester, during the 30 days and after;
    - a blind request reaching only an owner's number;
    - the one door's narrowing and refusals.
  - `api/links_test.go`: the chat and API path end to end, including:
    - the same words for a blind request to a business and to no one;
    - silence on a decline;
    - a viewer refused.

- **The web** (`web/src/pages/Links.tsx`, `web/src/lib/links.ts`).
  - **Chat** (`/chat`, in the menu for every member) is the Chat page's left pane until L3 adds the conversation. It lists requests to answer, with **Accept**, **Decline** and **Block**, then linked businesses, then requests waiting.
  - Its search box gives **Chats** (linked businesses whose name matches) and **Businesses on Taskiem**, each with **Request link** and the three role choices.
  - A phone number typed shows the one row **Send a link request to 0803…4567**.
  - Members without `link.manage` see the rows and the line "An owner or admin can link with this business", with no button.
  - **Linked businesses** (`/settings/links`, under More for `link.manage`) holds the **Let other businesses find us** switch and every link and request with what can be done to it: End link, Withdraw request, Unblock, Take off the list.
  - Tests: `web/src/lib/links.test.ts`; `web/e2e/links.spec.ts`, two businesses that find each other, link and end it, at 390 px, with axe in both themes; and both pages in `accessibility.spec.ts` and `plainwords.test.ts`.

- **Link codes.**
  - Every business has a link code (`link_settings.code`, eight letters and digits), made the first time its settings are read.
  - `/l/<code>` in the web app names the business and asks with **Request link**. Signed out, it offers to sign in or sign up first.
  - A request by code (`how` `code`) needs no findability, because the business chose to hand the code out. To its requester it reads as a search request does: after 30 days, "not accepted".
  - **Linked businesses** shows the link, **Copy the link** and the QR code (`/v1/link-settings/qr`, SVG or PNG).
  - **Printed.** An invoice's public page, in every theme, carries "Is your business on Taskiem too? Link with <business>" with the code (`taskiem_public_link_code`, the public role's). A paid invoice's page is the customer's receipt, so receipts carry it too. Sending an invoice makes the business's code if it has none.
  - The storefront's printable card carries a second, smaller code under the page's, "Link with us on Taskiem".
  - School receipts, which parents check, do not carry it.
- **The offer at the first document.**
  - An invoice sent to a phone that is another business's own (`taskiem_link_offer_to`: its owner's linked number or its own WhatsApp number, never a member's, the two neither linked nor asked) carries "<business> is on Taskiem too. Link with us: <link code>". So does a purchase order sent to such a supplier on WhatsApp, in the supplier's language.
  - Each phone is offered once (`link_offers`, a keyed hash on the sender's side).
  - On an invoice, the line is left out, and not counted as offered, when it would take the SMS past its three parts. With a naira amount the SMS is Unicode, so a longer business name often leaves no room, and the line then goes with a later document that has it.
  - On WhatsApp the offer also comes with a **Link** button (`lk:offer:<code>`), on a purchase order to a supplier, an invoice sent in chat and an order update. A member of the business who taps it is asked how they trade, and the request goes. From a number no one is bound to (a business's own WhatsApp number), it answers the link to open signed in.
- **Findability, asked once.**
  - A business made in chat is asked in the sign-up's last message: "send *find us* to let them, or *hide us*". Sign-up records that it was asked, so the web does not ask again.
  - Any other business is asked once on the Chat page, where **Not now** keeps the default.
  - `find us` and `hide us` work in chat at any time.
- **The buyer's account number at acceptance (§6.3).**
  - When a link is accepted, each side the other buys from issues it a customer account number (`IssueAccount`, the key `link:<buyer>`), after the answer and in its own scope.
  - It uses a connection that needs nothing a business cannot give (no email, BVN or wallet to choose), the invoice settings' connection first. Where there is none, nothing is issued.
  - The bill that carries the number is L1's.

- **The isolation harness (§14), for L0.** `api/links_isolation_test.go` runs the API on a pool whose query tracer logs every statement with its transaction's tenant scope, while two businesses search, ask, accept by chat, open a link code and end the link. It asserts that:
  - no transaction holds both businesses;
  - a transaction of one names the other only in a call to the link functions;
  - no statement reads `business_links` itself.

  A query planted in the accept path, naming the other business, makes it fail. L1 adds the mirror writes' checks: one `taskiem_link_enter` call and two audit entries each.

**Left for later milestones:** a business's card with its documents (L1), and messages in the search (L3).

### 16.2 As built (L1)

Migration 00276, `engine/links/bills.go`, `engine/matching/customers.go`, `connectors/bills`, the `pay-linked-bill` template, `api/bills.go`, `web/src/pages/Bills.tsx`.

- **The mirror.** It is not written in the seller's send transaction, as §6.1 has it, but through a durable queue. The seller's invoice is never refused for the buyer's side, and every path that changes an invoice is covered without touching each one.
  - A trigger on `customer_invoices` notes every sent invoice whose customer key is `link:<buyer>` in the seller's `link_mirrors_due`, on any change: send, amendment, void, a payment counted or undone, overdue, a query.
  - A worker (`LinkMirrorTick`, every few seconds) mirrors each in a transaction of its own. In the seller's scope it reads the invoice, the link and the way to pay, and takes the invoice off the list. It then enters the buyer's side through `taskiem_link_enter` (`link.bill`, audited in both) and writes the bill.
  - A mirror that cannot be written stays due, with back-off. An invoice with no active link mirrors nothing.
- **The bill.** It carries the seller's figures (number, lines, total, due date, what its match counted paid, void) and the way to pay: the account number issued at acceptance, its bank, and the invoice's page. `disputed` and `declared_paid_kobo` are the buyer's own, and so are `paying_kobo` and `sent_kobo` (a payout under way, and what the bank confirmed was sent). Its status is open, paying, part_paid, paid, disputed or void.
- **Paying from Taskiem (§6.2).**
  - `PUT /v1/bills/pay/setup` makes a managed workflow from `pay-linked-bill`, Taskiem's wallet by default: the name on the account found, one approval, `pay_bank` once keyed by the payment with the bill's token `INV-…/CODE` in the narration, the bank's confirmation awaited. The `bills@1` platform connector records the outcome.
  - `POST /v1/bills/{id}/pay` starts a payment. The supplier's bank is found by name among the banks the payments connection lists, with "Bank", "PLC", "Limited" and punctuation set aside and exactly one match; otherwise it is refused plainly (the decision of 31 October 2026: the rails return a bank's name, not its code, when they issue an account).
  - A bill is paying while a payment is under way, and a second **Pay** is refused. A payment whose run failed or was cancelled is settled as failed when bills are next paid.
- **Matching (§6.3), changed.** For a customer keyed `link:`, money into the account the seller issued it goes first to the open invoice it names: an invoice number in the rail's note, or, when the rail dropped the note, the invoice of the buyer's one unpaired payout of that amount in three days. `taskiem_link_payout_token` answers that token, or nothing.
  - With a token, the buyer's payment is marked paired, entering its side through `taskiem_link_enter` as the transaction's last write.
  - Oldest-first passes over invoices the buyer queried (`customer_invoices.disputed`). Money that names one still pays it. `docs/matching.md` says so.
- **What each side says.**
  - `POST /v1/bills/{id}/declare` records the buyer's own word that it paid outside Taskiem, never in `paid_kobo`.
  - `POST /v1/bills/{id}/query` queries a bill. The bill is not paid from Taskiem until settled, and the seller's invoice is flagged.
  - `POST /v1/invoices/{id}/settle-query` is the seller's answer, and clears both.
  - Each is a `link_notices` row written into the side told through `taskiem_link_enter`, and that side's people are told in their own scope.
- **The reconciler.** It is not the `taskiem_link_digests` of §5.3 but one function, `taskiem_link_reconcile()`, whose only write is the sellers' mirror list. Hourly, it compares every active link's invoices, sent since the link was accepted, with their bills by the mirrored figures (number, total, paid, due date, void). It puts each that differs or is missing back on its seller's mirror list, answering only a count. The worker then writes it again from the invoice, and the count is logged. Lines are not compared.
- **Reading and choosing.**
  - Chat: `bills` and `bill INV-0042`, which includes how to pay.
  - The web: **Bills** and a bill's page with **Pay**, **Paid outside Taskiem** and **Query this bill**. An invoice's page shows a buyer's query with **Settle the query**.
  - New invoice offers linked businesses that buy from us.
  - The Suppliers page shows what is owed to each linked supplier, from its open bills.
- **Tests.**
  - `api/bills_test.go`: the mirror, payments and voids followed, the reconciler, chat, Pay and its refusals, matching by note and by token with pairing, a queried invoice passed over, declare, query and settle.
  - `engine/links/bills_test.go`: bank names.
  - `templates/tests/pay-linked-bill.test.json`.
  - `e2e/bills.spec.ts`.
- **Not built in L1:**
  - asking "Is this invoice for <business>?" when an invoice goes to a linked business's owner's phone;
  - the bill's thread opening by itself after seven days of a declared payment the seller has not counted (it needs L3's conversation);
  - auto-pay (§5.4);
  - an alert to the buyer for a payout unpaired after three days.

### 16.3 As built (L2, first slice)

Migration 00277, `engine/links/orders.go`, `engine/stock/purchases.go` (`LinkSupplierTx`, the send), `api/link_orders.go`, `web/src/pages/TradeOrders.tsx`.

- **A linked supplier.** A supplier record says which linked business it is (`suppliers.link_id`, **On Taskiem** on the Suppliers page, `PUT /v1/stock/suppliers/{id}/link`), one supplier per link. It is chosen by the buyer, not inferred from a phone number. Its orders then go through the link while the link is active, and by chat as before otherwise.
- **The order.** It is not a storefront submission of kind `trade`, as §7 has it, but a table of its own in the seller's business, `trade_orders`: a storefront order needs a storefront, a sealed customer phone and a deployed workflow, none of which a seller selling to a business needs. It is shown on **Orders** (a card of those needing the seller), on **Orders from linked businesses** and in chat. It does not start the seller's order workflow, and does not lower the seller's stock or offer riders: named for a later slice.
  - **Sending.** The send notes the purchase order in `link_orders_due` (a trigger, since a send happens inside callers' transactions that write more after it), and the worker writes the order through `taskiem_link_enter` (`link.order.sent`). The lines carry the buyer's items, quantities and last costs as proposed prices.
  - **Every step after the send is one transaction.** The side that acts writes its own record, then enters the other through the door and writes the mirror, its last writes. A buyer's cancel that meets an acceptance made a moment before is refused whole, and asked again.
- **The seller's steps** (`POST /v1/trade-orders/{id}/{step}`): accept (every line priced), change (a quantity, 0 for none, and a price per line), decline, ready, dispatched, delivered (what was delivered of each line), cancel (its own, or yes to the buyer's request), keep (no to the request), agree (the buyer's count of a short delivery). The buyer's purchase order follows: confirmed, cancelled, the seller's status and word, the change, `delivered` per line.
- **The buyer's steps.** Cancel before acceptance cancels both sides; after, it asks. **Accept the change** takes the seller's quantities and prices and accepts the order on both sides: it is not sent again for the seller to accept what it proposed, as §7 has it. **All came** and **Not all came** (`POST /v1/stock/purchases/{id}/all-came`, `/not-all-came` with what came of each line) record what came as received, raising the buyer's stock. With neither, the worker receives the seller's count a day after the delivery.
- **The invoice.** Raised by the worker when the seller accepts or once the delivery is received, as the seller sets per link (`PUT /v1/links/{id}/trade`; on delivery by default, the founder's decision still to make, §15). On delivery it charges what came, by the seller's count or the buyer's once agreed. Raised at acceptance, a short count the seller agrees takes what did not come off it as a discount. It is sent to the buyer as a linked customer and becomes its bill (L1).
- **Telling.** Each step tells the other side's people who manage orders, in the bell and in chat, in their own scope: a new order with **Accept** and **Decline**, a delivery with **All came**, answered in chat for whichever of the person's businesses holds it.
- **Tests.** `api/link_orders_test.go`: the order's life (sent, change accepted, readied, dispatched, delivered, all came, invoiced and mirrored as a bill), a cancel before acceptance, a request kept and granted, a decline, invoiced at acceptance with a short count agreed, the day's receipt, chat buttons; the isolation harness over an order's life (`TestLinkedOrderIsolation`), which reads what follows the door as the other side's. `e2e/link-orders.spec.ts`.
- **Not built in this slice:** the `trade` storefront order and the seller's workflow, riders and stock; matching lines to the seller's items (L4's item links); auto-accept per link; orders the seller places for the buyer.

## 17. Risks and open questions

| Risk or question | What we do |
| --- | --- |
| Two businesses linked that are the same owner's (a shop and its kitchen as separate tenants) | Allowed; the link is a relationship between tenants, and the same owner on both sides accepts twice; the audit names them |
| The buyer's phone on the seller's invoice is a person's, who may be a consumer customer here and staff elsewhere | The offer at the first document goes only to the business behind its **owner's** linked number or its **own WhatsApp number**, never a member's; and no invoice becomes a bill by a phone number alone (§6.1), so Musa's personal invoices never land in his employer's business |
| The reconciler finds a difference | It compares only mirrored fields with their one source (the issuer's invoice for what is owed, the seller's match for `paid_kobo`, the seller's order for `delivered`), alerts the operators and both businesses' owners, names the document, and rewrites the mirror from the source. A field each side owns for itself is never rewritten; a difference there is a dispute on the document's card |
| A seller's order workflow expects a sealed customer phone (notify_customer) | A `trade` order's customer is the link; `notify_customer` on it writes to the buyer's thread and chat instead of SMS, and the order templates need no change |
| Received quantities disagree, or the buyer never checks | The seller's `delivered` is the default: a day with no **Not all came** confirms `received` from it, moves the buyer's stock by the seller's count and, where the invoice was raised at acceptance, leaves it charged for what the seller says it sent (20 bags billed when 15 came, until the buyer says so). A **Not all came** within the day sets the buyer's count, opens the order's card as a dispute, and the invoice follows the agreed quantity; after the day it is still a dispute on the card, with a stock adjustment and a credit note when the seller agrees |
| Name matching of order lines to the seller's items before a price list exists | Asked once per item on the seller's side ("Is Ada's 'rice 50kg' your 'Rice, 50 kg bag'?"), then remembered as an item link |

## 18. Out of scope

A shared document that both businesses edit (each keeps its copy); a marketplace or directory of businesses (a page to browse, categories, ratings, promoted listings), which would sit on the search's index if it ever comes; settlement on Taskiem's rail (§12, named for later); invoice financing, escrow and ratings; links across deployments of Taskiem; a business linking with a partner's sub-tenant (a sub-tenant is a business and may link like any other, but the partner is not told); consumer customers, who are outsiders as today; mentioning a person on the other side rather than the business (later); group conversations of more than two businesses.
