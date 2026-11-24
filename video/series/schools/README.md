# Series: Schools

"How a school collects its fees and knows who may come in, from the first term
to the next." Part of [the series plan](../README.md). Draft for approval,
17 November 2026; episodes reshaped 24 November 2026 to match the phone app. Based on `docs/schools.md` (what the app does today).

Same pattern as the other series (Episode 0, then one episode per chapter,
recap and bridge lines, Next cards, 9:16 and 16:9, English and Pidgin). Unlike
most series this one is **phone-first**: the School tab is in the phone app. The
**spine is different**: a school is not a shop. It sells nothing at a counter;
it enrols students, sets fees, invoices a term, collects money from families,
chases the rest, and decides at the gate who is cleared. The ten chapters
below replace the shop spine.

**Pidgin lines are drafts for a native speaker to check.** English is the source.

## 1. The school and the people (all invented, for approval)

**Bright Hill Academy**, Ikorodu, Lagos: nursery, primary and junior secondary,
about 400 students. Terms, a bursar's office, a gate on resumption day.

| Person | Role | In the story |
| --- | --- | --- |
| **Mrs Tolulope Fashola** | Proprietor and principal | Wants to know where the money is; approves what the rules say she must |
| **Mr Chukwudi Ibe** | Bursar | Runs the fees office; the main teacher of the series |
| **Mallam Audu** | Gateman | Holds one phone at the gate on resumption morning |
| **Mrs Bimpe Lawal** | A parent | Pays into her child's account number; later asks for a payment plan |
| **Mr Segun Danjuma** | A parent | Brings cash to the bursar |

Cast prompts for Flow are in [`characters.md`](characters.md) (draft; reference sheets still to approve).

## 2. What is different about filming this series

**Update, 18 November 2026:** main now has **Schools in the phone app** (built,
"waiting for the founder's word" in `docs/spec/mobile-app-look.md`, "The school,
as drawn"). My first draft said the phone app had no Schools pages; that is no
longer true. So this series is **phone-first, vertical**, and the web admin
pages are the second surface.

- **On the phone:** when Schools is on, a **School** tab takes Orders' place in
  the bottom bar (Chats, Today, Money, School). It has the term's fees collected
  of expected, owing by how late, the day's close; Students (search, import,
  add, one student's money and account number); Record a payment at the desk
  (a large amount waits for a second person); Debtors and reminders, promises
  and payment plans; Clearance and **the gate screen**; and Setup (classes and
  terms, fees, invoicing a term, importing students).
- **In the chat:** the morning briefing shows the school's figures; a cash
  posting waiting for a second person arrives as an **Approve / Decline card**;
  the tools panel starts with the school's tools. These parts can be filmed from
  the chat.
- **Still on the web only:** the school's wallet setup, and the **family page**
  that parents open from an SMS link (so that episode shows a parent's phone
  browser).
- **Filming constraint:** the School tab and its pages are the phone app's own
  design, not the web's. The recorder drives the web, so these screens need a
  **screen recording of the real app** (an Android emulator or a phone), as in
  `video/replan.md`, section 3. This environment has no Flutter or Android SDK.
  Chat parts (briefing, approval card, tools) can still be recorded from the web.
- **The demo world has no school.** It needs `seed-school.ts`: classes, a current
  term, students and families, fees, invoices, payments, debtors, a gate user.
  A student's own account number is issued by **iswallet**
  (permanent virtual accounts). The browser tests run a fake iswallet
  (`web/e2e/serve.sh`, `accounts.spec.ts`), so the demo world can issue numbers
  against it, or against iswallet Test mode. The school's wallet setup itself
  is web-only.
- **Personal data is sealed**: names, phones and dates of birth are masked unless
  someone with permission shows them. Demo names are fictional; the safety
  episode shows the masking on purpose.
- **WhatsApp and SMS carry parts of the story**: fee reminders, the family page
  link, the owner's second-person approvals.

## 3. Episodes (as filmed, 24 November 2026)

The ten films are rendered from the phone app. Where the app lacks a step the episode
is **reshaped** (what each one leaves out is in [`FIDELITY.md`](FIDELITY.md)). Each has a
recap, a bridge (English and a Pidgin draft), a Next card, Flow character clips and one
narration line per caption (`episodes/NN-slug/episode.json`).

| # | Episode | Film | Hero feature | Reshaped from the first draft |
| --- | --- | --- | --- | --- |
| 0 | One term at Bright Hill | a reel of ten stretches (about 2 min) | The whole term | Opening: Mr Ibe's desk |
| 1 | Set up your school's people | `school-open` | A gate role that sees only the gate screen | Was "Open your school". Schools is turned on **on the web**; the film starts with it on and says so. The Bursar is a custom role Taskiem adds when an owner turns Schools on (plan with custom roles); Gate is built in. Shows School settings and Add staff |
| 2 | Classes, sessions and terms | `school-classes-terms` | One current term; starting enrols every student | As planned |
| 3 | Students and families | `school-students` | A spreadsheet becomes the school | As planned |
| 4 | Set the fees | `school-fees` | Copy last term's fees | **No WAEC pass-through, no family discounts** |
| 5 | Invoice the term | `school-invoice-term` | The plan first; nobody invoiced twice | As planned |
| 6 | Get paid | `school-get-paid` | Cash over the limit waits for a second person | **No QR receipt**; a transfer landing on the child by itself is narrated only |
| 7 | Chase what is owed | `school-debtors` | Reminders by lateness; a promise; a plan | **No "skip a family on track"**; Mrs Lawal's plan request comes from the parents' web family page (stand-in) |
| 8 | Resumption day at the gate | `school-gate` | The gate screen on Mallam Audu's phone | **No teacher class list** |
| 9 | Know your school | `school-overview` | A close that says what does not reconcile | Trimmed to 36.4 s |
| 10 | Keep it safe, and start next term | `school-safe-next-term` | The same steps again, in minutes | **No audit log, no reveal control** (names show for roles that hold the permission, shown with two phones); copying fees is per class |

Episode 10 ends on **Start your free trial**, not a bridge.

## 4. What this series needs now

1. The cast's approved reference sheets from [`characters.md`](characters.md) (five people), then the Flow clips.
2. English narration recorded; the Pidgin drafts checked by a native speaker.
3. Names are in `video/characters/pronunciation.md` (Fashola, Ibe, Audu, Lawal, Danjuma, Ikorodu, Okafor, and more).
4. A decision on the reshaped episodes (above) before publishing.

Call to action: **Start your free trial.** Taskiem runs the school's work beside its
chats; never say it replaces WhatsApp.

## 5. Where it goes in the order

**Fourth, after Phones and gadgets** (confirmed 18 November 2026).
