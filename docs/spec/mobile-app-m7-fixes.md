# Phone app M7: what the audit found, and the plan to fix it

The audit of M7 ([plan](mobile-app-m7.md)), 12 November 2026: three
reviews, each reading one part of the app against the web pages it copies
(`web/src`) and the server's own routes (`api/`, `engine/`). Every request
the app sends was checked against the server: paths, methods, bodies,
query strings and uploads all match. What follows is what does not: 12
bugs, the gaps against the web, and the tests that would have caught
them. Each finding below was checked again in the code before it was
written here.

Severity: **bug** (wrong, or breaks), **gap** (the web does it, the app
does not), **minor** (small, or rare). Paths are under `mobile/` unless
they say otherwise; line numbers are as of commit `f970e6b5`.

## How the work is done

- Fixed part by part, in the order below; each part is one commit, with its
  tests, `flutter analyze` clean, and the part's own test files run.
- Every fix has a test that fails without it, where a widget test can see
  it. A test that only shows a screen does not count.
- The full suite runs once at the end, before the last push.
- Nothing about money is pulled forward: an order's invoice, a buyer's
  statement, the alerts' Reorder and Match it, and paying riders stay
  "coming to the app soon" until M8.
- Where the web has the same flaw (marked **web too**), the app is fixed
  and the web is left as it is; each is listed at the end for a web fix of
  its own.

## Part 1: shared kit (first: later parts use it)

| # | Sev | Where | What is wrong | Fix | Test |
|---|---|---|---|---|---|
| 1.1 | gap | `lib/kit/kit.dart:390` `TkChoiceRow` | A radio row says it is one of a group but never which one is chosen: `TkRadio` has no semantics and the row sets no `checked`. A screen reader cannot tell the delivery, pay or reason chosen. `connect.dart:264` works around it by wrapping; the rest do not. | Give `TkChoiceRow` a `checked` (bool?) it puts on its `Semantics` (`checked:`, with `inMutuallyExclusiveGroup` for radios). Pass it at every use: `customer_orders.dart` (delivery, pay), `storefront_orders_page.dart` (dispatch mode), `stock_page.dart:728, 977`, and drop the wrapper in `connect.dart`. | A kit test: a radio row's node is checked when chosen, not when not. The customer card test reads the chosen pay choice through semantics. |
| 1.2 | minor | pages that poll: `storefront_orders_page.dart:342, 529, 1197`; `customer_orders.dart` (4 s) | Polling goes on while another screen covers the page, and while the app is in the background; nothing reads again when the app comes back (the web reloads on becoming visible). | A small `Polling` mixin in `lib/kit/`: a periodic timer that skips a tick while `ModalRoute.of(context)?.isCurrent == false` or the app is not resumed, and reads once on resume (`AppLifecycleListener`). Cancelled in `dispose`. Used by every poll above. | A test: with a page pushed over it, ticks send no request; popped, the next tick does. |

## Part 2: orders from linked businesses (Orders sheet and the order screen)

