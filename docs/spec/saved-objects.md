# Saved objects: things a person or a business keeps, and calls up by name

Status: **plan, settled** (19 November 2026), written with the founder. Nothing here is built. The founder took every recommendation in §15 the same day. It is built in the phases of [the build plan for saved objects, made tools and payments](build-plan-saved-tools-payments.md).

Builds on: [chat tools](chat-tools.md) (tags), [paying people from the chat](chat-pay-people.md) (a plan, not built: its payees), [Taskiem for everyone](taskiem-for-everyone.md) (You, the person's own scope, saved addresses), [people chatting with people](people-chatting.md), [data tables](../data-tables.md), [Schools in the chat](schools-in-the-chat.md).

## 1. Summary

A person or a business can **save an object** in Taskiem, give it a short name, and call it up anywhere by typing that name after `@`: `@mum`, `@landlord`, `@warehouse`, `@bank-details`. Taskiem knows what kind of thing it is and what is in it, so the name can stand for the whole of it:

- "send 20k to **@mum** for her birthday present" opens the payment, already filled in: Mum's bank, account number, the bank's own name for the account, ₦20,000, "birthday present";
- "deliver to **@warehouse**" fills in the address, the landmark and the phone for the rider;
- "**@bank-details**" in a chat with a customer drops in the business's account details as a card they can copy;
- "remind me when **@car** insurance is due" reads the date on the car.

Today Taskiem's tags are what we built into it: businesses, products, services, orders, invoices, bills. Saved objects make the list the person's own. Because they are used in every chat, in commands, in forms and in workflows, they are designed as one of Taskiem's core parts, not a feature of one screen.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Object | One saved thing: a name, a kind, and its details. "Mum (bank account)" |
| Name | What it is called up by, after `@`: lowercase letters, digits and `-`, 2 to 32 characters (`mum`, `bola-stores`, `bank-details`). Unique for its owner |
| Title | What it is shown as: "Mum", "Bola Stores", "Our bank details" |
| Kind | What sort of thing it is, and so what its details are and what Taskiem can do with it (§4) |
| Owner | A **person** (on You, private to them) or a **business** (shared with its people) (§5) |
| Use | Calling an object up in a message, a command, a form or a workflow (§6) |
| Share | Sending an object's details to someone else, as a card (§6.2) |

## 3. Where we are

| Today | Where | What this needs from it |
| --- | --- | --- |
| Tags: `@` businesses (and, on You, people), `#` products and services, `/invoice` and the like documents. Each tag is checked on the server and kept on the message with its kind | `api/mentions.go` (`checkMentions`), `engine/links/messages.go` (`Mention`), `mobile/lib/chat/tags.dart`, `web/src/lib/mentions.ts` | A new kind of tag, `saved`, in the same picker and the same checks |
| Tagged things show as cards that fold to a row and open on a press | `ChatRefCard.tsx`, `ref_card.dart` | The same card for an object |
| Saved addresses on You: a label (Home, Mum's), the line, area, landmark, a phone; only the person reaches them; copied into an order, never shown to the business as a list | `person_addresses` (migration 00465) | The first kind of object, kept and moved over (§12) |
| Payees in the payments plan: a person or business paid before, the account number sealed, the bank's checked name, changing the account needs the owner and a passkey, a red warning when an account changes | [chat-pay-people.md](chat-pay-people.md) (plan) | A bank account object **is** a payee: one store, the plan's safety (§8) |
| Data tables: a business's own rows, kept in Xenibase, with columns and a personal-data flag kept out of logs and the AI | `00173_data_tables.sql`, [data tables](../data-tables.md) | Not the store for objects (rows live outside, for records not a person's own things); the personal-data rule is reused |
| Sealing: sensitive values sealed under the person's own key or the business's subject | `engine/secrets` | Sensitive details sealed the same way (§8) |
| Taskiem's chat words: commands ("invoice Ada 25,000", "spent 12k on diesel") and questions answered by the model | `api/whatsapp_chat.go` (`converse`), `api/chat_tables_ask.go` | Words that name an object (§6.1) |
| Forms and workflows: inputs filled by people, tables read and written | the builder, forms | Objects as inputs (§6.4) |

## 4. Kinds of object

A kind says what an object's details are and what Taskiem can do with it. Most needs are met by a few kinds Taskiem understands; anything else is a kind the owner makes (§4.2).

### 4.1 The kinds Taskiem knows

| Kind | Its details | What Taskiem does with it |
| --- | --- | --- |
| **Bank account** | Bank, account number, the bank's name for it (checked when saved), a note | Pay it (§7); share it as a card with Copy; put it on an invoice as where to pay |
| **Person** | Name, phone, email, an address (or a place object), a birthday, a note | Message them, invite them, fill a form or an order; birthday reminders |
| **Place** | Line, area, landmark, a phone for the rider, a map pin when there is one | Deliver to it, book at it, put it on an order or an invoice; share it with directions |
| **Text** | A block of words, with an optional photo or file | Dropped into the message being written, to change before sending (`@bank-details`, `@delivery-areas`, `@price-list`, `@terms`) |
| **Account with a provider** | The provider (an electricity company, DSTV, a phone network, a utility, a school), the number (meter, smartcard, phone, student number), the name on it | Fill a bill or airtime payment when one exists; share it; know a school's student by it |
| **File** | A photo or document, with a title (CAC certificate, logo, menu PDF) | Send it in a chat; attach it to an invoice or a form |

A business's own kinds sit beside these where it has them: a supplier is a **business** (linked, or a person object until linked); a customer is a customer. An object never copies what Taskiem already keeps (§11).

### 4.2 Kinds the owner makes

When none fits, the owner makes a kind: a name ("Car", "Generator", "Tenant", "Member") and its fields, each with:

- a label ("Plate number");
- a type: text, number, money, date, phone, email, a choice from a list, a place, a bank account, a photo or file;
- **required** or not;
- **sensitive** or not (§8): sealed, masked in lists, never sent to the AI, shown only on a fresh unlock.

Because each field has a type, Taskiem still knows what to do with a made-up kind: a **date** field can remind ("Car: insurance due 3 March"), a **bank account** field can be paid, a **place** field can be delivered to, a **phone** can be called or messaged. A kind can be changed later; objects keep what they had, and new fields start empty.

Proposed: made-up kinds from the first phase for businesses (it is what makes this useful in any scenario); for people too, with a few starters to copy (Car, Generator, Pet, Medical card).

## 5. Who owns an object, and who can use it

### 5.1 Two homes

- **Mine** (the person's, on You): private. Only the person sees or uses it, in any chat, on You or inside any business they work in. A business never sees the list. Mum's account, home, office, the car.
- **The business's** (or a workflows workspace's): shared with its people by role. The business's bank details, its warehouse, its landlord, its regular suppliers' accounts, its standard replies, its generators.

When saving, the person chooses where it goes. Proposed default: **Mine** on You; **the business's** inside a business, with "Keep it to myself" one tap away.

### 5.2 Inside a business

| Who | Can |
| --- | --- |
| Everyone in the business | Use its objects in messages and commands (call up `@bank-details`, deliver to `@warehouse`) |
| Owners, admins, and a role given **Manage saved things** (`saved.manage`) | Add, change and remove them |
| Those who may pay (`money.pay`, from the payments plan) | Pay one of the business's bank account objects; its account number shown in full only to them, on a fresh unlock |

Each change is in the business's audit chain (who, what, before and after, with sensitive values masked).

### 5.3 Mine, used inside a business

A person's own object can be used in a business chat or command (a place on an order, a text in a message; for an owner, sending 20k to `@mum` from the business's money). What the business keeps is a **copy of what was needed** at that moment (the payee on the payment, the address on the order), as #198 does with saved addresses. The object itself, and the rest of the person's list, never become the business's.

