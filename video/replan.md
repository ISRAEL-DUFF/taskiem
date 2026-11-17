# Video replan, 17 November 2026

Written after merging `main` (236 commits since the videos were made) into the
video branch. Sources: `docs/spec/mobile-app*.md`, `mobile/README.md`, the git
log, and `web/e2e/demo/` (unchanged since the videos were recorded).

## 1. What changed in the product

| Change | State today | What it means for video |
| --- | --- | --- |
| **A native phone app** (Flutter, Android and iPhone): the chat, Explore, linked businesses, selling, orders and deliveries, money (invoices, bills, matching, approvals with a passkey) | Built, milestones M1 to M8. **Not in the stores**: M9 (closed testing with design partners, then Play Store and App Store) comes next | A "Taskiem on your phone" film is now possible. No store badges or "download now" until M9 |
| **The app looks like the web chat inside the chat** (a parity test holds them to the same pictures) | Done | Our chat films and how-tos stay valid for the app: a screen recording of the web chat at phone size *is* the app's chat |
| **Pages away from the chat are redesigned for the phone** ("Direction B"): Today, Money, Orders, Team, invoices, stock, settings as one list. They **no longer mirror the web** | Done in the app | Any video that shows one of these pages is showing the web layout, which is not what the phone app shows. See section 3 |
| **Explore is never empty**: Featured, Trusted on Taskiem, Popular, category chips, Recently viewed, and "Across Nigeria" when no one is near | Built, web and app | Chat film scene 12 and how-tos 1 and 15 show the old Explore. They need refreshing |
| **The app keeps what it loaded** (works offline like WhatsApp: chats, photos, voice notes, unsent messages sent when back online) | Built | New story: "no network, still works" |
| **Notifications, links that open the app, passkeys, sign up in the app as on WhatsApp, app lock, mute a customer's chat** | Built (M4), but real push needs the Firebase project, not yet set up | New how-tos for sign-up, app lock, mute. Push on a real phone cannot be filmed yet |
| **Updates: Status and Channels** (as WhatsApp's) | **Spec only**, no code yet | Do not film or mention |
| **Made tools** (a workflow becomes a tool in the chat) | **Plan only**, "nothing here is decided" | Do not film or mention |
| A large hardening wave: sealed private data, audit chain, exactly-once effects, stock and money races fixed, VM install kit | Built | Strong material for a trust film for regulated buyers (idea 4 in our first slate) |

The merge was clean, and `video/` itself did not change on main.

## 2. What happens to the existing videos

| Video | Verdict | Why |
| --- | --- | --- |
| Chat explainer, hybrid cut (2:50) | **Keep, small update** | Concept and numbers still hold. Re-render scene 12 (Explore) from the new Explore. Re-render any scene whose screen changed ("Accept and Decline side by side", sheets that go back one level), checked against the app's parity pictures in `mobile/test/parity/web/` |
| Chat shorts (Morning, Tools, Trade, Staff, Explore, Deals) | **Keep**, rebuild after scene 12 | Explore short changes |
| How-tos 02, 03, 04, 07, 09, 10, 12, 13, 14, 16, 17 (chat-based) | **Keep for now** | They record chat screens, which match the app |
| How-tos 01 and 15 (find a business, connect) | **Re-record** | Explore changed |
| How-tos 02 (order page), 03 (invoice page), 05 (Staff page), 06 (Tasks page), 08 (Deliveries page), 11 (Money, To match) | **Decide web or phone** | They show a page away from the chat. The web layout differs from the phone app's. See section 3 |
| Narrator scripts (`howto/narration.md`) | Keep; edit lines for any re-recorded video | |
| Characters, bible, the eight Flow clips | **Keep** | Nothing about the cast changes |

## 3. The decision that shapes everything: which screen do we show?

Most of our users are on phones, and the phone app is the future. But our
recorder drives the **web** app in a browser at phone size. Two consequences:

- **Chat screens**: identical on web and phone, so recording the web is fine.
- **Pages away from the chat**: only the phone app shows the new look, and
  recording the real app needs a Flutter build on an Android emulator or a
  phone. This environment has no Flutter or Android SDK, so those recordings
  would be done on your side (or a machine with the SDK), by screen recording.

**Recommendation:** keep recording chat scenes from the web now. For the pages
away from the chat, wait to record until the app is in closed testing, then
screen-record the real app on a phone (a human with a phone is also the
cheapest way). Until then, avoid showing those pages in new videos.

## 4. New slate

| # | Video | Length | Needs | Ready to make? |
| --- | --- | --- | --- | --- |
| 1 | **Chat explainer v2**: scene 12 refreshed, same cutaways and narration | 2:50 | Re-render scene 12; rebuild | **Yes, now** |
| 2 | **How-tos 01 and 15 re-recorded**, then narration re-checked | 60 to 90 s each | The demo server, our recorder | **Yes, now** |
| 3 | **"Explore" short**: Featured, Trusted, categories, Across Nigeria (Kola and Ada) | 30 s, 9:16 | New scene from the new Explore | **Yes, now** |
| 4 | **New how-tos**: sign up in the app as on WhatsApp; the app lock; mute a customer's chat; Recently viewed and categories in Explore | 45 to 60 s each | Recorder for the web parts; the phone for sign-up and app lock | Partly. Web parts now, app parts later |
| 5 | **"Taskiem on your phone"**: no network, still works; open at once on what was there; passkey to approve; the chat on a real phone | 60 to 90 s | A screen recording of the real app, plus Flow cutaways (Ada, Tunde) | After a closed-testing build exists |
| 6 | **Trust film for regulated buyers**: durable runs, exactly-once effects, audit chain, sealed private data, hosted in Nigeria, install kit | 90 s | Script from the docs; no app footage needed (diagrams and the dashboard) | **Yes, but needs your answer: is this audience a priority?** |
| 7 | **Store launch film** | 30 to 45 s | The stores | Not until M9 |

## 5. Suggested order

1. Merge is done. Update scene 12 and rebuild the chat film (mostly
   rendering time).
2. Re-record how-tos 01 and 15 and re-check their narration.
3. Explore short (new).
4. The trust film, if that audience matters now. Otherwise the new how-tos.
5. Phone-app film once a build is available.

## 6. Questions for you

1. **Audience first**: shop owners using Chat, or regulated businesses buying
   the workflow engine? (Asked before; the repo's README leads with the engine.)
2. **Screens**: OK to keep recording chat from the web, and wait for the real
   app for the pages away from the chat?
3. **Do you want the phone-app film at all before the app is in the stores?**
4. **The repository moved** to `ISRAEL-DUFF/taskiem` (GitHub says so on every
   push). Shall I switch this clone's remote to the new address?
