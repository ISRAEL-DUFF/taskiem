# Chat: dispatch riders as participants

Status: **plan**, for review (8 November 2026). Written with the founder. Decision: [0065](../decisions/0065-riders-are-organisations.md) (proposed). Amends [dispatch.md](dispatch.md) (D1 to D6, built) and builds on [chat tools](chat-tools.md), [business links](business-links.md) ([0061](../decisions/0061-business-links.md)), [paying people](chat-pay-people.md) (plan) and [the five flows](chat-five-more-flows.md) (plan).

## Summary

The chat has two participants today: a business and the businesses it trades with. Every order, bill, payment and deal between them is a card in a conversation, and each side has tools in the chat for its half of the trade. The third participant in most of those trades is the person who carries the goods, and that person has no place in Taskiem at all: a rider is a row inside one business, a sealed name and phone, reached on WhatsApp with signed buttons, who can answer an offer and tap three steps and nothing else.

This plan makes the rider a participant:

- **A rider is an organisation on Taskiem**, as a person alone already is: they sign up as a rider, have a profile (name, photo, vehicle, areas, hours), a Taskiem conversation, and can be found in Explore under Dispatch.
- **A business adds a rider by linking with them**, the same link that joins a buyer and a seller, with the rider as a third kind of link. Both sides say yes. The business keeps its own settings for that rider (the fee it pays, the areas it uses them for); the rider keeps their own identity, availability and bank, once, for every business.
- **The delivery is a document in the conversation between them**, a card with its steps (Offered, Accepted, Picked up, Arrived, Delivered), on both sides, with diary lines as it moves, as orders and bills are between businesses.
- **The rider has tools in their chat**: Deliveries, Availability, Earnings, Pay (their bank), Businesses. The offer is an alert with Accept and Can't; a remittance due and a payout received are alerts too; the morning briefing takes their shape.
- **The rider is paid in the app**: a business's payout to a rider lands in the rider's own money log as income and as a card in their conversation, and cash a rider collected at the door is remitted from the rider's side and matched on both.
- **A dispatch company is an organisation with riders of its own**, linked by a business as one rider is, dispatching inside itself with the same tools. The fleet connector the dispatch plan deferred falls out of the model.

Everything a rider does today on WhatsApp or Telegram keeps working, word for word and button for button. A rider without a smartphone works as before; the tools are the same work reached from the web when they have it, the pattern already set for staff.

## What exists today

