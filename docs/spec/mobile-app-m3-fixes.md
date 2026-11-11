# The phone app: finishing M3 (bugs and gaps found in review)

Reviewed 11 November 2026, after main was merged into the app's branch: the
app's code (`mobile/lib`, every file) read for bugs, and the app set against
the web chat and [the app's spec](mobile-app.md) (§3.1, §3.2, §3.5, §3.6,
§8, §9, the M3 row of §10 and "As built: M3 so far") for what is missing.
Every bug listed was checked in the code. This plan is the work to finish
M3; each item is done when its fix and its test are in, and the spec's
**As built** says so.

Items are numbered to be named in commits (`B3`, `G7`…). The order of the
sections is the order of the work.

## 1. Bugs: how the app's screens are put together (first)

One cause under B1–B3: the signed-in app is the `home` of the app's only
Navigator, and every other screen (a conversation, Taskiem's, a customer's,
Settings, Explore, search, contact info) is pushed above it. What happens to
`home` (locked, signed out, another business) does not reach them.

| # | Bug | Where | Fix | Test |
| --- | --- | --- | --- | --- |
| B1 | **The app lock does not cover open screens.** Locked, `_Home` is replaced by the lock screen, but pushed routes stay painted above it: back after 5 minutes in a conversation shows it unlocked. Locking also disposes `_Home`'s ChatsData and ChatLive while those routes still use them (a disposed notifier used, polls still running). | `main.dart:98`, `app/lock.dart:122` | The lock goes above everything: in `MaterialApp.builder` (over the Navigator) as an overlay that covers and blocks input, never replacing the app beneath. ChatsData and ChatLive live above the Navigator, not in `_Home` | Open a conversation, pause past the lock time, resume: the lock is on top; unlock: the conversation is where it was, still following |
| B2 | **Signing out, or the session ended elsewhere, leaves screens up.** Sign out from Settings (a pushed route) appears to do nothing; a phone removed in Your phones, or a 401, leaves an open conversation polling with no token | `screens/signed_in.dart:186`, `main.dart _Root` | On leaving `signedIn`, pop to the first route and stop every poll and the stream before the sign-in screen shows | Sign out from Settings: sign-in shows; a 401 during a conversation: sign-in shows, no request after |
| B3 | **Switching business keeps the old business's chats.** `_Root` rebuilds the same `_Home`, so its ChatsData, caches (logos, files) and the open `/v1/chat/stream` (with its Last-Event-ID) stay on the old business until the next poll | `app/auth.dart:108`, `main.dart:198` | Key the signed-in tree by the business (`ValueKey(tenant)`), pop to the first route, start ChatsData and ChatLive afresh | Switch business in Settings: the list is the new business's at once; no event of the old one arrives |

## 2. Bugs: lifetimes and failures

