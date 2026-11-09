# Chat: dispatch riders as participants

Status: **plan**, for review (8 November 2026); **phase 1 built** (9 November 2026, §As built). Written with the founder. Decision: [0065](../decisions/0065-riders-are-organisations.md) (proposed). Amends [dispatch.md](dispatch.md) (D1 to D6, built) and builds on [chat tools](chat-tools.md), [business links](business-links.md) ([0061](../decisions/0061-business-links.md)), [fronts: services](fronts.md), [paying people](chat-pay-people.md) (plan) and [the five flows](chat-five-more-flows.md) (plan).

## Summary

The chat has two participants today: a business and the businesses it trades with. The third participant in most trades is the person who carries the goods, and that person has no place in Taskiem: a rider is a row inside one business, a sealed name and phone, reached on WhatsApp with signed buttons, who answers an offer and taps three steps and nothing else. They cannot see their deliveries across the businesses they work for, say once that they are off, hold their own bank, or be found by a business that needs them.

The first draft of this plan gave the rider a sign-up of their own, a kind, a profile, a link role, tools, roles, mirror tables and a module. Every one of those was a business concept with "rider" written on it, a seam to keep in step forever. The plan now says what a rider is in Taskiem's own terms, and almost nothing new is needed:

**A rider is a service business whose service is delivery. A delivery is an order for that service.**

- A rider **signs up as any business does** and answers "I deliver for businesses" to the one question the sign-up already asks. They have the business profile, a Taskiem conversation, chat tools, and a service, "Delivery", on their services table with their price and areas.
- A business **finds a rider in Explore** by searching for delivery, and **adds them by linking**, as it links with any supplier. The rider shares a price list with that business, which is how a supplier gives one buyer their price.
- A delivery is **a link order the business places with the rider**, of kind `delivery`: it carries the pickup, the drop, the fee and the customer's details while held, and it is one card in the conversation between them with the steps the order card already has, plus Arrived.
- The rider's tools are **the seller's tools that exist**: Orders (their deliveries), Invoices and Bills (their earnings and the cash they owe), the bank on their business, Linked businesses. Their alerts are a seller's alerts. Their briefing is a seller's briefing in their numbers.
- The rider is **paid per delivery**, as dispatch riders are: the business confirms a delivery and the fee goes to the bank on the rider's business through the payout path that exists, paid once, one card on both sides; cash held at the door nets the fee; a fee handed over in cash is recorded as paid.
- A dispatch company is **a service business with staff** who hold `order.handle` and work the orders, which is what that permission is for.

