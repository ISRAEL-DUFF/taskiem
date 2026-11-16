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

### Stock and supplies

Drawn and agreed with the founder, 15 November 2026, keeping the split: the
orders to suppliers leave the Stock screen for one of their own, Supplies.

- **Stock.** A top card of today's sales: the total, how it was paid, bars
  by the hour, Record a sale, and the terminal's sales nobody explained.
  Tiles: what is low (opens Supplies), the stock at cost, the last shelf
  count and what went missing. Running low, each item with a bar of what is
  left against its level, and Order them. One row to Supplies (how many are
  open, how many need you). The terminal's sales to say, today's sales,
  your items (a row opens Record stock, Change, History), the counts and
  every change.
- **Supplies** (from Stock and from Orders' "To suppliers"). Open, Received
  or Cancelled. A top card of what is on order and still owed, the orders as
  a flow (draft, sent, confirmed, coming in; violet, blue, teal, orange),
  and Order what is low. Those that need you first, then those on the way;
  each order keeps its buttons under its row.
- **Suppliers.** Current or Retired. A top card of what you owe, bills from
  linked suppliers and unpaid orders apart. Each supplier with what is open
  and what you owe, On Taskiem when linked; a row opens Change, Prices,
  Statement, On Taskiem, Send the link again, Retire. The approval limit
  and the delivery address stay as cards to fill in.
- **One order to a supplier.** Its total and what to do now (say what came,
  answer their change, confirm their draft), how far it has gone (sent,
  confirmed, delivered, received), All came or Not all came; the supplier on
  one card with what they last said; the lines with their amounts and the
  total; Cancel as a row.

### Cards in a conversation

Asked by the founder, 16 November 2026, for the app only, keeping the
bubbles: what a conversation shows for an order, an invoice, a product and a
tag.

- **An order or an invoice** stands free of the bubbles, on a card with its
  sender's corner tucked in: the kind and number small in capitals with its
  status as a pill (to pay or new lime, a counter orange, declined red), the
  amount large in Inter Tight, a line saying what happened, the deal's
  history, how far it has gone as dots on a line, its buttons (the main one
  pine, lime in the dark; the rest on the ground's colour), and "View order ›"
  as a row under a hairline.
- **A product** (`LiveCard`) sits above the words it came with, on its own
  card: its letters on a soft square (a clock for a service), its name large,
  its price, then your stock as a pill and Order.
- **A tag** in the words is a chip: a product's letters on a small square
  and its price, a business's letters on its face; a business opens its
  conversation.

A document named in the words (`RefCard`) is as it was. These six chat
screens no longer match the web's pictures; the parity test still builds and
drives them.

### One order

Drawn and agreed with the founder, 16 November 2026: the screens behind a
row on Orders, and the two the chat's Orders sheet opens.

- **A customer's order.** The top card: its status in its stage's colour
  (new and waiting orange, accepted teal, with a rider blue, completed lime,
  a refund due red), the total, how it is paid, and its steps with their
  times (ordered, accepted, with the rider, delivered or done). Its rider
  inside the card, with Call, how far they got and Take it back; before one
  has it, Offer to a free rider and Choose the rider. A refund due takes the
  card's words and buttons; new prices, theirs. The customer on one card with
  Call, WhatsApp, Map and Email, or Show customer's details when they are
  hidden. Then what they ordered, the money matched to it (Undo the match on
  its row), and the note, fulfilment, who decided and the workflow as rows.
- **A booking.** Its day (Today, Tomorrow or the date) and time large, the
  service and its hours, the price and when it was booked as two small
  tiles, Move and Cancel.
- **An order from a business.** What it needs from you ("New · answer
  them", "They ask to cancel", "Not all came, they say"), the total, what
  it is and when it is invoiced, its five steps, and the steps its status
  allows as buttons; past their credit limit as a callout that opens their
  statement. Each line says which of your items it is and how many you
  have. The invoice and the delivery as rows.
- **In the Orders sheet.** An order from a business takes the same top
  card. A rider's delivery: what to do now, the fee and payment at the door,
  the route from pick-up to drop-off with a map on each, its steps; the
  customer with Call; the photo the business asks for. The sheet's body is
  the page's ground, so its cards stand out.
- **Large text.** With text half as large again or more, a top card's
  buttons go one under another, its steps down the card, and a callout's
  button under its words.
- **Asked after comparing with the mockup** (16 November 2026): the head's ⋯
  on a customer's order holds Take it back while a rider has it (it shows
  only then); Show customer's details has an eye, Move a calendar; the
  business's card has Message, which opens its conversation. Figures and
  amounts are set in tabular figures, as the mockup's are; a phone number
  is grouped to read ("0803 412 7821"). The fonts are the mockup's own
  (Inter 4.001, Inter Tight 3.004); compared at the mockup's 340 px width,
  sizes and weights match.

### Team, riders and tables

Drawn and agreed with the founder, 16 November 2026, in the same look.

- **Team** (was Staff). "In today" large, as a count of the staff, and
  everyone's face in a row with a dot of their day: in (lime, the time they
  said in), late (orange), gone (teal) or off (grey). One late person is
  called out with Call. Tiles: the tasks open and late (opens Tasks) and
  the people invited. Then the staff, each with their role and their day,
  and the invitations with Send again (pressed: Send again or Cancel).
- **One person.** Today, this week or this month: the hours worked, the
  shifts and time keeping, and this week's days with the late and missed
  ones marked. Three tiles: sales (and orders), tasks, the till at
  handover. Call and WhatsApp; their job, hours and pay (for your
  records), each opening Change; Remove from the business here, not on the
  list. ⋯ holds Change.
- **Tasks.** Yours, everyone's and repeating, as a switch. Done, open and
  late as one bar; the late first, then open, sent back, and done struck
  through. Each task has its one button (Done, I'll do it, or Remind on a
  late one); pressed, the rest: Can't, Remind, Give to someone else, Call
  off, See the photo.
- **Riders.** Current or retired. "Free now" large and each rider's face:
  free, busy till a time, off, on leave. The link for riders with Share
  (Copy, or WhatsApp). Pay riders as a row with what is due and the cash
  owed. Each rider on one line (areas, fee, how Taskiem reaches them, their
  rating); pressed: Change, Send the link again, Retire. Areas and fees.
- **Pay riders.** What is due now, Pay riders now and Weekly pay; each
  rider's cash owed as a callout with Handed in. Each rider's amount, Add
  bank where none (pressed: add or change it); recent payouts, a pending
  one pressed to mark it not paid.
- **Tables.** Tiles two across: an icon, the name, its rows, what it is
  for; the money log says it is kept by Taskiem. **One table:** the search,
  Search in and Sort, the page with round Previous and Next, and each row a
  card: the first column large, a phone or email under it, three more by
  their labels (a status in its colour); + adds a row, ⋯ holds Export,
  Import and Delete table. The Tables tool in the chat keeps its list.

### The chat

Drawn and agreed with the founder, 16 November 2026: the chat leaves the web
chat's look for the app's own, as sleek as WhatsApp and Telegram. Its tools
were drawn the day before. These screens are no longer compared with the
web's pictures (the parity test still builds and drives them).

- **Colours.** The chat's tokens are the app's (`tkAppLight`, `tkAppDark` in
  `theme.dart`, over the web's): no wallpaper, the page's ground; their
  bubbles white (#1A1B1A in the dark), yours pine with white words (a deep
  green, #1D3A2D, in the dark); read ticks lime. The tokens have a
  `copyWith` (generated) and `TkTheme.recolour` gives a part of a screen
  colours of its own (your own bubble's words, a sheet's boxes).
- **The conversation.** The head on the ground: Back, the face, the name in
  Inter Tight, who they are or "typing…" in lime, Search and ⋯ as round
  buttons, a hairline under it. Bubbles 20 px round; a run from one side
  tucks one corner, at its end. A quote with a lime bar and the sender's
  name; reactions as pills on the card's colour; the day and "unread" as
  pills; a voice note with a lime play button and bars that fill as it
  plays; three dots in a bubble while they type. Replying: the line on a
  card with a round ×.
- **The chats list.** "Chats" large with the business under it; ⋯ and + (Find
  a business, which no longer floats over the rows) round in the head. The
  search on a card; All, Unread, Starred, Archived as the switch.
  Taskiem's conversation on its own card, what needs you as an orange
  count. A business asking to link on a card with an orange capsule, Accept,
  Decline, and Block under ⋯. The chats on one card, faces on their
  gradients, the time lime when unread, read ticks lime, the count a pine
  pill (grey when muted). Customers with their own heading and count.
- **The tools.** The panel floats as a card on the ground; each tool on its
  accent's square (selling and money lime, products and orders teal, stock
  and bills orange, invoices and price lists blue, services and tables
  violet) with its count as a capsule. A tool's sheet on the ground: round
  Back and Close on the card's colour, the title in Inter Tight, the search
  on a card, filters as the switch, the rows on one card. Sell and Order:
  − and + round on a soft track, lime once picked; the total a pine bar.
- **Left as it was:** the product form keeps its order (the photo's ways
  under the picture, the fields one under another) in the new colours; the
  documents named in a message (`RefCard`).

## Settings

Settings is one list on the ground (`lib/screens/signed_in.dart`):

- You at the top: your face, name, email, and your role in the business. An
  account made in a chat adds its email there.
- Your businesses, with Switch, when there is more than one.
- This phone:
  - notifications on or off, and whether they show what the message says,
    as switches;
  - What this phone tells you, which opens the rest;
  - App lock as a switch: on locks every time Taskiem opens; the row opens
    the choice of when (at once, after a minute, after half an hour).
- Appearance as a switch of three.
- Signing in: Passkeys and Your phones, each with its count.
- The chats you work from: WhatsApp, Telegram and Slack, each with
  whether it is linked.
- For everyone here: Chat tools, a switch, for owners and admins.
- Sign out of this phone.

Every row that has more opens its own page (`SettingsPage`, Back to
Settings), with its groups as headings over cards (`SettingsCard`).

WhatsApp's page has the number linked large on the top card, with Unlink.
Under it are Link another number, opened when asked for, and the approval
PIN: PIN set with Remove, then Change the PIN, which opens its form.

The other pages from Settings:

- **Linked businesses:** being found on the top card, with your card as
  others see it, Edit your card and Share your link; the QR code is on
  the head's button. Your links follow as rows with their state. A
  request is answered on its row, and the rest (End link, Block, Withdraw,
  Unblock) is a tap on the row.
- **Your business in Explore:** your card as others see it, Be found in
  Explore as a switch, then the card's words and the photos.
- **Invoice settings:** the next invoice's number large, from the prefix
  and the next number. Then Getting paid (the connection, who pays the
  fee, part payments and automatic matching) and Credit terms.

## Find a business and signing in

Find a business (`lib/chat/explore.dart`) has ‹ Chats over a large title,
then the search and the category chips. A card's logo carries how many
businesses it is linked with as a badge ("12+", three and more; read as
"Linked with 12+ businesses"), and its categories are small chips under
its line. A Featured card's cover runs to its edges, with the logo, name
and Connect under it; Connect stays because the card is how one links.

Signing in (`lib/screens/sign_in.dart`) keeps the brand on pine with one
card over its edge. Under Sign in, Forgot password? and the passkey are
the WhatsApp and Telegram logos, round in their own colours, with "A code
on WhatsApp or Telegram" under them. Either one asks for the number, and
the code is typed into six boxes. These screens are no longer compared
with the web's pictures.

A forgotten password is done in the app (`lib/screens/forgot_password.dart`):

1. **Reset your password:** the email, starting from what was typed on
   the card. Email me a link sends POST /v1/auth/password/forgot.
2. **Check your email:** the server's answer is the same for any email.
   Below it are Open your email app (the inbox, not a new email), Send it
   again, and Back to sign in.
3. **Choose a new password:** the email's link,
   `https://<server>/reset-password#token=…`, opens the app here. That
   path is in the server's appPaths and in Android's intent filter. The
   new password is typed twice and has at least 12 characters. Saving it
   sends POST /v1/auth/password/reset and signs every session out.
4. **Password saved:** the new password is already filled in, so the
   person types their email and signs in.

A link that comes while someone is signed in opens Choose a new password
over the app.

## Sheets and the chats, as drawn

- **Sign up:** what the business does is five tiles, two to a row, each
  with an icon and short words. Get started takes the code in six boxes,
  and its chat has Taskiem's mark in the head and a round send button.
- **Give this to someone** (`give_task.dart`) is a sheet:
  - who it is for: the people as faces, then anyone in a role as chips;
  - when: Any time, By a time, Daily or Weekly as a switch;
  - the day and time to pick, each with its icon;
  - a photo when it is done, as a switch.
- **Book with a business:** the services are rows on one card. DAY and
  TIME are small capitals over the chips, the times four to a row, and
  the check step puts its lines and the fields on cards.
- **Availability:**
  - the state is large, with Free, Busy and Off as a switch;
  - Busy till is chips: 1 hour, 2 hours, Till 5pm (until 4pm in Lagos),
    Rest of today;
  - "Another time" opens the box to type a time;
  - deliveries held show as a warning.
- **An invoice or a bill in the chat:** their actions are tiles
  (`TkActionTiles`), not long buttons. An invoice has Record a payment,
  Cancel it and Open it; a bill has Question it, Paid outside and Open
  it. The lines sit on a card.
- **Approve:**
  - the authenticator's code is behind "Use a code from your
    authenticator instead", and shows by itself once the phone has no
    passkey or the server asks for it;
  - a first payment is a warning;
  - Approve carries the passkey's icon.
- **Connect:** Live and Test are a switch, the keys sit on a card, and
  More options is a row. Once connected, "Copy this address now" is a
  warning and each address sits on a card.
- **Taskiem's chat:**
  - an alert card is compact, with a short main step (Reorder 10; its
    full words go to screen readers);
  - Give this to… is a person's icon, and Later is the ✕ in the corner;
  - an approval card has an orange edge and kicker;
  - the briefing's Got it is a lime pill;
  - your own messages are white on pine.
- **A customer's order:** Delivered and They pick it up are a switch.
  The card has Ask them while questions wait, else Confirm, with Drop as
  a bin beside it.

A sheet made with `TkFormSheet` has one foot, not a foot inside a foot.
A sheet's Cancel button is filled with the card, with no line around it.

Checked again against the drawings, in both themes:

- **Every sheet:**
  - sits 14 px from the screen's edges, head, body and foot alike;
  - a form's parts are 12 px apart;
  - summary lines are small, with the label muted and the value firm.
- **The sign-in screens:**
  - the band has Taskiem's mark on a lime tile, then its name in words;
  - fields have small muted labels and sit on the page's ground, which
    is black in the dark;
  - the envelope and the lock tiles are blue.
- **An invoice or a bill:**
  - the state sits beside the kicker on the pine, red-tinted when due;
  - the tiles are pine, and Cancel and Paid outside are ringed icons.
- **Taskiem's chat:**
  - the briefing has its day on the greeting's line, and the week's bars
    run across the card;
  - NEEDS YOU sits over the alerts, each alert with a solid coloured
    square and 34 px buttons;
  - the approval card's Approve is pine (lime in the dark), 40 px.
- **Approve:** carries no "needs your passkey" pill or line; the
  fingerprint on the button says it.

- **A product:**
  - its main photo is centred, 96 px; with none yet, a camera on violet
    where it goes;
  - editing one, Prices come first and Details (name, category) after the
    rows;
  - a tool form's Cancel is white.
- **Sizes and colours:** with two choices and up to three across, the
  boxes are one table: the last choice across the top, the one before it
  down the side. Same number for all and Fill sit at its foot.
- **A price list's items, and who sees it:** sheets, with Cancel and Save
  at the foot. A tick in the app's own look is a rounded lime square;
  "+ Lower price for more" is bold words.

Left different on purpose:

- Featured businesses keep a small Connect: it is how one links from
  them.
- Choose a new password and Password saved keep "Back to sign in": an
  iPhone has no other way back.
- Availability adds "Another time", to type one's own.
- A customer's order keeps what changes it: − and +, the address, the
  fee and how they pay.

## Order

1. Orders and Money, and the chat's motion. Done.
2. Today, Money and Orders in Direction B. **Done; waiting for the founder's word.**
3. Invoices and bills. **Done; waiting for the founder's word.**
4. Stock, Supplies, Suppliers and an order to a supplier. **Done; waiting for the founder's word.**
5. One order: a customer's, a booking, a business's, a rider's delivery. **Done; waiting for the founder's word.**
6. Team, riders and tables. **Done; waiting for the founder's word.**
7. The chat: the conversation, the chats list, the tools. **Done; waiting for the founder's word.**
8. The settings pages: done ("Settings" above).
9. Find a business, signing in and a forgotten password: done (above).
10. Signing up, the sheets and the two chats: done (above).
