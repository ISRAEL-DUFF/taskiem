# Video series by kind of business

One end-to-end course for each kind of business: how a fashion business uses
Taskiem from first day to full day, then the same for food, for phones and
gadgets, and so on. A viewer should watch their series and think "that is my
shop". Written 17 November 2026.

## 1. The pattern

Every series has the **same spine** (the chapters a business goes through) and
a **different world** (the business, the people, the products, the problems).

| Fixed across every series | Changes in each series |
| --- | --- |
| The ten chapters in section 2, in the same order | The business, its owner and area |
| The narrator, the voice and the pronunciation file | The cast (owner, helper, supplier, customer, rider) |
| The look: the app's real screens, captions, music, end card | The products, sizes, prices, photos and words they use |
| Length: 45 to 90 seconds per episode | The one **hero feature** each chapter leans on |
| An opening title card, a closing "next episode" card | The demo world the screens are recorded in |

The app's own categories give us the list of series (`web/src/lib/explore.ts`):
Food and drinks, Fashion, Phones and gadgets, Electrical and electronics,
Beauty and personal care, Building materials, Farm produce, Health and
pharmacy, Home and furniture, Cars and spare parts, Delivery and logistics,
Wholesale and distribution, Services.

## 2. The spine: Episode 0, then ten chapters

| # | Chapter | What the viewer can do after it | Taskiem parts (existing how-tos to start from) |
| --- | --- | --- | --- |
| 1 | **Open your shop** | Sign up, set the business, pick categories, be findable | Sign-up, Explore profile (how-to 15 for the Explore part) |
| 2 | **Put your goods in** | Add products with photos, categories and (where it fits) sizes and colours | Products and stock (how-to 10), categories and variants |
| 3 | **Take orders** | Sell at the counter; turn a customer's WhatsApp message into an order | Sell (9), customer order from WhatsApp (4) |
| 4 | **Get paid** | Invoice, match a transfer, handle a bill | Invoices and payments (11), bills (3) |
| 5 | **Deliver** | Send with a rider, see where it is | Riders and deliveries (8) |
| 6 | **Your team** | Add staff, give tasks, clock in, hand over a shift | Staff (5), tasks (6), handover (9) |
| 7 | **Your suppliers** | Link with a supplier, order from their price list, haggle a deal | Link and price list (1), order in chat (2), deals (14) |
| 8 | **Sell to businesses** | Share a price list with trade buyers, take their offers | Deals (14), price lists |
| 9 | **Know your numbers** | The morning briefing, "how did we do" | Morning briefing (7) |
| 10 | **Keep it safe** | Approvals for money going out, the app lock | Approvals (12), app lock |

Episode 0 (the overview, section 2a below) comes before chapter 1. Not every series needs all ten (a tailor has little use for chapter 5 on some
days; a gadget shop leans on 7 and 8). Each series says which it shortens.

### 2a. Episode 0: the whole picture

Every series opens with **Episode 0**, a 2 to 3 minute film that shows the
business's whole journey from beginning to end. A viewer who watches only this
should understand how Taskiem helps a business like theirs. The other episodes
then teach them how to do each thing Episode 0 showed.

- **One beat per chapter.** Episode 0 has a short beat (10 to 15 seconds) for
  each of the ten chapters, in order, in the series' own story and world. The
  chapter beats are the same moments that Episodes 1 to 10 teach in full.
