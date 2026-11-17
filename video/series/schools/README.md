# Series: Schools

"How a school collects its fees and knows who may come in, from the first term
to the next." Part of [the series plan](../README.md). Draft for approval,
17 November 2026. Based on `docs/schools.md` (what the app does today).

Same pattern as the other series (Episode 0, then one episode per chapter,
recap and bridge lines, Next cards, 9:16 and 16:9, English and Pidgin). The
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

None has a face or voice yet; each needs a bible entry and Flow references.

## 2. What is different about filming this series

- **It is mostly web admin pages** (`web/src/pages/School*.tsx`), not the chat.
  The phone app has no Schools pages today (only money and invoices). So
  recordings are the **web app, laptop size first (16:9)**. The vertical cuts
  need a plan: record at phone width (the web app is responsive, **check** the
  Schools pages at 390 px) or crop and zoom on the laptop recording.
- **The gate screen is built for a phone** (one screen, big ✓ CLEARED or
  ⚠ SEE THE BURSAR), so that episode is naturally vertical.
- **WhatsApp and SMS carry parts of the story**: fee reminders, the family page
  link, the owner's second-person approvals.
- **The demo world has no school.** It needs `seed-school.ts`: a school with
  classes, a current term, students and families, fees, some invoices, a few
  payments and debtors, a gate user. **Check:** a student's own account number
  needs an issuing connection (a bank rail); the demo has none, so a stand-in is
  needed or we show the setup without a live number.
- **Real personal data is sealed**: names, phones and dates of birth are masked
  in the app unless someone with permission shows them. All demo names are
  fictional; we show masking on purpose in the safety episode.

## 3. Episodes

### Episode 0: One term at Bright Hill
- **Goal:** see the whole term, from setting fees to the gate; know what each
  episode teaches.
- **Length:** 60 s vertical / 2:30 YouTube. Made last, from the other episodes.
- **Opening:** Mr Ibe's desk: a stack of fee books and a ringing phone.
- **Closing:** the map of episodes, **Start your free trial**.
- **Bridge:** "It starts with setting up the school." (to Episode 1)

### Episode 1: Open your school
- **Goal:** turn Schools on and give people the right roles.
- **Shows:** Turning Schools on under settings; the Bursar and Gate roles;
  who sees what.
- **Hero feature:** a gate role that can see nothing but the gate screen.
- **Length:** 40 s / 1:15.
- **Bridge:** "The school is open and empty. Where are the students?"
  (Pidgin draft: "School don open but nobody dey inside. Where the students?")
- **Next card:** Episode 2: Classes, sessions and terms.

### Episode 2: Classes, sessions and terms
- **Goal:** the school's structure in place.
- **Shows:** classes with arms (JSS 1 A, Primary 4 Gold), the 2026/2027 session,
  First term with its dates and fees due date, starting the term.
- **Hero feature:** one current term at a time; starting it enrols every student.
- **Length:** 50 s / 1:30.
- **Recap:** "Bright Hill is open. Now its classes and its terms."
- **Bridge:** "Four hundred students need to go in, with their parents."
- **Next card:** Episode 3: Students and families.

### Episode 3: Students and families
- **Goal:** put students in, with guardians.
- **Shows:** import a CSV (the columns it needs), the import's report; adding one
  student by hand; a family with a guardian known by phone; masked personal
  data; promotion at the end of a session.
- **Hero feature:** a spreadsheet becomes four hundred students.
- **Length:** 60 s / 1:45.
- **Recap:** "The terms are ready."
- **Bridge:** "Students are in. What do their parents owe?"
- **Next card:** Episode 4: Fees.

### Episode 4: Set the fees
- **Goal:** a fee structure for each class and term.
- **Shows:** Fees beside a term; Tuition, PTA levy, Books; required against
  optional (transport, lessons); a pass-through (WAEC) collected for someone
  else; Copy from the term before; discounts for a family.
- **Hero feature:** copy last term's fees in one tap.
- **Length:** 60 s / 1:45.
- **Recap:** "Four hundred students are in."
- **Bridge:** "Fees are set. Now every family needs a bill."
- **Next card:** Episode 5: Invoice the term.

