# How the Phones and gadgets footage differs from the real app

The footage is the phone app's own screens, drawn by its test runner, with Techline Gadgets'
data (the films' own set-ups, adapted from the Food films). It is **not** a recording of a live
server. Read this before publishing; if a caption or a line says more than the app shows, trim it.

## For every film
- The server is a stand-in: its answers are made up in the story's names, amounts and dates. No
  real money moves and no real customer data is shown. All names, phone numbers, account numbers,
  references and addresses are invented.
- Products are invented (Nova X5, Pulse tablet, Volt power bank, a charger); no real brand is shown
  or said. **Never claimed, because the app does not do it:** IMEI or serial numbers per device,
  warranty, repairs, trade-ins, instalment sales for customers.
- Light theme only. The phone clock follows `films.json`: 13:00 for every film except
  gadgets-add-staff 09:00, gadgets-know-your-numbers 08:00, gadgets-customer-order 12:03 and
  gadgets-deliver 13:20.
- The phone, status bar, keyboards, the fingerprint prompt and the tap ring are drawn by the
  compositor, not the app. Android's real fingerprint dialog differs.
- A "cut" (Kehinde's phone, Maryam's phone, Ikenna's phone) is a separate set-up, not one live run.
- Positioning: Taskiem runs the shop's work beside the chats it already has. The films and lines
  never say it replaces WhatsApp.
- Call to action: **Start your free trial.** The trial length is **14 days**, from the app's own
  sign-up line ("free for 14 days"); confirm it before publishing.

## Where the series differs from the first outline, because the app differs
- **Sizes and colours really take two axes** (Storage 64, 128, 256 GB by Colour Black, Blue, Gold:
  nine cells, a count in each). That is the **"Sizes and colours: Set up" editor on a product's
  form**; the Products list's "Make one product with sizes and colours" button only groups existing
  products and has no counts.
- **The morning briefing shows only a count of low items** ("3 items"). It cannot name Nova X5,
  128 GB, Blue, so the outline's hero feature for Episode 9, **"low stock told per variant", is not
  shown and not said**. The low-stock card in **gadgets-reorder** (Episode 7) does name the variant
  ("Nova X5, 128 GB, Blue"), and that is where the variant is told.
- **Maryam is added as a Shift manager**, not a cashier (Handover is a Shift manager tool). The
  narration says so, and the on-screen caption about her in the approvals film is overridden to
  match (see below). She is described as the sales attendant in the story.
- **The WhatsApp order card is a stand-in** (see gadgets-customer-order). The outline asked whether
  the order reader matches a size and colour said in words. The reader runs on the server and
  matches a said size only when it equals the stored value ("128 GB" against "128GB" is a spelling
  that matters), so the narration says only that **Taskiem reads the message into an order card** and
  never promises that it always matches a variant.
- **No IMEI/serial, no warranty, no repairs, no trade-ins, no instalments** are filmed, said or implied.

## Per film
Phone clocks and trims are in `films.json`. Lengths are of the rendered films.

**gadgets-open-your-shop** (Ep 1, 40.4 s): uses the server's real sign-up English
(`engine/lang/messages/en.json`); the replies are scripted; "free for 14 days" is the app's own
line; "Phones and gadgets" is the sign-up list's number 4.
**gadgets-be-found** (Ep 1, **trimmed to 24.8 s** from 26.3 s: the last second and a half were
held): two phones with a cut; the cards for Ojota Mobile Hub and Yaba Tablet Store are made up;
cards show letters, not photos; Kehinde's account is invented. The category is the app's own
"Phones and gadgets".
**gadgets-put-goods-in** (Ep 2, 54.7 s): the grid with counts is the editor above, with two
choices; the camera is a stand-in that returns a drawn phone picture. The category "Phones" is typed.
After the fill, 128 GB, Blue is set to 12 (the story's best seller), the other eight cells to 5.
**gadgets-sell-at-counter** (Ep 3, 33.3 s): the real Sell sheet; the Nova X5 with its sizes and
colours, the charger (₦6,500) and the counts are stand-ins at the story's prices. **Stock does not
visibly go down** after Record the sale, so the last caption is overridden in
`captions/gadgets-sell-at-counter.json` to "Tap Record the sale to save it." and the narration says
only "She records the sale."
**gadgets-customer-order** (Ep 3, 21.4 s): the order card is a recorded-style **draft** that matches
Chiamaka's message ("128 GB" spelled as the stored value), not Taskiem reading it live; the
reference "TG-1" and her number are invented; the delivery fee ₦2,000 and the total ₦187,000 are
the story's. The bubble's time may not match the phone clock (12:03).
**gadgets-send-invoice, gadgets-match-payment** (Ep 4; **send-invoice trimmed to 27.5 s** from
28.9 s, **match-payment to 25.5 s** from 26.9 s, both for a held last stretch): same stand-in
approach as Food and Fashion. The app shows only a "Saved" tick after sending an invoice and no
"Matched" line after matching (both reopen Invoices to show the result); the match picker is the
Money row's own sheet. "TRF FROM OKORO AND SONS" is the fixture's own style of description; INV-0014
and the figure (₦1,200,000 for ten tablets) are the story's.
**gadgets-deliver** (Ep 5, 23.4 s): the rider's moves (offer, accept, pick up, delivered) are made
on the stand-in server between frames, and **the rider's WhatsApp side is stood in** (the offer is
not shown arriving on Emeka's phone). The chat-to-Orders switch is a cut; the Orders tab is shown
without the home bar (as in Food); the ₦187,000 cash at the door is the story's.
**gadgets-add-staff** (Ep 6, 23.0 s): Maryam is added as a **Shift manager**. Joining by WhatsApp is
not an app screen: a cut shows Maryam on the Team page ("in today", clock 09:00).
**gadgets-tasks-and-handover** (Ep 6, 47.6 s): the phone app has **no "tag @Maryam in the chat" way**
to give a task and **no due-date field**; "by 2pm" is only in the words. The shift totals
(₦428,500 in 11 sales) and the ₦500 short are the story's stand-ins. Maryam's phone is a cut;
the Handover sheet is opened from the chat's tools.
**gadgets-find-a-supplier** (Ep 7, 20.6 s): the Gwarzo Distributors card (Kano) and the other
cards are made up; Connect shows Requested.
**gadgets-reorder** (Ep 7, 20.8 s): the supplier's price list (₦140,000 a phone) is invented; the
low-stock card names the variant (the one place in the series that does); the "How should we treat
it?" question does not appear; the sent state is the card going, not a confirmation page.
**gadgets-price-list** (Ep 8, 23.3 s, no trim): **the film ends before the Save changes result**
(the last frame is the product form with the lowest price typed), so nothing about the saved result
is said. Typed prices show without thousands separators (the app's own field). On the product
form, "Counted by" shows the app's "Could not be read" line, a stand-in artefact. The ₦185,000 list
price is in the data but not on screen.
**gadgets-a-deal** (Ep 8, 29.8 s): all on Ikenna's side of the app; Kehinde's own screens are not
filmed; the thread is cut between set-ups. **The card on Maryam's phone does not change after "Send
counter"** (the film jumps to a second render with Kehinde's ₦3,450,000 counter in place). **The
lowest price (₦3,500,000) is not enforced by the stand-in**: "under Ikenna's lowest price" and
"waits for Ikenna's OK" are the alert text and the story. "Lapses in 47 hours" is what the card
shows (never "7 days"). **The credit limit and the days to pay are not shown**, so they are not
said. After Agree the card simply goes.
**gadgets-know-your-numbers** (Ep 9, 25.0 s): the briefing and Today use stand-in figures
(₦1,284,000 up 9%, owed ₦640,000, 3 low items, 2 deliveries today). The briefing's phrase "last
Wednesday" appears on a Thursday card (the app's own wording); the film is not trimmed. **The
Today scene figures are stand-ins** for 08:00 and are not the story's numbers. "How we did this
week" is not filmed.
**gadgets-approve-a-payment** (Ep 10, **trimmed to 20.0 s** from 21.1 s): the Needs-your-OK card is
filmed in Taskiem's own conversation (not on Today); the approval is stood in, so there is no
passkey step; the account number is made up. The first caption is overridden in
`captions/gadgets-approve-a-payment.json` (the rendered text said "his cashier"; Maryam is a shift
manager).
**gadgets-app-lock** (Ep 10, 24.9 s): scene two is a fresh set-up (Settings inside the app lock);
leaving the app for a few minutes is a clock jump, not drawn (as in Food's app-lock film).

## Overrides and trims, in one place
- `captions/gadgets-sell-at-counter.json`: the last caption no longer says stock goes down.
- `captions/gadgets-approve-a-payment.json`: "his cashier" becomes "his shift manager".
- `films.json` `end`: be-found 24.8, match-payment 25.5, send-invoice 27.5, approve-a-payment 20.0.
- No caption slot is under 2 s, so no captions were merged for length (the two 2.0 s slots, in
  gadgets-a-deal and gadgets-send-invoice, carry the shortest lines).

## Episode 0
The reel is ten stretches (about 12 s each, 120 s in all) of the films above, one per episode in
order, each with its own caption and chapter tag. Taps and keyboards in each stretch are kept; each
stretch inherits the differences listed for its film. It never shows or says more than the chapters do.
