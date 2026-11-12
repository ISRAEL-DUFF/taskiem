# The Taskiem phone app (Flutter): a chat app, complete in itself

Status: **agreed with the founder** (2026-11-11; every question in §13
settled). Nothing here is built. Decision [0066](../decisions/0066-a-flutter-phone-app.md)
(accepted). It builds on [chat push](chat-push.md), [offline](offline.md),
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
  Google, invites staff, sends invoices, records expenses
  and cash, keeps tables, approves payments, switches business and language,
  and talks by voice note.
- **The conversations with linked businesses and customers** carry the trade:
  orders, deals, deliveries, bills, payments, with every chat tool and its
  cards and sheets.
- **The screens those lead to** (an invoice, an order, a product, a bill)
  are the app's own, built from the web's.

The app does not send people to the web. It opens a browser only where the
chat itself does, for a reason the app cannot remove (§3.5).

**It looks, moves and sounds exactly like the web chat** (§4.1). The demo
and advert films are made from the web chat, so a person who saw a film and
opens the app must see the same thing: the same screens, colours, type,
cards, sheets, motion, timings, sounds and haptics. The web chat is the
design; the app is checked against it, screen by screen and moment by
moment, on every change.

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
- **Android and iPhone together.** Flutter builds both from the same code;
  each milestone is built and tested on both, and both are released together
  (§10). What the iPhone needs beyond the code is in §4.2.

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
| Set-up | `connect <service>`; `invite …` (staff, with their role); `email`; `switch <business>`; `language …`; the chat PIN |
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

As built (12 November 2026): in Taskiem's conversation a connect link and a
draft's address are pressed to open them (`lib/chat/connect.dart`): a
service that signs in, and the canvas, in the browser tab; a service with
keys, in a form in the app. Asking from the web's or the app's
conversation needed the server to allow it (migration 00425).
| (nothing about billing) | Billing is off, on the web too (settled by the founder, 11 November 2026): the app has no plan, payment or checkout, and the chat's `plan` and `pay` are not offered while billing is off |

**Never in the app:** a copy of the business kept on the phone (§5.5).

### 3.6 Signing up, and the app's own settings

- **Signing up and signing in, both in the app** (settled by the founder,
  11 November 2026). Signing up is as on Taskiem's WhatsApp number: a
  stranger proves their phone with a code to their WhatsApp or Telegram, then
  has the sign-up conversation with Taskiem (what they sell, **Yes** to the
  terms, the first template), under the rules of
  [chat as the way in](chat-way-in.md): consent recorded, limits per phone,
  hour and country, off unless the operator turns it on. The server gains
  `app` as a way in beside `whatsapp`, `telegram` and `slack`
  (`tenant_onboarding.source`, `taskiem_signups_total`).
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
  which needs the founder's approval as the policy says. The app's own
  packages need not meet the server's and web's gate in full (the founder,
  11 November 2026): MPL-2.0, file-level copyleft, is allowed for `pub`
  packages only.
- **Taskiem's look** is the web chat's, exactly: §4.1.
- **Plain words.** The words an owner never sees (Appendix C of
  [no-technical-steps](no-technical-steps.md)) are checked in the app's
  strings by the same list, run from a Dart test.
- **Phones it must work on.** Android 8 (API 26) and later, iPhone on iOS 16
  and later. The budget is set on a cheap Android phone with 3 GB of memory:
  the app's download under 15 MB (split per processor), opening to the
  conversations list in under 2 seconds, and no image fetched larger than it
  is shown.

### 4.1 The same as the web chat, in look, motion and sound

The films are recorded from the web chat ([video](../../video/README.md):
the scenes are drawn from the app as built, and `video/recording` drives the
real web chat on the demo world). So the web chat is the design, and the app
mirrors it. This is a requirement of every milestone, not a polish step.

**Where the look comes from.** The web chat's own sources, read by a
generator in `tools/` (the same command that writes the API client), which
writes the app's theme; a test fails when the web changed and the theme was
not written again:

| Web | What the app takes from it |
| --- | --- |
| `web/src/styles.css` | Every colour token, light and dark (pine, lime, the tones, the surfaces), spacing, radii, shadows, type sizes and weights |
| `web/src/motion.css` | Every moment's duration (`--t-press` 90 ms, `--t-quick` 150, `--t-tick` 260, `--t-picker` 280, `--t-sheet` 360, `--t-land` 400, `--t-roll` 450, `--t-arrive` 600, `--t-cheer` 900, `--t-glow` 1400, `--t-stagger` 40 between rows) and the four curves (`--spring`, `--spring-settle`, `--spring-bounce`, `--ease-out`), used as the same cubic Béziers |
| `web/src/kit.css`, `web/src/chat.css` | The kit's parts and the chat's: bubbles and their one sharp corner, the initials' tiles, the pill, the composer, the tools button, sheets, cards, rows, chips, pills, the saving tick |
| `web/src/kit.tsx`, `lib/moments.ts` | The haptics (a 12 ms buzz where the web vibrates) and the sounds (the same tones, made the same way), with the same switches |
| The Inter font file the web bundles | The same font, at the same sizes, so lines break where they break on the web |

