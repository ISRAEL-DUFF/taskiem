# Chat tools: the chat as the business's control room

Status: **phases 1 and 2 built** (agreed with the founder). The plan, with every
decision and the six-phase build order, is the shared doc "Taskiem chat as the
business's control room"; the designs are the canvas "Taskiem chat: picker,
cards and tool sheets". This file is what is built, and it amends
[direct-messages.md](direct-messages.md) and [business-links.md](business-links.md) §5.5.

Everything here sits behind one switch per business, **chat tools**, off until an
owner or admin turns it on (`PUT /v1/settings/chat-tools`, `member.manage`, audited
as `settings.chat_tools`). Off, every channel works exactly as before.

## 1. Who a message goes to

**Recipients decide; things never do.** A recipient is a linked business, or
`@Taskiem`. Products, orders, invoices and the like can be named in a message, and
naming them never changes who gets it. Only businesses are recipients for now, not
people.

| Where it is typed | Names no one | Names linked businesses | Has `@Taskiem` |
|---|---|---|---|
| WhatsApp, Telegram, Slack, the web's Taskiem conversation | Taskiem | Those businesses, straight (no command, no model) | Taskiem |
| A conversation with a business on the web | That business | That business, and each other one named gets its own copy | Taskiem, in the person's own Taskiem conversation; no business gets it |

- **`@Taskiem`** is a word in any case, at the start or after a space or a mark;
  it is taken off (with a comma or colon after it) before Taskiem reads the
  message. A message that is only `@Taskiem` is "hi".
- **What a business receives** is the same as with the `...` marker: the mentions
  at the start left out, the rest as typed without the `@`
  ([direct-messages.md](direct-messages.md) §4).
- **A word after `@` that is no linked business** (an amount, a product, a typo)
  is left alone, and the message is Taskiem's. A mention that could be two
  businesses is asked about, and nothing is sent.
- **While Taskiem is asking something** (a pending question), the message is its
  answer, mentions or not.
- **Photos and files from the phone** follow the caption: a caption naming linked
  businesses sends the file to them, scanned first, each side keeping its own copy.
- **`...` is retired quietly.** It still works, and nothing tells people about it
  any more once chat tools are on.
- **Copies from a business conversation** (`also` in the answer to
  `POST /v1/link-chats/{link}/messages`): one per other business named, each
  answered with whether it was sent and why not. One failing does not stop the
  rest. There are no group conversations.
- **Permission:** sending to a business needs `chat.trade`, as before. Someone
  without it is told so, and nothing is sent.

**`@Taskiem` in a business conversation, phase 1.** There is no message kind that
only the sender sees inside a business conversation yet, so Taskiem's answer goes
to the sender's own Taskiem conversation and comes back in the answer (`202`, the
`TaskiemChat` messages). The web shows it beside the conversation. A message kind
seen by one side only comes in a later phase.

## 2. The look and motion (phase 1, for all of Taskiem)

The designs win, everywhere, the conversation included: the chat is redrawn in
Taskiem's own look rather than WhatsApp's, keeping WhatsApp's behaviours (ticks,
swipe to reply, long-press, hold to record, search, pin, mute, archive).

- **Motion** (`web/src/motion.css`): one token per moment, from the Motion
  artboard: `--t-picker` 280 ms, `--t-sheet` 360 ms, `--t-land` 400 ms with a
  1.4 s glow, `--t-roll` 450 ms, `--t-arrive` 600 ms, 40 ms between rows; the
  curves `--spring`, `--spring-settle`, `--spring-bounce`. App-wide: presses give
  to 97%, dialogs grow (and rise as sheets on a phone, pulled down to close),
  toasts and newly arrived cards come in with a bounce, lists rise one after
  another (`.tk-stagger`), a count rolls into place (`.tk-roll`), and things
  leave the way they came (a dialog shrinks or drops, the picker and the tools
  sink away, a sheet slides back). With motion turned down nothing moves or
  fades: things are simply there (the designs said 150 ms fades; turned off so
  that what is checked is never half-faded).
