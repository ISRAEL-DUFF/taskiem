# From staging to the first 5,000 businesses

Date: 2026-10-26 · Status: proposal for the founder's review · Owner: business strategy

This page is the business plan for taking Taskiem from one staging server to 5,000 businesses served. It sits above the [chat-first strategy](chat-first-nigeria.md) (what the product must become) and [what needs people](../needs-people.md) (the full register): it says which of those things matter for the count, in what order, and how we will know it is working.

Written against `main` at `68b339a`.

## 1. Where we are, honestly

| | Today |
| --- | --- |
| Product | Very broad and well tested: the engine, WhatsApp and Telegram chat, the AI builder, over 30 connectors, 58 templates, billing in naira, storefronts and field agents, forms, POS terminals, invoices and the money log, Schools |
| Running | Staging only, at `staging.ispend.name.ng` (since 23 October). No production |
| Customers | None. No design partner signed (X3), no tenant on a plan, no real request in the evaluation suite (AI2) |
| Prices | Decided 21 October (B1): Starter Trader ₦15,000, Growing Business ₦45,000, Enterprise & Regulated ₦180,000 a month; Shop ₦5,000 and Shop Plus ₦10,000 kept off the public list |
| People items | 115 open, 8 done ([register](../needs-people.md)) |
| Pace | 362 commits in the last 11 days |

The pace is our biggest asset and, right now, our biggest risk. Every week of new surface without a customer is a week of untested assumptions about what Nigerian businesses will pay for. **The constraint on reaching 5,000 is no longer code. It is legal, Meta, money, a production server, and someone selling.**

## 2. What "5,000 businesses served" means

We count a business as **served** when it is an organisation that ran at least one successful workflow in the last 30 days. Paid and comped are counted separately; signups that never run anything are not counted.

| Metric | Why |
| --- | --- |
| **Served (30-day active) businesses** | The goal. The north star |
| Paying businesses and MRR | Whether it is a business |
| Activation: first successful run within 7 days of signup | Where self-serve and agents leak |
| Time from signup to first run | Gate G4 wants under 15 minutes |
| Logo retention at day 30 and day 90, by channel | Whether a channel is worth scaling |
| Cost to acquire, by channel | Agents, partners and self-serve cost very different amounts |

The usage snapshots (`engine/billing/snapshot.go`) already hold most of this per tenant per day. One weekly report from them is the first thing the business needs from engineering.

## 3. The strategy in one paragraph

Turn it on for a handful of friendly businesses within six weeks; then win **one wedge where Taskiem is about money the owner can count** (school fees for the January term, and POS cash-up for shops) instead of selling "automation" to everyone; then grow through **channels that already hold many businesses** (school associations, field agents, POS providers, microfinance banks, Payrolla) rather than one-by-one self-serve; and keep two or three regulated design partners on Enterprise for credibility and margin. Self-serve on WhatsApp is the long-term front door, but it will not carry the first 5,000 on its own.

## 4. Why these wedges

The product does many things. A new company can only be known for one or two. The wedge has to pass four tests: the owner feels the pain weekly, the value is in naira they can see, the product for it is already built, and there is a channel that reaches many of them at once.

| Candidate | Pain felt | Value in naira | Built | Channel to many | Verdict |
| --- | --- | --- | --- | --- | --- |
| **Private schools: fee collection** ([schools](../schools.md)) | Every term | Fees collected faster; fewer debtors; no evening reconciling | Deep: account number per student, posting, receipts, debtors, reminder ladder, clearance, gate | Proprietors' associations, school software vendors, field agents | **Lead wedge** |
| **POS shops: daily cash-up** ([POS terminals](pos-terminals.md)) | Every evening | Shortages and failed payments caught the same day | Built for Moniepoint; OPay waits on OPay (P16) | Moniepoint and OPay themselves, their agent networks | **Second wedge**, gated on Moniepoint access (P15) |
| Storefronts with field agents | Moderate | Orders and bookings | Built (Phase 5) | Field agents | Ride along with the agent team; not a wedge alone |
| General SME "remind my debtors" | Real but vague | Hard to show | Built | Self-serve only | Keep as the default for walk-ins; not where we spend |
| Regulated fintechs and MFBs | High | Large | Built (governance, audit, BYOK, SSO) | Founder-led sales | Two or three design partners on Enterprise; not a volume channel |

**The timing for schools is a gift and a deadline.** Nigerian private schools resume for second term in early January. A school that issues second-term invoices on Taskiem is a school that keeps using it all term. Missing January means waiting until late April. Everything in section 6 is dated to make January.

## 5. Where 5,000 comes from

A working model, to be replaced by pilot data at the end of February. Every number below is an assumption until then.

