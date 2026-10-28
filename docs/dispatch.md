# Dispatch: your own riders

A business that delivers its storefront orders with riders of its own runs them in Taskiem. The riders are people outside the business, reached in chat. An order is offered to a rider, who taps **Accept**. The rider's own taps (**Picked up**, **Arrived**, **Delivered**) tell the customer and complete the order. A customer paying at the door gets a one-time account number for the order when the rider arrives, so the money never passes through the rider. Cash is recorded as the exception, owed by the rider until it is handed in. Riders are paid their fees each week under one approval, and customers rate each delivery. The plan is [dispatch](spec/dispatch.md) ([decision 0053](decisions/0053-dispatch-with-own-riders.md)).

## Delivery areas

On the order form's settings (Storefront > Forms > Order), a business that delivers lists the **areas it delivers to**, each with its **delivery fee**: "Lekki Phase 1, ₦1,500" or "Yaba, ₦2,000". A fee of ₦0 means free delivery. A business may list up to 30 areas, each named once.

With areas set, the order form asks a customer who chooses delivery for their area. It shows each area's fee and adds it to the total before they send the order. The order keeps the area and the fee (`zone`, `delivery_kobo`). The fee is part of the order's total, so a payment page, an account number or a refund covers it. The run of the order workflow gets them too (`trigger.body.zone`, `trigger.body.delivery_fee`), and its summary reads "2 × Jollof rice, delivery to Yaba ₦2,000". Without areas, the form takes the address alone, as before.

The form also asks where exactly to come:

- a **landmark** ("opposite the Shoprite gate");
- a **map pin**: a link pasted from a maps app, or **Use my location** in the browser (offered, never required).

Both are sealed with the customer's details. The rider sees them only while they hold the order. Taskiem never looks an address up on a map: the pin is passed on as the customer gave it.

**Customers may pay at the door** (off by default) lets a customer choosing delivery pay when the rider arrives, by transfer or in cash.

## Riders

**Storefront > Riders** lists the business's riders. Each row shows the rider's name, the areas they cover, their fee per delivery, where Taskiem reaches them, their state and their rating. People with `storefront.manage` add, change and retire them.

| Field | What it is |
| --- | --- |
| Name and phone | Who the rider is. The phone is the rider: it cannot change (retire them and add the new number) |
| Areas | The delivery areas they cover: the order form's, when it has some, else short labels |
| Fee per delivery | What the business pays them per delivery, paid out weekly (below) |
| Active | Off while they are on leave: kept, and offered nothing |
| Bank account | Where their pay goes, set under **Pay riders**: shown as "GTBank, ending 6789", with the name once their bank gave it |
| Rating | The average customers gave their deliveries, and how many rated |

A rider is **retired** rather than deleted, so their deliveries keep them; adding the same phone again brings them back.

### Tapping in

A rider is a person outside the business, reached as its customers are ([updates](updates.md), decision 0039). Adding one sends them an SMS: "You were added as a rider for Ada Stores. Tap to get their deliveries on WhatsApp: …". The link opens the business's page, where one tap opens WhatsApp (or Telegram) with a message ready to send.

Once they send it, Taskiem reaches them there and tells them: "You're set up as a rider for Ada Stores. Their deliveries will come here. Send OFF when you're not working and ON when you are." Until they tap, they are reached by SMS. **Send the link again** sends it once more (at most every ten minutes). The same link is on the Riders page to share another way.

### Off, on and busy

A rider tells Taskiem when they are working, in words to the platform's WhatsApp number or Telegram bot:

| They send | It means |
| --- | --- |
| `off` | Not working: no business offers them anything until they send `on` |
| `on` or `free` | Working and free |
| `busy till 3pm` (or `busy until 15:30`) | Working for someone else until then, Lagos time |
| `busy` | Busy for the rest of the day |

Their words apply to **every business they ride for**. A rider added by two businesses is a separate rider in each, with that business's own name, areas and fee, and neither business sees the other's riders. Taskiem keeps only one thing about them as one person: whether they are free, busy or off, keyed by a hash of their phone.

A rider holding a delivery for any business is **busy** there too, with how many they hold. Each business sees "Musa: off" or "busy till 15:00" beside the name, never which business they are busy for.

## Offering an order

An order is **ready for a rider** once three things hold:

- it is accepted;
- it is paid, when it is paid online (paid after acceptance or when ordering). An order paid at the door, or with no online payment, is ready once accepted;
- it is for delivery.

It is offered in one of these ways:

- **From its page**: the order's **Delivery** card has **Offer to a free rider** and **Choose the rider**;
- **From chat**: `order AK-7QX4KD to Musa`;
- **By its workflow**: with deliveries turned on (below), each confirmed order is offered by itself.

Without a rider named, the order goes **by rotation**. The candidates are the active riders covering the order's area who are not off and not marked not answering. Free riders come first, then the one holding the fewest deliveries, then the one offered longest ago. Under **How orders are offered** on the Deliveries page, a business may choose **Ask me in chat who takes it** instead. The owner is then asked, and answers with `order AK-7QX4KD to Musa`.