| Part | What it does today | What this plan changes |
| --- | --- | --- |
| `riders` (per business, migration 00242) | A sealed name and phone, zones, a fee, active or retired; keyed by the phone's hash; the same person added by two businesses is two rows | Stays as the business's record of the rider (its fee, its zones, its rating). Gains the link to the rider's organisation when there is one |
| `rider_presence` (platform, by phone hash) | `off`, `on`, `busy till`, inferred busy from open deliveries; read by businesses through a definer that answers a state | Becomes what the rider's own Availability tool writes; the definer and its answers unchanged |
| `deliveries` (the business's) | One row from offer to delivered, with the rider's taps; the offer and the steps on WhatsApp and Telegram with signed `dl1.` buttons | Unchanged on the business's side. Mirrored to the rider's organisation as a job, the way an invoice is mirrored as a bill |
| Assignment (D2) | Offer by hand or by rotation among the business's active riders in the zone, using presence | Offers linked riders and legacy riders alike; presence from the rider's account where there is one |
| Pay at the door, cash and remittances (D4) | A one-time number on Arrived; cash as the exception with a remittance due per rider | Unchanged. The rider sees what they hold and owe in Earnings and can remit from the app |
| Fees and payouts (D5) | Fees accrue per delivery; `pay-riders-weekly` pays each rider by `pay_bank`, net of cash owed; the bank set on the rider under step-up | The rider's bank lives on the rider's organisation, set once; the payout reaches the rider as income and a card |
| Business links (0061) | Two businesses linked as supplier, customer or both; documents mirrored through `taskiem_link_enter`; a conversation per link; Explore | A third role, rider; the conversation carries deliveries; Explore lists riders under Dispatch |
| Chat tools (phases 1 to 7) | Tools, sheets, cards, diary lines, alerts, staff roles, the briefing, for businesses | The rider's tools, alerts and briefing, in the same frame |
| The Assign rider alert (phase 4) | Riders on duty or the next in turn | Linked riders with their own presence |
| Paying people (plan) | `payees` with kind rider; a payment to a rider from Tools or a card | A payee that is a linked rider organisation; the payment as a card on both sides |
| Sign-up | `kinds`: shop, service, school, or just workflows | A fourth: rider (and dispatch, for a company) |

## The rider as an organisation

**Signing up.** The sign-up asks what the business does; **I deliver for businesses** is a new answer. It makes an organisation of kind `rider`: the Riders area on, the shop, service and school areas off, and the chat tools switch on from the start, since the chat is the rider's whole app. The sign-up is the chat sign-up where it exists (a rider on WhatsApp says "I'm a rider" and is walked through it), or the web's. A rider's organisation has one member, the rider, as owner. A phone number is required and proven, because every business that has them today knows them by it.

**The profile.** The business profile that exists (name, tagline, area, about, logo, gallery) with rider fields beside it: **vehicle** (bike, car, van, tricycle, on foot), **areas covered** (the same area labels businesses use for zones), **hours** ("Mon to Sat, 8am to 8pm"), and whether they take **cash at the door**. The photo is the rider's own face when they choose, since a business wants to know who is coming.

**Found in Explore.** A rider who turns on **Be found in Explore** is listed under **Dispatch**, apart from businesses: the card shows the name, photo, vehicle, areas, hours, "free now" or "busy" from their availability, how many deliveries they have done on Taskiem and their rating across businesses (§ Ratings). A business searching "dispatch Yaba" or "rider" sees riders first. The rules of Explore hold: nothing is shown that the rider did not choose to show, and a phone number never finds anyone.

**The rider's own conversation with Taskiem** is where their alerts, briefing and diary lines live, as it is for a business owner. On WhatsApp and Telegram it is the platform number, as today; what changes is that the rider is now a signed-in organisation there, not a contact of someone else's.

## The link

**Adding a rider is linking with them.** Three ways, each ending in a request the rider accepts:

1. **From Explore**, Connect on a rider's card, in the picker, in Explore or from the Assign rider alert's "Find a rider".
2. **By phone**, as `add rider Musa 0803…` works today. If a rider organisation owns that number, the request goes to it and is answered there. If none does, the rider is added as a legacy rider exactly as today (a contact, the tap-in link by SMS), and the invitation now also says "Sign up as a rider on Taskiem to see your deliveries and earnings in one place": tapping it starts the rider sign-up with the phone proven, and finishing it turns that business's row into a link (§ Claiming).
3. **From the rider's side**: a rider may ask a business to link ("I can deliver for you"), from its Explore card. The business's owner answers.

**The link record** is a `business_links` row with role `rider` on the business's side and `client` on the rider's. Each side's settings:

| Setting | Side | Default |
| --- | --- | --- |
| Fee per delivery, and per area where it differs | business | the business's default rider fee |
| Areas I use this rider for | business | the rider's areas that match the business's zones |
| Proof photo required | business | the business's setting |
| May collect cash at the door | business | the rider's profile says, else no |
| Offer them first, or in rotation | business | rotation |
| Take this business's offers while I am on | rider | yes |
| Tell me when: an offer comes, a payout lands, a remittance is due | rider | all on |

**One rider, many businesses.** The rider's identity, availability, vehicle, bank and rating are theirs, kept once on their organisation. What is particular to a pair is on the link and on the business's `riders` row: the fee, the areas, that business's rating of them, how many deliveries they did for it. Nothing of one business's deliveries is visible to another, as today; the rider sees all of their own.

**Claiming.** A business's legacy rider whose phone hash matches a rider organisation's proven phone is offered to both sides to become a link: the business sees "Musa is on Taskiem as a rider: link?" on the Riders tool, the rider sees "Ada Stores has you as a rider: accept?" in their chat. Either may start it; both say yes. On acceptance the `riders` row gains the link, past deliveries stay the business's record and are mirrored to the rider as past jobs so their history is whole, and the rider's bank on that row (if set) is retired in favour of the rider organisation's own. A legacy rider who never signs up keeps working as today for as long as they like.

**Ending a link** stops offers; jobs already held run to their end; history stays on both sides. A business may still **retire** a rider as today; a rider may **leave** a business.

## The delivery as a document

A delivery today is the business's row, driven by the rider's taps. It stays that: the business's `deliveries` row is the truth for the order it belongs to. What is added is the rider's copy, a **job**, mirrored into the rider's organisation the way an invoice is mirrored as a bill (`taskiem_link_enter`, audited on both sides, the rider's own rows under their own row-level security), and the conversation between the two in which the delivery is a card.

