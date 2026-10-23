# Balans — Product Overview

> Kept for reference. This is the founder's overview of Balans, the school fee product built before it was retired into Taskiem. It is not being continued as a separate product: [decision 0046](../decisions/0046-balans-in-taskiem.md) rebuilds it as Taskiem's **Schools** area, and [schools](../spec/schools.md) is the build plan. Where the two disagree (the double-entry ledger, the 72-hour parent link, receipts a parent can check), the decision and the plan win.

**Fee collection and reconciliation for Nigerian private schools.**

This document is for someone evaluating the product. It states the problems
Balans solves, how it solves each one, and then walks every flow that matters —
the money, the people, and the recovery paths — in diagrams.

Written 22 October 2026. Everything below describes software that exists and
runs, except §10, which says plainly what does not exist yet.

---

## 1. In one paragraph

A school collects fees. Parents transfer money. Somebody has to work out who
paid what, tell the parent it arrived, chase the ones who have not, and tell the
owner where things stand. Balans does that. The mechanism is a single idea:
**every student gets their own bank account number.** A parent transfers to it
and the payment lands on that child's ledger by itself, with no bursar in the
loop, no narration to decipher, and no argument about whether it arrived.

---

## 2. The problems, and how Balans solves each one

### 2.1 "How much did we collect this week, and who still owes?"

**The problem.** A proprietor cannot answer this without somebody spending an
afternoon compiling it from booklets, bank statements and WhatsApp. By the time
the answer exists it is out of date.

**How Balans solves it.** Collection is a by-product of the ledger, not a
separate reporting exercise. Every credit posts against a specific child's
enrollment the moment it arrives, so collected-vs-expected, the debtor list and
the aging buckets are always current. The proprietor opens a phone in the
evening and reads the number.

### 2.2 Money leaks, and it is structural

**The problem.** In a documented Nigerian case, a bursary insider had roughly
320 students pay into a personal account while marking them cleared on the
school portal — **₦457 million**, caught only by an audit that compared portal
records against bank statements. The hole was not the theft; it was that
"cleared" was a flag a person could set independently of whether money moved.

**How Balans solves it.** Four structural controls, described in full in §9:

- The ledger is **append-only**. Entries cannot be edited or deleted.
- Clearance is **derived** from the ledger, never a settable flag.
- Manual postings and discounts at or above the school's threshold need a
  **second person**.
- Every action is in an **audit trail** with actor and timestamp.

A bursar at a Balans school cannot mark a child cleared without a matching
credit existing, because there is no field to set.

### 2.3 "I paid — check your account"

**The problem.** A daily argument with no resolution. The school has no
per-child record a parent can see, and the parent has no proof the school
accepts.

**How Balans solves it.** The child's own account number makes the payment
self-identifying, a numbered receipt is issued the moment money lands, and the
parent has a private link showing the full statement — the school's working,
shown. The argument has nowhere left to start.

### 2.4 Late payment is the market, not the edge case

**The problem.** Over 60% of Nigerian private schools report delayed payment
from at least 40% of parents every term. Roughly a third of parents negotiate
instalments. A school that chases badly loses families; a school that does not
chase cannot pay salaries, which are 60–70% of its operating cost.

**How Balans solves it.** A three-rung reminder ladder the bursar drives in
batches, a promise-to-pay record so a conversation that already happened is not
repeated, and a parent-initiated payment-plan request so a family can propose
terms without having to come to the office and ask. Fee pressure is treated as a
household reality, not a moral failing.

### 2.5 Unmatched money gets guessed at

**The problem.** A transfer arrives from "CHUKWU VENTURES LTD" with a blank
narration. Under pressure, somebody assigns it to whoever complained most
recently. That error compounds quietly for a whole term.

**How Balans solves it.** Money that cannot be placed goes to an **exception
queue** rather than being guessed at. The bursar sees the raw credit exactly as
the bank sent it, ranked suggestions, and whether this payer has been matched
before. Confirming a match teaches the system, so the same company name is a
puzzle once and automatic every term after.

---

## 3. The people