The rider gets the offer in their chat. It carries:

- the pickup: the business's address;
- the drop: the area;
- the items in one line;
- their fee, and what the customer pays at the door;
- **Accept** and **Can't**.

The address, landmark, map pin and the customer's name and phone come only once they accept. The buttons are signed for the business, the delivery and the rider, and work once, for that rider only.

A rider has the offer window to answer (5 minutes by default). **Can't**, or no answer in time, passes the order to the next rider. Three offers left unanswered in a day mark a rider **not answering**: they are passed over until they tap anything. With no rider left, the owner is told, and can add riders or name one. **Accept** gives the rider the order, which becomes **With a rider**; every other offer for it stops working.

**Take it back** (or `release AK-7QX4KD` in chat) takes an order back from its rider, who is told, and leaves it ready to offer again.

## The delivery

The rider's taps, each a button in their chat (or the words `picked up`, `arrived`, `delivered` and `cash`):

| Tap | What happens |
| --- | --- |
| **Picked up** | The order is **On the way**; the customer is told, with the rider's name and phone |
| **Arrived** | The order is **At the door**; the customer is told. An order paid at the door and not yet paid gets its account number (below) |
| **Delivered** | The order is **completed**. The customer is told and asked to rate the delivery. With proof on, the rider is asked for a photo |

A **photo** the rider sends after Delivered is kept as proof, in the business's own storage. A **location** they share at the door is kept with the delivery. Both show on the order's Delivery card. Taskiem never tracks where a rider is.

**Quiet riders.** A rider who has not picked up an order in time (20 minutes by default), or not reached the door in time (90 minutes), is raised to the owner with the rider's phone. The owner can call them or take the order back.

The order's **Delivery** card shows the rider, each step and its time, the fee, the photo and the rating, and the earlier offers. **Storefront > Deliveries** lists a day's deliveries or those **under way now**, late ones marked. The order's statuses gain **With a rider** (`dispatched`), **On the way** (`on_the_way`) and **At the door** (`arrived`). The customer's status page says each in plain words.

## Paying at the door

For an order paid at the door and not yet paid, **Arrived** issues a **one-time account number** for the order's amount for 30 minutes. It is issued on the order form's payments connection: Paystack, or another rail that opens account numbers, including Taskiem's own (decision 0052). Invoices and order pages use the same limits, and the money is matched to the order exactly, by its reference. The rider is told: "Ask the customer to transfer ₦8,500 to 0107…, Wema Bank, ADA STORES. Valid until 14:30. I'll tell you when it lands." The customer is sent the same.

When it lands, the rider gets **Paid. Hand it over and tap Delivered.** **New number** gets a fresh account number when one expires.

**Cash received**, shown while the order is unpaid at the door, completes the order as paid in cash. The money log records a cash receipt for the order. The rider then **owes** that amount until they hand it in:

- A transfer from the rider whose narration names the order's reference counts by itself, oldest debt first.
- Cash handed in is recorded under **Pay riders > Record cash handed in**, and is added to the money log as cash.
- What a rider still owes when they are paid is taken off their pay.

Cash a rider owes above the business's limit (₦20,000 by default) is a **money leak** ("a rider owes ₦25,000 in cash taken at the door since …"). The leak resolves itself once it is handed in or taken off their pay.

## Paying riders

A delivery's fee is the rider's fee per delivery unless it was set on the offer. It is due to the rider once the order is delivered. **Storefront > Riders > Pay riders** shows:

- what is **due to riders now**, on pine;
- the **cash each rider owes**;
- what is **due to each rider**: their deliveries since their last payout, their fees, the cash taken off, and the bank account it goes to;
- **recent payouts**, each Paid, Being paid, Not paid or Not approved.

**Set up weekly pay** chooses a day and a time (Lagos). It makes the **Pay riders** workflow from the [`pay-riders-weekly`](templates.md) template and publishes it, paying from the business's Taskiem wallet. A business with another payments account chooses it through the API. **Pay riders now** starts the same payout at once. Each payout runs the same way:

1. Each rider's fees since their last payout are added up, less the cash they owe. A rider whose cash owed covers their fees is settled at once with nothing to pay. A rider with no bank account set is named and paid next time.
2. Each rider's bank confirms the name on their account.
3. The approver sees every rider, the name their bank gave and the amount, and approves the list once.
4. Each rider is paid once, however often anything is retried. The cash they owed is taken off and counted as handed in.
5. A payment that fails, or a list not approved, leaves those deliveries to be paid next time. The owner is told how it went.

## Ratings

Once an order is delivered, the customer's message and their status page ask **How was the delivery?** with 1 to 5, for 14 days. One tap keeps it; the customer never sees the rider's name. Each rider's average shows on the Riders page and in the daily close. When a rider's last ten ratings (at least three) average below the business's mark (3 by default), the owner is told once a week.

