# Schools: specification and build plan

Status: **Accepted** 2026-10-23; **B0 to B7 built** · Decision: [0046](../decisions/0046-balans-in-taskiem.md) · Workstream **B0 to B9**, one pull request per milestone · Source: [Balans overview](../strategy/balans-overview.md)

What a private school needed from Balans, built in Taskiem as an area any business can turn on: every student with their own account number, so a parent's transfer lands on the right child by itself; a term's fees as invoices; money nobody can place held for the bursar with suggestions; receipts; a parent page with no login; chasing that respects how families pay; a second person for cash and discounts; clearance nobody can set by hand; a gate screen with no amounts; and the proprietor's numbers on a phone in the evening.

Contents:

1. [Summary](#1-summary)
2. [Words used here](#2-words-used-here)
3. [Where we are](#3-where-we-are)
4. [The Schools area and its record](#4-the-schools-area-and-its-record)
5. [Fees and term invoices](#5-fees-and-term-invoices)
6. [Students' account numbers](#6-students-account-numbers)
7. [Matching school money](#7-matching-school-money)
8. [Cash at the desk, discounts and the second person](#8-cash-at-the-desk-discounts-and-the-second-person)
9. [Receipts](#9-receipts)
10. [The family page](#10-the-family-page)
11. [Chasing: reminders, promises and payment plans](#11-chasing-reminders-promises-and-payment-plans)
12. [Clearance and the gate](#12-clearance-and-the-gate)
13. [The proprietor: overview, daily close, summaries](#13-the-proprietor-overview-daily-close-summaries)
14. [Templates and the AI builder](#14-templates-and-the-ai-builder)
15. [Milestones](#15-milestones)
16. [Order and timing](#16-order-and-timing)
17. [Testing](#17-testing)
18. [Risks and open questions](#18-risks-and-open-questions)
19. [Out of scope](#19-out-of-scope)

---

## 1. Summary

| Balans feature | In Taskiem | Kind |
| --- | --- | --- |
| A bank account number per student | `virtual_account` from any connected rail, issued once per student (§6) | Capability, platform |
| A payment lands on the right child by itself | The permanent-account rule in matching (§7.1) | Platform (general) |
| Exception queue with suggestions, payer history, undo | To match, with payer memory (§7.3) | Platform (general) |
| One transfer covering siblings | Split payments across invoices (§7.2) | Platform (general) |
| Store first, then act; 48-hour recovery | Ingest and the money log (built); an overlapping look-back for credits (§6.5) | Platform |
| Gross versus net | Count what the parent sent (§7.4) | Platform |
| Students, classes, terms, fee items | The schools record (§4, §5) | Platform |
| Invoicing a term | One invoice per student and term, from the fee structure (§5.3) | Platform, template |
| Pass-through fees (exam bodies) | Pass-through lines (§5.2) | Platform |
| Append-only ledger, balanced in the database | **Dropped**: invoices, matches and the money log (0046, point 3) | — |
| Receipts with a number and QR | Receipt records, PDF, a verification page (§9) | Platform, template |
| Parent surface with no login | The family page on the public host (§10) | Platform |
| Reminder ladder, promises, payment plans | §11 | Platform, template |
| Two-person rule for cash and discounts | Approval policies with thresholds (§8) | Platform (built), wiring |
| Derived clearance, audited overrides | §12 | Platform |
| Gate screen, no amounts | A **gate** role with one screen (§12.3) | Platform, web |
| Dashboard, debtors, aging | Schools > Overview and Debtors (§13) | Web |
| Daily close | A nightly check and report (§13.2) | Platform, template |
| Audit trail with CSV | Built | Built |
| Row-level security per school | Built (one tenant per business) | Built |
| Onboarding command with KYC | The rail's own onboarding; Taskiem connects the rail (§6.1) | Rail |
| Partnership with a lender (§11 of the overview) | Out of scope (§19) | — |

## 2. Words used here

| Word | Meaning |
| --- | --- |
| **School** | A Taskiem business with the Schools area turned on |
| **Proprietor** | The owner; the `owner` role |
| **Bursar** | Whoever runs the fees office; a role with `school.manage` |
| **Family** | The guardians of one or more students, who pay together and share a family page |
| **Session, term** | The school year (2026/2027) and its parts (First, Second, Third term) |
| **Fee structure** | The fee items for a class and term, each with an amount |
| **Term invoice** | The invoice of one student for one term, built from the fee structure |
| **Student account** | The permanent account number a rail issued to one student |
| **Pass-through line** | A fee line collected on behalf of someone else, not the school's income |
| **Cleared** | A student whose term's fees are settled, by the rule in §12 |
| **Family credit** | Money from a family not yet counted towards any invoice (an overpayment) |

## 3. Where we are

| Piece | State | Where |
| --- | --- | --- |
| Invoices with lines, paid only by matched payments; the invoice page | Built (T3, T4) | [invoices](../invoices.md) |
| `virtual_account` capability and `virtual_account.credited` | Built (R1); mapped for Moniepoint only | `schemas/capabilities-v1.json` |
| The money log, kept tables, the poller | Built (T2) | [money log](../money-log.md) |
| Matching: exact, suggestions with reasons, automatic, confirm, reject, by hand, undo; orders | Built (T5, follow-ups) | [matching](../matching.md) |
| The permanent-account rule, split payments, payer memory | **Missing** (open items) | This plan, B1 |
| The `invoice` trigger on paid; receipts as messages | Built (follow-ups) | [invoices](../invoices.md) |
| Leaks, summaries, exports with a balanced journal | Built (T6, T7) | [leaks](../leaks.md), [money reports](../money-reports.md) |
| Approval policies, four-eyes, step-up, custom roles | Built (P2 M3, M5) | [governance](../governance.md) |
| Messages to parents on WhatsApp and Telegram after one tap, SMS | Built (0039) | [messages](../messages.md) |
| Forms, CSV upload into tables | Built | [forms](../forms.md) |
| iswallet connector: wallets, payouts, balances | Built; **no account issuing** | `connectors/iswallet` |
| Students, classes, terms, fees, clearance, the family page, the gate | **Missing** | This plan |

## 4. The Schools area and its record

### 4.1 Turning it on

An owner turns on **Schools** in Settings. The area appears in the sidebar for people with `school.read`. Turning it off hides it and stops the school's scheduled work; nothing is deleted.

### 4.2 Permissions and roles

| Permission | Lets a person | Built-in roles that have it |
| --- | --- | --- |
| `school.read` | See students, fees, payments, debtors, clearance | owner, admin, operator, auditor, viewer |
| `school.manage` | Add and change students, families, classes, fees; invoice a term; send reminders; record promises; match money; post cash (within thresholds) | owner, admin, operator |
| `school.approve` | Approve or decline cash postings and discounts at or above the thresholds, decide payment plans, override clearance | owner (and roles given it) |
| `school.gate` | The gate screen only (§12.3) | gate |

A **Bursar** role template (custom role with `school.read`, `school.manage`, `invoice.read`, `invoice.manage`) is offered when the area is turned on. Thresholds and who may approve are the owner's: changing them needs `school.approve` and the owner role, through the existing policy pages.

### 4.3 The record

| Record | Holds | Rules |
| --- | --- | --- |
| **Class** | Name (JSS 1, Primary 4), arm (A, B), order | Unique name and arm per school |
| **Session and term** | 2026/2027; First term with start, end and the date fees are due | One current term at a time; past terms read-only |
| **Student** | Name, admission number, class, status (active, withdrawn, graduated), date of birth (optional) | Admission number unique per school; personal data sealed like customers' (`x-pii`) |
| **Family** | Guardians (name, phone, email, relationship), their students | A student belongs to one family, and a guardian to one family (decided 2026-10-23) |
| **Enrollment** | A student in a class for a term | Created when a term starts from the student's class; changes are audited |
| **Student account** | The student's account number, bank, account name, rail connection and reference (§6) | One active account per student and connection |

Changes go through the API (and `schools@1` for workflows); data tables get read-only kept copies (`students`, `term_fees`) so templates and the AI builder can read them, as the money log does.

### 4.4 Getting students in

- **Import**: a CSV of students and guardians (name, admission number, class, guardian name, phone, email), checked line by line: a file with any bad line adds nothing and lists each line and why (the statement upload's rule). Re-uploading updates by admission number and never duplicates. Families are grouped by guardian phone, shown for confirmation before saving.
- **One by one** in the web app.
- **A form** the school shares with parents to fill in their own details (the existing forms), reviewed by the bursar before it becomes a student.
- **Promotion** at the end of a session: move every class up in one step, with exceptions (repeaters, leavers) chosen first.

## 5. Fees and term invoices

### 5.1 Fee structures

A fee structure is the list of fee items for a class in a term: tuition, PTA levy, ICT levy, exam fee, development levy, books. Each item has an amount, a kind (**required** or **optional**, such as transport or lessons a family chooses), and for pass-through items the body it is collected for. A structure can be copied from the last term or another class and changed. Changes after a term is invoiced apply to invoices not yet sent; sent invoices change only by amendment (§5.4).

### 5.2 Pass-through lines

A pass-through line (WAEC, NECO, an external exam) is shown on the family page as "collected on behalf of WAEC". In the Overview, summaries and exports it is money collected for someone else, never the school's income: the journal posts it to `Collected for <body>`, not `Sales`.

### 5.3 Invoicing a term

**Invoice the term** (web, or `schools@1` `invoice_term` in a workflow) makes one invoice per enrolled student from their class's fee structure, with the student's optional items, sibling and other standing discounts, and the term's due date. It is **exactly once per student and term** (keyed `term:<id>:student:<id>`), so running it again after adding students invoices only the new ones. It shows the plan first (how many students, the total, students with no guardian contact) and sends nothing until confirmed. Invoices are numbered in the business's invoice series, carry the student and term, and are sent by the template of §11 or held as drafts, as the school chooses.

An invoice's customer is the family; its subject is the student. Everything T3 to T7 built (the invoice page, matching, leaks, summaries) works on them unchanged.

### 5.4 Discounts and amendments

A discount (sibling, staff child, scholarship, hardship) or an amendment (an item added or removed) changes a sent invoice only as a recorded amendment with a reason, shown on the family page as its own line. One at or above the school's **discount threshold** waits for a second person (§8). Amendments are audited and appear in the export.

## 6. Students' account numbers

### 6.1 Which rails

Any connected prod rail that maps `virtual_account` can issue students' accounts. B0 adds mappings from each rail's public documentation:

| Rail | Its product | To confirm in B0 |
| --- | --- | --- |
| iswallet | Virtual accounts on a wallet | Issuing, naming, events, lookups for credits, fees, net or gross |
| Paystack | Dedicated virtual accounts | Needs a customer; may need Paystack to enable it; events; naming |
| Flutterwave | Static virtual accounts | BVN requirement for permanent accounts; events; naming |
| Moniepoint | Reserved accounts | Already mapped (R1); naming |

Each rail's own onboarding and KYC (the school's BVN, director, CAC) is done with the rail; Taskiem connects the rail with the school's keys on the Connections page, as for any business. The school picks which connection issues students' accounts in the Schools settings.

### 6.2 Issuing

Accounts are issued by `schools@1` `issue_account` (one student) and **Issue account numbers** in the web app (every student without one, in batches of up to 500), and by the template that issues an account to each new student. Issuing is exactly once per student and connection (keyed `student:<id>:acct:<connection>`): a retried, repeated or interrupted batch picks up where it stopped and never issues two. A dry run shows the plan first. Failures are listed per student with the rail's reason.

### 6.3 Naming

The account name a parent sees when they transfer is the school's name and the child's: `GREENFIELD COURT ACADEMY / EBUBE EZE`. Where the rail limits the length (often 31 characters) **the school's name shortens and the child's never does**: `GREENFIELD COURT / EBUBE EZE`, then `GREENFIELD / EBUBE EZE`. Rails that do not let the business set the name show their own; B0 records which.

### 6.4 The reference

Taskiem derives each account's reference from the business and the student (`<tenant>:student:<id>`, in the rail's allowed characters) and stores it with the account. A credit is matched only when its account number and reference both agree with what Taskiem stored for that student; a credit that names a student's reference but another account, or an account Taskiem does not know, is held in To match with the reason, never posted.

### 6.5 Credits that never arrive as webhooks

For rails with a credit lookup, the poller reads back credits over an **overlapping window** (the last 48 hours, every 15 minutes) for each connection that issued students' accounts; the money log's key makes reading the same credit again a no-op. A credit found only by the look-back is recorded and matched like any other, and the owner is told when the look-back found money the webhook missed (a sign the webhook is not set up). A look-back that fails repeatedly raises an alert.

## 7. Matching school money

Everything here is built in matching for every business; schools are its first users.

### 7.1 The permanent-account rule

A credit to a customer's permanent account (a student's account, or any customer's `virtual_account`) is matched **exactly**, without asking anyone, to that customer's oldest open invoice, then the next; for a school, the student's term invoices, oldest term first. What is left after the student's own invoices is offered to their siblings' open invoices oldest first if the school turns that on (off by default), otherwise held as **family credit**, shown on the family page and counted towards the next invoice when it is sent.

### 7.2 Split payments

One payment can count towards several invoices (siblings, two terms), each part a match of its own that can be undone on its own. Confirming a suggestion for a family offers the split oldest first, which the bursar can change.

### 7.3 Payer memory

When a person confirms or matches money from a payer to a family (or any customer), Taskiem remembers the payer: the payer's name as the rail sent it and, where given, the paying account. The next payment from that payer suggests that family first with the reason "this payer paid for this family before", and is matched automatically when the business has automatic matching on. A remembered payer can be forgotten in the web app. Payers are personal data: kept sealed, shown only to people who may see the family.

### 7.4 What the parent sent

Where a rail reports the amount that landed and its fee (it settled net), the amount counted towards invoices is what landed plus the fee: the family is credited what they sent, and the fee is a cost to the school shown in Fees and the journal. B0 records, per rail, whether credits are reported gross or net.

### 7.5 What the bursar sees

To match shows the credit as the rail sent it (payer, bank, account, narration), the suggestions with their reasons, the payer's history, and **Match**, **Split**, **Not this school's** (refund outside Taskiem) and **Leave**. Nothing is guessed: money with no confirmed match stays unmatched and visible in the Overview.

## 8. Cash at the desk, discounts and the second person

- **Cash or a transfer to the school's own account** is posted by the bursar with the student, amount, how it was paid and a note. It becomes a money log entry (`cash` or `statement`) and a match to the student's invoices.
- **At or above the school's cash threshold** the posting is **pending**: it is not on the child's fees, and an approval request goes to people with `school.approve` (in the web app and in chat, through the existing approvals). Approve posts it; decline records the reason and nothing reaches the fees.
- **Discounts at or above the discount threshold** wait the same way (§5.4).
- Thresholds are set by the owner, per school, in naira; a threshold of 0 means every posting needs a second person.
- The person who asked can never approve their own request (four-eyes, built).

## 9. Receipts

- **A receipt for every payment counted** towards a school invoice (by matching, by an approved cash posting, by a split part), numbered per business and term (`RCT-2026T1-0079`), issued exactly once per match and voided (never deleted) when the match is undone.
- **A PDF** (A5, an embedded font so ₦ prints anywhere): school, student, class, amount, date, what it paid, the invoice's balance after it, and a QR code.
- **The QR** carries a receipt token of its own (unguessable, not the receipt number). Anyone who scans it sees **Valid** or **Voided**, the school, the amount and the date, and nothing else; the number alone shows nothing, so the school's sequence cannot be walked.
- Staff can look a receipt up by number in the web app.
- **Sending** is the template of §14: the receipt link and PDF on WhatsApp, Telegram or SMS to the family, at once, on the `invoice` trigger's `payment` event (B5 adds the event: a payment counted, not only paid in full).

## 10. The family page

A family reaches its page from a link in any message from the school; there is no account, password or sign-up.

- **One page per family** at `/s/<token>` on the public host: each child (switchable, the choice in the address so it can be shared), this term's fees line by line with discounts and amendments, pass-through lines labelled, what has been paid and each receipt, the balance, family credit, and each child's account number with a copy button.
- **I have paid**: for money paid some other way, the parent gives the amount, date, how and any reference; it appears in To match as a claim beside the unmatched money, never as a payment.
- **Ask for a payment plan**: instalments with dates and a short message (§11.3).
- **Links**: each message mints a new link that works for a set time (72 hours by default, a school setting from 1 to 30 days). An expired link says "ask the school for a new link" and offers to send one to the guardian's number on file, never "not found".
- Pages run in the school's theme ([0042](../decisions/0042-public-page-themes.md)), work on a slow connection without script, and pass accessibility checks.

## 11. Chasing: reminders, promises and payment plans

### 11.1 Debtors and the ladder

**Debtors** lists students with a balance, by class, with aging (0 to 7 days past due, 7 to 14, 14 to 30, over 30), the last reminder and any promise. The bursar selects students (**at most 50 per send**, so "select all" at a school of 600 cannot message everyone by mistake) and a rung:

| Rung | Tone | Default wording (the school's to change) |
| --- | --- | --- |
| 1 | Gentle | A reminder of the balance and the account number |
| 2 | Firmer | The balance and the due date passed |
| 3 | Final | The balance and what happens next (the school's words) |

Each message carries a new family page link and goes to the guardians on WhatsApp or Telegram where they have tapped in, otherwise by SMS. Reminders can also be sent on a schedule by a template (§14), with the same cap per run.

### 11.2 Promises to pay

The bursar records a promise ("salary comes on the 25th": a date and a note). Reminders skip the family until the date; the day after, if the balance has not moved, the family is back on Debtors with "promise missed".

### 11.3 Payment plans

A parent asks for a plan on the family page; the bursar sees it with the parent's own words and approves (optionally changing dates or amounts) or declines with a note; the parent is told either way. The option is always shown: a school that does not do plans declines. An approved plan sets the instalment dates the reminders and clearance follow (§12.1). A missed instalment puts the family back on Debtors.

## 12. Clearance and the gate

### 12.1 The rule

A student is **cleared** for a term when, for that term's invoices:

- the balance is zero; or
- an approved payment plan exists and no instalment is past due; or
- an override is in force.

A term with some money paid but a balance left is **partly paid**, never cleared: the clearance list and the family page say partly paid, and the gate says **See the bursar** (it shows no amounts). Each student is therefore **cleared**, **partly paid** or **not paid** for a term.

Clearance is computed every time it is read. There is no clearance field.

### 12.2 Overrides

A person with `school.approve` can clear a student who does not meet the rule, with a reason ("bereavement in the family") and an end date (default the end of the term). Overrides are audited, listed on the clearance page, and can be withdrawn.

### 12.3 The gate

A person with the **gate** role sees one screen: a name search (three letters) that answers **Cleared** or **See the bursar** for a student, with a short list of recent checks. **No amount ever appears**: not the balance, not a percentage. Every other page and API route refuses a gate session (403), not just hides it. The screen works one-handed on a shared phone, in the school's theme, without a fast connection.

### 12.4 Clearance lists

Per class, for a teacher or an exam: who is cleared and who is not, with no amounts unless the reader has `school.read`. Exportable as CSV.

## 13. The proprietor: overview, daily close, summaries

### 13.1 Schools > Overview

For the current term (or any term): collected against expected (with the line of what was expected by now), this week, outstanding, matched automatically (percent), how parents paid (account number, card, cash), top debtors, aging, pass-through collected apart, and the last close. On a phone first.

### 13.2 The daily close

Every night, for each school: money in on students' accounts that day; what matched automatically, by a person, and what is still unmatched; cash postings and discounts waiting for approval; claims not yet found; credits found only by the look-back; and anything that does not reconcile (a student account credit with no log entry, a receipt with no match). Nothing is swept up: each item is flagged with where to fix it. The close is a platform check (exactly once per school and day) and a report the template of §14 sends to the proprietor.

### 13.3 Summaries and exports

The T7 daily chat summary and monthly email gain a schools section when the area is on (collected for fees, outstanding, cleared percentage). The exports gain the students' columns (student, class, term) and post pass-through lines apart.

## 14. Templates and the AI builder

| Template | Trigger | Does |
| --- | --- | --- |
| `school-issue-accounts` | A student is added; or by hand for all | Issues the student's account number and sends it to the family |
| `school-invoice-term` | Scheduled at the term's start; or by hand | Invoices the term, then sends each family its invoice and family page link |
| `school-fee-reminders` | Weekly, on chosen days | Rung by days past due, at most 50 per run, skipping promises and plans in good standing |
| `school-receipt` | `invoice` trigger, `payment` event | Sends the receipt link and PDF at once |
| `school-plan-decided` | A plan approved or declined | Tells the family, with the instalments |
| `school-daily-close` | Every night | Sends the proprietor the close (§13.2) |
| `school-gate-list` | The day before resumption | Sends class teachers their clearance list |

The AI builder learns the schools record and `schools@1` (a request about school fees uses them, never a data table of students with a "paid" column), with eval cases.

## 15. Milestones

### B0: rail facts and account mappings (M)

From each rail's public documentation, for iswallet, Paystack and Flutterwave (and Moniepoint's naming): issuing permanent accounts, naming and its limits, the credit event and its fields (gross or net, payer name and account), lookups of credits for a window, fees and limits per account, KYC each needs. Map `virtual_account`, `virtual_account.credited` and a credit lookup for each that documents them, with conformance fixtures; questions a rail does not answer are listed for the founder, as in 0045's T0.

**Built** (B0): the facts and the founder's questions are in [rail facts for schools](schools-rail-facts.md). `virtual_account` takes an `account_name` and an `account` (the wallet), and the customer a `nin`; `virtual_account.credited` carries a shared credit shape (`credit_id`, `amount_kobo` as what the payer sent, optional `settled_kobo`, `fee_kobo`, `account_number`, payer name, account and bank, narration, when); a new read capability `list_credits` (since, until, account) serves the look-back. Mapped for iswallet (issuing in batches, `list_credits`), Paystack (dedicated accounts, the credit as `charge.success` on `dedicated_nuban`, `list_credits`) and Flutterwave (static accounts, the credit as `charge.completed` on a `tva_` reference, `list_credits`); Moniepoint gains `account_name` and `credit_id`. Differences from §6:

- **Every credit is its own entry**: the money log's key includes `credit_id`, which fixed repeat payments into one permanent account (including Moniepoint's) being taken as one.
- **iswallet's credit webhook is not mapped**: its documentation does not say which fields name the account, so iswallet credits reach the log through the look-back (B4) until iswallet answers.
- **Paystack names accounts itself** ("Business name/Customer name") and needs an email per customer; Flutterwave needs a BVN or NIN per account and documents no name; §6.3's naming holds only where a rail lets the business set it.

### B1: matching for customers' accounts (M)

The permanent-account rule (§7.1), split payments (§7.2), payer memory (§7.3) and gross counting (§7.4), for every business, with the web app and chat. Closes the open items for the permanent-account rule and split payments.

**Built** (B1): migration 00210 (`customer_accounts`; invoices' `customer_key` and the derived `customer_id`; `money_entry_customers` for a customer's money and credit; `money_payers`; money entries' `account_number` and `net`; a database check, under the entry's lock, that an entry's parts never exceed what it counts for); `invoicing.Invoices.RegisterAccount` and `POST /v1/customer-accounts`; the rule, credit counted when an invoice is sent, split confirm and match, payer memory and `DELETE /v1/money/payers/{id}`, `GET /v1/invoices/{id}/customer` ([matching](../matching.md)). Differences from §7:

- **A customer is the invoice's `customer.key` where given** (`student:<id>` for a school), otherwise the pseudonym from their phone or email, so siblings billed to one parent's phone stay apart.
- **A rail that does not say which account was paid into** (Flutterwave, Moniepoint) is matched at the reference it issued; a credit is held only when its account number is given and differs, or its reference names another account.
- **Leftover credit goes to the next invoice sent to the same customer**; offering it to siblings is the Schools setting (B4).
- **Payers are remembered from a person's confirm or match only**, not from automatic matches, with weights in [matching](../matching.md) to be tuned on real data.
- **The account number of a customer's account is stored as the business's own collection account** (in the clear, as one-time accounts' are), so it can be compared.

### B2: the Schools area and its record (M)

Turning the area on; permissions and the gate role (the role only; its screen is B8); classes, sessions and terms, students, families, enrollments; import, one by one, promotion; the Schools pages for students and families; `schools@1` read actions; kept copies for templates.

**Built** (B2): migration 00211 (classes, sessions and terms, families and guardians, students, enrollments, promotions, the area's setting; past terms read-only by trigger); `engine/schools`; `/v1/schools/…` and `/v1/settings/schools`; `schools@1` with `list_students`, `get_student`, `list_classes`, `current_term`; kept copies `school_students` and `school_classes`; the Schools pages; the permissions, the **gate** role and the Bursar role; the role table moved to `engine/access` as the one source ([schools](../schools.md)). Differences from §4:

- **The kept copies are named `school_students` and `school_classes`**: kept names are reserved in every business, so plain `classes` would stop any business making a table of that name.
- **A gate session is refused everywhere but `/v1/me` and signing out** until B8 adds its screen; only owners can give the gate role, since only owners hold `school.gate` besides it.
- **A guardian needs a phone**: it is what makes a guardian belong to one family. A student with two guardians is two lines in an import.
- **Unlike the money log, the copies are updated in place** (a student changes class); the kept table's page still says corrections are new rows, which the guide explains.
- **Not yet**: the parent self-registration form (a later template) and the AI builder's knowledge of the record (B9).

### B3: fees and term invoices (M)

Fee structures with required, optional and pass-through items; invoicing a term exactly once per student; discounts and amendments with reasons; `schools@1` `invoice_term`; pass-through in summaries and exports.

**Built** (B3): migration 00212 (fee items per class and term; students' optional items and standing discounts; `school_term_invoices` linking each invoice to its student and term; `customer_invoice_amendments`; the discount threshold); invoicing a term with a plan and a confirm by its hash, exactly once per student and term; `schools@1` `invoice_term`, `list_fee_items`, `term_invoices`; amendments for every business's sent invoices (`POST /v1/invoices/{id}/amendments`: add, remove or discount, with a reason); pass-through in the invoice page, the summary, the exports and the journal; the kept copy `school_term_fees`; the Fees and term Invoices pages ([schools](../schools.md)). Differences from §5:

- **An invoice goes to the family's first guardian**, with `customer.key = student:<id>` so a student's own account pays their own invoices (B1).
- **A sent invoice's lines only grow**: each amendment is a new line naming its record; the total never goes below what was paid, a paid invoice whose total rises goes back to part-paid, and discounts never come off pass-through lines.
- **A payment pays the school's own lines first and pass-through lines last**, in whole kobo; the journal credits the pass-through share to `Collected for <body>`.
- **Discounts at or above the threshold are refused** (409 `needs_approval`) until B5 turns the refusal into an approval request; a term invoice for such a student is held in the plan as needing approval.
- **Only the current term can be invoiced**, since enrollments are made when a term starts.
- **Not yet**: schools in the daily and monthly summaries (B9); editing a school draft in the general invoice editor is not supported (drafts change through fees, options and discounts).

### B4: students' account numbers (M)

The school's issuing connection; issuing one and in batches, resumable, with a dry run; naming; references; exact matching of credits to students through B1's rule; held credits with reasons; the overlapping look-back and its alert; `school-issue-accounts`.

**Built** (B4): migration 00213 (the school's issuing connection and wallet; `school_student_accounts` tracking each student's issuing; batch jobs; `money_lookbacks`; `money_reversals`; money entries' `credit_id`); a student's account is a customer account (B1) for `key:student:<id>`; issuing one (`POST /v1/schools/students/{id}/account`, `schools@1` `issue_account`) and in batches (`POST /v1/schools/accounts/issue`, a plan confirmed by its hash, jobs worked every 2 seconds); naming; `engine/lookback`; siblings sharing credit; reversals for every business; the Account numbers page and each student's number ([schools](../schools.md), [matching](../matching.md)). Differences from §6 and §7:

- **Issuing is one capability call per student**, at most 500 per batch, exactly once by the key `student:<id>:acct:<connection>`. A call cut short is asked again on rails safe to repeat (iswallet, Moniepoint) and marked **unknown** for a person on the others (Paystack, Flutterwave).
- **Some students cannot be issued in a batch**: no guardian email on a rail that needs one; on Paystack a sibling already using the same email (one account per email); Flutterwave students, whose parent's BVN or NIN is entered on the student's page and never stored.
- **Names are capped at 31 characters on every rail**, since no rail documents its limit; Paystack names accounts itself and gets none.
- **References**: `tsk_` plus the key on iswallet and Moniepoint, `tva_` plus the key on Flutterwave (its tx_ref), the customer code on Paystack.
- **The look-back** reads 48 hours every 15 minutes per issuing connection that maps `list_credits`, re-reads a truncated window in quarters, tells the owners once a day when it finds a credit over 30 minutes old that the webhook should have brought, and alerts after three failures in a row.
- **Reversals** (decided 2026-10-23): money returned, reversed or refunded undoes its invoice matches and reopens the invoices; what is left of a partial reversal is matched again; owners get an alert and people with `invoice.manage` a notification. Receipts are voided through a hook B5 fills; money matched to storefront orders is left to the order's own refund.
- **Not yet**: the `school-issue-accounts` template (B9); two partial refunds of one payment are still one entry (the money log keys a refund by the payment it refunds).

### B5: cash, approvals and receipts (M)

Cash postings, thresholds and approvals for cash and discounts; receipts as records, the PDF, the QR verification page; the `invoice` trigger's `payment` event; `school-receipt`.

**Built** (B5): migration 00214 (`school_pending_postings` for cash postings, standing discounts, discount amendments and held term invoices, with their decisions; `school_receipts` and `school_receipt_counters`; the cash threshold; the `school_approval` outbox kind; `invoice_payment_starts`); `engine/schools` cash postings counted through the one payment path, approvals (the requester never decides), receipts issued once per payment part and voided on undo or reversal; `engine/pdf`, a small PDF writer with an embedded font so ₦ prints; the public check at `/r/<token>`; the `invoice` trigger's `payment` event; approvals in chat; the Payments, Receipts and Approvals pages ([schools](../schools.md), [invoices](../invoices.md)). Differences from §8 and §9:

- **B3's refusals became requests**: a discount or posting at or above a threshold answers 202 with the recorded request, and approving a held term invoice creates it under the same once-only key.
- **The cash threshold is unset by default** (no second person); 0 means every posting waits for one. Four-eyes is strict, with no exception for a one-person business.
- **Receipt numbers** are `RCT-<session's first year>T<term's ordinal>-<sequence>`; the PDF shows the student's name to anyone with `school.read`, audited per render, while lists stay masked.
- **The font is a subset of Liberation Sans** renamed "Taskiem Receipt Sans" under the OFL, two small files committed with their licence: the one exception to the rule against binaries in the repository, accepted by the founder on 2026-10-24.
- **`/r/<token>` shares its path with form links**: a receipt token is tried first; a guess gets the form link's not-found.
- **Not yet**: the `school-receipt` template (B9); requests older than 7 days are no longer pushed to approvers' phones, though still decidable in the web app.

### B6: the family page (M)

The family page with every child, fees, payments, receipts, account numbers; links minted per message with the school's expiry; I have paid claims into To match; payment plan requests.

**Built** (B6): migration 00216 (`school_family_links`, `school_family_inbox`, `school_payment_claims`, `school_plan_requests`, `school_family_messages`, the link lifetime setting); the family page at `/s/<token>` on the form host in the three themes, without script; links minted per message, an expired link offering **Send me a new link**; I have paid claims shown in To match and resolved when a payment is counted or by the bursar; plan requests recorded and decided (their effect on reminders and clearance is B7 and B8); `schools.SendFamily`, `schools@1` `family_link`; the staff pages ([schools](../schools.md), [public host](../public-host.md)). Differences from §10 and §11.3:

- **What the page shows is sealed under the link itself**: the family's names, account names and receipt links are encrypted with a key derived from the token, so the public role and the database alone show nothing personal; only the link's holder reads them. Receipts issued after a link was minted show without their check link until the next message.
- **The page's forms go through an inbox** the api role drains every 2 seconds, rate-limited per family in the database (claims 5 an hour, plans 3 an hour and one undecided per student, new links 3 an hour); the public host never writes the record.
- **A claim is matched to a counted payment of the same amount, else the oldest open claim**; there is no hand matching of claims to entries.
- **School invoices sent by SMS and email now carry the family page link** instead of the invoice's own; the WhatsApp and Telegram invoice message keeps its Pay buttons and the invoice link.
- **Unknown and expired links read the same** ("ask the school for a new link"); only a real expired link offers to send one.

### B7: chasing (S)

Debtors with aging; the three rungs with the school's wording; at most 50 per send; promises; plans decided and followed; `school-fee-reminders`, `school-plan-decided`.

**Built** (B7): migration 00217 (`school_reminder_sends`, `school_reminders`, `school_promises`, `school_plan_arrears`, the reminder wording and plan grace settings); `engine/schools` debtors with aging, the ladder, promises and plan standing; `GET /v1/schools/debtors` (and CSV), `POST /v1/schools/reminders`, promises on a student, `POST /v1/schools/chasing/check`; `schools@1` `send_reminders`, `plan_decisions`, `tell_plan_decision`; the templates `school-fee-reminders` and `school-plan-decided`; the Debtors page ([schools](../schools.md), [templates](../templates.md)). Differences from §11:

- **One message per guardian names every selected child of the family** with their balances summed; a family is sent a rung at most once a day. Plans in arrears are reminded at the rung of their earliest missed instalment.
- **A send is one request**: at most 50 students, with an `Idempotency-Key`; a retry replays the stored result and sends nothing.
- **Promises and plans are checked hourly**, not nightly, each decision exactly once; a promise made on its due date is decided the day after.
- **A plan's standing** counts payments since the plan was approved against the instalments due so far, with a grace in days (default 0); `PlanStanding` is exposed for B8's clearance rule and clearance itself is unchanged.
- **`school-plan-decided` runs every 15 minutes** over decisions not yet told, a safety net behind the immediate message B6 sends, sharing its once-only key; no new trigger type was added.
- **Template keywords** avoid "debtors", "overdue" and "chase" so the existing templates keep their place; `school-fee-reminders` needs "school" or "fees" in the goal.

### B8: clearance and the gate (S)

The clearance rule, overrides, clearance lists; the gate screen and the server refusing everything else for a gate session; `school-gate-list`.

### B9: the proprietor, templates and AI (M)

Schools > Overview; the daily close and `school-daily-close`; schools in the daily and monthly summaries; the Start page entry for schools; the AI builder's knowledge of the schools record, eval cases; publishing the guides.

## 16. Order and timing

| Order | Milestone | Why here |
| --- | --- | --- |
| 0 | B0 | Rail facts decide the mappings, naming and the look-back |
| 1 | B1 | General matching every school payment relies on; useful to every business at once |
| 2 | B2 | The record everything else hangs on; can run beside B0 and B1 |
| 3 | B3 | What each child owes |
| 4 | B4 | The visible feature: a parent pays to the child's account and it lands |
| 5 | B5 | Cash and receipts, which every school asks for on day one |
| 6 | B6, B7 | Parents and chasing, once there is something to show and chase |
| 7 | B8 | Clearance, before the first resumption |
| 8 | B9 | Last, over what exists |

A pilot school can start after B5 (students, fees, accounts, matching, cash and receipts), with B6 to B8 before its first resumption day.

## 17. Testing

| Level | What | Where |
| --- | --- | --- |
| Capability conformance | `virtual_account` issuing and credits on each rail's fixtures; gross and net amounts | `engine/conntest`, `connectors/<rail>/testdata` |
| Exactly once | A batch interrupted and run again issues each student one account; a term invoiced twice makes one invoice per student; a credit delivered twice and found again by the look-back counts once | `engine`, `api` |
| Matching | Oldest invoice first; siblings; family credit; payer memory; a reference that does not agree is held | `engine/matching` |
| Approvals | Cash and discounts above the threshold reach nothing until approved; nobody approves their own | `api` |
| Clearance | Derived only; overrides audited and ending; no route writes it | `engine`, `api` |
| The gate | No amount in any response to a gate session; every other route 403 | `api`, Playwright |
| Family page | Expired links; claims never count as payments; accessibility in every theme | Playwright |
| Receipts | Numbering under concurrency; voided on undo; the QR shows nothing beyond valid or voided | `engine`, `api` |

## 18. Risks and open questions

| Risk or question | Mitigation or owner |
| --- | --- |
| A rail does not let the business name the account, so parents see a code instead of the child's name | B0 records naming per rail; the family page always shows the name the parent should expect |
| Paystack and Flutterwave may need approval or a BVN per permanent account | B0 records each rail's requirement; the school chooses its issuing connection |
| A rail charges per account issued or per credit | B0 records the fee; shown before a batch is issued |
| Credits reported net with no fee field | Counted as landed and flagged for the bursar; B0 confirms per rail |
| A reversal that leaves a child's invoice paid when the money went back (Balans parked this) | **Decided** (2026-10-23): a reversed or refunded credit undoes its matches, voids its receipts and reopens the invoice; clearance is recomputed and the bursar is told (B4, B5) |
| Partial payment: is 90% paid "cleared"? | **Decided** (2026-10-23): no. A balance left is partly paid, never cleared (§12.1); an override with a reason is the only way to clear it |
| A guardian in two families in one school (remarried parents, an uncle paying for two households) | **Decided** (2026-10-23): one family per guardian, to keep it simple. A guardian with children in two schools is in a family in each, since each school is its own business |
| WhatsApp outside the 24-hour window | Utility templates for reminders and receipts submitted to Meta (founder); SMS until approved |
| Parent links forwarded to others | Links show one family's fees only, expire, and show no other family's data; account numbers are meant to be shared |

## 19. Out of scope

A ledger (0046, point 3); lending and the partnership with a lender; payroll (Payrolla and Run payroll exist); inventory, cafeteria wallets, hostel and transport; attendance and absence messages (a later template on forms, not this plan); multi-campus dashboards; a mobile app; withdrawals through Taskiem beyond the payout templates that already exist.
