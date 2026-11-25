# Filming the Phones and gadgets series (Techline Gadgets)

Read `video/series/FILM-GUIDE.md` first (environment, the Film API, the loop, the rules, and
**the founder's rule: every step must be reproducible by a viewer in the real app**), then
`video/series/gadgets/README.md` (the story and episodes), `video/series/food/FILMING-NOTES.md`
and `video/series/food/FIDELITY.md` (this series is the Food series retold for a phone shop:
the same flows, a new story) and `mobile/CLAUDE.md`.

Each Gadgets film is an **adaptation of a Food film** that already exists
(`mobile/test/film_food_*_test.dart`, with the shared worlds `film_ada_ep23_world.dart` and
`film_ada_ep45_world.dart`): copy it, keep its flow and structure, and change the story to
Techline Gadgets. Use the app fixtures' own names where the screen needs them and rename only
what the film shows. Where a Food film had stand-ins, say the same honestly.

## Rules
- Films are named `gadgets-<name>` (frames in `<out>/gadgets-<name>/`), tests
  `mobile/test/film_gadgets_<name>_test.dart`. Shared helper files need **unique names**
  (`film_tech_ep23_world.dart`, with your episode numbers). Do not edit existing films, tests or
  `mobile/lib`; import or copy.
- **Reproducible by the viewer** (the founder's rule): real route to every screen, real taps, no
  stand-in-only options, captions that match the frame. Cuts to a second person's phone are fine.
  Where a step in the outline does not exist in the phone app, do not fake it; film what is real
  and report it. Not in the app, so never shown or implied: IMEI/serial numbers, warranty,
  repairs, trade-ins, instalment sales. No real brands: products are invented (Nova X5, Pulse
  tablet, Volt power bank).
- Positioning: Taskiem runs the shop's work beside its chats; never say it replaces WhatsApp.
- Captions 8 to 12 words, present tense, none shorter than 2 s, first with `who:` (for example
  `Ikenna · Techline Gadgets`); the phone clock set by `--time` when composing (report it; match
  any time the app prints); `flutter analyze` clean; one real render at a time
  (`flutter test --timeout 40m`, background); compose a 9:16 preview and **look at stills**.
- Report: files, output folders (shared `<out>` is
  `/tmp/claude-0/-home-user-taskier/4a7f812e-7eae-5391-b4de-ccc94df594e3/scratchpad/films`),
  running time, captions with times, what each shows, the real route, stand-ins.

## The story and its numbers
**Techline Gadgets**, Computer Village, Ikeja, Lagos. Owner **Ikenna Nwachukwu**; sales attendant
**Maryam Sani** (a cashier in the app); distributor **Alhaji Ibrahim Gwarzo** (Gwarzo
Distributors, Kano); reseller **Kehinde Alabi** (Ibadan); rider **Emeka**; a WhatsApp customer
**Chiamaka Eze**; a company, **Okoro & Sons Ltd**.
Products (invented): **Nova X5** phone (Storage 64, 128, 256 GB by Colour Black, Blue, Gold;
the 128 GB is ₦185,000), Pulse tablet ₦120,000, Volt power bank 20,000 mAh ₦12,500, a charger ₦6,500.

## Gadgets film, the Food film to adapt, what changes
| Gadgets film | Adapt | Story |
| --- | --- | --- |
| `gadgets-open-your-shop` | `film_food_open_your_shop_test.dart` | Ikenna signs up with WhatsApp; business "Techline Gadgets"; name "Ikenna Nwachukwu"; sells: the app's own choice for phones/gadgets if the list has one, else the closest and say so; pays 1 2; "free for 14 days" |
| `gadgets-be-found` | `film_food_be_found_test.dart` | Be found in Explore; area "Computer Village, Ikeja"; category the app's closest (say which); tagline "Phones, chargers and gadgets you can trust"; Kehinde, a reseller, finds the card in Find a business |
| `gadgets-put-goods-in` | `film_food_put_goods_in_test.dart` | **Nova X5** with a photo, category Phones, price ₦185,000; "Sizes and colours" with two choices: **Storage** 64, 128, 256 GB and **Colour** Black, Blue, Gold, a count in each cell, if the app's grid takes two axes (verify and say) |
| `gadgets-sell-at-counter` | `film_food_sell_at_counter_test.dart` | Maryam sells a **Nova X5, 128 GB, Blue (₦185,000)** and a charger (₦6,500) at the counter, transfer |
| `gadgets-customer-order` | `film_food_customer_order_test.dart` | Chiamaka Eze's WhatsApp: "Good afternoon, one Nova X5, 128GB, blue, deliver to Allen Avenue, Ikeja". Order card ₦185,000, delivery ₦2,000, Confirm (₦187,000). Report whether the order reader matches the storage and colour in words |
| `gadgets-send-invoice` | `film_food_send_invoice_test.dart` | Ikenna invoices **Okoro & Sons Ltd**, "10 Pulse tablets", **₦1,200,000**, by SMS |
| `gadgets-match-payment` | `film_food_match_payment_test.dart` | The transfer **"TRF FROM OKORO AND SONS" ₦1,200,000** names no invoice; matched to their invoice |
| `gadgets-deliver` | `film_food_deliver_test.dart` | Chiamaka's order needs a rider; Ikenna assigns **Emeka**; offered, accepted, on the way, delivered; paid in cash at the door ₦187,000 |
| `gadgets-add-staff` | `film_food_add_staff_test.dart` | Ikenna adds **Maryam** by phone as a cashier |
| `gadgets-tasks-and-handover` | `film_food_tasks_and_handover_test.dart` | Task for Maryam: "Count the chargers by 2pm"; Maryam marks Done; at closing she hands over the shift, cash counted, short or over (invent shift totals, say so) |
| `gadgets-find-a-supplier` | `film_food_find_a_supplier_test.dart` | Search "phones"; **Gwarzo Distributors** (Kano) card; Connect; Requested |
| `gadgets-reorder` | `film_food_reorder_test.dart` | "**Nova X5, 128 GB, Blue**, is running low: 2 left"; Reorder 20 from Gwarzo Distributors; his price list ₦140,000 a phone; 20 phones ₦2,800,000; Place order |
| `gadgets-price-list` | `film_food_price_list_test.dart` | Price list for resellers shared with **Kehinde Alabi's business**: Nova X5 128 GB **₦185,000**; **from 10 phones ₦178,000**; lowest price **₦172,500**. End the film before any stuck "Saving…" (see Food's FIDELITY) |
| `gadgets-a-deal` | `film_food_deal_test.dart` | **Kehinde orders 20 Nova X5** (list ₦3,700,000): offer **₦3,400,000**; **Maryam counters ₦3,600,000**; Kehinde counters **₦3,450,000**, under Ikenna's lowest (₦3,500,000), so Maryam's Accept waits; Ikenna's **Needs your OK** card; Ikenna taps Agree |
| `gadgets-know-your-numbers` | `film_food_know_your_numbers_test.dart` | The 8am briefing: **yesterday's sales ₦1,284,000, up 9%**; owed ₦640,000; 3 low (Nova X5 128 GB Blue is one); 2 deliveries |
| `gadgets-approve-a-payment` | `film_food_approve_a_payment_test.dart` | **Maryam asks to pay Gwarzo Distributors ₦1,400,000**; first-payment warning; comment; Ikenna approves |
| `gadgets-app-lock` | `film_food_app_lock_test.dart` | The same flow for Ikenna Nwachukwu |
