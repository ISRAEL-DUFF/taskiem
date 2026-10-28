# Running a school

Taskiem keeps a school's fees office in one place: the students and their families, the fees for each term, each child's own bank account number, the money as it lands, receipts, the parents' page, reminders, who is cleared to come in, and a close of the books every night. This page is for the bursar and the proprietor; no technical steps.

## Turning it on

An owner opens **Settings > Schools** and turns Schools on. It appears in the menu for everyone who may see the school's record. Turning it on can also add a **Bursar** role, for whoever runs the fees office: give it to them under **Members & keys**. Nothing else changes for the rest of the business.

## Students, families, classes and terms

Start with **Schools > Classes and terms**: add your classes (JSS 1 A, Primary 4), the session (2026/2027) and its terms, each with when it starts and ends and when fees are due. Then **Students > Import students** with a spreadsheet saved as a CSV: a line per student with their name, admission number, class, a guardian's name and phone, and the guardian's email if you have it. Taskiem reads the whole file first and tells you about any line it cannot take; a file with a bad line saves nothing until you fix it. Students who share a guardian's phone are one **family**. Importing the same file again updates students by admission number and never adds anyone twice. One student at a time goes in under **Add a student**.

When a term begins, open it under Classes and terms and press **Start**: it becomes the current term and every active student is enrolled in their class for it. At the end of the session, **Promotion** moves everyone up a class in one step, with the exceptions you choose.

Names, dates of birth and phone numbers are personal data: they are kept sealed and shown masked unless someone allowed to see them presses **Show names**, which is recorded.

## Fees

Beside each term, **Fees** sets what each class pays: the item (Tuition, PTA levy), the amount, and whether every student of the class pays it (**required**) or only those who take it (**optional**, such as transport). Money you collect for someone else (WAEC, NECO) goes in with **Collected for** that body, and Taskiem keeps it apart from the school's own income everywhere. **Copy from** the term before fills a class's fees in one step.

On a student's page tick the optional items they take and record any standing discount (a sibling, a staff child, a scholarship), with its reason. A discount above the threshold the owner sets waits for a second person to approve.

When fees are set, **Invoice the term** (on the term's Invoices) makes one invoice per enrolled student, exactly once. It shows you the plan first (how many, the total, who is left out and why) and makes nothing until you confirm. Send them now, or keep them as drafts to check. Each family gets its invoice by SMS and email with a link to its own fees page. Running it again later invoices only the students who joined since. The *Bill the term's fees when the term starts* template does this on the day you choose each year.

## Each child's own account number

Under **Schools > Settings > Students' account numbers**, the owner chooses the connected payments account that issues them. A school with no payments account of its own turns on [receiving money with Taskiem](receiving-money.md) first: its wallet with Taskiem then issues the numbers, with nothing to paste. With iswallet, **Create the school's wallet** opens the wallet the numbers pay into: give the school's business email and the director's BVN, which go to iswallet once and are not kept by Taskiem. Then **Schools > Account numbers > Check who gets one** shows the plan and **Issue** gives each student a permanent account number of their own, named for the school and the child where the bank allows. A parent pays by ordinary transfer to it, and the money lands on that child's fees by itself: no one has to match it. The number is on the student's page and on the family's fees page, with a copy button.

A new student added during the term gets a number within a quarter of an hour when the *Give each new student their own account number* template is on; it also sends the family the number and the bank.

## Cash and transfers at the desk

Not every family pays into the child's number. For cash, a POS payment, or a transfer to the school's own account, open **Schools > Payments > Post a payment**: the student, the amount, how it was paid and the date. It is counted towards the child's fees at once and gets a receipt. Above the **cash threshold** the owner sets, a posting waits for a **second person** to approve under **Approvals**, and the person who posted it can never approve their own. Leave the threshold empty for no second person, or 0 for every posting to wait.

## Receipts

Every payment counted towards a child's fees gets a receipt, however the money came: a transfer to the child's number, cash at the desk, a payment matched in To match. Receipts are numbered per term (RCT-2026T1-0079). **Schools > Receipts** finds any by number; **Open PDF** prints it, with a QR code anyone can scan to see that it is valid. A payment that is undone or returned voids its receipt; it keeps its number and is never deleted. The *Send the family a receipt for every school fees payment* template sends the family the receipt's number, the amount and the check link the moment the money is counted.

## The family page

Parents need no app and no password. Every message the school sends them (an invoice, a receipt, a reminder) carries a link to **their own fees page**: each child's fees line by line, what was paid and what is still due, each child's account number with a copy button, the receipts, and two buttons: **I have paid** (for money paid some other way; it reaches your To match list, never the fees, until you find the money) and **Ask for a payment plan** (instalments by dates, which someone with approval rights accepts or declines under **Payment plans**). Links expire after the days set in Settings (3 by default); an expired link lets the family ask for a new one. From **Families** you can send a family a fresh link yourself.

## Chasing

**Schools > Debtors** lists every student with a balance, aged by how long past the due date, with the family's last reminder, any promise to pay and any payment plan. Select up to 50 and send a reminder at one of three rungs: gentle, firmer, final, in the school's own wording (**Settings > Reminder wording**). A family with a promise to pay not yet due, or a payment plan in good standing, is skipped. The *Remind families of school fees every week* template does this on two chosen days, at most 50 families a run, choosing the rung by how late the fees are. Record a **promise to pay** when a parent says "salary comes on the 25th"; Taskiem checks it the day after and tells you if it was missed.

## Clearance and the gate

Whether a child may come in on resumption day is **read from the money, never set by hand**: cleared when the term's fees are paid in full, a payment plan is in good standing, or someone with approval rights cleared the child by **override** with a reason and an end date; **partly paid** is never cleared. **Schools > Clearance** shows the term by class with the balances; its CSV too. The *Send the clearance lists the day before resumption* template sends the head teacher each class's list by name, with no amounts.

For resumption morning there is **the gate screen**: one screen for the person at the gate, with a name search and one answer in big letters, **CLEARED** or **SEE THE BURSAR**, never an amount. Give that person the **Gate** role: when they sign in they see only this screen and nothing else of the business.

## The overview

**Schools > Overview** is the proprietor's page, made for a phone: what the term's fees come to and how much has come in, what was due by today and is still unpaid, this week's money, what is outstanding, how much of the money matched by itself without anyone's hand, how parents paid (account numbers, card or POS, cash, transfers), the money collected for others kept apart, the biggest debtors and the aging, who is cleared, and the last close.

## The daily close

Every night Taskiem closes the day before for the school, once, and keeps it: what landed on students that day (into their account numbers, and at the desk), what matched by itself and what a person matched, what is still not matched, cash postings and discounts waiting for a second person, families who say they paid and were not found, and anything that does not reconcile, such as a payment with no receipt. Each item says which page to fix it on; nothing is swept under the carpet. **Schools > Overview > The daily close** lists them; **Close yesterday now** takes it before the night does. The *Send the proprietor the daily close every morning* template sends each morning's close to the owners, and says in its first line when a day does not reconcile.

The morning money summary on WhatsApp or Telegram, and the monthly email, also carry a line for the school: what was collected for fees, what is outstanding, and the share of students cleared.

## In your own words

You can also describe what you want under **Workflows** ("every Friday, send me the ten biggest school fees debtors by class"): Taskiem knows the school's record and builds the draft for you to review. The full description of every page and rule is in [Schools](../schools.md).