**The card**, on both sides: the business's name (on the rider's side) or the rider's (on the business's), the order's reference, the pickup, the drop's area, the fee, the state as a pill, the four steps (Accepted, Picked up, Arrived, Delivered), and the next step as a filled button for whoever holds it: **Accept** and **Can't** on an offer, **Picked up**, **Arrived**, **Delivered** (with the photo when asked) for the rider, **Release** for the business while the rider has not picked up. A card that arrives while the conversation is open slides in. The customer's name, phone, address, landmark and pin come onto the rider's card with Accept and leave it on Delivered, as the offer does today.

**Diary lines**, on each side in its own words: "Offered AK-7QX4KD to Musa", "Musa accepted AK-7QX4KD", "Picked up AK-7QX4KD from Ada Stores", "Delivered AK-7QX4KD · ₦1,500 earned", "Cash received ₦8,500 for AK-7QX4KD: you owe Ada Stores ₦8,500", "Paid Musa ₦12,000 for 8 deliveries". The customer's status page and messages are unchanged.

**The offer** is the same offer as today, delivered three ways at once to a linked rider: the card in the conversation, the alert in the rider's Taskiem chat, and the WhatsApp or Telegram message with its signed buttons. Whichever is tapped first answers it; the others follow. The offer window, rotation, Can't, not answering and release work as D2 built them.

**Multi-drop.** Out of scope in the dispatch plan and still out of scope here: one job is one order. A rider holding several jobs sees them as a list ordered by the time accepted.

## The rider's tools

With chat tools on (always, for a rider organisation), the tools button in the rider's Taskiem conversation opens their tools. A business's conversation shows the same tools filtered to that business.

| Tool | What it shows | What it does in place |
| --- | --- | --- |
| **Deliveries** | Today's jobs: offered (with the window counting down), held (in step order), done; then earlier days. Each a card | Accept, Can't, Picked up, Arrived, Delivered with the photo; the drop's map link; call the customer (their phone, while holding) |
| **Availability** | On or off; busy till; the areas covered today; hours | `on`, `off`, `busy till 3pm`, areas on and off for the day. Writes `rider_presence`, so every linked business sees the state as today |
| **Earnings** | Per business and in all: fees earned since the last payout, cash held and owed, payouts received, with the deliveries behind each number; the week's bars | **Remit** cash owed to a business (§ Getting paid); open a payout's card |
| **Pay** | The rider's bank account (sealed; set and changed under a passkey or code), the name the bank gave, and the payments received to it | Set or change the account; see a payout's state (sent, received by the bank, failed) |
| **Businesses** | Linked businesses with the fee each pays, requests waiting, and Explore | Accept or decline a request; leave a business; ask a business to link |

**Alerts** in the rider's Taskiem conversation, in the same card frame as a business's: an **offer** (Accept, Can't, the countdown), a **remittance due** over the business's limit ("You hold ₦23,500 of Ada Stores' cash: Remit"), a **payout received** ("Ada Stores paid you ₦12,000: Received by the bank"), a **rating** when a customer rated them, and a **link request** from a business. A new offer makes the sound.

**The briefing**, at the rider's start of day (their hours' start, 8am by default): yesterday's deliveries and what they earned, against the same day last week; what they are owed by whom; cash they hold; today's businesses on. **"How did I do?"** answers in words and a chart, as a business's "how did we do" does.

**Permissions.** A rider organisation's owner holds `rider.work` (the jobs and availability), `rider.money` (earnings, bank, remit), `link.manage`. A dispatch company's staff rider holds `rider.work` for the jobs offered to them and sees their own earnings only.

