# Tally — Product Overview

> Kept for reference. This is the overview of Tally, the reconciliation product built before Taskiem. It is not being continued as a separate product: [decision 0045](../decisions/0045-tally-in-taskiem.md) folds what merchants need from it into Taskiem, and [invoices and the money log](../spec/invoices-and-money-log.md) is the build plan.

**Money truth for Nigerian SMEs.**

*A plain-language guide to what Tally is, the problem it solves, who it serves, everything it does, and the user stories behind each capability.*

**Audience:** anyone who needs to understand Tally — new team members, partners, investors, prospective customers.
**Status key used throughout:** **Live** = built and tested · **Planned** = specified, not yet built.

---

## 1. The problem

A Nigerian small business does not have a money problem. It has a *truth* problem.

Money arrives through channels that do not talk to each other — bank transfers (still dominant), POS terminals, one or two payment gateways, USSD, and cash. Each one settles on its own timeline, deducts its own fees, and reports in its own format. Worse, bank transfers routinely arrive with a reference like "PAYMENT" or nothing at all, so there is no way to tell which sale the money belongs to.

Four consequences follow, and every merchant lives with all of them:

**No single source of truth.** Ask an owner "what did you actually earn last month, and how much of it has truly landed?" and the honest answer is a guess. The gateway dashboard says one thing, the bank app says another, and the POS printout says a third.

**Silent leakage.** Customers get debited while the merchant is never credited. Settlements arrive short. Terminals under-settle. Gateways deduct 2.1% when the contract says 1.5%. Each incident is small enough to miss and frequent enough to matter — and nobody is checking, because checking means an evening with three statements and a calculator.

**No reliable cash position.** Money a gateway calls "paid" may reach the bank tomorrow, or next week. Planning on it is guesswork, and guesswork is how businesses run out of cash while technically profitable.

**No verifiable financial record.** This is the deepest one. Because their cash flow cannot be proven, these businesses are invisible to lenders. Nigeria's SME credit gap persists not because the businesses aren't creditworthy — many clearly are — but because nobody can *demonstrate* it.

### How Tally solves it

Three layers, each independently valuable:

1. **Reconcile.** Pull every channel into one trustworthy ledger, match every payment to the sale it belongs to, and surface what doesn't match as quantified, recoverable items.
2. **Control the reference.** Give the merchant invoicing, payment links, and dedicated account numbers so future payments reconcile themselves automatically.
3. **Monetize the truth.** Use the resulting verified cash-flow record to underwrite working capital, repaid automatically from the merchant's own receivables.

---

## 2. What Tally is — and is not

**Tally is** a financial operations platform that sits beside a merchant's money. It observes, normalizes, reconciles, and explains. Every automated decision it makes stores the evidence that produced it, and every number traces back to a double-entry ledger that always balances.

**Tally is not:**

| Not | Why it matters |
|---|---|
| An accounting system or ERP | It exports clean records *to* QuickBooks and the like; it does not replace them. |
| A payment processor | It never processes payments or holds customer float. Licensed partners move the money. |
| A compliance/AML engine | That is a separate product; Tally feeds it, it does not replace it. |
| A consumer app | B2B only — a merchant back-office tool, never a wallet for the general public. |
| A tax filing service | It produces tax-ready summaries and exports; filing stays with the accountant. |

This restraint is deliberate. Tally earns trust by being the one system that tells the truth about money — which only works if it never has a reason to shade it.

---

## 3. Who benefits

### The business

Nigerian SMEs — retail, services, distribution — collecting through two or more channels, with enough volume that manual reconciliation has become impossible or has quietly been abandoned. They gain recovered money, reclaimed hours, books they can trust, and eventually access to credit.

### The people inside it

The owner stops guessing. The bookkeeper stops spending evenings on spreadsheets. The cashier issues an invoice that settles itself. Each is served by a different surface of the same product (see §4).

### The operator

The company running Tally gains a defensible position: the verified cash-flow dataset accumulating behind every merchant is the asset, and it compounds.

### The financial partner