- **The kit** (`web/src/kit.tsx`, `kit.css`): the new look as shared parts, used
  first by the chat, the rest of the app moving onto them page by page:
  `Sheet`, `Pill`, `KitEmpty`, `KitLoading`, `KitField`, `DiaryLine`, buttons
  (`.tk-btn` primary, soft, ghost), cards (`.tk-card`), rows (`.tk-row`),
  `usePresence` (leaving with motion) and `useMediaQuery`.
- **The chat** (`web/src/chat.css`): bubbles with soft corners (one sharp where a
  tail was), the business's initials in pine and lime, a footer with the tools
  button, the pill (emoji, clip, camera) and the round send or microphone.

## 3. The parts (phase 1)

All of them show only with chat tools on; the redrawn chat is for everyone.

- **The picker.** In a business conversation `@` tags things and businesses in
  groups: Products (the conversation's price list, with price), Services (from
  phase 2), Businesses (linked, with how we trade: "Linked · buys from you"; not
  the one you are talking to; only for those who may write to businesses), and
  the Taskiem row, always last. It floats over the chat. The first match takes
  Enter (the Taskiem row only when "Taskiem" is being typed, so "@4500" stays as
  typed); arrows move; Esc closes. With chat tools on the composer has no emoji
  button, as in the designs: keyboards have emoji. An item tagged with
  `@` is the same mention as `#` (the server accepts both). The Taskiem
  conversation keeps `@` for businesses and `#` for items.
- **The tools.** The grid button beside the composer, or `/` and a word as the
  whole message (`/pro`), opens the tools: Products, Services, Stock, Price
  lists, Orders and Tables, each with its count or a badge ("3 low", "4 new"),
  only those the person's role may open, and the two used last. Arrows move
  between tiles; Esc or a tap outside closes it. "/" and a word that names no
  tool is an ordinary message: Enter sends it. "/pro" is never kept as a draft,
  and neither it nor "@Taskiem …" tells the other business we are typing.
- **The sheet.** A tool opens in one frame on every screen, growing out of the
  tile that opened it (the same shape expands; its content fades in once it has
  room): up from the bottom on a phone (pulled down to close), a drawer from the
  right on a tablet, a panel beside the conversation on a computer, where the
  chat makes room and stays usable (no veil, focus is not held). A list that
  cannot be loaded says so, with Try again. It has the tool's
  name and count, a search, chips ("Low stock · 2"), the list and the full page.
  In phase 1 the lists are read-only and open the full page; phase 2 makes them
  work in place. Esc closes the sheet and only the sheet.
- **The card frame.** A document in the conversation is a card: its kind and
  reference, its state as a pill, what happened, the amount, an order's four
  steps (Placed, Accepted, On the way, Delivered), View order or View invoice,
  and its next step as a filled button. A card that arrives while the
  conversation is open slides in.
- **Diary lines.** A change we made ourselves (accepting an order, saying an
  invoice is paid) is a line in the middle of the chat, not a card: what changed,
  and "You, 10:42". One that arrives while the conversation is open drops in lit
  in lime; those already there just show. Changes made within five minutes of
  each other on one day are one line, the newest, with "and 2 earlier
  changes", which unfolds. A
  new document is always a card. Search results and quotes jump to a diary line
  as to any message.

## 4. Phase 2: the seller's tools

Status: **built**. Done when an owner changes a price, records stock in and
accepts an order from chat, a service business adds a service with its duration
and price, and each change shows as a diary line (`web/e2e/chat-tools-work.spec.ts`).

