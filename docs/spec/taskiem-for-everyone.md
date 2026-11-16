# Taskiem for everyone: one chat app for people and the businesses they deal with

Status: **draft for review** (2026-11-16). Nothing here is built. It replaces the earlier draft "Customers at the door" (both its parts).

It needs a new decision, **0068**, before any of it is built (§4). Builds on: [the phone app](mobile-app.md) ([0066](../decisions/0066-a-flutter-phone-app.md)), [chat push](chat-push.md), [chat as the way in](chat-way-in.md), [chat sign-in](chat-sign-in.md), [customers' conversations](chat-five-more-flows.md#as-built-phase-1-customers-conversations), [business links](business-links.md), [storefronts](storefronts.md).

## 1. Summary

The Taskiem app becomes **a chat app anyone can use**, like WhatsApp and Telegram. A person signs up with their phone number and chats:
- with **businesses**: to ask, order, book and pay;
- in time, with **other people** (§16).

Running a business is something a person *does in* Taskiem, not what they must be to use it. An owner or a member of staff is a person with a business attached. A customer is a person with none.

For businesses this changes how customers reach them:
- **In the Taskiem app, or in the phone's browser** with nothing to install (§9). Each business is its own conversation, under its own name and photo, with its own notifications. Orders, bookings and payments happen in the conversation, as cards. There is no 24-hour window and no template. This works for every business, whether or not it has a WhatsApp number of its own.
- **On WhatsApp,** for a business that has connected its own number, as today (§11). The same customer may use both. The business sees one customer.
- **Taskiem's shared WhatsApp number and Telegram bot** stay as they are for outsiders: updates, one way. They now point customers to the app or the browser to chat (§12).

The work comes in stages (§19):
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
| Chat link | A business's link that opens a customer conversation with it: `https://<host>/c/{code}`. In the app if installed, else in the browser |
| Discoverable | A business that chose to be found by the public in the app (§10). Not the same as being found by other businesses in Explore |
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

## 4. The decision this needs (0068)

1. **Taskiem is a chat app for everyone.** Anyone may make a personal account with a phone number and use the app and the web chat with no business. This replaces three earlier rules:
   - [mobile-app.md](mobile-app.md) §1–2: "for owners and their staff", "does not replace WhatsApp";
   - [storefronts §3](storefronts.md#3-goals-and-non-goals): customer accounts as a non-goal;
   - [0039](../decisions/0039-reaching-people-outside.md): customers always outsiders.
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
   - A number that already belongs to an account signs in to it: chat-born owners and staff, and any account with that number bound (§5.3).
   - A new number makes a **personal account**, with a name (required) and a photo (optional).
   - Agreeing to the terms and privacy notice is recorded, as for every way in today.
3. **Then the app's home** (§7), on **You**. A **Start a business** button is always there. It leads to today's sign-up (the form, or the conversation with Taskiem), and the business it makes is attached to this person.

**Email and password stay** for people who have them: **Sign in with email** under the phone box, passkeys, and single sign-on for businesses that require it. An account can add an email later in Settings, as chat-born owners do today.

### 5.2 What an account is, in the data

- **`users.email` becomes optional.** Accounts made by phone have none. The placeholder (`ownerPlaceholderEmail`) is retired for new accounts and stays on old ones until they add a real email.
- **`user_phones`** (new): one verified phone per account, unique across accounts, kept as a keyed hash with the number sealed under the person's subject, plus when it was verified.
  - Existing `whatsapp_bindings` numbers are copied in. Where an account's bound WhatsApp number is free, it becomes its phone.
  - A number bound to two accounts is not copied; both are asked to verify once (§18).
- **A session may have no business.** The session's scope becomes either **personal** (no tenant) or **a business**, as today. A personal session reaches only the person's own routes (§8, §13); a business session also reaches the person's own routes. The invitee-only session (`invitations_only`) becomes an ordinary personal session.
- **The 90-day dormancy rule for a phone stays** ([chat as the way in §12](chat-way-in.md)). A phone silent for 90 days re-proves itself before it signs in, since numbers are reissued.

### 5.3 People who already have accounts

| Today | After |
| --- | --- |
| Owner or staff with email and password | Unchanged sign-in. Once signed in, asked once to add their phone (skippable for 30 days). They then have **You** as well as their business |
| Chat-born owner (placeholder email, WhatsApp number) | Signs in with the phone code. Their WhatsApp number is their phone |
| A person with only invitations | A personal account; the invitations wait in **You** |
| A business's customer (a phone on orders, a thread on its own number) | Nothing changes until they make an account. When they do, with the same phone, §11 and §13 connect what is theirs |

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

**`@Taskiem` on You** is a help conversation: "How do I pay?", "Where is my order?", answered from the person's own orders and Taskiem's help. It never acts in a business. It is the place for **Start a business**.

## 8. Customers chatting with a business

### 8.1 Starting

A person starts a conversation with a business from:
- **its chat link** (§8.2), shared on its storefront, Instagram, a flyer or a receipt;
- **Discover** (§10), or the business's profile there;
- **an order or booking** they made on its storefront with the same phone: "Chat with Mama's Kitchen about this order";
- **a message from the business** in the app: a reply, an order card, a reminder. A business can write first only to a person who has an order, booking or invoice with it, and only about that (§15).

### 8.2 The chat link

- `https://<host>/c/{code}`. The code is one per business, ten characters with no look-alikes. The owner can replace it (**Make a new link**); the old one says "This link no longer works" for 30 days.
- **Where the business finds it:** in its chat under **Tools > Customers > Your chat link**, and in Settings. It comes with:
  - a QR code to print;
  - words to paste: "Chat with Mama's Kitchen on Taskiem: {link}".
- **The storefront** gains **Chat with us**, beside or instead of **Chat on WhatsApp**, as the owner chooses.
- **What it opens:**
  - **the app,** if installed (it joins the app's link list, [mobile-app.md §5.6](mobile-app.md));
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

**On the business's side:** the same customer thread as today, in Customers, marked **in the app**. Drafted orders, replies, STOP and the rest work, with three differences from WhatsApp:
- **No 24-hour window and no templates.** A reply is always a message.
- **Rich replies.** Staff can send an order card, a pay link, a booking offer or a product from the catalogue (`#`), not only words.
- **Read ticks** are the person's own, shown only if they leave them on.

**STOP becomes Mute and Block**, the person's own choice in the conversation's menu:
- **Mute** silences notifications.
- **Block** stops the business writing to them. The business sees "This customer has stopped messages", as STOP shows today.

The business can block a customer too.

## 9. Chatting in the browser

**For a customer who won't install an app,** the chat link opens the conversation in the phone's browser. It is the web chat, already built, on the personal side, at `https://<host>/c/{code}` and then `/chat?with=…`.

1. **The phone code** (§5.1), in the page. No password, no email.
2. **The conversation,** as in the app (§8.3), with the same cards, tools and payments.
3. **Notifications:**
   - the browser's push, after one tap on **Tell me when they answer**, where the browser allows it ([chat push](chat-push.md));
   - where it doesn't (an iPhone with the page not added to the home screen), the answer goes by SMS: "Mama's Kitchen answered you on Taskiem: {link}", at most one per conversation per hour.
4. **The app, offered,** as a quiet line under the conversation ("Get the Taskiem app for your chats with every business"), never in the way.

**Staying signed in:** the browser session lasts 90 days on that device. **Sign out** is in the page's menu. A shared phone is warned at sign-in ("Is this your phone? If not, don't stay signed in").

## 10. Discover: finding businesses

- **The setting.** A business chooses to be **found by the public in the Taskiem app** in Settings (default off). Turning it on asks for:
  - a category;
  - an area;
  - a photo;
  - hours.

  Most of these come from its storefront.
- **What a person sees.** Search by name, what they want (rice, hair, cakes) and area. Each business shows its name, photo, category, area, open now, and a **Chat** button. Results near the person come first, when they share their location; that location is never kept.
- **A business's profile** in Discover: its photos, hours, address, **Their shop**, **Book**, **Chat**.
- **Kept apart from Explore.** Explore (businesses finding businesses) keeps its own setting and its own list. A business may be in either, both or neither.
- **Moderation.** As storefronts are moderated today ([moderation](../moderation.md)): a report button on every business, and listings reviewed when reported.

## 11. Businesses with their own WhatsApp number

Their customers on WhatsApp chat there as today. Some of the same customers will also use the app.

- **One customer, one thread.** When a person in the app has the same verified phone as a customer thread on the business's own number, the business sees **one thread with both lines**. Each message is marked with where it came from. The person's app conversation shows the WhatsApp messages too, marked "on WhatsApp", since they were theirs.
- **Where a reply goes.** By default, on the line the customer last wrote on. Staff can choose the other line. Replies on WhatsApp keep its 24-hour window and templates.
- **The business's chat link** for such a business opens a choice: **Chat on WhatsApp** (its own number) or **Chat on Taskiem**.

Matching by phone needs the phone verified on both sides: by the code on the app's side, and by WhatsApp having delivered the message on the other. A phone typed on a form matches nothing.

## 12. The shared WhatsApp number and Telegram bot

They stay one-way for outsiders (0039). What they say changes:
- **Updates a business sends** to an outsider on the shared number can end with "Reply in the Taskiem app: {chat link}", when the business turns that on. It is on by default for businesses with no own number.
- **A stranger** writing to the shared number gets the sign-up offer with **Start my business**, **I use Taskiem** and **I'm a customer**. WhatsApp allows three buttons, so **Not now** becomes typing "no" or not answering. **I'm a customer** answers: "To chat with a business, open its Taskiem link, or find it in the Taskiem app: {app link}."
- **An outsider who replies to an update** gets, in place of today's fixed answer, "To chat with {business}, open {chat link}". This goes once per business per day, within the existing limits.

## 13. A customer's orders and bookings

**Orders** on **You**: every order and booking the person has with any business, newest first. Each shows the business, what, total, state, **Pay** if due, and **Chat about this**.

**What counts as theirs:**
- orders and bookings made in the app or the browser chat while signed in;
- storefront orders and bookings made with **the same phone**, once that phone is verified on the account. The phone is checked against each order's customer subject (`storefront_orders.subject_id`).

**Looking up a phone's orders** reaches across businesses. That is new: every query today stays inside one business. It is done only through one narrow function that returns the person's own orders: no other columns, and only for their verified phone. Nothing in a business's own data changes. An order placed before the account was made appears only after the person confirms "These are mine" on the first list.

**Saved addresses** are kept on the person's side. One is offered when ordering, and a business sees only the address on its order.

## 14. Privacy and data

| What | Who keeps it | Deleted when |
| --- | --- | --- |
| The account: name, photo, phone, email | The person (personal scope) | The person deletes their account |
| The person's copy of a customer conversation | The person | The person deletes the chat or the account |
| The business's copy of the same conversation (its customer thread) | The business, sealed under the customer's subject as today | The business's retention (a year), the customer's STOP or Block, or an erasure request to that business |
| Orders, bookings, invoices | The business, as today; the person sees theirs (§13) | As today |
| Saved addresses, Discover searches | The person; searches are not kept | The person deletes them or the account |

- **Deleting an account** removes the person's side at once and ends every session. It tells each business the person chatted with only that "this customer left Taskiem". The businesses keep their own records under their retention, and an erasure request goes to each business through today's process (NDPA).
- **A person's phone number** is shown to a business they chat with, since orders and deliveries need it. The person is told so when they first chat with a business. It is never shown to other customers or in Discover.
- **No business sees** a person's other businesses, their chats with others, or their orders elsewhere.
- **Taskiem reads business conversations** for what it does today: order drafts and translations. The person is told this in the privacy notice and in the first conversation's banner ("Messages with businesses can be read by Taskiem to help with your orders"). Whether chats between people (§16) are end-to-end encrypted is that spec's decision.

## 15. Safety and limits

- **Businesses writing first.** Only to a person with an order, booking or invoice with it, and only about it. At most 3 messages until the person answers. No broadcasts to customers in stages 1 to 4.
- **A person writing to businesses.** New conversations limited per hour and per day. Messages per conversation per minute limited. New accounts get lower limits for their first week.
- **Reporting.** **Report** on every conversation and in Discover, sending the last 20 messages to Taskiem's moderators with the person's consent. Apple and Google require this of apps where people message each other.
- **Fraud.** A business's verification status (phone verified, payments connected, trading for N months) shows on its profile and in the conversation's header. Paying is only ever through Taskiem's pay links, and the app warns on a message that asks for a transfer to a personal account.
- **Age.** Sixteen and over in the terms. No age check in stages 1 to 4.
- **Phone codes cost money** (SMS). They go by WhatsApp first; SMS limits are as in §5.1. The operator dashboard shows code sends and failures by country.

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
| Web `/signup`, `/login` | Business form / email sign-in | The phone way in (§5.1), with **Sign in with email** and **Start a business** |
| The app's first screen | Sign in, or sign up a business | The phone way in |
| Taskiem's WhatsApp number, a stranger | Sign-up offer (three buttons) | The same, with **I'm a customer** in place of **Not now** (§12) |
| Taskiem's Telegram bot, a stranger | Sign-up offer | The same, plus **I'm a customer** |
| Signed in, in no business | "You do not belong to an organisation" | **You** (§7) |

Nobody makes a business by mistake any more: a customer makes a personal account, and **Start a business** is a deliberate step.

## 18. Data and server changes

| Change | Notes |
| --- | --- |
| `users.email` nullable | The unique index becomes partial (where not null). Sign-in by email looks up only non-null |
| `user_phones` (new) | Keyed hash, sealed number, verified at. Unique on the hash. Copied from `whatsapp_bindings` where unambiguous |
| Sessions: personal scope | A session carries an optional tenant. Personal routes check the person, not a membership. Business routes are unchanged |
| A person's row-level scope | Personal tables use `taskiem_person_scope()` (the session's person), as business tables use `taskiem_tenant_scope()` |
| `person_conversations`, `person_messages` (new) | The person's copies of customer conversations (later, of chats with people). Paired with the business's `customer_messages` by a shared message id, as `link_messages` pairs copies |
| `customer_conversations` | Gains `line` (`whatsapp`, `app`) and `person_id` (set when the customer is a person with an account, §11). The unique key covers the line |
| Chat codes | `customer_chat_codes`: per business, hashed, replaceable |
| Discover | A business's public listing (category, area, hours, photo), its setting, and a search index across businesses that opted in, served by a narrow function |
| Orders by phone | One function returning a verified phone's orders and bookings across businesses (§13) |
| Phone codes | Generalised from WhatsApp-only codes (`whatsapp_otps`) to WhatsApp or SMS, with the limits in §5.1 |
| Push | Notifications carry the side (You or a business) and open that conversation |

## 19. Stages

Each stage ships on its own, Android, iPhone and web together:

1. **Personal accounts** (§5, §6, §7 without Orders and Discover, §17):
   - the phone way in;
   - personal sessions;
   - **You** with `@Taskiem` help and **Start a business**;
   - the switcher;
   - existing accounts adding a phone.
2. **Customers chatting with businesses** (§8, §9, §12, §14, §15):
   - chat links and the storefront button;
   - the conversation, its tools and cards on both sides;
   - the browser chat;
   - mute and block, reporting;
   - the shared number's new words.
3. **Discover and Orders** (§10, §13).
4. **One customer across the app and WhatsApp** (§11).
5. **People chatting with people:** its own spec and decision (§16).

**Before stage 1:**
- decision 0068 accepted;
- the privacy notice and terms rewritten for personal accounts (legal review);
- the store listings changed;
- the SMS provider's cost per code agreed.

## 20. Tests

- **Accounts:**
  - the phone way in by WhatsApp code and by SMS;
  - lockouts and limits;
  - an existing chat-born owner signing in by phone; an email user adding a phone;
  - a number bound to two accounts;
  - a personal session reaching no business route, and a business session reaching both.
- **Customer conversations:**
  - a chat link opening the app and the browser;
  - a message from a person landing in the business's Customers list marked **in the app**;
  - a reply landing in the person's copy;
  - order, booking and pay cards on both sides;
  - Mute and Block both ways;
  - a business writing first refused without an order, and allowed with one up to 3 times;
  - deleting an account leaving the business's copy.
- **Browser:**
  - signed in by code with no install;
  - push where allowed, SMS where not;
  - 390 and 360 px; axe in both themes.
- **Isolation:**
  - no personal route reaches another person's data;
  - no business sees a person's other businesses;
  - the orders-by-phone function returns only verified phones' orders.
- **App:** parity pictures for every new screen, light and dark, against the web ([mobile-app.md §9](mobile-app.md)).
- **Load:** a business with 10,000 customers in the app; one person with 50 businesses.

## 21. Risks

| Risk | What helps |
| --- | --- |
| Customers won't install another app | The browser chat (§9) needs nothing installed; the app is the better way for people who come back |
| Businesses keep sending customers to their personal WhatsApp | The chat link on the storefront, receipts and updates; orders and payments that work better in Taskiem's chat than on a personal WhatsApp |
| People expect WhatsApp's privacy | Business conversations are plainly marked as readable by Taskiem (§14); chats between people get their own decision (§16) |
| Fraud and spam once anyone can sign up | Writing-first rules, limits, reporting, verification marks, Taskiem-only payments (§15) |
| Taskiem becomes a controller of consumers' personal data | Legal review before stage 1; the person's side kept apart from businesses' data (§14) |
| The app becomes two products in one | One look; the switcher; a customer never sees business tools (§6) |
| Phone codes by SMS cost money and are abused | WhatsApp first; limits per number, device and IP; the dashboard (§15) |

## 22. Questions for review

1. **The first screen's line.** "Chat with the businesses you buy from, and run your own": the right words for the store listing and the first screen?
2. **Email sign-in's place.** A small **Sign in with email** link under the phone box, or two equal buttons?
3. **`@Taskiem` on You.** A help conversation (§7), or nothing until there is more for it to do?
4. **Businesses writing first** (§15). Is "only about an order, booking or invoice, at most 3 until answered" right for stage 2, or do businesses need broadcasts to customers sooner?
5. **The browser's fallback by SMS** (§9). Worth its cost, or only push and the app?
6. **One thread across WhatsApp and the app** (§11). Is merging by phone right, or should a business see the two lines as two threads?
7. **Discover's default** (§10). Off until a business turns it on, or on for businesses with a published storefront?
8. **Age.** Sixteen, as in the terms here, or thirteen, as WhatsApp's minimum in many countries?
