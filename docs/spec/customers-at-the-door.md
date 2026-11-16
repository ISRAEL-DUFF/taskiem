# Customers at the door: a customer who finds Taskiem is helped, not signed up

Status: **draft for review** (2026-11-16). Nothing here is built. No new decision is needed. This keeps [0039](../decisions/0039-reaching-people-outside.md)'s rule that customers are outsiders, and [0058](../decisions/0058-chat-as-a-way-in.md)'s rule that only shared channels sign people up. It changes what the shared doors say to a person who is not a business. Builds on: [chat as the way in](chat-way-in.md), [storefronts](storefronts.md), [the phone app](mobile-app.md), [no technical steps](no-technical-steps.md).

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