- **The sheets work.** Each tool does the common task in place, through the same
  API as its page; "Open full page" is for the rest.
  - Products: the price and the count on the shelf (a count movement); a new
    product.
  - Stock: goods that came in (a received movement).
  - Services: a new service with how long it takes and its price (a row of the
    storefront's services table, then the catalogue refreshed).
  - Price lists: each item's price.
  - Orders: what a linked business asks for, Accept or Decline (confirmed).
  - Tables: any data table's latest rows, and a row added.
- **Saving.** The tick draws itself (260 ms, a light buzz) and the sheet closes
  at 1.3 s. Someone who may not change a tool's thing sees it, with no Save
  (products, stock and tables need `table.edit`; services `table.edit` and
  `storefront.manage`; prices and orders `storefront.manage`). Esc steps back from
  a form to its list, then closes. The product form has its photos (add one),
  its price, its count (sent only when changed: an item may have none) and "Show
  on your storefront" (`available` on `PATCH /v1/stock/items/{id}`). Services
  can be changed and removed; a table's rows changed and added. An order shows
  its four steps.
- **Changes told, from anywhere** (`api/chat_changes.go`). With chat tools on, a
  change a person makes on any page or tool is a diary line in their own
  Taskiem conversation, written by the server: a price, showing or hiding, a new
  product, stock in, a count, a price list, an order's step, a service added or
  changed, and a table row added or changed from a chat tool (not imports or
  page edits of other tables). The line's new value rolls into place. It is told
  on the person's own WhatsApp, Telegram or Slack as their "changes" setting says
  (Settings, Notifications: on by default, never by email); changes made within a
  minute are told in one message ("✓ 3 changes: …"). From a business's
  conversation a tool says the change is in the Taskiem chat; the business
  conversation keeps its own cards (an accepted order), on our side as lines.
  `POST /v1/chat-diary` stays for a client that tells a change itself (a line on
  our side of a business's conversation, or in the Taskiem conversation).
- **Live cards.** A product tagged in a message is a card with what is true now:
  today's price on the conversation's price list (and what it was when tagged),
  our own stock of it (to those who may see stock), Order when we buy from them,
  and its photo when it is our own product. A service of our own (from the
  picker's Services; `{kind: service, service_id}`, checked against our
  storefront's services) shows its price and minutes, on both sides.
- **Typed requests** (`web/src/lib/intents.ts`, `api/chat_tool_requests.go`):
  "change the price of rice 50kg to 80k", "20 bags of rice 25kg came in", "add a
  service: rice milling, 30 min, 2500", "accept order AD-…" offer a chip over the
  composer that opens the form filled in. Patterns read the common wordings in
  the browser; what they do not know, the model reads once typing pauses
  (`POST /v1/chat-tools/request`, metered against the AI budget like a chat
  question). Words never save anything: the form's Save does. A thing is never
  guessed between two that fit as well.
- **On WhatsApp, Telegram and Slack.** "tools" (or "/tools") answers with a link
  to each tool the person may open; "/pro" with Products'. A typed request no
  command claims is read by the model and answered with a link to its form,
  filled in ("Change the price of Rice, 50kg bag to ₦80,000? Open the form,
  filled in, and Save there: …"). The link opens the Taskiem conversation on the
  web with the tool at that form (`/chat?with=taskiem&tool=…&row=…&price=…`).
- **Moments.** A soft sound for a new order or payment while the chat is open
  (opened by the first tap, as browsers ask), switched off from the chat menu; a
  short lime ring around a payment or the day's first order. Neither with motion
  turned down.
- **Still to come:** a buyer's live card shows no photo (the price list a seller
  shares carries none yet); requests the model reads offer prices, stock in and
  new services, and "accept order" only from the browser's patterns.

**A product's price list** (the DeskSeller board): for those who keep the
storefront's prices (`storefront.manage`), a product's form shows its
**Storefront price** beside its price on a price list ("Trade price list"):
the list shared with the business whose conversation the sheet was opened
from, else the one holding the product, else the first (a choice of list
when there are more). Under them **Shared with** names the linked businesses
that see the list, and **Change** picks them. Saving writes the product, the
list's price (`PUT /v1/price-lists/{id}/items`, the product added to the list
if it was not on it) and who sees it (`PUT /v1/price-lists/{id}/links`), each
a diary line, as the note under the form says ("Saving posts a line in this
chat and updates the price for every business on the Trade price list").

## 5. Phase 3: the buyer's tools

The same conversation, from the other side: a business buying from a linked
supplier orders, follows its orders and pays its bills where it talks to them.
Everything goes through the APIs the buyer's pages already use (price lists and
item links, purchase orders, bills); the chat adds no new kind of record.

**Where they show.** In a conversation with a business that shares a price list
with us (`/v1/link-chats/{link}/items` answers `whose: theirs`), to someone who
may both draft (`table.edit`) and send (`storefront.manage`) orders:

- an **Order** button in the conversation's head, and **Order** first in the
  tools panel ("From Bayo Rice");
- **Order** on a live card of their item (it opens the sheet with that item in
  the bag; from the phone, or with chat tools off, the prices page as before);
- **View order** on a card of our order with them, **View invoice** and **Pay
  ₦X** on a card of their invoice: each opens it in a sheet beside the chat.

Two more tools in every panel: **Your orders** (`table.read`, the orders sent to
suppliers, "3 to confirm" when they wait on us) and **Bills** (`invoice.read`,
"2 to pay"). In a business's conversation both list only what is with that
business, and Your orders ends with **New order**; an order to a supplier not on
Taskiem shows in no conversation. Order cards and bill rows show an icon, and an
order or bill is titled by its number.

**Order** (the Buyer design). Their price list as rows, each with a stepper:
the first tap takes their least ("min 2"), then one at a time, down to none; an
item not available now cannot be added. **Same as last time** fills the bag
with the last order to them at today's prices. A pine bar floats over the list with
how many items and the total, and **Review order**. Reviewing first asks, when they are not yet one of our suppliers, which of
ours they are or for their phone number to make them one; then shows the lines
and the total; says once which of our items each of theirs is (the same name is
found first, or a new item of ours), which is remembered as the item link; a
note for them. What still stops the order is said above the buttons. **Place
order** links the supplier, drafts the order at their price for us and sends
it; a retry after a failure part way repeats nothing already done (each step
keeps its key). Above the business's purchase limit the order stays drafted for
someone else to send, and the sheet says so. **Ask for a quote** (for those who
may write to businesses) sends the lines to them as a message instead. The
tick, then the sheet goes.

**Your orders.** An order's steps (Placed, Accepted, On the way, Delivered), its
lines and what they said (their change with what it comes to now); what we can
do now, each as the person's permission allows: confirm their quote (saying
which of our items each line is), accept their change, say all came or how many
of each did (**Not all came**), cancel or decline; and **Reorder**, which opens Order with the last
order's lines at today's prices (their least where it went up, and nothing
they no longer offer).

**Bills.** A pine hero with what is left to pay and how much is paid, the lines,
the account to transfer to with the bill's number; **Pay ₦X** opens the
supplier's own checkout (their invoice page, a card or a transfer) in a new
tab, and **Pay from Taskiem** where the business pays bills by a workflow;
**Question it** and **I paid outside Taskiem**, each telling the supplier, with
**Not now** back to the bill.

**Diary lines** for the buyer's steps, kept and told like the seller's (§4):
"Sent order PO-0042 to Bayo Rice: ₦234,000", "Confirmed Bayo Rice's quote for
order PO-0042", "All of order PO-0042 came from Bayo Rice", "Started paying
INV-0107 from Taskiem: ₦304,000 to Bayo Rice", "Told Bayo Rice you paid ₦50,000 of
INV-0107 outside Taskiem", "Questioned INV-0107 from Bayo Rice". The seller's
price for one buyer is a line too: "Price of Rice 50kg for Ada's Kitchen:
₦76,000 → ₦78,000". An order sent again writes no second line. Order cards
now carry the order's total on both sides (a change's card, what the change
comes to).