| # | Sev | Where | What is wrong | Fix | Test |
|---|---|---|---|---|---|
| 2.1 | **bug** (web too) | `lib/chat/order_view.dart:131-133` `_TradeOrderView._act` | A cashier (`order.handle`, not `storefront.manage`) may act in the sheet (`tool_sheet.dart:42`). Accepting an offer under the lowest price, the server answers **202** `{"waiting":"owner","why":…}` (`api/link_orders.go:193`, `api/trade_deals.go:165`) and accepts nothing. The app still writes "Accepted order X from Y" and shows Saved. | Read the answer: when `waiting == 'owner'`, no diary line, no Saved; show the server's `why` (or "It waits for the owner's OK") in the sheet and keep the buttons. As `deal.dart:207` already does for a deal. | Stub a 202 waiting answer: no `onSaved`, the words shown, Accept still there. |
| 2.2 | **bug** (web too) | `order_view.dart:142, 171` | Accept shows on a `changed` order (our change waits for the buyer). The server accepts only `received` (`engine/links/orders.go:914`), so pressing it fails. | Accept only on `received`; Decline on `received` or `changed`. | A `changed` order shows Decline and no Accept. |
| 2.3 | minor (web too) | `order_view.dart:28-48` | `arrived` has no word and no step: the pill says "arrived", the progress bar goes. | `'arrived': 'At the door'`, step 2 (On the way). | Words test. |
| 2.4 | minor | `order_view.dart:286` (Call), and every `callPhone`/`openOutside` | A call or map that cannot open does nothing visible. | A toast when the launch answers false: "That number could not be called." / "The map could not be opened." | Stub `callPhone` returning false: the toast shows. |
| 2.5 | minor bug | `lib/chat/trade_orders_page.dart:805-821` `_TradeSettings` | Changing "Raise the invoice" or the auto-accept switch reloads the terms, which overwrites Days to pay and Credit limit typed but not saved. The web keeps what is typed (`days ?? terms`). | Keep the typed values: refill the two boxes only on first load and after Save terms. | Type 14 days, flip auto-accept: 14 is still there. |
| 2.6 | gap | `trade_orders_page.dart:331-335` | The over-limit notice drops the web's "Their statement" link (`TradeOrders.tsx:245`). | Add the link in the notice through the same `onStatement ?? coming soon` as the terms card (statement is M8). | The notice has "Their statement". |
| 2.7 | gap | `trade_orders_page.dart:203, 976` | The empty list lacks the web's **Linked businesses** action; the draft with no buyers lacks **Find them**. | Empty list: a Linked businesses button (opens `LinksScreen`). No buyers: a Find them button (opens Explore), closing the modal. | Both buttons shown and open their screens. |
| 2.8 | gap | `TradeOrderPage` | No **Messages about it**: the web shows the order's thread with a reply box under the actions (`Conversation.tsx:2078` `DocThread`, `GET /v1/link-chats/thread?doc=order&reference=…`, reply `POST /v1/link-chats/{link}/messages` with `reply_to`, for `chat.trade`). | Its own part (Part 7), after the fixes: it draws the thread's bubbles and cards, which is more than a fix. | See Part 7. |

## Part 3: orders from customers (`lib/chat/storefront_orders_page.dart`)

