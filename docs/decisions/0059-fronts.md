# 0059 — Fronts: a storefront is pages of things and actions, not one catalogue and four forms

Date: 2026-10-30 · Status: **Proposed** · Plan: [fronts](../spec/fronts.md)

## Context

A storefront ([0036](0036-storefronts.md)) is one page: one catalogue table and four fixed forms (order, enquiry, call-back, booking), each with one workflow. A business that sells more than one kind of thing, or takes more than one kind of request, does not fit: the shop that also cooks, the tailor who hires out chairs, the church with a bookshop and a hall. The founder met it directly: a delivery workflow for the food side was refused because the shop's order form already had a workflow.

The first answer considered was a second business. It was wrong for the case at hand (one business, one money log, one number, with the WhatsApp number's display name fixed to one business), and a second business is in any case a different question, kept as its own plan ([a second business](../spec/new-business.md)).

Forms ([forms](../forms.md)) already put any page of questions in front of any workflow, with the form as the workflow's inputs schema. The storefront's forms are special cases of that, hard-wired.

## Decision

1. **A front is a page under the storefront with blocks and actions, and nothing more specific.** Blocks are text, photos, or any of the business's data tables shown as a list. Actions are forms, any number, each starting its own workflow. A storefront has one or more fronts; the first is what the storefront shows today.
2. **Forms are the only mechanism for an action.** An action's page is the hosted form of its workflow, with the forms work's checks, verification, limits and refusals, rendered under the storefront's look at the front's address.
3. **Ordering, booking and hiring are fields, not code paths.** A basket from a list, a time from a calendar, dates and a quantity against counts, a quantity against a count. A form uses any, or none. A new way of selling is a new field type, never a new kind of storefront.
4. **Kinds are presets over the model, and what owners see.** Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Questions, Call-backs and Custom: each a table shape, a form with its fields, a workflow template, settings and chat words, kept as data checked against one schema. Picking a kind makes an ordinary front that can be opened up and changed; a new kind is a new preset, with a new field type only when none of the four fits. Custom is the escape hatch, never the default.
5. **One workflow per action**, not per storefront form. The `storefront` trigger names the action. The editor offers to replace the action's workflow or to make a new action for the new workflow, instead of refusing.
6. **The storefront stays the business.** Its card, look, hours, domains, number, delivery areas, riders, payment connection and money log serve every front. There is one money log and one set of submissions, each naming its front and action.
7. **Existing storefronts migrate in place.** A Shop front takes the catalogue and the enabled forms, a Services front the services table; the public paths keep serving; owners who never add a front never meet the word.
8. **A calendar belongs to a list, not to the business**, so a time field on one front books independently of another's.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A second business per kind of thing sold | Splits the money, the number, the staff and the plan of what is one business; the WhatsApp display name is one per number |
| Several storefronts per business | Each would need its own slug, domains, look and hours, which the owner would set twice; what differs between a shop and its kitchen is what is on the page and what can be done, not the business card |
| Only fixed kinds (an order form, a booking form, a hire form, a quote form), each its own code | Every new way of selling would be new code, a new table and a new set of chat words; the question would come back the moment the list ran out |
| Only the generic model, with no kinds | Owners would assemble blocks and fields to sell bread; the words owners, chat and the builder use would have nowhere to live |
| Categories in one catalogue | Categories cannot carry a different form, payment, fulfilment or workflow, which is what the kitchen needs and the shop does not |
| Fronts with their own forms system, separate from hosted forms | Two ways to put a page in front of a workflow, each with its own checks and renderer, drifting apart |

## Consequences

- New tables `storefront_fronts`, `storefront_blocks`, `storefront_lists`; `storefront_actions` keyed by id with a front; `storefront_orders` and `storefront_bookings` become `storefront_submissions` (views under the old names for a release); `triggers.storefront_action` becomes an action id.
- Four form field types (`basket`, `time`, `hire`, `count`) in the forms schema and renderer; the storefront's order and booking pages move onto the forms renderer.
- A kind catalogue (`storefront/kinds/*.json`) with its schema; the storefront templates become action templates with a `front` parameter; `storefront-hire`, `storefront-tickets`, `storefront-classes` and `storefront-quote` are new; `dispatch-orders` is an action template.
- Chat gains `add front`, `… to <front>`, `turn on … for <front>`, `orders <front>`.
- Stock: a confirmed hire moves stock out and, on return, back in.
- Docs: storefronts rewritten around fronts; forms, stock and the owners' guide updated; the atlas's business modules page.

## Provenance

From the founder on 29 and 30 October 2026, after a delivery workflow was refused on a storefront that already had an order workflow: "It's one business, the same shop also selling food… keep the single storefront but the storefront can have multiple Pages called fronts each dedicated to specific items or category of items… Fronts should be generic, it could be of any type or thing, it could have any number of workflows… flexible enough to be used in any form, otherwise we will come back to this conversation the moment we exhaust its use case."
