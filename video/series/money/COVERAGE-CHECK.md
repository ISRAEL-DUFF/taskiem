# Money and stock series: coverage check (phone app)

Date: 25 November 2026. Read-only check of the phone app (`mobile/lib/chat/*.dart`,
`mobile/lib/screens/*.dart`, `mobile/test/*_test.dart`) against `video/series/coverage.md`,
`FILM-GUIDE.md` (the founder's rule: every step shown is one a viewer can repeat in the real
app by real taps; never invent a screen) and the docs. `claude/busy-lovelace-brevob` was
merged into this worktree first. Nothing in `mobile/lib` was changed.

**What I did not do.** I did not run Flutter, any test or any film (no render, no dry run).
Everything below is from reading code, tests and docs. Where I could not confirm something it
says **Unconfirmed**. "Built" means the screen exists in `mobile/lib` and an app test drives it;
the server side was read only in `api/chat_actions.go` (the menu) and the docs.

## Read this first: five findings that change the plan

1. **`film_pay_test.dart` takes a route a viewer cannot repeat.** It puts "Pay someone" under
   the Today menu's **Money** topic. The real server (`api/chat_actions.go` line 377) puts
   `pay.person` in its own topic, **Pay** (id `pay`), shown only to people with `money.pay`
   in a business that has a payments account (`payMay`). The stand-in's catalogue (`_catalogue`
   in that file) is stand-in-only. Any new Pay film must use a catalogue with a **Pay** topic.
2. **Today's menu cannot open the Payments, Payroll or Payees pages.** On Today the menu is
   built with `onPage: (_) => false` (`today_tab.dart` lines 459 and 678), so "Your payments",
   "Payroll", "Your payees" run as chat actions and answer in a sheet. The pages open from the
   **Taskiem chat**: Open tools, then the topics menu, then Pay (`taskiem.dart` line 920
   `onPage` calls `_openPage`, which maps `/payments`, `/payroll`, `/settings/saved`, `/money`).
   Pay someone and Pay an account number do work from Today's menu (they open the Pay sheet:
   `menu_card.dart` line 241).
3. **`statement_page.dart` is not a bank statement.** It is a linked business's statement
   (what we owe each other, with terms and credit limit). The bank statement upload is
   `MoneyStatementsPage` inside `money_page.dart` (Money, then Bank statement).
4. **Payroll is in the app but not live.** The app has the whole Payrolla front (P4a to P4b-5),
   merged into this branch. The docs say "not yet in production" (waits for Payrolla to
   register Taskiem and its production sign-in address, `chat-pay-people.md` line 313), it is
   built and tested only against a **fake Payrolla**, and real money waits for D6. `coverage.md`
   still says payroll is "not yet on main"; that is out of date for this branch (see the
   payroll section). Founder said Payrolla pays customers' staff through iswallet already, but
   that is Payrolla's own product; Taskiem's front is not yet connected to a live Payrolla.
5. **Several steps need the phone's own system screens**, which a test cannot draw: the
   passkey/fingerprint prompt (the film adds a drawn overlay, `Film.system('biometric')`),
   the camera and photo gallery (photo reading), the file picker (statement upload) and the
   share sheet (receipts, downloads). A viewer will see their own phone's screen at those
   moments; the film can only show a stand-in or cut.

## How a person reaches each screen (the real routes, from the code)

