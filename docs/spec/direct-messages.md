# Messages straight to linked businesses, and names found by their start

Status: **built** (agreed with the founder). This amends business links L4
([business-links.md](business-links.md) §5.5, §16.4). Two problems prompted it,
both in the Taskiem conversation on WhatsApp, Telegram, Slack and the web:

1. **Names with spaces.** `@Dangote` did not find **Dangote Nigeria Ltd**. Only the
   whole name matched, so Taskiem answered "You are not linked with Dangote…",
   which was wrong.
2. **No way to just talk.** Every message with an `@` is read as an order.
   `@Dangote how much is a bag of cement?` asks for no quantity, so Taskiem
   answered "Say how many of what…" and nothing reached Dangote.

An earlier draft of this plan had a "free chat" mode, turned on by a lone `@` for
30 minutes. **The founder dropped it** for a marker on each message:
- no mode to remember;
- no table and no timer;
- nothing changed for messages without the marker.

## 1. Summary

- **A business is found by the start of its name,** everywhere a linked business is
  named: mentions, `ask …`, `link@1`.
  - `@Dangote` finds Dangote Nigeria Ltd.
  - When two businesses fit, Taskiem asks which, naming each with its role. It
    never picks one in silence.
- **`...` first sends a message straight to the businesses it mentions.** No command
  and no model reads it.
  ```
  ...
  @Dangote how much is a bag of cement?
  ```
  - **What Dangote receives:** "how much is a bag of cement?", in our conversation
    with them.
  - **What the sender sees:** "✓ Sent to *Dangote Nigeria Ltd*."
- **Without the marker, every message is read as it was.** `Buy 400 bags of cement
  from @Dangote` still makes a draft order. The only changes there are the name fix
  and one hint (§4).

## 2. What stays as it is

- Mention orders (L4): one draft per business, and one **Yes** that sends them all.
- `ask Dangote …` and `ask Dangote about INV-0042 …`. They now use the same name
  matching (§3).
- A quoted reply on the phone to a business's message goes back into that
  conversation.
- A linked business's messages reach the phone as before: "[Dangote Nigeria Ltd]
  Musa: …".
- The Chat page's conversation with a business, which was always direct.
- Commands, sales, money, stock, invoices, the AI builder, and voice notes.

## 3. Finding a business by its name

`links.Mentioned` reads the words after an `@`. `links.Named` reads a name given
whole. Both are in `engine/links/named.go`, and they follow one rule.

- **Case and punctuation are ignored.** Characters are folded, so İ and the Kelvin
  sign read as they did before; İ is still not i.
- **A legal suffix at the end of a name is ignored,** on both sides: `ltd`,
  `limited`, `plc`, `inc`, `co`, `company`. So `@Dangote Ltd` finds Dangote Nigeria
  Ltd.
- **Words from the start of the name, in order.**
  - `Dangote` and `Dangote Nigeria` fit Dangote Nigeria Ltd.
  - `Nigeria` does not.
  - A partial word does not: `Dang` finds nothing.
- **One word with the spaces left out** fits too: `@DangoteNigeria`.
- **The longest run of words wins.**
  - In `@Dangote how much…`, only `Dangote` fits.
  - In `@Dangote Nigeria is late`, `Dangote Nigeria` fits.
  - A run is the words separated by single spaces; a comma, a question mark or a new
    line ends it.
- **A whole name beats one it only begins.** With both "Dangote" and "Dangote
  Stores" linked, `@Dangote` is "Dangote".
- **Ambiguous:**
  - **When:** the longest run fits several businesses, as with `@Dangote` for both
    Dangote Cement and Dangote Sugar, or two links of the very same name.
  - **Then:** the reply names each business with its role (`wa.mention.which`), and
    nothing is sent.
- **None fits:** the reply is "You are not linked with *Dangote*…"
  (`wa.mention.unlinked`), as before.

**Where it is used:**
- **Mentions** (`parseMentions`, `api/mentions.go`), for orders and for `...`.
- **`ask …`** (`waAskLinked`, `api/link_chat.go`).
  - Its old character-by-character prefix (`ask Dang hi`) is gone.
  - A name that fits several businesses is now asked about, rather than passed on
    to the tables' `ask`.
- **`link@1`'s `business`** (`linkNamed`, `api/link_connector.go`): the exact name,
  then the start of a name, then, as a last resort, a name containing the words, so
  no existing workflow breaks.

The web composer's `@` picker still puts the whole name in, and the whole name still
matches.

## 4. The `...` marker

`api/chat_direct.go`. It is checked after a quoted reply and before everything else
in `waCommand`: pending questions, commands, mention orders and the model.