| # | Bug | Where | Fix | Test |
| --- | --- | --- | --- | --- |
| B4 | **Polls leak when a conversation is left before it loads.** `open()` checks nothing after its awaits: closed before the first page comes, it still listens to the stream and starts its 3 s and 9 s timers, which nothing cancels; one pair more for every quick back | `chat/conversation.dart:213` (`open`), `chat/taskiem.dart:187`, `chat/customer.dart:116` | `if (_disposed) return;` after every await in each `open()`, before listening or starting timers | Each: open with the first page held, dispose, answer it: no timer, no listener, no request after |
| B5 | **A send that fails after leaving throws.** The words are given back through a StreamController already closed: "Cannot add new events after calling close" | `chat/customer.dart:199`, `chat/taskiem.dart:265` | Give back only while not closed (`if (!_back.isClosed)`) | Send offline, dispose, let it fail: no error |
| B6 | **The app can stay on the opening pine for good.** `start()` reads the token outside any try and catches only ApiError and Offline: secure storage failing (Android Keystore after a restore) or an unexpected answer leaves `checking` | `app/auth.dart:67` | Any other error: the token forgotten, signed out (the stored device id kept) | A store that throws, and `me` answering nonsense: the sign-in screen shows |
| B7 | **Voice note: let go before the recorder has started.** `stop()` runs while `start()` is still waiting (a slow phone, the microphone question showing); it reads the last note's deleted file (uncaught), then recording starts with nothing to stop it | `chat/composer.dart:76` (`Recorder`), `_record`/`_stop`/`_release` | The recorder knows it is starting; a stop while starting waits for the start and throws the note away; `_path` cleared after each stop; a missing file is no note; `_stop` catches | A fake recorder whose start is held: press, release, finish the start: nothing recorded, nothing sent, no error |
| B8 | **Explore's cards do not change with the links.** The cards are built outside the ListenableBuilder on ChatsData: a request arriving or accepted while Explore is open leaves Connect / Accept / Withdraw as they were | `chat/explore.dart:413` | Build the cards inside the builder | Explore open, a link answered: its card changes |
| B9 | **A logo that failed once is never asked for again.** Its slot is kept as null on a failure as on "no logo" | `chat/chats.dart:181` | Keep null only for "no logo" (404); on a failure remove the entry and ask again after a while (on the next load) | A logo failing then served: it shows after the next load |
| B10 | **Files: no backoff, no limit.** A file that fails (403/404) is asked for again on every rebuild (every 3 s pull); every fetched photo, PDF and voice note is kept in memory for the app's life | `chat/attachments.dart:136` | Remember a failure (asked again on Retry or after a minute); keep at most ~50 MB, the least recently drawn going first | A failing file asked for once per minute at most; the cache stays under its limit |
| B11 | **A tap recogniser made on every build, never disposed** (a leak per rebuild) | `chat/conversation.dart:1343` | Keep the recognisers in the State and dispose them | Widget test: rebuilt many times, no growth (or by reading) |
| B12 | **Searches sent again when only the cursor moves.** The search boxes listen to the controller, which also tells of cursor and selection | `chat/explore.dart:137`, `chat/chats.dart:350` | Search only when the text changed | Moving the cursor sends nothing |

## 3. Cleanups found in the same reading

| # | What | Where | Fix |
| --- | --- | --- | --- |
| C1 | The https check and the mapping of errors copied four times | `api/transport.dart` | One private send used by all four |
| C2 | `_with` copies an Explore card through JSON to change one field | `chat/explore.dart` | A `copyWith` |

## 4. Gaps: in M3, missing from the app

M3 is "the chat (§3.1, §3.2)… and connecting (§3.5)". The web chat does
each of these; the app does not.

