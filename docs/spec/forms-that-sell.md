# Forms that sell: a business's products in its forms, and following up a form left half-filled

Status: **being built, from 25 November 2026**, phase by phase (§11), on the founder's word to build all of it; **L0 and L1 built** 25 November 2026 ([§16](#16-as-built-l0-l1)); **L3 built** 25 November 2026 ([§18](#18-as-built-l3)); §12's recommendations are taken as the decisions until the founder changes one. Written 24 November 2026 from a screen recording the founder shared (§4) and read against the code the same day (§3, §15). §12 lists what the founder has to decide, each with a recommendation. Builds on [forms](../forms.md) ([decision 0037](../decisions/0037-forms.md)), [fronts](fronts.md) ([0059](../decisions/0059-fronts.md)), [storefronts](storefronts.md) ([0036](../decisions/0036-storefronts.md)), [embed sites](../embed-sites.md), [updates for people outside](../updates.md) ([0039](../decisions/0039-reaching-people-outside.md)), [dispatch](../dispatch.md) and [no technical steps](no-technical-steps.md).

**What Taskiem is for** ([0068, amendment](../decisions/0068-taskiem-for-everyone.md#amendment-2026-11-19-what-taskiem-is-for)): this plan is weighed by what it saves a seller. A trader who sells one product by pay-on-delivery through a page on their own website today keeps the page, the form, the orders and the follow-ups in four places or in none. After this plan the form is Taskiem's, the orders are orders, and a buyer who typed their number and left is followed up without anyone noticing they left.

## 1. Summary

A business can embed a Taskiem form on its own website today. What it cannot do is put **its products** in that form: a form has text, numbers, choices and files, never an item with a price, a package to choose, a total to see. A seller with a catalogue in Taskiem who wants an order form on their site must use the storefront's own order view, which is the storefront's, not a form of theirs, and offers a basket, never "1 for ₦25,000, 2 for ₦45,000, 3 for ₦65,000".

This plan gives forms two things:

1. **Products in a form.** A form may sell: one product or a few, as a basket or as **packages** at bundle prices, with the total worked out from the catalogue (never from the form), the delivery fee from the area the buyer picks, pay on delivery or pay now, and a submission that is a real order: in Orders, in the money log, offered to riders, under the order workflow. The form stays the business's own, is drawn in its look, and embeds where its forms embed today. A new **kind of front**, *One product*, makes the whole landing page in the video from a catalogue row: the promise line, the headline, the photos, the feature chips, the price bar, the form.
2. **Following up a form left half-filled.** When a buyer types a phone or an email and does not send the form, Taskiem keeps that one fact and what they were buying, waits, and follows up once, by the channel they gave: WhatsApp where the person can be reached there, else SMS, else email, with a link that opens the form again with their choice kept. The seller sees how many left and how many came back. Nothing else they typed is kept, and the buyer can stop with one word.

Both halves hold to the rules every public page keeps: prices and totals are Taskiem's, personal data is sealed, the host site reads nothing, and a public form cannot be made into a phishing kit.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| **Hosted form** | A workflow's form trigger, at `/f/<id>`, embeddable at `/embed/f/<id>` on a verified website ([forms](../forms.md)) |
| **Action** | A form of a front that starts a workflow: Order now, Book, Ask a question ([fronts](fronts.md) §5.3). An action's form is a hosted form drawn under the storefront's look |
| **Front** | A page of the storefront: blocks and actions. The home front is what the storefront page shows ([fronts](fronts.md) §5.1) |
| **A form that sells** | A form with a product field (§5). Its submission is an order |
| **Package** | One of a few bundles of a product at a price of its own: 1 for ₦25,000, 2 for ₦45,000 (§5.2) |
| **Left half-filled** | A form where a contact field was filled in and the form was not sent within the wait (§6) |
| **The follow-up** | The one message Taskiem sends to someone who left a form half-filled, and the link in it that brings them back |

Owners never see "hosted form", "action" or "widget". On their screens a form **sells** something, a buyer **left without ordering**, and Taskiem **followed up**.

## 3. Where we are

Read against the code on 24 November 2026.

| Today | Where | What this plan needs from it |
| --- | --- | --- |
| A hosted form's fields: text, number, money, email, phone, URL, date and time, choices, a choice from a data table, yes or no, consent, files, hidden and prefilled fields | [forms](../forms.md), `engine/publicweb/formfields.go`, `hostedforms.go` | No product, price, basket or total. **The gap the founder named** |
| A front's action form draws on the front's list with four fields: **basket**, **time**, **dates and quantity**, **quantity against a count**; prices from the catalogue copy, the price check, claims under the list's lock | [fronts](fronts.md) §5.4, `engine/storefront/answers.go` (`Widgets`), `engine/publicweb/forms.go` | The basket is products in a form already, as one list with quantities. No packages, no one-product form, no feature chips |
| An action's form is a hosted form: the same schema, the same renderer, `trigger.answers` apart from `trigger.submission`, one workflow per action | [0059](../decisions/0059-fronts.md) points 2 and 5 | A form that sells is an action; a hosted form becomes one |
| The storefront embeds on a verified website: the page, the catalogue, one action's form (`action:<kind>`, `/embed/<slug>/a/<action>`), or a button that opens it in an overlay; `?item=` pre-fills one product; `channel: embed` on the submission; `postMessage` events carry no answers | [storefronts](storefronts.md) §6.11, [embed sites](../embed-sites.md), `engine/publicweb/publicweb.go` (`/embed/` is stripped before routing), `static/form-embed.js` | The embed exists for an action's form. It needs the one-product form and the packages field |
| Delivery areas with a fee each; the buyer picks one and the fee joins the total; pay at the door by an account number or cash; riders | [dispatch](../dispatch.md), [storefronts](../storefronts.md) | The video's "delivery fee by state" and pay on delivery are built |
| Kinds of front as presets: a table shape, a form, a workflow template, settings and chat words, checked against one schema | `engine/storefront/kinds/*.json` (shop, food, hire, spaces, tickets, classes, quotes, questions, callbacks, custom) | A new kind, *One product* |
| A hosted form's state between pages lives in a signed token in the page, not on the server; the public host holds no vault key and writes submissions to an inbox the `api` role takes within a second | `engine/publicweb/hostedforms.go` (`hstate`), `form_inbox`, `storefront_inbox` | Nothing of a form is kept before it is sent. Left half-filled is new, and must go through the inbox |
| Reach codes: the public host mints one-time codes with the person's phone **sealed with the public key for the api role**, and an unproven (typed) phone reaches its own submission only | [0039](../decisions/0039-reaching-people-outside.md) points 3 and 4, `engine/publicweb/reach.go`, `reach_request_links` | The precedent for keeping a typed contact without a vault key, and the rule that bounds who it may reach |
| `notify@1` `message` reaches a person by their linked app, else the run's own submission link, else SMS, else email; outside WhatsApp's window the `taskiem_update` utility template; cross-tenant per-person limits | [updates](../updates.md), [whatsapp templates](../whatsapp.md#templates) | The follow-up is this step. A typed number that never wrote to the business is reached by SMS, not WhatsApp, unless §6.4's decision allows a template |
| The public host's guards: a signed one-time token, a honeypot, time taken, per-address and per-form limits, a Nigerian phone check | `formguard`, `FormSubmissionsPerIP`, `FormSubmissionsPerForm` | The same guards on a left-half-filled record |
| `once_per` phone or email; `verify` with a code | [forms](../forms.md) | A verified phone is a proven one; a typed one is not |
| Storefront order limits by plan, embed sites by plan (10; 1 on Shop, 3 on Shop Plus) | [moderation](../moderation.md#plans) | Left-half-filled records and follow-ups need limits of their own (§6.5) |
| Nothing records a form a person opened and did not send | checked: no autosave, partial or abandoned anything in `engine/publicweb`, `engine/forms`, the docs | §6 is new ground |

## 4. The case: the video

A 90-second screen recording of a pay-on-delivery landing page on a seller's own site (`ismartyshop.vercel.app/ear-cleaner`), as a Nigerian cash-on-delivery seller runs one today:

1. **The page:** a red promise line ("Payment on delivery available nationwide + free delivery today only"), a "Pay on delivery" badge, "Limited stock: 14 units left today", a headline and a reason to buy, a product video, four feature chips, and a sticky bar with the promo price and **Order now (pay on delivery)**.
2. **The form, under the page:** "Deliveries take 24 to 48 hours within Lagos and 2 to 4 days outside Lagos"; the product named with a "Secure checkout" badge; three steps: **Select package** (1 item for ₦25,000, 2 for ₦45,000, 3 for ₦65,000, free delivery), **Contact details** (full name, phone "required for verification", email optional), **Delivery destination** (state, street address); then the subtotal, the delivery fee, the total payable on delivery, a warning not to order unless ready to receive in 2 to 4 days, **Order now**, and "Having trouble filling the form? Order via WhatsApp".
3. **The narration** (the founder's account): when a buyer fills in a phone or an email and forgets to send the form, the system follows up by WhatsApp, SMS or email, by what they filled in.

What of this Taskiem has: the promise and stock lines (a front's text block), the photos, pay on delivery, delivery areas with fees, the order pipeline, embedding on the seller's site. What it lacks: the packages, a form that sells one product with the chips and the price bar, the WhatsApp fallback as part of the form, and the follow-up.

## 5. Products in a form

### 5.1 A form that sells is an action of a front

Decision 0059 settled that **forms are the only mechanism for an action** and that an action's form is a hosted form. This plan keeps to it from the other side: **a hosted form that sells becomes an action**. There is one renderer, one price check, one order pipeline and one submissions table, and a form with a product field is never a second way to take an order.

Two ways in, meeting in the same place:

| From | What the owner does | What happens |
| --- | --- | --- |
| **A workflow's form** (the builder, the Forms page) | **Sell something with this form**: picks a product (or a few) from the catalogue, and how (as a basket, or as packages) | The form becomes an action of the home front (or a front the owner picks), its workflow the action's workflow, as 0059's editor does when a workflow is given "a new action". Its address stays; the storefront's `/a/<action>` address is added. The form draws under the storefront's look from then on |
| **The storefront** (Fronts, chat) | **Add an order form** to a front, or **Sell `<product>` on a page** (§5.4) | An action with a product field, as today's order form is, with packages where asked |

A form that sells is refused where its business has the Storefront area off, since orders, the catalogue and the look live there: the owner is offered to turn it on, as templates offer today.

### 5.2 The product field, and packages

Two fields draw on the catalogue. The first exists; the second is new.

| Field | Schema | What the buyer sees | What the run gets |
| --- | --- | --- | --- |
| **basket** (exists) | `{"type":"array","x-form":{"widget":"basket","list":"<block>","min":1}}`, with a new `items: ["<row>", …]` to show only those rows | The listed items with quantities and a running total | As today: `lines`, `total_kobo`, `trigger.submission` |
| **packages** (new) | `{"type":"object","x-form":{"widget":"packages","list":"<block>","item":"<row>","packages":[{"qty":1,"price_kobo":2500000,"label":"1 item + free delivery"},{"qty":2,"price_kobo":4500000},{"qty":3,"price_kobo":6500000}]}}` | One product, named with its photo and line, and a choice of packages with their prices; the chosen one's total below | `{row_id, item, qty, package_kobo, unit_price_kobo}` as one line of `lines`, `total_kobo`, `trigger.submission` |

Rules:

- **A package's price is the price.** It is kept with the action's form, published with the storefront, and the server prices the submission from it, never from the form's numbers, as the basket is priced from the catalogue copy. A package may not be dearer per unit than the catalogue's price for the item unless the owner says so when publishing (a warning, not a refusal: a bundle may carry a gift).
- **Stock.** A package of three takes three of the item's stock, as a basket line of three does. A package the stock cannot fill is shown greyed with "Not enough in stock", and refused on the server.
- **At most six packages**, each a quantity and a price, with an optional label; the label is drawn by Taskiem from the quantity and the price when none is given ("2 items · ₦45,000").
- **Sizes and colours.** Where the item has sizes or colours ([chat categories and variants](chat-categories-and-variants.md)), the package asks for one choice per unit, or one for all, as the owner sets.
- **Delivery.** The form's delivery area field is the storefront's (an area and its fee, or **Free** where the storefront says so), and the fee joins the total under the packages, as it does in the basket. "Free delivery" in a package's label is the owner's words; the fee the storefront charges for the area is what is added, so a seller who means free delivery sets the areas' fees to nothing, which the editor offers.
- **The warning line** ("Do not order if you are not ready to receive it in 2 to 4 days") is the action's `notice`, drawn in the storefront's warning tone above the button, with a default the owner may change or clear.
- **Pay on delivery** is the action's payment setting as today (pay now, pay after acceptance, pay at the door). The video's form is pay at the door.

### 5.3 One product, pre-chosen

An action's form whose basket or packages field names a single `item` draws that product at the top (name, photo, line, the storefront's "Secure checkout" mark where payment is connected) and never asks the buyer to pick a product. `?item=` on the link keeps working for forms that take any item.

### 5.4 The *One product* kind of front

A kind, as the others are, so the whole page in the video is made from a catalogue row and the owner never assembles blocks:

| Part | What it is | From |
| --- | --- | --- |
| The promise line | A text block in the storefront's accent, at the top ("Pay on delivery nationwide") | A setting of the front, with a default |
| The stock line | "Only 14 left" from the item's stock, where stock is kept and the owner turns it on; never a made-up number | The catalogue |
| The headline and the reason to buy | A text block: the item's name and description, or the owner's own words | The catalogue row, editable |
| Photos or a video | The item's photos; a video where the owner adds one to the row | The catalogue row (media) |
| Feature chips | Up to six short lines with an icon from the fixed set | A setting of the front |
| The price bar | The package's price, or the item's, with **Order now**, fixed to the bottom on a phone | The action |
| The form | The action's form with the packages field, contact details, the delivery area, the notice, the button | The action |
| **Order via WhatsApp** | Under the button: a link that opens WhatsApp to the business's own number, or the shared number with the business's code, with the product named in the first message; the conversation then takes the order as the customer door does ([Taskiem for everyone](taskiem-for-everyone.md)) | The business's number, as the storefront's **Chat with us** is |

The kind's table is `products` (shared with Shop), so a seller with a shop adds a One product front for a hero item without copying it. Its workflow template is `storefront-order` with pay at the door, and its chat words are in §7.

The page is drawn by the storefront's renderer in the storefront's look, so there is no new page builder, and it embeds as any front does: the page, the form alone, or a button.

### 5.5 Embedding the form that sells

Nothing new in the mechanism ([storefronts](storefronts.md) §6.11): the script and the element, a plain frame, or a button that opens the form in an overlay, on a verified website only. What is added:

- the embed snippet for **one action**, offered on the action's page and under **Your website** in Settings, so the owner copies the form that sells and not the whole page;
- the `postMessage` event `storefront:submitted` carries the action and the reference as today, and nothing of the buyer;
- the frame's height follows the packages field as it opens.

A seller whose site is a page builder that allows no script uses the plain frame, as today.

### 5.6 What the run gets

As any action's run: `trigger.body` with the reference, the customer, `lines` (the package as one line with its quantity and package price), `total_kobo` with the delivery fee, the payment; `trigger.submission`; `trigger.answers` for the action's own questions. The default order workflow runs unchanged: the owner accepts or declines in chat, the customer is told, the order is offered to riders where the business has them, pay at the door at the door.

## 6. Left half-filled

### 6.1 What is kept, and when

A buyer who has typed a **contact** (a phone or an email in a field the form marks as the respondent's) and does not send the form within the **wait** leaves a form half-filled. Taskiem keeps, for that form:

- the contact as typed, sealed by the public host with the public key for the `api` role, as reach codes are;
- what they were buying: the chosen package or the basket's lines, and the delivery area if chosen;
- the form, the front, the site it was embedded on, and the time;
- **nothing else they typed.** Not their name, not the address, not any answer. A name is needed for nothing and an address is personal data with no purpose once the form is not sent.

How the public host knows:

- The page sends a small **keep** request (a beacon) when the contact field loses focus with a valid value, and again when the page is hidden or left, carrying only the contact, the product choice and a **keep token** of its own. The keep token is minted when the page is drawn and signed by the public host as the form's one-time submit token is: the form, the time it was drawn and a nonce, and nothing typed. It is never the page's state token, which carries every answer typed so far, files included (`hstate`), so nothing from an earlier page leaves the page in a keep. It is sent only after the contact is valid (a Nigerian phone, or an email) and only when the form has not been sent.
- The record is written through the inbox, as submissions are, and the `api` role takes it: one row per form and contact, replaced by a later keep.
- A submission from the same contact on the same form **within the wait** deletes the record, before any follow-up. One **after the follow-up** does not delete it: it sets `came_back_at`, and the record stays until it is purged (§6.7).
- No JavaScript, no keep: a form filled in without scripts has no abandonment, and loses nothing else.

### 6.2 Consent and the words on the form

A form that follows up says so where the contact is asked, in the storefront's quiet tone, under the field: **"We may message you about your order on WhatsApp or by text or email. Reply STOP to stop."** The words are Taskiem's, in the form's language, and the owner may turn the follow-up off, not reword the line. A form with the follow-up off shows no line and keeps nothing.

The follow-up is about **the order the buyer started**, not an offer: it names the product and the package, offers the link back, and nothing else. It is not marketing to a list, and a left-half-filled record is never a lead list for anything but this one message.

### 6.3 The wait, and the message

| Setting | Default | Range |
| --- | --- | --- |
| The wait after the last keep | 30 minutes | 10 minutes to 24 hours |
| Messages | One | One. A second nudge is a decision for later (§12, 5) |
| The link's life | 7 days from the message | Fixed; the record lasts as long (§6.7) |

The message, in the storefront's language, from the business's name: *"[Ada's Kitchen] You started an order for the Smart Visual Ear Cleaner Kit (2 items, ₦45,000) and did not finish. Finish it here: <link>. Reply STOP to stop."* On WhatsApp with a button **Finish my order** where the template allows.

The link opens the form again with the package and the area chosen (the contact is not put in the link, and the buyer types it again), in the same frame if the buyer came from the seller's site (the link goes to the seller's page with the form, where the storefront knows it, else to the storefront's own address). It is one-time for the restore and seven days long; after that the form opens plain.

### 6.4 By which channel

The follow-up is the `notify@1` `message` step, so it reaches the person as [updates](../updates.md) do, in that step's order, with one change for a typed contact that never wrote to the business:

| The contact | Reached by |
| --- | --- |
| A phone the form **verified** with a code (`verify`), which is proven, and which has linked an app with this business or wrote to its own number within 24 hours | WhatsApp or Telegram, as today |
| A phone the buyer typed and the form did not verify, whatever it matches | **SMS**, counted in the plan's texts. WhatsApp only where the business has its own number and Meta has approved a template for this message (§12, 3); until then SMS |
| An email, and no phone | Email |
| Both | The phone's channel, with the email left out, so one message is sent |

A phone the buyer typed is **unproven** (0039 point 3): the message reaches it about this one form and nothing else, and the link in it opens this one form. A typed phone is never matched to a contact's linked chat, even when the digits are the same: that is the binding 0039 point 3 forbids, and it would let anyone send a business's customer a message in their own chat by typing their number. It never binds the number to a contact of the business, and a reply to it, on WhatsApp or by SMS, does not open the business's customer conversation unless the person's own reply does.

### 6.5 Limits and stopping

- One follow-up per contact per form in seven days; one left-half-filled record per contact per form at a time.
- **Across forms:** at most one follow-up per contact in 24 hours and three in seven days, across every form and every business, keyed by the contact's keyed hash, as `message`'s cross-tenant per-person limits are. A number typed into fifty forms gets one text that day.
- **Per form and per business:** at most 50 follow-ups a day per form and 100 a day per business (starting figures), besides the month's `form_followups`. A sudden run of keeps on one form (more than ten times its daily average of submissions) holds its follow-ups for the owner to release, with a chat alert, so keeps from many addresses with made-up numbers cannot spend the business's texts.
- Keeps are guarded as submissions are: the signed token, the honeypot, the per-address and per-form buckets, and a bucket of their own (`FormKeepsPerIP`), so a script cannot fill a business's inbox or spend its texts.
- Follow-ups count against the plan's monthly texts (SMS) or the WhatsApp template count, as the step's messages do, and a plan limit `form_followups` caps them a month (200 on the lowest plan is a starting figure).
- **STOP** by SMS or WhatsApp, or the email's unsubscribe link, stops every follow-up to that contact from that business, and is honoured by `message` as the existing stop is.
- The person who was followed up can ask the business to delete what it kept; the owner's **Erase this person** covers it.

### 6.6 What the owner sees

- On the form's and the action's page: **Left without ordering** this week (a count), **Followed up** (how many), **Came back** (how many ordered after the message), and the list, each row the product, the package, when, the channel, and the contact masked (the last four digits, the email's domain). Opening a row shows nothing more.
- In the chat: an alert when people leave a form today ("3 people left your order form today; 2 were followed up"), with the form's page behind it, as other alerts open their pages.
- Nothing of a left-half-filled record reaches the AI builder or a page's or tool's reads: it is not a table, and only the `api` role reads it, through narrow functions. The public host reads nothing of it (§10).

### 6.7 Retention and erasure

A left-half-filled record is kept for 7 days after its last keep, or, once followed up, for 7 days after the follow-up, so it lasts as long as the restore link and a buyer who comes back on the link's last day is still counted; then it is deleted. The count of **Came back** is kept as a number on the form, not as the record. Erasing a person erases their records as it erases their submissions. The records are not exported with the storefront's orders.

## 7. What the AI builder and the templates learn

- **Templates:** `storefront-order` gains the follow-up as a setting of the action (off by default for actions that exist, on for a One product front); a template `storefront-one-product` makes the front, its action and its workflow from one catalogue row.
- **The builder:** "sell my ear cleaner on a page, pay on delivery, 1 for 25k, 2 for 45k, 3 for 65k, and follow up anyone who leaves" drafts the One product front, its packages and the follow-up setting, read back in plain words. The builder is given product names and prices, never a buyer's contact.
- **Chat** (the only list of these words): `sell <product> on a page`, or `add a page for <product>`, makes the front from chat with the one-person approval rule, as `turn on orders` does; `follow-ups on` and `follow-ups off` for the action.

## 8. Security and abuse

| Concern | Control |
| --- | --- |
| A form that fakes a price or a total | Prices are the packages published with the storefront and the catalogue copy; the server prices every submission; the form's numbers are never read (0059 point 2, as today) |
| A keep used to harvest or spam | A keep carries a contact, a product choice and its own keep token, never the page's answers; it is guarded like a submission and is one per contact per form |
| One person's number typed into many forms | One follow-up per contact a day and three a week across every form and business (§6.5) |
| Made-up numbers sent from many addresses to spend a business's texts | Daily caps per form and per business, and a hold on a form whose keeps jump, released by the owner (§6.5) |
| A follow-up as unsolicited marketing | The words are Taskiem's and name the order the person started; the consent line is on the form; STOP is honoured across the business; nothing is sent to a list |
| A typed number that is someone else's | The message goes by SMS about this one form, with a link that restores a product choice and nothing personal; a typed number is never matched to a linked chat; it binds nothing and opens no conversation (0039 point 3) |
| Personal data kept without purpose | Only the contact and the product choice are kept, sealed for the `api` role, for 7 days; names, addresses and answers are never kept before a form is sent |
| The host site reading the form | A frame, as today: the host site's scripts cannot read it, and `postMessage` carries no answers |
| A phishing page in the form's look | The same checks as every public form and storefront: a verified email, reserved and look-alike names, the Report link, operator suspension |
| A WhatsApp follow-up outside the window | Only by an approved template on the business's own number, once Meta's category for it is known (§13); otherwise SMS |
| Cost | Follow-ups count against the plan's texts and templates and a limit of their own; a form's keeps count against nothing but their bucket |

## 9. Tests

| Test | What it proves |
| --- | --- |
| The packages field | Drawn with the item and its packages; the server prices the chosen package from the published form, refuses one the stock cannot fill, adds the area's fee, writes one line with the package's price; the price check never reads the form's numbers; `trigger.body` and `trigger.submission` as §5.6 |
| A hosted form that sells | **Sell something with this form** makes an action of the front, keeps the form's address, draws it under the storefront's look; its workflow runs on the storefront trigger; the Storefront area off refuses it with the offer to turn it on |
| One product | The kind's front from a catalogue row: every part of §5.4 drawn on the web and in the app's parity test, in the three themes, at 360 and 390 px with axe; **Order via WhatsApp** opens the right number with the product named |
| Embedding | The action's form framed on a verified site, the snippet from Settings; the height event follows the packages field; the submitted event carries the action and reference and nothing else; an unverified site refused |
| Left half-filled | A keep after a valid contact only; a keep refused without the token, past the buckets, or after the form was sent; one record per contact and form; a keep carries no answer from an earlier page; a submission within the wait deletes the record and one after the follow-up sets `came_back_at`; the wait, then one message, by the right channel for each row of §6.4, and a typed phone that matches a linked contact goes by SMS; the caps across forms, per form and per business, and the hold; the restore link opens the form with the choice kept, once, within 7 days of the message, and is still counted as came back on its last day; STOP stops; the record purged when its link has expired; erasure |
| The owner's view | The counts and the masked list; the chat alert; nothing in the AI builder's inputs or a page's reads (isolation test, as data tables have) |
| Plain words | The form's consent line and the follow-up's words in the five languages, against the plain-words list |
| Migrations | Down and up for the new tables |

## 10. Storage and API

- **`storefront_actions`** gains `packages` (in the form's schema, so nothing new) and the follow-up settings: `followup_on`, `followup_wait`. **`storefront_fronts`** gains the One product kind's settings (the promise line, the chips) as the kind's options do.
- **`form_left`** (new): tenant, form or action, front, embed site, the contact sealed for the `api` role, a keyed hash of the contact for the one-per-contact rule (as reach contacts are keyed), the product choice, `last_keep_at`, `followed_up_at`, `came_back_at`, `expires_at` (7 days after the last keep, moved to 7 days after the follow-up when one is sent). Under forced row-level security; the public host writes through the inbox and reads nothing of it; the `api` role reads through narrow functions.
- **`form_restores`** (new): one row per follow-up, written by the `api` role: a hash of the restore token, the form, the product choice and area, `expires_at`, `used_at`. Never the contact. The public host claims a row through one definer, by the token's hash, getting back only the product choice and area and marking it used, as one-time links are claimed today.
- **The public host:** `POST /a/<action>/keep` and `/f/<id>/keep` (a beacon), `GET /back/<restore token>` (the form restored; not under `/r/`, which link forms and receipts already share).
- **API:** `GET /v1/storefront/actions/{id}/left` (the counts and the masked list), `PATCH /v1/storefront/actions/{id}` (the follow-up settings, the packages through the form), `GET /v1/forms/{id}/left` the same for a hosted form that sells; the embed snippet for one action in `GET /v1/storefront/embed`. OpenAPI and the app's client as always.
- **The follow-up** is a run of a managed workflow (`storefront-followup`, the server's, not the owner's to edit) started by the `api` role when the wait passes, with `notify@1` `message` and the restore link; its run is the audit of what was sent, where and whether it came back.

## 11. Phases

| Phase | What | Done when |
| --- | --- | --- |
| L0 **built** ([§16](#16-as-built-l0-l1)) | The decision; the packages field in the forms schema, the contract and the validator; `items` on the basket; the One product kind's schema; the keep and `form_left` tables and functions; nothing drawn yet | A form with packages validates or is refused for the right reason; a keep is stored and purged |
| L1 **built** ([§16](#16-as-built-l0-l1)) | **Products in a form:** the packages field drawn on the web and the public host, the one-product form, the total with the area's fee, the notice; **Sell something with this form** in the builder; the embed snippet for one action; the submission as an order | A seller embeds a form selling one product in three packages on their site, a buyer orders two for pay at the door, the order reaches Orders, the owner accepts in chat, a rider is offered it |
| L2 **built** ([§17](#17-as-built-l2)) | **One product:** the kind, its template, its chat words, the page drawn with the promise line, the chips, the price bar and **Order via WhatsApp**; the app's parity test | `sell <product> on a page` makes the page in the video from a catalogue row, on the web and in the app |
| L3 **built** ([§18](#18-as-built-l3)) | **Left half-filled:** the keep, the consent line, the wait, the follow-up by SMS and email, the restore link, STOP, the counts and the list, the chat alert, retention | A buyer who typed a phone and left gets one text after 30 minutes and finishes the order from its link; the owner sees it under **Came back** |
| L4 | **WhatsApp follow-ups** by template on the business's own number, once the category is settled; a second nudge if decided | A buyer on the business's own number is followed up on WhatsApp with **Finish my order** |

L1 is the first thing worth showing, and it is what the founder named. L3 can be built beside L2.

## 12. For the founder to decide

On 25 November 2026 the founder asked for all of it to be built. Each recommendation below is built as the decision; any can still be changed, and the build follows.

| # | Question | Recommendation |
| --- | --- | --- |
| 1 | Products in a hosted form directly, or a form that sells becomes an action of a front? | **An action.** One renderer, one price check, one order pipeline, one submissions table, and 0059's rule stands. The owner never sees the difference: **Sell something with this form** does it |
| 2 | Packages as a field of their own, or a basket with fixed quantities? | **A field of their own.** A package has a price that is not the unit price times the quantity, and the buyer chooses one, not quantities |
| 3 | Does a typed phone get a WhatsApp follow-up? | **Not until** the business has its own number and Meta has approved a template whose category is known (utility or marketing decides its price and rules: §13). SMS from the first day; WhatsApp when linked already. The founder's account says WhatsApp; the rules say when |
| 4 | The wait before following up | **30 minutes**, the owner choosing from 10 minutes to 24 hours |
| 5 | One message, or a second nudge later? | **One** in this plan. A second message needs a reason beyond "they did not answer", and the cost is the business's |
| 6 | Is the follow-up on by default? | **On for a One product front, off for actions that exist**, since those owners did not ask and their forms show no consent line yet |
| 7 | What is kept from a form left half-filled? | **The contact and the product choice only**, for 7 days. Not the name, not the address |
| 8 | The kind's name | **One product.** "Landing page" is the trade's word; "One product" says what it is |
| 9 | A plan limit on follow-ups | **Yes**, `form_followups`: 200 a month on the lowest plan, with the texts counted as texts |
| 10 | **Order via WhatsApp** on the page: the business's own number only, or the shared number too? | **Both**, as the storefront's chat link does today: the own number where there is one, else the shared number with the business's code |

## 13. Needs a person

| Item | Who |
| --- | --- |
| The decisions of §12 | The founder |
| Meta's category for a "finish your order" template (utility or marketing), its wording, and whether a URL button to the restore link is allowed | Whoever holds the WhatsApp Business Account |
| The consent line and the follow-up's words, in the five languages, against the plain-words rules; whether Nigeria's data protection rules need more than the line on the form for one message about an order the person started | Product, native speakers, whoever owns privacy |
| The fixed set of chip icons for One product | Design |
| Whether "Only N left" from stock is shown by default or opt-in, given the trade's habit of made-up counts | The founder |

## 14. What this is not

- **Not a page builder.** One product is a preset drawn by the storefront's renderer; the owner sets words, photos and chips, never layout or style.
- **Not a second order path.** A form that sells is an action; nothing takes an order but an action's submission.
- **Not tracking.** No pixel, no analytics on the seller's site, no record of who opened the page. A keep is sent only after a buyer typed a valid contact into Taskiem's own form.
- **Not a lead list.** Left-half-filled records lead to one message about one order and are purged in seven days; they are not exported, not searched, not given to the AI.
- **Not marketing messages.** Offers to past customers are [Taskiem for everyone](taskiem-for-everyone.md)'s, with their opt-in and their two a week; this plan sends one message about an order the person started.

## 15. What a review of this plan changed

The plan was read against the code on the day it was written. What that found:

| Was (first draft) | Is |
| --- | --- |
| A product field added to hosted forms, with its own pricing | A form that sells becomes an action of a front, so the basket's pricing, the price check, claims, the order pipeline and the submissions table are reused and 0059's "forms are the only mechanism for an action" holds from both sides (§5.1) |
| The follow-up "on WhatsApp, SMS or email, by what they filled in" | By the `message` step's order, with the rule of 0039 point 3 that a typed phone is unproven: SMS first for a typed number, WhatsApp where linked already or, later, by an approved template on the business's own number (§6.4) |
| The form's state autosaved on the server | A hosted form's state lives in a signed token in the page, and the public host holds no vault key; so a keep carries the contact and the product choice only, sealed with the `api` role's public key as reach codes are, through the inbox (§6.1) |
| Everything typed kept, to restore the form whole | The contact and the product choice only; the restore fills those and the buyer types the rest again (§6.1, §12, 7) |
| "Free delivery" as a package property | The storefront's delivery areas and fees are what is charged; the label is the owner's words, and the editor offers to set the fees to nothing (§5.2) |
| The embed as new work | The storefront embeds one action's form on a verified site already; what is new is the snippet per action and the packages field's height (§5.5) |
| A keep carried the form's signed state token | That token holds every answer typed so far, files included; a keep carries a token of its own with nothing typed in it (§6.1) |
| A typed phone that matched a linked contact went to their chat | A typed phone goes by SMS whatever it matches; a linked chat only for a phone the form verified (§6.4, 0039 point 3) |
| Follow-ups capped per contact per form only | Also per contact across every form and business, per form and per business a day, and a hold when a form's keeps jump (§6.5, §8) |
| The record purged 7 days after the last keep, before its link expired | The record lasts as long as the link; the restore goes through `form_restores`, which the public host claims by one definer and which holds no contact; the public host reads nothing of `form_left` (§6.7, §10) |
| A submission both deleted the record and closed it as came back | Within the wait it deletes it; after the follow-up it sets `came_back_at` (§6.1) |
| The restore link at `/r/` | At `/back/`: `/r/` serves link forms and receipts already (§10) |

## 16. As built (L0, L1)

Built 25 November 2026 on the decisions of §12.

**L0.**
- **The packages field** (`engine/storefront/packages.go`): `x-form.widget` `packages` with `list`, `item`, one to six `packages` (`qty` 1 to 20, each once; `price_kobo` from ₦1; `label` up to 60 characters on one line with no links, drawn as "2 items · ₦45,000" when absent) and `choose` (`all` or `each`, for sizes and colours), read strictly; a form sells by a basket or by packages, never both; a basket's `items` names its rows, each once. `CheckForm` refuses each for its own reason (`TestPackagesField`). A hosted form's field with a storefront widget is refused by `engine/wd` with the way to sell (`TestFormsValidate`). The contract (`docs/contracts/wd-v1.md`) has both.
- **One product** (`engine/storefront/oneproduct.go`, `kinds/one-product.json`): its settings (a promise line up to 80 characters, up to six chips each an icon of a fixed set of twelve and words up to 40, the stock line on or off) checked strictly by `CheckFrontSettings`; the kind is loaded and checked with the others but marked `later`, so `FrontKinds` does not offer it until L2 draws it (`TestOneProductSettings`). The order action's `notice` (`ActionOptions.Notice`) is checked here too.
- **Left half-filled storage** (migration 00800): `form_left_inbox`, `form_left` and `form_restores`, each under forced row-level security with no access for the api role but through its definers. The public host writes a keep sealed with the forms inbox key (`taskiem_public_form_keep`, only for a live form or an action that is on) and claims a restore link once by its token's hash (`taskiem_public_form_restore`, the form and the choice only). The api role (`engine/forms/left.go`) takes keeps (`DrainKeeps`, every second with the forms inbox), seals the contact under the person in the vault, keys it as `message`'s per-person limits do (`sender.Queue.ContactHash`), and keeps one record per form and contact (`taskiem_form_left_put`); `Sent` forgets it within the wait or marks it came back after the follow-up; `FollowedUp` writes the restore link's row; `PurgeLeft` (every ten seconds) deletes what is past its 7 days. `TestFormLeftKeptAndPurged` covers each, with the api and public roles refused a direct read.

**L1.**
- **The public host** (`engine/publicweb/packages.go`, every theme's `form.html`): the packages field drawn with the product at the top (its photo and line, **Secure checkout** where the order form takes payment through the payment page), a package the stock cannot fill greyed **Not enough in stock**, sizes and colours as one choice or one per item, the total with the area's fee (`sell.js`, only shown), and the notice in a warning colour above the button. `packageLines` prices the chosen package from the published form, never the browser's numbers, holds each size's stock to what is left, and makes one line (a line per size when chosen for each, shares adding up to the package) with `package_qty` and `package_kobo`. `?item=` still pre-fills a form that takes any item. `TestPackagesForm`, `TestPackagesSizes`, `TestBasketItems`, `TestPackagesInEveryTheme`.
- **The price check** prices a package's lines as the package from the page as published (a new price is a price change, a package no longer offered is gone), never per item from the table (`TestRepricePackages`). `trigger.body` gains `lines` beside `basket`.
- **Sell something with this form** (`api/forms_sell.go`, `POST /v1/forms/{id}/sell`; the web's Forms page): an order action on the home page or a front picked, with the form's own questions; the workflow's new version names the action and its reads move to `trigger.body.customer` and `trigger.answers`; published with the page. The endpoint keeps the form (`form_endpoints.action_id`, migration 00801; a form trigger deployed again clears it) and `/f/<id>` and `/embed/f/<id>` redirect to the action's address. Refused, naming the area, while Storefront or Orders is off; the web sheet says so and turns it on. `TestSellSomethingWithThisForm`; Playwright `forms-that-sell.spec.ts`.
- **Add an order form** (Fronts; `POST /v1/storefront/fronts/{id}/actions` with `kind: order`): the product from `GET …/sellable`, its packages, the button and the notice, the home order form's payment, areas and paying at the door; **Set up** gives it the business's order workflow, `dispatch-orders` when deliveries are on. `PUT /v1/storefront/actions/{id}/sells` changes what it sells. A package dearer per item answers in `warnings` (and the page's publish, `PublishWarnings`), never refused.
- **One form on a website**: `GET /v1/storefront/embed?action=` gives a frame and the lines that size it, or a plain frame, on the order form's card and under **On your website**. A framed order form posts `storefront:height` and, once sent, `storefront:submitted` with the action and the reference only (`form-embed.js`; `TestPackagesEmbedded`).
- **The phone app** has no screen where forms or fronts are edited (they open on the web), so Sell and Add are the web's; its order page says "Package of 2 for ₦45,000" under a package's line, as the web's Orders does (`storefront_orders_page_test.dart`, pictures light and dark).
- **Done when** (`TestFormThatSellsEndToEnd`, with the database): an order form selling the Smart Ear Cleaner in three packages is added and set up, framed on a verified website with its code, a buyer orders two to Lekki paying at the door (₦45,000 and ₦1,500), the order is in Orders with the package as one line, the run's `trigger.body.lines` and `trigger.submission` carry it, the owner accepts in WhatsApp, and Musa is offered it. Playwright repeats the buyer's part on the public host, with axe in both themes at 360 and 390 px.

**Review fixes** (25 November 2026, after a review of L0 and L1):
- A basket's `items` hold when sent, not only when drawn: a row the form does not name is never ordered (`TestBasketItems`), and the price check treats such a line as gone (`TestPriceCheckHoldsTheForm`).
- A package is had whole or not at all at the price check: when one of its sizes is gone, every line of it is gone and said so, never the rest at the package's price (`TestRepricePackages`).
- The published page unread at the price check is tried again, then noted *Prices were not checked*, as a table that does not answer is, never every package line gone (`TestPriceCheckHoldsTheForm`).
- A form that sells is closed as a form when it sells: `/f` and `/embed/f` still lead to the action while it is there, it cannot be paused or reopened on the Forms page, and once the action is deleted its address says it is closed, never serving or running the old version; a form trigger deployed again opens it (`TestSoldFormClosedWhenItsActionGoes`, `TestSellSomethingWithThisForm`).
- **Sell something with this form** takes at most one field for each of the buyer's name, phone, email and address (marked `x-pii`, else named so, else of its format), the address only where the order form delivers; every other field stays the action's question, read from `trigger.answers` (`TestSellingDefinitionKeepsOtherFields`).
- Selling is refused while the workflow has a version newer than the live form's (`newer_version`), and the web sheet says to publish it first (`TestSellRefusesOverANewerVersion`, `sell.test.ts`).
- **Secure checkout** shows only where the order form is paid through the payment page on a connected Paystack account (`TestSecureCheckoutOnlyWhenConnected`).
- `taskiem_form_left_put` keeps a front only as its action's own (migration 00860), reading the action's business through its storefront: it read columns its owner may not, so a keep for an action was refused (`TestFormLeftFrontIsTheActions`).
- The *prices have changed* message names at most three gone lines and counts the rest, and its SMS is written in plain letters (the naira as N) when a naira sign or a dash in a name would take it over three parts, so the buyer is always told (`TestPriceChangedSMSFits`).

## 17. As built (L2)

Built 25 November 2026 on the decisions of §12.

- **The kind** (`kinds/one-product.json`, no longer `later`; migration `00820_one_product_fronts.sql` lets a front be `one-product`): offered second, after Shop. Its settings (§16's promise, chips and stock line) are the front's `options`, read back as `settings`, changed with `PATCH /v1/storefront/fronts/{id}` (`CheckFrontSettings`) and published as `one_product` on the front; it starts with "Pay on delivery nationwide" and two chips (Pay when it arrives, Delivered to your door), the stock line off (§13 leaves on-by-default to the founder). The headline and the reason to buy are the front's name and line, the product's name and words to start with, so no new setting holds them.
- **Made from a catalogue row** (`storefront.CreateOneProduct`): the front's list is the catalogue's own table (shared with Shop, not copied; a business with no products table is told to add its products first); its order form is `AddOrderAction` selling the item in its packages (one of one at the item's price when none are given), delivered and paid at the door, with the home order form's areas and payments, and the kind's notice. `POST /v1/storefront/fronts` with `kind: one-product` and `item`, `packages` makes it on the web (refused, naming the area, while Storefront or Orders is off); the front's view says what it sells (`sells`) and the packages' `warnings`.
- **The template** `storefront-one-product` (the order workflow, written for orders paid at the door): `setupActionTx` gives it to a One product front's order form, or `dispatch-orders` when the business's orders go to its riders. The AI builder's prompt names it for "sell my ear cleaner on a page…", with product names and prices only.
- **The page** (`engine/publicweb/oneproduct.go`, `templates/shared/oneproduct.html`, `static/oneproduct.css`): `/<slug>` of a One product front whose order form sells one product draws, in every look (each look's order form is now its `orderform` template, and every look has an `fmain`), the promise line in the page's colour, a Pay on delivery mark, the stock line ("Only 14 left", from the item's stock, only when on), the headline and reason, the photos (the photo and the gallery, swiped), the chips with the fixed icons drawn on the server, the order form itself under it (its token, posting to `/<slug>/a/<action>`), Order via WhatsApp under the button (the business's WhatsApp with "I want to order <product>" in the first message, else its chat link as "Order in a chat"; the storefront has no shared-number-with-code link today, so that half of §12, 10 is the chat link), and the price bar fixed to the bottom ("From ₦25,000", "1 item · pay on delivery", Order now to the form). Closed, paused, not open or sold out: said in place of the form. `TestOneProductPage` (every part in every look, the order priced from the package, sold out, the links, a front selling nothing drawn as a front).
- **Chat** (`api/chat_oneproduct.go`): `sell <product> on a page` and `add a page for <product>`, with packages after it ("1 for 25000, 2 for 45,000, 3 for 65k"), read before a sale's words; the page shown with its packages and address, then on yes made, its workflow published from the template with the Forms page's checks (or asked for under the approval rule, the page staying off), the form and the front on and the page published again. "add front" no longer offers One product by number (it is made from a product, not a name) and points to the words instead. Drafts in Hausa, Igbo, Pidgin and Yoruba for every message (`wa.page.*`) and the intent `sell_on_page` (`make page for`, `yi shafi don`, `ṣe ojú-ìwé fún`, `mee peeji maka`), reached as "add a page for …". `TestSellOnPageArgs`, `TestMatchIntent`.
- **The web** (Storefront > Fronts): Add a front > One product picks the product and its packages (named after the product unless named); the front's screen has **The page**: what it sells, the promise line, the stock line and up to six chips (`OneProductFront.tsx`, `lib/oneproduct.ts` with its vitest); its orders banner says the page takes orders once on.
- **The phone app** has no screen where fronts are edited; the words work in its chat as on WhatsApp, and the parity test has the conversation (`chat-taskiem-page`, web and app, light and dark, 360 and 390).
- **Done when** (`TestOneProductEndToEnd`, with the database): in WhatsApp, "sell smart ear cleaner on a page 1 for 25000, 2 for 45,000, 3 for 65k" shows the page and, on yes, makes it from the catalogue row, its workflow from `storefront-one-product` published and the page live at `/smart-ear-cleaner`; the public host draws the promise, the headline, the chips, the packages, the areas, the notice and the price bar; a buyer orders two to Lekki paying at the door (₦46,500) and the order is in Orders with the package as one line; on the web the front says what it sells, its settings change (a chip of another icon refused) and are published, and a second is added from the product with its warning. Playwright `one-product.spec.ts` does the web part and the buyer's on the public host, with axe in both themes at 360 and 390 px.
- **Not built:** a video on the page. Catalogue rows hold photos only (the media store takes images), so "a video where the owner adds one to the row" needs a video column and media for it first: a decision for the founder.

## 18. As built (L3)

Built 25 November 2026 on the decisions of §12, with the two findings of the L0/L1 review that were L3's (a keep taken for any live form; a restore link used up by opening it).

- **Settings** (migration 00840): `storefront_actions.followup_on`, `followup_wait` (10 to 1440 minutes, 30 by default), `followup_held_at` and `followup_released_at`. Off for an order form that exists; a trigger turns it on for an order form added to a One product front (§12, 6), so L2's builder and template get it without a setting of their own. Changed by `PATCH /v1/storefront/actions/{id}` (`followup_on`, `followup_wait`, `release_hold`; audited `storefront.followups`) and the chat words `follow-ups on` / `follow-ups off` (with `for <form or page>` when there are several order forms; `api/chat_followups.go`; en reviewed, ha/ig/pcm/yo drafts). The packages stay `PUT …/sells` (L1).
- **The keep.** A form that follows up draws the consent line under the email (`form.followup.consent`, in the page's language) and a keep token that is its submit token's **twin** (`formguard.Guard.Twin`: the same nonce and time, its own form id `…#keep`), never the page's state. `static/keep.js` sends the contact (a Nigerian phone, else an email) and the choice (package, size, area, or the basket's lines) when the phone or email loses focus with a valid value and when the page is hidden or left, never after the form is submitted, and the honeypot; the frame's verified website goes with it. `POST …/a/<form>/keep` (also on a front and under `/embed/`) answers 204 whatever happens, 429 past `FormKeepsPerIP` (120 an hour) or the form's bucket; the token is checked, the choice is read only from what the form offers. The keep names the status hash its page's order has (`IdempotencyKeyFor`), and `taskiem_public_form_keep(action, payload, sent)` refuses it unless the order form is on, follows up, and no order with that hash exists (the review's first finding). The api role drops it too if the order was recorded first, and keeps one record per page: a phone takes the place of an email typed on the same page. The record holds the contact masked for the owner (`… 1234`, `…@example.com`).
- **The follow-up** (`engine/forms/followup.go`, every 10 seconds in the forms loop): records past their wait are claimed across businesses (`taskiem_form_left_due`), counted as left once, then sent or marked why not: `off`, `stopped` (the contact stopped this business), held (the form's keeps today passed ten times its orders a day over four weeks, at least 3: `followup_held_at` set, an alert, nothing sent until released; one released today is not held again that day), `capped` (50 a day per form, 100 per business, or the contact's one a day and three a week across every business, `taskiem_form_followup_take` on keyed hashes only, or the outbox's own per-person limits), `limit` (the plan's `form_followups`: 200, 500, 1,000, 1,000, none), `stale` (held longer than a day past its wait), `failed`. A typed phone gets an **SMS**, an email an **email**, through the outbox (`sender.Queue`, counted in the plan's texts); WhatsApp is a seam, `forms.Service.FollowupChat`, nil until L4. The SMS: "Ada Stores: You started an order for Smart Ear Cleaner (2 items, N45,000) and did not finish. Finish it here: https://<page>/back/<token> (you can stop these messages there)", in the storefront's language (`form.followup.sms`, `form.followup.email`, `form.followup.email_subject`); the naira is written N so the text fits three parts, and the package is left out of a text still too long.
- **The link** (`engine/publicweb/back.go`, every theme's `back.html`): `GET /back/<token>` draws a page that claims nothing (`taskiem_public_form_restore_ok`), so a link preview uses nothing up (the review's second finding); **Finish my order** posts, claims the restore once and redirects to the order form with `?pkg=2&zone=Lekki` chosen; used or past 7 days it says so (410 on the post). **Stop these messages** posts to `/back/<token>/stop` (`taskiem_public_form_stop`, through the restore's record: every follow-up from that business to that contact).
- **Sent and came back.** `storefront.Orders.FormSent` (`forms.Service.OrderSent`) tells the records an order was recorded, by the buyer's phone and email, in its transaction: within the wait the record goes; after the follow-up it is came back, counted once.
- **What the owner sees.** `GET /v1/storefront/actions/{id}/left` (and `GET /v1/forms/{id}/left` for a hosted form that sells; 409 for one that sells nothing): the settings, the week's left, followed up and came back (from `form_left_stats`, per form and day, kept 90 days, so Came back outlasts its records), and the records that left, the contact masked, never opened. The web's **Left without ordering** (`/storefront/left/<id>`, from an order form's card, the Forms page and the alert) and the phone app's same page (`lib/chat/left_page.dart`, pictures light and dark) show them with the switch, the wait and **Release follow-ups**. The chat alert `left`: "3 people left your order form today" with "2 were followed up · <form>", or "Follow-ups held on <form>".
- **Retention and erasure.** Records last 7 days after their last keep or their follow-up; restore links 7 days; a contact's follow-ups across businesses 8 days; counts 90 days (`taskiem_form_left_purge`). Erasing a subject deletes the records sealed under it (a trigger on `subject_keys`). None of the new tables is readable by the api or public roles but through their functions (tested).
- **Tests.** `TestFormLeftKeptAndPurged` and `TestFormLeftFollowedUp` (engine/forms, with the database: keeps refused while off, one record a page, masked, isolation, sent within the wait, the restore once, came back counted, each reason not to send, the hold and its release, erasure, the purge); `TestTwin` (formguard); `TestFormLeftHalfFilledEndToEnd` (api, the **done when**: follow-ups on in WhatsApp, the consent line on the framed form, a keep with nothing but the number and the choice, no text within the wait and one after 30 minutes, the link's page opened twice without using it, Finish my order restoring two to Lekki, used once, the buyer orders, a keep from the sent page refused, the owner's view with **Came back** 1 and the contact masked, the chat alert, Stop, the wait's bounds, follow-ups off); vitest `left.test.ts`; Playwright `forms-left.spec.ts` (the owner's page, the buyer's form, the keep and the text from the real binary, the link's page, Finish my order and Stop, axe in both themes at 360 and 390 px); Flutter `left_page_test.dart` with pictures.

Differences from the plan, each for a reason:

- **The follow-up is the outbox's message, not a run of a managed workflow** (§10). A managed workflow per business is published under the business's governance (a second person's approval where four-eyes publishing is on) and counts against its runs; and `notify@1` `message` tries the person's linked chat first, which §6.4 forbids for a typed phone. The outbox is what `message` uses for SMS and email; the record (`form_left`: when, the channel, the outcome) and the audit of the settings are the trail.
- **The consent line and the text do not say "WhatsApp" or "Reply STOP"** (§6.2, §6.3). Texts go out from a sender name, so a reply cannot reach Taskiem, and L3 sends no WhatsApp; the words promise only what is so: one message by text or email, stopped from its link. `forms.Service.StopContact` is the seam for an inbound STOP (a two-way SMS number, or L4's WhatsApp). §13's review of the words still stands.
- **The link opens the form at the storefront's own address**, not the seller's page around the frame (§6.3): a frame learns only its parent's origin, not the page, so "where the storefront knows it" is never; the verified website is kept for the owner's list.
- **No `/f/<id>/keep`** (§10): a hosted form that sells is drawn at its order form's address (L1), so its keeps are that form's; a keep for a form endpoint is refused.
- **The hold's floor** (§6.5 says ten times the form's daily orders): a new form has none, so the floor is 3 a day (30 keeps) before anything is held. A starting figure, as the caps are.
