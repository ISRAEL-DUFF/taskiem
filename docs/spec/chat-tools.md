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

## 5. Tests

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
- Unit tests: `lib/tools`, `lib/cardframe`, `lib/mentions` (the groups),
  `lib/sheetDrag`, `lib/chattools`.
