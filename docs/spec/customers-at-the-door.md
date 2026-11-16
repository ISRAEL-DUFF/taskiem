# Customers at the door: a customer who finds Taskiem is helped, not signed up

Status: **draft for review** (2026-11-16). Nothing here is built. Two parts:
- **Part 1** (§1 to §12): what the shared doors say to a person who is not a business. It needs no new decision: it keeps [0039](../decisions/0039-reaching-people-outside.md)'s rule that customers are outsiders, and [0058](../decisions/0058-chat-as-a-way-in.md)'s rule that only shared channels sign people up.
- **Part 2** (§13 to §29): customers chatting with a business through Taskiem's shared WhatsApp number and Telegram bot, for businesses with no number of their own. It **amends 0039**, under which an outsider on the shared channels can say only STOP and START, and needs that amendment accepted first (§24).

Builds on: [chat as the way in](chat-way-in.md), [storefronts](storefronts.md), [the phone app](mobile-app.md), [no technical steps](no-technical-steps.md), [customers' conversations](chat-five-more-flows.md#as-built-phase-1-customers-conversations).

## 1. Summary

Every door into Taskiem assumes the person runs a business: the web sign-up, the phone app, and Taskiem's own WhatsApp number and Telegram bot. A customer of one of those businesses finds Taskiem anyway. They see "Order with Taskiem" on a shop's page, get an order message from Taskiem, or search for the app. Today they either make a business they do not want or reach a dead end.

This spec adds one question at each door, **"Do you run a business, or are you buying from one?"**, and a short answer for the buyer:
- you do not need an account to buy;
- reach the business on its own number or link;
- your order's page is in the message the business sent you.

Nothing is made for them and nothing about them is kept.

It is deliberately small. Customer accounts, "My orders" across businesses, and a public list of businesses are not part of it (§9).

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Customer, buyer | A person who buys from a business on Taskiem: through its storefront, its own WhatsApp number, or an invoice. Never a Taskiem user ([storefronts §3](storefronts.md#3-goals-and-non-goals)) |
| Door | A place a stranger can first meet Taskiem: the web sign-up and sign-in pages, the phone app's first screen, Taskiem's shared WhatsApp number, its Telegram bot |
| Customer help | The answer a buyer gets at any door (§5). The same words everywhere, in every language Taskiem offers |
| Order page | The storefront's status page, `/o/{reference}?k=…`, linked from the order's messages. The key is kept only as a hash (`status_hash`), so Taskiem cannot send the link again later |

## 3. Where we are

| Door | What a customer meets today | Where |
| --- | --- | --- |
| Web sign-up | "Start with Taskiem: Create your organisation". Requires a business name and "What does your business do?" (five kinds, all businesses). Submitting makes a business | `web/src/pages/Signup.tsx`, `POST /v1/signup` (`api/onboarding.go`) |
| Web sign-in | Email and password, or chat sign-in. Nothing for someone with no account | `web/src/pages/Login.tsx` |
| Phone app, first screen | "Sign in to Taskiem", with the tagline "Orders, payments and approvals for your business, in one place". **Create an account** leads to the business form or the chat sign-up. Both make a business | `mobile/lib/screens/sign_in.dart`, `sign_up.dart`, `app_sign_up.dart` |
| Phone app, signed in with no business | Nothing handles it. The web has `InviteeHome` ("You do not belong to an organisation on Taskiem yet") | `web/src/App.tsx`; not found in `mobile/lib` |
| Taskiem's WhatsApp number, a stranger | After everything else has had its turn (reach codes, invoice, rider and supplier buttons, STOP, invitations): `signup.offer`, "Taskiem runs your business's errands…", with three buttons: **Start my business**, **I use Taskiem**, **Not now**. Free text that a model reads as "sign me up" starts sign-up at once | `api/chat_signup.go` (`signupOffer`, `signupModelRoute`) |
| Same, a number some business sends updates to | `reach.about`: "This chat sends you updates from {businesses}". No offer. Only **start** begins a sign-up | `api/reach.go` |
| Same, chat sign-up off | `wa.unbound` / `tg.unbound`: "This number is not linked to a Taskiem account…" | `api/whatsapp_chat.go`, `api/telegram.go` |
| A business's own WhatsApp number | Handled well: the stranger is that business's customer and gets a thread, its menu and draft orders. Never a sign-up | `api/customer_chat.go`, `api/order_drafts.go` |
| Storefront | Orders and bookings need no account. The order page is linked from the order's SMS, email or WhatsApp | [storefronts §5](storefronts.md#5-journeys), `engine/publicweb/status.go` |

The gap is only at the shared doors. A customer there has no way to say "I'm a customer". If they persist, they make an empty business that skews sign-up figures and is nudged by messages it will never answer.

## 4. What stays as it is

- **Customers stay outsiders.** No account, no profile and no history are made or kept for a buyer at any door. Customer help is a fixed answer; nothing the buyer sends is stored beyond the channel's usual rate-limit counters.
- **A business's own number** is unchanged. Strangers there are its customers and are never shown customer help or sign-up.
- **The order of handling on the shared number** is unchanged. Everything a business's outsiders send (reach codes, invoice, rider and supplier buttons, STOP, updates, invitations, a sign-up in progress) is handled first. Customer help replaces nothing that answers today. It changes only the offer.
- **Sign-up itself.** `POST /v1/signup`, the chat sign-up steps, their limits and countries, and consent: all unchanged.
- **People who get updates** (`reach.about`) keep their answer. It is a better one than customer help, since it names their businesses.

## 5. Customer help: the words

One message, the same at every door and translated like every other message (`engine/lang/messages`). In English:

> **You don't need an account to buy.**
> Taskiem is the tool the businesses you buy from use to take orders and payments.
>
> - **To order or ask something:** use the business's own WhatsApp number, or the link it gave you.
> - **To check an order:** open the link in the message the business sent you (by text, email or WhatsApp). Can't find it? Message the business and they will help.
>
> Run a business yourself? You can start one here any time.

On the web and in the app, the last line is a button, **Start my business**, back to sign-up. In chat it reads "Send *start* any time to begin a business". There is no other button: a buyer has nothing else to do here.

The words avoid every word in [Appendix C](no-technical-steps.md) and never name Taskiem's own pages. A buyer does not know what a "storefront" or a "status page" is.

## 6. Each door

### 6.1 Web sign-up (`/signup`)

The page gains a first step before the form:

> **Start with Taskiem**
>
> ( ) **I run a business**: take orders, get paid, keep stock, with your staff.
> ( ) **I'm buying from a business**: check an order, or reach a shop.

- **I run a business** shows today's form, unchanged.
- **I'm buying from a business** shows customer help (§5) in the same card, with **Start my business** under it. It records nothing and makes no call to the server.
- The choice is in the address (`/signup?for=business` or `?for=buying`), so a link can open either. Taskiem's own site can use `?for=business` to skip the question.
- Back from customer help returns to the question.

The two choices are a radio group of large rows, keyboard-reachable, at 390 and 360 px, and pass axe in both themes like every page ([web/AGENTS.md](../../web/AGENTS.md)).

### 6.2 Web sign-in (`/login`)

Under the form, beside "New to Taskiem?", one line:

> Buying from a business? **You don't need an account.**

The link opens `/signup?for=buying`. Customers who reach sign-in because Taskiem's name was on their order message are the ones most likely to try to sign in.

### 6.3 Phone app

- **First screen.** The tagline stays. Under **Create an account**, the same line as §6.2: "Buying from a business? **You don't need an account.**" It opens customer help as its own screen.
- **Create an account** asks §6.1's question first, as a screen of two large choices, before the business form or the chat sign-up. **I'm buying from a business** opens customer help.
- **Signed in, belonging to no business.** This happens when the owner removed them, or the business was closed. The app gets the screen the web has (`InviteeHome`):
  - invitations waiting, if any;
  - **Start a business**;
  - **Sign out**;
  - customer help, folded under "Buying from a business?".

  Today the app has nothing for this.
- Customer help in the app is a screen of the app's own, built like its other screens ([mobile-app-look](mobile-app-look.md)), not a web page.

### 6.4 Taskiem's WhatsApp number

WhatsApp allows at most three buttons on a message (`engine/whatsapp/graph.go`), and the offer already uses three. The offer becomes:

> Hello! Taskiem runs businesses' errands from this chat: payments, invoices, stock, reminders, a shop page. Do you run a business? Start for free.
>
> **Buying from a business?** You don't need to sign up: tap *I'm a customer*.
>
> To chat in another language, type its name: {languages}.

Buttons: **Start my business** · **I use Taskiem** · **I'm a customer**.

- **Not now** loses its button. Sending nothing is "not now". The words "no" and "not now" still give `signup.later`, matched like the sign-up words are today (`signupWord` / `signupPhrase`, in every language).
- **I'm a customer** (`su:customer`) answers customer help (§5) and changes nothing. A later message from the same number gets the offer again under the same limits as today (two answers per ten minutes per number), so a customer is not answered at length twice.
- **Free text.** `signupModelRoute` today asks one thing: does this mean "sign me up"? It is asked a second thing in the same call: does it sound like a customer ("where is my order", "I want to buy rice", "how much is…", "I paid but…")? If so, the answer is customer help, not the offer. With no model, or when unsure, the offer goes as today, and its new line points a customer to the button.
- **Chat sign-up off.** `wa.unbound` gains one sentence: "Buying from a business? You don't need an account here; use the business's own number or link."

### 6.5 Taskiem's Telegram bot

Telegram has no three-button limit. The offer keeps **Not now** and adds **I'm a customer** as a fourth. Everything else is as §6.4, including `tg.unbound`'s added sentence.

### 6.6 Slack

Unchanged. A Slack door is a workspace installing Taskiem's app, which no customer does.

## 7. Counting

To see whether this matters and whether it works:

- `taskiem_door_choice_total{door, choice}`. `door` is `web_signup`, `web_login`, `app_first`, `app_signup`, `whatsapp` or `telegram`; `choice` is `business`, `buying` or `have`. The web and app choices are counted by one unauthenticated beacon (`POST /v1/door`, no body but the two labels, rate-limited per IP). Nothing identifies the person.
- `taskiem_signup_offer_total{channel, route}`, where `route` is `offer`, `customer_model` or `signup_model`, says how often the model sends a stranger to customer help.

**Before building, measure.** Count the businesses made in the last 90 days that look like a customer's, for the founder's eyes only and as counts only:
- nothing done in their first 7 days (no product, invoice, order, workflow run or second member); or
- a business name equal to the owner's own name.

If they are a handful, §6.1 to §6.4 are still worth their small cost. If they are many, §10's next step is worth planning.

## 8. Tests

- **Web (e2e).**
  - `/signup` asks the question first. **I'm buying** shows customer help and posts nothing to `/v1/signup`. **Start my business** returns to the form, and `?for=` opens either.
  - `/login` has the line and it opens customer help.
  - All of this at 390 and 360 px and with axe in both themes. Both pages are added to `e2e/accessibility.spec.ts`, and the words to `src/lib/plainwords.test.ts`.
- **App (widget tests and parity).**
  - The first screen's line, the question before sign-up, customer help, and the no-business screen.
  - Parity pictures for customer help and the question, light and dark.
- **Chat (Go).**
  - On the shared number, a stranger gets the offer with the three new buttons; `su:customer` answers customer help; "not now" gives `signup.later`.
  - A model-routed "where is my order" gets customer help, and "I want to start selling" starts sign-up.
  - With no model the offer goes.
  - A number some business reaches still gets `reach.about`.
  - A stranger on a business's own number still gets that business, never customer help.
  - Telegram has four buttons.
  - The rate limit holds across the offer and customer help.
- **Words.** Every new message is in `en.json` with `reviewed: true` and translated with the others before release, as the chat's words are.

## 9. Not in this spec

- **Customer accounts, profiles or saved addresses.** They remain a non-goal ([storefronts §3](storefronts.md#3-goals-and-non-goals)).
- **"My orders" across businesses**, by a code to the customer's phone (§10).
- **Finding a business from customer help.** Explore lists businesses that chose to be found *by other businesses*. Showing them to the public is a different consent and a different product (§11, question 3).
- **Sending an order's link again.** It cannot be done today, since only the key's hash is kept (§2). Whether a business should be able to send a fresh link is §11, question 4.

## 10. The next step this leaves room for

**"My orders" by phone.** A customer proves their number with a code, as chat sign-in does, and sees every open order and booking placed with that number across businesses, each with its state and its pay link. Each order already holds the customer's personal-data subject, taken from their phone (`storefront_orders.subject_id`), so finding them needs no new data. It does need:
- a lookup across businesses, which every query today avoids by design;
- fresh order-page keys;
- a privacy review.

It belongs in its own spec, and only if §7's figures say customers are arriving.

## 11. Questions for review

1. **The buyer's label.** "I'm a customer" (button) and "I'm buying from a business" (web and app): are these the words, or one of them everywhere?
2. **Losing the "Not now" button on WhatsApp.** Is replacing it with **I'm a customer** right? The alternative is to keep three buttons and put the customer path only in the words and the model, which customers who don't read would miss.
3. **A business finder in customer help.** Should customer help let a buyer search businesses that opted in, showing their own WhatsApp number and shop link? It would need a new "show me to the public" setting, separate from Explore's.
4. **Sending an order's link again.** Should a business be able to send a fresh order link from the order (a new key, the old one spent)? It would make "message the business and they will help" fully true.
5. **The measurement in §7.** Run it before or alongside building §6?

## 12. Phasing

One milestone, in this order, each shippable alone:

1. Chat: the offer's words and buttons, `su:customer`, the model's second question, `wa.unbound` / `tg.unbound`.
2. Web: the sign-up question, customer help, the sign-in line.
3. App: the first screen's line, the question, customer help, the no-business screen.
4. Counting (§7).

---

# Part 2: chatting with a business on Taskiem's shared number

## 13. Summary

Today a customer can chat with a business through Taskiem only when the business has connected **its own WhatsApp number**. That needs Meta's business verification, which most of the small businesses Taskiem is for do not have. Everyone else's customers write to the owner's personal WhatsApp, outside Taskiem: no thread, no order drafted from the message, no staff to share it with, no record. Telegram has no customer conversations at all.

Part 2 lets any business hold its customers' conversations on **Taskiem's shared WhatsApp number and Telegram bot**:
- **The link.** The business shares a **chat link** (and its QR code): on its storefront, Instagram, a flyer, a receipt.
- **The customer.** Tapping it opens Taskiem's WhatsApp, or the Telegram bot, with a message that names the business. From then on, what the customer writes goes to that business, and the business's replies come back in the same chat, each headed with the business's name.
- **The business.** The conversation is the same thread as an own-number customer's, in the business's chat on the web and the app, so drafted orders, replies, STOP and the rest work unchanged.

A customer who chats with more than one business on the shared number is told which business each message comes from, and chooses which one they are writing to (§19). A business that later connects its own number moves to it (§21.4).

## 14. Words used here

| Word | Meaning |
| --- | --- |
| Line | Where a customer's conversation with a business runs: the business's **own** WhatsApp number, Taskiem's **shared** WhatsApp number, or Taskiem's **Telegram** bot |
| Chat link | A business's standing link to start a conversation with it on a shared line: `wa.me/<Taskiem's number>?text=…` for WhatsApp, `t.me/<bot>?start=…` for Telegram, and one page that offers both (§17). Not one-time: it is printed and shared |
| Chat code | What a chat link carries so Taskiem knows the business: `C-` and 10 characters, one per business, which the owner can replace |
| Current business | For one customer on one shared line, the business their next plain message goes to (§19) |

## 15. Where we are

| Exists | Where |
| --- | --- |
| A customer's thread with a business: one per customer number per own number, sealed under the customer's subject. Staff read it and reply in the chat; the reply goes as text within 24 hours of the customer's last message, and as the approved template (900 characters, one line) after. STOP deletes the thread's messages; START undoes it | `customer_conversations`, `customer_messages` (migration 00411), `api/customer_chat.go` |
| Orders drafted from a thread's messages, confirmed by staff, then sent to the customer with the total and how to pay | `api/order_drafts.go`, `order_drafts` |
| **Replying needs an own number.** `sendCustomer` refuses when the business has none (`!wa.Own()`). `customerInbound` takes messages only on an own number | `api/customer_chat.go:207, :817` |
| Updates for outsiders on the shared number and bot: a one-time code link (`U-…`) opts a person in to a business's messages. Anything they write back gets one fixed answer. A cross-business lookup, `taskiem_reach_businesses`, names the businesses that reach an address | `api/reach.go`, `engine/reach`, migration 00193 |
| Messages on the shared number in a business's name are headed `[Business] ` | `engine/whatsapp/platform.go` (`Prefix`) |
| The storefront's **Chat on WhatsApp** button: `wa.me/` to the number the owner typed on the storefront card, often their personal WhatsApp | `engine/storefront/model.go` (`WhatsAppLink`) |
| Telegram: one platform bot; a business's own bot is "later" | [no technical steps](no-technical-steps.md) N0-9, [chat as the way in §15](chat-way-in.md) |

## 16. What stays as it is

- **Own numbers.** A business with its own number keeps its customers there. Its chat link points at its own number (§17), and nothing in §19 applies to it.
- **Members on the shared number.** A person bound to a Taskiem account who writes to the shared number is in Taskiem's conversation, as today. They reach a business as its customer only through a chat link (§20).
- **Updates.** Opting in to a business's updates (`U-…` codes) is unchanged. A customer in conversation with a business also gets that business's updates there, as one chat.
- **What is handled first.** Reach codes, invoice, rider and supplier buttons, STOP and START, rider and supplier words, invitations and a sign-up in progress keep their place ahead of everything in Part 2 (§19.4).
- **Customers stay outsiders.** A customer has no account. Their thread belongs to the business, sealed under the customer's subject and erased with it.

## 17. The business's chat link

**Where it is.** In the business's Taskiem chat, **Tools > Customers > Your chat link**, and in Settings on the web. It shows:
- the link;
- a QR code to download or print;
- the words to paste into Instagram or a status: "Chat with {business} on WhatsApp: {link}";
- whether it opens the business's own number or Taskiem's.

**What it opens.**
- **WhatsApp:** `https://wa.me/<Taskiem's number>?text=Hello%20{business}%20(C-xxxxxxxxxx)`. The customer sees a ready message, "Hello Mama's Kitchen (C-7h2k9q4m1x)", and sends it.
- **Telegram:** `https://t.me/<bot>?start=c_xxxxxxxxxx`. The customer taps **Start**.
- **One page for both**, `/c/{code}` on the form host. It shows the business's name and logo with **Chat on WhatsApp** and **Chat on Telegram**, like the updates page `/u/{id}`. This is the link to share when the business does not know which app its customers use.

**With an own number,** the WhatsApp link is `wa.me/<the business's number>` with no code, and the Telegram link is the same as above.

**The code.**
- One per business, made when the business is.
- Ten characters from an alphabet with no look-alike characters.
- The owner can replace it (**Make a new link**) when it is abused. The old code then answers "This link no longer works. Ask {business} for their new one" for 30 days, and then nothing.

**The storefront's Chat on WhatsApp button.** A business with no own number chooses, on the storefront card, between **Chat with us on Taskiem** (the chat link, the default for new storefronts) and **My own WhatsApp** (today's typed number). Existing storefronts keep what they have until the owner changes it. The chat says once: "Your shop page's chat button opens your personal WhatsApp. Want customers' messages in Taskiem instead?"

## 18. The customer's side

**First message.** The customer sends the link's message. Taskiem answers once, in the business's name:

> *[Mama's Kitchen]* Hello! You're chatting with Mama's Kitchen through Taskiem. Write here and they'll answer. Photos and voice notes are fine.
>
> Send *STOP* to stop messages from Mama's Kitchen. Chatting with more than one business here? Send *SWITCH* to choose.

The greeting is the business's own words if it set one (up to 300 characters, checked like any message it sends), followed by Taskiem's fixed second paragraph.

**After that:**
- What the customer sends (words, photos, voice notes, files, a location) goes into the business's thread, kept as an own number's thread keeps it.
- Nothing is answered automatically. The business answers when staff reply.
- Every reply arrives headed `[Mama's Kitchen]`, as shared-number messages from a business are today.
- The customer's WhatsApp shows the conversation under Taskiem's name, not the business's. The heading is what tells them who is writing.

**Ordering.** Orders are drafted from the messages and confirmed by staff as on an own number (`order_drafts`). The confirmed order, its total and how to pay come in the same chat.

**STOP.**
- **STOP** stops the current business only: its thread's messages are deleted and nothing more comes from it until **START**, as on an own number. Taskiem answers "Mama's Kitchen won't message you here again. Send *START* to undo."
- **STOP ALL** stops every business on this line for this customer, along with their updates.

## 19. Which business a message is for

One customer may chat with several businesses through the one shared number. Each message must reach exactly one, and the customer must always be able to tell which.

### 19.1 The rule

For a message from a customer on a shared line, in this order:

1. **A chat code** in the message (or a Telegram `start` with one): that business. It becomes the current business.
2. **A reply to a message** (WhatsApp's quoted reply, Telegram's reply): the business that sent the quoted message. It becomes the current business.
3. **A choice button** from §19.2: that business. It becomes the current business.
4. **Otherwise, the current business**, unless another business has written to the customer since the customer last wrote. Then Taskiem asks first (§19.2) and holds the message until they answer.
5. **No current business**, and none has written within 30 days: not a conversation. Part 1's handling goes on (customer help, or the sign-up offer).

The current business stays for 30 days after the customer last wrote to it. After that, rule 5 applies until a code, a reply or a choice.

### 19.2 Asking

> Who is this for?
>
> **Mama's Kitchen** · **Bola Stores**

- The businesses offered are those the customer has written to or heard from in the last 30 days, most recent first.
- Up to three are shown as buttons. More than three come as a WhatsApp list (up to ten), or as Telegram buttons.
- The held message is delivered to the chosen business as if just sent. One not chosen within an hour is dropped, and the customer is told so the next time they write.

**SWITCH** (and its translations) asks the same question at any time, without a held message.

### 19.3 Telling the customer

- Every message from a business is headed with its name (`[Mama's Kitchen] `).
- When the current business changes by rules 1 to 3, Taskiem says nothing: the customer chose it.
- When a different business writes while the customer is mid-conversation with another, its heading is enough. The customer's next plain message is asked about (rule 4) rather than guessed.

### 19.4 Where this sits in the shared number's handling

Ahead of Part 2:
- reach codes;
- invoice, rider and supplier buttons;
- STOP and START (a customer's, then the reach book's);
- rider and supplier words;
- link offers;
- the key guard;
- invitations;
- a sign-up in progress.

Then **chat codes, quoted replies, choice buttons and the current business (§19.1)**. Then the existing `reach.about` and Part 1's customer help or sign-up offer.

A rider or supplier of a business who is also some business's customer keeps their words first, as today: "busy" from a rider is a rider's word, not a customer message.

## 20. Members who are also customers

A person bound to a Taskiem account writes to the shared number to work: their messages are Taskiem's conversation. The same person may be a customer of another business, for example a cashier at one shop buying lunch from another.

- **The way in.** A member reaches a business as a customer only through its chat link. A code from a bound number opens a **customer conversation** with that business.
- **While it is open:**
  - plain messages go to the business;
  - the business's replies come in headed with its name;
  - each of Taskiem's own messages to them (approvals, alerts) is headed `[Taskiem]` so the two can be told apart.
- **How it closes.**
  - It closes after 30 minutes with no message either way, or when they send **DONE**. Their messages go back to Taskiem's conversation.
  - A business's reply after that comes in headed with its name. A quoted reply to it reopens the conversation for 30 minutes.
- **What never happens.** Taskiem never sends a member's own business data in a customer conversation, and never sends a customer conversation's messages into the member's business.

## 21. The business's side

### 21.1 The thread

A shared-line thread is a customer thread like any other:
- the same Customers list, unread counts and search;
- read marks, drafted orders and the card actions.

On the web and in the app it is marked with where it runs: **on Taskiem's WhatsApp** or **on Telegram**, beside the customer's name. Staff need nothing new to answer it.

### 21.2 Replying

- **Within 24 hours of the customer's last WhatsApp message:** text, headed `[Business] `, from the shared number.
- **After 24 hours:** one platform template, `customer_message`, with two variables (the business's name and the words, 900 characters, one line), as an own number's template carries them. The template's words say it is a reply from that business to a conversation the customer started.
- **Telegram:** no window and no templates. Text always, headed with the business's name.
- **Never first.** A business can reply only to a thread the customer started on that line. It cannot start a shared-line conversation with a number that never wrote to it, however it learned the number.

### 21.3 Blocking

New for every line: **Block this customer** in the thread's menu, for spam and abuse. A blocked customer's messages are not kept and they get no answer. On the shared line they are answered once: "{business} isn't taking messages from you here." The block is lifted from the same menu.

### 21.4 Moving to an own number

When a business connects its own number:
- its chat link changes to open the own number;
- new customers start there;
- existing shared-line threads stay on the shared line, and replies keep going from it, until the customer writes to the own number;
- the business can send each open shared-line thread a one-time message: "We have our own WhatsApp number now: {number}. Message us there."

Taskiem never moves a thread to a number the customer has not written to, since a message from an unknown number looks like spam.

The reverse also works: a business that disconnects its own number keeps its threads, and the chat link falls back to the shared number with its code.

## 22. Telegram

- **The address.** The customer is a Telegram account (`tg:<user id>`). Their thread's subject is made from that address. No phone number is needed to chat.
- **Orders.** An order that needs a phone (delivery, a pay link by text) asks for it once with **Share my number**, as sign-up on Telegram does. The order goes on without it if the business takes it so.
- **Sending.** No window and no templates (§21.2).
- **Choosing a business.** Choices are inline buttons, any number.
- **STOP.** STOP and STOP ALL work as on WhatsApp.

## 23. Data

**`customer_conversations` gains:**
- `line`: `own`, `shared` or `telegram`;
- `address_hash`: the keyed hash of the customer's WhatsApp number or Telegram address, so the line's router can find threads without opening sealed fields;
- `blocked_at`.

`phone_number_id` keeps the own number's id. For `shared` it holds the shared number's id, and for `telegram` the bot's username. The unique key becomes `(tenant_id, line, phone_number_id, subject_id)`.

**`customer_chat_codes`** (tenant-scoped):
- `tenant_id`, the code's hash, the code itself (sealed);
- `made_at`, `replaced_at`;
- an index on the hash.

**`customer_routes`** is a platform table, not tenant-scoped: one row per (line, `address_hash`). It holds:
- the current business and its thread;
- `chosen_at`, the customer's last inbound;
- a held message's reference and when it was held.

The api role reads and writes it only through `SECURITY DEFINER` functions that return one route or the list of businesses for one address, as `taskiem_reach_businesses` does. No query lists routes, and none reads a tenant's thread through a route without entering that tenant.

**Retention and erasure.**
- Messages keep their year.
- A route goes 30 days after its last use.
- Erasing a customer's subject in a business deletes that business's thread and its place in routes.
- STOP ALL deletes the routes.

## 24. Safety, limits, cost and the decision

**Abuse by customers.**
- Per customer per business: 30 messages an hour.
- Per business on shared lines: a daily ceiling of new threads (500, by plan).
- Media size as today.
- Block (§21.3).

**Abuse by businesses.**
- A business cannot write first on a shared line (§21.2).
- Outside the window, at most 3 templates per thread before the customer writes again.
- What a business sends is checked like any outbound message on the shared number.
- A business whose customers block or STOP it far above the norm has shared-line conversations paused for review: its threads stay readable, and new messages are not sent.

**The shared number's standing.** Every business's conversations ride on one WhatsApp number whose quality rating Meta tracks. The operator can switch Part 2 off globally or per business. The quality rating and per-business STOP and block rates are on the operator's dashboard.

**Who the customer is talking to.**
- The heading is the business's name as registered on Taskiem.
- Two businesses with the same name are told apart by their area (`[Mama's Kitchen · Ikeja]`).
- A name that imitates Taskiem or a bank is refused at sign-up, as today.

**Cost.**
- WhatsApp charges for templates outside the window. Each template sent for a business counts against its plan's messages, as its other templates do.
- Replies inside the window are free on WhatsApp and are not counted.

**The decision.** Part 2 amends [0039](../decisions/0039-reaching-people-outside.md). Today an outsider on the shared channels can only opt in to updates and say STOP and START, with one fixed answer to anything else. After the amendment, an outsider who opens a business's chat link, or who has a thread with a business on a shared line, converses with that business there, under §19's routing and §24's limits. [0058](../decisions/0058-chat-as-a-way-in.md) point 6 stands: no one is signed up from inside a customer conversation. The amendment is a short decision record, 0068, written with this spec's review.

## 25. Finding a business by name

The question Part 1 left open (§11, question 3) has a narrow answer here, as its own phase:

- **The setting.** **Customers can find us by name in chat**, off by default, beside the chat link in Settings.
- **The search.** A message on the shared line with no current business, which the model reads as "I want {business}" (or the words **find {name}**), is searched among the businesses that turned it on: by name and area, at most three matches.
- **The answer.** "Did you mean: **Mama's Kitchen · Ikeja** · **Mama's Kitchen · Lekki**?" Choosing one is a chat code.
- **No match**, or the business didn't turn the setting on: customer help (§5), unchanged. Taskiem never says whether a business is on Taskiem.

## 26. Changes to Part 1

- **Customer help (§5)** gains: "To chat with a business, use its chat link or the number it gave you."
- **The model's customer question (§6.4)** also tells "talk to Mama's Kitchen" (§25) apart from "where is my order".
- **§9's "Finding a business"** is answered, narrowly, by §25.

## 27. Tests

**Go.**
- Routing (§19.1):
  - a code sets the current business; a quoted reply goes to its sender;
  - a plain message goes to the current business, or asks when another business wrote since;
  - the held message is delivered on choice and dropped after an hour;
  - SWITCH; the 30-day expiry; with no route, Part 1's handling.
- The handling order holds (§19.4): a rider's words, STOP and a sign-up in progress are untouched.
- Replies:
  - text in the window; the template after it; refused when the customer never wrote;
  - three templates at most; refused when blocked or stopped.
- STOP and STOP ALL; START.
- Members (§20): a code opens a customer conversation, DONE and 30 minutes close it, and `[Taskiem]` headings appear.
- Moving to an own number (§21.4).
- Telegram: start codes, inline choices, sharing a number for an order.
- Erasure removes the route.
- The routes table is reachable only through its functions.

**e2e.**
- A business with no own number shows its chat link and QR.
- A customer message (from the fake Graph API) appears in the Customers list marked **on Taskiem's WhatsApp**.
- Staff reply; an order is drafted and confirmed.
- Block.
- All at 390 and 360 px, axe in both themes.

**App.** The thread's line mark, block, the chat link screen; parity pictures.

**Load.** One customer with ten businesses; one business with 500 new threads in a day.

## 28. Phasing

After Part 1, each phase shippable alone:

1. **WhatsApp shared line:**
   - chat codes and the link;
   - §19's routing;
   - threads and replies, with the template;
   - STOP, STOP ALL and block;
   - the storefront button choice;
   - limits and the operator switch.
2. **Members as customers** (§20) and **moving to an own number** (§21.4).
3. **Telegram** (§22) and the `/c/{code}` page for both apps.
4. **Finding a business by name** (§25).

Before phase 1:
- decision 0068 accepted;
- the `customer_message` template submitted to and approved by Meta.

## 29. Questions for review

1. **The heading.** `[Mama's Kitchen]` (today's form) or `*Mama's Kitchen*:` in bold, which reads better on a phone?
2. **The storefront default.** Should new storefronts' chat button default to the Taskiem chat link, or keep asking for a number?
3. **Members as customers.** Is §20's 30-minute customer conversation with **DONE** right, or should members reach businesses as customers only on businesses' own numbers in phase 1?
4. **The business's greeting.** Is it allowed in phase 1, or is the fixed greeting enough?
5. **The new-thread ceiling** per business per day (500): by plan, or one number for all?
6. **Finding by name** (§25): its own phase, or not at all until businesses ask for it?
