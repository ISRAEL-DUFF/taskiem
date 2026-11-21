# Menus everywhere, and a picker that searches

Status: E1 built, 21 November 2026; E2–E4 to come. Follows [Menus in the chat](chat-menus.md)
(M1–M4, built) and [Chat tools](chat-tools.md).

## 1. Why

The topic menus ("What would you like to do?") made Taskiem easy to use without
knowing its commands, but only inside the Taskiem chat. People work in other
places too: in a chat with a supplier, on Today, looking for something. The
same menus should be there, and do the same things.

The tag picker ("/product", "@", "/invoice") shows six of each kind from what
the phone has loaded: the newest 100 invoices or orders, every product. A
business with 400 products, or an invoice from March, cannot find what it wants.
The words typed after "/product" do narrow the list, but nothing on the screen
says so.

## 2. Decisions (founder, 21 November 2026)

1. The topic menus come to **three more places**: the **grid button in any
   chat**, the **Today** screen, and a new **search everywhere**.
2. The tag picker gets a **search box** that searches on the server across
   everything (not only what was loaded), with "Show more", and **kind chips**
   (All · Products · Services · Invoices · Orders · Businesses · Saved).
   Status chips (Unpaid, Overdue…) and "recent first" are not now (§9).

## 3. One menu, drawn in many places

The menu is the same everywhere: the person's own catalogue
(`GET /v1/chat/actions`), topics as tiles, a topic's five rows and "All N", the
picked row first. What changes from place to place is only **what a row does when
pressed**, because only the Taskiem chat can hold a conversation with Taskiem:

| A row that… | In the Taskiem chat (today) | Anywhere else (new) |
|---|---|---|
| shows something ("Today's sales") | Taskiem answers in the chat | The answer in a **sheet** over the screen, with "Open Taskiem chat" |
| opens a tool or a page | Opens it here | Opens it here (the same) |
| takes values ("Add a rider") | Its form (M4) | Its form (M4, the same), never questions step by step |
| changes something | The form's Save is the Yes | The same |

So no row asks step by step outside the Taskiem chat: every such action has its
fields (M4), and a form is quicker than questions there. What was done is said
in a **toast** ("Rider added: Musa") with "Open Taskiem chat", and kept in the
Taskiem chat's history like everything else Taskiem did, so it is never lost.

**In a business's chat** the answer is also left in that conversation as a
**note only you see**, the same note "@Taskiem" leaves (kind `taskiem`, built 21
November 2026). The other business never sees it. This is what makes the menu
useful there: "Who owes you" while chatting with a customer, and the answer
stays under the conversation.

The menu component is moved out of the chat so every place uses one: the web's
`ChatMenu.tsx` `MenuCard` and the app's `menu_card.dart` `MenuCard` gain a
`place` (`taskiem` | `sheet`) that decides the table above. Its look does not
change.

## 4. The grid button in any chat

The grid button beside the message box opens the **tools panel** today: the
tools (Sell, Stock, Invoices…) and, in a business's chat, what is between us and
them. It gains a second view:

- At the top of the panel, two chips: **Tools** · **What would you like to do?**
  The panel opens on the one used last (Tools at first).
- *What would you like to do?* is the menu (§3) inside the panel: topic tiles,
  then a topic's rows. Pressing a row does what §3 says, with the note in that
  chat.
- "/" typed as the whole message opens the panel on Tools, as now.

Why a chip and not a second button: the panel is already where people go for
"do something", and one button keeps the message box as WhatsApp's.

## 5. Today