### Episode 5: Invoice the term
- **Goal:** one invoice per student, exactly once, sent to the family.
- **Shows:** Invoice the term shows the plan first (how many, the total, who is
  left out and why); keep as drafts, or send now by SMS with a link to the
  family page.
- **Hero feature:** the plan before anything is made; nobody is invoiced twice.
- **Length:** 60 s / 1:45.
- **Recap:** "Fees are set."
- **Bridge:** "The bills are out. How will parents pay?"
- **Next card:** Episode 6: Get paid.

### Episode 6: Get paid
- **Goal:** money arrives and lands on the right child.
- **Shows:** a student's own account number (a transfer lands on that child by
  itself); cash and transfers posted at the desk; a posting over the
  threshold waits for a second person (approved on WhatsApp or the web);
  receipts with a QR code that checks as Valid.
- **Hero feature:** a transfer lands on the right child with no one asked.
- **Length:** 75 s / 2:00. **Check:** account numbers need the issuing connection.
- **Recap:** "The bills are out."
- **Bridge:** "Some parents have not paid. Who, and how late?"
- **Next card:** Episode 7: Chase what is owed.

### Episode 7: Chase what is owed
- **Goal:** find who owes and ask kindly, then firmly.
- **Shows:** Debtors, aged by days past due; the reminder ladder in the school's
  own words; a promise to pay; a payment plan a family asks for, which the
  owner approves; reminders skip a family whose plan is on track.
- **Hero feature:** three rungs of reminders; a plan that pauses them.
- **Length:** 75 s / 2:00.
- **Recap:** "Some families have not paid."
- **Bridge:** "Resumption day. Who may come in?"
- **Next card:** Episode 8: Resumption day.

### Episode 8: Resumption day at the gate
- **Goal:** know in one second who is cleared.
- **Shows:** Clearance by class (Cleared, Partly paid, Not paid, never set by
  hand); an override with a reason and a last day; the gate screen: three
  letters, a tap, ✓ CLEARED or ⚠ SEE THE BURSAR, in large type; a class list for
  a teacher with no amounts.
- **Hero feature:** the gate screen. Mallam Audu on one phone.
- **Length:** 60 s / 1:45. Naturally vertical.
- **Recap:** "It is resumption morning."
- **Bridge:** "At night, the day's money has to add up. Does it?"
- **Next card:** Episode 9: Know your school.

### Episode 9: Know your school
- **Goal:** read the overview and the daily close.
- **Shows:** Overview; the daily close (money in on students, matched, not yet
  matched, waiting for a second person, and "reconciles" or "does not
  reconcile"); the morning summary to the owner on WhatsApp.
- **Hero feature:** a close that says reconciles, and lists what does not.
- **Length:** 60 s / 1:45.
- **Recap:** "The term is under way."
- **Bridge:** "Money, names, who did what. How does the school keep it safe?"
- **Next card:** Episode 10: Keep it safe, and start next term.

### Episode 10: Keep it safe, and start next term
- **Goal:** personal data protected, every change on record; the next term
  started.
- **Shows:** names, phones and dates of birth masked unless someone with
  permission shows them; the audit log of who posted and approved what;
  promotion of a session; start the next term and copy its fees.
- **Hero feature:** the same steps, a second time, in minutes.
- **Closing, not a bridge:** "That is a term at Bright Hill. **Start your free trial.**"
- **Length:** 60 s / 1:45.

## 4. What this series needs

1. `seed-school.ts` (see section 2) and a decision on account numbers in the demo.
2. The cast's bible entries and Flow references (five people).
3. Recording scripts per episode in this folder, for the web at laptop size.
4. English narration, then Pidgin and its review; names added to
   `characters/pronunciation.md` (Fashola, Ibe, Audu, Lawal, Danjuma, Ikorodu).
5. A plan for the vertical cuts of web admin footage.

## 5. Where it goes in the order

I suggest **fourth**, after Phones and gadgets: it needs the most new work (a
whole new demo world and a different kind of footage), and the first three can
reuse the existing chat recordings. If schools are a bigger or nearer sales
opportunity than gadgets, it can move up.
