# Dispatch: your own riders

A business that delivers its storefront orders with riders of its own keeps them in Taskiem: who they are, the areas they cover, and how to reach them in chat. This page grows with the [dispatch plan](spec/dispatch.md) ([decision 0053](decisions/0053-dispatch-with-own-riders.md)); what is built today is the first milestone, D1: riders, delivery areas with fees, and riders saying when they are working.

## Delivery areas

On the order form's settings (Storefront > Forms > Order), a business that delivers lists the **areas it delivers to**, each with its **delivery fee** ("Lekki Phase 1, ₦1,500"; "Yaba, ₦2,000"; a fee of ₦0 is free delivery). Up to 30 areas, each named once.

With areas set, the order form asks a customer who chooses delivery for their area, shows each area's fee, and adds it to the total before they send the order. The order keeps the area and the fee (`zone`, `delivery_kobo`); the fee is part of the order's total, so a payment page, an account number or a refund covers it. The run of the order workflow gets them too (`trigger.body.zone`, `trigger.body.delivery_fee`), and its summary reads "2 × Jollof rice, delivery to Yaba ₦2,000". Without areas, the form takes the address alone, as before.

## Riders

**Storefront > Riders** lists the business's riders by name, with the areas each covers, their fee per delivery, where Taskiem reaches them and their state. People with `storefront.manage` add, change and retire them.

| Field | What it is |
| --- | --- |
| Name and phone | Who the rider is. The phone is the rider: it cannot change (retire them and add the new number) |
| Areas | The delivery areas they cover: the order form's, when it has some, else short labels |
| Fee per delivery | What the business pays them per delivery (paid through payouts in a later milestone) |
| Active | Off while they are on leave: kept, and offered nothing |

A rider is **retired** rather than deleted, so their deliveries keep them; adding the same phone again brings them back.

### Tapping in

A rider is a person outside the business, reached as its customers are ([updates](updates.md), decision 0039). Adding one sends them an SMS: "You were added as a rider for Ada Stores. Tap to get their deliveries on WhatsApp: …", a link to the business's page where one tap opens WhatsApp (or Telegram) with a message ready to send. Once they send it, Taskiem reaches them there, and they are told: "You're set up as a rider for Ada Stores. Their deliveries will come here. Send OFF when you're not working and ON when you are." Until they tap, they are reached by SMS. **Send the link again** sends it once more (at most every ten minutes); the same link is on the Riders page to share another way.

### Off, on and busy

A rider tells Taskiem when they are working, in words to the platform's WhatsApp number or Telegram bot:

| They send | It means |
| --- | --- |
| `off` | Not working: no business offers them anything until they send `on` |
| `on` or `free` | Working and free |
| `busy till 3pm` (or `busy until 15:30`) | Working for someone else until then, Lagos time |
| `busy` | Busy for the rest of the day |

Their words apply to **every business they ride for**. A rider added by two businesses is a separate rider in each, with each business's own name, areas and fee, and neither sees the other's riders; what Taskiem keeps of them as one person is only this state, by a keyed hash of their phone. Each business sees "Musa: off" or "busy till 15:00" beside the name and never which business they are busy for.

## In chat

| Say | What happens |
| --- | --- |
| `riders` | Your riders, their areas, their state and where they are reached |
| `add rider Musa 0803 123 4567 Lekki Ajah` | Adds Musa covering Lekki and Ajah (areas are matched to the order form's, longest first; separate them with commas when a name has spaces and is not one of yours) and sends the link |

## In workflows: `riders@1`

The riders platform connector acts on the run's own business:

| Action | What it does |
| --- | --- |
| `list_riders` | The current riders (active ones unless `include_inactive`), optionally covering one `zone`, each with `state` (free, busy, off) and `reached` |
| `get_rider` | One rider by id |
| `add_rider` | Adds a rider by `name` and `phone`, with `zones` and `fee_kobo`, and sends the link; a phone already riding answers with that rider (`already`) |
| `retire_rider` | Retires a rider |

Offering an order to a rider, the delivery's taps, payment at the door and paying riders follow in the next milestones (D2 to D6 in the [plan](spec/dispatch.md#10-milestones)).

## API

`GET /v1/riders` (`?retired=true`), `POST /v1/riders`, `PATCH /v1/riders/{id}`, `POST /v1/riders/{id}/retire`, `POST /v1/riders/{id}/invite`, all with `storefront.manage`; the areas are the order form's `options.zones` (`PUT /v1/storefront/actions/order`). See the [API reference](sdk.md).