| # | Gap | The web | The app | Work |
| --- | --- | --- | --- | --- |
| G1 | **Connecting a service** (`connect <service>`; §3.5, named in M3) | The one-time link `/c/<token>` in Taskiem's answer (`api/connect_links.go`, `api/chat_history.go:482`) | No way to open a browser at all (no url_launcher / custom tabs); no return to the app; no form for key-based services | Open `/c/` links (and only §3.5's) in the phone's browser tab (Custom Tabs / SFSafariViewController); come back to Taskiem's conversation on return (an app link, with M4's S6, or on resume); a key-based service (Paystack, Termii…) gets a secure form in the app, posting where the web's connect page does, never through a message. Needs: the server's answer to say which kind a connect is. Test: a connect answer opens the browser; a key form saves |
| G2 | **A workflow's "open it"** (§3.5) | Opens the canvas | Nothing | Same browser opening as G1, for the canvas link only |
| G3 | **Typing is never sent** | `POST /link-chats/{link}/typing` at most every 3 s while typing (`Conversation.tsx:1376-1387`) | Shows theirs only; `linkTyping` exists (`api/api.g.dart:159`) and is never called | Call it from the business conversation's box, at most every 3 s. Test: typing sends once per 3 s |
| G4 | **Drafts** (§3.1) | Unsent words kept per conversation (`ChatComposer.tsx:142-156`, `CustomerChat.tsx:301`, `TaskiemChat.tsx:215/348`); the list says "Draft: …" (`Links.tsx:684-686, 948-951`, `CustomerChat.tsx:80-82`) | None | Keep each conversation's unsent words (kept on the phone with Prefs, cleared on send; §5.5 allows: it is the person's own words); the list's preview "Draft: …" as the web's. Test: type, leave, the list says Draft; back, the words are there |
| G5 | **Received photos and PDFs cannot be opened** | A photo opens full size; a PDF downloads (`Conversation.tsx` AttachmentView ~:364-411) | Only voice notes play (`chat/attachments.dart:150+`) | A photo opens full screen (pinch, close); a PDF is handed to the phone's viewer / share sheet. Test: tap opens |
| G6 | **Reference cards** (ChatRefCard, §3.1 Cards) | For documents tagged in a message (`Conversation.tsx:963-969`) and references in Taskiem's answers (`TaskiemChat.tsx:643-652`, `/v1/chat-card`): the amount, the paid bar, lines, Open, **Give this to…**, Send / Remind / Pay / Message; the customer kind with its figures and Message (`ChatRefCard.tsx:43-205`) | None | Draw the card from `/v1/chat-card` in both places; Send, Remind, Message, Give this to… (`give_task.dart`) work from it; Open and Pay wait for their screens (M5–M8) and are not shown until then (as the alert steps). Parity: a tagged invoice, light and dark |
| G7 | **"Open the order" on Taskiem's alert does nothing** | Opens the customer's conversation (`ChatAlerts.tsx:335-340`) | `taskiem.dart:448-456` passes no `onOpen` | Pass `onOpen`; `customer` opens that customer's conversation. Test |
| G8 | **Taskiem's conversation: "@" and "#"** | "@" linked businesses; "#" our products and suppliers' items, with categories (`TaskiemChat.tsx:156-207, 253-273`); tagged references in its answers open their item (`:105-140`) | No tags (`taskiem.dart:379-390`) | The tag picker (`tag_picker.dart`) with Taskiem's sources; tags drawn in its answers (opening what has a screen). Test, parity: "@" in Taskiem's |
| G9 | **Taskiem's messages: no menu** | Copy (`TaskiemChat.tsx:590-593`) | None | Long press: Copy (`menu.dart`). Test |
| G10 | **"briefing" does not bring the card back** | `asksBriefing` brings back a briefing put away (`TaskiemChat.tsx:276`) | Sent as words only | The same check before sending: the card shown again (`?show=1`). Test |
| G11 | **Link previews** | A preview card for a link, opening it (`Conversation.tsx:602-616, 971`) | `msg.preview` used only to lay out the bubble (`conversation.dart:1282`) | Draw the preview; open the link in the browser (G1's opening; add to §3.5 as "a link someone sent"). Parity: a message with a link |
| G12 | **The chime and its switch** | A soft chime for good news on a card (`Conversation.tsx:1756`, `ChatAlerts.tsx:66`; `lib/moments.ts`), and the head menu's "Sound for new orders and payments" (`Links.tsx:590-602`) | None (As built defers "the sound" with notifications to M4) | Decide: the in-chat chime is the chat's look and sound (§4.1), not a notification, so M3; the same tone made the same way, the switch in the chats' menu. Test: news chimes when on, not when off |
| G13 | **Taskiem's unread mark is forgotten on restart** | Kept (`lib/taskiemSeen`) | Kept only while running | Keep the last seen on the phone (Prefs). Test |
| G14 | **"@" does not find businesses not linked, nor offer categories** | `Mentions.tsx` (grouped, `onCategory`) | "Not yet" in As built | Ask `/v1/explore` as the web's Mentions does; categories. Test, parity |
| G15 | **Voice notes made on the web do not play on an iPhone** (WebM/Ogg) | — | As built notes it | Server: keep an MP4 (AAC) copy of each voice note and offer it; the app asks for it. Test on the server and the app |
| G16 | **The chat PIN's step-up words** | `Approvals.tsx:63-64` has `whatsapp_pin` (ChatAlerts' sheet does not) | approve.dart has the code only | Say it as Approvals.tsx does if the server asks for the PIN; check approving with the PIN in Taskiem's conversation works end to end (M3 says "approvals with the chat PIN") |

## 5. Gaps: Settings (§3.6)

The spec's Settings list against `screens/signed_in.dart` (which has Your
businesses, Your phones, Appearance, Sign out):

| # | Missing | Note |
| --- | --- | --- |
| S1 | The app lock's switch (and its time) | The lock exists (`app/lock.dart`) but cannot be turned off or set |
| S2 | The chat PIN (set, change) | Used for approvals in Taskiem's conversation |
| S3 | Your name and email | |
| S4 | The chats you have linked (WhatsApp, Telegram, Slack) | On the web under Account, linked from `ChatDoors` and `ChatAlerts` |
| S5 | The chat tools switch, for owners and admins | |
| S6 | Notifications and showing what a message says | M4 (§5.3), not M3 |

