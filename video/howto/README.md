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
| 6 | Tasks for staff | to do |
| 7 | The morning briefing | to do |
| 8 | Riders and deliveries | to do |

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