**How it is drawn.** Every screen from the app's own parts, built to match
the web's kit, never Flutter's Material or Cupertino look: no ripple, no
Material shadows or app bars, the web's press (to 97%), the web's sheet that
rises on a phone and is pulled down to close, the web's toasts. The phone's
status and navigation bars take the chat's colours. Behaviour is the web
chat's too: swipe to reply, long-press, hold to record, search, pin, mute,
archive, the picker resting on the message bar, motion turned down when the
phone asks (nothing moves, as on the web).

**How it is checked: the parity test.** For each screen and each moment, the
web chat and the app are put in the same state on the same demo world, on
the same screen (390 × 844 and 360 × 640, at the phone's density, light and
dark), and compared:

- **Screens**: a screenshot of each, compared by a perceptual difference
  with a small threshold (text drawn by two engines is never identical to the
  pixel); a difference above it fails the check, with both pictures and the
  difference kept.
- **Moments**: each motion (the picker opening, a card growing into its
  sheet, a diary line landing and its glow, a number rolling, an order
  arriving, the tick, a good moment) captured at fixed points of its time
  (0, 25, 50, 75 and 100%) on both, and compared the same way.
- **The list** of screens and moments is the films' scenes and acts
  (`video/films/chat/script.md`, `video/recording/tour.mjs`), and grows with
  every card, sheet and screen the web chat gains.
- The check runs in CI on every pull request that touches `web/src` or
  `mobile/`, beside axe; a pull request that changes how the web chat looks
  shows the app's difference too, so the two change together.

**Where the two cannot be the same**, and are left to the phone, each named
so it is not mistaken for a difference to fix: the keyboard and its emoji,
the phone's own share sheet, camera and photo picker, notifications on the
lock screen, the system's text-selection handles, and the browser tab of
§3.5.

### 4.2 What the iPhone needs beyond the code

The same code runs on both; around it, the iPhone needs:

- the Apple Developer Program account (A3) and the app's signing, from M1;
- builds on macOS: CI builds and tests the iPhone app on a macOS runner on
  every pull request touching `mobile/`, and runs the parity test on an
  iPhone simulator beside the Android emulator;
- Apple's notification service behind Firebase: an APNs key in the Firebase
  project (A4);
- the `apple-app-site-association` file for links and passkeys (S6);
- App Store review, with a test business for the reviewers;
- a real iPhone in the test phones (A8), from M3.

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
  the server's step-up. The lock covers every screen, whatever is open on
  top of the chats, and keeps them as they were underneath. Signing out, or
  the session ending, closes every screen; switching business closes them
  and starts the chats afresh for the other business.
- **Step-up is unchanged.** What asks for a fresh proof today (a payout
  approval under a policy, keys) asks the same of the app: a passkey, an
  authenticator code, or the chat PIN.
- **Dormancy** follows chat bindings ([decision 0058](../decisions/0058-chat-as-a-way-in.md)):
  a phone number can be given to someone else. A device session unused for
  30 days ends; signing in again with a chat code proves the number again.

**As built** (M2): migration 00424 gives sessions a device (`device_id`,
`device_name`, `device_platform`) and `last_used_at`. `POST /v1/auth/login`,
`/v1/auth/passkey`, `/v1/auth/chat/verify`, `/v1/auth/chat/link` and
`/v1/signup` take `device` beside `bearer: true` (`api/devices.go`); such a
session lasts 30 days, `taskiem_auth_session_touch` renews it while it is
used (at most once an hour, never past 180 days from signing in, never
moving when it signed in, which step-up reads), and signing in again on the
same phone ends its earlier session. `GET /v1/me/devices` lists Your phones,
`DELETE /v1/me/devices/{id}` signs one out (audited `auth.device_removed`).
Signing out everywhere, a password changed and a role ending end them, as
they end every session. The 30-day dormancy of an unused phone is the
session's own expiry. Tested in `api/devices_test.go`.

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
decision of its own (§13, question 7): what is kept, for how long, encrypted
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

