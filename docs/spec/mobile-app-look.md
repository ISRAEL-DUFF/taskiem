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

## Order

1. Orders and Money, and the chat's motion. **Done; waiting for the founder's word.**
2. Then Today, invoices and bills, stock, suppliers, riders, tables, Team, and the settings-like pages, the same way.
