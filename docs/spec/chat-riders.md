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
- The rider is **paid as a supplier is paid**: they invoice the business for delivered orders, the business pays the bill, paid once and matched twice. Cash held at the door is a bill the other way.
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
| Business links (0061): roles supplier, customer, both; price lists; link orders with steps; conversations; paid once matched twice | Trade between two businesses | The rider is a supplier; the delivery is a link order of a new kind; the fee is a shared price list; earnings are invoices |
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
| **Invoices** | What they have earned: a weekly invoice per business drafted from delivered orders (§ Getting paid), sent with a tap; its state as it is paid |
| **Bills** | Cash they hold for a business, as that business's bill to them, paid from the app or by transfer |
| **Availability** | On, off, busy till, areas today. New, small; writes presence (§ Availability) |
| **Linked businesses** | As any business's: who they deliver for, requests, Explore |

**Alerts** are a seller's: a new order (the offer, with Accept and Can't and the countdown), a bill arrived (cash to remit), a payment landed (a payout), a link request, a rating. **The briefing** is a seller's with their numbers: deliveries yesterday against last week, what they are owed by whom, cash held. **"How did I do?"** answers as "how did we do" does.

**Permissions.** A rider alone is the owner of their business and holds everything. A dispatch company's riders hold `order.handle`, which lets them see, accept and move orders and nothing else, exactly as a cashier works a shop's orders today.

## Availability

A supplier of a time-bound service is free, busy or off. Dispatch has this as `rider_presence` by phone hash; it becomes the business's **availability**: on, off, busy till a time, and the areas on today, kept on the rider's business and written from the Availability tool or the words `on`, `off`, `busy till 3` that exist. Businesses read it through the definer that exists, which answers a state and never a business or an order: "free", "busy, 2 deliveries", "off today". Busy from held orders is inferred as today, across every business the rider delivers for. Explore shows it on the card. For a dispatch company, availability is per rider member and the company is free while any rider is.

## Getting paid

Money between a business and a rider goes two ways, and both are what business links already carry.

**Fees.** Each delivered order is a sale of the rider's Delivery service to the business. The rider **invoices** the business for them: a draft invoice per business gathers the week's delivered orders (one line per delivery, with the reference and the fee), drafted on a day the rider chooses or at any time from Invoices, sent with a tap, arriving as a **bill** in the business as any linked supplier's invoice does. The business pays it as it pays bills: `pay-riders-weekly` becomes paying the rider's bill, under the business's approval policy, to the bank on the rider's business, paid once and matched twice with the pairing token. The rider sees the payment land as a card and a diary line and in their money log as income. A business that prefers to pay per delivery pays each bill as it comes.

