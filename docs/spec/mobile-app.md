# The Taskiem phone app (Flutter): the chat first

Status: **proposal for the founder's review** (2026-11-11). Nothing here is
built. The decision it asks for is [0066](../decisions/0066-a-flutter-phone-app.md)
(proposed). It builds on [chat push](chat-push.md), [offline](offline.md),
[chat tools](chat-tools.md), [chat as the way in](chat-way-in.md) and
[chat sign-in](chat-sign-in.md).

## 1. Summary

A native app for Android and iPhone, written in Flutter, for the people who
run a business from their phone: owners and their staff. **For now it is the
chat and nothing else**: Taskiem's chat, the conversations with linked
businesses and with customers, and the chat tools, with what the chat cannot
work without (signing in and notifications). Other parts of Taskiem come to
the app after the chat, one at a time, each by a decision of its own (§3.3).

- **What it is for.** Notifications that arrive when nothing is open, on
  every Android phone that has Google's services. A camera, voice notes and
  photos that feel like the phone's own. Speed on a cheap Android phone. Being
  in the Play Store, which traders trust more than "add to home screen".
- **What it is not.** It does not replace WhatsApp, Telegram or Slack as the
  way most traders use Taskiem; the strategy still starts in the chat
  ([chat-first strategy](../strategy/chat-first-nigeria.md)). It is not the
  web app on a phone: Home, Money, Orders, invoices, the builder, settings,
  billing and schools stay on the web for now.
- **What it costs.** A second client for the chat, the part of the product
  that changes most. The chat is about 17,000 lines of the web app's 88,000.
  §9 is how the two stay the same; §12 is the risk.
- **Order.** Android first (most Nigerian phones), iPhone built from the
  same code all along and released once Android is in use. Eight milestones
  (§10); the first a person can use is M3, the chat itself.

## 2. Who uses it, and for what

| Person | What they do on the phone today | In the app |
| --- | --- | --- |
| Owner | Chat with linked businesses, customers and `@Taskiem`, with the chat tools; act on the alerts and approvals the chat brings | The chat, natively |
| Staff | Chat, and their tools in it: sell, record stock, take orders, their tasks ([chat tools](chat-tools.md) phase 5) | The chat, natively, within their role |
| Rider | Deliveries, which are chat orders ([decision 0065](../decisions/0065-riders-are-organisations.md)) | With the chat when their cards are built; a mode of their own is a later question (§13) |
| Anyone, for anything else | Home, Money, Orders, invoices, settings, the builder | The web, as today; not in the app for now |

## 3. What is in it

### 3.1 The first release: the chat

| Area | What | Built on |
| --- | --- | --- |
| Sign in | A code sent to your WhatsApp or Telegram; email and password; a passkey; staying signed in on this phone (§5.2). Signing up stays in the chat apps and on the web | `POST /v1/auth/chat/start` and `/verify`, `/v1/auth/login`, `/v1/auth/passkey`, all with `bearer: true` |
| The conversations | The list: `@Taskiem` first, then linked businesses and customers, with unread counts, drafts, pinned, muted and archived | `/v1/link-chats`, `/v1/taskiem-chat`, the customers' conversations |
| A conversation | Text, photos, voice notes, files, replies, reactions, ticks, typing, search, forward, star, delete for me | `/v1/link-chats/{link}/messages` and its siblings, `GET /v1/chat/stream` (server-sent events) |
| `@Taskiem` | Taskiem's chat, with its reply buttons and voice notes, as on WhatsApp | `/v1/taskiem-chat` |
| Chat tools | The picker, the cards and the tool sheets, phase by phase as on the web, alerts and approvals included: an approval that comes as a card is decided on the card, with step-up where the policy asks | [chat-tools.md](chat-tools.md) |
| Chat notifications | A new message when the app is closed, the count on the icon, a tap opening that conversation | §5.3 |
| Business switcher | A person in two businesses switches between their chats | `tenant_id` on sign-in, as the web does |
| Settings for the chat only | Notifications on this phone, showing what a message says, the app lock, signing out, **Your phones** | §5.2, §5.3 |