Three things are rider-specific and stay: the content of a delivery order (pickup, drop, Arrived, the proof photo, the door payment tied to the business's own storefront order), availability (a supplier who is on, off or busy, offered to first when free), and the offer with a window (an order placed to several suppliers in turn, the first to accept holding it). Each is a small addition to what exists, not a module.

Everything a rider does today on WhatsApp or Telegram keeps working, word for word and button for button. A rider with no smartphone and no sign-up works as before, exactly as a supplier not on Taskiem receives a purchase order by message today.

## What exists today

| Part | What it does today | What this plan does with it |
| --- | --- | --- |
| `riders`, `rider_presence`, `deliveries` (dispatch, D1 to D5) | A rider as a contact of one business; presence by phone hash; a delivery as the business's row driven by signed buttons; door payment; cash and remittances; fees and payouts | Stays as the path for a rider not on Taskiem. For a linked rider, the delivery is a link order instead (§ The delivery), and the rest of dispatch reads it |
| Sign-up `kinds` (shop, service, school, workflows) | One question, four answers | A fifth answer, "I deliver for businesses", which is kind `service` with the Delivery service made |
| Business profile and Explore | Name, tagline, area, about, logo, gallery; found by name, kind, area and what is sold | Unchanged. A rider is found by their Delivery service's words and area |
| Services (the fronts' services table, a known table) | A service with a price and a duration, shown on the storefront, tagged in chat | The rider's service: "Delivery", with their areas and vehicle as its words, their price |
| Business links (0061): roles supplier, customer, both; price lists; link orders with steps; conversations; paid once matched twice | Trade between two businesses | The rider is a supplier; the delivery is a link order of a new kind; the fee is paid per delivery through the payout path dispatch has |
| Chat tools, phases 1 to 7: tools, cards, diary lines, alerts, staff roles, the briefing, deals | For businesses | Used as they are by the rider's business; the delivery card gains its fields |
| `order.handle` (phase 5) | Working the orders without the storefront's settings | A dispatch company's riders |
| Paying people (plan) | A payee of kind rider | A linked rider is a supplier with a bill; the payee kind stays for riders not on Taskiem |

## The rider's business

**Signing up.** The sign-up (web or chat) asks what the business does; **I deliver for businesses** is a new answer. It is kind `service` with one thing added: a service named Delivery on the business's services table, with the areas and vehicle typed in the chat that follows ("Where do you deliver?", "Bike, car, van?") and a price per delivery. The chat tools switch is on from the start, as the chat is the rider's app. The phone is proven, as the chat sign-up proves it, because every business that has the rider today knows them by it. Nothing else is different from any other service business.

**The profile** is the business profile. The rider's photo is the logo, their areas the area field, their vehicle and hours in the tagline or the service's description. Explore shows the card as it shows any business's.

**Found in Explore.** A business searching "delivery", "rider", "dispatch" or an area finds businesses whose services say so, as a search for "catering" finds caterers today. The result card shows the Delivery service with its price, the areas, and availability (§ Availability) as "free now", "busy" or "off". No separate list; a chip **Delivery** at the top of Explore filters to them, the way the categories plan filters products.

## The link

**Adding a rider is linking with them**, role supplier, the three ways business links already has: Connect in Explore, the picker or the Chat page's search; `add rider Musa 0803…` by phone, which sends a link request to the business behind that number if there is one and otherwise adds a legacy rider exactly as today, the invitation now saying "On Taskiem? Link instead"; and a request from the rider's side.

**The price.** The rider shares a price list with the business, as suppliers do, with the Delivery service on it at the price for that business, and per area where it differs ("Yaba ₦1,500, Lekki ₦2,500" as separate lines, or quantity breaks as the deals work added). The business's per-link settings that exist apply: accept their orders automatically or ask, pay their bills automatically up to an amount, tell me when.

**Claiming.** A business's legacy rider whose proven phone belongs to a business on Taskiem with a Delivery service is offered to both sides as a link, from the Riders tool and from the rider's chat; on acceptance the `riders` row gains the link and is read-only, past deliveries stay the business's record and show in the conversation as cards from before the link. A legacy rider who never signs up keeps working as today.

## The delivery

A delivery for a linked rider is **a link order the business places with the rider**, kind `delivery`, the way a purchase order placed with a linked supplier becomes an order in the supplier's business. The business is the buyer; the rider is the seller; both sides have the order under their own tenancy and the conversation shows one card.

**What the order carries**, beyond an order's lines (one line: the Delivery service, the fee): the storefront order it is for (its reference, the business's side only), the pickup (the business's address and directions link), the drop's area, and, sealed and present only from Accepted to Delivered, the customer's name and phone, the address, the landmark and the pin; whether a proof photo is required; whether the customer pays at the door.

**The steps** are the order card's, with Arrived added for this kind: Placed, Accepted, Picked up, Arrived, Delivered. The rider moves them from the card, from the Orders tool, or with the WhatsApp and Telegram buttons and words that exist (the `dl1.` payloads become this order's steps). Delivered with the photo when required; the photo is stored on both sides as link attachments are. Each step tells the customer as today and is a diary line on both sides.

**The offer.** The business's order workflow places the delivery order as the dispatch module offers today: by hand to one rider, or by rotation among its linked riders who are free in the area, one at a time, each with the offer window; the first to accept holds it and the order to any other is withdrawn. On the order this is a `proposed` order with an expiry, which link orders already have as a status; a rider's Can't declines it. A business with no linked rider free falls back to its legacy riders, then to the owner's "Who takes it?".

**The door, cash and proof** are unchanged: the one-time account number is issued on Arrived against the business's storefront order as today, Paid reaches the rider's card, Cash received records the cash and the rider's debt (§ Getting paid). Quiet riders raise the alerts dispatch has, now reading the order's step times.

**Dispatch keeps its record.** The business's `deliveries` row is the record of every delivery, linked rider or not: the hold, the window, the door, cash, proof and the quiet-rider sweep all hang off it, as built. What changes for a linked rider is how they receive and answer it: the order in their own business, mirrored from the delivery and back, step for step. Nothing is rewritten for a rider not on Taskiem.

## The rider's tools

The rider's business has chat tools on, so it has the seller's tools. They show in the rider's shape because of what the business is, not because of a role:

| Tool | For a rider |
| --- | --- |
| **Orders** | Their deliveries: offered (with the window counting down), held in step order, done; each the delivery card with Accept, Can't, Picked up, Arrived, Delivered. In a business's conversation, only that business's. "Deliveries" is the tile's name when the business's only service is Delivery |
| **Earnings** | What they have earned: each confirmed delivery's fee as it is paid (§ Getting paid), in their money log, and what is still waiting on a business's Confirm |
| **Bills** | Cash they hold for a business, as that business's bill to them, paid from the app or by transfer |
| **Availability** | On, off, busy till, areas today. New, small; writes presence (§ Availability) |
| **Linked businesses** | As any business's: who they deliver for, requests, Explore |

**Alerts** are a seller's: a new order (the offer, with Accept and Can't and the countdown), a bill arrived (cash to remit), a payment landed (a payout), a link request, a rating. **The briefing** is a seller's with their numbers: deliveries yesterday against last week, what they are owed by whom, cash held. **"How did I do?"** answers as "how did we do" does.

