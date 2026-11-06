# Chat tools: the chat as the business's control room

Status: **phase 1 in progress** (agreed with the founder). The plan, with every
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
swipe to reply, long-press, hold to record, search, pin, mute, archive). The look
and motion apply app-wide: tokens, timings and springs, reduced motion respected,
and shared parts (buttons, cards, the sheet/panel/drawer frame, toasts, empty
states, shimmer). Bold and rich, made for good phones.

## 3. Phase 1 parts still to come

- The picker for recipients and things, grouped and limited by role.
- The tools button and `/` in the composer, beside the clip, camera and microphone.
- The card frame and the sheet frame (phone, tablet and desktop), and diary lines.

## 4. Tests

- `api/chat_tools_test.go`: the switch (off by default, a viewer refused, on for
  everyone, off again); on, names straight to businesses with and without `...`,
  two at once, `@Taskiem` anywhere, naming no one, a non-business `@`, an
  ambiguous name, a viewer, the web's Taskiem conversation, `202` in a business
  conversation with nothing sent, `also` copies; photos by caption.
- `api/chat_tools_internal_test.go`: cutting `@Taskiem`.
- `api/authz_test.go`: a key limited to one environment cannot flip the switch.
