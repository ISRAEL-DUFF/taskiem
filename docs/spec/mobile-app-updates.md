# Phone app: the Updates tab (draft for discussion)

The home's bottom bar has Chats, **Updates**, Money and Orders. Chats,
Money and Orders are built; Updates still says it is coming, with the words
"What happened in your business, as it happens." This is the plan for it,
written 12 November 2026 after M8, to be settled with the founder before it
is built.

## What exists to build it from

| Source | What it holds | Who sees it | Endpoint |
| --- | --- | --- | --- |
| **Notifications** (the web's bell) | Mostly the automation's: a run that failed, needs a check or finished; a connection or key problem; an alert rule's finding; an approval waiting; a few business ones (a bill, a link, a staff task, a payment reversed, a school's cash). Repeats are one item with a count; kept 90 days | Each person their own | `GET /v1/notifications` (newest first, `?unread=true`, `?before=`), `GET /v1/notifications/count` (unread, and approvals waiting), `POST /v1/notifications/read` (`ids` or `all`) |
| **Taskiem's cards** (in Taskiem's conversation) | What needs a person now, each with its one step: money to match, an overdue invoice to remind, stock to reorder, an approval, an order to accept, a rider to assign, a customer's order draft | By permission | `GET /v1/chat-alerts`, `POST /v1/chat-alerts/later` |
| **The briefing** (in Taskiem's conversation) | Today: sales and how they compare, owed, low stock, deliveries, staff, tasks, bookings | Owners and managers | `GET /v1/chat-briefing` |
| **Money** | Every movement of money, by period | `invoice.read` | `GET /v1/money/summary`, the money log |
| **The audit log** | Who did what, everything | `audit.read` | `GET /v1/audit` |
| **The chat's live stream** | New messages, as they come | Each person | `GET /v1/chat/stream` |

What is **missing**: a feed of what happened in the business, in its own
words, as it happens: "₦25,000 came in from Ada for INV-0042", "Order AK-7QX
placed on your storefront", "Musa delivered PO-9 to Rivers Foods", "Bisi
counted the shelf: 2 bags short", "Tunde added 20 bags of rice". The pieces
are in the money log, the orders, the deliveries, the stock moves and the
chat tools' diary lines, but nothing gathers them into one list.

## A first shape

Updates as WhatsApp's: a few things at the top that need you, then what
happened, newest first.

| # | Part | What it shows | Server |
| --- | --- | --- | --- |
| U1 | **Needs you** | The approvals waiting and Taskiem's cards, each with its one step (the same cards and sheets as Taskiem's conversation) | Exists |
| U2 | **Today** | The day's figures from the briefing (sales, owed, low stock, deliveries), each leading to its screen | Exists |
| U3 | **What happened** | The business's events, newest first, grouped by day, each opening its screen (the invoice, the order, the delivery, the count); read again live | **New**: a feed endpoint |
| U4 | **For you** | The person's notifications (the bell), read and marked read, each opening its screen in the app or the web for the automation's | Exists |
| U5 | **The tab's count** | Unread updates and approvals waiting, on the Updates tab as Chats has its unread count | Exists (`/count`); more with U3 |
| U6 | **What reaches you** | The notification settings (which groups come by chat or email) | Exists |
| U7 | **On the lock screen** | An update that needs you, pushed when the app is closed | With M4 (push) |

## Decisions to make

1. **What happened (U3)**: build the server's business feed (new work: a
   table of events written where each thing happens, or a view over the
   money log, orders, deliveries and stock), or start the tab with what
   exists (U1, U2, U4) and add the feed after?
2. **Who sees which events**: everyone by their permissions (money for
   `invoice.read`, orders for `order.handle`, stock for `table.read`), or
   owners see everything and staff only what they did or were given?
3. **Needs you (U1)**: show Taskiem's cards in Updates as well as in
   Taskiem's conversation (one card, two places; dealt with in one is gone
   from both), or move them to Updates and leave the conversation for
   talking?
4. **The automation's notices** (runs, connections, keys): in Updates for
   those who may see runs, or left on the web, where the workflows are?

## Order of the work (once settled)

U1, U2, U4 and U5 first (no server work), then U3 with its endpoint and the
events it carries, U6, and U7 with M4. Each with its tests, large text and
touch targets, as every milestone; an audit after.
