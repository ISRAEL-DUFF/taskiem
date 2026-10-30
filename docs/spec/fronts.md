# Fronts: a storefront with any number of pages, each with its own things and its own actions

Status: **Draft** 30 October 2026, for review · Decision: [0059](../decisions/0059-fronts.md) (proposed) · Builds on: [storefronts](../storefronts.md) ([0036](../decisions/0036-storefronts.md)), [forms](../forms.md) ([spec](forms.md)), [data tables](../data-tables.md), [stock](stock.md) ([0057](../decisions/0057-stock-by-chat.md)), [dispatch](dispatch.md), [invoices and the money log](invoices-and-money-log.md), [public page themes](../decisions/0042-public-page-themes.md)

## 1. Summary

A shop that also sells food, a tailor who also hires out chairs and canopies, a church that sells books and takes bookings for its hall: one business, several faces. Today a business has one storefront with one catalogue and four fixed forms (order, enquiry, call-back, booking), and one workflow per form. That is why the owner who pasted a delivery workflow for the food side was refused: the shop's order form already had its workflow.

A **front** is a page under the storefront with things on it and ways to act on them, and nothing more specific than that. The things are blocks: text, photos, and any of the business's data tables shown as a list. The ways to act are forms, any number of them, each starting its own workflow, as [forms](../forms.md) already do. What makes an action feel like ordering, booking or hiring is a **field** the form uses (a basket from a table, a time from a calendar, dates and a quantity against counts, a quantity against a count), not a kind of front. A new way of selling later is a new field type, never a new kind of front.

Owners do not assemble fronts from blocks. They pick a **kind**: Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Questions, Call-backs, or Custom (§6). A kind is a preset over the model: a table shape, a form with its fields, a workflow template, settings and chat words, so "Add a Hire front" gives canopies with counts and a day rate, a dates-and-quantity form, a deposit, due-back and returned, and `add item … to Canopies`, with nothing to assemble. A kind is data, not a code path: every front a kind makes is an ordinary front that can be opened up and changed, and a new kind later is a new preset. Custom is the escape hatch for what no kind covers.

The storefront stays the business: name, look, hours, number, domain, money. Fronts sit under it, each with its own name, link, photos and actions, and the storefront's home page lists them. Today's catalogue and four forms become a Shop front (and a Services front where there is a services table) on the storefront, and nothing an existing storefront does changes.

Eight milestones, F1 to F8; F1 to F3 give the owner who started this what they asked for.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Storefront | The business's public site, as today: card, look, hours, domain, the money behind it. One per business |
| Front | A page under the storefront: a name, a link (`/<front>`), a header photo, blocks and actions. A storefront has one or more; the first is the one that exists today |
| Block | Something shown on a front: text, photos, or a **list block**, one of the business's data tables with the columns the owner chose to show (name, price, photo, description, availability) |
| Action | A button on a front that opens a form, which starts a workflow on each submission. The form is the workflow's inputs schema ([forms](../forms.md)); a front has any number of actions, each with its own workflow |
| Field | A question on a form. Besides the types forms have today, this plan adds fields that draw on a front's list blocks: a **basket**, a **time**, **dates and quantity**, and **quantity against a count** |
| Kind | A preset that makes a front: the table shape, the form and its fields, the workflow template, the settings and the chat words. Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Questions, Call-backs, Custom (§6). The front it makes is an ordinary front |
| Submission | What a customer sent through an action: today's order, question, call-back or booking, generalised. It keeps its reference, status, customer and money, and names its front and action |

## 3. Where we are