| Role | Who they are | What they need | What they fear |
|---|---|---|---|
| **Proprietor** | Owner, usually the founder. Final authority on money. | How much came in, who owes, is anything leaking. | Staff fraud; looking less in control of their own school. |
| **Bursar** | A one or two person finance office. Interrupted constantly. | To get through the morning without a mismatch. | Being blamed for figures; software that makes them slower. |
| **Parent** | Guardian, relative, employer, or a sponsor abroad. Android, WhatsApp, often low data. | To pay, and to know it was credited. | Paying and not being credited. |
| **Gate staff** | Whoever is at the gate on resumption morning. Shared phone. | One binary answer, fast, one-handed. | Holding up a queue; embarrassing a family in public. |

The proprietor is the buyer. The bursar is the daily user and can veto a
purchase. The parent never buys anything but decides whether the school looks
competent.

---

## 4. System shape

```
  Parent's phone        Bursar's laptop      Proprietor's phone     Gate phone
        │                      │                      │                 │
        │ tokenised link       │ session cookie       │ session         │ session
        └──────────┬───────────┴──────────┬───────────┴────────┬────────┘
                   │                      │                    │
                   ▼                      ▼                    ▼
          ┌──────────────────────────────────────────────────────────┐
          │       Balans SPA — React + Vite, served as static files   │
          │       22 routes: staff shell, parent surface, gate        │
          └───────────────────────────┬──────────────────────────────┘
                                      │  HTTPS · JSON · session cookie
                                      ▼
          ┌──────────────────────────────────────────────────────────┐
          │                  Balans API — Go                          │
          │   auth · matching · ledger · receipts · reminders · audit │
          └──────┬───────────────────────────────────────┬───────────┘
                 │                                       │
                 ▼                                       ▼
   ┌─────────────────────────────┐      ┌────────────────────────────────┐
   │  PostgreSQL                 │      │  iswallet  (iSpend)            │
   │  • append-only ledger       │      │  • virtual account issuance    │
   │  • row-level security       │      │  • credit webhooks             │
   │  • one tenant per school    │      │  • settlement                  │
   └─────────────────────────────┘      └───────────────┬────────────────┘
                                                        │
                                                        ▼
                                              Nigerian banking rails
                                              (any bank, any app, USSD)
```

**The school's money is never Balans' asset.** Funds sit in accounts in the
school's name. Balans records and instructs; it does not custody.

---

## 5. The money flow

This is the central flow. Everything else in the product exists to support it.

### 5.1 A parent pays

```
 PARENT                BANK RAILS           ISWALLET                 BALANS
   │                       │                    │                       │
   │ opens the fee link    │                    │                       │
   │ copies 9038883919     │                    │                       │
   │                       │                    │                       │
   │ transfers ₦185,500 ──▶│                    │                       │
   │ from any bank app     │                    │                       │
   │                       │ credit to the ────▶│                       │
   │                       │ child's VA         │ takes its fee         │
   │                       │                    │                       │
   │                       │                    │ POST /webhooks/       │
   │                       │                    │ iswallet ────────────▶│ 1 verify HMAC
   │                       │                    │                       │   (300s window)
   │                       │                    │                       │ 2 write to inbox
   │                       │                    │◀──── 200 OK ──────────│   STORE FIRST
   │                       │                    │                       │ 3 then act
   │                       │                    │                       │
   │                       │                    │                       │ 4 parse reference
   │                       │                    │                       │ 5 post to ledger
   │                       │                    │                       │ 6 issue receipt
   │                       │                    │                       │
   │◀──────── receipt, and the balance on their link is now current ────│
```

**Store first, then act.** The webhook body is written to an inbox table before
anything is interpreted. If matching crashes, the event is still on disk and can
be replayed. The HTTP status is an instruction to the provider: `200` stop,
`400` never retry this, `5xx` retry — and an event that cannot be attributed
returns `5xx` deliberately, so the provider keeps trying rather than the money
vanishing quietly.

### 5.2 How the payment knows which child it is

Each virtual account carries a structured reference:

```
   balans : {schoolID} : student : {studentID}
     │         │           │          │
     │         │           │          └── whose ledger to credit
     │         │           └───────────── scope (student or family)
     │         └───────────────────────── which tenant — no database read needed
     └─────────────────────────────────── namespace
```

Encoding the school in the reference means an incoming webhook declares its own
tenant before any query runs. The parsed reference is then checked against the
account actually stored for that student; a divergence parks the event rather
than posting it.

### 5.3 The ledger posting — and why gross ≠ net

