# The Taskiem phone app (Flutter): a chat app, complete in itself

Status: **proposal for the founder's review** (2026-11-11). Nothing here is
built. The decision it asks for is [0066](../decisions/0066-a-flutter-phone-app.md)
(proposed). It builds on [chat push](chat-push.md), [offline](offline.md),
[chat tools](chat-tools.md), [chat as the way in](chat-way-in.md) and
[chat sign-in](chat-sign-in.md).

## 1. Summary

A native app for Android and iPhone, written in Flutter, for owners and their
staff. **It is the web chat as a phone app, like WhatsApp: everything the web
chat does today, and nothing else.** That is already almost the whole
business, because Taskiem's chat is how a business runs:

- **`@Taskiem`** is the same conversation as Taskiem on WhatsApp. In it a
  person sets up the shop and its fronts, adds products, stock, services and
  hours, builds a workflow with AI and publishes it, connects Paystack or
  Google, invites staff, sees their plan, sends invoices, records expenses
  and cash, keeps tables, approves payments, switches business and language,
  and talks by voice note.
- **The conversations with linked businesses and customers** carry the trade:
  orders, deals, deliveries, bills, payments, with every chat tool and its
  cards and sheets.
- **The screens those lead to** (an invoice, an order, a product, a bill)
  are the app's own, built from the web's.

The app does not send people to the web. It opens a browser only where the
chat itself does, for a reason the app cannot remove (§3.5).

- **What it is for.** The chat quick on a cheap Android phone; notifications
  when nothing is open; a camera, voice notes, photos and sharing that feel
  like the phone's own; a place in the Play Store.
- **What it is not.** It does not replace WhatsApp, Telegram or Slack, where
  most traders will still talk to Taskiem
  ([chat-first strategy](../strategy/chat-first-nigeria.md)). It has nothing
  the web chat does not: no dashboards, Money's reports, or the workflow
  canvas.