## The business's side

Little changes on the business's screens, and what changes is for the better:

- **The Riders tool** (Storefront > Riders and the chat tool) lists linked riders and legacy riders together, each with their state from presence, their fee and areas for this business, how many deliveries, their rating here, and for a linked rider their vehicle and photo and **Message** (the conversation). A legacy rider on Taskiem shows the claim prompt.
- **Assign rider** (the alert and `order X to Musa`) offers linked riders by rotation as today, with their presence from their own Availability, and **Find a rider** opens Explore's Dispatch list for a business with nobody free.
- **The conversation with a rider** is where the business's staff see the delivery's cards and lines and can message the rider ("Customer says the gate is blue"). `order.handle` holders may work it, as they work orders.
- **Pay riders** (Riders > Pay and `pay-riders-weekly`) pays a linked rider to the bank on the rider's organisation, never a bank held on the business's row. The payment is a card in the conversation and a diary line on both sides; a failed payout is told to both.

## Getting paid

Money between a business and a rider goes two ways: fees the business owes the rider, and cash the rider collected at the door and owes the business. Both already exist as records on the business's side (D4, D5). This plan gives each a mirror on the rider's side and a way for the rider to act.

**Fees and payouts.** A payout made by `pay-riders-weekly`, by Riders > Pay, or by the paying-people plan's Pay sheet to a payee that is a linked rider, is written on the business's side as today and mirrored to the rider's organisation through the link as a **payout received**: a money-log entry of kind income from that business, with the deliveries it covers, and a card in the conversation on both sides. The bank account comes from the rider's organisation (`rider.money`, set under step-up); the business's workflow reads it through a definer that answers a bank code and an account number for a linked rider and nothing else, the shape `taskiem_link_payout_token` already has. The first-payment warning applies as to any payee.

**Cash and remittances.** A rider who tapped Cash received owes the business (`rider_remittances`). The rider's Earnings shows it per business; **Remit** pays it from the rider's own payments connection where they have one (their wallet on the platform rail, or a bank they connected), through the same pay path as a bill, with a pairing token in the narration so the business's side matches it to the remittance exactly, as bills are matched twice (business-links §6.3). A rider with no connection remits by transfer to the business's own number as today, and the match is as today; or hands the notes in and the business records it, which closes the rider's side through the mirror.

**One truth per record.** The business's `deliveries`, `rider_payouts` and `rider_remittances` are the truth; the rider's `rider_jobs`, `rider_payouts_received` and `rider_remittances_owed` are mirrors, written through `taskiem_link_enter` and compared by the hourly link reconciler through the digest definer, as every mirrored document is.

## A dispatch company

