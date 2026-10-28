# Marketing kit

Date: 2026-10-26 · For the team: marketing, sales, QA, product, and investors

Four documents in the relay brand ([brand](../../brand/README.md)), written from the repository on 26 October 2026. Each fact in them comes from the specs, decisions and code, and each figure from outside is sourced.

| Document | For | File |
| --- | --- | --- |
| Positioning brief | Everyone who writes or talks about Taskiem: who it is for, the problem, three pillars, proof points, messaging by audience, voice, and claims to hold back | [taskiem-positioning-brief.pdf](taskiem-positioning-brief.pdf) (one A4 page) |
| What is Taskiem? | Beginners: marketing, QA, product managers, new joiners. What Taskiem is, every feature with an example, how it helps SMEs and fintechs, what is live, notes for each team, FAQ | [what-is-taskiem.docx](what-is-taskiem.docx) (Word, 24 pages) |
| Onboarding businesses | Operators, field agents, partner success and support: the three ways to bring a business on (field agent, self-serve sign-up, `taskiem bootstrap`), what to set up first, each step, a visit script, the first week, verification, a staging rehearsal, troubleshooting and a quick reference. Written 28 October 2026 from [agents](../../agents.md), [onboarding](../../onboarding.md), [the staging runbook](../../runbooks/staging.md), [moderation](../../moderation.md) and [WhatsApp](../../whatsapp.md); where they disagree, the docs win | [onboarding-businesses.docx](onboarding-businesses.docx) (Word, 19 pages) |
| Investor presentation | Fundraising: problem, market, every feature area, trust, partners, business model, edge, progress, team and the ask, with speaker notes in the editable version | [taskiem-pitch-deck.pdf](taskiem-pitch-deck.pdf) (20 slides) |

## Business cards and staff ID cards

The [card maker](https://claude.ai/artifact/8cR1TFT5U9RiVB6RpwiruJ) makes both cards for one person, with a PDF download; `make-cards.mjs` makes them for a whole list. Both use one design, `cards/card-maker.html`: see [cards](cards/README.md).

## Editing them

The editable originals are Claude artifacts owned by the founder's account: the [pitch deck](https://claude.ai/artifact/1BccFeT6bA8771ELKzSrVF) (downloads as .pptx or PDF, with speaker notes) and the [positioning brief](https://claude.ai/artifact/VkLbjT1r236nHn5VNtnUvy). The guide and the onboarding playbook are edited in Word. When one changes, replace its file here.

## Before using them outside the team

- **Fill the deck's placeholders.** The team slide, the ask and the closing contacts have `[brackets]`.
- **Check the claims register.** The documents follow [the website's claims register](../website.md#claims-register). The deck's cover and notes say "Hosted in Nigeria", which needs the Nigerian region (P4-C1) before it is said in public.
- **Recheck figures as the product grows.** Counts are as of 26 October 2026: 58 templates, 37 connectors (32 outside services plus Taskiem's own), 15 money rails. Market figures: 39.6 million MSMEs and about 46% of GDP (SMEDAN/NBS National MSME Survey, 2020 data); 95%+ of Nigerian internet users on WhatsApp (Statista, Q3 2024).
- **Taskiem is a working name**, pending the trademark search ([decision 0006](../../decisions/0006-working-name.md)).
