# Free chat: talking to linked businesses from the Taskiem conversation

Status: **agreed with the founder, not built.** This amends business links L4
([business-links.md](business-links.md) §5.5 and §16.4). Two problems prompted it,
both in the Taskiem conversation on WhatsApp, Telegram, Slack and the web:

1. **Names with spaces.** `@Dangote` does not find **Dangote Nigeria Ltd**. Only the
   whole name matches, so Taskiem answers "You are not linked with Dangote…", which
   is wrong.
2. **No way to just talk.** Every message containing `@` is read as an order.
   `@Dangote how much is a bag of cement?` has no quantity, so Taskiem answers "Say
   how many of what from Dangote…" and nothing reaches Dangote.

## 1. Summary

- **A mention finds a business by the start of its name**, everywhere a linked
  business is named. `@Dangote` finds Dangote Nigeria Ltd. Two businesses that fit
  are asked about; one is never picked in silence.
- **Free chat** is a mode turned on in chat, with no button or indicator:
  - **Sending a lone `@`** turns it on for that person.
  - **While it is on,** a message mentioning a linked business goes straight to
    that business, unread by Taskiem's commands and its AI. A message with no
    mention goes to Taskiem as usual.
  - **It goes off** after 30 minutes in which the person sends nothing to Taskiem,
    or at once when they send `@` again.
- **With free chat off, nothing changes**, apart from the name fix and one added
  hint. `Buy 400 bags of cement from @Dangote` still makes a draft order.

## 2. What stays as it is

- Mention orders (L4): `Buy 400 bags of cement from @Dangote and 20 blocks from
  @Cherry`, with one draft per business and one yes that sends them all.
- `ask Dangote …` and `ask Dangote about INV-0042 …`.
- A quoted reply on the phone to a business's message goes back into that
  conversation, in either mode.
- A linked business's messages reach the phone as today: "[Dangote Nigeria Ltd]
  Musa: …".
- The web's conversation with a linked business (the Chat page) is already direct,
  and is not affected.
- Commands, sales, money, stock, invoices, the AI builder, and voice notes and their
  transcripts.

## 3. Finding a business by its name

### 3.1 The rule

A single matcher in `engine/links` (`links.Named`, or similar) is given the active
links and the words a person typed. It returns one link, several (ambiguous) or
none, plus how many of the typed words it used.

- **Case and punctuation are ignored**, and characters are folded as `parseMentions`
  already folds them: İ, the Kelvin sign, and the like. `Dangote, Nigeria` is the
  same as `dangote nigeria`.
- **The legal suffix is ignored** on both sides: `ltd`, `limited`, `plc`, `inc`,
  `co`, `company`. So `@Dangote Ltd` finds Dangote Nigeria Ltd. (The list is open,
  §9.)
- **Words from the start of the name.** The typed words match when they are the
  first words of the name, in order:
  - `Dangote` and `Dangote Nigeria` both fit **Dangote Nigeria Ltd**.
  - `Nigeria` does not, since it is not how the name starts.
- **One word with the spaces left out** also fits: `@DangoteNigeria` finds Dangote
  Nigeria Ltd.
- **The longest run wins.** After `@`, try the most words that still fit at least
  one business, then fewer. In `@Dangote how much is cement`, only `Dangote` fits,
  so the message is "how much is cement". In `@Dangote Nigeria is late`, `Dangote
  Nigeria` fits, and the message is "is late".
- **An exact whole name always wins** over a partial one. So "Dangote" and "Dangote
  Nigeria Ltd", if both are linked, are not ambiguous when the whole name is typed.
- **Ambiguous:**
  - **When:** the longest run fits more than one business, as with `@Dangote` for
    Dangote Cement and Dangote Sugar.
  - **Then:** Taskiem asks which one and sends nothing.
    - **On WhatsApp and Telegram:** a button for each business, up to three. The
      message is held for 10 minutes and sent when one is tapped.
    - **Elsewhere, or with more than three:** a list of the businesses, and a
      request to send the message again with more of the name.
  - **Two links with the very same name** are asked about the same way, with each
    link's role. This is what `wa.mention.which` already does.
- **None fits:** today's "You are not linked with *Dangote*…" (`wa.mention.unlinked`).

### 3.2 Where it is used

The matcher replaces each place's own matching, so a business is found the same way
everywhere:

| Where | Today | After |
| --- | --- | --- |
| Mentions (`parseMentions`, `api/mentions.go`): orders and free chat | the whole name only | §3.1 |
| `ask Dangote …` (`waAskLinked`, `api/link_chat.go`) | the start of the name, character by character | §3.1 |
| `link@1`'s `business` input (`linkNamed`, `api/link_connector.go`) | the exact name, else the only name containing it | §3.1, then today's "contains" as a last resort, so no existing workflow breaks |
| The web composer's `@` picker (`web/src/lib/mentions.ts`) | inserts the whole name | unchanged; the whole name still matches |

## 4. Free chat

### 4.1 Turning it on and off