A payment to a person's own object from a business's account is for **owners only**, and is recorded as **personal** in the business's books (§7.3).

## 6. Using an object

### 6.1 Calling it up

- **Typing `@`** opens the picker, as for businesses and people today, with a **Saved** group: the owner's objects first (Mine, then the business's when inside one), each with its kind's icon and a hint ("GTBank ••6789", "Yaba", "4 lines of text").
- **Typing a name** filters as today; **`@mum`** typed in full and sent is matched by name on the server even when not picked from the list.
- **Two things with one name** (a contact called Mum and a saved Mum; Mine and the business's `@bank-details`): the picker shows both, each labelled; a typed name the server cannot tell apart is asked ("Which @mum? Your saved Mum (GTBank ••6789) or Mum in your contacts").
- **Nothing found**: the picker's last row is **Save "mum"…**, which starts a new object with that name.

### 6.2 In a chat with someone (a business, a customer, a person, a group)

Calling up an object in a message **shares** it: the message carries it as a card, folded to a row like orders' cards, that opens to its details with **Copy** beside each and **Save this** for the receiver (a copy of their own).

- **Text** objects drop their words into the box instead, to change before sending; nothing is shared but the words.
- **Before sharing anything sensitive** (an account number, a phone, an ID number), the sender confirms: "Share Mum's account details with Bola Stores?". A field marked **never share** stays out of every card.
- **A copy, not a link:** the receiver gets the details as they were when sent. Changing the object later does not change what was shared (proposed; §15).
- **A receiver on WhatsApp, Telegram or Slack** (most customers) gets the card as plain lines: the title, then each detail on its own line, easy to copy. The same sensitive check runs first.
- In a business's chat with a customer, the card follows that chat's rules (#198): a business sharing `@bank-details` with a customer shows the transfer warning only where it is a personal account.

### 6.3 In a command to Taskiem

In the Taskiem chat (and over WhatsApp, Telegram and Slack), an object stands for its details in any command that needs them:

| Command | What the object gives |
| --- | --- |
| "send 20k to @mum for her birthday" | The payee: the payment flow, filled in (§7) |
| "deliver order P-7QX to @warehouse" | The place: the delivery's address, landmark and phone |
| "invoice @acme 250k for the October job" | The person or business: who it is to, their email and address |
| "remind me when @car insurance is due" | The date field: a reminder on that day, and the year after |
| "what's @generator's last service date?" | A field's value, answered from the object |
| "share @bank-details with Ada" | The card, into Ada's chat |
| "save this as @driver" (replying to a message with account details) | A new object, read from the message (§9) |

On WhatsApp, Telegram and Slack there is no picker: `@name` typed in a message to Taskiem is matched on the server, and two things with one name are asked as a numbered pick. Commands that pay work there only from the payments plan's last phase, by link or code.

Taskiem reads what kind an object is to know which of its details a command needs; a command that cannot use an object's kind says so ("@warehouse is a place: who should the money go to?").

### 6.4 In forms, orders and workflows

- **Anywhere a form, an order or a sheet asks for** an address, a phone, a person, a bank account or a date, a **Saved** button offers the owner's objects of the right kind (saved addresses at checkout today, made general).
- **Workflows** of a business or a workflows workspace may take the business's objects as inputs ("pay `@landlord` on the 1st"; "every Monday, send `@price-list` to the customers who ordered last week"). A workflow reads the object when it runs, so a changed object is used from the next run; a payment run still checks the account as the payments plan does. A person's own objects are never a business workflow's input.

## 7. Paying a saved account

This joins [paying people from the chat](chat-pay-people.md) and depends on its first phase.

### 7.1 One store

A **bank account object is a payee** in that plan's sense: the account number sealed, the bank's checked name kept, a passkey to change the account, the account-changed and first-payment warnings. There is one list of saved payees, not two: the plan's payees are the business's bank account objects, and paying someone new offers **Save as…** at the end. Staff's and riders' bank details stay on their own records (§11); the Pay sheet picks from those too, with the same checks.

### 7.2 "send 20k to @mum for her birthday present"

1. Taskiem reads the command: an amount (₦20,000), a payee (`@mum`, a bank account), what it is for ("birthday present").
2. It opens the plan's **Pay sheet**, filled in: Mum · GTBank · ••6789 with the bank's name for the account checked again and shown large; ₦20,000 with the balance after; "What for: Other, birthday present".
3. The person confirms with a passkey or code, as every payment does; over their limit it waits for the owner's OK.
4. The plan's checks all run: the name check, an account changed since it was saved (red, stopped for the owner), a duplicate within 24 hours ("Pay again?").

Taskiem never pays from words alone: the sheet and the confirmation are always shown.

### 7.3 From which money

Money leaves only from a business's own payments account, as the plan says. Paying the person's own `@mum` from a business is allowed only to an **owner**, recorded in the business's books as **personal** (owner's drawings), and shown so in its money log. Staff, whatever their limits, are offered the business's saved accounts only: money to a staff member's own private account, booked as the owner's, would be wrong in the books and a quiet way to pay oneself. On You, with no business, there is nothing to pay from: `@mum` can be shared or copied but not paid (proposed; §15).

## 8. Safety and privacy

- **Sensitive details** (an account number, an ID or BVN-like number, a phone or email where the owner marks it, any field marked sensitive) are **sealed** at rest, under the person's own key for Mine and the business's for its own, as phones and orders are under #198. Lists show them masked (••6789); the full value needs a fresh step-up proof on the server (a passkey, which the phone's lock releases in the app, or an authenticator code).
- **The AI never sees sensitive values.** It sees an object's name, title and kind ("@mum, a bank account"), as data tables' personal columns are kept from it today. Commands are read by the model with names; the values are filled in by the server.
- **Changing a bank account** on an object needs a passkey and is recorded; the next payment to it shows the red "not the account you paid before" warning until the owner confirms (the payments plan's fraud check).
- **Sharing** sensitive details asks first (§6.2); **never share** fields never leave.
- **Limits:** up to 500 objects for a person; a business by plan (proposed 2,000 on the lowest plan); 40 made-up kinds; 30 fields a kind; a text object up to 4,000 characters; a file object up to the chat's file limit.
- **Deleting:** a person's objects go with their account (as saved addresses do); a business's with the business, or by an erasure request where they hold a person's data. Copies already shared or used (a payment's payee, an order's address) stay where they were sent, as records.
- **Audit:** every add, change, reveal of a sensitive value, share and use in a payment is recorded (the person's own log for Mine; the business's chain for its own).

## 9. Saving an object

Saving should take seconds, from wherever the details already are:

1. **From a message** (any chat): hold it, **Save as…**; Taskiem reads what is in it (an account number and bank; an address; a phone; an email) and offers the kind and the fields filled in.
2. **From the picker:** **Save "mum"…** when nothing matches.
3. **After an action:** after paying someone new ("Save Chidi as @driver?"), after a delivery to a new address, after a new customer's first order.
4. **In words:** "save mum: GTBank 0123456789" or "save @warehouse: 12 Herbert Macaulay Way, Yaba, opposite the filling station"; Taskiem shows the object it understood, with the bank's name check for an account, and saves it on **Save**.
5. **The Saved page** (§10): add, change, remove, by kind.
6. **For a business, from a file:** a CSV of suppliers' accounts or branches, as students and products are imported.
7. **Suggested:** "You've sent money to this account three times. Save it as an object?" (from the automation discussion: suggest, never surprise).

A bank account is checked with the bank as it is saved; a name that does not match what the person typed is shown in amber, as the payments plan does.

## 10. Screens

- **Saved**, on You's Settings and in each business's Settings (and from the tools panel as a tile): a search; the kinds as chips (All, Accounts, People, Places, Texts, Files, then the made-up kinds); each object a row (its icon or photo, title, `@name`, a hint, masked); **Add**.
- **One object**: its fields (sensitive ones masked, **Show** after unlocking), **Use** (start a message or a command with it), **Share**, **Edit**, **Delete**; for a bank account, the payments made to it; for a made-up kind with dates, what is coming up.
- **Kinds**: the made-up kinds, each with its fields, to add, reorder, mark required or sensitive.
- **The picker and the card** in every chat, as §6.
- Built on the web first and matched in the app, in the app's own look, light and dark, at 360 px, as every screen is.

## 11. What an object is not

- **Not a copy of Taskiem's own records.** A product, a customer, a linked business, an order, a student stays what it is; tags for those stay as they are. An object may point to one ("@bola is our supplier Bola Stores") rather than copy it.
- **Not a table.** Data tables are a business's records, many rows of one shape, kept in Xenibase. Objects are a person's or a business's named things, few of many shapes, kept in Taskiem's own database where they can be sealed and checked on every use. A made-up kind with hundreds of objects is a sign it wants to be a table, and the Saved page says so.
- **Not a contact book.** People chats have contacts (people on Taskiem). A **person** object is someone the owner keeps details of, whether or not they are on Taskiem; when they are, it can point to them.

## 12. Data model, server and API

- **`saved_kinds`**: an owner's made-up kinds: `id`, the owner (`person_id` or `tenant_id`, one of them), `name`, `fields` (an ordered list: key, label, type, required, sensitive, never_share, options for a choice), timestamps. Taskiem's own kinds (§4.1) are fixed in code, not rows.
- **`saved_objects`**: `id`, the owner (`person_id` or `tenant_id`), `name` (unique per owner, lowercase), `title`, `kind` (a known kind or a made-up kind's id), `details` (the fields that are not sensitive), `sealed` (the sensitive ones, sealed), `hint` (what lists show, masked), a photo, `points_to` (a contact, a linked business, a customer, when it is one), `created_by`, `updated_by`, timestamps, `archived_at`.
- **Row-level security:** Mine through the person's own scope (`taskiem_person_scope()`, as `person_addresses`); a business's under tenant isolation, with `saved.manage` for changes.
- **Payees:** the payments plan's `payees` table becomes `saved_objects` of kind bank account for the business, with the plan's `payments.payee` pointing to it (§7.1). A payment to an owner's own object keeps a sealed copy of the account on the payment instead, since the business cannot read the object; the changed-account check compares with the copies on that owner's earlier payments. The plan is updated to say so before its first phase is built.
- **Saved addresses:** `person_addresses` rows become Mine **place** objects; `/v1/me/addresses` stays as it is, reading and writing them, so the app and checkout need no change.
- **Tags:** `links.Mention` gains the `saved` kind (the object's id on the sender's copy; the shared card's details on the receiver's), checked by `checkMentions` and its people-chat and customer-chat equivalents: the sender may use it, it is not archived, sensitive sharing was confirmed.
- **API:** `/v1/me/saved` and `/v1/saved` (list, search, get, add, change, delete, reveal with a fresh proof), `/v1/me/saved-kinds` and `/v1/saved-kinds`, `/v1/saved/resolve?name=` (for the picker and commands; both owners when inside a business), and the import. OpenAPI, the app's generated client and the SDK as always.
- **Commands:** `converse` resolves `@name` to an object before acting; the model is given names and kinds only (§8).

## 13. Phases

1. **Objects and the picker.** Built in two parts in the build plan: the store for saved accounts first (S0), so payments need not wait, then the rest (S1). Known kinds (person, place, text, file, account with a provider, bank account shown and shared but not yet paid), made-up kinds, Mine and the business's, the Saved page, `@` in every chat, cards with Copy and Save this, text objects into the box, sharing with confirmation, saving from a message, the picker and words; saved addresses moved over; sealing, limits and audit.
   - Done when: a person saves `@mum` (a bank account) from a message in under 15 seconds, shares it with a friend as a card they can copy, and a business's staff drop `@bank-details` into a customer chat.
2. **Objects in actions.** Delivery, invoices, orders and forms take a saved place, person or account; date fields remind; questions about an object's details answered in the chat.
3. **Paying a saved account.** With the payments plan's first phase: bank account objects as payees, "send 20k to @mum", the personal tag in the books, the change and first-payment warnings.
4. **Objects in workflows and suggestions.** A business's objects as workflow inputs; the import; "save this?" suggestions.

## 14. Tests

- **Server:** the owner's rules (Mine never reached by a business or another person; a business's by role); sealing and masking; reveal needs a fresh proof; the AI's context holds no sensitive value (a test reads what is sent); a tag naming another person's object refused; sharing sensitive without the confirmation refused; names unique per owner and ambiguity asked; limits; deleting an account removes Mine and leaves copies already used; saved addresses read the same through `/v1/me/addresses` after the move; migrations down and up.
- **Web end-to-end and app:** saving from a message; the picker with Saved and two of one name; a card folding and opening, Copy, Save this; a text object into the box; the Saved page and a made-up kind; axe in both themes; 360 px.
- **Payments (phase 3):** the payments plan's tests, with the payee being an object.

## 15. Settled (19 November 2026)

The founder took each recommendation:

| # | Question | Settled |
| --- | --- | --- |
| 1 | Marks | Saved objects come up under **`@`**, in their own Saved group beside businesses and people. `/` stays for documents (`/order`, `/invoice`) and [made tools](made-tools.md)' commands; `/` with a word that is neither searches saved objects, so `/mum` still finds Mum |
| 2 | Who owns by default | **Mine** when saving on You; **the business's** when saving inside a business, with "Keep it to myself" one tap away |
| 3 | Paying a person's own object from a business | Allowed for **owners** (narrowed from "those who may pay" by the review of 19 November 2026, and confirmed by the founder the same day: §7.3), recorded as **personal** (owner's drawings) in the business's books. On You, with no business, objects are shared and copied, not paid |
| 4 | Shared cards | **A copy**: changing an object never changes what someone was already sent |
| 5 | Made-up kinds | In the **first phase**, for people and businesses, with starters to copy |
| 6 | Limits | **500** for a person; **2,000** for a business on the lowest plan, more on higher plans |
| 7 | The name in the product | **Saved**: the page is "Saved", one is "a saved …" ("a saved account", "a saved place") |
