# 0065 — A dispatch rider is an organisation on Taskiem, linked to the businesses it delivers for

Date: 2026-11-08 · Status: **Proposed** · Plan: [chat: dispatch riders as participants](../spec/chat-riders.md)

## Context

The dispatch plan ([dispatch.md](../spec/dispatch.md), D1 to D6, built) made a rider a row inside one business: a sealed name and phone, reached on WhatsApp or Telegram with signed buttons, who answers an offer and taps three steps. The same person added by two businesses is two rows and one shared "presence" keyed by the phone's hash. The rider has no account, no view of their deliveries or earnings, and no bank of their own; each business holds one for them.

Chat tools (phases 1 to 7) made the conversation between two linked businesses the place where their trade happens, with tools, cards, diary lines and alerts for each side. The founder, on 8 November 2026, named the third participant: the rider is a business too, should be found and added as one, and should have the same experience in the chat, receiving deliveries, declaring availability and being paid in the app.

## Decision

1. **A rider is an organisation**, signed up as one (kind `rider`), with a profile, a Taskiem conversation, chat tools on, and a place in Explore under Dispatch when they choose to be found. A dispatch company is an organisation of kind `dispatch` whose riders are members with a built-in `rider` role.
2. **A business adds a rider by linking with them**: a `business_links` row with role `rider`, both sides consenting, with the pair's settings (the fee, the areas, proof, cash) on the link and the business's `riders` row. The rider's identity, availability, bank and rating are the rider's, kept once. A legacy rider (a phone with no organisation) keeps working exactly as today and may claim their rows by signing up.
3. **The delivery is a document in the conversation between the two.** The business's `deliveries` row stays the truth; the rider's `rider_jobs` row is a mirror written through `taskiem_link_enter`, as a bill mirrors an invoice, and both sides see one card with the same steps. Payouts and remittances mirror the same way; the hourly reconciler covers them.
4. **The rider has tools**: Deliveries, Availability, Earnings, Pay, Businesses, in the chat tools frame, with the offer, remittance, payout, rating and link request as alerts, and a briefing in their shape. WhatsApp and Telegram keep every word and button; the tools are the same work from the web.
5. **Money moves through the paths that exist.** A business pays a linked rider to the bank on the rider's organisation, read through a definer; the rider remits cash through their own connection with a pairing token, or by transfer or hand-in as today. Taskiem holds nothing.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A rider app, or a rider "mode" inside a business's account | A rider works for several businesses; they need one identity, one availability and one bank across all of them, which only an organisation of their own gives. A mode inside one business is today's row with a screen |
| A rider as a person (a user) linked to businesses, not an organisation | Taskiem has no relationship between a person and a business other than membership; a rider is not a member of the businesses they deliver for. The link between organisations already carries documents, settings, a conversation and consent; a person-to-business link would need all of that built again |
| Leave riders as contacts and add a web page they can open from the tap-in link | Gives a view, not a participant: no availability of their own, no earnings across businesses, no bank of their own, no Explore, no conversation. The tap-in link stays as the way a legacy rider works |
| A fleet connector for dispatch companies | A fleet is an organisation with riders; the dispatch module already dispatches inside an organisation. Platforms with their own apps remain a possible connector |

## Consequences

- A fourth kind of organisation and a third kind of link; Explore gains a Dispatch list; the sign-up gains an answer.
- Three mirrored records on the rider's side (jobs, payouts received, remittances owed) and a rider bank definer, all under the link's existing pattern and isolation test; no new way of crossing tenants.
- The business's riders code is unchanged for legacy riders and gains a branch for linked ones; the rider's side is a new module, `engine/rider`.
- The paying-people plan's payee of kind rider becomes a linked organisation when the rider has one; the five-flows plan's orders from customers end in a delivery a rider sees.
- Six phases; the rider's organisation first, the dispatch company last.
