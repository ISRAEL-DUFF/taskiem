# Dispatch: a business's own riders, deliveries in chat, payment at the door

Status: **Draft** 27 October 2026, for review · Decision: [0053](../decisions/0053-dispatch-with-own-riders.md) · Builds on: [storefronts](../storefronts.md) (orders), [reaching people outside](../decisions/0039-reaching-people-outside.md), [invoices and the money log](invoices-and-money-log.md) (one-time account numbers, matching), [leaks](../leaks.md), [media](../media.md) · Stories: [user stories](../strategy/user-stories.md) 50–52, 65, 105, 106

## 1. Summary

A storefront order today ends with the owner typing `order AK-7QX4KD delivered`. This plan fills what happens between **accepted** and **delivered** for a business with riders of its own: the riders as people outside the business reached in chat, an order offered to a rider with **Accept**, the delivery driven by the rider's taps (**Picked up**, **Arrived**, **Delivered**, a photo as proof), the customer told at each step, **payment at the door by a one-time account number issued when the rider arrives** so the money never passes through the rider, cash as the recorded exception with a remittance due per rider, riders paid through the existing payout path, and a rating from the customer. Companies' fleets and delivery platforms are a later connector behind the same step.

Six milestones, D1 to D6, one PR each, in order.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Rider | A person who delivers for the business: not a member, no Taskiem account; a contact under decision 0039 with a name, phone, zones and whether active |
| Zone | A short label the business gives areas it delivers to (Lekki, Yaba, Island); an order carries one from its address |
| Offer | An order put to one rider with Accept and Can't, for a few minutes |
| Delivery | The record of one order's journey: the rider, each tap and its time, the photo, how it was paid at the door |
| At the door | A one-time account number for the order's amount, issued when the rider taps Arrived |
| Remittance due | Cash a rider received for an order and has not yet handed to the business |

## 3. Where we are

| Exists | Where |
| --- | --- |
| Orders with statuses, a reference, the customer sealed, the status page, accept and decline in chat, payment online or after acceptance, stock, refunds | [storefronts](../storefronts.md) |
| People outside the business reached in chat by one tap, their buttons signed per business and contact, STOP and START | decision 0039, [updates](../updates.md) |
| One-time account numbers per order matched exactly by reference; the money log; the daily close and summaries | [invoices](../invoices.md), [money log](../money-log.md), [money reports](../money-reports.md) |
| Photos stored in the business's own project, upright, resized | [media](../media.md) |
| Leaks: things that stall, closed with a reason | [leaks](../leaks.md) |
| Payouts under approval, name enquiry, remembered accounts | `pay_bank`, [templates](../templates.md) |
| `order X delivered` from chat; the order workflow's "mark it delivered or collected" step | [storefronts: from chat](../storefronts.md#from-chat) |

## 4. The riders (D1)

- A `riders` record per business: `riders` (id, tenant, name sealed, phone as a contact under 0039, zones as labels, active, fee per delivery default, created, retired). Riders are listed on **Riders** (Storefront > Riders), added by name, phone and zones, retired rather than deleted (their deliveries stay). Adding one sends the tap-in link ("Tap to get Ada Stores' deliveries on WhatsApp"); until the rider taps, they are reached by SMS.
- `riders@1` platform connector: `list_riders` (active, zone), `get_rider`, `add_rider`, `retire_rider`, and the dispatch actions of D2 to D5. Permissions: `order.manage` for riders and assignment (as orders), `order.read` to see.
- Chat for the owner: `riders` (the list and who holds what), `add rider Musa 0803…  Lekki Ajah`.

## 5. Assignment (D2)

- An order that is accepted and, where paid online, paid, and whose fulfilment is delivery, becomes **ready to dispatch**. The order workflow (the `storefront-order` template, extended, or the new `dispatch-orders` template the storefront's "turn on deliveries" publishes) offers it to a rider:
  - **By hand**: the owner is asked "Who takes AK-7QX4KD to Lekki?" with the active riders in the zone as buttons (and "any rider"); or from chat, `order AK-7QX4KD to Musa`.
  - **By rotation**: the rider in the order's zone with the fewest open deliveries, then the next; the owner can set "always ask me" instead.
- The offer in the rider's chat: the pickup (the business's address), the drop address and landmark from the order, the customer's first name and phone, the items in one line, the fee, **Accept** and **Can't**. Buttons are signed for the business, the order and the rider's contact (as an invoice's are for a customer) and work once. No answer in the business's offer window (default 5 minutes) or **Can't** moves to the next rider; no rider left tells the owner, who can offer again later.
- Accepting records the delivery with its rider and moves the order to `dispatched`; every other offer for that order stops working. The customer is told "Your order is on its way with Musa" at pickup, not at acceptance.
- `riders@1`: `offer` (order, rider or zone, window), `assign` (by hand), `release` (take it back from a rider).

