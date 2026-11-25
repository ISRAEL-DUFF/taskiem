# How the "Taskiem for you" footage differs from the real app

The footage is the phone app's own customer screens, drawn by its test runner, with
Tobi Adewale's data (`mobile/test/film_you_*_test.dart` and their world files). It is **not**
a recording of a live server. Read this before publishing; if a caption or a line says
more than the app shows, trim it.

## For every film
- The server is a stand-in: its answers are made up in the story's names, amounts and
  dates. No real money moves, no real customer or business data is shown. All names, phone
  numbers, addresses and references are invented.
- Light theme only. The phone clock follows `films.json`: 13:00 for every film except
  `you-order-pay`, 13:02.
- The phone, status bar, keyboards and the tap ring are drawn by the compositor, not the app.
- Other people's phones (Mama Tobi's in Episode 7, Bola's and Kemi's in Episode 9) are
  separate set-ups, not one live run.
- Positioning: Taskiem sits beside WhatsApp and Telegram. The films and lines never say
  it replaces them.
- **Never claimed, because the app does not do it or it is not true:** end-to-end
  encryption (the app says chats are **not** end-to-end encrypted, and Episode 10 says so),
  calls, a download from the app stores (the app is not in them yet), a browser chat without
  installing the app.

## Where the series is reshaped, because the phone app differs from the first outline
- **Episode 1 is the welcome screen**: the phone number, the six-digit code, then **Make your
  account** (name, "I'm 18 or over", the terms) and Create. It lands on You, with Chats,
  Orders and Discover. The narration does not say which app the code arrives in.
- **Episode 2 has no "near you".** Discover says Near me is not here yet. The film uses the
  **Area box** (Yaba) and the **category chips** (Food and drinks). The Discover **name
  search** is *not filmed*, so it is not narrated.
- **Episode 3 starts from the chat-link card** (the app's own chat-link screen), not a
  phone browser: the browser-without-installing way is **not in the phone app** and is not
  shown or said. The two lines of the film that are Ada's own messages (the note on what Ada
  sees, "Ada asks about offers", her reply with prices) are the stand-in's.
- **Episode 4: the order is built in Their shop** (plus steppers, Review order, Deliver it to
  me, area, address, Place order), not in a hand-typed message. After Ada accepts, her order
  card and the **pay card** come into the chat. The film ends on the Pay tap: the **external
  pay page is not filmed**, and nothing is said about what it looks like or about paying
  inside the app.
- **Episode 5**: Orders on the You tab, the order's page, the delivery steps. The **delivery
  update is a pull-to-refresh** (the film shows the pull, then the steps change); it does not
  arrive by itself on screen.
- **Episode 6: customers have no Bookings page.** A booking is a **row in Orders**, kind
  booking. The booking is made through the chat's tools, **Book**. **No booking card** is
  filmed and none is narrated. The business is called "Kola" in the app fixture and the
  service is a haircut, not Kola's Bakery (see the open question in `README.md`).
- **Episode 7**: sharing a business or an order with a person, and a stranger's first message
  waiting as a **message request** on Mama Tobi's phone (a separate set-up).
- **Episode 8**: **New group** with a purpose (Description), people added; the film ends on
  the group's tools button offering Order together. It does not film an order.
- **Episode 9: Order together is real**: one shared basket from **one business**, each person
  adds items, the person holding the basket places it. **"I'll pay all" or "Each pays their
  share" only exist where the business takes payment in the app**, so the narration is
  conditional. **Pay my share is not filmed.** Bola's and Kemi's items arrive from their
  phones (stand-ins).
- **Episode 10**: Mute, About (the app says chats are not end-to-end encrypted), Block,
  Blocked people, Privacy (what a business sees: your name and number), and **Delete your
  account**, whose page is shown but **deletion is not completed** in the film.

## Per film
Phone clocks: all 13:00 except you-order-pay, 13:02.

**you-sign-up** (Ep 1): the code digits and the name are typed on the compositor's keyboard.
27.9 s, no trim.
**you-find** (Ep 2): Ada's Kitchen is the only food business near Yaba in the stand-in's list.
30.7 s, no trim.
**you-chat-business** (Ep 3): 30.4 s. Two captions merged in `captions/you-chat-business.json`
(a 1.8 s slot at 4.0 s).
**you-order-pay** (Ep 4): 46.9 s, no trim: it ends on the Pay tap. Prices are the story's:
jollof ₦3,500 twice, zobo ₦1,500, three items ₦8,500, delivery ₦1,500, total ₦10,000.
**you-follow-orders** (Ep 5): 25.1 s. Two captions merged in `captions/you-follow-orders.json`
(a 1.5 s slot at 15.8 s).
**you-book** (Ep 6): **trimmed to 32.8 s** (the last second is a held frame). There is a held
**3.3 s** stretch (20.8 to 24.1 s) in the middle of the film with the booking confirmed and
nothing moving; `end` cannot cut the middle, so it stays (the caption "The booking is
made" and its narration fill it).
**you-share** (Ep 7): 50.6 s, no trim. The cut to Mama Tobi's phone is at about 29 s.
**you-groups** (Ep 8): **trimmed to 43.4 s** (the last second is held).
**you-order-together** (Ep 9): **trimmed to 62.6 s** (from 64.8 s: the last two seconds are held).
**you-keep-safe** (Ep 10): **trimmed to 63.0 s** (from 65.6 s). The About page is held for
8 s (16 to 24 s) while the encryption line is read; that is deliberate.
Caption lines never claim more than the film shows; the one on "Nothing is deleted yet" is the
app's own note.

## Episode 0
The reel is ten stretches (about 12 s each, 122 s in all), one from each film, in episode
order, each with its own caption and chapter tag. Taps and keyboards in each stretch are
kept; each stretch inherits the differences listed for its film. It never shows or says
more than the chapters do.
