# The Taskiem website: structure and copy

Date: 2026-10-21 · Status: **built**; the copy below is what the site says, to be edited here and in `tools/docsite/assets/www/`

This page is the public website: which pages it has, what each says, and where each fact comes from. It speaks to **the SME owner on a phone**, the audience of [the chat-first strategy](chat-first-nigeria.md). Fintechs and partners get one page of their own, not the headline.

Where a sentence depends on something that is not true yet (a live WhatsApp number, real prices, reviewed translations), the [claims register](#claims-register) at the end says so, and the generator leaves the sentence out until it is true, so nothing false ships.

## How the site is built

- **The deck's look, hand-written.** Since 26 October 2026 the site uses the look of the investor deck in the relay brand ([decision 0043](../decisions/0043-taskiem-brand.md), [brand](../brand/README.md)): pine bands, mist and white sections, Plus Jakarta Sans with JetBrains Mono labels, and lime only on pine. It replaces the designer's Tailwind pages of 21 October. Every page is a Go template in `tools/docsite/assets/www/`; `layout.html` is the shared header and footer, and `parts.html` holds the brand mark, the line icons, the two closing buttons (`cta`) and the closing band. The stylesheet is one hand-written file, `tools/docsite/assets/www.css`, with no build step. It has a dark theme in which lime takes pine's place as the accent.
- **Built from the repository.** The template cards (`templates/library`), the connector lists and count (`connectors/`), and the prices, plan contents and comparison table (`deploy/plans.yaml`) are read at build time. The WhatsApp link, the app's sign-up and the contact email appear only when given (`-whatsapp`, `-app`, `-email`).
- **Everything is self-hosted.** Plus Jakarta Sans and JetBrains Mono (SIL OFL) are in `assets/fonts/` with their licences, each with a per-package exception in `tools/licencecheck/policy.json` on the same basis as the console's Inter. The mark and the share picture are copies of `docs/brand/relay` in `assets/www/img/`. Icons are inline SVG. No request leaves the site's host. The one script is the pricing page's monthly and yearly toggle, inline.
- **Only what is true.** The new pages leave out every claim the register below lists as not yet true that the designer's pages carried: the "NDPR & Bank-Grade Certified" badge, 99.99% uptime and the Lagos node, the generated customer story, "1,400+ merchants", certifications, the money-back guarantee and price locks.
- **Prices are decided** (B1, 21 October 2026): Starter Trader ₦15,000, Growing Business ₦45,000, Enterprise & Regulated ₦180,000 a month, ten months for a year, in `deploy/plans.yaml`. The storefront plans and Business stay active for tenants on them but are not public.
- **Building it:** `make docs` (`go run ./tools/docsite`), with `SITE_APP`, `SITE_WHATSAPP` and `SITE_EMAIL` for the outside facts; without a WhatsApp number the WhatsApp buttons lead to the app's sign-up, and without an app to the owner's guide. Pages are `name.html` linked as `/name`, so the host rewrites `/name` to `name.html`.
- This page stays under `internal` in `tools/docsite/pages.go`.

## Voice

- Plain words. One idea per sentence. Say what Taskiem does, not what it is.
- Nigerian by default: naira, WhatsApp, Paystack, Moniepoint, OPay, BVN. No translation of Nigerian life for a foreign reader.
- Talk to one person: "your customers", "your debtors", "you approve".
- The product word is **workflow**, as in the app and the owner's guide. Headlines avoid it; body text uses it once per page and explains it the first time ("a workflow is something Taskiem does for you again and again").
- Never "AI-powered", "seamless", "leverage", "empower", "revolutionise". Never a claim without a page behind it.
- Every page ends with the same two buttons: **Chat with Taskiem on WhatsApp** and **Start on the web**.

## Site map

| Page | Address | Job | Source |
| --- | --- | --- | --- |
| Home | `/` | Say what Taskiem does in one screen and get the person to WhatsApp or sign-up | hand-written |
| How it works | `/how-it-works` | The three steps, with a real conversation | hand-written |
| What you can automate | `/templates` | Every template, grouped by the owner's problem | generated from `templates/library` |
| Works with | `/integrations` | Every connector, Nigerian ones first | generated from manifests |
| A service | `/integrations/<id>` | How it connects, what workflows do with it, what starts one, the templates that use it | generated from its manifest and `templates/library` |
| Your records | `/tables` | Data tables: your debtors, stock and staff without a spreadsheet | hand-written |
| Your page | `/storefront` | The one-page shop with orders and bookings | hand-written |
| Pricing | `/pricing` | Plans in naira, what each includes, the trial | generated from `deploy/plans.yaml` |
| Trust | `/trust` | Approvals, PINs, audit, personal data, where data lives | hand-written |
| For fintechs and partners | `/partners` | Embedding, white-label, regulated use | hand-written, short |
| About | `/about` | Who makes it, where, and why | hand-written |
| Contact | `/contact` | WhatsApp, email, office | hand-written |
| Docs | `/docs` | The existing docs site | existing |
| Status | status host | The existing status page | existing |
| Terms, Privacy, Acceptable use | `/legal/*` | Legal pages: **not built**; the footer links to none until they exist | need a lawyer (P4-B4) |

Navigation: How it works · Templates · Works with · Pricing · Trust · Docs · **Start on WhatsApp** (or **Start free** with `-app` alone). Everything else lives in the footer. Status and the legal pages are not linked until they exist.

## Home

**Hero**

> # Run your business from WhatsApp.
>
> Tell Taskiem what to do in your own words. Remind the customers who owe you. Thank each one who pays. Pay your staff after you approve. It runs every day, whether you remember or not.
>
> [Chat with Taskiem on WhatsApp] [Start on the web]
>
> Free for 14 days. Pay in naira by card or transfer.

Under the buttons, a phone showing a real exchange:

> **You:** every Friday at 10am text the customers in my debtors table who still owe me
>
> **Taskiem:** Here is what I will do every Friday at 10:00:
> 1. Read your *Debtors* table.
> 2. For each person with a balance above ₦0, send an SMS: "Good morning {name}, your balance with Ada's Kitchen is ₦{balance}. Thank you."
> 3. Send you a count of who was reminded.
>
> Shall I set it up? Reply **yes**.

**Three steps** (section heading: *How it works*)

1. **Say it.** In English or Pidgin, typed or as a voice note. Or pick one of our ready-made workflows.
2. **Connect once.** Your Paystack, Moniepoint, OPay or Google Sheet. One screen, one key. Taskiem keeps it locked away.
3. **Approve and go.** Taskiem reads back exactly what it will do and who it will message. Say yes. It starts.

Link: *See a full conversation →* `/how-it-works`

**What owners use it for** (section heading: *Things Taskiem does for people like you*)

Six cards, each a template with its real one-line summary and a *Set it up* link:

- **Weekly reminders to customers who owe you.** Every week, text each customer who still has a balance.
- **Thank-you message when a customer pays.** The moment Paystack confirms it, the customer hears from you.
- **Daily sales summary.** Every evening, today's total in your chat.
- **Pay salaries after you approve.** On payday, read the staff list, get your yes, pay each person, text them.
- **Low stock alert.** Every morning, the items at or below their reorder level.
- **Alert when a transfer fails or is reversed.** So you never release goods against money that did not come.

Link: *All workflows →* `/templates`

**Your records, without a spreadsheet** (section heading)

> Keep your debtors, customers, stock and staff in Taskiem. Add a row from WhatsApp: *add debtors*. Find one: *find debtors Chidi*. Bring in the spreadsheet you already have, and your workflows read it from here.

Link: *Your records →* `/tables`

**Works with what you already use** (section heading)

Logos, Nigerian first: Paystack, Flutterwave, Moniepoint, OPay, Interswitch, Remita, Termii, Dojah, Mono, then Google Sheets, Gmail, Slack, Telegram, M-Pesa, MTN MoMo. Caption: *and more every month*. Link `/integrations`.

**Money is handled carefully** (section heading)

Three short points, each linking to `/trust`:

- **Nothing moves without you.** Payments wait for your yes, with a PIN where you set one.
- **Every action is on record.** Who approved what, when, and what happened. You can show it to your accountant.
- **Customers' details stay private.** Phone numbers and balances are hidden from logs, from the AI, and from anyone you have not given access.

**Your page** (section heading: *A page for your business, in five minutes*)

> Say *make my page* on WhatsApp. Answer a few questions. You get a page with your products or services, your opening hours and a Pay button, at an address you can print on a card or put on a domain you own. Orders and bookings come to you in chat to accept.

Link `/storefront`.

**Pricing teaser**

> From ₦5,000 a month for a shop. ₦15,000 a month for a business with staff. Everything is in naira, with VAT shown. Try it free for 14 days.

Link `/pricing`.

**Closing**

> # Start with one message.
>
> [Chat with Taskiem on WhatsApp] [Start on the web]

## How it works

**Intro**

> You do not install anything or learn anything. You talk to Taskiem the way you talk to a manager, and it tells you what it understood before it does it.

**One full conversation**, as a scrolling chat, annotated at the side:

1. *You:* hi
   *Taskiem:* Welcome. I run things for your business so you do not have to remember them. What is your business called? (side note: *A business account is made for you from the chat. No website, email or app.*)
2. *You:* Ada's Kitchen
   *Taskiem:* What do you sell, and how do customers usually pay you?
3. *You:* food, mostly transfer to Moniepoint
   *Taskiem:* Here are three things businesses like yours set up first: … Pick one, or tell me what you want in your own words. (side note: *Three suggestions, not a menu of fifty.*)
4. *You:* every evening tell me how much I sold today
   *Taskiem:* I need your Moniepoint account to see payments. Tap below to connect it. (side note: *A WhatsApp form. You paste one key, never a password.*)
5. *Taskiem:* Connected. Every day at 18:00 I will add up the payments received into Moniepoint since midnight and send you the total and the count. Shall I set it up?
   *You:* yes
   *Taskiem:* Done. The first summary comes today at 18:00. Send *status* any time.
6. *18:00. Taskiem:* Today: ₦86,500 from 23 payments. Best hour: 13:00. (side note: *The result lands in the same chat.*)

**What a workflow is**

> A workflow is something Taskiem does for you again and again: on a schedule, or when something happens, such as a payment arriving or a row being added to your table. Each one is a few plain steps. You can read them, change them, pause them, and see every time they ran.

**On the web too**

> Everything you can do in chat you can do at app.<domain>: the same workflows as a list of steps, your tables as a grid, your team and their permissions, every run and what it did. Use whichever is in your hand.

**In your language**

> Taskiem speaks English and Nigerian Pidgin, and understands voice notes. Yoruba, Hausa and Igbo are coming once native speakers have reviewed them. (claims register: L1 to L4)

**Who else can use it**

> Add your accountant, your manager or your sales staff by their WhatsApp number. Each gets their own role: your accountant can see payments but not approve them; your manager can approve up to a limit you set.

Closing buttons.

## What you can automate (`/templates`)

Generated. Intro copy:

> Every workflow below is ready to use. Pick one, fill in a few details (your sheet or table, your times, your wording), connect what it needs and publish. Or describe something new in your own words and Taskiem builds it.

Groups, each a heading with its templates' titles and summaries from `templates/library`:

- **Money in**: receipts and thank-yous, record every payment, new order alerts, transfer received, failed or reversed transfers, low balance, weekly balances.
- **Money owed to you**: weekly reminders by SMS, by WhatsApp, from your table; follow up an unpaid invoice.
- **Money out**: pay salaries after approval, pay a supplier after approval, salary day reminder.
- **Customers**: welcome by SMS, BVN check before onboarding.
- **Stock and sales**: daily sales summary, low stock alert.
- **Your page**: orders, bookings, questions and call-backs from your storefront.
- **Just remind me**: remind me every day.

Each card: title, one line, the apps it connects, and *Set it up* (deep link to the app or `wa.me` with a prefilled *build* message).

## Works with (`/integrations`)

Generated from the manifests. Intro copy:

> Taskiem talks to the services Nigerian businesses already use. Connecting one takes a minute: one screen, one key, and Taskiem keeps it sealed. You can disconnect at any time.

Groups: **Payments and banking** (Paystack, Flutterwave, Moniepoint, OPay, Interswitch, Remita, Lenco, Anchor, Breet, iswallet, M-Pesa, MTN MoMo); **Messages** (WhatsApp, Telegram, Termii, Africa's Talking, Gmail, Slack); **Identity checks** (Dojah, Prembly, Youverify, Mono); **Your data** (Google Sheets, Xenibase, PostgreSQL, MySQL, Amazon S3, SFTP, Forms). "Tell me" and "Storefront" are Taskiem's own and are described in prose, not as logos.

Footer line: *Building a service for Nigerian businesses? See how to add yours →* `/docs/connector-submissions`.

## Your records (`/tables`)

> **Your debtors, stock and staff, kept in Taskiem.**
>
> Most small businesses keep their lists in a notebook or a spreadsheet, if anywhere. A table in Taskiem is a list your workflows can read and write: your debtors with their balances, your stock with reorder levels, your staff with their account numbers.
>
> - **Make one in chat.** *make a debtors table with name, phone and balance.* Or let a workflow make the one it needs.
> - **Bring in your spreadsheet.** Upload the CSV. Taskiem matches the columns and asks about any it cannot place.
> - **Add and find from WhatsApp.** *add debtors* asks you column by column and shows the row before saving. *find debtors Chidi* answers with the matches.
> - **Edit in the web app.** A grid you can search, edit, import and export.
> - **Your workflows use it.** Remind everyone with a balance. Mark a debtor paid when Paystack confirms. Start a workflow the moment a row is added.
>
> Your tables are yours. Phone numbers and emails are treated as personal data: hidden in logs, never shown to the AI, visible only to people you have given the permission. Export everything as a CSV any time.

## Your page (`/storefront`)

> **A page for your business, from one message.**
>
> Say *make my page*. Taskiem asks what you sell, your hours and how to reach you, and makes a page with your products or services, photos, a Pay button and a QR code to print. Customers order or book from it; each order comes to you on WhatsApp to accept, and the customer is told by text.
>
> Put it on an address Taskiem gives you, on a domain you own, or inside a website you already have. Your web designer gets the code by email; you do not need to understand it.

Three screenshots: the page on a phone, an order arriving in chat, the printable card.

## Pricing (`/pricing`)

Generated from `deploy/plans.yaml`. Intro:

> Flat prices in naira. No charge per run. VAT is shown separately. Change or cancel from the app at any time.

Plan cards, monthly and annual toggle, from the file: **Shop** ₦5,000, **Shop Plus** ₦10,000, **Starter** ₦15,000, **Growth** ₦60,000, **Business** ₦250,000, **Enterprise** talk to us. (All placeholders until B1 is decided; the page must not go live with `placeholder_prices: true`.)

Each card lists, from the plan's limits, in owner's words: how many workflows, how many tables, WhatsApp messages included per month, SMS included, whether *Describe it* (AI) is included, how long run history is kept, how many people on the team.

Below the cards:

- **Try it free for 14 days** on the Growth plan. (confirm whether a card is required)
- **WhatsApp messages beyond your plan** are charged at Meta's rate, passed through at cost, and shown on your invoice.
- **Invoices** come as PDFs with VAT, numbered, to your email and your chat.
- **Pay** by card or bank transfer through Paystack. (confirm Flutterwave too)

FAQ: *What happens when I go over?* *Can I pay yearly?* *What if I stop paying?* (seven days' grace, then new runs pause; nothing is deleted for the retention period) *Do you charge for failed runs?* (no).

## Trust (`/trust`)

> **Built for businesses that handle money.**
>
> Taskiem was designed first for banks and lenders, where a mistake costs real money and a regulator asks questions. You get the same protection for your shop.

Sections, each three or four sentences, linking to the docs page that proves it:

- **Nothing moves without your yes.** Payments and other money steps wait for approval. You set who can approve, up to what amount, and whether a PIN or a passkey is needed. (`/docs/governance`)
- **Done once, never twice.** If the network drops or Taskiem restarts, a payment is never sent twice and a message is never resent. (`/docs/reliability`)
- **Everything on record.** Every approval, every payment, every change to a workflow, with who and when, in a tamper-evident log you can export for your accountant or auditor. (`/docs/compliance`)
- **Customers' details are private.** Phone numbers, emails and balances are hidden in logs and alerts, never sent to the AI, and visible only to people with the permission. (`/docs/privacy`)
- **Your keys stay sealed.** Your Paystack key is stored encrypted and is never shown again, to anyone, including us. You can bring your own encryption key. (`/docs/byok`)
- **Where your data lives.** Taskiem runs in Nigeria. (claims register: P4-C1) Your records live in a database project of your own that you can take with you.
- **You can leave.** Export your tables as CSV and your workflows as files at any time.

Closing: *Questions from your compliance officer? Read the docs or write to us.*

## For fintechs and partners (`/partners`)

Short, one screen.

> **Taskiem inside your product.**
>
> Lenders, payroll companies and banks embed Taskiem so their customers get approvals, payouts and reminders under their own brand. Your users never see Taskiem; you keep the relationship. Durable, exactly-once execution, approval policies, audit chain, PII sealing, self-hosting and bring-your-own-key for regulated deployments.

Links: embedding, operations, compliance docs; *Talk to us*.

## About

> Taskiem is made in Nigeria by the team behind Xenibase, a managed database used by Nigerian developers. We built Taskiem because every small business owner we know runs their business from WhatsApp and a notebook, and the tools built for them were built for someone else.
>
> Names, photos, office, year founded. (to fill)

## Contact

WhatsApp number (the product's own), support email, office address, hours. A form is not needed; the WhatsApp button is the form.

## Footer

Product: How it works · Templates · Integrations · Your records · Your page · Pricing. Company: About · Trust · Contact · Partners. Help: Docs · Owner's guide · Status · WhatsApp support. Legal: Terms · Privacy · Acceptable use. Line: *Made in Lagos. Prices in naira.*

## Claims register

Each claim the copy makes that is not true today, and what makes it true. The site does not go live, or the sentence is cut, until the row is done.

| Claim on the site | Depends on | Register item |
| --- | --- | --- |
| "Chat with Taskiem on WhatsApp" button, every page | A live WhatsApp Business number and approved templates | W1, W2 |
| Sign-up from WhatsApp ("hi" makes an account) | Chat-first gap 1 in the strategy | strategy 3.2 |
| Connecting Paystack or Google from the chat | Chat-first gap 2 | strategy 3.2 |
| Every price on `/pricing` | Decided 21 October 2026; `placeholder_prices: false` | B1 (done) |
| "Pay by card or transfer" | A Paystack merchant account | P4-B2 |
| "Free for 14 days" | Confirm trial terms and whether a card is taken | B1 |
| "Taskiem speaks Pidgin"; Yoruba, Hausa, Igbo "coming" | Native-speaker review | L1 to L4 |
| Voice notes | Beta, off by default; say "beta" or cut | decision 0029 |
| "Describe it" and the AI examples | A real model account and data-processing terms | AI1 |
| "Taskiem runs in Nigeria" | A Nigerian region and the first deployment | P4-C1, D5 |
| Logos on `/integrations` | Sandbox accounts and live tests for each | P1 |
| "A page for your business" | Storefronts are off by default; turn on for launch | S2, S5 |
| Terms, Privacy, Acceptable use | Legal review | P4-B4 |
| About page names and photos | The founder | — |
| Analytics without cookies | Choose a provider | new |
| "Trusted by 1,400+ Nigerian merchants…" (home) | Real customer numbers | new |
| "NDPR & Bank-Grade Certified" (header), NDPR Certified, SOC 2 readout, ISO 27001 ready, AES-256, HSM (security, pricing, integrations) | The audits and the controls; the founder's decision of 21 October 2026 is that the system will meet them | new |
| "99.99% uptime", "Multi-region redundancy" (integrations, pricing) | A measured SLO and the deployment ([reliability](../reliability.md)) | new |
| "30-day unconditional money-back guarantee" (pricing) | A decided refund policy and its terms | new |
| The customer story (Mrs. Folashade Adeyemi, photo and figures) is generated by the design tool | A real, consenting customer before launch; built verbatim as a placeholder by the founder's decision | new |
| "Zero FX volatility risk", "price-lock for 12 months" (pricing) | Terms of service | P4-B4 |

## Open decisions

1. **Domain**, and moving the app to `app.<domain>` so the website can have the apex: a second site block in `deploy/vm/Caddyfile` serving `site/` with `try_files`, and `TASKIEM_DOCS_URL` set to `https://<domain>/docs`.
2. **Legal pages** (terms, privacy, acceptable use): written by a lawyer (P4-B4), then added as pages and to the footer. The footer has no legal links until they exist, rather than dead ones.
3. **Analytics**: none (today), a cookieless provider, or self-hosted.
4. **Real screenshots** to replace the drawn screens once the WhatsApp number is live: the Tables page with a debtors table, a storefront page on a phone, and a WhatsApp and a Telegram conversation. Until then the drawn screens stand in.
5. **Pidgin version of the home page**: a language switch on the site itself, once L1 is reviewed.
6. **Whether `/partners` deserves a second page** for the regulated audience, or the docs are enough.