- **The marker:** three or more full stops, or the single `…` a phone's keyboard
  makes of them, at the start of the message.
  - It must be followed by a space, a new line or nothing. `...and five more` is no
    marker.
  - On its own line, the message follows on the next lines.
  - On the same line, `... @Dangote …`, for keyboards where Enter sends.
  - **It needs an `@`.** A `...` before a message with no `@` (`... sold 5 bags of
    rice`) is read as it always was, so nobody who starts a message with dots is
    caught out. Only a lone `...`, or one with an `@`, is the marker.
- **A lone `...`:** Taskiem answers "Now send the message, with *@business*…", and the
  next message goes to the businesses it mentions.
  - The wait is in the chat session (`chatData.Direct`) and lasts five minutes.
  - **A pending question is kept.** If Taskiem was waiting on a question (a draft to
    confirm, say), it is still waiting.
  - **A next message with no `@`** ends the wait, and is read as usual. So do a
    photo, a file, a voice note or a completed form, which are then taken as they
    would be.
  - Saving the wait keeps a pending security step's ten minutes.
- **Who it goes to:** every business the message mentions (§3).
  - Several mentions mean the same words to each, as its own message in its own
    conversation.
  - **Unless every mention names exactly one linked business, nothing is sent to
    anyone.** The reply names the unknown business, or asks which one.
  - **An `@` before a number is an amount, not a business:** `cement @4500` is
    sent as typed. This also fixes `buy 20 bags @4500 from @Dangote`, which before
    read "4500" as a business not linked.
- **What they receive:**
  - The mentions at the start are left out: `@Dangote, @BUA: price?` arrives as
    "price?".
  - A mention further in keeps the words typed, without the `@`: `Hi @Dangote, is the
    depot open?` arrives as "Hi Dangote, is the depot open?".
  - New lines are kept.
- **Replies when nothing can be sent:**
  - An `@` that names nothing (`... @ how much?`): "Who is it for? Add
    *@business*…".
  - Nothing left once the mentions are removed: "What do you want to say to
    *Dangote Nigeria Ltd*?".
- **How it is sent:** through `sendLink`, as `ask …` is.
  - **Checks:** the `chat.trade` permission (without it, `chat.link.denied`), the
    plan's messages per day, and the audit in both businesses.
  - **Reaching them:** their people are told on their phones (unless muted), with
    the conversation on the web.
- **The receipt:**
  - "✓ Sent to *Dangote Nigeria Ltd*, *BUA Foods Plc*."
  - A business it could not go to is named with the reason ("Not sent to *X*:
    …"), under the ones that went.
- **Photos and files** captioned `...` are not sent. The reply says that only words
  go this way, and that photos and files go on the Chat page. They are not read as a
  product photo or a list either.
- **Voice notes** cannot carry the marker, so they go to Taskiem as before.

**The hint without the marker.** When a mention asks for no quantity, the reply
ends with "To send it to *Dangote* as a message instead, start it with *...*". This
happens in two places:
- the "Say how many of what…" reply (`wa.mention.how`);
- "shares no price list with you" (`wa.prices.none` with `chat.direct.hint`).

It is how people find the marker. The help text (`waHelp`) also gains a line for
anyone who may write to linked businesses.

## 5. Words

The new messages are in all five languages: English reviewed, the other four drafts.
- `chat.direct.wait`, `chat.direct.who` and `chat.direct.empty`;
- `chat.direct.sent`, `chat.direct.not_sent`, `chat.direct.files` and
  `chat.direct.hint`;
- `wa.help.direct`.

Two English messages changed:
- `wa.mention.how` gains the hint;
- `wa.mention.which` now reads "*@Dangote* could be more than one of your linked
  businesses: …. Use more of the name, or pick it on the Chat page".

## 6. Cost

- Without the marker, a message costs one string check. With it, it costs the link
  list read (already read for any mention) and one send per business.
- Nothing is stored beyond the message, apart from a lone `...`'s five-minute wait
  in the existing chat session.

## 7. Tests

- **`engine/links` `TestMentioned`:**
  - the start of a name, the whole name, suffixes, no spaces, punctuation and new
    lines;
  - two that fit, and a word that decides between them;
  - a whole name beating a longer one;
  - ended links, and İ.
- **`api` `TestParseMentions`:** the old cases, the start of a name, and two that
  fit.
- **`TestCutDirect`:** the marker's forms, and what is not one.
- **`TestDirectBody`:** what is received.
- **`api/chat_direct_test.go` `TestDirectMessages`, on WhatsApp:**
  - the marker on the same line, on its own line, and as `…`, each received as said;
  - a lone `...` then the message, and a lone `...` then a message with no mention;
  - `... links` and `...and` read as before;
  - an amount after `@`;
  - in `TestMentionsOrderFromTwo`: a lone `...` and a message to Dangote while two
    drafts wait, and the drafts' **Yes** still sends both;
  - two businesses;
  - nothing sent for no mention, an unknown business, or nothing to say;
  - `...and` read as before;
  - the hint without the marker;
  - two businesses that "Dangote" fits, and the right one when more of the name is
    given;
  - a viewer refused.