## 6. Scope to settle in the spec

The spec says two things about each of these; the plan proposes one.

| # | What | The conflict | Proposed |
| --- | --- | --- | --- |
| D1 | **Orders read from a customer's messages** (CustomerOrders: the card Taskiem read, its questions, Ask them / Confirm / Drop, the confirmed card, Start an order) | §3.1 lists it in M3; the M7 row and As built put it in M7; the app's Start an order does nothing (`customer.dart:357`) | M7 (it leads to the order sheet); until then Start an order is not shown, rather than doing nothing; the spec's §3.1 says M7 |
| D2 | **Requests typed as words** (RequestChip: `POST /v1/chat-tools/request`) | §3.1 lists it in M3; the chip opens a tool's form (M5–M8) | With the tools (M5–M8) |
| D3 | **Taskiem's tools menu: Tasks, Clock in / Clock out** (`TaskiemChat.tsx:240-248`) | Not in the spec | With the tools; add to §3.3 and §3.4 |
| D4 | **A tool opened from a link** (`?tool=`, `TaskiemChat.tsx:308-325`) | Not in the spec | M4's links (S6) with the tools |
| D5 | **The composer's tools button does nothing** (`composer.dart:324`) | The tools are M5–M8 | Not shown until the tools come, rather than doing nothing |
| D6 | **The alert steps that open tool sheets** (Record stock in, Reorder, Review order, Assign rider, Match it) | Declared out of M3 | Not shown until their sheets come, rather than doing nothing (the web shows them, so parity pictures keep them: say so in As built) |
| D7 | **Signing up with a phone code and the sign-up conversation** (§3.6) | The app signs up as the web does (As built M2) | Needs the server's `app` way in; plan it with M4 or as its own item |

## 7. M3's own "done when", not yet met

| # | What | Work |
| --- | --- | --- |
| T1 | **Against the real server** (§8): integration tests on an Android emulator in CI, against `web/e2e/serve.sh` | `mobile/integration_test/`, a CI job |
| T2 | **The shared list of chat scenarios** (§8): two businesses talk, a message that did not go, a reaction both see… one list both suites follow | Write the list (`docs/spec/chat-scenarios.md` or beside the e2e); mark which web e2e tests and app integration tests cover each |
| T3 | **Two businesses chat from two phones; an owner sets up their shop, adds products and publishes a workflow by talking to `@Taskiem` in the app** | Integration tests for both (two app instances or the app and the web); checks that Taskiem's answers with images (a `qr`) and long workflows draw |
| T4 | **Every web link in the chat is a screen in the app** (§9.3) | A test listing the addresses the chat links to (`lib/tools.ts`, the chat's pages) failing for one without a screen and not in §3.5 (the tools' screens listed as M5–M8 until then) |
| T5 | **Large text and 360 px** (§8): every screen | Widget tests at 1.3× and 2× text scale, at 360 px, both themes: nothing cut off or overflowing |
| T6 | **48 px touch targets, labels on everything pressed** (§8) | A semantics test over each screen (Flutter's `meetsGuideline(androidTapTargetGuideline)` and `labeledTapTargetGuideline`) |
| T7 | **The chat film's acts** | The acts without tools (1, 2, 3, 4, 5, 8, 11, 12, 13, 14) as parity screens where not yet; acts 6, 7, 9, 10, 15 come with the tools |
| T8 | **Real phones** (§4.2, A8): a cheap Android, a mid-range Android, an iPhone | Needs people (A8); `mobile/RELEASE.md` checklist run once |

## 8. Order of the work

1. B1–B3 together (one change to how the app is put together), then B4–B7.
2. B8–B12 and C1–C2.
3. G7, G9, G10, G13, G3, G16 (small, each on its own).
4. G4, G5, G11 (drafts, opening files, previews).
5. G1, G2 (the browser, connecting), with the server's part.
6. G6, G8, G14 (cards and tags), G12 (the chime).
7. S1–S5.
8. D1–D7 settled in the spec, and the not-yet buttons hidden.
9. T1–T7; G15 with the server; T8 with A8.

After each step: `flutter analyze`, the whole app suite with the parity
test, the web parity spec where a web picture changed, and the spec's As
built brought up to date.
