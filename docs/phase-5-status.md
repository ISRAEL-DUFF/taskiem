# Phase 5 status: storefronts and forms

Phase 5 gives Taskiem a public front door: storefronts for businesses without a website, signed up in person by field agents, and hosted forms for every workflow ([build plan](spec/build-plan.md#phase-5--storefronts-and-forms-about-20-weeks)). The designs are [storefronts](spec/storefronts.md) (decision 0036) and [forms](spec/forms.md) (decision 0037), both proposed.

Gate G5: 100 storefronts published in the pilot market (median under 10 minutes); 40% with an order or booking in their first week; 25 organisations using hosted forms; p75 under 3 seconds on 3G; no reported scam page live beyond 24 hours; a security review with no open high findings; 60% of agent-signed businesses active after 30 days.

Nothing of Phase 5 ships to customers before gate G4.

## Milestones

| # | Milestone | Status |
| --- | --- | --- |
| S0 | Decisions | **Open**: see below |
| S1 | Shared public foundation | **Built**, one PR in review: the [public host](public-host.md), form guards and name checks, [messages](messages.md) by SMS and email, [Xenibase storage](media.md), [embed sites](embed-sites.md), and the VM kit's [on-demand certificates](vm.md#public-pages-storefronts-and-forms) |
| S2 | The storefront page | Not started |
| S3 | Orders | Not started |
| S4 | Bookings | Not started |
| S5 | Chat set-up and field agents | Not started |
| S6 | Domains and embedding | Not started; waits for the domain platform contract |
| S7 | Forms | Not started |
| S8 | Trust, plans and pilot | Not started |

## S0: decisions

| Decision | Who | Status | Blocks |
| --- | --- | --- | --- |
| Accept decisions 0036 (storefronts) and 0037 (forms) | Product owner | Proposed | Everything after S1's groundwork |
| Moderation: an AI check with a review queue, an image-safety service, or people only | Product owner | Proposed: AI check plus queue | S2 (uploads), S8 |
| The preview domain and the form host's domain | Product owner, infra | Open | S1 |
| The domain platform's integration contract: API, renewals with the plan, the domain if a business leaves, transfers | Product owner, domain platform team | Open | S6 |
| Shop and Shop Plus prices, annual pricing, a trial | Product owner, finance | Open | S8 |
| Field agents: staff or paid per business, who registers them, commission | Product owner, operations | Open | S5 |
| Refunds when an owner declines a paid order or cancels a booking | Product owner | Open | S3 |
| The malware scanner for form uploads: run by Taskiem, or a service | Security | Open | S7 |
| The feature names customers see | Product owner | Open | S8 |

Already answered (2026-10-19): own domains from the domain platform; photos in Xenibase storage; SMS, or email when given, to customers; no free plan; new businesses first; both payment timings; bookings in v1; field agents; embedding on the business's own website.

## Dependencies outside Taskiem

| Dependency | Status | Needed by |
| --- | --- | --- |
| Xenibase storage integration (P4-T1) | Built ([media](media.md)); to be proven on staging with `taskiem media check` | S1 |
| Termii sender ID, and an email sender (P4-O1) | The sender is built ([messages](messages.md)); the sender ID approved for the `dnd` channel, Africa's Talking credentials and the no-reply address are open | S1 |
| The domain platform's API | Contract not agreed | S6 |