- **A lone `@`** turns it on. The message is just `@` once spaces are trimmed; the
  fullwidth `＠` counts too.
  - **The reply:** "Free chat is on. A message with *@business* goes straight to
    that business; messages without one still come to me. It ends after 30 minutes
    without a message, or send *@* again."
  - **When:** in any state, without dropping a question Taskiem is waiting on (a
    draft to confirm, say). That question is still answered by the person's next
    message without a mention.
- **A lone `@` while it is on** turns it off: "Free chat is off; @ messages are read
  as before."
- **Inactivity: 30 minutes in which the person sends nothing to Taskiem**, on any
  channel. Every message the person sends to the Taskiem conversation, with a
  mention or without, renews the 30 minutes.
- **Per person, per business:**
  - The mode belongs to the person (the member) in the business the conversation is
    for.
  - The same person in two businesses has two modes.
  - The mode is the same on every channel, as the Taskiem conversation's history
    already is (L3).
- **No button or indicator.** On the web the replies in the conversation are the
  only sign, as on the phone. The composer only has to let a lone `@` be sent (§6).

### 4.2 While it is on

A message is a **free-chat message** when it mentions at least one linked business
(§3).

- **A free-chat message is sent to the business**, from this person, through the
  same path as `ask Dangote …` (`sendLink`).
  - **Checks:** the `chat.trade` permission, the plan's messages per day, and the
    audit in both businesses.
  - **Commands and the AI:** nothing else reads it; no command, mention order or
    model.
  - **Before** a pending flow's reply, and before the AI. After a quoted reply to a
    business's message, which keeps working as it does today.
- **What the business receives:**
  - **Mentions at the start** are removed: `@Dangote how much is cement?` arrives
    as "how much is cement?".
  - **A mention later in the message** keeps the words typed, without the `@`: `Hi
    @Dangote, is the depot open?` arrives as "Hi Dangote, is the depot open?".
  - **Nothing left** once the mentions are removed: "What do you want to say to
    *Dangote Nigeria Ltd*?", the existing `chat.link.ask_what`, worded for `@`.
- **Several businesses mentioned:** the same words go to each, separately, as one
  message in each conversation. Each business replies in its own.
- **The receipt:** "✓ Sent to *Dangote Nigeria Ltd*", or "✓ Sent to *Dangote Nigeria
  Ltd* and *BUA Foods*".
  - **A business it could not go to** is named with the reason (not linked, the
    limit, no permission), from today's `chat.link.not_sent`. The ones that went are
    still named.
- **A free-chat message that reads like an order**, such as `buy 400 bags of cement
  from @Dangote`:
  - **What happens:** it is still sent as a message.
  - **The receipt adds:** "To order instead, send *@* to end free chat."
  - **When:** an order word and a quantity, as `orderVerbRe` and the quantity
    phrases already read it.
- **A message with no mention** goes to Taskiem exactly as with free chat off.
- **Ambiguous or unknown mentions** are answered as in §3.1, and nothing is sent to
  anyone, even to the businesses that were clear. Sending to some and asking about
  the rest would split one message.

### 4.3 When it ends

- **No message is sent when the time runs out,** since nobody asked for it.
- **With the next reply:** the person's next message after the end is read with
  free chat off. Taskiem's reply to it starts with "Free chat ended." on a line of
  its own, once.
- **Why:** if that message was `buy 400 bags from @Dangote`, the person sees why it
  became a draft order, not a message.

### 4.4 The hint while it is off

- **The one change to today's replies, with free chat off:** the "how many"
  reply to a mention with nothing to order (`wa.mention.how`) gains a line: "Or send
  *@* first to talk to *Dangote Nigeria Ltd* directly."
- **The trigger:** `@Dangote how much is cement?`.
- **Why:** this is where people find the feature.

### 4.5 Attachments (a slice of its own)

- **Today:** the phone sends only text to a linked business.
- **The slice:** in free chat, a photo or a document whose caption mentions a linked
  business is sent to it with the caption, as a photo or a file in that
  conversation. Each business keeps its own copy (L3).
  - This needs the phone's media fetched and passed as `SendOpts.Ours` and
    `TheirCopy`, as the web's upload does (`api/link_chat.go`).
- **Without this slice:** such a photo goes to Taskiem as today, and the receipt
  path is not reached.
- **Voice notes are not forwarded.** The `@` cannot be spoken, so a voice note goes
  to Taskiem as today, in either mode.

## 5. Storage

- **Migration 00313** (the next number on main): `chat_free` with these columns:
  - `tenant_id`, `user_id` (the primary key);
  - `until timestamptz`, the end, renewed by each message;
  - `ended_noticed boolean`.
- **Access:** row-level security on the tenant (`tenant_isolation`), with SELECT,
  INSERT, UPDATE and DELETE for `taskiem_app`.
- **The row's life:**
  - Turning it on writes the row; each message renews `until`.
  - Turning it off with `@` deletes it.
  - A row past `until` is read as off. The next message's reply carries "Free chat
    ended." (§4.3) and deletes it.
  - Rows a day past `until` are swept by the existing chat cleanup.