Today gains a **"What would you like to do?"** panel under its top card: the
topic tiles, two rows of four (the eight most used, by `chat_action_uses`, then
the catalogue's order), and "All topics" for the rest. A tile opens that topic's
rows in a sheet. Rows do what §3 says.

## 6. Search everywhere

A **search** button in the head of Chats and Today opens one search screen:

- A box, and the kind chips of §7.
- As you type, results grouped: **Actions** (the catalogue's titles, lines and
  shortcuts: "add rider" finds "Add a rider"), **Products and services**,
  **Invoices and bills**, **Orders and purchases**, **Businesses**, **Chats**
  (a conversation's name, and its messages' words), **Saved**.
- An action opens its form or answer (§3). Anything else opens its page, or its
  conversation at the message.
- Three of each group, "Show more" in a group for the rest.

One server call answers it: `GET /v1/search?q=&kind=&cursor=` (§8).

## 7. The tag picker searches

The picker over the message box ("/product", "/invoice", "@"):

- Its first line ("Tag a thing or a business") becomes a **search box**,
  holding what was typed after the mark. Typing in either moves both.
- **Kind chips** under it: All · Products · Services · Invoices · Orders ·
  Businesses · Saved, only the kinds the place offers (a business's chat
  offers its own invoices and orders with that business). "/invoice" opens on
  Invoices.
- Results come from the server (§8) across **all** the records, not only the
  newest 100: six at first, then **"Show more"**, six at a time.
- With nothing typed, the newest first (as now).
- Offline, it searches what the phone has kept, and says "Searching what is on
  this phone".

## 8. What changes for the API

- `GET /v1/search?q=&kind=&link=&cursor=&limit=`: one search over what the
  person may see. `kind` is one of `action`, `item`, `service`, `invoice`,
  `bill`, `order`, `purchase`, `business`, `chat`, `saved`, or none for all
  (grouped). `link` narrows invoices and orders to those with one business (the
  picker in that chat). Words match the start of any word of a name or
  reference first, then anywhere. Permissions as each list's own (an invoice
  needs `invoice.read`). Uses the existing indexes, adding trigram indexes
  where a list has none.
- `POST /v1/chat/actions/{id}/run` `{values, link?}`: an action pressed outside
  the Taskiem chat. It is read in the Taskiem chat as the row's `act:` or
  `form:` would be, and kept in its history. It answers Taskiem's words for the
  sheet or the toast and, with `link`, the note left in that conversation. A
  questions action without its values is refused (`400`): its form is asked
  first.
- The picker reads `GET /v1/search` instead of `/v1/link-chats/{link}/refs`
  and the price lists in the phone. Those stay for what they also do.
- OpenAPI, the generated clients, and the docs follow.

## 9. Not now

- Status chips (Unpaid, Overdue, Paid; New, Delivered; Low stock) in the picker
  and the search.
- "Recent first" from what you tagged or opened.
- The menus on WhatsApp outside the Taskiem conversation: WhatsApp has only the
  one conversation.

## What E1 built (21 November 2026)

- **The search** is on the two lists the picker already read, not a new
  `/v1/search`: `GET /v1/chat-refs` (the Taskiem chat) and
  `GET /v1/link-chats/{link}/refs` (a business's conversation, only what is
  between the two) take `q`, `kind` (invoice, bill, order, purchase,
  customer; comma separated), `cursor` and `limit`, and answer `refs` with a
  `cursor` for the next page ("" for no more). Without any of them they answer
  as before. A customer's name is sealed, so no index finds it: each list is
  read a page of 200 at a time, newest first, each row opened and matched,
  until enough are found or 2,000 rows were read; the cursor keeps where each
  kind stopped (`api/chat_refs_search.go`). A word matches the start of a word
  of the number, who it is with (a person's name in full, though it is shown
  masked), the kind or the status ("unpaid", "overdue", "delivered"); digits
  anywhere in a reference ("0104"). `GET /v1/search` stays for E4.
- **Products, services, businesses and saved things** are searched on the
  phone or the page, as before: those lists were already whole there, only
  shown six at a time.
- **The picker** (app `tag_box.dart`, `tag_picker.dart`; web `Mentions.tsx`):
  a search box at the top holding the words after the mark (typed in either,
  both change); the kinds as chips, only those with something to offer;
  "/invoice" opens on Invoices, "/bill" and "/purchase" keep to their own kind
  until another chip is pressed; six, then "Show more", six at a time, the
  server's next page when those run out; "Nothing found" when nothing matched,
  with the chips still there to try; "Searching what is on this phone" when the
  server could not be asked. Something picked under another chip goes in with
  its own mark: a business or a saved thing "@", an invoice or an order "#".

## 10. Phases

| | What | Where |
|---|---|---|
| **E1** | The picker's search box, kind chips and "Show more"; the refs lists searched on the server (built) | server, app, web |
| **E2** | `POST /v1/chat/actions/{id}/run`; the menu in a sheet (`place: sheet`); the grid button's second view, with the note in a business's chat | server, app, web |
| **E3** | Today's panel | app, web |
| **E4** | Search everywhere, actions included | app, web |

E1 first: it is the one people hit every day as their lists grow, and E4 reuses
its search.
