# Fronts: a storefront with any number of pages, each with its own things and its own actions

Status: **Draft** 30 October 2026, for review (revised the same day after a review against the code: §3, §5, §9, §10, §13 and §14; and again for the review's gaps: part payments, Food's options, the Spaces calendar, one reservation model, the form's one owner, known tables shared with 0060) · Decision: [0059](../decisions/0059-fronts.md) (proposed) · Builds on: [storefronts](../storefronts.md) ([0036](../decisions/0036-storefronts.md)), [forms](../forms.md) ([spec](forms.md)), [data tables](../data-tables.md), [stock](stock.md) ([0057](../decisions/0057-stock-by-chat.md)), [dispatch](dispatch.md), [invoices and the money log](invoices-and-money-log.md), [public page themes](../decisions/0042-public-page-themes.md)

## 1. Summary

A shop that also sells food, a tailor who also hires out chairs and canopies, a church that sells books and takes bookings for its hall: one business, several faces. Today a business has one storefront with one catalogue and four fixed forms (order, enquiry, call-back, booking), and one workflow per form. That is why the owner who pasted a delivery workflow for the food side was refused: the shop's order form already had its workflow.

A **front** is a page under the storefront with things on it and ways to act on them, and nothing more specific than that. The things are blocks: text, photos, and any of the business's data tables shown as a list. The ways to act are forms, any number of them, each starting its own workflow, as [forms](../forms.md) already do. What makes an action feel like ordering, booking or hiring is a **field** the form uses (a basket from a table, a time from a calendar, dates and a quantity against counts, a quantity against a count), not a kind of front. A new way of selling later is a new field type, never a new kind of front.

Owners do not assemble fronts from blocks. They pick a **kind**: Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Questions, Call-backs, or Custom (§6). A kind is a preset over the model: a table shape, a form with its fields, a workflow template, settings and chat words, so "Add a Hire front" gives canopies with counts and a day rate, a dates-and-quantity form, a deposit, due-back and returned, and `add item … to Canopies`, with nothing to assemble. A kind is data, not a code path: every front a kind makes is an ordinary front that can be opened up and changed, and a new kind later is a new preset. Custom is the escape hatch for what no kind covers.

The storefront stays the business: name, look, hours, number, domain, money. Fronts sit under it, each with its own name, link, photos and actions, and the storefront's home page lists them. Today's page becomes the storefront's first front, a Shop front with the catalogue, the services and all four forms exactly where they are, and nothing an existing storefront does changes.

Nine milestones, F0 to F8. F0 alone gets the owner who started this unstuck. F1 to F3 give them a food front.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Storefront | The business's public site, as today: card, look, hours, domain, the money behind it. One per business |
| Front | A page under the storefront: a name, a link (`/<front>`), a header photo, blocks and actions. A storefront has one or more; the first is the one that exists today |
| Block | Something shown on a front: text, photos, or a **list block**, one of the business's data tables with the columns the owner chose to show (name, price, photo, description, availability) |
| Action | A button on a front that opens a form, which starts a workflow on each submission. The form is the action's own (its questions); the run gets the answers and Taskiem's record of the submission separately (§5.5). A front has any number of actions, each with its own workflow |
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
| Submissions as `storefront_orders`, one table for all four kinds (`kind`, `reference`, items, customer sealed, total, status, payment); a booking is a row of kind `booking` with `starts_at` and `ends_at`, its slot held while unpaid in `storefront_holds`, over a copy of the services table (`storefront_services`); one calendar per business. There is no bookings table | migrations 00180, 00181 |
| The public host writes only through definers (`taskiem_public_submit`, `taskiem_public_book` and the change and pay functions) and reads through `taskiem_dispatch` with column grants listed in `engine/db/dispatch_columns.txt`; about a dozen definers and Go queries find an action by `(storefront_id, kind)` (00180, 00181, 00183, 00237; `engine/storefront/orders.go`, `bookings.go`, `transfers.go`, `actions.go`) | `engine/db`, `engine/storefront` |
| The page is a draft until published: publishing (`publishGated`) needs a verified phone, holds the plan's `storefront_items` (a count of `storefront_catalogue`), runs moderation, holds a first publish for review where it must, and writes an immutable snapshot (`storefront_snapshots`) that the public host serves | `api/moderation.go`, migration 00179 |
| The `storefront` trigger's `action` is an enum in the frozen wd/v1 schema (`order`, `enquiry`, `callback`, `booking`), used by every storefront template, Git-managed flows and published versions | `schemas/wd-v1.schema.json`, `engine/ingest/triggers.go` |
| Plan limits: `storefront_items`, `storefront_orders_monthly`, `storefront_bookings_monthly`, and a daily cap until the business is verified | `engine/runtime/limits.go`, `engine/billing/change.go`, `api/trust.go` |
| Stock: `stock_movements` (reasons `order`, `sale`, `received`, `adjustment`, `count`, `outside`) names an item of the one catalogue; `storefronts.keeps_stock` is generated from the catalogue's mapping | migration 00261, [stock](stock.md) |
| The `storefront@1` connector: `decide`, `payment_link`, `notify_customer`, `get_booking`, `refund`, and the templates `storefront-order`, `-enquiry`, `-callback`, `-booking`, `dispatch-orders` | [storefronts: workflow actions](../storefronts.md#workflow-actions) |
| Hosted forms that start a workflow: the `form` trigger, pages, `show_if`, `verify`, `once_per`, `after`, fields as the inputs schema with `x-form` hints; types text, number, email, phone, URL, date, time, choices, yes/no, consent, files; live at `forms.<domain>/f/<id>` | [forms](../forms.md) |
| Chat: `make my page`, `turn on orders`, `add product`, `add service`, `orders`, `order <ref> ready`, `turn on deliveries` | [storefronts: from chat](../storefronts.md#from-chat) |
| Orders page, the daily summary, matching and the money log keyed by order reference; delivery areas and riders on an order | [invoices and the money log](invoices-and-money-log.md), [dispatch](dispatch.md) |

## 4. What stays as it is

- **The storefront is the business.** One per business; its slug and domains, card, look, hours, closed and reopen, QR code, the Chat on WhatsApp button, which connection takes payments. A front has none of these of its own; it inherits them.
- **Money.** A submission with a total is paid, matched and refunded exactly as an order is today, through the same connection, into the same money log, with the front named on it. No second money log, no second Paystack.
- **Forms are the mechanism.** A front's action is a hosted form in every respect: its checks, `verify`, `once_per`, files and the refused-field rules apply. Its questions are the action's, not the workflow's inputs schema, because a storefront run's inputs are Taskiem's record of the submission (`reference`, `total`, `payment`), which a customer must never be asked for or able to set (§5.5). This plan adds field types and a place to put a form; it does not add a second form system.
- **Existing storefronts change nothing.** The migration makes each storefront's first front from what it has: the catalogue and the services as list blocks, and all four forms as actions, at the same addresses, so the home page shows what it shows today. An owner who never adds a front never sees the word.
- **Publishing is the gate.** Fronts, blocks and actions are part of the storefront's draft and reach customers only through publishing, with the same verified-phone rule, moderation, item limit and first-publish hold (§5.1).
- **Existing workflows keep working.** A `storefront` trigger naming `order`, `enquiry`, `callback` or `booking` keeps its meaning; nothing published or held in Git has to change (§5.3).
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

`storefront_fronts`: id, storefront, tenant, `slug` (letters, digits, hyphens; unique per storefront; the first front's is empty, so the storefront's home stays at `/`), `name`, `tagline`, `photo`, `position`, `enabled`, `options`.

- **Addresses.** A front's address is `/<slug>` on the storefront's host and every domain. Its status pages stay at `/o/<reference>`, and its embed is `/embed/<slug>`, since the public host strips `/embed` before routing. Slugs that the public host routes are reserved: `a`, `o`, `report`, `embed`, `qr.png`, `f`, `pay`, `r`, `static`, and any later top-level route, checked by a test that walks the router's cases.
- **The home page.** It shows the card, then the first front's blocks and actions as today, then **the other fronts** as cards (photo, name, tagline) when there are any.
- **Fronts are published, not live.** A front's rows are the owner's draft, as the page is today. What a customer sees is the published snapshot:
  - `storefront_snapshots.model` gains the fronts, their blocks (text, photo ids, list block settings) and their actions' labels and forms.
  - **Publish**, from the web app or chat, runs `publishGated` over every enabled front: the verified-phone rule, moderation of every front's text and photos, the item limit over every list (§5.6), and the first-publish hold.
  - Turning a front on (the switch, or `front Snacks on` in chat) is a change to the draft followed by a publish, and is refused with the same reasons.
  - A front with `enabled` off is not in the snapshot, not linked, and answers 404, so an owner can build one before showing it.

### 5.2 Blocks

`storefront_blocks`: front, position, `kind` (`text`, `photos`, `list`), `config`.

- **What a list block is.** It names a data table and its mapping (the catalogue mapping of today: name, price, photo, description, category, availability, stock; any may be absent), plus which columns show and a `group_by`.
- **One copy per table, not per block.** The copy and refresh that `storefront_catalogue` does today become per **table**: `storefront_catalogue` gains `table_name` in its key. A table shown by two blocks is copied once and shown twice, so it cannot drift and its items count once against the limit.
- **Today's two copies.** The catalogue of today is the first front's first list block. The services table, today copied into `storefront_services`, is its second.
- **Stock stays on one table in this plan.** Stock (0057) is kept on the catalogue table only: `stock_movements` names an item but not its table, and `keeps_stock` is generated from the catalogue's mapping. Lists from other tables show availability and, where mapped, a count, but nothing moves their stock except an owner's change to the table.
- **Hire and counted sales need more.** They get their own counting (§5.4), not stock movements. Stock on more than one table is a later change to 0057: `stock_movements.table_name` and `keeps_stock` per list.

### 5.3 Actions

`storefront_actions` gains `id` (the new primary key), `front_id`, `slug` and `position`. Its old key `(storefront_id, kind)` becomes a partial unique index on the first front's four built-in actions (`WHERE builtin`), so exactly one `order`, `enquiry`, `callback` and `booking` action stays findable by kind for as long as anything looks one up that way.

An action has:
- a `label`;
- a `slug` (unique per front; `order`, `booking` and the rest for the built-in four);
- a `workflow_id`;
- `payment` (`at_order`, `after_accept`, `none`; for bookings also a deposit percentage, as today);
- `options` (fulfilment, delivery areas, `verify`, `once_per`, `after`, the things the order and booking settings hold today);
- `form` (§5.5).

**The trigger, without breaking wd/v1.** The `storefront` trigger keeps `action` and its four values, which mean the first front's built-in action of that kind, so every published workflow, template and Git-managed flow keeps working unchanged.
- It gains an optional `action_id` (a uuid). When it is set, `action` is ignored, and the action is any action on any front.
- The schema change is additive, made under this decision (CONTRIBUTING's rule for a change to a frozen contract): a new optional property and no changed meaning. `sdk/src/wd.ts`, the schema test and `wdtext` learn it.
- `registerTriggers` checks uniqueness per action: per `action_id`, or per built-in kind when only `action` is set.
- `triggers.storefront_action` keeps its column, and a `storefront_action_id` column is added beside it.

**Every lookup by kind is moved to the action's id**, listed so none is missed. Each finds the action from the submission's `action_id`, or by `(storefront_id, kind) WHERE builtin` for a submission made before the migration:
- `taskiem_public_submit` and `taskiem_public_book` (00180, 00181), which take an action id from the page instead of a kind;
- the booking change and cancel function (00181, rewritten in 00183);
- the transfer-account read (00237);
- `engine/storefront` `orders.go` (three queries), `bookings.go` (two), `transfers.go` (two) and `actions.go` (the zones read, the list and the insert).

**Where the form lives, and who owns it.** The form has one owner: the action. It is a column on `storefront_actions` (`form`, the questions' schema with its `x-form` hints), edited on the action's page, part of the storefront's draft, and published with the storefront's snapshot. A template that makes an action (§8) copies its form into the action once, at creation; after that the workflow and the form are edited and published separately, and nothing in a workflow version carries the form. A question change goes live with the storefront's publish; a workflow change with the workflow's. The two meet at the trigger: a workflow's `storefront` trigger names the action, and the action's form decides what `trigger.answers` holds, so a field the workflow reads that the form no longer asks is reported by the workflow's checks as a missing input, the way a removed table column is.

That check runs when the workflow is published. **The storefront's publish checks the other way round.** For each enabled action it reads the live workflows that name it (in every environment) and refuses a publish whose form no longer asks a question one of them reads. The refusal names the question and the workflow, with **Keep the question** or **Open the workflow**. A form change can therefore never break a live workflow unseen, whichever is published second.

The action's page is rendered by the hosted-forms code (`engine/publicweb/hostedforms.go`), which already lives in the public host's package, as a route of the **storefront's host and domains**, not of the forms host (`forms.<domain>`), under the storefront's look and chrome, at `/<front>/a/<action slug>`, with the storefront's honeypot, limits, language and status pages. The forms' own `verify`, `once_per` and rate limits apply per storefront, as the storefront's submission limits do (§10). The built-in four keep `/a/<kind>`.

### 5.4 Fields that draw on the front

Four field types, each declared in the action's form with `x-form.widget` and a reference to one of the front's list blocks:

| Field | Schema | What the customer sees | What the run gets |
| --- | --- | --- | --- |
| **basket** | `{"type": "array", "x-form": {"widget": "basket", "list": "<block>", "min": 1}}` | The list with quantities, a running total in naira, the price check as today. Where the list block maps an **options** column (Food: size, extras, each with its price), a line also takes the chosen options | `[{row_id, name, unit_price_kobo, qty, options: [{name, choice, price_kobo}]}]`, `total_kobo`, and `trigger.submission` (§5.5). The server prices a line as the unit price plus its chosen options' prices from the copy, and the price check compares that, never the form's numbers |
| **time** | `{"type": "object", "x-form": {"widget": "time", "list": "<services block>"}}` | A service from the list, then a day and a slot from the calendar, with the booking rules (hours, days off, how far ahead, buffers, capacity). The calendar's **grain** is the list block's: `minutes` (today's services, a duration per item), `hours` (a from and to hour on one day, a rate per hour) or `days` (whole days, a from and to date, a rate per day); Spaces use hours or days | `{row_id, service, starts_at, ends_at, units, unit_price_kobo, price_kobo}` |
| **dates and quantity** | `{"type": "object", "x-form": {"widget": "hire", "list": "<block>"}}` | An item, a from and to date, a quantity; refused beyond what is free for those dates | `{row_id, item, from, to, qty, unit_price_kobo, days, total_kobo}` |
| **quantity against a count** | `{"type": "object", "x-form": {"widget": "count", "list": "<block>"}}` | An item (an event, a class) and how many; refused beyond what is free | `{row_id, item, qty, unit_price_kobo, total_kobo, left}` |

A form may use any of them, or none:
- a "request a quote" action is plain fields;
- a "hire for an event" action is a dates-and-quantity field plus a delivery address;
- a "book the hall" action is a time field with a deposit;
- "buy tickets" is a quantity against the event's count.

The basket is today's order form, and the time field is today's booking form. Both are rebuilt on the forms renderer in F1, the first two of the four.

**Counting, one model for every reservation.** The item's count column is what the owner has (canopies owned, seats in the hall, places on the course, a barber's one chair as capacity), and nothing Taskiem does changes it. What is taken is a **claim**, and a booking's slot hold is a claim too:
- `storefront_claims` holds the submission, the list item, the quantity, and a from and to: dates for a hire, times for a slot, nothing for a count.
- While a submission waits for payment or approval, its claim has an `expires_at`, as `storefront_holds` holds a booking's slot today. A confirmed or paid submission's claim has none. A cancelled, declined, refunded or expired one is released, which is the one place the booking change and cancel flow, a refund and **Returned** all release from.
- **What is free.** For a count field it is the count less every live claim on the item. For a hire or a slot it is the count (or the service's capacity) less the live claims that overlap in time.
- **The check.** It runs in the public host's submitting definer (`taskiem_public_claim`, which `taskiem_public_book` becomes a caller of), in one transaction with the item's copy row locked, so two customers cannot both take the last seat.
- **No double counting.** Hires make no stock movements: a hire that is out is a claim, and **Returned** ends it. The canopies are never counted out twice, and a hire for other dates is never refused because one is out now.
- **Migration.** `storefront_holds` rows become claims with times; the table goes in F5 once the booking flow reads claims, so F1 touches no booking behaviour.

Calendars: one per **time field's list block**, not one per business, so a salon's chairs and the hall can be booked separately. The booking rules (hours, days off, lead time, cut-off, confirm automatically, the grain, setup time between bookings) move from the storefront to the list block's options, defaulting to the storefront's. A claim names its block, so a hold on one calendar never blocks another.

**Part payments.** Four kinds take money in more than one step: Hire and Spaces a deposit then the balance, Classes instalments, Quotes a deposit then the rest. Today a submission takes one payment (`payment_link` is made once, and `storefront.payment` is paid or expired), while an [invoice](../invoices.md) already takes part payments against a balance with an **Amount to pay** box. So a submission gains what an invoice has:
- `amount_due_kobo` and `paid_kobo` on the submission, with `payment` (`unpaid`, `part_paid`, `paid`) derived;
- `payment_link` takes an optional `amount_kobo` (a deposit, an instalment) and may be called again until the balance is paid; each link is one payment, keyed by the submission and the amount sequence, so a retried step makes no second link;
- the `storefront.payment` signal's payload gains `amount_kobo` and `balance_kobo`, so a workflow waits for a deposit, then later for the balance;
- **Matching learns part-paid orders**, as new work. Today matching counts a payment towards an order as its whole payment: an order waits for money, then is paid. It gains the invoice path's parts:
  - a payment smaller than the balance is a part, matched to the reference and added to `paid_kobo`, not taken as a wrong amount;
  - the order is paid when `paid_kobo` reaches `amount_due_kobo`;
  - a payment larger than the balance is split as an invoice's is, with the rest waiting in To match;
  - the order's suggestions offer the balance, not the total;
- the money log records each payment against the one reference; a refund refunds what was paid;
- the status page shows what is paid and what is due, with **Pay the balance**.
This is F5's first item, since Hire is the first kind to need it, and `storefront-classes` and `storefront-quote` build on it.

### 5.5 Submissions

`storefront_orders` already holds every kind of submission, bookings included, so nothing folds in and nothing is renamed:
- It gains `front_id` and `action_id`, filled for every row by the migration from its storefront's first front and the built-in action of its `kind`.
- Its `kind` check widens to the new kinds (`hire`, `tickets`, `classes`, `quote`).
- `lines` holds a hire's or a count's claim alongside the basket lines that are there today.

There is no rename and no view, so reports, exports, definers and the money log keep reading the table they read.

**What a run sees, kept apart.** The customer's answers and Taskiem's record are never mixed:
- **`trigger.submission`** is Taskiem's record: `reference`, `front`, `action`, `total_kobo`, `payment`, `customer` masked, `status_link`, `lines`.
- **`trigger.body`** is, for the built-in four, exactly what it is today: the server-built record (`reference`, `summary`, `total`, `fulfilment`, `payment`). No form field can reach it, so a field named `total` cannot set a total.
- **`trigger.answers`** carries an action's own extra questions.
- An action that is not built-in has the same `trigger.submission` and `trigger.answers`, and a `trigger.body` equal to `trigger.submission`.
- **Answers cannot shadow Taskiem's fields.** A form field whose name is one of them is refused at deploy, among the refused-field rules.

The `storefront@1` actions (`decide`, `payment_link`, `notify_customer`, `get_booking`, `refund`) work by reference as today, for every submission; `get_booking` answers for any submission with a time; a new `get_submission` returns the whole thing, and a new `mark_returned` ends a hire's claim and, where the owner set a late fee per day and the return is late, adds the fee to the amount due and sends a payment link for it. `notify_customer`'s `say` messages gain one set per kind: hire (`hire_confirmed`, `hire_due_back`, `hire_returned`, `hire_late`), tickets (`ticket_issued`, `event_reminder`), classes (`enrolled`, `instalment_due`, `session_reminder`, `waiting_list`), quotes (`quote_sent`, `quote_accepted`, `quote_expired`), and the part-payment ones (`deposit_received`, `balance_due`), each in the five languages.

### 5.6 Limits

- **`storefront_items`** counts the rows of every copied table, each table once (§5.2), against the plan's limit. Publishing refuses past it, as today.
- **Monthly limits.** `storefront_orders_monthly` counts submissions with a basket, a hire or a count. `storefront_bookings_monthly` counts those with a time. Questions, call-backs and quotes count against neither, as enquiries and call-backs do today.
- **The unverified business's daily cap** counts every submission, whatever its action.
- **New kinds** add no limit of their own until a plan needs one.

## 6. Kinds of front: the presets

A kind is a row in a catalogue Taskiem ships (`storefront/kinds/*.json`, like the template library): a name and a line for the owner, the table shape it makes (or the mapping it asks for over a table the owner has), the actions it adds with their forms and workflow templates, the settings each action shows, the chat words, and what the public page shows for an item. Picking a kind makes an ordinary front: the owner can then add blocks and actions, change the questions, or swap the workflow, and nothing about it is special afterwards. Kinds are the words owners, chat and the AI builder use; the model underneath never changes for a new kind.

| Kind | For | Table it makes | Actions, fields and template | Settings | Chat |
| --- | --- | --- | --- | --- | --- |
| **Shop** | A shop's products: provisions, fashion, phones, cosmetics | `products`: name, price, photo, description, category, available, stock (today's) | **Order**: basket · `storefront-order`; **Ask a question**: plain · `storefront-enquiry` | Payment (after accept, at order, none), pickup and delivery with areas, the payments connection (today's) | `add product … [to <front>]`, `price`, `hide`, `show`, `stock`, `turn on orders`, `turn on deliveries` (today's) |
| **Food** | A kitchen, a restaurant, snacks and baked goods | `menu`: name, price, photo, description, category, available, **options** (size, extras with their prices), **ready in** (minutes) | **Order**: basket with options and a ready-by line · `storefront-order` with delivery on by default, `dispatch-orders` when riders are on | Shop's, plus opening times for orders (a kitchen closes before the shop), a minimum order for delivery | Shop's words, with `add dish`, `sold out <dish>`, `kitchen closed` |
| **Services** | A barber, a salon, a tailor's fitting, a tutor, a clinic | `services`: name, price, duration, capacity, buffer (today's) | **Book a time**: time · `storefront-booking` | Booking hours, days off, how far ahead, cut-off, confirm automatically, deposit (today's, now per front) | `add service …`, `turn on bookings` (today's) |
| **Hire** | Chairs, canopies, cooking pots, sound equipment, generators, cameras | `hire_items`: name, photo, description, **count**, **day rate**, deposit (percentage or amount), available | **Hire for dates**: dates and quantity, plus a delivery address · `storefront-hire` (approval, deposit by payment link, due-back reminder, **Returned**, the canopies held by a claim from collection to return) | Deposit default, delivery and collection, the days a hire may run, late fee per day | `add item … 20 at 1500/day to Canopies`, `hire AK-7QX4KD returned`, `due back` |
| **Spaces** | Halls, event centres, a church's auditorium, a training room, a studio | `spaces`: name, photos, description, **capacity**, rate per day or per hour, deposit, what is included | **Book the space**: time with whole-day or hour slots on the space's own calendar · `storefront-booking` with a deposit and a site visit option | Days and hours bookable, minimum hours, deposit, cleaning or setup time between bookings | `add space …`, `bookings for Hall` |
| **Tickets** | An event: a concert, a seminar, a conference, a party, a match | `events`: name, photo, description, **date and time**, venue, **tickets** (count), price, a ticket type per row (regular, VIP) | **Buy tickets**: quantity against the count, paid at order · `storefront-tickets` (a ticket with a reference per seat, sent by SMS and WhatsApp; **check in** by reference or QR from chat) | Sales close at, tickets per person, refund rule | `add event …`, `tickets left`, `check in <ref>` |
| **Classes** | A course, lessons, a workshop, a bootcamp, coaching | `classes`: name, description, **starts**, sessions (count and days), **places**, fee, instructor | **Enrol**: quantity against places (usually one) with the learner's details · `storefront-classes` (enrol, pay in full or by instalment, reminders before each session, a certificate line at the end) | Instalments, waiting list when full, the reminder times | `add class …`, `places left`, `learners` |
| **Quotes** | Tailoring, catering, printing, furniture, repairs, anything made to order | none required; optionally `work` for examples with photos and from-prices | **Request a quote**: plain fields, photos, a date needed by · `storefront-quote` (the owner prices it in chat or the web app, the customer accepts, a payment link, a deposit, done) | Deposit, how long a quote stands | `quote AK-7QX4KD 45000`, `quotes` |
| **Questions** | A question about anything on the page | none | **Ask a question**: plain · `storefront-enquiry` (today's) | — | today's |
| **Call-backs** | "Call me" | none | **Request a call-back**: phone and a time · `storefront-callback` (today's) | — | today's |
| **Custom** | What no kind covers | the owner's choice | blocks and actions by hand; any template or a blank form | per action | `add front … custom` |

**What the kinds share.** Every submission, whatever the kind, is one `storefront_orders` row with a reference, a status, the customer sealed, lines and money, so the Orders page, the daily summary (which gains a line per front when there is more than one), matching and refunds treat a ticket, a hire and a dish alike. Every kind's workflow is an ordinary workflow the owner can open. Every kind's table is an ordinary data table, defined as a **known table** in the one registry [no sheet needed](no-sheet-needed.md) §5.4 sets up (`templates/tables/`): `products` and `services` move there, and `menu`, `hire_items`, `spaces`, `events` and `classes` join them, so a kind's preset names a known table rather than carrying its own columns, and templates, chat words, the builder and fronts read one definition. Its count column is what the owner has, and claims say what is taken (§5.4). Only the catalogue keeps stock (§5.2); stock on a kind's other tables (a Food menu first) is the one follow-up both plans defer to a change to 0057.

**Adding a kind later** is a preset file, a template, the chat words in five languages, and code where the kind's workflow needs a step that does not exist. The four field types cover how every kind above takes a submission: Tickets and Classes share one field, and Hire and Spaces share the calendar and the claims. They are not the only code. Food's options, Tickets' check-in, Classes' instalments and waiting list, Hire's **Returned** and Quotes' acceptance are each new code, sized in §13.

**Migration into kinds.** An existing storefront's page becomes one **Shop** front holding everything it has today: the catalogue, the services as a second list, and all four actions, booking included. Nothing moves on the public page, and the services and their **Book** links stay on the home page. An owner who later wants the services on their own page moves them to a **Services** front, which is an ordinary change followed by a publish.

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

- `add front Snacks` asks the kind as a list (Shop, Food, Services, Hire, Spaces, Tickets, Classes, Quotes, Questions, Call-backs, Custom); `add food front Snacks`, `add hire front Canopies` skip the question. The front is made off, with its table, and the reply says what to add first (`add dish …`, `add item …`) and offers **Turn it on?** once it has an item. `fronts` lists them with their kinds. `front Snacks on` (a publish, with its checks: §5.1), `front Snacks off`.
- Each kind's words (§6): `add product … to Snacks`, `add dish …`, `add item … 20 at 1500/day to Canopies`, `add space …`, `add event …`, `add class …`. Without `to <front>` the Shop front, as today; when a word fits one front only (`add dish` with one Food front) it goes there. `price`, `hide`, `show`, `stock`, `sold out` likewise, with the front's name when the item is in more than one.
- `turn on orders for Snacks`, `turn on bookings for Hall`, `turn on hire for Canopies`, `turn on tickets for Concert`: the kind's action from its template, its workflow published from chat with the checks the Forms page makes, as `turn on orders` does today. `turn on deliveries for Snacks` publishes the delivery workflow for that front's order action, which is the exact case that started this.
- `orders` shows the front beside each; `orders Snacks` filters. `order <ref>` and the rest are unchanged.
- The storefront's existing commands keep their meaning for the first front, so nothing an owner learned stops working.

### 7.3 Customers

The storefront's home: the card, the first front's content, then the other fronts as cards. A front's page: its header, its blocks, its action buttons; the order and booking pages look as they do today, in the business's look. A link to a front (`adas-kitchen.taskiem.ng/snacks`) shares as a card with the front's name and photo. Status pages, payment pages, updates on WhatsApp, the price check: unchanged, per submission.

## 8. Templates and the builder

- The four storefront templates and `dispatch-orders` become **action templates** with a `front` parameter: the same definitions, the trigger naming the action by `action_id` (the built-in four keep `action`), and the form inherited from the template. New, one per kind that lacks one: `storefront-hire` (a hire request, an approval, deposit through `payment_link`, due-back reminder from the hire's `to` date, **Returned** by the owner or from chat, which ends the claim), `storefront-tickets` (paid at order, a ticket per seat with its reference sent by SMS and WhatsApp, check-in from chat, the count released on refund), `storefront-classes` (enrol, pay in full or by instalment through `payment_link`, a reminder before each session, a waiting list when full) and `storefront-quote` (plain fields, an approval that types the price, the customer's acceptance from the status page, `payment_link`). Spaces uses `storefront-booking` with a deposit; Food uses `storefront-order` and `dispatch-orders`.
- The AI builder's storefront rules gain kinds: "sell snacks on my page too" makes a Food front; "let people hire my canopies" a Hire front; "sell tickets for my concert" a Tickets front; the builder names the kind and never assembles blocks unless asked for something no kind fits; one eval case per kind.
- The one-workflow-per-action rule reaches the editor (F0, before fronts): publishing a workflow whose `storefront` trigger names an action that another workflow holds shows "This action already runs **Storefront orders**" with **Replace it with this one** (the swap `turn on deliveries` does, in one transaction, refused under four-eyes with the reason) and **Make a new action for this workflow**. F0 ships the first choice only, since today's key `(storefront_id, kind)` allows one action of each kind. The second comes with F1's action ids: a copy of the action on the same front, off, with this workflow. **Replace** alone gets the owner who started this unstuck, because the delivery workflow replaces the plain order workflow on the one order action.

## 9. Migration

One migration, no downtime, the storefront unchanged to a customer:

1. **Fronts and blocks.** Every storefront gets a first front: kind Shop, `slug` empty, the storefront's name, `position` 0, enabled.
   - Where there is a catalogue, it gets a list block from the catalogue mapping.
   - Where there is a services table, it gets a second list block for it.
   - `storefront_catalogue` gains `table_name` (filled from `catalogue_table`), and `storefront_services` stays as the services copy until F3 folds it into the per-table copy.
   - The published snapshot is rewritten to the new model's shape, with the same content and hash recomputed, so the public page renders the same.
2. **Actions.** Every `storefront_actions` row gets an id, `front_id` (the first front's), `slug` (its kind), `builtin` true, and its form (the template's for the four kinds).
   - The primary key moves to `id`, and `(storefront_id, kind) WHERE builtin` stays unique.
   - Trigger rows keep `storefront_action` and gain `storefront_action_id`.
3. **Submissions.** Every `storefront_orders` row gets `front_id` and `action_id` from its storefront's first front and built-in action. There is no rename.
4. **The definers and queries listed in §5.3** are replaced in the same migration, with the old signatures kept as wrappers for one release, so a public host on the old binary keeps working during a deploy.
5. **The public host's grants.** The new columns the public host reads (`storefront_fronts`, `storefront_blocks`, `storefront_claims`, `storefront_actions.id`, `front_id` and `slug`) are granted to `taskiem_dispatch` column by column and listed in `engine/db/dispatch_columns.txt`. Every new table has forced row-level security with `tenant_isolation`, and a `dispatch` read policy where the public host reads it. No view is added.
6. **Nothing moves in the money log, matching or dispatch.** They key by reference.

**Down migration.** A real one, not views:
- it drops the new tables and columns;
- it restores the old key, which refuses while any storefront has a second action of a kind;
- it restores the old definers;
- it rewrites the snapshots back.

It is tested by the migrations job's up, down, up.

The public paths of today (`/`, `/a/order`, `/a/booking`, `/o/<ref>`) keep serving: `/a/<kind>` resolves to the first front's built-in action of that kind.

## 10. Security and abuse

| Concern | Control |
| --- | --- |
| A form that asks for what forms must never ask | The hosted-forms refusals apply to every action's form at deploy, as now |
| A front that shows a table with personal data | A list block shows only the mapped columns, and no column that is personal (`phone`, `email`, or marked personal: the rule is per column, [storefronts](../storefronts.md)) can be mapped to a shown one; the copy holds only shown columns, as `storefront_catalogue` does |
| A basket or hire priced by the customer | Prices and counts come from the copy on the server; the price check and the hire's overlap check run at submit, never from the form's numbers |
| Hire fraud (take and not return) | The deposit through `payment_link`, the due-back reminder, the owner's approval before it goes out; a customer with an unreturned hire is refused a new one on that storefront (by verified phone, as `once_per` works) |
| Flooding through many actions | The storefront's submission limits apply per storefront, not per action; the honeypot and `verify` per action as today |
| A front enabled by mistake, or text and photos that should not be public | Off by default. Turning one on is a publish, through `publishGated`: moderation, the verified phone, the item limit and the first-publish hold. The audit records `storefront.front.enable` |
| A form field that sets a total or a reference | Taskiem's record and the customer's answers are kept apart (`trigger.submission`, `trigger.answers`), and a field named like one of Taskiem's fields is refused at deploy (§5.5) |
| Overselling a seat, a place or a hire | One claim model with expiring claims for unpaid submissions, checked in the submitting definer with the item's row locked (§5.4) |
| A front's slug taking a route | Reserved slugs, with a test over the router's cases (§5.1) |
| Cross-tenant | Every new table carries `tenant_id` under forced row-level security with `tenant_isolation`. The public host reads only through `taskiem_dispatch`, with each column granted and listed in `dispatch_columns.txt` (`definers_test.go` fails otherwise), and writes only through definers. The public renderer resolves the front under the storefront it is serving |

## 11. Testing

- **The migration**: every e2e storefront test passes unchanged after it (the page, order, booking, deliveries, chat), and the old paths serve.
- **Kinds**: each kind made from chat and from the web app gives a front with its table, actions and settings; an item added through the kind's words shows on the page; the preset files are checked against the schema and every template they name exists.
- **Fronts**: a second front with its own table and order action, and the first front's order workflow untouched; a delivery workflow on the second front's order action while the first keeps the plain one (the case that started this); a hire action refusing an overbooked quantity for overlapping dates and accepting it for other dates; a time field with its own calendar.
- **Forms**: the four field types through the forms harness (schema, render, submit, and the `trigger.submission` and `trigger.answers` shapes); a field named `total` refused at deploy; two customers racing for the last seat or canopy, with one refused.
- **Contract**: a published workflow with `action: "order"` runs unchanged after the migration; `action_id` round-trips through the SDK, the schema test and `wdtext`; `get_submission`, `mark_returned` and `payment_link`'s `amount_kobo` are in the connector's manifest, fixtures and the generated SDK helpers.
- **Part payments**: a deposit then the balance on one reference, each in the money log and matched as a part (never as a wrong amount), the status page's balance, a refund of what was paid; a retried `payment_link` step makes no second link.
- **Food**: a basket line with options priced on the server; a form's option prices ignored.
- **Migration**: up, down, up on a database with orders, bookings, holds and refunds, with the public pages' HTML identical before and after.
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
| F0 | The editor's dead end, today's model: **Replace it with this one** on a held storefront action (one transaction, refused under four-eyes with the reason); the uniqueness error names the action | S |
| F1 | The model and the migration: fronts, blocks, actions by id with built-in kinds, `front_id` and `action_id` on `storefront_orders`, `action_id` in the trigger and the wd/v1 schema, every definer and query of §5.3, the public host's grants, the snapshot's new shape, the Down migration; the order and booking pages rebuilt on the forms renderer with the **basket** and **time** fields; `trigger.submission` and `trigger.answers`; every existing test green and the public pages' HTML unchanged | L |
| F2 | **Make a new action for this workflow**, on the action ids F1 adds | S |
| F3 | Fronts in the web app and on the public page, with the kinds that exist today as presets (**Shop**, **Services**, **Questions**, **Call-backs**) plus **Food** (options with prices, ready-in, delivery by default); fronts in the snapshot and publishing (§5.1); the per-table copy and the limits (§5.2, §5.6); the Fronts tab, Add a front by kind, blocks, actions from templates, the home page's front cards, Orders by front; the action templates with the `front` parameter | L |
| F4 | Chat: `add front` with the kind, the kinds' words, `… to <front>`, `turn on … for <front>`, `front … on` as a publish, `orders <front>`, the five languages | M |
| F5 | **Part payments** (§5.4) first, matching's order path with parts included; then **Hire** and **Spaces**: claims as the one reservation model, `taskiem_public_claim`, holds folded in; the dates-and-quantity field; `storefront-hire` with deposit, due back, **Returned** and the late fee; calendars per list with the hours and days grains | L |
| F6 | **Tickets** and **Classes**: the count field over claims; `storefront-tickets` with check-in; `storefront-classes` with instalments and the waiting list | L |
| F7 | **Quotes** and **Custom**: `storefront-quote` with the customer's acceptance on the status page, blank forms | M |
| F8 | The builder's kinds and one eval per kind; docs ([storefronts](../storefronts.md) rewritten around fronts and kinds, [forms](../forms.md) with the four fields, [stock](stock.md) on what fronts do and do not move, the owners' guide "Sell more than one thing"); the decision accepted with what was built; the atlas's business modules page | M |

F0 ships first and alone: it is the owner's case, today. Then come F1, then F2 and F3 together (the food front), then one PR each. The kinds after F3 can be reordered by what owners ask for first.

## 14. Risks and open questions

| Risk or question | What we do |
| --- | --- |
| The migration touches every storefront | No rename: the tables keep their names, and the old definer signatures stay as wrappers for a release. The old paths are kept, every storefront e2e runs before and after with the HTML compared, and a real Down migration is tested up, down, up |
| Two list blocks on the same table drift apart | One copy per table (§5.2), so they cannot |
| The forms renderer and the storefront renderer differ in look | They already share the business's look (0042); the action pages move onto the forms renderer with the storefront's chrome, and the order and booking pages are the first to prove it |
| Owners make a front per product | Add a front asks for a kind, and a kind is a whole line of business, which says what a front is for; the builder prefers an item on an existing front for one more product |
| The kinds multiply and drift from the model | A kind is a preset file checked against one schema, and every front it makes is an ordinary front; a kind that needs new code is a new field type, reviewed as one |
| A hire's or a ticket's count under concurrent submissions | Claims are checked and written in the submitting definer with the item's row locked; unpaid claims expire (§5.4) |
| Stock on more than one table | Not in this plan: stock stays on the catalogue (§5.2). Owners who want stock counted on a Food menu wait for a change to 0057 |
| Calendars per block break the "one calendar" assumption in reminders and the owner's bookings page | The bookings page groups by calendar; reminders key by submission already |
| A form and its workflow drift apart (a question removed that the workflow reads) | The form has one owner, the action; the workflow's checks report a missing input at the workflow's publish, and the storefront's publish refuses to remove a question a live workflow reads (§5.3), and the editor shows the action's questions beside the trigger |
| Part payments change what "paid" means for the money log and matching | Matching's order path gains parts as the invoice path has them (§5.4); a submission's payments are recorded as an invoice's part payments are, one reference, several payments; `payment` is derived and never written by a form; the order templates that exist keep paying in one step |

## 15. Out of scope

Branches or locations of one business (a front is not a place); kinds beyond the eleven here (subscriptions and memberships, donations and offerings, a marketplace of sellers) until an owner asks, each a preset when it comes; several storefronts per business; a marketplace of fronts across businesses; a second money log; customer accounts across fronts beyond the status link; fronts on a business's own WhatsApp public menu beyond listing them (the menu gains "which front?" only when a number serves a storefront with more than one, a later step).