The provider settles **net**: it keeps its fee before the money arrives. A naive
implementation credits the family what landed, leaving every parent short by the
fee on every payment. Balans derives the gross and posts the difference
explicitly.

```
  Parent sent (gross)        ₦185,500    ← derived: amount_credited + fee_charged
  Provider's fee             ₦  2,000
  Actually landed (net)      ₦183,500

  ┌──────────────────────────────────────────────────────────────────┐
  │  DR   CASH_IN_TRANSIT:iswallet          ₦183,500                 │
  │  DR   PLATFORM_FEES                     ₦  2,000                 │
  │           CR   AR:{enrollment}                       ₦185,500    │
  └──────────────────────────────────────────────────────────────────┘
                                            ──────────────────────────
                            balanced in the database: 185,500 = 185,500

  The family is credited the full ₦185,500 — what they actually sent.
  The ₦2,000 is a visible cost line, not a silent shortfall.
```

The balanced-transaction invariant is enforced by the database, not by
application code. An unbalanced posting cannot be written.

### 5.4 Account codes

```
  AR:{enrollment}            what this child owes this term
  REVENUE:{feeItem}          earned income, per fee line
  PASS_THROUGH:{beneficiary} money collected on behalf of someone else (e.g. WAEC)
  PARENT_CREDIT:{guardian}   overpayment held for the family
  CASH_IN_TRANSIT:{provider} received by the provider, not yet settled to the school
  SETTLED:{bankRef}          landed in the school's own bank account
  PLATFORM_FEES              the provider's cut, stated not buried
```

Pass-through is a deliberate separation: an exam fee collected for an external
body is never counted as the school's revenue.

---

## 6. The matching flow

```
                        a credit arrives
                               │
                               ▼
               ┌───────────────────────────────────┐
               │  Does the reference resolve to a  │
               │  student's virtual account?       │
               └──────────┬─────────────┬──────────┘
                      yes │             │ no
                          ▼             ▼
            ┌──────────────────┐   ┌──────────────────────────────┐
            │     MATCHED      │   │       EXCEPTION QUEUE        │
            │  • allocated     │   │  Nothing is guessed. The     │
            │  • posted        │   │  money is held, visible,     │
            │  • receipted     │   │  and attributed to nobody.   │
            │  • parent's link │   └──────────────┬───────────────┘
            │    updates       │                  │
            └──────────────────┘                  ▼
                                   ┌──────────────────────────────────┐
                                   │  Bursar opens the exception      │
                                   │                                  │
                                   │  • the raw credit as the bank    │
                                   │    sent it — payer, bank,        │
                                   │    account, narration            │
                                   │  • ranked suggestions            │
                                   │  • "this payer" history          │
                                   └───────┬──────────────┬───────────┘
                                           │              │
                                    confirm│              │dismiss
                                           ▼              ▼
                         ┌─────────────────────────┐  ┌──────────────────┐
                         │ posted to that student  │  │ stays open —     │
                         │ receipt issued          │  │ nothing is       │
                         │ ALIAS LEARNED:          │  │ written off or   │
                         │ "CHUKWU VENTURES LTD"   │  │ lost             │
                         │ → Mrs Chukwu            │  └──────────────────┘
                         │                         │
                         │ next term it matches    │
                         │ automatically           │  ◀── undo available
                         └─────────────────────────┘
```

On the seeded demo school, **86% of digital payments match with no human
involvement.** The exception queue is what the remaining 14% is for.

---

## 7. Flows by persona

### 7.1 Parent — no account, no password, ever

