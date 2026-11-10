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
| 2 | Order in a business chat, and the seller's side of it | to do |
| 3 | Bills: get one, pay it, query it | to do |
| 4 | Customers' WhatsApp orders | to do |
| 5 | Staff by phone, and clocking in | to do |
| 6 | Tasks for staff | to do |
| 7 | The morning briefing | to do |
| 8 | Riders and deliveries | to do |

## Making one

```sh
bash web/e2e/demo/serve-demo.sh &              # a fresh demo world at localhost:18090
node web/e2e/demo/seed.ts                      # filled with the demo businesses
node video/howto/01-link-and-price-list.mjs    # out/howto/01-link-and-price-list.mp4 and .srt
```

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
