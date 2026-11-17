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
3. **Notifications** (founder, 2026-11-17):
   - **a push first**, to every live device on the person's own side: the app, or the browser after one tap on **Tell me when they answer**, where the browser allows it ([chat push](chat-push.md)). A business's message says "{business}: {first words}" (only "A business wrote to you" where the device's preview is off) and opens the chat on You (`/you/chats/{id}` on the web, the same chat in the app). Order updates the business's workflow sends in the chat are pushed too. These devices belong to the person, not to any business, and stay whatever businesses they are in;
   - **the SMS only when the person has no live push device** (an iPhone with the page not added to the home screen): "Mama's Kitchen answered you on Taskiem: {link}", only for a reply from a person at the business (never Taskiem's automatic words, such as order updates, and never an offer), at most one per conversation per hour, charged to the business.
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
  - a sign-in code asked for through a business's chat link counts against that business's plan **only when a new account made with it (its phone proved) then starts a chat with that business**, once, and at most 50 such codes per business per day; above that Taskiem pays (founder, 2026-11-17). A code is not charged when it is sent: codes that lead nowhere (anyone can ask for codes through any link), and existing accounts signing in on a new device, are Taskiem's cost, and do not count against the business's SMS;
  - the browser's SMS fallback for a business's customer counts against that business;
  - offers and broadcasts count against the business that sends them.

  Personal accounts are free. A code for a person who came with no business (from Discover, the store or the website) is Taskiem's cost. Each business sees what it is charged in its plan's messages, as it sees its SMS today.

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
| 2 | Who pays | **Businesses pay for all customer messaging**: codes for people who came through a business (refined 2026-11-17: only a new account made through its link that then chats with it, at most 50 a day), the browser's SMS fallback for its customers, and its offers, all counted against its plan. Personal accounts are free; codes for people who came with no business are Taskiem's (§15) |
| 3 | Digital goods | **No special handling**: sold and paid in the app like everything else. The store risk is accepted (§15, §22) |
| 4 | The closed reply and unanswered times | **On by default**: the closed reply once a day, **Needs you** after 30 minutes, the owner told after 2 hours. Each business can change or turn off each (§8.6) |
| 5 | Set-aside accounts | **90 days** to claim with a second proof, then deleted (§5.4) |

## As built: stage 1, stream A (accounts)

Built on branch `stage1-accounts`: the server side of §5 and the personal session of §6. The web and app screens, and **Start something** (§18, stream B), are not part of it.

**Migration `00448_personal_accounts.sql`:**
- `user_phones` (one phone per account, unique), filled from live (not dormant) `whatsapp_bindings`, then Telegram's shared numbers where free;
- `phone_codes` (per number and purpose `sign_in`, `change_new`, `change_old`: hashed code, sends per hour, failures, lock, the new-account ticket, proof of a change code);
- `user_devices` (a device per account, when it last signed in, its second proof) and `second_proof_links`;
- `sessions.tenant_id` nullable (a personal session), `auth_method` `phone`; `users.adult_confirmed_at`;
- `taskiem_person_scope()` and a policy letting a person's own transaction (`db.InPersonTx`) read their `users` row;
- the definers that reach all of it (listed in `engine/db/definers.tsv`); `taskiem_auth_session` returns personal sessions, and a business session's tenant must still be active.

**Endpoints** (all in `api/openapi/openapi.yaml`, the app's in `mobile/api.txt`):

| Endpoint | Body | Answer |
| --- | --- | --- |
| `POST /v1/auth/phone/start` | `{phone, via?: whatsapp\|sms}` | 202 `{sent_via, expires_in}`; 429 (`locked`), 503 (`sms_unavailable`), 502 |
| `POST /v1/auth/phone/verify` | `{phone, code, bearer?, device?}` | 200 `{status: signed_in, user_id, tenant_id: null, personal: true, tenants, token?}` or `{status: new, ticket, expires_in}`; 401, 401 `phone_signin_off`, 429 `locked` |
| `POST /v1/auth/phone/create` | `{ticket, name, adult: true, terms: true, bearer?, device?}` | 201 `{status: created, …}` as signed in; 400 without name, adult or terms; 401 ticket used or expired; 409 number taken |
| `POST /v1/me/switch` | `{tenant_id}`, or `{tenant_id: null}` / `{personal: true}` for **You** | 200 as signing in, with `personal`; 401 `sso_required`, `passkey_required`, `second_proof_required` (with `ways`); 409 |
| `GET /v1/me`, `GET /v1/me/businesses` | | `personal`, `businesses: [{id, name, current?, locked, lock}]`, `second_proof: {required, device_proved, ways: {passkey, email, pin}}`, `user.phone` |
| `POST /v1/auth/second-proof/email/start` | | 202 `{sent_to, expires_in}` (a link to the confirmed email) |
| `POST /v1/auth/second-proof/email/verify` | `{token}` | 200 `{device_proved: true}` |
| `POST /v1/auth/second-proof/pin` | `{pin}` | 200 `{device_proved: true}`; 401 with `tries_left`; 429 `locked` |
| `POST /v1/me/phone/start` | `{phone, via?, confirm_old?}` | 202 `{sent_via, old_sent_via?, expires_in, needs_old_code}` |
| `POST /v1/me/phone/verify` | `{code, old_code?, phone?}` | 200 `{phone}`; 403 `old_code_required` |

**How it behaves:**
- A personal session reaches only `GET /v1/me`, `POST /v1/me/terms`, `GET /v1/me/businesses`, `POST /v1/me/switch`, `POST /v1/me/start` (stream B), `/v1/me/phone/*`, `/v1/auth/second-proof/*`, the invitation routes and sign-out; anything else is 403 `{personal_only: true}`. A business session reaches all of these too.
- Password, passkey and chat sign-in of someone in no business now give a personal session (`invitations_only` still says when invitations wait). Invitee sessions are no longer started; those already given are still read. Accepting an invitation from a personal session moves it into the business, under its sign-in rules (no second proof: §5.4 asks for one to be set up on joining instead).
- Codes go by WhatsApp from Taskiem's number, else by SMS through the platform's providers (`sender.CodeSender`, no tenant, no outbox). Five codes per number an hour, 20 starts per address an hour, sign-in limits on checking; five wrong codes lock the number for an hour.
- The phone way in respects "sign in with WhatsApp or Telegram" turned off (`users.chat_signin`).
- **Switching into a business** checks, in order: single sign-on (a session made by single sign-on still switches to no other business; it may go to **You**); passkeys, the same test a password sign-in meets (held with a passkey: only a session made with the passkey enters; held without one: enters to add one, as on signing in); a **second proof** for a session made by a phone code on a device without one.
- **Devices** are the app's install id, or a browser's `taskiem_device` cookie (set when it signs in by phone or passkey). An API or CLI token is no device and is never proved. A passkey sign-in proves its device. A device that has not signed in for 90 days, or any device when the account's phone has not signed in for 90 days, is new again.
- **An account with no second proof to give** (a chat-born owner with no confirmed email, passkey or chat PIN) is let into its business from a new device, and the switch is audited with `second_proof: none_possible`, so no real owner is locked out. `/v1/me` tells the app which proofs exist, so it can ask them to set one up.
- **Changing a phone** needs the old number's code only for a session made by a phone or chat code on an unproved device; an account with no phone yet adds one with the new number's code alone (email users adding a phone, §5.3). WhatsApp and Telegram bindings are not moved. A number found only through a binding signs in its account only while that account has no phone of its own, so the old number signs in no one after a change.

**Deviations from the spec:**
- **`users.email` stays required** (§5.2, §19 said optional). It is assumed non-null in about 40 places; accounts made by phone get a placeholder that receives no mail, `person+<hash of the account id>@phone.taskiem.invalid`, as chat-born owners have one (`ownerPlaceholderEmail`), and `/v1/me` reports `email_placeholder`. Making it optional is left for a later change.
- **Numbers are kept as `whatsapp_bindings` keeps them** (E.164 text), not as a keyed hash with the number sealed under the person's subject (§5.2, §19).
- A number bound to two accounts cannot occur in `whatsapp_bindings` (unique); a Telegram number is copied only where free, and dormant bindings are not copied.
- A new account on a number still bound to another account's WhatsApp is refused (409), even when that account has changed its phone.
- People in no business have no audit chain of their own; phone sign-ins, second proofs and phone changes are audited in each business the person belongs to.

**Left for later:**
- the 180-day "Is this a new number for you?" question and set-aside accounts (§5.4);
- asking existing accounts (email, Slack, Telegram owners) to add a phone (§5.3), and telling a person of a new phone sign-in on their chat or email;
- listing a phone's personal session under **Your phones** (`/v1/me/devices` is a business route and lists business sessions; signing a phone out there, a password reset, a passkey reset and "sign out everywhere" end personal sessions too);
- limits per device on sending codes; counting codes' costs (§24 point 2);
- the web and app screens: the first screen, **You**, the switcher's locks and the second-proof prompts (the web app does not yet handle a personal session with no invitations).

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
- ~~Personal sessions (stream A)~~: done when the streams were merged. The session now moves with `taskiem_auth_session_move` (a definer, so a personal session needs no tenant in scope), inside the tenant's own transaction since the review fixes (see "Review fixes: server").
- A cookie start by someone with no confirmed email (stream A's phone-only people) would meet the `signup` source's "confirm your email" rule; choose the source again when personal sessions land.
- The web and app screens (Start something, the switcher's workflows mark, what a workspace opens on, "What you use") are the web and app streams'.
- Taskiem Ops does not show the kind yet.

### Streams A and B together

Merged on 16 November 2026. **Start something** from a personal session moves the session with stream A's `taskiem_auth_session_move`, as switching does (a definer: a personal session has no tenant to scope); since the review fixes this is inside the tenant's own transaction again, under a lock on the person. A business or workspace started by a person whose sign-in email is a placeholder (made with a phone, or in chat) is recorded with the onboarding source `app`, so the web sign-up's "confirm your email" holds never apply to someone with no email. `GET /v1/me/businesses` items carry `kind` beside stream A's `locked` and `lock`. Tested by `TestStartSomethingFromPersonal`.

## As built: stage 1, web

Built on branch `stage1-web`, on streams A and B merged (`ccr-87497aa3-gf060z` at 9143428f): the web screens of §5, §6, §7 and §18.

**The way in** (`web/src/pages/WayIn.tsx`, §5.1, §17). `/login` and `/signup` show one screen: "Welcome to Taskiem", "Chat with the businesses you buy from, and run your own.", the phone number and **Continue**. A local number (0803 123 4567) is Nigerian; `+`, `00` and a bare country code are read as international (`toE164` in `web/src/lib/phone.ts`). Then the 6-digit code ("We sent a 6-digit code to WhatsApp on +234 …"), a new-code timer of 60 seconds, **Send by SMS instead**, and plain words for a wrong code, a locked number and SMS not being available. A new number gets **Make your account**: the name, "I'm 18 or over" and the terms and privacy notice (linked when the deployment has them), then **Create**. **Sign in with email** under the phone box opens the email, password, passkey, single sign-on and chat sign-in as before, at `/login?with=email` (a single sign-on that comes back with an error opens it too). The signed-out panel beside the card says the new line. The business form of `/signup` is gone from the screens; `POST /v1/signup` stays.

**You** (`web/src/pages/You.tsx`, §7). A personal session, invitations waiting or not, lands on `/you` in a frame of its own: a top bar with the switcher (only for a person in a business) and two tabs, **Chats** and **Settings**.
- **Chats**: "Hello, {first name}", **Start something**, the invitations waiting (Accept moves the session into the business), the chats' place ("Chats with businesses will show here.", stage 2), and **Your businesses** as the switcher's rows. A new device whose businesses wait for a second proof says so, with **Prove it's you**.
- **Settings**: name, phone with **Change number**, email (read only), the theme, and **Sign out**.
- `/you/start` is Start something; `/second-proof` is the email link's page.

**The switcher** (`web/src/pages/Switch.tsx`, §6, §5.4). A button naming what is open; its sheet lists **You**, then each business with a letter (a workflows mark for a workspace), what is open ticked, and a locked one with a lock and why ("Opens with your organisation's sign-in", "Opens with your passkey", "New device: prove it's you to open it"), and **Start something**. It is in the business app's menu (computer) and top bar (phone). Choosing one calls `POST /v1/me/switch` and loads the new side afresh (`/you`, `/workflows` for a workspace, else `/home`). A refusal opens the step it asks for:
- **single sign-on**: the person's email is looked up (`/v1/auth/sso/discover`) and the business's sign-in started; else "Sign out" to sign in there;
- **passkey**: a passkey sign-in straight into that business (`POST /v1/auth/passkey` with `tenant_id`);
- **second proof**: the ways the account has: **Use your passkey**, **Email me a link** ("We sent a link to a•••@… Open it on this device within 15 minutes."), or the chat PIN; after the PIN the switch is tried again, and the link's page (`/second-proof#token=…`, read from the fragment) proves the device and lists the businesses to open.
- The API client no longer treats these refusals, or a wrong code while changing the phone (`/v1/me/phone/*`), as being signed out.

**Change number** (`web/src/pages/ChangeNumber.tsx`, §5.5), on You's Settings and the business app's Account (**Add a phone** where there is none). The new number gets a code; where the server says `needs_old_code`, the person is asked whether they still have the old number: **Yes** sends it a code too (both are asked for), **No** opens the second-proof sheet, after which only the new number's code is needed.

**Start something** (`web/src/pages/StartSomething.tsx`, §18.1), at `/you/start` and in the business app at `/start-something` (from the switcher). "What do you want to start?": **A business** (its name and "What does your business do?": sell, bill for a service, deliver, run a school) or **Workflows** (an optional name, "{name}'s workflows" when left empty). `POST /v1/me/start`, then the new tenant opens: a workspace on Workflows, a business on Get started.

**A workflows workspace** (§18.2): `tenant_kind` workflows calls **What your business uses** **What you use** (page and menu) and opens on Workflows (`/`). Get started already leaves out **Make your page** when the shop is off (`forAreas` in `web/src/lib/onboarding.ts`).

**Tests.** `web/e2e/you.spec.ts`: a new person by phone (the code read from the fake WhatsApp) → You → Start something, workflows then a business; an owner with an email adds a phone under Account, signs in by phone in a new browser, is asked for a second proof, proves it with the emailed link (the SMTP sink), switches into the business and back to You, and changes number (a wrong code first). `accessibility.spec.ts` checks the way in (its three screens), You, the switcher, the second-proof sheet, Settings, Change number and Start something in both themes at 390 and 360 px (with the server's answers stood in for), and adds `/start-something` and the phone screen to the page sweep. New pages are in `plainwords.test.ts`; `lib/phone.test.ts` covers the number reading and the words. Specs that signed in at `/login` open `/login?with=email`; those that made a business through the old form use `POST /v1/signup` or Start something (`areas.spec.ts`); the invitee test expects You.

**Deviations and left over.**
- **Settings on You** shows the email but cannot add or change it, and lists no sessions: those routes are business routes (`/v1/me/email`, `/v1/me/devices`), refused to a personal session (stream A's left-over list).
- No unread counts in the switcher, and nothing for `@Taskiem` on You (stage 2).
- The phone app's parity pictures of the signed-out screens (`mobile/test/parity/web/sign-in*`, `sign-up*`) no longer match the web: they are to be written again (`PARITY_UPDATE=1`) with the app's own screens (stream D); `parity.spec.ts` now opens `/login?with=email` and `/signup`'s way in.
- `you.spec.ts` listens on the fake Graph's and the SMTP sink's ports, as `whatsapp.spec.ts` and `password.spec.ts` do: run on its own when running specs side by side locally.

## As built: stage 1, app

Built on branch `stage1-app` (`mobile/`), against streams A and B as merged; the words are the web's (`web/src/pages/WayIn.tsx`, `Switch.tsx`, `StartSomething.tsx`, `ChangeNumber.tsx`).

- **The first screen** is the phone way in (`lib/screens/welcome.dart`): the number, the code (by WhatsApp, **Send by SMS instead**, **Send a new code**, **Use another number**; problems in the web's plain words), and for a new number **Make your account**: name, "I'm 18 or over", the terms. Every step asks for a device session (`bearer`, `device`) as signing in does. **Sign in with email** opens the email, passkey and chat sign-in, which links back with **Use your phone number instead**. The business form and the WhatsApp-style sign-up conversation are no longer reached from signing out (their screens stay in the code, unused, for removal later).
- **You** (`lib/screens/you.dart`): a personal session opens on it, not on the business chats: an empty chat list ("Chats with businesses will show here."), **Start something**, invitations waiting (**Join** moves into the business when the server answers a token), the switcher when the person is in a business, and Settings: name, phone with **Change number** (§5.5: the old number's code asked for where the server says), email when real, sign out.
- **The switcher** (`lib/screens/switcher.dart`): a sheet with You and each business, its line as the web's (`Open now`, `Workflows`, or the lock), the workflows mark for a workspace. A 401 on switching opens its step: the passkey (signing in with it, then switching), **Prove it's you** with the passkey, **Email me a link** ("We sent a link to …", then **I've opened the link** reads `/v1/me` again), or the chat PIN. From a business: the chats' ⋯ menu (**Switch to You or a business**) and Settings.
- **Start something** (`lib/screens/start_something.dart`): **A business** (name and what it does, `kinds` one of the four) or **Workflows** (name optional); `POST /v1/me/start` answers the new session's token and the app opens on it.
- A 401 carrying `sso_required`, `passkey_required`, `second_proof_required` or `tries_left` no longer signs the phone out (`Transport.stillSignedIn`). What the phone keeps of a business is not opened on a personal session.
- Tests: `test/everyone_test.dart`; `stack_test.dart` expects the new first screen.

**Left over:**
- The email's second-proof link (`/second-proof#token=…`) opens in the app only once the server lists `/second-proof` in its app links (`appPaths`, `api/applinks.go`); the app reads it (`appPageOf`) and confirms the device on **You**. Until then the person opens it in the browser and taps **I've opened the link**.
- Parity pictures: the web half now takes `sign-up` as the way in and `sign-in` at `/login?with=email`. Once its pictures are taken again (`PARITY_UPDATE=1`), the app's `sign-up` becomes `WelcomeScreen` and `sign-in` gets `signup: false, onPhone`; You, the switcher and Start something have no parity screens yet.
- §7's tabs (Orders, Discover), a photo, sessions, notifications and delete account on You; a single sign-on switch in the app (it says to use the web); the code's resend countdown the web shows.

### Web and app together

Merged on 16 November 2026. The parity pictures of the signed-out screens were taken again from the web (`sign-up` is now the way in at `/signup`; `sign-in` and its chat and wrong-password pictures are the email sign-in at `/login?with=email`), and the app's parity screens follow: `sign-up` is `WelcomeScreen`, and `sign-in` is `SignInScreen` with its link back to the phone way in. To match the web, the app's first screen gained the number box's placeholder (`TkField.hint`, the web's 72 % muted), the web's space under its heading (`AuthTitle(bottom: 8)`) and the web's space under **Sign in with email**. `/second-proof` is one of the paths that open the app, so the second-proof email link lands in it.

### Review fixes: server

Built on branch `stage1-fix-server` after a review of stage 1. Migration `00450_stage1_review_fixes.sql`.

1. **Second proofs can no longer be gone round.**
   - A session made by a code to the phone or chat (`auth_method` `phone` or `chat`) on a device with no second proof, of someone who could give one, adds no way of signing in: adding a passkey or authenticator, a password change by passkey or code, an email (`POST /v1/me/email`), a chat link or a PIN all answer 401 `second_proof_required` with `ways` (`refuseUnproved`, called by `proveFactor`). The session keeps its method and device when it switches or starts something, so a business or workspace session made from it is held the same. Tested by `TestStartThenAddFactorNeedsSecondProof`.
   - Chat sign-in (`/v1/auth/chat/verify` and one-time chat links) on such a device, for a person in a business who could give a proof, gives a **personal session** (`personal: true`, `second_proof_required: true`, `ways`), recorded as `auth.chat_signin` with `second_proof: required`; with no proof possible it enters as before, recorded `second_proof: none_possible`. Chat sessions count as phone sessions for second proofs. A WhatsApp or Telegram binding whose account's phone is now another number signs no one in (`taskiem_auth_chat_user`, `taskiem_auth_chat_number_current`). Tested by `TestChatSigninNeedsSecondProof`, `TestChatSigninRefusesAChangedNumber`.
   - Accepting an invitation from such a session joins the business but does not enter it: 200 with the invitation accepted and `{sign_in: "second_proof_required", ways}`; the person proves, then switches. Tested by `TestInvitationAcceptNeedsSecondProof`.
2. **The PIN.** `POST /v1/auth/second-proof/pin` is limited per person and per address, and checks and counts a guess in one transaction holding the PIN's row (`taskiem_wa_pin_lock`), so guesses sent side by side cannot pass the five-try lock, and a right PIN while locked is refused. Tested with 60 parallel guesses.
3. **More ways to prove.** A usable password counts (`POST /v1/auth/second-proof/password {password}`, limited as signing in is; `ways.password`: set, usable in a business of the person's, on a confirmed email where the server sends email), and so does the email link to a real email that is not yet confirmed. The person's own passkey proves a device without changing the session: `POST /v1/auth/second-proof/passkey/options` then `POST /v1/auth/second-proof/passkey {credential}` → `{device_proved: true}`. `user_devices.proved_by` gains `password`. Tested by `TestSecondProofByPasskeyAndPassword`.
4. **Changing a phone** ends every other session of the person and forgets every other device's proof (`taskiem_auth_phone_change_fence`).
5. **`POST /v1/me/phone/start`** no longer says a number is another account's: the code goes anyway, and verifying refuses it (409).
6. **Change codes** another person asked for the same number, still pending or proved, are not replaced (429).
7. **Start something** takes a lock on the person, counts the day's starts, makes the tenant and moves the session in one transaction: a second start at once waits and sees the first; a double request with one session makes one tenant. Tested by `TestStartSomethingIsAtomic` (12 at once from one session make 1; 8 sessions at once make 5).
8. **Links**: a phone number names only a business (`taskiem_link_phone_business` leaves workspaces out), and a link code or shop address never reaches a workflows workspace (`taskiem_link_by_code`, `taskiem_link_request`). Tested by `TestLinksSkipWorkflowsWorkspaces`.
9. **Onboarding source** is `app` exactly when the person's sign-in email is a placeholder, whatever the door; the email-sending holds (`needVerified` with `sendsEmail`) now also cover `app` tenants whose owner has no confirmed email, as chat sign-ups. Tested by `TestStartSomething`, `TestStartedByPhoneWaitsForEmail`.
10. **Old "Just workflows for now" sign-ups** (every business area a row and off, Schools off) are made `kind = workflows` by 00450 (its Down leaves them so). Tested by `TestStage1FixesMigrationKinds`.
11. **WhatsApp, then SMS**: `sent_via` already says how the code went (`whatsapp` only when WhatsApp took it; `sms` when asked for or when WhatsApp refused it); a person who gets nothing on WhatsApp asks again with `via: "sms"`. Documented; no change in behaviour.
12. **Small**: `/v1/auth/second-proof/email/start` is audited (`auth.second_proof_link`); Slack's workflows sign-up asks "What should we call this workspace?" (`signup.ask_workspace`, `signup.workspace_bad`); the dead `newBusiness.Scope` is gone.

**Answers the app and web rely on:**
- 401 for a wrong or expired code on `/v1/auth/phone/verify` and `/v1/me/phone/verify` carries `code_wrong: true`; a used or expired ticket on `/v1/auth/phone/create` carries `ticket_expired: true`. The PIN's 401 keeps `tries_left`.
- One rule reads a person's own number (`engine/phone.Normalise`), on the phone way in, a phone change and chat sign-in: spaces, dashes, dots and brackets dropped; `00` is `+`; `+234` then `0` drops the `0`; `0` and 10 digits is `+234` and the 10; 10 digits starting 7, 8 or 9 is `+234` and them; a Nigerian number is exactly `+234` and 10 digits starting 7, 8 or 9; any other `+` and 8 to 15 digits. Anything else is 400 `{error, phone_invalid: true}`. `sender.NormalisePhone` reads `+`, `00` and `0` numbers by the same rule (so `+234 0803…` works there too) and keeps its own reading of bare digits.

**Not done:** the English copies of stage 1's chat words in `ha`, `ig`, `pcm` and `yo` stay, marked `reviewed: false`: `TestEveryEnglishMessageIsInEveryCatalogue` requires every catalogue to be complete, and an unreviewed entry is how drafts are marked. The two new Slack words are added the same way.
- A chat sign-in on a new device now lands on the person's own side, where "sign in with WhatsApp or Telegram" (`PUT /v1/me/chat-signin`, a business route) cannot be turned off until the device is proved and the person switches; making it a personal route is left for later. `TestAccessChatPasswordFollowsChatSignin` signs in on a proved device.

### Review of the review fixes: a device id is not a proof

A second review found one more way past the second proof, and proved it: the app's device id is chosen by the app, Your phones (`GET /v1/me/devices`) listed it, and a sign-in by code on a device that had been proved within 90 days kept that proof. Whoever held a reissued number could start a workspace, read the owner's phone's id from Your phones, sign in by code presenting it, and enter the owner's business. Fixed two ways (migration 00451, `api/devices.go`): **a sign-in by code never inherits an earlier proof**, on any device (the person proves once more after each sign-in by code; a passkey, password or email-link sign-in is unchanged), and **Your phones lists each phone by a stand-in** made from the person and the device id, from which the id cannot be read back; removing a phone takes the stand-in. Tested by `TestDeviceIdIsNotASecondProof` and the updated `TestDevices`. The web was not open to this: its device is a random server-made cookie that is never shown.

### Review fixes: web and app

Built on branch `stage1-fix-clients`, against the server's review fixes (`stage1-fix-server`, merged in, and tested end to end with it). The passkey proof sends `{credential}` as the server takes it; an accepted invitation is read as it came, since its `ways` name only what the person has.

1. **A wrong code is not a signing out.** A 401 carrying `code_wrong`, `ticket_expired`, `tries_left`, `sso_required`, `passkey_required` or `second_proof_required` keeps the session, on any route; any other 401 ends it, changing the number included (web `keepsSession` in `web/src/api.ts`, which replaced the blanket exemption of `/v1/me/phone/*`; app `Transport.stillSignedIn`). Tested by `apiAuth.test.ts`, `everyone_test.dart` and `you.spec.ts` (a wrong code on Change number).
2. **One rule for numbers**, the server's (`engine/phone`): `toE164` in `web/src/lib/phone.ts` and `mobile/lib/app/phone.dart`, with one table of cases in `phone.test.ts` and `test/phone_test.dart` (the server's: digits with a country code but no `+` are not a number). A number that cannot be one is told "That doesn't look like a phone number" before anything is sent; numbers are sent as `+234…` and read back grouped (`showPhone`) on both.
3. **Plain words.** `codeProblem` (web and app): "That code is not right. Check it, or ask for a new one." only for `code_wrong`; "That code has expired: ask for a new one." for `ticket_expired`; "That PIN is not right. 2 tries left."; 429 or `locked`: "Too many tries. Wait an hour and try again."; an account that is off: "This account can't sign in. Contact Taskiem."; any other server sentence starts with a capital and ends with a stop. The chat sign-in on the email page uses the same words.
4. **Second proofs leave the session where it is.** A passkey proves the device with `POST /v1/auth/second-proof/passkey[/options]` (web `passkeyProof` in `passkeys.ts`, app `passkeyProof` in `screens/second_proof.dart`), never a new passkey sign-in; the password is a way too (`ways.password`). The app's proof is one widget (`SecondProof`) used by the switcher, You's **Prove it's you**, Change number and invitations. The tab or app that asked for the email link checks `/v1/me` again on coming back (focus, visibility, the app resuming) and offers **I've opened the link**.
5. **Where the way in goes.** The way in keeps `?next=`: a page on You opens as it is; a business's page switches into the person's one business (its step first if it asks for one) and opens there, or with several asks "Choose the business to open this in"; anything else opens You. `?start=business` (and `workflows`) opens Start something with it chosen, and `/signup?start=business` is where the website's **Start on the web** and **Start 14 days free** and the business links page's "Start your business on Taskiem" now lead. **Start in Slack** is back under the phone box when `GET /v1/signup` offers Slack. The second proof's link opened where nobody is signed in says "Open this link in the browser where you asked for it", and its token goes neither into `?next=` nor the history.
6. **Once only.** Start something, the way in's **Create** and code, and Change number's confirm keep their button off from the first press until the page has moved on (a second press before it is drawn off sends nothing).
7. **Ways back.** **Use another number** on the account step (web); a code that ran out (`ticket_expired`, or the ticket refused on **Create**) goes back to the number with "Your code has expired: ask for a new one." (web and app; Change number says "That code has expired: ask for a new one.").
8. **Routing.** A workflows workspace opens on Workflows wherever a tenant opens: `/you` in a workspace, `/login` signed in, `/signup` signed in, single sign-on's return, an invitation accepted (to `/`, which knows the kind). Accepting an invitation that answers `sign_in` (`second_proof_required` with `ways`, or a business's own sign-in) shows that step and then switches, on the web and in the app. Chat sign-in's personal answer opens You on both.
9. **The app matches the web**: Change number asks "Do you still have your old number?" (**Yes, send it a code** / **No, prove it's me another way**) instead of guessing; **Your businesses** on You, and the new-device notice with **Prove it's you**; **Sign out**; the 60-second new-code countdown on the way in and Change number; "We sent a new code." is gone.
10. **The app offline.** Whether the kept session is personal is kept on the phone (`personal` in the key store, written on every sign-in and read on opening), so a personal session opened offline opens You, not a business's chats.
11. **Small.** A pasted "123 456" is six digits on the web (digits first, then six); the switcher's button has `aria-expanded`.

Tests: `you.spec.ts` (a number refused before sending, a pasted code, **Use another number**, the second proof's link signed out, `next` after a phone sign-in to You and to a business, Start something double-clicked making one business, a wrong code on Change number keeping the session, an invitation needing a second proof proved by the link opened in another tab); `phone.test.ts`, `apiAuth.test.ts`, `passkeyProof.test.ts`; `everyone_test.dart` and `phone_test.dart`.

**Left over.** The email sign-in still shows the server's "invalid email or password" as it comes (its parity picture would change with it); the invitation link page for someone with no account (`/accept-invitation`) is unchanged but for landing on `/`.

## As built: stage 2, stream A

Built on branch `stage2-core`, on stage 1 as merged (`ccr-87497aa3-gf060z`): the server of §8 (but the chat link's codes and the browser's SMS, stream B), with §14's two copies and §15's limits. The web and app screens are later streams; the API is written for them.

**Migration `00452_customer_door.sql`:**
- `customer_conversations` gain `line` (`whatsapp`, `app`; existing threads are `whatsapp`) and `person_id`; an app thread has `phone_number_id` `app`, one per person per business; the unique key is now `(tenant_id, line, phone_number_id, subject_id)`. They also gain the blocks both ways (`blocked_at` by the business, `person_blocked_at` by the customer), `last_reply_at`, `closed_reply_at` and `owner_told_at`.
- `customer_messages` gain the kind `card`, `card`, `attachment` (the business's copy, in its own storage) and `offer`.
- The person's copy: `person_conversations` (muted, archived, both blocks, the offers answer), `person_messages` (the same id as the business's copy: `customer_messages.id = person_messages.id`), `person_files`. Scoped by `taskiem_person_scope()` and, once a door names one business, by `taskiem_person_business()` (`app.person_business`) to that business's rows only (listed in `wide_policies.tsv`).
- `customer_offer_consents` (the business's record of the answer and when), `customer_chat_settings`, `customer_reports` (insert only, as the person), `customer_typing` (definers only).
- `storefront_orders` gain the kind `app` and `person_id`; the audit chain's actor types gain `person`.
- Definers (in `engine/db/definers.tsv`): `taskiem_customer_enter(person, business, action, detail)`, the door; `taskiem_customer_reach(business, person, action, detail)`, the way back; `taskiem_customer_business_names`, `taskiem_customer_typing`, `taskiem_customer_typing_of`, `taskiem_customer_order`, `taskiem_customer_waiting`.

**The door (§8.4).** `taskiem_customer_enter` refuses unless the person is the transaction's own, the business is active and a business (not a workflows workspace), the action is one of `customer.start`, `customer.chat`, `customer.block`, `customer.shop`, `customer.book`, `customer.orders`, `customer.order`, `customer.booking`, `customer.pay`, the business has not blocked the person (Block itself excepted), the person has an app thread with it (or, for `customer.start`, the business has a published shop, a resolved chat code, or an order of the person's), and the area the action needs is on (`storefront` and `orders` for the shop, Book, ordering and booking; `orders` for their orders and paying; nothing for the chat). It writes `{actor_type: person, actor_id: the person}` into the business's chain, then sets the scope to that business for the rest of the transaction. The way back (`taskiem_customer_reach`) needs the business in scope and an app thread with the person, refuses messages after the person's Block, audits messages and blocks (not read marks or typing), then puts that person's rows of that business in scope. A message is written to both copies in that one transaction.

**Endpoints, the person's side** (any user session for its own person: a personal session, listed in `personalRoutes`, or a business session; an API key is refused, 403):

| Endpoint | What |
| --- | --- |
| `GET /v1/me/chats` | Your chats, latest first (`?archived=1`), with `unread` |
| `POST /v1/me/chats` `{business_id \| code, from?}` | Start (201) or answer the one you have (200); `notice` says what the business sees; `ask_offers`; 409 without a phone, 403 `blocked`, 410 `link_replaced`, 429 limits |
| `GET /v1/me/chats/{id}` | One chat, with `typing` and the business's `hours` line |
| `PUT /v1/me/chats/{id}` `{muted?, archived?, offers?}` | Your settings; `offers` is recorded with the business |
| `GET /v1/me/chats/{id}/messages?before=` | Your copy, 50 a page |
| `POST /v1/me/chats/{id}/messages` `{text, client_id?}` | Write; 409 `you_blocked`, 403 `blocked`, 429 |
| `POST /v1/me/chats/{id}/files` (multipart) | A photo, voice note or PDF |
| `GET /v1/me/chats/{id}/files/{message}` | Your copy of a file |
| `POST /v1/me/chats/{id}/read`, `/typing` | Read marks (mirrored), typing |
| `POST`/`DELETE /v1/me/chats/{id}/block` | Block, unblock the business |
| `POST /v1/me/chats/{id}/report` `{reason, note?, consent: true}` | The last 20 messages to Taskiem's moderators |
| `GET /v1/me/chats/{id}/shop` | Their shop: items, sizes and colours, `few_left` (5 or fewer, as the page says), pickup, delivery areas and fees, when paid |
| `GET /v1/me/chats/{id}/services`, `/services/{service}/times?day=` | Book: services and free times |
| `GET /v1/me/chats/{id}/orders` | Your orders and bookings with them (placed in the app) |
| `POST /v1/me/chats/{id}/orders` (Idempotency-Key) `{lines, fulfilment, zone, address, note}` | An order of the kind `app` |
| `POST /v1/me/chats/{id}/bookings` (Idempotency-Key) `{service_id, start, note}` | A booking |
| `GET /v1/me/chats/{id}/orders/{reference}/pay` | Its pay link while something is due |

**Endpoints, the business's side** (`storefront.manage|order.handle` unless said):

| Endpoint | What |
| --- | --- |
| `GET /v1/customer-chats…` | As before; threads carry `line`, `blocked`, `notice` ("This customer has stopped messages"), and on one thread `offers` and `typing`; messages carry `card`, `attachment`, `offer` |
| `POST /v1/customer-chats/{id}/messages` `{text?, client_id?, card?, offer?}` | To an app customer: always a message, no window or template; `card` is `{type: order\|pay\|booking\|product, reference \| service_id \| item_id}`, made from the business's own data; an offer only after a yes, 2 a week (429) |
| `POST /v1/customer-chats/{id}/files` (multipart), `GET …/files/{message}` | Files to and from an app customer |
| `POST`/`DELETE /v1/customer-chats/{id}/block` | Block, unblock an app customer |
| `POST /v1/customer-chats/{id}/typing` | Typing |
| `POST /v1/customer-chats/app` `{reference}` | Writing first: the app thread of the person who placed an order |
| `POST /v1/customer-chats/offers` `{text, client_id?}` | A broadcast offer to every customer who said yes, counted per customer |
| `GET /v1/settings/customer-chats`, `PUT` (`storefront.manage`) | `closed_reply`, `closed_words`, `needs_you`, `tell_owner` |

**How it behaves.**
- **Orders and bookings in the app** go into the business's storefront inbox as sealed submissions (`Channel: app`, `PersonID`), as a page's do, and the same worker records them: an order is of the kind `app` (its run is the order workflow's: trigger `storefront/order`, body `kind: order`), a booking stays `booking`; both carry `person_id`. The customer's name and phone are sealed under the business's subject for the person's phone. Stock, delivery, the status page, counts and briefings treat `app` as `order`; messages to the customer of an `app` order (`NotifyCustomer`, the workflow's) go into their app conversation, not by SMS. Each order or booking leaves a card in both copies; a repeat of the Idempotency-Key is found by that card.
- **Files** are scanned (`media.ScanChatFile`, refused without a scanner), then checked and cleaned once (`media.CheckChatFile`: photos 10 MB, voice notes 5 minutes for Ogg and 5 MB otherwise, PDFs 25 MB without scripts), before either copy is kept: the business's in its own storage (`PutChatFile`), the person's in `person_files`.
- **When nobody answers (§8.6).** A customer's message tells those who handle orders (the owners when nobody does). While the business is closed by its shop's hours, the closed reply goes once a day, in the business's name, with its words or "We're closed now. We open at 9:00 and will answer then." (tomorrow, or a weekday, as it falls). An app customer waiting 30 minutes while open is a `customer_waiting` card on Needs you (`GET /v1/chat-alerts`); after 2 hours `RunCustomerChats` (every minute) tells the owners once per wait. Each can be turned off.
- **Limits (§15).** 10 new chats an hour and 30 a day, 20 messages a chat a minute; half that in an account's first week.
- **The person told of an answer.** Every business message to an app customer, once kept, is given to `Server.CustomerAnswered` (`CustomerAnswerHook`, with the person, business, conversation, message, the business's name, whether it is an offer and whether the chat is muted). Stream B fills it with the browser's SMS fallback.

**Hooks for stream B.**
- `Server.ChatCodes` (`ChatCodeResolver.ResolveChatCode(ctx, code) (business, error)`, with `ErrChatCodeUnknown` and `ErrChatCodeReplaced`), used by `POST /v1/me/chats {code}`; until set, no code is known (`noChatCodes`). When it lands, `taskiem_customer_enter`'s `customer.start` should check the code itself (`customer_chat_codes`) rather than trust the API's `via: code`.
- `Server.CustomerAnswered` (`CustomerAnswerHook.BusinessAnswered`), above.

**Deviations from the spec.**
- The person's copy is kept in their own rows under their own scope, but not sealed under a key of their own (as stage 1 kept phones in the clear): messages in `person_messages.body`, files as bytes in `person_files`.
- "Your orders with them" lists orders and bookings placed in the app (by `person_id`); storefront orders made with the same phone wait for the phone index and "These are mine" (§13, stage 3). Invoices are not listed; a business can send a pay link card for an order.
- A voice note's five minutes are read from Ogg only; others are held to 5 MB.
- Read ticks are always mirrored: the "leave them off" setting of §8.3 is not built.
- Every door entry is audited, looks included (the shop, the times, the hours line of `GET /v1/me/chats/{id}`), so the business's chain has one entry per look.

**Left for later.** Push notifications to a person (their side has no push yet; the hook carries what is needed); deleting a chat; the warning on a message asking for a transfer to a personal account (§15); offers paused for businesses muted or blocked far above the norm (§15); counting offers and messages against the business's plan (stream B); the web and app screens.

**Tests.** `TestCustomerDoor` (both copies, sealing, notices, typing, read marks, offers and broadcasts, cards, the shop, an `app` order through the order workflow and its card, pay refused when nothing is due, files and malware, blocks both ways, a report, mute, the closed reply once, Needs you, the owner told once, an area off, the audit entries); `TestCustomerDoorIsolation` (another person's chat, another business's thread, the door and the way back refusing another person, a business with no chat, an action not on the list, a business not in scope, a workflows workspace; through the door only that business's rows and only that person's chat with it; a business session is its person, an API key is refused); `TestCustomerDoorBookingAndLimits` (Book, the booking's card and repeat, a booking offer card, the first week's limits); `engine/media` `TestCheckChatFile`.


## As built: stage 2, stream B

Built on branch `stage2-links`, on stage 1 as merged (`ccr-87497aa3-gf060z` at 5fdb3fca): chat links and the storefront's **Chat with us** (§8.2), the phone index (§13), the shared number's new words (§12), the browser chat's SMS (§9) and who pays for codes and that SMS (§15). The conversations, the customer door, offers and blocks are stream A's (migration 00452); this stream's migrations are 00453 and 00454.

**Data.**
- `00453_chat_links.sql`:
  - `customer_chat_codes`: one live code per business (a partial unique index), the code kept only as a keyed hash beside the random nonce it is made from (the code is HMAC of the nonce with a key derived from `TASKIEM_MESSAGE_HASH_KEY`, spelt in an alphabet with no look-alikes, `23456789abcdefghjkmnpqrstwxyz`, ten characters), so the owner reads the link back and a copy of the database alone gives no one the codes. A replaced code keeps its row, `replaced_at` set, for 30 days.
  - `customer_chat_link_settings`: `takes_chats` (on), `storefront_button` (`whatsapp`, `taskiem` or `both`, the default), `link_in_updates` (null: on exactly when the business has no WhatsApp number of its own).
  - `customer_chat_notices`: "at most once per …" for the browser's SMS (per conversation per hour) and the answer to an outsider (per business per day), claimed in one statement.
  - `customer_message_charges`: sign-in codes sent by SMS to people who came through a business's link; `phone_codes.came_from`: the business a code was asked for through.
  - Definers: `taskiem_chat_code_find` (a code's hash → its business, and `replaced_at` within 30 days; only an active business, never a workflows workspace), `taskiem_reach_business_list` (`taskiem_reach_businesses` with the ids), `taskiem_auth_phone_code_came_from`.
- `00454_customer_phone_index.sql`:
  - `customer_phone_index (order_id, tenant_id, phone_hash, created_at)`: no number, name or amount. The order's foreign key deletes its row with it; a trigger on `subject_keys` deletes the rows of an erased customer's orders (in the business's own transaction); a trigger on `storefront_orders.forgotten_at` deletes the row of an order whose customer retention forgot.
  - `phone_index_backfill` marks the businesses the one-time job finished; `taskiem_phone_index_backfill_due` lists the others, ids only.
  - `taskiem_customer_phone_orders(person, index key, limit)`: given a person, never a phone, it reads their verified phone from `user_phones`, makes its hash and returns for each indexed order only `reference, state, total_kobo, made_at, business, business_id, waiting`. `waiting` is an order made before the phone was verified on the account and not yet confirmed (`taskiem_customer_phone_orders_confirm`, "These are mine", `user_phones.orders_confirmed_at`, forgotten when the number changes).
  - The hash is `sha256(index key || 'phone-index:' || E.164)`, where the index key is HMAC(`TASKIEM_MESSAGE_HASH_KEY`, "taskiem phone index"): the database is given that narrowed key, never the platform key, and the hashes match nothing else.

**Engine.**
- `engine/chatlinks`: `Links` (`CurrentTx`, made the first time; `ReplaceTx`; `Resolve`; `LinkFor`; `URL`), `SettingsTx`/`SaveSettingsTx`, `CardTx` (the business's card: the profile's name, tagline, area and logo over the published storefront's, its hours and whether open now, its shop's address, whether it takes chats; a rejected photo left out), `TakeNoticeTx`, and the browser chat's SMS (`Tell`).
- `engine/phoneindex`: `Index.WriteTx` (in the order's transaction), `Backfill` (business by business, each order's sealed phone opened in its own business; trade orders and forgotten or erased customers left out), `Orders`, `Confirm`.
- Storefront orders and bookings (`Orders.record`) and chat orders (`PlaceChatTx`) write the index in their own transaction (`storefront.Orders.PhoneIndex`). Orders between businesses (`trade`) are not indexed.

**Endpoints** (in `api/openapi/openapi.yaml`; the app's in `mobile/api.txt`, regenerated):

| Endpoint | Who | What |
| --- | --- | --- |
| `GET /v1/chat-links/{code}` | anyone (60 a minute per address) | The business's card: `name, tagline?, area?, photo?, hours?, open_now?, takes_chats, shop?, code, link, words`. 410 `{gone: true, error: "This link no longer works. …"}` for a code replaced in the last 30 days; 404 for any other |
| `GET /v1/customers/chat-link` | `storefront.manage` or `order.handle` | The link (made the first time): `code, url, created_at, words` ("Chat with {business} on Taskiem: {link}"), `qr` (where its QR code is), `settings`. 503 without a message hash key |
| `GET /v1/customers/chat-link/qr` | the same | The link's QR code, SVG or `?format=png` (the storefront's QR code maker) |
| `POST /v1/customers/chat-link` | `storefront.manage` | **Make a new link**: audited `chat_link.replaced`; a published storefront is published again with it |
| `PUT /v1/customers/chat-settings` | `storefront.manage` | `{takes_chats?, storefront_button?, link_in_updates?, link_in_updates_default?}`; audited `chat_link.settings`; the storefront is published again when its button or `takes_chats` changes |
| `GET /v1/me/storefront-orders` | the person (a personal session too) | `{orders: [{reference, state, total_kobo, made_at, business, business_id, waiting}], waiting}`, `?business=` one business's, `?limit=` up to 200 |
| `POST /v1/me/storefront-orders/confirm` | the person | "These are mine": `{confirmed: true}`; 409 with no phone |
| `POST /v1/auth/phone/start` | anyone | Gains `chat_code`: the link the person came through |
| `GET /m/{code}` | anyone | A page with the business's name, photo, hours and open now, **Chat here in your browser** (to `<public URL>/login?next=/m/{code}`) and **Get the Taskiem app** (`TASKIEM_APP_GET_URL`); 410 "This link no longer works", 404. Only where this server serves no web app: where it does, `/m/{code}` is the web app's (the browser chat, a later stream), which reads `GET /v1/chat-links/{code}` |

`/m/*` is in the app's links (`appPaths`), so a chat link opens the app where it is installed. `/v1/customers/*` belongs to the Orders area (`areaPaths`), with customer chats: off, the owner has no link to read or replace.

**The storefront.** Publishing (`publishGated`) passes the chat link and the button to `storefront.PublishWith`: the published model gains `chat_link`; `taskiem` takes WhatsApp's link off the page, `whatsapp` leaves the chat link out, and a business not taking chats offers only WhatsApp. Every look shows **Chat with us** where it shows WhatsApp (the shared contact line too). Replacing the link, or changing the button, publishes a published page again, in its own transaction after the change (a page that cannot be published now keeps its old button; the change stands).

**The shared number and bot (§12).**
- An update to an outsider ends "Reply in the Taskiem app: {link}" when the business offers it (default on with no number of its own) and takes chats. It is in the message's text, which goes inside WhatsApp's 24 hours and always on Telegram; outside the window `taskiem_update` goes as before, without it. `taskiem_update_chat` (the same words, "Reply in the Taskiem app.", and a **Reply in Taskiem** button opening `/m/{code}`) is in the template library and `docs/whatsapp.md`, and goes in its place only with `TASKIEM_WA_UPDATE_CHAT_TEMPLATE=on`, once Meta approves it.
- A stranger on the shared number or bot gets `signup.offer_everyone` with **Start my business**, **I use Taskiem** and **I'm a customer** (`su:customer`); "no", "not now" or "no thanks" typed is Not now (`su:no` pressed on an older offer still is). **I'm a customer** answers "To chat with a business, open its Taskiem link, or find it in the Taskiem app: {app link}" (`TASKIEM_APP_GET_URL`), or without an app link where the business shares its link. Slack's offer is unchanged.
- An outsider writing to the number that sends them updates gets "To chat with {business}, open {link}", one line for each business reaching them that takes chats and has not said so in the last day, within the existing limit (two answers in ten minutes); otherwise the fixed answer, as before. The same on Telegram.
- New words in `engine/lang/messages` (`reach.reply_in_app`, `reach.chat_link`, `signup.offer_everyone`, `signup.button_customer`, `signup.customer`, `signup.customer_no_app`), the English copied unreviewed into the other languages.

**Who pays (§15).** A code asked for with a `chat_code` that names a business is recorded with it (`phone_codes.came_from`) and counts against it: by WhatsApp as one of its templates (`SendTemplate` with the business, the usage meter), by SMS in `customer_message_charges`, which the SMS allowance and `sender.Usage` now count with `platform_messages`. A code with no link, or an old link, is Taskiem's. The browser chat's SMS goes from the business's own outbox (`sender.Queue`, its allowance).

**For stream A** (the conversations and the customer door):
- `s.ChatLinks.Resolve(ctx, code) (uuid.UUID, error)`, behind `chatlinks.Resolver`: `ErrNotFound`, `ErrReplaced` (say "This link no longer works"), `ErrOff`. A business that set `takes_chats` off should not be offered a new conversation: `chatlinks.SettingsTx`.
- `s.tellAnswered(ctx, chatlinks.Answer{Tenant, Conversation, Person})` after a business's reply is kept, outside its transaction (`chatlinks.Teller`, `s.Answered`; `chatlinks.NoTell` tells no one). It pushes through `chatlinks.Pusher` where one is set, else texts once per conversation per hour. There are no person-scoped browser push subscriptions yet: `Tell.Push` is nil, so every answer to a person with a verified phone goes by SMS; give `Tell` a `Pusher` when the browser chat registers one.
- `s.PhoneIndex.WriteTx(ctx, tx, tenant, order, phone)` in the transaction that makes an `app` order (`storefront.PhoneIndexer`).
- `chatlinks.TakeNoticeTx` for any other "once per …".

**Deviations.**
- The narrow function also returns `business_id` (to open "Chat with {business} about this order") and `waiting`, which is not an order's column but the confirmation's; nothing else.
- The function is given a key: the narrowed index key, not the platform key, so that it, not its caller, makes the hash of the person's own phone.
- The storefront's button is a chat setting (`customer_chat_link_settings.storefront_button`), not a field of the storefront's card, so it sits with the link and its other settings; the page takes it at publishing.
- `GET /v1/me/storefront-orders` returns every order, the waiting ones marked, rather than leaving the waiting ones out until confirmed: "These are mine" is asked on that list.
- The chat link's page is served by the API only where it serves no web app; elsewhere it is the web app's.
- Codes counted against a business by SMS are kept in their own table, not `platform_messages` (they go straight to the provider, outside the outbox, as stage 1 sends them).

**Tests.** `engine/chatlinks` (`TestCodesHaveNoLookAlikes`: 200 businesses, every code ten characters of the alphabet, none twice; `TestLinkLifecycle`: one live code, only its hash kept, the old code `ErrReplaced` at 29 days and gone at 31, cleared by a later new link, workspaces and suspended businesses resolve to nothing, one business never reads another's codes; `TestLinkMadeOnceAtOnce`; `TestNoticesOncePerPeriod`; `TestSettings`; `TestTellAnswered`: one SMS per conversation per hour, each conversation its own, a push instead of the SMS, nobody without a phone). `engine/phoneindex` (`TestBackfill`, `TestOrdersOfAPerson`: only the person's own verified phone, only the listed columns, nothing under another key or for a disabled account; `TestTheseAreMine`). `engine/storefront` `TestOrdersWritePhoneIndex` (written with a storefront order and a chat order; removed at retention, on erasure and when the order is deleted). `engine/publicweb` `TestStorefrontChatButton` (every look, both, alone, none). `api`: `TestChatLinks`, `TestPhoneCodeThroughChatLink`, `TestMyStorefrontOrders`, `TestSharedNumberChatWords` (WhatsApp inside and outside the window, the template behind its flag, turned off, the outsider's answer once a day, Telegram), and `TestChatSignupOnWhatsApp` for the three buttons.

**Left over.**
- The web's `/m/{code}` (the browser chat and its page) and the app's screens: Tools > Customers > Your chat link, Settings, **Chat with us** on You, "These are mine".
- Meta's approval of `taskiem_update_chat`, then `TASKIEM_WA_UPDATE_CHAT_TEMPLATE=on`; `TASKIEM_APP_GET_URL` set where the app is listed.
- Push to a person's browser for the browser chat (a `chatlinks.Pusher`).
- The full Orders list on You (stage 3); re-making the index when a person changes phone is not needed (it is keyed by the order's phone, and the function reads the person's phone now), but orders made with the old number then stop being listed.
- The operator dashboard's code costs by country (§15).

## As built: stage 2, streams A and B together

- **A chat link opens the door only when it is the live code.** Stream A's door (00452) let a person open a business with no published shop when the API said `via: code`, and the API took `from: "code"` from the person, so any business could be opened by its id. Migration 00455 redefines `taskiem_customer_enter`: `customer.start` from a link carries the code's keyed hash (`engine/chatlinks`), and the door checks it against the business's live code in `customer_chat_codes`. The hash is left out of the audit entry. The API refuses `from: "code"`.
- **Codes resolve through the chat links:** `POST /v1/me/chats {code}` uses `chatlinks.Links.Resolve` when chat links are set up; a replaced link answers 410 with `link_replaced`.
- **A business that turned chats off** (`takes_chats: false`) takes no new chats from any door (409); chats already open go on.
- **The person is told when the business answers** (§9): after each business message in an app chat, `chatlinks.Tell` (browser push when there is one, else the SMS at most once an hour). Not for an offer, and not into a muted chat.
- **App orders are in the phone index:** they go through the storefront inbox like a page's order, which writes the index.
- **One settings table each:** both streams made a `customer_chat_settings`. Stream B's is renamed `customer_chat_link_settings` (whether the business takes chats, the storefront button, the link in updates); stream A's keeps the name (the closed reply, Needs you, telling the owner).
- Tested in `TestCustomerDoorWithChatLinks`.

## As built: stage 2, web

Built on branch `stage2-web`, on stage 1 and both stage 2 server streams merged (33efa461): the web screens of §8, §9, §12 (the business's settings), §13 ("Chat about this order") and §15.

**You: a person's chats** (`web/src/pages/PersonChats.tsx`, `PersonTools.tsx`, `AppCards.tsx`, words in `lib/personchat.ts`).
- **The list** on `/you`, in place of stage 1's placeholder: a row per chat (`GET /v1/me/chats`) with its last words ("You: " before the person's), time, unread count, muted mark and an unsent draft; **Archived** shows `?archived=1`. Polled every 8 seconds.
- **A conversation** at `/you/chats/{id}` fills the window (`you-chat`; on a phone the top bar goes and Back returns), built from the chat's own pieces: `ChatHead` (the name, with "typing…" or the hours line under it), `ChatStream`, `BubbleShell` and `useComposer`/`ComposerView` (emoji, photo, PDF, camera, voice note, paste and drop, drafts), which gained a `lead` for the conversation's own tools button. Messages and the chat are polled every 4 seconds; read marks are posted while the tab is seen; typing is posted at most every 4 seconds. A message shows at once with a clock and is replaced by the server's copy (`client_id`); one refused comes back to the box with the reason. Files are the person's own copy (`/v1/me/chats/{id}/files/{message}`).
- **The first-chat notice** (what the business sees, and that Taskiem may read business chats) is shown once, from the start's answer, with **OK**. **The offers question** ("Get offers from {business}?", **Yes**/**No**) is a card at the end of the thread while `ask_offers`; an offer is marked **Offer** with **Stop offers from {business}** under it while the answer is yes.
- **The menu**: Mute/Unmute notifications, Archive/Unarchive, Stop offers or Get offers (once asked), Block/Unblock (Block asks first, in the app's own sheet: nothing is deleted), **Report** (a sheet: why, a note, and consent to send the last 20 messages; afterwards it offers Block), and **Sign out** (§9, for a shared phone).
- **The tools button**: **Their shop** (items with price, "Only 3 left", sizes and colours from a list, a stepper, then a check step with pickup or delivery, the delivery area and its fee, the address, a note, how it is paid, and **Place order** with an `Idempotency-Key` kept for the basket as checked; when paid at ordering, **Pay** right after), **Book** (services, days, Lagos times, a note, **Book** with its own key; a time taken since says so and shows fresh times), **Your orders** (state in a customer's words, with **Pay** where something is due) and **Pay** (only those). **Pay** asks for the pay link, then shows **Open the pay page** (a new tab: a link opened after a request would be blocked as a pop-up).
- **Cards**: an order card (with **Pay** while its order has something due, read from the person's orders once the thread has order cards), a pay card (its link), a booking offer (**Book** opens Book at that service) and a product (**Order** opens their shop with it in the basket).
- **Orders from shops** (`MyShopOrders` on `/you`, §13): up to 10 of `GET /v1/me/storefront-orders`, each with **Chat with {business} about this order**; when some wait, **These are mine** (`POST …/confirm`) first, and waiting ones have no button. The chat opens at `/you/chats/new?business=&ref=` (`POST /v1/me/chats {business_id, from: "order"}`) with "Hello, about my order {ref}: " in the box.

**The chat link's page** (`pages/ChatLinkPage.tsx`, `/m/{code}`, the SPA's): from `GET /v1/chat-links/{code}` the business's logo or letter, name, tagline, area and open now, its hours folded, **Chat here in your browser**, **Get the Taskiem app** (when the server has the link), and **Visit their shop**. 410: "This link no longer works"; 404: "This is not a chat link"; `takes_chats` off: "{business} is not taking chats in Taskiem right now." Signed out, **Chat here** goes to `/login?chat={code}&next=/you/chats/new?code={code}`: the way in says "Chat with {business}", warns "Is this your phone? If not, don't stay signed in…", and sends `chat_code` with `/v1/auth/phone/start` (so the business pays); after the code (and the account, for a new number) the chat opens (`POST /v1/me/chats {code}`). Signed in on You it opens at once; in a business's session it moves to You first (`/v1/me/switch {personal}`). The app link is kept in the browser, so the conversation can show the quiet "Get the Taskiem app for your chats with every business" line under it.

**The business's side.**
- **Customers threads** (`CustomerChat.tsx`, `lib/customerlinks.ts`): an app thread is marked "· in the app" in the list and "Customer in the app" (or "typing…") under its name; it has no 24-hour note or length limit. Its box adds a cards button (`CustomerCards.tsx`: an order or its pay link, from the thread's references or one typed; a booking offer from the shop's services; a product from the catalogue, searchable, the chat's `#`), a clip for a photo, PDF or voice note (`POST …/files`), typing, and **Send as an offer** for a customer who said yes. Cards, files and the Offer mark are drawn as on the person's side. The menu has **Block this customer** (asks first) / **Unblock**; a blocked or stopped thread says so in the box's place ("This customer has stopped messages").
- **Writing first**: an order of the kind `app` shows **Write to them in the app** on its page (`POST /v1/customer-chats/app`), which opens the thread. App orders are shown as orders (their lines, money and delivery), labelled "Order in the app".
- **Needs you**: the `customer_waiting` card, with **Answer them** opening the thread.
- **Customer chats** (`pages/CustomerChatSettings.tsx`, `/settings/customers`, a card on Settings for those who handle orders): **Your chat link** (the link, its QR code, the words to paste with Copy, Download the QR code, and **Make a new link** for `storefront.manage`, asked first); **Taking chats** (takes chats, the shop's button: both, Chat with us only, WhatsApp only, and the link at the end of updates); **When nobody answers** (the closed reply and its words, Needs you, telling the owner); **Send an offer** (to every customer who said yes, with how many were sent and skipped). **Tools > Customers** in the Taskiem chat (a tile, for those who handle orders) opens Your chat link and Send an offer in a sheet.

**Server change.** `GET /v1/chat-links/{code}` gains `app` (`TASKIEM_APP_GET_URL`), so the web's page can offer the app as the API's own page does (tested in `TestChatLinks`; `mobile/lib/api/api.g.dart` regenerated).

**Tests.** `web/e2e/customer-chat.spec.ts`: a chat link opened signed out at 390 px, Chat here, the phone way in with `chat_code` (checked in the request), the chat opened with its notice and the offers question, a message each way, the thread in the app on the business's side with no window, an offer with Stop offers, read ticks, Block and its notice on both sides, a report with consent, the list at 360 px and sign out; the business's settings at 390 and 360 px, chats off on the link's page, the closed reply's words, a new link and the old one's "no longer works", an offer to nobody; every tool and card from stand-ins at 390 and 360 px (the order's body and key, the booking's, the pay link, a product card's basket, a booking offer, a file's address); a replaced link and a business not taking chats. axe in both themes throughout. `accessibility.spec.ts` adds `/settings/customers` to the sweep and a test of the chat link page and a person's chat at 390 and 360 px in both themes; its You test stands in for the new lists. Unit tests: `lib/personchat.test.ts`, `lib/customerlinks.test.ts`; the new pages are in `plainwords.test.ts`.

**Deviations and left over.**
- The browser chat is You's conversation at `/you/chats/{id}`, not `/chat?with=…` (§9), which is the business's chat page.
- No live stream for the person's side: the list and the conversation poll (the stream is a business's). No browser push for people (not on the server yet); "Tell me when they answer" is not offered.
- Sending a message that fails takes it out and puts its words back with the reason, rather than WhatsApp's red "!" with Retry (the business chat's outbox is per link conversation).
- Reactions, replies, stars, forward, search in a conversation and message info are not on the person's chat (the server has none for app chats).
- The person's own "Your orders" lists orders placed in the app (stream A); "Chat about this order" from shop orders is on `/you`, not a full Orders tab (stage 3).
- `GET /v1/chat-links/{code}` is cached for a minute (`max-age=60`), so a browser that opened a link just before it was replaced may show the old card for up to a minute.
- **Write to them in the app** shows for orders of the kind `app`; a booking made in the app is a `booking` and the web cannot tell it from a page's, so it is not offered there (the server would answer it).
- The order card's **Open** on the business's side opens the order's page; there is no invoice card.

## As built: stage 2, app

Built on branch `stage2-app` (`mobile/`), on stage 1 and both stage 2 server streams as merged (33efa461). No server changes: every screen uses the endpoints of streams A and B as they are, through the generated client (`mobile/api.txt` already listed them). The web's screens for stage 2 were being built at the same time, so the words here are the spec's and the server's; where the web's differ once it lands, the app follows it (§6).

**The person's side (You).**
- **Chats** (`lib/chat/my_chat.dart`, `lib/screens/you.dart`): You lists the chats with businesses from `GET /v1/me/chats`, latest first, each with its last line ("You: …", "You blocked them"), time, unread count and mute mark, read again every 10 seconds while You is on top. Archived chats (`?archived=1`) are apart, behind an **Archived** row with how many. "No chats yet / Chats with businesses will show here." when there are none (a server with no chats, 404, counts as none).
- **A conversation** (`MyChatScreen`, `MyChatData`): the business's name and its hours line under it (`hours.line`), "typing…" while it types; the messages of `GET /v1/me/chats/{id}/messages` (older pages as the reader nears the top), read marks posted when theirs are shown (`/read`), the person's typing told at most every 3 seconds (`/typing`), and their own messages ticked (one tick sent, two blue once read). Words go with a `client_id`; a refused message gives its words back to the box with the server's reason (429: "You are sending too fast…"). Photos, voice notes and PDFs go as multipart (`/files`, the chat composer's own picker and recorder, 25 MB at most here, the server checking again) and come from the person's own copy (`/files/{message}`), drawn as the business chats draw them (`ChatFiles` now takes where a message's file is).
- **Said once:** the notice of `POST /v1/me/chats` (what the business sees of you, that Taskiem may read business conversations) shows over the thread until **OK**. "Get offers from {business}?" shows over the box while `ask_offers` and the answer is `unasked`; **Yes** and **No** go as `PUT {offers}`.
- **Offers** are marked **Offer**; under one, while the answer is yes, **Stop offers from {business}** (`PUT {offers: false}`: "No more offers from {business}. Messages about your orders still come.").
- **Cards** (`lib/chat/door.dart`, shared with the business's side): an order (its state, in the customer's words: "Sent", "Waiting for them to accept", then the business's), a pay link ("₦4,500 due", **Pay** opens its page in the app's browser tab), a booking offer (**Book**), a product (**See in their shop**).
- **The menu:** Mute / Unmute notifications, Archive / Unarchive (archiving closes the chat), Get offers / Stop offers from {business}, Block {business} (asked first: "They can no longer write to you. Nothing is deleted…") and Unblock, Report {business}. While blocked the box is gone and a line says so with **Unblock**; while the business blocked the person, "{business} is not taking messages from you."
- **Report** (a sheet): why (spam, a scam or fraud, abuse or threats, something else), a note, and "Send the last 20 messages of this chat to Taskiem's moderators", without which nothing is sent; the server's thanks shown after.
- **The tools** (`lib/chat/my_chat_tools.dart`), behind the composer's tools button (`Composer.onTools`): **Their shop** (items at the shop's prices, each size and colour its own line, "Only 3 left", sold out; a bag, then pickup or delivery with the delivery areas and fees, the address, a note and how they take payment; **Place order** with an Idempotency-Key kept for the same bag, so a retry after a lost answer places one order), **Book** (the linked businesses' Book sheet, given the chat's path: no phone asked, its own Idempotency-Key), **Your orders with them** (reference, state, when, total) and **Pay** (only what is due; **Pay** opens the pay link from `GET …/orders/{reference}/pay` in the app).

**Chat links** (`lib/screens/chat_link.dart`, `lib/app/applinks.dart`):
- `/m/{code}` is a page the app opens (`appPageOf`). Signed in on You, the business's card (`GET /v1/chat-links/{code}`: photo or letters, name, tagline, area, open now, the week's hours) with **Chat with {business}**, which starts the chat (`POST /v1/me/chats {code}`) and opens it with its notice. A business session opens the same screen (a business session is its person too). "{business} isn't taking chats in Taskiem right now." when `takes_chats` is off; 409 without a phone offers **Add your phone**.
- Signed out, the link waits behind signing in: the card shows over the phone way in ("Sign in to chat with {business}."), the code goes with the phone code (`chat_code`), and once signed in the chat starts without asking again (`LinkInbox.shownSignedOut`).
- 410 (and 404) says **This link no longer works**, with the server's reason.

**Shop orders on You** (§8.1, §13): `GET /v1/me/storefront-orders` under **Your shop orders**. Waiting ones (made before the phone was on the account) are listed under "We found an order made with your number before it was on your account. Is it yours?" and **These are mine** (`POST …/confirm`). The person's own open a choice: **Chat with {business} about this order**, which starts (or opens) the chat with that business (`{business_id, from: "order"}`) with "About my order {reference}: " in the box.

**The business's side** (`lib/chat/customer.dart`, `lib/chat/customer_app.dart`, `lib/chat/chat_link_page.dart`):
- A customer thread of the line `app` says "Customer in the app" under the name, has no 24-hour window or length limit, shows the customer typing, and draws cards, files and offers as above. Staff typing is told to the customer (every 3 seconds at most).
- **Rich replies:** a **+** before the box opens **Send**: an order, a pay link (chosen from the business's orders, this customer's first by name, or by reference), a booking offer (a service), a product (an item of the catalogue, not its sizes), a photo, a PDF, and, to a customer who said yes, **Send as an offer** (the box's words, `offer: true`). Cards are made by the server from the business's own records (`card: {type, reference | service_id | item_id}`).
- **Block this customer** / **Unblock this customer** in the thread's menu (asked first). A customer who blocked the business: "This customer has stopped messages", and no box; in the list, "Stopped messages" (WhatsApp's "Sent STOP" stays for WhatsApp threads).
- **Your chat link** (Settings > **Customer chats** > **Your chat link**, for `storefront.manage` or `order.handle`): its QR code (`…/qr?format=png`), the link, the words to paste with **Share** (the phone's share sheet) and **Copy the link**, and for `storefront.manage` **Make a new link** (asked first: the old one says "This link no longer works" for 30 days). Then the settings: **Take chats in the Taskiem app**, **Your link at the end of updates**, **Your shop's chat button** (both, Taskiem, WhatsApp; `PUT /v1/customers/chat-settings`), and **When nobody answers** (`/v1/settings/customer-chats`): **Answer when you're closed** with the business's own words (empty: Taskiem's), **Needs you**, **Tell the owner**. Without `storefront.manage` they are shown, not changed.

A file's row in a bubble (`AttachmentView`) is pressed in 48 by 48 now, drawn as before (`TapArea`); the linked businesses' chats have the same row.

**Tests** (`flutter test`): `test/my_chats_test.dart` (13: the app link, You's list and archived, a conversation's notice, offers question and Stop offers, a pay card and read ticks, typing; writing and a refused message; the menu with Block and Report's consent; their shop with sizes, few left, delivery and one Idempotency-Key over a retry; Pay; Book without a phone; a chat link's card and start; a link that no longer works; the way in signed out sending `chat_code`; shop orders, "These are mine" and chatting about an order; the conversation and its shop pressable, and at large text) and `test/customer_app_test.dart` (6: an app thread (pressable), rich replies and an offer, a customer who stopped messages, Block and Unblock, Your chat link with its settings, and without managing the shop).

**Not done.**
- Push for a person's new message (not on the server); the person's chats are read again every 4 seconds while open and every 10 on You.
- Parity pictures: the web has no pictures of these screens yet, so there are no parity entries; they are to be added once `web/e2e/parity.spec.ts` takes them.
- **Your chat link** is in Settings only: the business chat's tools panel has no Customers tool yet (the web's tools list, `web/src/lib/tools.ts`, which the app copies, has none either).
- Saved addresses (§13) and the warning on a message asking for a transfer to a personal account (§15); a booking offer card opens Book at its services, not at that service.
- The person's side keeps nothing on the phone for offline (the business chats do); offline it says so and reads again when back.

## As built: stage 2, review fixes (links, billing, push)

Built on branch `stage2-fix-links`, on 530b4dbe (stage 2 server, web and app merged). Two founder decisions (2026-11-17, now in §9 and §15) and the review's findings on the chat links, the phone index and the shared number. The conversations' own fixes are a parallel stream's (migration 00456); this one's migrations are 00457 to 00460.

**Who pays for a sign-in code through a link (§15, decision A; `00457_chat_link_signup_charges.sql`).**
- Nothing is charged when a code is sent: every code goes on Taskiem's account (WhatsApp's template with no business, SMS with no charge row). After sending, each sign-in code records where it was asked for through and how it went (`taskiem_auth_phone_code_source`: `phone_codes.came_from`, the business or none, and `sent_via`); a code asked for again without a link is no one's.
- A **new** account (`POST /v1/auth/phone/create`) keeps the business its proving code came through (`customer_signup_sources`, by `taskiem_auth_phone_signup_source`, in the new account's own scope, with the code's SMS segments). Signing an existing account in records nothing.
- The customer door charges it: `taskiem_customer_enter`'s `customer.start` that opens the person's first chat with **that** business calls `taskiem_customer_signup_charge` (not granted to the app), which settles the source once: a row in `customer_message_charges` (its channel, `sms` or `whatsapp`, and segments) while the business has had fewer than 50 charged codes today (UTC, under a per-business advisory lock), else Taskiem's (`charged = false`, no row). Rolled back with a start that fails.
- Only charged codes are in `customer_message_charges`, so only they count against the business's SMS allowance (`sender.Queue`, `sender.Usage`). `taskiem_auth_phone_code_came_from` (00453) is gone.

**Push on the person's own side (§9, decision B; `00460_push_on_your_side.sql`).**
- `push_subscriptions` and `push_outbox` take rows with no business (`tenant_id` NULL): a person's own devices and pushes, scoped by `taskiem_person_scope()` (policies `person_own`, in `wide_policies.tsv`), one per person and address (`push_subscriptions_person`). Such a device takes only the new kind `you`; a business's device never takes it (a check). `DropOrphansTx` (losing one's last role in a business) no longer touches them.
- **Registration** is the existing endpoints, on the person's side when the session is personal (now in `personalRoutes`) or with `?side=you` from a business session: `GET /v1/me/push/key`; `PUT /v1/me/push` (a browser's `{endpoint, keys: {p256dh, auth}, label?, preview?, replaces?, known?}`, or the app's `{kind: "fcm", token, platform, label?, preview?}`, as before; `kinds` may only be `["you"]`, the default); `GET /v1/me/push`; `PATCH /v1/me/push/{id}` (`label`, `preview`); `DELETE /v1/me/push/{id}`; `DELETE /v1/me/push` `{endpoint | token}` when signing out. Answers gain `side` (`business` or `you`), and `kinds` is `["you"]` on that side. An app phone is still tied to its install's session (`device_id`).
- **What is pushed.** Every business message in an app chat that is not muted goes to `chatlinks.Tell` (`Answer` gains `Words`, `FromPerson` and `Offer`; the single call site in `customer_app.go`). Tell first asks `Server.PersonPusher()` (`chatlinks.Pusher`): if the person has a live device of their own (taking `you`, not failing, a phone's install signed in, and this server able to send to its kind: `push.LiveYouTx`), a push is queued in the person's scope: title the business's name, body the first 120 letters (or "Sent you a photo", a card's title…), link `/you/chats/{id}` (the person's conversation), tag `you:{id}`, `business` empty, `kind` `you`; with the preview off, "Taskiem" / "A business wrote to you". Order updates (`ChatNotify`, sender `system:orders`) and offers are pushed the same way. `RunPush` drains the people with pushes due (`taskiem_push_due_people`) in each one's own scope after the businesses; the badge counts their chats with something unread.
- **The SMS** goes only when no push could be queued, only for a reply from a person at the business (not `system:` senders, not offers), once per conversation per hour, from the business's outbox (its allowance), as before.

**Review fixes.**
1. **A new link or the chat settings no longer publish the draft.** `republishForChat` uses `storefront.RepublishChat`: the published snapshot copied as the next version with only its chat link and WhatsApp link changed; no moderation, the draft untouched; nothing for a page that is not published.
2. **Orders off means no chats.** `chatlinks.SettingsTx` reports `takes_chats` false (and `orders_off`) while the Orders area is off, so the storefront's button, the card, the link at the end of updates, the outsider's answer and the API's start all follow; the door (00457) needs Orders for `customer.start` and `customer.chat`. Turning Orders on or off publishes the page's chat button again.
3. **Waiting orders are only counted.** `GET /v1/me/storefront-orders` lists only confirmed orders (no `waiting` field on an order) and answers `waiting` and `waiting_count`; nothing of an order made before the phone was on the account is shown until "These are mine". The phone-order functions refuse a person not in scope and are called in `InPersonTx` (00458: `taskiem_customer_phone_orders(person, key, business, limit)`, `taskiem_customer_phone_orders_waiting`, the confirm).
4. **The index's fill** reads each batch's orders with their customer's key `FOR SHARE` (only keys not shredded, orders not forgotten), so an erasure or retention running at the same time waits and then removes what was written, or came first and is seen; an order it cannot open is skipped (in a savepoint) and counted (`phone_index_backfill.skipped`); one replica fills a business at a time (a session advisory lock); a business that fails is marked (`tries`, `failed_at`) and tried again after an hour a try (at most a day), behind those never tried.
5. **A typed "no" is Not now only after the offer.** The offer on the shared number records that it went (`chat_signup_offers`, by address digest); "no" within the hour after it is Not now (`taskiem_chat_signup_offer_taken`, which takes it); otherwise an outsider who receives updates gets the outsider's answer (the chat link, or the fixed answer). `00459_chat_notices_purge_signup_offers.sql`.
6. **`customer_chat_notices` are emptied** after two days by the chat history's sweep (`taskiem_customer_chat_notices_purge`, also sign-up offers past a day and sign-up codes no chat settled in 30 days).
7. **`SaveSettingsTx`** writes only the fields given, in one statement (no read before the write).
8. **`?business=`** is passed to the function, before the limit.
9. `web/e2e/whatsapp.spec.ts` expects 25 templates (both of its counts).

**Not done / for the clients.**
- The web and the app must register the person's own device: the web's browser chat calls `PUT /v1/me/push` (personal session, or `?side=you`) after **Tell me when they answer**; the app registers its FCM token on the person's side too (`?side=you` from a business session), shows the `you` channel, and opens `/you/chats/{id}` (the app's `appPath` accepts it; its router does not yet map it to the person's chat). A payload with no `business` is for You.
- A WhatsApp code that is charged is a `customer_message_charges` row (`whatsapp`); it is not added to the WhatsApp template meter.
- The web and app still show `waiting` per order; they now get none listed until confirmed and should show `waiting_count`.

**Tests.** `api`: `TestPhoneCodeThroughChatLink` (codes through a link charge nothing; a new account's first chat charges once and counts in the SMS; again, nothing; an existing account through the link, nothing; a chat with another business, nothing; the 51st of a day is Taskiem's), `TestChatLinkRepublishKeepsTheDraft`, `TestChatsWithOrdersOff`, `TestPushOnYou` (a personal device with no business and kind `you`; the business sees none; a reply pushed with its words, link and no SMS; preview off; an order update pushed; with no device the update is not texted and two replies text once; a member's You device survives losing their role), `TestMyStorefrontOrders` (waiting hidden, counted), `TestSharedNumberChatWords` (an outsider's "no"). `engine/phoneindex`: `TestBackfillReviewFixes` (an unopenable order skipped and counted, a business held by another replica left, an erasure during the fill not indexed, a failed business behind a fresh one), `TestOrdersOfAPerson`, `TestTheseAreMine`. `engine/chatlinks`: `TestTellAnswered` (the push's words and link; no SMS for automatic words or offers).

## As built: stage 2, review fixes (conversations)

Built on branch `stage2-fix-door`, on stage 2 as merged (530b4dbe): the reviewers' findings on the conversations and the customer door (§8, §13–§15), each checked again before it was fixed. A parallel stream (`stage2-fix-links`) fixes chat links, the phone index, billing, push and the shared number, in migrations 00457 and after; this one is migration `00456_customer_door_review_fixes.sql` alone (functions replaced, Down restoring the earlier bodies).

**Security and privacy.**
- **The way back cannot be chained.** `taskiem_customer_reach` checked only that the business was in tenant scope. A person's own transaction has the business in scope once `taskiem_customer_enter` lets it in, so reach could be called there for another person, and it set `app.person_scope`, opening that person's copy and their `users` row (`users.person_self`). Now reach refuses any transaction with a person scope, and any with `app.person_business` set by a door; it sets its own setting, `app.person_reach` (`taskiem_person_reached()`), which only the person's copy with that one business reads (`person_conversations`, `person_messages`, `person_files`: their own in their own scope, or the reached person's with the business that reached them). The `users` row stays keyed on the person's own scope. `taskiem_customer_typing` (the business's side) and `taskiem_customer_typing_of` (asked of a person) refuse a person's transaction too.
- **The business's copy of a file** keeps its name sealed under the customer's subject (`pii.SealText`, category `other`, as messages are); names kept before are read as they are. **Erasure** (`POST /v1/pii/erase`) deletes the objects of the erased subjects' app files from the business's storage and forgets their path and name (`{erased: true}` stays). **Retention**: `taskiem_customer_messages_purge()` now answers `(tenant_id, path)` for each deleted message's file, and the sweep deletes them (`Media.Delete`, a hundred at a time). The person's own copy is in their rows (`person_files`), deleted with the account; there is no "delete this chat" yet, so no other path.
- **The owner's two-hour notice** says "A customer has waited 2 hours for an answer", with no name (it may show on a locked phone).
- **App threads are a person's own.** The thread was found by the phone's subject, and `ON CONFLICT … DO UPDATE SET person_id` moved an existing thread to whoever now held the number. The unique key `(tenant, line, phone_number_id, subject_id)` is now a partial index for `line = 'whatsapp'` only; app threads are keyed by `(tenant, person)` (`customer_conversations_person`) and made with `ON CONFLICT DO NOTHING`, never re-pointed. A new holder of a number gets a thread of their own (with the same subject, so an erasure of that phone reaches both); the old holder keeps writing in theirs.
- **Typing while blocked** is refused either way (`taskiem_customer_typing`: hint `blocked` when the business blocked the person, 403 `blocked` to them; `stopped` when the person blocked the business, 409 `you_blocked` to them; 409 to the business). `typing_of` answers false while either block stands.

**Correctness.**
- **Waiting** is `customer_conversations.waiting_since`: set by a customer's message when unset, cleared by the business's own answer (not Taskiem's words, such as the closed reply or an order's news, and not an offer). Needs you (30 minutes) and the owner's notice (2 hours) count only the **open minutes** since it began (`openBetween`: the hours' openings as times, past midnight and overlapping ones merged), by the hours of the **published** page (`businessHoursTx` reads `storefront.PublishedModel`, never the draft; a business with no published hours counts the clock). `owner_told_at` is compared with `waiting_since`: told once per wait. Threads either side blocked are not waiting.
- **The owners' sweep** (`taskiem_customer_waiting`, now answering `(tenant_id, conversation_id, waiting_since)`) takes the oldest waits first, 500 at a time, skipping a thread until its `wait_check_at`: one not due yet (a closed business, or one not open long enough) is looked at again only once it may be (now plus what is missing; an hour when the business turned telling off). Open businesses are never crowded out by closed ones. Each thread is claimed by `UPDATE … WHERE owner_told_at IS NULL OR owner_told_at < waiting_since … RETURNING id`, so replicas running the sweep at once tell once.
- **Limits under a lock.** An offer holds its thread (`FOR UPDATE`) before counting, so offers sent at once never pass two a week; a person's new chat holds an advisory lock on the person; a message holds the person's conversation. A chat the business opened by writing first (`person_conversations.by_business`) is not counted against the person's new chats.
- **An erased customer:** writing to them is 409 "This customer's details were erased, so this conversation is closed." (the thread's phone reads empty, or sealing finds the subject erased); the person writing after it gets 409 "This business erased your details from its records, so this conversation is closed." The thread keeps its subject: a fresh one would collect again what the business was asked to erase.
- **The person's file** goes into the business's storage before the transaction (which holds the business's audit chain once the door is open), and is deleted when the message is not kept or is a repeat.
- **The closed reply** and the bell each run in a savepoint (`Server.savepoint`): a failure is logged and undone (the day's claim too), and the customer's message is kept.
- **Telling a customer of an order** (`ChatNotify`) finds the app thread by the order's person (`line = 'app' AND person_id`), never by the phone's subject; an order with no person is told in the WhatsApp thread of its number. `storefront.Order` gains `PersonID` (`in_app` to the business); a draft confirmed in an app thread makes a chat order with that person (`ChatOrder.PersonID`), listed on their side. Anything with a person, an app booking included, is told in the conversation (`queued: ["app"]`), not by SMS.
- **Stock** sums an item's quantities across the lines it is on. **A repeated Idempotency-Key** for an app order or booking waits for the first (an advisory lock on the key) and then finds it by its card, so the same booking sent twice at once answers the first booking rather than "That time was just taken".
- **Voice notes:** an Ogg whose length cannot be read is held to the voice note's byte cap (5 MB), as other voice notes are.
- **A business's block** no longer fails the person's mute or archive: `PUT /v1/me/chats/{id}` keeps them and refuses only the answer to offers (`offers_refused: true`), or answers 403 `blocked` when only that was asked.
- **Asked once** (§15): `person_conversations.offers_asked_at`, set when the question is answered or shown and passed over (`PUT {offers_asked: true}`); `ask_offers` is false from then on, and while either side blocked the other. **The business's record of the answer** (`customer_offer_consents`) outlives the account: its key is its own `id`, `(tenant_id, person_id)` is unique, and `person_id` is set null when the account is deleted.

**Built for the clients (§8.3, §15).**
- `GET /v1/me/chats/{id}` (one chat) gains `photo` (the business's logo as the chat link's card has it: its profile's, else its published page's, never one the moderators rejected) and `verified` `{phone, business, payments, months}`: a phone proved (`taskiem_tenant_phone_verified`), the business checked, a payment provider connected (an active live connection of paystack, flutterwave, moniepoint, interswitch, opay, opaybusiness or remita), and whole months on Taskiem (since the business was made). Read through the door with the hours line, in one audited entry (`read: header`).
- **`warn_transfer`** on a person's message from the business: it holds an account number (ten digits standing alone, together or in groups with spaces, dashes or dots, never part of a longer number such as a phone) and a banking word (account, acct, a/c, bank, or a bank's name: `bankWords` in `api/transfer_warning.go`). It cannot tell a personal account from a business's, and warns on both; cards never warn. The clients show "Pay only through Taskiem's pay links" with it.
- **Invoice and delivery cards:** `POST /v1/customer-chats/{id}/messages {card: {type: invoice, invoice_id}}` (one of this customer's sent invoices: its number, what is due, its pay page while something is due) and `{card: {type: delivery, reference}}` (the rider's progress on this customer's order once a rider holds it: `delivery {status, line, accepted_at, picked_up_at, arrived_at, delivered_at}`, nothing of the rider). A person's orders (`GET /v1/me/chats/{id}/orders`) carry `delivery` as it is now, so a card's snapshot can be followed.

**API changes for the web and the app** (OpenAPI updated, `mobile/lib/api/api.g.dart` regenerated):
- `PersonChat`: `photo`, `verified`, `offers_refused`; `ask_offers` false once asked.
- `PUT /v1/me/chats/{id}`: `offers_asked`.
- `PersonMessage`: `warn_transfer`.
- `CustomerCard`: the types `invoice` and `delivery`, with `invoice_id` and `delivery`; the business's card request takes `invoice_id`.
- `DoorOrder`: `delivery`; `DeliveryProgress` is new.
- `StorefrontOrder`: `in_app` (the web can offer **Write to them in the app** for bookings made in the app too).
- Errors: 409 for an erased customer on both sides; typing 403 or 409 while blocked.

**Tests** (`api/customer_door_review_test.go`): `TestCustomerDoorReachChainRefused` (a person's reach through the door, for another person and for themself, refused; typing as the business refused; no users row after a business's reach; a second reach narrows to that person), `TestCustomerDoorThreadsNeverRepointed`, `TestCustomerDoorWaitingSince` (three messages, an offer, the closed reply, closed minutes not counted, told once with no name, blocked not waiting), `TestOpenBetween`, `TestCustomerChatsSweepNotStarved` (600 waits at a closed business), `TestCustomerDoorConcurrentOffers` (eight at once, two sent), `TestCustomerDoorErasedCustomer` (the sealed file name, the files deleted, 409 on both sides), `TestCustomerDoorClosedReplyFailureKeepsMessage`, `TestCustomerDoorBlocksTypingAndOffersAsked`, `TestCustomerDoorHeaderAndTransferWarning`, `TestWarnTransfer`, `TestCustomerDoorDraftInAppThread`, `TestCustomerDoorDeliveryCard`, `TestCustomerDoorStockAcrossLines`. `TestCustomerDoorBookingAndLimits` gains a booking sent four times at once, the booking told in the chat, and chats the business opened not counted; `engine/media` gains `TestCheckChatFileOggLengthUnknown`.

**Left over.**
- Location messages (§8.3).
- "Delete this chat" on the person's side (and so deleting their own file copies before the account goes).
- A fresh subject for an erased customer who writes again (the conversation stays closed).
- `verified.payments` reads a payment provider's connection; a business paid only through Taskiem's own accounts shows false.
- The clients' use of the new fields (the header's photo and badges, the transfer warning, invoice and delivery cards, `offers_asked`).

## As built: stage 2, web review fixes

Built on branch `stage2-fix-web`, on 1308508b (stage 1, main, stage 2 and both server review fixes merged): the web's use of what the server's fixes added, and the review's findings on the stage 2 screens, each checked again before it was fixed. Main's recent web commits (to e3f19cc1) changed no screen near these (its redesign was the phone app's), so nothing of the look moved.

**The server's changes, used.**
- **Orders from shops** (`MyShopOrders`): only confirmed orders are listed; `waiting_count` is said as "We found N orders made with your number before it was on your account. Are they yours?" with **These are mine**, and nothing else of them (`waitingWords`). The card shows with only waiting orders too.
- **The conversation's header**: the business's photo (`photo`, at 320 px; kept for the tab, `sessionStorage`, so the list's row shows it once the chat was opened: the list itself carries none) and what it has proved under the hours, in words (`trustWords`: "Phone checked · Business checked · Payments through Taskiem · Trading 8 months", "New on Taskiem" under a month, years from 12 months).
- **The offers question, asked once**: Yes, No, and **Not now**, which passes it over (`PUT {offers_asked: true}`); leaving the chat with it shown and unanswered passes it over too. `offers_refused` on a PUT is said ("… so your answer about offers was not kept").
- **`warn_transfer`**: under such a business message, "Pay only through Taskiem's pay links. Taskiem never asks you to send money to a personal account." (`TRANSFER_WARNING`, a `role="note"` on the warning's tint); never under the person's own.
- **Invoice and delivery cards** on both sides (`AppCardView`): an invoice (its number, what is due, **Pay** to its page while something is due; on the business's side **Open** goes to the invoice) and a delivery (the rider's line, a pill, and four steps as a bar, `deliveryStep`). The business's card sheet gains **Delivery** (an order's reference) and **Invoice** (its number, found with `GET /v1/invoices/{number}`, sent as `invoice_id`). **Your orders** shows each order's `delivery` line and steps.
- **`in_app`**: **Write to them in the app** shows on any order or booking made in the app (`in_app`, or the kind `app`), with "booked" words for a booking.
- **Orders off**: Taking chats says "Your Orders area is off, so chats are closed…" with a link to the areas, and its switch is off and disabled (`orders_off`, `ORDERS_OFF_WORDS`).
- **An erased customer**: a 409 whose words say the details were erased closes the box on either side, saying so plainly in its place (`erasedWords`: the server's sentence, or ours for the WhatsApp thread's terse one). Typing is not posted while either side blocked the other, and a refused typing (403, 409) is ignored.
- **Push on You** (§9): under the person's conversation, where the browser can take a push, **Tell me when they answer** registers it as the person's own device with the existing web push flow (`enableYouPush`: permission, the worker, the subscription, then `PUT /v1/me/push?side=you` with the keys, the device's name and `kinds: ["you"]`; `?side=you` is always sent, which a personal session takes as it is); then "We'll tell you on this device when they answer." Where it cannot (an iPhone's browser not on the Home Screen, no worker or push, the server without keys): "We'll text you when they answer." Signing out also sends `DELETE /v1/me/push?side=you {endpoint}` before the browser forgets its subscription. The worker knows the kind `you`; a push with no `business` opens its link as it is (`/you/chats/{id}`), and You now follows the worker's word to an open window (`useWorkerGo`, shared with the business's app). A business's session opened at `/you/chats/{id}` (a window that was in a business) shows "A chat of your own" with **Open it on You**, which switches by the person's tap.

**Review findings, fixed.**
1. **Cards' orders**: read again whenever the cards name another order or a new order or delivery card comes (`cardsKey`), every 15 seconds while such cards are shown, and up to three times soon after a failed read; an order's card shows the order's state now and a delivery card the rider's progress now (`liveCard`), not the snapshot.
2. **Send an offer**: one `client_id` per offer, kept until it is sent or its words change.
3. **Polls and sends**: the last look's error is cleared by the next one that works; send errors are kept apart and shown first (both sides).
4. **The business's box**: a card or a file is no longer dropped while another message is on its way: each send has its own `client_id` and nothing waits on another (`act.busy` gates no send).
5. **A send whose answer never came** (no connection, or 5xx: `mayHaveArrived`): its words come back with their `client_id` kept, and the same words sent again reuse it (`clientFor`), on both sides.
6. **`PersonChatPage` is keyed by the chat's id**, so nothing of one chat shows in the next.
7. **Archived**: each list answer says which list it is; the toggle shows the skeleton until its own list is back.
8. **The business's clip** in an app thread accepts voice notes (`audio/*`).
9. **`payWords`** says "You pay when it reaches you." for `on_delivery`.
10. **When nobody answers**: turning a switch keeps closed-reply words typed and not saved; only saving the words ends their edit.
11. **The chat link's page** tells You from a business as `App.tsx` does (`personal || (tenant_id === null && !gate_only)`).
12. **Their shop**: changing pickup or delivery starts a new order key.
13. **`.chat-link-qr`** takes `--qr-ground` (white in both themes, for a camera).

**Tests.** `lib/personchat.test.ts` (waiting words, trust words, the warning, photos, erasure words, kept client ids, `on_delivery`, invoice and delivery cards and `liveCard`, `cardsKey`), `lib/customerlinks.test.ts` (orders off), `lib/push.test.ts` (a `you` push opens `/you/chats/{id}` with no business). `e2e/customer-chat.spec.ts`: on the real server, the header's "New on Taskiem" and a business's message with an account number warned on the person's side (and their other message not); with stand-ins at 390 and 360 px, the waiting count and nothing of those orders, the trust line, the warning, invoice and delivery cards (the delivery card following the order's rider), **Tell me when they answer** present, the offers question answered (390) or passed over (360) once and not asked again after a reload. `accessibility.spec.ts`'s person's chat adds the trust line, the warning and both new cards to axe in both themes.

**Left over.**
- The list of chats carries no photo: a row shows the business's photo only once its chat was opened in the tab.
- **Tell me when they answer** cannot be pressed through in the browser tests (the worker is blocked there); its request is the business flow's with `?side=you` and `kinds`.
- The business's Invoice card is found by its number with `invoice.read`; a member who handles orders without it cannot send one.

## As built: stage 2, app review fixes

Built on branch `stage2-fix-app`, on 1308508b (stage 1, main's redesigned app, stage 2 and both server review streams merged). App only (`mobile/`); no server or `mobile/api.txt` change: every call is in the generated client already.

**In the app's own look** (docs/spec/mobile-app-look.md, as main redid its own screens).
- **A person's conversation** (`lib/chat/my_chat.dart`): the head on the conversation's ground as the business chats' (Back, the business's photo or letters, the name in Inter Tight, the hours line or "typing…", ⋯); three dots in a bubble while they type; bubbles 20 px round, a run's last one with its corner tucked, the person's own white on pine (they were dark on pine before). The first chat's notice, the blocked lines and the closed line are cards over the box; "Get offers from {business}?" is a card with **Yes**, **No** and ✕.
- **Cards** (`lib/chat/door.dart`) stand free of the bubbles on the card's frame, as an order's does in a business chat: the kind and reference small in capitals with the state as a pill, the amount large ("₦4,500 due"), the line, and the button (pine; lime in the dark). Shared by both sides.
- **The tools** (`lib/chat/my_chat_tools.dart`): the tools, their shop's items, Your orders and Pay are rows on one card on the sheet's ground, each tool on its accent's square; the check step's parts are form groups.
- **You** (`lib/screens/you.dart`): the chats on one card (a business's photo where the list has it); **Your shop orders** counts only what waits for "These are mine" in amber (it counted every order), with **It's mine** / **These are mine** and "Nothing of them shows until you say so."; each order with its state as a pill.
- **A chat link** (`lib/screens/chat_link.dart`): the business's card on the top card (photo through `photo.urls`, open now as a pill, the week's hours), **Chat with {business}** as the card's main button; "This link no longer works" on its own card.
- **The Welcome screen's** links are the sign-in card's `AuthLink`s.
- **The business's side** (`lib/chat/customer.dart`, `customer_app.dart`): app threads draw the same bubbles and cards, typing as three dots; the notes over the box are tinted cards; **Send** and its card picker are rows on cards with accent squares.
- **Customer chats** (`lib/chat/chat_link_page.dart`, Settings > Customers > Customer chats): a settings page (`SettingsPage`): the link large on the top card with its QR code, the words to paste, **Share** and **Copy the link**, **Make a new link**; then **Chats in the app**, **When nobody answers** and **Send an offer** as settings cards.
- **Tools > Customers** in the chat's tools panel (`tools.dart`, for `storefront.manage` or `order.handle`; the web's tile): a sheet with Your chat link (Share, Copy, **QR code and settings** to Customer chats) and **Send an offer** (`POST /v1/customer-chats/offers`, one `client_id` for the same words; "Sent to 12 customers. 3 skipped: …").

**The API's new fields.**
- `PersonChat.photo` in the head and the list (`photoUrl`: the smallest size at least twice the face's width, web addresses only; the letters until it loads or if it cannot); `verified` as one plain line under the head with a shield: "Phone checked · Business checked · Payments through Taskiem · Trading 8 months" (only what is so; "New on Taskiem" under a month).
- `ask_offers`: the question shows only while it is true; ✕ sends `PUT {offers_asked: true}`. An answer the server did not take (`offers_refused`) is said: "{business} is not taking messages from you, so your answer about offers was not saved."
- `warn_transfer`: under such a message, a red card: "Pay only through Taskiem's pay links. Taskiem never asks you to send money to a personal account."
- Cards `invoice` (its number, what is due, **Pay** or **See the invoice** opening its page in the app's browser tab; states To pay, Part-paid, Overdue, Paid) and `delivery` (the rider's line and its four steps: Rider coming, Picked up, At your door, Delivered) on both sides; the business's **Send** gains **An invoice** (its sent invoices, this customer's first, drafts and void ones left out; `invoice_id`) and **A delivery** (an order's reference). `DoorOrder.delivery` shows on Your orders (the rider's line and step as the row's state).
- A booking offer's **Book** opens that service's times (`BookSheet.service`); a service not among those read in the last minute is read afresh.
- `StorefrontOrder.in_app`: **Write to them in the app** on the order's page (`POST /v1/customer-chats/app`), which opens the customer's thread (`Openers.customer`).
- Chat settings `orders_off`: "Your Orders area is off, so customers can't start chats with you…", and **Take chats** cannot be turned on.
- 409 for an erased customer: on the person's side the box goes and the server's words stay over it ("This business erased your details from its records, so this conversation is closed."); on the business's side an app thread with no phone says "This customer's details were erased, so this conversation is closed." with no box.
- Typing is never told while either side has blocked the other (or the details were erased); a 403 or 409 to it is ignored as before.

**Push on You** (`lib/app/notify.dart`, §9 decision B).
- **A personal session**: `PhoneNotifications` works on the person's side (keys `push-*:you`): **Notify me on this phone** registers `PUT /v1/me/push {kind: "fcm", token, platform, kinds: ["you"]}` (no `side`: the session is personal); You asks once ("Get a notification when a business answers you?"), and You's Settings has the switch and **What this phone tells you**.
- **A business session**: turning notifications on registers the phone with the business as before and also with `?side=you` (`kinds: ["you"]`), so a business answering the person's chat reaches them while they work in a business. **Chats with businesses** in Notifications on this phone turns that alone off (remembered: turning the business's on again does not bring it back) or on. Started again later, both are found by their token (`known`), a changed token replaces each (`replaces`), and one removed elsewhere (404) is forgotten.
- **Signing out** deletes the business's registration and You's (`DELETE /v1/me/push` with the token, `?side=you` from a business session) before the session ends, then makes the token worthless; every `push-*` key goes.
- **Opening one**: a push with no `business` and the link `/you/chats/{id}` opens that conversation over the chats in either session (`routeOf` → `RouteScreen.you`; a business session's `/v1/me/chats` is its person's); with the app open it is a toast with **Open**, and nothing while that conversation is on the screen (its tag `you:{id}`; `PushNote.business` is null for an empty one, so it is never taken for another business's).
- **Android** has a channel `you`, "Chats with businesses".
- **Notifications on this phone** lists the other devices of the side in use and, from a business, **Your devices for You** apart, each with Remove.

**Fixed in review.**
1. The person's own messages were dark words on pine in the light theme (`BubbleFrame` now takes the run's `last` and recolours your own words, as the business chats do).
2. A conversation read every 4 seconds could have two reads in flight, a slow one landing over a newer one (both sides): one read at a time.
3. A message whose answer never came (offline, a 5xx) was sent again with a new `client_id`, so the server kept two: the same words (or the same card) again reuse the id (both sides, Send an offer too). Ids gain a random part.
4. The business's thread swallowed an error that was neither the server's nor offline (`error = null`): now "Something went wrong. Try again."
5. A menu change that failed on the person's side left its error in the thread for good; it is said once as a toast (`actionError`).
6. Typing was told while blocked or erased (refused by the server each time).
7. Answers landing after a conversation closed were written into it (guarded).
8. A push with `business: ""` was taken for another business's and toasted over its own conversation.
9. Book's services were cached a minute per conversation, so a booking offer for a new service found nothing; the phone box's controller was not let go when the services were read again.
10. Shop orders' amber count counted every order rather than those waiting.
11. `test/notify_test.dart` failed on 1308508b (the push answers' new required `side`): its stand-in server keeps the business's devices and You's apart, by `side`.

**Tests** (`flutter test`): `test/my_chats_test.dart` +7 (the route of a push on You; the head's words and photo, the transfer warning, the question passed over; offers refused; invoice and delivery cards, Your orders following the rider, a booking offer opening its service; erased and blocked typing; a resend keeping its client id), `test/customer_app_test.dart` +4 (an invoice and a delivery from **Send**, the client id kept; an erased customer; the Customers tool and Send an offer; Orders off), `test/notify_test.dart` +3 (a personal session's device; a business session registering You too, turned off apart, found again, both taken off at sign out; a push for You opened and quiet on its own screen). No parity entries: the web has no pictures of these screens. Full run: 1095 passed, 6 skipped; `flutter analyze` clean.

**Left over.**
- The person's side still keeps nothing on the phone for offline (the business chats do); offline it says "Waiting for network…" and reads again.
- Location messages, "Delete this chat", saved addresses (§13) as before.
- Notifications on You come only once the server sends them (its `chatlinks.Tell`); the app's badge count on You is the server's.

## As built: stage 3, Orders and addresses

Built on branch `stage3-orders`, on 4aff5ccc (stages 1 and 2 merged): the server of §13's Orders on You and saved addresses, with §14's deletion. Discover (§10) is a parallel stream (migrations 00461 and 00462, and 00466 after review); this one is migration `00465_person_orders_and_addresses.sql` alone. The web and app screens are later streams; the API is written for them.

**Data (00465).**
- `person_addresses`: the person's own rows (`label` 1 to 40 letters, `line` 1 to 200, `area`, `landmark`, an optional `phone` in E.164, `is_default`, one default per person by a partial unique index). Policy `person_own`: `person_id = taskiem_person_scope()` **and no door open** (`taskiem_person_business()` is null), so once any door names a business in a transaction the saved list cannot be read there. `ON DELETE CASCADE` from `users`.
- `taskiem_person_orders(person, index key, business?, reference?, before_at?, before_ref?, limit)`: a person's orders across businesses, for the person in scope only. The orders that are theirs (`taskiem_person_order_ids`, not callable by the app): `storefront_orders.person_id` is the person, **or** the order is in the phone index under the hash of their verified phone, is not another person's app order, and is not waiting for "These are mine". Each once (a union of ids), newest first by `(made_at, reference)`, at most 101. Only these columns: `business_id, business` (the tenant's name), `photo` (the profile's logo unless rejected, else the published page's logo: a map of sizes to addresses), `reference, kind` (`booking` for a booking, else `order`), `state` (the order's status), `summary` ("2 × Jollof rice, 1 × Chapman", a dish's options in brackets, or a booking's service; up to 200 letters), `total_kobo` (with extras), `due_kobo`, `pay`, `made_at`. Enquiries, callbacks, quotes, trade orders, forgotten orders, and businesses that are not active businesses are left out. `pay` is true only while something is due, it is paid in the app (`payment` not `none`), the order is `awaiting_payment`, `accepted`, `received` (not when paid once accepted) or `paid`, the business has not blocked the person, and Orders is on.
- `taskiem_person_order_enter(person, index key, business, reference, action)`: **the order door**, narrower than `taskiem_customer_enter` and apart from it (so Discover's changes to that door do not meet these). Actions `customer.order_detail` and `customer.pay` only; the person in scope; no other door open in the transaction; an active business; one order of the person's (the same rule as the list); refused with `blocked` when the business blocked the person and `area_off` when Orders is off. It needs **no conversation** with the business (a storefront order made with the phone has none). Audited in the business's chain as `{actor_type: person}` with `{reference, door: customer, from: orders}` (never the key), then that business is in scope with `app.person_business` naming it.
- `taskiem_customer_phone_orders` and `…_waiting` (00458) now leave out an app order of another person made with the same number: an app order is its person's, so a number's later holder never sees it, even after "These are mine".
- `storefront_orders_person_any` (`person_id, created_at DESC`); `taskiem_dispatch` may read `storefront_orders.basket`, `extra_kobo`, `paid_kobo` (`dispatch_columns.txt`). Both functions are in `definers.tsv`.

**Endpoints** (in `api/openapi/openapi.yaml`; `mobile/api.txt` and `mobile/lib/api/api.g.dart` regenerated). A personal session reaches all of them (`personalRoutes`), and so does a business session for its person; an API key is refused (403).

| Endpoint | What |
| --- | --- |
| `GET /v1/me/orders?before=&limit=` | Orders on You: `{orders: [PersonOrder], next, waiting_count}`. `limit` 1 to 100 (30); `next` is the cursor for `before`, null on the last page; 400 for a cursor or limit that is not one. `waiting_count` is the orders waiting for "These are mine" (`POST /v1/me/storefront-orders/confirm`, as before): nothing else of them |
| `GET /v1/me/orders/{business}/{reference}` | One order (`PersonOrderDetail`): the row, `lines [{name, quantity, price_kobo, line_kobo}]`, `fulfilment`, `zone`, `delivery_kobo`, `paid_kobo`, `address {address, landmark?}` (the address on the order, for a delivery), `service_name`, `starts_at`, `ends_at`, `delivery` (the rider's progress, as `DoorOrder`'s). 404 not theirs, 403 `blocked`, 409 `area_off` |
| `GET /v1/me/orders/{business}/{reference}/pay` | `{reference, url, due_kobo}` through the order door (`customer.pay`); 409 with the reason when nothing is due, it is paid outside the app or once accepted, or the business has no page to pay on |
| `GET /v1/me/addresses` | `{addresses: [SavedAddress]}`, the default first |
| `POST /v1/me/addresses` | `{label, line, area?, landmark?, phone?, default?}` → 201 `SavedAddress`. The first is the default; `default: true` takes it. 400 for a missing name or street, too long, or `phone_invalid`; 409 past 20 |
| `PUT /v1/me/addresses/{id}` | The fields given change; `phone: ""` removes it; `default: true` makes it the default (`false` is ignored) |
| `DELETE /v1/me/addresses/{id}` | 204; deleting the default makes the newest left the default |
| `POST /v1/me/chats/{id}/orders` | Gains `address_id` (one of the person's saved addresses, 404 otherwise) and `landmark` |

`PersonOrder` is `{business_id, business, photo?: {urls}, reference, kind: order|booking, state, summary, total_kobo, due_kobo, pay, made_at, chat_id}`. **Chat about this**: `chat_id` is the person's conversation with that business when they have one (open `/you/chats/{chat_id}`, and put "About my order {reference}: " in the box, as stage 2 does); when null, `POST /v1/me/chats {business_id, from: "order"}` starts it (the door opens a business the person ordered from in the app, one with a published shop, one reached by its chat link, one listed in Discover with its Chat button, or, since Discover's 00462, one the person has a confirmed storefront order with, found through the phone index: so every order on the list can start its chat). **Pay**: when `pay`, call `…/{business_id}/{reference}/pay` and open `url`.

**Ordering with a saved address.** The address is read in the person's own scope before the customer door opens, then copied into the sealed order as the business's orders keep an address: `address` is the line and the area ("12 Herbert Macaulay Way, Yaba"), `landmark` is the landmark, followed by "Phone for delivery: +234…" when the address has a phone (the landmark is what the rider holding the order sees). The audit entry of `customer.order` says `saved_address: true`. The business sees only that copy; the label (Home) and the list never leave the person's side.

**Account deletion (§14).** There was no route to delete an account here; it came with the review fixes (`POST /v1/me/delete`, "As built: stage 3, Orders and account review fixes"), which also changed what deleting a `users` row does to the phone index (decision B: the account's orders leave it). As this stream left it: What the database does when the `users` row goes is now tested: the saved addresses go with it (cascade); the list goes with it (it is computed from `person_id` and the phone, both gone); the business's orders stay, `person_id` set null, with their sealed customer and address; the phone index's rows stay (the business's records, removed only by erasure, retention or the order's deletion).

**Decisions.**
- Detail and paying through a door of their own, not `taskiem_customer_enter`: that door needs a conversation with the business, and a phone order may have none; and Discover changes that door in its own migrations.
- The list runs no door per business (it would write one audit entry per business on every look); it is one narrow function answering the row's columns. A door, audited, is used for anything more of one order.
- Paging by a cursor (date and reference), not by page numbers, so new orders do not shift pages.
- A delivery phone has no field of its own on an order; it goes into the landmark, which the rider sees.
- Forgotten orders (retention) leave the list, as they leave the phone index.

**Tests.** `api/person_orders_test.go`: `TestOrdersOnYou` (an app order in the phone index listed once; a waiting phone order only counted, then listed after "These are mine"; another phone's order and another person's app order on the same number never listed; newest first; pages of two and one making the list; the row's fields exactly; `chat_id` with a chat and null without; Pay only for an order due and payable, its link through the door, 409 when nothing is due, audited; the detail with its lines, audited, also with no chat; another person reaches nothing; a reference under another business is no order; the functions refuse another person; through the door only that business's orders; a second door in one transaction refused; no Pay and the door shut while blocked; no Pay and `area_off` with Orders off) and `TestSavedAddresses` (add, the first default, a second made default, edits, a phone read and removed, a phone that is not one, missing and too long fields, delete the default, another person's 404s, 20 at most; ordering with a saved address copies line, area, landmark and phone into the business's order and the person's detail, another person's address 404; the business's scope and a person's transaction with a door open read no saved address; the account deleted: addresses gone, the order and its index row kept, `person_id` null). `engine/db`: `TestPersonOrdersMigrationUpDownUp`; `TestDefinersAllowList`, `TestDispatchColumnsAllowList`, `TestEveryTenantTableIsProtected` cover the new definers, columns and table.

**Left over.**
- The web's and the app's screens: the Orders tab on You (with "These are mine" from `waiting_count`), one order, Pay, Chat about this; saved addresses in Settings and in Their shop's check step.
- ~~"Chat about this" for a phone order with a business that has no published shop and no app order of the person's~~: Discover's 00462 made the door open for a confirmed storefront order found through the phone index, so it is possible for every order on the list (corrected in the review fixes).
- ~~Deleting an account (a route, telling each business "this customer left Taskiem")~~ (done in the review fixes); invoices on Orders (§8.4 lists them; not in this list); an erased customer's app orders (`person_id` still set) are still listed to the person; the business's photo is read as the chat link's card reads it, in SQL, so a change there must be made in both.

## As built: stage 3, Discover

Built on branch `stage3-discover`, on stages 1 and 2 as merged (4aff5ccc): the server of §10, with §14's "searches are not kept" and §15's reporting in Discover. A business's Orders across businesses and saved addresses (§13) are a parallel stream's (`stage3-orders`, migration 00465, and 00467 and after); this one's are 00461 and 00462, and 00466 after review ("As built: stage 3, Discover review fixes"). The web and app screens are later streams; the API is written for them.

**Data (`00461_discover.sql`).**
- `discover_settings`: the switch **Found by the public in the Taskiem app**, apart from Explore's (`link_settings.findable`). `listed` NULL is the default: on with a published storefront, off otherwise; a workflows workspace is never listed, whatever it says. The listing's own `category` (a key of Explore's categories), `area`, `hours` and place (`lat`, `lng`, kept to three decimals, about 100 m), each winning over the profile's and the page's. `told_at` (the one-off notice) and `removed_at`, `removed_by`, `removed_note` (Taskiem's moderators).
- `discover_index`: the search index, one row per business shown: name, line, category, area, address, map link, photo, up to 8 photos, hours, place, `words` (its name, line, category, area and the names and categories of its page's available items) with a `simple` tsvector and a GIN index, its page's address, and `can_chat`, `has_shop`, `has_book`. Written only from the business's own transaction; nothing else of a business is in it.
- `discover_dirty`: businesses whose listing may have changed, marked by a trigger (`taskiem_discover_touch`, run as `taskiem_dispatch` so a moderator's or a definer's change marks it too) on `storefronts` (status, published version and slug, catalogue sync), `storefront_domains`, `business_profile`, `business_gallery`, `storefront_media` (moderation), `customer_chat_link_settings`, `business_areas` and `tenants` (status, kind, name). Every business with a published storefront starts marked.
- `page_reports.kind` gains `discover` (`site_id` is the business).
- Definers (in `engine/db/definers.tsv`): `taskiem_discover_search(q, area, category, lat, lng, limit, offset)` answering only `business, name, category, area, photo, hours, can_chat, km`; `taskiem_discover_profile(business)` answering only the listing's columns; `taskiem_discover_report`; `taskiem_discover_dirty`, `taskiem_discover_notice_due` (ids only). `taskiem_discover_open(business)` (not granted to the application) says whether a listing may be shown: in the index, an active business, its page not suspended, and fewer than three different people with open reports in the last 30 days.

**The door (`00462_discover_door.sql`).** `taskiem_customer_enter`'s `customer.start` also opens a business **listed in Discover with its Chat button** (`discover_index.can_chat`) while its listing may be shown, so a listed business with no published shop, no chat link and no order of the person's can be chatted with. It also opens a business the person has a **confirmed storefront order** with, found through the phone index (asked for by the Orders stream, for "Chat with {business} about this order" where the business has no published shop): the API passes only the narrowed index key (`phoneindex.Index.DoorKey`, as `index_key`), the door calls `taskiem_customer_phone_orders` for the person and that business, which makes the hash from the person's own verified phone and leaves out orders waiting for "These are mine"; a wrong key finds nothing, and the key is left out of the audit entry. Otherwise 00457's body, unchanged.

**Engine (`engine/discover`).** `SettingsTx` reads the listing from the settings, the profile (name, line, area, categories, logo), the published storefront (name, line, area, address, map link, hours, logo, items and services), the gallery, the chat settings and the areas, and says what is missing; `RefreshTx` writes the row or deletes it; `SaveTx`, `RemoveTx`; `Search`, `ProfileOf` (through the definers), `HoursLine` ("Open now, closes at 18:00", "Closed now. Opens tomorrow at 09:00"). `Sweeper.Sweep` writes again the marked listings, each in its own scope, clearing a mark only if it is the one it read; `Sweeper.Tell` is the one-off notice. `Server.RunDiscover` runs both every minute (`cmd/taskiem/serve.go`).

**Endpoints** (in `api/openapi/openapi.yaml`; the app's in `mobile/api.txt`, regenerated):

| Endpoint | Who | What |
| --- | --- | --- |
| `POST /v1/discover/search` `{q?, area?, category?, near?: {lat, lng}, offset?}` | a person: a personal session, or a business session for its person; API keys 403 | `{results: [{business, name, photo (sizes by width), category, category_name, area, open_now, line, can_chat, km?}], more}`, 20 a page (`offset` up to 200). 400 for an unknown category or a place off the map; 429 past 30 a minute |
| `GET /v1/discover/categories` | a person | `{categories: [{key, name}]}`, Explore's thirteen |
| `GET /v1/discover/{business}` | a person | `{business, name, tagline?, category, category_name, area, address?, map_link?, photo, photos: [{urls, caption?}], hours, open_now, line, shop?, can_chat, has_shop, has_book, chat?}`; `chat` is the person's own conversation with it when they have one; 404 for a business not listed or hidden |
| `POST /v1/discover/{business}/report` `{reason: scam\|impersonation\|prohibited\|offensive\|other, note?}` | a person | 202 `{reported: true, words}`; 404; 429 (three a day per person per listing; twelve reports every two hours in all, shared with chat reports) |
| `GET /v1/settings/discover` | `storefront.manage` | `{listed, chosen (null: the default), default, workspace?, category?, area?, hours?, location?, missing: [category\|area\|photo\|hours], missing_line?, shown, hidden?: removed\|reported\|suspended, removed_note?, chat_off?: orders_off\|chats_off, listing}` |
| `PUT /v1/settings/discover` `{listed?, default?, category?, area?, hours?, clear_hours?, location?, clear_location?}` | `storefront.manage` | The same, after writing the listing to the index; 400 for an unknown category or bad hours; 409 for a workspace turning it on; audited `discover.settings` |
| `POST /v1/ops/discover/{tenant}/{remove,restore}` `{reason, step_up}` | Taskiem's operators (passkey step-up `ops.discover.remove`/`restore`) | Takes a listing out (a reason, told to the owners) or puts it back; open reports on it are acted on, or dismissed; audited in the business's chain and the platform's |

Chat from a profile is the existing `POST /v1/me/chats {business_id, from: "discover"}` (audited `customer.start` with `via: discover`). Their shop and Book are the chat's tools (stage 2), so the app opens or starts the chat, then the tool.

**How it behaves.**
- **The defaults (§10, §23 row 7).** On for a business with a published storefront until it turns it off; off for every other business until it turns it on; a workflows workspace is never listed (`workspace: true`, 409 on turning it on), and one made a business by turning an area on follows the rules of a business.
- **A listing needs a category, an area, a photo and hours.** Each comes from the listing's own setting, else the profile, else the published storefront (category from the profile's Explore categories; the photo is the profile's logo, else the page's, else the first gallery photo; a photo the moderators rejected is never used). A listing missing any is not in the index, and the settings say so (`missing`, and in words: "Customers can't find you in Discover until you add a category, an area and a photo.").
- **The search.** Every word typed must match the start of a word of the listing ("cakes" finds "cake"; "rice" finds a page selling "Jollof rice"); `area` matches part of the listing's area; a number that could be a phone finds nothing. With `near`, listings that gave a place come first, nearest first, with `km`; then names that match, then the best matches, then by name. The place is sent in a POST's body and used only as the function's arguments: nothing of a search is written, audited or logged (§14).
- **Fresh within a minute.** The business's own changes to its Discover settings are written at once; every other source (its page published or suspended, its profile, photos, chat settings, areas, being suspended) through the marks and the sweep. What may be shown is also checked live by the definers (an active business, its page not suspended, not reported by three people), so a suspension or reports hide it at once.
- **The one-off notice (§10).** A business listed by default (a published storefront, no choice of its own) is told once, the first time its listing is shown: `discover_settings.told_at` is claimed in its transaction, which records the owners' alert `discover_listed` (title "Your shop is in Discover", the words "Your shop is now listed in Taskiem's Discover, where customers find businesses. Turn it off here.", the link `<public URL>/settings/discover`): the bell, and each owner's chat on WhatsApp or Telegram through the outbox (kept in the web Taskiem conversation as it is sent). Audited `discover.told`. A sweep run twice, or on two replicas at once, tells no one twice; a business that chose either way is never told.
- **Reports (§10, §15).** A report is a page report of the kind `discover`, in the moderators' list (`GET /v1/ops/reports`), the reporter kept only as a keyed digest (HMAC with the chat links' key) to count different people. Three different people with open reports in 30 days hide the listing until a moderator looks (`hidden: reported`); dismissing the reports shows it again. A moderator's **remove** hides it until **restore**, whatever the business's switch says (`hidden: removed`, `removed_note`).

**Decisions.**
- **Orders off, or not taking chats: listed without the Chat button** (`can_chat: false`, `chat_off` says why), rather than not listed. The listing still shows the business's hours, address and page to the public; Their shop and Book go with it (`has_shop` and `has_book` need the storefront and Orders areas, as the door does). The door does not open from the listing then (404 to `POST /v1/me/chats`, as for any business it cannot open).
- **Signed in only.** §6, §7 and §10 put Discover on You; search, profiles and reports need a person's session. A signed-out Discover is left for later.
- **A POST for the search,** so the person's place is never in a URL, a history or an access log.
- **The category is Explore's list** (13 categories, the profile's first, or the listing's own); the area is free text; the place is the business's own pin, optional (nothing else in Taskiem has coordinates). Without pins, near-first orders nothing: area and words still find.
- **Reported listings hide at three people,** as pages are suspended at three (moderation.md); one person cannot take a business out of Discover.
- **Reading the settings writes the listing again** (`GET /v1/settings/discover` calls `RefreshTx`), so what the owner sees is what the public see.
- **Reporting in Discover sends no messages:** a listing has none; the conversation's report (stage 2) stays for that.

**Consequences to know.** A Discover report is a `page_reports` row, so, like a footer report, an open one takes the business out of Explore's sections until a moderator looks (Explore's rule since 00429). `tellOwners` gained `tellOwnersAt` to link elsewhere than `/storefront`.

**Tests.** `api/discover_test.go`: `TestDiscover` (the default on with a published storefront and what is missing; a business without one off until turned on, its own category, area and hours; a page turned off never found; a workspace never listed; by name, by an item and a plural, by area, by category, not by a phone; a result's fields; near first both ways with `km`, the place found nowhere in the database; a profile and its `chat`; Chat from Discover for a business with no shop or link, audited `via: discover`; turned off, not found and the door shut; a lost area hides it; Orders off listed without Chat, through the sweep; an API key refused), `TestDiscoverNarrowFunctions` (the definers' result columns exactly the listing's; from another business's scope the search finds only the listed business and the index reads no rows; an unlisted business's profile not found), `TestDiscoverReportsAndNotice` (the notice once over two sweeps, its words; a business that chose never told; a report in the moderators' list; three a day per person; one person cannot hide it, three can; hidden in the settings; a moderator's remove, the reports acted on, the owner's switch not bringing it back, restore; both audited). `TestCustomerDoorOpensForPhoneIndexOrders` (a business with no shop opened by a confirmed order found by the person's phone; not while it waits for "These are mine", not for another person, not for a made-up key; the key not audited). `engine/discover`: `TestMissingWords`, `TestHoursLine`, `TestCheckHours`.

**Left over.**
- The web and app screens: Discover on You (search, categories, near me with the phone's place asked for each search, results, a profile, Chat, Report), the business's Settings > Discover (the switch, what is missing, its own category, area, hours and pin), and the operator console's remove and restore.
- A signed-out Discover, and the business's verification on its profile (§15's badges; `GET /v1/me/chats/{id}` has them once chatting).
- Coordinates for areas (a place per area name), so near-first works for businesses that set no pin.
- Showing open reports' count to the business, and an appeal for a removed listing (a storefront's `APPEAL` covers pages only).

## As built: stage 3, app

Built on branch `stage3-app` (`mobile/`), on a4ef60a0 (stages 1 and 2, main's redesigned app, and both stage 3 server streams merged). No server change and no change to `mobile/api.txt`: every call was in the generated client. The web's screens for stage 3 were built at the same time, so the words are the spec's and the server's; where the web's differ once it lands, the app follows it (§6). Every new screen is in the app's own look (docs/spec/mobile-app-look.md): `TkScreen` heads, `TkSection` and `TkListRow` on cards, `TkTopCard`, `SettingsPage`, form groups on sheets.

**You's tabs (§7)** (`lib/screens/you.dart`): You is now the home's bottom bar (`HomeTabs`, as a business's home): **Chats** (as before: the chats and Archived, invitations, Start something, Your businesses; the unread count on its tab), **Orders** and **Discover**, each read when first opened. Settings stays the head's button. "Your shop orders" (stage 2's `GET /v1/me/storefront-orders` section) left the Chats tab: Orders shows the same orders and more.

**Orders (§13)** (`lib/chat/your_orders.dart`):
- `GET /v1/me/orders`, newest first, as rows on one card: the business's photo (`photo.urls`, else its letters), its name, "Booking · " for a booking, the summary and when, the total (or "₦4,500 due" with an amber dot while `pay`), and the state as a pill in the customer's words (`yourOrderStatus`). **Show older orders** reads the next page (`next` sent as `before`) until `next` is null. Pulled down, read again.
- `waiting_count` is a top card: "We found an order made with your number before it was on your account. Is it yours?" (or N orders), "Nothing of them shows until you say so.", **It's mine** / **These are mine** (`POST /v1/me/storefront-orders/confirm`), then the list is read again.
- **One order** (`GET /v1/me/orders/{business}/{reference}`): the top card with the kind and reference, the state, the business, a booking's day and time with its service and hours, the total, what is to pay and paid, **Pay ₦…** while `pay` (`GET …/pay`, its `url` in the app's browser tab) and **Chat about this**; the rider's line and its four steps (Rider coming, Picked up, At your door, Delivered, with their times); the lines with delivery and the total; how it comes (Delivered to you with the zone, or You pick it up) and the address on the order with its landmark.
- **Chat about this** opens `chat_id` when there is one, else starts it (`POST /v1/me/chats {business_id, from: "order"}`) with its notice; either way with "About my order {reference}: " in the box (`chatWithBusiness` in `lib/chat/my_chat.dart`, shared with Discover).

**Discover (§10)** (`lib/chat/discover.dart`), apart from Find a business (Explore):
- Under the head: **Search by name or what you want**, **Area**, and the categories as a switch (`GET /v1/discover/categories`, All first). A search (`POST /v1/discover/search {q?, area?, category?, offset?}`) runs at once on a category, 0.4 s after typing stops, and on pulling down; an answer to an older search is let go. 20 a page: **Show more** sends `offset` as the results given so far.
- Each result is a row: photo, name, "{category} · {area}" (and km when the server gives it), **Open now** in lime or **Closed**, and **Chat** while `can_chat` (`POST /v1/me/chats {business_id, from: "discover"}`, then the conversation). "No businesses found / Try other words, another area or another category." A 429 (or any refusal) is said in the server's words over the list ("You are searching too fast. Wait a moment and try again.").
- **A business's profile** (`GET /v1/discover/{business}`): its photo, name, tagline, category and area, open now and the hours line; **Chat**, **Their shop** and **Book** (shown while it takes chats or the person already has its chat): each opens the person's chat (`chat`) or starts it with `from: "discover"`, then Their shop or Book opens over the conversation (`MyChatScreen.tool`). Then its photos across (captions read out), the week's hours with the note, the address and **Open in maps** (`map_link`, in the browser), and **Report this business**. "{business} isn't taking chats in Taskiem right now." when it cannot be chatted with.
- **Report this business** (a sheet): why, in the web's words (Looks like a scam, Pretends to be someone else, Sells something not allowed, Offensive, Something else), a note up to 500 letters; `POST /v1/discover/{business}/report`; the server's thanks, or its 429, shown.

**Saved addresses (§13)** (`lib/chat/addresses.dart`):
- You's Settings gains **Ordering > Saved addresses** (a `SettingsPage`): the list, the default first with "Default", **Add an address** (gone at 20, with "You can keep up to 20 addresses. Delete one you no longer use to add another."); a row opens it to change.
- The sheet: **Name** (Home, Work), **Street and number**, **Area**, **Landmark**, **Phone for delivery**, and **Make this my default** (not shown on the default); **Delete** asks first. `phone_invalid` is said under the phone; a 409 past 20 and the other refusals in the server's words. A change sends `phone: ""` to remove a phone and `default: true` only when it is turned on.
- **Their shop's check step** (`lib/chat/my_chat_tools.dart`): for a delivery, the saved addresses as choices (the default picked) and **Another address**; a saved one goes as `address_id`, a typed one as `address` and `landmark`. **Save this address** with **Name it** saves the typed one (`POST /v1/me/addresses`) once the order has gone; if it cannot be saved, the order's words say why. The order's Idempotency-Key follows the address chosen.

**The business's side** (`lib/chat/discover_settings.dart`): Settings > Customers > **Discover** (for `storefront.manage`), a `SettingsPage`:
- The listing as customers see it on the top card (photo, name, tagline, category and area, Chat / Their shop / Book / photos as pills, Shown / Not shown / Off), then **Found by the public in the Taskiem app** as a switch (`PUT {listed}`), and **Use the default** while a choice is set (`PUT {default: true}`). A workflows workspace says it is never listed, and cannot turn it on.
- What is missing (`missing_line`), why it is hidden (removed by the moderators with their note, reported, the page suspended) and why it shows without Chat (`chat_off`: Orders off, or not taking chats), each on a card.
- **Your listing**: **Category** (Explore's thirteen, `PUT {category}`), **Opening hours** (a sheet, a day each: open or closed, opens and closes as HH:MM; a day left as it was keeps all its openings; `PUT {hours}`), **Use your shop's hours** (`PUT {clear_hours: true}`), and **Area** with Save (`PUT {area}`, empty for the profile's or the page's). Without `storefront.manage` it is shown, not changed.

**Tests** (`flutter test`): `test/you_stage3_test.dart` (11: You's three tabs, each read when first opened; Orders with "These are mine", Pay, older pages, one order with the rider and the address, Chat about this opening the chat it has; Chat about this starting one from a booking; Discover's categories, search, more, 429 and Chat from a row; a profile's photos, hours, map, Report and Their shop over a started chat; saved addresses with `phone_invalid`, default and delete; no Add at 20; the check step with a saved address and a typed one saved; a business's Settings > Discover; Orders and Discover pressable and at large text; the listing's words). `test/my_chats_test.dart` loses its shop-orders test (Orders replaces the section) and counts the unread on the Chats tab too. Full run: 1064 passed, 6 skipped; `flutter analyze` clean. No parity entries: the web has no pictures of these screens. Full run: 1095 passed, 6 skipped; `flutter analyze` clean.

**Left over.**
- **Near me** (§10): the app has no package for the phone's place, so none was added; a search sends no `near`. It needs a location package (asked once, on the tap) and the server's `km` is already drawn.
- The business's pin on the map (`location`, `clear_location`) in Settings > Discover.
- App links: the web had no `/you/orders` or `/you/discover/{business}` routes on this base, and the server's `appPaths` has no `/you/*`; once they exist, `appPageOf` and `web_links.txt` should map them to Orders and a profile.
- A signed-out Discover, and the person's side kept on the phone for offline (as before).

## As built: stage 3, web

Built on branch `stage3-web`, on a4ef60a0 (stages 1 and 2, and both stage 3 server streams merged): the web screens of §7's tabs, §10 (Discover, the business's setting and the operators' remove and restore) and §13 (Orders on You and saved addresses). No server change.

**You's tabs** (`pages/You.tsx`): **Chats** (`/you`), **Orders** (`/you/orders`), **Discover** (`/you/discover`) and **Settings** (`/you/settings`), one line that scrolls inside itself on a phone. Orders on You takes the place of stage 2's "Your orders from shops" card on Chats (`MyShopOrders` is gone; its "These are mine" moved to Orders). The chat list's empty state points to Discover.

**Discover** (`pages/Discover.tsx`, words in `lib/discover.ts`): a sibling of Explore in its look (Explore's search pill and chips, `tk-x`), apart from it in everything else.
- **The search**: one box for a name or what the person wants, an **Area** field and the categories as chips (`GET /v1/discover/categories`); the typing settles for 350 ms, then `POST /v1/discover/search` with only what was given (`searchBody`). **Near me** asks the browser for its place only when tapped, sends it as `near` in the body (never in an address), keeps it only in the page's state for that page, and lets it go when tapped again; refused or slow, it says so and the search goes on without it (`placeProblem`). Results are rows (`Row`): the photo (the size nearest 160 px, `photoAt`), the name (a link to the profile), "Open now" or "Closed now", the category, area and how far ("700 m away", `kmWords`), the hours line, **Chat** when `can_chat`, and **See more**. **Show more businesses** asks the next page by `offset`. A 429 is said as "You are searching too fast. Wait a moment and try again." (`discoverProblem`); nothing found says what was searched (`nothingFound`).
- **A business's profile** (`/you/discover/:business`): its logo or letter, name, tagline, category and area, open now and the hours line; **Chat**, **Their shop** and **Book** (the last two only with `has_shop`/`has_book`), or "{business} is not taking chats in Taskiem right now." without `can_chat`; its photos in a row that scrolls; its opening hours (Lagos time); its address with **Open in your map** (`map_link`) and **Visit their page** (`shop`); and **Report {business}**. 404 says "This business is not in Discover any more."
- **Chat, Their shop, Book** (`chatPath`): the profile's `chat` opens `/you/chats/{chat}` (with the tool in the history's state); without one, `/you/chats/new?business=&from=discover[&tool=shop|book]`, whose `StartChat` posts `{business_id, from: "discover"}` and opens the chat with the tool's sheet open (`PersonChatPage` takes `tool` from the history's state). A 404 there: "This business can't be reached by chat in Taskiem right now."
- **Report** (a sheet): the five reasons in words, an optional note (500), "Taskiem's moderators look at every report. {business} is not told who sent it."; the server's `words` once sent; a 429 said as "You have sent a lot of reports. Please wait a while before you send another."

**Orders on You** (`pages/YouOrders.tsx`, words in `lib/youorders.ts`).
- **The list** (`/you/orders`): `GET /v1/me/orders?limit=30`, then **Show older orders** with `before={next}` until `next` is null (pages joined without a row twice, `addPage`). `waiting_count` is the notice with **These are mine** (`POST /v1/me/storefront-orders/confirm`, then the list again). Each order is a row: the business's photo (or the icon), its name (a link to the order), the total, the summary, reference and time, the state in a customer's words (`stateWords`) and what is due ("To pay", "₦500 still to pay", `dueWords`); **Pay** when `pay` (`GET …/{business}/{reference}/pay`, then **Open the pay page** as a link the person opens: a tab opened after a request would be blocked), **Chat about this** (`chat_id`: the chat, with "Hello, about my order {reference}: " in its box when it holds no draft; null: `/you/chats/new?business=&ref=`, which starts it with `from: "order"`), and **See the order**. A 403 or `area_off` on Pay is said plainly (`payProblem`).
- **One order** (`/you/orders/:business/:reference`): the pine hero (still to pay, or the total; the state as its tag; the business and when), **Pay** and **Chat about this**, the rider's progress (the line and four steps), a booking's service and times (Lagos time), the lines as a table (with delivery and the total), and how it comes with the address as it was copied onto the order. 404: "This order is not one of yours, or it is no longer kept."

**Saved addresses.**
- **In You's settings** (`SavedAddresses`): rows with the name, the street and area, "Default · Near {landmark} · Rider calls {phone}" (`addressSub`); **Add an address** (hidden at 20), **Change**, **Make default** and **Delete** (asks first, in the app's sheet: "Orders you placed with it keep their copy"). The form (a `Modal`): Name it, Street and number, Area, Landmark, Phone for the rider, Make this my default address; a change sends `phone: ""` to take a phone away (`addressBody`); 409 and `phone_invalid` are said plainly (`addressProblem`).
- **At checkout** (Their shop's check step, `PersonTools.tsx`): with delivery, the saved addresses as choices ("Deliver to", the default chosen) and **Another address**; a saved one sends `address_id` (and `address: ""`); another is typed, with an optional **Landmark** (sent as `landmark`) and **Save this address** (named Home unless changed), which saves it first (`POST /v1/me/addresses` with the zone as its area) and orders with its `address_id`; saved once per address typed, so a retry does not save it again. Addresses that cannot be read leave the typed address as before.

**Settings > Discover** (`pages/DiscoverSettings.tsx`, `/settings/discover`, a card on Settings for `storefront.manage`; the owners' one-off notice links here).
- **Your listing**: where it stands as a badge and a line (`standing`: Listed, Not shown yet, Hidden, Off), the switch **Found by the public in the Taskiem app** (turned at once, put back if refused), under it the default or the choice (`listedWords`: "On by default, because your shop's page is published.", "Off, as you chose. Without your choice it would be on…"), **Go back to the default** (`default: true`) once chosen; a workspace's switch is off and says it is never listed. Then what is missing (`missing_line`, else ours), why it is hidden (`hiddenWords`: Taskiem's `removed_note`, reports, a suspended page) and why it has no Chat button (`chatOffWords`, with a link to the areas or to Customer chats).
- **What customers search by**: the category (Discover's list, "From my profile" for none; Explore's names if the list cannot be read) and the area, each saying what is used now when left empty.
- **Opening hours**: the page's hours as lines, or the listing's own; **Set hours for Discover** opens the storefront's hours editor (`HoursEditor`, now exported from `Storefront.tsx`), **Use my page's hours** sends `clear_hours`.
- **Your place on the map**: **Use where I am now** (the browser's place, kept to three decimals), **See your place on the map**, **Remove the place** (`clear_location`), and the place typed under a fold.
- **How customers see you**: the listing as a row (photo, name, category and area, tagline, Chat, Their shop, Book), with "Customers don't see this yet." while it is not shown.

**The operator console** (`ops/Trust.tsx`): a report of the kind `discover` in Reports says "Discover listing" and offers **Remove or restore** in place of Suspend or restore: a reason (needed to remove; told to the owners), **Remove from Discover with passkey** (asks first) and **Restore with passkey**, each through the step-up (`ops.discover.remove`/`restore`, target the business); the panel stays open saying what was done.

**Tests.**
- Unit: `lib/discover.test.ts` (the search's body, photo sizes, distances, lines, counts, refusals, the chat's path, the place's rounding, the setting's words), `lib/youorders.test.ts` (pages, paths, a row's line, what is due, addresses' lines and bodies, refusals). `plainwords.test.ts` adds `Discover.tsx`, `DiscoverSettings.tsx` and `YouOrders.tsx`.
- `e2e/discover-orders.spec.ts`, on the real server: a business signed up with its phone and business checked, a published shop (pickup and delivery to Yaba), its profile's category, area and tagline and a logo; listed by default (`/settings/discover`: Listed, the default's words, the preview with Chat); a person signed in by phone at 390 px finds it by name (the search's body checked, no place in any address) and by category; its profile (tagline, address, hours, Their shop); Chat from Discover (`from: "discover"`); Their shop from the profile with the shop open, an address typed with a landmark and **Save this address** (the order sent with `address_id` and no address); the order on Orders on You with Chat about this; its page with the address and landmark as copied onto the order; Chat about this with the words in the box; at 360 px the saved address in settings, another added (a bad phone said plainly, then the new default), both offered at checkout with the default chosen; the business turns Discover off (not found, "Off, as you chose.") and back to the default (found again). With stand-ins at 390 and 360 px: Orders on You (These are mine, Pay and its link, older orders by `before`, an order with lines, address and the rider, a booking's times, Chat about this starting a chat with `from: "order"`), and Discover at 360 px (Near me sending the place in the body only and letting it go, Show more by `offset`, a 429, a business not taking chats, a report). axe in both themes and nothing wider than the page throughout.
- `e2e/accessibility.spec.ts`: `/settings/discover` in the sweep; You's orders, an order, Discover, a profile, the report sheet, settings with an address and the address form at 390 and 360 px in both themes; the You test stands in for the addresses. `e2e/customer-chat.spec.ts`: the shop orders on `/you` are now checked on Orders (These are mine, only confirmed orders, Chat about this); the shop stands in for no saved addresses. `e2e/ops.spec.ts`: a Discover report's listing removed with a reason and the passkey, then restored. `e2e/you.spec.ts` and `e2e/parity.spec.ts` pass unchanged (no shared screen's picture changed).

**Deviations and left over.**
- The test server has no data tables (no Xenibase), so in `discover-orders.spec.ts` the shop's items are written as a sync would leave them (`storefront_catalogue`, with no table behind them, and the published page marked as having a catalogue) and the logo as a media row pointing at the app's own file. With no table to check prices against, the order comes in as "New prices to confirm", so **Pay** on a real order is covered only with stand-ins.
- The business's photo in Orders on You comes from `photo`; the chat list still has none (stage 2's left over).
- A signed-out Discover, a business's verification on its profile (§15's badges) and showing open reports' count to the business are the server's left overs too and have no screens.
- Deleting an account (§14) is not on You's settings: the server had no route yet (it has since: `POST /v1/me/delete`, "As built: stage 3, Orders and account review fixes").
- The phone app's screens for all of this are a later stream.

## As built: stage 3, Orders and account review fixes

Built on branch `stage3-fix-orders`, on 4058c0a6 (stage 3's server, app and web merged): the review's findings on Orders on You, each checked again before it was fixed, two founder's decisions (A and B below), and what §7 and §14 still asked of the server for the person's account. Migration `00467_person_orders_review_fixes.sql` alone; Discover's review stream owns 00466 (see "For merging").

**Founder's decisions.**
- **A. Earlier orders on a reissued number.** A storefront order found only through the phone index and made **before the phone was verified on this account** (`created_at < user_phones.verified_at`, not the person's own app order) keeps the short view only, even after "These are mine": the business, reference, state, total and date. Its row has `earlier: true`, an empty `summary`, `due_kobo` 0 and `pay` false. `GET /v1/me/orders/{business}/{reference}` answers that row alone (`lines: []`, no fulfilment, address, landmark or rider) and opens no door (nothing more is read from the order, nothing is audited); `…/pay` is 409 `{earlier: true}`; the order door itself refuses it (hint `earlier`). By default it is never paid here: **Chat about this** is the way to ask the business about it (the door opens for a confirmed phone order since Discover's 00462). Orders made with the number since it was verified, and the person's own app orders, are shown in full as before.
- **B. Deleting an account removes its orders from the phone index.** The bug: `storefront_orders.person_id` is `ON DELETE SET NULL`, and 00465's "an app order is its person's" was tested on `person_id`, so once an account's row was deleted its app orders (address and all) looked like anyone's, and the number's next holder got them through "These are mine". Now:
  - `storefront_orders.person_left` marks an order of a deleted account; the phone path of every function (`taskiem_person_order_ids`, `taskiem_customer_phone_orders`, `…_waiting`) takes an order only if its `person_id` is the person, or is null **and the order is not marked**. The index's fill (`phoneindex.Backfill`) never indexes a marked order again.
  - A `BEFORE DELETE ON users` trigger (`taskiem_users_before_delete`, for any deletion of the row) runs `taskiem_person_orders_release`: the account's app orders leave the index and are marked, before the foreign key clears `person_id`.
  - The deletion route (below) does the same, and also removes **every index row under the account's phone**, so storefront orders made with the number (theirs, or still waiting from before them) are not found by its next holder at all.

**Findings fixed.**
1. **One rule for Pay.** `storefront.PayableNow(status, payment, left)` and its SQL twin `taskiem_order_payable_now`, pinned by `TestPayableNowMatchesSQL` over every state, way of paying and amount: something left, paid in the app (`payment` not `none`), a state of `storefront.PayableStatuses` (`awaiting_payment`, `accepted`, `received`, `paid`, `dispatched`, `on_the_way`, `arrived`), and not `received` while paid once accepted. Orders on You's `pay` uses the SQL (with the business's block and Orders area, as before); `payLinkTx`, behind both `GET /v1/me/orders/{b}/{ref}/pay` and the chat's `GET /v1/me/chats/{id}/orders/{ref}/pay`, uses the Go: 409 "This order is closed." for a cancelled, declined, expired, completed, no-show, refunded or refund-due order; "This order waits for you to confirm a new price…" for `price_changed`; the earlier refusals (nothing due, paid outside the app, paid once accepted) otherwise. A completed hire with a late fee is therefore not payable in the app (Chat about this).
2. **`waiting_count` counts what "These are mine" then shows**: not forgotten, not an enquiry, callback, quote or trade order, an active business (not a workflows workspace), and not another person's or a deleted account's app order.
3. **Bookings of every kind.** `kind` is `booking` for `booking`, `hire`, `space`, `class` and `tickets`, with `booking_type` (`service`, `hire`, `space`, `class`, `tickets`; absent for orders) on the row and the detail. The summary names what was booked: a service ("Haircut"), a hire ("2 × Canopy, 3 days": items a day, then days), a space ("Hall A"), tickets ("3 tickets, Afrobeats Night"), a class ("3 places, Pastry class"); the lines when there is no name.
4. **Looks are limited per person.** One order's detail and its pay link each write an entry in the business's audit chain: together, a burst of 30, then one every 2 seconds per person (`person-order:`), 429 "Too many tries. Wait a moment and try again." past it.
5. **The business's photo** on Orders leaves out a published page's logo whose picture the moderators rejected after it was published (the snapshot's `card.logo` media must exist and not be `rejected`), as the profile's logo already was. Implemented locally in `taskiem_person_orders`; see "For merging".

**Deleting an account (§14).**
- **The proof.** `POST /v1/me/delete/start {via?}` sends a code to the account's phone for this alone (`phone_codes.purpose` gains `delete`; the limits and lock of every phone code). `POST /v1/me/delete/passkey/options` is a passkey challenge scoped `account.delete`. `POST /v1/me/delete {confirm: true, code | credential | password}` needs one of them (400 without `confirm`; 403 `{reauth: [phone_code, passkey, password]}` without a proof; 401 `code_wrong`, `password_wrong` or the passkey's 401). As for stage 1's factor changes, a session made by a code on a device with no second proof, of a person who could give one, gives that proof first (401 `second_proof_required` with `ways`, `refuseUnproved`). Ten tries a minute per person.
- **Who cannot yet.** A person who owns a business: 409 `{owns_business: true, businesses: [names]}`, "Close it, or make someone else its owner and ask them to remove you". Nothing closes a business today and there is no "hand over" step: an owner adds another owner (`POST /v1/members`), who then removes them (`DELETE /v1/members/{user}/roles/owner`). A person with any other role in a business: 409 `{in_business: true, businesses}`, "Ask its owner to remove you" (the business's records of what they did name their account, and §5.4's second proof guards accounts in a business). `taskiem_person_delete` checks the memberships again, so a role taken meanwhile still refuses.
- **What it does, in one transaction** (`deletePerson`): for each business the person has an app chat with, a line in its thread, "This customer left Taskiem." (sender `system:left`, shown as "Taskiem", a plain string, not sealed: it is not the customer's words), the thread no longer waiting, and `customer.left` (actor the person) in that business's chain; then `taskiem_person_delete(person, index key)` (a definer, for the person in scope only, with no door open): the index rows under their phone and for their app orders, their app orders marked and no longer naming them, the businesses' threads no longer naming them (`person_id` null, `person_left_at`), every session revoked, and their `user_phones`, `user_devices`, `second_proof_links`, `phone_codes`, passkeys, WhatsApp and Telegram bindings and chat PIN deleted; then their own side (chats, messages, files, saved addresses, devices and pushes on You, photo, push choices); then the row emptied: no name, `deleted+…@deleted.taskiem.invalid`, no password, `status = disabled`, `users.deleted_at`; and `account.delete` (`how`, `businesses_told`, `index_rows`, `ip`) in the platform's chain. The answer is `200 {deleted: true, businesses_told}`, with the session cookie cleared.
- **What the businesses keep.** Their threads (with the line), their orders with the sealed customer and address, invoices, the answers to offers: their records, under their retention, erased only through their own process (§14). The thread then says `notice: "This customer left Taskiem"`, `stopped: true`, and a message to it is 409 "This customer left Taskiem." (stage 2's answer for a thread with no person).
- **Decision: the row is emptied, not deleted.** About forty tables reference `users` without a cascade (sessions, staff and approval records, and more); deleting the row would fail on any of them. The emptied row names no one and signs no one in (`taskiem_auth_session` needs an active user), and keeps the businesses' keys whole. A row deleted by hand still releases its orders (decision B's trigger).

**You's settings (§7).**
- **Photo.** `PUT /v1/me/photo` (multipart `file`): a JPEG, PNG or WebP up to 10 MB, checked and cleaned by `media.CheckProfilePhoto` (decoded within the pixel limit, turned upright, re-encoded at most 800 pixels wide, so no metadata; not scanned for malware, since only its re-encoded pixels are kept), stored in `person_photos` (the person's own rows, policy `person_own`, no door open), answering `{photo: {url, updated_at}}`; `GET /v1/me/photo` the image (`private` cache), 404 without one; `DELETE /v1/me/photo`. `GET /v1/me` gains `user.photo` (`{url, updated_at}` or null). **Decision (§14): businesses are not shown it.** Only the person's own side shows it; a business sees the name and phone the order or chat carries, as before. Sharing it with businesses would be a later choice of the person's.
- **Pushes on You.** `GET`/`PUT /v1/me/notifications {messages?, updates?, offers?}` (`person_notify`; all on with no row): `messages` (someone at a business wrote), `updates` (its automatic words: an order's or a booking's news), `offers`. `chatlinks.PersonPush` gains `Kind` (`chatlinks.KindOf(answer)`); the person's pusher answers "handled" for a kind turned off, so neither a push nor the SMS fallback goes; the message is in the chat.
- **Privacy.** `GET /v1/me/privacy`: `shown_to_businesses {name, phone, photo: false}`; `businesses [{business_id, business, chat_id, orders, last_order_at}]`, the businesses that have the person's number: those they chat with (their own chats) and those with orders on their Orders on You (`taskiem_person_businesses`, a definer answering only the business, a count and the latest date; waiting orders not counted); and `erase {words, how: "ask_in_chat"}`: today's process is to ask each business in its chat (each keeps its own records under the NDPA; Report in the chat when it does not answer), and deleting the account does not erase the businesses' records.

**Links.** `appPaths` (`api/applinks.go`) gains `/you`, `/you/chats/*`, `/you/orders`, `/you/orders/*`, `/you/discover` and `/you/discover/*` (You's settings stay on the web), so a push's `/you/chats/{id}` and links to Orders and Discover open the app where it is installed. The app's `appPageOf` (`mobile/lib/app/applinks.dart`) does not map them yet (a client change).

**API for the clients** (OpenAPI updated; `mobile/api.txt` gains `deleteAccountStart`, `deleteAccountPasskeyOptions`, `deleteAccount`, `deleteMyPhoto`, `myNotifications`, `putMyNotifications` and `myPrivacy`, and `mobile/lib/api/api.g.dart` is regenerated; the photo's upload and image are the app's own calls, as a chat's files are):
- `PersonOrder`, `PersonOrderDetail`: `booking_type`, `earlier`; `summary` empty and `due_kobo` 0 for an earlier order.
- Pay (both pay routes): 409 "This order is closed.", 409 `earlier`, 429.
- Detail: 429; an earlier order's short row.
- `Me.user.photo`; the new routes above, each reachable from a personal session (`personalRoutes`) and from a business session for its person; API keys 403.

**For merging.**
- **Discover's 00466** adds one SQL function for a business's public photo that drops moderator-rejected logos. Once both are merged, `taskiem_person_orders`' two photo subqueries (00467) should call it, so the rule is in one place; the check here is the same (the profile's logo unless rejected, else the published page's logo unless its media is rejected).
- 00467 follows 00466 by number; a database that ran 00467 before 00466 existed needs goose's out-of-order run for 00466 (00467 touches none of Discover's objects).
- The chat's header photo (`GET /v1/me/chats/{id}`, `chatlinks.CardTx`) still takes the published page's logo without the moderation check; Discover's shared function is the place to fix it.

**Tests.** `api/person_orders_review_test.go`: `TestOrdersPayRuleClosedOrders` (Pay off, and both pay links 409 "This order is closed.", for cancelled, declined, expired, refund_due and completed; price_changed said), `TestOrdersEarlierOnTheNumber` (the short row after "These are mine", its detail with no door and no audit, its pay 409 `earlier`, the door's refusal, an order made since in full), `TestOrdersWaitingCountMatchesTheList` (an enquiry, a quote and a trade order not counted), `TestOrdersBookingKinds`, `TestOrdersLookLimited`, `TestOrdersPhotoLeavesOutARejectedLogo`. `api/person_account_test.go`: `TestDeleteAccount` (an owner refused; confirm and a proof needed; a wrong code; the code; the session ended; the row emptied and the person's side gone; the business's thread with its line and notice, taking no more messages; its order kept; `customer.left` and `account.delete` audited; no index row left; the number's next holder finds nothing, even after "These are mine", nor in stage 2's list), `TestDeletedUsersRowReleasesItsOrders` (the founder's bug: a row deleted by hand, its order marked, and even put back in the index never the next holder's), `TestYouSettings` (the photo: a text file refused, one put, read back, on `/v1/me`, another person's 404, removed; pushes: the defaults, offers and updates off, an offer turned off answered handled; the privacy page: name, phone, no photo, a chat's business and an order's business with its count, how to erase, another person's empty). `TestSavedAddresses` now expects the index's row gone with the account. `api/applinks_test.go` checks the `/you/*` paths. `engine/storefront`: `TestPayableNowMatchesSQL`. `engine/media`: `TestCheckProfilePhoto`. `engine/db`: `TestPersonOrdersReviewFixesMigrationUpDownUp`; the definers, dispatch columns and table checks cover 00467.

**Left over.**
- The web's and the app's screens: Delete account in You's settings (the proof, the refusals with the businesses named), the photo, Notifications, Privacy; an earlier order's short page (`earlier`) with only Chat about this; bookings by `booking_type`; the app's `appPageOf` for `/you/*`.
- Closing a business, and a "hand over" step for an owner (today: add another owner, who removes you).
- Showing the person's photo to businesses, if it is ever wanted (now never).
- Invoices on Orders; an erased customer's app orders still listed to the person (as before).

## As built: stage 3, Discover review fixes

Built on branch `stage3-fix-discover`, on stage 3 as merged (4058c0a6): the review's findings on Discover's server, in migration `00466_discover_review.sql` (00467 and after are the Orders and accounts stream's). It supersedes "As built: stage 3, Discover" where they differ.

**One writer at a time.** Every writer of a business's listing (`RefreshTx`, `SaveTx`, `RemoveTx`, the sweep, the one-off notice) takes the advisory lock `discover:`||tenant before it reads the settings and holds it until it commits, so the sweep can no longer write back, from an older read, a listing its owner just turned off or a moderator took out. A change to `discover_settings` marks the sweep too (`discover_touch`). The sweep claims each mark with `FOR UPDATE SKIP LOCKED` and takes the business's lock without waiting, so two replicas never write the same listing and a business being changed waits for the next minute; a business whose listing fails to write goes to the back of the queue (its mark's time moves on).

**What may be shown is in the index.** `discover_index.hidden` (`off`, `removed`, `suspended`, `reported`, NULL when shown) and `reporters` are set by the database on every write of the row and at once when its settings, its reports, its page or its business change (triggers on `discover_settings`, `page_reports`, `storefronts`, `tenants`); a counting report leaving the 30 days is worked out again by the sweep (`taskiem_discover_recount`). The search filters on that one column and uses the index's own indexes (by name, by category, the area's words, a GiST index on the place for near-first), so a search over thousands of listings takes about a millisecond. `taskiem_discover_open` (the profile, the door, the notice) also asks the settings directly: listed (not off) and not removed.

**Reports (the founder's rule).** A report counts only when the reporter's account was at least 7 days old when they made it (`page_reports.seasoned`), and not when two or more of that person's earlier Discover reports were dismissed and none was acted on. Three counting reports from different people in 30 days put the listing first in the moderators' queue (`GET /v1/ops/reports`); they hide it (`hidden: reported`) only when one of them is `scam`. Reports are still taken on a listing hidden for them. Each person may send twelve Discover reports in two hours and three a day per listing, counted in the database (`taskiem_discover_report` answers `taken`, `listing` or `many`); the server's own limit, shared with chat reports, stays in front. Discover reports no longer take a business out of Explore's sections or Featured (00429 and 00431's rules leave out the kind `discover`); a report on a page still does.

**The moderators.** The reports' own buttons (`POST /v1/ops/reports/{id}/{dismiss,action}`) answer 409 for a Discover report and name `POST /v1/ops/discover/{tenant}/remove` (acts on its reports) and `/restore` (dismisses them), so a listing hidden for reports is never shown again by a report's button. Restoring a listing that was not taken out only dismisses its reports and tells the owners nothing.

**The business's role.** `taskiem_app` may write only the business's own fields of `discover_settings` (`listed`, `category`, `area`, `hours`, `lat`, `lng`) and only the listing's columns of `discover_index`; `removed_*` goes through `taskiem_discover_moderate` (an operators' transaction, the platform's chain in scope, widened to the business) and `told_at` through `taskiem_discover_claim_notice`.

**The one-off notice (§10).** Only to a listing that is shown, and only to a business whose storefront was published when Discover shipped: `discover_launch.shipped_at` records that moment (when 00466 ran). A business that publishes later is listed by default and not told. The businesses due are taken those not tried first, then the longest since they were tried (`discover_index.notice_tried_at`, set when telling one fails).

**Sources.** `storefront_catalogue` marks the sweep once per statement (its refresh, a stock change, its rows emptied).

**Photos.** `taskiem_business_photo(business)` is a business's public photo: its profile's logo, else its published page's logo resolved through `storefront_media` (the snapshot's card names the media), never one the moderators rejected; NULL for none. It runs as its caller (the business's own scope, or a definer), for Discover and for a person's Orders.

**Search words.** A word ending in "es" after s, x, z, ch or sh is also tried without it ("dresses" finds "dress", "buses" finds "bus"), one ending in s without the s ("cakes" finds "cake"), and the word as typed always. "shop" and "buy" are searched like any word; words typed that are all left out ("the", "near me") find nothing. The area matches the start of a word of the listing's area ("ikeja" finds "Ikeja GRA"; part of a word no longer does).

**Hours.** The line follows yesterday's opening that runs past midnight (Friday 20:00 to 02:00 seen on Saturday at 01:00: "Open now, closes at 02:00"), and looks a full week ahead: a business open only on Saturdays, seen on Saturday evening, "Opens next Saturday at 10:00".

**For the clients.**
- `POST /v1/discover/search`: `offset` from 0 to 200, else 400; `more` is true only while the next page is within 200. Results keep `line`.
- `GET /v1/discover/{business}` and its report: 404 with "This business is not in Discover any more.", to show as it comes.
- `POST /v1/discover/{business}/report`: 429 "You have sent many reports. Try again later." past twelve in two hours (now from the database too).
- The operators' console: a Discover report is decided with remove or restore, not with the report's dismiss or action (409).

**Tests.** `engine/discover`: `TestRefreshWaitsForTheOwner` (two connections: the sweep has read "on", the owner's "off" waits for it, then takes the listing out, and the next sweep leaves it out), `TestOpenAsksTheSettings`, `TestTheBusinessCannotRemoveOrRestore` (42501 for `removed_*`, `told_at`, the index's state and the moderators' definer from a business's scope), `TestSweepOnTwoReplicas` (twelve marks, two sweeps at once, each listing written once), `TestSweepBacksOffAFailure`, `TestBusinessPhotoLeavesOutRejected`, `TestSearchAtScale` (2,000 listings, a tenth turned off: every kind of search under 150 ms, none hidden found, the filter a plain column, nearest first, paging), `TestHoursLine` (overnight, a week ahead). `api`: `TestDiscover` (plurals, stop-words, paging, the 404 words), `TestDiscoverReportsAndNotice` (the founder's rule end to end, the queue's order, the refused buttons, Explore untouched, the database's per-person limit; the notice only when shown, not to one that chose, not to one published after Discover shipped), `TestDiscoverSweepSources` (the catalogue marks the sweep; the notice's order).

**Left over.** The review's other items are unchanged: a signed-out Discover, verification on a profile, coordinates for areas, showing reports to the business and an appeal for a removed listing. Near-first finds its candidates by straight-line distance on the map's degrees and orders them by km; at Nigeria's latitudes the two agree but at a page's edge.

## As built: stage 3, the review fixes together

- **One rule for a business's public photo:** `taskiem_business_photo` (00466). The chat link card and the chat header (`chatlinks.CardTx`) now use it too, so no screen shows a logo the moderators rejected. Orders on You keeps its own copy of the same check in `taskiem_person_orders` (00467), which matches it.
- **Migration order:** 00466 (Discover) and 00467 (Orders and accounts) were written side by side and redefine no function in common. A database that ran 00467 before 00466 needs an out-of-order run of 00466.

## As built: stage 3, app review fixes

Built on branch `stage3-fix-app` (`mobile/`), on 08374720 (stages 1 to 3 with every server review fix). The review's findings on the app, each checked again before it was fixed, and You's settings for the person's own account. No server change and no change to `mobile/api.txt`: every call was in the generated client. Words are the web's (`lib/discover.ts`, `lib/youorders.ts`, `lib/personchat.ts`) or the server's; where the web has no screen yet (an earlier order, You's settings), they are the spec's and the server's. Every screen is in the app's own look (docs/spec/mobile-app-look.md).

**Discover** (`lib/chat/discover.dart`).
- **Show more** let its "Loading…" go only after the answer, and an answer to a search older than the one shown returned before that, so it stayed on Loading for good: it is let go in a `finally` (by a newer search too).
- Show more pages through the search the results are of (`_shown`: its words, area and category), not the fields' text since, so pages of two searches never mix; a failed search clears the results and `more`. A page past `offset` 200 is not asked for (the server's `more` is false there).
- The web's words: **Open now** / **Closed now**, the hours `line` under each row's category, area and distance ("700 m away", `kmWords`), **Show more businesses**, and nothing found saying what was searched (`nothingFound`: "No business found for “jollof” Food and drinks in Yaba. Try other words, or another area. Only businesses that chose to be found are listed."). A profile that is gone (404) is its own card, "Not in Discover" with "This business is not in Discover any more."; one not taking chats says "{business} is not taking chats in Taskiem right now. You can still visit their page." with **Visit their page** (`shop`, in the browser tab), which a chattable one has under **Where they are**; **Open in your map**, **Opening hours**, **Report {business}**; 429 on a search or a report and 403 in the web's words (`discoverProblem`).

**One at a time.** `chatWithBusiness` (`lib/chat/my_chat.dart`) opens one chat at a time for each You (its `OpenMyChat`): a second tap on a row's Chat, a profile's buttons, Chat about this or Ask to erase while one is being started or is open does nothing, so no second chat is started and none is opened twice. `payMyOrder` asks one pay link per order at a time, so a double tap opens one tab.

**Orders on You** (`lib/chat/your_orders.dart`).
- Back from an order, only its row is read again (`GET …/{business}/{reference}`) in its place: the older pages and the scrolling stay. The tab reads itself again each time it is shown (`HomeTabs.onShow`), and on pulling down, keeping the pages read (the first page's rows take their places, new ones come on top). A failure to read again is a toast; the rows shown stay.
- Pay: the order is read again when the person comes back to the app from the pay page (the app's lifecycle). Its refusals are said plainly: the server's "This order is closed.", an earlier order's and 429's words, and the web's for 403 and `area_off` (`payProblem`).
- **An earlier order** (`earlier: true`): its row says "Made before this number was yours · {when}", with no Pay; its page is the row alone (nothing more is read), says only this much of it shows and it is not paid here, and has **Chat about this** alone.
- **Bookings by kind** (`booking_type`): a row's line starts "Booking · ", "Hire · ", "Space · ", "Class · " or "Tickets · ", the page's title likewise; the summary names what was booked.
- Chat about this starts "Hello, about my order {reference}: " (the web's `aboutOrderDraft`). An order's page can open from its business and reference alone (`YourOrderPage.of`, for links).

**Their shop's check step** (`lib/chat/my_chat_tools.dart`). Saved addresses that come after the person chose one (or Another address) or typed one no longer replace the choice with the default. **Save this address** sends the zone delivered to as its area, names it Home unless named ("Name it", hint "Home, unless you name it"), keeps the typed address to 200 letters while ticked ("To save it, keep the address to 200 letters." past it), and is not offered at 20 addresses.

**Settings > Discover** (`lib/chat/discover_settings.dart`).
- **Opening hours** shows and edits every opening of a day (a break at midday makes two: **Add an opening on {day}**, up to four, and ✕ to take one away), where it showed only the first and a change dropped the rest.
- A change no longer resets an area typed and not saved; refusals are said where they were made (the area's under its field, the rest as a toast); a failure to read the page stays at its top.
- **Category** can go back to **From my profile** (`category: ""`); the row says "From my profile: {category}" then.
- Where the listing stands is the web's `standing` (`discoverStanding`: Listed, Not shown yet, Hidden, Off, with its line), in place of the unused `discoverLine`. The page is only for `storefront.manage`, as the web lists it (and the server reads it for no one else), so its read-only branch is gone.

**App links.** `appPageOf` (`lib/app/applinks.dart`) takes the server's `/you`, `/you/chats`, `/you/chats/{id}`, `/you/orders`, `/you/orders/{business}/{reference}`, `/you/discover` and `/you/discover/{business}`; `/you/chats/new` keeps only its `business`, `tool` (shop, book) and `ref`. `youRouteOf` (`lib/app/route.dart`) reads them. On You (`openYouPage`, `lib/screens/you_links.dart`), what is open closes, then the tab and the screen over it: a chat, Orders, an order's page, Discover, a profile, or a chat started with a business (from an order, with its words, when `ref` is given; else from Discover, with the tool). In a business's session (`RouteScreen.youPage`) the same screens are pushed over the business. The web's chat links to none of them, so `mobile/web_links.txt` is unchanged.

**You's settings (§7, §14)** (`lib/screens/you_account.dart`, `YouSettingsScreen` in `lib/screens/you.dart`).
- **The photo** on the top card: **Add a photo** / **Change photo** (the phone's photos, `PUT /v1/me/photo` as a multipart upload through `Transport.upload`, which now takes PUT; 413 said as too large) and **Remove** (asks first; `DELETE`); shown from `user.photo.url` through the signed image call. "Only you see your photo. Businesses never do."
- **Notifications** (a row to its page): this phone's switch and "What this phone tells you" as before (they were on Settings itself), then **What businesses may tell you**: Messages, Updates and Offers (`GET`/`PUT /v1/me/notifications`), each turned at once and put back with a toast if refused, "These are for every phone you use Taskiem on. What is turned off still waits for you in the chat."
- **Privacy** (`GET /v1/me/privacy`): what businesses see (name, phone, and the photo "Never. Only you see it."); the businesses that have the number, with their orders and the last one's date; **Ask to erase** opens that business's chat (the person's, else one started from an order) with "Hello, please erase the details you keep about me: " in the box; and the server's words on erasing.
- **Delete your account** (a red row under Sign out): what is deleted, that each business chatted with is told "This customer left Taskiem.", what businesses keep and that it cannot be undone. The proof: **Send a code to {phone}** (`POST /v1/me/delete/start`, WhatsApp, or SMS from there), **Use my passkey** (`POST /v1/me/delete/passkey/options`, the phone's passkey) or **Use my password**; asked once more, then `POST /v1/me/delete {confirm: true, code | credential | password}`. A wrong code or password and 429 in the server's words; 401 `second_proof_required` opens the second proof (its `ways`), then deletes; 409 `owns_business` / `in_business` (at the code or the deletion) takes the proof's place with the businesses named ("You own Mama’s Kitchen and Bola Stores on Taskiem. Close it, or make someone else its owner and ask them to remove you, before you delete your account."). Deleted, the phone forgets everything (`Auth.forgetDeleted`: the session, the person's settings, this phone's notification keys and those shown, the chats kept); the server has already ended every session and dropped the pushes.

**Tests** (`flutter test`). `test/you_review_fixes_test.dart` (22): Show more pages the search shown, not the words typed since; a late page let go and Show more working again; a failed page said and pressed again; nothing found saying what was searched; a row's Chat pressed twice starting one chat; a profile gone from Discover and one not taking chats with Visit their page; the words; back from an order reading only its row with the older page kept; Orders read again when shown and a failure said as a toast; Pay pressed twice opening one page and the order read again on coming back; Pay refused for a closed order; an earlier order's short row and page with only Chat about this, and a hire's line; Chat about this pressed twice; the check step's saved address (Home, the zone as area), one too long to save, no Save at 20, a late list leaving the typed address; the hours sheet's openings kept, added and taken away; the area kept across a change, refusals by the field and as a toast, From my profile; links to You opening an order, a profile, Discover and a chat started from an order; an order page from a link; where a listing stands. `test/you_settings_test.dart` (7): the photo added and removed (multipart PUT); notifications turned and put back; privacy with Ask to erase on a chat had and one started; deleting with a code (a wrong one first, then signed out); an owner told with the businesses named; the second proof before deleting by password; Settings pressable. `test/applinks_test.dart` (You's links, `youRouteOf`, `routeOf`), `test/home_open_test.dart` (a business session opening the person's order and Discover from links). `test/you_stage3_test.dart` follows the words (Closed now, Show more businesses, Open in your map, Report {business}, Hello, about my order) and a failed search clearing the results. The tests' stand-in server can now hold an answer back (a route may answer with a Future). No parity entries: the web has no pictures of these screens. Full run: 1095 passed, 6 skipped; `flutter analyze` clean.

**Left over.**
- **Near me** (§10) is still left out: the app has no package for the phone's place. It needs one (asked once, on the tap); the server's `km` is drawn.
- The business's place on the map in Settings > Discover (`location`, `clear_location`).
- The words of an earlier order's page and of You's settings are the spec's until the web's screens land (the web stream builds them now); then the app follows the web's.
- Discover's report reasons keep the app's words ("Looks like a scam" …), not the web's newer ones; a later pass should align both.

## As built: stage 3, web review fixes

Built on branch `stage3-fix-web`, on 08374720 (stages 1 to 3 with the server's review fixes): the review's findings on the web, each checked again before it was fixed, and the screens for what the server's review fixes added (earlier orders, bookings by kind, You's settings and deleting an account). No server change. It supersedes "As built: stage 3, web" where they differ.

**Discover** (`pages/Discover.tsx`, `lib/discover.ts`).
- **A category tapped while the typing settles is kept.** The 350 ms timer read the search as it was when it was set, so it put the old category back; now it reads what is typed when it fires, and tapping a category cancels it and searches at once with what is typed.
- **Back returns to the search.** `q`, `area` and `category` live in the page's address (`?q=&area=&category=`, `searchToParams`/`searchFromParams`); the person's place (Near me) never does, and stays in the page's state only. The results, the pages opened and where the list was scrolled are kept in the tab's memory for five minutes (`lib/pagecache.ts`, eight searches at most; a search with the place is not kept), so Back from a profile shows them without searching again. A profile's way back links to the last search.
- **Try again.** A refused or failed search says so with **Try again** (a count in the search's key asks again). Show more's own refusal is said under the list, its button becomes Try again, and the words go once a page comes.
- **Paging.** The next page's `offset` is the rows the pages held (`received`), not the rows shown after dropping a business found twice; Show more is offered only while the server says `more` and the next offset is within 200 (`canShowMore`); a 400 past it is said as "That is as far as this search goes…".
- **The server's words.** A profile's 404 and a report's 429 show the server's sentence as it comes ("This business is not in Discover any more.", "You have sent many reports. Try again later.", "You have reported this business several times today…"), ours only when it sent none.
- **One row.** `DiscoverRow` draws a result (photo, name, Open now or Closed now, category, area and distance, the hours line, Chat, See more); Settings > Discover's preview is the same row (`previewResult`, its buttons inert), its hours line worked out as the server's `HoursLine` does (`hoursLine`: Lagos time, an opening past midnight, a week ahead), with the tagline, Their shop and Book said under it.

**Orders on You** (`pages/YouOrders.tsx`, `lib/youorders.ts`).
- **Earlier orders** (`earlier: true`): the row says "Made before this number was yours · {reference} · {when}" with the state and total, no Pay, no link to a page, only **Chat about this**, and a line under the list says why. Its page, if opened, is the row alone (the hero with the total and the state) with Chat about this.
- **Bookings of every kind** by `booking_type`: the page's title (Booking, Hire, Space booking, Class booking, Tickets, `orderTitle`), the icon (tickets a tag), the server's summary ("2 × Canopy, 3 days") where a service shows its name, and the end with its own day when it is another ("Fri 20 Nov, 10:00 to Mon 23 Nov, 10:00", `bookingWhen`).
- **Refusals said plainly** (`payProblem`, `orderProblem`): Pay's 409 shows the server's sentence ("This order is closed.", a new price to confirm, nothing due), 409 `earlier` says it can't be paid here and to chat, 429 on Pay and on an order's page "Too many tries. Wait a moment and try again.", 403 and `area_off` as before.
- **Back keeps the pages.** The pages opened and the scroll are kept (`PageCache`); coming back, the newest page is read again and freshens the kept rows (`freshen`: the same orders replaced, new ones first). Show older orders' error is its own, with Try again.

**Checkout's saved address** (`pages/PersonTools.tsx`). With **Save this address** ticked the address box takes 200 letters (a saved address's most; a longer one is said and the order waits, `keepProblem`); Save is not offered at 20 addresses; a new name keeps the address already saved (a retry does not save it twice) while another zone forgets it (the zone is its area, sent as `area`). An address typed before the saved ones load stays chosen over the default that loads after (`chosenAddress`).

**A chat's tool** (`pages/PersonChats.tsx`). Their shop or Book asked for on the way in leaves the history's state once opened (`withoutTool`), so a reload does not open it again.

**The operators' console** (`ops/Trust.tsx`, `ops/Agents.tsx`). Suspend a page, remove a listing and retire an agent ask before anything runs: a "no" does nothing and says nothing (it used to say "Listing removed"). A Discover report offers only **Remove or restore** (its Dismiss and Mark acted on answer 409); once decided, what was done is said above the list, the panel closes and the list is read again, so the report leaves the open ones.

**You's settings** (`pages/YouAccount.tsx`, words in `lib/youaccount.ts`).
- **Photo** (Your account): the photo or the letter, **Add a photo** / **Change photo** (a JPEG, PNG or WebP up to 10 MB, checked before it is sent, `photoFileProblem`; `PUT /v1/me/photo` as a multipart form, `putForm`), **Remove photo**; "Only you see it. Businesses you chat with or order from never see your photo: they see your name and phone number." The server's refusal said as it comes (`photoProblem`). `Me.user.photo` is read for it.
- **Notifications**: three switches, Messages, Order and booking news, Offers (`GET`/`PUT /v1/me/notifications`, each sent alone, turned at once and put back if refused), and that a kind turned off is sent neither as a notification nor by SMS, the message still in the chat.
- **Privacy and your account**: rows to **Privacy** and **Delete account**.
- **Privacy** (`/you/settings/privacy`, `GET /v1/me/privacy`): what a business sees (the name, the phone; "Not your photo: only you see it."; the address given with an order), the businesses that have the number as rows ("You chat with them · 3 orders, the latest 2 Nov 2026", `privacyLine`) with **Ask in the chat** (their chat) or **Start a chat**, and how to ask one to erase (the server's words, `ask_in_chat`: ask it in its chat, Report when it does not answer; deleting the account does not erase what businesses keep).
- **Delete your account** (`/you/settings/delete`): what happens (chats, addresses, photo and settings gone; signed out everywhere; each business told only "This customer left Taskiem"; businesses keep their records, with a link to Privacy to ask first; it can't be undone), then the proof the account has (`deleteProofs`): a code to its phone (`POST /v1/me/delete/start`, WhatsApp or SMS, resent after a minute), its passkey (`POST /v1/me/delete/passkey/options`, asked when Delete is pressed) or its password; "I understand my account is deleted for good"; **Delete my account** (`POST /v1/me/delete {confirm: true, code | credential | password}`) and **Keep my account**. A 401 `second_proof_required` opens the second proof (`ProofStep` with the server's `ways`), then "Done: this device is proved. Now delete your account." A 409 `owns_business` or `in_business` replaces the form with the businesses named and the steps (make someone else its owner in Members and ask them to remove you; or ask its owner to remove you), and **I've done this, try again**. A wrong code or password, a passkey refused, 429 and the rest are said beside the button (no toast over the second proof's sheet).
- **Deleted**: the tab forgets what it kept of the person (`auth.gone`: drafts, the badge, Explore's views; the server ended every session already) and loads `/account-deleted?told={n}` afresh, a signed-out page (`AccountDeleted`, through `alone()`, shown signed in or out) that says what went and how many businesses were told (`deletedWords`); only the count travels in the address.
- **Sessions.** A 401 with `password_wrong`, or a passkey refused on `POST /v1/me/delete`, is the proof refused, not the session ending (`keepsSession`).

**Tests.**
- Unit (1,147 in 134 files): `lib/discover.test.ts` (the server's words, the search in the address, paging from the rows received and the cap, the hours line overnight and a week ahead, the preview's row), `lib/pagecache.test.ts`, `lib/youorders.test.ts` (earlier rows, bookings' titles, icons and times, pay and page refusals, freshening kept pages, keeping a typed address and the default), `lib/personchat.test.ts` (`withoutTool`), `lib/youaccount.test.ts` (photo checks and refusals, the privacy lines, the proofs, the delete body, the businesses in the way, refused proofs, the deleted page's count and words), `apiAuth.test.ts` (a wrong password or passkey while deleting keeps the session; an ended one does not). `plainwords.test.ts` adds `YouAccount.tsx`.
- `e2e/discover-orders.spec.ts`: on the real server, Back from a profile to the category's results without searching again; and a new test of You's settings: a photo added (multipart), shown, a text file refused, removed (the photo 404 after); offers turned off and kept; the privacy page with the business, "You chat with them" and its chat; the account deleted with a code to WhatsApp (a wrong one first), the deleted page, `/v1/me` 401, and the business's thread saying "This customer left Taskiem.". With stand-ins: Show more from the rows received (a duplicate dropped, offset 40 after 39 shown), a refused page and Try again, on to 200 and no further; a category tapped while typing searched with the words and kept in the address; Try again after a 429; a profile gone in the server's words; Orders with an earlier order (its row and its page), a hire (its summary, Pay "This order is closed.", its page 429 then its days) and Back from an order keeping the older pages; deleting refused for a business owned, a wrong password, a new device proving itself first, then deleted (390 and 360 px).
- `e2e/accessibility.spec.ts`: You's settings with a photo and notifications, Privacy, Delete account and its refusal, the deleted page, and an earlier order, in both themes at 390 and 360 px. `e2e/ops.spec.ts`: a "no" removes nothing and says nothing; a Discover report has no Dismiss or Mark acted on, leaves the open list once removed, and is restored from Acted on. `e2e/customer-chat.spec.ts`, `e2e/you.spec.ts` and `e2e/parity.spec.ts` pass unchanged.

**Left over.**
- No e2e drives checkout's Save at 20 addresses, the 200-letter cap or the late-loading default (unit-tested in `keepProblem` and `chosenAddress`), nor a reload after a tool opened (`withoutTool`).
- The privacy page's **Start a chat** for a business with orders and no chat starts it as from its shop (`from: "shop"`); the server has no `from: "privacy"`.
- The phone app's screens for all of this, and its `appPageOf` for `/you/*`, are a later stream.
- The passkey's refusal on `POST /v1/me/delete` is told apart from an ended session by its words; the server could add a flag (`passkey_wrong`) as it does for codes and passwords.

## As built: stage 4, server

Built on branch `stage4-server`, on 2d28a962 (stages 1 to 3 merged and reviewed): the server of §11 and §5.5 item 4, with the chat link's choice. Migrations `00468_one_customer_two_lines.sql` and `00469_one_customer_indexes.sql` (its indexes, built concurrently). The web and app screens are later streams; the API is written for them.

**Who is one customer.** Inside one business, a person in the app whose phone is verified on their account (`user_phones`) and a customer thread on the business's own WhatsApp number that WhatsApp has delivered a message from. A WhatsApp thread is only ever made by a delivered message (`customerThreadTx`), and matching starts from the person's verified phone, never from what a thread holds: a phone typed on a form (a saved address's delivery phone, an order's), and the number an app thread was made with for a person with no verified phone, match nothing. The business's own key makes the subject of the person's phone (`SubjectForTx`, no key made) and finds the thread by it (`customer_conversations_whatsapp_subject`); nothing is looked up across businesses. An erased customer never matches (their phone's subject is a new generation, and an app thread whose subject or number is erased is never joined).

**How the link is kept.** On the WhatsApp thread: `customer_conversations.linked_to` names the app thread (`linked_at`; one WhatsApp thread per app thread, a unique index; only a WhatsApp thread may carry it). The app thread's id stands for the pair; either line's id opens it. Joining takes a per-business advisory lock (`customer-link:`), so two joins at once never pair a thread twice.

**When it is made.**
- When the person starts a chat (`POST /v1/me/chats`, a new chat) or writes in one (`POST /v1/me/chats/{id}/messages`, `…/files`), in their own transaction through the door: `joinAppTx` with `taskiem_customer_link_phone` (the person asks only for themself).
- When WhatsApp delivers a message from a number that is the verified phone of a person who chats with the business in the app (`taskiem_customer_link_person`, never from a person's transaction), in the business's transaction: `joinWhatsAppTx`, through the way back.
- Once for the pairs there were before (and each app thread made since): the sweep (`joinSweep`, `taskiem_customer_link_due`, 200 a minute) runs with the owners' sweep (`CustomerChatsTick`), each business in its own transaction, and marks each app thread looked at (`link_checked_at`).
- Every join is audited in the business's chain as `customer.link` (`linked: true`, the two threads), as the person when they made it, else as `system:lines` through the way back; the target is the person.

**When it is undone** (`customer.link`, `linked: false`, with `why`):
- **The phone changes** (`POST /v1/me/phone/verify`, after the change, §5.5 item 4): in each business the person chats with, the WhatsApp thread stays under the old number, is parted and says "This customer changed their number" (`number_changed_at`, `notice`; cleared when it is joined again); the app thread takes the new number (its `who.number` sealed again) and is matched again only by a WhatsApp message delivered after the change (`relink_after`). A failure here is logged: the number has changed.
- **The account is deleted** (`deletePerson`, beside "This customer left Taskiem").
- **The business erases** either line's customer (`POST /v1/pii/erase`): each line keeps its own erasure, and nothing more crosses between them.

**The business's side** (`GET /v1/customer-chats…`, `storefront.manage|order.handle` as before):
- **One row per customer.** The list, search, `chat-refs` and the unread count (`/v1/customer-chats/unread` counts a customer once) show the pair once: the app thread's id, name and settings, the latest message of either line, unread across both, ordered by the latest of either.
- **`CustomerChat.lines`** (`CustomerThreadLine`): each line's `id`, `phone`, `window_open`, `window_until`, `stopped`, `blocked`, `notice`, `last_in_at`. Each line keeps its own meaning: on WhatsApp `stopped` is STOP ("This customer sent STOP on WhatsApp"); in the app `stopped` is the customer's Block (or their leaving Taskiem) and `blocked` the business's ("You blocked this customer in the app"). A thread of one line has one entry.
- **`reply_line`**: the line the customer last wrote on (each line's `last_inbound_at`), or the other when that one cannot take a message (STOP, a block either way, left, erased) and the other can. The top-level `window_open`, `window_until`, `stopped`, `blocked` and `notice` are `reply_line`'s; `line` is the app thread's (`app`).
- **Messages** (`GET …/{id}/messages`) are both lines' in time order, each with `line` (`whatsapp`, `app`); paging by `before` crosses both.
- **A reply** (`POST …/{id}/messages`) goes on `reply_line` unless `line` (`whatsapp` or `app`) chooses; 409 for a line the customer has no thread on, 400 for another word. On WhatsApp the 24 hours and the template hold as before (outside the window the approved `taskiem_customer_reply`, else 409); cards, offers and files always go in the app. The answer carries its `line`. Internal senders (order drafts' questions and confirmations, `ChatNotify`) still write on the thread they name.
- **What works across both:** read marks (`…/read` marks both lines; the person's copy shows them read), archive and mute (`PUT …/{id}` changes both), typing and blocks (`…/typing`, `…/block`: the app line, by either id), files (`…/files/{message}` by either id), order drafts (`GET …/{id}/orders` lists both lines'; a draft's questions go on its own line), and what the customer owes (both lines' subjects).
- **Waiting and Needs you once.** `waiting_since` is kept on the app thread for the pair: a WhatsApp message from a joined customer starts it there (and unarchives the pair), an answer on either line from someone at the business ends it (not Taskiem's words, nor an offer). The bell's "A customer wrote" groups on the pair (`/chat?with=c-{app thread}`).
- **Offers count once.** Offers go only in the app, so the opt-in (`customer_offer_consents`) and the two a week are the person's whichever line's id is used; a broadcast reaches the pair once.

**The person's side.**
- **`PersonChat.lines`**: `["app"]`, or `["app", "whatsapp"]` once joined (`person_conversations.whatsapp_since`). **`PersonMessage.line`**: `app` or `whatsapp`; show the latter marked "on WhatsApp". It cannot be answered on WhatsApp from the app (the person writes in the app).
- **What is shown.** From the join on: each message the customer sends on WhatsApp (`mine`) and each the business sends there, copied through the way back (`customer.whatsapp`, audited as a message is) from the business's copy, opened from its seal under the customer's subject. **History before the join (decision):** the WhatsApp line's last 30 days, at most 200 messages, and never from before the person's phone was verified on their account (`user_phones.verified_at`), so a number that was someone else's shows none of their messages; erased bodies are left out. The business's messages are marked read (the person had them on WhatsApp); no push or SMS is sent for them.
- **The person's copy rules** of stage 2 hold: it is theirs, kept until they delete the account; STOP (which deletes the business's WhatsApp messages) and the business's erasure do not reach it. While the person blocks the business nothing more is copied (the way back refuses it, as for a message), and a join copies no history.
- The person's rate limit counts their app messages only (`line = 'app'`).

**STOP and Block** keep their meanings and show both states: STOP on WhatsApp deletes that line's messages from the business's copy (the app line's stay) and stops that line until START; Block in the app stops the app line, deletes nothing, and stops WhatsApp messages being copied into the person's copy. A reply defaults to whichever line is open.

**The chat link's choice** (§11). `chatlinks.CardTx` reads the business's own number (`whatsapp_numbers`, active): `GET /v1/chat-links/{code}` gains `whatsapp {number, link}` (`https://wa.me/{digits}`) and `choices` (`["whatsapp", "taskiem"]` in that order; `taskiem` while it takes chats). The page `/m/{code}` (where this server serves no web app) shows **Chat on Taskiem** and **Chat on WhatsApp** (WhatsApp alone when chats in Taskiem are off). A new setting, `customer_chat_link_settings.link_whatsapp` (`PUT /v1/customers/chat-settings {link_whatsapp}`, on by default, audited in `chat_link.settings`), turns WhatsApp off; `ChatSettings.link_whatsapp` says it.

**Data (00468, 00469).** `customer_conversations`: `linked_to`, `linked_at`, `relink_after`, `number_changed_at`, `link_checked_at`; `person_messages.line`; `person_conversations.whatsapp_since`; `customer_chat_link_settings.link_whatsapp`. `taskiem_customer_reach` gains `customer.link` and `customer.whatsapp` (refused after the person's Block, hint `stopped`). New definers (in `definers.tsv`): `taskiem_customer_link_person(uuid,text)`, `taskiem_customer_link_phone(uuid,uuid)`, `taskiem_customer_link_due(integer)`; `taskiem_dispatch` may read `customer_conversations.link_checked_at`.

**API for the web and the app** (`api/openapi/openapi.yaml`; `mobile/lib/api/api.g.dart` regenerated, every new field optional in the client; `mobile/api.txt` unchanged: no new operations):
- `CustomerChat`: `lines` (`CustomerThreadLine[]`), `reply_line`. Show one row per customer, both lines' states in the thread's header, and a line chooser on the box ("on WhatsApp" / "in the app") defaulting to `reply_line`; the 24-hour note and the template apply when the chosen line is `whatsapp` (its `window_open`, `window_until`).
- `CustomerMessage.line`: mark each bubble "on WhatsApp" / "in the app".
- `POST /v1/customer-chats/{id}/messages {line}`.
- `PersonChat.lines`, `PersonMessage.line`: mark WhatsApp messages "on WhatsApp".
- `ChatLinkCard.whatsapp`, `ChatLinkCard.choices`; `ChatSettings.link_whatsapp` and `PUT /v1/customers/chat-settings {link_whatsapp}`: the web's `/m/{code}` page and the app's chat link screen offer **Chat on WhatsApp** (open `whatsapp.link`) beside **Chat on Taskiem**.

**Decisions.**
- The link lives on the WhatsApp thread (one column, one unique index) rather than a shared customer key: a pair is two rows at most, and parting is clearing one column.
- The app thread stands for the pair (its id, name, offers, blocks, typing, waiting); the WhatsApp thread keeps its own messages, drafts, window and STOP.
- A reply defaults to the line last written on, falling back to the open line; order drafts and order news stay on the thread they belong to.
- History before the join: 30 days, 200 messages, never before the phone was verified on the account.
- WhatsApp messages in the person's copy are not pushed or texted: the person had them on WhatsApp.
- A phone change moves the app thread's number to the new one but keeps its subject (its messages stay sealed under the old one); a WhatsApp thread of the new number joins it once that number writes.

**Tests.** `api/customer_lines_test.go`: `TestOneCustomerTwoLines` (a WhatsApp thread joined when the app chat starts, the person's copy showing it marked; one row with both lines, unread across both, the unread count once; either id opening the thread; messages interleaved with lines; Needs you once; a reply defaulting to the app, then to WhatsApp after a WhatsApp message, sent from the business's number and shown in the person's copy; a repeat; the app chosen by `line` from the WhatsApp line's id; 400 for another line; the person's copy in order, what they had on WhatsApp not unread; outside the 24 hours the reply refused until the template is approved, then sent as it; offers counted once, by either id, broadcasts once; typing and order drafts by either id; search once; STOP deleting the WhatsApp line's messages only, the reply going in the app and 409 on WhatsApp; Block in the app shown beside STOP; START opening WhatsApp again while the Block keeps WhatsApp out of the person's copy), `TestOneCustomerMatchingIsVerifiedOnly` (a typed delivery phone never matches; a person with no verified phone never matches, by message or sweep; another business's thread never joined; a verified phone joined by its WhatsApp message; another person never sees it; the narrow functions refuse a person asking about others and a business out of scope), `TestOneCustomerPartedAndJoinedAgain` (the sweep joining a pair there was; a phone change parting them, the WhatsApp thread saying so under the old number, the app line on the new number; the old number never joining again; the new number's WhatsApp message joining; erasure parting them and an erased number never joining again; account deletion parting them; every join and parting audited), `TestChatLinkOffersWhatsApp` (no number: Taskiem only; with its number, both, on the card and the page; turned off; chats off, WhatsApp alone). `engine/db`: `TestOneCustomerMigrationUpDownUp`; `TestDefinersAllowList`, `TestDispatchColumnsAllowList` and `TestConcurrentIndexMigrationsAreRecognised` cover 00468 and 00469.

**Left over.**
- The web's and the app's screens: one row per customer, both lines' states, the line chooser, bubbles marked by line, the person's WhatsApp messages marked, and the chat link's two buttons.
- A person whose app thread the business opened (`POST /v1/customer-chats/app`) but who has no chat of their own yet is not reached by a phone change (it finds businesses through the person's own chats); that thread is matched with the new number's WhatsApp thread when either side writes.
- A voice note put in words after it arrived shows "Voice note" in the person's copy (the business's copy gets the words).
- WhatsApp's delivery and read statuses are not copied into the person's copy (the business's messages are shown read).

## As built: stage 4, app

Built on branch `stage4-app` (`mobile/`), on e04d2f8d (the stage 4 server, with `mobile/lib/api/api.g.dart` regenerated). No server change and no change to `mobile/api.txt`: every field was in the generated client, and every one is optional there, so a server without them shows the screens as before. The web's screens were built at the same time, so the words are the spec's and the server's; where the web's differ once it lands, the app follows it (§6).

**The business's side** (`lib/chat/customer.dart`, `customer_app.dart`, `chats.dart`):
- **One row per customer.** A thread whose `lines` has both shows its row once (the server's), with small marks after the name, **WhatsApp** and **App**, red when that line cannot take a message (`lineShut`); the preview's "Sent STOP" or "Stopped messages" follows `reply_line`.
- **One thread.** Both lines' messages in time order (paging as before), each with "on WhatsApp ·" or "in the app ·" before its time (`LineMark`, `lib/chat/door.dart`); an app message drawn as the app's (`DoorBubble`), a WhatsApp one as WhatsApp's (with its template mark), by its own `line`. The head says "Customer in the app and on WhatsApp".
- **Both lines' states** on a card under the head (`_LineStates`): On WhatsApp (and its number) and In the app, each "Open", "More than a day since they wrote" (WhatsApp past its 24 hours), "Sent STOP", "You blocked them", "Stopped messages" or "Left Taskiem", "Details erased".
- **The line chooser** by the box (`_LineChooser`): "Reply" with On WhatsApp and In the app as a segmented switch, on `reply_line` at first; staff switch it, and the reply carries `line`. A line that cannot take a message is not offered (disabled, its reason as the hint) and says why under the switch ("Not on WhatsApp: they sent STOP. Nothing can go there until they send START.", "Not in the app: you blocked this customer there.", or the app line's notice). A chosen line that shuts falls back to `reply_line`. With nothing open there is no box, as before. On WhatsApp past its 24 hours the note and the 900-character limit of today hold ("More than a day since they last wrote on WhatsApp: your reply goes as a WhatsApp message they answer to carry on.").
- **Cards, offers and files** go in the app: the + shows only while the app line takes messages, and to a customer on both lines its sheet says "In the app, not on WhatsApp: cards, offers and files go there"; they are sent without `line` (an offer never carries one). Typing is told only while the reply is for the app. **Block** in the menu reads and changes the app line's block.
- **A changed number.** A WhatsApp thread whose customer moved their app line (parted, its own row) shows its `notice`, "This customer changed their number", as a note by the box; it can still be answered on the old number.

**The person's side** (`lib/chat/my_chat.dart`): when `PersonChat.lines` has `whatsapp`, "Your WhatsApp chat with them is here too" under the business's name; each message with `line: whatsapp` is marked "on WhatsApp ·" before its time (app messages unmarked). The box writes in the app.

**The chat link** (`lib/screens/chat_link.dart`, `welcome.dart`): when `choices` has `whatsapp` (`whatsappChoice`), **Chat on Taskiem** (starts the chat as before) and **Chat on WhatsApp** (its `whatsapp.link`, opened outside the app with `openOutside`, "WhatsApp didn’t open. Try again." if nothing could), or **Chat on WhatsApp** alone while chats in Taskiem are off (`taskiemChoice`; then a link opened on the way in starts no chat). Without `whatsapp` the screen is as before ("Chat with {business}"). Signed out, **Chat on WhatsApp** shows under the card over the way in: no signing in for it.

**Customer chats** (`lib/chat/chat_link_page.dart`): **Offer WhatsApp on your chat link** ("Your link offers Chat on WhatsApp, on your own number {number}, beside Chat on Taskiem. A customer on both is one chat here."), `PUT /v1/customers/chat-settings {link_whatsapp}`; shown, as on the web, only for a business with its own number (`ChatSettings.own_number`, added by the web's stream) when the settings carry `link_whatsapp`.

**Tests.** `test/one_customer_test.dart` (9): one row with both marks (STOP marked); one thread with both lines marked, both states on the head, the reply on `reply_line` then switched (`line` sent, typing only for the app), pressable; a shut line not offered and why, the + saying cards go in the app, WhatsApp past 24 hours with the template note and the app blocked, Unblock in the menu; large text in the dark on both sides; a changed number; the person's WhatsApp messages marked and the line under the name (and not without `whatsapp`); the chat link's two buttons, Chat on WhatsApp opening wa.me, Chat on Taskiem starting the chat, WhatsApp alone with no chat started; signed out, Chat on WhatsApp; the `link_whatsapp` switch saved. No parity entries: the web's pictures of these screens are not in `test/parity/web/` yet.

**Left over.**
- Parity pictures for the chooser, the line marks and the chat link's two buttons, once the web's are kept.
- The person's list row on You does not mark a chat joined to WhatsApp (the conversation does).

## As built: stage 4, web

Built on branch `stage4-web`, on e04d2f8d (stages 1 to 3 and the stage 4 server): the web screens of §11. Words in `web/src/lib/customerlines.ts` (new) and `lib/personchat.ts`.

**The business's side** (`pages/CustomerChat.tsx`).
- **One row per customer.** The list keeps its shape; a customer on both lines has "· WhatsApp · App" after their name (`listMark`; "· in the app" for the app alone, nothing for WhatsApp alone, as before), and a stopped row's preview follows `reply_line` (`stoppedPreview`).
- **One thread.** Under the name, "On WhatsApp and in the app" (or "typing…"). Both lines' messages in time order, each with a small line at the top of its bubble, "on WhatsApp" or "in the app" (`CustomerMessage.line`), shown once the thread has both lines or holds messages of both.
- **Where the reply goes**: a chooser by the box ("Your reply goes", two pills: **in the app**, **on WhatsApp**; native radios in a fieldset), starting at `reply_line` and keeping the choice while that line can take a message (`chosenLine`). A line that cannot is turned off and says why under the pills (`lineClosed`): "Not on WhatsApp: They sent STOP on WhatsApp.", "Not in the app: You blocked them in the app.", the customer's own Block or leaving ("This customer left Taskiem"), erased details. WhatsApp past its 24 hours stays a choice: the box's note says the reply goes as a message they answer, and the 900-character limit holds, as before (`lineNote`). A note the chooser does not cover, such as "This customer changed their number.", shows above it (`lineStates`); a thread of one line shows its states in the box's note as before (a WhatsApp thread parted by a number change now says so there). The reply's body carries `line` only for a thread with both (`replyLineBody`); the box's label and placeholder say where it goes ("Reply in the app", "Reply on WhatsApp").
- **Cards, offers and files go in the app**: the cards button, the clip and **Send as an offer** show while the app line can take a message, whatever line is chosen; with both lines their names say "in the app", and "Cards, offers and files go in the app." is under the chooser while WhatsApp is chosen. Typing is told only while the app is chosen. **Block** acts on the app line, by either id; with both lines it reads **Block this customer in the app** and says their WhatsApp stays as it is.
- In a thread with both lines a business's bubbles leave room for the time in the corner of the last line (`stamp-gap`, as a person's do).

**The person's side** (`pages/PersonChats.tsx`). A message of the WhatsApp line (`PersonMessage.line`) has a small "on WhatsApp" at the top of its bubble; with `PersonChat.lines` holding `whatsapp`, a line under the business's name says "Your WhatsApp chat with them is here too" (`whatsappHere`). Nothing else changes: the person writes in the app.

**The chat link's page** (`pages/ChatLinkPage.tsx`, `linkChoices`). With `choices` holding `whatsapp` and a `whatsapp.link`: **Chat on Taskiem** (the browser chat, as **Chat here in your browser** was) and **Chat on WhatsApp** (the wa.me link, a new tab, `rel="noopener noreferrer"`); with chats in Taskiem off, **Chat on WhatsApp** alone, without "not taking chats". Without `choices` (an older answer) it is as before.

**Settings > Customer chats** (`pages/CustomerChatSettings.tsx`, Taking chats): **Offer WhatsApp on your chat link** (`link_whatsapp`, turned at once and put back if refused), with the number it goes to, shown only for a business with its own number.

**Server change.** `ChatSettings.own_number` (`engine/chatlinks/settings.go`): the business's own WhatsApp number, when it has an active one, so Settings knows to offer the switch (`GET /v1/customers/chat-link`'s `settings` and `PUT /v1/customers/chat-settings`). Checked in `TestChatLinkOffersWhatsApp`; `mobile/lib/api/api.g.dart` regenerated.

**Tests.**
- Unit: `lib/customerlines.test.ts` (lines, the line chosen, why a line is closed, notes, states, the list's mark and preview, the body), `lib/personchat.test.ts` (`whatsappHere`, `linkChoices`), `lib/customerlinks.test.ts` (the line under the name). `plainwords.test.ts` already covers the pages changed.
- `e2e/one-customer.spec.ts` (new), on the real server with WhatsApp's Graph API stood in for: a business connects its own number; Settings shows the switch with the number; a customer writes to it on WhatsApp; the chat link signed out at 390 px offers Chat on Taskiem and Chat on WhatsApp (the wa.me link, a new tab, noopener); the customer signs in by phone from the link with the same number and writes in the app, and sees "Your WhatsApp chat with them is here too"; the business has one row ("WhatsApp · App") and one thread with both lines' messages marked; the reply goes in the app by default, then on WhatsApp once chosen (sent from the business's number to the customer); the person sees it marked on WhatsApp, and their own WhatsApp message too; `reply_line` follows to WhatsApp and the chooser starts there at 390 and 360 px; STOP turns WhatsApp off with why and the reply goes in the app; stood in, WhatsApp past its 24 hours with the app blocked (the template note, the app turned off, no cards); Offer WhatsApp off (the link offers Taskiem alone) and on; chats off (WhatsApp alone). axe in both themes and nothing wider than the page throughout.
- `e2e/accessibility.spec.ts`: the chat link page with both choices and with WhatsApp alone, and a person's chat with WhatsApp messages and the line under the name, at 390 and 360 px in both themes.
- Run: vitest 1,158 tests in 135 files; `tsc`; eslint over the repository; Playwright `one-customer.spec.ts`, `customer-chat.spec.ts`, `accessibility.spec.ts`, `whatsapp.spec.ts`, `you.spec.ts`, `discover-orders.spec.ts` and `parity.spec.ts` (223 tests) pass, no parity picture changed; `go test` of `TestChatLinkOffersWhatsApp` and `engine/chatlinks`.

**Left over.**
- The phone app's screens for §11 are a later stream.
- A message's line is not told in the business's search results or the person's list preview.
- The chooser's choice is kept only while the thread is open (it starts at `reply_line` each time).
- In a thread of one line a business's short message can still have its time over its last words (no `stamp-gap` there, as before): fixing it changes the `customer-chidi` parity pictures the app copies, so it waits for the app to change with it.

## As built: stage 4, web and app review fixes

Built on branch `stage4-fix-clients`, on 2c909828, beside a server stream fixing the server's findings (it makes every `CustomerChat` of a pair carry the app thread's `id`, and the default `reply_line` prefer the open app line while WhatsApp's template is not approved). No change to `api/`, `engine/` or the API's shape.

**An offer is never sent as a message.** A ticked **Send as an offer** goes as an offer, in the app, or not at all. When the app line shut, or the customer said no to offers, after the tick (web), or while the + sheet was open (app), it is refused with why ("Not sent: an offer goes in the app, and it can't take a message now. Your words are still in the box: send them again to send them as a message.", or "…this customer no longer takes offers…"; `offerRefused` in `web/src/lib/customerlines.ts` and `CustomerData.offerRefused` in `mobile/lib/chat/customer.dart`); the words stay in the box and the web takes the tick off. The 900-character limit after WhatsApp's 24 hours is of the line actually used, so it does not hold for an offer; on the web Enter no longer sends past it.

**The reply's line is held while staff write.** Once the box has words, the line in effect then is kept (web `held`, app `_held`), whatever `reply_line` the server says next. If the line chosen or held shuts while there are words, the reply goes on the other and a note says so: "Now goes in the app: it can't go on WhatsApp now." (`lineMoved`, `movedWords`). With the box empty the line follows `reply_line` again (an explicit choice is kept).

**One set of words.** The app follows the web (§6): the head says "On WhatsApp and in the app"; the chooser is "Your reply goes" (on its own line in the app) with the lines in the server's order, the app first; the box's label and hint say the line ("Reply to Tolu Ade on WhatsApp", "Reply in the app"); the notes are the web's ("More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.", "You blocked this customer. Unblock them to write to them."); **Block** to a customer on both reads "Block this customer in the app", and its question "Block {name} in the app?" says their WhatsApp stays as it is; bubbles are marked by line once the thread has both lines or holds messages of both. The web's why under the chooser takes the app's sentences: `lineClosed` gives lower-case phrases without the line's name, after "Not on WhatsApp:" / "Not in the app:" ("they sent STOP. Nothing can go there until they send START", "you blocked this customer there", "this customer left Taskiem", "their details there were erased"). The list's marks behave as the app's on both: a line that cannot take a message is red, with ", stopped" for screen readers (`listMarks`, `.line-mark .off`). The app shows the business's own number with `phoneWords` in Settings.

**Look.** The web's line mark on our own bubbles uses `--chat-out-meta` (it was `--muted`, 3.71:1 on the dark theme's green), as the app's `LineMark` uses `chatOutMeta`; the chooser's pills are 36 px high. In the app the notes and the chooser over the box take at most 30% of the screen and scroll beyond it, so large text on a small phone keeps the thread.

**Links.** The web offers Chat on WhatsApp only for a `whatsapp.link` starting `https://wa.me/` (`linkChoices`); the app offers and opens it only when `Uri.tryParse` reads it as `https://wa.me/…` (`whatsappUri`), anything else is not offered. Signed out in the app, a chat link that takes no chats in Taskiem makes **Chat on WhatsApp** the main button, says "{business} isn’t taking chats in Taskiem right now." and no longer says "Sign in to chat with {business}." On the web, Settings' hint under "Customers can start a chat with you" says "Off: your link offers Chat on WhatsApp only, on your own number, and no new chats start here." while WhatsApp is offered (`takesChatsHint`).

**One customer, one id.** The web's draft (`customer:{id}`) and the app's draft and notifications' tag (`c-{id}`) go by the thread's own id from the server's answer (`CustomerChat.id`: a pair's is the app thread's; `CustomerData.threadId`), not the id in the link, so a pair opened by its WhatsApp line's id shares the draft and holds back its notifications.

**Tests.**
- Web unit: `customerlines.test.ts` (the why's words, `lineMoved`, `offerRefused`, `listMarks`), `customerlinks.test.ts` (`takesChatsHint`), `personchat.test.ts` (only a wa.me link). vitest 1,161 tests in 135 files; `tsc`; eslint over the repository.
- `e2e/one-customer.spec.ts`: the business's page now follows the emulated theme ("system"), so its dark checks see the dark theme (they did not, which is how the bubble's contrast got past axe); axe's colour check reaches our own bubbles' line mark in the dark and passes it (it failed with the old colour). Stood in: the list's stopped mark; the line held while they write on the other line, then the note when the held line shuts; the 900-character note gone for an offer; an offer whose app line shut after the tick refused with no message sent, the words kept and the tick off; Settings' hint with WhatsApp offered and not. Passing with `accessibility.spec.ts`, `customer-chat.spec.ts` and `parity.spec.ts` (207 tests in all); no parity picture changed.
- App: `test/one_customer_test.dart` (14: the held line and the note; an offer refused, never sent; opened by the WhatsApp line's id, the draft the customer's own; Block's words; marks in a thread of one line holding both; WhatsApp alone signed out; only a wa.me link; the number in Settings), `test/home_open_test.dart` (a pair opened by its WhatsApp line's id holds back notifications tagged with the app thread's id). `flutter analyze` clean; `flutter test` 1,110 passed, 6 skipped.

**Left over.**
- The list highlights the open row by the id in the link: a pair opened by its WhatsApp line's id is not shown selected in the web's list until the server stream's ids land.
- No parity pictures yet for the chooser, the line marks or the chat link's two buttons (as before).