A company with ten riders is an organisation of kind `dispatch`: it links with businesses as a rider does (role `rider` on the business's side), and inside it its riders are members with a built-in role `rider` (the staff-roles pattern of chat tools phase 5), each with their own phone, availability and earnings, paid by the company. An offer from a business reaches the company and the company dispatches it to one of its riders by the same rotation the dispatch plan built (the company is the "business" of its own `deliveries`), so the business sees the company accept and the rider's taps, and the customer sees "on its way with Musa from Swift Dispatch". The company's owner sees every job, every rider's state and the money; each rider sees their own. Fees are the company's; what it pays its riders is its own affair, through the same payouts.

This is the fleet connector the dispatch plan deferred, without a connector: a fleet is an organisation. Platforms with their own apps (Kwik, Glovo, GIG) remain a later connector behind `offer`, if ever.

## Data model, engine and API

The core rule (the core cleanup plan, PR 172): the new tables are records with rules and engine state in the riders module's schema, each named here, none a table a rider could keep in a sheet.

| New or changed | Holds | Kind |
| --- | --- | --- |
| `tenants.kind` gains `rider` and `dispatch`; `signup` takes `rider` in `kinds` | What the organisation is | platform |
| `rider_profile` (the rider's organisation) | vehicle, areas, hours, takes cash, found in Explore; beside `business_profile` | record |
| `business_links.role` gains `rider` and `client`; link settings gain the rider pair's (fee, areas, proof, cash, offer first; take offers, tell me) | The link | platform, as today |
| `riders.link_id` (the business's) | Which link, when the rider is an organisation; null for a legacy rider | record, as today |
| `rider_jobs` (the rider's) | The mirror of a delivery: link, the business's name, order reference, pickup, drop's area, fee, status, the steps' times, the customer's details sealed while held, the photo's path (the rider's copy) | record (mirror) |
| `rider_payouts_received`, `rider_remittances_owed` (the rider's) | Mirrors of the business's payouts and remittances | record (mirror) |
| `rider_bank` (the rider's) | Bank code, account sealed, the bank's name; set under step-up | record |
| `rider_presence` | Unchanged; written by the Availability tool as well as by words | engine state |
| `rider_ratings` (the rider's) | The mirror of each rating, by business, never the customer | record (mirror) |

Kept copies (the core cleanup plan) are not made for any of these until a rider or a business asks to read them in a workflow.

**Engine.** `engine/dispatch` (the business's side, as built) learns a linked rider: offers reach the link's rider organisation as well as the phone; the taps arriving from the rider's tools or the rider's WhatsApp are the same `dl1.` payloads, now also accepted from the rider's signed-in session for their own job. A new `engine/rider` is the rider's side: jobs, availability, earnings, the bank, remittances, mirrored through `taskiem_link_enter` from the dispatch module's writes; `riders@1` gains `list_linked`, `link`, `unlink`. The link reconciler covers the three mirrors. Explore indexes rider profiles under Dispatch. The dispatch company is `engine/dispatch` running inside a `dispatch` organisation whose riders are members, with the rotation it has.

**API.** `/v1/rider/jobs` (list, and the steps as `POST /v1/rider/jobs/{id}/{step}` with the photo), `/v1/rider/availability`, `/v1/rider/earnings`, `/v1/rider/bank` (step-up), `/v1/rider/remit`, `/v1/rider/profile`; `/v1/riders` (the business's) lists linked and legacy riders; `/v1/links` takes role `rider`; `/v1/explore?kind=dispatch`. The chat tools endpoints (`/v1/chat-alerts`, `/v1/chat-briefing`, `/v1/chat-diary`) answer for a rider organisation with the rider's kinds.

## WhatsApp, Telegram and Slack

Every word and button a rider uses today stays. Added, on the platform number, for a signed-in rider: **"my earnings"** (per business and in all, cash held), **"remit Ada Stores"** (a link to Earnings, confirmed in the app or by the one-time code the paying-people plan allows), **"link Ada Stores"** and **"businesses"**, **"on"**, **"off"** and **"busy till 3"** as today, **"my deliveries"** as today. An offer on WhatsApp carries the business's name and a link to the card. A rider who is not signed up and says "I'm a rider" is walked into the rider sign-up.

## Safety and privacy

- **A rider sees a customer's details only while holding the job**, as today; the mirror seals them and clears them on Delivered, and the business's copy keeps them under its own retention.
- **A business sees of a rider only what the rider shows**: profile, vehicle, areas, availability state, the rating across businesses as a number, and what the rider did for that business. Never the rider's other businesses or their earnings elsewhere.
- **Money moves only through the payment paths that exist** (`pay_bank` under approval, the pairing token), never a balance Taskiem holds; a rider's bank changes only under step-up; the first-payment warning holds.
- **Explore shows nothing a rider did not turn on**, and a phone number finds nobody.
- **Every mirror is one `taskiem_link_enter` call** per event, audited on both sides, and the isolation test's list of allowed definers gains only the rider bank definer.
- **Plain words:** "Rider", "Deliveries", "Earnings", "Remit", "Available"; never "fleet", "dispatcher", "payout" or "tenant" on a rider's screen.

## Phases

Six phases, each a PR that ships on its own and leaves nothing half-built. The rider's own account comes first because every other phase stands on it; the dispatch company last because it needs all the rest.

1. **The rider's organisation.** Sign-up kind `rider` (web and chat), the rider profile, Explore's Dispatch list, the rider's Taskiem conversation with chat tools on; a rider's presence written from Availability.
   - Done when: a rider signs up from WhatsApp, is found in Explore under Dispatch by a business in their area, and their on and off is seen by that business's Riders page.
2. **The link and the claim.** Role `rider`, the pair's settings, Connect from Explore and the picker, `add rider` by phone finding a rider organisation, the claim of a legacy rider on both sides, the Riders tool showing both kinds, Assign rider offering linked riders.
   - Done when: a business that had Musa as a legacy rider links with Musa's organisation in two taps, keeps its fee and his deliveries, and his next offer reaches him as before.
3. **The delivery as a document.** `rider_jobs` mirrored through the link, the card and the diary lines on both sides, the offer as a card and an alert, the steps from the rider's tools, the conversation between business and rider.
   - Done when: an order offered to a linked rider is accepted from the card, run to Delivered with the photo from the Deliveries tool, told to the customer at each step, and both conversations show the same card at the same step.
4. **Earnings and getting paid.** Earnings and Pay tools, `rider_bank` under step-up, payouts mirrored as income and cards, the business's payout reading the rider's bank through the definer, the payout and remittance alerts, the rider's briefing and "how did I do".
   - Done when: `pay-riders-weekly` pays a linked rider to the bank on their organisation, the rider sees it land in Earnings and as a card, and the business's row holds no bank.
5. **Cash remitted from the app.** Remit from Earnings through the rider's own connection with the pairing token; matched on the business's side exactly; hand-in and plain transfer still closing the rider's side.
   - Done when: a rider who collected ₦8,500 cash remits it from Earnings and both sides show the remittance settled with nothing in To match.
6. **The dispatch company.** Kind `dispatch`, the built-in `rider` staff role, the company's own dispatch of a business's offer, each rider's own jobs and earnings, the company's view of all.
   - Done when: a business offers an order to Swift Dispatch, Swift's rotation gives it to Musa, the business and the customer see Musa's steps, and Swift's owner sees the week's jobs and money per rider.

## Tests and checks

- **Go:** the link with role `rider` and its settings; the claim of a legacy rider in both directions and the row's history kept; each mirror written once through `taskiem_link_enter` and the reconciler finding a planted difference; the offer answered from the card, the alert and WhatsApp with the first answer winning; the steps from the rider's session accepted only for their own job; the bank definer answering a linked rider's bank and nothing for anyone else; payouts and remittances paired by token; the isolation test's definer list; a rider seeing nothing of another business; the dispatch company's rotation inside itself.
- **Browser (e2e):** at 390 and 360 px in both themes with axe: a rider's sign-up from the chat, Explore's Dispatch card and Connect, the claim, the Deliveries tool through the three taps with a photo, Availability seen on the business's Riders page, Earnings and Remit, Pay with a passkey, the briefing; the business's conversation with the rider; the company's dispatch.
- **WhatsApp:** the fake Graph API the e2e tests run: an offer reaching the rider on WhatsApp and the card at once, answered on either.
- **Plain words:** every new rider-facing line through `src/lib/plainwords.test.ts`.
- **The recording:** a new act in `video/recording/`: a rider found in Explore, linked, a delivery from offer to delivered on their phone, paid on Friday.

## Open questions

The plan assumes the answer in brackets.

- [ ] Ratings across businesses on the Explore card: shown as a number from the first rating, or only after ten? (After five.)
- [ ] A rider's areas: the business's zone labels, or free text matched by name? (The same labels, with free text allowed and matched by name.)
- [ ] Should a rider see the customer's phone, or call through Taskiem? (The phone, while holding, as today; calling through Taskiem is a later addition.)
- [ ] A legacy rider's bank on the business's row: retired on claim, or kept until the rider sets their own? (Kept and used until the rider sets their own, then retired.)
- [ ] Can a business set a rider's fee per delivery at offer time, as the offer allows today? (Yes; the link's fee is the default.)
- [ ] A dispatch company's riders: members with a role, or each a rider organisation linked to the company? (Members: one sign-in, the company's phone numbers, the company pays them.)
- [ ] Remit from a rider's wallet on the platform rail: needs decision 0052's wallet for individuals. (Phase 5 waits on it where no bank is connected.)
