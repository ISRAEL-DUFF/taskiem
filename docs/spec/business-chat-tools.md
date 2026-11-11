# Business chat tools: a conversation with a business is about that business

Status: **agreed, not built**. Agreed with the founder in conversation
(11 Nov 2026), with the decisions in §7. It amends
[chat-tools.md](chat-tools.md) §5 (the buyer's tools) and §3 (the panel and
the picker) for conversations with another business. The Taskiem conversation
does not change.

## 1. The idea

When Ada's Kitchen talks to John's Superstore, the conversation is about the
business between the two of them. So the tools in that conversation (the panel,
"/", "#" and "@") are about **John's Superstore and Ada's dealings with it**,
not about Ada's own shop. Ada's own tools (Sell, her Products, Stock, Tasks,
Briefing, Tables, Money and the rest) stay where they are today: in her
conversation with Taskiem.

It works the same from the other side. In the same conversation John sees
Ada's Kitchen: what she sells, the orders and invoices between them.

Two linked businesses can do business without going anywhere else: find what
the other sells, look around as on their storefront, order, book, agree a
price, invoice and pay.

## 2. What each side sees of the other

**Everything they show the world, and whatever they share with you.**

- **Their storefront, without a price list.** A linked business sees the other's
  products and services as their storefront shows them: names, photos,
  descriptions, categories, sizes and colours, the storefront price, and how
  many are left where the storefront shows it (or available, sold out).
  Nothing has to be shared first, and the storefront need not be published: a
  linked business sees every item marked to show on it, as the published page
  would show it. What a storefront shows is public, so the conversation shows
  all of it.
- **Their price for you, where they share one.** Where John shares a price list
  with Ada, his price for her (and his lower price for more, his least) is shown
  instead of the storefront price for those items, as ordering from a price list
  does today.
- **Never:** stock their storefront does not show, their costs, their lowest prices, their
  other customers, their other orders, invoices and bills, their staff, or their
  money.

**Between the two of you, both ways.** Orders Ada placed with John and orders
John placed with Ada; invoices each sent the other (each side's bill is the
other's invoice); deals and their history; the statement and credit terms
between them. Nothing between either of them and anyone else.

## 3. The tools panel in a business's conversation

Their side first, then what is ours but about them.

| Tool | What it is |
| --- | --- |
| **Their shop** | Their products and services as in §2: search, their categories as chips, each item's card (photos, sizes, price, Order or Book). Opening an item shows it as their storefront does. |
| **Order** | Their items in a bag with steppers, as the Order sheet today; with their price list where they share one, with the storefront price where they don't. Review, Place order, Ask for a quote, Make an offer, as today. |
| **Book** | Their services, a day and a time from their availability, as their storefront's booking. Shown only when they offer services. |
| **Orders** | The orders between us, both ways, each with its steps and what can be done now (accept, confirm, not all came, reorder). "2 to answer" when they wait on us. |
| **Invoices and bills** | The invoices between us, both ways: what they invoiced us (pay, question, paid outside Taskiem) and what we invoiced them (remind, record a payment). |
| **Statement** | What is owed each way, the credit terms and limit, as the statement page has it for this business. |
| **Invoice them** | Ours, about them: a new invoice to this business, its name and contact filled in. |
| **Share my prices** | Ours, about them: which of our price lists they get, or a new one for them. |
| **Make an offer** | Ours, about them: an offer on an order or on items, as a deal today. |

The panel shows nothing else of ours. A tool whose rows would be empty still
shows with what it would hold ("No orders with John's Superstore yet"), except
Book, which shows only when they offer services.

**Who sees what.** The same permissions as today decide each tool: someone who
may not order sees Their shop without Order; a cashier sees what their role's
tools allow, scoped to this business. Staff get the same business-scoped panel,
narrowed by their role.

## 4. "/", "#" and "@" in a business's conversation

- **"/" alone** opens this panel, not the Taskiem panel.
- **"/order", "/invoice", "/bill", "/booking"** pick among the ones between us
  (as "/bill" does today).
- **"#"** tags one of their items, with their price for us (today: only from a
  shared price list; now from their storefront too).
- **"@"** offers their products and services together (as today), and
  businesses as today.
- Our own items are tagged in a business's conversation only from **Share my
  prices** and **Make an offer**, which say what they share.

## 5. What does not change

- The Taskiem conversation: the full panel of our own tools, alerts, the
  briefing, approvals.
- Cards in the conversation (orders, invoices, items, deals) and their buttons.
- WhatsApp, Telegram and Slack: the business-scoped panel is the web's and the
  app's. On a phone, "tools" in a business's conversation is unchanged in this
  phase.
- Explore and linking: until two businesses are linked, neither sees the
  other's shop in a conversation (there is none); Explore shows only what a
  business chose to show there.

## 6. Build outline (for the plan, not decided)

1. Server: one read of a linked business's shop for the other side, from the
   storefront's public copy of the catalogue and services, with the price for
   us from a shared list where there is one. Linked only; nothing private.
2. Ordering at the storefront price from the conversation where no list is
   shared, as a trade order sent straight to them: they accept it or change
   it, as with any order today (the order's lines say which price they used).
3. Booking from the conversation, through the storefront's booking.
4. The panel, "/", "#" and "@" switched by who the conversation is with.
5. Tests: two businesses, one sharing a price list and one not; each side's
   panel shows the other's shop and the dealings between them, and nothing of
   either's other customers; staff see their role's part.

## 7. Decisions

1. **A storefront that is not published** (draft, closed, or never set up):
   linked businesses see its products and services anyway, those marked to
   show on the storefront.
2. **Ordering without a shared price list**: the order goes straight to them
   at the storefront prices; they accept it or change it, as today.
3. **Stock**: whatever the storefront shows, counts included ("3 left"), is
   public, and the conversation shows it too.
