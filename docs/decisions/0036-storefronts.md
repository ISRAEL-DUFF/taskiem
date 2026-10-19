# 0036 — Storefronts: a public page whose forms start workflows

Date: 2026-10-19 · Status: Proposed (spec under review: [storefronts](../spec/storefronts.md))

## Context

The chat-first strategy targets small Nigerian businesses that run on WhatsApp and have no website. They need to be found and ordered from, but a website is out of reach, and the forms and automations behind one more so. Taskiem has no hosted forms (open question Q10 in the Xenibase plan), though it has most of what a small public page needs behind it: data tables, schema-generated forms, approvals in chat, payment and SMS connectors, PII sealing and custom-domain verification.

## Decision (proposed)

1. **A one-page storefront per tenant, as the front door to workflows**, not a website builder: fixed mobile-first templates, a business card, a catalogue and actions. No custom HTML, CSS or JavaScript, ever.
2. **The catalogue and orders are data tables.** Owners edit them as they already edit tables, from the web app or chat, and workflows read and write them.
3. **Actions are hosted forms generated from the action workflow's input schema**, starting runs through a new `storefront` trigger in wd/v1, deduplicated per submission. The customer gets an immediate reference; the outcome arrives by message, never by holding a page open.
4. **Served on a dedicated registrable domain**, isolated from the app's origin and cookies, rendered on the server by the edge role, small enough for 3G and cheap phones.
5. **Trust and safety in v1:**
   - verification levels gate what a page can do;
   - look-alike and reserved names are refused;
   - forms can never ask for card details, PINs, passwords, OTPs, BVN or NIN;
   - payments go only through the business's provider link;
   - content is moderated, with public reports and operator suspension;
   - rate limits and tracker-free bot defences protect the forms.
6. **Personal data:** the business is the controller and Taskiem the processor. Customer fields are sealed, there is a notice on every form, retention applies, and pages carry no trackers or cookies.
7. **A free plan and a low-priced shop plan**, because the cheapest plan today is beyond a street business.

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
- **Integrating an existing storefront or form product** instead of building one. Rejected for the target users: a second account, a second bill and no tie to the approvals in chat.

## Consequences

- Taskiem gains a public, unauthenticated surface. Abuse handling (moderation, reports, operator review) becomes an ongoing operational job, not only code.
- Media storage becomes a requirement (logos and photos); it waits on a storage choice (Xenibase V4 or the platform's own).
- SMS to customers costs money per message, so the free plan's allowance must be metered.