| Channel | How it works | Assumption | By end of 2027 |
| --- | --- | --- | --- |
| Field agents | The contracted team already decided for Phase 5 (base plus a bonus per activated business, with clawback); they sign up schools, shops and storefronts in person | 25 agents by mid-2027, about 4 activations each a week, 60% still active at 90 days | ~1,800 |
| Associations and vendors | School proprietors' associations (zone by zone), school-management software vendors who lack payments, market and trade associations | 15 partnerships averaging 60 active businesses | ~900 |
| POS providers | A referral or co-marketing arrangement with Moniepoint (and OPay once its API is open) so cash-up is offered to their merchants | One provider, starting Q2 | ~1,000 |
| Embedding and holdco | Payrolla's companies, MFBs offering Taskiem to SME customers under their name ([embedding](../embedding.md)) | 2 partners | ~600 |
| Self-serve and referral | Website, WhatsApp, owners telling owners | Grows once WhatsApp signup (chat-first gap 1) ships | ~700 |
| **Total** | | | **~5,000** |

The illustrative revenue at 5,000, with an assumed mix (2,000 on Shop at ₦5,000; 2,000 on Starter Trader at ₦15,000; 950 schools and growing businesses at ₦45,000; 50 on Enterprise at ₦180,000), is about **₦82 million a month** before VAT and pass-through costs. The mix matters more than the count: one school is worth nine shops.

## 6. The plan by stage

### Stage 1: Turn it on (now to 6 December 2026)

**Done when:** production is up on a Nigerian server, ten real businesses (comped) have each run a workflow, five strangers have been timed from signup to first run, and we can legally take a naira.

| Item | Register | Why it is on the critical path | Target |
| --- | --- | --- | --- |
| Trademark search for the name | L3 | We are about to print QR cards, agent shirts and a domain. If the name fails, all of it is wasted. Choose between the three brand directions in `docs/brand/` only after | This week |
| Meta business verification, the shared number, templates, Flows | W1, W2 | The product is WhatsApp-first and Meta takes weeks. Start today; pilot on Telegram and the web until it lands | Submit this week |
| Terms of service, privacy policy, subscription agreement, DPA | P4-B4 | Cannot charge, and the website's legal links are empty | Counsel engaged this week; drafts by 20 Nov |
| Paystack merchant account in the company's name; VAT registration | P4-B2, P4-B3 | Cannot charge | 20 Nov |
| Production: provider, region, domain, one VM per decision 0032, backups and a restore drill | P4-C1, P4-C3, D5 | Staging sits on a holdco domain; customers need ours | Live by 30 Nov |
| Sending domain for signup email | P4-O1 | Public signup | 20 Nov |
| Model API account and its data terms | AI1 | AI builder and moderation | 13 Nov |
| On-call: a primary and a secondary | P4-R1 | Someone answers when a school's payments stop posting | 30 Nov |
| Ten friendly businesses from the holdco network, comped (`taskiem billing grant`) | P4-B5 | Real requests; the first proof | 6 Dec |
| Five timed onboarding sessions with strangers; three to five owner sessions on templates | P4-O4, NT1 | What actually slows people down | 6 Dec |

