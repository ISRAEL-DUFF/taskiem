# Build plan: saved objects, made tools and payments

Status: **plan** (19 November 2026), written with the founder; reviewed against the four plans and the code the same day (§8 says what that changed). Nothing here is built. It orders four plans into one sequence of phases, so they are built once, in the right order, on shared parts.

| Plan | What it gives | Its own phases |
| --- | --- | --- |
| [Saved objects](saved-objects.md) (settled) | Things a person or a business saves and calls up by `@name`: accounts, people, places, texts, files, kinds of their own | 1 to 4 |
| [Made tools](made-tools.md) | Any workflow started by hand becomes a tool in the chat: a tile, a `/command`, a form, an answer | T0 to T7 |
| [Pages](pages.md) | A business's own screens, made of blocks fed by its tables, Taskiem's records and its read-only tools | G0 to G5 |
| [The app as a node](device-fields.md) | A form step asks a person's device: a location, a photo, a scanned code, a signature, at their tap | D0 to D2 |
| [Paying people from the chat](chat-pay-people.md) | Pay one person, from a message, several people, payroll; payees, limits and step-up | 1 to 4 |
| [The payments flow](chat-payments-flow.md) | Paying a bill by transfer, in the chat (part 1); later parts as agreed | Part 1 |

Each phase below is one pull request (or a few, by server, web and app, merged together), ships on its own, and names which plan it builds. A phase is done when its **done when** holds on the web, in the phone app (Android and iPhone) and, where it says, on WhatsApp, Telegram and Slack.

The order: saved objects first, in full; then payments, in full; then made tools and [pages](pages.md) (§4).

## 1. How the four fit together

They meet in a few places. Each shared part is built once, in the first phase that needs it.

