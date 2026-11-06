# Building screens in Taskiem's look

This is the guide for anyone, an engineer or a language model, who builds or changes a screen in Taskiem's web app (`web/`). Follow it so every page looks like the same product: the one in the mockups that Home, Approvals and Money were built from.

The [brand guide](README.md) says what the brand is: the mark, the colours, the rules for the files. This page says how the app's screens use it. The source of truth for values is the code: the tokens at the top of `web/src/styles.css` and the components in `web/src/ui.tsx`. If this page and the code disagree, the code is right and this page needs fixing.

**Before building a new screen, open the closest finished one and copy its shape:**

| You are building | Copy |
| --- | --- |
| A page about one thing with money (an invoice, an order, a payout) | The invoice page, `InvoicePage` in `web/src/pages/Invoices.tsx` |
| A page about one thing that runs or waits (a run, a job, a batch) | The run page, `web/src/pages/Run.tsx` |
| A list of things that open a page | Activity (`Runs.tsx`), Invoices, Orders |
| A list of things with their own buttons | Members (`Members.tsx`), Clearance (`SchoolClearance.tsx`) |
| A summary of money for a period | Home's money card (`Home.tsx`), Money (`Money.tsx`), the School overview (`SchoolOverview.tsx`) |
| A decision someone confirms | Approvals (`Approvals.tsx`), its confirm sheet `Decide` |
| A signed-out page | Sign in (`Login.tsx`); the frame is `alone()` in `App.tsx` |
| A checklist of steps | Get started (`Start.tsx`) |

## Who the screens are for

Taskiem's people run Nigerian small businesses and schools: traders, shop and salon owners, bursars, office and finance staff. Most are not technical, and many use only a phone. So:

- **Phone first.** Every screen works at 390 px wide (and 360 px), with nothing scrolling sideways. Design the phone layout first, then let it widen.
- **Plain words.** No developer terms on an owner's screen (see [Words](#words)).
- **The figure first.** A page about money leads with the amount, as large as the mockups make it, before anything else.
- **One main thing per page.** The page says what it is, shows the one figure or status that matters, then the actions, then the details.

## The brand on a screen