A partner bank or MFB providing the banking rails gains low-cost, sticky deposits routed through dedicated accounts, plus an SME lending pipeline that arrives pre-scored on evidence rather than self-reported figures — and a repayment mechanism that sits inside the borrower's own cash flow.

### The merchant's customers

Indirectly but really: clearer invoices, a dedicated account number to pay into, and instant confirmation that their payment landed — instead of a WhatsApp exchange of blurry screenshots.

---

## 4. Personas

| Persona | Who they are | What they need | Primary surfaces |
|---|---|---|---|
| **Amaka — Owner** | Owner of a small/medium business. Non-technical. Checks on her phone between other work. | "How much did I really make? Is anything missing? Can I get cash to restock?" | Dashboard, Exceptions, Financing |
| **Tunde — Finance / Bookkeeper** | Part-time or in-house bookkeeper for a mid-size merchant. Spreadsheet-native, detail-driven. | Reconcile inflows to invoices, clear exceptions, export clean records. | Reconciliation, Exceptions, Reports |
| **Ngozi — Cashier / Staff** | Front-line staff issuing invoices and collecting payments. Limited permissions by design. | Create invoices and payment links, see today's takings. | Invoices, Dashboard (limited) |
| **Operator Admin** | Internal — Tally staff running the platform. | Onboard merchants, manage integrations, monitor ingestion health. | Ops tooling, source health |
| **Credit Analyst** | Internal — reviews financing above the auto-approval threshold. *(Phase 3)* | Assess cash flow, set limits, approve or decline advances. | Analyst review queue |

### Roles and permissions

Access is scoped to a single merchant and enforced at the query layer — no cross-tenant read is possible through the API, regardless of role.

| Role | Can do |
|---|---|
| **Owner** | Everything: connect sources, reconcile, export, manage team, request financing |
| **Admin** | Everything except transferring ownership |
| **Finance** | Reconcile, resolve exceptions, export, create invoices — no source or financing access |
| **Staff** | Create invoices and payment links, see today's activity |
| **Viewer** | Read-only dashboard |

Operator-side roles sit outside the tenant: `operator_admin`, `credit_analyst`, `support`.

---

## 5. Features

### 5.1 Onboarding & sources — **Live**

- Sign up, create an organization, invite team members with roles.
- Connect money sources five ways: automatic via the banking rails, bank aggregation, gateway API keys, POS terminal linkage, or CSV/statement upload.
- Per-source health: last sync, status, and the actual error when something breaks.
- Map each source's fee schedule, or let Tally infer effective fees from settlement deltas.
- A failing source degrades only itself — one broken gateway never takes down the tenant.

### 5.2 Ingestion & normalization — **Live**

- Every inbound event is normalized into one canonical transaction shape, whatever its origin.
- Three ingestion modes per source: **push** (webhooks, signature-verified), **pull** (scheduled polling), **upload** (statement files).
- Raw payloads are retained immutably alongside the normalized record, for audit and re-processing.
- Duplicate-proof by construction: the same event delivered twice produces exactly one transaction.
- Replay any source over any date range without creating duplicates.

### 5.3 Reconciliation — **Live**

- **Auto-matching** of payments to sales and invoices above a confidence threshold, with the scoring features stored so every decision is explainable.
- **Ranked suggestions** for anything below the threshold, each showing *why* it was suggested.
- **Manual match, confirm, reject, and unmatch** — every action audited and fully reversible, including the ledger movements it produced.
- **Exact matching** for payments into dedicated virtual accounts: no scoring, no ambiguity, matched at the moment of ingestion.

### 5.4 Exceptions — the leakage engine — **Live**

Eight detected anomaly types, each quantified in Naira:

| Exception | Meaning |
|---|---|
| `unmatched_inflow` | Money arrived with no sale to attach it to |
| `unmatched_sale` | A sale was recorded but no money ever came |
| `duplicate` | The same economic event ingested or settled twice |
| `failed_but_debited` | The customer was debited; the merchant was never credited |
| `short_settlement` | Settled for less than expected, after fees |
| `late_settlement` | Settled past the source's stated SLA |
| `fee_discrepancy` | More was deducted than the agreed or inferred fee |
| `chargeback_reversal` | A reversal or refund undoing a prior match |

