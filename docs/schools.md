# Schools

Schools is an area any Taskiem business can turn on: the school's students, their families, its classes, sessions and terms, beside Invoices and Money. It is the record the rest of the Schools work hangs on ([decision 0046](decisions/0046-balans-in-taskiem.md), [the plan](spec/schools.md)). This page covers what is there now: turning it on, who can do what, the record, getting students in, promotion, [fees and term invoices](#fees), [each student's own account number](#account-numbers), [cash and transfers posted at the desk](#cash-and-transfers), [the second person](#the-second-person), [receipts](#receipts), [the family page](#the-family-page), chasing ([debtors and aging](#debtors), [the reminder ladder](#the-ladder), [promises to pay](#promises), [payment plans](#payment-plans)), [clearance](#clearance): [the rule](#clearance), [overrides](#overrides), [clearance lists](#clearance-lists) and [the gate](#the-gate); and the proprietor's: [the Overview](#overview), [the daily close](#the-daily-close), [the summaries](#summaries) and [the templates](#templates). The plain-language guide for a bursar or proprietor is [Running a school on Taskiem](guide/schools.md).

## Turning it on

An owner turns Schools on under **Secrets & settings** (or **Schools > Settings**). It appears in the menu for everyone who may read the school's record (`school.read`). Turning it off hides it again and its pages and API answer that it is off; nothing is deleted, and turning it back on brings everything back as it was. Only an owner can turn it on or off, and each change is recorded in the audit log.

When an owner turns it on, Taskiem can also add a **Bursar** role (below) for whoever runs the fees office, when the plan has custom roles and the business has no role named `bursar` yet.


## Who can do what

| Permission | Lets a person | Built-in roles |
| --- | --- | --- |
| `school.read` | See students, families, classes and terms, fees, payments, receipts, debtors and their aging, promises and plans, clearance and overrides; use the gate screen | Owner, admin, operator, auditor, viewer |
| `school.manage` | Add and change students, families, classes, sessions and terms; import students; start a term; promote a session; set fees, students' optional items and discounts; invoice a term; issue account numbers; post cash and transfers; send reminders; record and withdraw promises to pay; run the chasing check | Owner, admin, operator |
| `school.approve` | Approve or decline cash postings and discounts at or above the school's thresholds ([the second person](#the-second-person)); decide [payment plans](#payment-plans); [clear a student by override](#overrides) and withdraw one | Owner, and custom roles given it |
| `school.gate` | [The gate screen](#the-gate) only | Gate |

- **Bursar** is a custom role with `school.read`, `school.manage`, `invoice.read` and `invoice.manage`. Give it to people under **Members & keys**, and change it under custom roles like any other.
- **Gate** is a built-in role for whoever stands at the school gate. It has `school.gate` and nothing else, and the server refuses a gate session every page and API route but who it is, signing out and [the gate screen's two routes](#the-gate) (403, `gate_only`): nothing is merely hidden. The gate screen is a name search answering **Cleared** or **See the bursar**, never an amount. Only an owner can give someone the gate role, as it takes holding `school.gate`.

Students' and guardians' names, dates of birth, phones and emails are personal data: sealed where they are kept, and masked in the web app and the API (`E•••`, `•••4567`) for everyone. People with `pii.reveal` can show them (**Show names**, **Show personal details**), which is recorded in the audit log. Searching students by name needs the names shown; the admission number can always be searched.

Every change to the record (a class, a session or term, a student, a family or guardian, an import, starting a term, a promotion) is recorded in the audit log with who made it.

## The record

| Record | Holds | Rules |
| --- | --- | --- |
| **Class** | A name (JSS 1, Primary 4), an arm (A, B, Gold), and its order | A name and arm once per school. Arms of one class share its order. A class no student was ever in can be removed; one with history stays |
| **Session** | The school year, written 2026/2027 | Once per school |
| **Term** | A name (First term), when it starts and ends, and when fees are due | One **current** term at a time. A term that has started and is no longer current is **past**: it and its enrollments can no longer be changed. A term that has not started can be changed or removed |
| **Student** | Name, admission number, class, status (active, withdrawn, graduated), date of birth (optional), family | An admission number once per school, whatever its case |
| **Family** | Its guardians (name, phone, email, relationship) and students | A student is in one family. A guardian, known by their phone, is in **one family** in a school: a phone already in another family is refused |
| **Enrollment** | A student in a class for a term | Made when a term starts, once per student and term |

**Starting a term** (Classes and terms, **Start…**) makes it the current term, makes the one before past, and enrols every active student in their class for it. Each student is enrolled in a term once: starting it again enrols nobody twice. A student added while the term runs is enrolled at once; one moved to another class during the term has their enrollment for it moved too, while past terms keep the class each student was in then. A student's page shows their class history.

### Kept copies for workflows

Taskiem copies the school's students, classes and term invoices to three [kept tables](data-tables.md#kept-tables) in the business's data tables, so templates and workflows can read them like the money log: `school_students` (student id, admission number, name in a personal column, class and class id, status, family id, when it last changed), `school_classes` (class id, name, arm, label, order) and `school_term_fees` (each student's invoice for a term: invoice id and number, student id, admission number, name in a personal column, class and class id, term and term id, `total_kobo`, `paid_kobo`, status, when it last changed). Each row follows its record within a minute or so of a change, a term fee's as its invoice is sent and paid. No person or workflow can add, change or delete their rows; the record changes only on the Schools pages and through the API.

## Getting students in

### Importing a file

**Students > Import students** takes a spreadsheet saved as a CSV. Its first line names the columns:

| Column | Also written | Needed |
| --- | --- | --- |
| name | student name, student | Yes |
| admission number | admission no, adm no | Yes |
| class | | Yes, written as on the Classes page: `JSS 1 A` (case, spaces and dashes do not matter) |
| guardian name | guardian, parent name, parent | Yes |
| phone | guardian phone, parent phone | Yes: `08031234567` or `+2348031234567`, or a number abroad with its country code |
| email | guardian email | No |
| relationship | relation | No: Mother, Father, Uncle |
| date of birth | dob | No: `2015-03-21` or `21/03/2015` |

A student with two guardians is on two lines with the same admission number, name and class.

**Check the file** reads every line first. A file with **any** line that cannot be taken saves **nothing**, and the page lists each such line and why: a missing name or phone, a phone that is not one, a class the school does not have, an admission number given with two names, a phone given for two different guardians, guardians already in two different families. Fix the file and check it again.

A file that is all good is shown before anything is saved: how many students are added and how many updated, and the **families**, grouped by the guardians' phones (students sharing a guardian are one family; a guardian already here brings the student into their family). **Save** saves exactly the file that was shown, in one step.

Importing the same students again **updates** them by admission number (their name, class, family and guardians' details) and never adds them twice.

### One by one

**Students > Add a student** takes the name, admission number, class, an optional date of birth, and optionally a guardian, which makes a new family. While a term runs the student is enrolled in it at once.

A form parents fill in themselves, reviewed by the bursar before it becomes a student, comes later as a template on [forms](forms.md).

## Promotion

At the end of a session, **Classes and terms > Promotion at the end of 2026/2027** moves every active student up one class in one step: to the class of the next order with the same arm (else the first arm of that order). Students in the last class graduate. Before promoting, choose the exceptions: a student who **repeats** the class, **leaves** the school (withdrawn), or **graduates** early. The page counts what will happen as you choose.

A session is promoted **once**: promoting it again is refused, and the session then shows when it was promoted. Promote before the next session's first term starts (after that it is refused, as that term has enrolled students in their old classes). If the session's last term is still current when you promote, its enrollments keep the classes the students were in during it.

## Fees

**Classes and terms > Fees** (beside each term) sets each class's **fee structure** for the term: its fee items, each with

| | |
| --- | --- |
| **Name** | Tuition, PTA levy, ICT levy, Books. Once per class and term |
| **Amount** | In naira |
| **Kind** | **Required** (every student of the class pays it) or **optional** (transport, lessons: only the students who take it) |
| **Collected for** | Empty for the school's own fees. For money the school collects on someone else's behalf, the body it is for (WAEC, NECO): a [pass-through](#pass-through) item |

**Copy from** the term before (the same class), or from another class this term, fills a class's structure in one step; change it after. A past term's fees are its history and read-only.

A change to a structure after the term is invoiced reaches the term's invoices that are still **drafts**: they are rebuilt from the fees as they are now. An invoice already **sent** is not rebuilt: it changes only by [amendment](invoices.md#amending-a-sent-invoice), with a reason.

### A student's optional items and discounts

A student's page shows their fees for the current term (the invoice their class's fees, their optional items and their discounts make now, and the term's invoice once they have one):

- **Optional items this term**: tick the optional items of their class the student takes (transport). Saved per term.
- **Standing discounts**: a **sibling**, **staff child**, **scholarship**, **hardship** or **other** discount, as a percent of the school's own fees or an amount, with a reason. It is taken off every term invoice made while it stands, as its own negative line with the reason ("Sibling discount (10%)"); **End** stops it (invoices already sent keep it). A discount comes off the school's own fees only, never off money collected for others, and never below nothing.

Both reach the student's drafts at once, as fee changes do.

### Discounts

A discount **at or above the school's discount threshold** waits for [a second person](#the-second-person) (the plan's [§8](spec/schools.md#8-cash-at-the-desk-discounts-and-the-second-person)). The threshold is the owner's, in naira, under **Schools > Settings > Discount threshold**; 0 or empty means none. Such a discount is **recorded as a request, not given** (the API answers 202, `code: needs_approval`, with the request), whether a standing discount (a percent weighed against the student's fees in the current term) or a discount amendment on a sent invoice; a term invoice whose standing discounts come to the threshold is left out of the plan, and confirming the plan records a request for it too. Approved, the discount is given (the standing discount stands and the student's drafts are rebuilt; the invoice is amended; the held term invoice is made, and sent when the plan said so); declined, nothing changes. Changes to the threshold are in the audit log.

## Invoicing a term

**Invoice the term** (a term's **Invoices**, while it is the current term) makes **one invoice per student enrolled in the term, exactly once**. It shows the plan first, and makes nothing until you confirm it:

- how many students it invoices and the total (and of it how much is collected for others), by class;
- how many are invoiced already (they are never invoiced again);
- who is left out, and why: no guardian to send it to (add one on Families), no fees for their class, nothing to pay after discounts, or a discount waiting for a second person.

Then choose **Keep them as drafts** (to check, then **Send the drafts** from the same place) or **Send them now** (by SMS and email with a fresh link to [the family page](#the-family-page), where each child's fees, payments and ways to pay are, in place of the invoice's own page link; each family's credit from an earlier payment counts first). Running it again after students join invoices only them, so it is safe to run as often as you like. If the plan changes between showing it and confirming it (a student added, fees changed), the confirmation is refused: check it again.

Each invoice:

| | |
| --- | --- |
| Customer | The family: its first guardian's name, phone and email, sealed as every invoice's customer is |
| Customer key | `student:<id>`: one student's invoices are one [customer's](matching.md#customers-own-account-numbers), so the student's own account number, once issued, pays their invoices, oldest first, and siblings stay apart |
| Lines | The class's required items, the student's optional items, then each standing discount as its own line with its reason |
| Due | The term's fees due date |
| Number | The business's next invoice number |
| Note | The admission number, class and term (no name: a note is not sealed) |

The invoice records its student, their class for the term and the term, so **Invoices** for the term lists them by class with their status, and filters by class and status. Everything else about invoices (their page, matching, leaks, the money reports) works on them unchanged.

### Pass-through

An item collected for someone else (WAEC's fee) is a **pass-through** line: the invoice's page says "Collected on behalf of WAEC", and the [money reports](money-reports.md#pass-through) keep it out of the school's own income: the Overview shows it as **Collected for others**, the invoices export has pass-through columns, and the journal posts it to `Collected for WAEC`, not sales. What a family pays counts towards the school's own fees first and the pass-through items last.

## Account numbers

Each student can have **their own bank account number**: a permanent number a rail issues for them, which parents pay into by ordinary transfer. A payment into it lands on that child by itself: it is matched to the student's own term invoices, oldest term first, with no one asked ([customers' own account numbers](matching.md#customers-own-account-numbers)). The number is the school's own collection account, meant to be given to parents; the family page shows it with a copy button when it arrives.

### The issuing connection

An owner chooses, under **Schools > Settings > Students' account numbers**, the connection that issues them: an active connection in prod whose rail can issue account numbers. The page lists the business's connections that can, with what each rail needs, in plain words (from what each rail documents, [rail facts](spec/schools-rail-facts.md)):

| Rail | What it needs | Who names the account |
| --- | --- | --- |
| iswallet | The wallet the numbers pay into, chosen here; iswallet issues numbers only once it holds the wallet's business director's BVN | iswallet's partner bank, whatever Taskiem asks |
| Paystack | An email for each account, **one account per email**: each student needs a guardian email no brother or sister shares; a registered Nigerian business that has gone live (at most 1,000 accounts unless Paystack raises it) | Paystack: "your business name / the child's name" |
| Flutterwave | A parent's **BVN or NIN** for each account, typed in on the student's page and sent to Flutterwave, never kept; and a guardian email | Not documented |
| Moniepoint | An email for each account (the guardian's) | Taskiem: the school's and the child's names |

No rail documents a fee per account issued; each charges for payments into the accounts (iswallet's partner 1.4%, at most ₦2,000; Paystack 1%, at most ₦300; Flutterwave 2% plus VAT), which the plan repeats. The rail's own checks of the business (its BVN, director and registration) are done with the rail when the business connects it. Changing the setting is in the audit log.

### Issuing

**Schools > Account numbers > Check who gets one** shows the plan first, and nothing is issued until you confirm it:

- how many active students already have an account on the issuing connection;
- how many this batch issues (**at most 500**; the plan says how many more the next batch can);
- who **cannot** be issued yet, and why: no guardian email where the rail needs one, a guardian email a sibling's Paystack account has, Flutterwave's BVN or NIN (issue those on each student's page), a student whose details were erased;
- who is **waiting**: an account being issued now, or a lost answer to check (below).

**Issue** confirms exactly that plan (if it changed since, the confirmation is refused: check again) and starts the batch, which works in the background; the page follows it (how many issued, how many not) and lists each student not issued with **the rail's reason**, such as `iswallet KYC_REQUIRED: the director's BVN is missing`. Fix what the rail says and check again: the next batch tries them again. Only one batch runs at a time, and the same plan confirmed twice is one batch.

A student's page has **Account number**: the number (grouped as banks print it, with a copy button), its bank, the account's name, and the connection. Someone with `school.manage` can **Issue the account number** there for one student, with a parent's BVN or NIN on Flutterwave.

**Exactly once.** A student is issued one account per connection, ever: the rail's key is `student:<id>:acct:<connection>`, and the issue is claimed on the student's record before the rail is asked. Issuing again (a second batch, a retried request, the workflow step run twice) answers the account already issued without asking the rail. A batch cut short (the server stopped) is picked up where it stopped: on iswallet and Moniepoint, which answer the same account for the same key, the student is simply asked for again; on Paystack and Flutterwave, where asking again could open a second account, the student is left as **Check with the rail**: look on the rail's dashboard for an account under the student's name, then **I checked: issue it again** on their page. A failure that may pass on its own (the rail did not answer, on a rail where asking again is safe) is tried up to three times by the batch.

The account is registered as the student's [customer account](matching.md#customers-own-account-numbers) (`key:student:<id>`), with the account number and the reference the rail answered, so the permanent-account rule matches what is paid into it.

### Naming

Where the rail lets the school name the account (Moniepoint; and iswallet and Flutterwave are asked), the name parents see when they check the number is **the school's name and the child's**, in capitals: `GREENFIELD COURT ACADEMY / EBUBE EZE`. Where the rail limits its length (no rail documents its limit, so 31 characters is assumed) **the school's name shortens and the child's never does**: word by word from its end (`GREENFIELD COURT / EBUBE EZE`, then `GREENFIELD / EBUBE EZE`), then its one word is cut. Only when the child's name alone does not fit is it cut, from its end, and the account is flagged on the student's page so the school checks what parents see. Paystack names the account itself and is sent no name. The student's page says when the rail names the account itself, as parents may then see another name.

The account's name has the child's name, so it is personal data: sealed where it is kept and masked (`G•••`) unless someone with `pii.reveal` shows it, which is audited.

### The reference

Each account's reference is derived from the business, the student and the connection, in the rail's own characters (`tsk_` and 32 letters and digits on iswallet and Moniepoint, `tva_` and the same on Flutterwave), so it is the same however often the account is asked for. Paystack knows its accounts by its own customer code (`CUS_…`), which it answers. Taskiem registers the reference the rail answered, the one each payment into the account carries; a payment is matched only when its reference and, where the rail says it, its account number agree with what was registered (a payment that disagrees is held in To match with the reason).

### Brothers and sisters

**When a student's account takes more than their own fees** (Schools > Settings, off by default): turned on, what a payment into a student's account leaves after the student's own open invoices counts towards their brothers' and sisters' open invoices (the other students of their family, by admission number), each one's oldest first, each part saying "for a sibling (the school shares family credit)". What is still left is the student's own credit; a sibling's invoice sent later takes it too, after their own. Off, what is left stays the child's own credit, counted towards their next invoice.

### The look-back

Some payments never arrive as a webhook: iswallet's credit notice is not mapped yet (which of its fields names the account is not documented), and any rail's webhook may not be set up. So for each connection that issued students' accounts and can list its payments (iswallet, Paystack, Flutterwave), Taskiem **reads back the last 48 hours every 15 minutes**. Each payment it finds is recorded in the [money log](money-log.md) as the rail's notice of it would be, with the same key (the account's reference and the rail's own id for the payment), so a payment seen by both is one entry, and matched like any other. A window with more payments than one read holds is read again in quarters.

When the look-back finds a payment the webhook should have brought (on a rail whose webhook is mapped, a payment more than half an hour old the log did not have), the owners are told once a day per connection that its webhook may not be set up, with where to set it. When the look-back of a connection fails three times in a row, an alert says so (once a day while it fails): payments its webhook does not bring wait until it works again.

## Cash and transfers

Not every family pays into the child's account number: some bring cash to the bursar, some transfer to the school's own bank account, some pay by POS at the desk. **Schools > Payments > Post a payment** (`school.manage`) takes the student (by admission number), the amount, how it was paid (cash, a transfer to the school's account, POS, another way), the date (today when empty) and a note. The posting becomes an entry in the [money log](money-log.md) as money the school entered itself (kind `cash` on the channel `cash`, `statement` on `bank` for a transfer, `pos_sale` on `pos`), recorded as that student's money, and is counted along the one path every payment takes ([customers' own account numbers](matching.md#customers-own-account-numbers)): towards the student's open term invoices oldest first, then, where the school shares family credit, their brothers' and sisters', and what is left stays as the family's credit for the next invoice. Each part counted gets a [receipt](#receipts) at once. The page says what it did: "₦20,000 posted: ₦20,000 towards INV-0007 (receipt RCT-2026T1-0001)". An `Idempotency-Key` makes a retried request the same posting.

The posting is in the audit log (`school.payment.post`), on the invoice as a payment "Received by the school" by whoever posted it, and on Payments with its state: **Posted**, **Waiting for a second person** or **Declined**.

## The second person

The owner sets two thresholds under **Schools > Settings**, in naira:

| Threshold | Applies to | Meaning |
| --- | --- | --- |
| **Cash threshold** | Cash and transfer postings | At or above it a posting waits for a second person. **0 makes every posting wait**; "no second person needed" (empty) lets the bursar post anything |
| **Discount threshold** | Standing discounts, discount amendments and term invoices carrying such a discount | At or above it a discount waits ([discounts](#discounts)); 0 or empty: none |

A posting or discount at or above its threshold is **recorded as a request** and nothing else: it is not on the child's fees, not in the money log, not on the invoice. The request goes to everyone who may approve for the school (`school.approve`: owners, and custom roles given it) under **Approvals** in the web app, and on the WhatsApp or Telegram they linked to Taskiem, with **Approve** and **Decline** buttons, as a run's approval does ([governance](governance.md#school-requests)). Each request shows what was asked (the amount, the student by admission number and class, how the money was paid, the discount's kind and reason) and who asked.

- **Four-eyes, always.** The person who asked never decides their own request, whatever their role: an owner who posts ₦60,000 at the desk waits for another owner. The request says "You asked for this, so a second person decides it".
- **Approve** does exactly what was asked, once: the cash is posted (entry, parts, receipts), the discount given, the invoice amended, the held term invoice made. A request decided twice, or by two people at once, is refused the second time.
- **Decline** needs a reason, and changes nothing; the reason is kept with the request.
- Either way the requester is told in their notifications, and the decision is in the audit log (`school.approval.decide`, with who, what and how it was decided; `school.approval.request` records the asking).

A pending request is listed on Payments (and, for a discount, under the student) until it is decided. One not decided within a week stops being sent to approvers' phones; it still waits in the web app.

## Receipts

**Every payment counted towards a term invoice gets a receipt**, however the money came: a credit to the student's account number, a suggestion confirmed or a match by hand in To match, a split part, cash posted at the desk, an approved posting. It is issued in the same step as the payment is counted, exactly once per payment part, and numbered per school and term from a counter: `RCT-2026T1-0079` is the 79th receipt of the first term of 2026/2027. A payment undone (matched to the wrong invoice) or [money that went back](matching.md#money-that-went-back) to the payer **voids** its receipt: it keeps its number and its place, marked voided with why, and is never deleted. The next payment gets a new receipt.

**Schools > Receipts** lists the latest receipts and finds one by number (case does not matter); each student's page lists theirs, and an invoice's page names the receipt beside each payment. **Open PDF** makes the receipt on demand: A5, with its own font embedded so the naira sign prints on any phone or printer, the school's name, the student and class, the amount and the date paid, how it was received, what it paid (the invoice's number and lines), the invoice's balance after it, the receipt number and a QR code. Nothing is kept on disk. The PDF carries the student's name, so each rendering is in the audit log (`school.receipt.pdf`); the lists mask names as the rest of the area does.

### Checking a receipt

The QR code opens `https://<form host>/r/<token>`, where the token is the receipt's own, unguessable and unrelated to its number. The page answers **Valid** or **Voided**, the school's name, the amount and the date, and nothing else: not the student, not the invoice, not the number. A receipt number typed into the address, or any guess, opens nothing, so the school's sequence cannot be walked from a receipt in hand. The link is also what a template sends the family: the `invoice` trigger's `payment` event carries it ([invoices](invoices.md#starting-a-workflow-when-an-invoice-is-paid)), and the `school-receipt` [template](#templates) sends it the moment a payment is counted.

## The family page

A family needs no account, password or sign-up: it reaches **its own fees page** from a link in any message the school sends (an invoice, a receipt, a link sent from Families), at `https://<form host>/s/<token>` on the public host, in the school's [look](decisions/0042-public-page-themes.md). The page works without script and on a slow connection, and shows:

- **Each child**, switchable; the chosen child is in the address (`?child=`), so a parent can bookmark or forward one child's page.
- **This term's fees line by line**, with discounts and amendments as their own lines and each line collected for someone else labelled "Collected on behalf of WAEC"; the total, what was paid and what is still due; fees from an **earlier term still open**, marked as such; and the **balance** across them.
- **Family credit**: money paid that no invoice counts yet (and a note when the school shares it among siblings).
- **Each child's own account number** and bank, with a Copy button where the browser allows it, and the name the parent should expect when they transfer. A child with no account number yet gets a link to each open invoice's own page, where a one-time account number or card payment can be asked for.
- **Receipts**: number, date, amount, and **Valid** or **Voided**, each linking to [the receipt's check](#checking-a-receipt).
- **I have paid**: for money paid some other way (cash at the school, a transfer to another account), the parent gives the amount, the date, how (transfer, cash, POS, another way), a reference and a note. It is recorded as a **claim**, never as a payment: nothing changes on the fees. The bursar sees it in [To match](matching.md#confirming-rejecting-and-matching-by-hand) beside the unmatched money ("Families say they paid") and on the student's page; when a payment for the student is counted (a credit matched, cash posted) the claim of that amount, else the oldest open one, is marked **found and counted**; one the school cannot find is **dismissed** with a reason. The page shows the family each claim's state. At most 5 claims an hour and 20 a day per family.
- **Ask for a payment plan**: one to six instalments (an amount by a date) and a short message. It is always offered; a school that does not do plans declines. The request reaches the student's page, in the parent's own words, and **Payment plans** is decided by someone with `school.approve`: **approve**, changing the instalments if need be, with an optional note, or **decline** with a note. The family is told at once, with a fresh link (and the `school-plan-decided` template tells any family that could not be reached), and the page shows the decision. One request at a time per student while the last is undecided. An approved plan's instalments are the schedule reminders follow ([payment plans](#payment-plans)); clearance comes next.

**Links.** Every message mints a new link, valid for the school's **family page links** setting (**Schools > Settings**, 1 to 30 days; 3 by default). An expired link says **ask the school for a new link**, never "not found", and offers **Send me a new link**: the family taps it, and a new link goes to the guardians' numbers on file (on WhatsApp or Telegram where a guardian gets the school's updates there, otherwise by SMS), without the page ever showing a number; at most 3 an hour and 10 a day per family. A link nobody minted gets the same words with no button. The database keeps only each link's hash; the children's names and the links of their invoice pages and receipts travel with the link, sealed under a key made from the token itself, so the public host reads them only when it holds the link. Staff can **Send a link** to a family from the Families page (the link is shown to pass on by hand, which is audited), and a workflow mints one with `schools@1` `family_link` or finds one in the `invoice` trigger's `payment` event (`trigger.body.family_link`).

The forms go to the api role through an inbox on the [public host](public-host.md#hosted-forms), as storefront orders do, sealed under the public host's key: the public host records nothing itself and holds no sealed data. Each claim, plan decision and link is in the audit log (`school.claim.resolve`, `school.plan.decide`, `school.family.link`, `settings.schools.family_link_days`).

## Debtors

**Schools > Debtors** (`school.read`) lists every student with a balance on a sent term invoice: this term's, and earlier terms' invoices still open. Each is **aged** by whole days past the invoice's due date (the term's fees due date), Lagos time, into five buckets: **not yet due**, **0 to 7 days**, **7 to 14**, **14 to 30** and **over 30**. The totals by bucket (how many students, how much) stand at the top; choose one to see only its students. Filter by class, and by what stands against the student: a promise to pay, a promise missed, a plan in good standing, a plan in arrears, or none. Each row shows the balance, the open invoices behind it, the last reminder the family got (its rung and when), the open promise or the last one missed, and the standing of any payment plan. Names are masked as everywhere in the area unless someone with `pii.reveal` shows them (audited). **Download CSV** gives the same rows, masked the same way, with the aging, the last reminder, the promise and the plan.

A student with nothing left to pay leaves the list by itself; a part payment lowers the balance. Withdrawn students still owing are listed with their status, so the money is not forgotten.

### The ladder

Reminders go up a ladder of three rungs, each in the school's own words (**Schools > Settings > Reminder wording**, the owner's, audited):

| Rung | Tone | Default wording |
| --- | --- | --- |
| 1 | Gentle | "A friendly reminder: {balance} is outstanding on {child}'s school fees, due {due_date}. You can pay by transfer to the account number {account_number}. Your fees page: {link}" |
| 2 | Firmer | "{child}'s school fees of {balance} were due on {due_date} and are still outstanding. Please pay to the account number {account_number} this week, or ask for a payment plan on your fees page: {link}" |
| 3 | Final | "Final notice: {balance} is still outstanding on {child}'s school fees, which were due on {due_date}. Please pay to the account number {account_number} now or see the bursar; the school's next step follows. Your fees page: {link}" |

The placeholders are `{balance}` (what the family's chosen children owe together), `{child}` (their names: "Ebube and Chidi"), `{account_number}` (each child's own account number and bank on the issuing connection, or "on your fees page" for a child without one), `{due_date}` and `{link}`. A wording with any other `{placeholder}` is refused; one without `{link}` has it added at the end, so every message carries a fresh link to [the family page](#the-family-page); up to 600 characters. The school's name goes before every message as it is sent. The settings page previews each rung with sample values; an empty rung keeps the default. An SMS gets the same words with the naira sign written NGN (a naira sign halves what fits in three parts), and a short form with the same facts when even so it would not fit.

On Debtors the bursar selects students, **at most 50 per send** (the counter says so; the server refuses more), chooses a rung and sends. For each selected student's family Taskiem sends **one message per guardian**, naming every selected child of the family and their balance together, on WhatsApp or Telegram where the guardian gets the school's updates there, otherwise by SMS. The result lists each student: **sent**, with how the family was reached, or **skipped** and why:

- nothing due, or no family to send to;
- **a promise to pay** not yet due ([promises](#promises));
- **a payment plan in good standing** ([payment plans](#payment-plans)), with the next instalment's date; a plan in arrears is reminded;
- the family was **sent that rung today already** (for a brother or sister, or by an earlier send): a family gets one message per rung per day.

A send is **exactly once per request** (`Idempotency-Key`): the same key answers what the first send did and messages nobody twice. Each reminder is recorded per student with the rung, the balance then and how the family was reached (`school.reminders.send` in the audit log); the student's page lists them, and Debtors shows the last. When no guardian can be reached (no phone, messages off), the reminder is recorded as sent to nobody and the result says so.

The `school-fee-reminders` [template](templates.md) sends them on a schedule: on two chosen days a week, to the debtors past due (of one class, or all), oldest due first, at most 50 per run, with the rung chosen by days past due (1 up to a week, 2 up to two weeks, 3 after), skipping promises and plans in good standing the same way, then tells the bursar what went out.

### Promises

A bursar who hears "salary comes on the 25th" records a **promise to pay** (Debtors, or the student's page; `school.manage`, audited): the date (today to a year on) and a note, for a student with a balance; one open promise per student. Reminders **skip the family until the date**. The check that runs after it (every hour, Lagos time; the day after the date) finds the promise **kept** when the balance moved down since it was made (or is nothing), **missed** when it did not, each exactly once; a missed promise puts the student back on Debtors flagged **promise missed** (its own filter), the bursars are told in their notifications, and the family is reminded again. A promise can be **withdrawn** by hand; nothing is deleted, and the student's page lists every promise with its state. **Check promises and plans now** on Debtors runs the check at once; running it again changes nothing.

### Payment plans

When a plan request is approved ([the family page](#the-family-page)), the instalments as approved are the schedule. A plan is **in good standing** while what the family has paid for the student since the plan was approved (every payment counted towards the student's invoices from then on, however it came) covers every instalment past its date plus the school's **grace** (**Schools > Settings > Payment plan grace**, 0 to 60 days, 0 by default); it is **in arrears** from the first instalment not covered; it is **settled** once the instalments are paid or nothing is due. Debtors shows the standing, with the next instalment or the one missed and how much is short; the student's page shows the whole plan.

Reminders **skip** a family whose plan is in good standing, and remind one in arrears by the days past the instalment missed (not the invoice's own due date), so a family that fell behind by three days gets the gentle rung. The hourly check marks each instalment found missed **exactly once** (`school_plan_arrears`) and tells the bursars once per check; paying the instalment restores good standing, and the mark stays as history on the student's page.

[Clearance](#clearance) reads the same standing: `schools.PlanStanding(student, term)` is the latest plan approved since the term started, and a student whose plan is in good standing is cleared although a balance remains.

## Clearance

Whether a child may come in on resumption day is **read from the money, never set by hand**. There is no clearance field anywhere in the record: it is computed every time it is asked for, from the term's invoices, the standing of any payment plan and any override in force, so a payment that lands changes the answer at once, and a reversed payment takes it back. A student is, for a term, one of three things:

| Status | When |
| --- | --- |
| **Cleared** | The term's invoices are paid in full; **or** an approved [payment plan](#payment-plans) is in good standing (or settled); **or** an [override](#overrides) is in force |
| **Partly paid** | Some money was counted towards the term's invoices and a balance is left. **Never cleared** (decided 2026-10-23): 90% paid is not paid; the gate says see the bursar, and only an override clears it |
| **Not paid** | Nothing was counted, or the term has fees for the class and the student has no invoice yet |

A student with **no invoice for the term** is cleared only when their class's fee structure for the term has **no required items** (there is nothing to pay: a nursery with no fees set, an art club); when the class has required fees and the student was not invoiced (added after the term was invoiced, or left out for a missing guardian), they are **not paid** until invoiced, so a child nobody billed is not waved through. A plan in arrears does not clear. Each answer carries why: paid in full, payment plan in good standing, cleared by override, nothing to pay, a balance is left, not invoiced yet.

**Schools > Clearance** shows the current term (or any term) by class: each student's status, why, the balance (the page needs `school.read`, so the money is shown), and the override in force. A student's page has the same under **Clearance**. **Download CSV** gives the same rows, with names masked unless shown.

### Overrides

A person with `school.approve` can **clear a student who does not meet the rule**, on the clearance page or the student's page (**Clear by override**), with a **reason** ("bereavement in the family"; required, kept sealed with the override) and the **last day it counts** (the end of the term by default; any day from today to a year on). One override in force per student and term; **withdraw** it to record another. An override **past its end date no longer counts**: the student is back to what the money says. Withdrawing stops it at once. Nothing is deleted: the clearance page lists the term's overrides, in force, ended and withdrawn, with who gave each, and the audit log holds each override (`school.clearance.override`) and withdrawal (`school.clearance.withdraw`).

### Clearance lists

For a class teacher or an exam, the list of who is cleared and who is not, per class: **Schools > Clearance** filtered by class, and its CSV, for people with `school.read` (with the balances). For a teacher's phone, the `school-gate-list` [template](templates.md) sends each class's list with **no amounts**: how many are cleared, and each student not cleared by name and admission number with partly paid or not paid, one message per class to the phone given (the head teacher or exam officer, who hands them on), on the day before resumption at the time chosen, or by hand. The workflow action behind it is `schools@1` `clearance_list`.

### The gate

The person at the gate on resumption morning holds one phone, one-handed, in public. They get **one screen**: the school's name, a search box, and a tap. Type **three letters** of the child's name, tap the child, and the screen says **✓ CLEARED** or **⚠ SEE THE BURSAR** with the child's name and class, in large type, in the console's light or dark look. A short **Just checked** list below (kept in this browser tab only, cleared on sign-out) saves looking the same child up twice in a queue. **No amount ever appears**: not the balance, not a percentage, not how much is left, and the API behind the screen answers only the child's id, name, class and the two words (`GET /v1/gate/search?q=`, at most ten children, and `GET /v1/gate/students/{id}`, `cleared` or `see_bursar`; the document's schemas allow no other field, and a test walks every gate answer for one). Searches are limited per session (a person typing, not a script reading the roll), and each check is in the audit log (`school.gate.check`). The gate is the one place a gate session sees names, since its purpose is to find the child; withdrawn and graduated students are not found there. When no term is current the gate says see the bursar.

A member with the **Gate** role lands on this screen when they sign in, whatever address they type, with no menu; the server refuses everything else (403, `gate_only`), as a test over the whole route table checks. The screen is the same one at **Schools > Clearance > Open the gate screen** (`/schools/gate`) for a bursar, whose session may use the gate routes too. Partly paid is see the bursar; an override in force is cleared; the answer is read live, so a payment posted at the desk a minute ago shows at the gate.

## Overview

**Schools > Overview** (`school.read`; the first tab of the area) is the term's money as it stands, on a phone first: the current term, or any term chosen.

- **Collected against expected**: what the term's sent invoices come to, what was counted towards them and the share, with the line of what was **due by today** (the invoices whose due date has passed) and how much of that is still unpaid.
- **This week**: counted since Monday, Lagos time. **Outstanding**: what is left, and of it what is past due.
- **Matched by itself**: the share of the counted money that came through the automatic path (a credit to the child's own account number, a suggestion taken without a person) against what a person matched (a suggestion confirmed, a match by hand, cash posted at the desk).
- **How parents paid**: by account number, card or POS, cash, a transfer to the school's own account, or other (a payment the money log has no entry for, such as a one-time account's on the invoice page).
- **Collected for others**: what of the collected money reached [pass-through](#pass-through) lines, by body, kept apart from the school's own.
- **Aging** and **top debtors**: the totals by bucket and the five largest balances, from [Debtors](#debtors) (every term's open invoices), names masked unless shown (`pii.reveal`, audited).
- **Cleared**: the term's students by [clearance](#clearance) status.
- **The last close**: its day and whether it reconciled; below, [the daily closes](#the-daily-close).

Nothing on the page is stored: it is read from the invoices, the payments and the record as they stand.

## The daily close

Every night after midnight (Lagos time) Taskiem **closes the day before** for each school with the area on, **exactly once**: the close is a row of `school_daily_closes` keyed by the school and the day, taken before the report is computed and in the same transaction, so a second server, a restart, or a person asking at the same moment finds it rather than making another; and a close is **never recomputed**: what the books said when the day closed is what the proprietor was sent. It says:

- **Money in on students** that day: what landed as students' money, split into credits to their **account numbers** and money posted **at the desk** (cash, a transfer to the school's account, POS).
- **Matched to fees by itself and by a person**: the payments counted that day towards term invoices, by path; and **not matched**: students' money the log holds that no invoice counts yet (held credits, money beyond the fees), each one listed.
- **Found only by the look-back**: the day's credits the [look-back](#the-look-back) recorded before any webhook did.
- **Waiting for a second person**: cash postings and discounts pending under [the second person](#the-second-person), each listed.
- **Families who say they paid**: open [claims](#the-family-page) not yet found.
- **What does not reconcile**: a payment counted as paid into a student's account with no money log entry behind it; a receipt still valid although its payment was undone or went back to the payer; a payment counted towards a term invoice with no receipt. These are checked over everything, not only the day, so a problem stays flagged until it is fixed.

Nothing is swept up: every item carries the page to fix it on (To match, Approvals, the invoice, the receipt), and the close says **reconciles** or **does not reconcile** with how many problems. A close names no student: admission numbers, invoice and receipt numbers only.

**Schools > Overview > The daily close** lists the closes, opens any, and **Close yesterday now** (`school.manage`) takes it before the night does; a day closed already shows the stored close. The `school-daily-close` [template](#templates) sends each morning's close to the owners. Over the API: `POST /v1/schools/close` (`{day}`, yesterday by default; a day not over, or more than a year back, is refused; audited as `school.close.run` when the call took it), `GET /v1/schools/closes?from&to` and `GET /v1/schools/closes/{day}`; `schools@1` `daily_close` answers the same with its text.

## Summaries

When the area is on and a term is current, the [daily money summary](money-reports.md) on chat and the monthly email gain a schools line, in the five languages the summary speaks: what was **collected for fees** in the period, what is **outstanding** on the term's invoices now, and the share of the term's students **cleared**. The exports carry the students' columns (student, class, term) and pass-through apart since the fees were built.

## Templates

Seven [templates](templates.md#the-library) run a school's year with `schools@1` and Tell me; each is offered only for a goal that names a school, its fees, its pupils or students, so it never takes an invoice template's place:

| Template | When | Does |
| --- | --- | --- |
| `school-issue-accounts` | Every 15 minutes, and by hand for everyone when first turned on | Issues each active student without an account number their own (`list_students` with `without_account`, then `issue_account`, exactly once per student), and sends the family's first guardian the number, the bank and a fresh fees page link |
| `school-invoice-term` | On the day and month chosen (the start of term), and by hand | Invoices the current term once per enrolled student (`invoice_term`), sends each family its invoice with its fees page link, and tells the bursar the counts and who was left out |
| `school-fee-reminders` | Two chosen days a week | [The ladder](#the-ladder), at most 50 per run |
| `school-receipt` | The `invoice` trigger's `payment` event, for term invoices only (the body carries a `receipt`) | Sends the family the receipt's number, the amount, its check link and a fresh fees page link, once per payment |
| `school-plan-decided` | Every 15 minutes | Tells families of [plan decisions](#payment-plans) they were not told of |
| `school-daily-close` | Every morning at the time chosen | Takes [yesterday's close](#the-daily-close) (`daily_close`) and sends it to the owners, the title flagging a day that does not reconcile |
| `school-gate-list` | The day before resumption | [The teachers' lists](#clearance-lists), no amounts |

The Start page lists them for a business with the area on (and offers an owner to turn it on); the AI builder knows the record ([AI](ai.md#what-it-does)).

## In a workflow

The **Schools** platform connector (`schools@1`, no connection needed) works on the business's own record only, and only while the area is on:

| Action | Does |
| --- | --- |
| `list_students` | Students by admission number, optionally of one `class` (its id or label, such as `JSS 1 A`) and `status` (`active` by default, `any` for all), with `without_account` only those with no account number issued on the issuing connection, at most `limit` (default 200, up to 1,000): id, admission number, name, class, status and family |
| `get_student` | One `student` by id or admission number, with their family's guardians (name, phone, email, relationship) |
| `list_classes` | The classes in their order, with how many active students each has |
| `current_term` | The current term (`found: false` when none runs), its session and dates |
| `list_fee_items` | A `term`'s fee structures (the current term by default), optionally of one `class`: each item's id, name, `amount_kobo`, kind and who it is collected for |
| `term_invoices` | A `term`'s invoices (the current term by default), optionally of one `class` and `status`, at most `limit`: number, status, student id, admission number, class, `total_kobo`, `paid_kobo`, `due_kobo`. No names |
| `issue_account` | [Issues a `student`'s account number](#issuing) (by id or admission number) on the issuing connection, exactly once per student and connection: running the step again, or for a student who has one, answers the same account (`account_number`, `bank_name`, `account_name`, `reference`, `connection`). `bvn` or `nin` is a parent's, for Flutterwave. The rail's refusal fails the step with its reason |
| `invoice_term` | [Invoices the term](#invoicing-a-term) (the current term by default): the plan and its confirmation in one, exactly once for the step (its retry answers what the first call did), and with `send` sends the term's drafts. Answers the counts: enrolled, to invoice, created, already invoiced, the total and pass-through, those left out and why, sent and failed |
| `family_link` | A fresh link to [a family's page](#the-family-page), for a `family` (its id) or a `student`'s family (id or admission number): `link` and `expires_at`. Put it in a `notify@1` `message` or an `invoices@1` `notify_customer` |
| `list_debtors` | [Debtors](#debtors) with their aging, optionally of one `class`, one `bucket` or those that `has` a promise, a missed promise, a plan or a plan in arrears: each student's balance, days past due, bucket, last rung, promise and plan state, and the totals by bucket. No names |
| `send_reminders` | [Sends reminders](#the-ladder) to `students` (ids or admission numbers, at most 50) or, without them, to the debtors past due (of one `class`), oldest due first, at most `limit` (50), at the `rung` given or `auto` by days past due; exactly once for the step. Answers `sent`, `skipped`, `by_rung` and each student's outcome with the reason skipped |
| `plan_decisions` | Plan requests decided in the last `since_days` (7), `untold` (the default) keeping those whose family was not told yet, with the instalments as decided and the note |
| `tell_plan_decision` | Tells the family of a decided `plan` the decision with a fresh link, exactly once per decision whoever tells first; `told_before` when nothing was sent |
| `clearance` | [Where a student stands](#clearance) for a `term` (the current one by default): one `student` (id or admission number), or every student of a `class`, with `status` (cleared, partly_paid, not_paid), `reason`, and the balances |
| `clearance_list` | [The teachers' lists](#clearance-lists): per class of the `term` (one `class`, or every class), the counts, who is not cleared by name and admission number, and a `text` ready to send, with no amounts |
| `daily_close` | [The close](#the-daily-close) of a `day` (yesterday by default), taken now when it was not yet, else as stored: the sums, the lists of what waits and what does not reconcile (each with its page), and a `text` ready to send, with `link` (the web app's address) before each page. No names |

Names, phones and emails are declared personal data, so they are sealed in the run's history like any personal data. [Seven templates](#templates) use these.

## API

Under `/v1/schools/`: `classes`, `sessions` (with `sessions/{id}/terms` and `sessions/{id}/promotion`), `terms/{id}` (with `start`, `enrollments`, `fees`, `fees/{class}` and `fees/{class}/copy`, `invoice` and `invoices`), `students` (with `students/import` and `students/{id}`, by id or admission number, and `students/{id}/fees`, `students/{id}/options` and `students/{id}/discounts`) and `families` (with `families/{id}/guardians`). Account numbers: `students/{id}/accounts` (a student's, `?reveal=true` for the account's name) and `students/{id}/account` (issue one: `{bvn, nin, retry}`; 422 `rail_refused` with the rail's reason), `accounts/issue` (`{}` for the plan, `{confirm: <the plan's hash>}` to start the batch, 202) and `accounts/jobs` (with `accounts/jobs/{id}`, a batch's progress and who was not issued and why). Invoicing a term takes `{}` for the plan and `{confirm: <the plan's hash>, send}` to invoice it (its answer counts `requested`, the students held for a discount and sent to a second person). Amendments are `POST /v1/invoices/{id}/amendments` ([invoices](invoices.md#amending-a-sent-invoice)). Cash and transfers: `POST /v1/schools/payments` (`{student, amount_kobo, how, date, note}` with an `Idempotency-Key`; 201 with the `posting`, its parts and receipts, or 202 `code: needs_approval` with the `request`) and `GET /v1/schools/payments` (`?status=`, `?kind=`, `?student=`). Requests for a second person: `GET /v1/schools/approvals` and `POST /v1/schools/approvals/{id}` with `{decision: "approved" | "declined", reason}` (`school.approve`, people only, never the requester). Receipts: `GET /v1/schools/receipts` (`?student=`, `?term=`, `?invoice=`, `?number=`), `GET /v1/schools/receipts/{id}` (by id or number) and `GET /v1/schools/receipts/{id}/pdf`. The family page: `GET /v1/schools/claims` (`?status=` open by default, `any`, `matched`, `dismissed`; `?student=`, `?family=`) and `POST /v1/schools/claims/{id}/resolve` with `{reason}`; `GET /v1/schools/plans` (`?status=` requested by default) and `POST /v1/schools/plans/{id}` with `{decision: "approved" | "declined", instalments, note}` (`school.approve`, people only); `POST /v1/schools/families/{id}/link` with an optional `{message}` answers the `link`, `expires_at` and the `channels` it went on; `GET /v1/money/unmatched` lists the open `claims` beside the money. Chasing: `GET /v1/schools/debtors` (`?class=`, `?bucket=`, `?has=`, `?format=csv`; `debtors`, `totals` by bucket, `students`, `balance_kobo`), `POST /v1/schools/reminders` (`{students: [ids, at most 50], rung: 1..3}` with a required `Idempotency-Key`; `sent`, `skipped`, `by_rung`, `results` with each student's `status` and `reason`, `replayed` for a key seen before), `GET /v1/schools/students/{id}/chasing` (the balance and aging, `promises`, `reminders`, `plan` standing and `arrears`), `POST /v1/schools/students/{id}/promise` (`{date, note}`, 201; 409 for nothing due or a promise open already), `GET /v1/schools/promises` (`?student=`, `?status=`), `POST /v1/schools/promises/{id}/withdraw`, and `POST /v1/schools/chasing/check` (runs the check now: `promises_kept`, `promises_missed`, `arrears`). Clearance: `GET /v1/schools/clearance` (`?term=` the current term by default, `?class=`, `?format=csv`; `students` each with `status`, `reason`, `balance_kobo` and the `override` in force, and `totals`), `GET /v1/schools/students/{id}/clearance` (`?term=`; the `clearance` and the student's `overrides`), `POST /v1/schools/students/{id}/clearance-override` (`{reason, ends_on, term_id}`, `school.approve`, 201; 409 for a second in force), `GET /v1/schools/clearance-overrides` (`?student=`, `?term=`, `?in_force=true`) and `POST /v1/schools/clearance-overrides/{id}/withdraw` (`school.approve`). The proprietor: `GET /v1/schools/overview` (`?term=` the current term by default, `?reveal=true`; `overview` with the sums, `how_paid`, `matched`, `pass_through`, `top_debtors`, `aging`, `clearance` and `last_close`), `POST /v1/schools/close` (`{day}`; `close` and `taken_now`), `GET /v1/schools/closes` (`?from=`, `?to=`) and `GET /v1/schools/closes/{day}`. The gate: `GET /v1/gate/search?q=` (three letters at least; `students` with `student_id`, `name`, `class` and nothing else, at most 10; 429 when a session searches too fast) and `GET /v1/gate/students/{id}` (`status` `cleared` or `see_bursar`, `name`, `class`), open to `school.gate` and `school.read`; a gate session is refused every other route. The switch is `GET` and `PUT /v1/settings/schools` (with `discount_threshold_naira`, `cash_threshold_naira` (`null` for none, 0 for every posting), `family_link_days` (1 to 30), `reminder_wording` (`{"1": …, "2": …, "3": …}`, `""` for the default), `plan_grace_days` (0 to 60) and `issuing`: `{connection, account, siblings_share_credit}`, the owner's; `GET` also lists the `issuers`, the `reminder_defaults` and the `reminder_placeholders`), and `GET /v1/me` says whether the area is on (`schools`). Lists mask personal data unless the caller holds `pii.reveal` and asks with `?reveal=true`. The import takes `{csv}` for the plan and `{csv, confirm: <the plan's hash>}` to save it; a file with lines that cannot be taken answers 422 with `code: import_unreadable`, `problems` (each line and why) and `total`. The full description is in the API reference (`api/openapi/openapi.yaml`, tag Schools).
