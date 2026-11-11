# 0066 — A phone app in Flutter for owners and staff: the chat, approvals, Home and Orders natively, the rest on the web signed in

Date: 2026-11-11 · Status: **Proposed** · Plan: [the Taskiem phone app](../spec/mobile-app.md)

## Context

Taskiem reaches a business's people in three ways: WhatsApp, Telegram and Slack, where most traders are; the web app, which can be added to a phone's home screen; and the field agent. The web app's chat has grown into the business's control room ([chat tools](../spec/chat-tools.md), phases 1 to 7, and the flows after them), about 17,000 of its 88,000 lines. [Chat push](../spec/chat-push.md) and [offline](../spec/offline.md) give the home-screen app notifications and an honest offline page, but on a phone the web app still signs out every 12 hours, its notifications depend on the browser, and it is not in the Play Store, where traders look for apps.

The founder asked, on 11 November 2026, for a native app in Flutter.

## Decision

1. **A native app, in Flutter**, for Android and iPhone from one code base, in `mobile/` in this repository.
2. **For owners and staff**, doing natively what they do many times a day: the chat (with linked businesses, customers and `@Taskiem`, and the chat tools), approvals, Home and Orders, and notifications. Everything else is opened on the web, signed in, from the app.
3. **The server owns behaviour.** The app is a client of the public API, through a client generated from the API description; a rule that exists only in the app is a bug. A chat feature is done when it is in the web and the app, or its spec says why not.
4. **Nothing of the business is kept on the phone** in the first release, as on the web ([offline](../spec/offline.md)); a cache is a decision of its own.
5. **Staying signed in** is a device session the server lists and can end, opened with the phone's own lock; step-up for money is unchanged.
6. **Notifications through Firebase Cloud Messaging**, as one more transport of chat push, with the same rules.
7. **Android first.** iPhone is built from the same code throughout and released after Android is in use.
8. **No plan purchases in the app.**

## Alternatives considered

| Alternative | Why not |
|---|---|
| The web app on the home screen only, with chat push and offline | Built, and kept: it may be enough for many. But sessions of 12 hours, notifications that depend on the browser, and no store listing are what owners will meet every day. Recorded as the case against: the critical path to real users is still the people items of the [chat-first strategy](../strategy/chat-first-nigeria.md), not code |
| The web app inside a native shell (a WebView with native notifications) | Store presence and native push quickly, with no second chat; but the chat would still feel like a web page on a cheap phone, and app stores reject thin shells. Kept as the way the app opens pages it does not have, in the phone's own browser tab, not inside the app |
| React Native, sharing code with the web app | Shares the language, not the screens: the web's chat is built on the DOM. Flutter draws the same on every phone and is quick on cheap Android ones; Xenibase already ships a Dart SDK |
| The whole web app in the app | Doubles everything for pages rarely opened on a phone. Pages move into the app when the app's numbers show they are needed |
| An app per kind of user (owner, staff, rider) | Three listings and three reviews for one account model; roles already decide what a person sees |

## Consequences

- Server changes, each with its tests ([plan §5](../spec/mobile-app.md#5-what-the-server-needs)): `GET /v1/app/config`; device sessions and **Your phones** under Account; FCM beside Web Push in `engine/push` and a `kind` on `push_subscriptions`; `POST /v1/me/web-link`; the `.well-known` files for app links and passkeys, and the app's origin among the WebAuthn origins.
- `tools/licencecheck` reads `pubspec.lock`; the font exception for Inter covers the app's bundled font (founder's approval).
- A Go command in `tools/` generates the app's API client and its theme from the web's tokens; tests fail when either is stale.
- CI builds the Android app and runs its tests against the same server the web's browser tests use.
- People ([plan §11](../spec/mobile-app.md#11-what-needs-people)): the name (L3) before any listing; Google Play and Apple developer accounts; a Firebase project and its terms; signing keys; a Flutter engineer.

## Provenance

The founder's request of 11 November 2026, after chat push (#173) and offline. Nothing here copies another product's code or design; the app follows Taskiem's own [screens guide](../brand/screens.md).