- **The parity test** (§4.1): every screen and moment against the web chat.
- **Unit and widget tests** for every screen, in light and dark, at 360 px
  and with large text.
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
| M1 | **Foundation**: `mobile/`, the generated client and theme, the app's kit of parts matching the web's, the parity test in CI, the licence gate for Dart, S1 | M | A build from `main` installs on a phone, and its sign-in screen passes the parity test against the web's |
| M2 | **Signing up and signing in**: sign-up as on WhatsApp; chat code, password, passkey; S2; the app lock; the business switcher; the app's settings (§3.6) | M | A stranger signs up from the app and is still signed in a week later, on a real phone |
| M3 | **The chat** (§3.1, §3.2): chats, a conversation, sending, files, voice notes, the stream; customers' conversations; `@Taskiem` with every command, reply buttons, voice notes, approvals with the chat PIN, and connecting (§3.5) | L | Two businesses chat from two phones; an owner sets up their shop, adds products and publishes a workflow by talking to `@Taskiem` in the app; every web chat e2e scenario on the shared list passes; every screen and moment of the chat film's acts passes the parity test |
| M4 | **Notifications and links** (S3, S6): the switch, **Your phones**, the icon's count, links that open the app | M | A message to a closed app shows on a real Android phone's lock screen in under 5 seconds and opens that conversation |
| M5 | **Linked businesses** (§3.3): Explore, cards, linking, your profile, the tools' picker and sheets | M | Two businesses find each other and link from the app alone |
| M6 | **Selling** (§3.3): products and stock, Sell, Handover, services and bookings, price lists and deals | L | A shop keeps its products, stock and prices from the app alone for a week |
| M7 | **Orders and deliveries** (§3.3): orders from linked businesses and from customers' messages, deliveries and availability, their cards | L | An order goes from a buyer's message to delivered and received with both sides in the app |
| M8 | **Money** (§3.3): invoices (new, send, payments, PDF), bills and Pay from Taskiem, purchases and suppliers, money and matching, approvals with step-up, tables | L | An order is invoiced, sent, paid and matched, and a bill paid with a passkey, from the app alone |
| M9 | **Release, Android and iPhone**: closed testing with the design partners (Play's testing track and TestFlight), then the Play Store and the App Store | M | Ten businesses, on both kinds of phone, run their day from the app for two weeks without opening the web, with no lost message and no crash a person reports |
| — | **What the web chat gains** (§3.4): tasks, receipts, quotes, restocking, paying people and payroll, riders | — | Each in the app within one milestone of the web |

For one engineer who knows Flutter: M1 to M4 about two and a half months,
M5 to M8 about four more, and the release a few weeks after: about seven
months to both stores, the iPhone's share of the work (§4.2) included. Two
engineers bring it to about four: after M3, one takes M5 and M6 while the
other takes M7 and M8, since they divide by screen.

### As built: M1 and M2 (11 November 2026)

- **M1**: `mobile/` (Flutter, Android 8+ and iOS); `tools/mobilegen` writes
  the theme from the web's CSS and copies the brand's files; the web's Inter
  unpacked from its own files; the parity test in two halves
  (`web/e2e/parity.spec.ts`, `mobile/test/parity_test.dart`);
  `GET /v1/app/config`; the licence gate reads `pubspec.lock`;
  `.github/workflows/mobile.yml`.
- **M2**: device sessions (§5.2, migration 00424); the generated API client
  (`mobile/api.txt` → `lib/api/api.g.dart`); signing in with a password or a
  WhatsApp or Telegram code, signing up as the web does, staying signed in
  (the token in the phone's secure storage, checked on opening), signing
  out, Your phones, the app lock, and switching business (`GET
  /v1/me/businesses`, `POST /v1/me/switch`: the same session in the other
  business, never a fresh sign-in). The app opens on its settings until the
  chat comes (M3). Parity: sign-in, its chat steps, a wrong password and
  sign-up, light and dark, at two sizes (20 pictures). The parity test found
  one web bug, fixed: the code's field did not take the focus.
- **Reviewed** after building (an independent read of the server side, and
  the app's own): fixed that signing in again on a phone ended its earlier
  session only in the business signed in to; that a single sign-on session
  could switch into another business (and so skip its passkey rule: such a
  session now does not switch); races between two sign-ins on one phone or
  two switches from one session (one live session per person per phone is
  now a unique index; switching ends the old session first); renewal before
  the membership check; and in the app, a session ended elsewhere not
  noticed while open, no network timeout, a held session (403) forgotten on
  opening, the lowest served version never checked, Android's back leaving
  the app from signing up, the status bar unreadable on settings, errors
  lost, Appearance (light, dark, match the phone) missing, and two CI gaps.
- **Not yet in M2**: passkeys (they need the app's link to Taskiem's address,
  §5.6, and its signing key: with M4; the button says so); signing up with a
  phone number instead of an email (the chat apps' way: a stranger's
  WhatsApp needs an approved code template, a people item); a person with no
  business yet who accepts an invitation on the phone gets a 12-hour session,
  not a phone's (invitee sessions keep no device); an administrator held to
  passkeys who has none can sign in on the phone but do nothing until they
  add one on the web; the app's picture in the phone's list of open apps is
  not hidden; the terms' checkbox has no parity picture (the browser tests'
  server asks no terms); the app's CI has not yet run on GitHub.

### As built: M3 so far (11 November 2026)

M3 is built in slices, each held to the web by the parity test with the
demo world's data (recorded answers, the clock stopped: `mobile/README.md`).

- **The chats** (`lib/chat/chats.dart`): the web's list as it draws on a
  phone: the head, search, the chips, Taskiem's row with what needs you, the
  businesses asking to link, the conversations (logo or initials, time,
  ticks, preview, unread, pin, mute) and Find a business. The web's icons and
  the words a card's event is said in are generated from the web
  (`tools/mobilegen`); its previews, times and tones are ported
  (`lib/chat/words.dart`, held to the web's answers by `words_test.dart`).
- **A conversation** (`lib/chat/conversation.dart`): opened from the list in
  its place, as the web does on a phone (no transition); the head (back,
  logo, name, the link's role or typing…), the wallpaper with its dots, the
  days, where the unread begin, the bubbles (the tail's corner on the first
  of a run, the time in the corner of the last line, two ticks, blue once
  read) and the box (tools, attach, camera, the mic, Send once there are
  words). Opening reads it; what is newer is asked for every 3 seconds;
  a message shows at once with a clock and the server's copy takes its
  place, or it shows not sent with Retry and Delete (the same client id, so
  a retry is never a second message). Parity: Mama Tunde's Market's
  conversation, light and dark, two sizes.
- **Cards** (`lib/chat/cards.dart`): a document's card as the web's
  (cardframe.ts ported: the kind and reference, the state's pill, what
  happened, the amount, an order's or a delivery's progress), its View
  button and its own steps on the document's newest card (accept, the
  delivery's steps, accept a change, all came, confirm a rider's delivery,
  Pay an invoice), every card of a document showing where it is now; our own
  changes as diary lines, those made within 5 minutes of each other folded
  under the newest. The thread opens with the first unread 40 px under the
  top, the day's chip stays at the top while its day is under it, and the
  round button takes the reader to the newest, with how many came meanwhile.
  A new message is followed to the newest message, not past it: with
  Taskiem's cards after it, the answer ends at the bottom of the view and
  the cards stay below (useThread's toNewest).
  Parity: Bayo Rice Mills' conversation (an order and an invoice), light and
  dark, two sizes. Chrome breaks a line after a hyphen before a digit and
  Flutter does not: card titles are given the break (`breakAfterHyphens`).
- **The live stream** (`lib/chat/live.dart`): `GET /v1/chat/stream`
  followed while the app is in front (stopped behind another app), as the
  web's ChatLive: messages new and changed, ones gone, a conversation's
  settings, who is typing; a stream quiet for 30 seconds or dropped is
  opened again (after 30 seconds) with Last-Event-ID, so nothing is missed
  or told twice. While it is connected the conversation asks for what is
  newer every 15 seconds and the newest page every 30 (3 and 9 without
  it, which also bring our ticks turning blue), and reads a burst of
  incoming messages once; the list is asked again on a change and every
  30 seconds. A phone's bearer token is taken by the stream (checked
  against the demo world). The sound and the phone's notification for a
  message are M4's.
- **Photos, PDFs and voice notes** (`lib/chat/attachments.dart`,
  `lib/chat/composer.dart`): shown as the web shows them (a photo in its
  bubble up to 320 px tall, a PDF as its name and size, a voice note in
  the browser's own player, which the app draws as Chromium does and plays
  with `just_audio`), each this business's own copy; sent as the web sends
  them: the clip's menu (Photo, Document, Camera) and the camera beside it
  (the phone's gallery, files and camera: `image_picker`, `file_selector`;
  a photo as a JPEG, so an iPhone's HEIC goes), the file waiting above the
  box for its caption, then shown at once with a clock and retried with the
  same client id; a voice note held and released (sent at once), slid left
  (thrown away) or tapped (until Done, then with a caption), recorded as
  AAC in MP4 (`record`). The phone's permissions: the microphone (Android),
  the camera, photos and microphone with their reasons (iPhone). Parity: a
  conversation with a photo, a voice note and a PDF, at its unread and at
  its end, the clip's menu open, and a photo waiting for its caption.
  Found on the way: the web's voice note player was collapsed to its menu
  button (a percentage width in a bubble sized by its content), fixed; the
  demo world's stand-in storage signed no links, so no demo file could be
  read, fixed; and the app's release build lacked the INTERNET permission
  (only debug and profile builds had it), fixed. A voice note recorded on
  the web is WebM or Ogg Opus, which an iPhone does not play: it needs the
  server to keep an MP4 copy (not yet).
- **`@Taskiem`, its conversation** (`lib/chat/taskiem.dart`), as the web's
  TaskiemPane: opened from the list's Taskiem row; its history from every
  channel (diary lines, Taskiem named on the first of its answers, *bold*
  marked, a long answer folded with Read more, the channel a message came
  by, "how did we do?" as its sales chart); words sent at once with a clock
  ("typing…" while Taskiem answers) and given back to the box if they could
  not go; a reply button sends its id with its title as the words, the
  choice ticked after; the person's ticks blue once Taskiem has answered; a
  photo or a voice note sent as one on WhatsApp; what Taskiem said since
  the person last looked marked, and counted on the list's row (kept while
  the app runs: not yet across restarts). The thread is now one piece for
  every conversation (`lib/chat/thread.dart`, as the web's ChatStream and
  useThread), the business's conversation moved onto it with its pictures
  unchanged.
- **Taskiem's cards** (`lib/chat/alerts.dart`): after its conversation, as
  the web's: the day's briefing on pine (the greeting, yesterday's sales
  with the week's bars, what is owed, low and out today, who is in and
  late, the tasks due, Got it putting it
  away for the day), then a card for each thing that needs the person
  (overdue, a payment to match, a new order, low stock, a rider, a deal, a
  delivery to confirm, a customer's draft order), each with its next step
  and Later (a row that wraps, as the web's: the step grows to fill its
  line, Later onto the next when there is no room), low stock with "Give
  this to…" for those who give tasks (the web's GiveTask form in a modal
  from the bottom: what is to be done, already said; who, a person or
  anyone in a role; when, with its day or weekday and time; a photo when
  done; `lib/chat/give_task.dart`), and approvals (who asks for what, the amount, to whom and the
  account, a first payment or a step-up said, Decline and Approve). Later,
  Got it, Send reminder, Agree and Confirm work from the card. Parity: the
  conversation, its cards and its approval, light and dark, two sizes.
- **Approvals** (`lib/chat/approve.dart`, `lib/kit/sheet.dart`): Approve or
  Decline on the card opens the web's sheet, which every tool will open in
  too (up from the bottom over a dimmed, softly blurred veil, the grab bar,
  the head with Close, a foot of two buttons; rising on the spring, leaving
  down; closed by the veil, Close, Back or a pull): what is asked, who asks,
  every field of the request, a first payment's warning, a comment, and the
  confirmation the approval's rule asks for, as the server says it: a code
  from the person's authenticator (a passkey comes with M4; a decision that
  needs one says to make it on a device with it). The tick, then the card
  goes. Parity: the sheet over Taskiem's conversation, light and dark.
- **Customers' conversations** (`lib/chat/customer.dart`), as the web's
  CustomerChat: the list's Customers (how many wait; archived ones under
  Archived), a conversation (who they are, their number, what they owe;
  their messages and the business's replies with who sent them and how far
  each got: a clock, one tick, two, blue once read, or not delivered and
  why), read on opening, followed every 4 seconds; a reply shown at once
  and given back to the box if it could not go; a customer who sent STOP
  told and not replied to; after the 24 hours, how a reply goes and its
  900 characters. Start an order is drawn (the order it opens is M7's).
  With it the list's first-day parts: no linked businesses yet, and whether
  other businesses may find this one (Yes or Not now). Parity: a shop with
  two customers who wrote, its list and a conversation, recorded from the
  browser tests' server (which takes a customer's WhatsApp message).
  Found: in the chat every initials avatar is pine with lime letters,
  whatever its name's tone, because chat.css's `.chat-avatar` comes after
  kit.css's tones; the app draws what the web draws.
- **A message's menu** (`lib/chat/menu.dart`, `lib/kit/modal.dart`), as
  the web's BubbleShell on a phone: a message held opens the reactions in a
  pill over it and what can be done in a card under it, each on the
  bubble's side and kept on the screen (a tap elsewhere, Back or a scroll
  closes it). A reaction is shown at once and sent (one each, the same
  again takes it back, also from its chip under the bubble); Reply puts
  the message over the box ("Replying to…", Cancel) and sends `reply_to`,
  the answer quoting it, as a swipe to the right also does; Copy says
  Copied; Forward picks other linked businesses; Star marks it by its time;
  Message info says when it was sent, delivered and read and who reacted;
  Delete for me asks first, in the web's confirmation (`showTkConfirm`, the
  web's Modal from the bottom), then says so in the web's toast. To a
  screen reader a bubble is one node with its own labels kept and a long
  press for the menu. Parity: the menu open, replying, and the
  confirmation, light and dark, two sizes. Found on the way: a reaction
  chip hangs 12 px under its bubble, outside what Flutter takes taps in,
  so the bubble keeps the room under it; and Flutter draws no rounded
  border of two colours (the toast's), so the toast is drawn clipped.
  The pill's "+" opens every emoji in its place (`lib/chat/emoji_panel.dart`,
  the web's EmojiPanel): a search by the web's words ("okada", "naija"),
  the recently used first, the groups as tabs (grey until chosen), a skin
  tone for the hands; the list is the web's, copied by `tools/mobilegen`
  (`emoji.g.dart`) and held to the web's tests (`emoji_test.dart`). What
  was picked lately and the tone are kept while the app runs (the web keeps
  them in the browser). Parity: the "+" open. Found: on the web the panel's
  tabs were squashed to a sliver (its column shrank them to fit), and the
  picker was placed by its loading placeholder and grew over the bubble;
  both fixed on the web, which the app copies.
- **Decline, and what a step did** (`lib/chat/cards.dart`): an order that
  came in has Decline beside Accept, as on the web; it asks first, in the
  web's confirmation ("Decline ORD-7?", red), and Cancel sends nothing.
  Every step on a card now says what it did in the web's toast, in the
  web's words ("ORD-7 accepted", "Received, and your stock is up"). A step
  the server took is done even when asking for what is newer then fails
  (it is asked again on the next turn). Held by `cards_test.dart`.
- **Older messages** (`lib/chat/thread.dart`): in every conversation (a
  business's, a customer's, Taskiem's), as the web's useThread: while there
  are older messages than those shown (a full page of 50, or the server's
  `more` for a customer), the page before the oldest is asked for as the
  reader comes within 200 px of the top, drawn above with the reader left
  where they were, and the next asked for if they are still near the top;
  a short page ends it. The web's 1 px mark at the top while there are
  older ones is kept, so the thread sits where the web's does. Held by
  `older_test.dart`.
- **A business's long message, folded** (`lib/chat/conversation.dart`),
  as the web's ReadMore: the first ten lines or 600 characters, cut where
  a word ends, then "… Read more", each tap adding twenty lines or 1,200
  characters (readmore.ts, as Taskiem's answers already were). Parity:
  Mama Tunde's conversation with a price list added to its recording
  (`chat-long`), light and dark, two sizes; held by `fold_test.dart`.
- **The chats list's answers and Starred** (`lib/chat/chats.dart`): a
  request to link answered from the list (Accept; Decline; Block, asked
  first), said in the web's toast ("Linked with Kola's Bakery"), the list
  read again; Waiting for an answer under the chats (the businesses asked,
  the requests sent to those not on Taskiem), as the web lists them. The
  Starred chip lists the messages starred, by business, read afresh each
  time it is chosen; one opens its conversation at it (`?msg=` on the
  web): the page around it, it in the middle with the web's accent ring
  for 1.8 s, and, with newer messages beyond that page, the round button
  showing "Jump to the latest messages" (nothing newer followed until
  then; sending from there goes back to the newest). Parity: the Starred
  view (the chats' recording with two starred messages added), light and
  dark, two sizes; held by `chats_test.dart`.
- **The list's search** (`lib/chat/chats.dart`), as Links.tsx: from the
  first letter the list narrows (a chat whose name or last message has a
  word beginning with it; customers by name; Taskiem by its name); from
  two, 150 ms after the typing stops, the chips go and the results show:
  the linked businesses found, the messages found (each opening its
  conversation at itself), the customers and their messages found, and
  the businesses on Taskiem that chose to be found, each with Request
  link (the web's sheet: how we trade with them, then Send request) or
  Cancel request for one waiting; a phone number or a page address is
  never searched, only offered a request sent to it. Parity: "rice"
  searched (the chats' recording with its answers added), light and dark,
  two sizes; held by `chats_test.dart`. Finding a business (the Find a
  business button) opens the web's Explore screen, not yet built.
- **Search in a conversation** (`lib/chat/search.dart`), as the web's
  ChatSearch on a phone: the head's search opens it over the conversation
  (in from the side), with Close, a pill box searched 250 ms after the
  typing stops, and the messages found as rows (who, when, the words
  around the hit with the words in bold, three lines at most); a row
  closes it and takes the thread to that message, flashed (in the thread
  already, or the page around it). The calendar beside the box goes to a
  day (the web's DayPicker: the month and its arrows, today filled in, the
  days after it not to be chosen); its first message, or "No messages on
  or after that day." Back closes the calendar first. Parity: "tomato"
  found, and the calendar open (Mama Tunde's recording with the search's
  answer added), light and dark, two sizes; held by `search_test.dart`.
  Found: on the web the calendar's grid took the site's table look (a
  rounded box, and on a phone `display: block`, so its days did not fill
  its width and the line under the weekdays stopped short); fixed.
- **Contact info and the head's menu** (`lib/chat/contact.dart`), as the
  web's ContactInfo on a phone: the picture opens it over the conversation
  (in from the side): the logo, name, how we trade, when linked; Search and
  Pin as round buttons; About (the business's tagline, area and what it
  sells, as its public card says, when it may be found); Starred messages
  (this chat's, each opening the thread at it); Media and files (the latest
  photos, opened to every photo, voice note and file); Mute as a switch;
  Archive; and End link, in red, asked first. Each setting is said in the
  web's toast. The head's ⋯ opens the web's menu: Contact info, Pin, Mute,
  Archive (not Keyboard, which is a computer's). Parity: contact info, its
  media opened, its starred messages, and the menu (the files' recording
  with the card's answer and a starred message added), light and dark, two
  sizes; held by `contact_test.dart`. Found: on the web contact info left
  a strip of the conversation showing on a phone (only search was made the
  whole screen there); fixed. The parity spec can now see a screen as a
  touch phone does (`touch`). A customer's ⋯ offers Archive (or
  Unarchive; archived, the chat goes back to the list); Taskiem's has
  nothing of its own, so on a phone it shows no ⋯ (on the web it held only
  Keyboard, hidden on a phone, and opened an empty box; fixed).
- **Where a thread opens, steadily**: a reader at the bottom stays there as
  what is below grows (Taskiem's cards arriving after its messages, a
  photo loading), unless the thread opened at the first unread or at a
  message. Found: on the web Taskiem's conversation opened above its cards
  or at their end depending on which loaded first (the parity picture
  changed between runs); fixed in useThread, and the app now opens it at
  the end, cards and all, as the web now always does.
- **Tags: "@" and "#"** (`lib/chat/tags.dart`, `tag_picker.dart`,
  `live_card.dart`), as the web's Mentions and ChatLiveCard: in a
  business's conversation "@" opens, with chat tools, the designs' picker
  over the thread (the products on the price list when it is theirs, the
  other linked businesses with how we trade, Taskiem last; without chat
  tools, the price list's products and our services; the first drawn ready
  to take; "Matches for …" as words are typed), and "#" the plain list of
  the price list's items with their prices above the box. A pick puts
  the name in the words; the things and businesses still named when it is
  sent go with the message (`mentions`), a name typed over is dropped. A
  message's tags are drawn marked: an item with its price ("#Rice, 50kg
  bag (₦78,000 a bag)"), a business that opens its conversation; with chat
  tools the first product or service tagged has its live card above the
  words (the price on the list today and what it was, our stock of it,
  Order when we buy from them). The tag logic is held to the web's own
  tests (`tags_test.dart`). Parity: "@", "#", and a tagged message with its
  card (Bayo's recording with the message added), seen as a touch phone,
  light and dark, two sizes. Not yet: businesses not linked found by
  Explore, categories, and "/invoice" and the like (with the tools);
  Order on the card says ordering is not in the app yet (the Order sheet
  is a tool's).
- **A deal's offers and counters** (`lib/chat/deal.dart`), as the web's
  ChatDeal on the newest card of an order: the back and forth so far
  (the last four steps, ours in pine, the newest bold) and when the offer
  lapses. The seller, on the buyer's offer or counter: Accept (for those
  who set prices, keeping the price for this buyer this order or 7, 14
  or 30 days; under the lowest price it waits for the owner and says so),
  Counter with a total and a word, Decline (asked first). The buyer, on
  the seller's counter: Accept it, Counter back, Cancel the order (asked
  first). A counter is read as people type naira ("755k", "₦760,000") and
  refused until it sits between the two (more than their offer, under
  their price), the share off said under it. A lapsed offer has nothing
  to answer. The buttons wrap as the web's flex row does. Held to the
  web's own deal tests and by `deal_test.dart`. Parity: Bayo's counter on
  our order, and countering it (Bayo's recording with the counter added),
  as a touch phone, light and dark, two sizes.
- **Find a business** (`lib/chat/explore.dart`), as the web's Explore:
  the chats' Find a business opens it over them (rising in): Back, the
  title, the search (a name, what they sell, or an area, searched 250 ms
  after the typing stops); with nothing typed, Near you (with our area),
  Delivery and New on Taskiem, each with Show more; each business a card
  (its logo and name, its line, its photos swiping with their words on
  them and dots under them, its words) with what can be done: Connect
  (asked at once, supplier and customer both; Requested then, Withdraw
  under it), Message (a linked one: its conversation in Explore's place),
  or Accept and Not now (it asked us). A phone number is offered a
  request sent to it; nothing found says so. "Your business in Explore"
  is a computer's (the web hides it on a phone too). Parity: Explore and
  "rice" searched, recorded from the demo world (`EXPLORE=1` in
  `tool/capture.mjs` opens it and searches as it records), as a touch
  phone, light and dark, two sizes; held by `explore_test.dart`. Found: on
  the web the search pill showed a grey box inside it (the site's input
  rule is more specific than Explore's bare one); fixed.
- **Not yet in M3**: the steps that open a tool's sheet
  (Record stock in, Reorder, Review order, Match it, Assign rider: the
  tools' sheets are M5 to M8), the documents the cards open (View order, View
  invoice, Pay: the tools' sheets and those screens are M5, M7 and M8 in
  the milestones), and the chat tools: M5 to M8.
- **Review** (11 November 2026): the bugs and gaps found reading the app
  and setting it against the web chat, and the work to finish M3, are in
  [Finishing M3](mobile-app-m3-fixes.md).

## 11. What needs people

| # | Item | Who | Unblocks |
| --- | --- | --- | --- |
| A1 | **The name.** "Taskiem" is a working name pending a trademark search (L3, [what needs people](../needs-people.md)); a store listing and an app ID are hard to change once people have installed the app | Leadership, counsel | Everything public |
| A2 | Google Play developer account (an organisation account needs a D-U-N-S number) | Founders | M9 |
| A3 | Apple Developer Program as an organisation (also a D-U-N-S number) | Founders | M1 |
| A4 | A Firebase project for Taskiem, and its data-processing terms (notifications pass through Google, as Web Push in Chrome already does); an APNs key from the Apple account in it | Founders | M4 |
| A5 | App signing keys, held as the release secrets and backed up offline | Founders, ops | M9 |
| A6 | Store listing: words, screenshots, the privacy policy's address, a support email | Founders, design | M9 |
| A7 | A Flutter engineer (or two) | Founders | M1 |
| A8 | Three test phones: a cheap Android, a mid-range Android, an iPhone | Ops | M3 |

## 12. Risks

| Risk | What we do |
| --- | --- |
| The two chats drift apart | §9: the server owns behaviour, a feature is done in both, one scenario list |
| The app slows the chat's pace on the web | The app follows the web by up to one milestone; the web is not held back for it |
| The app drifts from the web chat's look, and from the films | The parity test (§4.1) on every pull request touching either; the theme generated from the web's own CSS |
| Two engines never draw text the same | A threshold set on the first screens and kept; Inter bundled in both; differences that are the phone's own are listed (§4.1) |
| Store review rejects the app (payments wording, the account deletion rule, reviewers who cannot sign in) | A test business and account for reviewers; account deletion from the app, as both stores require |
| Phones without Google's services | Told plainly; WhatsApp and Telegram still reach them |
| The name changes after launch | A1 before M9; the app ID chosen to survive a new display name |
| A lost or shared phone | The app lock, device sessions listed and ended from the web, nothing of the business kept on the phone |
| The screens of §3.3 are a large part of the web app to build again | Built from the web's own screens and endpoints, the everyday work only; set-up is done by talking to `@Taskiem`, which costs the app nothing per command (§3.2); M6 to M8 can be divided between two engineers |
| A web chat link with no screen in the app | The test of §9, point 3 |
| Cost of two stores and their reviews on every release | Releases every two weeks at most, from `main`, by CI |

## 13. Decisions

All settled with the founder on 11 November 2026.

1. **A native app, in Flutter** (decision 0066).
2. **Android and iPhone together**: built and tested on both from M1,
   released together at M9 (§4.2).
3. **The app is the web chat, all of it**: §3, opening a browser only as in
   §3.5.
4. **Signing up and signing in in the app**: §3.6.
5. **The look is the web chat's**, in every screen, motion, sound and haptic:
   §4.1, checked by the parity test.
6. **No billing in the app.** Billing is off, on the web too; when it is
   turned on, how a plan is paid (the founder's direction: from the
   business's Taskiem wallet, internal to Taskiem) gets a plan of its own.
7. **Nothing of the business kept on the phone** in the first release (§5.5);
   a cache is a decision of its own, later.
8. **Device sessions**: 30 days unused, 180 at most (§5.2).
9. **Notifications through Firebase**, for iPhones too, through Apple's
   service behind it (§5.3).