| # | Sev | Where | What is wrong | Fix | Test |
|---|---|---|---|---|---|
| 3.1 | **bug** | `lib/chat/bookings_page.dart:44-58` `orderStatus` (used at `storefront_orders_page.dart:462, 581`) | No words for `dispatched`, `on_the_way`, `arrived`, `proposed`, `changed`: an order with a rider shows a grey "dispatched". The engine sets them (`engine/dispatch/dispatch.go:793, 823`). | Add the web's (`orders.ts` ORDER_STATUS): With a rider (info), On the way (info), At the door (info), Waiting for the business to confirm (info), Waiting for the business to accept your change (info). | Words test for all five. |
| 3.2 | **bug** | `storefront_orders_page.dart:900-904`, `:744` `OrderDelivery` | Read once: the order page's poll never reaches it (a fixed key, its own state). A rider accepting or delivering, and **Confirm, pay the fee**, show only after reopening. The web reloads it live (`Dispatch.tsx:74`). | Its own poll with the Part 1 mixin (30 s), and read again whenever the page's order reads again (the page passes a counter that changes on each load). | A rider's status changes on the stub; after a tick the card shows Delivered and Confirm. |
| 3.3 | **bug** (accessibility) | `storefront_orders_page.dart:291` `_Kv`, rows at `:855-860`, `:960-966` | Each row is merged into one node; where the value holds links (Call and WhatsApp; the rider's Call) a screen reader gets one node with one tap: one link cannot be reached. The test does not see it (merged nodes are still found by label). | Merge only rows of text; a row with links keeps its links as their own nodes, each saying what it is ("Call Ngozi, 0803…", "WhatsApp Ngozi"). | Semantics test: two tappable nodes in the Phone row; `expectPressable` passes. |
| 3.4 | **bug** (web too) | `storefront_orders_page.dart:965` | A cashier gets the rider's phone masked ("•••1234", `api/dispatch.go:799-808`); the app still offers "Call •••1234". | `isMasked` → plain text, no link. Same for the customer's phone (already right) and email. | Masked rider phone: no Call link. |
| 3.5 | **bug** | `storefront_orders_page.dart:311` `StorefrontOrdersPage` | Nothing opens the list: it cannot be reached. An order opened from a chat card says "Back to Orders" though it goes back to the chat. | Open it from **Orders from linked businesses** (the web's crumb leads there: an "All your orders" link at the top of `TradeOrdersPage`). The order page says "Back" unless opened from the list. | Tap All your orders: the list opens; an order from a card says Back. |
| 3.6 | gap (permissions) | `:606-641` Refund, Mark refunded; `:1026-1046` Confirm the fee, Paid in cash; `:1270` delivery settings; Turn on deliveries | Shown to people the server refuses (403: `api/server.go:545-546, 601-605` need `storefront.manage`). `alerts.dart:613` already gates its Confirm. | Show them only with `can('storefront.manage')`. | Without it: none of these buttons. |
| 3.7 | minor bug | `:1212, 1252` Turn on deliveries | A failed turn-on writes the page error; the next 30 s read clears it, and it shows at the top, not in the card. | Its own error, shown inside the card, cleared on the next press. | A refused turn-on: the error stays in the card after a tick. |
| 3.8 | minor bug | `:379` list | On a 404 ("takes no orders") an earlier error stays beside the empty state. | Clear the error on any answer, 404 included. | Error, then 404: only the empty state. |
| 3.9 | minor | `:538-545` order page `_load` | No request counter: a slow poll answer can overwrite the newer read made after an action. | The `_asked` guard, as the list has. | A slow then fast answer: the fast one stays. |
| 3.10 | gap | `:683-691` Details | Drops the web's **Workflow** row and "…or on the **Approvals** page". | A Workflow row: "Started" when `run_id`, else "Not started yet" (the run's page is the web's, not the app's). The hint names Approvals (approvals are in Taskiem's conversation in the app: say "or in your conversation with Taskiem"). | Both shown. |
| 3.11 | gap | `:863` | The email is plain text; the web makes it a `mailto:` link unless masked. | A link that opens the mail app (`launchUrl` `mailto:`), not when masked. | Link present unmasked, absent masked. |
| 3.12 | minor | list rows `:452-500` | The web tints waiting orders amber (received, awaiting payment, new prices) and refund due red. | `TkRow` gains a tone for its icon tile; the row's tile warn or danger. | Golden-free check: the row's tile tone. |
| 3.13 | minor | `:412` | Drops "…has what is theirs alone." | The web's words. | Text test. |
| 3.14 | minor | `:701`, `:716-719` booking actions | Move is not disabled while busy; a cancel gives no "Booking cancelled" toast. | Disable Move while busy; toast "Booking cancelled". | Both. |
| 3.15 | minor | `:1270-1272` Deliveries' head button | Shows "Settings", a screen reader hears "How orders are offered": the words seen are not in the name. | The web's words on the button: "How orders are offered" (it wraps at large text). | Label equals the words. |
| 3.16 | minor | `:1302` | Turning "Under way now" off brings back the old day; the web clears it. | Clear the day with it. | Off: today's list asked, no `day=`. |

## Part 4: the customer's order card (`lib/chat/customer_orders.dart`)

| # | Sev | Where | What is wrong | Fix | Test |
|---|---|---|---|---|---|
| 4.1 | **bug** | `:409`, in `customer.dart:337-370` | The card's body is 45% of the screen's height, keyboard or not; the screen's column does not scroll. On a 360×640 phone with the keyboard up (typing the address or fee, which are in that body) the screen overflows; at twice the text, likely without it. | Measure the room left (`LayoutBuilder` around the thread and card) and give the card at most 45% of it; with the keyboard up, the card shrinks and scrolls inside. The thread keeps at least its head room. | `CustomerScreen` with an order at 360×640, text ×2, and with `viewInsets.bottom = 280`: no overflow; `expectPressable`. |
| 4.2 | **bug** | `:325` `didUpdateWidget` | Either saved field changing refills both boxes: picking a delivery area (which sets the fee) wipes an address typed but not saved. | Refill the address only when `address` changed, the fee only when `delivery_kobo` changed (as the web, `CustomerOrders.tsx:575-579`). | Type an address, pick an area: the address stays. |
| 4.3 | minor bug | `:327, 377, 503` | The pay choice is cleared only when `pay` changes. Pay at the door, then pickup: the server keeps `on_delivery`, which pickup does not offer, so no choice shows chosen. A refused change leaves the choice shown that the server refused. | Show `payNow` whenever the shown choice is not among `payChoices`; clear `_getBy`/`_pay` when a change fails. | Door, then pickup: the first way it still offers shows chosen (with a pay page, "They pay now, by bank transfer"). A 409: the choices go back. |
| 4.4 | gap (accessibility) | `:459-460, 486-487` | "Deliver to" and "Delivery fee, ₦" are text beside their boxes: the boxes have no name for a screen reader. | `TkKitField(label:)` around each (as the line rows do). | Fields found by label. |
| 4.5 | gap | `:380` | The fee is read with `num.tryParse`: "1,500" or "₦1500" disables Save with no word. | `feeKobo()` (the web's), and "Enter an amount, like 1500." under the box when it is not money. | "1,500" saves 150000; "abc" shows the words. |
| 4.6 | minor | `:182` | `onTold` calls the parent's `setState` after an await, with no `mounted` check. | Check `mounted`. | — |
| 4.7 | minor | `:385` | The working card has no name for a screen reader (the confirmed one does). | Its label: the kicker and the pill ("Order from their message, 1 question"). | Semantics label. |

## Part 5: Availability (`lib/chat/availability_sheet.dart`)

| # | Sev | Where | What is wrong | Fix | Test |
|---|---|---|---|---|---|
| 5.1 | gap | `taskiem.dart:453-455`, `conversation.dart:745-747` | A change reloads only the tile. The server writes a diary line (`api/availability.go` diaryChange) and the web pulls the conversation (`ChatComposer.tsx:512`). | Call the same `landed()` as Sell: pull the conversation (and in Taskiem's, the alerts). | onChanged pulls: a request for newer messages after the change. |
| 5.2 | gap | `lib/chat/tools.dart:626-636` `availabilityWord` | "Busy till 00:30" when it runs past midnight; the web's `shortWhen` names the day when it is not today. | Name the day when `until` is not today in Lagos ("Busy till Fri 00:30"), as `shortWhen`. | Words test across midnight. |
| 5.3 | minor | `:35, :148` | The Busy till box stays editable while a change is sent; `CloseSoon` is mixed in and never used. | Disable the box while busy; drop the mixin. | Busy: the box takes no typing. |
| 5.4 | test | `test/availability_sheet_test.dart` | No error answers: a time already past (400), none of the businesses (409). | — | Both errors shown, buttons back. |

## Part 6: stock counts (`lib/chat/stock_page.dart`)

| # | Sev | Where | What is wrong | Fix | Test |
|---|---|---|---|---|---|
| 6.1 | gap | `:1060-1062` `_CountsCard._load` | The month's figures and the latest counts are read one after the other in one try: if one fails, neither shows. The web reads them apart. | Two reads, each with its own error. | Shrinkage fails, counts still listed. |
| 6.2 | minor | `:1151` | A failed limit save shows its error at the top of the card. | Under the Save the limit button. | Refused save: the error by the button. |
| 6.3 | minor | `bookings_page.dart:404` | A blank line left by the refactor. | Remove. | — |
| 6.4 | test | `test/stock_page_test.dart` | The count's refusal by the server is not tested. | — | A 400 on the count: the error in the modal, Record again. |

## Part 7: Messages about it on the order screen (after Parts 1 to 6)

The web's `DocThread` (`web/src/pages/Conversation.tsx:2078`) under an
order's steps: the thread of messages and cards about that order, newest
last, read again every 10 seconds; for `chat.trade`, a box to reply to the
order's latest card (`POST /v1/link-chats/{link}/messages` with `text` and
`reply_to`), shown only once the thread has a card. The app draws the
bubbles and cards with the conversation's own widgets (`thread.dart`,
`cards.dart`), without the conversation's menu. Then the same section is
added where the web has it for purchases and bills (M8).

Test: the thread's bubbles and the latest card shown; a reply sent with
`reply_to`; no box without `chat.trade`; nothing shown with no card.

## Part 8: tests the audit asked for, not covered above

- **Orders sheet** (`test/order_view_test.dart`): a refused step shows the
  error and gives the buttons back; Delivered without proof sends JSON, not
  a form.
- **Order screen** (`test/trade_orders_page_test.dart`): Keep the order,
  Agree their count, Cancel confirmed with Yes; no Answer card without
  `storefront.manage`, no terms without `link.manage`; the over-limit
  notice's words; a drafted line from our own item sends
  `{item_id, quantity}` and no price; Record the delivery disabled while a
  box is empty.
- **Customers' orders** (`test/storefront_orders_page_test.dart`): riders
  off (409): no Delivery card, the Deliveries empty state; the hidden
  details' words without `pii.reveal`; no Undo without `invoice.manage`;
  the From filter with two fronts sends `front=`; Paid in cash sends
  `{"how":"cash"}`; the held and asked-owner offer words; the Deliveries
  screen and the rider sheet at large text.
- **Customer order card** (`test/customer_orders_test.dart`): Not sent to
  them and Send it again (`POST …/tell`) with a stub that answers the
  confirmed order; a 409 on a stale version (the error, then read again);
  Save of the address and fee; Drop; setting a price; Add an item; Start an
  order refused.

## Order of the work

1. Part 1 (kit: radio rows, polling) — one commit.
2. Part 2 (orders from linked businesses).
3. Part 3 (customers' orders and deliveries).
4. Part 4 (the customer's order card).
5. Parts 5 and 6 (Availability, stock counts).
6. Part 8's remaining tests, the full suite, push; this file's "Where it
   stands" filled in.
7. Part 7 (Messages about it), its own commit, after the fixes.

## Left as it is, and why

- An order's invoice, a buyer's statement, the alerts' Reorder and Match
  it, paying riders: money, M8. They say they are coming.
- Reading a count from a CSV: the web's; a phone counts the shelf by hand.
- The riders' own page (adding riders): its own part, not a fix; listed for
  M8 with paying riders.
- The briefing's Owed, Staff and Tasks tiles: their screens are M8 (Owed)
  and the tasks and staff screens (not yet in a milestone of the app; to be
  placed when this plan is done).

## For the web (the same flaws, to fix there)

- A cashier's Accept answered "waiting for the owner" is shown as accepted
  (`ChatToolForms.tsx` TradeOrderView) — 2.1.
- Accept offered on a changed order (`ChatToolForms.tsx`) — 2.2.
- `arrived` has no word or step in the Orders sheet — 2.3.
- A masked rider phone offered as a call link (`Dispatch.tsx` OrderDelivery)
  — 3.4.
- Refund, Confirm the fee and the delivery settings shown to people the
  server refuses (`StorefrontOrders.tsx`, `Dispatch.tsx`) — 3.6.

## Where it stands

| Part | Done |
|---|---|
| 1 | 1.1, 1.2: a choice row says it is checked (from its own radio or checkbox, every screen); the `Polling` mixin on every screen that reads again (`lib/kit/polling.dart`) |
| 2 | 2.1 to 2.7; the shell's Linked businesses and Explore opened from a tool's screen through `lib/app/openers.dart`; `openOrSay` says when a call, map or mail cannot open |
| 3 | 3.1 to 3.16; the list opened from Orders from linked businesses ("All your orders"); `TkRow` gained a tone |
| 4 | 4.1 to 4.7; `KeyboardRoom.up` says when the keyboard is up; a refused Start an order keeps its words (found by the tests) |
| 5, 6 | 5.1 to 5.4, 6.1 to 6.4; Availability's tile read at once after a change (it waited until five seconds old: found by the tests); text boxes can be disabled |
| 8 | Every test listed, and one for each fix |
| 7 | Messages about it on the order screen (`lib/chat/doc_thread.dart`): the thread's cards and words, read again every 10 seconds while seen, a reply to its latest card for `chat.trade` (sent once, with a client id); the same section for purchases and bills comes with their screens (M8). The full suite: 745 passed |
