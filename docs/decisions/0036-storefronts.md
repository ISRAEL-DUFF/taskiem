# 0036 — Storefronts: a public page whose forms start workflows

Date: 2026-10-19 · Status: Proposed (spec under review: [storefronts](../spec/storefronts.md))

## Context

The chat-first strategy targets small Nigerian businesses that run on WhatsApp and have no website. They need to be found and ordered from, but a website is out of reach, and the forms and automations behind one more so. Taskiem has no hosted forms (open question Q10 in the Xenibase plan), though it has most of what a small public page needs behind it: data tables, schema-generated forms, approvals in chat, payment and SMS connectors, PII sealing and custom-domain verification.

## Decision (proposed)

1. **A one-page storefront per tenant, as the front door to workflows**, not a website builder: fixed mobile-first templates, a business card, a catalogue and actions. No custom HTML, CSS or JavaScript, ever.
2. **The catalogue and orders are data tables.** Owners edit them as they already edit tables, from the web app or chat, and workflows read and write them.
3. **Actions are hosted forms generated from the action workflow's input schema**, starting runs through a new `storefront` trigger in wd/v1, deduplicated per submission. The customer gets an immediate reference; the outcome arrives by message, never by holding a page open.
4. **Each business on its own domain**, registered and pointed by the group's domain platform as part of its plan, with a dedicated preview domain beside it. Pages are isolated from the app's origin and cookies, rendered on the server by the edge role, and small enough for 3G and cheap phones.
5. **Trust and safety in v1:**
   - verification levels gate what a page can do;
   - look-alike and reserved names are refused;
   - forms can never ask for card details, PINs, passwords, OTPs, BVN or NIN;
   - payments go only through the business's provider link;
   - content is moderated, with public reports and operator suspension;
   - rate limits and tracker-free bot defences protect the forms.
6. **Personal data:** the business is the controller and Taskiem the processor. Customer fields are sealed, there is a notice on every form, retention applies, and pages carry no trackers or cookies.
7. **Paid storefront plans only, priced below Starter**, each including the domain and an SMS allowance. There is no free plan.
8. **Orders and bookings both in v1**, each with payment at the time or after the owner accepts. Bookings use a services table, the business's availability and held slots.
9. **Field agents sign businesses up in person**, through time-limited onboarding sessions the owner consents to by a code to their WhatsApp. The agent never becomes a member of the business.
10. **Photos in Xenibase storage**, in the business's own Xenibase project beside its data tables.
11. **Customers hear back by SMS, and by email when they give one.** WhatsApp to customers is a later phase.
12. **Embeddable on the business's own website**: the catalogue, a form or a button that opens an overlay. Each is a frame from the storefront's domain, allowed only on websites the owner has verified.
13. **Moderation (proposed, awaiting decision):** every upload and text change is checked by a model through the AI layer against a written policy, and anything flagged waits for a person in an operator review queue.

## Where the ideas come from

From public material only, per the clean-room policy:

- **Forms that start workflows:** n8n's Form Trigger, Zapier Forms and Activepieces' Human Input, from their user documentation.
- **Small business pages with a catalogue and chat ordering:** WhatsApp Business catalogues, link-in-bio pages and payment-provider storefronts, from their marketing pages.

Original to Taskiem:
- tables as the catalogue and the order book;
- approvals in chat as the order desk;
- personal data sealed by default;
- the safety design and pricing aimed at street businesses.

## Alternatives considered

- **Hosted forms only, no page.** Rejected as the goal: a form alone does not make a business visible. The forms are still the core, and could ship first.
- **A full site builder.** Rejected: it would turn Taskiem into a different product and open the door to arbitrary content and phishing.
- **Pages on the app's domain.** Rejected: one mistake in a page could reach the app's cookies and origin.
- **Embedding as HTML written into the host site** (a script that builds the form in the site's page). Rejected: the site's own scripts could read what customers type, and the storefront's CSP would not apply. A frame keeps the form on the storefront's origin.
- **A free plan.** Rejected by the product owner: every storefront comes with a domain, which has a cost, and paid plans keep out throwaway scam pages.
- **Moderation:**
  - *A dedicated image-safety service* is cheap and good at nudity and violence, but cannot judge scams, counterfeits or text.
  - *People only* is the most accurate, but does not scale to pages signed up by agents every day.
  - The proposal combines a model with people for what it flags.
- **Integrating an existing storefront or form product** instead of building one. Rejected for the target users: a second account, a second bill and no tie to the approvals in chat.

## Consequences

- Taskiem gains a public, unauthenticated surface. Abuse handling (moderation, reports, operator review) becomes an ongoing operational job, not only code.
- Media storage becomes a requirement (logos and photos); it waits on a storage choice (Xenibase V4 or the platform's own).
- SMS to customers costs money per message, so each plan's allowance is metered, with top-ups.
- The domain platform becomes a dependency of v1, with an integration contract still to agree (registration, DNS, renewal, the domain if a business leaves).
- Field agents bring an operational side: registering agents, their accountability, and possibly commission.
