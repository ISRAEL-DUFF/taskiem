# How the footage differs from the real app

The footage is the app's own screens, drawn by its test runner, with the story's
data (Zuri Atelier). It is **not** a recording of a live server. This file says,
film by film, what is stand-in or left out, so nothing is claimed that the app
does not do. Read it before publishing; if a caption in the footage says more
than the app shows, trim it.

## For every film
- The server is a stand-in: its answers are made up in the story's names, amounts
  and dates. No real money moves and no real customer data is shown.
- Text that comes from the server (an answer to "how did we do", an alert's line,
  an invoice's send message) is illustrative of the app's answers, not copied
  from a live run. Numbers are the story's.
- Light theme only; the phone clock is 13:00 unless noted (`films.json`).
- The phone, its status bar, keyboards, the fingerprint prompt and the tap ring are
  drawn by the compositor, not the app. Android's real fingerprint dialog differs.
- A "cut" between two scenes is two separate set-ups, not one live run.

## Episode 1
**open-your-shop**: the sign-up conversation uses the server's real English
(`engine/lang/messages/en.json`, `signup.*`); the replies are scripted.
**be-found**: two phones, a cut between them. The two other Explore cards (Adire
Corner, Lagos Lace House) are made up and have letters, not photos. Turning on
Be found here did not ask "What do you sell?" (that prompt is elsewhere in the
app). The profile card shows grey photo placeholders.

## Episode 8
**price-list**: the product form is shown in a plain sheet (as its own test does),
not reached through the stock tool. Typed amounts show without commas. Saves go to a
stand-in server.
**a-deal**: all on Zuri Atelier's side; Ada's own screens are not filmed. The thread is
cut between three set-ups. The "Needs you" card's title ("{who} would agree {amount} with
{business}") is the app's real string; the rest of its line ("was ₦960,000 · under your
lowest price") and all amounts are the story's. After Agree the card simply goes.

## Episode 9
**know-your-numbers**: the sales answer ("Sales this week: ₦2,871,000, up 17%...") is a
stand-in. In the app, the week's answer is a chat message with a chart, which this flow
does not show. Clock 10:30 (a morning).

## Episode 10
**approve-a-payment**: the Needs-your-OK card is filmed in Taskiem's own conversation, not
on the Today tab (Today's Needs you row has an Approve button but its sheet has no "who
asks" line). The approval is stood in, so there is no passkey step. About 4 s of empty chat
follows the card.
**app-lock**: scene two is a fresh set-up (the same Settings inside the app lock); leaving
the app for a few minutes is a clock jump, not drawn. Your phones shows 0 (stand-in).

## Episodes 4 and 5
**send-invoice / match-payment / deliver**: the tools panel still shows the fixture's
Ada's Kitchen counts in places ("Products 34 items", "Stock 5 low"), and the chat header
"Tuesday / 2 unread messages" is the fixture's. Names are shown in full; the real list
may mask them ("N•••"). "TRF FROM NNEKA EZE" is a made-up transfer description in the
fixture's style. The app shows only a "Saved" tick after sending an invoice (the "texts
Nneka with a pay link" line is the story's, from the send answer); after Match it, no
"Matched" line appears, so both films reopen Invoices to show the result. The match
picker is the Money row's own sheet, not the To match page with suggestions. In deliver,
the rider's moves are made on the stand-in server between frames (31 s of app time is
skipped without pictures each time so the page's poll picks them up): cuts, not live
waits; the chat-to-Orders switch is a cut; the Orders tab is shown without the home bar.