```
  A reminder arrives on WhatsApp, carrying a fresh link (72-hour life)
                               │
                               ▼
              ┌─────────────────────────────────┐
              │   /p/{token}                    │
              │   The token IS the credential.  │
              │   No login. No password reset.  │
              │   No account to create.         │
              └──────────────┬──────────────────┘
                             │
         ┌───────────────────┼─────────────────┬──────────────────┐
         │                   │                 │                  │
         ▼                   ▼                 ▼                  ▼
  ┌─────────────┐   ┌────────────────┐  ┌─────────────┐  ┌────────────────┐
  │ Switch      │   │ Full fee       │  │ Pay by      │  │ "I have paid"  │
  │ child       │   │ breakdown      │  │ transfer    │  │                │
  │             │   │                │  │             │  │ Paid some      │
  │ One         │   │ Every line,    │  │ Copy the    │  │ other way —    │
  │ transfer    │   │ every          │  │ child's own │  │ send details   │
  │ often       │   │ discount,      │  │ account no. │  │ and the school │
  │ covers      │   │ every          │  │             │  │ will find it   │
  │ siblings    │   │ amendment      │  │ Any bank,   │  │                │
  │             │   │                │  │ any app,    │  └────────────────┘
  │ Selection   │   │ Pass-through   │  │ USSD        │
  │ is in the   │   │ items labelled │  │             │  ┌────────────────┐
  │ URL, so it  │   │ "collected on  │  │ Lands on    │  │ Request a      │
  │ can be      │   │ behalf of…"    │  │ the right   │  │ payment plan   │
  │ bookmarked  │   │                │  │ child by    │  │                │
  │ or          │   └────────────────┘  │ itself      │  │ Always offered │
  │ re-shared   │                       └──────┬──────┘  │ — a family     │
  └─────────────┘                              │         │ asking is      │
                                               ▼         │ never harmful  │
                                    ┌────────────────────┐└────────────────┘
                                    │ Receipt issued     │
                                    │ Balance updates    │
                                    └────────────────────┘
```

**Why 72 hours is not a problem.** Every reminder mints a fresh token, so an
expired link is never a parent's only route — the next message carries a new
one. An expired link says *"ask the school for a fresh one"*, not *"not found"*,
because expiry is the overwhelmingly likely cause.

### 7.2 Bursar — a resumption-week morning

```
 08:10   /payments
         ┌────────────────────────────────────────────────────────┐
         │  Collected today   ₦172,000                            │
         │  86% matched automatically                             │
         │  [ Matched ]  [ Exceptions · 3 ]                       │
         └────────────────────────────────────────────────────────┘
            │
            ▼
 08:12   Exceptions — the only credits that need a person
         ┌────────────────────────────────────────────────────────┐
         │  ₦40,000 · CHUKWU VENTURES LTD · GTBank · blank        │
         │                                                        │
         │  Suggestions:   Chidi Chukwu (JSS 2)   owes ₦40,000 ◀  │
         │                 Ada Chukwu (Primary 4) owes ₦28,000    │
         │  This payer:    matched to Mrs Chukwu last term        │
         │                                                        │
         │  [ Confirm ]  [ Dismiss ]                              │
         └────────────────────────────────────────────────────────┘
            │ confirm
            ▼  posted · receipt issued · alias learned · undo available
         
 08:25   /debtors
         ┌────────────────────────────────────────────────────────┐
         │  Aging:  0–7d ₦2.4m │ 7–14d ₦1.0m │ 14–30d ₦1.1m      │
         │  Select up to 50 ──▶ send tier-1 reminder              │
         │  Each message carries a fresh 72-hour link             │
         │  Record a promise to pay ──▶ "Salary comes on the 25th"│
         └────────────────────────────────────────────────────────┘
            │
            ▼
 08:40   /post-payment — the parent who came to the desk with cash
         ┌────────────────────────────────────────────────────────┐
         │  Student · amount · channel · note                     │
         │                                                        │
         │  If amount ≥ the school's cash threshold:              │
         │     status = pending_approval                          │
         │     an approval request goes to the proprietor         │
         │     the money is NOT yet on the child's ledger         │
         └────────────────────────────────────────────────────────┘
            │
            ▼  receipt PDF available immediately on success

 Through the day:  /students/{id} to answer "what does this child owe?"
                   /clearance     before any list goes to a teacher
                   /invoices      amendments, with a reason, always audited
```

### 7.3 Proprietor — evenings, on a phone

