# 0065 — A dispatch rider is a service business whose service is delivery, and a delivery is an order for it

Date: 2026-11-08 · Status: **Proposed** · Plan: [chat: dispatch riders as participants](../spec/chat-riders.md)

## Context

The dispatch plan ([dispatch.md](../spec/dispatch.md), D1 to D6, built) made a rider a row inside one business: a sealed name and phone, reached on WhatsApp or Telegram with signed buttons, who answers an offer and taps three steps. The same person added by two businesses is two rows and one shared presence keyed by the phone's hash. The rider has no account, no view of their deliveries or earnings across businesses, and no bank of their own.

Chat tools (phases 1 to 7) made the conversation between two linked businesses the place where their trade happens, with tools, cards, diary lines and alerts for each side. The founder, on 8 November 2026, named the third participant: the rider is a business too, should be found and added as one, and should have the same experience in the chat, receiving deliveries, declaring availability and being paid in the app. A first draft gave the rider a sign-up, a kind, a profile, a link role, tools, roles, mirror tables and a module of their own; the founder pointed out that each was a copy of a business concept, coupling and duplication at every seam.

## Decision

1. **A rider is a service business whose service is delivery.** They sign up as any business does, answering "I deliver for businesses", which is kind `service` with a Delivery service on their services table (areas, vehicle, price) and chat tools on. They have the business profile, a Taskiem conversation, and are found in Explore by what they offer, as any business is. No new kind, profile, or module.
2. **A business adds a rider by linking with them as a supplier**, and the rider shares a price list with it, which is how a supplier gives one buyer a price. A legacy rider (a phone with no business on Taskiem) keeps working exactly as today, as a supplier not on Taskiem receives a purchase order by message, and may claim their rows by signing up.
3. **A delivery is a link order of kind `delivery`** the business places with the rider: one line, the Delivery service and its fee; the pickup, the drop, and the customer's details sealed while held; the order card's steps with Arrived added. Dispatch's rotation, offer window, door payment, cash and quiet-rider alerts act on that order; `deliveries` stays for legacy riders.
4. **The rider's tools, alerts and briefing are a seller's**, shown in the rider's shape because of what their business is. One small tool is added, Availability, writing the presence that exists; a dispatch company's riders are staff with `order.handle`.
5. **Fees are the rider's invoices; cash held is the business's invoice to the rider.** Each is paid as bills are paid between linked businesses, once, matched on both sides, to the bank on the rider's business. Nothing new moves money.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A rider organisation of its own: kind `rider`, a rider profile, link role `rider`, rider tools and roles, mirrored `rider_jobs`, `rider_payouts_received`, `rider_remittances_owed`, an `engine/rider` module (the first draft) | Each piece is a business concept with "rider" written on it: a second sign-up, a second profile, a third link role, a second order mirror, a second payment mirror, a second staff role. Every one couples the business code to riders and must be kept in step; a service business with a Delivery service already has all of it |
| A rider app, or a rider mode inside a business's account | A rider works for several businesses and needs one identity, one availability and one bank across them, which only a business of their own gives |
| A rider as a person (a user) linked to businesses | Taskiem has no relation between a person and a business other than membership, and a rider is not a member of the businesses they deliver for; the link between businesses already carries orders, prices, settings, a conversation and consent |
| Leave riders as contacts and give them a web page from the tap-in link | A view, not a participant: no availability, earnings or bank of their own, no Explore, no conversation. The tap-in path stays as the legacy rider's |
| A fleet connector for dispatch companies | A company is a service business with staff; its riders hold `order.handle` and its own rotation dispatches inside it |

## Consequences

- One new order kind with a few fields and a step; one new sign-up answer; one small tool; the services table carrying areas and a vehicle. No new tenant kind, link role, permission, module or core table.
- `engine/dispatch` places a link order for a linked rider and keeps its rows for legacy riders; `engine/links` and `engine/invoicing` learn the delivery kind and the weekly draft.
- Any time-bound service can later take orders with a window and rotation, since nothing in the change is about riders but the Delivery service.
- The paying-people plan's payee of kind rider is for riders not on Taskiem; a linked rider is paid as a supplier. The five-flows plan's customer orders end in a delivery a rider holds as an order.
- Five phases; the delivery as a link order first.