| Shared part | Used by | Built in |
| --- | --- | --- |
| **The `@` and `/` picker** in every chat: `@` for businesses, people and saved objects; `/` for documents, then made tools' commands, then saved objects when nothing else matches. An object's name never blocks or takes a command | Saved objects, made tools | S1 (the `@` Saved group), M1 (made tools on `/`) |
| **Cards** that fold to a row and open (built 17 November 2026) | Saved objects (shared cards), made tools (`card` and `list` answers), payments (the Pay result, the transfer card) | Built; extended in S1, M2, P1 |
| **The reader** (`engine/ai/intent`): words to a command, given names and kinds only, never sensitive values | Saved objects ("deliver to @warehouse"), payments ("send 20k to @bayo", "transfer 45k to 0123456789 gtbank"), made tools (`says`, the model) | **The order of reading is fixed in S2**, the first phase to extend it: Taskiem's own commands, then `@name`, then (later) tools. S2 teaches it `@name`; P1 the words that pay; P2 account details in words; M3 tools |
| **Reading details from a message** (an account number and bank, an address, a phone) | Saving an object from a message (S1), the "Pay this account" chip (P2) | S1 builds the reader of details; P2 adds the NUBAN check and the chip |
| **The bank list and the name check** (`GET /v1/banks`, `POST /v1/name-check`, over the connectors' `resolve_account`) | Saving a bank account (S0), paying (P1) | S0 |
| **The step to connect a payments provider**, with **Open a Taskiem wallet** (iswallet) first | Checking a saved account's name (S0), being paid (F1), paying (P1) | S0 |
| **Confirming with a passkey or the PIN** (step-up) | Payments (every confirm), made tools (care: step-up), saved objects (revealing a sensitive value, changing a bank account) | Exists for a passkey or an authenticator code (`checkStepUp`), used today for keys and approvals; a six-digit PIN exists for approvals on WhatsApp. S0 adds step-up's use for changing and revealing an account (passkey only). P1 adds it for confirming a payment, with the PIN as the fallback: a lower limit, a first PIN at sign-up, the lock after wrong tries, no reset by text ([paying people](chat-pay-people.md#settled-19-november-2026), 3) |
| **`money.pay` and pay limits** | Payments; saved objects (paying a saved account) | P1 |
| **Payees** | Payments | **Are saved objects of the kind bank account** (settled): S0 makes the store, P1 pays it. Staff's and riders' bank details stay on their own records (an object never copies what Taskiem keeps); the Pay sheet picks from both |
| **Preparing a payment from a workflow** (the `payments@1 prepare` action: it makes the record and stops for a person's confirmation; it never sends) | Workflows that pay a saved account, made tools that pay | P1 |
| **Sealing and masking** | Saved objects' sensitive fields, payees, the transfer card's number | Exists (`engine/secrets`); S0 for accounts, S1 for the rest |
| **Who may start a workflow** (a tool's `roles` on every path) | Made tools. Not payments: `pay-person` is a managed workflow the server starts, behind `money.pay` and `pay_limits` | M0 |

## 2. The rules every phase keeps

From the automation discussion with the founder:

1. **Suggest, don't surprise.** Taskiem proposes; nothing starts without a person saying yes.
2. **Show it before it acts the first time:** what will happen, to whom, for how much.
3. **Money and messages to customers always pass a gate:** step-up, an approval, or a limit. Nothing pays from words alone. This holds for a made tool and a workflow as it does for the Pay sheet (M0, P1).
4. **Easy to see and stop:** each action leaves a line in the chat diary and the audit; what runs by itself can be paused.

And as for every screen: built on the web first and matched in the app's own look, light and dark, at 360 px, with axe and the plain-words test; the AI never sees a sensitive value.

## 3. Before building

| Item | What | Who |
| --- | --- | --- |
| D1 | **A decision record for made tools** (the next free number; 0070 as of today). The `tool` block in `wd/v1` and its rules ([made tools](made-tools.md) §5, §14) | Founder accepts; written in M0 |
| D2 | **A decision record for saved objects** (the next free number; 0071 as of today). Objects as a core part, their owners, sealing, and payees being objects ([saved objects](saved-objects.md) §5, §8, §15) | Founder accepts; written in S0 |
| D3 | **Made tools' nine choices** (§14 there). **Settled 19 November 2026:** the founder took each recommendation, with the review's rule that a tool which moves money always asks for step-up (§8 there) | Done |
| D4 | **Paying people's open questions.** **Settled 19 November 2026** ([its last section](chat-pay-people.md#settled-19-november-2026)): owners and roles the owner chooses, staff at ₦0; Pay shown with the step to connect, and **a business with no provider is given a Taskiem wallet (iswallet)**; a PIN as the fallback to a passkey, not a code by text; the business paying bears the fee; payslips with the full breakdown; the person paid sees a card; banks, and Taskiem wallet to wallet | Done |
| D5 | **The payments flow's five open questions.** **Settled 19 November 2026** ([its last section](chat-payments-flow.md#settled-19-november-2026)): the seller bears the fee; transfer first, the last choice remembered; one bill at a time with "Pay the next one"; customers in part 1; 60 minutes with "I need longer" | Done |
| D6 | **A payments provider in test mode.** The Taskiem wallet (iswallet) is the main one for the end-to-end tests, since every business may have it: balances, the name check, payouts, wallet to wallet and one-time account numbers. One outside provider (Moniepoint or Anchor) is tested beside it. The provider questions left open by the system audit (Paystack's transfer statuses, Flutterwave's second transfer) are closed before those providers are offered for paying people | Founders |
| D7 | **A security review** of S0 and P1 together before launch (a check, not a switch): step-up on every confirm, change and reveal; sealed numbers; nothing paid from text; a business never reaching a person's own objects. S1's sharing of sensitive details is reviewed the same way before it ships | Someone not on the build |
| D8 | **How each phase is switched on.** **Settled 19 November 2026: on for every business, with no new switch and no release flag** (there is no production yet). Plan limits, a connected payments provider, `money.pay` and staff starting at ₦0 are the gates. Made tools' panel follows the chat tools switch; their commands work wherever `run` does | Done |
| D9 | **Sizes.** No phase is sized here. Each is sized when it is started; S1 is the largest and is expected to be several pull requests | Whoever builds it |

## 4. The phases

**The order** (the founder, 19 November 2026): **saved objects first, in full**, since they need nothing else; **then payments, in full**; **then made tools and pages**. So payments is built on saved objects as they will be, with the picker, the cards and every kind already there, and nothing in payments is added to later.

Within a stage, phases that do not need each other may be built at once by separate people or sessions. A phase may start once what it **needs** is merged.

### Stage 1: saved objects, the store and the picker

**S0. Saved accounts: the store** (the first part of saved objects phase 1). Needs: D2; the Taskiem wallet (iswallet) in test mode (D6).
- **Status (20 November 2026): the server is built** ([decision 0071](../decisions/0071-saved-objects.md), proposed): the store, sealing, passkey step-up, the audit, the bank list, the name check, the step to connect and `@name` resolved, with the API documented. The Saved list on the web and in the app is the next pull request. Tested against a fake wallet; D6 (a wallet in test mode) is still open.
- `saved_objects` with both owners (a person's own, a business's) and its row-level security; the kind **bank account** only; the account number sealed and masked; changing it, and showing it in full, by step-up; the audit of each; the bank list and the name check; a plain Saved list (add, change, remove) on the web and in the app; `@name` resolved on the server.
- **The step to connect a payments provider** (moved here from P1 by the founder, 19 November 2026, so an account's name can be checked from the first day): **Open a Taskiem wallet** first, for a business with no provider (an email and the director's BVN, in plain words, with why), or **I already have an account with…** Paystack, Flutterwave, Moniepoint or Anchor. Only the owner sees it. Saving a bank account offers it when no provider is connected; an account may still be saved unchecked, marked "Name not checked yet", and is checked as soon as one is.
- No picker group, cards, sharing or other kinds yet: those are S1, built straight after. No paying yet: the wallet's balance, **Add money** and the Pay sheet are P1.
- Done when: an owner with no provider opens a Taskiem wallet from the Saved list, saves a supplier's account as `@bayo`, and the bank's name for it is checked and shown; staff see it masked; changing the number asks for a passkey and is in the audit.

**S1. Saved objects and the picker** (the rest of saved objects phase 1). Needs: S0.
- **Status (20 November 2026): all 6 parts built, but for files**, on the S0 pull request. Part 1: the kinds Taskiem knows (a person, a place, a text, an account with a provider) beside the bank account, and the Saved page with them (filter by kind, a form for each), on the server, the web and the app. Part 2: `@` offers saved things in every chat on the web and in the app (a Saved group with chat tools; a list of its own otherwise), read the first time `@` is typed; a text's words go in the box, to change before sending; in the Taskiem chat every kind is offered and written as its `@name`, and a message to Taskiem that is only `@name` (from the web, the app, WhatsApp, Telegram or Slack) is answered with it, an account number masked. Part 3 (so far): in people chats, groups and a business's customer chats, `@` shares any other saved thing as a card (a person's own with people; the business's or the member's own with a customer), asking first, in the server's words, before sharing what is sensitive; each share is in the owner's log or the audit; the card folds to a row, opens to each detail with Copy, and offers **Save this** to whoever got it; a customer on WhatsApp gets it as plain lines; a card can't be forwarded. A customer shares their own with a business from the app (sealed in the business's copy under the customer's subject, so it goes when they are erased), and the business can save it into its Saved; between linked businesses a saved thing is a tag of its own kind, its `@name` in the words, a card on both sides. Part 4: the details reader (`engine/saved`, `POST /v1/me/saved/read`) finds an account number and its bank, a phone, an email, an address with its area and landmark, and a name given as one; **Save as…** on a message (people chats, groups, business chats and the Taskiem chat, on the web and in the app) opens the sheet for what they make, filled in; in the picker, a name nothing has yet offers **Save "@mum"…**; and "save mum: GTBank 0123456789" to Taskiem (also on WhatsApp, Telegram and Slack) is shown as understood, a business's account with the bank's name for it, and saved on **Save** (in the business's for whoever may manage them, **Keep it to myself** a tap away), its details sealed while it waits. Part 5: kinds the owner makes (`saved_kinds`; `/v1/me/saved-kinds`, `/v1/saved-kinds`), with starters to copy (a car, a generator, a pet, a medical card), up to 40 an owner and 30 fields a kind: text, a number, money, a date, a phone, an email or a choice, each required, sensitive (sealed with the object's other sensitive values, masked in lists, in full only by reveal with a passkey) or never shared (kept out of every card); changing a kind keeps what objects had and moves values when a field becomes sensitive or no longer is; a kind with live objects is not removed; on the Saved page and in Save something, on the web and in the app. Part 6: the saved addresses moved over (migration 00494): each is now one of the person's own place objects under the same id, `/v1/me/addresses` unchanged for the app and checkout. Still to build: files (the File kind, and place, bank account and file fields on a made-up kind); Save as… in customer chats (they have no message menu yet); saving after an action and suggestions (S2). Before it ships, D7's review of sharing what is sensitive.
- The other kinds Taskiem knows, made-up kinds with starters, the full Saved page, the `@` Saved group in every chat (Taskiem chat, business chats, customer chats, people chats and groups), `/` falling back to objects, cards with Copy and **Save this**, text objects into the box, sharing with the sensitive check, saving from a message (the details reader), the picker and words; saved addresses moved over (`/v1/me/addresses` unchanged); limits.
- **On WhatsApp, Telegram and Slack:** there is no picker, so `@name` typed in a message to Taskiem is matched on the server. A card shared with a customer who is on WhatsApp reaches them as plain lines (the title, then each detail on its own line, easy to copy), under the same sensitive check.
- It is several pull requests. Suggested split: kinds, the Saved page and the picker; cards and sharing; saving from a message and in words; made-up kinds; the address move.
- Done when: a business's staff drop `@bank-details` into a customer chat, and a customer on WhatsApp gets it as lines they can copy; a person saves `@mum` from a message in under 15 seconds and shares it as a card; checkout still offers saved addresses.

### Stage 2: saved objects in actions and workflows

**S2. Saved objects in actions** (saved objects phase 2). Needs: S1.
- Delivery, invoices, orders and forms take a saved place, person or account ("deliver to @warehouse", "invoice @acme 250k"); date fields remind ("remind me when @car insurance is due"); questions about an object's details answered in the chat; the reader learns `@name` for every kind (names and kinds only). **The reader's order is fixed here**, the first phase to extend it: Taskiem's own commands, then `@name`, then (later) tools.
- These words work in the web chat, the app, and the Taskiem chat on WhatsApp, Telegram and Slack. Words that pay come with payments (P1; on the chat apps, P4).
- Done when: an owner sends an order to `@warehouse` and an invoice to `@acme` by words, from the app and from WhatsApp, each filled in, and gets the insurance reminder on its day.

**S3. Saved objects in workflows** (saved objects phase 4). Needs: S2.
- A business's objects as workflow inputs (the builder's picker; read when the run starts); the import from a file; "save this?" suggestions after repeated actions.
- Done when: a workflow "every Monday, send `@price-list` to the customers who ordered last week" runs with the object as its input, a changed `@price-list` is used from the next run, and a file of suppliers' accounts is imported as saved accounts.

### Stage 3: being paid, and paying one person

**F1. Pay a bill by transfer, in the chat** (the payments flow, part 1). Needs: nothing (D5 is settled).
- Pay on a bill card gives the buyer a one-time account number for that bill, with the amount, a countdown and Copy; both cards turn to Paid when it lands; part paid, expired, late; "Pay the next one"; "I need longer"; "pay INV-0001" on WhatsApp and Telegram.
- For linked businesses and for **customers**: a customer's invoice card pays the same way, and a customer on WhatsApp gets the number as plain lines.
- Built on what exists (one-time numbers, matching, the bill mirror), so it ships early and alone.
- Done when: a linked buyer pays a bill by transfer from the chat and both sides see it paid without a reload; a customer on WhatsApp is sent the number, pays, and is told it landed.

**P1. Pay one person, safely** (paying people phase 1, with saved objects phase 3). Needs: S0 to S3 (all of saved objects), D6; D7 before launch.
- `payments`, `pay_limits`, `money.pay`; payees as bank account objects; the `pay-person` workflow; the `payments@1 prepare` action for workflows; the Pay tile and sheet, which leads to S0's step to connect where a business has no provider yet; the balance and **Add money**; "what for"; the fee shown before confirming; the passkey on confirm, with the PIN as the fallback; bank transfers, and Taskiem wallet to wallet where both have one; the card in the conversation for the person paid; the account-changed and first-payment warnings; limits and the owner's OK; diary lines, the receipt, the money log; the first-payment warning on payroll and supplier approvals.
- The reader, which already knows `@name` (S2), learns the words that pay: "send 20k to @bayo for rice" in the web chat and the app opens the sheet filled in. The `@` picker, cards and **Save as…** for any kind are already there.
- **A workflow that pays a saved account** uses `payments@1 prepare` with a business's saved account as its input (S3): the run makes the payment and waits; a person who may pay confirms it.
- **Paying a saved account:** the Pay sheet's "who" is a saved account (or a new one, saved at the end). An **owner** may pay their own saved account (`@mum`) from the business, recorded as personal (owner's drawings); the payment keeps a sealed copy of the account, since the business cannot read the person's own object. Staff pay the business's saved accounts only.
- Done when: an owner pays a rider ₦45,000 from Tools in under 30 seconds, recorded as a delivery; "send 20k to @bayo" opens the filled-in sheet and pays on a passkey; a changed account for a past payee is stopped for the owner's OK; a staff member's own saved account is not offered as a payee; a workflow "pay `@landlord` on the 1st" runs, its payment waits for the owner's passkey, and a changed landlord account is stopped as any payee's is.

### Stage 4: the rest of payments

**P2. Pay from a message** (paying people phase 2). Needs: P1.
- The details reader gains the NUBAN check and candidate banks; the **Pay this account** chip on web chat and business-chat messages and photo text; a chip already paid says so; **Save as…** beside it; Pay on staff, riders and refunds (their bank details read from their own records, with the same checks); the reader takes "transfer 45k to 0123456789 gtbank".
- Done when: account details in a WhatsApp message that reached the web chat become a checked, filled-in payment in one tap, and can be saved as `@driver` on the way.

**P3. Pay several people** (paying people phase 3). Needs: P2.
- Batches: picking (saved accounts as a list), collecting from messages, pasting and photographing a list, last time's again; a check per row, held-out rows, one confirmation, results per person, paying the failed ones again.
- Done when: 12 riders are paid in one batch, a bad account is held out before anything moves, and the others are paid.

**P4. Payroll and the chat apps** (paying people phase 4). Needs: P3.
- The Payroll tile over `run-payroll`; adjustments; its approval card in the chat; payslips to each person's chat; payslips with the full breakdown; the WhatsApp, Telegram and Slack words with confirmation by link, or by the PIN within its limit ("send @driver 45k" on WhatsApp opens the sheet by link).
- Done when: this month's payroll is reviewed, adjusted, approved and paid from the chat, and a payment prepared on WhatsApp is confirmed in the app.

### Stage 5: the first tools

**M0. Made tools: the declaration** (made tools T0). Needs: D1 (D3 is settled). Its money rule relies on P1's `payments@1 prepare`, which by then is built. **The roles rule is its first pull request, on its own**: it changes who may start any workflow, with or without a tool, and is reviewed apart from the rest.
- The decision record; `tool` and `answer` in the schema and the contract; the validator's rules, with the money rule (a step that moves money makes care step-up; refused for customers' and linked tools); care worked out and stored; a tool's `roles` held on every way to start a workflow.
- Done when: a definition with `tool` validates or is refused for the right reason; a tool with a payout step is marked step-up; and a member outside a workflow's roles cannot start it by any path.

**D0. Device fields: the declaration** ([the app as a node](device-fields.md) D0). Needs: nothing.
- The four field types (location, photo, scan, signature) and their options in the forms schema; `to` as a person on the form step; `source`, `device` and the server's time on every answer; a location sealed and masked.
- Done when: a form step with a `location` field validates, and a location in a run's history is sealed and masked.

**M1. Your tools** (made tools T1). Needs: M0.
- The index of deployed tools; **Your tools** in the panel and on `/`; commands on WhatsApp, Telegram and Slack; the form; care; the `text` answer; **Use it from chat** in the builder; the SDK.
- A tool's input of a type a saved kind has (a place, a person, a bank account, a phone, a date) offers the person's and the business's **Saved** objects of that kind in its form.
- Done when: a person makes "Log an expense" without Advanced and uses it from the web, the phone app and WhatsApp.

### Stage 6: cards, the reader and the first pages

**M2. Cards, lists and buttons** (made tools T2). Needs: M1.
- `card` and `list` answers drawn with the fold-and-open cards; buttons on answers and on `notify.tell`; the sheet for a list.
- Done when: "Open tickets" lists, and **Assign to me** on a row works from a phone.

**M3. The reader knows your tools** (made tools T3). Needs: M1, S2, P1 (one reader).
- Tools given to the reader after Taskiem's commands and saved objects' names, in the order S2 fixed; proposed values, including `@objects` as inputs; the numbered pick; evaluation cases in English and Pidgin.
- Done when: "spent 4,500 on diesel" opens the expense tool filled in; "send 20k to @bayo" still reaches payments; no built-in command is taken by a tool.

**M4. A question inside a run** (made tools T4). Needs: M1.
- `to: "starter"` on the form step: a run asks in the chat it was started from, and goes on.
- Done when: a run asks "Which branch?" in the chat and goes on with the answer (a saved place offered among the choices).

**D1. Location and photo, everywhere** (the app as a node D1). Needs: D0; M4 for `to: "starter"`; M1 for tools' inputs.
- Location and photo in the phone app (offline too), on the web, and on WhatsApp and Telegram by their own location and photo messages; asked only at the person's tap, after the field's `why`; `require_source` for a workflow that needs a reading, not a claim.
- Done when: a rider on WhatsApp and a rider on the app each prove a delivery, and the Confirm card shows the pin and the photo and says which was read by the phone and which was sent.

**G0 and G1. Pages: the format, and pages from tables** ([pages](pages.md) G0, G1). Needs: nothing but data tables; started once M2 is under way.
- The decision record and `page/v1`; figures, grouped lists, tables, details and filters; the Pages area, pinning, roles, draft and publish; **Show as a page**.
- Done when: an owner turns the tickets table into a page with a count and a list in under two minutes, and staff open it on a phone.

### Stage 7: customers, and pages with buttons

**M5. Customers' tools** (made tools T5). Needs: M2.
- The public menu carried over; answers for customers with `public` outputs only; in the app, customers' tools in a business's customer chat (Taskiem for everyone's customer door rules apply).
- Done when: a customer picks from **menu**, fills the form, says **yes** and gets the answer.

**D2. Scan and signature** (the app as a node D2). Needs: D1.
- Scanning a code and signing on the screen; a scan from the phone's home screen that starts a workflow, and tools on the home screen (settled by the founder, 19 November 2026).
- Done when: a stock count by scanning works in the app; a customer signs for a delivery on the rider's phone; a code scanned from the home screen, with no workflow open, offers the workflow that takes it.

**G2 and G3. Pages with tools, and in the chat** (pages G2, G3). Needs: G1, M2 (G3 also M1).
- Buttons, row actions, tools feeding a page, **Pin as a page**; the tile, `/name`, and the short form on WhatsApp, Telegram and Slack.
- Done when: "Tickets" has **New ticket** and **Assign to me**, and "tickets" on WhatsApp answers with the figures, five rows and a link.

### Stage 8: the builder, boards and charts

**M6. Recipes and the AI builder** (made tools T6). Needs: M3, S3.
- The eight recipes; "Describe it" drafts the workflow, its inputs and outputs and the `tool` block, using the business's saved objects by name ("every Friday send `@price-list` to last week's customers").
- Done when: "Describe it" produces a working tool from one sentence, naming a saved object.

**G4 and G5. Boards, charts, starters and Describe it** (pages G4, G5). Needs: G2; G5 also M6 and S1.
- Done when: a ticket dragged from Open to Doing runs its tool, and "Describe it" makes a working page from one sentence.

### Later, each as its own plan

- **M7.** Made tools for linked businesses (made tools T7): [its plan](made-tools-linked.md) is written and marked for later, not to be started before M5 is in use.
- **Payments flow, later parts** (statements, several bills at once), as the founder agrees them.
- **Paying from a person's own side** (You, with no business): not planned; saved accounts on You are shared and copied only.
- **Schools** ([schools in the chat](schools-in-the-chat.md)) and the rest of the automation list: planned beside this, sharing its parts (saved objects for schools, made tools for school routines).

## 5. Order at a glance

| Stage | Saved objects | Payments | Made tools | Pages and the app as a node |
| --- | --- | --- | --- | --- |
| 1 | S0 saved accounts; S1 objects and the picker | | | |
| 2 | S2 objects in actions; S3 objects in workflows | | | |
| 3 | | F1 pay a bill by transfer; P1 pay one person | | |
| 4 | | P2 from a message; P3 several people; P4 payroll and chat apps | | |
| 5 | | | M0 the declaration; M1 your tools | D0 device fields declared |
| 6 | | | M2 cards and buttons; M3 the reader; M4 asking back | G0, G1 pages from tables; D1 location and photo |
| 7 | | | M5 customers | G2 with tools; G3 in the chat; D2 scan and signature |
| 8 | | | M6 recipes and the builder | G4 board and chart; G5 starters |

All of saved objects is done at the end of stage 2, and all of payments at the end of stage 4. F1 needs neither saved objects nor P1, so where there are hands for it, it may be built beside stages 1 and 2. The payments flow's later parts (statements, several bills on one number) are not planned yet and are not in these stages.

## 6. For each phase, as built

Each phase adds an **As built** section to its own plan (saved objects, made tools, paying people, the payments flow), as Taskiem for everyone's stages did: the migrations, the API, what changed from the plan and why, the tests, and a review's fixes. This plan is updated with a tick against each phase as it merges:

- [ ] S0 · [ ] S1
- [ ] S2 · [ ] S3
- [ ] F1 · [ ] P1
- [ ] P2 · [ ] P3 · [ ] P4
- [ ] M0 · [ ] M1 · [ ] D0
- [ ] M2 · [ ] M3 · [ ] M4 · [ ] G0 · [ ] G1 · [ ] D1
- [ ] M5 · [ ] G2 · [ ] G3 · [ ] D2
- [ ] M6 · [ ] G4 · [ ] G5

## 7. Risks

| Risk | What keeps it small |
| --- | --- |
| Money sent to the wrong account | Payees are saved objects with the payments plan's checks: the bank's name shown large, the changed-account stop, the first-payment warning, step-up on every confirm, a security review of S0 and P1 before release |
| Someone paying themselves from the business | Only an owner may pay their own saved account from the business; staff are offered the business's saved accounts only, within their limits (P1) |
| One reader trying to do three things (commands, tools, objects) | One order of reading, fixed in S2 and tested with evaluation cases from then on; built-in commands always first; objects and tools only ever proposed, never acted on without the form or the sheet |
| Sensitive values reaching the model | The model is given names and kinds only; a test reads what is sent, in S1, S2, P1, P2 and M3 |
| Three tracks at once colliding in the same files (the picker, the reader, the cards) | Each shared part has one owner phase (§1); later phases extend it rather than copy it |
| Saved objects used as a database | Limits, and the Saved page pointing a large made-up kind to tables |
| A made tool or a workflow that pays | The validator marks a tool with a step that moves money as step-up, whatever the business's policies, and refuses it for customers and linked businesses (M0). On WhatsApp, Telegram and Slack such a tool is confirmed by link in the app, never by a typed yes. A workflow that pays a saved account does it through `payments@1 prepare`, so P1's checks and a person's passkey always stand between the run and the money |
| Payments waiting for all of saved objects | Chosen by the founder, so payments is built once on the finished store, picker and cards. F1, which needs neither, may be built beside stages 1 and 2 |
| Payments built before the tools that will use them | `payments@1 prepare` is built in P1, so a made tool that pays (M0) meets P1's checks from its first day |

## 8. What a review of this plan changed

The plan was read against the four plans and the code on 19 November 2026. What that found:

| Was | Is |
| --- | --- |
| "A payment step makes a tool step-up" (§7), though made tools gave step-up only when a business's policies asked for it, so a tool that pays could run on a typed **yes** | A rule of the validator, in M0 and in [made tools](made-tools.md) §8 and §12 |
| "Pay `@landlord` on the 1st" prepared a payment, with nothing a workflow could prepare one with | `payments@1 prepare`, built in P1; S3 needs P1 |
| "send 20k to @mum" in P1, with the reader learning `@name` in S2 and its order fixed in M3 | The order is fixed in the first phase to extend the reader: S2, now that saved objects come first |
| S1 checked an account with the bank, using the bank list and name check P1 was to build | Both are built in S0 |
| "Step-up exists; used as is" | It exists for a passkey or an authenticator code, for keys and approvals. Its use for payments, changes and reveals is new work. The fallback is the PIN, settled by the founder in place of a code by text |
| Anyone who may pay could pay their own saved account as "owner's drawings" | Owners only ([saved objects](saved-objects.md) §7.3) |
| `payments.payee` pointed to the object, which a business cannot read when it is a person's own | A sealed copy on the payment for an owner's own object (saved objects §12) |
| P1 waited for all of S1, the largest phase | First S0 was split out so P1 need not wait; then the founder chose saved objects in full before payments, so P1 follows S0 to S3. S0 stays as the first pull request of saved objects |
| Nothing on WhatsApp for saved objects | S1 and S2 say what works there and what a customer on WhatsApp receives |
| The security review covered P1 only | S0 and P1 together; S1's sharing before it ships |
| M1, M4, M3 and S3 used phases they did not list; `pay-person` was said to use M0's roles rule | Needs corrected |
| "One list, not two" for payees | Staff's and riders' bank details stay on their own records; the Pay sheet picks from both |
| The step to connect a provider came with P1, so accounts saved in stages 1 and 2 could not be name-checked | Moved into S0 by the founder, with the Taskiem wallet first |
| Nothing on switching a phase on, payout fees, or sizes | D4 and D8, both settled; D9 |
| Pages specified twice, and the app as a node inside made tools (pull request #211) | One pages plan, with #211's sums, record views, signals, alerts and change notices taken in; the app as a node as its own plan, with `source` and the server's time added; its phases D0 to D2 in stages 5 to 7 |
| Three tracks side by side, payments ending in stage 5 | One after another, by the founder's order of 19 November 2026: saved objects in full (stages 1 and 2), payments in full (3 and 4), then made tools and pages. The reader's order is therefore fixed in S2, and the workflow that pays a saved account moves from S3 to P1 |