### 3.2 When the chat points elsewhere

A card or a message sometimes names something that lives on a web page (an
invoice in full, a workflow run). In the first release the app opens that
page in the phone's own browser tab (Custom Tabs on Android,
`SFSafariViewController` on iPhone), where the person signs in as on the web
if they are not already. Opening it already signed in is a later step (§5.4).
A card is never left without somewhere to go.

### 3.3 After the chat

Each of these comes to the app only after the chat is released and in use,
in an order chosen then from what people ask for and what the app's numbers
show (§9.3), each with its own plan:

- **Approvals as a screen of their own**, beyond the cards in the chat.
- **Home**: today's money in and out, what needs you.
- **Orders**, as a list and an order, beyond the order cards in the chat.
- **Opening the web signed in** from the app (§5.4).
- **Notifications for things other than chat messages** (a new order, an
  approval waiting), unless they already come as a chat message.
- **Riders' own mode**, if riders need more than their cards in the chat.

**Never in the app:** buying or changing a Taskiem plan. Apple and Google
take a share of digital subscriptions bought in an app, and their rules on
linking out change often; plans are bought on the web, as now. And **a copy
of the business on the phone** (§5.5).

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
- **What is pushed is a chat message only**, as chat push pushes today
  (alerts and approvals that come as chat messages included). Other kinds
  (an order, an approval waiting) come after the chat (§3.3).
- **The sender** is `engine/push` with a second transport, behind the same
  egress guard and metrics. Its service-account key is a secret like any
  other (`TASKIEM_FCM_CREDENTIALS`), and a deployment without it sends
  nothing to apps.
- **Phones without Google's services** (some Huawei phones) get no FCM. The
  app tells them so on the Notifications page, and their WhatsApp or
  Telegram still tells them, as it does today.

### 5.4 S4, after the chat: opening a web page signed in

Not in the first release (§3.2). When it comes: `POST /v1/me/web-link {path}` (device session only, `path` an allow-listed
page of the app) answers a one-time link for one minute, like the chat's
**sign in** link ([chat-sign-in.md](chat-sign-in.md)), whose page already
reads the token from the fragment and signs in with a POST. The web session
it makes is an ordinary 12-hour one, never the device session.

### 5.5 S5: what is kept on the phone

The rule of [offline](offline.md) holds: **nothing of the business is kept
on the phone** in the first release. Conversations, money and orders are held
in memory while the app is open and fetched again when it opens. What is
kept: the token (secure storage), the person's settings, a draft being typed
(as the web keeps one), and a message not yet sent until it is sent or
deleted. A cache of conversations for speed and bad signal is a later
decision of its own (§13, question 4): what is kept, for how long, encrypted
how, and wiped when.

### 5.6 S6: links that open the app

