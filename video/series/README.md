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

## 2. The spine: ten chapters

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

Not every series needs all ten (a tailor has little use for chapter 5 on some
days; a gadget shop leans on 7 and 8). Each series says which it shortens.

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
   `characters/pronunciation.md`).
4. **Flow clips** of the cast: 3 to 5 cutaways per episode, one clip per moment.
5. **Series kit**: title card, "next episode" card, end card, thumbnail style,
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

## 7. Open questions

1. **Order of series.** I propose Fashion, Food, Phones and gadgets. Right?
2. **Where will viewers watch?** (Instagram, TikTok, YouTube, WhatsApp
   status, LinkedIn.) It sets the length and the opening hook of each episode.
3. **Language.** English only, or also Nigerian Pidgin and Yoruba narration for
   some series?
4. **New businesses to invent**: should I name the gadget shop and its people,
   or do you have real businesses in mind?
5. **Who is the viewer's first step**: "start free", "book a demo", or
   "download the app" (not possible until the stores)?
