# Build plan: what the specs ask for that is not built yet

Status: **plan** (24 November 2026). Written from the testing checklist's "Not built yet" list (66 items) after PRs #214, #218, #220, #221, #222 and #224, with every item checked against the code on main (`6649686e`). Nothing here is built.

The earlier [build plan](build-plan-saved-tools-payments.md) ended with "Left" lines in many phases. Those lines are where things went missing. This plan has none: every item is either a task in a phase below, or listed in §12 with the decision or the outside thing it waits for. §15 maps all 66 items to where they went, so nothing can drop out again.

## 1. What the check found

| State | Items |
| --- | --- |
| Already built (only the gap list or a doc is out of date) | `g-scan-echo` (a scanned input reads as its text in a tool's echo), `g-menu-nocmd` (actions with no typed command are in the menus), the app's phrase chip (half of `g-ct-chip`), the PIN in WhatsApp's form (`g-doc-pin` is the doc only) |
| Partly built | `g-pay-first`, `g-pay-staff`, `g-old-cols`, `g-stock-lang`, `g-thing-suggest`, `g-menu-lang`, `g-pages-mine`, `g-ct-chip` (optional inputs) |
| Not built | Everything else, in §3 to §11 |
| Deferred by a decision, or waiting on something outside | §12 |

## 2. The rules every phase keeps

The rules of the earlier plan's §2 hold (suggest, don't surprise; show it before it acts; money and customer messages pass a gate; easy to see and stop). Added, because of how items were lost last time:

1. **A phase is done only when every task in it is done.** A phase does not end with a "Left" line. Anything that cannot be built goes back to the founder as a question, and is then moved to §12 with the answer written next to it, or stays open in the phase.
2. **Every surface the spec names, in the same phase:** the web, the phone app (light and dark, 360 px, `looks_test`, `sizes_test`, `targets_test`), and WhatsApp, Telegram and Slack where the spec says. A phase that builds the web only is not done.
3. **Words in every language.** New chat words go into `engine/lang/messages/en.json` (reviewed) and into ha, ig, pcm and yo as drafts (`"reviewed": false`, context starting "Draft, not reviewed:"), never as English placeholders.
4. **Tests for every task:** Go (with the database), vitest, Playwright (axe in both themes, nothing scrolling sideways at 390 px), Flutter widget tests and pictures looked at.
5. **A review before merging:** a reviewer reads the phase's diff for security (tenant isolation, roles, personal data, money gates) and correctness, and its findings are fixed in the same phase.
6. **CI green on the pull request,** including all six browser test shards.
7. **Docs in the same pull request:** the spec's status line, its "As built" section, the guide in `docs/`, the earlier build plan's §6 tick, and this plan's §15 row.
8. **Migrations** take the next free number when the phase starts (00590 is the last today). Phases built side by side are each given their own range when they start.

## 3. Phase H1: put the record straight (docs only) — done 24 November 2026

Small, first, and on its own, so the specs match the code before anything else is built.

| Task | Item |
| --- | --- |
| `docs/spec/saved-objects.md` status: S0 to S3 built, with dates and links to each "As built" | `g-doc-saved` |
| Earlier build plan §6: tick every merged phase (S0 to S3, F1, P1 to P4, I1 to I3, M0 to M5, D0 to D2, G0 to G3), and correct its M3 status (the app has the phrase chip) | `g-doc-plan`, `g-ct-chip` |
| `docs/spec/device-fields.md` status and §10: `to: "starter"` is built; tool inputs are still to do (H3), not blocked by M1 | `g-doc-device` |
| `docs/spec/chat-pay-people.md`: take "the PIN in WhatsApp's form" off the "Not yet" line | `g-doc-pin` |
| `docs/spec/chat-menus.md` M1 note: the rows with no command are in the catalogue | `g-menu-nocmd` |
| Mark `g-scan-echo` built in `device-fields.md` §16 | `g-scan-echo` |

**Done when:** each of these specs' status lines matches the code, and the checklist's items above can be marked built.

## 4. Phase H2: buying, labels and Things we have — done 24 November 2026

The spec is [what a business buys](what-a-business-buys.md). Five of these are against the spec.