`https://<host>/chat` and `https://<host>/chat?with=…` open in the app
when it is installed (only the chat: every other address stays the web's) (Android App Links, iOS Universal Links). The server
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
3. **The app earns pages, after the chat.** Nothing but the chat comes into
   the app until the chat is released and in use (§3.3). Then a part moves in
   when people ask for it and the app's own numbers show it (which web pages
   are opened from the chat's cards, §3.2).

## 10. Milestones

Sizes: **S** a few days, **M** one to two weeks, **L** three weeks or more,
for one engineer who knows Flutter.

| | Milestone | Size | Done when |
| --- | --- | --- | --- |
| M0 | **People and decisions** (§11, §13) | — | The name is cleared, both store accounts and the Firebase project exist, decision 0066 is accepted |
| M1 | **Foundation**: `mobile/`, the generated client and theme, CI building an Android app, the licence gate for Dart, S1 | M | A build from `main` installs on a phone and shows the sign-in screen in Taskiem's look |
| M2 | **Signed in, and staying signed in**: chat code, password, passkey sign-in; S2; the app lock; the business switcher | M | An owner signs in with a WhatsApp code on a real phone and is still signed in a week later |
| M3 | **The chat**: the conversations, a conversation, sending, files, voice notes, the stream; `@Taskiem`; customers' conversations | L | Two businesses chat from two phones, and every web chat e2e scenario on the shared list passes in the app |
| M4 | **Chat notifications**: S3, the switch and device list, the icon's count, links that open the chat (S6) | M | A message to a closed app shows on the lock screen of a real Android phone in under 5 seconds, and opens that conversation |
| M5 | **Chat tools**, in the web's phase order, alerts and approvals with step-up included | L | Each phase's web tests pass in the app; an owner approves a payout on its card with a passkey |
| M6 | **Android release**: closed testing with the design partners, then the Play Store | M | Ten businesses use the app's chat for two weeks with no lost message and no crash a person reports |
| M7 | **iPhone release**: TestFlight, then the App Store | S–M | The same, on iPhones |
| — | **After the chat** (§3.3): each part by a plan of its own | — | — |

M1 to M4 are about two and a half months for one engineer; with M5 and M6,
four to five. A second engineer shortens M3 and M5, which divide by screen.

## 11. What needs people

| # | Item | Who | Unblocks |
| --- | --- | --- | --- |
| A1 | **The name.** "Taskiem" is a working name pending a trademark search (L3, [what needs people](../needs-people.md)); a store listing and an app ID are hard to change once people have installed the app | Leadership, counsel | Everything public |
| A2 | Google Play developer account (an organisation account needs a D-U-N-S number) | Founders | M6 |
| A3 | Apple Developer Program as an organisation (also a D-U-N-S number) | Founders | M7 |
| A4 | A Firebase project for Taskiem, and its data-processing terms (notifications pass through Google, as Web Push in Chrome already does) | Founders | M4 |
| A5 | App signing keys, held as the release secrets and backed up offline | Founders, ops | M6 |
| A6 | Store listing: words, screenshots, the privacy policy's address, a support email | Founders, design | M6 |
| A7 | A Flutter engineer (or two) | Founders | M1 |
| A8 | Three test phones: a cheap Android, a mid-range Android, an iPhone | Ops | M3 |

## 12. Risks

| Risk | What we do |
| --- | --- |
| The two chats drift apart | §9: the server owns behaviour, a feature is done in both, one scenario list |
| The app slows the chat's pace on the web | The app follows the web by up to one milestone; the web is not held back for it |
| Store review rejects the app (payments wording, the account deletion rule, reviewers who cannot sign in) | A test business and account for reviewers; account deletion from the app, as both stores require; no plan purchases in the app |
| Phones without Google's services | Told plainly; WhatsApp and Telegram still reach them |
| The name changes after launch | A1 before M6; the app ID chosen to survive a new display name |
| A lost or shared phone | The app lock, device sessions listed and ended from the web, nothing of the business kept on the phone |
| A chat-only app feels thin to an owner who wants today's money on the phone | The web on the home screen still does everything; the chat's cards and `@Taskiem` already answer "how much came in today?"; Home is one of the first candidates once the chat is in use (§3.3) |
| Cost of two stores and their reviews on every release | Releases every two weeks at most, from `main`, by CI |

## 13. Decisions to take

1. **The app at all, and in Flutter** (decision 0066). The case against is in
   0066 too: the web app on the home screen, with chat push and offline, may
   be enough, and the critical path is still the people items of the chat-first
   strategy, not code.
2. **Android first**, iPhone released after (recommended), or both at once.
3. **The first release is the chat only** (settled by the founder, 11
   November 2026): §3.1. What comes after it, and in what order, is decided
   once the chat is in use (§3.3).
4. **A cache of conversations on the phone** (§5.5): not in the first release
   (recommended), or a decision of its own before M3.
5. **Device sessions** (§5.2): 30 days unused, 180 at most (recommended), or
   other numbers.
6. **Notifications through Firebase** (§5.3), including for iPhones, rather
   than Apple's service directly (recommended: one transport, one key).
7. **Riders**: their delivery cards in the chat with the chat tools, and a
   mode of their own only if they need more, after the chat.