| To reach | Real taps | Evidence |
| --- | --- | --- |
| Pay sheet (Pay someone) | Today: scroll to "What would you like to do?", tap the **Pay** tile (only the first 7 topics are tiles; the rest are under "All topics"), tap **Pay someone**. Or Taskiem chat: Open tools, topics menu, **Pay**, **Pay someone** | `api/chat_actions.go` 115, 377; `today_tab.dart` 661; `menu_card.dart` 241 |
| Payments page (list, limits, Pay several people, Payroll row) | Taskiem chat: Open tools, topics menu, **Pay**, **Your payments**. Or the link Taskiem sends in the chat (`/payments/<id>`) | `taskiem.dart` 675, 920; `main.dart` 675 |
| Saved accounts ("Your payees") | Taskiem chat: Open tools, topics menu, **Pay**, **Your payees** | `taskiem.dart` 678 |
| Money page | Bottom tab **Money**. Or Open tools, **Money** tile | `main.dart` 756; `tool_sheet.dart` 41 |
| To match | Money tab, the **To match** tile | `money_page.dart` 498 |
| Leaks, Bank statement, Cash and expenses, Fees, Bills, Invoices, Labels | Money tab: **Leaks** tile, "Go to" rows, Settings row | `money_page.dart` 487 to 553 |
| Things we have | Money tab, "Go to"; or Stock page, "Kept, not counted" | `money_page.dart` 397, 543; `stock_page.dart` 351 |
| Stock page | Taskiem chat: Open tools, **Stock** (or **Products**), "Open stock" | `tools.dart` 320; `tool_sheet.dart` 34 |
| Count the shelf, Record stock, Order what is low, Goods came in | On the Stock page: "Counts and losses" card, stock rows, "Orders to suppliers" card | `stock_page.dart` 1253, 907; `purchases_page.dart` 935, 973 |
| Suppliers page | Open tools, **Suppliers** tile (`purchases`) | `tool_sheet.dart` 42 |
| Pay it back | Orders tab: an order in **Refund due**, paid by transfer, signed in as owner with `money.pay` | `storefront_orders_page.dart` 847 |
| Payroll | Taskiem chat topics menu, Pay, **Payroll** (needs `payroll.read`); or the Payroll row on the Payments page | `chat_actions.go` 389; `payments_page.dart` 281 |

## Topic 1. Paying people

Business story default: Ada's Kitchen (Ada Obi, Tunde the cashier, Bayo Rice Mills, Emeka the
rider). Food numbers: 20 bags of rice at ₦32,500 = ₦650,000 (Episode 7); Tunde's ₦450,000 to
Bayo needing Ada's OK (Episode 10, already filmed).

