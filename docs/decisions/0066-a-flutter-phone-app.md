# 0066 — A phone app in Flutter: the web chat as a phone app, like WhatsApp, doing everything the web chat does

Date: 2026-11-11 · Status: **Accepted** (11 November 2026; not built) · Plan: [the Taskiem phone app](../spec/mobile-app.md)

## Context

Taskiem reaches a business's people in three ways: WhatsApp, Telegram and Slack, where most traders are; the web app, which can be added to a phone's home screen; and the field agent. The web app's chat has grown into the business's control room ([chat tools](../spec/chat-tools.md), phases 1 to 7, and the flows after them), about 17,000 of its 88,000 lines. [Chat push](../spec/chat-push.md) and [offline](../spec/offline.md) give the home-screen app notifications and an honest offline page, but on a phone the web app still signs out every 12 hours, its notifications depend on the browser, and it is not in the Play Store, where traders look for apps.

The founder asked, on 11 November 2026, for a native app in Flutter, and settled its scope the same day: a chat-only app like WhatsApp, with all the chat tools working seamlessly, implementing every screen the chat needs (invoices, orders, products…) as the web does them today, and no linking to the web unless absolutely necessary; the web chat's capabilities today are what the app is.

## Decision

1. **A native app, in Flutter**, for Android and iPhone from one code base, in `mobile/` in this repository.
2. **The web chat, all of it, and nothing else.** `@Taskiem` (the same conversation as Taskiem on WhatsApp, so every command, set-up included: the shop and its fronts, products and stock, bookings, invoices and money, tables, building and publishing workflows with AI, connecting services, inviting staff, the plan), the conversations with linked businesses and customers, every chat tool, and every screen the chat's tools and cards lead to, built from the web's own. What the web chat gains (tasks, receipts, quotes, restocking, paying people, riders) the app gains after it.
   **A browser only where the web chat itself needs one** ([plan §3.5](../spec/mobile-app.md#35-where-the-app-opens-a-browser-and-why)): signing in to a service by OAuth, the workflow canvas for those who want it, and nothing about paying for a plan, which is charged to the wallet. A test fails when the web chat links somewhere the app has no screen for.
3. **The server owns behaviour.** The app is a client of the public API, through a client generated from the API description; a rule that exists only in the app is a bug. A chat feature is done when it is in the web and the app, or its spec says why not.
4. **Nothing of the business is kept on the phone** in the first release, as on the web ([offline](../spec/offline.md)); a cache is a decision of its own.
5. **Staying signed in** is a device session the server lists and can end, opened with the phone's own lock; step-up for money is unchanged.
6. **Chat notifications through Firebase Cloud Messaging**, as one more transport of chat push, with the same rules.
7. **Android and iPhone together**, from the same code, built and tested on both throughout and released together.
8. **A plan is paid from the business's Taskiem wallet** ([decision 0052](0052-taskiem-as-a-collection-rail.md)), internal to Taskiem: never a store purchase or a card checkout in the app. Charging plans to the wallet is a server change with a plan and decision of its own.
9. **Signing up and signing in, both in the app**, signing up as on Taskiem's WhatsApp number, under decision 0058's rules, with `app` as a way in.
10. **The app looks, moves and sounds exactly like the web chat.** The demo and advert films are made from the web chat, so the web chat is the design. The app's theme is generated from the web's own CSS (colours, type, radii, the motion tokens and curves), its parts are built to match the web's kit (never Material or Cupertino), and a parity test compares every screen and moment of the two on the demo world, in CI, on every change to either.

## Alternatives considered

| Alternative | Why not |
|---|---|
| The web app on the home screen only, with chat push and offline | Built, and kept: it may be enough for many. But sessions of 12 hours, notifications that depend on the browser, and no store listing are what owners will meet every day. Recorded as the case against: the critical path to real users is still the people items of the [chat-first strategy](../strategy/chat-first-nigeria.md), not code |
| The web chat inside a native shell (a WebView with native notifications) | It would match the web chat's look by construction, which the films now require, with no second chat to keep the same. Not chosen: the founder asked for Flutter, for an app quick on a cheap Android phone that the stores accept (they reject thin shells). The cost of that choice is the parity test, which must hold the two the same on every change; if it cannot, this alternative is the fallback |
| React Native, sharing code with the web app | Shares the language, not the screens: the web's chat is built on the DOM. Flutter draws the same on every phone and is quick on cheap Android ones; Xenibase already ships a Dart SDK |
| The whole web app in the app | Doubles everything for pages rarely opened on a phone. The chat first; other parts move in after it when people and the app's numbers show they are needed |
| The chat, approvals, Home and Orders in the first release | The first draft of this plan. The founder chose the chat instead, with everything it leads to |
| The chat only, opening the web for what its cards and tools link to | The second draft. Rejected by the founder, who wants the app complete in itself: no linking to the web unless absolutely necessary |
| The chat and its screens, with set-up (sign-up, connecting, staff, plans) left to the web | The third draft. The founder pointed out that the web chat already does all of it, through `@Taskiem` |
| An app per kind of user (owner, staff, rider) | Three listings and three reviews for one account model; roles already decide what a person sees |

## Consequences

- Server changes, each with its tests ([plan §5](../spec/mobile-app.md#5-what-the-server-needs)): `GET /v1/app/config`; device sessions and **Your phones** under Account; FCM beside Web Push in `engine/push` and a `kind` on `push_subscriptions`; the `.well-known` files for links that open the app's screens and for passkeys, and the app's origin among the WebAuthn origins; reads that answer a screen in one call where the web needs several.
- `tools/licencecheck` reads `pubspec.lock`; the font exception for Inter covers the app's bundled font (founder's approval).
- A Go command in `tools/` generates the app's API client and its theme from the web's tokens; tests fail when either is stale.
- CI builds the Android app and runs its tests against the same server the web's browser tests use.
- People ([plan §11](../spec/mobile-app.md#11-what-needs-people)): the name (L3) before any listing; Google Play and Apple developer accounts; a Firebase project and its terms; signing keys; a Flutter engineer.

## Provenance

The founder's request of 11 November 2026, after chat push (#173) and offline. Nothing here copies another product's code or design; the app follows Taskiem's own [screens guide](../brand/screens.md).