```
                          /dashboard
   ┌──────────────────────────────────────────────────────────┐
   │  Collected this term      ₦11,936,425    52% of expected │
   │  This week                ₦ 3,724,509                    │
   │  Outstanding              ₦10,824,575                    │
   │  Matched automatically    86%                            │
   │                                                          │
   │  Collected against expected  ▁▂▃▅▆▇  (vs the plan line)   │
   │  How parents paid            transfer · USSD · cash · … │
   │  Who owes the most           top debtors, ranked         │
   │  Last close                  ⚠ 3 items need attention    │
   └───────────┬──────────────────────────────────────────────┘
               │
   ┌───────────┼───────────────┬────────────────────┐
   ▼           ▼               ▼                    ▼
 /approvals  /audit         /debtors            /settings
   │           │               │                    │
   │           │               │                    └─ thresholds, staff,
   │           │               │                       reminder copy
   │           │               └─ who owes, how long
   │           │
   │           └─ every action, every actor, timestamped, exportable as CSV
   │              "Receipt RCT-…-0079 issued for ₦25,000 by Mr Emeka Okonkwo"
   │
   └─ manual postings and discounts waiting on a second person
      ┌──────────────────────────────────────────────────────┐
      │  ₦80,000 cash · Halima Eze                           │
      │  "Manual cash posting of ₦80,000, at or above the     │
      │   school's ₦50,000 approval threshold"                │
      │  Requested by Mr Emeka Okonkwo · 2 hours ago          │
      │                                                       │
      │  [ Approve ]   [ Decline + note ]                     │
      └──────────────────────────────────────────────────────┘
```

### 7.4 Gate staff — resumption morning

```
   A queue of parents and children. One member of staff. One phone,
   held one-handed, in public.

   ┌────────────────────────────────────┐
   │  Greenfield Court · Gate           │
   │  ┌──────────────────────────────┐  │
   │  │ 🔍 Abdul                     │  │   type three letters
   │  └──────────────────────────────┘  │
   │                                    │
   │  Abdullahi Okeke      JSS 1    ▸   │   tap
   │  Abdulrahman Bello    Primary 5▸   │
   └────────────────┬───────────────────┘
                    ▼
   ┌────────────────────────────────────┐
   │       Abdullahi Okeke · JSS 1      │
   │                                    │
   │               ⚠                    │
   │          SEE BURSAR                │        or        ✓  CLEARED
   │                                    │
   └────────────────────────────────────┘
                    │
                    ▼
            Just checked: a short recent list, so the same
            child is not looked up twice in a queue

   ╔══════════════════════════════════════════════════════════════╗
   ║  NO AMOUNTS APPEAR ON THIS SCREEN.                           ║
   ║  Not the balance. Not the percentage. Not how much is left.  ║
   ║  The gate is a public place and the person holding the phone ║
   ║  is often not the bursar. The answer is binary: come in, or  ║
   ║  see the bursar.                                             ║
   ╚══════════════════════════════════════════════════════════════╝

   A gate-check account is allowed exactly this one screen. It has no
   nav, and typing any other URL is refused — not hidden, refused.
```

---

## 8. Lifecycle flows

### 8.1 Onboarding a school

```
  School agrees to use Balans
            │
            ▼
  ┌───────────────────────────────────────────────────────────┐
  │  cmd/onboardschool  -school {uuid} -bvn … -director "…"    │
  │                                                            │
  │  1. Create the school's wallet at iswallet                 │
  │     KYC: BVN, director name, CAC certificate               │
  │                                                            │
  │  2. Batch-issue virtual accounts, 500 students at a time   │
  │     ┌──────────────────────────────────────────────────┐   │
  │     │ Chidi Chukwu      → 9038883919                   │   │
  │     │ Halima Eze        → 9199711905                   │   │
  │     │ … one NUBAN per enrolled student                 │   │
  │     └──────────────────────────────────────────────────┘   │
  │                                                            │
  │  Account name, 31 characters, degrades in this order:      │
  │     GREENFIELD COURT ACADEMY / EBUBE EZE                   │
  │     GREENFIELD COURT / EBUBE EZE                           │
  │     GREENFIELD / EBUBE EZE                                 │
  │     ─ the SCHOOL name shortens; the CHILD's never does ─   │
  │                                                            │
  │  Safe to re-run. -dry-run shows the plan first.            │
  └───────────────────────────────────────────────────────────┘
```

### 8.2 A term, start to finish

