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
| **Schools in the phone app** (a School tab, students, desk payments, chasing, clearance and the gate, setup; school figures in the briefing; approval cards) | Built 18 Nov, "waiting for the founder's word" | The Schools series is phone-first (`series/schools/README.md`) |
| **Updates: Status and Channels** (as WhatsApp's) | **Spec only**, no code yet | Do not film or mention |
| **Made tools** (a workflow becomes a tool in the chat) | **Plan only**, "nothing here is decided" | Do not film or mention |
| A large hardening wave: sealed private data, audit chain, exactly-once effects, stock and money races fixed, VM install kit | Built | Strong material for a trust film for regulated buyers (idea 4 in our first slate) |

The merge was clean, and `video/` itself did not change on main.

## 2. What happens to the existing videos

| Video | Verdict | Why |
| --- | --- | --- |
| Chat explainer, hybrid cut (2:50) | **Redo the app scenes** | I first said "keep, small update". That was wrong: I judged from the docs, not from the pictures. Side by side with the app's current screens (`mobile/test/parity/web/`), our hand-built scenes differ: the conversation header, the Explore page (now a full "Find a business" page with search, categories and Featured cards, not a bottom sheet), the tools panel (now a grid of tiles with counts), the order and offer cards, the composer, and the phone frame around everything. The story, script, narration, characters and Flow clips still hold |
| Chat shorts (Morning, Tools, Trade, Staff, Explore, Deals) | **Rebuild** after the scenes are redone | Same reason |
| How-tos 02, 03, 04, 07, 09, 10, 12, 13, 14, 16, 17 (chat-based) | **Keep; spot-check** | They are recordings of the real web chat, so they follow the app's current look when re-run. Check each against the parity pictures before use |
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

1. Merge is done. Redo the chat film's app scenes so they match the app's
   current look (see section 7), then rebuild the film with the same cutaways
   and narration.
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

## 7. How to make the scenes match the app, and keep them matching

Our 16 scenes are hand-drawn web pages (`films/chat/scenes/*.html`) built from
the design boards. Every time the app's look changes, they go out of date, as
now. Two ways to fix it:

| | A. Redraw the scenes in the kit | B. Record the real app (recommended) |
| --- | --- | --- |
| How | Update `kit.css` and the 16 HTML scenes to the new look | Use the real web chat on the demo world (`recording/` and `howto/` already do this) and add our captions, music and end card around the recordings |
| Matches the app | Only as well as I redraw it | Exactly, by construction (the app's own parity test keeps web and phone the same in the chat) |
| Next time the app changes | Out of date again | Re-run the recording |
| Motion and polish | Full control (taps, glows, bursts) | Real timings; less choreographed |
| Cost | 16 scenes of hand work and checking | A new set of recording scripts for the film's moments, then rendering |

Go, Postgres 16 and psql are installed in this environment, so the demo world
(`bash web/e2e/demo/serve-demo.sh`, `seed.ts`) should run here and I can record.
I have not started it yet.

## 8. Update, 19 November 2026: "Taskiem for everyone"

Merged main again (393 commits). The biggest change is not a feature but the
product's audience: decision **0068, "Taskiem for everyone"** (accepted
16 November). Source: `docs/spec/taskiem-for-everyone.md`,
`people-chatting.md`, `people-chatting-5b.md`.

**What it is.** Anyone can now use the Taskiem app, with no business needed: a
person signs up with a phone number and deals with businesses (ask, order,
book, pay) in one conversation each, and can also chat with people. **This is not
a rival to WhatsApp or Telegram** (founder, 19 November): Taskiem is there to
automate businesses alongside their conversations, in the most natural and easy
way. WhatsApp and Telegram stay; Taskiem works with them (a business can even
run on its own WhatsApp number). People-to-people chat is a supporting part, not
the point. Running a business is something a person *does in* Taskiem. A person
with no business can also start workflows alone (Start something).

| Stage | What | State (per the specs' "As built" sections) |
| --- | --- | --- |
| 1 Personal accounts | Sign up with a phone; **You** tab; switch between businesses | Built |
| 2 Customers chat with businesses | In the app **or in the phone's browser with nothing to install** (a business's chat link, `/m/{code}`); orders, bookings and payments as cards; no 24-hour window | Built |
| 3 Discover and Orders | Customers find businesses in **Discover**; **Your orders** in one place | Built |
| 4 One customer across the app and WhatsApp | The business sees one customer, whichever line they use | Built |
| 5a People chat with people | Find people by phone or link; one-to-one chats; a stranger's first message waits as a request | Built |
| 5b Groups and **ordering together** | Groups; a family or an office picks together from one business, one person pays or each pays their share | Built (web and app) |
| 5c Calls | Voice and video calls | **Planned, not built** |

**Held back:** the legal drafts (privacy notice, terms for people chats) are
"for counsel"; the store listings and the app's release are pending. So the
audience is real, but **a public launch is not ready**.

### What this means for the videos

1. **A new audience and a new series.** Customers are now users too, so they get
   a series about **dealing with businesses on Taskiem** (not a messenger tour) ("Taskiem for you": sign up, find a
   business, chat, order, pay, track, group chats, order together). Outline:
   `series/everyone/README.md`.
2. **Every business series gains a chapter.** Chapter 3 ("Take orders") now has a
   second door: customers chat with the business in the app or in a browser
   through its **chat link**. Businesses put that link on Instagram and the
   storefront. The shop owner's view: one customer, two lines.
3. **Explore versus Discover.** Explore is businesses finding businesses.
   Discover is the public finding businesses. The earlier Explore refresh is
   still right but the two must not be confused in narration.
4. **The characters change role.** Chioma, Nneka, Kehinde and others are now
   possibly *Taskiem users themselves*, not only names in a shop's chat.
5. **Claims to avoid.** People chats are **not end-to-end encrypted** and the
   app says so; never say or imply it. No calls yet. No "download now" until
   the stores. Group and ordering-together features are built but new: film
   them only against the real app.
6. **Surfaces.** The new screens (You, Discover, Your orders, groups, baskets)
   are the phone app's own look, so they need a screen recording of the real
   app, as in section 3. The customer's browser chat (`/m/{code}`) can be
   recorded from the web.

## 9. Update, 23 November 2026: store listing, made tools, pages, saved things, payments

Merged main again (407 more commits). Sources: `docs/store/google-play/listing.md`,
`docs/spec/build-plan-saved-tools-payments.md`, decisions 0070 to 0073.

### 9.1 What is new, and how real it is

| Area | What it is | State | Film it? |
| --- | --- | --- | --- |
| **Google Play listing** | App "Taskiem: Run your business", category Business, free, package `app.taskiem.chat`; 8 store screenshots | **Draft for the founder.** Nothing published yet | Words and taglines: yes (section 9.3). "Download" calls to action: not yet |
| **Made tools** (decision 0073, accepted 23 Nov) | Any workflow declares itself a **tool in the chat**: a tile, a `/` command, a form, cards and buttons, a question inside a run, tools for customers | M0 to M5 **built** (T0 to T5). M6 (recipes and the AI builder) **not built** | Yes, the built parts. It is the "automate the rest" story |
| **Pages** (decision 0072, accepted) | A business's own screens, drawn from its tables (heading, figure, list, table, row detail card), with buttons | G0 to G3 **built** | Yes, as part of "automate the rest" |
| **Saved objects** (decision 0071, proposed) | Things kept and called up by `@name`: bank accounts, people, places, text. "deliver to @warehouse", "invoice @acme 250k", "remind me when @car insurance is due"; shared as cards | S0 to S3 **built** (web, app, chat apps) | Yes |
| **Payments** | Pay a bill by transfer in the chat; pay one person safely (limits, passkey); pay from a message; batches of up to 100; payroll (Payrolla) | Built **against fakes, not on a real wallet** | **No**, not as a working feature, until a real wallet is connected |
| **Device fields** | Scan a barcode, sign, location and photo in a form; a stock item's barcode; the app reads text from a photo on the phone (account details) | D0 to D2 **built** | Yes (barcode scan suits shops) |
| **Schools in the chat** | School figures in the briefing, approval cards, school tools | Built | In the Schools series |
| **Stock apart from products** (decision 0070) | Count things a business buys but never sells (fabric, flour, diesel) | Accepted, **not built** | No |

### 9.2 What it changes in the plan

1. **The store launch is closer.** Episode 0 and the series calls to action stay
   "Start your free trial" until the listing is published, then add the Play
   link (iPhone later). The "store launch film" (slate item 7) becomes real: it
   can reuse the listing's eight screenshots and taglines.
2. **A new chapter for every series: "Automate the rest"** (made tools, saved
   things, pages). It speaks to the original product, the workflow engine: a
   business turns a routine into a tool in the chat. Film only the built
   parts; leave out the AI builder until M6. Proposed as **Episode 11** of
   Fashion, Food and Gadgets (an owner's own tool, a page of their tables,
   `@warehouse`), not a new series.
3. **Hero features gain real material:**
   - **Gadgets** (Episode 2, 3): barcode scan at the counter and for stock counts.
   - **Fashion and Food:** deliver to `@warehouse`, `@mum`'s address for a
     customer's order; a signature on a delivery.
   - **Every series:** a reminder "remind me when @car insurance is due".
4. **Do not show payments as live.** "Pay Alhaja Sade in the chat" is built
   against fakes. The series use matching incoming money (live) and approvals
   (live); outgoing payments wait.
5. **Positioning:** the founder's amendment to decision 0068 (19 November) says
   exactly what you told me: Taskiem is not a chat-app replacement; it automates
   businesses alongside their conversations. The store listing follows it, and
   our rule in `series/README.md` section 0 already matches.

### 9.3 Official words to reuse

From the listing (draft): **"Orders, money, stock and staff, run from the chat
your customers already use."** The store screenshots carry four taglines:
*Your business, every morning* · *Buy and sell in the chat* · *No order slips
through* · *One customer, any line*. Use these for titles, end cards and
captions so the videos and the store say the same thing.

### 9.4 The look has moved again

The store screenshots show the app's real home: **Today** ("Good afternoon, Ada",
a *Money in today* figure with a chart against a usual Thursday, Sales by hour,
Orders today, Needs you), an **Orders** page (All, Customers, Businesses), and a
conversation that shows which line a customer wrote on (the app or WhatsApp).
Our chat film's scenes (a briefing card, a chat list) predate this. This
reinforces `replan.md` section 7: **record the real app** for the film, and for
Today, Orders and the like use the phone app, not the web.

### 9.5 Open decisions

1. Add **Episode 11, "Automate the rest"**, to the first three series?
2. Should the **store launch film** be made now (for when the listing goes
   live), reusing the screenshots?
3. Do you want the chat film re-made from the real app now, or after the store
   launch decides the look?
