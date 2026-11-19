# Build plan: saved objects, made tools and payments

Status: **plan** (19 November 2026), written with the founder. Nothing here is built. It orders four plans into one sequence of phases, so they are built once, in the right order, on shared parts.

| Plan | What it gives | Its own phases |
| --- | --- | --- |
| [Saved objects](saved-objects.md) (settled) | Things a person or a business saves and calls up by `@name`: accounts, people, places, texts, files, kinds of their own | 1 to 4 |
| [Made tools](made-tools.md) | Any workflow started by hand becomes a tool in the chat: a tile, a `/command`, a form, an answer | T0 to T7 |
| [Paying people from the chat](chat-pay-people.md) | Pay one person, from a message, several people, payroll; payees, limits and step-up | 1 to 4 |
| [The payments flow](chat-payments-flow.md) | Paying a bill by transfer, in the chat (part 1); later parts as agreed | Part 1 |

Each phase below is one pull request (or a few, by server, web and app, merged together), ships on its own, and names which plan it builds. A phase is done when its **done when** holds on the web, in the phone app (Android and iPhone) and, where it says, on WhatsApp, Telegram and Slack.

## 1. How the four fit together

They meet in a few places. Each shared part is built once, in the first phase that needs it.

| Shared part | Used by | Built in |
| --- | --- | --- |
| **The `@` and `/` picker** in every chat: `@` for businesses, people and saved objects; `/` for documents, made tools' commands, and saved objects when no other match | Saved objects, made tools | S1 (the `@` Saved group), M1 (made tools on `/`) |
| **Cards** that fold to a row and open (built 17 November 2026) | Saved objects (shared cards), made tools (`card` and `list` answers), payments (the Pay result, the transfer card) | Built; extended in S1, M2, P1 |
| **The reader** (`engine/ai/intent`): words to a command, given names and kinds only, never sensitive values | Saved objects ("send 20k to @mum"), made tools (`says`, the model), payments ("transfer 45k to 0123456789 gtbank") | One reader, extended in turn: objects (S2), tools (M3), payments (P2) |
| **Reading details from a message** (an account number and bank, an address, a phone) | Saving an object from a message (S1), the "Pay this account" chip (P2) | S1 builds the reader of details; P2 adds the NUBAN check and the chip |
| **Confirming with a passkey or code** (step-up) | Payments (every confirm), made tools (care: step-up), saved objects (revealing a sensitive value, changing a bank account) | Exists (`checkStepUp`); used as is |
| **`money.pay` and pay limits** | Payments; saved objects (paying a saved account) | P1 |
| **Payees** | Payments | **Are saved objects of the kind bank account** (settled): S1 makes the store, P1 pays it |
| **Sealing and masking** | Saved objects' sensitive fields, payees, the transfer card's number | Exists (`engine/secrets`); S1 for objects |
| **Who may start a workflow** (a tool's `roles` on every path) | Made tools; payments' `pay-person` workflow | M0 |

## 2. The rules every phase keeps

From the automation discussion with the founder:

1. **Suggest, don't surprise.** Taskiem proposes; nothing starts without a person saying yes.
2. **Show it before it acts the first time:** what will happen, to whom, for how much.
3. **Money and messages to customers always pass a gate:** step-up, an approval, or a limit. Nothing pays from words alone.
4. **Easy to see and stop:** each action leaves a line in the chat diary and the audit; what runs by itself can be paused.

And as for every screen: built on the web first and matched in the app's own look, light and dark, at 360 px, with axe and the plain-words test; the AI never sees a sensitive value.

## 3. Before building

| Item | What | Who |
| --- | --- | --- |
| D1 | **Decision 0070: made tools.** The `tool` block in `wd/v1` and its rules ([made tools](made-tools.md) §5, §14) | Founder accepts; written in M0 |
| D2 | **Decision 0071: saved objects.** Objects as a core part, their owners, sealing, and payees being objects ([saved objects](saved-objects.md) §5, §8, §15) | Founder accepts; written in S1 |
| D3 | **Made tools' nine choices** (§14 there): each has a recommendation | Founder, before M0 |
| D4 | **Paying people's open questions** (its last section): each has an answer in brackets | Founder, before P1 |
| D5 | **The payments flow's five open questions** (fees, default way to pay, several bills, customers, how long a number is good for) | Founder, before F1 |
| D6 | **A payments provider in test mode** with payouts (Paystack, Moniepoint or Anchor) for the end-to-end tests, as the fakes allow for the rest | Founders |
| D7 | **A security review** of P1 before it ships to anyone: step-up on every confirm, sealed numbers, nothing paid from text | Someone not on the build |

## 4. The phases

Three tracks run side by side where nothing joins them: **S** (saved objects), **M** (made tools), **P** and **F** (payments). The order below is the order to start them; a phase may start once what it **needs** is merged.

### Stage 1: foundations that stand alone

**S1. Saved objects and the picker** (saved objects phase 1). Needs: nothing.
- The kinds Taskiem knows, made-up kinds with starters, Mine and the business's, the Saved page, the `@` Saved group in every chat (Taskiem chat, business chats, customer chats, people chats and groups), `/` falling back to objects, cards with Copy and **Save this**, text objects into the box, sharing with the sensitive check, saving from a message (the details reader), the picker and words; saved addresses moved over (`/v1/me/addresses` unchanged); sealing, masking, reveal by step-up, limits, audit; decision 0071.
- A bank account object is shown, checked with the bank when saved where a provider is connected, shared and copied: **not yet paid**.
- Done when: a person saves `@mum` from a message in under 15 seconds and shares it with a friend as a card they can copy; a business's staff drop `@bank-details` into a customer chat; checkout still offers saved addresses.

**F1. Pay a bill by transfer, in the chat** (the payments flow, part 1). Needs: D5.
- Pay on a bill card gives the buyer a one-time account number for that bill, with the amount, a countdown and Copy; both cards turn to Paid when it lands; part paid, expired, late; "pay INV-0001" on WhatsApp and Telegram.
- Built on what exists (one-time numbers, matching, the bill mirror), so it ships early and alone.
- Done when: a linked buyer pays a bill by transfer from the chat and both sides see it paid without a reload.

**M0. Made tools: the declaration** (made tools T0). Needs: D1, D3.
- Decision 0070; `tool` and `answer` in the schema and the contract; the validator's rules; care worked out and stored; a tool's `roles` held on every way to start a workflow.
- Done when: a definition with `tool` validates or is refused for the right reason, and a member outside a workflow's roles cannot start it by any path.

### Stage 2: the first things people use every day

**M1. Your tools** (made tools T1). Needs: M0.
- The index of deployed tools; **Your tools** in the panel and on `/`; commands on WhatsApp, Telegram and Slack; the form; care; the `text` answer; **Use it from chat** in the builder; the SDK.
- A tool's input of a type a saved kind has (a place, a person, a bank account, a phone, a date) offers the person's and the business's **Saved** objects of that kind in its form (S1).
- Done when: a person makes "Log an expense" without Advanced and uses it from the web, the phone app and WhatsApp.

**P1. Pay one person, safely** (paying people phase 1, with saved objects phase 3). Needs: S1, D4, D6, D7 before release.
- `payments`, `pay_limits`, `money.pay`; payees as bank account objects; the `pay-person` workflow; the Pay tile and sheet; the bank list and the name check; "what for"; the passkey on confirm; the account-changed and first-payment warnings; limits and the owner's OK; diary lines, the receipt, the money log; the first-payment warning on payroll and supplier approvals.
- **Paying a saved account:** the Pay sheet's "who" is a saved account (or a new one, saved at the end); "send 20k to @mum for her birthday" in the web chat and the app opens the sheet filled in; a person's own object paid from a business is recorded as personal (owner's drawings).
- Done when: an owner pays a rider ₦45,000 from Tools in under 30 seconds, recorded as a delivery; "send 20k to @mum" opens the filled-in sheet and pays on a passkey; a changed account for a past payee is stopped for the owner's OK.

### Stage 3: words, actions and answers

**S2. Saved objects in actions** (saved objects phase 2). Needs: S1.
- Delivery, invoices, orders and forms take a saved place, person or account ("deliver to @warehouse", "invoice @acme 250k"); date fields remind ("remind me when @car insurance is due"); questions about an object's details answered in the chat; the reader learns `@name` (names and kinds only).
- Done when: an owner sends an order to `@warehouse` and an invoice to `@acme` by words, each filled in, and gets the insurance reminder on its day.

**M2. Cards, lists and buttons** (made tools T2). Needs: M1.
- `card` and `list` answers drawn with the fold-and-open cards; buttons on answers and on `notify.tell`; the sheet for a list.
- Done when: "Open tickets" lists, and **Assign to me** on a row works from a phone.

**M3. The reader knows your tools** (made tools T3). Needs: M1, S2 (one reader).
- Tools given to the reader with Taskiem's commands and saved objects' names; proposed values, including `@objects` as inputs; the numbered pick; evaluation cases in English and Pidgin.
- Done when: "spent 4,500 on diesel" opens the expense tool filled in; "send 20k to @mum" still reaches payments; no built-in command is taken by a tool.

**P2. Pay from a message** (paying people phase 2). Needs: P1.
- The details reader gains the NUBAN check and candidate banks; the **Pay this account** chip on web chat and business-chat messages and photo text; a chip already paid says so; **Save as…** beside it (S1); Pay on staff, riders and refunds; the reader takes "transfer 45k to 0123456789 gtbank".
- Done when: account details in a WhatsApp message that reached the web chat become a checked, filled-in payment in one tap, and can be saved as `@driver` on the way.

### Stage 4: many at once, and asking back

**P3. Pay several people** (paying people phase 3). Needs: P2.
- Batches: picking (saved accounts as a list), collecting from messages, pasting and photographing a list, last time's again; a check per row, held-out rows, one confirmation, results per person, paying the failed ones again.
- Done when: 12 riders are paid in one batch, a bad account is held out before anything moves, and the others are paid.

**M4. A question inside a run** (made tools T4). Needs: M1.
- `to: "starter"` on the form step: a run asks in the chat it was started from, and goes on.
- Done when: a run asks "Which branch?" in the chat and goes on with the answer (a saved place offered among the choices).

**S3. Saved objects in workflows** (saved objects phase 4, first half). Needs: S2, M1.
- A business's objects as workflow inputs (the builder's picker; read when the run starts); the import from a file; "save this?" suggestions after repeated actions.
- Done when: a workflow "pay `@landlord` on the 1st" runs, its payment prepared for the owner's passkey (with P1), and a changed landlord account is stopped as any payee's is.

### Stage 5: payroll, customers and the builder

**P4. Payroll and the chat apps** (paying people phase 4). Needs: P3.
- The Payroll tile over `run-payroll`; adjustments; its approval card in the chat; payslips to each person's chat; the WhatsApp, Telegram and Slack words with confirmation by link or code ("send @driver 45k" on WhatsApp opens the sheet by link).
- Done when: this month's payroll is reviewed, adjusted, approved and paid from the chat, and a payment prepared on WhatsApp is confirmed in the app.

**M5. Customers' tools** (made tools T5). Needs: M2.
- The public menu carried over; answers for customers with `public` outputs only; in the app, customers' tools in a business's customer chat (Taskiem for everyone's customer door rules apply).
- Done when: a customer picks from **menu**, fills the form, says **yes** and gets the answer.

**M6. Recipes and the AI builder** (made tools T6, saved objects phase 4, second half). Needs: M3, S3.
- The eight recipes; "Describe it" drafts the workflow, its inputs and outputs and the `tool` block, using the business's saved objects by name ("every Friday send `@price-list` to last week's customers").
- Done when: "Describe it" produces a working tool from one sentence, naming a saved object.

### Later, each as its own plan

- **M7.** Made tools for linked businesses (made tools T7).
- **Payments flow, later parts** (statements, several bills at once), as the founder agrees them.
- **Paying from a person's own side** (You, with no business): not planned; saved accounts on You are shared and copied only.
- **Schools** ([schools in the chat](schools-in-the-chat.md)) and the rest of the automation list: planned beside this, sharing its parts (saved objects for schools, made tools for school routines).

## 5. Order at a glance

| Stage | Saved objects | Made tools | Payments |
| --- | --- | --- | --- |
| 1 | S1 objects and the picker | M0 the declaration | F1 pay a bill by transfer |
| 2 | | M1 your tools | P1 pay one person (pays saved accounts) |
| 3 | S2 objects in actions | M2 cards and buttons; M3 the reader | P2 pay from a message |
| 4 | S3 objects in workflows | M4 asking back | P3 several people |
| 5 | (M6 with the builder) | M5 customers; M6 recipes and the builder | P4 payroll and chat apps |

Within a stage the three tracks can be built at once by separate people or sessions; across stages, each phase waits only for what it **needs**.

## 6. For each phase, as built

Each phase adds an **As built** section to its own plan (saved objects, made tools, paying people, the payments flow), as Taskiem for everyone's stages did: the migrations, the API, what changed from the plan and why, the tests, and a review's fixes. This plan is updated with a tick against each phase as it merges:

- [ ] S1 · [ ] F1 · [ ] M0
- [ ] M1 · [ ] P1
- [ ] S2 · [ ] M2 · [ ] M3 · [ ] P2
- [ ] P3 · [ ] M4 · [ ] S3
- [ ] P4 · [ ] M5 · [ ] M6

## 7. Risks

| Risk | What keeps it small |
| --- | --- |
| Money sent to the wrong account | Payees are saved objects with the payments plan's checks: the bank's name shown large, the changed-account stop, the first-payment warning, step-up on every confirm, a security review of P1 before release |
| One reader trying to do three things (commands, tools, objects) | One order of reading, fixed in M3 and tested with evaluation cases; built-in commands always first; objects and tools only ever proposed, never acted on without the form or the sheet |
| Sensitive values reaching the model | The model is given names and kinds only; a test reads what is sent, in S1, M3 and P2 |
| Three tracks at once colliding in the same files (the picker, the reader, the cards) | Each shared part has one owner phase (§1); later phases extend it rather than copy it |
| Saved objects used as a database | Limits, and the Saved page pointing a large made-up kind to tables |
| A made tool that pays | Care is worked out from its steps: a payment step makes it step-up, and it still pays through P1's sheet and checks |