```
  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐
  │ 1. FEES      │──▶│ 2. INVOICE   │──▶│ 3. COLLECT   │──▶│ 4. CHASE     │
  │              │   │              │   │              │   │              │
  │ Fee items    │   │ Issue to the │   │ Parents      │   │ Tier 1 → 2   │
  │ per class    │   │ roster       │   │ transfer to  │   │ → 3 ladder   │
  │              │   │              │   │ each child's │   │              │
  │ Tuition      │   │ Each child   │   │ own account  │   │ Promises to  │
  │ PTA levy     │   │ gets an      │   │              │   │ pay recorded │
  │ ICT levy     │   │ AR balance   │   │ Matched      │   │              │
  │ Exam fee     │   │              │   │ automatically│   │ Plans        │
  │ Development  │   │ Amendments   │   │ or queued as │   │ requested    │
  │ Books        │   │ need a       │   │ exceptions   │   │ and decided  │
  │              │   │ reason and   │   │              │   │              │
  │ ₦185,500     │   │ are audited  │   │ Receipt per  │   │              │
  └──────────────┘   └──────────────┘   │ payment      │   └──────┬───────┘
                                        └──────────────┘          │
                                                                  ▼
                                                        ┌──────────────────┐
                                                        │ 5. CLEAR         │
                                                        │                  │
                                                        │ Clearance list   │
                                                        │ per class        │
                                                        │                  │
                                                        │ Derived from the │
                                                        │ ledger — never   │
                                                        │ set by hand      │
                                                        │                  │
                                                        │ Overrides are    │
                                                        │ possible, with a │
                                                        │ reason, audited  │
                                                        │ ("bereavement in │
                                                        │  the family")    │
                                                        └──────────────────┘
                                                                  │
                            every night ◀─────────────────────────┘
                     ┌──────────────────────────────────────┐
                     │  DAILY CLOSE                          │
                     │  Everything that does not reconcile   │
                     │  is flagged, not swept up             │
                     └──────────────────────────────────────┘
```

### 8.3 The reminder ladder

```
   /debtors  ──▶  select students (max 50 per send)  ──▶  choose a rung

   ┌─────────┐      ┌─────────┐      ┌─────────┐
   │ TIER 1  │ ───▶ │ TIER 2  │ ───▶ │ TIER 3  │
   │ gentle  │      │ firmer  │      │ final   │
   └────┬────┘      └────┬────┘      └────┬────┘
        │                │                │
        └────────────────┴────────────────┘
                         │
                         ▼
        Each message mints a FRESH 72-hour parent link
                         │
                         ▼
        ┌────────────────────────────────────────────┐
        │ Parent opens it and can:                   │
        │   • see exactly what is owed               │
        │   • pay to the child's own account         │
        │   • say "I have paid" with details         │
        │   • propose a payment plan                 │
        └────────────────────────────────────────────┘

   The 50-per-send cap is deliberate: it is more than a bursar selects
   on purpose, and far less than a mis-click on "select all" at a
   school of six hundred. These are real phones.
```

### 8.4 Payment plan — parent asks, school decides

```
  PARENT                                      SCHOOL
    │                                            │
    │  /p/{token}/plan-request                   │
    │  "Propose how you'd like to pay"           │
    │     ₦60,000 by 30 Oct                      │
    │     ₦60,000 by 15 Nov                      │
    │     + a short message to the bursar        │
    │                                            │
    ├───────────────────────────────────────────▶│  /plans
    │                                            │  ┌──────────────────────┐
    │                                            │  │ Requested · pending  │
    │                                            │  │ the parent's own     │
    │                                            │  │ words, shown         │
    │                                            │  │                      │
    │                                            │  │ [Approve] [Decline]  │
    │                                            │  └──────────┬───────────┘
    │                                            │             │
    │◀────────── decision, with a note ──────────┼─────────────┘
    │                                            │
    │  The plan is always offered to every family. A school that does
    │  not do plans simply declines. Hiding the option would hide it
    │  from the families who need it most.
```

### 8.5 Approval — the two-person rule

```
   Bursar posts manually              Bursar applies a discount
   (cash at the desk)                 (sibling, hardship, scholarship)
            │                                      │
            ▼                                      ▼
   ┌─────────────────────────┐          ┌─────────────────────────┐
   │ amount ≥ school's cash  │          │ amount ≥ school's       │
   │ approval threshold?     │          │ discount threshold?     │
   └────┬───────────────┬────┘          └────┬───────────────┬────┘
     no │            yes│                 no │            yes│
        ▼               ▼                    ▼               ▼
   ┌─────────┐   ┌──────────────────┐   ┌─────────┐   ┌──────────────────┐
   │ posted  │   │ pending_approval │   │ applied │   │ pending_approval │
   │ receipt │   │                  │   │         │   │                  │
   │ issued  │   │ NOT on the       │   │         │   │ NOT on the       │
   └─────────┘   │ child's ledger   │   └─────────┘   │ child's ledger   │
                 │ yet              │                 │ yet              │
                 └────────┬─────────┘                 └────────┬─────────┘
                          │                                    │
                          └──────────────┬─────────────────────┘
                                         ▼
                            Proprietor · /approvals
                                         │
                       ┌─────────────────┴──────────────────┐
                       ▼                                    ▼
                  ┌─────────┐                         ┌──────────┐
                  │ APPROVE │                         │ DECLINE  │
                  │ posted  │                         │ declined │
                  │ receipt │                         │ + note   │
                  └─────────┘                         └──────────┘

   Thresholds are policy, not preference. The proprietor sets them;
   the bursar cannot.
```