Each exception carries a severity, an assignee, and a lifecycle: **open → investigating → resolved | written off | escalated**. Resolutions record the amount actually recovered, and **cumulative leakage recovered** is tracked as the product's headline value metric.

### 5.5 Cash flow & reporting — **Live**

- Dashboard: total inflow for the period, settled vs pending, effective take-rate in basis points, net cash position, open exceptions, leakage recovered, and auto-match rate.
- Time-series of inflow and outflow, with a settled-vs-pending breakdown by source.
- Exports to CSV and Excel across five datasets: **ledger**, **exceptions**, **summary**, **accounting journal** (double-entry postings, import-ready), and **VAT** (monthly tax-ready bases).
- Exports generate asynchronously and download via a secure time-limited link.

### 5.6 Invoicing & collection — **Live**

- Create, send, and track invoices with structured reconciliation references.
- Full lifecycle: **draft → sent → part-paid → paid**, plus **overdue** and **void**. An invoice is only ever marked paid by reconciliation — never by hand.
- Payment links carrying a reconciliation token.
- **Dedicated virtual accounts** per invoice or per merchant: the customer pays into an account issued for them, and the payment reconciles itself the instant it lands.
- Automatic overdue detection and customer nudges.

### 5.7 Payables & expenses — **Live**

- Record money-out with vendor, category, amount, and date, so net cash flow is complete rather than inflow-only.
- Each payable creates its own settled outflow transaction, so the ledger, dashboard, and every export pick it up through the same machinery as any other movement.

### 5.8 Notifications — **Live**

Templated, event-keyed alerts delivered exactly once, however many times the system retries:

| Event | Goes to |
|---|---|
| Invoice sent | The customer, with payment link and reference |
| Invoice paid | The merchant |
| Invoice overdue | The customer, as a nudge with the outstanding amount |
| Exception raised | The merchant |
| Source degraded | The merchant, once per source per day |

Email ships first; SMS and WhatsApp are adapter slots.

### 5.9 Identity & KYC — **Live**

- Capture the contact identity a licensed provider requires before it will open a collection account: name, email, phone, date of birth, BVN.
- The interface states plainly whether details are complete and names exactly which fields are outstanding.
- BVN is write-only: stored for submission, returned only as its last four digits, never displayed in full again.

### 5.10 Working capital — **Planned (Phase 3)**

Specified and schema-ready; gated behind a feature flag and a licensed lending partner.

- **Underwriting** from reconciled data only: average daily settled inflow, inflow stability, auto-match rate, exception rate, tenure → a deterministic score and a conservative limit.
- **Transparent offers**: amount, fee, term, and daily repayment percentage, accepted by the merchant.
- **Disbursement** via the banking partner on acceptance.
- **Automatic repayment sweep** of a configured share of daily receivables, tracked to zero.
- **Facility management**: outstanding balance, repayment history, early payoff, missed-sweep handling, closure.
- **Analyst review queue** for offers above the auto-approval threshold.

### 5.11 Cross-cutting guarantees — **Live**

These are not features so much as promises the architecture keeps:

- **Money is never a float.** All amounts are integer minor units (kobo) with a currency; rates are integer basis points.
- **The ledger always balances.** Double-entry, append-only, enforced by a database trigger and a property test.
- **Everything is audited.** Every user and system action on a financial record is written to an append-only log.
- **Writes are idempotent.** Retrying any financial write — a webhook, an API call, a background job — can never duplicate money.
- **Tenants are isolated** at the query layer, with a test suite that gates deployment.
- **Mobile-first**, tolerant of low-end Android browsers and intermittent connectivity.

---

## 6. User stories

Written as `As a <persona>, I want <capability>, so that <outcome>`.

### Onboarding & sources

