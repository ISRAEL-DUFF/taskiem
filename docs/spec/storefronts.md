# Storefronts: a public page and order forms for businesses without a website

Status: **draft for review** (proposed in [decision 0036](../decisions/0036-storefronts.md)). Nothing here is built. Questions still to answer are in [section 13](#13-open-questions).

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
| **The owner**: a food seller, tailor, phone repairer, provisions shop, salon, pharmacy, caterer, fashion seller on Instagram | Sells by WhatsApp chat, Instagram DMs and word of mouth. Prices are retyped for every customer, orders sit in chat history, stock lives in their head, payment is "send to my account" | A link and a QR code on the stall, the bag and their WhatsApp status. Orders arrive structured, in chat, with Accept and Decline. Customers get confirmations and payment links without the owner typing |
| **The customer**: anyone with a phone and a browser, often on a cheap Android phone with metered data | Asks "how much?" in DMs and waits | Sees prices and photos, orders in under a minute, gets a confirmation by SMS or WhatsApp |
| **A field agent** (optional): staff of Taskiem, a partner or a cooperative who sign businesses up | n/a | Sets up a page for a business in minutes and hands it over |

Organisations already on Taskiem (Payrolla's and iSpend's customers, partners' sub-tenants) are the first users. Street businesses with no Taskiem account at all are the target ([open question 1](#13-open-questions)).

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

## 6. Functional specification

### 6.1 The storefront

| Part | Detail |
| --- | --- |
| Business card | Name, a one-line description, a logo or photo, address or area served, a map link (no embedded map), opening hours (with "open now" or "closed"), a phone call button, and a **chat on WhatsApp** button (`wa.me` link, the owner's number) |
| Template | One of 2–3 fixed, mobile-first templates (v1: *Simple* and *Grid*), with an accent colour chosen from a palette. Nothing else is styled by the owner |
| Language | Page text is in English, Pidgin, Yoruba, Hausa or Igbo (the catalogues in `engine/lang`, drafts until reviewed); the owner writes their own content |
| Status | `draft` → `published`. Taken down by Taskiem is `suspended`; switched off by the owner is `closed`. A closed page shows the card and "not taking orders now" |
| "Closed today" | One command (`close shop today`) or one toggle stops new orders until a time, without unpublishing |
| Address | `<slug>.<storefront domain>`; the slug is chosen at setup and checked against reserved and look-alike names ([7.2](#72-names-and-impersonation)). Custom domains on paid plans ([6.6](#66-addresses-domains-and-sharing)) |
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
| `booking` | later | service + date and time from slots + name + phone | Needs availability and slots; separate spec |
| `custom` | later | any input schema the rules above allow | Any workflow |

**What the customer sees after submitting:**
- An immediate confirmation with a reference: the run is queued, not awaited.
- An optional **status link** (v2): a signed, unguessable URL showing the order's status as the workflow moves it.
- Never a "wait while we process" page tied to a run finishing. Street connections drop, and the run's outcome reaches the customer by message instead.

### 6.4 Submissions and orders

- **Recording:** a submission is recorded before anything else, and acknowledged with its reference.
- **Idempotency:** the form carries a one-time token, so submitting twice (a double tap, a retry on a bad connection) makes one submission.
- **Orders:** the storefront keeps an `orders` data table with:
  - `reference` (6 characters, unambiguous: no 0, O, 1 or I), `status`, `items` (json), `total_kobo`;
  - customer `name`, `phone` and `address`, all personal;
  - `channel` (`web` or `qr`) and `created_at`.
- **Order statuses:** `new` → `accepted` | `declined` → `paid` (optional) → `ready` → `completed`, or `cancelled`. The default workflow moves them. The owner can move them from chat or the Tables page.
- **Totals** are computed on the server from catalogue prices at submission time, and are what the order records. A later price change does not change an order.

### 6.5 Default workflows

Making an action installs a ready-made workflow from a template (the template library already exists). The default order workflow:

1. **Tell the owner:** an approval step addressed to the storefront's owners. On WhatsApp or Telegram it arrives with **Accept** and **Decline** (the existing approval delivery), and it times out after a configurable time (default 2 hours: "the business did not answer").
2. **On Accept:**
   - message the customer;
   - set the order `accepted`;
   - lower stock where tracked;
   - if a payment connection is set, create a payment link (Paystack `transaction.initialize`, or Moniepoint/OPay equivalents) and send it. The order becomes `paid` on the provider's webhook (a connector event trigger the template wires).
3. **On Decline or timeout:** message the customer, and set the order `declined`.

Messages to customers go by SMS (Termii, sender ID per business where registered, else the platform's) in v1. WhatsApp messages to customers need the business's own WhatsApp number or approved templates on the platform number ([open question 7](#13-open-questions)). In every case the customer can reach the business through the **chat on WhatsApp** button.

### 6.6 Addresses, domains and sharing

- **Storefront domain:** pages are served on a **dedicated registrable domain**, never under the app's own (for example `<slug>.taskiem.shop`, never `<slug>.app.taskiem.com`). Cookies, the web app's origin and its CSP can then never be reached from a page ([8.3](#83-isolation-from-the-app)).
- **Slugs:** 3–30 of `a-z 0-9 -`, unique, not reserved ([7.2](#72-names-and-impersonation)), changeable at most twice a year (the old slug redirects for 90 days).
- **Custom domains (paid):** the business's own domain (`adaskitchen.ng`), verified by a DNS TXT record and served with a certificate. This uses the same flow, checks and re-verification as partners' embed domains ([embedding §10](../embedding.md#10-custom-domains)).
- **Sharing:** QR code, printable card, status text, and Open Graph tags (title, description, image).

### 6.7 Media

Logos and product photos are uploaded by the owner (web, or a photo in chat).

**Processing:**
- **On upload:** checked by type (JPEG, PNG, WebP), size (5 MB) and dimensions; EXIF metadata, including location, is stripped.
- **Stored resized:** 320 and 800 pixels wide, in WebP with a JPEG fallback.
- **Moderated** before publishing ([7.4](#74-content-and-moderation)).

**Storage:** in object storage the platform runs, not in data tables, which only hold the reference. Xenibase storage (P4-T1, waiting on Xenibase V4) or the platform's S3-compatible storage are both possible ([open question 8](#13-open-questions)).

**Serving:** images are served from the storefront domain with long cache lifetimes and content-hashed names.

### 6.8 Managing it from chat

| Message | What happens |
| --- | --- |
| `make my page`, `my page` | Start setup, or show the link, status and today's orders count |
| `add product <name> <price>`, a photo with a caption | Add an item (summary, then **yes**) |
| `price <item> <price>`, `hide <item>`, `show <item>`, `stock <item> <n>` | Change the catalogue |
| `orders`, `order <ref>`, `order <ref> ready` | See and move orders |
| `close shop`, `close shop today`, `open shop` | Stop or resume orders |
| `qr` | Send the QR code and the printable card |

Every change is shown and confirmed with **yes**, needs `table.edit` (or a new `storefront.manage` permission), and is audited, as data-table changes from chat are today.

## 7. Trust and safety

Public pages made by businesses Taskiem has not met will be abused unless the design prevents it. These controls are part of v1.

### 7.1 Verification

| Level | How | Unlocks |
| --- | --- | --- |
| **Phone verified** | The owner's account has a linked WhatsApp number or Telegram account, or passes a phone OTP | Publish a page; `enquiry` and `callback` |
| **Business verified** | A name and address check, and optionally a CAC number or an ID check through Dojah, Prembly or Youverify (connectors that exist), reviewed when unclear | `order`, payment links, a "verified" badge, custom domains, more than 50 orders a day |
| **Agent onboarded** | Set up by a known field agent who confirms they met the business | Counts as business verified, with the agent recorded |

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

- **Automatic checks** on publish and on every change of text or images: a prohibited-content word list (in the five languages), a check of images for adult or violent content (a provider, or a model through the AI layer, with the data-processing terms in AI1), and price sanity (a ₦0 item, or a price above a ceiling, is flagged).
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
| `storefronts` | One per tenant: slug, status, template, accent, business card fields, hours, language, catalogue table id, orders table id, verification level, published snapshot version, closed-until |
| `storefront_actions` | Per storefront: kind, label, workflow id, enabled, sort, form options (prefill, OTP) |
| `storefront_snapshots` | Published versions: the rendered page model (business card, actions, catalogue *structure*), content-hashed; the catalogue *rows* are read live with a short cache |
| `storefront_submissions` | Reference, action, idempotency token hash, sealed personal fields, basket, totals, the run it started, IP hash, status |
| `storefront_media` | Upload records: hashes, sizes, moderation state, storage keys |
| `storefront_domains` | Custom domains, verified like embed domains |
| `storefront_reports` | Reports from the public, for operators |
| `storefront_slugs` (platform) | Slug → tenant, reserved and blocked names; read by the public host through a narrow `SECURITY DEFINER` function, as other cross-tenant lookups are |

Orders live in the tenant's `orders` **data table** on Xenibase, so owners and workflows use them like any table. The `storefront_submissions` row is Taskiem's own record and link to the run.

### 8.2 The storefront trigger

New trigger type `storefront` in wd/v1:

```json
"trigger": { "type": "storefront", "config": { "action": "order" } }
```

**The run's trigger input:**
- `submission.reference`, `submission.action`, `submission.fields` (with `x-pii` fields sealed);
- `submission.basket` (`items`: id, name, unit price, quantity, line total) and `submission.total_kobo`;
- `storefront.slug` and `storefront.name`;
- `submission.channel`.

**How it starts:** a submission starts a run through the ingest pipeline, deduplicated on the submission's id (spec 8.1), within the tenant's ingest limits. A workflow with this trigger can be used by one action of one storefront.

### 8.3 Isolation from the app

- **Separate domain:** pages and forms are served on the storefront domain only. That host serves nothing of the app or the API, the same rule as custom embed domains.
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
| `POST/GET/DELETE /v1/storefront/domains...` | Custom domains |
| Public, on the storefront domain: `GET /` (the page), `GET /a/{action}` (a form), `POST /a/{action}` (submit), `GET /media/{hash}`, `POST /report` | Served by the edge, no session |
| Operators: `/v1/ops/storefronts...` | Search, reports, suspend, restore, verification review |

**Permissions:** a new `storefront.manage` (owners and admins by default) for the page, actions and publishing; catalogue and order edits stay under `table.edit`. Everything is audited (`storefront.publish`, `storefront.suspend`, `storefront.action.create`...).

### 8.6 What it reuses

- Data tables (catalogue and orders) and the WhatsApp and Telegram conversation (owner commands, approvals).
- The template library (default workflows), connectors (Termii, Paystack, Moniepoint, OPay, iswallet, Dojah, Prembly), and the PII sealing and detector.
- Plan limits and billing; custom-domain verification; the operator console; the audit chain.

What is new: the public renderer, the `storefront` trigger, media, moderation, and abuse controls.

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

The cheapest plan today, Starter at ₦15,000 a month, is too much for a street business. Proposed (prices are placeholders for B1, like the others):

| Plan | Price | Storefront |
| --- | --- | --- |
| **Free** (new) | ₦0 | One page on the storefront domain with the Taskiem footer; up to 30 catalogue items; `enquiry` and `callback`; `order` up to 20 orders a month; the default workflows only (no editing on the canvas); SMS to customers counted against a small allowance |
| **Shop** (new) | about ₦2,000–3,000 a month | 200 items, unlimited orders within fair limits, payment links, editing the default workflows, a bigger SMS allowance |
| Starter and above | as today | Storefront included; custom domains, 2,000 items, removing the footer brand (never the safety line), all workflows |

The free plan is the acquisition channel: a business that starts with a page grows into automations. SMS costs money, so the free allowance is small and metered like WhatsApp templates (`plans.yaml` gains `storefront_items`, `storefront_orders_monthly` and `sms_monthly`).

## 12. Phasing

| Phase | Scope |
| --- | --- |
| **v1** | Page (card, catalogue from a data table, 2 templates), `order`, `enquiry` and `callback` actions, the `storefront` trigger, default workflows with owner approval in chat and SMS to customers, setup and management from chat and web, phone verification, the trust-and-safety controls in section 7, QR and sharing, the free plan |
| **v2** | Payment links in the default order workflow (Paystack first), business verification with the badge, a status link for customers, custom domains, `booking` (slots), the Shop plan |
| **v3** | `custom` forms, WhatsApp messages to customers (own numbers), multi-branch businesses, a public directory (if wanted), simple analytics |

**Success measures for v1:**
- time from `make my page` to published (target: median under 10 minutes);
- the share of pages with at least one order in their first week;
- orders per active page per week;
- reports per 1,000 pages;
- p75 page load on 3G.

## 13. Open questions

1. **First users:** existing tenants (Payrolla's and iSpend's customers, partners' sub-tenants), or street businesses signing up fresh? This decides how much onboarding goes through chat and field agents.
2. **Payment at order time or on confirmation?** The spec assumes order first, then a payment link on Accept (common practice, and simpler). Is paying up front needed for some sellers?
3. **Products only, or services and bookings in v1?** The spec puts bookings in v2.
4. **Who sets pages up:** owners themselves, field agents, or both? Agents need a role and a commission or tracking model.
5. **The storefront domain:** which registrable domain (after the trademark search, L3)? Subdomains (`ada.<domain>`) or paths (`<domain>/ada`)? The spec prefers subdomains, for isolation and memorable links.
6. **Free plan economics:** is a free plan acceptable, and what SMS allowance does it carry?
7. **Customer messages over WhatsApp:** on the platform number with approved utility templates per message (cost per message, and Meta's rules on messaging people who did not write first), only through businesses' own numbers, or SMS only in v1?
8. **Media storage:** Xenibase storage (waiting on V4) or the platform's own S3-compatible storage? The spec needs one by v1.
9. **Moderation provider:** a model through the AI layer, or a dedicated image-moderation service, and its data terms?
10. **The name** *Storefront*, before it is shown to anyone.

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
