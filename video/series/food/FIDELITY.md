# How the Food footage differs from the real app

The footage is the app's own screens, drawn by its test runner, with Ada's Kitchen's data. It
is **not** a recording of a live server. Read this before publishing; if a caption says more
than the app shows, trim it.

## For every film
- The server is a stand-in: its answers are made up in the story's names, amounts and dates.
  No real money moves and no real customer data is shown.
- Text that comes from the server (an answer, an alert's line, an invoice's send message) is
  illustrative, not copied from a live run. Numbers are the story's.
- Light theme only. The phone clock follows `films.json` (default 13:00).
- The phone, status bar, keyboards, the fingerprint prompt and the tap ring are drawn by the
  compositor, not the app. Android's real fingerprint dialog differs.
- A "cut" is two separate set-ups, not one live run. The phone numbers, account numbers and
  references shown are invented.

## Episode 1
**food-open-your-shop**: uses the server's real sign-up English (`engine/lang/messages/en.json`);
the replies are scripted; "free for 14 days" is the app's own line.
**food-be-found**: two phones with a cut; the cards for Mama Put Corner (Surulere) and Island
Suya Spot (Lekki) are made up; cards show letters, not photos; the buyer's account (Chioma) is invented.

## Episode 2
**food-put-goods-in**: the grid with counts is the **"Sizes and colours: Set up" editor on a
product's form** (one choice, Size: 5, 10, 20 plates); the Products list's "Make one product
with sizes and colours" button only groups existing products and has no counts. The camera is a
stand-in that returns a drawn party-pack picture. The product list is shortened and the recorded
jollof price (₦4,000) differs from the story's counter price (₦3,500). The category "Party packs"
is typed (no existing chip).

## Episode 3
**food-sell-at-counter**: the real Sell sheet; the four items and Zobo (and its stock) are
stand-ins at the story's prices (jollof ₦3,500, zobo ₦1,500). Stock does not visibly go down.
**food-customer-order**: the order card is a recorded-style draft answer matching Chioma's
message, not Taskiem reading it live; the order reference "AK-1" and her number are invented;
the bubble's time may not match the phone clock.

## Episode 4 and 5
Same stand-in approach as Fashion's invoice, match and deliver films (see
`../fashion/FIDELITY.md`): the app shows only a "Saved" tick after sending an invoice and no
"Matched" line after matching (both reopen Invoices to show the result); the match picker is the
Money row's own sheet; in deliver the rider's moves are made on the stand-in server between
frames (time skipped without pictures), the chat-to-Orders switch is a cut, and the Orders tab is
shown without the home bar. "TRF FROM OKONKWO C" is the fixture's own style of description.

## Episode 6
**food-add-staff**: the role list has only owner and cashier, so Cashier is the default; joining
is a stand-in (WhatsApp joining is not an app screen), so a cut shows Tunde on the Team page
("in since 08:12", clock 09:00).
**food-tasks-and-handover**: the phone app has **no "tag @Tunde in the chat" way** to give a task
and **no due-date field**; "by 2pm" is only in the words. The shift totals (₦148,500 in 23 sales,
cash ₦96,000, transfer ₦52,500) are invented. The handover sheet opens over the Tasks page here;
in the app it opens from the chat's tool tile. A cut shows Tunde's phone.

## Episode 7
**food-find-a-supplier**: the Bayo card's area ("Mile 12 Market, Lagos"), tagline and the two
other cards (Mushin Spice Traders, Yaba Pack Supplies) are made up.
**food-reorder**: the price list (Rice 25kg bag ₦32,500, Oloyin beans, Ijebu gari) is invented; the
item is set as "remembered", so the "How should we treat it?" question does not appear; the sent
state is the card disappearing, not a confirmation screen.

## Episode 8
**food-price-list / food-a-deal**: all on Ada's side of the app; Rivers Foods' own screens are not
filmed; the thread is cut between set-ups; the "Needs your OK" card's title ("{who} would agree
{amount} with {business}") is the app's real string, the rest of its line is the story's; after
Agree the card simply goes.

## Episode 9
**food-know-your-numbers**: the briefing card and Today use the stand-in data of the story
(₦486,500 up 12%, owed ₦212,000, 3 low, 4 deliveries); the week's answer is not invented in a
caption.

## Episode 10
**food-approve-a-payment**: the Needs-your-OK card is filmed in Taskiem's own conversation (not on
Today); the approval is stood in, so there is no passkey step; the account number is made up.
**food-app-lock**: scene two is a fresh set-up (Settings inside the app lock); leaving the app
for a few minutes is a clock jump, not drawn; "Your phones" shows 0.