- As an **Owner**, I want to sign up and create my business account in minutes, so that I can see value before I lose interest.
- As an **Owner**, I want to invite my bookkeeper with the right permissions, so that they can work without seeing things only I should see.
- As an **Owner**, I want to connect my bank, my gateway, and my POS, so that Tally sees all my money and not just part of it.
- As an **Owner** with no API access to a channel, I want to upload a statement file, so that a channel I can't integrate still counts.
- As an **Owner**, I want to see when a source last synced and what went wrong when it didn't, so that I know whether to trust today's numbers.
- As an **Owner**, I want to record what each provider agreed to charge me, so that Tally can tell me when they charge more.

### Reconciliation

- As a **Bookkeeper**, I want payments matched to sales automatically, so that I only handle what actually needs judgement.
- As a **Bookkeeper**, I want to see *why* a match was made, so that I can trust it without re-checking it myself.
- As a **Bookkeeper**, I want ranked suggestions for ambiguous payments, so that I can clear the queue by choosing instead of searching.
- As a **Bookkeeper**, I want to reject a wrong suggestion and have the engine stop proposing it, so that I don't fight the same mistake twice.
- As a **Bookkeeper**, I want to match a payment to a sale manually, so that an unusual case doesn't become a permanent gap.
- As a **Bookkeeper**, I want to undo a match completely, so that a mistake costs me a click rather than a corrupted ledger.
- As a **Bookkeeper**, I want yesterday's work to still be cleared this morning, so that the queue reflects real work and not system churn.

### Exceptions & recovery

- As an **Owner**, I want to be told how much money is missing and why, in Naira, so that I know whether it's worth chasing.
- As an **Owner**, I want to know when a customer was debited but I was never credited, so that I can raise it with the provider while it's still fresh.
- As an **Owner**, I want to know when a settlement came up short, so that I can claim the difference instead of absorbing it.
- As an **Owner**, I want to know when I was charged a higher fee than agreed, so that I can dispute it with evidence.
- As a **Bookkeeper**, I want to assign an exception to myself and record what I did, so that nothing is investigated twice or forgotten.
- As a **Bookkeeper**, I want to record how much was actually recovered, so that the number we report is real.
- As an **Owner**, I want to see total leakage recovered since I started, so that I know what this product has been worth to me.

### Cash flow & reporting

- As an **Owner**, I want one number for what I actually earned this period, so that I can stop reconciling three dashboards in my head.
- As an **Owner**, I want settled money separated from pending money, so that I don't spend what hasn't arrived.
- As an **Owner**, I want to see my effective take-rate across all providers, so that I know what payments really cost me.
- As an **Owner**, I want a daily chart of money in and out, so that I can see the shape of my business at a glance.
- As an **Owner**, I want to see which source brings in what, so that I know which channels are worth keeping.
- As a **Bookkeeper**, I want to export a clean reconciled ledger, so that my accountant stops asking me for things.
- As a **Bookkeeper**, I want an accounting journal export, so that I can import straight into our bookkeeping tool.
- As a **Bookkeeper**, I want a VAT-ready summary, so that tax season is an export and not a reconstruction.

### Invoicing & collection

- As a **Cashier**, I want to create an invoice in a few taps, so that I can bill a customer standing in front of me.
- As a **Cashier**, I want to send the invoice with a payment link, so that the customer can pay without me explaining account details.
- As a **Cashier**, I want a dedicated account number for an invoice, so that the payment is unmistakably tied to it.
- As an **Owner**, I want invoices to flip to paid by themselves when money lands, so that nobody has to mark anything manually.
- As an **Owner**, I want part-payments tracked, so that I know what's still outstanding on each invoice.
- As an **Owner**, I want overdue invoices chased automatically, so that chasing doesn't depend on me remembering.
- As an **Owner**, I want to void an invoice raised in error, so that a mistake doesn't sit in my books forever.
- As an **Owner**, I want to be told the moment an invoice is paid, so that I can release goods with confidence.

### Payables