| Piece | Built in phone app? | Real route | Test to copy (stand-in) | Filmable | Already filmed | Caveats |
| --- | --- | --- | --- | --- | --- | --- |
| Pay sheet: balance, Add money, fee, balance after | Yes: `pay_sheet.dart` `PayBalanceRow`, `payFeeWords`, `payBalanceAfter` | Pay, Pay someone (above) | `test/pay_finished_test.dart` "an account typed: the balance and Add money, the fee before paying..." (own `_Api`, `FakePasskeys`, opens `PaySheet` directly); route/menu from `film_pay_test.dart` | Yes, with the stand-in `GET /v1/payments/balance` | No | Wallet route says "Taskiem wallet · arrives at once, no fee"; bank route "₦X + ₦25 fee" is whatever the stand-in quotes. No "Test: no real money" label exists on the Pay sheet (only on Payroll settings), so a Test connection looks like Live |
| Pay one person (`@bayo`, amount, what for, Next, bank's name large, first-payment warning, Pay) | Yes: `pay_sheet.dart` | Pay, Pay someone | `test/film_pay_test.dart` (a film, with `_FilmPasskeys`) and `test/pay_test.dart` "Pay someone: @name chips..." | Yes | Test written (`film_pay_test.dart`, "pay-a-supplier", 12 Nov, Ada and Bayo, ₦156,000). **Not in any series `films.json` or episode**; I found no render. Uses the wrong topic (finding 1) | Fix the stand-in catalogue to the Pay topic. The bank's name check, balance and fee are recorded answers, not a real wallet. Fingerprint is a drawn overlay |
| Pay an account number (not saved) | Yes: `pay.account` (owner only) | Pay, Pay an account number | `pay_test.dart` "Pay an account number: 10 digits, the bank from the list..." and "an account number with no bank: the likely banks first..." | Yes | No | Owner only (`chat_actions.go` 384) |
| Pay a bill by transfer | Yes: `pay_transfer.dart` (ways sheet; the transfer card: bank, number, name, each with Copy, exact amount, countdown, follows the payment) | Money tab, Bills, a bill, **Pay**, "By transfer" | `pay_transfer_test.dart` "the bill's page: Pay opens the ways; an open number shows on it" (line 596) and "Pay: the ways in a sheet; by transfer..." (241) | Yes | No | The one-time account number comes from iswallet; the stand-in shows a made-up number. "Copy" writes the clipboard (not visible). Paid state needs the answers to change as the film runs (see `AdaWorld` in `film_ada_ep45_world.dart` for how). Pay "from Taskiem" on a bill is a second way (`money_sheets_test.dart` "a bill: paid from Taskiem") |
| Pay from a message (`pay_chip`: "Pay this account" under a message with a 10-digit number) | Yes | Chats tab, a **business-to-business (link) chat** or a customer chat, a message holding the account, **Pay this account** | `conversation_test.dart` "pay from a message: account details get Pay this account..." (line 323, fixture `chat-tunde`, message "Send 45k for the tomatoes. 0123456789 GTBank Chidi Okafor") | Yes (rename to Bayo) | No | Shown only to people with `money.pay` in a business. Staff see "Ask an owner to save it first" for unsaved accounts |
| Save as `@name` (beside the chip) | Yes: `pay_save.dart` | Same chip, **Save as...** | `conversation_test.dart` line 473 (light and dark) | Yes | No | Needs `saved.manage` |
| Batch from messages (`pay_collect`: "Add to the list", "N to pay" foot, Pay these) | Yes | Chip's **Add to the list**; foot of the chat "3 to pay"; Pay these | `test/pay_collect_test.dart` (lines 345, 407, 594) | Yes | No | List lives on the server (stand-in keeps it) |
| Pay several people (`pay_many`: Saved, Riders, Staff, Paste, From a photo of a list, From your list; Check; one passkey; failed ones again) | Yes | Payments page, **Pay several people** | `test/pay_many_test.dart`: pasted list (168), riders (581), staff (681), a batch sent and the owner's OK (370) | Yes | No | "Up to 100" is server-side. Staff rows need a linked saved account. Paste uses the phone keyboard; a film can type it |
| Saved accounts, name checked with the bank | Yes: `saved_accounts.dart` | Pay, Your payees, **Save something**, A bank account | `saved_accounts_test.dart` (line 161: bank search, "ADA OBI STORES" shown before saving; 244: owner with no payments account is offered a Taskiem wallet) | Yes | No | Showing a number in full or changing the account asks for a passkey. The "Open a Taskiem wallet" step asks for an email and the director's BVN: do not film with real details |
| Read account details from a photo | Yes: `photo_text.dart` (read on the phone with ML Kit; only the words go to Taskiem) | Pay sheet, **From a photo**, Take a photo or Choose from your photos. Also a chat photo's **Pay this account**; Pay several, **From a photo of a list** | `photo_text_test.dart` (211, 316, 408), `photo_ai_test.dart` (Read it with AI) | **Partly.** The sheet and the filled-in result can be filmed; the camera or gallery and ML Kit are stand-in hooks (`photoText`, `pickPhotoToRead`, `photoTempDir`) | No | The viewer sees the OS camera or gallery in the real app and we cannot. Film would cut from "Choose from your photos" to the filled sheet and must say so. Handwriting needs an owner to turn on AI reading |
| Passkey, PIN fallback, second proof | Passkey step-up on every confirm: yes (`app/passkey.dart`, `pay_sheet.dart`). PIN fallback inside its limits: yes. Owner's OK for a payment over a limit or to a changed account: yes (`approve.dart`, Needs your OK) | Pay sheet, Pay; Taskiem chat or Today, Needs you, Approve | `passkey_test.dart` (`FakePasskeys`), `pay_test.dart` 324, 354, 417 (PIN pad), `confirm_proof_test.dart` | Yes (PIN pad is a real app screen; passkey is an overlay) | Owner's OK already filmed: `food-approve-a-payment` (Ep 10) | `screens/second_proof.dart` is **not** payments: it proves a new phone at sign-in. Do not use it for money |
| Receipt and the diary line | Yes: `pay_diary.dart`; receipt link in `pay_sheet.dart` | After paying, the Taskiem chat shows "Paid Bayo Rice Mills ₦650,000 (stock bought) · GTBank ••6789" and how it stands | `pay_finished_test.dart` 238 (receipt, Try again) and 288 (diary line) | Yes for the diary line; **receipt goes out through the OS share sheet** (`SharePlus`), not filmable | No | "Received by the bank" needs the stand-in's answer to change after Pay |
| Pay it back (refund by transfer) | Yes | Orders tab, an order in Refund due paid by transfer (not card), owner with `money.pay`, **Pay it back**, then **Mark refunded** | `pay_finished_test.dart` 343 (sheet opened directly); the page: `storefront_orders_page_test.dart` 262 (refund_due, Mark refunded) | Yes, from the order page (real route) | No | Button hidden for staff and for card-paid orders (they get "Refund through Paystack"). Needs an order fixture in `refund_due` |
| Payments page: statuses, limits per role | Yes | Pay, Your payments | `pay_test.dart` 535, 652 | Yes | No | Owner-only limits |

## Topic 2. Matching and reconciliation

| Piece | Built in phone app? | Real route | Test to copy | Filmable | Already filmed | Caveats |
| --- | --- | --- | --- | --- | --- | --- |
| Money page (period, bars, by channel, fees, invoices paid, collected for others, CSV via share sheet) | Yes: `money_page.dart` | Money tab | `money_page_test.dart` "the overview..." (217) | Yes | Indirectly: Today's money card in `food-know-your-numbers` (Ep 9). The Money page itself: no | CSV goes to the OS share sheet |
| To match (list of unmatched payments) and match to an invoice | Yes: `invoice_match.dart` (suggestions with reasons, Confirm, Not this one, match by hand, split by hand, split across invoices, rest as credit, a match held for a second person) | Money tab, To match; or Open tools, Money, To match | `invoice_match_test.dart` (189 to 297); film set-up `film_ada_ep45_world.dart` (`AdaWorld`) | Yes | **Yes**: `food-match-payment` (Ep 4, ₦37,500 from OKONKWO C to INV-0014), plus fashion and gadgets copies | Existing film uses the chat's Money tool sheet (`money_sheets.dart`), a different screen from the Money tab's To match page. Split payments and "Match strong suggestions automatically" are not filmed. Auto-match setting is on Invoices, Settings |
| Different sources: bank statement upload | Yes: `MoneyStatementsPage` | Money tab, Bank statement, type the account (e.g. `gtbank/current`), **Choose the file**, Upload statement; columns matched if the file is in the bank's own format; "Transfers to confirm" (Same money / Different money) | `money_page_test.dart` "a statement refused for its columns: matched by hand, sent again" (292, hook `pickStatement`) | **Partly**: **Choose the file** opens the OS file picker, replaced by the `pickStatement` hook | No | CSV only (no PDF). Cut at the picker and say so |
| Cash, expenses | Yes: `MoneyEntriesPage`, plus chat tool Money `money_sheets.dart` | Money tab, Cash and expenses | `money_page_test.dart` 357; `money_sheets_test.dart` 265 | Yes | No (Ep 3 handover counts cash, not this page) | Over a limit it waits for a second person (server) |
| POS and wallet money | **No dedicated screen.** It arrives from the server (`pos.sale`, iswallet credits) and shows as channels in "By channel", in Today's money card and in To match; a counter sale paid by transfer or POS waits for its money on the Stock page | Money tab, By channel | No test covers POS or wallet specifically; channel names come from the stand-in's `money/summary` | Only as a number in the stand-in's channels | No | Do not claim the app has a POS screen. It does not |
| Your account number ("Receive", Taskiem wallet) | Yes: `_ReceiveCard` on Money | Money tab | `money_page_test.dart` 411 | Yes | No | Stand-in account number |
| Leaks (assign, close as recovered, written off, not a leak) | Yes: `MoneyLeaksPage` | Money tab, Leaks tile | `money_page_test.dart` "a leak assigned, then closed as recovered" (267) | Yes | No | Leaks are found by the server; the stand-in supplies them |
| Money reports | **Partly.** On the phone: the Money overview, CSV exports (money log, invoices, accounting journal) through the share sheet, the morning briefing and Today. The **daily summary in chat** (WhatsApp from 08:00) and the **monthly email** are server messages; the app has no setting for them (I found no "Money summaries" row in `mobile/lib`) | Money tab; Download button | `money_page_test.dart` 217 | Overview yes. The WhatsApp message and email: no, not the app | Briefing: `food-know-your-numbers` | Notification choices (Settings, Notifications, Money summaries) exist on the web only, as far as I can tell. **Unconfirmed** |
| Spending by label | Yes: `spending.dart` card on Money, page by month | Money tab, Spending card; Settings, **Labels** (`labels_page.dart`) | `things_page_test.dart` 257; `labels_page_test.dart` 52, 71 | Yes | No | Past spending is not rewritten when a label is renamed |
| `statement_page.dart` (a linked business's statement) | Yes (different thing, see finding 3) | A supplier's or buyer's page, **Statement** | `statement_page_test.dart` | Yes | No | Not the bank statement |
| Claims ("Families say they paid") | **Only in Schools** (`school/students_page.dart`). Not in To match on the phone | Schools, a student | `school_test.dart` | Only for Bright Hill | Not in `school-get-paid` | Out of scope for Ada |
| The money log itself | **No screen.** Server record, a kept table `money_log`, CSV on Money | n/a | n/a | Not as a screen | No | Say "the record of every naira" only in words, never show a screen that does not exist |

## Topic 3. Stock apart from products

| Piece | Built in phone app? | Real route | Test to copy | Filmable | Already filmed | Caveats |
| --- | --- | --- | --- | --- | --- | --- |
| Stock page (today's sales, what is low, each stock item, history) | Yes: `stock_page.dart` | Taskiem chat, Open tools, Stock, Open stock | `stock_page_test.dart` "what the page shows" (174); `stock_items_fixture.dart` | Yes | Not as a page. Related: `food-put-goods-in` (Products, sizes with counts), `food-reorder` (low-stock card) | The fixture's items are Indomie and cartons, not Ada's: rename to rice, flour, oil |
| Stock items you count but never sell (material; "Add something to count", **Do you sell it?**) | Yes: `stock_items.dart` | Stock page, **Add something to count** | `stock_page_test.dart` 491 (No makes a material, Yes a product with a price); 436 (All, Sold, Not sold) | Yes | No | Needs `table.edit`. The Today menu also has `stock.new` ("Add something to count") as a chat form |
| Counts ("what went missing") | Yes: Counts and losses card, **Count the shelf**, why, "₦25,500 missing", a count limit | Stock page, Count the shelf | `stock_page_test.dart` 267, 404 | Yes | No | The screen says "missing", "lost", "fewer": there is no "what went missing" title. Count by scanning: camera is a hook (below) |
| Barcode (set on an item, scan to count) | Yes: `_CountForm`, `code_scanner.dart` | Count the shelf, **Scan to count**; a code no item has is said which item and kept on it | `stock_page_test.dart` 294, 360 (`StandInCamera`) | **No for the camera.** Everything around it yes | No | The OS camera view cannot be filmed. Cut or skip |
| Labels | Two meanings. **Spending labels**: yes (`labels_page.dart`, label picker on a purchase line). **Printed shelf or product labels**: **not in the app**, and I found no feature for it in the docs | Money tab, Labels; a purchase line's Label | `labels_page_test.dart`; `purchases_page_test.dart` 229, 259 | Spending labels yes | No | Do not promise barcode labels or printing |
| Purchases of anything, with a label and a use ("How should we treat it?": Add to stock / Keep track of it / ...) | Yes: `purchases_page.dart`, `purchase_view.dart` | An order on its own screen (Orders tab, a purchase; or the link in the chat); lines confirmed with their treatment and label | `purchases_page_test.dart` 229, 259, 531, 614 (crates into bottles); `purchase_view_test.dart` 112 | Yes | No | The treatment question is asked when a **linked supplier** drafts an order or an order is made by hand |
| Orders to suppliers: **Order what is low**, send, Mark confirmed, **Goods came in**, record a payment | Yes (the Stock page's "Orders to suppliers") | Stock page, Orders to suppliers | `purchases_page_test.dart` "Supplies: what is low ordered; goods in; a payment" (342); `purchase_view_test.dart` 138 | Yes | **Reorder only**: `food-reorder` (Ep 7: Reorder 20 from Bayo). Goods came in: no | The same fixture family (`treat_fixture.dart`) is used by `film_food_reorder_test.dart` |
| Receiving ("all came" or how many did, our count) | Yes | Purchase page, Goods came in; or Stock page | `purchase_view_test.dart` 138; `purchases_page_test.dart` 324 | Yes | No | |
| Supplier items ("what you buy from them, remembered") | Yes: `supplier_items.dart`, Suppliers page | Suppliers tile, a supplier, their items | `purchases_page_test.dart` 727 | Yes | No | Applies to orders drafted after the change |
| "Things we have" (kept, not counted) | Yes: `things_page.dart` | Money tab, Things we have; or Stock page, Kept, not counted | `things_page_test.dart` 90, 199, 232, 323 | Yes | No | Sold with a price becomes money in (needs `invoice.manage`) |
| "Things we have" for stock-only staff | Yes: `thingsOnly(auth)` = `table.read` without `invoice.read`; their Money tab shows only that row, "The money itself is for those who manage it." | Sign in as that person; Money tab | `things_page_test.dart` 342 | Yes (cut to the staff phone; allowed by the guide) | No | Needs a second person's fixture |
| Sales that move stock, evening total | Yes (Record a sale on Stock; `food-sell-at-counter`) | Stock page, Record a sale | `sell_sheet_test.dart`, `stock_page_test.dart` 205 | Yes | **Yes**, `food-sell-at-counter` (Ep 3) | Not part of this series |

## Topic 4. Payroll on Payrolla

| Piece | Built in phone app? | Real route | Test to copy | Filmable | Already filmed | Caveats |
| --- | --- | --- | --- | --- | --- | --- |
| Payroll page (runs, state, who was not paid and why), run page, staff in Payrolla | Yes: `payroll_page.dart` | Taskiem chat topics menu, Pay, **Payroll**; or Payments, Payroll row | `payroll_test.dart` (209, 276, 330) | Yes, on a stand-in | No | Needs `payroll.read` (owner only by default) |
| Connect Payrolla, Reconnect, Link my Payrolla account by one code, who can approve | Yes (`payroll_settings.dart`) but **connecting and reconnecting happen on the web** (Payrolla sends the browser back there) | Payroll, Payroll settings | `payroll_settings_test.dart` | The linking by code, yes. Connecting, **no** (a web step, and live registration pending) | No | "Connected · Test, no real money" is the only Test wording |
| Team, Add someone (bank checked), change pay, end employment, Account changes approved with a code, My pay | Yes (`payroll_team.dart`) | Payroll, Team and Account changes; staff phone: My pay | `payroll_team_test.dart` (160, 254, 322) | Yes, on a stand-in | No | The bank name shown before saving is a recorded answer |
| This month's payroll: prepare, breakdown, bonus or deduction, leave out and put back, Recalculate, wallet against total, Approve / Reject / **Lock and pay** with Payrolla's one-time code | Yes (`payroll_run.dart`) | Payroll, Prepare November payroll, the run | `payroll_run_test.dart` (226, 256, 342) | Yes on a stand-in; the **code is typed from a phone, email or authenticator** that is not on the screen | No | Preparer cannot approve (second person needed: two phones). **No Taskiem passkey on payroll**: Payrolla's own code replaced it (decision 0074). Do not show the money-pay passkey here |
| After locking: Payment, shortfall, Pay now, Pay the failed ones again, payslips (card, Save as PDF, Send a link), My pay | Yes (`payroll_run.dart`, `payslip.dart`) | The run's page; payslip from the run; staff phone My pay | `payslip_test.dart` (233, 267, 295, 319, 337) | Yes; **PDF and link go out through the OS share sheet** | No | A payslip shows pay figures: use invented names and amounts |
| Settings and the PAYE, pension, NHF, NHIS schedules | Yes (`payroll_schedules.dart`): settings read-only with "Change in Payrolla"; pension PINs masked until a passkey shows them; Download via share sheet | Payroll, settings; Tax and pension | `payroll_schedules_test.dart` (138, 154) | Yes on a stand-in | No | This one does use a passkey (to show masked PINs) |

**Is it safe to film payroll?** Not yet as a public how-to, for these reasons (all from docs):
- "Not yet in production, waits for Payrolla to register Taskiem" (P4b-1, `chat-pay-people.md`
  313). All five slices are "built against the fake Payrolla", never real money; "live waits
  for Payrolla's registration and its production sign-in address".
- Money screens here are not covered by the **D7 security review**, which is "to be done before
  launch" (`build-plan-saved-tools-payments.md` D7, `build-plan-gaps.md` `g-saved-review`).
  `coverage.md` also says I do not know if it was done. **Unconfirmed**; it is the founder's to
  confirm. This applies to Topic 1 as well as payroll.
- Payroll is the only place where the approval is not a Taskiem passkey. A film would teach a
  flow whose last step (typing a code from another factor) cannot be shown on the same screen.
- The "Still in Payrolla" list (changing settings, correcting a locked run, cancelling an
  approved run, changing a phone number) means a how-to must end with "do this in Payrolla".
- Payslips and schedules show people's pay: use stand-in names only.

Recommendation: one **held** film (below) and no payroll in the public cut until the founder
says Payrolla's registration is live and D7 is done. The stand-in can be filmed now for the
founder's review.

## Existing films that touch this series (so we do not repeat them)

| Film | Series | Covers |
| --- | --- | --- |
| `food-match-payment`, `gadgets-match-payment`, `match-payment` | Ep 4 of each | One payment matched to one invoice from the chat's Money tool |
| `food-send-invoice` | Ep 4 | Invoices |
| `food-approve-a-payment` (and gadgets, fashion) | Ep 10 | The owner's OK for a payment (Needs your OK card); not the Pay sheet |
| `food-reorder` | Ep 7 | Low-stock card, Reorder 20, place the order with a supplier's price list |
| `food-put-goods-in` | Ep 2 | Products and sizes with counts |
| `food-know-your-numbers` | Ep 9 | Briefing and Today |
| `food-sell-at-counter`, `food-tasks-and-handover` | Ep 3, 6 | Counter sale, shift handover with cash counted |
| `pay-a-supplier` (`film_pay_test.dart`) | none | Test exists, not placed in any episode, wrong menu topic (finding 1) |
| `school-get-paid`, `school-debtors` | Schools | Account numbers, cash needing a second person |

Nothing covers: the Pay sheet in a published episode, pay from a message, batches, saved
accounts, the Money page itself, bank statement upload, leaks, cash and expenses, the Stock
page, counts, receiving, Things we have, payroll.

## Proposed films

Names `money-<name>`. Default story: **Ada's Kitchen** (Food series), same numbers. Each is one
flow of 20 to 60 s. "Test" is the app test to copy for the stand-in. Every route below is the
real one from the table above.

| # | Film | Flow (real taps) | Story | Seconds | Test to copy | Fix / caveat |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `money-pay-a-supplier` | Today, Pay tile (All topics first if it is not in the first seven), Pay someone, @bayo, ₦650,000, "stock bought", Next, read the bank's name, Pay, fingerprint, Sent; back in the chat, the diary line | Ada pays Bayo Rice Mills for the 20 bags she reordered | 50 to 60 | `film_pay_test.dart` + `pay_finished_test.dart` 148 | Rebuild the catalogue with a **Pay** topic; add balance and fee answers; fingerprint overlay is drawn |
| 2 | `money-pay-from-a-message` | Chats, Bayo's chat, his message with the account, Pay this account, sheet filled in, Save as @bayo | Bayo texts his new account number | 40 to 50 | `conversation_test.dart` 323 and 473 | Link chat fixture; pay step after Save as is the same as film 1, so stop at Next |
| 3 | `money-pay-a-bill` | Money tab, Bills, a bill from Bayo Rice Mills, Pay, By transfer, the transfer card, then paid | Ada pays a supplier's bill by transfer | 35 to 45 | `pay_transfer_test.dart` 596 and 241 | Paid state needs answers that change; Copy is invisible |
| 4 | `money-pay-the-team` | Chat tools menu, Pay, Your payments, Pay several people, Riders (Emeka) and Staff (Tunde), Check, one passkey | Friday pay: Emeka's deliveries and Tunde's wage in one go | 50 to 60 | `pay_many_test.dart` 581, 681, 370 | Staff must have a saved account linked in the fixture; held-out row shown honestly |
| 5 | `money-pay-it-back` | Orders tab, an order in Refund due (paid by transfer), Pay it back, sheet filled in, pay, Mark refunded | A customer's party tray was cancelled; Ada refunds ₦48,000 | 35 to 45 | `storefront_orders_page_test.dart` 262 + `pay_finished_test.dart` 343 | Needs an order fixture; owner only |
| 6 | `money-where-it-went` | Money tab: period, Money in and out, By channel, Leaks tile, open a leak, Assign to, Close as recovered | Ada checks the week and a leak | 45 to 60 | `money_page_test.dart` 217 and 267 | Leaks and channels are stand-in numbers; do not mention POS as a screen |
| 7 | `money-upload-a-statement` | Money, Bank statement, account `gtbank/current`, Choose the file, Upload statement, "Added N lines"; then To match | Ada uploads the bank's file | 35 to 45 | `money_page_test.dart` 292 | **File picker is a hook**: cut from the button to the chosen file name and say so; or skip until the founder decides |
| 8 | `money-add-something-to-count` | Chat tools, Stock, Open stock, Add something to count, flour, "Do you sell it?" No, unit and level, save | Ada adds flour: counted, never sold | 30 to 40 | `stock_page_test.dart` 491 | Needs `table.edit`; rename fixture items |
| 9 | `money-count-the-shelf` | Stock, Counts and losses, Count the shelf, type counts, reason ("rats"), Record the count, see what went missing in naira | Month-end count in the kitchen's store | 40 to 55 | `stock_page_test.dart` 267 | No scanning; barcode camera not filmed |
| 10 | `money-goods-came-in` | Stock, Orders to suppliers, Order what is low, Goods came in, all came, stock count goes up | The 20 bags of rice arrive | 40 to 55 | `purchases_page_test.dart` 342; `film_food_reorder_test.dart` set-up | Follows `food-reorder` (Ep 7): do not repeat its first half |
| 11 | `money-things-we-have` | Staff phone: Money tab shows only Things we have; open it, add "Standing mixer"; cut back to Ada's phone | A stock-only staff member records a mixer | 30 to 40 | `things_page_test.dart` 342, 232 | Two fixtures (owner, staff); the label is suggested from the name |
| 12 | `money-payroll-this-month` **(HELD, do not film for publication)** | Chat tools, Pay, Payroll, Prepare November payroll, breakdown, bonus, Approve with a code, Lock and pay | Ada runs November payroll | 55 to 60 | `payroll_run_test.dart` 256 | See "Is it safe to film payroll?". Needs two phones (the preparer cannot approve) and the code from another device |

### Episodes

| Episode | Title | Films | Why this grouping |
| --- | --- | --- | --- |
| M1 | Pay one person | 1, 2, 3 | The three ways in: the Pay tile, a message, a bill |
| M2 | Pay several, pay back | 4, 5 | Batches and refunds |
| M3 | Where the money went | 6, 7 | Money page, leaks, statement upload (cash and expenses is a spare short: `money_page_test.dart` 357) |
| M4 | What you buy | 8, 9, 10, 11 | Things you count, things you keep, and what goes missing |
| M5 | Payroll (held) | 12 | Hold until live and the security review is confirmed |

Matching one payment to one invoice is already `food-match-payment` (Ep 4): M3 should open
with a bridge to it rather than refilming it. Split a payment across invoices and auto-match are
possible later shorts from `invoice_match_test.dart` 221 and 249.

## Questions for the founder

1. **D7:** has the security review of saved accounts and paying people been done? We should not
   publish films of "pay from Taskiem" before. (Also, are the Taskiem-side limits and passkey
   steps on for every business?)
2. **Payroll:** is Payrolla's registration of Taskiem live in production? Until then film 12
   stays held. Is it fine for payroll to be Payrolla's code, not Taskiem's passkey, in a
   how-to? Can we film an owner and an approver on two phones?
3. **Pay topic route:** should Today's menu open the Payments page (today it answers in a sheet,
   finding 2)? A how-to is simpler if "Your payments" opens the real page from Today. That is an
   app change, so not mine to make.
4. **Test vs Live:** the Pay sheet shows no "Test: no real money" mark. Do we film in Test with
   a stand-in and say nothing, or add a caption "practice account"?
5. **System screens:** may a film cut at the fingerprint prompt, the file picker, the camera
   and the share sheet (with a caption), as the guide allows for a second person's phone?
   If not, films 7 and the photo-reading and receipt moments are cut.
6. **Shelf labels and barcode printing:** do you mean something I could not find? I found no
   printed labels in the app or docs; "labels" in the app are spending labels and the barcode is
   scanned, not printed.
7. **Notifications:** is the daily money summary and the monthly email controlled anywhere in the
   app? I did not find it. If it is web only, the "money reports" episode should say so.
8. **Story:** Ada's Kitchen for all of M1 to M4, or a second business for stock (a fashion house
   buying fabric is the docs' own example: Zuri Atelier, Alhaja Sade)?
9. **Film 1:** keep `film_pay_test.dart` as the base (fix its catalogue), or retire it?

## Things I could not confirm

- That any film in this plan runs: no tests were run, and no renders exist that I could find.
- Whether the real server shows the Pay tile within Today's first seven topics for a typical
  business; the order is in `chatTopics` (`chat_actions.go` 105), where Pay is the tenth, so
  it is likely under **All topics**, not a first-row tile. A film must tap "All topics" first.
  I read this from the code, not from a running server.
- How a person summons the topics menu in the Taskiem chat besides Open tools (typing "menu" or
  a topic word); the tools-panel route is the one I verified.
- Whether the Orders tab shows "Refund due" for a transfer-paid order in the real flow (the
  page and its tests do; the server rule is not checked).
- D7 and D6 status (docs say pending; no newer evidence).
