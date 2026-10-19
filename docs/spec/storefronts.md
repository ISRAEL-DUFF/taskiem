# Storefronts: a public page and order forms for businesses without a website

Status: **draft for review** (proposed in [decision 0036](../decisions/0036-storefronts.md)). Nothing here is built. Revised 2026-10-19 with the first answers to the open questions ([section 13](#13-open-questions)): own domains from the domain platform, Xenibase storage, SMS or email to customers, no free plan, new businesses only, both payment timings, bookings in v1, field agents.

*Storefront* is a working name, like Taskiem itself. It is a generic word, and is checked against competitors' marks before it ships (clean-room policy, rule 3).

## 1. Summary

A small business with no website gets a one-page public **storefront** from Taskiem:

- who it is;
- what it sells (a catalogue);
- what a visitor can do: order, book, ask for a quote, request a call back.

Every action is a hosted form, and every submission starts one of the business's workflows. That workflow tells the owner on WhatsApp or Telegram, lets them accept or decline, messages the customer, sends a payment link, and records the order.

The page is the business's public face, and the workflows are the back office it never had. **The page exists to feed workflows.** It is not a website builder, and it never becomes one ([non-goals](#3-goals-and-non-goals)).

## 2. Who it is for

| Person | Today | With a storefront |
| --- | --- | --- |
| **The owner**: a food seller, tailor, barber, phone repairer, provisions shop, salon, pharmacy, caterer, fashion seller on Instagram | Sells by WhatsApp chat, Instagram DMs and word of mouth. Prices are retyped for every customer, orders sit in chat history, stock lives in their head, payment is "send to my account" | A link and a QR code on the stall, the bag and their WhatsApp status. Orders arrive structured, in chat, with Accept and Decline. Customers get confirmations and payment links without the owner typing |
| **The customer**: anyone with a phone and a browser, often on a cheap Android phone with metered data | Asks "how much?" or "are you free Saturday?" in DMs and waits | Sees prices and photos, orders or books in under a minute, gets a confirmation by SMS, or email when they gave one |
| **A field agent**: a person who visits businesses where they trade and signs them up ([6.10](#610-field-agents)) | n/a | Sets up the business's account and storefront in about ten minutes, with the owner, and hands it over |

The first users are **new street businesses and SMEs**: Taskiem is not launched yet, so none of them has an account. Most will be signed up by field agents, the rest from chat or the web app.

## 3. Goals and non-goals

**Goals**

1. A business is public in **under 10 minutes**, entirely from WhatsApp or Telegram if it wants.
2. A customer can **browse and order on a slow connection** from a basic smartphone.
3. Every submission goes into a **workflow** with Taskiem's guarantees: idempotent, audited, personal data sealed, retried.
4. **Safe by default.** A storefront cannot be turned into a phishing page, a scam shop or a data-harvesting form without being caught.
5. **Cheap enough** for a business that earns little ([section 11](#11-plans-and-pricing)).

**Non-goals**

- A website builder: free layout, arbitrary pages, blogs, themes, or custom HTML, CSS or JavaScript.
- A marketplace or directory of businesses (later, perhaps; not in this spec).
- Holding customers' money. Payments go through the business's own provider account (Paystack, Moniepoint, OPay, iswallet) or a transfer to the business. Taskiem never holds funds.
- Delivery logistics, beyond a delivery address and the owner's own notes.
- Accounts for customers. A customer is a phone number on an order, never a Taskiem user.

## 4. Concepts

| Term | Meaning |
| --- | --- |
| **Storefront** | One public page per tenant (one per sub-tenant for partners): business details, a catalogue, actions. A draft is edited; a published snapshot is served |
| **Catalogue** | The items on the page. It is a [data table](../data-tables.md) whose columns follow a catalogue shape ([6.2](#62-catalogue)), so the owner edits it like any table: the Tables page, CSV, or `add to products` in chat |
| **Action** | Something a visitor can do: `order`, `enquiry` and `callback` in v1; `booking` and `custom` later. An action has a form and a workflow |
| **Hosted form** | The form an action shows. Its fields come from the workflow's input schema ([6.3](#63-actions-and-hosted-forms)) |
| **Submission** | One filled form. It is recorded, and it starts a run of the action's workflow through a new `storefront` trigger ([8.2](#82-the-storefront-trigger)) |
| **Order** | A submission of the `order` action. It is a row in an `orders` data table the storefront keeps, and its status is moved on by the workflow |
| **Booking** | A submission of the `booking` action: a customer's appointment for a service at a time, a row in a `bookings` data table, checked against the business's availability ([6.9](#69-bookings)) |
| **Field agent** | A person who signs businesses up where they trade, with the owner present ([6.10](#610-field-agents)) |

## 5. Journeys

### 5.1 The owner sets up, from chat

1. Owner: `make my page`.
2. Taskiem asks, one question at a time:
   - the business name;
   - what it sells (one line);
   - the area or address;
   - opening hours;
   - a photo (optional; a WhatsApp or Telegram photo is accepted).
3. Owner: `add product Jollof rice 2000` (repeatable), or sends a photo with the caption `Jollof rice ₦2,000`.
4. Taskiem shows a preview link and asks for **yes** to publish.
5. On publish, Taskiem sends the link, a QR code image, and a short text to paste in their WhatsApp status.

The same is possible on the web, under **Storefront** in the web app. A field agent can do it on the owner's behalf with the owner's consent, recorded ([7.1](#71-verification)).

### 5.2 A customer orders

1. The customer opens `ada-kitchen.<storefront domain>`, from a QR code, a status post or a link.
2. They see the business card, then the catalogue: photo, name, price and an **Add** button for each item.
3. Their basket shows the total. They tap **Order**.
4. The order form asks for name, phone, and delivery or pickup, plus anything the owner's workflow asks for. They submit.
5. A confirmation page shows an order reference and "Ada's Kitchen will confirm on WhatsApp/SMS".
6. They receive the confirmation, then the owner's decision, and a payment link if the owner set payment up.

The page works without JavaScript for browsing; the basket uses a small script and falls back to a one-item order form without it.

### 5.3 The owner handles it, from chat

```
[Ada's Kitchen] New order #A7K2 from Tunde (0801 ••• 4567)
2 × Jollof rice  ₦4,000
1 × Chicken      ₦1,500
Total ₦5,500 · Delivery to Yaba
[Accept] [Decline]
```

- **Accept**: the customer is told, and gets a payment link if set up. The order becomes `accepted`, and stock goes down if tracked.
- **Decline**: the customer is told, with an optional reason.
- `orders` lists today's orders, and `order A7K2 ready` or `delivered` moves one on.

This is the ready-made default workflow ([6.5](#65-default-workflows)). Owners and builders can change it on the canvas like any workflow.

### 5.4 A customer books

1. On a barber's page, the customer taps **Book** on "Haircut, 30 min, ₦3,000".
2. They pick a day, then one of the free times shown ("Sat 10:00, 10:30, 11:30..."). The slot is held for them for 10 minutes.
3. They give their name and phone (and email if they want), then submit: payment first if the business takes it at booking.
4. They get a reference, then a confirmation once the owner accepts (or at once, if the business confirms automatically), a reminder the day before and two hours before, and a link to reschedule or cancel.

### 5.5 A field agent signs a business up

1. The agent, signed in to Taskiem with the agent role, starts **New business** on their phone, with the owner beside them.
2. They enter the owner's WhatsApp number. The owner gets a code and reads it out: that is the owner's consent, and links the owner's number.
3. Together they take photos, set prices or services and hours, and choose a domain from the names offered.
4. They publish. The agent's phone shows the QR code to print, and the owner gets the link and a welcome message in WhatsApp with the commands they will use.
5. The agent's access to that business ends at handover, and the business is recorded as signed up by that agent.

## 6. Functional specification

### 6.1 The storefront

| Part | Detail |
| --- | --- |
| Business card | Name, a one-line description, a logo or photo, address or area served, a map link (no embedded map), opening hours (with "open now" or "closed"), a phone call button, and a **chat on WhatsApp** button (`wa.me` link, the owner's number) |
| Template | One of 2–3 fixed, mobile-first templates (v1: *Simple* and *Grid*), with an accent colour chosen from a palette. Nothing else is styled by the owner |
| Language | Page text is in English, Pidgin, Yoruba, Hausa or Igbo (the catalogues in `engine/lang`, drafts until reviewed); the owner writes their own content |
| Status | `draft` → `published`. Taken down by Taskiem is `suspended`; switched off by the owner is `closed`. A closed page shows the card and "not taking orders now" |
| "Closed today" | One command (`close shop today`) or one toggle stops new orders until a time, without unpublishing |
| Address | The business's **own domain**, provided with its plan by the domain platform ([6.6](#66-addresses-domains-and-sharing)); a platform address `<slug>.<preview domain>` for previews and as a fallback. The slug and the domain name are checked against reserved and look-alike names ([7.2](#72-names-and-impersonation)) |
| Sharing | A QR code (PNG, and a printable A6 PDF with the name and link); Open Graph tags so the link previews well in WhatsApp; a short text for the WhatsApp status |

### 6.2 Catalogue

A catalogue is a data table the storefront marks as its catalogue. It can be made at setup or picked from existing tables when it has the required columns:

| Column | Type | Required | Notes |
| --- | --- | --- | --- |
| `name` | text | yes | Up to 80 characters |
| `price_kobo` | integer | yes | Prices are kept in kobo like everywhere in Taskiem, and shown in naira |
| `description` | text | no | Up to 300 characters |
| `image` | text | no | A media reference ([6.7](#67-media)), never an external URL |
| `available` | boolean | no | `false` hides the item. Default `true` |
| `category` | text | no | Groups items on the page |
| `stock` | integer | no | When present, ordering more than the stock is refused, and the default workflow lowers it on Accept |
| `sort` | integer | no | Order on the page; otherwise by name |

- **Limits:** up to 200 visible items per page in v1, 2,000 on paid plans.
- **What is public:** the page reads only the catalogue's public columns. Columns marked personal can never be catalogue columns.
- **When changes appear:** catalogue edits are shown within a minute, because the published page caches the catalogue ([8.4](#84-rendering-caching-and-performance)).

### 6.3 Actions and hosted forms

Each action has a kind, a label, the workflow it starts, and its form. A form's fields are the **required and optional inputs of the workflow's input schema**, rendered by the same field logic the web app and WhatsApp already use (`whatsapp.Field`, schema-checked):

- **Field types:** text, number, phone (Nigerian format checked), email, choice, date (v2), and a delivery choice (delivery or pickup, with an address when delivery).
- **The `order` action** also carries the basket. Item ids, names, unit prices and quantities are taken from the catalogue **on the server**, never from the browser.
- **Fields refused in any storefront form:** card numbers, CVV, PINs, passwords, OTPs, BVN, NIN and bank login details. This is checked when the action is saved, by field names and `x-pii` categories, and again on submission by the PII detector (spec 9.3), which rejects values that look like card numbers or BVNs. A storefront never collects what a scam page would ([7.3](#73-forms-cannot-phish)).
- **Personal data:** customer name, phone and address are `x-pii` and sealed at rest like every input.
- **Prefill:** fields can be prefilled from the URL (`?item=…`), which is useful for QR codes on a single product.

| Kind | v1? | Form | Starts |
| --- | --- | --- | --- |
| `order` | yes | basket + name + phone + delivery/pickup (+ the workflow's other inputs) | The order workflow; an `orders` row is created before the run |
| `enquiry` | yes | name + phone + message | Its workflow (default: forward to the owner in chat) |
| `callback` | yes | name + phone + best time | Its workflow (default: remind the owner) |
| `booking` | yes | service + a slot from the business's availability + name + phone (+ email) | The booking workflow; a `bookings` row holds the slot before the run ([6.9](#69-bookings)) |
| `custom` | later | any input schema the rules above allow | Any workflow |

**What the customer sees after submitting:**
- An immediate confirmation with a reference: the run is queued, not awaited.
- An optional **status link** (v2): a signed, unguessable URL showing the order's status as the workflow moves it.
- Never a "wait while we process" page tied to a run finishing. Street connections drop, and the run's outcome reaches the customer by message instead.

### 6.4 Submissions and orders

- **Recording:** a submission is recorded before anything else, and acknowledged with its reference.
- **Idempotency:** the form carries a one-time token, so submitting twice (a double tap, a retry on a bad connection) makes one submission.
- **Payment timing:** each order action is set to one of two modes, chosen by the business:
  - **Pay when ordering:** after the form, the customer is taken to the business's payment page (a provider link made at once). The order is `new` until the provider confirms payment, then `paid`, and only then goes to the owner. Unpaid orders expire after 30 minutes and release any stock or slot they held.
  - **Pay after the owner accepts** (the default): the order goes to the owner first, and a payment link is sent with the acceptance. Cash or transfer on delivery is a variant: no link, and the owner marks it paid.
- **Orders:** the storefront keeps an `orders` data table with:
  - `reference` (6 characters, unambiguous: no 0, O, 1 or I), `status`, `items` (json), `total_kobo`;
  - customer `name`, `phone` and `address`, all personal;
  - `channel` (`web` or `qr`) and `created_at`.
- **Order statuses:**
  - pay after acceptance: `new` → `accepted` | `declined` → `paid` (unless cash on delivery) → `ready` → `completed`;
  - pay when ordering: `awaiting_payment` → `paid` → `accepted` | `declined` (a declined paid order is refunded through the provider, by the owner or automatically where the provider allows) → `ready` → `completed`;
  - `cancelled` and `expired` from any open state.
  The default workflows move them. The owner can move them from chat or the Tables page.
- **Totals** are computed on the server from catalogue prices at submission time, and are what the order records. A later price change does not change an order.

### 6.5 Default workflows

Making an action installs a ready-made workflow from a template (the template library already exists). The default order workflow:

1. **Tell the owner:** an approval step addressed to the storefront's owners. On WhatsApp or Telegram it arrives with **Accept** and **Decline** (the existing approval delivery), and it times out after a configurable time (default 2 hours: "the business did not answer").
2. **On Accept:**
   - message the customer;
   - set the order `accepted`;
   - lower stock where tracked;
   - with pay after acceptance, create a payment link (Paystack `transaction.initialize`, or Moniepoint/OPay equivalents) and send it. The order becomes `paid` on the provider's webhook (a connector event trigger the template wires).
3. **On Decline or timeout:** message the customer, and set the order `declined`. A paid order is refunded ([6.4](#64-submissions-and-orders)).

With pay when ordering, the workflow starts with the payment: it waits for the provider's confirmation (a signal step, as the Payrolla workflow waits for a payout's outcome) before the owner is asked.

**Messages to customers** go by **SMS** (Termii, the business's registered sender ID where it has one, else the platform's), and also by **email** when the customer gave an address (through the platform's mail sender, from a no-reply address with the business's name and its reply-to). WhatsApp messages to customers are a later phase. In every case the customer can reach the business through the **chat on WhatsApp** button.

### 6.6 Addresses, domains and sharing

- **Every business gets its own domain** (`adaskitchen.ng`) as part of its plan, from the **domain platform** the group already runs. At setup, Taskiem asks the domain platform for available names, registers the chosen one, and sets its DNS to point at the storefront servers. A certificate is issued automatically. Because the domain platform is the registrar, none of this is done by the owner. *The integration contract (API, ownership of the domain if the business leaves, renewals with the plan, transfers) is to be agreed separately.*
- **Owning the domain:** the domain is registered in the business's name, so it stays theirs. If the plan lapses, the page stops and the domain points to a "this business is not on Taskiem now" page until the registration itself lapses.
- **Preview domain:** pages are also reachable on `<slug>.<preview domain>`, a **dedicated registrable domain**, never under the app's own (never `<slug>.app.taskiem.com`). It is used for previews before publishing, and as a fallback while a new domain is being set up. Cookies, the web app's origin and its CSP can never be reached from a page on either ([8.3](#83-isolation-from-the-app)).
- **Slugs:** 3–30 of `a-z 0-9 -`, unique, not reserved ([7.2](#72-names-and-impersonation)).
- **A business's own existing domain** can be used instead, verified by a DNS TXT record as partners' embed domains are ([embedding §10](../embedding.md#10-custom-domains)).
- **Sharing:** QR code, printable card, status text, and Open Graph tags (title, description, image).

### 6.7 Media

Logos and product photos are uploaded by the owner (web, or a photo in chat).

**Processing:**
- **On upload:** checked by type (JPEG, PNG, WebP), size (5 MB) and dimensions; EXIF metadata, including location, is stripped.
- **Sizes:** 320 and 800 pixels wide, in WebP with a JPEG fallback, using Xenibase storage's image transforms where they cover this.
- **Moderated** before publishing ([7.4](#74-content-and-moderation)).

**Storage:** in **Xenibase storage** (released on production), in a bucket of the business's Xenibase project, the same project as its data tables. Data tables only hold the object's reference. **Taskiem needs a storage integration first**: upload, public-read objects for published images, image transforms, and deletion. This was planned as P4-T1 in the Xenibase plan and is now a v1 dependency.

**Serving:** images are served through the storefront's own domain (proxied and cached by the storefront servers), with long cache lifetimes and content-hashed names, so a page never sends visitors to another host.

### 6.8 Managing it from chat

| Message | What happens |
| --- | --- |
| `make my page`, `my page` | Start setup, or show the link, status and today's orders count |
| `add product <name> <price>`, a photo with a caption | Add an item (summary, then **yes**) |
| `price <item> <price>`, `hide <item>`, `show <item>`, `stock <item> <n>` | Change the catalogue |
| `orders`, `order <ref>`, `order <ref> ready` | See and move orders |
| `bookings`, `today`, `booking <ref> cancel`, `move <ref> to <day> <time>`, `off <date>` | See today's and coming bookings, change them, take a day off |
| `add service <name> <minutes> <price>`, `hours mon-fri 9-6` | Set services and opening hours |
| `close shop`, `close shop today`, `open shop` | Stop or resume orders |
| `qr` | Send the QR code and the printable card |

Every change is shown and confirmed with **yes**, needs `table.edit` (or a new `storefront.manage` permission), and is audited, as data-table changes from chat are today.

### 6.9 Bookings

For services (barbers, salons, tailors' fittings, repairers, tutors, clinics), a **services** data table and the business's **availability** stand where a catalogue and stock stand for goods.

**Services table** (catalogue-shaped, [6.2](#62-catalogue)):

| Column | Notes |
| --- | --- |
| `name`, `description`, `image` | As for products |
| `price_kobo` | Optional (some services are priced on the day: "from ₦5,000" shows as such) |
| `duration_minutes` | 5 to 480, required |
| `capacity` | How many customers at once (default 1: one chair; a class may take 10) |
| `buffer_minutes` | Time kept free after each booking (default 0) |
| `available` | As for products |

**Availability** (part of the storefront's settings):
- weekly hours per day, with breaks (for example Mon–Fri 09:00–18:00, break 13:00–14:00);
- days off and holidays as dates;
- a booking window: how far ahead (default 30 days) and how soon (default 2 hours from now);
- the time zone is the tenant's (Africa/Lagos by default).

v1 has one calendar per business (one barber's chair, or one team booked as a whole). Several staff with their own calendars are v2.

**Slots:**
- **Offering:** offered slots are the availability cut into the service's duration plus its buffer, minus what is booked or held.
- **Holding:** a slot is **held** when the customer picks it, for 10 minutes or until payment is confirmed (pay at booking), so two customers cannot take the same slot.
- **Concurrency:** holds and bookings are rows checked in one transaction against capacity, so concurrent picks cannot overbook.

**The bookings table** (kept by the storefront, like `orders`):
- `reference`, `service`, `starts_at`, `ends_at`, `status`;
- customer `name`, `phone` and `email` (personal), `total_kobo`, `created_at`.

**Booking statuses:** `held` → `awaiting_payment` (pay at booking) → `requested` → `confirmed` | `declined` → `completed` | `no_show`; or `cancelled` and `expired`.

**Payment timing**, as for orders: at booking (deposit or full price, the business chooses), or after the owner accepts, or at the appointment.

**Default booking workflow:**
- **Confirmation:** owner approval with Accept and Decline in chat (or automatic confirmation, a setting).
- **Customer messages:** SMS (and email when given) on confirmation, a reminder the day before and two hours before, and a message if the owner cancels.
- **Owner reminders:** today's appointments sent to the owner each morning.
- **Reminder timing:** reminders are timer steps in the run, so they survive restarts like any wait.

**Changes:** the customer's messages carry a signed link to a small page to **reschedule** (to another free slot) or **cancel**, up to a cut-off the business sets (default 2 hours before). A change moves the booking's run on (a signal to the waiting workflow) and tells the owner. The owner can do the same from chat: `bookings`, `booking <ref> cancel`, `move <ref> to Sat 11:00`.

### 6.10 Field agents

Most businesses are signed up in person. A **field agent** is someone who visits businesses where they trade and sets up their account and storefront with them, the way POS and mobile-money agents work today.

| Part | Design |
| --- | --- |
| Who | People an operator (or, later, a partner) registers as agents; whether they are staff or paid per business is open ([13](#13-open-questions)) |
| Role | A platform-level `agent` identity, apart from tenant roles: it can create a business (tenant), set up its storefront and catalogue, and publish, only during an **onboarding session** for that business |
| Consent | The owner's WhatsApp number receives a code; the session starts only when the owner gives it to the agent. The owner then owns the tenant; the agent is never a member of it |
| Handover | Publishing (or 24 hours) ends the session; after that the agent cannot see or change the business. The owner can invite the agent back (a support visit) for a time-limited session |
| Accountability | Every business records its agent; every action of an onboarding session is in the business's audit chain under the agent's name; operators see agents' businesses, their verification outcomes and reports against them |
| Verification | An agent attests that they met the business at its address and saw its owner (with a photo of the stall, location attached), which counts as business verification ([7.1](#71-verification)) |
| Commission (if adopted) | Counted from businesses that publish and pay; payouts are outside this spec |

## 7. Trust and safety

Public pages made by businesses Taskiem has not met will be abused unless the design prevents it. These controls are part of v1.

### 7.1 Verification

| Level | How | Unlocks |
| --- | --- | --- |
| **Phone verified** | The owner's account has a linked WhatsApp number or Telegram account, or passes a phone OTP | Publish a page; `enquiry` and `callback` |
| **Business verified** | A name and address check, and optionally a CAC number or an ID check through Dojah, Prembly or Youverify (connectors that exist), reviewed when unclear | Pay-when-ordering and payment links, a "verified" badge, more than 50 orders or bookings a day |
| **Agent onboarded** | Set up by a field agent who attests they met the business and its owner at its address ([6.10](#610-field-agents)) | Counts as business verified, with the agent recorded |

`order` and `booking` with payment after acceptance (or at delivery) are open to phone-verified businesses: no money passes until the owner accepts.

A verification is a record with who did it, how and when, and appears in the audit chain.

### 7.2 Names and impersonation

- **Reserved slugs:** platform words (`www`, `app`, `api`, `admin`, `login`, `secure`, `support`...).
- **Look-alike slugs refused:** names of banks, fintechs, telcos, government agencies and large brands, from a maintained list, compared after normalising look-alike characters (`0`/`o`, `1`/`l`, repeated letters).
- **Business names** are checked against the same list. A match goes to review instead of being refused outright.
- A storefront can never show "bank", "verify your account" or similar flows: its actions are only the kinds in [6.3](#63-actions-and-hosted-forms).

### 7.3 Forms cannot phish

- **No owner code:** no custom HTML, CSS or JavaScript, and no embedding of other sites.
- **Links:** only `wa.me`, `tel:`, the map link, and the business's verified domain.
- **Fields:** the refused fields of [6.3](#63-actions-and-hosted-forms) are refused when saved and when submitted.
- **Payments:** only through the business's payment-provider link, created by the workflow and sent by message. Never a form asking for account or card details.
- **Footer:** every page carries a small footer with Taskiem's name, a **Report this page** link, and a line saying Taskiem never asks for card details, PINs or OTPs.

### 7.4 Content and moderation

- **Automatic checks** on publish and on every change of text or images. **Proposed (awaiting decision, [13](#13-open-questions)):**
  - every upload and text change is checked by a model through Taskiem's existing AI layer, against a written content policy, in the five languages; it answers allowed, flagged or blocked, with a reason;
  - a cheap word list catches the obvious first;
  - price sanity flags a ₦0 item or a price above a ceiling.
  Anything flagged waits in a **review queue** in the operator console instead of going public. The alternatives (a dedicated image-safety service, which cannot judge scams or counterfeits; or people only, which does not scale) are recorded in decision 0036.
- **Prohibited categories**, from a list Taskiem publishes:
  - weapons, drugs, counterfeit goods;
  - "investment" and "doubling" schemes;
  - financial services that need a licence the business cannot show;
  - adult services.
- **Reports** from the page footer reach the operator console, with the page, reason and reporter's optional contact.
- **Actions:**
  - Operators can **suspend** a page: it shows "This page is unavailable" and its forms stop.
  - A suspension is recorded with a reason, the owner is told in chat, and an appeal is a reply.
  - Pages with several reports are suspended pending review automatically.

### 7.5 Abuse of forms

- **Rate limits:**
  - per page and per visitor IP, in buckets: 5 submissions in 10 minutes, 30 a day;
  - per phone number on orders;
  - per page per hour, with alerts to the owner and operators when one trips.
- **Bot defences without third-party trackers:**
  - a honeypot field;
  - a minimum fill time;
  - a light proof-of-work in the form's script, which falls back to a phone OTP for a page under attack.
- **Phone OTP on orders (optional):** the customer gets a code by SMS before the order is taken. Off by default, on automatically for a page seeing spam.
- **Plan limits:** submissions per day and orders per day per plan; beyond them, the form shows "orders are paused" and the owner is told.

## 8. Architecture

### 8.1 Data model (new, tenant data under forced row-level security)

| Table | Holds |
| --- | --- |
| `storefronts` | One per tenant: slug, status, template, accent, business card fields, hours, language, catalogue table id, orders table id, services table id, bookings table id, Xenibase bucket, verification level, published snapshot version, closed-until |
| `storefront_actions` | Per storefront: kind, label, workflow id, enabled, sort, form options (prefill, OTP) |
| `storefront_snapshots` | Published versions: the rendered page model (business card, actions, catalogue *structure*), content-hashed; the catalogue *rows* are read live with a short cache |
| `storefront_submissions` | Reference, action, idempotency token hash, sealed personal fields, basket, totals, the run it started, IP hash, status |
| `storefront_media` | Upload records: hashes, sizes, moderation state, and the object's key in the business's Xenibase storage |
| `storefront_domains` | The business's domain: name, source (the domain platform, or one the business brings), the platform's registration id, DNS and certificate state, renewal date |
| `storefront_availability` | Opening hours per service or for the whole business, slot length, buffer, how far ahead people can book, and closed days |
| `storefront_holds` | A slot held while a customer pays: service, start, end, submission, expiry. A unique constraint on (storefront, service, start) for live holds and confirmed bookings prevents double booking |
| `storefront_moderation` | Each check: what was checked, the model's verdict and reasons, the reviewer, the decision |
| `storefront_reports` | Reports from the public, for operators |
| `storefront_agents` (platform) | Field agents: the Taskiem account, region, status, who registered them |
| `storefront_onboarding` (platform) | A sign-up an agent runs: agent, tenant, the owner's number, consent code hash, consent time, expiry. The agent's access to the tenant ends at expiry or when the owner takes over |
| `storefront_slugs` (platform) | Slug → tenant, reserved and blocked names; read by the public host through a narrow `SECURITY DEFINER` function, as other cross-tenant lookups are |

Orders and bookings live in the tenant's `orders` and `bookings` **data tables** on Xenibase, and services in its `services` table, so owners and workflows use them like any table. Photos are objects in the same Xenibase project's storage. The `storefront_submissions` row is Taskiem's own record and link to the run.

### 8.2 The storefront trigger

New trigger type `storefront` in wd/v1:

```json
"trigger": { "type": "storefront", "config": { "action": "order" } }
```

**The run's trigger input:**
- `submission.reference`, `submission.action`, `submission.fields` (with `x-pii` fields sealed);
- `submission.basket` (`items`: id, name, unit price, quantity, line total) and `submission.total_kobo`;
- for a booking, `submission.booking` (service id and name, start, end, the hold id) and `submission.deposit_kobo`;
- `submission.payment` (`at_order` or `after_accept`, and the payment reference when paid at the time);
- `storefront.slug` and `storefront.name`;
- `submission.channel`.

**How it starts:** a submission starts a run through the ingest pipeline, deduplicated on the submission's id (spec 8.1), within the tenant's ingest limits. A workflow with this trigger can be used by one action of one storefront.

### 8.3 Isolation from the app

- **Separate domains:** pages and forms are served on the business's own domain and the preview domain only. That host serves nothing of the app or the API, the same rule as custom embed domains.
- **No cookies:** a form's idempotency token is in the form itself, so there is nothing for a page to steal or confuse.
- **Strict CSP:** `default-src 'self'`, no inline scripts except the hashed basket script, `frame-ancestors 'none'`, `form-action 'self'`.
- **Where it runs:** pages are rendered by the `edge` role (or a new `storefront` route group on it). They read through narrow functions, never with a tenant's broad scope, so a page can only ever see its own published snapshot and catalogue's public columns.

### 8.4 Rendering, caching and performance

- **Rendering:** on the server (Go templates), as HTML that works without JavaScript. Page weight is **under 60 KB before images**, and the basket script under 10 KB.
- **Images:** lazy-loaded, sized for the screen, WebP with fallback.
- **Caching:** the published snapshot is cached for a long time and invalidated on publish; catalogue rows are cached for 60 seconds; responses carry `ETag`s. A CDN or caching proxy in front is optional, and the design does not depend on one.
- **Target:** the first view is usable in under 3 seconds on a 3G connection with a low-end Android phone. A page budget test in CI enforces the sizes.

### 8.5 API

| Endpoint | Purpose |
| --- | --- |
| `GET/PUT /v1/storefront` | The draft: card, template, slug, hours, catalogue table, settings |
| `POST /v1/storefront/publish`, `POST /v1/storefront/close` | Publish a snapshot; close or reopen |
| `GET/POST/PATCH/DELETE /v1/storefront/actions[/{id}]` | Actions, each tied to a workflow |
| `POST /v1/storefront/media` | Upload a logo or product photo |
| `GET /v1/storefront/submissions` | Submissions, with personal fields masked as everywhere |
| `GET /v1/storefront/qr` | QR code and printable card |
| `GET/POST /v1/storefront/domains...` | The business's domain: search and claim one from the domain platform, or bring one; DNS and certificate state |
| `GET/PUT /v1/storefront/availability`, `GET /v1/storefront/bookings` | Hours and slots; bookings, with accept, reschedule and cancel |
| `POST /v1/storefront/onboarding`, `POST /v1/storefront/onboarding/{id}/consent`, `POST /v1/storefront/onboarding/{id}/handover` | Field agents: start a sign-up, confirm the owner's code, hand the page over |
| `GET /v1/ops/storefront-moderation`, `POST .../{id}/decide` | The review queue |
| Public, on the storefront domain: `GET /` (the page), `GET /a/{action}` (a form), `POST /a/{action}` (submit), `GET /slots/{service}` (free slots), `GET/POST /b/{reference}` (a customer reschedules or cancels, by the link in their SMS), `GET /media/{hash}`, `POST /report` | Served by the edge, no session |
| Operators: `/v1/ops/storefronts...` | Search, reports, suspend, restore, verification review |

**Permissions:** a new `storefront.manage` (owners and admins by default) for the page, actions and publishing; catalogue and order edits stay under `table.edit`. Everything is audited (`storefront.publish`, `storefront.suspend`, `storefront.action.create`...).

### 8.6 What it reuses

- Data tables (catalogue and orders) and the WhatsApp and Telegram conversation (owner commands, approvals).
- The template library (default workflows), connectors (Termii, Paystack, Moniepoint, OPay, iswallet, Dojah, Prembly), and the PII sealing and detector.
- Xenibase storage for photos; the group's domain platform; the AI layer for moderation;
- Plan limits and billing; custom-domain verification; the operator console; the audit chain.

What is new: the public renderer, the `storefront` trigger, media, bookings and slots, field-agent onboarding, moderation, and abuse controls.

## 9. Privacy (NDPA)

- **Roles:** the business is the **data controller** for its customers' details, and Taskiem the processor. The storefront terms, which owners accept before publishing, say so.
- **Notice:** every form shows a short notice: who receives the details (the business by name), why (to handle the order), and a link to a privacy page Taskiem generates for the business from its card.
- **Collection:** only the fields the action needs. Personal fields are sealed at rest, masked in messages and the web app (spec 9.3), and access to unmasked values is audited.
- **Retention:** submissions and their personal fields are kept for the business's retention setting (default 12 months) and then erased. The existing erasure function applies to a customer's request through the business.
- **No tracking:** no analytics scripts, no third-party fonts or trackers, and no cookies on pages. Visit counts for the owner come from server logs, aggregated without IPs ([10](#10-what-the-owner-sees)).

## 10. What the owner sees

- **In chat:** new orders, daily summaries ("Today: 12 orders, ₦48,500, 2 declined"), and alerts (a page under spam, a suspension, stock running out).
- **In the web app:** the Storefront page (draft and preview, actions, sharing, verification) and the orders table. Simple numbers: visits per day (aggregated), orders, conversion, and top items.

## 11. Plans and pricing

There is **no free plan**. A storefront comes in paid plans, each including the business's **domain** from the domain platform (its registration and renewal are part of the price) and an SMS allowance. The cheapest Taskiem plan today, Starter at ₦15,000 a month, is priced for automation customers, so storefront plans sit below it. Proposed shape (prices are placeholders for B1, like the others):

| Plan | For | Storefront |
| --- | --- | --- |
| **Shop** (new) | A single trader or service | Own domain; 50 products or services; orders and bookings with fair-use limits; payment after acceptance; the default workflows (adjustable in chat, not on the canvas); a monthly SMS allowance |
| **Shop Plus** (new) | A busy shop or salon | 500 items; pay-when-ordering and deposits; email to customers with the business's name; editing workflows on the canvas; a larger SMS allowance; several owners or staff |
| Starter and above | Businesses automating beyond the storefront | Storefront included; 2,000 items; removing the footer brand (never the safety line); all workflows |

- **Annual pricing:** with the domain included, an annual option suits domain registration.
- **Trial:** whether to offer one (for example a paid first month at a discount, the domain bought only on conversion) is open.
- **Metering:** SMS costs money per message, so the allowance is metered like WhatsApp templates (`plans.yaml` gains `storefront_items`, `storefront_orders_monthly`, `storefront_bookings_monthly` and `sms_monthly`), with top-ups.

## 12. Phasing

| Phase | Scope |
| --- | --- |
| **v1** | Page (card, catalogue, 2 templates) on the business's own domain from the domain platform; `order`, `booking`, `enquiry` and `callback` actions; both payment timings (Paystack first); the `storefront` trigger; default workflows with owner approval in chat; SMS and email to customers; booking reminders and reschedule or cancel links; setup and management from chat and web; field agents and onboarding sessions; phone and agent verification; the trust-and-safety controls in section 7; media on Xenibase storage; QR and sharing; the Shop plans |
| **v2** | Business verification through ID providers with the badge; a status link for orders; several staff calendars for bookings; Moniepoint and OPay payment links; agent commissions (if adopted) |
| **v3** | `custom` forms, WhatsApp messages to customers (own numbers), multi-branch businesses, a public directory (if wanted), simple analytics |

**v1 depends on:**
- the domain platform integration (register, DNS, renew);
- the Xenibase storage integration;
- a storefront domain for previews;
- a decision on moderation;
- Termii sender IDs and an email sender (P4-O1).

**Success measures for v1:**
- time from `make my page` to published (target: median under 10 minutes);
- the share of pages with at least one order or booking in their first week;
- orders and bookings per active page per week;
- businesses signed up per agent per day, and the share still active after 30 days;
- reports per 1,000 pages;
- p75 page load on 3G.

## 13. Open questions

**Answered (2026-10-19):**
- domains come from the group's domain platform;
- photos go in Xenibase storage;
- customers hear back by SMS, or email when given;
- there is no free plan;
- the first users are new businesses;
- both payment timings are supported;
- bookings are in v1;
- field agents set pages up.

**Still open:**

1. **Field agents:**
   - staff, or independent and paid per business?
   - do partners (cooperatives, traders' associations) run agents?
   - is there commission?
2. **Moderation:** the AI layer with a human review queue (proposed in [7.4](#74-content-and-moderation)), a dedicated image service, or people only? Who staffs the review queue?
3. **The domain platform integration:**
   - its API (search, register, DNS, renew, transfer);
   - whose name the domain is registered in;
   - what happens to the domain if the business stops paying;
   - which top-level domains (`.ng`, `.com.ng`, `.com`) are offered.
4. **The preview domain:** which registrable domain previews and fallbacks use (after the trademark search, L3).
5. **The name** of the feature as owners and customers see it. *Storefront* is a working name; it could share a brand with the domain platform.
6. **Plans:** the prices, SMS allowances and a trial.
7. **Refunds** when a paid order is declined: automatic through the provider where possible, or always by the owner?

## 14. Where the ideas come from

From public material only, per the clean-room policy:

- **Hosted forms that start a workflow** (a form as a trigger, an immediate acknowledgement, a completion page): n8n's Form Trigger, Zapier Forms and Activepieces' Human Input, from their user documentation.
- **One-page business pages with a catalogue and chat ordering:** common across WhatsApp Business catalogues, link-in-bio pages and payment-provider storefronts, from their marketing pages.

What is Taskiem's own:
- the catalogue and orders as data tables the owner already edits;
- the page as the front door to approval and workflow automation in chat;
- personal data sealed by default;
- the trust-and-safety design;
- the pricing for street businesses.