**Permissions.** A rider alone is the owner of their business and holds everything. A dispatch company's riders hold `order.handle`, which lets them see, accept and move orders and nothing else, exactly as a cashier works a shop's orders today.

## Availability

A supplier of a time-bound service is free, busy or off. Dispatch has this as `rider_presence` by the rider's phone, written by the words `on`, `off`, `busy till 3` texted to a business's WhatsApp number, and read by rotation (free riders first, off ones skipped) through a definer that answers a state and never a business or an order. Busy from held orders is inferred, across every business the rider delivers for.

Phase 3 makes it the rider's business's **availability** without a second store: the rider's business writes the same row, for the rider rows that are linked to it (`riders.link_id`, phase 1), from an **Availability** tool in its own chat (On, Busy till a time, Off) or the words that exist, and reads it back the same way. A business that uses the rider sees it as today, in its riders list and the offer; Explore's card shows it ("Free now", "Busy", "Off today") for a business that delivers, from the same definer, so a business learns a state and nothing else. The areas on today, and a dispatch company's per-member state, are left for later phases: the profile's delivery areas and the business's rider zones already say where, and a company needs to exist first (phase 5).

## Getting paid

Riders are paid **per delivery**, not by the week: that is how dispatch riders work, and the fee is already on each delivery (dispatch D5). Nothing new moves money. The paths are the payout that exists, the business's approval policy and the money log; Taskiem holds nothing.