**From the phone.** "tools" lists Your orders and Bills with the rest, and
"/bills" opens Bills on the web.

**Done when** the first slice's trade runs end to end between two businesses:
the seller changes the buyer's price from chat (a diary line); the buyer tags
the item, sees its card, and orders from the price list; the seller sees the
order's card and accepts it; the invoice arrives as the buyer's bill and is paid
on the seller's checkout; both see the order and the invoice in their history.
`api/chat_trade_test.go` runs exactly that.

## 6. Phase 4: alerts and approvals

The Taskiem conversation ends with what needs the person now, as cards (the
Alerts design): each says what it is and has its next step, which acts in
place or opens the right tool, filled in. The cards are read when the person
looks (`GET /v1/chat-alerts`, again every minute while the conversation is
open and in view, at once when the person comes back to the tab), so one is
gone as soon as it is dealt with, here or anywhere else; they are not messages
kept in the history. At most five of a kind and twenty in all, approvals never
left out. A new order, money in or an approval arriving makes the sound the
owner can turn off, and the conversation list's Taskiem row says how many
things need them. Each kind is only for those whose role may see it:

| Card | Who sees it | Its next step |
|---|---|---|
| Low stock: "Rice, 25kg bag is running low", "4 bags left · you reorder at 10"; none while the item is on an open order | `table.read` | **Reorder 20 from Bayo Rice Mills** when a linked supplier sells it (the item link) and the person may order: the Order sheet with that many in the bag (up to the item's target level, else twice its reorder level, at least the supplier's least). Otherwise **Record stock in** (the Stock tool at that item), or **Open stock** |
| An overdue invoice (not the debtor notebook's entries) | `invoice.read` | **Send reminder** (`invoice.manage`): the invoice's link sent again, and the card put off for a day |
| A new order from a linked business | `storefront.manage` | **Review order**: the Orders tool at that order, its lines first, Accept there |
| A delivery order ready to go (paid, or paid on delivery) that no rider has been asked to take, when the business has riders and deliveries | `storefront.manage` | **Assign rider**: the riders on duty, or the next in turn; an offer nobody could take says so and the card stays |
| A payment not matched yet | `invoice.read` | **Match it** (`invoice.manage`): every open invoice and order (`GET /v1/chat-alerts/match-options`), the same amount first, found by number, amount or name; a match held for a second person says so |
| An approval waiting for the person ("NEEDS YOUR OK", who asked, the amount and payee) | whoever may decide it | **Decline** or **Approve** |

**Later** puts a card off for its person alone, for four hours
(`POST /v1/chat-alerts/later`, kept in `chat_alerts_later`). A card dealt with
leaves with a little motion, and the conversation pulls in its diary line.

**Approvals** are confirmed in a sheet: what it is and who asked, the fields of
its subject, a recorded comment, and the confirmation its rules ask for, a
passkey for this decision on this approval, or an authenticator code (the chat
PIN on WhatsApp, as before). The server refuses an approval without it, so a
card cannot pass one on a tap. Decline is confirmed the same way, with the same
confirmation, and the sheet asks for whatever the server says it needs. The card
and sheet say a first payment, the level of several and the approvals counted.

Each action is a diary line, wherever it is taken: "Approved Pay Bayo Rice
Mills (Pay supplier bills)", "Declined …", "Sent invoice INV-0042", "Sent a
reminder for invoice INV-0042", "Matched a payment to invoice INV-0009",
"Offered order ADA-1234 to Musa Bello".

Cards are white with a pine next step and a soft Later; the approval's Approve
is lime with the fingerprint, as drawn. On a computer they stay message-width.

## 7. Phase 5: staff and the briefing

**Staff use chat as their app.** Two built-in roles for those who work the
counter and the shift, and a permission for working orders without the
storefront's settings:

| Role | Permissions | Their tools |
|---|---|---|
| `cashier` | `table.read`, `table.edit`, `order.handle`, `invoice.read` | Sell, Stock, Orders |
| `shift_manager` | the cashier's, and `run.read`, `invoice.manage`, `chat.trade` | Handover, Sell, Stock, Orders, Bills |

`order.handle` is working the orders: seeing them (`GET /v1/trade-orders`,
`/v1/storefront/orders`), accepting, declining, readying and sending them out
(`POST /v1/trade-orders/{id}/{step}`), the day's deliveries, and offering an
order to a rider (`/offer`, `/release`, `GET /v1/riders`). It is not the
storefront's settings, prices, refunds, riders' pay or purchasing, which stay
`storefront.manage`. Routes and chat take either (`storefront.manage|order.handle`
in the OpenAPI document), and those holding either are told of a new order.

Its limits, on the server (`orderStepAllowed`, `cashierView`):

- Of a linked business's order, only accept, decline, ready, dispatched and
  delivered; not changing it (`change`), and accepting only an order priced
  from the seller's own prices (a buyer-priced one waits for the owner).
- A customer order's own approval steps (accept or decline, done, done and
  paid, in the storefront-order workflow) are decided by anyone with
  `order.handle`, as the owner would, on the web, in the bell and on
  WhatsApp; its refund step and every other workflow's approvals are not.