## Turning on deliveries

Orders can be offered from their page or chat with any order workflow. **Turn on deliveries** (on the Deliveries page, or `turn on deliveries` in chat) goes further. It makes the order form's workflow from the [`dispatch-orders`](templates.md) template and publishes it in place of the one the form starts now, in one change. The form and its settings stay as they are.

The workflow accepts, takes payment and confirms as the plain order workflow does. It then offers each confirmed order for delivery to the riders, and the rider's Delivered completes the run. An order for pickup still asks the owner to mark it collected. The order form must deliver. When the form starts a workflow the business made or changed itself, Taskiem asks before taking it out of use (the API answers 409 `replaces_workflow` until `{"replace": true}`; in chat it is refused, pointing to the Deliveries page). Under four-eyes publishing, it is refused: the business adds the template from Templates and a second person publishes it.

## Money

The **Money** page's summary for a period lists each rider with deliveries, cash or a rating in it. The **daily close** in chat adds a line per rider, in the owner's language:

> Riders yesterday:
> Musa: 4 delivered, ₦8,500 paid at the door, ₦3,000 cash, owes ₦3,000, rated 4.5 of 5

## In chat

| Say | What happens |
| --- | --- |
| `riders` | Your riders, their areas, their state and where they are reached |
| `add rider Musa 0803 123 4567 Lekki Ajah` | Adds Musa covering Lekki and Ajah and sends the link. Areas are matched to the order form's, longest first; separate them with commas when a name has spaces and is not one of yours |
| `order AK-7QX4KD to Musa` | Offers the order to Musa (or `to any rider` for rotation) |
| `release AK-7QX4KD` | Takes it back from its rider |
| `deliveries` | Today's deliveries, late ones first |
| `turn on deliveries` | Asks, then turns deliveries on (above) |

A rider's chat has their buttons, and the words `picked up`, `arrived`, `delivered`, `cash`, `off`, `on` and `busy till 3pm`. Their messages are in English, Nigerian Pidgin, Yoruba, Igbo and Hausa (drafts for review).

## In workflows: `riders@1`

The riders platform connector acts on the run's own business:

| Action | What it does |
| --- | --- |
| `list_riders` | The current riders (active ones unless `include_inactive`), optionally covering one `zone`, each with `state` (free, busy, off) and `reached` |
| `get_rider` | One rider by id |
| `add_rider` | Adds a rider by `name` and `phone`, with `zones` and `fee_kobo`, and sends the link; a phone already riding answers with that rider (`already`) |
| `retire_rider` | Retires a rider |
| `offer` | Offers an order (`reference`) to a rider (`rider_id`) or by rotation, with an optional `fee_kobo`. Its `status` is `offered`, `held`, `asked_owner` or `no_rider` |
| `release` | Takes an order back from its rider |
| `prepare_payouts` | A payout batch (`batch_id`): each rider's payout with their bank account, and those not paid for want of one |
| `finish_payouts` | Records how each payout went (`results`), or `cancel` for a list not approved |

The rider's taps are not steps. A workflow that goes on after Delivered waits for the signal `storefront.delivered` with the order's reference as its correlation. The AI builder knows `riders@1` ("hand my orders to my riders", "pay my riders weekly").

## API

All with `storefront.manage`. See the [API reference](sdk.md).

| Route | What it does |
| --- | --- |
| `GET /v1/riders` (`?retired=true`), `POST /v1/riders`, `PATCH /v1/riders/{id}`, `POST /v1/riders/{id}/retire`, `POST /v1/riders/{id}/invite` | Riders |
| `GET /v1/storefront/orders/{reference}/deliveries` | An order's deliveries, and why it is not ready for a rider, if it is not |
| `POST /v1/storefront/orders/{reference}/offer` (`{"rider_id", "fee_kobo"}`, both optional) | Offers it |
| `POST /v1/storefront/orders/{reference}/release` | Takes it back |
| `GET /v1/deliveries` (`?day=2026-10-28` or `?open=true`) | The day's deliveries, or those under way |
| `GET`/`PUT /v1/dispatch/settings` | Rotation or ask, the offer window, pickup and arrival times, proof, the cash limit and the rating mark |
| `GET`/`POST /v1/dispatch/workflow` | Whether deliveries are on; turning them on (also needs `workflow.edit` and `workflow.publish`) |
| `GET /v1/riders/owed`, `POST /v1/riders/{id}/remit` (`{"amount_kobo", "how": "cash"}`) | Cash riders owe, and cash handed in |
| `PUT /v1/riders/{id}/bank` | A rider's bank account |
| `GET /v1/riders/pay`, `PUT /v1/riders/pay/setup`, `POST /v1/riders/pay` | Riders' pay, weekly pay (with `workflow.publish`), paying now (with `run.start`) |

The areas, the landmark and pin, and paying at the door are the order form's `options` (`zones`, `pay_on_delivery`) on `PUT /v1/storefront/actions/order`.
