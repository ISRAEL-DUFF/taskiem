# Becoming Nigeria's workflow automation platform: a chat-first strategy

Date: 2026-10-16 · Status: proposal for the founder's review

Taskiem and Xenibase together are to be the go-to workflow automation platform in Nigeria: automation workflow as a service, reached first through a chat interface to an AI agent, because Nigerian SMEs live on their phones. This page says how far the two products already are, what stands between them and that goal, and the order to do the rest in.

It was written against both codebases on 16 October 2026: Taskiem at `main` (`3c4a221`) and Xenibase at `main` (`9fa9166`, release 4.2.2). Related: the joint document *Xenibase × Taskiem: How the Two Platforms Work Together* (7 October 2026, in the Xenibase repository), [the integration plan](../xenibase-integration.md), [what needs people](../needs-people.md), the [build plan's go-to-market sequence](../spec/build-plan.md#go-to-market-sequence), and the [user-story catalogue](user-stories.md) that imagines the finished product.

## 1. Summary

**Most of the chat-first product is already built.** The critical path to the goal is not code. It is roughly ten people-and-operations items, followed by three product gaps that stop the chat experience from being truly chat-first, and then Xenibase becoming the invisible data layer under the agent.

| | Today | Needed for the goal |
| --- | --- | --- |
| WhatsApp as a client of the platform | Built: bind a number, status, run with forms, approvals with buttons and PIN, build with AI, own numbers, public menus, four Nigerian languages (drafts), voice notes (beta), USSD | Live: a real number, approved templates, reviewed translations |
| AI | Built: builder from a template library, repair of failed runs, evaluation suite, budgets, redaction | A real model account and its data-processing terms; evals from real requests |
| Money | Built: flat naira plans, Paystack and Flutterwave checkout, invoices with VAT, dunning | Prices, a merchant account, terms of service |
| Cloud | Built: Helm chart, one-VM kit, HA and failover code, SLOs, status page | A Nigerian region, the first deployment, on-call |
| Xenibase | Live on production (4.2.2): managed Postgres, data API, auth with SMS and WhatsApp codes, storage, realtime, naira billing | Parameterised writes, project creation by token; then data tables under the agent |
| Chat-first | Web-first, chat-assisted | Signup from WhatsApp, connecting apps from chat, guarded publishing from chat, model-routed intents in English and Pidgin |

## 2. What already exists

### Taskiem

Taskiem's WhatsApp number is a full client of the platform ([WhatsApp](../whatsapp.md)), not a notification channel. A person who has linked their number can:

- ask `status` or "what failed today?" and get runs by status, the latest failures and approvals waiting for them;
- `run <workflow>` and fill the inputs in a WhatsApp form (Flows), then confirm with **yes**;
- approve or reject a payout with buttons, confirmed by a six-digit PIN where the policy allows it, or by a passkey through a hand-off link;
- say "every Friday text my customers who owe me" and get an AI-drafted workflow read back as plain numbered steps, saved as a draft after **yes** ([AI](../ai.md), [templates](../templates.md));
- work in Pidgin, Yoruba, Hausa or Igbo once the drafts are reviewed, and send voice notes ([languages](../languages.md));
- on an organisation's own number, offer customers a public self-service menu, and on a USSD code a menu for feature phones ([USSD](../ussd.md)).

Around that: nineteen SME templates; twenty-five connectors, sixteen of them African (Paystack, Flutterwave, Moniepoint, Termii, Dojah, Mono, Remita, Interswitch, OPay, M-Pesa, MTN MoMo and others); durable, exactly-once execution with approvals, audit chain and PII sealing; flat-priced plans in naira with Paystack checkout ([billing](../billing.md)); self-serve signup with a guided first workflow and signup-to-first-run measured ([onboarding](../onboarding.md)); embedding for partners such as Payrolla ([embedding](../embedding.md)); the Xenibase connector, `xenibase@1`, which starts a workflow when a row changes and reads rows ([integration](../integrations/xenibase.md)). All of it is tested against fakes.

### Xenibase

Xenibase is on production at release 4.2.2: shared and dedicated managed Postgres, branching, backups, transactional webhooks, scheduled jobs, and the V4 backend services: a data API, auth with email, phone (SMS and WhatsApp codes), OAuth and MFA, storage with signed URLs and image transforms, realtime, SDKs for JavaScript, Dart and Go, and naira billing with price books and a ledger. V4.1 closes the gaps V3 and V4 left; 4.2 adds the emails, legal pages and brand.

## 3. What stands in the way

### 3.1 People and operations, not code

Until these are done, no Nigerian SME can use the product, however good the code is. They are Taskiem's own register, [what needs people](../needs-people.md), reduced to the items on the critical path:

| Item | What it unblocks |
| --- | --- |
| W1, W2: Meta business verification, a WhatsApp Business Account and number, the eight templates approved, the two Flows published | Every WhatsApp feature for real users |
| AI1: a model API account (Anthropic by default) and its data-processing terms; whether prompts may leave Nigeria | The builder, repair and intent routing against a real model |
| B1, P4-B2, P4-B4: naira prices, a Paystack merchant account, terms of service | Charging anyone |
| P4-C1, D5: a Nigerian cloud region and the first deployment (decision 0032: one VM until a named trigger) | A public URL |
| P1: sandbox accounts for Paystack, Termii, Moniepoint and the rest | Connectors going live |
| L1 to L4: native speakers to review the four language drafts | Pidgin and the others for real users |
| X3: two design partners | Real requests, real feedback, the evaluation suite (AI2) |
| P4-O1: a sending domain for signup email | Public signup |

These are the only priority for the first month.

### 3.2 Three product gaps that keep the chat from being chat-first

Once live, the WhatsApp experience is "web-first, chat-assisted". Three gaps, each already recorded in the docs as not built, keep it from being chat-first:

1. **Signup from WhatsApp.** Today a person signs up on the web, confirms their email, then links a number under Account > WhatsApp. The go-to-market sequence (step 2) wants the reverse: a stranger messages the number, an organisation is created for them, and they touch the web only later, if ever. [Phase 4 status](../phase-4-status.md#p4-2-onboarding) lists this as not built.
2. **Connecting apps from chat.** Every template needs a connection (a Paystack key, a Termii key, a Google account, the [POS terminals'](pos-terminals.md) client and webhook secret), and connections are set up only in the web app. The agent must be able to send a WhatsApp form for an API key, or an OAuth link for Google, and record the connection. The generic OAuth connection flow is "planned" in the [integration plan](../xenibase-integration.md) (P2-T1).
3. **Publishing from chat.** The AI builder saves drafts only, and publishing happens in the web app with the usual checks. That rule is a real trust asset for fintechs and must stay for anything that moves money. But a provisions-store owner will never open a canvas to click Publish. Chat publish, after the plain-steps read-back and an explicit **yes**, should be allowed for workflows that only read and message; money workflows keep the web path or a PIN step-up under a policy. Changing an existing workflow by chat is also not built ([WhatsApp: not yet](../whatsapp.md#not-yet)).

A fourth gap sits under these. English intents are matched by keyword prefixes, and the model routes messages only in the other languages. A chat-first agent needs the model to handle free-form, multi-turn English and Pidgin too, under the same constraint as today: it may only route to the existing commands and may never confirm, publish or approve on its own.

### 3.3 Xenibase under the agent

For a phone-only SME, Xenibase should be invisible. The templates today assume a Google Sheet for customers, debtors, staff and stock; a trader in Onitsha has none of those. The joint document's "data tables" idea, item P3-T2 in the integration plan, is the fix: told "remind my customers who owe me", the agent creates a debtors table in a Xenibase project on the customer's behalf, fills it from chat, and the workflow runs off it. The SME never hears the word database. That needs two things from Xenibase, both already open in the plan: a parameterised write endpoint (P1-G1) so Taskiem can write rows safely, and project creation by token with quotas (P3-G2).

Xenibase's phone-OTP auth then gives a second product for free: a customer-facing self-service menu on the SME's own number, backed by real records, which Taskiem's public menus already support. And Xenibase becomes Taskiem's first money-handling reference customer by running its own dunning, onboarding and incident alerts as Taskiem workflows (P4-T4).

## 4. Sequence

| Weeks | Theme | Work | Done when |
| --- | --- | --- | --- |
| 1 to 4 | **Turn it on** | The people items in 3.1. Deploy to one Nigerian VM (decision 0032). Put ten SMEs from the Payrolla and holdco network on the shared number with comped plans (`taskiem billing grant`). | Ten organisations have run a workflow started from WhatsApp; signup-to-first-run measured |
| 4 to 10 | **Make it chat-first** | WhatsApp signup; connect-from-chat (a Flow for keys, an OAuth link for Google); guarded chat publish; model-routed English and Pidgin intents; change-by-chat. Grow the evaluation suite from the real requests these SMEs send (closes AI2). | A new SME goes from first message to first run without opening the web app |
| 8 to 16 | **Xenibase underneath** | Parameterised writes and project creation by token in Xenibase; the Xenibase connector's write actions; data tables in the agent; retire the Google Sheets assumption from the SME templates; Xenibase's own operations as Taskiem workflows. | "Remind my customers who owe me" works with no sheet and no database in sight |
| 16 on | **Distribution** | The Business Solution Provider decision (W3) for own numbers; vertical template packs (retail, schools, churches, logistics, estates, clinics); partners who already own SME relationships: Payrolla, POS agent networks, microfinance banks, telcos; the embedded tier for SaaS companies. | Starter and Growth self-serve; the first partner-sourced cohort |

## 5. Decisions to take early

1. **A chat-only tier.** The placeholder Starter plan (₦15,000 a month) is high for the long tail. A cheaper tier on the shared number, with template costs passed through at cost, fits the flat-pricing rule (spec 16) and the Meta cost model.
2. **AI data residency.** Prompts leave Nigeria under the default Anthropic provider. The self-hosted provider exists in code; the NDPA review (AI1) decides whether a residency tier needs it.
3. **How far chat publish goes.** The proposal above: read-and-message workflows publish from chat after **yes**; anything with a payment write needs the web, or a PIN step-up under a policy. Record it as a decision before building.
4. **Data tables: Xenibase only, or one backend among several** (plan question Q9). Xenibase-only is simpler and is the bundle's reason to exist; it must still pass the third-party test.
5. **Which product issues the combined invoice** (Q3, Q4), once there is something to bundle.

## 6. Risks

| Risk | Mitigation |
| --- | --- |
| Meta: verification delays, template rejections, the BSP question | Start W1 and W2 now; own numbers by hand until W3 is decided; USSD and SMS as the fallback channel |
| The AI drafts the wrong thing | Already structural: drafts only, validated like human work, read back as plain steps, explicit yes; keep the money rule; measure on the real-request suite |
| SMEs churn after the first workflow | The checklist and templates by vertical; outcomes back on WhatsApp so the value is seen daily; a chat-only price |
| Collecting payment from SMEs | Paystack cards and transfers are built; add dedicated virtual accounts; annual plans at a discount |
| Coupling the two products | The integration principles (decision 0018): public surfaces only, each product stands alone |
| Personal data in chat and prompts | Masking, sealing and redaction are built; the DPA and the residency decision are people items |
