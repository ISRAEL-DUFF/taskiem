# Phone app M4: notifications, links, passkeys and signing up as on WhatsApp

The plan for M4 of the [phone app](mobile-app.md) (§10, S3, S6, §3.6). Written 14 November 2026, after M8, offline keeping and the new Explore.

M4 builds on [chat push](chat-push.md) (PR #173, merged 12 November 2026). That PR is the web's Web Push for a chat message:
- the subscriptions table;
- `engine/push`;
- `/v1/me/push`;
- the service worker;
- the switch, the device list, the badge and the logo.

M4 adds the phone to it, and closes what #173 left open.

**Done when** (mobile-app.md §10): a message to a closed app shows on a real Android phone's lock screen in under 5 seconds and opens that conversation.

## 1. What is there already

| | Where |
| --- | --- |
| Web Push to browsers: who (members whose role holds `chat.trade`), when (a message from a linked business, not muted), the words, one notification per conversation, the preview switch, 404/410 dropping a browser, ten browsers a person | `engine/push`, `api/push.go`, migration 00423 |
| The browser's side: the worker, the switch, **Your other devices**, the iPhone note, the badge, the sender's logo | `web/public/sw.js`, `web/src/lib/push.ts`, `pages/ChatPush.tsx` |
| Device sessions and **Your phones** in the app (sign out a phone) | migration 00424, `api/devices.go`, the app's Settings (M2) |
| `GET /v1/app/config`: the lowest version, passkeys on, sign-up on | `api/app_config.go` |
| Signing in with a WhatsApp or Telegram code in the app | `auth/chat/verify` (M2) |
| Signing up in chat, step by step (terms, names, what they sell, how customers pay, three templates) on WhatsApp and Telegram | `api/chat_signup.go` ([chat as the way in](chat-way-in.md) C1) |
| Everything a business's people are told: the bell, WhatsApp, Telegram, Slack | `tellHoldersSent` (`api/bills.go`) for chat messages, bills, orders, reminders, reactions and staff; the alerts (`engine/alerts`); approvals by their own loop (`api/whatsapp_approvals.go`) |

## 2. What #173 left open

These are the "make it better" of this plan; §4 closes each:

1. **Only a chat message is pushed.** A new order, a bill, an approval waiting, a payment that came in, a failed run: these reach the bell, WhatsApp and Telegram, but never a notification on a closed app.
2. **A customer's message tells nobody.** A customer writing to the business's own WhatsApp number leaves no bell item, no WhatsApp line to its staff and no push. For many businesses that is the message that matters most.
3. **A push can be lost.** It is sent in the background after the message commits; a server restart in that second loses it (chat push §10).
4. **A person in two businesses.** A notification for their other business opens the chat page, where that conversation is not found (chat push §13).
5. **A browser that renews its subscription** comes back as a new device: its name and its preview switch are lost (chat push §13).
6. **Nothing can be turned off by kind.** A device takes every push or none.

## 3. Decisions (settled 14 November 2026: each as suggested)

| # | Question | Suggested |
| --- | --- | --- |
| D1 | What is pushed | **Everything the bell tells a person, plus customers' messages** (§4.2), each kind with its own switch |
| D2 | The kinds a person can switch, per device | **Business chats · Customer chats · Orders and money · Approvals · Alerts** (failed runs, connections, stock running low). All on, but Alerts on only for owners and admins |
| D3 | Firebase (A4) | **The code is ready without it and turns on by configuration**. Both are sent to the app by the server, so no new app build is needed:<br>- the project's public client values (`TASKIEM_FCM_CLIENT`);<br>- the service-account key (`TASKIEM_FCM_CREDENTIALS`).<br>A founder creates the project; until then the phone shows nothing about notifications. |
| D4 | Quiet hours | **Not now** (a phone's own Do Not Disturb does it) |
| D5 | Reply from the notification | **Not now**; it comes with the app's own notification actions later |
| D6 | Signing up in the app | **As settled on 12 November**: the phone proved by a WhatsApp or Telegram code, then the same sign-up conversation as on WhatsApp, in the app's own chat screen |
| D7 | Passkeys in the app | **Sign-in and step-up** (approving a payment) with the phone's own passkeys, through Android's Credential Manager and iOS's AuthenticationServices; the same passkeys as the web |

## 4. What M4 builds

### 4.1 N1: the phone as a push target (server)

- **Two kinds of device.** `push_subscriptions` gains `kind` (`webpush` or `fcm`).
  - An app's row keeps its FCM registration token in `endpoint`, with no browser keys.
  - `PUT /v1/me/push` takes either: `{kind: "fcm", token, label, preview, platform}` or a browser's subscription as today.
  - Everything else is shared: the list, rename, remove, ten devices, preview, dropping on "gone".
- **The FCM transport** in `engine/push`:
  - **Sending:** FCM HTTP v1 (`POST fcm.googleapis.com/v1/projects/<id>/messages:send`) through the egress guard, purpose `fcm`.
  - **Authorisation:** an OAuth token from the service account. It is signed with the standard library (RS256), so no new dependency, and kept until it expires.
  - **"Gone":** `UNREGISTERED` and `INVALID_ARGUMENT` on the token mean gone; 429 and 5xx are retried, as Web Push is.
- **One notification per conversation:**
  - Android: `android.notification.tag` (per conversation) and `collapse_key`.
  - iPhone: `apns-collapse-id`.
- **The icon's count:**
  - Android: `notification_count`.
  - iPhone: `aps.badge`.
- **Android channels:** one per kind (§4.3), so the phone's own settings can silence one.
- **What the app is told** (`GET /v1/app/config`): `push: {fcm: {api_key, app_id_android, app_id_ios, project_id, sender_id}}` when `TASKIEM_FCM_CLIENT` is set. These are Firebase's public client values, the same as in any app's own `google-services.json`. The app starts Firebase with them itself, so turning Firebase on needs no new build.
- **Metrics** gain `kind` (`webpush`, `fcm`).

### 4.2 N2: push follows the bell, durably (server)

This closes 2.1, 2.2 and 2.3 with one design: **a push is queued in the same transaction as what it tells**, and a sender sends from the queue.

- **`push_outbox`** (migration): `tenant_id, user_id, kind, title, body, link, tag, icon, created_at, attempts, next_at`, row-level secured.
- **It is filled two ways:**
  - **A trigger on `notifications`.** Every bell item for a person who has a device in that business queues one push. This covers everything that already tells the bell: `tellHoldersSent`, the alerts, the reversals, schools. A repeat of the same group (the bell's count) queues again with its new title ("Payroll failed 3 times").
  - **Approvals** become one more channel of `notifyApprovalsOn` (`push`), told once per approver and level, as WhatsApp is.
- **Customers' messages** get a bell item of their own:
  - **Who is told:** the people who answer customer chats are told "Ade wrote", once per conversation while unread. This uses the bell's grouping, so ten messages are one item with a count.
  - **What follows:** the trigger pushes it, and WhatsApp and Telegram tell them too, as for any bell item.
  - **Mute:** a customer chat can be muted, as a business's can (`customer_chats.muted_at`, Contact info's switch).
- **The kind of a push** is read from what it is:
  - **Business chats:** `group_key` `msg:` or `reaction:`.
  - **Customer chats:** a customer's message.
  - **Orders and money:** `order:`, `bill:`, `reminder:`, payments.
  - **Approvals:** from the approvals channel.
  - **Alerts:** the alerts and failed runs.
  - **Anything else** falls under Alerts.
- **The sender** waits on `LISTEN push_outbox`, with a poll every 5 s behind it:
  - It claims rows (`FOR UPDATE SKIP LOCKED`, through a narrow definer, as the WhatsApp outbox does).
  - It sends to the person's devices that take that kind, and deletes the row.
  - A failure waits 5 s, then 30 s, then 2 min, and is dropped after 12 hours (TTL), as a push service would.
  - A server restart loses nothing: the row is there when it comes back.
- **The words and privacy are #173's:**
  - no references, amounts or names in a document's line;
  - with the preview off, the title is the business's name and the body "New message" (chat), or "Taskiem" and "Something needs you" (everything else);
  - a link opens only a path inside the app.
- **`pushMessage`** (the direct send from `tellLinkMessage`) goes, since the bell item now queues it. Its words, logo and tag move to the queue.

### 4.3 N3: what a device takes, and which business (server and web)

- **Kinds per device** (closes 2.6): a device row gains `kinds text[]`, by default all but Alerts for people who are not owners or admins. `PATCH /v1/me/push/{id}` sets them. The web's **Chat when Taskiem is closed** becomes **Notifications when Taskiem is closed**, with the five switches under each device.
- **The business in the push** (closes 2.4): the payload carries `business` (the tenant).
  - **The web:** the worker opens `/chat?with=…&business=…`, and the page switches to that business first when the person has a session for it, or asks them to sign in to it.
  - **The app:** it switches business, then opens the conversation, as Settings' **Your businesses** does.
- **A renewed browser subscription** (closes 2.5): the worker sends the old address with the new (`replaces`). The server moves the name, preview and kinds to the new row and drops the old.

### 4.4 N4: notifications in the app

- **Libraries:** `firebase_core` and `firebase_messaging` (BSD-3-Clause), checked by the licence gate.
  - Firebase is started from the values `GET /v1/app/config` gives (§4.1).
  - Nothing Firebase is in the build's files, so the app builds and runs with no Firebase project; Notifications then says nothing.
- **Settings, Notifications on this phone** (the web's words, §6.6 of chat push):
  - the switch, **Show what the message says**, and the five kinds;
  - **Your other devices**, phones and browsers, with Remove;
  - on a phone without Google's services, the line that WhatsApp or Telegram tells them instead.
  - Android 13 and later asks for the permission when the switch is turned on; a refusal says how to allow it in the phone's settings.
- **Asked once:** after signing in, a sheet like the web's banner asks "Get a notification when a business or customer writes?". **Yes** turns it on, **Not now** asks again only from Settings.
- **The token:** registered with `PUT /v1/me/push` when the switch is on, renewed when Firebase rotates it, and removed when signing out or the session ends (before the token is forgotten).
- **A tap** opens what the push is about:
  - the conversation (`/chat?with=`);
  - a customer's chat;
  - an order, a bill or an approval, by the same links the bell uses (`Openers`).
  - It works from a closed app (`getInitialMessage`), in the background (`onMessageOpenedApp`), and for the other business (switched first).
- **With the app open:** no system notification.
  - In the conversation it is about, nothing (it is on the screen).
  - Anywhere else, a toast at the top with **Open** for a while, and the chime the app already has.
- **The icon's count** is the server's number in each push. The app sets it again when it opens and when the chats' unread count changes, through Android's notification count and iOS's badge (`flutter_app_badger`'s replacement on the platform channel, or the platform's own API).
- **Android channels:** one per kind, named in plain words ("Business chats", "Customer chats", "Orders and money", "Approvals", "Alerts"), so the phone's settings can quiet one.

### 4.5 N5: links that open the app (S6)

- **The server** serves `/.well-known/assetlinks.json` and `/.well-known/apple-app-site-association` from configuration:
  - `TASKIEM_ANDROID_APP`: the package name and its signing certificates' SHA-256;
  - `TASKIEM_IOS_APP`: the team and bundle id.
  - Without the configuration both answer 404.
- **The app:**
  - **Declaring the links:** Android declares verified links (`autoVerify`) for its server's host, set at build time from `TASKIEM_URL`; iOS declares Associated Domains.
  - **Receiving them:** the `app_links` package hands each link to the app's router.
  - **The paths it opens:** `/chat`, `/chat?with=…`, `/invoices/{id}`, `/trade-orders/{id}`, `/storefront/orders/{ref}`, `/bills/{id}`, `/purchases/{id}` and `/approvals`.
  - **Signed out:** the link waits behind signing in, then opens.
- **The web** is unchanged: without the app, the same link opens the web.

### 4.6 N6: passkeys in the app

- **The server's WebAuthn origins** gain the Android app's origin (`android:apk-key-hash:<base64url of the signing certificate's SHA-256>`) from `TASKIEM_ANDROID_APP`, and the iOS app's through the associated domain. The relying party is the same host as the web, so a person's passkeys work in both.
- **The app** uses the platform's passkey APIs through one package (`passkeys`, MIT, checked by the licence gate):
  - **Signing in with a passkey** on the sign-in screen (the button is there);
  - **Adding a passkey** in Settings;
  - **Approving with a passkey** in the approval sheet, beside the authenticator code it has today. A rule that takes only a passkey then works in the app (M8's note).
- **Off** where `GET /v1/app/config` says passkeys are off, or where the phone has none (Android below 9, no screen lock): the app says so and offers the other ways.

### 4.7 N7: signing up as on WhatsApp

- **The way in** (`tenant_onboarding.source = 'app'`):
  - **Proving the phone:** the number gets a code by WhatsApp or Telegram, as signing in with a chat code does.
  - **The conversation:** a new number is offered the sign-up conversation, in the app's own chat screen with Taskiem.
  - **The rules are chat as the way in's:** off unless the operator turns it on, and limits per phone, hour and country.
- **The server:**
  - **The sign-up's steps** in `chat_signup.go` (terms with **Yes**, the business's name, theirs, what they sell, how customers pay, the three templates) are run by a third channel, `app`. Its "messages" are `POST /v1/signup/app/turn` with a short-lived sign-up token got from the proven phone, and its answers carry the same buttons.
  - **The result:** a business made as on WhatsApp, the owner signed in on this phone with a device session, the phone's WhatsApp or Telegram bound as proven, and consent recorded.
- **The app:**
  - **Get started** on the sign-in screen opens the phone step.
  - **The conversation** uses the chat screen's bubbles and buttons.
  - **At the end**, it lands in the chats with Taskiem's first message.
  - **The email form** (the web's sign-up) stays behind "Sign up with email instead".

## 5. Order of the work

| Part | What | Server | Web | App |
| --- | --- | --- | --- | --- |
| P1 | N1 the FCM transport and app rows; N2 the outbox, the trigger, approvals as a channel, customers' messages in the bell | ✓ | | |
| P2 | N3 kinds per device, the business in the push, renewed subscriptions | ✓ | ✓ | |
| P3 | N4 notifications in the app | | | ✓ |
| P4 | N5 links | ✓ | | ✓ |
| P5 | N6 passkeys | ✓ | | ✓ |
| P6 | N7 signing up as on WhatsApp | ✓ | | ✓ |
| P7 | The audit (three reviewers as before), the fixes, the owner's checklist for real phones | | | |

**After P3, M4's done-when can be tried on a phone, once a Firebase project exists.**

## As built: P1, the server (14 November 2026)

Migration 00446. Nothing in the app yet: no phone registers until P3.

- **Devices** are browsers (`webpush`) or the app (`fcm`, its registration token in `endpoint`, no keys), each with the kinds it takes (`kinds`: business, customers, orders, approvals, alerts). A new device takes them all for owners and admins, all but alerts for the others. `PUT /v1/me/push` takes `{kind: "fcm", token, platform}`; `PATCH /v1/me/push/{id}` takes `kinds`; `DELETE /v1/me/push` takes `token`. `GET /v1/me/push` says whether browsers and phones can be notified on this server.
- **The FCM transport** (`engine/push/fcm.go`):
  - FCM HTTP v1 through the egress guard (purpose `fcm`), with an OAuth token from the service account, signed with the standard library and kept until a minute before it expires (a 401 asks for a new one).
  - Each message carries the Android notification (tag per conversation, the kind's channel, the icon's count, high priority, 12 hours), the APNs headers (collapse id, badge) and the data the app reads (link, tag, kind, business, unread).
  - `UNREGISTERED`, a 404, or a 400 about the registration token drops the device; a 429 or 5xx is tried again; a wrong project is kept and logged.
  - `TASKIEM_FCM_CREDENTIALS` and `TASKIEM_FCM_CLIENT` (both or neither); `GET /v1/app/config` gives the app the client values (`push.fcm`).
- **The queue** (`push_outbox`):
  - **Filled two ways:** by a trigger on every bell item (new, or its group repeating) for a person with a device, and by the server for a business chat's message (#173's words, without a document's reference; that bell item is skipped).
  - **Drained** by `RunPush`, on `LISTEN push_outbox` or every 5 seconds, business by business in each one's scope (`taskiem_push_due`).
  - **Claimed by lease** (90 s), so nothing is held open while a push service answers.
  - **Kinds:** each push goes to the person's devices that take its kind.
  - **Failures:** a failed device waits 5 s, 30 s, 2 min, 10 min, 1 h, then the push is dropped, as anything older than 12 hours is.
  - **A restart loses nothing queued.**
  - **One per conversation:** of several pushes for the same person and conversation waiting together, only the newest is sent. Sent at once, an older one could arrive last and replace it on the phone (found by the test).
- **The kind** of a bell item is read from what it is (`taskiem_push_kind`). Not pushed: a person's own changes, a run that finished.
- **Customers' messages** are a bell item for those who handle orders: "Ade wrote", then "Ade wrote 3 messages", per conversation while unread, in the message's own transaction. With the preview off, a customer's push says only "A customer wrote" (their name or number is theirs).
- **Approvals waiting** are a bell item for each approver who may decide them, once per level ("Approval waiting: Payroll"), by a loop of their own every 15 seconds whether or not a chat app is set up (`RunApprovalBell`, `taskiem_approval_tenants`); the maker is never told.
- **Tests:**
  - `engine/push` (`TestFCMSend`, `TestFCMOutcomes`, `TestParseFCM`, `TestValidTokenAndKinds`): a fake Google checks the service account's signature.
  - `TestPushEverywhere`, `TestApprovalInTheBell`, and `TestChatPush` (unchanged in what it covers, now through the queue).

## As built: P2, the web (14 November 2026)

- **The worker** (`web/public/sw.js`) opens any page of the app a push names (one plain question at most, never another site), with `?business=` for the business it is for. A renewed subscription is sent with `replaces`, so the server keeps its name, words and kinds and drops the old one.
- **The page** (`web/src/lib/pushOpen.ts`): `?business=` for another business switches to it (`POST /v1/me/switch`, the browser's own session) and opens the page afresh; one the person is not in, or cannot be moved to (single sign-on), is said on the page. A tap while a window is open goes wherever the push is for, from any page (it was the chat page only).
- **Notifications when Taskiem is closed** (`ChatPush.tsx`): for everyone, not only those who chat; a switch per kind, for the kinds the person can get (business chats with `chat.trade`, customers with orders); other devices say whether they are a browser or the app.
- **`known`** on `PUT /v1/me/push`: a page that lost which device it is (storage cleared, the worker renewed) finds it by its address, and one removed from another device is not added back. A PUT without a label keeps the device's own.

## As built: P3, the app (14 November 2026)

- **Firebase** (`firebase_core` 5.1.0, `firebase_messaging` 17.0.1, BSD-3-Clause; the licence gate passes) is started from `GET /v1/app/config`'s values; the build has no Firebase file. On Android the values are also kept for the app's process (`TaskiemApplication.kt`), so a notification that starts a closed app finds Firebase started. Without `push.fcm`, Notifications is not shown; on a phone without Google's services it says that WhatsApp or Telegram can tell them instead.
- **The phone is a device per business** (`lib/app/notify.dart`): turned on in a business, it is registered there with its name; opened again, it is found by its token (`known`) and never added back if removed from another device; a token Firebase changes replaces the old one (`replaces`, now for the app too), keeping its choices.
- **Settings, Notifications on this phone**: the switch, Show what the message says, the kinds, the other devices with Remove; a refusal in the phone's settings says where to allow it.
- **Asked once** after signing in, per business ("Get a notification when a business or customer writes?"); Not now asks again only from Settings.
- **Sign out** takes the phone off while the session still counts, then makes the token worthless (`deleteToken`), so no business keeps notifying it; the session ending elsewhere does the second.
- **A tap** opens its page, by the bell's links: a business's or a customer's conversation, Taskiem's conversation for approvals, Orders, Money, else Today; for another business it switches first and the new home opens it. From closed (`getInitialMessage`) and from behind (`onMessageOpenedApp`).
- **With the app open** nothing shows from the system (an iPhone too); a toast with Open does, except for the conversation on the screen.
- **Android**: a channel per kind ("Business chats", "Customer chats", "Orders and money", "Approvals", "Alerts"), the brand's mark as the small icon on pine, `POST_NOTIFICATIONS` asked for on Android 13 and later.
- **The icon's count** is the server's, in each push (Android's notification count, the iPhone's badge). The app does not set it itself yet.
- **Tests**: `mobile/test/notify_test.dart` (a stand-in Firebase and server): off without Firebase, no Google services, on and off, refused, a changed token, opened again and removed elsewhere, signing out in order, taps in this and another business, the conversation on the screen, and the Settings card.

## As built: P4, links (14 November 2026)

- **The server** serves `/.well-known/assetlinks.json` (`handle_all_urls` and `get_login_creds`, for the package and its certificates) and `/.well-known/apple-app-site-association` (the pages the app opens, and `webcredentials`) from `TASKIEM_ANDROID_APP` (`{"package", "sha256": [...]}`) and `TASKIEM_IOS_APP` (`TEAMID.bundle.id`); without them, 404. A bad value stops the server starting, with what it should be.
- **Android** declares verified links (`autoVerify`) for the host of the `TASKIEM_URL` it is built with (read from the build's Dart defines in `build.gradle.kts`): `/chat`, `/approvals`, `/invoices/…`, `/trade-orders/…`, `/storefront/orders/…`, `/bills/…`, `/purchases/…`. **iPhone**: Associated Domains (`applinks:`, `webcredentials:`) for `TASKIEM_HOST` in `Flutter/*.xcconfig`, push's `aps-environment`, and the remote-notification background mode.
- **The app** (`lib/app/applinks.dart`, `app_links` 7.2.2, Apache-2.0): a link to one of those pages of its own server opens the conversation (a customer's too), the invoice, bill, purchase or order over its tab, or Taskiem's conversation for the approvals; a question other than a conversation's is dropped. A link that comes signed out waits behind signing in. Notifications use the same opening.
- **Tests**: `TestParseAppLinks`, `TestWellKnownAppFiles`; `mobile/test/applinks_test.dart`.

## As built: P5, passkeys (14 November 2026)

- **The server**: the Android app's origin (`android:apk-key-hash:` and the certificate's SHA-256, base64url) joins the relying party's origins when `TASKIEM_ANDROID_APP` is set; an iPhone app's passkeys come from the web's origin through its associated domain. The relying party is the web's host, so one passkey works in both.
- **The app** (`lib/app/passkey.dart`, `passkeys` 2.24.1, BSD-3-Clause):
  - **Sign in with a passkey** on the sign-in screen: a device session, as every way in; closing the phone's sheet says nothing.
  - **Settings, Passkeys**: the person's passkeys, and **Add a passkey on this phone**, after the proof the server asks (an authenticator code, the password, or the passkey they already have), named after the phone.
  - **Approvals**: a decision that asks for confirmation uses the phone's passkey (operation `approval.decide`, target run/step/decision), or a typed authenticator code; a rule that takes only a passkey now works in the app.
  - The phone's own refusals are said in plain words: no passkey here, the app not set up for passkeys yet (no verified domain), a phone that cannot (no screen lock, Android below 9).
- **Still on the web**: linking WhatsApp, Telegram and Slack, and the WhatsApp PIN, for a person whose only proof is a passkey (those requests take a code or the password).
- **Tests**: `mobile/test/passkey_test.dart` (a stand-in for the phone's passkeys): sign-in, cancelled, off on the server, adding with a proof, an approval's passkey.

## As built: P6, signing up as on WhatsApp (14 November 2026)

Migration 00447.

- **The server** (`api/app_signup.go`): a third channel into chat sign-up, `app`, on when the operator lists it in `TASKIEM_CHAT_SIGNUP` (with WhatsApp to send codes, and terms); `GET /v1/app/config` says so (`chat_signup`).
  - `POST /v1/signup/app/code {phone}`: the countries and limits of chat sign-up, then a code by WhatsApp's authentication template from Taskiem's number (three a number while one lives, ten an hour from an address).
  - `POST /v1/signup/app/verify {phone, code}`: five tries; a number already on an account is sent to sign in (409 `sign_in`, said only once the number is proven); else a sign-up token and the first question (the terms, with Yes and No).
  - `POST /v1/signup/app/turn {phone, token, text | reply, device}`: the WhatsApp sign-up's own steps (`chat_signup.go`), its answers this request's (`appChannel`); cancelling ends it; the last answer makes the business as on WhatsApp (source `app`, terms recorded with channel `app`), its owner bound to the proven number (`taskiem_chat_bind_whatsapp_proven`: the code stands for WhatsApp's inbound message), and this phone signed in with a device session.
- **The app**: **Create an account** on the sign-in screen opens **Get started** where the server has it (else the email form, as before; the form stays behind "Sign up with email instead"): the number, the code, then a chat with Taskiem, its questions with their buttons, a box to answer; at the end the app opens on the new business. A number already on an account is sent to sign in.
- **Not as planned**: codes go by WhatsApp only (a Telegram bot cannot write to a number first); the templates the WhatsApp sign-up offers at the end are left out in the app, which opens Taskiem's conversation instead.
- **Tests**: `TestAppSignup`, `TestAppSignupCancelled`; `mobile/test/app_sign_up_test.dart`.

## The owner's checklist: trying M4 on real phones

What the build machine cannot try (a real FCM delivery, a lock screen, a real passkey, a verified link), in order. Each line says what to set, then what to see.

**Firebase (A4), for notifications on Android:**
1. In the Firebase console, make a project; add an Android app with the package `app.taskiem.chat`; under Project settings, Service accounts, make a key (JSON).
2. Set `TASKIEM_FCM_CREDENTIALS` to that JSON (a secret) and `TASKIEM_FCM_CLIENT` to `{"api_key", "app_id_android", "project_id", "sender_id"}` from the app's settings in the console. Restart the server; it refuses to start if either is wrong, and says which.
3. Install the APK, sign in. **See:** the sheet "Get a notification when a business or customer writes?"; Yes asks Android's permission (Android 13+).
4. Close the app. From another account, write to this business in a linked chat. **See:** a notification under "Business chats" with the sender's words; tap it: the conversation opens.
5. Settings, Notifications on this phone: turn off "Show what the message says", write again. **See:** "New message", no words. Turn off Business chats: nothing comes.
6. A customer writes on the business's WhatsApp. **See:** "Ade wrote" under "Customer chats" (owners and those who handle orders).
7. Start an approval that waits for this person. **See:** "Approval waiting: …"; tap it: Taskiem's conversation with the card.
8. In two businesses: a push from the other one. **See:** tapping it switches business, then opens it.
9. Sign out. **See:** nothing more comes to that phone.

**Links and passkeys (the signing key's SHA-256):**
10. `keytool -list -v` on the release keystore (or Play Console, App signing) for the SHA-256; set `TASKIEM_ANDROID_APP={"package":"app.taskiem.chat","sha256":["AA:…"]}`. **See:** `https://<server>/.well-known/assetlinks.json` answers it.
11. Reinstall the app (Android checks links at install). Tap a link to `https://<server>/chat?with=…` in WhatsApp. **See:** the app opens on that conversation, not the browser. Signed out: it opens after signing in.
12. Settings, Passkeys, Add a passkey on this phone (the authenticator code or password first). **See:** the phone's own sheet; then "Passkey added". Sign out, then Sign in with a passkey.
13. An approval rule that asks for a passkey: approve in the app. **See:** the phone's sheet, then Approved.

**Get started (sign-up as on WhatsApp):**
14. Add `app` to `TASKIEM_CHAT_SIGNUP` (the terms' versions set, WhatsApp on, the OTP template approved). **See:** Create an account on the sign-in screen opens Get started.
15. A new Nigerian number: the code on WhatsApp, the questions, Yes to the terms. **See:** the app opens on the new business; WhatsApp sign-in works for that number afterwards.

**iPhone (A3), later:** an APNs key in Firebase; `app_id_ios` in `TASKIEM_FCM_CLIENT`; `TASKIEM_IOS_APP=TEAMID.app.taskiem.chat`; `TASKIEM_HOST` in `ios/Flutter/*.xcconfig` set to the server's host; Push Notifications and Associated Domains on the App ID. Then 3–13 on an iPhone.

## 6. Tests

**Go:**
- The FCM transport against a fake FCM: the token request signed with the service account, the message's tag, collapse key, count and channel, `UNREGISTERED` dropping the device, 429 and 5xx retried.
- The outbox:
  - a bell item queues one push for a person with a device and none without;
  - a repeat queues again;
  - a restart between queueing and sending still sends;
  - each kind honoured per device;
  - the preview off.
- **Approvals:** told once per approver.
- **Customers' messages:** in the bell, grouped, and muted.
- **The business in the payload;** a renewed subscription keeping its settings.
- **The well-known files:** with and without configuration.
- **The app origin** accepted for a passkey.
- **The app sign-up:** start to business, limits, consent, a proven phone required.

**The app:**
- the switch, the permission refused, the five kinds, the device list;
- a tap from closed, background and the other business;
- the toast in the foreground and nothing in the open conversation;
- the token removed on sign-out;
- links opening each screen, and waiting behind sign-in;
- passkey sign-in and approval with a fake platform;
- the sign-up conversation;
- large text and 360 px for each screen.

**The web:**
- the five kinds;
- the business switch from a push;
- a renewed subscription keeping its name;
- the existing chat-push e2e unchanged in what it covers.

**Not testable from the build machine:** a real FCM delivery, a real lock screen, a real passkey, a real verified link. Each is on the owner's checklist (P7).

## 7. Needs from people

| | | When |
| --- | --- | --- |
| A4 | A Firebase project: its Android (and later iOS) app registered with the package name, a service-account key with the Firebase Cloud Messaging role, and the client values | Before P3 can be tried on a phone |
| A3 | The Apple account, for iPhone notifications (an APNs key in Firebase), Associated Domains and passkeys | iPhone only; Android does not wait for it |
| — | The release signing key's SHA-256 (for verified links and passkeys) | P4, P5 on a phone; the debug key works for testing |