| Exists | Where |
| --- | --- |
| One storefront per business (`storefronts.tenant_id` unique), at `<slug>.<preview domain>` or its own domains; the card, the look (the business's, for every public page), hours, closed and reopen | [storefronts](../storefronts.md), migration 00179 |
| One catalogue: a data table mapped to name, price, photo, description, category, availability and stock, copied into `storefront_catalogue` and refreshed; the stock work treats it as the stock | [storefronts: catalogue](../storefronts.md#the-catalogue-copy), [stock](stock.md) |
| Four fixed actions, `storefront_actions (storefront_id, kind)` with `kind IN (order, enquiry, callback, booking)`, a label, a workflow, payment and options; the public forms at `/a/<kind>`; status pages at `/o/<reference>` | migration 00180, 00181; `engine/publicweb/forms.go`, `booking.go` |
| One workflow per form per environment: the `storefront` trigger with `action` (`triggers.storefront_action`), refused when another workflow holds it | `engine/ingest/triggers.go` (the error the owner met) |
| Submissions as `storefront_orders` (`kind`, `reference`, items, customer sealed, total, status, payment) and `storefront_bookings` with a services table (`storefront_services`), one calendar per business | migrations 00180, 00181 |
| The `storefront@1` connector: `decide`, `payment_link`, `notify_customer`, `get_booking`, `refund`, and the templates `storefront-order`, `-enquiry`, `-callback`, `-booking`, `dispatch-orders` | [storefronts: workflow actions](../storefronts.md#workflow-actions) |
| Hosted forms that start a workflow: the `form` trigger, pages, `show_if`, `verify`, `once_per`, `after`, fields as the inputs schema with `x-form` hints; types text, number, email, phone, URL, date, time, choices, yes/no, consent, files; live at `forms.<domain>/f/<id>` | [forms](../forms.md) |
| Chat: `make my page`, `turn on orders`, `add product`, `add service`, `orders`, `order <ref> ready`, `turn on deliveries` | [storefronts: from chat](../storefronts.md#from-chat) |
| Orders page, the daily summary, matching and the money log keyed by order reference; delivery areas and riders on an order | [invoices and the money log](invoices-and-money-log.md), [dispatch](dispatch.md) |

## 4. What stays as it is

- **The storefront is the business.** One per business; its slug and domains, card, look, hours, closed and reopen, QR code, the Chat on WhatsApp button, which connection takes payments. A front has none of these of its own; it inherits them.
- **Money.** A submission with a total is paid, matched and refunded exactly as an order is today, through the same connection, into the same money log, with the front named on it. No second money log, no second Paystack.
- **Forms are the mechanism.** A front's action is a hosted form in every respect: its questions are the workflow's inputs, it is deployed with the workflow, its checks, `verify`, `once_per`, files and the refused-field rules apply. This plan adds field types and a place to put a form; it does not add a second form system.
- **Existing storefronts change nothing.** The migration makes each storefront's first front from what it has, with its catalogue as a list block and its enabled forms as actions, at the same addresses. An owner who never adds a front never sees the word.
- **The one-workflow-per-form rule.** Kept, at the right size: one workflow per action. Two fronts never collide, because the action, not the storefront, owns the workflow.

## 5. The model

```
storefront (one per business)
  └─ fronts (ordered; the first is the home page's own content)
       ├─ blocks: text | photos | list (table, shown columns)
       └─ actions: label, form (a workflow's trigger), payment, options
            └─ submissions (reference, status, customer, lines, total, payment)
```

### 5.1 Fronts

`storefront_fronts`: id, storefront, tenant, `slug` (letters, digits, hyphens; unique per storefront; the first front's is empty, so the storefront's home stays at `/`), `name`, `tagline`, `photo`, `position`, `enabled`, `options`. A front's address is `/<slug>` on the storefront's host and every domain; its status pages stay at `/o/<reference>`. The home page shows the card, then the first front's blocks and actions as today, then **the other fronts** as cards (photo, name, tagline) when there are any. A front with `enabled` off is not linked and answers 404, so an owner can build one before showing it.

### 5.2 Blocks

`storefront_blocks`: front, position, `kind` (`text`, `photos`, `list`), `config`. A list block names a data table and its mapping (the catalogue mapping of today: name, price, photo, description, category, availability, stock; any may be absent), plus which columns show and a `group_by`. The copy and refresh that `storefront_catalogue` does today become per list block (`storefront_lists`, keyed by block), so a table used by two fronts is copied twice and each front can show it differently; the stock work's "the catalogue table is the stock" holds for every list block's table. The catalogue of today is the first front's one list block.

### 5.3 Actions

`storefront_actions` loses its `(storefront_id, kind)` key and gains `id`, `front_id`, `position`; `kind` becomes what the form's fields make it (§5.4) and is kept only for the four built-in actions' migration and for chat's words. An action has a `label`, a `workflow_id` whose trigger is `{"type": "storefront", "config": {"action": "<action id>"}}` (the trigger names the action, no longer a kind; the uniqueness check in `registerTriggers` is per action id), `payment` (`at_submit`, `after_accept`, `none`; for bookings also `deposit` with a percentage, as today), and `options` (fulfilment, delivery areas, `verify`, `once_per`, `after`, the things the order and booking settings hold today).

The form is the workflow's: the action's page is rendered by the hosted-forms renderer from the workflow's inputs schema, under the storefront's look and at `/<front>/a/<action slug>`, with the storefront's honeypot, limits, language and status pages. An action made from a template (§7) starts with the template's form; the owner edits it where forms are edited today.

### 5.4 Fields that draw on the front

Three field types, each declared in the inputs schema with `x-form.widget` and a reference to one of the front's list blocks:

| Field | Schema | What the customer sees | What the run gets |
| --- | --- | --- | --- |
| **basket** | `{"type": "array", "x-form": {"widget": "basket", "list": "<block>", "min": 1}}` | The list with quantities, a running total in naira, the price check as today | `[{row_id, name, unit_price_kobo, qty}]`, `total_kobo`, and `trigger.submission` (§5.5) |
| **time** | `{"type": "object", "x-form": {"widget": "time", "list": "<services block>"}}` | A service from the list, then a day and a slot from the calendar, with the booking rules (hours, days off, how far ahead, buffers, capacity) | `{row_id, service, starts_at, ends_at, price_kobo}` |
| **dates and quantity** | `{"type": "object", "x-form": {"widget": "hire", "list": "<block>"}}` | An item, a from and to date, a quantity; refused beyond the item's count for those dates, counting other hires that overlap | `{row_id, item, from, to, qty, unit_price_kobo, days, total_kobo}` |
| **quantity against a count** | `{"type": "object", "x-form": {"widget": "count", "list": "<block>"}}` | An item (an event, a class) and how many; refused beyond what is left of the item's count, counting other confirmed submissions | `{row_id, item, qty, unit_price_kobo, total_kobo, left}` |

A form may use any of them, or none: a "request a quote" action is plain fields; a "hire for an event" action is a dates-and-quantity field plus a delivery address; a "book the hall" action is a time field with a deposit; "buy tickets" is a quantity against the event's count. The basket is today's order form; the time field is today's booking form. The hire field's counts come from the list block's stock column, and a confirmed hire is a stock movement out and, on return, back in (the stock work's `outside` and `received` kinds, with `hire` as the reason), so the low list and shrinkage see hires. The count field's "left" is the item's count less confirmed submissions, kept on the list copy and checked in the submitting transaction with the row locked, as stock sales are.

Calendars: one per **time field's list block**, not one per business, so a salon's chairs and the hall can be booked separately. The booking rules (hours, days off, lead time, cut-off, confirm automatically) move from the storefront to the list block's options, defaulting to the storefront's.

### 5.5 Submissions

`storefront_orders` becomes `storefront_submissions` (a rename with a view under the old name for a release): `front_id`, `action_id`, `reference` (the same shape, `AK-7QX4KD`), `status`, the customer sealed, `lines` (basket lines, the service and time, the hire), `total_kobo`, `payment`, `fulfilment`, `delivery`, the rider fields dispatch added. `storefront_bookings` folds in as submissions whose lines carry a time, keeping the move and cancel flows. A run sees `trigger.body` (the form's answers) and `trigger.submission` (`reference`, `front`, `action`, `total_kobo`, `payment`, `customer` masked, `status_link`), which is what `trigger.body.reference` and friends are today, kept under the old names for existing workflows.

The `storefront@1` actions (`decide`, `payment_link`, `notify_customer`, `get_booking`, `refund`) work by reference as today, for every submission; `get_booking` answers for any submission with a time; a new `get_submission` returns the whole thing. `notify_customer`'s `say` messages gain the hire ones (`hire_confirmed`, `hire_due_back`, `hire_returned`).

## 6. Kinds of front: the presets

A kind is a row in a catalogue Taskiem ships (`storefront/kinds/*.json`, like the template library): a name and a line for the owner, the table shape it makes (or the mapping it asks for over a table the owner has), the actions it adds with their forms and workflow templates, the settings each action shows, the chat words, and what the public page shows for an item. Picking a kind makes an ordinary front: the owner can then add blocks and actions, change the questions, or swap the workflow, and nothing about it is special afterwards. Kinds are the words owners, chat and the AI builder use; the model underneath never changes for a new kind.

| Kind | For | Table it makes | Actions, fields and template | Settings | Chat |
| --- | --- | --- | --- | --- | --- |
| **Shop** | A shop's products: provisions, fashion, phones, cosmetics | `products`: name, price, photo, description, category, available, stock (today's) | **Order**: basket · `storefront-order`; **Ask a question**: plain · `storefront-enquiry` | Payment (after accept, at order, none), pickup and delivery with areas, the payments connection (today's) | `add product … [to <front>]`, `price`, `hide`, `show`, `stock`, `turn on orders`, `turn on deliveries` (today's) |
| **Food** | A kitchen, a restaurant, snacks and baked goods | `menu`: name, price, photo, description, category, available, **options** (size, extras with their prices), **ready in** (minutes) | **Order**: basket with options and a ready-by line · `storefront-order` with delivery on by default, `dispatch-orders` when riders are on | Shop's, plus opening times for orders (a kitchen closes before the shop), a minimum order for delivery | Shop's words, with `add dish`, `sold out <dish>`, `kitchen closed` |
| **Services** | A barber, a salon, a tailor's fitting, a tutor, a clinic | `services`: name, price, duration, capacity, buffer (today's) | **Book a time**: time · `storefront-booking` | Booking hours, days off, how far ahead, cut-off, confirm automatically, deposit (today's, now per front) | `add service …`, `turn on bookings` (today's) |
| **Hire** | Chairs, canopies, cooking pots, sound equipment, generators, cameras | `hire_items`: name, photo, description, **count**, **day rate**, deposit (percentage or amount), available | **Hire for dates**: dates and quantity, plus a delivery address · `storefront-hire` (approval, deposit by payment link, due-back reminder, **Returned**, stock out and back) | Deposit default, delivery and collection, the days a hire may run, late fee per day | `add item … 20 at 1500/day to Canopies`, `hire AK-7QX4KD returned`, `due back` |
| **Spaces** | Halls, event centres, a church's auditorium, a training room, a studio | `spaces`: name, photos, description, **capacity**, rate per day or per hour, deposit, what is included | **Book the space**: time with whole-day or hour slots on the space's own calendar · `storefront-booking` with a deposit and a site visit option | Days and hours bookable, minimum hours, deposit, cleaning or setup time between bookings | `add space …`, `bookings for Hall` |
| **Tickets** | An event: a concert, a seminar, a conference, a party, a match | `events`: name, photo, description, **date and time**, venue, **tickets** (count), price, a ticket type per row (regular, VIP) | **Buy tickets**: quantity against the count, paid at order · `storefront-tickets` (a ticket with a reference per seat, sent by SMS and WhatsApp; **check in** by reference or QR from chat) | Sales close at, tickets per person, refund rule | `add event …`, `tickets left`, `check in <ref>` |
| **Classes** | A course, lessons, a workshop, a bootcamp, coaching | `classes`: name, description, **starts**, sessions (count and days), **places**, fee, instructor | **Enrol**: quantity against places (usually one) with the learner's details · `storefront-classes` (enrol, pay in full or by instalment, reminders before each session, a certificate line at the end) | Instalments, waiting list when full, the reminder times | `add class …`, `places left`, `learners` |
| **Quotes** | Tailoring, catering, printing, furniture, repairs, anything made to order | none required; optionally `work` for examples with photos and from-prices | **Request a quote**: plain fields, photos, a date needed by · `storefront-quote` (the owner prices it in chat or the web app, the customer accepts, a payment link, a deposit, done) | Deposit, how long a quote stands | `quote AK-7QX4KD 45000`, `quotes` |
| **Questions** | A question about anything on the page | none | **Ask a question**: plain · `storefront-enquiry` (today's) | — | today's |
| **Call-backs** | "Call me" | none | **Request a call-back**: phone and a time · `storefront-callback` (today's) | — | today's |
| **Custom** | What no kind covers | the owner's choice | blocks and actions by hand; any template or a blank form | per action | `add front … custom` |

**What the kinds share.** Every submission, whatever the kind, is one `storefront_submissions` row with a reference, a status, the customer sealed, lines and money, so the Orders page, the daily summary, matching and refunds treat a ticket, a hire and a dish alike. Every kind's workflow is an ordinary workflow the owner can open. Every kind's table is an ordinary data table with the stock work's rules where it has a count.

**Adding a kind later** is a preset file, a template, the chat words in five languages and, where it needs one, a field type. The field types are the only code: basket, time, dates-and-quantity and count cover every kind above, so Tickets and Classes share one field, and Hire and Spaces share the calendar and the count.

**Migration into kinds.** An existing storefront's first front becomes a **Shop** front (its catalogue, its order and enquiry actions) and, where it has a services table and a booking form, a **Services** front; its call-back action stays on the Shop front. Nothing moves on the public page.

## 7. What the owner sees

### 7.1 The web app

Storefront gains a **Fronts** tab beside Page, Look, Forms and Orders:

- The list of fronts as rows (photo, name, kind, link, on or off, what it has: "12 items, 2 actions"), **Add a front**, drag to order.
- **Add a front** asks for a name and a kind (§6), as cards with one line each, Custom last. The kind makes the table (or asks which table the owner already has and how its columns map), the actions and the settings, and opens the front's page with **Add your first item**.
- A front's page: name, tagline, photo, the address shown; its items (the kind's table, edited in place as the catalogue is today); **Blocks** (add text, photos, or a list from one of the business's tables, choosing the columns and the grouping); **Actions** (the kind's, plus add from the templates: Order from this list, Book a time, Hire for dates, Buy tickets, Enrol, Ask a question, Request a call-back, Request a quote, or **Start from a blank form**; each row shows its label, its workflow, payment, on or off). An action's settings are the kind's settings for that action. **Edit the questions** opens the form editor the forms work has.
- The existing **Forms** tab becomes the Shop front's actions, so the four switches an owner knows stay where they are.
- **Orders** gains a front filter and a column; a submission's page names its front and action. The hire ones show from, to, due back and returned.
- Delivery areas and riders stay the storefront's and apply to any action whose options turn fulfilment on.

### 7.2 Chat

- `add front Snacks` asks the kind as a list (Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Custom); `add food front Snacks`, `add hire front Canopies` skip the question. The front is made off, with its table, and the reply says what to add first (`add dish …`, `add item …`) and offers **Turn it on?** once it has an item. `fronts` lists them with their kinds. `front Snacks on`, `front Snacks off`.
- Each kind's words (§6): `add product … to Snacks`, `add dish …`, `add item … 20 at 1500/day to Canopies`, `add space …`, `add event …`, `add class …`. Without `to <front>` the Shop front, as today; when a word fits one front only (`add dish` with one Food front) it goes there. `price`, `hide`, `show`, `stock`, `sold out` likewise, with the front's name when the item is in more than one.
- `turn on orders for Snacks`, `turn on bookings for Hall`, `turn on hire for Canopies`, `turn on tickets for Concert`: the kind's action from its template, its workflow published from chat with the checks the Forms page makes, as `turn on orders` does today. `turn on deliveries for Snacks` publishes the delivery workflow for that front's order action, which is the exact case that started this.
- `orders` shows the front beside each; `orders Snacks` filters. `order <ref>` and the rest are unchanged.
- The storefront's existing commands keep their meaning for the first front, so nothing an owner learned stops working.

### 7.3 Customers

The storefront's home: the card, the first front's content, then the other fronts as cards. A front's page: its header, its blocks, its action buttons; the order and booking pages look as they do today, in the business's look. A link to a front (`adas-kitchen.taskiem.ng/snacks`) shares as a card with the front's name and photo. Status pages, payment pages, updates on WhatsApp, the price check: unchanged, per submission.

## 8. Templates and the builder

- The four storefront templates and `dispatch-orders` become **action templates** with a `front` parameter: the same definitions, the trigger pointing at the action, the form inherited from the template. New, one per kind that lacks one: `storefront-hire` (a hire request, an approval, deposit through `payment_link`, due-back reminder from the hire's `to` date, **Returned** by the owner or from chat, a stock movement each way), `storefront-tickets` (paid at order, a ticket per seat with its reference sent by SMS and WhatsApp, check-in from chat, the count released on refund), `storefront-classes` (enrol, pay in full or by instalment through `payment_link`, a reminder before each session, a waiting list when full) and `storefront-quote` (plain fields, an approval that types the price, the customer's acceptance from the status page, `payment_link`). Spaces uses `storefront-booking` with a deposit; Food uses `storefront-order` and `dispatch-orders`.
- The AI builder's storefront rules gain kinds: "sell snacks on my page too" makes a Food front; "let people hire my canopies" a Hire front; "sell tickets for my concert" a Tickets front; the builder names the kind and never assembles blocks unless asked for something no kind fits; one eval case per kind.
- The one-workflow-per-action rule reaches the editor: publishing a workflow whose `storefront` trigger names an action that another workflow holds shows "This action already runs **Storefront orders**" with **Replace it with this one** (the swap `turn on deliveries` does, in one transaction, refused under four-eyes with the reason) and **Make a new action for this workflow** (a copy of the action on the same front, off, with this workflow). This fixes the dead end that started this plan whether or not fronts are ever added.

## 9. Migration

One migration, no downtime, the storefront unchanged to a customer:

1. For every storefront, a first front of kind Shop (`slug` empty, the storefront's name, `position` 0, enabled) and, where there is a catalogue, one list block from the catalogue mapping with `storefront_catalogue` moved under it; where there is a services table, a Services front (`slug` `services`, off until its booking action was on) with a list block for it. The booking page keeps its old path (§9, last line).
2. Every `storefront_actions` row gets an id, `front_id` the first front's, and its form (the template's for the four kinds). Its trigger rows get the action id; `triggers.storefront_action` is replaced by `storefront_action_id`.
3. `storefront_orders` and `storefront_bookings` become `storefront_submissions` with the first front and the matching action; views under the old names for one release so reports and exports keep working.
4. Nothing moves in the money log, matching or dispatch: they key by reference.

The public paths of today (`/`, `/a/order`, `/a/booking`, `/o/<ref>`) keep serving: `/a/<kind>` resolves to the first front's action of that kind.

## 10. Security and abuse

| Concern | Control |
| --- | --- |
| A form that asks for what forms must never ask | The hosted-forms refusals apply to every action's form at deploy, as now |
| A front that shows a table with personal data | A list block shows only the mapped columns, and a table with an `x-pii` column cannot be mapped to a shown column; the copy holds only shown columns, as `storefront_catalogue` does |
| A basket or hire priced by the customer | Prices and counts come from the copy on the server; the price check and the hire's overlap check run at submit, never from the form's numbers |
| Hire fraud (take and not return) | The deposit through `payment_link`, the due-back reminder, the owner's approval before it goes out; a customer with an unreturned hire is refused a new one on that storefront (by verified phone, as `once_per` works) |
| Flooding through many actions | The storefront's submission limits apply per storefront, not per action; the honeypot and `verify` per action as today |
| A front enabled by mistake | Off by default; the owner turns it on; the audit records `storefront.front.enable` |
| Cross-tenant | Every new table carries `tenant_id` and the storefront's row-level security policy; the public renderer resolves the front under the storefront it is serving |

## 11. Testing

- **The migration**: every e2e storefront test passes unchanged after it (the page, order, booking, deliveries, chat), and the old paths serve.
- **Kinds**: each kind made from chat and from the web app gives a front with its table, actions and settings; an item added through the kind's words shows on the page; the preset files are checked against the schema and every template they name exists.
- **Fronts**: a second front with its own table and order action, and the first front's order workflow untouched; a delivery workflow on the second front's order action while the first keeps the plain one (the case that started this); a hire action refusing an overbooked quantity and moving stock out and back; a time field with its own calendar.
- **Forms**: the three field types through the forms harness: schema, render, submit, `trigger.body` shape.
- **Editor**: the replace and the new-action choices on a held action; four-eyes refusal.
- **Chat**: `add front`, `add product … to`, `turn on orders for`, `turn on deliveries for`, `orders <front>`.
- **Axe and plain words**: the Fronts tab and a front's page, both themes; the owner's words (Appendix C) on every new screen and message.

## 12. Needs a person

| Item | Who | Blocks |
| --- | --- | --- |
| The customer-facing word for a front on the public page ("Our kitchen", "Hire", a plain section heading, or no word at all) and whether fronts show as cards or as a menu bar | Founder, with the brand | F3 |
| Whether hires take a deposit by default and at what percentage | Founder | F5 |
| The kinds' names and one-line descriptions as owners should read them in the five languages; which kinds show first | Founder, reviewers | F3 |
| Tickets: whether a ticket is a QR code (a check-in app on the owner's phone is then the chat's camera) or a reference alone | Founder | F6 |
| The hire messages in five languages | Reviewers | F5 |

## 13. Milestones

| Id | Milestone | Size |
| --- | --- | --- |
| F1 | The model and the migration: fronts, blocks, actions by id, submissions; the trigger by action id; the kind catalogue and its schema; views under the old names; every existing test green | M |
| F2 | The editor's dead end fixed: Replace it with this one, Make a new action for this workflow; the uniqueness error names the action and the front | S |
| F3 | Fronts in the web app and on the public page with the kinds that exist today as presets: **Shop**, **Services**, **Questions**, **Call-backs**, and **Food** (options, ready-in, delivery by default); the Fronts tab, Add a front by kind, blocks, actions from templates, the home page's front cards, Orders by front; the action templates with the `front` parameter | L |
| F4 | Chat: `add front` with the kind, the kinds' words, `… to <front>`, `turn on … for <front>`, `orders <front>`, the five languages | M |
| F5 | **Hire** and **Spaces**: the dates-and-quantity field, `storefront-hire`, the overlap check, stock movements out and back, deposit, due back, returned; calendars per list, whole-day and hour slots | M |
| F6 | **Tickets** and **Classes**: the count field, `storefront-tickets` with check-in, `storefront-classes` with instalments and the waiting list | M |
| F7 | **Quotes** and **Custom**: `storefront-quote` with the customer's acceptance, blank forms; the builder's kinds and evals; docs ([storefronts](../storefronts.md) rewritten around fronts and kinds, [forms](../forms.md) with the four fields, [stock](stock.md) with hire and counts, the owners' guide "Sell more than one thing") | S |
| F8 | The decision accepted with what was built; the atlas's business modules page updated | S |

In order, one branch and one PR for F1 to F3 (the owner's case), then one each. F2 can ship alone first if the pilot needs it. The kinds after F3 can be reordered by what owners ask for first.

## 14. Risks and open questions

| Risk or question | What we do |
| --- | --- |
| The migration touches every storefront | A view under each old name, the old paths kept, every storefront e2e run before and after; the migration is reversible by the views alone |
| Two list blocks on the same table drift apart | Each block's copy refreshes from the table on the same trigger; the stock work writes the table, never a copy |
| The forms renderer and the storefront renderer differ in look | They already share the business's look (0042); the action pages move onto the forms renderer with the storefront's chrome, and the order and booking pages are the first to prove it |
| Owners make a front per product | Add a front asks for a kind, and a kind is a whole line of business, which says what a front is for; the builder prefers an item on an existing front for one more product |
| The kinds multiply and drift from the model | A kind is a preset file checked against one schema, and every front it makes is an ordinary front; a kind that needs new code is a new field type, reviewed as one |
| A hire's overlap count under concurrent submissions | The check and the movement run in one transaction with the row locked, as stock sales do |
| Calendars per block break the "one calendar" assumption in reminders and the owner's bookings page | The bookings page groups by calendar; reminders key by submission already |

## 15. Out of scope

Branches or locations of one business (a front is not a place); kinds beyond the eleven here (subscriptions and memberships, donations and offerings, a marketplace of sellers) until an owner asks, each a preset when it comes; several storefronts per business; a marketplace of fronts across businesses; a second money log; customer accounts across fronts beyond the status link; fronts on a business's own WhatsApp public menu beyond listing them (the menu gains "which front?" only when a number serves a storefront with more than one, a later step).
