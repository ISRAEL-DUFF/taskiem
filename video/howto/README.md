# How-to videos

Short videos that show how to do one thing in Taskiem, step by step. They
are recorded from the real app on the demo world (`web/e2e/demo/`), on a
phone-sized screen, with a caption under the phone for each step. They have
no sound, so they work muted and in any language: an `.srt` file is written
beside each video, so a voice-over or a translation can follow the same
timings.

| # | Video | Script |
| --- | --- | --- |
| 1 | Link with a supplier, share a price list, order from it | `01-link-and-price-list.mjs` |
| 2 | An order in the chat, from both sides: accept, ready, sent, delivered, All came, the invoice | `02-order-in-chat.mjs` (after video 1) |
| 3 | A bill: question it, the seller takes an amount off, pay outside Taskiem and say so | `03-bills.mjs` (after videos 1 and 2) |
| 4 | A customer's order from WhatsApp: read into an order card, a delivery fee, confirmed, a reply | `04-customer-whatsapp-order.mjs` (after videos 1 to 3) |
| 5 | Staff by phone, and clocking in: added from the chat, joining from WhatsApp, usual hours, in and Clock in, who is in | `05-staff-and-clocking-in.mjs` (a fresh world; needs none of 1 to 4) |
| 6 | Tasks for staff: given by tagging in the chat, a daily one, Done from the chat, the Tasks page | `06-tasks-for-staff.mjs` (a fresh world; needs none of 1 to 5) |
| 7 | The morning briefing: the card, a tile, Got it and back, how did we do this week | `07-morning-briefing.mjs` (a fresh world, after 8:00 Lagos time; needs none of 1 to 6) |
| 8 | Riders and deliveries: the order needs a rider, Assign rider, Kunle on WhatsApp (accept, picked up, arrived, cash received), the Deliveries page | `08-riders-and-deliveries.mjs` (a fresh world; needs none of 1 to 7) |
| 9 | Selling at the counter and the shift handover: Sell in the chat's tools (pick, how it was paid, record), Handover (the shift's sales, cash counted, short or over, a note) | `09-selling-and-handover.mjs` (a fresh world; needs none of 1 to 8) |
| 10 | Products and stock: Products in the chat's tools, Add product (name, category, price, sold by, in stock), Stock with low first, the Low stock chip, goods came in with a note | `10-products-and-stock.mjs` (a fresh world; needs none of 1 to 9) |
| 11 | Invoices and matching payments: Invoices in the chat's tools, New invoice (sent by SMS with a link to see it and pay, or kept as a draft), Money's To match, Match to an invoice | `11-invoices-and-payments.mjs` (a fresh world: it matches the seeded ₦37,500 transfer from OKONKWO C; needs none of 1 to 10) |
| 12 | Approvals: the Needs your OK card (who asks, what, how much, to which account), Approve it? with the first-payment warning, a recorded comment, the line it leaves | `12-approvals.mjs` (a fresh world: it approves the seed's request from Tunde to pay Bayo Rice Mills; needs none of 1 to 11) |
| 13 | Your own WhatsApp number: Settings, WhatsApp number, the guide, the ids and keys from Meta, Connect, the addresses and templates; a customer writes to the number and Ada replies from it | `13-own-whatsapp-number.mjs` (a fresh world: Ada's number not yet connected; needs none of 1 to 12) |
| 14 | Deals: a lower price for 50 or more and the lowest price; Rivers Foods orders 50 and makes an offer; Tunde counters; Rivers counters under the lowest price; Tunde accepts, it waits for Ada's OK; she agrees in her Taskiem chat | `14-deals.mjs` (a fresh world; needs none of 1 to 13) |
| 15 | Explore and connecting: Find a business, search by what they sell, a card with photos, Connect and Requested; the other side accepts; a business that asked you: Accept, Decline or Block | `15-explore-and-connect.mjs` (a fresh world: Kola's Bakery has asked Ada, Mallam Musa Suya Spot is not linked; needs none of 1 to 14) |
| 16 | Tags in the chat: # one of a business's items, @ a product (a card with Order and your own stock), /bill a bill, and @ in the Taskiem chat finding a business you don't trade with yet | `16-tags-in-the-chat.mjs` (a fresh world; needs none of 1 to 15) |
| 17 | The tools in a chat with a business: in Ada's chat with Bayo Rice Mills the tools are about Bayo (his shop at his prices for her, orders and bills between them, the statement, Invoice them); Bayo looks around Ada's storefront (her prices, how many are left) and books one of her services (day, time, phone, note); her own tools stay in her Taskiem chat | `17-business-chat-tools.mjs` (a fresh world; it turns Ada's online bookings on first; needs none of 1 to 16) |

## Making one

```sh
bash video/howto/world.sh                      # a fresh demo world at localhost:18090, seeded
node video/howto/01-link-and-price-list.mjs    # out/howto/01-link-and-price-list.mp4 and .srt
node video/howto/02-order-in-chat.mjs          # each video starts where the one before ends
```

`world.sh` runs the demo server (`web/e2e/demo/serve-demo.sh`) with
WhatsApp and the model switched on against stand-ins (`fakes.mjs`), as the
demo's fake Xenibase stands in for stock and photos. The WhatsApp stand-in
accepts what is sent, answers for Ada's Kitchen's own number, and keeps
what it sent (`GET /demo/sent?to=…`, with a template's buttons) so a
video can tap them for someone, as Kemi taps Join in video 5; the model
stand-in gives the order reader its reading of the messages the videos
send, while matching what was said to the menu and its prices stays
Taskiem's own code. `whatsapp.mjs` delivers a customer's message, or a
person's words and taps to Taskiem's own number, as WhatsApp's webhook
does, signed.

Each script records its acts (`video/out/recording/<act>.mp4`, with
`<act>.cues.json` holding its captions and their times), then joins them.
Give act names to record only those again (`… order`), or `build` to join
without recording. The acts change the world as they go, so a whole run
wants a freshly seeded one.

`howto.mjs` holds what every video shares:

- `act(page, name, who, fn)` records one act. `who` is the line over the
  captions, for example "Ada · Ada's Kitchen, the buyer".
- `say(text, hold)` shows a caption from now until the next one, then waits
  `hold` ms so it can be read.
- `build(slug, parts)` joins title cards (`{ title }`, with the words in
  `*stars*` in pine) and acts (`{ act }`) at 1080 × 1920.

Each caption says what the screen is showing at that moment, in the app's
own words: check every caption against a frame of the finished video.
Set-up that belongs to another video, such as Grace's stock in video 1,
is done off camera through the API.