**Cash.** A rider who tapped Cash received owes the business. The business's remittance due becomes **the business's invoice to the rider**, drafted by Taskiem when the cash is recorded (or the day's cash in one), arriving as a bill in the rider's business: the rider pays it from the app through their own payments connection, or by transfer with the token, or hands the notes in and the business marks it paid, each closing both sides. The rider's Bills tool shows what they hold; the business's debtor book shows what each rider owes, with the leak that exists.

**Nothing new moves money.** The bank is the one on the rider's business; the paths are bill pay and matching; Taskiem holds nothing. A rider not on Taskiem is paid as today, and the paying-people plan's payee of kind rider stays for them.

## A dispatch company

A company with ten riders is a service business with staff: the riders are members with `order.handle`. It links with businesses as one rider does and shares its price list. A business's delivery order reaches the company, and the company's own order workflow offers it to its riders by the rotation dispatch has, inside the company; the business and the customer see the company accept and the rider's steps ("on its way with Musa from Swift Dispatch"). The company invoices the businesses and pays its riders as staff, through the paying-people plan's payroll or however it likes; each rider's Orders shows their own, the owner's shows all. This is the fleet connector the dispatch plan deferred, without a connector.

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
| Invoices and bills | Unchanged: an invoice drafted from delivered orders; the cash invoice drafted by dispatch |

**Engine.** `engine/dispatch` learns to place a link order instead of a `deliveries` row when the rider is linked, keeps rotation, the window, the door, cash, quiet riders and the customer's messages, and drafts the cash invoice. `engine/links` learns the `delivery` kind's fields and the Arrived step on an order. `engine/invoicing` gains the weekly draft from delivered orders (a template, `invoice-deliveries-weekly`, the rider's). Explore indexes services' areas and availability. No new module.

**API.** Nothing rider-named. `/v1/trade-orders` and the link order routes take kind `delivery` and its fields and the `arrived` step; `/v1/availability` (the business's on, off, busy, areas); `/v1/explore?service=delivery`; `/v1/riders` lists linked suppliers with a Delivery service beside legacy riders; `/v1/links` unchanged.

## WhatsApp, Telegram and Slack

Every word and button a rider uses today stays and acts on the delivery order. A signed-in rider has, as any business owner has, "orders", "invoices", "bills", "tools", "how did we do", and "on", "off", "busy till 3" as today. "I'm a rider" from someone not on Taskiem starts the chat sign-up with the delivery answer. An offer on WhatsApp is the link order's message with Accept and Can't, as a supplier's order arrives today.

## Safety and privacy

- **A rider sees a customer's details only while holding the order**: the fields are sealed on the order and cleared on Delivered on the rider's side; the business's copy keeps them under its retention.
- **A business sees of a rider what any linked supplier shows**: the profile, the service, the price list shared with it, availability as a state, and the orders between them. Never another business's orders.
- **Money moves only through bill pay and matching**, under the business's approval policy, to the bank on the rider's business; the first-payment warning holds; Taskiem holds nothing.
- **Explore shows nothing a business did not turn on**; a phone finds nobody.
- **No new way across tenants**: the link definers that exist carry the order, the invoice and the payment; the isolation test's list is unchanged.
- **Plain words:** "Rider", "Deliveries", "Available", "Earned", "Cash you hold"; never "fleet", "dispatcher", "payout" or "tenant" on a rider's screen.

## Phases

Five phases, each a PR that ships on its own. The delivery order first, because it is the one real change and everything else is wiring.

1. **The delivery as a link order.** Kind `delivery` with its fields and Arrived; dispatch placing it for a linked supplier with a Delivery service, by hand and by rotation with the window; the card on both sides; the steps from the card, the tool and the WhatsApp buttons; the door payment, cash and quiet riders reading the order; the legacy path untouched.
   - Done when: a business linked with a service business that sells Delivery offers an order to it, the rider accepts from the card, runs it to Delivered with a photo, the customer is told at each step, and both conversations show the same card.
2. **The rider's sign-up and Explore.** The "I deliver for businesses" answer on the web and in chat; the Delivery service with areas and vehicle; the Delivery chip and availability in Explore; `add rider` by phone finding a business; the claim of a legacy rider.
   - Done when: a rider signs up from WhatsApp in under three minutes, is found by a business searching "delivery Yaba", and a business's legacy rider becomes a link in two taps with history kept.
3. **Availability.** The tool and the words writing the business's availability; rotation reading it; Explore showing it; a dispatch company's per-member state.
   - Done when: a rider who says "off" from the tool is offered nothing by two businesses until they say "on", and Explore shows them as off.
4. **Earnings and cash.** The weekly invoice drafted from delivered orders and sent; `pay-riders-weekly` paying the rider's bill; the cash invoice drafted on Cash received and paid from the rider's Bills; the rider's briefing and "how did I do".
   - Done when: a rider's week of deliveries is one invoice paid by the business under approval to the bank on the rider's business, and cash the rider held is a bill they pay from the app, both matched on both sides.
5. **The dispatch company.** Staff with `order.handle` working delivery orders; the company's own rotation inside itself; the company's and each rider's view.
   - Done when: a business offers an order to a company, the company's rotation gives it to a member, the business and the customer see that member's steps, and the company's owner sees the week's orders and money.

## Tests and checks

- **Go:** a delivery order placed, offered in turn with the window and held by the first to accept; the steps accepted only from the rider's side and only for their own order; customer details present only while held; the door payment and cash on the storefront order as before; the legacy path unchanged by every test dispatch has; the claim; availability read as a state only; the weekly invoice's lines; the cash invoice; paid once matched twice with the token; `order.handle` in a company; the isolation test's definer list unchanged.
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