- **Pine `#0d3b2e`** is the brand. It is the colour of primary buttons, links, and the **hero card** (a page's main figure).
- **Lime `#c8f169`** appears **only on pine**: the money line on Home's card, a hero's tag and bar, the open tab's icon, the dots on the sign-in panel. Never lime text or lime fills on white or mist; it does not read.
- **Mist `#f1f4ef`** is the page's ground in the light theme; cards are white on it.
- **In the dark theme lime becomes the accent**: primary buttons are lime with pine text, links are lime. The hero card stays pine.
- **The mark is never redrawn.** Use the files through `brandFile("mark.svg")` (`web/src/brand.ts`), so the `?brand=seal` and `?brand=signal` experiments still work. No recolouring, outlines or shadows on the mark.
- **On a business's own public pages Taskiem steps back**: those pages use the business's theme ([decision 0042](../decisions/0042-public-page-themes.md)), not these rules.

## Colour tokens

Use the CSS variables, never a hex value in a component. The variables change with the theme and with the brand experiments; a hex value does not.

| Token | Light | Dark | Use |
| --- | --- | --- | --- |
| `--bg` | `#f1f4ef` mist | `#0d0e0c` | The page's ground |
| `--panel` | `#ffffff` | `#151614` | Cards, lists, sheets |
| `--panel-2` | `#f6f8f4` | `#1a1b19` | A quieter surface inside a card, hover |
| `--text` | `#0e1a14` | `#ededea` | Text |
| `--muted` | `#55615a` | `#9a9d95` | Secondary text, hints, `.sub` lines |
| `--border`, `--border-strong` | `#e2e7df`, `#d3dbd0` | `#262724`, `#34362f` | Card and field edges |
| `--brand` | `#0d3b2e` pine | pine | Hero cards, the brand's own shapes |
| `--brand-dark` | `#c8f169` lime | lime | Lime on pine only (tags, bars, the money line) |
| `--brand-dark-text` | pine | pine | Text on lime |
| `--accent` | pine | lime | Links, focus, selected states |
| `--primary`, `--primary-text` | pine, white | lime, pine | The page's main button |
| `--ok` | `#1f7a50` | `#47cd89` | Done, paid, cleared |
| `--warn` | `#b45309` | `#fdb022` | Waiting, part-paid, needs a look |
| `--danger` | `#c4321f` | `#f97066` | Failed, overdue, refund due, destructive buttons |
| `--info` | `#2f5bd3` | `#84adff` | Running, sent |
| `--tint`, `--tint-text` | pine at 9% | lime at 12% | The square behind a row's icon |
| `--tint-warn`, `--tint-warn-text` | `#fdf0dc`, `#8a3410` | amber at 14% | A row's icon when it needs a look |
| `--chart-in`, `--chart-out` | `#1f8060`, `#c9822f` | the same | Money in and out in charts (checked for colour-blind readers on both grounds) |

**Tones.** Status everywhere uses five tones, always with the same meaning: `ok` (done, paid), `warn` (waiting, partly done, needs a look), `danger` (failed, overdue, owed back), `info` (in progress, sent) and `muted` (cancelled, void, ended). A row's icon, its badge and a hero's tag use the same tone for the same thing.

## Type

- **Inter** (`--font`), 14 px body text at 1.55 line height. Hints and `.sub` lines are 13 px in `--muted`.
- **h1** 26 px bold (22 px on a phone), one per page, from `PageHeader` or the page's own card. **h2** 15 px semibold for card and section titles. **h3** 14 px.
- **Figures** use tabular numbers (`font-variant-numeric: tabular-nums`). A hero's amount is 40 px bold (48 px on a wide screen); a tile's figure about 28 px.
- **Sentence case** everywhere: "Clear by override", not "Clear By Override". No all-capitals except the gate screen's one-word answers, which are read across a school gate.

## Shape, space and depth

- **Radius:** `--radius` (16 px) for cards and lists; **24 px** for the large cards (hero, run card, approval card, Home's money card, the signed-out card); 12 px for a row's icon square; fully round (999 px) for pills: badges, chips, row buttons, switches.
- **Space:** 16 px gutters on a phone; cards 18 to 20 px inside; rows 14 px by 18 px (16 px on a phone); 12 px between cards.
- **Depth:** cards are flat with a border. Only sheets, dialogs and the signed-out card use `--shadow-lg`. No other shadows.
- **Motion:** short and calm (0.12 to 0.24 s). Everything that moves stops under `prefers-reduced-motion: reduce`.

## A page's layout

From top to bottom, leave out what a page does not need:

1. **Back link** to the list it came from: `<p className="crumbs"><Link to="/invoices" aria-label="Back to Invoices">Invoices</Link></p>`. The arrow is drawn by CSS.
2. **Heading**: `PageHeader` with `title`, an optional `description` (folded behind an (i) button on a phone) and `actions` for page-level buttons.
3. **Tabs** for a section with several pages (Money, Schools, Invoices): a `<nav className="tabs">` of `NavLink`s. On a phone they stay on one line and scroll sideways inside themselves; the page does not.
4. **The hero**, when the page has one main figure: a pine card (below).
5. **The page's actions**, under the hero: the primary one filled, the others plain, a destructive one red.
6. **Cards and lists** with the rest, most useful first. Each card has an h2.

On a phone the side navigation becomes the bottom tab bar (Home, Money, Orders, Workflows, Menu); leave room under the page for it (the shell does). Breakpoints in use: 860 px (phone layout), 640 px and 480 px for small adjustments, 900 px for the wide signed-out layout.

**The workflow builder is the one exception** ([the workflow builder](../workflow-builder.md)). It fills the window: the shell's `builder-mode` shrinks the menu to a rail of icons on a computer and hides the top and tab bars on a phone. The builder has its own bar (back, name, tabs, Save draft, Test workflow, Publish). On its canvas, what starts the workflow is the one pine card, with a lime icon; step cards are white with a tinted icon tile (`.bcard-icon.tone-*`). Don't add another full-window page; a page that needs more room uses `main wide`.

## Components

All of these exist; use them rather than building new ones. Their code is in `web/src/ui.tsx` and the classes in `web/src/styles.css`.

### The hero card

A page's main figure on pine, as Home's money card. Used by the invoice page, an order's page, Get started and the School overview.

```tsx
<section className="hero" aria-label="What is due" data-testid="invoice-hero">
  <div className="hero-top">
    <span className="hero-label">Still due</span>
    <span className="hero-tag">Part-paid</span>
  </div>
  <div className="hero-amount">{naira(x.due_kobo)}</div>
  <p className="hero-sub">of {naira(x.total_kobo)} · {naira(x.paid_kobo)} paid</p>
  <div className="hero-bar" role="img" aria-label="25% paid">
    <span style={{ width: "25%" }} />
  </div>
  <dl className="hero-foot">
    <div><dt>Customer</dt><dd>{name}</dd></div>
    <div><dt>Due</dt><dd>{dateLabel(x.due_on)}</dd></div>
  </dl>
</section>
```

- One hero per page, at the top, only for the page's main figure.
- `hero-label` says what the figure is; `hero-tag` (lime) is the status in a word or two, never a sentence; `hero-sub` is one short line.
- A figure that is words, not money ("3 of 8 done", "Price on the day"), takes `className="hero-amount words"`.
- Put the wording in a pure function in `web/src/lib/` with a unit test (`invoiceHero` in `lib/invoices.ts` is the example).

### Rows: `Items`, `Item` and `Row`

A list of things is rows, not a table. Every row has an icon (or a letter) on a tint, a title, something at its end, and lines under it.

```tsx
// Rows that open a page: the whole row is the link.
<Items label="Invoices">
  {list.map((x) => (
    <Item key={x.id} to={`/invoices/${x.number}`} icon="invoices" tone="warn"
      title={x.customer.name} side={naira(x.total_kobo)} sub={`${x.number} · due ${dateLabel(x.due_on)}`}
      sideSub={<StatusPill status={x.status} />} testid="invoice" />
  ))}
</Items>

// Rows with their own buttons: the buttons go under the lines.
<Items label="Passkeys">
  {keys.map((k) => (
    <Row key={k.id} icon="lock" title={k.name}
      sub={<span className="sub">Added {fmtTime(k.created_at)}</span>} testid="passkey">
      <button className="danger" onClick={...}>Remove</button>
    </Row>
  ))}
</Items>
```

- `Item` for a row that opens something; `Row` for a row with buttons. Never put a button inside an `Item` (the row is already a link).
- `icon` names one of the icons in `web/src/icons.tsx`; `letter` (a person's or a business's name) shows its first letter instead.
- `tone` tints the icon by the row's status (see [Tones](#colour-tokens)); leave it out for the brand's tint.
- `side` is the figure or status at the end of the title line (an amount, a badge). Keep it short: long titles are cut on a phone, so put names and details in `sub`.
- In `Row`, wrap `sub` in `<span className="sub">`; a second line inside it is `<span className="block">`.
- Give each row a `testid`, and status as `attrs={{ "data-status": x.status }}` when tests or styles need it.
- A list inside a card keeps its own border; that is intended.

### Status: badges, pills and chips

- `Badge` (`<Badge value={run.status} />`) shows a raw workflow status with its dot; tests read its text.
- For a business word ("Part-paid", "Cleared"), use a `badge sf-status <tone>` span through the page's helper (`StatusPill` on invoices, `StatusBadge` on orders and clearance, `ToneBadge` on the storefront checks). Each takes its words and tone from a table in `web/src/lib/`.
- Chips (`<ul className="chips"><li className="warn">…</li></ul>`) are short facts about a card: who may decide, a step-up, "First payment to this account".

### Cards, tiles and notes

- `<section className="card">` with an h2 for each block of a page.
- Tiles for a few figures side by side: `.stats` with `.stat` (School overview) or `.tiles` (Dashboard); two to a row on a phone.
- `<div className="notice" role="status">` for news after an action; `<div className="warning" role="note">` for a caution (the first-payment note); `<ErrorBox error={...} />` for errors from the API.
- `<EmptyState icon="…">One sentence saying what goes here.</EmptyState>` when a list has nothing yet, with its action when there is one.
- `<Skeleton rows={3} label="Loading the invoices…" />` while loading; its label says what is loading.

### Buttons

- `className="primary"` for the page's main action, one per view. Pine in light, lime in dark.
- No class for other actions; `className="danger"` for destructive ones (they also ask `confirm(…)` first).
- `className="link"` for a quiet action in a line of text; `className="small"` only inside dense rows.
- A link that acts like a button: `<Link className="button" …>`.
- Buttons say what they do ("Send", "Clear by override", "Approve"), never "OK" or "Submit".
- When several rows have the same button, add the row to its accessible name: `See the answers<span className="sr-only"> to {s.reference}</span>`.

### Sheets and dialogs

`<Modal title="Approve" onClose={…}>`: a bottom sheet on a phone, a centred dialog on a wide screen. Use it to confirm a decision and show what is being decided (the approval sheet shows the amount, the payee and the account, because the decision records what the person was shown).

### Forms and switches

- Fields have no outline: a soft filled shape (`--field`), a deeper tint and the ring on focus, a red tint when wrong. Never put a border back on a text field. Motion is short (120–220 ms) and only under `prefers-reduced-motion: no-preference`.
- `<Field label="…" hint="…">` around every input. Every input has a visible label.
- Several on/off choices in a row: `<label className="switch-line"><input type="checkbox" aria-label="…" /> <span>By email</span></label>`.
- Choices that only a developer needs go under **Advanced** (`useAdvanced()`), or in a `<details>`.

### Signed-out pages

Every signed-out page goes through `alone()` in `App.tsx`, which draws the brand on pine (above the card on a phone, beside it on a wide screen). A new signed-out page renders only its card: `<div className="login card"><h1>…</h1>…</div>`.

## Rows or a table?

Use **rows** for a list of things a person looks at one at a time: people, invoices, orders, runs, students, keys, channels, settings. Use a **table** only for figures and line items read across columns:

| Rows | A table |
| --- | --- |
| Members, invoices, orders, bookings, students, passkeys, notifications, checks | An invoice's lines, a table's data, usage by day, a run's events |
| Each item has a status, an action or a page | Each cell is a figure or a short value |
| Read on a phone one at a time | Compared down a column |

Tables also stay where people tick many rows at once (Debtors) and in the security settings (single sign-on, keys, secrets, the catalogue). A table on a phone uses `className="cards"` with `data-label` on each cell, so it stacks.

## Words

The rules of the [no-technical-steps plan](../spec/no-technical-steps.md) apply to every screen an owner sees.

- **Never show** the words in its [Appendix C](../spec/no-technical-steps.md#appendix-c-words-an-owner-never-sees): JSON, schema, expression, cron, kobo, token, webhook, endpoint, payload, API key (outside the guided key screen), step id, `null`, `undefined`, raw HTTP statuses. `web/src/lib/plainwords.test.ts` checks the owner's pages; add a new owner page to its `OWNER_FILES`.
- **Money** in naira with `naira(kobo)` (`lib/invoices.ts`): "₦25,000", "₦25,000.50". Never kobo, never a bare number for money.
- **Times** with `fmtTime(iso)` ("Today, 18:03", "Yesterday, 09:10", then the date); dates with `dateLabel(day)` ("24 Oct 2026"); booking times say "Lagos time".
- **Counts** agree: "1 run", "2 runs"; "1 student owes", "2 students owe".
- **British spelling**: organisation, colour, cancelled.
- **Say what happened and what to do**: "The run did not complete. Open it to see which step stopped and why.", not "Error 500".
- **No ids** on an owner's screen: show names, references people use (INV-0001, AK-2X4F, FAM/002) and links.
- **Personal data is masked** until someone allowed to see it presses "Show names" or "Show customer details", which is audited. Show the masked form ("A•••", "•••4567") as the server sends it.
- **Short.** A hint is one sentence. A page description is two at most and folds away on a phone.

## Icons

Use the line icons in `web/src/icons.tsx` by name: `home`, `money`, `invoices`, `orders`, `storefront`, `workflows`, `runs`, `activity`, `approvals`, `forms`, `tables`, `schools`, `members`, `keys`, `lock`, `bell`, `alerts`, `clock`, `start`, `refund`, `connections`, `settings`, `billing`, `reports`, `audit`, `inbox`, `templates`, `search`, `plus`, `close`, `chevron`, `info`, `sparkle`, `bolt`, `back`, `more`, `menu`, `tick`, `tick-double`, `emoji`, `send`, `down`, `pin`, `mute`, `image`, `star`, `dashboard`, `catalogue`, `policies`. One icon per row; the same thing always has the same icon. Add a new icon to `icons.tsx` in the same 24-unit line style rather than using an emoji or another set.

## The Chat page

The Chat page is the one screen that does **not** follow Taskiem's look. Business links §5.5 makes it WhatsApp Web's look, feel and way of working, deliberately and in detail, so a person who knows WhatsApp has nothing to learn. Copy it for any other conversation screen.

- **It fills the window.** `/chat` sets `chat-mode` on the shell (`App.tsx`), which reuses the builder's rail: a 64px menu on a computer, no top or tab bar on a phone. There is no page title or padding; a visually hidden `h1` stays for screen readers. The list pane and the open conversation each scroll inside; the composer stays at the bottom. On a phone one pane shows at a time and the thread has an arrow back to the list.
- **Chat colours are tokens**, not brand colours: `--chat-wallpaper`, `--chat-in`, `--chat-out`, `--chat-out-meta`, `--chat-tick-read`, `--chat-select`, each with a light and a dark value in `styles.css`. Use them for bubbles, the wallpaper, read ticks and the selected row; never a hex colour in a component.
- **What stays Taskiem's:** the name and logo (the chat never carries WhatsApp's name, logo or traced icon artwork), the type, the document cards, and the business's card where a contact's profile would be.
- **Bubbles and rows copy WhatsApp Web's.** The first bubble of a run (one sender's messages in a row) has a tail and the sender's name; the rest sit close under it. A chevron at the bubble's top corner opens its menu (Reply, Copy, Forward, Delete for me) on hover or focus (a swipe to reply or a hold on a touch screen). The time and ticks sit in the corner of the last line of text (the text leaves a gap for them); with a photo only, a link preview or a card they have a row of their own. Ticks are SVG (`Ticks` in `Conversation.tsx`: two when delivered, blue once read). The thread has a quiet dotted wallpaper (`--chat-pattern`, from tokens, no image). Text is 14.2px on a computer, 16px on a phone. The chat list's rows are `ChatRow`: avatar, name and time, a preview with a tick before ours, unread count, pin and mute marks, and the open one highlighted.
- **Your business in Explore** (`pages/BusinessProfile.tsx`, `/settings/links/profile`, reached from the Chat page's menu, Linked businesses and the "find you" card) is where a business sets how others see it: a live card, a logo (a letter stands in until there is one), the name, a line, the area, a text about it, and up to 12 photos with captions, earlier and later buttons and Remove. Fields have no outline, as everywhere. Tests: `e2e/business-profile.spec.ts`.
- **Explore** (`pages/Explore.tsx`) is the screen "Find a business" opens, over the chats: a back arrow, a search box with the focus, "Near you" and "New on Taskiem" before anything is typed, then cards: logo or letter, name, a line, a swipe of photos with dots that grow as they are reached (scroll-snap, arrow keys, 24 px dots, still under reduced motion), and Connect (asks at once as both supplier and customer), then Requested with Withdraw, or Message. Tests: `e2e/explore.spec.ts`.
- **Contact info** (`pages/ContactInfo.tsx`) is WhatsApp's: the picture and name, round actions with their words under them (Search, Pin), then rows with an icon each: Starred messages (a count and a list of this chat's), Media and files (a strip of the latest pictures, opening to every picture, voice note and file), Mute notifications as a switch (`role="switch"`), Archive this chat, and last, in red, End link. No outlined text buttons. **Confirmations are the app's own sheet** (`useConfirm` in `pages/ConfirmSheet.tsx`: Cancel first and focused, the red button agrees), never the browser's `confirm()`, in the chat (delete a message, decline an order, end or block a link). Tests: `e2e/link-chat-info.spec.ts`.
- **A linked business in chat** is shown by the name and logo it chose (`ChatAvatar`): a round logo in the list, the conversation's head and its contact info, or the first letter when it has none or the picture cannot load.
- **A message that was not sent** looks as WhatsApp's does: the bubble as usual with a small red "!" where the ticks go, and two round icon buttons (try again, delete) beside it, no words and no red text. The reason is read out by screen readers and is the tooltip (`data-testid="chat-failed"`).
- **The composer copies WhatsApp Web's.** An emoji button opens a panel above the box (search, recently used, groups: Unicode's own characters in `lib/emoji.ts`, no image set); the clip opens Photo, Document and Camera (the computer's webcam in a sheet, or on a phone the camera app); a picture pasted or a file dropped on the thread waits for a caption; the box grows to six lines and has no drag handle; Esc gives up the panel, then a waiting file, then a reply; an unsent message is kept per conversation (`lib/drafts.ts`) and shown as "Draft:" in the list. A message is **sent at once**: it shows with a clock, then the server's copy takes its place; if it cannot be sent it shows a red "!" with round Retry and Delete icon buttons beside it. Every send carries a `client_id` so a Retry is the same message. Messages not yet sent live in `lib/outboxStore.ts`, outside the conversation's page: one at a time per conversation in order, kept when you switch chats (the pane is keyed by conversation), sent again by themselves when the connection is back or after a "too fast" refusal, and forgotten at sign-out with the drafts. A photo waiting for its caption shows the picture (a data URL: the app's content policy allows no `blob:` images; `e2e/link-chat-csp.spec.ts` runs under the real policy). The emoji panel is Unicode 14's whole set (about 1,850, nine groups, loaded when the panel is first opened: `lib/emojiData.ts`, written by `web/scripts/gen-emoji.mjs` from Unicode's emoji-test.txt and CLDR's annotations; newer emoji are left out because older phones draw them as empty boxes), with one emoji in the tab order and the arrow keys, skin tones for every hand and person that takes one, and words people here use (okada, naija, jollof); the camera sits beside the microphone where the screen is touched.
- **Live.** One live stream for the whole signed-in app (`pages/ChatLive.tsx`): a message, a tick, a reaction and the typing line appear in a second or two; the pages poll more slowly while it is open, and as before when it is not (`lib/useThread`'s pages keep their polling). The tab's title carries the unread count and the icon a dot. A stream that goes quiet (no event or ping for thirty seconds) is closed and opened again; one the server refuses (too many open) is tried again after thirty seconds; the stream opens when the first business is linked, without a reload. A read is posted once for a burst, only while the tab is visible and in focus. A notification (and an optional sound) for a message that comes while the tab is hidden is the person's choice on this browser, asked once, never on load; what it says is in `lib/chatNotify.ts` (a document: only what it is).
- **Reactions, stars and info.** The bubble's menu floats over the chat as WhatsApp's does (the bubble keeps its size): a pill above it with the six emoji (the person's own is marked) and a **+** that opens the full emoji picker in its place, and a card by it with one row each, an icon and its words: Message info, Reply, Copy, Forward, Star, then a rule and Delete for me. It opens from a right click, the chevron or a hold (the whole menu), and on a computer from the smiley that shows beside the bubble on hover (the pill alone); it keeps on the screen (the pill goes below a bubble at the top), closes on Esc (the focus back on the chevron), a click elsewhere, a scroll or a resize, and arrow keys, Home and End move through its buttons. Tests: `e2e/link-chat-popover.spec.ts`. A chip under the bubble for each emoji given (a count past one; the one you gave marked; tap to take yours back or to react the same); a star by the time; Info is a small sheet of sent and read times. The list's Starred chip shows the person's starred messages, each opening the conversation at it. The thread refreshes its newest page every few seconds, so the other side's reactions appear.
- **Keyboard** (desktop). Plain-key shortcuts, off while a field or a dialog has the focus and never taking Ctrl, Alt or Cmd: `/` finds a business or a message, `n` asks a business to link (the same box, with a spoken hint), `j` and `k` go to the next and previous chat, `?` lists them (the **Keyboard** button in the list's head shows on screens with a pointer; `lib/chatKeys.ts`). Arrows go down the list from the search box, chat to chat, and back up; Enter opens. **Esc** gives up one thing at a time: the menu or sheet that is open, the emoji panel, a waiting photo, a reply, then the chat itself, with the focus on its row (the one that gives something up takes the key with `preventDefault`, so the next layer sees it was taken). A right click on a bubble opens its menu, as the hover chevron does (a link, a player, a selection or Shift keeps the browser's own); Esc or a click elsewhere closes it, and Esc returns the focus to the chevron. The cursor goes back to the message box after sending and, with a keyboard, when a panel, the info sheet, the forward sheet, a photo or a reply is closed (`lib/composerFocus.ts`; on a phone only after sending, so the keyboard does not rise over a screen just closed). Esc during a voice recording cancels the recording and nothing else. Caps Lock does not matter to the letters, and they stay out of an open menu, sheet or panel. The conversation's menu has a **Keyboard** entry (for every member, read-only ones too), which opens the same sheet. Arrows go through everything below the search box that can be pressed (chats, results, asks), not the question card; Esc in an empty search box leaves it. A right click keeps the browser's menu only for text selected inside that bubble. Every control reached by Tab has a ring (the navigation's **More** and **Sign out** had lost it to `box-shadow: none`; a test tabs through the list and a conversation). Opening a chat does not put the cursor in the box: that would turn `j` and `k` off. Rows and the chevron have a visible ring; the unread count is read as "3 unread messages", the ticks as "Delivered" or "Read". Tests: `e2e/link-chat-keys.spec.ts`, `lib/chatKeys.test.ts`.
- **One chat for Taskiem and for businesses.** A business's conversation (`Conversation.tsx`) and the person's own with Taskiem (`TaskiemChat.tsx`) are built from the same pieces, so they scroll, send and look alike: `ChatHead` (back, avatar, name with "typing…", the menu with its **Keyboard** entry), `ChatStream` (the log, older messages at the top, days, "N unread messages", the round button), and `useComposer` with `ComposerView` (box, emoji, photo, camera, voice, "@" and "#", paste and drop, reply, draft, Esc, the cursor going back). What differs is given to them: what may be attached (`BUSINESS_FILES` has PDFs, `TASKIEM_FILES` has photos only), where the draft is kept, what is done with what is sent, and how a bubble looks. Taskiem's own: its answers' buttons are WhatsApp's reply buttons in one column under the bubble (`.reply-buttons`; the one chosen has a tick and is read as "You chose …", an answer's buttons press while it is the newest and the person has said nothing since, the server giving their ids for an answer not yet answered, so after a reload or in another tab too; once answered, or past the step's 30 minutes, they are marks only), a message from another channel says "on WhatsApp", "on Telegram" or "on Slack", our messages have ticks (a clock while sent, grey once sent, blue once Taskiem has answered), "typing…" shows while the answer is made, a message that did not go comes back to the box, and what Taskiem said while the person was away is counted (`lib/taskiemSeen.ts`: a "seen" time kept in this browser, since Taskiem reads at once and keeps no read marks): "N unread messages" in the thread, a badge on its row, and the tab's count. The bubble is shared too (`BubbleShell`: the tail, the hold and swipe on a touch screen, the right click and the chevron for the menu, Esc and a click elsewhere to close it): a business's menu offers React, Reply, Copy, Forward, Star, Info and Delete for me, Taskiem's Copy. A message sent from here is shown at once and replaced by its stored copy, however the two arrive (the stream may tell of it before the request is answered); one on its way when the page goes away is known on coming back (`inflight`, kept in the tab): if the server has it nothing is done, if not its words are back in the box with a notice. The unread ones are kept by id, so the same message told twice (a reload and the stream's look-back) counts once, and seeing them in one tab clears them in another. Times from the server have a varying number of digits after the second and are compared as times (`lib/stamp.ts`), never as strings. With no messages yet, Taskiem's empty state says what to say (help, status), in place of a line above the thread. Taskiem stays pinned at the top of the list. Not shared yet: reactions, reply, star, forward, info and search in the conversation, which a business's chat has and Taskiem's answers do not need; the small reply box on a document's page (`DocThread`) has its own composer. Tests: `e2e/link-chat-taskiem.spec.ts`, `lib/taskiemSeen.test.ts`, `lib/stamp.test.ts`, `lib/taskiemchat.test.ts`.
- **The look of WhatsApp's app and of WhatsApp Web, in our own icons and colours.** Flat heads with plain icons (no bordered buttons); the conversation's head has the name with who they are under it (their role, or "typing…"), a search icon that opens the search over the thread, and the ⋯ menu drawn as three vertical dots. The list's head is the title, the round button to start a chat (**Find a business to link with**, which is the `n` key) and the ⋯ menu (**Linked businesses**, **Keyboard**), then a pill search, the filter chips, and rows with 52 px round avatars, no dividing lines, the time in green with the unread count. On a phone the Chat list is a screen with the tab bar under it (Home, **Chat** with its unread count, Money, Orders, Menu) and the round button floats over it as an extended button; a conversation takes the whole screen, with no tab bar, and Back returns. The tab bar's Chat replaced Workflows there (Workflows is in the menu). Messages sit on the wallpaper in tight runs (2 px inside a run, 6 px between), the bubble up to 84% of a phone's width and 65% of a computer's, a quote with its sender's name in colour above its text (three lines at most). The box is one rounded pill (emoji, the words, attach and the camera inside it) with the round microphone beside it, which turns into the round send button once there are words. A reaction is a small round chip on the bubble's bottom corner (the left for theirs, the right for ours), half over its edge, with no border (a ring of the wallpaper's colour cuts it out) and a count widening it into a pill; the one you gave is tinted. A long message is folded (`lib/readmore.ts`, `pages/ReadMore.tsx`): the first 600 characters or 10 lines, cut where a word ends, then **Read more** shows the next 1,200 characters or 20 lines, and so on to the end; one that would hide only a few words is not folded. Not done: a stack of reactions beside a bubble on hover, a pinned-message bar, system lines ("You added …"), `*bold*` and `` `code` `` in business messages, a bottom bar of WhatsApp's own tabs (ours are the app's). Tests: `e2e/link-chat-mobile.spec.ts`, `e2e/link-chat-reactions-look.spec.ts`, `lib/readmore.test.ts`.
- **Search in a conversation** is a panel, as WhatsApp's (`pages/ChatSearch.tsx`): the magnifier in the head opens it beside the thread on a computer (the head, the thread and the box narrow to make room) and as the whole screen on a phone. It has a ✕ and the title "Search messages", a calendar button and a pill box focused at once, the words "Search for messages within *name*." before anything is typed, and the messages found as rows (who, when, and the words around the hit with the hit in bold, `lib/snippet.ts`), found a moment after the typing stops. The thread behind it is not replaced: a row takes it to that message (the message flashes); on a computer the panel stays, on a phone it gives way. Arrow keys move between the box and the rows, Esc closes the panel only and the focus goes back to the magnifier. The calendar button opens a month calendar (`pages/DayPicker.tsx`, `lib/calendar.ts`): the month with ‹ › arrows, Sunday to Saturday, round day buttons, today filled in the accent colour, days after today dimmed and not to be chosen, arrow/Home/End/Page keys to move, Esc or a click elsewhere closes it alone. Choosing a day: the thread goes to the first message of that day, or the next one after it, and the panel says "No messages on or after that day." when there is none (it uses `?after=` with `limit=1`, so no server change). Tests: `e2e/link-chat-search.spec.ts`, `lib/snippet.test.ts`, `lib/calendar.test.ts`.
- **Icons** are our own line icons in `icons.tsx`: `tick`, `tick-double`, `clock`, `emoji`, `send`, `down`, `attach`, `camera`, `mic`, `back`, `more`, `search`.
- **Tests:** `e2e/link-chat.spec.ts` runs axe in both themes at 390px; `accessibility.spec.ts` covers `/chat`.

## Accessibility

- **Contrast:** every screen passes axe in both themes; `web/e2e/accessibility.spec.ts` checks every console page. Add a new page's path to its list.
- **Colour is never the only signal:** a status has words as well as a colour.
- **Labels:** every input has a label; an icon-only button has an `aria-label`; a list has `aria-label` (`Items label="…"`); a hero has `aria-label`; a chart or bar is `role="img"` with an `aria-label` that says the figure.
- **Touch:** buttons in rows are at least 36 px high, those on the signed-out pages 44 px.
- **Focus** shows the ring (`--ring`); do not remove outlines.
- **Plain links** in text are underlined (pine is too close to the text's colour to tell apart otherwise).

## Testing a screen

- Give lists and cards `data-testid`s and test what a person sees: the words, the amounts, the status.
- An end-to-end test at 390 px (`test.use({ viewport: { width: 390, height: 844 } })`) that the page shows its figure and works, with `document.documentElement.scrollWidth <= 390`.
- `accessibility.spec.ts` covers axe and the phone width for every page in its list.
- Wording that picks words from data ("Still due" or "Paid in full", "1 done · 1 waiting") lives in a function in `web/src/lib/` with a unit test.
- Check it in the dark theme (Account → Appearance) and at 360 px.

## Do and don't

| Do | Don't |
| --- | --- |
| Use the CSS variables | Write a hex colour in a component |
| Lime only on pine | Lime text or fills on white or mist |
| Lead a money page with the amount on a pine hero | Start a page with a table of details |
| Rows for lists, tables for figures | A table of people, invoices or settings |
| `naira()`, `fmtTime()`, `dateLabel()` | Kobo, ISO dates, ids |
| One primary button per view | Several filled buttons competing |
| Reuse `Items`, `Item`, `Row`, `PageHeader`, `Modal`, `EmptyState` | A new list or card component for one page |
| Test at 390 px in both themes | Check only on a laptop in light |
| The mark from `brandFile()` | A redrawn, recoloured or shadowed mark |

## Before you open a pull request

- [ ] The page works at 390 px and 360 px with nothing scrolling sideways.
- [ ] It reads well in the light and the dark theme.
- [ ] Colours are tokens; lime appears only on pine.
- [ ] Lists are rows; tables only hold figures or line items.
- [ ] No word from Appendix C; money, times and counts use the helpers.
- [ ] Every input is labelled; repeated buttons have unique names.
- [ ] The page is in `accessibility.spec.ts`, and owner pages in `plainwords.test.ts`.
- [ ] An end-to-end test covers what the page shows, and a unit test covers its wording.
