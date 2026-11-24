# Filming the customer series ("Taskiem for you")

Read `video/series/FILM-GUIDE.md` first (environment, the Film API, the loop, the rules, and
**the founder's rule: every step must be reproducible by a viewer in the real app**), then
`video/series/everyone/README.md` (the outline, written before the app was checked),
`docs/spec/taskiem-for-everyone.md` and `mobile/CLAUDE.md`.

This series is for **customers** (people with a personal account, no business): the phone
app's customer side. Screens are in `mobile/lib/chat/` and `mobile/lib/screens/`
(`discover.dart`, `customer_app.dart`, `customer_orders.dart`, `order_sheet.dart`,
`book_sheet.dart`, `bookings_page.dart`, `groups.dart`, `group_chat.dart`, `group_new.dart`,
`people*.dart`, `your_orders.dart`, `you*.dart`, `app_sign_up.dart`). The app's own tests are
the set-up to copy: `grep -l -i -E "discover|customer|order_sheet|book|group|people" mobile/test/*_test.dart`
(`everyone_test.dart`, `customer_orders_test.dart`, `order_sheet_test.dart`,
`service_book_test.dart`, `groups_test.dart`, `people_test.dart`, `app_sign_up_test.dart`...).
Film patterns: `mobile/test/film_food_*_test.dart`, `film_school_*_test.dart`.

## Rules specific to this series
- Films are named `you-<name>` (frames in `<out>/you-<name>/`), tests
  `mobile/test/film_you_<name>_test.dart`. Shared helper files need **unique names**
  (`film_tobi_ep12_world.dart`, with your episode numbers). Do not edit existing films, the app's
  own tests or `mobile/lib`; import or copy.
- **Only what the app does, reproducible by the viewer.** Before a film, read the screen's code
  and find the **real route** (which tab, which row, which button). Reach each screen by taps; the
  film may begin on a screen the person opens before the video starts. If a step in the outline
  does not exist in the phone app (the browser chat link, a group order feature, a stand-in
  only option), **do not fake it**: film what is real, and say so in your report so the episode
  can be reshaped. The outline was written before the app was checked.
- Positioning: Taskiem sits beside WhatsApp and Telegram; the films are about dealing with
  a business (find, ask, order, pay, follow), not chatting for its own sake. Never claim
  end-to-end encryption (the app says people chats are not), calls, or store downloads.
- Cuts to a second person's phone are fine.
- Captions: 8 to 12 words, present tense, no caption shorter than 2 s, first caption with
  `who:`; phone clock by `--time` when composing (report it; match any time the app prints).
- `flutter analyze` clean. One real render at a time (background), `flutter test --timeout 40m`;
  compose a 9:16 preview and **look at stills** at the key moments (tap rings, keyboard,
  result), checking each caption against its frame.
- Report back: files, output folders, running time, captions with times, what each film shows,
  the real route to each screen, and every stand-in or difference.

## The story
**Tobi Adewale**, 27, a young professional in Lagos (phone-first). **Mama Tobi**, 54, his
mother, new to apps. **Chioma Nnadi**, a customer of **Ada's Kitchen** (Yaba). The businesses
Tobi deals with: **Ada's Kitchen** (food: jollof rice ₦3,500, zobo ₦1,500, delivery ₦1,500),
**Bayo Rice Mills**, **Kola** (a service business that takes bookings: use the app fixture's
own service if one exists). Use the app fixtures' own names wherever you can.

## Films (the helper decides what the app lets us film)
| Episode | Films |
| --- | --- |
| 1 Sign up with your phone | `you-sign-up` (sign-up in the app; the You tab after) |
| 2 Find a business | `you-find` (Discover: near you, categories) |
| 3 Chat with a business | `you-chat-business` (open a business chat in the app; the browser chat-link way only if the phone app shows it) |
| 4 Order and pay | `you-order-pay` (the order card, basket, paying) |
| 5 Follow your orders | `you-follow-orders` (Your orders, a delivery update) |
| 6 Book a service | `you-book` (free times, the booking card, Bookings) |
| 7 Share a business | `you-share` (send a business or order to a person; a stranger's first message as a request) |
| 8 Groups for a purpose | `you-groups` (a new group, its purpose) |
| 9 Order together | `you-order-together` (only what the app has) |
| 10 Keep it safe | `you-keep-safe` (mute, block, delete the account; what is stored) |
