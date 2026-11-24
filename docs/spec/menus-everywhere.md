# Menus everywhere, and a picker that searches

Status: built (E1–E4), 21 November 2026; saved things and services in search
everywhere built in H4, 24 November 2026 ("What H4 built"). Follows [Menus in
the chat](chat-menus.md) (M1–M4, built) and [Chat tools](chat-tools.md).

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

## What E2 built (21 November 2026)

- **`POST /v1/chat/actions/{id}/run`** `{values, link}` (`api/chat_actions.go`):
  read in the Taskiem chat as the row's `act:` (or `form:` with values) would
  be, kept in its history, answered with what Taskiem said; an action that
  takes values without them is refused (400), as are values it does not have;
  with `link`, what Taskiem said is kept in that conversation as the note
  "@Taskiem" leaves (kind `taskiem`), answered as `note`.
- **The tools panel's second view** (app `tools_panel.dart`, web
  `ChatTools.tsx`): two chips under its heading, **Tools** and **What would
  you like to do?**; it opens on the one used last; "/" and a word is always
  the tools. The view is the menu card without its own heading or ✕
  (`MenuCard(id: null, head: false)`).
- **Pressed in a business's conversation**: a row that shows something or
  asks is done with the run, its answer in a sheet (`showActionAnswer`,
  `ActionAnswer`: the words, a question's buttons answered there, Done and
  Taskiem chat) and the note lands in the thread at once; a row with values
  opens its form first; one with a tool the conversation has opens it.
  **In the Taskiem chat** a row is said there, as a row of the chat's own
  menus.
- The web draws a row's form outside the composer (a portal), so its Save
  submits its own form, not the message box's.

## What E3 built (21 November 2026)

- **Today** (app `today_tab.dart`, web `Home.tsx`): "What would you like to
  do?" under the day's figures, before Needs you: the person's topics as
  tiles, the seven used most (the catalogue already gives them most used
  first) and an **All topics** tile for the rest, shown in place. A tile opens
  that topic's rows in a sheet; a row is done with the run of E2 (no
  conversation, so no note), its answer in the answer sheet; a row with values
  opens its form first. Today has no tools, so a row whose tool would open
  elsewhere is done as one that asks (the web's `MenuCard` takes `openTool` as
  optional now).
- The run and its answer are one helper on each side now: `runActionHere`
  (app) and `useActionRun` (web), used by the tools panel and Today alike.

## What E4 built (21 November 2026)

- **Search everywhere** (app `search_everywhere.dart`, web
  `SearchEverywhere.tsx` at `/search`): a search button in the head of
  Chats and of Today. One box, and chips: All · Actions · Chats · Products ·
  Invoices · Orders · Customers. As the words are typed, the results in
  groups, three of each in All, and "Show more" goes to the group's chip,
  eight at a time there.
- **Where each comes from**: actions from the person's catalogue (a word of
  the title, the topic's name, or the command: "add rider" finds "Add a
  rider"); chats from the linked businesses; products from the business's
  stock; invoices and bills, orders and purchases, and customers from the
  refs search of E1 (`GET /v1/chat-refs` with `q`, `kind`, `cursor`), every
  record. There is no `GET /v1/search`: each list was already searchable
  where it is, so one more endpoint would only join them.
- **What a result does**: an action is done as from Today (its form first,
  its answer in a sheet); a chat or a customer opens its conversation; an
  invoice, a bill, an order or a purchase opens its own page; a product opens
  the stock list.
- **Not in E4**: the words of messages (each conversation's own search has
  them), saved things, and services (the last two built in H4).

## What H4 built (24 November 2026)

[Build plan, gaps](build-plan-gaps.md) §6, `g-search-saved`.

- **The server's search** (`GET /v1/chat-refs`) takes two more kinds, asked
  for by name only (with no kind it answers the picker's kinds, as before):
  `service`, the business's services (to those who may manage its page, as
  its services list), with the price and minutes; and `saved`, the person's
  own saved things and their business's (`api/chat_refs_saved.go`), matched
  in the database by its name or its kind's words ("mum", "bank account", a
  made-up kind's name; a % or _ typed is itself), the person's own read in
  their own scope before the request's business transaction opens (one
  pooled connection at a time), the business's on it, merged newest first,
  and answered with **its name, its
  kind and whose it is only**: never its title, its hint, an account's
  number or anything it holds, as the picker and the model are told them.
  Another person's, another business's, and an API key's or an embed app's
  user's (who have none) are never read; a member gets the business's and
  only their own. "Show more" goes on by the cursor, as the other kinds.
- **Search everywhere** (web `SearchEverywhere.tsx`, app
  `search_everywhere.dart`) has **Services** and **Saved** chips and groups:
  All asks the refs, the services and the saved things each on its own, so
  one kind's many never crowd out another's few; three of each, Show more to
  the chip. A service shows its price and minutes and opens the bookings
  page; a saved thing shows "@name", its kind and "Yours" or "The
  business's" (web `lib/found.ts`, app `foundTag`) and opens the saved
  things: in the app the person's own or the business's list, on the web
  the Saved page, which now shows the person's own beside the business's
  (in a person's own session only: a field agent working there has none).

## 10. Phases

| | What | Where |
|---|---|---|
| **E1** | The picker's search box, kind chips and "Show more"; the refs lists searched on the server (built) | server, app, web |
| **E2** | `POST /v1/chat/actions/{id}/run`; the grid button's second view, its answer in a sheet, the note in a business's chat (built) | server, app, web |
| **E3** | Today's panel (built) | app, web |
| **E4** | Search everywhere, actions included (built) | app, web |

E1 first: it is the one people hit every day as their lists grow, and E4 reuses
its search.