- **What it costs.** A second client for the chat, the part of the product
  that changes most (about 17,000 of the web app's 88,000 lines), and the
  everyday part of the pages the chat's tools link to. §9 is how the two stay
  the same.
- **Order.** Android first, iPhone from the same code and released after.
  Milestones in §10.

## 2. Who uses it

| Person | What they do in it |
| --- | --- |
| Owner | Everything above: runs the business from `@Taskiem` and the conversations |
| Staff | The same within their role (`cashier`, `shift_manager`, and the roles a business gives): sell, stock, orders, handover, their tasks |
| A business that delivers | Its deliveries and availability, which are chat orders and a tool ([decision 0065](../decisions/0065-riders-are-organisations.md)) |

## 3. What the app does: what the web chat does

Drawn from the web chat's code and the chat's plans. The rule: **if the web
chat does it, the app does it, in the same words and with the same
confirmations; if the web chat links to a page for it, the app has that page
as a screen of its own.**

### 3.1 The conversations

| Part | What | In the web chat |
| --- | --- | --- |
| Sign in | A code to your WhatsApp or Telegram; email and password; a passkey; staying signed in (§5.2) | `Login.tsx`; `/v1/auth/chat/*`, `/v1/auth/login`, `/v1/auth/passkey` with `bearer: true` |
| Chats | `@Taskiem` first, then linked businesses and customers; unread, drafts, pinned, muted, archived; search across chats; the business switcher | `Conversation.tsx`, `ChatRow`, `ChatSearch`, `/v1/link-chats` |
| A conversation | Text, photos, camera, voice notes, files, replies, reactions, ticks, typing, forward, star, delete for me, `@` and `#` mentions, requests typed as words | `Conversation.tsx`, `ChatComposer`, `ThreadParts`, `BubbleShell`, `Mentions`, `RequestChip`, `/v1/chat/stream` |
| A customer's conversation | On the business's own WhatsApp number: replies within the 24-hour window, templates outside it, STOP honoured; **orders read from their messages**, confirmed, paid, sent out | `CustomerChat.tsx`, `CustomerOrders.tsx` ([five flows](chat-five-more-flows.md), phases 1 and 2) |
| Cards | Live cards, reference cards, deals, money, alerts and approvals, the briefing and its chart, orders and deliveries | `ChatLiveCard`, `ChatRefCard`, `ChatDeal`, `ChatMoney`, `ChatAlerts`, `ChatStaff`, `CustomerOrders` |

### 3.2 `@Taskiem`

The web's Taskiem conversation (`TaskiemChat.tsx`, `/v1/taskiem-chat`) is the
conversation Taskiem has on WhatsApp, Telegram and Slack (`converse`,
`api/whatsapp_chat.go`): messages are text with reply buttons, and what a
command needs is asked one thing at a time. The app draws the same messages,
so **every command works in the app as soon as the conversation does**, and
each new one the server learns works in the app the same day. Today they are
([WhatsApp](../whatsapp.md#for-people-using-taskiem-on-whatsapp)):

| For | Commands |
| --- | --- |
| The day | `help`, `status`, `approvals` (with the chat PIN, or a passkey in the app), the morning briefing, yesterday's money |
| Selling | `make my page`, `add product …` (or a photo with a caption), `price`, `hide`, `show`, `hours …`, `close shop`, `open shop`, `qr`; fronts (`add front …`, `add dish …`, `sold out …`, `turn on orders for …`, tickets, hire, spaces, quotes); `orders`, `order <ref> ready` |
| Stock | `add 50 cartons of Indomie at 8,500`, `stock …`, `how many … do I have?`, `adjust … -2 spoilt` |
| Bookings | `bookings`, `today`, `move <ref> to …`, `booking <ref> cancel`, `off 25 Dec`, `refund <ref>` |
| Money | `invoice Ada 25,000 for …`, `spent 12k on diesel`, `cash in …`, `cash out …`, debts kept as invoices (*Ada owes 5,000*, `who owes me?`, `send Ada her balance`), payments to match |
| Records | `tables`, `add to <table>`, `find <table> …`, questions answered from the rows |
| Automation | `build …` or the goal in words ("every Friday text my customers who owe me"): the AI's draft as plain steps, the details asked one by one, saved on **yes**, tables made, settings asked, **Publish it now?**; `run <workflow>` with its inputs; `publish <workflow>` |
| Set-up | `connect <service>`; `invite …` (staff, with their role); `plan`, `pay`; `email`; `switch <business>`; `language …`; the chat PIN |
| Voice | A voice note, read as if typed |

### 3.3 The tools, and the screens they lead to

Every chat tool (`lib/tools.ts`, [chat tools](chat-tools.md) phases 1 to 7,
[categories and variants](chat-categories-and-variants.md)) works in the app
as in the web chat: the picker, each tool's sheet with chips, search and rows,
its form for a row, its saving tick and diary line. Where the web sheet ends
with a link to a full page (**Open invoices**, **Open stock**…) or a card
links to one, the app has that page as a screen, doing its everyday work:

| Tool | The web page it links to | The app's screens |
| --- | --- | --- |
| Invoices | `/invoices`, `/invoices/{id}` (`Invoices.tsx`) | The list and filters; an invoice: lines, what is due, status, payments, send it (to the chat, or as a PDF through the phone's share sheet), record or undo a payment, discount, amend, void; a new invoice; a customer's credit and open bills |
| Money | `/money` (`Money.tsx`, `ChatMoney`) | Money in and out, cash and expenses, transfers to confirm, payments to match (`MatchSheet`) |
| Orders from linked businesses | `/trade-orders/{id}` (`TradeOrders.tsx`) | The list; an order: accept, decline, propose a change, can't fulfil, delivered and received, what they counted, raise its invoice, documents; their statement; credit limit and days to pay |
| Orders from customers | `/storefront/orders/{ref}` (`StorefrontOrders.tsx`) | The list; an order: items, payment, fulfilment, decide it, money matched |
| Deliveries, Availability | `/storefront/deliveries` | Deliveries and their steps; the rider's side and availability |
| Products, Stock, Sell, Handover | `/storefront/stock` (`StorefrontStock.tsx`, `ChatToolForms`) | Products with price, photos, category, sizes and colours, hide; stock counted, in, taken off or added with a reason, reorder level, low, today's sales, every change |
| Services and bookings | `/storefront/bookings` (`StorefrontBookings.tsx`) | Services; bookings upcoming, moved, days off, booking hours |
| Price lists and deals | `/storefront/price-lists` (`PriceLists.tsx`) | Lists, items, prices and quantity breaks, who sees each, offers from buyers, staff's discount limit |
| Purchases, suppliers, Order | `/purchases/{id}`, `/storefront/suppliers` (`ChatBuyer`, `StockPurchases.tsx`, `StorefrontSuppliers.tsx`) | Orders to suppliers: make, send, counter, cancel, all came, paid; suppliers linked or not; the second-person threshold; a supplier's prices |
| Bills | `/bills/{id}` (`Bills.tsx`, `ChatBuyer`) | Bills to pay and paid; pay by transfer or cash, Pay from Taskiem with step-up, what is wrong |
| Tables | `/tables/{id}` (`Tables.tsx`) | A table's rows: search, add, change, remove after **yes** |
| Explore and links | `/settings/links`, `/settings/links/profile` (`Explore`, `Links`, `BusinessProfile`) | Find a business and connect; requests; your QR; your business profile |
| Briefing tiles | `/invoices`, `/storefront/deliveries`, `/storefront/bookings` | The screens above |

### 3.4 What the web chat is gaining, and the app with it

The chat's plans in progress come to the app as they are built on the web
(§9): **tasks for staff**, **expenses from a receipt photo**, **quote to
cash**, **restocking that drafts itself** ([five flows](chat-five-more-flows.md),
phases 3 to 6), **paying people and payroll** from the chat
([paying people](chat-pay-people.md)), and **riders** as participants
([riders](chat-riders.md)). Each adds cards, sheets and screens on the same
pattern; the app's milestone for each follows the web's.

### 3.5 Where the app opens a browser, and why

Only where the web chat itself sends a person to a page that the app cannot
draw, each for a reason that is not the app's to remove:

| When | Why a browser |
| --- | --- |
| `connect <service>` for a service that signs in (Google and others) | OAuth runs in a browser by the provider's rules: the chat's one-time connect link (`/c/<token>`, [decision 0058](../decisions/0058-chat-as-a-way-in.md)) opens in the phone's browser tab and comes back to the app. A key-based service (Paystack, Termii…) gets a secure form in the app, as Telegram's Mini App is, never a chat message |
| A saved workflow's **open it** | The workflow canvas, for the few who want to change a draft's steps by hand; everything else about a workflow is done in the chat |
| `plan` then **Pay** | The chat sends a checkout link today. Apple and Google have rules on paying for digital subscriptions inside an app; until §13 question 5 is settled the app shows the plan and opens the checkout in the browser, as the web chat does |

**Never in the app:** a copy of the business kept on the phone (§5.5).

### 3.6 Signing up, and the app's own settings

- **Signing up in the app**, as on Taskiem's WhatsApp number: a stranger
  proves their phone with a code to their WhatsApp or Telegram, then has the
  sign-up conversation with Taskiem (what they sell, **Yes** to the terms,
  the first template), under the rules of [chat as the way in](chat-way-in.md):
  consent recorded, limits per phone, hour and country, off unless the
  operator turns it on. §13 question 4.
- **Settings**: notifications on this phone and showing what a message says
  (§5.3); the app lock; **Your phones** (§5.2); the chat PIN; your name and
  email; the chats you have linked (WhatsApp, Telegram, Slack; on the web
  under Account, linked from `ChatDoors` and `ChatAlerts`); the chat tools
  switch for owners and admins; sign out.

## 4. How it is built

- **Flutter**, one code base for Android and iPhone, in `mobile/` in this
  repository, so a change to the API and to the app can be one pull request
  and CI checks both.
- **The API client is generated** from `api/openapi/openapi.yaml` (576 paths)
  for the paths the app uses, listed in one file. The generator is a small Go
  command in `tools/`, like `tools/sdkgen`, with the same test that fails when
  the generated file is stale. A Java code generator would add a toolchain to
  CI for one job; a hand-written client would drift from the server unseen.
- **Libraries**: one for state (Riverpod, MIT), one for routing (go_router,
  BSD-3-Clause), the phone's secure storage (Keychain, Keystore), Firebase
  Messaging (Apache-2.0) for notifications, the platform's passkey APIs. Each
  is checked by the licence gate: `tools/licencecheck` learns `pubspec.lock`
  (decision 0007). Inter, the typeface, is already allowed under its font
  licence for the web; the exception is widened to the app's bundled font,
  which needs the founder's approval as the policy says.
- **Taskiem's look**: the colours, type sizes and motion timings are
  generated from `web/src/styles.css` and `web/src/motion.css` into a Dart
  theme, with a staleness test, so pine and lime never drift between the two.
  The rules of [Building screens in Taskiem's look](../brand/screens.md) apply
  to the app as written: phone first at 360 px, rows not tables, lime only on
  pine, the mark from the brand's own files, light and dark, motion turned
  down when the phone asks.
- **Plain words.** The words an owner never sees (Appendix C of
  [no-technical-steps](no-technical-steps.md)) are checked in the app's
  strings by the same list, run from a Dart test.
- **Phones it must work on.** Android 8 (API 26) and later, iPhone on iOS 16
  and later. The budget is set on a cheap Android phone with 3 GB of memory:
  the app's download under 15 MB (split per processor), opening to the
  conversations list in under 2 seconds, and no image fetched larger than it
  is shown.

## 5. What the server needs

Each is a change of its own, with its tests, before or with the milestone
that needs it.

### 5.1 S1: what the app is told about itself

`GET /v1/app/config` (no session): the lowest app version the server still
serves, and whether passkeys and notifications are on. An app older than that
asks to be updated rather than failing in ways nobody can see.

### 5.2 S2: staying signed in on a phone

Today a session lasts 12 hours (`sessionTTL`, `api/auth.go`). On a phone that
means signing in again every morning, which no one will do twice.

- **A device session**: asked for at sign-in (`device: {name, platform,
  install_id}` beside `bearer: true`), kept on the server as a session with a
  longer life (30 days without use, renewed by use, 180 days at most), listed
  under Account as **Your phones** with **Sign out this phone**, and ended by
  the same things that end a session today (a role ending, a password
  changed, signing out everywhere). Audited like a sign-in.
- **The token** is kept in the phone's secure storage, never in plain files
  or logs. The app locks itself after 5 minutes away and opens with the
  phone's own fingerprint, face or PIN; that lock is on the phone and is not
  the server's step-up.
- **Step-up is unchanged.** What asks for a fresh proof today (a payout
  approval under a policy, keys) asks the same of the app: a passkey, an
  authenticator code, or the chat PIN.
- **Dormancy** follows chat bindings ([decision 0058](../decisions/0058-chat-as-a-way-in.md)):
  a phone number can be given to someone else. A device session unused for
  30 days ends; signing in again with a chat code proves the number again.

### 5.3 S3: chat notifications to the app

[Chat push](chat-push.md) sends Web Push to browsers. The app needs Firebase
Cloud Messaging, which also carries notifications to iPhones through Apple.

- `push_subscriptions` gains a `kind` (`webpush`, `fcm`) and an FCM token for
  the app's rows. `PUT /v1/me/push` takes either. Everything else in the chat
  push spec stays: who gets a push, when, the words, the switch for showing
  what the message says, one notification per conversation, a muted
  conversation silent, devices removed when the last role ends.
- **What is pushed** starts with a chat message (M4), alerts and approvals
  that come as chat messages included. A new order and an approval waiting
  follow with their screens (M7, M8), as chat push §10 foresaw: each a call
  from `tellHolders` with its own words, opening its screen in the app.
- **The sender** is `engine/push` with a second transport, behind the same
  egress guard and metrics. Its service-account key is a secret like any
  other (`TASKIEM_FCM_CREDENTIALS`), and a deployment without it sends
  nothing to apps.
- **Phones without Google's services** (some Huawei phones) get no FCM. The
  app tells them so on the Notifications page, and their WhatsApp or
  Telegram still tells them, as it does today.

### 5.4 S4: what the app's screens need from the server

The screens of §3.3 use the endpoints the web's pages use today, through the
generated client. Where a web page does something in several calls the app
would repeat on a slow network (an invoice with its payments and the
customer's credit), the server gains one read that answers it all, used by
both. Each is listed in the milestone that needs it.

PDFs (an invoice, an order's documents) come from the endpoints the web uses
and are shown and shared with the phone's own viewer and share sheet.

Connecting a key-based service in the app's own secure form posts to the
connect link's endpoint (`POST /v1/connect/{token}`), as the web's connect
page and Telegram's Mini App do, so the key never becomes a chat message.

### 5.5 S5: what is kept on the phone

The rule of [offline](offline.md) holds: **nothing of the business is kept
on the phone** in the first release. Conversations, money and orders are held
in memory while the app is open and fetched again when it opens. What is
kept: the token (secure storage), the person's settings, a draft being typed
(as the web keeps one), and a message not yet sent until it is sent or
deleted. A cache of conversations for speed and bad signal is a later
decision of its own (§13, question 6): what is kept, for how long, encrypted
how, and wiped when.

### 5.6 S6: links that open the app

`https://<host>/chat`, `https://<host>/chat?with=…` and the addresses of the
screens in §3.3 (`/invoices/{id}`, `/trade-orders/{id}`,
`/storefront/orders/{ref}`, `/bills/{id}`, `/purchases/{id}`) open in the app
when it is installed, so a link in a WhatsApp message lands in the app (Android App Links, iOS Universal Links). The server
serves `/.well-known/assetlinks.json` and
`/.well-known/apple-app-site-association` from configuration. The same files
let the platform passkey APIs use the web's passkeys in the app; the server's
WebAuthn origins gain the Android app's signing-key origin.

## 6. The chat in the app

- **Live**: while the app is open it holds `GET /v1/chat/stream` open, with
  `Last-Event-ID` on reconnecting, and polls as the web does when the stream
  is refused. In the background it holds nothing; notifications take over.
- **Sending** works as the web's outbox does: the message shows at once with a
  clock, each send carries a client id so a retry is never a second message,
  and a message that did not go shows **Not sent** with Retry.
- **Files**: photos are made smaller on the phone before they are sent;
  voice notes are recorded in the format the server already takes; every
  file is scanned by the server as today.
- **Chat tools** come over phase by phase in the order they were built for the
  web (§10, M6), each with the web's tests as its list of what must work.

## 7. Privacy and security

- The token never leaves secure storage except in the `Authorization` header;
  the app pins nothing to a certificate (the platform's trust is used) but
  refuses plain `http`.
- No analytics or crash reporting library that sends data to a third party
  in the first release. Crashes are reported to Taskiem's own server
  (`POST /v1/app/crash`, no message content), with the person's consent.
- Screenshots are blocked on Android (`FLAG_SECURE`) while a payout approval
  card is open with its step-up; elsewhere the phone's own rules apply.
- The stores' privacy forms (Google's Data safety, Apple's privacy labels)
  are filled from this section and kept in `mobile/STORE.md`; the NDPA
  review covers the app as part of the product.

## 8. Tests

- **Unit and widget tests** for every screen, in light and dark, at 360 px
  and with large text; **golden images** of the chat's bubbles and cards,
  compared with the web's designs.
- **Against the real server**: the app's integration tests run against the
  same server `web/e2e/serve.sh` starts, on an Android emulator in CI. The
  chat's scenarios (two businesses talk, a message that did not go, a
  reaction both see) are the web's e2e scenarios, kept as one list both
  suites follow.
- **The client** is checked against the API description (§4) on every build.
- **Accessibility**: TalkBack and VoiceOver labels on everything that can be
  pressed, checked by Flutter's semantics tests; every touch target at least
  48 px.
- **Real phones** before each release: a cheap Android phone, a mid-range
  one, an iPhone; the checklist in `mobile/RELEASE.md`.

## 9. Keeping two chats the same

The web and the app will both show the chat. They drift unless something
stops them.

1. **The server owns behaviour.** Who a message goes to, what a card says,
   which tools a role has: decided by the server and given to both. A rule
   written only in a client is a bug.
2. **A chat feature is done when it is in both**, or when its spec says why
   the app does not have it. The spec's **As built** says which.
3. **Every web link in the chat is a screen in the app.** A test lists the
   addresses the web chat links to (from `lib/tools.ts` and the chat's pages,
   as §3 was drawn up) and fails when one has no screen in the app and is not
   in §3.5. A new tool or card on the web cannot quietly send app users to
   the web.

## 10. Milestones

Sizes: **S** a few days, **M** one to two weeks, **L** three weeks or more,
for one engineer who knows Flutter.

| | Milestone | Size | Done when |
| --- | --- | --- | --- |
| M0 | **People and decisions** (§11, §13) | — | The name is cleared, both store accounts and the Firebase project exist, decision 0066 is accepted |
| M1 | **Foundation**: `mobile/`, the generated client and theme, CI building an Android app, the licence gate for Dart, S1 | M | A build from `main` installs on a phone and shows the sign-in screen in Taskiem's look |
| M2 | **Signed in, and staying signed in**: chat code, password, passkey; S2; the app lock; the business switcher; the app's settings and signing up (§3.6) | M | An owner signs in with a WhatsApp code on a real phone and is still signed in a week later |
| M3 | **The chat** (§3.1, §3.2): chats, a conversation, sending, files, voice notes, the stream; customers' conversations; `@Taskiem` with every command, reply buttons, voice notes, approvals with the chat PIN, and connecting (§3.5) | L | Two businesses chat from two phones; an owner sets up their shop, adds products and publishes a workflow by talking to `@Taskiem` in the app; every web chat e2e scenario on the shared list passes |
| M4 | **Notifications and links** (S3, S6): the switch, **Your phones**, the icon's count, links that open the app | M | A message to a closed app shows on a real Android phone's lock screen in under 5 seconds and opens that conversation |
| M5 | **Linked businesses** (§3.3): Explore, cards, linking, your profile, the tools' picker and sheets | M | Two businesses find each other and link from the app alone |
| M6 | **Selling** (§3.3): products and stock, Sell, Handover, services and bookings, price lists and deals | L | A shop keeps its products, stock and prices from the app alone for a week |
| M7 | **Orders and deliveries** (§3.3): orders from linked businesses and from customers' messages, deliveries and availability, their cards | L | An order goes from a buyer's message to delivered and received with both sides in the app |
| M8 | **Money** (§3.3): invoices (new, send, payments, PDF), bills and Pay from Taskiem, purchases and suppliers, money and matching, approvals with step-up, tables | L | An order is invoiced, sent, paid and matched, and a bill paid with a passkey, from the app alone |
| M9 | **Android release**: closed testing with the design partners, then the Play Store | M | Ten businesses run their day from the app for two weeks without opening the web, with no lost message and no crash a person reports |
| M10 | **iPhone release**: TestFlight, then the App Store | S–M | The same, on iPhones |
| — | **What the web chat gains** (§3.4): tasks, receipts, quotes, restocking, paying people and payroll, riders | — | Each in the app within one milestone of the web |

For one engineer who knows Flutter: M1 to M4 about two and a half months,
M5 to M8 about four more, so a first Android release in about seven. Two
engineers bring it to about four: after M3, one takes M5 and M6 while the
other takes M7 and M8, since they divide by screen.

## 11. What needs people

| # | Item | Who | Unblocks |
| --- | --- | --- | --- |
| A1 | **The name.** "Taskiem" is a working name pending a trademark search (L3, [what needs people](../needs-people.md)); a store listing and an app ID are hard to change once people have installed the app | Leadership, counsel | Everything public |
| A2 | Google Play developer account (an organisation account needs a D-U-N-S number) | Founders | M9 |
| A3 | Apple Developer Program as an organisation (also a D-U-N-S number) | Founders | M10 |
| A4 | A Firebase project for Taskiem, and its data-processing terms (notifications pass through Google, as Web Push in Chrome already does) | Founders | M4 |
| A5 | App signing keys, held as the release secrets and backed up offline | Founders, ops | M9 |
| A6 | Store listing: words, screenshots, the privacy policy's address, a support email | Founders, design | M9 |
| A7 | A Flutter engineer (or two) | Founders | M1 |
| A8 | Three test phones: a cheap Android, a mid-range Android, an iPhone | Ops | M3 |

## 12. Risks

| Risk | What we do |
| --- | --- |
| The two chats drift apart | §9: the server owns behaviour, a feature is done in both, one scenario list |
| The app slows the chat's pace on the web | The app follows the web by up to one milestone; the web is not held back for it |
| Store review rejects the app (payments wording, the account deletion rule, reviewers who cannot sign in) | A test business and account for reviewers; account deletion from the app, as both stores require; a plan's checkout in the browser until §13 question 5 is settled |
| Phones without Google's services | Told plainly; WhatsApp and Telegram still reach them |
| The name changes after launch | A1 before M9; the app ID chosen to survive a new display name |
| A lost or shared phone | The app lock, device sessions listed and ended from the web, nothing of the business kept on the phone |
| The screens of §3.3 are a large part of the web app to build again | Built from the web's own screens and endpoints, the everyday work only; set-up is done by talking to `@Taskiem`, which costs the app nothing per command (§3.2); M6 to M8 can be divided between two engineers |
| A web chat link with no screen in the app | The test of §9, point 3 |
| Cost of two stores and their reviews on every release | Releases every two weeks at most, from `main`, by CI |

## 13. Decisions to take

1. **The app at all, and in Flutter** (decision 0066). The case against is in
   0066 too: the web app on the home screen, with chat push and offline, may
   be enough, and the critical path is still the people items of the chat-first
   strategy, not code.
2. **Android first**, iPhone released after (recommended), or both at once.
3. **The app is the web chat, all of it** (settled by the founder, 11
   November 2026): §3, opening a browser only as in §3.5.
4. **Signing up in the app** (§3.6), as on Taskiem's WhatsApp number
   (recommended), or signing in only, with sign-up left to the chat apps and
   the web.
5. **Paying for a plan from the app.** The web chat's **Pay** opens a
   checkout; Apple's and Google's rules on digital subscriptions bought in an
   app need counsel's reading for a business tool sold in Nigeria. Until then,
   the checkout opens in the browser (§3.5).
6. **A cache of conversations on the phone** (§5.5): not in the first release
   (recommended), or a decision of its own before M3.
7. **Device sessions** (§5.2): 30 days unused, 180 at most (recommended), or
   other numbers.
8. **Notifications through Firebase** (§5.3), including for iPhones, rather
   than Apple's service directly (recommended: one transport, one key).