## 6. The delivery (D3)

- The rider's three taps, each a signed button in the chat (and the words `picked up`, `arrived`, `delivered` for SMS): **Picked up** (the order moves to `on_the_way`; the customer is told, with the rider's first name and phone); **Arrived** (the customer is told; the door payment of §7 starts when the order is not yet paid); **Delivered**, with "send a photo as proof" when the business asks for proof (the photo lands in the business's project under the order, upright and resized, as storefront photos do). Delivered completes the order as the owner's `delivered` does, and the run waiting on that step finds it completed.
- **Quiet riders**: an order `dispatched` with no pickup in 20 minutes, or `on_the_way` with no arrival in 90 minutes (the business's settings), raises a leak to the owner naming the rider and the order, closed when the next tap comes or the owner releases the order.
- The owner sees deliveries on the order ("Musa: picked up 12:04, arrived 12:41, delivered 12:43, photo") and on a **Deliveries** list for the day: open, late, done.
- The order's statuses gain `dispatched`, `on_the_way`, `arrived`; `completed` stays the end. The customer's status page shows them in plain words.

## 7. Payment at the door (D4)

- The order form's fulfilment gains **pay on delivery** (an owner's setting; off by default). Such an order is accepted without payment and dispatched; it is `awaiting_payment` at the door.
- On **Arrived**, when the order is not paid: Taskiem issues a **one-time account number** for the order's amount on the business's payments connection (the storefront's connection, or the platform rail of [decision 0052](../decisions/0052-taskiem-as-a-collection-rail.md) once built), exactly as the invoice page does: the same limits, the same expiry handling, the same exact match by the order's reference. It is sent to the rider ("Ask the customer to transfer ₦8,500 to 0107… Wema, ADA STORES. I'll tell you when it lands.") and to the customer (their status page and chat, where they tapped in). When the credit is matched, the rider's chat says **Paid. Hand it over.** and the customer's says "Paid, thank you"; the rider then taps Delivered. A number that expires before payment gets a new one with **New number**.
- **Cash received**: the rider's fourth button, shown only while the order is unpaid at the door. It completes the order as paid in cash: the money log records a cash receipt for the order (as the schools' cash posting does, matched exactly to the order) and a **remittance due** against the rider for the amount. The rider remits by transfer to the business's own number (the narration carrying the order reference) or hands the notes in and the owner records it; a remittance is matched to the rider's open remittances oldest first (the permanent-account rule's shape, with the rider as the customer); the suggestion matching serves transfers without the reference.
- **Per rider in the daily close and on the Money page**: delivered, paid at the door, cash received, remitted, owed. A rider owing more than a threshold raises a leak.
- A business whose customers mostly pay cash may give a rider a **permanent account number of their own** (the customer-account path, the rider as the customer), so remittances match exactly; an option on the rider, off by default.

## 8. Riders' fees, payouts and ratings (D5)

- A fee per delivery (the rider's default, or per zone, or set on the order) accrues to the rider when the order completes. **Riders > Pay** shows each rider's deliveries since the last payout, fees, cash owed, and the net; **Pay riders** makes one payout per rider through `pay_bank` (the rider's bank account resolved by name enquiry the first time and remembered), under the business's approval policy, with an idempotency key per rider and period, recorded in the money log as any payout. A template `pay-riders-weekly` does it on a chosen day.
- **Ratings**: after Delivered, the customer gets a one-tap "How was your delivery?" (1 to 5, in their chat or on the status page); ratings show on the rider and in the daily close; a rider falling below a mark raises a leak (story 65).

## 9. Templates, chat and docs (D6)

- `dispatch-orders`: the order workflow with dispatch in it (offer by rotation or ask the owner, the three taps, pay at the door, cash as the exception, proof of delivery on request); the storefront's **turn on deliveries** publishes it in place of the plain order workflow, keeping the order form. `pay-riders-weekly`.
- The owner's chat: `riders`, `add rider`, `order X to Musa`, `deliveries` (today's, late first), `release X`. The rider's chat: the buttons, and the words for SMS, in the five languages (drafts for review).
- Docs: `docs/dispatch.md` (the guide), the storefront page's order flow, the money reports (per-rider lines), the owner's guide; the AI builder learns `riders@1` ("tell my rider", "pay on delivery") with eval cases.

## 10. Milestones

| Id | Milestone | Size | Done when |
| --- | --- | --- | --- |
| D1 | Riders: the record, the Riders page, `riders@1` listing and adding, the tap-in link, chat `riders` and `add rider` | S | A rider added by phone taps in on WhatsApp and is reachable there; retired riders keep their deliveries |
| D2 | Assignment: ready to dispatch, offer by hand or rotation with Accept and Can't, the offer window, release, `dispatched` | M | An order offered to two riders is held by exactly one; a rider who does not answer is passed; the owner sees who has it |
| D3 | The delivery: Picked up, Arrived, Delivered with a photo, the customer told each step, Delivered completes the order, quiet-rider leaks, the Deliveries list | M | A rider's three taps run an order to completed with the customer told three times; a quiet rider raises a leak; the photo is on the order |
| D4 | Pay at the door: the fulfilment option, the one-time number on Arrived, Paid to the rider, New number, Cash received with remittance due, per-rider lines in the close and the Money page, the optional rider account | M | A pay-on-delivery order is paid at the door and matched exactly with no one asked; a cash order shows the rider's remittance due until a transfer matches it |
| D5 | Fees, payouts and ratings: fees accrued, Riders > Pay, `pay-riders-weekly` under approval net of cash owed, the rating tap | M | Two riders are paid in one approved run, one net of cash owed, both in the money log; a rating shows on the rider |
| D6 | Templates, chat, docs, the AI builder | S | `turn on deliveries` publishes `dispatch-orders`; the guides are published; the builder uses `riders@1` for "tell my rider" |

Order: D1 to D6. D4's door payment on the platform rail needs decision 0052's W3; on a business's own Paystack, Flutterwave or OPay it works from D4.

## 11. Testing

| Level | What | Where |
| --- | --- | --- |
| Signed buttons | A rider's button works once, only from that rider's contact, only while the order is theirs; a released order's buttons die | `api` |
| Exactly once | An order offered to several riders is accepted by one; a double tap on Delivered completes once; a door payment heard twice counts once | `engine/storefront`, `engine/moneylog` |
| Money | The door payment matched exactly by reference; cash received as a receipt plus a remittance due; a remittance matched oldest first; the payout net of cash owed | `engine/matching`, `engine/moneylog`, `api` |
| Chat | The rider's flow on WhatsApp, Telegram and SMS words; the owner's commands; the five languages | `api`, `engine/plain` |
| Web | Riders, Deliveries, the order's delivery record, Riders > Pay, the status page's new words | Playwright |

## 12. Risks and open questions

| Risk or question | Mitigation or owner |
| --- | --- |
| A one-time number's window (30 minutes on most rails) against a customer fumbling at the door | Issued on Arrived, not before; **New number** at a tap; Taskiem's own expiry handling counts a late transfer the rail still accepts |
| Riders on SMS only (no WhatsApp) | The words work by SMS reply; buttons are a convenience |
| A rider's photo of the wrong door | Proof is for the business's record, not a dispute system; the customer's rating is the other half |
| Rider fees as income to the rider: withholding tax, contracts | Out of scope; the business's own affair; the payout is a payout |
| The offer window and quiet-rider times | Settings per business, defaults 5, 20 and 90 minutes; tuned with the first businesses |
| Fleets and platforms (Kwik, Glovo, GIG) | Later connectors behind `offer`; nothing in the record assumes a person |

## 13. Out of scope

Route planning and maps; live location; a rider app; multi-drop runs (one rider, several orders at once: a later addition to the offer); platform and fleet connectors; insurance and disputes.
