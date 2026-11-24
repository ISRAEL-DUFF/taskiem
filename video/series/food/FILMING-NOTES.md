# Filming the Food series

Read `video/series/FILM-GUIDE.md` first (environment, the Film API, the loop, rules, how to
report) and `video/series/food/README.md` (the story and its numbers). Each Food film is an
**adaptation of a Fashion film that already exists**: copy that film's test file
(`mobile/test/film_*_test.dart`), keep its flow and structure, and change the story to Ada's
Kitchen. The Fashion films renamed the app's fixtures to Zuri Atelier; for Food, **use the
fixtures' own names wherever you can** (the app's test data is already Ada's Kitchen, Tunde,
Bayo Rice Mills, Rivers Foods), and the story numbers below.

## Rules that differ from Fashion
- Films are named `food-<name>` (frames in `<out>/food-<name>/`), tests
  `mobile/test/film_food_<name>_test.dart`.
- Shared helper files you create must have **unique names** (add your episode numbers, for
  example `film_ada_ep3_world.dart`): two helpers once made files with the same name.
- Do not copy or edit the Fashion films or the Fashion shared world files
  (`film_zuri_world.dart`, `film_zuri_shop_world.dart`); import or copy from them if useful.
- Captions: 8 to 12 words, present tense, and **no caption shorter than 2 seconds** (merge
  steps that are quick). The first caption has `who: 'Ada · Ada's Kitchen'` (or the person
  whose phone it is). Only what the app shows: say honestly what is a stand-in.
- Phone clock: set it through captions' `--time` when composing (a morning scene 08:00; the
  lunch scenes 13:00); report it.
- Run `flutter analyze` on your files and fix everything (no unused imports; add
  `// ignore_for_file: depend_on_referenced_packages` only where a test needs a transitive
  package).
- Real renders: `flutter test --timeout 40m`. Run one render at a time, in the background.

## Food film, the Fashion film to adapt, what changes

| Food film | Adapt | Story |
| --- | --- | --- |
| `food-open-your-shop` | `film_signup_test.dart` | Ada Obi signs up with her WhatsApp number; "Ada's Kitchen"; "Ada Obi"; sells 1 (Food); pays 1 2 (cash, transfer); "free for 14 days" |
| `food-be-found` | `film_befound_test.dart` | Be found in Explore; area "Yaba, Lagos"; category **Food and drinks**; tagline "Home-cooked Nigerian food"; a buyer sees the card in Find a business |
| `food-put-goods-in` | `film_put_goods_in_test.dart` | **Party pack, 10 plates**: photo (a drawn jollof picture), category Party packs, price ₦48,000; "Sizes and colours: Set up" with one choice, **Size: 5 plates, 10 plates, 20 plates** (counts in each) |
| `food-sell-at-counter` | `film_sell_at_counter_test.dart` | Tunde sells **2 jollof rice (₦3,500 each) and a zobo (₦1,500)** at the counter, pays by transfer |
| `food-customer-order` | `film_customer_order_test.dart` | Chioma Nnadi's WhatsApp: "Good afternoon, 2 jollof rice and 1 zobo, deliver to 12 Herbert Macaulay Way, Yaba". The order card (₦8,500), delivery fee ₦1,500, Confirm (₦10,000) |
| `food-send-invoice` | `film_send_invoice_test.dart` | Ada invoices **Chinedu's office**, "Office lunch, 15 packs", **₦37,500**, sent by SMS |
| `food-match-payment` | `film_match_payment_test.dart` | The transfer **"TRF FROM OKONKWO C" ₦37,500** names no invoice; matched to Chinedu's invoice |
| `food-deliver` | `film_deliver_test.dart` | Chioma's order needs a rider; Ada assigns **Emeka** (CoolBox Logistics); on the way; delivered; paid in cash at the door **₦10,000** |
| `food-add-staff` | `film_add_staff_test.dart` | Ada adds **Tunde** by phone as a cashier (or shift manager if the handover needs it: say which) |
| `food-tasks-and-handover` | `film_tasks_and_handover_test.dart` | Task for Tunde: "Restock the drinks fridge by 2pm"; Tunde marks Done; at closing Tunde hands the shift over, cash counted, short or over |
| `food-find-a-supplier` | `film_find_a_supplier_test.dart` | Search "rice"; **Bayo Rice Mills** (Mile 12 market) card; Connect; Requested |
| `food-reorder` | `film_reorder_test.dart` | "**Rice, 25kg bag**, is running low: 4 left, you reorder at 10"; **Reorder 20 from Bayo Rice Mills**; his price list ₦32,500 a bag; 20 bags ₦650,000; Place order |
| `food-price-list` | `film_price_list_test.dart` | Price list shared with **Rivers Foods Ltd**: Party pack, 10 plates **₦45,000**; **from 10 packs ₦43,000**; lowest price **₦41,500** |
| `food-a-deal` | `film_deal_test.dart` | **Rivers Foods (Ibinabo George) orders 20 party packs** (list ₦860,000): offer **₦800,000**; **Tunde counters ₦840,000**; Rivers counters **₦820,000**; under Ada's lowest (₦830,000), so Tunde's Accept waits; Ada's **Needs your OK** card ("Tunde would agree ₦820,000 with Rivers Foods Ltd"); Ada taps Agree |
| `food-know-your-numbers` | `film_numbers_test.dart` | The 8am briefing: **yesterday's sales ₦486,500, up 12%**; owed ₦212,000; 3 low; 4 deliveries; film Today and, if the app's tests have the briefing card in the Taskiem chat, that too |
| `food-approve-a-payment` | `film_approve_test.dart` | **Tunde (cashier) asks to pay Bayo Rice Mills ₦450,000**; first-payment warning; comment; Ada approves |
| `food-app-lock` | `film_lock_test.dart` | The same flow for Ada Obi |

Fashion's own films and their fidelity notes (`video/series/fashion/FIDELITY.md`) show what
is stand-in; write the same kind of honest list for yours.
