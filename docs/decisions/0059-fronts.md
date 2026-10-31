# 0059 — Fronts: a storefront is pages of things and actions, not one catalogue and four forms

Date: 2026-10-30 · Status: **Proposed** · Plan: [fronts](../spec/fronts.md)

## Context

A storefront ([0036](0036-storefronts.md)) is one page: one catalogue table and four fixed forms (order, enquiry, call-back, booking), each with one workflow. A business that sells more than one kind of thing, or takes more than one kind of request, does not fit: the shop that also cooks, the tailor who hires out chairs, the church with a bookshop and a hall. The founder met it directly: a delivery workflow for the food side was refused because the shop's order form already had a workflow.

The first answer considered was a second business. It was wrong for the case at hand (one business, one money log, one number, with the WhatsApp number's display name fixed to one business), and a second business is in any case a different question, kept as its own plan ([a second business](../spec/new-business.md)).

Forms ([forms](../forms.md)) already put any page of questions in front of any workflow, with the form as the workflow's inputs schema. The storefront's forms are special cases of that, hard-wired.

## Decision

1. **A front is a page under the storefront with blocks and actions, and nothing more specific.** Blocks are text, photos, or any of the business's data tables shown as a list. Actions are forms, any number, each starting its own workflow. A storefront has one or more fronts; the first is what the storefront shows today.
2. **Forms are the only mechanism for an action.**
   - An action's page is a hosted form, with the forms work's checks, verification, limits and refusals, rendered under the storefront's look at the front's address.
   - The form is the action's own questions, not the workflow's inputs.
   - The run gets Taskiem's record of the submission (`trigger.submission`, and `trigger.body` as today for the built-in four) apart from the customer's answers (`trigger.answers`), so no answer can set a total or a reference.
3. **Ordering, booking and hiring are fields, not code paths.** A basket from a list, a time from a calendar, dates and a quantity against counts, a quantity against a count. A form uses any, or none. A new way of selling is a new field type, never a new kind of storefront.
4. **Kinds are presets over the model, and what owners see.** Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Questions, Call-backs and Custom: each a table shape, a form with its fields, a workflow template, settings and chat words, kept as data checked against one schema. Picking a kind makes an ordinary front that can be opened up and changed; a new kind is a new preset, with a new field type only when none of the four fits. Custom is the escape hatch, never the default.
5. **One workflow per action**, not per storefront form.
   - The `storefront` trigger keeps `action` and its four values, meaning the first front's built-in action of that kind, and gains an optional `action_id`.
   - This is an additive change to the frozen wd/v1 contract, recorded here: no existing definition changes meaning.
   - The editor offers to replace the action's workflow (first, alone, on today's model) or to make a new action for the new workflow, instead of refusing.
6. **The storefront stays the business.** Its card, look, hours, domains, number, delivery areas, riders, payment connection and money log serve every front. There is one money log and one set of submissions, each naming its front and action.
7. **Existing storefronts migrate in place.**
   - One Shop front takes everything the page has: the catalogue, the services and all four forms, so nothing moves on the public page.
   - The public paths keep serving.
   - Owners who never add a front never meet the word.
   - Tables keep their names: `storefront_orders` already holds every kind of submission and gains `front_id` and `action_id`.
8. **A calendar belongs to a list, not to the business**, so a time field on one front books independently of another's.
9. **Fronts are published, never live.** Fronts, blocks and actions are part of the storefront's draft and reach customers through the published snapshot. Turning a front on is a publish, with the verified-phone rule, moderation, the item limit and the first-publish hold.
10. **What is taken is claimed, not moved.**
    - A list's count is what the owner has.
    - Hires, tickets, places and booking slots are claims against it, expiring while unpaid, checked with the item locked in the public host's submitting function. Slot holds become claims, so there is one reservation model.
    - Stock (0057) stays on the catalogue table and is not moved by hires.
11. **A submission can be paid in parts.** It carries an amount due and its payments, as an invoice does; a payment link may be for a deposit or an instalment and may be made again until the balance is paid; the money log records each payment against the one reference, and matching's order path learns parts as the invoice path has them, so a deposit is a part, never a wrong amount. The order templates that exist keep paying in one step.
12. **The form has one owner, the action.** It is published with the storefront; a template copies its form into the action once; the workflow's checks report a question the workflow reads that the form no longer asks, and the storefront's publish refuses to remove a question a live workflow reads.
13. **A kind's table is a known table** in the registry decision 0060 sets up, so templates, chat, the builder and fronts read one definition.

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

- **New tables.** `storefront_fronts`, `storefront_blocks` and `storefront_claims`, each under forced row-level security, with the public host's reads granted column by column and listed in `dispatch_columns.txt`.
- **Changed tables.**
  - `storefront_actions` is keyed by id, with a front, a slug, `builtin` and the action's `form`, and `(storefront_id, kind)` stays unique for the built-in four.
  - `storefront_orders` gains `front_id`, `action_id`, `amount_due_kobo` and `paid_kobo`; `storefront_holds` folds into `storefront_claims` in F5.
  - `payment_link` takes an amount and may be repeated; the `storefront.payment` signal carries the amount and the balance; `get_submission` and `mark_returned` are new.
  - `storefront_catalogue` is copied per table.
  - Triggers gain `storefront_action_id`.
- **No rename and no views.** A real Down migration, tested up, down, up.
- **Every definer and query that finds an action by kind is moved to the action's id.** These are `taskiem_public_submit`, `taskiem_public_book`, the booking change function, the transfer-account read, and the `engine/storefront` queries. The old signatures stay as wrappers for a release.
- **wd/v1 gains the optional `action_id`**, with the SDK's types, the schema test and `wdtext`.
- **Forms.** Four form field types (`basket`, `time`, `hire`, `count`) in the forms schema and renderer. The order and booking pages move onto the forms renderer first, in F1.
- **Kinds and templates.**
  - A kind catalogue (`storefront/kinds/*.json`) with its schema.
  - The storefront templates become action templates with a `front` parameter, and `dispatch-orders` is an action template.
  - `storefront-hire`, `storefront-tickets`, `storefront-classes` and `storefront-quote` are new.
- **Chat** gains `add front`, `… to <front>`, `turn on … for <front>`, `front … on` (a publish) and `orders <front>`.
- **Limits.** `storefront_items` counts each copied table once. The monthly order and booking limits count submissions by what their form takes.
- **Docs.** Storefronts is rewritten around fronts. Forms, stock (what fronts do not move) and the owners' guide are updated, along with the atlas's business modules page.

## Provenance

From the founder on 29 and 30 October 2026, after a delivery workflow was refused on a storefront that already had an order workflow: "It's one business, the same shop also selling food… keep the single storefront but the storefront can have multiple Pages called fronts each dedicated to specific items or category of items… Fronts should be generic, it could be of any type or thing, it could have any number of workflows… flexible enough to be used in any form, otherwise we will come back to this conversation the moment we exhaust its use case."
