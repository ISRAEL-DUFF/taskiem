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

Use the line icons in `web/src/icons.tsx` by name: `home`, `money`, `invoices`, `orders`, `storefront`, `workflows`, `runs`, `activity`, `approvals`, `forms`, `tables`, `schools`, `members`, `keys`, `lock`, `bell`, `alerts`, `clock`, `start`, `refund`, `connections`, `settings`, `billing`, `reports`, `audit`, `inbox`, `templates`, `search`, `plus`, `close`, `chevron`, `info`, `sparkle`, `bolt`, `back`, `more`, `menu`, `dashboard`, `catalogue`, `policies`. One icon per row; the same thing always has the same icon. Add a new icon to `icons.tsx` in the same 24-unit line style rather than using an emoji or another set.

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
