# Chat tools: the chat as the business's control room

Status: **phase 1 built** (agreed with the founder). The plan, with every
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
  toasts arrive with a bounce, lists rise one after another (`.tk-stagger`).
  With motion turned down nothing moves: things are simply there.
- **The chat** (`web/src/chat.css`): bubbles with soft corners (one sharp where a
  tail was), the business's initials in pine and lime, a footer with the tools
  button, the pill (emoji, clip, camera) and the round send or microphone.

## 3. The parts (phase 1)

All of them show only with chat tools on; the redrawn chat is for everyone.

- **The picker.** In a business conversation `@` tags things and businesses in
  groups: Products (the conversation's price list, with price), Services (from
  phase 2), Businesses (linked; not the one you are talking to), and the Taskiem
  row. The first match takes Enter; arrows move; Esc closes. An item tagged with
  `@` is the same mention as `#` (the server accepts both). The Taskiem
  conversation keeps `@` for businesses and `#` for items.
- **The tools.** The grid button beside the composer, or `/` and a word as the
  whole message (`/pro`), opens the tools: Products, Services, Stock, Price
  lists, Orders and Tables, each with its count or a badge ("3 low", "4 new"),
  only those the person's role may open, and the two used last.
- **The sheet.** A tool opens in one frame on every screen: up from the bottom on
  a phone (pulled down to close), a drawer from the right on a tablet, a panel
  beside the conversation on a computer (the chat makes room). It has the tool's
  name and count, a search, chips ("Low stock · 2"), the list and the full page.
  In phase 1 the lists are read-only and open the full page; phase 2 makes them
  work in place. Esc closes the sheet and only the sheet.
- **The card frame.** A document in the conversation is a card: its kind and
  reference, its state as a pill, what happened, the amount, an order's four
  steps (Placed, Accepted, On the way, Delivered) and its buttons.
- **Diary lines.** A change we made ourselves (accepting an order, saying an
  invoice is paid) is a line in the middle of the chat, not a card: what changed,
  and "You, 10:42". It drops in lit in lime. A new document is always a card.

## 4. Tests

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
- Unit tests: `lib/tools`, `lib/cardframe`, `lib/mentions` (the groups),
  `lib/sheetDrag`, `lib/chattools`.
