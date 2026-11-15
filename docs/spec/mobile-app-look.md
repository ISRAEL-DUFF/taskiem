# The app's own look, away from the chat

Settled with the founder, 12 November 2026: the pages away from the chat
(Today, Money, Orders, Team, invoices, stock and the rest) **stop mirroring
the web**. On a phone the web's layout (a paragraph under the title, full
width buttons, cards of long wrapped lines) is hard for a business to read.
The chat still matches the web chat (mobile-app.md §9); these pages do the
same jobs, with the same server calls, in a phone's layout.

## The kit (`lib/kit/mobile.dart`)

| Part | What it is |
| --- | --- |
| `TkScreen` | The head: Back (or nothing on a tab), the title (large on a tab), icon buttons at its end; what stays under the head (chips); the parts, rising in one after another. No description paragraph |
| `TkHeadButton` | A round icon button in the head (Download on Money) |
| `TkChips` | Chips across, scrolling sideways, one on (Money's period) |
| `TkSummary` | The pine card: a label, the figure large and counting up, the figures beside it, a line under them |
| `TkSection` | A title, an amber count of what waits, "See all ›", and its rows on a rounded panel |
| `TkListRow` | One row: a round tile (initials or an icon, an amber dot when it waits), a title and a line under it, each on one line, the amount and its status at the end |
| `TkTiles` | Tiles three across for the pages behind a screen, a count on one where something waits |
| `TkSkeleton` | Shimmering rows while a section loads |

## Motion

All of it stands still when the phone asks for less motion.

- **Tabs.** The tab opened fades in and slides a little from the side of the bar it was chosen toward; its icon grows into its pill.
- **The chats list.** Rows rise in one after another as it appears, a new chat slides in, and a chat that moves (a new message takes it to the top) glides to its place; a row gives a little when pressed.
- **Screens.** A pushed screen slides in from the right as the one under it drifts left (`TkRoute`), conversations included. Modals and sheets keep their own motion.
- **Pages.** A page's parts rise into place one after another (`TkEnter`). A figure counts up (`TkCountUp`). A share bar grows.
- **The chat:**
  - a new message rises and grows from its own side (`MessageArrive`);
  - a reaction pops in (`PopIn`);
  - a swiped bubble shows the reply arrow and springs back, with a tick of the phone at the point it replies;
  - the round jump button fades and grows in;
  - the recording dot breathes;
  - the tools panel springs up from the box and drops away as it closes, as the web chat's does;
  - the head's ⋯ menu grows from its button as it fades in.

## Sheets

In the app and the web chat alike (13 November 2026, the founder):

- **Pulled.** By its grab bar or head, a sheet pulled up takes the whole screen; pulled down from there, its own height; thrown down, or pulled past a third of its height, it closes; let go early, it springs back.
- **Back, one level at a time.** Back (and Esc on the web, the phone's own back in the app) comes back from the deepest step open inside first (a product's sizes to the product, a bill's "paid outside" to the bill: `TkSheetStep`, `useSheetStep`), then a row to its list, then from a tool's list to the tools panel. Close still closes the whole sheet.

## Direction B: Today, Money and Orders

Chosen with the founder, 15 November 2026, from three directions drawn as
mockups (a glowing money header, rings, a wallet of passes): the calm of
Apple's Health and Fitness pages. Two changes asked on the rings version:
**no goals** (a business sets no target; each page compares the business
with itself or says where things stand), and **a head of its own on each
page**, fitted to what the page holds.

Each page is: its head, a top card that answers "how is it going?",
highlight tiles two across, then grouped lists of what to do or where to
go. The accents are hand-written in `lib/theme/look.dart` (not generated
from the web's CSS) and mean the same on every page: lime is the page's
main figure, teal what moves along, orange what goes out or waits, red a
leak, blue what is on its way. Each has a graphic colour and an ink colour
for words that keeps 4.5:1 on a card.

| Page | Head | Top card |
| --- | --- | --- |
| Today | The greeting, the date and the business; the person's initials | Money in today counting up, its line through the day over a dashed usual day (the same weekday, the last four weeks, averaged hour by hour from `/v1/money/summary`), and how far ahead or behind that is at the same time of day. With no history, today's line alone. A shop without Money draws its paid orders |
| Money | "Money" small, the period's chips; then the kept figure as the page's headline, set against an earlier stretch (a day against the same weekday a week before, this week and this month against last week's and last month's same days, last month against the month before, chosen dates against as many days before them) | Money in rising and money out dropping from one line, a pair a day (or an hour), on one scale; pressed, a pair's figures |
| Orders | "Orders" large; chips for All, Customers, Businesses, Suppliers when the person sees more than one | The open orders to the business in three stages, New, Getting ready, On the way (orders to suppliers: To answer, Sent, Coming), and the order that has waited longest, with Open |

The parts (`lib/kit/look.dart`):

| Part | What it is |
| --- | --- |
| `TkTopCard` | The card a page opens with; in the dark a faint lime light at its top left |
| `TkEyebrow` | A small label in capitals in an accent's colour, its icon, words at its end |
| `TkHighlight`, `TkHighlights` | A tile with an eyebrow, a figure, a line and an optional chart; two across, a wide one on its own row |
| `TkDayLine`, `TkChartKey` | A day's running total against a usual day's, and the key under a chart |
| `TkFlow` | Stages side by side with their counts and a dot for each order |
| `TkTrend` | Up, down or about the same, an arrow before the words |
| `TkSplitBar` | A whole in shares, one bar and its key |
| `TkFace` | Initials on a warm gradient |

`TkScreen` takes a `head` in the title's place (Today) and a `compact` title
(Money); `TkListRow` takes a `square` colour, its icon white on a rounded
square, for lists of places to go.

### Matched to the mockup

Asked by the founder after the first build, 15 November 2026: the details
of the mockup, on every page away from the chat, not only these three.

- **Type.** Titles, section titles and figures in **Inter Tight** (bundled
  from Google Fonts, 500, 600 and 700; `tkDisplay` in `theme.dart`), set
  tight; the rest stays Inter.
- **Surfaces.** A ground of #F2F3F0 (light) or black (dark), cards white or
  #1A1B1A on it **with no border**, a faint hairline between rows.
- **Spacing.** 14 px between a page's parts; section titles 20 px.
- **Rows.** A place to go has its icon white on a 30 px square of an
  accent; a person or a business has their letters white on a 40 px
  gradient, the same name always the same colour. A status is in its
  stage's accent (an order: new orange, getting ready teal, on the way
  blue). "See all" in the muted colour.
- **Icons.** The mockup's own ("b-…", `theme/look_icons.dart`), a stroke of 2.
- **Switches.** Periods and kinds as a phone's segmented switch: a soft
  track, the one chosen on a card; wider than the screen, it scrolls.
- **The bottom bar.** As a phone's own: the ground's colour and a hairline,
  no pill, the open tab's icon and word in pine (lime in the dark).
- **Tiles.** Today has the day's sales by hour, its best hour picked out;
  Orders the next booking and the busiest hour.

Left as they were, on purpose: words in an accent use its darker ink in
the light theme (the mockup's bright lime and teal are under 3:1 on
white); Money keeps its six periods and its bar chart keeps the picked
day's figures and "See the figures" for a screen reader; Orders has no
"Delivered" stage (a customer's order keeps no time it was delivered) and
no search or + in its head (the tab has neither yet).

### Invoices and bills

Drawn and agreed with the founder, 15 November 2026, in the same look.

- **The head of a pushed page** (`TkPageScreen`'s `titleSize`): Back as a
  link naming the page it goes back to ("‹ Money"), the buttons at its
  end, the title large under them (30 for a list, 22 for one document).
- **Invoices.** The filters, the owed ones first (All, Sent, Part-paid,
  Overdue, Paid, Draft, Recorded, Notebook debts, Void). On All, a top card
  of what customers still owe, split into not due yet and overdue, and the
  payments waiting to be matched with Match; then the invoices grouped:
  overdue, waiting for payment, drafts, paid, void. Statuses: overdue red,
  part-paid orange, sent blue, paid lime, draft and void grey.
- **One invoice.** Its number as the title; Share (once its page is known)
  and ⋯ (Edit, Void) in the head. The top card: what is left large, how much
  is paid as a bar, and the next step (Send, Send again, Its page). Then the
  customer on one card with Call and Email, the lines with their total, and
  the rest as before.
- **Bills** (also from Money's "Go to"). A top card of what is left to pay,
  the week ahead as bars of what falls due each day (what is late counted
  today), and the next bill with Pay; then to pay, by due date, and paid.
  Statuses: late red, due within two days or queried orange, being paid
  violet, paid lime.
- **One bill.** The top card: what is left, how much is paid, and Pay from
  Taskiem with what happens next under it. The supplier's account read out
  in groups, with Copy; the lines; paid outside Taskiem and a query as a
  short list; the messages.

## Order

1. Orders and Money, and the chat's motion. Done.
2. Today, Money and Orders in Direction B. **Done; waiting for the founder's word.**
3. Invoices and bills. **Done; waiting for the founder's word.**
4. Then stock, suppliers, riders, tables, Team, and the settings-like pages, the same way.
