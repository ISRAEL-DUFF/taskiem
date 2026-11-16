# Taskiem for everyone: one chat app for people and the businesses they deal with

Status: **draft for review** (2026-11-16; its first questions settled the same day, §23; revised after a review of gaps the same day, and that review's questions settled too, §24). Nothing here is built. It replaces the earlier draft "Customers at the door" (both its parts).

Decision [**0068**](../decisions/0068-taskiem-for-everyone.md) is **accepted** (16 November 2026), amending [0039](../decisions/0039-reaching-people-outside.md) and [0066](../decisions/0066-a-flutter-phone-app.md). Builds on: [the phone app](mobile-app.md) ([0066](../decisions/0066-a-flutter-phone-app.md)), [chat push](chat-push.md), [chat as the way in](chat-way-in.md), [chat sign-in](chat-sign-in.md), [customers' conversations](chat-five-more-flows.md#as-built-phase-1-customers-conversations), [business links](business-links.md), [storefronts](storefronts.md).

## 1. Summary

The Taskiem app becomes **a chat app anyone can use**, like WhatsApp and Telegram. A person signs up with their phone number and chats:
- with **businesses**: to ask, order, book and pay;
- in time, with **other people** (§16).

Running a business is something a person *does in* Taskiem, not what they must be to use it. Nor is a business needed to automate work: a person can start **workflows** on their own or with a team, with no shop or business questions (§18). An owner or a member of staff is a person with a business attached. A customer is a person with none.

For businesses this changes how customers reach them:
- **In the Taskiem app, or in the phone's browser** with nothing to install (§9). Each business is its own conversation, under its own name and photo, with its own notifications. Orders, bookings and payments happen in the conversation, as cards. There is no 24-hour window and no template. This works for every business, whether or not it has a WhatsApp number of its own.
- **On WhatsApp,** for a business that has connected its own number, as today (§11). The same customer may use both. The business sees one customer.
- **Taskiem's shared WhatsApp number and Telegram bot** stay as they are for outsiders: updates, one way. They now point customers to the app or the browser to chat (§12).

The work comes in stages (§20):
1. personal accounts;
2. customers chatting with businesses, in the app and the browser;
3. finding businesses, and a customer's own orders;
4. one customer across the app and WhatsApp;
5. people chatting with people, under its own spec.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Person | Anyone with a Taskiem account. Every account is a person's |
| Personal account | A person's account, made with a phone number. It may belong to no business (a customer) or to several (an owner, staff) |
| Business | What a person runs or works in: a tenant, with its members, as today |
| Customer conversation | A conversation between a person and a business they buy from. On the business's side it is a customer thread; on the person's, a chat with the business |
| Line | Where a customer conversation runs: **app** (the Taskiem app or the browser chat) or **WhatsApp** (the business's own number) |
| Chat link | A business's link that opens a customer conversation with it: `https://<host>/m/{code}`. In the app if installed, else in the browser |
| Customer door | The narrow, audited way a person who is not a member reaches one business's data: its shop, free times, their own orders with it, placing an order and paying (§8.4) |
| Second proof | Something besides a code to the phone that shows the account is the same person's: a passkey, a link to the account's email, or the chat PIN (§5.4) |
| Discoverable | A business that chose to be found by the public in the app (§10). Not the same as being found by other businesses in Explore |
| Workflows workspace | What **Start something > Workflows** makes: a tenant with every business area off, for automating work alone or with a team, with no business questions (§18) |
| You | The personal side of the app: your chats with businesses and people, your orders. Beside it, each business you work in |

## 3. Where we are

| Today | Where |
| --- | --- |
| **Every account has an email.** `users.email` is required and unique. An account made in chat has a placeholder (`owner+…@phone.taskiem.invalid`) with its WhatsApp number as the real credential | `00002_identity.sql`, `api/agents.go` (`ownerPlaceholderEmail`), `whatsapp_bindings` |
| **A session needs a business.** Signing in with no membership fails ("no active membership"), except for a person with invitations waiting, who sees only those | `api/passkeys.go`, `api/auth.go`, `web/src/App.tsx` (`InviteeHome`) |
| **Every conversation is a business's:** the Taskiem conversation; chats with linked businesses (two copies, one per side, `link_messages`); customer threads on the business's own WhatsApp number (`customer_conversations`). All tenant-scoped under row-level security | migrations 00294, 00411 |
| **Customers are outsiders:** a phone number on an order, never a Taskiem user | [storefronts §3](storefronts.md#3-goals-and-non-goals), [0039](../decisions/0039-reaching-people-outside.md) |
| **The app is for owners and staff,** and "does not replace WhatsApp, Telegram or Slack" | [mobile-app.md §1–2](mobile-app.md) |
| **Sign-in by phone exists:** a code to a WhatsApp number or Telegram account; a phone OTP on storefront forms | [chat sign-in](chat-sign-in.md), [storefronts §7](storefronts.md#7-trust-and-safety) |
| **Links that open the app** (`/chat`, `/chat?with=…`, document pages); push to the app; push to the browser | [mobile-app.md §5.3, §5.6](mobile-app.md), [chat push](chat-push.md) |
| Customers on the shared WhatsApp number and Telegram bot get updates only; anything they send gets one fixed answer | `api/reach.go`, 0039 point 7 |
| **A phone's personal-data subject is different in every business.** It is a keyed pseudonym made with that business's own key, so the same phone has a different `subject_id` in each business and nothing matches across them | `engine/secrets/subjects.go` (`baseSubjectID`) |
| **Businesses' sign-in rules:** single sign-on required (`sso_required`), passkey-only people, administrators required to use passkeys (`require_admin_passkeys`). Chat sign-in already refuses where single sign-on is required | `api/chat_signin.go`, `api/passkeys.go`, migration 00315 |
| **Customer media on an own number is not kept:** a photo or voice note shows as what it was | migration 00411 (`customer_messages.kind`) |
| `/c/{token}` is taken twice: the connect-a-service page in the web app, and the customer account page on the public pages | `api/connect_links.go`, `web/src/App.tsx`, `engine/publicweb/customer.go` |

## 4. The decision this needs (0068)

The decision is [0068](../decisions/0068-taskiem-for-everyone.md), accepted on 16 November 2026. It also records the review's answers (§23) and Start something (§18); the points below are its core.

1. **Taskiem is a chat app for everyone.** Anyone may make a personal account with a phone number and use the app and the web chat with no business. This replaces three earlier rules:
   - [mobile-app.md](mobile-app.md) §1–2: "for owners and their staff", "does not replace WhatsApp";
   - [storefronts §3](storefronts.md#3-goals-and-non-goals): customer accounts as a non-goal;
   - [0039](../decisions/0039-reaching-people-outside.md): customers always outsiders. 0039 is narrowed, not replaced: it still holds on the shared WhatsApp number and Telegram bot (point 4 below).
2. **A person's own side is separate from every business.** What a person does as a customer, and later with friends, is theirs. No business sees it unless the person sends it that business. What a person does inside a business stays the business's, as today.
3. **Customer conversations have two copies,** the business's and the person's, as conversations between businesses already do (`link_messages`). Each side keeps, archives and deletes its own.
4. **The shared WhatsApp number and Telegram bot stay one-way for outsiders** (0039 points 1 to 7 stand for people who are not in the app). They gain a link to chat in the app or browser.
5. **People chatting with people** (§16) is a later stage with its own spec. Its own decisions (encryption, moderation, contacts) are taken there. Nothing in stages 1 to 4 depends on them.

[0058](../decisions/0058-chat-as-a-way-in.md) is unchanged: signing a *business* up from chat works as today.

## 5. Accounts

### 5.1 Making one

The app's first screen and the web's `/signup` and `/login` become one way in:

> **Welcome to Taskiem**
> Chat with the businesses you buy from, and run your own.
>
> [ Your phone number ]  **Continue**

1. **The phone number.** Taskiem sends a 6-digit code:
   - by WhatsApp when the number is on WhatsApp (as chat sign-in does);
   - else by SMS;
   - **Send by SMS instead** is always offered.

   Five wrong codes lock the number for an hour. At most five codes are sent per number per hour, and limits per device and per IP apply too.
2. **The code.**
   - A number that already belongs to an account signs in to it: chat-born owners and staff, and any account with that number bound (§5.3). The phone code opens the **person's own side**. Entering a business still follows that business's rules (§5.4): single sign-on where it requires it, a passkey for passkey-only people and for administrators where the business requires one, and step-up for money as today. A new device may also need a second proof (§5.4).
   - A new number makes a **personal account**, with a name (required) and a photo (optional).
   - Agreeing to the terms and privacy notice, and confirming they are 18 or over (§15), is recorded, as for every way in today.
3. **Then the app's home** (§7), on **You**. A **Start something** button is always there: a business or workflows (§18). What it makes is attached to this person.

**Email and password stay** for people who have them: **Sign in with email** under the phone box, passkeys, and single sign-on for businesses that require it. An account can add an email later in Settings, as chat-born owners do today.

### 5.2 What an account is, in the data

- **`users.email` becomes optional.** Accounts made by phone have none. The placeholder (`ownerPlaceholderEmail`) is retired for new accounts and stays on old ones until they add a real email.
- **`user_phones`** (new): one verified phone per account, unique across accounts, kept as a keyed hash with the number sealed under the person's subject, plus when it was verified.
  - Existing `whatsapp_bindings` numbers are copied in. Where an account's bound WhatsApp number is free, it becomes its phone.
  - A number bound to two accounts is not copied; both are asked to verify once (§19).
- **A session may have no business.** The session's scope becomes either **personal** (no tenant) or **a business**, as today. A personal session reaches only the person's own routes (§8, §13); a business session also reaches the person's own routes. The invitee-only session (`invitations_only`) becomes an ordinary personal session.
- **Reissued numbers** are handled by §5.4. A code to a phone proves only that someone holds the number today, not that it is the same person.

### 5.3 People who already have accounts

| Today | After |
| --- | --- |
| Owner or staff with email and password | Unchanged sign-in. Once signed in, asked once to add their phone (skippable for 30 days). They then have **You** as well as their business |
| Chat-born owner (placeholder email, WhatsApp number) | Signs in with the phone code. Their WhatsApp number is their phone |
| A person with only invitations | A personal account; the invitations wait in **You** |
| A business's customer (a phone on orders, a thread on its own number) | Nothing changes until they make an account. When they do, with the same phone, §11 and §13 connect what is theirs |
| Owner who signed up through Slack (a Slack placeholder email, no phone) | Signs in with Slack as today. Asked once to add a phone, as email users are; until then they have no phone way in and no **You** of their own beyond what Slack sign-in gives |
| Owner who signed up on Telegram (a Telegram placeholder email; the phone shared as their own contact at sign-up) | The phone they shared becomes their phone, as a WhatsApp number does, where no other account has it. Codes for it go by SMS (Taskiem cannot send a code to a Telegram account by phone number) |

### 5.4 Business rules and second proofs

**A business's sign-in rules still hold.** The phone way in signs a person in to their own side. Switching to a business checks that business's rules first:
- **Single sign-on required:** the business opens only after its single sign-on, as today. Chat sign-in already refuses where single sign-on is required (`sso_required`); the switcher asks for it instead.
- **Passkeys:** a passkey-only person, and an administrator of a business that requires administrators to use passkeys, opens the business only with their passkey.
- **Step-up** for money and approvals is unchanged.

**Reissued numbers.** Phone companies give numbers that have been unused for months to new people, and the new holder receives the codes. So a phone code alone is never enough to reach what an earlier holder built:
- **An account that belongs to any business** needs a **second proof** the first time it signs in by phone on a new device: a passkey, a link sent to the account's email, or the chat PIN. Without one, the person reaches only the personal side, and the switcher shows the businesses as locked with how to unlock them. An account with no second proof yet is asked to set one up when it joins or starts a business.
- **A personal-only account unused for 180 days** that signs in by phone on a new device is asked "Is this a new number for you?":
  - **No, it's still mine** needs a second proof if the account has one. Without one, its chats and orders stay hidden until one is added from the old device or by support.
  - **Yes, it's new to me** starts a new personal account on the number. The old account is set aside, kept for 90 days for its owner to claim with a second proof, then deleted.
- **Any account whose phone has not signed in for 90 days** is treated as on a new device.

### 5.5 Changing your phone number

**Settings > Phone > Change number:**
1. A code to the old number, if the person still has it, or a second proof.
2. A code to the new number. The new number must belong to no other account.
3. The account keeps everything. Its personal-side order index (§13) is re-made for the new number, so orders made with the old number stay listed and new orders find the new one.
4. Businesses that had threads with the person on their own WhatsApp number (§11) keep those threads under the old number, and the app line moves to the new number. Each business's thread shows "This customer changed their number", without the number, and the two lines are matched again only when the new number writes on WhatsApp.

## 6. The app and the web chat: who sees what

**One app, one look.** Everything in [mobile-app.md](mobile-app.md) about look, motion and sound holds. The web chat stays the design, and every new screen is built on the web first.

**What a person sees depends on what they are:**
- **A person with no business** sees **You** only: chats, orders, settings. There are no business tools, no `@Taskiem` business conversation, no switcher.
- **A person in one or more businesses** sees the switcher at the top of the chat list: **You**, then each business. Each business is today's chat for that business: `@Taskiem`, its customers, its linked businesses, its tools. Unread counts show on each entry in the switcher, so nothing is missed on the side not open.
- **Notifications** say which side they are for ("Mama's Kitchen · Tolu: is the jollof ready?" against "Bola Stores: your order is on its way").

**The store listings** change from "for your business" to the new line. The first screen (§5.1) works for both kinds of people.

## 7. You: a person's own side

**The chat list:**
- businesses the person chats with (§8);
- later, people (§16);
- pinned, muted and archived chats, and search across chats, as today.

**Tabs:**
- **Chats**;
- **Orders**: their orders and bookings with every business (§13);
- **Discover**: businesses to find (§10);
- **Settings**: name, photo, phone, email, sessions, notifications, privacy, delete account.

**`@Taskiem` on You** is a help conversation: "How do I pay?", "Where is my order?", answered from the person's own orders and Taskiem's help. It never acts in a business. It is the place for **Start something** (§18).

## 8. Customers chatting with a business

### 8.1 Starting

A person starts a conversation with a business from:
- **its chat link** (§8.2), shared on its storefront, Instagram, a flyer or a receipt;
- **Discover** (§10), or the business's profile there;
- **an order or booking** they made on its storefront with the same phone: "Chat with Mama's Kitchen about this order";
- **a message from the business** in the app: a reply, an order card, a reminder, or an offer to a customer who agreed to offers. A business can write first to anyone who has chatted or ordered with it, within §15's limits.

### 8.2 The chat link

- `https://<host>/m/{code}` (`/c/` is taken, §3). The code is one per business, ten characters with no look-alikes. The owner can replace it (**Make a new link**); the old one says "This link no longer works" for 30 days.
- **Where the business finds it:** in its chat under **Tools > Customers > Your chat link**, and in Settings. It comes with:
  - a QR code to print;
  - words to paste: "Chat with Mama's Kitchen on Taskiem: {link}".
- **The storefront** gains **Chat with us**, beside or instead of **Chat on WhatsApp**, as the owner chooses.
- **What it opens:**
  - **the app,** if installed (`/m/*` joins the app's link list, `appPaths` in `api/applinks.go`, [mobile-app.md §5.6](mobile-app.md));
  - **otherwise a page** with the business's name, photo and hours, and two buttons: **Chat here in your browser** (§9) and **Get the Taskiem app**.

### 8.3 The conversation

**On the person's side:**
- the business's name, photo and a line under the name (open now, closes at 6pm);
- text, photos, voice notes, files and location; replies, reactions and ticks; typing; push notifications. All as in today's chats, which already have them.

**The tools** for a customer, behind the same tools button as today's chats:
- **Their shop**: the storefront's items and prices, in sizes and colours, with stock where the business shows it. The same sheet a linked business sees, at storefront prices.
- **Book**: the business's services and free times, as a linked business books them today.
- **Your orders with them**.
- **Pay**: an open order's or invoice's pay link, inside the app.

**Cards in the conversation:** an order card (what, total, its state, **Pay**), a booking card, an invoice card, a delivery card with the rider's progress. They are the same cards businesses already see, so the order page link is no longer needed in the app.

**On the business's side:** the same customer thread as today, in Customers, marked **in the app**. Drafted orders, replies and the rest work, with three differences from WhatsApp:
- **No 24-hour window and no templates.** A reply is always a message.
- **Rich replies.** Staff can send an order card, a pay link, a booking offer or a product from the catalogue (`#`), not only words.
- **Read ticks** are the person's own, shown only if they leave them on.

**In the app, STOP's place is taken by Mute and Block**, the person's own choice in the conversation's menu:
- **Mute** silences notifications.
- **Block** stops the business writing to them. The business sees "This customer has stopped messages". **Nothing is deleted:** the business keeps its copy, since orders and payments may be disputed later. A person who wants the business's copy gone asks for erasure (§14).

The business can block a customer too, on the same terms. **STOP on a business's own WhatsApp number** keeps today's meaning there, deleting that line's messages (§11).

### 8.4 The customer door

A person chatting with a business is not its member, yet "Their shop", **Book**, their orders and paying all read or write that business's data. Linked businesses reach each other through one audited entry point (`taskiem_link_enter`, [0061](../decisions/0061-business-links.md)); a person gets one of their own, narrower:
- **`taskiem_customer_enter(person, business)`** opens one business's data for one request, for one person who has a customer conversation with it, or is opening one from its chat link or Discover. It refuses a business that blocked the person, and a business that turned the needed area off.
- **What it allows, and nothing else:**
  - the published catalogue at storefront prices, with stock where the business shows it;
  - free times for its services;
  - the person's own orders, bookings and invoices with that business;
  - placing an order or booking, and paying one;
  - their own conversation.
- **An order placed in the app** is a storefront order of a new kind, `app`, carrying the person's name, phone and chosen address, sealed under the business's subject for that phone as storefront orders are, and linked to the person. It goes through the business's order workflow as any storefront order does.
- **Every entry is audited** in the business's chain (who, what, when), as linked businesses' entries are.
- **Limits** per person per business and per person overall (§15).

### 8.5 Photos, voice notes and files

In the app, both sides send and receive media:
- **Each copy keeps its own.** A file is stored once per side: the business's copy sealed under the customer's subject in that business, the person's under their own key. Erasing one side never reaches the other.
- **Limits:** photos up to 10 MB (made smaller on the phone first, as the chat does today), voice notes up to 5 minutes, files up to 25 MB.
- **Retention:** the business's copy follows its thread (a year, then deleted); the person's copy stays until they delete the chat or the account.
- **Checks:** files get the checks media gets today (`engine/media`: the file's real type, and PDFs refused when they carry scripts or embedded files), and the malware scan form files get (`engine/forms/scanner.go`), before the other side can open them.

On a business's own WhatsApp line nothing changes: media there is still shown as what it was, not kept.

### 8.6 When nobody answers

Customers in the app expect answers. For the business:
- **Who is told:** a customer message notifies the staff who handle orders (`order.handle`), as WhatsApp customers' messages do now, and the owner when no one has that role.
- **When the business is closed:** the conversation shows its hours under its name. A message sent while it is closed gets an automatic reply in the business's name once a day: "We're closed now. We open at 9:00 and will answer then." The business can change the words or turn it off.
- **Unanswered:** a customer message with no reply after 30 minutes in open hours goes into the business's **Needs you**. After 2 hours, the owner is told.

## 9. Chatting in the browser

**For a customer who won't install an app,** the chat link opens the conversation in the phone's browser. It is the web chat, already built, on the personal side, at `https://<host>/m/{code}` and then `/chat?with=…`.

1. **The phone code** (§5.1), in the page. No password, no email.
2. **The conversation,** as in the app (§8.3), with the same cards, tools and payments.
3. **Notifications:**
   - the browser's push, after one tap on **Tell me when they answer**, where the browser allows it ([chat push](chat-push.md));
   - where it doesn't (an iPhone with the page not added to the home screen), the answer goes by SMS: "Mama's Kitchen answered you on Taskiem: {link}", at most one per conversation per hour.
4. **The app, offered,** as a quiet line under the conversation ("Get the Taskiem app for your chats with every business"), never in the way.

**Staying signed in:** the browser session lasts 90 days on that device. **Sign out** is in the page's menu. A shared phone is warned at sign-in ("Is this your phone? If not, don't stay signed in").

## 10. Discover: finding businesses

- **The setting.** **Found by the public in the Taskiem app**, in Settings.
  - **On by default for a business with a published storefront,** since its page is already public. It can turn Discover off. Businesses with a published storefront when this ships are told once in their chat, with the switch: "Your shop is now listed in Taskiem's Discover, where customers find businesses. Turn it off here."
  - **Off for every other business** until it turns it on.
  - Being listed needs a category, an area, a photo and hours. Most of these come from the storefront; a listing missing them is not shown until they are added.
- **What a person sees.** Search by name, what they want (rice, hair, cakes) and area. Each business shows its name, photo, category, area, open now, and a **Chat** button. Results near the person come first, when they share their location; that location is never kept.
- **A business's profile** in Discover: its photos, hours, address, **Their shop**, **Book**, **Chat**.
- **Kept apart from Explore.** Explore (businesses finding businesses) keeps its own setting and its own list. A business may be in either, both or neither.
- **Moderation.** As storefronts are moderated today ([moderation](../moderation.md)): a report button on every business, and listings reviewed when reported.

## 11. Businesses with their own WhatsApp number

Their customers on WhatsApp chat there as today. Some of the same customers will also use the app.

- **One customer, one thread.** When a person in the app has the same verified phone as a customer thread on the business's own number, the business sees **one thread with both lines**. Each message is marked with where it came from. The person's app conversation shows the WhatsApp messages too, marked "on WhatsApp", since they were theirs.
- **Where a reply goes.** By default, on the line the customer last wrote on. Staff can choose the other line. Replies on WhatsApp keep its 24-hour window and templates.
- **The business's chat link** for such a business opens a choice: **Chat on WhatsApp** (its own number) or **Chat on Taskiem**.

Matching by phone needs the phone verified on both sides: by the code on the app's side, and by WhatsApp having delivered the message on the other. A phone typed on a form matches nothing. Matching happens inside one business, so its own key makes the subject for the person's phone and finds the thread; no lookup across businesses is needed.

## 12. The shared WhatsApp number and Telegram bot

They stay one-way for outsiders (0039). What they say changes:
- **Updates a business sends** to an outsider on the shared number can end with "Reply in the Taskiem app: {chat link}", when the business turns that on. It is on by default for businesses with no own number. Outside the 24-hour window updates go as the `taskiem_update` template, so its new wording must be approved by Meta first (§20, before stage 2); until then the link is added only inside the window.
- **A stranger** writing to the shared number gets the sign-up offer with **Start my business**, **I use Taskiem** and **I'm a customer**. WhatsApp allows three buttons, so **Not now** becomes typing "no" or not answering. **I'm a customer** answers: "To chat with a business, open its Taskiem link, or find it in the Taskiem app: {app link}."
- **An outsider who replies to an update** gets, in place of today's fixed answer, "To chat with {business}, open {chat link}". This goes once per business per day, within the existing limits.

## 13. A customer's orders and bookings

**Orders** on **You**: every order and booking the person has with any business, newest first. Each shows the business, what, total, state, **Pay** if due, and **Chat about this**.

**What counts as theirs:**
- orders and bookings made in the app or the browser chat while signed in;
- storefront orders and bookings made with **the same phone**, once that phone is verified on the account.

**Looking up a phone's orders** reaches across businesses. That is new: every query today stays inside one business. It cannot use the orders' `subject_id`, because a phone's subject is made with each business's own key and differs in every business (§3). So:
- **A phone index** (`customer_phone_index`, new): one row per order or booking, holding a keyed hash of the customer's phone made with a **platform** key (the one `reach_contacts` uses, `TASKIEM_MESSAGE_HASH_KEY`), the business and the order. No number, name or amount is in it.
- **Written when an order or booking is made,** in the same transaction, and removed when the order's customer is erased.
- **Filled once for existing orders,** business by business, by a job that opens each order's sealed phone inside its own business and writes the hash.
- **Read only through one narrow function** that takes the person's verified phone, finds the index rows, and returns from each business only that order's reference, state, total, date and business name. No other columns, and only for the person's own verified phone.

Nothing in a business's own data changes. An order placed before the account was made appears only after the person confirms "These are mine" on the first list.

**Saved addresses** are kept on the person's side. One is offered when ordering, and a business sees only the address on its order.

## 14. Privacy and data

| What | Who keeps it | Deleted when |
| --- | --- | --- |
| The account: name, photo, phone, email | The person (personal scope) | The person deletes their account |
| The person's copy of a customer conversation | The person | The person deletes the chat or the account |
| The business's copy of the same conversation (its customer thread) | The business, sealed under the customer's subject as today | The business's retention (a year), a STOP on the business's own WhatsApp line (that line's messages, as today), or an erasure request to that business. Not by Block (§8.3) |
| Photos, voice notes and files in an app conversation | Each side its own copy (§8.5) | With the copy they belong to |
| The phone index (§13) | Taskiem, as a keyed hash only | When the order's customer is erased, or the order is deleted |
| Orders, bookings, invoices | The business, as today; the person sees theirs (§13) | As today |
| Saved addresses, Discover searches | The person; searches are not kept | The person deletes them or the account |

- **Deleting an account** removes the person's side at once and ends every session. It tells each business the person chatted with only that "this customer left Taskiem". The businesses keep their own records under their retention, and an erasure request goes to each business through today's process (NDPA).
- **A person's phone number** is shown to a business they chat with, since orders and deliveries need it. The person is told so when they first chat with a business. It is never shown to other customers or in Discover.
- **No business sees** a person's other businesses, their chats with others, or their orders elsewhere.
- **Taskiem reads business conversations** for what it does today: order drafts and translations. The person is told this in the privacy notice and in the first conversation's banner ("Messages with businesses can be read by Taskiem to help with your orders"). Whether chats between people (§16) are end-to-end encrypted is that spec's decision.

## 15. Safety and limits

- **Businesses writing first.** A business may message anyone who has chatted or ordered with it, for any reason (settled in review). Two kinds of message are told apart:
  - **About something of theirs**: an order, a booking, an invoice, a delivery. No limit beyond the ordinary sending limits.
  - **Anything else** (an offer, news, "we're open today"): only to customers who **opted in**, at most **2 per customer per week**. Each is marked **Offer** in the conversation, with **Stop offers from {business}** under it. A business may also send one such message to many opted-in customers at once (a broadcast), counted against each customer's 2.
  - **Opting in** (settled in review, §24): at a customer's first chat or first order with a business, the app asks once, "Get offers from Mama's Kitchen?" with **Yes** and **No**. Only **Yes** receives offers. The answer, and when it was given, is recorded with the business. The customer can change it at any time in the conversation's menu.
  - **Stop offers** keeps order messages and stops the rest. **Block** stops everything.
  - A business whose offers are muted or blocked far above the norm has its offers paused for review.
- **A person writing to businesses.** New conversations limited per hour and per day. Messages per conversation per minute limited. New accounts get lower limits for their first week.
- **Reporting.** **Report** on every conversation and in Discover, sending the last 20 messages to Taskiem's moderators with the person's consent. Apple and Google require this of apps where people message each other.
- **Fraud.** A business's verification status (phone verified, payments connected, trading for N months) shows on its profile and in the conversation's header. Paying is only ever through Taskiem's pay links, and the app warns on a message that asks for a transfer to a personal account.
- **Age.** **Eighteen and over** in the terms (settled in review), in line with Nigeria's data protection rules, under which a person under 18 needs a parent's consent, and because the app takes payments. Making an account asks the person to confirm they are 18 or over. There is no parental-consent flow. Legal review confirms this before stage 1.
- **Offers and consent.** Offers need the customer's opt-in (above), which is what Nigeria's data protection rules expect for marketing. The legal review checks the words of the question.
- **Digital goods in the app.** No special handling (founder, §24): every item a business sells, digital or not, is sold and paid for in the app as in the browser, through Taskiem's pay links. Apple and Google require their own payment systems for digital goods (courses, e-books, subscriptions, downloads) sold in an app. The founder accepted the risk that a store review rejects an update or removes the app over this; it is listed in §22 and revisited if a review raises it.
- **Phone codes cost money.** SMS costs per code, and WhatsApp's authentication messages cost per code too. Codes go by WhatsApp first, where it is cheaper; SMS limits are as in §5.1. The operator dashboard shows code sends, costs and failures by country. **Businesses pay for all customer messaging** (settled in review, §24):
  - a sign-in code for a person who came to Taskiem through a business's chat link, storefront or message counts against that business's plan;
  - the browser's SMS fallback for a business's customer counts against that business;
  - offers and broadcasts count against the business that sends them.

  Personal accounts are free. A code for a person who came with no business (from Discover, the store or the website) is Taskiem's cost. Each business sees these in its plan's messages, as it sees its SMS today.

## 16. People chatting with people (a later stage, its own spec)

This spec only lists what that stage must decide, so stages 1 to 4 do not close any doors:

- **Contacts.** Finding people from the phone's address book (matched by keyed hash, opt-in, never uploaded in clear), by phone number, or by a person's link.
- **One-to-one chats,** then groups, then calls.
- **Encryption.** End-to-end encrypted chats between people are what WhatsApp and Telegram's secret chats offer and what people will expect. Business conversations cannot be end-to-end encrypted, because Taskiem reads them to draft orders. That spec decides whether chats between people are, and the app must show the difference plainly.
- **Moderation, spam, blocking and reporting** at the scale of strangers, and lawful requests.
- **The server.** Presence, delivery and media at consumer scale, beyond what business chats need today.
- **Positioning.** Competing with WhatsApp for chats between people is hard. The reason to chat on Taskiem is the businesses there; that spec must say what brings people's own chats.

Stages 1 to 4 build what that stage will need: personal accounts, personal scope, two-copy conversations, push and blocking.

## 17. The doors (from the earlier draft)

Every door now leads to the same place:

| Door | Today | After |
| --- | --- | --- |
| Web `/signup`, `/login` | Business form / email sign-in | The phone way in (§5.1), with **Sign in with email** and **Start something** (§18) |
| The app's first screen | Sign in, or sign up a business | The phone way in |
| Taskiem's WhatsApp number, a stranger | Sign-up offer (three buttons) | The same, with **I'm a customer** in place of **Not now** (§12) |
| Taskiem's Telegram bot, a stranger | Sign-up offer | The same, plus **I'm a customer** |
| Signed in, in no business | "You do not belong to an organisation" | **You** (§7) |

Nobody makes a business by mistake any more: a customer makes a personal account, and **Start something** is a deliberate step: a business, or workflows (§18).

## 18. Starting something: a business, or workflows

A person who wants Taskiem to automate work (send reminders, collect form answers, chase payments, move data between their apps) is not always a business with a shop. Today every way in treats them as one:
- **Web sign-up** offers "Just workflows for now", but as the last of five answers to "What does your business do?", after a required business name.
- **Sign-up in chat and in the app** never asks. It makes a business with every area on (shop, orders, stock, money, linked businesses), except a delivery business (`api/chat_signup.go`, `areas.DefaultOn`). A person who wanted workflows must find **What your business uses** in Settings and turn the rest off.

### 18.1 The choice

**Start a business** becomes **Start something**, on **You**, in `@Taskiem` on You, and at every door that makes one:

> **What do you want to start?**
>
> **A business**: sell, take orders and bookings, send invoices, with your staff.
> **Workflows**: automate work for yourself or a team: forms, reminders, approvals, your other apps.

- **A business** leads to today's flow: a name, what it does (the areas it starts with, as `kinds` sets them today), and the rest.
- **Workflows** makes a **workflows workspace** (§18.2) at once. It asks nothing more than an optional name.

### 18.2 A workflows workspace

It is a tenant like any business, so workflows, runs, forms, tables, connections, approvals, members, billing and audit all work unchanged. What differs:

| | A workflows workspace |
| --- | --- |
| Name | Optional. "{Person}'s workflows" until renamed |
| Areas | Every area off (`kinds: []`, as "Just workflows for now" today) |
| What it opens on | Workflows: the templates and the AI builder ("What do you want to automate?"), not a shop checklist |
| Its chat | `@Taskiem` for building and running workflows. No shop, stock or money tools, since their areas are off (business areas phase 3 already hides their words) |
| Discover and Explore | Never listed, and no nudges to open a shop |
| Customers' chat links | None until it turns a business area on |
| Members | Invited as in a business, to share workflows with a team |
| Plans | The same plans as a business |
| In the switcher | Shown under its name with a workflows mark, beside **You** and any businesses |

**Becoming a business** needs no move. Turning an area on in **What your business uses** (renamed **What you use** for a workspace) makes it one. Its workflows, connections and members stay, and the shop, Discover and the rest open as for any business. Turning every area off again makes it a workflows workspace again. Nothing is deleted either way.

The difference is a single flag on the tenant, `kind`: `business` or `workflows`. It is set at the start and changed by turning areas on or off. It decides only the name's requirement, what the workspace opens on, and Discover and Explore. Areas still decide what is reachable, through the gate business areas already has.

### 18.3 Every door asks the same

| Door | Today | After |
| --- | --- | --- |
| Web | "What does your business do?" with "Just workflows for now" last, a business name required first | **Start something** (§18.1) after the phone way in; the business name only for **A business** |
| The app | The business form, or the sign-up conversation | **Start something** |
| WhatsApp and Telegram sign-up | The questions, then a business with every area on | The first question becomes "Is this for a business, or for workflows?" **Workflows** skips the questions about selling and payments and makes a workflows workspace. **A business** goes on as today and asks what it sells, which sets its areas, so it no longer starts with every area on |
| Slack ("Add to Slack") | A business for the Slack workspace, every area on | A workflows workspace, every area off: a Slack workspace is usually a team wanting workflows and approvals. Its owner turns areas on if it sells |
| Field agents | A business, every area on | Unchanged: agents sign up traders, who are businesses |
| Partners' sub-tenants, the API without `kinds` | Every area on | Unchanged ([business areas](business-areas.md) §7, phase 4) |

### 18.4 Not done here

- **Workflows on a person's own side** (**You**), with no workspace. Connections, secrets, approvals, billing and audit all belong to a tenant; a personal copy would rebuild most of the platform. A workflows workspace gives the same result with what exists.
- **Moving a workflow between workspaces or businesses.** Today's export and import of a workflow serves, as it does between businesses.

## 19. Data and server changes

| Change | Notes |
| --- | --- |
| `users.email` nullable | The unique index becomes partial (where not null). Sign-in by email looks up only non-null |
| `user_phones` (new) | Keyed hash, sealed number, verified at. Unique on the hash. Copied from `whatsapp_bindings` where unambiguous |
| Sessions: personal scope | A session carries an optional tenant. Personal routes check the person, not a membership. Business routes are unchanged |
| A person's row-level scope | Personal tables use `taskiem_person_scope()` (the session's person), as business tables use `taskiem_tenant_scope()` |
| `person_conversations`, `person_messages` (new) | The person's copies of customer conversations (later, of chats with people). Paired with the business's `customer_messages` by a shared message id, as `link_messages` pairs copies |
| `customer_conversations` | Gains `line` (`whatsapp`, `app`) and `person_id` (set when the customer is a person with an account, §11). The unique key covers the line |
| `tenants.kind` | `business` or `workflows` (§18.2). Existing tenants are `business`. Set at the start; changed when areas are turned on or all off |
| Chat codes | `customer_chat_codes`: per business, hashed, replaceable; served at `/m/{code}`, added to the app's links (`appPaths`) |
| Discover | A business's public listing (category, area, hours, photo), its setting, and a search index across businesses that opted in, served by a narrow function |
| Orders by phone | `customer_phone_index` (platform-keyed hash, business, order), written with each order and booking, filled once for existing ones; one function returning a verified phone's own orders across businesses (§13) |
| The customer door | `taskiem_customer_enter` and its narrow reads and writes, audited in the business's chain (§8.4); storefront orders gain the kind `app` and a `person_id` |
| Media in app conversations | Stored per side under each side's key, with the limits and retention of §8.5 |
| Second proofs and devices | A device seen per account; the business rules checked on switching (§5.4); the 180-day "new number?" question and set-aside accounts |
| Changing a phone | §5.5: both codes or a second proof; the phone index re-made; threads keep the old number |
| Phone codes | Generalised from WhatsApp-only codes (`whatsapp_otps`) to WhatsApp or SMS, with the limits in §5.1 |
| Push | Notifications carry the side (You or a business) and open that conversation |

## 20. Stages

Each stage ships on its own, Android, iPhone and web together:

1. **Personal accounts** (§5, §6, §7 without Orders and Discover, §17, §18):
   - the phone way in;
   - personal sessions;
   - **You** with `@Taskiem` help and **Start something** (§18), with the same question at every door;
   - the switcher, checking each business's sign-in rules (§5.4);
   - second proofs and reissued numbers (§5.4), changing a phone (§5.5);
   - existing accounts adding a phone, Slack and Telegram owners included.
2. **Customers chatting with businesses** (§8, §9, §12, §14, §15):
   - the customer door (§8.4), which everything below reads and writes through;
   - the phone index (§13), so "Chat about this order" finds storefront orders;
   - chat links and the storefront button;
   - the conversation, its tools and cards on both sides;
   - the browser chat;
   - media on both sides (§8.5);
   - hours, the closed reply and unanswered chats (§8.6);
   - mute and block, reporting;
   - the shared number's new words.
3. **Discover and Orders** (§10, §13).
4. **One customer across the app and WhatsApp** (§11).
5. **People chatting with people:** its own spec and decision (§16).

**Before stage 1:**
- ~~decision 0068 accepted~~ (done, 16 November 2026);
- the privacy notice and terms rewritten for personal accounts (legal review);
- the store listings changed;
- the SMS provider's cost per code agreed.

**Before stage 2:**
- the `taskiem_update` template's new wording approved by Meta (§12);
- the legal review of the offers question's words (§15).

## 21. Tests

- **Accounts:**
  - the phone way in by WhatsApp code and by SMS;
  - lockouts and limits;
  - an existing chat-born owner signing in by phone; an email user adding a phone;
  - a number bound to two accounts;
  - a personal session reaching no business route, and a business session reaching both;
  - a business requiring single sign-on, a passkey-only person and an administrator under required passkeys: each refused on switching until its own rule is met;
  - a reissued number: an account in a business without a second proof reaches only the personal side; the 180-day question both ways; the set-aside account claimed with a second proof within 90 days, and deleted after;
  - changing a phone: orders under the old number still listed, new ones under the new, WhatsApp threads not re-matched until the new number writes.
- **Customer conversations:**
  - a chat link opening the app and the browser;
  - a message from a person landing in the business's Customers list marked **in the app**;
  - a reply landing in the person's copy;
  - order, booking and pay cards on both sides;
  - Mute and Block both ways;
  - a business writing first to a past customer: order messages unlimited; offers marked **Offer**, only to opted-in customers, refused past 2 per customer per week, broadcasts counted per customer; **Stop offers** stopping offers but not orders;
  - deleting an account leaving the business's copy;
  - Block keeping both copies; STOP on an own WhatsApp line deleting only that line's messages;
  - media both ways, each side's copy erased alone;
  - the closed reply once a day; an unanswered message reaching **Needs you** after 30 minutes and the owner after 2 hours.
- **The customer door:**
  - a person reaches only the catalogue, free times, their own orders, placing and paying, and their conversation, in one business at a time;
  - refused for a business that blocked them or turned the area off;
  - each entry audited in the business's chain;
  - an `app` order going through the business's order workflow.
- **The phone index:**
  - written with each order and booking, removed on erasure;
  - the backfill filling it business by business;
  - the function returning only the verified phone's own orders and only the listed columns.
- **Starting something (§18):**
  - **Workflows** from the web, the app, WhatsApp, Telegram and Slack makes a workspace with every area off, opening on the workflows, never listed in Discover or Explore;
  - **A business** from chat sets its areas from what it sells, not every area;
  - turning an area on makes a workspace a business with its workflows, connections and members intact; turning all off makes it a workspace again.
- **Browser:**
  - signed in by code with no install;
  - push where allowed, SMS where not;
  - 390 and 360 px; axe in both themes.
- **Offers:** the opt-in asked once at the first chat or order and recorded; offers refused to a customer who said **No** or chose **Stop offers**; broadcasts reaching only opted-in customers.
- **Costs:** a code, an SMS fallback and an offer each counted against the right business; a code for a person who came with no business counted as Taskiem's.
- **Isolation:**
  - no personal route reaches another person's data;
  - no business sees a person's other businesses;
  - the orders-by-phone function returns only verified phones' orders.
- **App:** parity pictures for every new screen, light and dark, against the web ([mobile-app.md §9](mobile-app.md)).
- **Load:** a business with 10,000 customers in the app; one person with 50 businesses.

## 22. Risks

| Risk | What helps |
| --- | --- |
| Customers won't install another app | The browser chat (§9) needs nothing installed; the app is the better way for people who come back |
| Businesses keep sending customers to their personal WhatsApp | The chat link on the storefront, receipts and updates; orders and payments that work better in Taskiem's chat than on a personal WhatsApp |
| People expect WhatsApp's privacy | Business conversations are plainly marked as readable by Taskiem (§14); chats between people get their own decision (§16) |
| Fraud and spam once anyone can sign up | Limits, reporting, verification marks, Taskiem-only payments (§15) |
| Businesses' offers make customers mute the app | Offers only to customers who opted in, marked as such, 2 per customer per week, **Stop offers** in one tap, offers paused for businesses stopped far above the norm (§15) |
| Taskiem becomes a controller of consumers' personal data | Legal review before stage 1; the person's side kept apart from businesses' data (§14) |
| The app becomes two products in one | One look; the switcher; a customer never sees business tools (§6) |
| Phone codes by SMS cost money and are abused | WhatsApp first; limits per number, device and IP; the dashboard (§15) |
| A reissued number reaches someone else's account | Second proofs for accounts in a business; the "new number?" question for old personal accounts (§5.4) |
| The customer door becomes a way around a business's walls | One audited entry point with a fixed list of what it allows, one business at a time (§8.4) |
| A store rejects an update or removes the app over in-app payment for digital goods | Accepted by the founder (§15, §24); revisited if a review raises it |

## 23. Settled in review (2026-11-16)

| # | Question | Answer |
| --- | --- | --- |
| 1 | The first screen's line | "Chat with the businesses you buy from, and run your own." (§5.1) |
| 2 | Email sign-in's place | A small **Sign in with email** link under the phone box (§5.1) |
| 3 | `@Taskiem` on You | A help conversation: a person's own orders and Taskiem's help, and **Start something**. It never acts in a business (§7) |
| 4 | Businesses writing first | To anyone who has chatted or ordered with them, for any reason. Order messages are unlimited; offers go only to customers who opted in (§24), are marked, limited to 2 per customer per week, and can be stopped (§15). The limit of 2 is this spec's default, open to change |
| 5 | The browser's fallback by SMS | Yes: an SMS when the business answers and the browser cannot be notified, at most one per conversation per hour (§9) |
| 6 | One customer on WhatsApp and the app | One thread with both lines; each message marked; replies go where the customer last wrote, and staff can switch (§11) |
| 7 | Discover's default | On for businesses with a published storefront, which can turn it off; off for the rest (§10) |
| 8 | Minimum age | 18, confirmed when making an account; no parental-consent flow; legal review to confirm (§15) |

## 24. Settled in the review of gaps (2026-11-16)

| # | Question | Answer |
| --- | --- | --- |
| 1 | Offers and consent | **Opt in, asked once** at a customer's first chat or first order with a business: "Get offers from {business}?" Only **Yes** receives offers; order messages always come (§15) |
| 2 | Who pays | **Businesses pay for all customer messaging**: codes for people who came through a business, the browser's SMS fallback for its customers, and its offers, all counted against its plan. Personal accounts are free; codes for people who came with no business are Taskiem's (§15) |
| 3 | Digital goods | **No special handling**: sold and paid in the app like everything else. The store risk is accepted (§15, §22) |
| 4 | The closed reply and unanswered times | **On by default**: the closed reply once a day, **Needs you** after 30 minutes, the owner told after 2 hours. Each business can change or turn off each (§8.6) |
| 5 | Set-aside accounts | **90 days** to claim with a second proof, then deleted (§5.4) |

## As built: stage 1, stream B

The server's half of **Start something** (§18). Phone sign-in and personal sessions are stream A, built apart.

**Data.** Migration `00449_tenant_kind.sql`: `tenants.kind` (`business` or `workflows`, default `business`, so every existing tenant is a business). `taskiem_link_findable` now also needs a business, so Explore, its sections and scores, and the links search never list a workflows workspace, and a business that becomes one drops out. `taskiem_dispatch` reads the column (listed in `engine/db/dispatch_columns.txt`).

**One way to make a tenant.** `insertTenantTx` (`api/start.go`) makes the tenant of its kind, its owner's membership, the Default workspace and the `dev` and `prod` environments, for an owner who already exists; the web form (`createTenant`), chat sign-up and field agents (`makeBusiness`) all use it. A workflows workspace given no name is called "{owner's name}'s workflows", or "My workflows". `startAreasTx` writes a new tenant's areas as its own rows (Schools through its switch, a delivery business's set-up).

**Endpoints.**
- `POST /v1/me/start` `{kind: "business"|"workflows", name?, kinds?: ["shop"|"service"|"delivery"|"school"]}` → `201 {tenant_id, kind, name, user_id, tenants, token?}`. Workflows: name optional (2 to 80 characters when given), `kinds` refused, every area off. Business: name required (2 to 80, no links or emails); `kinds` as `/v1/signup` takes them, left out every area but Schools on, an empty list refused (400: start workflows instead). The signed-in person becomes its owner (no new account); `tenant_onboarding` source `app` for a bearer session, `signup` for a cookie one; audited `tenant.create`; the trial starts (`Billing.Ensure`). In the same transaction this session ends and its twin in the new tenant is made, as `/v1/me/switch` does (same signing-in moment, method, phone and expiry; audited `auth.switch`), answered as a token or a cookie the way the session came. Five a day per person (429), counted through `tenant_onboarding.channel_hash` and `taskiem_chat_signup_counts`. 403 for an API key, a field agent's session or an embed user; 409 for a single sign-on session (as switching).
- `GET /v1/me` gains `tenant_kind`; `GET /v1/me/businesses` items gain `kind`. In `mobile/api.txt` (`startSomething`) and regenerated.

**The kind follows the areas.** `PUT /v1/settings/areas` and `PUT /v1/settings/schools`: turning any area on in a workflows workspace makes it a business; ending with every area off makes a business workflows. Audited `tenant.kind` `{from, to}` after `settings.areas`.

**Every door.**
- Chat sign-up (WhatsApp, Telegram, the app's sign-up conversation): after the terms (first, for a person with an account), "Is this for a business, or for workflows?" with **A business** and **Workflows** (`su:kind:business`, `su:kind:workflows`; 1, 2, "business" and "workflows" typed also answer). Workflows asks only the person's name when unknown (Telegram still asks for their contact last), makes the workspace named after them with every area off, offers two starters (daily reminder, leave requests) and never asks about being found. A business goes on as before and its areas follow what it sells: food, provisions, fashion, phones → `shop`; salon, services → `service`; deliveries → `delivery`; school → `school` (Schools on, with Money); something else keeps every area but Schools, as before.
- Slack: a workflows workspace. It still offers the Slack workspace's name, then makes it with every area off; nothing about selling or payments is asked.
- Web `POST /v1/signup`: unchanged, except `kinds: []` makes `kind = workflows`.
- Field agents, partners' sub-tenants and the API without `kinds`: unchanged (businesses).
- New words in `engine/lang/messages` (`signup.ask_kind`, `signup.button_business`, `signup.button_workflows`, `signup.starters_workflows`, `signup.tpl.leave-request`), English copied unreviewed into the other languages.

**Deviations.**
- There is no `web` onboarding source: a web start is recorded as `signup` (so it has the first-run measure and the email check `signup` has), a phone's as `app`.
- Slack asks no "business or workflows?" question: it always starts workflows, as §18.3 says.
- `kind: "business"` with `kinds: []` is refused rather than made as a business with nothing on, which would contradict the kind following the areas.

**Left over.**
- Personal sessions (stream A): `startSomething` uses only the person; the twin-session statement is the same as switching's and scopes the old session's tenant, so when sessions may have no tenant it needs the same change as `/v1/me/switch` (and an invitee session today gets 401).
- A cookie start by someone with no confirmed email (stream A's phone-only people) would meet the `signup` source's "confirm your email" rule; choose the source again when personal sessions land.
- The web and app screens (Start something, the switcher's workflows mark, what a workspace opens on, "What you use") are the web and app streams'.
- Taskiem Ops does not show the kind yet.