| Task | Item | Details |
| --- | --- | --- |
| **Things we have for people who may see stock but not invoices** | `g-things-menu` | Today it sits only under Money, which needs invoice rights (`web/src/App.tsx` 513–516, 621–625). Give `table.read` holders their own way in on the web (a Things we have entry in the menu, and a Money page that shows only Things when invoices are not theirs) and in the app (Money › Go to already exists; check the same rule there). |
| **A suggested label pre-selected in the app** | `g-label-pre` | `mobile/lib/chat/treat_line.dart` `suggest()` picks only an existing label sharing a word. Pre-select the server's first suggestion; when it is not a label yet, make it on confirm. Same on the web's treat box if it differs. |
| **Merged labels show their kept label on order and bill cards** | `g-label-merge` | Cards look up `label_id` in a list that leaves merged labels out (`web/src/lib/treat.ts` 118–121, the app's `treat_line.dart` 128, `purchases_page.dart` 472). The server sends each line's label name, following `merged_into`, so the web and the app never resolve it themselves. |
| **A suggested label in Add something we have, on the web** | `g-thing-suggest` | `web/src/pages/Things.tsx` 226–231 passes the business's labels only. Ask `GET /v1/money/labels?item=` with the name, and pre-select on both surfaces (as `g-label-pre`). |
| **Search, a row's supplier, and the list on Stock** | `g-thing-extra` | Search on Things we have (web and app); a supplier field on a thing (migration: `kept_things` UPDATE grant gains `supplier_id`; `PATCH /v1/things/{id}` takes it; audited); Things we have reached from the Stock page (web `StorefrontStock.tsx`, app `stock_page.dart`). |
| **How many stock units a product uses, asked when ordering** | `g-uses-ask` | When a line is Add to stock into a stock item counted in other units, ask "how many does one use" (web `TreatLine.tsx`, app `treat_line.dart`, chat `api/chat_purchase_treat.go` 543–558), remembered with the item. Give the model (`api/chat_understand.go`) the buying question, so "add 10 bags of rice" in a reply is read as its answer. |
| **The old-columns warning reads expressions** | `g-old-cols` | `engine/stock/oldcolumns.go` 360–368 skips a select starting with `=`, and `set` is read from map keys only. Find column names inside CEL expressions (selects, filter values, `set`/`row` given as an expression, a column name passed as a string). Tests for each form. |

**Done when:** a staff member with `table.read` and no invoice rights opens Things we have from the menu on the web and the app; a merged label shows as its kept label on an old order card; the second order of an item treated before asks nothing and pre-selects its label; a product counted in grams asks how many grams one bag is, once; and a workflow writing the old stock column through an expression is listed by the warning.

Built: every task above, on the web, in the app and in chat; migration 00600. What was built and its tests are in [what a business buys](what-a-business-buys.md), "As built: H2".

## 5. Phase H3: device fields

The spec is [device fields](device-fields.md). Two of these are against the spec.

| Task | Item | Details |
| --- | --- | --- |
| **The app's Confirm card shows the pin, the photo and who signed** | `g-dev-confirm` | The web has it (`ChatAlerts.tsx` 395 `ProofOf`). Add it to the app's Confirm card in `mobile/lib/chat/alerts.dart`: the masked map pin, the photo, "signed by the customer", and which value the phone read and which was sent. Pictures in light and dark. |
| **The one-time page (by text or email) asks the device** | `g-dev-link` | `engine/publicweb/formfields.go` and `hostedforms.go` (`/r/{token}`) draw no device fields. Add location (the browser's prompt, an answer of refused or unavailable), photo (camera or file), signature (a pad), and scan (camera where the browser has `BarcodeDetector`, else typed). Same checks and audit as the app's answers, and the same content-security policy tests. |
| **Scanning by camera on the web** | `g-scan-web` | Where `BarcodeDetector` exists, scan with the camera in the web's form step and tool sheet; elsewhere typed, as today. Shared with `g-dev-link`. |
| **A scan typed on WhatsApp and Telegram** | `g-scan-wa` | Today a scan field is unavailable there. Ask for the code as typed words, and take only the next message in that chat as its answer, never a command word and never a bare number that a menu is waiting on (the rule M4 uses for typed answers). |
| **Location, photo and signature as a tool's inputs** | `g-scan-tool` | `engine/wd/devicefields.go` 227 refuses them. Allow them as a members' tool's top-level inputs: the validator, the tool sheet on the web and the app, the chat (WhatsApp location and photo as a form step does), care stays as worked out. Customers' tools keep refusing them unless the founder says otherwise. |
| **Erasing a person erases where they were** | `g-scan-erase` | `engine/pii` taint maps a value to a category only. Seal a location (and the copies taint makes of it) under the respondent's own subject, so erasing the person erases it. Needs a migration of how taint is stored and a test through a run that copies the location. |

**Done when:** a delivery confirmed on a rider's phone shows the pin, the photo and the signature on the app's Confirm card; a customer answers a location and a signature on the one-time page from a text; a stock code is typed on WhatsApp; an "Add a site visit" tool takes a location from the app's sheet; and erasing the respondent leaves no readable location in the run.

## 6. Phase H4: menus and search

The specs are [chat menus](chat-menus.md) and menus everywhere. Three of these are against the spec.

| Task | Item | Details |
| --- | --- | --- |
| **A typed command counts toward "most used"** | `g-menu-used` | `actionUsed` is called only for rows, forms and the model (`api/chat_menus.go` 293, 616; `chat_understand.go` 287). Call it when a command typed in full runs its action, in every chat. |
| **Topic words in each language** | `g-menu-words` | `topicHints` in `api/chat_actions.go` 532–550 is English and Pidgin in Go. Move topic words to `engine/lang/intents/*.json` (a `topics` key), drafts for yo, ig and ha, matched in the person's language first, then English. |
| **Search everywhere finds Saved things and Services** | `g-search-saved` | Add Saved (names, kinds, never sensitive values; the person's own and the business's) and Services to the server's search and to the web `SearchEverywhere.tsx` and app `search_everywhere.dart`, three each with Show more. |
| **Telegram: topics two to a row** | `g-menu-tg` | `engine/telegram/platform.go` 112 makes one button a row. |
| **Slack: five rows, then More…** | `g-menu-slack` | `api/slack.go` 126–146 puts every button in one block. |

**Done when:** "invoice" typed three times puts New invoice first in the menu; "owo" finds the Money topic in Yoruba; searching a saved thing's name finds it; Telegram's topic menu is two buttons to a row; and a Slack topic of nine rows shows five and More….

## 7. Phase H5: pages, the rest of G1 to G3

The spec is [pages](pages.md). Three of these are against the spec.

| Task | Item | Details |
| --- | --- | --- |
| **Mine on a phone column** | `g-pages-mine` | The drawing handles a phone already (`engine/pages/render.go` 259–266), but `check.go` 272–273 refuses anything but email, `api/pages.go` 772 never sets the viewer's phone, and the editor offers email columns only (`PageEditor.tsx` 728). Allow phone columns in all three. |
| **The app ticks a row in place** | `g-pages-tick` | The app opens the row today. Tick a yes/no cell through `PATCH /v1/tables/{id}/rows/{row}`, shown only with `table.edit`, as the web does. |
| **An estimate of a day's tool runs in a page's settings** | `g-pages-runs` | For a page with tool parts: views a day (from the last seven days) times its tool parts, against the plan's run limit, in the page's settings on the web. |
| **Buttons, tool parts, status, alerts, ticks and place in the plain-words editor** | `g-pages-editor` | The editor's Add a part offers count, sum, list, table and heading only (`web/src/lib/pages.ts` 470–476). Add buttons (a tool, a signal, an approval; `when`), a tool as a source, a status part, a figure as an alert, an editable yes/no column, and where the page shows (pinned, a tab under an area). No technical words; the plain-words test covers it. |
| **A page opened from a notification** | `g-pages-notif` | `notify.tell` buttons take a tool only (`connectors/notify/notify.go` 104–127). Add a page button (a page and its filters), drawn in each chat, opening the page in the app and on the web, only for people its roles let open it. SDK helpers regenerated. |
| **A row's own status** | `g-pages-rowstatus` | A status line on each row and card, for a run named by the row (a column holding a run id) or by the page's address, `run.read` rules as the page status. |
| **A page as a tab under Linked businesses** | `g-pages-tab` | Linked businesses gets a tab bar (web and app), and `place.area` accepts it (`engine/pages/pages.go` 365–370). |

**Done when:** a page of my tickets filtered by Mine on a phone column shows a staff member's own; the app ticks Done on a row; the deliveries fixture page is made from the editor alone, without a file; a notification opens a page filtered to today; each delivery row shows its run's status; and a page sits as a tab under Linked businesses.

## 8. Phase H6: made tools and saved things, the rest

The specs are [made tools](made-tools.md) and [saved objects](saved-objects.md).

| Task | Item | Details |
| --- | --- | --- |
| **On the web, a customer's answer kept in the conversation** | `g-ct-webkeep` | As the app's TheirRuns: after Yes the sheet closes and the answer lands in the person's chat with the business (`web/src/pages/PersonChats.tsx`, `PersonTools.tsx` 189–227), kept on reload. |
| **Optional inputs read from words** | `g-ct-chip` | `api/tools_read.go` 56–67 gives the model only what the chat form asks. Give it the optional inputs too, still shown before anything runs. |
| **Retire the separate public menu: a business's customer menu is its customers' tools** | `g-ct-oldmenu` | Decided 24 November 2026: nothing is in production, so nothing is carried over. Remove the public-menu list (`whatsapp_public_workflows`, its `command` column and the carry-over in migration 00569, `PUT /v1/whatsapp/public-menu`, the menu's settings page on the web and in the app, and the code reading both lists in `api/tools_customers.go`). **menu** on WhatsApp, and the app's and web's Their tools, list the business's customers' tools only, each with its answer. The `public_menu` switch stays as the business's choice to offer customers' tools at all, or goes if customers' tools have their own switch; the docs (`docs/tools.md`, `docs/whatsapp.md`, made tools §11, decision 0073 point 6) say so. A new migration drops the table; 00569's down stays reversible. |
| **Tool changes said in plain words** | `g-ct-later` | `engine/wdtext` says what changed in a tool (command, phrases, who it is for, the answer) in plain words, where the version history and review show changes. |
| **A test file stating the answer** | `g-ct-later` | `wd-test/v1` gains an expected answer (text, card fields, list rows) checked by `engine/wdtest`. |
| **A workflow sends a saved file** | `g-saved-wf` | `api/saved_runs.go` 131–134 gives a run the file's name and type only. Let a step attach a saved file (a message, an email), read at run start under the business's scope, never a person's own file. |
| **Each run's use of a saved thing recorded** | `g-saved-wf` | A row for each saved thing a run read (which, when, which step), shown on the saved thing's page and the run. Migration. |
| **Saved things in the SDK** | `g-saved-wf` | The SDK's `saved` root (types and helpers), as the server's `saved.*` expressions. |
| **An address on an invoice made with @name** | `g-saved-addr` | Invoices hold name, phone and email only. Add an address (sealed with the customer), on the PDF, the web and the app, filled from a saved person's or business's place. Migration of the customer envelope. |

**Done when:** a customer's answer stays in their chat after a reload on the web; "log 4500 diesel at Total" fills an optional "where" input; **menu** on WhatsApp lists only the business's customers' tools, each answering, and no public-menu settings remain; a review of a changed tool says "now also says 'spent'"; a wd-test fails when the answer changes; a weekly workflow emails `@price-list`; a saved thing's page lists the runs that read it; and `invoice @acme 250k` prints Acme's address.

## 9. Phase H7: paying one person, finished

The spec is [paying people from the chat](chat-pay-people.md). The first item is against the spec, and its own test for the phase expects the payment recorded. This is the largest of the H phases; it may be two pull requests, server first.

| Task | Item | Details |
| --- | --- | --- |
| **The balance and Add money on the Pay sheet** | `g-pay-core` (Settled 2) | The provider's balance; Add money shows the wallet's own account number. Web `PaySheet.tsx`, app `pay_sheet.dart`, the chat's pay card. |
| **The fee before paying** | `g-pay-core` (Settled 4) | "₦45,000 + ₦25 fee" and the balance after, before the passkey or PIN; the balance check includes the fee; the fee recorded as its own money-log line (bank charges). The `pay-person` template passes `fee_kobo` to finish (`api/payments.go` 1488 already takes it). The provider is asked for a quote; where it gives none, the fee shows on the result, as today. |
| **A diary line in the chat** | `g-pay-core` | "Paid Chidi Okeke ₦45,000 (delivery) · GTBank ••6789", then "Received by the bank", or "Failed: refunded" with Try again, in the chat it was paid from (`api/chat_diary.go`). |
| **A receipt to share** | `g-pay-core` | A receipt (a card and a PDF) the payer can send back into a chat. |
| **The money log knows what it was for** | `g-pay-core` | `engine/runtime/moneylog.go` 24 records a payout with no purpose. Record "what for" (bill, wages, delivery, refund, stock, other) so reports split by it. |
| **A card to the person paid** | `g-pay-core` (Settled 6) | Posted in the conversation with the payee once the provider confirms; updated if it fails or is reversed; the amount, what for, the date and the last four digits only; not for wages, an owner's own account or refunds to customers; **Tell them in the chat** a tick on the sheet, on by default; **Send them a receipt** for people not on Taskiem. |
| **Save as a beneficiary at the end of the sheet** | `g-pay-saveas` | On by default for a new account, with a name; saved as a bank-account saved thing. Web and app. |
| **First-payment warning on supplier approvals** | `g-pay-first` | `api/approvals.go` 93–105 needs the account in the subject; add `account_number` and bank to `supplier-payment-approval.json`'s subject (and `pay-riders-weekly`, `pay-a-list` if they approve). Payroll is Payrolla's, so its part no longer applies. |
| **Pay on a refund** | `g-pay-staff` | Staff and riders are built. Add Pay on a refund (an order's or an invoice's refund), opening the Pay sheet filled in with "refund". |
| **Paying several: fees, balance after, one diary line** | `g-pay-batch` | The batch's foot shows the total of fees and the balance after (short by how much, and which rows fit); one diary line for the batch (`api/payment_batches.go` 67 keeps its counts already). |
| **Taskiem wallet to Taskiem wallet** | `g-pay-w2w` | Where both sides have a Taskiem wallet: that route, "Taskiem wallet · arrives at once" on the sheet, falling back to the bank. |
| **A batch whose last row is refused at confirming still ends** | (found in H1: `chat-pay-people.md` "Not yet") | Today it stays prepared, so no message is sent until it ends. A row refused at confirming ends as refused, and the batch ends and is told when its other rows have. |
| **Forward or reply "pay" on WhatsApp, Telegram and Slack** | `g-pay-fwd` | A forwarded message, or a reply of "pay" to one, is read for an account as a typed one is: "Pay Chidi Okeke (GTBank ••6789) how much, and for what?". |

All of this is built and tested against the stand-in provider, as P1 to P4 were. Real money still waits for D6 (§12).

**Done when (the spec's own, in full):** an owner pays a rider ₦45,000 from Tools in under 30 seconds, sees the fee and the balance after before confirming, and it is recorded as a delivery in the money log with its fee as bank charges; the chat's diary line turns to "Received by the bank"; the rider sees a card in their chat; a changed account for a past payee is stopped for the owner's OK; and a forwarded message with an account number is answered with the name and "how much?".

## 10. Phase H8: languages

| Task | Item |
| --- | --- |
| The 34 stock and treat entries still English in ha, ig, pcm and yo (wa.treat.*, wa.stock.*, menu.stock.*, button.treat_*, button.yes_sell, button.no_sell) written as drafts | `g-stock-lang` |
| The 3 menu.stock.new entries; pcm's 60 menu entries identical to English checked one by one (some are right as Pidgin) | `g-menu-lang` |
| Every word added by H2 to H7, as each phase lands (rule 3 of §2) | All |
| Native speakers' review of all four languages | Outside the build (§12) |

## 11. The later milestones (stage 8 of the earlier plan)

These were planned and not started. They come after H2 to H8, in this order, as the earlier plan has it.

### M6: recipes and the AI builder for tools (`g-ct-m6`, the template part of `g-ct-later`)

- **The eight recipes** (made tools §13), each a template with its `tool` block, installed with a command taken at install: look up, add a record, list and act, ask for approval, tell the team, call a system, daily report, watch and act.
- **The template library** shows recipes with a tool (§17).
- **Describe it**, on the web and in the chat: drafts the workflow, its inputs, its outputs and the `tool` block together, read back in plain steps, saved as a draft; uses saved things by name ("every Friday send @price-list to last week's customers"); a tool for customers has only `public` outputs in its answer.
- **The AI builder's schema, property rules and `evals/builder` cases** for tools, in English and Pidgin.
- **Done when:** Describe it produces a working tool from one sentence, naming a saved thing.

### G4: board and chart (`g-pages-g4`)

- **Board:** a list in columns by a choice (Open, Doing, Done), fed by a table or a tool; moving a card to another column runs a tool (held to the tool's roles and care, as a page button is), on the web by drag and by keyboard, in the app by a menu on the card.
- **Chart:** bars or a line, one measure over time or by a group, fed by a table's sum by period or a tool's list output with a label and a number; drawn on the server's figures, accessible (a table behind it), in both themes; in the app too.
- **Done when:** a ticket dragged from Open to Doing runs its tool, and expenses show by week.

### G5: starters, Describe it and saved things as a source (`g-pages-g5`)

Needs M6.

- **Starters:** Tickets desk, Expenses this month, Leave, Stock count, Deliveries today, Waiting list, Service log; each brings its table and tools with it.
- **Describe it** for pages: drafts the page and names what it would create; the AI sees table and tool names and types only.
- **Saved things of one kind as a source:** a list of a made-up kind ("Generators") with dates coming up.
- **Done when:** Describe it makes a working page from one sentence.

## 12. Not built by this plan, and what each waits for

| Item | What | Waits for |
| --- | --- | --- |
| `g-pay-wallet` | Real money (everything runs on the stand-in provider) | D6: a provider in test mode (the Taskiem wallet, iswallet) |
| `g-saved-review` | Security review of sharing sensitive details (and S0 with P1) | D7: a reviewer outside the build; must happen before launch |
| `g-photo-phone` | Photo reading tried on a real phone | A person with Android and iPhone devices; the testing checklist's photo items close it |
| `g-pay-payroll` | Approving, adjusting and payslips inside Taskiem; payslip PDF | Payrolla's API offering them |
| `g-pay-momo` | Mobile money by phone; payroll software other than Payrolla | Founder: comes later |
| `g-recipes` | Making something uses up its materials | Founder chose counting by hand for now; its own plan later |
| `g-own-purchase` | An owner moving their own purchase into the business | Founder: a business buys as the business |
| `g-deprec` | Depreciation | Founder: the list only, for now |
| `g-saved-personal` | Paying from your personal space | Founder: not planned |
| `g-pages-cust` | Pages for customers and linked businesses | Founder: not in the pages plan |
| `g-ct-linked` | Tools for linked businesses (M7, its plan is written) | Founder: after members' and customers' tools are in use |
| `g-ct-files` | Files as answers, dates from a calendar, tools for embedded builders | Founder: not in the made tools plan |
| `g-ct-tg` | Customers' tools on Telegram and Slack | One bot for everyone, no menu per business; needs its own design |
| `g-ct-waform` | A partly filled tool as a filled WhatsApp form | WhatsApp forms take no starting values |
| `g-scan-sense` | Sensing without a tap | Founder chose: ask for now |
| `g-menu-later` | A row's last values; a menu in a customer's own chat; a spoken menu | Founder: later |
| `g-search-notnow` | Status chips, recent first, menus outside Taskiem's conversation on WhatsApp | Founder: not now |

Any of these can be moved into a phase by the founder's word; then it gets a task row and a done when, like the rest.

## 13. Order, and what can run side by side

| Wave | Phases | Why |
| --- | --- | --- |
| 1 | H1 | Small; the specs match the code before building |
| 2 | H2, H3, H4, H5, H6 side by side | Different areas, different files; each its own migration range |
| 3 | H7 | Payments touch the chat diary, the money log and the Pay sheets in both apps; built alone so its review is clear |
| 4 | H8 | The words from waves 2 and 3 are in by then |
| 5 | M6, then G4, then G5 | G5 needs M6 |

## 14. Decisions (answered by the founder, 24 November 2026)

| Phase | Question | Answer |
| --- | --- | --- |
| H3 | Device fields (location, photo, signature, scan) in customers' tools? | **No: members' tools only.** Customers' tools keep refusing them |
| H3 | How the web scans a code | **The camera where the browser has a barcode reader; typed elsewhere.** No outside barcode library |
| H6 | The old public menu's entries | **Nothing is in production, so nothing is carried over: retire the separate public menu.** A business's customer menu is its customers' tools, each with its answer |
| H7 | When the provider gives no fee quote | **Pay, and show the fee after.** The sheet says "The fee shows after paying"; the fee is recorded when the provider reports it |
| H7 | Who chooses wallet to wallet when both sides have a Taskiem wallet | **Taskiem, whenever it can.** "Taskiem wallet · arrives at once", falling back to the bank if that route fails |
| G4 | How charts are drawn | **By Taskiem, as plain SVG**, as the money screens; a table behind each chart for screen readers; no chart library |

## 15. Every item, and where it went

| Item | Where |
| --- | --- |
| g-pay-core | H7 |
| g-things-menu | H2 (done 24 November 2026) |
| g-label-pre | H2 (done 24 November 2026) |
| g-label-merge | H2 (done 24 November 2026) |
| g-dev-link | H3 |
| g-dev-confirm | H3 |
| g-menu-used | H4 |
| g-menu-words | H4 |
| g-menu-nocmd | Built; H1 fixes the doc |
| g-search-saved | H4 |
| g-pages-mine | H5 |
| g-pages-runs | H5 |
| g-pages-tick | H5 |
| g-recipes | §12 |
| g-own-purchase | §12 |
| g-old-cols | H2 (done 24 November 2026) |
| g-stock-lang | H8 |
| g-uses-ask | H2 (done 24 November 2026) |
| g-deprec | §12 |
| g-thing-suggest | H2 (done 24 November 2026) |
| g-thing-extra | H2 (done 24 November 2026) |
| g-saved-wf | H6 |
| g-saved-addr | H6 |
| g-saved-personal | §12 |
| g-saved-review | §12 |
| g-pay-wallet | §12 |
| g-pay-w2w | H7 |
| g-pay-saveas | H7 |
| g-pay-first | H7 |
| g-pay-staff | H7 (refunds; staff and riders built) |
| g-pay-batch | H7 |
| g-pay-fwd | H7 |
| g-pay-payroll | §12 |
| g-pay-momo | §12 |
| g-photo-phone | §12 |
| g-pages-g4 | §11 G4 |
| g-pages-g5 | §11 G5 |
| g-pages-editor | H5 |
| g-pages-notif | H5 |
| g-pages-rowstatus | H5 |
| g-pages-tab | H5 |
| g-pages-cust | §12 |
| g-ct-m6 | §11 M6 |
| g-ct-linked | §12 |
| g-ct-files | §12 |
| g-ct-later | H6 (plain words, wd-test answer); §11 M6 (template library) |
| g-ct-oldmenu | H6 (the public menu retired, §14) |
| g-ct-tg | §12 |
| g-ct-webkeep | H6 |
| g-ct-chip | Chip built (H1 fixes the doc); optional inputs H6 |
| g-ct-waform | §12 |
| g-scan-web | H3 |
| g-scan-wa | H3 |
| g-scan-tool | H3 |
| g-scan-erase | H3 |
| g-scan-sense | §12 |
| g-scan-echo | Built; H1 fixes the doc |
| g-menu-lang | H8 |
| g-menu-tg | H4 |
| g-menu-slack | H4 |
| g-menu-later | §12 |
| g-search-notnow | §12 |
| g-doc-saved | H1 |
| g-doc-plan | H1 |
| g-doc-device | H1 |
| g-doc-pin | H1 |
| (found in H1) a batch whose last row is refused never ends | H7 |

66 items, and one found while doing H1: 44 (and the one found) in phases H1 to H8, 3 in the later milestones, 17 waiting (§12), and 2 already built (their docs fixed in H1).