- Riders and deliveries are seen with the rider's number to its last four
  digits and no fee or bank; an offer sets no fee (`fee_kobo` is refused).
- A business's own role made before these were built in, named `cashier` or
  `shift_manager`, keeps its own permissions and is listed once.

A person whose roles are all staff roles sees their role's tools only, in the
panel and in "tools" on WhatsApp and Telegram; anyone else sees every tool their
permissions allow. **Sell** is a counter sale as the Order sheet looks: what
has a price, with steppers, the pine bag, then how it was paid (cash,
transfer, POS, arrow keys moving between them); recorded as the Stock page's
sales are (`POST /v1/stock/sales`, one key per sale: a retry of the same sale
records it once, a changed one is new), with one diary line ("Sold 2 Rice, 1
Garri: ₦188,000 (POS)"), none for a replay. More than the stock holds is said
on the row ("Only 3 left in your stock"), not refused.
On WhatsApp a cashier sells as before ("sold 2 bags of rice 156,000 cash"),
works the orders ("orders", "order ADA-1234 ready") and their buttons, and is
told of new orders.

**Handover** (a shift manager's): the shift so far (`GET /v1/chat-handover`,
today in Lagos): sold and how it was paid, the orders still open, deliveries
on the way and done, what is low; the cash counted against the cash sold
("₦6,000 short"), and a note (300 characters at most). Handing over (`POST
/v1/chat-handover`, `invoice.manage` or `storefront.manage`, chat tools on,
three in ten minutes at most) tells those who run the business, not the one
handing over, in the bell and on their chat ("Sam handed the shift over. Sold
₦234,000 in 3 sales…", at most five low items then "and N more"), with a
diary line for the manager. The open orders count the storefront's too.

**A rider** works the shift on WhatsApp or Telegram as before (Accept, Can't,
Picked up, Arrived, Delivered, Cash, the proof photo and location, the drop's
map pin), and now asks "my deliveries" (or "jobs", "today"): each open
delivery, where it goes and its pin once it is theirs, and the word to send
next. Delivered ones are left out.

**The morning briefing** (the Alerts design): from 8am Lagos, for those who run
the business (`storefront.manage` or `invoice.manage`), a pine card leads the
end of the Taskiem conversation (`GET /v1/chat-briefing`): "Good morning, Ada",
yesterday's sales against the same day a week before ("Up 12% on last
Wednesday") with the week's bars, and tiles for what is owed (the invoices
page), low stock (the Stock tool), today's deliveries and bookings, each only
for those who may see it. Tapping the sales asks "how did we do yesterday";
**Got it** puts the card away for the day (if that fails the card stays and
says so). The card is read again when the page comes back and every ten
minutes, so it appears at 8am and a new day's replaces yesterday's. On the
phone the owner keeps the 8am money summary, now with today's deliveries
and bookings ("Today: 4 deliveries, 1 bookings.") and, with chat tools on,
the link to the morning card in the Taskiem conversation.

**"How did we do?"** ("how did we do this week?", "today", "yesterday", "last
week", "this month", "sales", and "@Taskiem how did we do?" from any
conversation): the numbers in words on every channel ("Sales, the last 7 days:
₦2,966,500, up 8% on the week before", each day's figure), and in the Taskiem
conversation a chart card (a message of kind `chart`, migration 00406): the
total, the change, and the period's bars, the day asked about lit.

## 8. Phase 6: Explore and links

**Tagging a business you are not linked with.** In a conversation with chat
tools (a business's or the Taskiem one), "@" with two letters or more also
asks Explore (`GET /v1/explore?q=`): businesses that chose to be found and
match the words join the picker's Businesses group after the linked ones,
three at most, each "Not linked · Yaba" (or "you asked to link", "asked you
to link"; linked and blocked ones are not offered there). Picking one tags
nothing: the words stay as typed and its Explore card opens in a sheet over
the chat, its logo, name, line and area, its photos with their captions and
dots, and what it says about itself, with **Connect** (`POST /v1/links`, both
ways, as Explore). Connect says "Asked Kola's Bakery to link" and the card
says Requested, with **Withdraw**; nothing is shared until they accept. A
business that asked us is answered on its card (**Accept**, **Not now**), in
the sheet and in Explore; once linked, **Message** opens its conversation.
The picker's row follows what was done in the sheet. Someone who may not link
sees the card with "Not linked" and "An owner or admin can connect." Asking,
taking an ask back and each answer are diary lines ("Asked Kola's Bakery to
link", "Linked with Kola's Bakery"), named as the link list names the
business. The
Taskiem conversation gets the grouped picker with chat tools (without the
Taskiem row: it is who is being talked to).

**Explore and Your business in Explore** take the new look (designs 10 to
12): on a phone, the back arrow and "Find a business", the search ("A name,
what they sell, or an area"), "Near you · Yaba, Lagos" (our own area, `area`
on the first page) and "New on Taskiem" with Show more beside each; each card
is the name and line with Connect first, then the photos (captions on them,
`captions` on each card) and their dots, then the words. On a computer the
search sits beside the title with "Your business in Explore" (our logo) at
the end, and the cards are a grid that lifts under the pointer. The profile
page is the switch "Be found in Explore" (pine when on), the card as others
see it (Connect, and the photos as a mosaic with "+3"), the logo with its
"+", the fields as the designs draw them with their counts, and the photos
two by two, the first marked, each with remove, earlier and later, its
caption under it, and "Add a photo".

**Keys.** On the Chat page: "/" finds a business or a message, "n" opens
Explore, "@" starts a tag in the open chat's box (the picker opens as if it
were typed). The letters stay out of a sheet, the tools, the picker and
Explore while one is open; in Explore they are typed into its search.

## 9. Tests

- `api/chat_tools_test.go`: the switch (off by default, a viewer refused, on for
  everyone, off again); on, names straight to businesses with and without `...`,
  two at once, `@Taskiem` anywhere, naming no one, a non-business `@`, an
  ambiguous name, a viewer, the web's Taskiem conversation, `202` in a business
  conversation with nothing sent, `also` copies; photos by caption.
- `api/chat_tools_internal_test.go`: cutting `@Taskiem`.
- `api/authz_test.go`: a key limited to one environment cannot flip the switch.
- `api/mentions_test.go`: an item written `@Cement` is the same mention as `#Cement`.
- `web/e2e/chat-tools.spec.ts`: the switch; `@Taskiem` in a business
  conversation; the picker, the tools and a sheet on a phone and a computer
  (axe in both themes, nothing scrolling sideways, Esc closing only the sheet);
  a card with its progress and a diary line.
- `api/chat_diary_test.go`: a diary line in each place, the other business never
  seeing it, refused reactions and replies, told once on WhatsApp, who may.
- `api/chat_diary_test.go` (`TestChangesTold`): changes made on the pages told
  as lines, together on WhatsApp in one message, not at all with the setting off
  or chat tools off.
- `api/chat_tool_requests_test.go`: "tools" and "/pro" on WhatsApp; a request read
  by the model from the web and from WhatsApp, as links to the filled-in form; no
  guess between two products; a question is no request; nothing saved.
- `api/chat_tools_test.go` (`TestServiceMention`): a service of ours tagged, on
  both sides; one not ours refused.
- `web/e2e/chat-tools-work.spec.ts`: price, stock in, order and service from
  chat on a phone and a computer, each a diary line, ours alone; the Taskiem
  conversation's diary line kept; a typed request; a live card; tagging a
  service; the sound switch.
- Unit tests: `lib/toolforms`, `lib/intents`, `lib/moments`.
- `api/explore_test.go`: captions on each card and on search results; our own
  area, from the profile or else the published storefront; link diary lines
  on each side (`TestLinkDiaryLines`).
- `web/e2e/explore.spec.ts`: tagging a business not linked shows its card and
  Connect asks it to link, Withdraw takes it back (axe in both themes); one
  that asked us accepted from its card, the keys staying out of the sheet,
  Message opening the conversation and the diary line; the "@" key; the keys
  stay out of Explore. `web/e2e/chat-staff.spec.ts`: a cashier's card has no
  Connect. `web/e2e/business-profile.spec.ts`:
  the redrawn profile.
- Unit tests: `lib/mentions` (businesses found in Explore), `lib/chatKeys` ("@").
- Unit tests: `lib/tools`, `lib/cardframe`, `lib/mentions` (the groups),
  `lib/sheetDrag`, `lib/chattools`.
- `api/chat_trade_test.go`: the five-step trade between two businesses (§5).
- `api/chat_tool_requests_test.go`: Your orders and Bills in "tools", and "/bills".
- `api/chat_tools_internal_test.go`: the buyer's diary words.
- `web/e2e/chat-buyer.spec.ts`: on a phone and a computer, Order from the
  conversation's head (steppers, their least, the bag, review, our item found by
  its name, the note, placed and sent), Ask for a quote, a bill paid on their
  checkout and questioned, and Reorder from Your orders; axe in both themes;
  a quote confirmed with our items, Not all came, Not now; a supplier added and
  linked once with a retry that repeats nothing, Same as last time; no Order
  without leave to send orders; a card opening its bill or order.
- Unit tests: `lib/buyer` (steppers, the bag, the quote, an order's state, reorder).
- `api/chat_alerts_test.go`: each kind of alert for the right people only (low
  stock with and without Reorder, none while on order, a new order for the
  seller alone, Later for one person, accepted and gone, overdue and its
  reminder, unmatched and its match options, an approval with who asked, none
  for a viewer, approved and gone); a rider alert only when ready, gone once
  offered; each action's diary line.
- `web/e2e/chat-alerts.spec.ts`: on a phone and a computer, every card's
  button: Reorder opens the Order sheet filled in, Record stock in the Stock
  form, Send reminder (put off a day), Review order and Accept there, Assign
  rider, Match it (invoices and orders), Approve with a comment; Later; Decline
  confirmed, with its code; an approval needing a code cannot pass without six
  digits; an offer nobody took; levels and approvals; the Taskiem row's count;
  nothing sideways at 360 px; none with chat tools off; axe in both themes.
- `api/chat_alerts_internal_test.go`: the caps per kind, who asked.
- Unit tests: `lib/alerts`.
- `api/chat_staff_test.go`: a cashier's shift from chat (a sale on WhatsApp and
  from Sell, their tools, refused the storefront's work, told of a new order,
  its card, accepting it); a shift manager's handover and the owner told; the
  briefing before and after 8am, not for a cashier, Got it; "how did we do"
  in words and as a chart; a rider's shift through "my deliveries".
- `web/e2e/chat-staff.spec.ts`: a cashier's tools and Sell; the handover with
  the cash counted; the briefing and a chart card on a phone and a computer,
  Low stock opening Stock, Got it; none for a cashier; axe in both themes.
- Unit tests: `lib/staff` (the sale, the change and the bars, the greeting,
  the cash), `lib/tools` (role-shaped tools), `ChatTools` (`order.handle`).
