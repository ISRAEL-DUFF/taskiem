# Explore that is never empty, and an app that keeps what it loaded

Asked by the founder, 13 November 2026:

1. **Explore** should show something before anyone types: featured businesses, new ones, and so on, not a blank screen.
2. **The app should keep what it loaded on the phone**: chats, photos, voice notes and the rest. Then it opens at once on what was there last, instead of loading everything afresh each time.

Settled by the founder, 13 November 2026: every suggestion in §4 is taken, and every part of §5 is to be built, the saved database for chats included; the app is to be as robust offline as WhatsApp.

**Built so far:**
- X1 (§2.3; Explore's empty page offers to be found and to set the area).
- K1 to K5: files, the encrypted store with chats and messages, only what changed, pages kept with `ETag`, the offline outbox.
- X2 to X4 on the server and the app (migration 00427):
  - the sections Featured, Trusted on Taskiem and Popular, and the category chips with their counts;
  - categories chosen in Your business in Explore (the app);
  - Recently viewed, kept in the phone's store: a business is counted as looked at when its photos are swiped or it is acted on;
  - the operators' console has *Featured in Explore* (add a pick until a date, end one).
- X2 to X4 on the web: the same sections and chips in Explore, and *What you sell* in Your business in Explore. Recently viewed is kept in the browser for the business signed in, and forgotten on signing out.
- The rest of §2 and §3, on the server, the web and the app:
  - **Across Nigeria** (E-S4, migration 00428): with no one in our area, or no area set, findable businesses from anywhere, the fullest cards first, in Near you's place.
  - **The chats with no links** suggest Featured first, then Near you (or Across Nigeria), then Delivery and New.
  - **Categories asked** when a business makes itself findable with none ("What do you sell?", which can be put off).
  - **The Featured card** is wide, with the first photo as a cover (or the initial on a tint), then the logo, name, line and Connect.
  - **Pages drawn at once** (§3.1): a page kept from before is shown at once and brought up to date behind it; a screen reads again when the fresh copy differs. "Updating…" shows over the tabs when that takes more than a second. The business profile and the switch to be found are never kept: they are edited and saved whole.
  - **Pictures and files kept long** (K-S4): a picture asked for by its version, and every attachment, may be kept 30 days (`private, max-age=2592000, immutable`).
  - **Tests:** the sections at twice the text on a small phone, and the start page as a parity screen (`explore-start`).
- After review (migrations 00429, 00430):
  - **Kept pages are named**, not every read: Today's, Money's and Orders' lists, the tools' lists and Explore's start page. Who is signed in, the app's settings, forms, a section's next page and searches are never kept. Of the kept ones, only the screens that redraw in place are drawn at once; the lists a form also reads (the stock a sale is taken from) ask the server first.
  - **A slow answer never wins**: an answer to an earlier ask arriving after a newer one is not kept. A write ends any "just brought up to date" answer from the phone, and another business starts afresh.
  - **"No connection"** only when there is none; a slow answer is not offline.
  - **Reported businesses** (reports not yet looked at) leave every section and cannot be featured. A pick may start on a later date.
  - **Popular's count** is rounded below ten too: "3+", "5+", then tens, and the section is ordered by what is shown.
  - **Trusted** counts the activity of those who run a business (owner, admin, operator).
  - **Scores kept**: whether a business is Trusted, and its count of links, are worked out every ten minutes (`explore_scores`), not for every business on every opening of Explore. What may be shown at all is still read live.
  - **Recently viewed** shows each business's link as it is now, and is kept per person and business on the web. Each time Explore opens it is checked with the server (`section=known`, migration 00431): a business that stopped being findable, or was suspended or reported, goes; the rest are shown as they are now.
  - **Attachments** are kept by a browser for a day, not 30, so a deleted message's file does not linger there; on the phone, deleting a message deletes its kept file too. Business photos asked for by their version are still kept 30 days.

## 1. Where things stand

**Explore** (web `Explore.tsx`, app `lib/chat/explore.dart`, server `GET /v1/explore`, `engine/links/explore.go`):
- With nothing typed, it already asks for three sections:
  - *Near you*: findable businesses in our own area;
  - *Businesses that deliver*: those covering our area first;
  - *New on Taskiem*: the newest findable businesses.
- Each section has "Show more".
- A section with no businesses is hidden. When all three are empty, the page says "No businesses to show yet".
- Only businesses that **chose to be found** appear, and the ones we are already linked with are left out.

On staging few businesses are findable, so all three sections come back empty, and Explore looks blank. Two more blank places come from the same gap:
- **The Chats list with no links** (the founder's screenshot): it shows only a line of text ("No linked businesses yet. Search for…") and nothing to tap.
- **Near you** finds nothing when our own area is not set, or no one else is in it.

**Keeping things on the phone**: the app keeps almost nothing between launches.
- **In memory only**, while the app runs:
  - photos, PDFs and voice notes (up to 50 MB, the least lately seen let go first: m3-fixes B10);
  - logos and Explore's pictures.
  - All of it is gone when the app is closed.
- **Kept on the phone**:
  - the session, in the phone's secure storage;
  - a few settings in `Prefs`: the theme, the lock, the last tools used, and drafts.
- **Fetched afresh every time the app opens**:
  - the chat list, every conversation's messages, Taskiem's conversation;
  - Today, Money, Orders and every page.
  - Each shows a loading state first.
- **What the server already offers that helps:**
  - messages page by `before` and `after` (`api/link_chat.go`), so the app can ask only for what is newer than what it holds;
  - the live stream (`/v1/chat/stream`) tells of changes while the app is open;
  - pictures carry a version (`v`), so a kept copy can be trusted until the version changes.

## 2. Explore: never empty

### 2.1 The page with nothing typed, top to bottom

| Part | What it shows | Where it comes from |
| --- | --- | --- |
| **Categories** | Chips across: Food and drinks, Building materials, Fashion, Phones and gadgets, Logistics…, each with how many businesses are in it. One tapped lists that category | New: a business has no category today (the categories "@" knows are products' within a catalogue). Each business picks one or two from a fixed list in Your business in Explore |
| **Featured** | A wide card that swipes, 3 to 6 businesses: logo, cover photo, tagline, Connect | New: picked by Taskiem (§2.2) |
| **Near you** | As now | As now |
| **Trusted on Taskiem** | Businesses with a verified phone or business, a complete profile (logo, tagline, at least two photos) and someone of theirs active on Taskiem in the last 30 days | New section, from data the server already has: `tenant_verifications`, the profile, and when their people last wrote in on WhatsApp or Telegram (`last_active_at`) |
| **Popular** | The businesses with the most links in the last 90 days | New section: a count of active links, never who they are with |
| **Businesses that deliver** | As now | As now |
| **New on Taskiem** | As now | As now |
| **Recently viewed** | The last ten businesses opened here | Kept on the phone only (§3), never sent to the server |

The rule that matters most: **the page is never blank**.
- When our area has no one in it, Near you is replaced by "Across Nigeria".
- When every section is empty, Featured and the categories are still there.
- With no businesses findable at all, it says how to be the first: "Make your business findable", which opens Your business in Explore.

### 2.2 Featured: who chooses

**Suggested: Taskiem picks, and the page fills in by itself until it has.**
- Taskiem's team marks a business as featured, until a date, from the operators' console. That needs a small new table: `explore_featured(business, from, until, note)`, with the operator, recorded.
- Until there are picks, or to fill the row to its six, the best of *Trusted on Taskiem* fill in, chosen at random each day, so it is not always the same few.
- A business is featured only while it is still findable and in good standing. One suspended or reported falls out at once.
- A business cannot pay to be featured for now. That could come later, as a plan feature.

### 2.3 Chats with no links

Where the Chats list says "No linked businesses yet", it shows instead:
- a row of the first Featured and Near you cards, each with Connect;
- a **Find a business** button that opens Explore.

The same goes for the web's chat page.

### 2.4 Privacy

Every new section keeps Explore's rules:
- only businesses that chose to be found;
- only what they show in public;
- never ourselves, and never who links with whom. Popular uses a count only, rounded, and never shown below 3.

The categories' counts are of findable businesses only.

### 2.5 What the server needs

| | What |
| --- | --- |
| E-S1 | `GET /v1/explore` (nothing typed) also answers `featured`, `trusted`, `popular` and `categories` (each category with its count) |
| E-S2 | `section=featured \| trusted \| popular \| category&category=…` with offset, as the sections page now |
| E-S3 | `explore_featured` and its place in the operators' console (add, end, list), audited |
| E-S4 | "Across Nigeria" when our area is empty or unset |
| E-S6 | A business's categories: a fixed list, one or two chosen in Your business in Explore (web and app), shown on its card; asked of a business when it makes itself findable |
| E-S5 | Tests: each section's rules (findable only, linked left out, a suspended business never featured, Popular's count rounded and hidden under 3) |

## 3. The app keeps what it loaded

### 3.1 How it behaves

**Show what we have at once, then bring it up to date.**
- **Opening the app.** The chat list, Today, Money and Orders draw at once from what was kept, with no loading state. Each then asks the server for what changed and updates in place. A small "Updating…" shows only if that takes more than a second.
- **A conversation** opens on the messages kept. The app asks only for messages *after* the newest one it has, then follows the live stream as now. Scrolling up past what is kept loads older ones from the server, as now, and keeps them.
- **Photos, voice notes, PDFs and logos** are kept on the phone as files. A photo seen once opens at once next time, even offline. A voice note played once plays offline. The picture's version (`v`) says when a kept copy is stale.
- **Offline.**
  - Everything kept can be read. A bar says "You're offline".
  - Messages written are kept in an outbox and sent when back online, each with its own client id, as a retry does now. They show the clock until then.
  - Anything that changes money or stock (an approval, a payment, an order) is not queued: it needs the connection, and says so.
- **Explore**:
  - the start page is kept for an hour, so it opens at once, then refreshes;
  - Recently viewed is kept on the phone only.

### 3.2 What is kept, and for how long

| Kept | How much | Where |
| --- | --- | --- |
| The chat list, Taskiem's conversation, customers' chats | All of it | The phone's database |
| Messages | The last 200 in each conversation, and any starred ones | The phone's database |
| Today, Money, Orders, the tools' lists (products, stock…) | The last answer of each | The phone's database |
| Photos, voice notes, PDFs, logos, Explore's pictures | Up to 300 MB in all; the least lately seen go first | Files in the app's own cache folder |
| Drafts, Recently viewed, the last tools | As now | `Prefs` |

The phone may clear the cache folder when it is short of space. Nothing is lost by that: it is fetched again when needed.

### 3.3 Safety of what is kept

What is kept holds a business's conversations, customers' names and amounts of money. So:
- **Encrypted on the phone.** The database is encrypted with SQLCipher. Its key is made at random on first use and kept in the phone's secure storage, as the session is. Files are kept in the app's private folder, which other apps cannot read.
- **One store per business and person.** Moving to another business (or another account) opens that one's own store. Nothing from one shows in the other.
- **Signing out wipes it**: the database and the files.
  - So does a session the server ends, as when a device is signed out from Settings.
  - So do 30 days without opening the app, as the session would have ended.
- **Behind the app lock.** The lock covers what is drawn from the store, as it covers everything now.
- **Personal data revealed on purpose** (`pii.reveal`) is never kept: it is fetched each time, as now.

### 3.4 How it is built (the app)

- **A local database: Drift** (SQLite, with SQLCipher), holding:
  - tables for conversations, messages, the last answers of pages, and the outbox;
  - each row keyed as the server keys it, so an update replaces it.
- **A file cache** for media, by the server's path and version, with the size limit (replacing the in-memory 50 MB keep of B10).
- **A thin layer between the screens and the transport.**
  - It answers from the store first, then from the server, and tells the screen of each.
  - The screens change little: what they read comes first from the store.
- **Drafts and the outbox** survive the app being closed.
- **The web** can do the same later with IndexedDB. It is less needed there, since a browser keeps its own cache of pictures. The app comes first.

### 3.5 What the server needs

| | What |
| --- | --- |
| K-S1 | The chat list answers only what changed since a time (`?since=`), with ids of conversations gone |
| K-S2 | `ETag` and `If-None-Match` on the pages' answers (Today, Money, Orders, the tools' lists), so an unchanged page costs a `304` and no body |
| K-S3 | Messages deleted or edited since a time, so the kept copies follow (the stream tells of them live; this is for while the app was closed) |
| K-S4 | Pictures and files served with long cache lifetimes, keyed by their version |

Messages already page by `before`/`after`, and the stream already tells of changes live.

## 4. Settled (13 November 2026: each as suggested)

1. **The categories:** the list of business categories, and whether a business picks one or two (suggested: two). Businesses already findable are asked to choose the next time they open Your business in Explore.
2. **Featured:**
   - Taskiem's picks, filled in by Trusted businesses (suggested);
   - or chosen automatically only;
   - or paid for (later).
3. **Popular:** show it, using link counts (suggested: yes, rounded, never under 3), or leave it out.
4. **Encryption of what is kept:** suggested yes (SQLCipher). It makes the app slightly larger, about 7 MB, and costs a little speed.
5. **How much is kept:** suggested 200 messages a conversation and 300 MB of media. More keeps more offline; less saves space.
6. **The web too:** later, after the app (suggested), or at the same time.

## 5. Order of the work

| # | Part | Size |
| --- | --- | --- |
| X1 | Explore never empty: Across Nigeria when the area is empty; the Chats-with-no-links cards; the "Make your business findable" way in | Small, server and both apps |
| X2 | Trusted on Taskiem; then categories (the list, choosing them in Your business in Explore, the chips) | Medium |
| X3 | Featured: the table, the operators' console, the row | Medium |
| X4 | Popular, and Recently viewed (on the phone) | Small |
| K1 | The file cache for photos, voice notes, PDFs and logos on the phone (the biggest win for the least work) | Small |
| K2 | The local database, encrypted, with the chat list and messages; the app opening at once on them; wiping on sign-out | Large |
| K3 | Only what changed: the chat list's `since`, messages `after`, deleted and edited since | Medium, server and app |
| K4 | Today, Money, Orders and the tools' lists kept, with `ETag` | Medium |
| K5 | The outbox: messages written offline sent when back | Medium |

Each part comes with its tests:
- **Server:** each section's rules, and `since` and `ETag`.
- **App:**
  - kept data drawn before the server answers, and updated after;
  - offline reads;
  - the wipe on sign-out;
  - the size limits;
  - large text.
- **Web:** e2e and parity for Explore.

K1 and X1 can start at once. K2 is the largest part, and the rest of keeping things builds on it.
