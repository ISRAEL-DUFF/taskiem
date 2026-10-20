# Phase 5 status: storefronts and forms

Phase 5 gives Taskiem a public front door: storefronts for businesses without a website, signed up in person by field agents, and hosted forms for every workflow ([build plan](spec/build-plan.md#phase-5--storefronts-and-forms-about-20-weeks)). The designs are [storefronts](spec/storefronts.md) (decision 0036) and [forms](spec/forms.md) (decision 0037), both proposed.

Gate G5: 100 storefronts published in the pilot market (median under 10 minutes); 40% with an order or booking in their first week; 25 organisations using hosted forms; p75 under 3 seconds on 3G; no reported scam page live beyond 24 hours; a security review with no open high findings; 60% of agent-signed businesses active after 30 days.

Nothing of Phase 5 ships to customers before gate G4.

## Milestones

| # | Milestone | Status |
| --- | --- | --- |
| S0 | Decisions | **Open**: see below |
| S1 | Shared public foundation | **Built** and merged: the [public host](public-host.md), form guards and name checks, [messages](messages.md) by SMS and email, [Xenibase storage](media.md), [embed sites](embed-sites.md), and the VM kit's [on-demand certificates](vm.md#public-pages-storefronts-and-forms) |
| S2 | The storefront page | **Built** and merged: [storefronts](storefronts.md): drafts and published snapshots, the business card with opening hours, the catalogue from a data table with its copy, photos with the moderation hook, two templates and eight accents, the preview address, the web editor, the QR code and printable card. Moderation decisions wait for S8 |
| S3 | Orders | **Built** and merged: [orders, questions and call-backs](storefronts.md#orders-questions-and-call-backs), with the inbox from the public host, Paystack payment when ordering or after acceptance (checked by polling, unpaid orders expiring), the storefront trigger and platform connector, three default workflows (accept or decline in chat, customer texts and emails), [stock](storefronts.md#stock) (refused above what is left, lowered at Xenibase on Accept by compare-and-set), and the orders page. Refunds are manual (refund due) until the S0 refund decision. The price check before a run (spec 8.1, 8.2) shipped with S4 |
| S4 | Bookings | **Built**, one PR in review: [bookings](storefronts.md#bookings): services from a data table (duration, capacity, buffer), availability (booking hours, days off, the window, a change cut-off, automatic confirmation), slots held in the same transaction as the booking so none is taken twice, the booking workflow (confirm in chat, reminders a day and two hours before as wait steps, attended or no-show), moves and cancels by the customer from their link and by the owner in chat or the web app, the day's bookings each morning; and [the price check](storefronts.md#the-price-check) before every order's and booking's run. One calendar per business; several staff calendars are v2 |
| S5 | Chat set-up and field agents | Chat set-up **built**, in review: [the storefront from chat](storefronts.md#from-chat) on WhatsApp and Telegram: `make my page` (questions one at a time, a products table made and mapped, publish with the link, the QR code image and a status text), products by text or a captioned photo, price, hide, show and stock, hours, services, close and open, orders and `order <ref> ready` or `delivered`, and the public `/qr.png`. [Field agents](agents.md) **built**, in review: operators register checked agents; an agent signs a business up and the owner agrees by sending `AGREE` and a code from their WhatsApp (decision 0036, 16); a 24-hour session on the page and its tables only, audited as the agent; the visit's photo and place (agent onboarded); publishing hands over; support visits the owner invites; `STOP AGENT`; the agent app, the owner's page and the console's agents pages |
| S6 | Domains and embedding | **Own domains built**, in review: [your own domain](storefronts.md#your-own-domain): a business connects a domain it already owns, proven by a DNS TXT record and re-checked every 6 hours (unverified after 4 failures over a day), served by the public host with an on-demand certificate; the preview address and the `www.` twin redirect to the primary domain; links, the QR code, the card, `og:url` and chat links use it; the editor's "Your own domain". Registering domains through the domain platform waits for its contract; the embed views, script and overlay button come later |
| S7 | Forms | **Built**, one PR in review: [forms](forms.md): the `form` trigger (public, link and members access; pages with conditions; prefill from the link; verification by SMS or email code; once per respondent; the message, redirect and result modes and the status link) and the `form` step (a one-time link by SMS or email, or members of a role in the app; reminders; a timeout), on the form host with no cookies; files scanned (a clamd the operator runs) and kept in Xenibase storage, read through `form@1`; one-time links; submissions masked; members forms and form steps in the app; the form editor in the canvas; the AI builder drafting forms. Not yet: options from a data table, text blocks, proof-of-work, the organisation's logo on forms, report handling (S8), forms in chat (v2) |
| S8 | Trust, plans and pilot | Trust and plans **built**, in review: [trust and safety](moderation.md): the checks on every publish and catalogue item (a word list, prices, a model through the AI layer), a first publish waiting for a person unless the business is verified, verification levels (phone, business checked by Taskiem, agent onboarded) and what they unlock, the badge, reports from every page and hosted form, suspension (by hand, or by three people's reports) with appeals in chat, paused orders (the plan's month, 50 a day until verified), the review queue in the operator console; the Shop and Shop Plus plans with placeholder prices, item, order, booking, SMS and embedding limits. Still to come: the security review, and the 6-week pilot |

## S0: decisions

| Decision | Who | Status | Blocks |
| --- | --- | --- | --- |
| Accept decisions 0036 (storefronts) and 0037 (forms) | Product owner | **Accepted** 2026-10-20, amended to what was built | — |
| Moderation: an AI check with a review queue, an image-safety service, or people only | Product owner | **Decided** 2026-10-20: word list, price check and model check, with the review queue staffed part-time at pilot scale (0036, 13) | S8 |
| The preview domain and the form host's domain | Product owner, infra | Open | S1 |
| The domain platform's integration contract: API, renewals with the plan, the domain if a business leaves, transfers | Product owner, domain platform team | Open | S6 |
| Shop and Shop Plus prices, annual pricing, a trial | Product owner, finance | Open | S8 |
| Field agents: staff or paid per business, who registers them, commission | Product owner, operations | **Decided** 2026-10-20: a contracted pilot team, a base and a bonus per activated business, clawback (0036, 15) | — |
| Refunds when an owner declines a paid order or cancels a booking | Product owner | **Decided and built** 2026-10-20: the owner confirms, Taskiem refunds through Paystack; a booking form may keep the deposit (0036, 14) | — |
| The malware scanner for form uploads: run by Taskiem, or a service | Security | **Decided** 2026-10-20: ClamAV run by Taskiem, started by the VM kit and the Helm chart (0037, 6) | — |
| The feature names customers see | Product owner | Open | S8 |

Answered 2026-10-20: the decisions, moderation, field agents, refunds and the scanner (above). Already answered (2026-10-19): own domains from the domain platform; photos in Xenibase storage; SMS, or email when given, to customers; no free plan; new businesses first; both payment timings; bookings in v1; field agents; embedding on the business's own website.

## Dependencies outside Taskiem

| Dependency | Status | Needed by |
| --- | --- | --- |
| Xenibase storage integration (P4-T1) | Built ([media](media.md)); to be proven on staging with `taskiem media check` | S1 |
| Termii sender ID, and an email sender (P4-O1) | The sender is built ([messages](messages.md)); the sender ID approved for the `dnd` channel, Africa's Talking credentials and the no-reply address are open | S1 |
| The domain platform's API | Contract not agreed | S6 |