- **Why not `chat_sessions`:** it is kept per number and channel and removed when
  idle, and the mode must outlive both.
- **Down:** the migration drops the table.

## 6. The web composer

- **Today:** in the Taskiem conversation, typing `@` opens the picker, and Enter
  takes the first business (`web/src/pages/Mentions.tsx`).
- **The change:** when the text is just `@`, with no query typed after it, Enter
  sends it. Tab still picks. This is the composer's only change.
- **Not in a linked business's conversation:** free chat has no meaning there.

## 7. Words

New strings, in the five languages. English is reviewed, the other four are drafts,
as before:

- `chat.free.on`: the reply that turns it on.
- `chat.free.off`: the reply that turns it off.
- `chat.free.ended`: the line before the next reply.
- `chat.free.sent`: the receipt.
- `chat.free.sent_some`: the receipt when some businesses could not be sent to.
- `chat.free.order_hint`: the hint for a message that reads like an order.
- `chat.free.which`: the question when a mention is ambiguous. Buttons are the
  business names.
- `wa.mention.how`: gains the hint line (§4.4).
- The help text (`waHelp`) gains one line: "*@* to talk straight to a linked
  business".

## 8. Slices

| Slice | What | Size |
| --- | --- | --- |
| F1 | **The matcher** (§3) in `engine/links`, used by mentions, `ask …` and `link@1`. Unit tests over the names in §3.1, case folding, legal suffixes, run length, exact-beats-partial, two links of one name. | S |
| F2 | **Free chat** (§4.1 to §4.4): migration 00313, on and off, the 30 minutes, sending, the receipts, the hint, ambiguity buttons with the held message, the words, the composer's lone `@`. | M |
| F3 | **Attachments** (§4.5): a photo or document with a mention in its caption. | S |
| F4 | **Docs:** the owners' guide (talking to linked businesses), `docs/whatsapp.md`, `business-links.md` (§5.5 and an As built for L4's amendment), the OpenAPI if anything is added. | S |

## 9. Tests

- **Matcher:**
  - `@Dangote`, `@dangote nigeria`, `@Dangote Ltd` and `@DangoteNigeria` find
    Dangote Nigeria Ltd.
  - `@Nigeria` finds nothing.
  - Dangote Cement and Dangote Sugar make `@Dangote` ambiguous, and `@Dangote
    Cement` is not.
  - "Dangote" and "Dangote Nigeria Ltd": the whole name wins.
  - A `link@1` workflow naming a business by a word inside its name still finds it.
- **Free chat off:**
  - **Unchanged:** every existing mention order test passes untouched.
  - **The name fix:** `Buy 20 bags of rice from @Dangote` makes the draft when the
    link is "Dangote Nigeria Ltd".
  - **The hint:** `@Dangote how much is cement?` gets the "how many" reply with it.
- **Free chat on** (on WhatsApp, Telegram and the web):
  - **Sending:** a lone `@` turns it on. `@Dangote how much is cement?` reaches
    Dangote's conversation as "how much is cement?", their people are told on their
    phones, and the sender gets the receipt.
  - **Without a mention:** `sell 5 bags of rice` is a sale.
  - **Several:** `@Dangote @BUA price?` makes one message in each conversation.
  - **Order-like:** `buy 400 bags from @Dangote` is sent, with the order hint.
  - **Ambiguous and unknown:** an ambiguous mention sends nothing and asks; a tap
    sends the held message. An unknown mention sends nothing.
  - **Refusals:**
    - Without `chat.trade`: refused, nothing sent.
    - At the plan's daily limit: named in the receipt.
  - **A pending question:** a draft waiting for Yes is still confirmed by "yes"
    after a free-chat message.
  - **Time:**
    - 29 minutes of quiet keeps it on.
    - 31 minutes turns it off: the next `@` order is a draft, after "Free chat
      ended.", which appears once.
    - A lone `@` again turns it off.
    - On in WhatsApp, it is on on the web for the same person.
    - Another member of the business is unaffected.
  - **Quoted replies:** a quoted reply to a business's message still goes to that
    conversation.
- **Composer:** a lone `@` with Enter sends; `@D` with Enter picks.
- **Isolation:**
  - `chat_free` rows are invisible across tenants. This is added to the isolation
    harness.
  - A free-chat message crosses only through `taskiem_link_enter`, audited in both
    businesses.

## 10. To confirm

- **What counts as activity:** every message to Taskiem renews the 30 minutes, not
  only free-chat messages. This follows "after 30 minutes of inactivity".
- **The legal suffixes ignored:** is `ltd, limited, plc, inc, co, company` enough,
  or should `enterprises`, `ventures`, `nig`/`nigeria` be ignored too? They are
  often part of how people say a name, so they are left in for now.
- **F3 (attachments):** with F2, or later.
- **No new decision:** this is an amendment to decision 0061's L4, recorded in its
  spec.