### 8.6 Receipt and verification

```
   Payment posted
         │
         ▼
   ┌──────────────────────────────────────┐
   │  Receipt  RCT-202627TERM1-0079       │
   │  A5 PDF · embedded font so the ₦      │
   │  sign renders on any printer          │
   │                                       │
   │  Greenfield Court Academy             │
   │  Abdullahi Okeke · JSS 1              │
   │  ₦104,000 · 8 Oct 2026                │
   │                                       │
   │        ┌─────────┐                    │
   │        │ ▪▪ ▪ ▪▪ │ ◀── QR to /r/{no}  │
   │        │ ▪  ▪▪ ▪ │                    │
   │        └─────────┘                    │
   └──────────────────────────────────────┘
         │                       │
         ▼                       ▼
  ┌──────────────┐      ┌──────────────────────────────────────┐
  │ BURSAR scans │      │ PARENT scans their own receipt       │
  │ or types the │      │                                      │
  │ number       │      │  "CANNOT CHECK — take it to the      │
  │              │      │   school office"                     │
  │ ✓ VALID      │      │                                      │
  │ ✗ VOIDED     │      │  Deliberate, and §10 explains it:    │
  │              │      │  receipt numbers run in sequence,    │
  │ Catches a    │      │  so a public lookup would let anyone │
  │ forged       │      │  walk a school's whole collection    │
  │ printout     │      │  history one number at a time.       │
  └──────────────┘      └──────────────────────────────────────┘
```

### 8.7 Recovery — when a webhook never arrives

The ledger cannot depend on a delivery that may not happen.

```
                    every scheduled run
                            │
                            ▼
                  ┌──────────────────────┐
                  │  cmd/pollrecovery    │
                  └──────────┬───────────┘
                             │
                             ▼
     ┌───────────────────────────────────────────────────┐
     │ Ask iswallet for ALL credits in the last 48 hours │
     │                                                   │
     │ An OVERLAPPING WINDOW, not a cursor. A cursor     │
     │ that advances past a gap can never come back for  │
     │ it; a window that always re-reads the last two    │
     │ days cannot miss one.                             │
     └───────────────────────┬───────────────────────────┘
                             ▼
     ┌───────────────────────────────────────────────────┐
     │ Compare against the inbox                         │
     │   • present  → nothing to do (idempotent)         │
     │   • missing  → ingest it now                      │
     └───────────────────────┬───────────────────────────┘
                             ▼
              ┌──────────────┴──────────────┐
              ▼              ▼              ▼
        ┌──────────┐  ┌────────────┐  ┌──────────────┐
        │ exit 0   │  │  exit 2    │  │   exit 3     │
        │ all      │  │  gaps      │  │   gaps found │
        │ clear    │  │  found and │  │   and NOT    │
        │          │  │  recovered │  │   recovered  │
        └──────────┘  └────────────┘  └──────┬───────┘
                                              │
                                              ▼
                                      page a human —
                                      money is missing
```

---

## 9. What makes the data trustworthy

These are the controls that answer the ₦457m question, and incidentally make
the ledger usable as evidence by a lender.

