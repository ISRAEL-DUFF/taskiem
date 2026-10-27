# 0053 — Dispatch with a business's own riders: assignment and delivery in chat, payment at the door by a one-time account number, cash the exception

Date: 2026-10-27 · Status: **Proposed** · Plan: [dispatch](../spec/dispatch.md)

## Context

A storefront order ([decision 0036](0036-storefronts.md)) runs from the customer's basket to the owner's acceptance, payment online or after acceptance, the stock lowered, and the customer's status page; then the run waits for the owner to say **delivered** or **collected**. Everything between accepted and delivered happens outside Taskiem: who takes the order, whether it got there, and for money collected at the door, whether it ever came back. The user-story catalogue names the gap (stories 50 to 52, 65, 105 and 106).

In Lagos most businesses that deliver use **their own riders**: one or a few people on bikes, reached on WhatsApp, paid per delivery or weekly, often handling the customer's cash. Logistics companies' fleets and the delivery platforms exist and can be connectors later; the first version is for the rider a business already has.

Riders are people outside the business, like a school's guardians or a form's respondents: no Taskiem account, reached on WhatsApp, Telegram or SMS by one tap ([decision 0039](0039-reaching-people-outside.md)). Taskiem already has the pieces a delivery needs: outsiders' buttons signed per business and contact, photos stored in the business's own project ([media](../media.md)), one-time account numbers per invoice or order matched exactly by reference ([0045](0045-tally-in-taskiem.md)), leaks for things that stall, payouts under approval, and the daily close.

## Decision

1. **A business keeps its riders** as people outside it: name, phone, zones, active or not, each reached on the channel they tapped in on, else SMS. No rider account; nothing of the rider's beyond the phone and name is kept. A rider added by several businesses is a contact in each, under each one's own security; the platform keeps one **presence** per person (free, busy, off) from their deliveries anywhere and their own words, and shows each business only the state, never the other businesses.
2. **An order is offered to a rider in chat** with **Accept** and **Can't**; no answer in a few minutes moves it to the next rider in the order's zone, free riders first; the owner can name a rider by hand and always sees who has the order and each rider's state. Only one rider holds an order at a time. The drop is what the customer gave on the order form: an area from the business's zones with its fee, the address and a landmark, and a map pin or a shared location when offered; Taskiem geocodes nothing and tracks no one.
3. **The rider drives the delivery with three taps**: **Picked up**, **Arrived**, **Delivered**, with a photo as proof on request; each one tells the customer, and **Delivered** completes the order in place of the owner. A rider who holds an order and goes quiet raises a leak to the owner, as money does.
4. **Payment at the door is a one-time account number issued on Arrived**, for the order's amount, sent to the rider and the customer; the customer transfers, Taskiem matches it exactly to the order as it matches the storefront's online payment, and the rider's phone says **Paid, hand it over** within seconds. Money never passes through the rider, so there is nothing to remit and no shortfall to list. The number is issued at arrival, not at ordering, because one-time numbers expire in minutes.
5. **Cash is the exception.** A rider who takes notes taps **Cash received**: the order completes, a remittance due is recorded against the rider, and the rider later transfers it to the business's own number or hands it in; the daily close lists, per rider, what was collected in cash, what was remitted and what is owed. A business whose customers mostly pay cash may give a rider a permanent account number of their own; that is an option, not the default.
6. **Riders are paid through the existing payout path**: a fee per delivery accrues to the rider; a weekly payout (or on demand) pays each rider net of cash they owe, under an approval policy, with the bank account resolved by name enquiry and remembered. Ratings are a one-tap message to the customer after delivery.
7. **Companies' fleets and platforms come later** as connectors behind the same assignment step, when a business has one; nothing in the record assumes a person on a bike.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Cash on delivery remitted by the rider as the norm | The rider becomes a float the business chases; the one-time number at the door removes the float and keeps exact matching, and riders then need no account numbers (and no BVN on rails that want one per account) |
| A permanent account number per rider | Works, and stays an option for cash-heavy businesses, but makes the rider a collection point by design; the door payment is simpler and leaves nothing to reconcile |
| The number issued at ordering for pay-on-delivery | One-time numbers expire in minutes; an hour later the customer pays into a dead number, which the provider may or may not honour |
| A rider app | Riders will not install one for a business with two deliveries a day; the chat they already use, with buttons, is enough |
| Start with Kwik or Glovo integrations | Most of the businesses Taskiem is for use their own riders; the platforms are a later connector |

## Consequences

- Orders gain the statuses between accepted and delivered, and a delivery record with its rider, taps, times and photo.
- The storefront's **pay on delivery** fulfilment option becomes real: the order form offers it, and the door payment serves it.
- The money log gains remittances due per rider; the daily close and the Money page show them.
- A new `riders@1` platform connector, a Riders page, a `dispatch-orders` template, chat commands for the owner, and new words for the rider in the five languages.

## Provenance

From the founder on 27 October 2026: the order process is complete except dispatch; most Lagos businesses use their own riders, so build for them and add the other options later; a one-time account number per order at the point of payment, which also serves pay on delivery, rather than a permanent number per rider.