**The fee, per delivery.** A delivery's fee is due when the rider taps Delivered and becomes **payable when the business confirms** it: a Confirm on the business's card (and in Orders and the daily close), the same place the owner sees the proof photo. Confirming makes a payout of that one delivery and pays it through the path that exists (`pay_bank` to the bank on the rider, under the business's approval policy, keyed by the payout so it is paid once), so the owner's Confirm is also the approval where the policy allows, and the approval card where it does not. A business that does not confirm within a day is reminded; a delivery the business disputes is released back with a word, and nothing is paid. For a linked rider the payment lands as a **paid card** in the shared conversation and in the rider's money log as income against their delivery order; a rider with no business is paid as today, from a payout of one.

**Cash at the door nets the fee.** When the rider took cash for the order, the fee is taken out of the cash: the remittance due shrinks by the fee the moment the delivery is confirmed, and the rider remits the rest as today. A rider whose cash covers the fee is paid nothing and owes less; nothing crosses the bank twice.

**Paid in cash by hand.** A business that hands the rider their fee in cash marks the delivery **Paid in cash** instead of Confirm: the payout is recorded as paid with no transfer, the money log has a cash payment out against it, and the rider's card and log show it as paid in cash. The rider can say otherwise from their side, which raises it with the owner.

**What this replaces.** The weekly invoice from the rider, `pay-riders-weekly`, and the cash invoice to the rider are not built: a payout per delivery and the netting at the door cover them. A business that prefers to pay several deliveries at once confirms them together, and the payouts go out as one approval. A bill across the link can come later if a real business asks for one.

## A dispatch company

A company with ten riders is a service business with staff: the riders are members with `order.handle`. It links with businesses as one rider does and shares its price list. A business's delivery order reaches the company, and the company's own order workflow offers it to its riders by the rotation dispatch has, inside the company; the business and the customer see the company accept and the rider's steps ("on its way with Musa from Swift Dispatch"). The company is paid per delivery as one rider is, and pays its own riders per delivery the same way, by confirming its own delivery; each rider's Orders shows their own, the owner's shows all. This is the fleet connector the dispatch plan deferred, without a connector.

## Data model, engine and API

Under the core rule (the core cleanup plan, PR 172), nothing here adds a business-record table to the core, because every record is one that exists.

| Changed | What |
| --- | --- |
| Sign-up | `kinds` accepts `delivery`, which is `service` plus the Delivery service made and chat tools on |
| Services table (known table) | A service may carry `areas` (labels) and `vehicle`; shown in Explore |
| `storefront_orders` / link orders | `kind` gains `delivery`; delivery fields on the order: `for_order` (the business's storefront order), `pickup`, `drop_area`, the sealed customer details while held, `proof_required`, `door_payment`; `arrived_at` as a step; `expires_at` for a proposed order, which exists |
| `riders` | `link_id`, set on claiming; a row with a link is read-only and the legacy path does not use it |
| `rider_presence` | Keyed by the business (tenant) as well as the phone hash; written by the Availability tool; the definer unchanged |
| `price_list_items` | Unchanged: the Delivery service on a list, per area as lines |
| Payouts (dispatch D5) | A payout of one delivery, made on the business's Confirm (or recorded as paid in cash), through `pay_bank` and the approval policy as today |

**Engine.** `engine/dispatch` learns to place a link order instead of a `deliveries` row when the rider is linked, keeps rotation, the window, the door, cash, quiet riders and the customer's messages, and makes a payout of one delivery on Confirm, netting the door's cash. `engine/links` learns the `delivery` kind's fields and the Arrived step on an order, and the paid card. Explore indexes services' areas and availability. No new module.

**API.** Nothing rider-named. `/v1/trade-orders` and the link order routes take kind `delivery` and its fields and the `arrived` step; `/v1/availability` (the business's on, off, busy, areas); `/v1/explore?service=delivery`; `/v1/riders` lists linked suppliers with a Delivery service beside legacy riders; `/v1/links` unchanged.

## WhatsApp, Telegram and Slack

Every word and button a rider uses today stays and acts on the delivery order. A signed-in rider has, as any business owner has, "orders", "money", "tools", "how did we do", and "on", "off", "busy till 3" as today. "I'm a rider" from someone not on Taskiem starts the chat sign-up with the delivery answer. An offer on WhatsApp is the link order's message with Accept and Can't, as a supplier's order arrives today.

## Safety and privacy

- **A rider sees a customer's details only while holding the order**: the fields are sealed on the order and cleared on Delivered on the rider's side; the business's copy keeps them under its retention.
- **A business sees of a rider what any linked supplier shows**: the profile, the service, the price list shared with it, availability as a state, and the orders between them. Never another business's orders.
- **Money moves only through bill pay and matching**, under the business's approval policy, to the bank on the rider's business; the first-payment warning holds; Taskiem holds nothing.
- **Explore shows nothing a business did not turn on**; a phone finds nobody.
- **No new way across tenants**: the link definers that exist carry the order and the paid card; the payout path carries the payment; the isolation test's list is unchanged.
- **Plain words:** "Rider", "Deliveries", "Available", "Earned", "Cash you hold"; never "fleet", "dispatcher", "payout" or "tenant" on a rider's screen.

## Phases

Five phases, each a PR that ships on its own. The delivery order first, because it is the one real change and everything else is wiring.

1. **The delivery as a link order.** Kind `delivery` with its fields and Arrived; dispatch placing it for a linked supplier with a Delivery service, by hand and by rotation with the window; the card on both sides; the steps from the card, the tool and the WhatsApp buttons; the door payment, cash and quiet riders reading the order; the legacy path untouched.
   - Done when: a business linked with a service business that sells Delivery offers an order to it, the rider accepts from the card, runs it to Delivered with a photo, the customer is told at each step, and both conversations show the same card.
2. **The rider's sign-up and Explore.** The "I deliver for businesses" answer on the web and in chat; the Delivery service with areas and vehicle; the Delivery chip and availability in Explore; `add rider` by phone finding a business; the claim of a legacy rider.
   - Done when: a rider signs up from WhatsApp in under three minutes, is found by a business searching "delivery Yaba", and a business's legacy rider becomes a link in two taps with history kept.
3. **Availability.** The tool and the words writing the business's availability; rotation reading it; Explore showing it; a dispatch company's per-member state.
   - Done when: a rider who says "off" from the tool is offered nothing by two businesses until they say "on", and Explore shows them as off.
4. **Getting paid, per delivery.** Confirm and Paid in cash on the business's side; the payout of one delivery through the payout path and the approval policy; the netting of cash at the door; the paid card and the rider's money log; the reminder to confirm; the rider's "how did I do".
   - Done when: a rider taps Delivered, the business confirms, the fee reaches the bank on the rider's business under the business's approval policy with one card on both sides, cash the rider took is netted against the fee, and a fee handed over in cash is recorded as paid with no transfer.
5. **The dispatch company.** Staff with `order.handle` working delivery orders; the company's own rotation inside itself; the company's and each rider's view.
   - Done when: a business offers an order to a company, the company's rotation gives it to a member, the business and the customer see that member's steps, and the company's owner sees the week's orders and money.

## Tests and checks

- **Go:** a delivery order placed, offered in turn with the window and held by the first to accept; the steps accepted only from the rider's side and only for their own order; customer details present only while held; the door payment and cash on the storefront order as before; the legacy path unchanged by every test dispatch has; the claim; availability read as a state only; a payout of one delivery on Confirm, paid once; cash netted against the fee; paid in cash recorded with no transfer; `order.handle` in a company; the isolation test's definer list unchanged.
- **Browser (e2e):** at 390 and 360 px in both themes with axe: the rider's chat sign-up; Explore's Delivery chip and Connect; the delivery card through the steps with a photo on a phone; Availability; the invoice sent and the bill paid; the company's dispatch.
- **WhatsApp:** the fake Graph API the e2e tests run: an offer reaching the rider on WhatsApp and the card at once, answered on either.
- **Plain words:** every new rider-facing line through `src/lib/plainwords.test.ts`.
- **The recording:** a new act in `video/recording/`: a rider found in Explore, linked, a delivery from offer to delivered on their phone, paid on Friday.

## Open questions

The plan assumes the answer in brackets.

- [ ] Should every service business be able to receive orders with a window and rotation, or only those with a Delivery service? (Any time-bound service; Delivery is the first.)
- [ ] The weekly invoice: drafted by Taskiem and sent by the rider, or sent by Taskiem on the rider's day? (Drafted, sent by the rider; a setting to send on its own.)
- [ ] The cash invoice: one per cash delivery, or one per day? (Per day, at the business's close.)
- [ ] A rider's rating across businesses on the Explore card: from the first, or after five? (After five.)
- [ ] A rider's areas: the business's zone labels, or free text? (Labels, with free text matched by name.)
- [ ] Should a rider see the customer's phone, or call through Taskiem? (The phone, while holding, as today.)
- [ ] A rider with no bank connected paying a cash bill from the app: needs decision 0052's wallet for individuals. (Phase 4 uses transfer or hand-in until then.)

## As built (phase 1, 9 November 2026)

- **The delivery is a trade order with delivery fields**, not a kind of its own: the rider's order is a storefront order of kind `trade` in their business, as a linked supplier's order is, and `link_orders` carries the delivery (`delivery_id`, the business's delivery; `pickup`, `pickup_link`, `drop_area`, `proof_required`, `door_payment`, `expires_at`, `picked_up_at`, `arrived_at`; migration 00408). The API shows it as `delivery` on the order, with the customer (name, phone, address, landmark, pin) only while the rider holds it (accepted to arrived); the details are cleared from the rider's side once it is delivered, declined or cancelled, the name kept. The storefront status `arrived` is the order's "At the door".
- **The business's `deliveries` row stays the record**, with `link_order_id` pointing at the rider's order; `riders.link_id` says which of the business's active links the rider is (`PATCH /v1/riders/{id}` with `link_id`, or `""` to take it off). The claim of a legacy rider is phase 2.
- **Placing and mirroring** (`engine/dispatch/linked.go`, `engine/links/deliveries.go`): `offerTx` writes the business's card and then, through `taskiem_link_enter` (`link.delivery.*`, gated on the Links area, so a rider's business needs nothing else turned on), the rider's order and card, and returns the scope to the business (`links.ReturnTx`, which only ever restores the caller's own side). Every step through `press`, from the rider's buttons and words, is mirrored the same way; a step from the rider's own business (`Dispatch.RiderStep`) enters the business's side, takes the step as the button would, returns, and writes the rider's side. Withdrawn, passed and released offers cancel the rider's order; Can't declines it. A rider whose business has its links off, or whose link ended, is offered by chat alone and the delivery goes on.
- **The rider's steps** are `POST /v1/trade-orders/{id}/{accept|cant|decline|picked_up|arrived|delivered}` on their order, `delivered` as a multipart form with a `photo` when the business asks for proof, kept in the business's storage as a photo from WhatsApp is. The chat app's offer and buttons go out as before, so a rider on the road has both.
- **Cards**: the business's side names the document `dispatch`, the rider's `delivery`, one pair per step, with the steps Offered, Accepted, Picked up, Arrived, Delivered on both; the rider's own steps are diary lines on their side. The web's card shows the five steps and, on the rider's side, the next step as a button; the Orders tool opens a delivery as its own view (where to pick up, where it goes, the customer while held, the fee, Can't and the next step, the photo on Delivered).
- **Not yet**: the Orders tile is still named Orders for a rider (the rename to Deliveries comes with the sign-up answer in phase 2); the `/v1/trade-orders` routes sit under the Orders area, which a rider's business of kind `service` has off, so phase 2's sign-up turns it on for them; the alert's countdown and the business-side Release button on the card.
- **Tests**: `engine/dispatch/linked_db_test.go` (offer to delivered through both paths, the customer held and cleared, the window, Can't, release, a legacy rider untouched); `api/link_deliveries_test.go` (the same over the API with the WhatsApp button and the photo, both conversations' cards); `web/src/lib/cardframe.test.ts`.

## As built (phase 2, 9 November 2026)

- **The sign-up answer.** `delivery` is a kind beside shop, service and school (`areas.Kinds`: "I deliver for businesses"), on the web's sign-up and as choice 8 of the chat sign-up's "What do you sell or do?" in every language. It turns on Orders and Links (the rider's deliveries are its orders), turns the chat tools on from the start, and marks the profile as one that delivers. Nothing else is special about the business.
- **The profile says it delivers**, not a service on a services table: the plan's Delivery service needs a storefront, which a rider has no use for, so `business_profile` carries `delivers`, `vehicle` and `delivery_areas` (migration 00409), set from the profile page ("I deliver for other businesses", what with, which areas) and read by Explore. The price per delivery stays the business's fee on the rider (`riders.fee_kobo`), as in phase 1; a shared price list is for a later phase.
- **Explore** has a Delivery section (`GET /v1/explore?section=delivery`, those covering the business's own area first, by name), and a search for delivery words (deliver, dispatch, rider, courier, logistics, okada, bike) finds the businesses that deliver whatever their name says. The card shows "Delivers by bike · Lekki, Ajah". Availability in Explore is phase 3.
- **Add a rider by phone links the business.** `POST /v1/riders` (and `add rider` in WhatsApp) with a number that is another business's owner's WhatsApp, or its WhatsApp number, sends that business a link request as a supplier, the blind request a phone search sends (nothing of who is behind the number is told until it answers; `link_requested` in the answer). One already linked with is named on the rider at once. The checks are two narrow definers, whether the number is a business and our link with it, not the business's identity.
- **The claim of a legacy rider.** `Riders.ClaimLinkedTx` names, on each current rider with no business yet, the active link with the business their number belongs to: run when a rider is added and on both sides when a link is accepted. History is kept: the rider row is the same, its link filled in; the rider's past deliveries stay theirs.
- **Small things from phase 1's "not yet"**: the Orders tile is named Deliveries for a business whose orders are all deliveries; the new-order alert for a delivery reads "Delivery P-… for Ada Stores to Lekki · ₦1,000 · answer within 4 min".
- **Not yet**: availability (phase 3); the rider's own price list; the Release button on the business's card.
- **Tests**: `api/areas_test.go` (the kind), `api/explore_test.go` (`TestExploreDelivery`), `api/link_deliveries_test.go` (`TestRiderByPhoneLinksBusiness`), the chat sign-up test's choices, `engine/db` migration up/down.

## As built (phase 3, 9 November 2026)

- **One store, the rider's business writes it.** Availability stays `rider_presence` by the rider's phone. The rider's business writes and reads it for the rider rows linked to it (`riders.link_id`), through three definers (migration 00410): say on, off or busy till a time for every business that uses it; read its own state with how many businesses that is; and, for Explore, a findable or linked delivery business's state as `free`, `busy` or `off`, nothing when no business uses it as a rider yet. A business's riders list and rotation read presence exactly as before, so a rider with and without a business are one path.
- **The Availability tool** (`GET`/`PUT /v1/availability`) is in the chat tools of a business whose profile says it delivers (`delivers` on `/v1/me`): Free, Busy (with a time today, else the rest of the day) and Off, the tile reading "Free", "Busy till 3:30pm" or "Off today", a warning badge when not free. A business no one has added as a rider yet is told so and refused the change until then (409). A held delivery is busy whatever was said, as today.
- **The words** `on`, `off`, `free`, `busy till 3` from the rider's WhatsApp already reached presence by the rider's phone, which is the rider business's owner's number, so nothing was added for them; Telegram's words go by the contact, as before.
- **Explore** shows "Free now", "Busy" or "Off today" on a delivery business's card; the riders list line and the offer's choice of rider are unchanged.
- **Left for later**: the areas on today (the profile's delivery areas and the business's rider zones already say where); a dispatch company's per-member state (phase 5); words in the rider's own web conversation (the tool does it there).
- **Tests**: `api/link_deliveries_test.go` `TestRiderAvailability` (nothing to say before a link; off, busy till a time, free; the card and the rider's presence; no rider offered while off; busy while holding); `engine/db` migration up/down/up; the web's tools and plain-words tests.

## As built (phase 4, 9 November 2026)

- **Paid per delivery, on the business's Confirm.** `POST /v1/storefront/orders/{reference}/confirm` (the Confirm button on the business's card, the Confirm card among the chat's alerts, and `confirm REF` in WhatsApp) marks the delivery confirmed (`deliveries.confirmed_at`, migration 00411) and makes a payout of that one delivery (`rider_payouts.delivery_id`, `how`), bound to the delivery so a weekly batch never pays it again. A payment by bank starts the pay-riders run for that one-payout batch, so the approval policy, the bank's name check, `pay_bank` keyed by the payout and the money log are all the ones that exist; `Riders > Pay` must be set up first, and a rider needs a bank account, else the confirm is refused in plain words (in cash still works). When the run records the payout paid, the fee's **paid card** goes to both sides and the fee is income in the rider's money log, against their delivery order.
- **Cash at the door nets the fee** as a batch nets it: what the rider owes, up to the fee, comes off at the confirm (a `netted` remittance part), and a fee the cash covers is settled with nothing to pay and the paid card at once.
- **Paid in cash by hand** (`how: cash`, "Paid in cash" on the card, `confirm REF cash`) records the payout paid with no transfer and a cash payment out in the business's money log; the rider's card and log say cash. Nothing is netted: the whole fee changed hands, and what the rider owes from the door stands.
- **The reminder** is the chat's Confirm card for every delivered delivery not yet confirmed (up to 14 days), with the fee and the rider's name; the daily close's rider lines are unchanged.
- **When the payout does not go out** (the run failed or was not approved, the owner marked it not paid, or the run could not start), both sides' cards say "Fee not paid", the Confirm card comes back ("confirm again to pay it"), and the next Confirm makes a new payout; the run is recorded on the payout so Not paid waits for it to end and nothing is paid twice. The order's page on the web has Confirm and Paid in cash beside Take it back, and the delivery's steps show how the fee stands.
- **Each confirm's payout is approved under the pay-riders workflow's approval step**, as the weekly batch is: one Approve per delivery today. Making the owner's Confirm count as that approval needs the workflow's approval step to be conditional, which is a change to the workflow runtime's handling of a skipped step and is left for its own change.
- **Left for later**: the approval above; a disputed delivery released with a word (today the owner simply does not confirm); the rider saying a cash payment was not received; the rider's "how did I do"; the weekly template still exists for a business that prefers to confirm nothing and pay by the week.
- **Tests**: `api/link_deliveries_test.go` `TestConfirmPaysPerDelivery` (the alert; no bank and no set-up refused; the payout and run on confirm; both sides' cards; the paid card and the rider's income when the run pays; cash netted; paid in cash with the money log entry; the WhatsApp words); `engine/db` migration up/down/up; `web/src/lib/cardframe.test.ts`.

## As built (phase 5, 9 November 2026)

- **A company is a rider business with riders of its own.** Nothing new is signed up or configured: a delivery business adds its riders as any business adds riders (the tap-in link, `add rider`), and a business links with the company and names it as its rider as with any rider business (phases 1 and 2). A dispatch company's rider need not be a member of the company's account: they work from WhatsApp as a business's rider does; one who is a member with `order.handle` also has the company's Orders tool.
- **The company's own rotation.** When a business's offer places its delivery order in the company (phase 1), dispatch offers that order on inside the company to the company's riders by the rotation it has (`companyOfferTx`, in the company's scope through the door, under a savepoint so what goes wrong inside the company never fails the business's offer), when the company has any active rider. The order inside the company is a delivery order from a business (`innerOrderTx`: a trade order with a delivery behind it), so it is offered and accepted before the company's order is accepted, and the rider's WhatsApp offer names the pickup as the business's address, from the order, not the company's storefront.
- **The rider's steps are the company's steps, and the company's steps are the rider's.** A step the company's rider takes on the inner delivery (their button or word) is taken on the business's delivery through the link as the company's own Orders tool would take it (`riderStepTx`, phase 1's `RiderStep` in the same transaction), so the business's delivery, the company's order and the customer's messages move together; the business's card names the rider ("Musa Bello (Swift Dispatch)") and the customer is told "on its way with Musa from Swift Dispatch" with the rider's own number. The other way, a step the company takes by its own hand (its card or Orders tool) moves the rider's inner delivery along and tells them, so nothing is left held. Can't inside the company goes to the next of the company's riders and leaves the business's offer standing; the customer hears from the business alone (the inner delivery tells no customer and completes no order). When the business's delivery ends (the offer withdrawn or passed, the delivery released, the company's Can't), the inner delivery is withdrawn or released too and the rider is told.
- **Paid twice over, per delivery.** The business confirms and pays the company (phase 4); the company confirms its own delivery and pays its rider the same way, by bank or in cash.
- **Left for later**: each rider member's own view in the company's Orders (today the owner's shows all; a rider works from WhatsApp); a company with no free rider telling the business so before the window runs out (today the business's offer runs out as any does); the company's price list for a business.
- **Tests**: `api/link_deliveries_test.go` `TestDispatchCompany` (the company's order and its own offer with the business's pickup, the rider's steps moving both sides with the rider named, the company confirming its own delivery; Can't inside the company with the business's offer standing; the business taking the order back while the rider holds it; the company's owner moving the order by hand with the rider's delivery following).
- **A note on the platform's message limits**: the sender caps messages to one recipient across tenants (five an hour, twenty a day by default); a rider gets about five per delivery, so a deployment with riders raises those limits (the test does).
