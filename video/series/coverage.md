# Coverage: does the series plan cover the whole system?

**Short answer: no.** The plan covers the small business that runs on its
phone (orders, stock, money basics, staff, deals, schools). It does not yet
cover the workflow engine and builder (the product's original core), several
channels, the online shop, deeper money tools, or the developer and
self-hosting side. Audit of 24 November 2026, from `docs/*.md`, `web/src/pages`,
`mobile/lib`, `connectors/` and `templates/`.

Key: **Covered** = has an episode. **Partly** = touched, not taught.
**Not covered** = no episode. **Wait** = not built, or only built against fakes,
so we must not film it as working.

## 1. Running a business (the chat, phone and web)

| Area | State in the app | Series coverage | Where / proposal |
| --- | --- | --- | --- |
| Sign up, profile, categories, being found (Explore, Discover) | Built | **Covered** | Episode 1 of each series; how-to 15 |
| Products, categories, photos, sizes and colours | Built | **Covered** | Episode 2 |
| Stock: counts, low-stock, restocking, Reorder | Built | **Covered** | Episodes 2, 7, 9 |
| Stock counts ("what went missing"), purchases and purchase orders, suppliers page | Built | **Partly** | Only Reorder. Add one episode: "Count and buy" |
| Selling at the counter, shift handover and cash | Built | **Covered** | Episodes 3, 6 |
| Orders: from customers, from businesses, customer WhatsApp orders | Built | **Covered** | Episode 3, 7, 8 |
| **Customers chat with you in the app or a browser** (chat link, one customer on any line) | Built | **Not covered** | New chapter in "Take orders" (replan section 8) |
| Invoices, matching payments, bills | Built | **Covered** | Episode 4 |
| Money page, spending, statements, **money reports, leaks found** | Built | **Not covered** | Add: "Where your money went" (reports, leaks, statement) |
| **Online shop (storefront)**: the public shop page, preview, domains, embedding on a website, trust page, fronts | Built | **Not covered** | Add: "Your online shop" |
| Services and bookings (free times, booking cards) | Built | **Partly** | Fashion Episode 3 only; add to Services series |
| Riders and deliveries, availability | Built | **Covered** | Episode 5 |
| **Paying riders** (payouts), rider app on WhatsApp | Built | **Not covered** | Add to Episode 5 or a short |
| Staff, roles, tasks, clock-in | Built | **Covered** | Episode 6 |
| Approvals, passkey, app lock, notifications, offline | Built | **Partly** | Episode 10 covers approvals and app lock; notifications and offline have no episode |
| Morning briefing, how did we do | Built | **Covered** | Episode 9 |
| Links, price lists, deals, credit limits | Built | **Covered** | Episodes 7, 8 |
| Saved things (`@name`), made tools, pages, device fields (scan, sign) | Built (made tools M0-M5, pages, saved) | **Proposed** | "Episode 11: Automate the rest" (replan 9) |
| Schools (fees, account numbers, clearance, gate) | Built | **Covered (series 4)** | `series/schools` |
| People chat, groups, ordering together | Built | **Draft** | `series/everyone` |
| **Paying people**: pay a bill by transfer, pay one person (limits, passkey), pay from a message, batches of up to 100, saved accounts, reading account details from a photo | Built (web, app, server; the bank connection is a stand-in in the demo) | **Not covered, film it** | New chapter "Pay people" (section 7) |
| **Payroll on Payrolla** (connect in one step, staff and pay from Taskiem, this month's payroll approved and locked) | **On the way**: P4b-1 to P4b-3 built on branch `ccr-87497aa3-gf060z`, not yet on main (decision 0074 accepted 24 Nov) | **Not covered, film when merged** | In "Pay people" or its own episode |
| **Stock apart from products** (stock items you count but never sell; purchases of anything with a label and a use; receiving; "Things we have") | **Built** (S1a, S1b, S2 web and app, U1, U2a web; decision 0070) | **Not covered, film it** | New chapter "What you buy" (section 7) |
| Updates (Status and Channels, as WhatsApp's) | **Not built, deliberately**: the founder deferred it on 12 November ("not built for now"), no code on main or any branch | Wait | Film when it is built |
| **Helpdesk and tickets** (storefront tickets, a ticket kit, a helpdesk on WhatsApp and email) | Built (tickets on main, the kit and mail polling on branch `ccr-e260e63d-tickets`) | **Not covered, film it** | New chapter "Support your customers" (section 7) |

## 2. Channels

| Channel | Series coverage |
| --- | --- |
| WhatsApp (shared number, own number) | **Covered** (how-to 13) |
| Telegram | **Not covered** (docs/telegram.md) |
| Slack | **Not covered** (a connector and a chat way in) |
| USSD (for phones with no data) | **Not covered** (docs/ussd.md) |
| Email, SMS | **Partly** (invoices by SMS) |

## 3. The workflow engine and builder (the product's core)

`README.md` calls Taskiem "a workflow automation engine that regulated
businesses can trust with money movement". **Nothing in the series covers this.**

| Area | Series coverage |
| --- | --- |
| The workflow builder (canvas, steps, test, publish), templates, AI build | **Not covered** |
| Runs: durable, replayable, exactly-once effects, the activity view | **Not covered** |
| Approvals, policies and governance, the audit chain, compliance, leaks | **Not covered** (only the shop-owner's approval card) |
| 50+ connectors (Paystack, Flutterwave, Mono, Moniepoint, OPay, Termii, WhatsApp, Slack, Google Sheets, QuickBooks, Zoho, Postgres, MySQL, SFTP, S3...) | **Not covered** |
| Forms, data tables, email, code steps, container steps, agents and AI, BYOK | **Not covered** |
| Embedding, the CLI, SDK, connector SDK | **Not covered** |
| Billing and plans, members and API keys, environments | **Not covered** |
| Self-hosting: VM kit, Kubernetes, operations | **Not covered** |
| Operator console | **Not covered** (internal) |

## 4. Other business types

The app has 13 categories; the plan has Fashion, Food, Gadgets and Schools.
Beauty, Building materials, Home and furniture, Farm produce, Health and
pharmacy, Cars and spare parts, Wholesale, Services, Delivery and logistics,
Electrical and electronics are **later**.

## 5. What I recommend adding

1. **A "Workflows" series** for the engine and builder: a second audience
   (operators, finance and compliance teams, developers) and the product's
   original promise. Outline in `series/workflows/README.md` (to draft).
2. **A short "Online shop" series or episode** (storefront, domains, embed).
3. **A "Money" episode** (reports, leaks, statements, spending).
4. **Channels**: short how-tos for Telegram, Slack and USSD.
5. **Notifications and offline** how-tos once the app is in the stores.
6. **Episode 11, "Automate the rest"**, bridging the shop series to workflows.

## 6. Decisions for you

1. Is the engine and builder a priority audience now, or after the shop series?
2. Do we cover Telegram, Slack and USSD, or only WhatsApp?
3. Is "complete coverage" a goal, or is "the best 80% for each audience" enough?

## 7. Corrections and additions, 24 November 2026

The first version of this audit labelled several things "Wait" from the docs
alone. Checked against the code and the branches:

| I said | The code says |
| --- | --- |
| Paying people and payroll: wait, "against fakes" | The web pages (`Payments`, `PayBatch`, `PayByTransfer`, `PayCollect`, `Payroll`), the app screens (`pay_*`, `payroll_page`) and the API (`api/payments*.go`, `payment_batches.go`) exist. Only the **bank connection** is a stand-in, as the demo's WhatsApp and SMS already are. We can film it in the demo world. Payroll's next slices are on a branch, not yet on main |
| Stock apart from products: not built | **Built.** Migrations 00501 (stock items), 00503 (purchase lines), 00504 (receipts), 00590 (barcode); the app has `stock_items`, `purchases_page`, `purchase_view`. The doc I read said "not built" because it was written before the build |
| Updates (Status and Channels): spec only | **Still not built**, and the founder said so: "not built for now" (12 November). There is no code for it on main or on any branch I can find. If you have a branch I have missed, tell me |

**One caution on payments:** filming payments in the demo is fine. A *public*
film that says "pay your suppliers from Taskiem" should wait until a real
wallet is connected (the build plan's D6 and D7 before launch); the narration
says "in the demo" or stays on the steps, not on live money, until then.

### New chapters for the spine (proposed)

| Chapter | What it teaches | Built? |
| --- | --- | --- |
| **Pay people** | Pay a bill by transfer in the chat; save an account as `@bayo`, name checked with the bank; pay one person with a limit and a passkey; pay from a message; pay several at once; read account details from a photo; payroll | Built (payroll next) |
| **What you buy** | Things you count but never sell (fabric, flour, thread); buying anything with a label and what it is for; receiving; "Things we have"; stock counts | Built |
| **Support your customers** | Tickets from the storefront, a helpdesk on WhatsApp and email | Built (kit on a branch) |
| **Automate the rest** | Made tools, pages, saved things, scan and sign | Built (M0 to M5) |
| **Your online shop** | The public shop page, domains, embedding | Built |
| **Where your money went** | Reports, leaks, statements, spending | Built |

These join the ten chapters, so each series would have up to sixteen. Not every
business needs all of them; each series picks the ones it uses.