- **A visible map.** Each beat carries a small chapter tag ("Episode 3: Take
  orders") so the viewer knows where to learn it.
- **One continuous story.** A single day or week in the business, not ten
  unrelated demos (the chat explainer is the model: one owner, one working day).
- **It ends on the map.** A closing card lists the episodes, and leads into
  Episode 1.
- **Mix.** Character cutaways (Flow) and real app recordings, as in the chat
  film. Narrated. Works muted.
- Episode 0 is made **last**, once the chapters exist, so its beats can reuse
  their footage.

### 2b. Continuity between episodes

Every episode, 0 to 10, is built to hand over to the next:

| Part | Length | What it does |
| --- | --- | --- |
| **Opening recap** | 3 to 5 s | One line and one frame of where the last episode ended ("Zainab has her dresses in. Now, her first order.") |
| **The episode** | 45 to 90 s | One chapter, one goal, taught step by step |
| **The bridge** | 5 to 8 s | A problem or question that the next episode answers ("But how does she get paid?"), shown on a character beat, then a **Next card**: "Episode 4: Get paid" with a short preview of what it shows |
| **End card** | 3 s | The series name, the episode number and the next episode's title |

Rules:
- The bridge always names the next episode's *problem*, not its feature. The
  viewer should want to see the answer.
- The last episode ends on a call to action (what to do now), not a next card.
- The recap line and the bridge line are in the narration script, so the
  voice carries the story across episodes.
- Episode numbers and titles are fixed in the series outline, so every card is
  consistent.

## 3. What differs, series by series

### Fashion (first)
- **Business:** Zuri Atelier, Surulere. Made-to-measure clothing, ankara and
  lace, uniforms. Already in the demo world (owner Zainab Yusuf).
- **Hero features:** products with **sizes and colours** (each with its own
  stock), photos taken from the phone, categories (Dresses, Bags),
  bookings for fittings, price lists for resellers and uniform clients.
- **Story:** a customer messages for an ankara dress in a size and colour;
  Zainab books a fitting, takes the order, delivers with a rider, gets paid.
- **Chapter 2** becomes "sizes and colours": one dress, a grid of sizes, a
  colour in each.

### Food and drinks (second)
- **Business:** Ada's Kitchen, Yaba (our existing cast and demo world).
- **Hero features:** the morning briefing, low stock and reorder from the rice
  mill, party-pack deals, staff and shift handover, riders with cool boxes.
- **Story:** one working day, which is the chat explainer's story told in
  full detail.

### Phones and gadgets (third)
- **Business:** a new demo business, a gadget shop (for example "Ikeja Tech
  Hub"). Needs a seed (see section 5).
- **Hero features:** products with **storage and colour** variants, price
  lists for dealers and resellers, offers and counters, credit limits for
  trade buyers, approvals on large supplier payments.
- **Story:** a dealer wants 20 phones; the shop owner haggles, ships, invoices.
- **To check before filming:** which gadget-specific things the app really
  does (serial numbers or IMEI, warranty, instalment sales). I have not
  confirmed these exist. We only show what the app does today.

### Schools (fourth, different spine)
Schools is a separate area of the app (fees, student account numbers, term
invoices, debtors, clearance and a gate screen), mostly on the web's admin
pages, and a school is not a shop. It follows the same Episode 0 and
continuity pattern with its own ten chapters. Outline in
[`schools/README.md`](schools/README.md).

### Later series
In rough order of reach and fit: Beauty and personal care; Building
materials (sizes and colours, lots of trade); Home and furniture; Farm
produce; Health and pharmacy; Cars and spare parts; Wholesale; Services
(bookings lead); Delivery and logistics; Electrical and electronics.

## 4. Characters per series

The recurring cast from `characters/bible.md` stays: **Emeka** (rider) appears
in every series; **Ibinabo** (buyer) in series that sell to businesses.
Each series adds three or four people:

| Series | Owner | Helper | Supplier | Customer |
| --- | --- | --- | --- | --- |
| Fashion | Zainab Yusuf (Zuri Atelier) | an apprentice or shop attendant | a fabric seller | an owambe customer |
| Food | Ada Obi | Tunde | Bayo Adeyemi | Chioma |
| Phones and gadgets | new | new | a distributor | a reseller |

New characters get a full entry in the bible before any Flow clip is made.

## 5. What each series needs to be made

1. **A demo world for the business.** `web/e2e/demo/seed.ts` is a food world
   with one fashion business inside it. Each series needs its own seed
   (`seed-<kind>.ts`) with that business's products (with sizes or colours),
   prices in naira, photos, suppliers, a few customers, and staff.
2. **Recording scripts** per chapter, based on the existing how-tos but set in
   that world (`video/series/<kind>/NN-*.mjs`).
3. **Narration**, one script per series, in the same voice (plus new names in
   `characters/pronunciation.md`), including each episode's recap and bridge lines.
4. **Flow clips** of the cast: 3 to 5 cutaways per episode, one clip per moment.
5. **Series kit**: title card, **"Next episode" card** (a new part of the kit: the next title and a preview clip), chapter tags for Episode 0, end card, thumbnail style,
   a colour accent per series inside the brand palette.

Folder: `video/series/<kind>/` with `README.md` (outline), `seed`, episode
scripts, narration, and `NOTES.md` (what was verified against the app).

## 6. Rules for every episode

- Show only what the app does **today** (no specs, no plans).
- Record the real app, so the look is always current (see `video/replan.md`,
  section 7).
- Chat screens are fine from the web. Pages away from the chat wait for the
  real phone app (`video/replan.md`, section 3).
- Names, prices and numbers match the series' demo world; amounts are said as
  words by the narrator.
- Every episode works muted: captions carry it.

## 7. Decisions (17 November 2026)

1. **Order of series:** Fashion, then Food, then Phones and gadgets; Schools
   proposed fourth (added 17 November, to confirm).
2. **Where viewers watch:** Instagram and TikTok, YouTube, and WhatsApp status
   and groups. So every episode is made in **two shapes**: vertical 9:16
   (Instagram, TikTok, WhatsApp; 30 to 60 s, a hook in the first 2 seconds,
   captions burned in, light file size) and horizontal 16:9 (YouTube; up to
   about 2 minutes, a title and thumbnail, chapters). Episode 0 may run longer
   on YouTube than in the vertical cut. LinkedIn is not a target for these
   series.
3. **Language:** English **and Nigerian Pidgin.** Every episode gets two
   narration tracks. The Pidgin script is written for the ear, not translated
   word for word, and **a native Pidgin speaker must check each script before
   it is recorded** (the English scripts are the source). Names and amounts
   stay as in the pronunciation file. On-screen captions stay in English
   unless we decide otherwise.
4. **New businesses and people:** invented by me and approved by you before
   anything is made. They are fictional.
5. **Call to action:** "Start free on Taskiem" (sign up on the web or app).
   **To confirm before it goes on any card:** Taskiem gives each new business
   a *trial* (`docs/billing.md`); there is no free plan that I can see, so the
   wording should say "Start your free trial" and state the length once the
   trial period is set. Signup must also be open (`TASKIEM_ALLOW_SIGNUP`). When
   the app is in the stores, add the download link.

## 8. Next

1. Draft the Fashion series outline, episode by episode, with each episode's
   recap line, bridge line and Episode 0 beat (English first, then Pidgin).
2. Draft the gadget shop and its people for your approval.
3. Write the Fashion demo world (`seed-fashion.ts`) so recordings show a
   fashion business with sizes, colours and fittings.