**What engineering does in Stage 1.** A freeze on new areas and new connectors. Capacity goes to: whatever the ten businesses and five sessions trip on; the weekly metrics report (section 2); and readiness for Schools in January (statement imports and posting from the school's actual bank, the parent-facing messages reviewed by a bursar). New work starts only when a customer or a pilot asks for it.

### Stage 2: Win the wedge (7 December 2026 to 28 February 2027)

**Done when:** 40 schools ran second-term fees on Taskiem, 150 shops ran a daily cash-up or a storefront, two regulated design partners are signed, and we know day-30 retention and cost to acquire by channel.

- **Schools, one city first (Lagos).** Recruit 40 schools by mid-December so their January invoices go out from Taskiem. Sell the outcome, not the software: *every child gets an account number; you see who paid tonight; the reminders go themselves*. Each school gets a named onboarding person who imports students and starts the term with the bursar, in person or on a call.
- **POS shops, one market.** Start Moniepoint access now (P15). Pilot with 150 shops through the field-agent team in one dense market, cash-up first, storefront offered second.
- **Design partners.** Two regulated fintechs or MFBs (X3), founder-led, on Enterprise & Regulated, comped to 31 March then paid. Their compliance sign-off (X5) is the reference that sells the next ten.
- **Turn billing on** (`TASKIEM_BILLING=on`) on 1 February for everyone not comped, after the trial. Convert at least a third of the pilot to paid by end of February; a pilot that will not pay is a finding, not a failure.
- **Gate G4 conflict.** The build plan says nothing of Phase 5 ships before G4, which needs 60 days of 99.9% availability. The January schools window cannot wait for that. Proposal: run Stage 2 as an explicitly labelled pilot on comped and early plans, while the G4 window is measured in parallel. This needs the founder's decision (section 8).

### Stage 3: Build the channels (March to June 2027)

**Done when:** 1,000 businesses served, at least two channels with day-90 retention above 60% and a known cost to acquire that a plan pays back within three months.

- Scale the agent team from the pilot size to about 15, in Lagos and one more city, paid on activated businesses retained at 30 days.
- Sign the first proprietors' associations and one or two school-software vendors; offer them a partner price and their members a term's discount.
- Approach Moniepoint with the pilot's numbers for a referral or co-marketing arrangement.
- Ship the chat-first gaps that self-serve needs ([chat-first strategy, 3.2](chat-first-nigeria.md#32-three-product-gaps-that-keep-the-chat-from-being-chat-first)): signup from WhatsApp, connecting from chat, guarded publishing from chat.
- Third-term (late April) is the schools' second sale: renewals plus every school their association referred.

### Stage 4: Scale to 5,000 (July to December 2027)

- 25 agents in three cities; partner-sourced cohorts each month; the first MFB embedding live.
- September's first term is the largest school onboarding of the year: plan for it from June.
- Customer success moves from named people to a team with a WhatsApp support line and playbooks per vertical.
- Ghana or Kenya only after 5,000 in Nigeria, and only where a partner brings the first cohort.

## 7. People the business needs

None of the plan works without these. Engineering cannot fill them.

| Role | When | Owns |
| --- | --- | --- |
| Partnerships and sales lead | Now | Schools associations and vendors, POS providers, MFBs, design partners |
| Onboarding and customer success (two, Yoruba, Hausa or Igbo between them) | December | The first 40 schools and 150 shops; becomes the support line |
| Field agent lead | December | Recruiting, training, quality and fraud checks on agents |
| Counsel (retained) | Now | L1 to L3, P4-B4, P4-K2, the agent contracts |
| Finance (part-time) | December | VAT, invoices, the refunds in P4-B7, agent pay |

## 8. Decisions for the founder

| # | Decision | Recommendation |
| --- | --- | --- |
| 1 | The lead wedge | Schools for January, POS second, everything else rides along |
| 2 | Run a labelled pilot before G4's 60-day window ends | Yes, on comped and early plans, with the window measured in parallel |
| 3 | A freeze on new areas and connectors until ten businesses are live | Yes; exceptions only for what a pilot customer needs |
| 4 | How schools pay | Per term, which is how schools budget: for example the Growing Business price for a term at a discount to three months, or priced by student count. Billing today supports monthly and annual, so a term interval would need building; until then, annual with a mid-year cancel |
| 5 | Shop pricing is not public, but the website teaser says "From ₦5,000 a month for a shop" | Make Shop public for agent-signed businesses, or change the teaser. Do not advertise a plan people cannot buy |
| 6 | A chat-only tier below Starter (chat-first decision 1) | Wait for pilot data; Shop already fills the gap if it is public |
| 7 | Budget for Stage 2: agents, onboarding staff, counsel, Meta and SMS costs | A number from the founder; the plan above scales to it |

## 9. Risks

| Risk | Mitigation |
| --- | --- |
| Meta is slow or rejects templates | Submit now; pilot on Telegram, the web and SMS; USSD for feature phones |
| The name fails its trademark search after we have spent on it | Search this week, before any printing |
| Building continues to outrun customers | The freeze; a weekly review where every new piece of work names the customer who asked for it |
| Missing the January school window | Dated plan; schools recruited by mid-December; onboarding people hired in December |
| Field agents sign up businesses that never use it | Pay on activation retained at 30 days, with clawback (already decided); the owner's `AGREE` code already prevents signing up without consent |
| SMEs churn after the first month | Wedges where value repeats weekly; results sent to the owner on WhatsApp daily; retention watched by channel |
| Pass-through costs (SMS, WhatsApp templates, AI) eat the cheap plans | Plan allowances and overage are built; check gross margin per plan monthly from the usage snapshots |
| A money incident at a school or a partner | Approvals, PINs, exactly-once and the audit chain are built; on-call in place before the first school; one written incident process |

## 10. The first two weeks

1. Founder decides 1, 2, 3 and 7 in section 8.
2. Trademark search started; counsel engaged for terms, privacy and agent contracts.
3. Meta business verification and the WhatsApp number submitted.
4. Production provider and domain chosen; Paystack merchant account and VAT registration started.
5. Partnerships lead hired or named; a list of 100 Lagos private schools and 3 proprietors' association contacts.
6. Ten friendly businesses named and invited; the first one live on staging over Telegram.
7. Engineering: the weekly metrics report, and the schools readiness list for January.

Progress is tracked here and in [what needs people](../needs-people.md); this page is updated every Monday.