- As a **Bookkeeper**, I want to record money going out with a vendor and category, so that the cash picture is complete rather than inflow-only.
- As an **Owner**, I want payables reflected in my cash position immediately, so that the number I see is what I can actually spend.
- As a **Bookkeeper**, I want to filter spending by category, so that I can see where the money goes.

### Team & access

- As an **Owner**, I want to give my cashier invoice access without financing access, so that people see only what their job needs.
- As an **Owner**, I want to change someone's role when their job changes, so that access reflects reality.
- As an **Owner**, I want every action on my financial records logged, so that I can answer "who did this?" with certainty.

### Identity & accounts

- As an **Owner**, I want to be told exactly which details are still missing, so that I'm not hunting through a form guessing.
- As an **Owner**, I want my BVN stored securely and never displayed again, so that supplying it doesn't create a new risk.
- As an **Owner**, I want a dedicated account number for my business, so that customers can pay me directly into something that reconciles itself.

### Working capital — *Planned*

- As an **Owner**, I want to see whether I qualify for an advance and for how much, so that I know my options before I need them.
- As an **Owner**, I want the fee and repayment terms stated plainly up front, so that I'm not agreeing to something I don't understand.
- As an **Owner**, I want repayment taken as a small share of daily takings, so that repayment follows my cash flow instead of fighting it.
- As an **Owner**, I want to see my outstanding balance and every repayment, so that I always know where I stand.
- As an **Owner**, I want to pay off early without penalty, so that a good month works in my favour.
- As a **Credit Analyst**, I want borderline offers in a review queue with the underwriting evidence attached, so that I can decide on facts rather than impressions.

### Operator

- As an **Operator Admin**, I want to onboard a merchant and connect their sources, so that they reach value without a technical lift.
- As an **Operator Admin**, I want to monitor ingestion health across all merchants, so that I find a broken integration before the merchant does.
- As an **Operator Admin**, I want to replay a source over a date range, so that a provider outage can be repaired without duplicating anything.

---

## 7. The four journeys that matter

**1. First reconciliation — the "aha" moment.** An owner signs up, connects the rails plus a bank account and a gateway, and Tally ingests 90 days of history. Within minutes: *"You have ₦480,000 across 12 exceptions we can help you recover."* They resolve the top three and export a clean statement. This is the moment the product proves itself.

**2. Daily close.** The bookkeeper opens the reconciliation workspace. Auto-matched items are already cleared. They work the suggestion queue — accept, reject, accept — resolve the new exceptions, and the day's cash position is trustworthy. Fifteen minutes instead of an evening.

**3. Self-reconciling collection.** A cashier creates an invoice with a payment link and a dedicated account number. The customer pays. The inflow reconciles at the moment of ingestion, the invoice flips to paid, and the merchant is notified. Nobody matched anything.

**4. Working capital.** *(Planned)* The owner sees *"You're eligible for up to ₦1,500,000"*, reviews a transparent fee and a 12% daily-receivables repayment, and accepts. Funds are disbursed by the banking partner, and repayment sweeps automatically until the facility closes.

---

## 8. Where the product stands today

| Phase | Scope | Status |
|---|---|---|
| **Phase 1 — Reconciliation wedge (MVP)** | Multi-source ingestion, normalized ledger, matching engine, exceptions, cash-flow dashboard, exports | **Complete** — built, tested, verified end-to-end |
| **Phase 2 — Operational tooling** | Invoicing, payment links, virtual accounts, payables, accounting/VAT exports, notifications | **Complete** |
| **Phase 3 — Working capital** | Underwriting, offers, disbursement, repayment sweep, analyst queue | **Not built** — specified and schema-ready, gated behind a licensed lending partner |

The reconciliation engine, exception recovery, dashboards, invoicing, virtual accounts, payables, and exports are all working today and exercised by an automated test suite that runs the full journey — from a signed webhook arriving to an invoice marking itself paid — against real infrastructure.

What remains before a public launch is operational rather than conceptual: production credentials for the banking partner, email delivery, deployment configuration, and a pilot cohort.

---

*Prepared by the Tally team. Living document — update it when the product changes.*