```
┌────────────────────────────────────────────────────────────────────────┐
│                                                                        │
│  APPEND-ONLY LEDGER                                                    │
│    Database triggers forbid UPDATE and DELETE on ledger entries.       │
│    A correction is a new, visible, reversing entry. Nothing is         │
│    rewritten — not by a bursar, not by a proprietor, not by us.        │
│                                                                        │
│  BALANCED BY CONSTRUCTION                                              │
│    Debits must equal credits. The invariant lives in the database,     │
│    so an unbalanced transaction cannot be written by any code path.    │
│                                                                        │
│  DERIVED CLEARANCE                                                     │
│    "Cleared" is computed from the ledger every time it is read.        │
│    There is no clearance flag to set, which is precisely the field     │
│    the ₦457m case depended on.                                         │
│                                                                        │
│  TWO-PERSON RULE                                                       │
│    Manual postings and discounts at or above the school's own          │
│    thresholds are held until a second person decides. The money is     │
│    not on the child's ledger while it waits.                           │
│                                                                        │
│  TENANT ISOLATION                                                      │
│    Row-level security, FORCED, on every table. The application's       │
│    database role cannot bypass it. A school cannot read another        │
│    school's rows even if the application asks for them.                │
│                                                                        │
│  COMPLETE AUDIT TRAIL                                                  │
│    Every action, actor and timestamp. Exportable as CSV.               │
│                                                                        │
│  MONEY IN THE SCHOOL'S NAME                                            │
│    Funds sit in accounts belonging to the school. Balans records and   │
│    instructs; it never custodies, and never pools schools together.    │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 10. Current state — what is built, and what is not

**Balans has not run its pilot. There are zero live schools and no collection
history.** Everything in §4–§9 runs today against the real backend and a seeded
demo school; none of it has yet run against a real one.

### Built and working end to end

Virtual-account issuance · webhook ingestion with HMAC verification and replay
protection · store-then-act inbox · tier-0 matching · exception queue with
suggestions, payer history and undo · append-only ledger with net-settlement
handling · receipts with embedded-font PDF and QR · parent surface
(statement, breakdown, claim, plan request) · gate mode · debtors and the
reminder ladder · promises to pay · payment plans · approvals · clearance with
audited overrides · daily close · audit trail with CSV export · poll-based
recovery · three staff roles with server-enforced access.

### Not built yet

| Area | State |
|---|---|
| **Invoice generation** | The screen previews correctly but does not write. No endpoint yet. |
| **Student import** | Wizard is UI-only; no import route. |
| **Fee structure editing** | Read-only. No save path. |
| **Payment reversal** | Deliberately parked — the ledger treatment of a reversal that takes a school negative is an open decision. |
| **Settings writes** | Thresholds and reminder copy display but do not persist. |
| **Automated WhatsApp** | Needs a Business Solution Provider. The sanctioned fallback works: the bursar taps a prefilled `wa.me` link from their own number. |
| **Parent receipt verification** | Needs a receipt-scoped token in the QR; a printed receipt outlives the 72-hour parent token. |
| **Outflows** | Money comes in; schools cannot yet withdraw through Balans. |
| **Tier 1–2 matching** | Only tier-0 (virtual account → student). Everything else goes to the exception queue, which is correct but means more manual work than the design intends. |

### Explicitly not in scope

Payroll · inventory · cafeteria wallets · hostel or transport management ·
multi-campus dashboards · accounting-software integrations · a mobile app.

Balans does fee collection and reconciliation. It is not a school management
system and does not pretend to be one.

---

## 11. Where the partnership fits

The ledger Balans produces as an ordinary by-product — billed, collected,
outstanding, per pupil, per term, in records nobody can rewrite — is exactly the
underwriting artefact Nigerian private schools lack. Most have no income
statement at all, which is why banks cannot lend to them.

With a microfinance partner holding the deposit accounts, two things become
possible that are not possible separately:

```
   Collection rail  ═══╗
                       ╠══▶  the school's revenue arrives, itemised and
   Deposit account  ═══╝     in real time, into accounts the lender holds
                                              │
                                              ▼
                        ┌───────────────────────────────────────────┐
                        │  A facility can be swept from incoming    │
                        │  fees BEFORE the school draws them.       │
                        │                                           │
                        │  Ordinary SME lending depends on a        │
                        │  borrower paying from money the lender    │
                        │  cannot see. Here the lender sees it as   │
                        │  it lands.                                │
                        └───────────────────────────────────────────┘
```

Detail in `BALANS-MFB-PARTNERSHIP-BRIEF.md`.

---

*Market figures quoted here are sourced in `specs/school-financial-os-nigeria-deep-research.md`
(UBEC 2022 NPA; NAPPS 2023). The ₦457m case is referenced as a documented
industry pattern — it is not a Balans customer and was not detected by Balans.*
