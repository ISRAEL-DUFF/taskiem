# Schools

Schools is an area any Taskiem business can turn on: the school's students, their families, its classes, sessions and terms, beside Invoices and Money. It is the record the rest of the Schools work hangs on ([decision 0046](decisions/0046-balans-in-taskiem.md), [the plan](spec/schools.md)): term fees as invoices, each student's own account number, receipts, a page for each family, chasing, clearance and the gate come in later steps. This page covers what is there now: turning it on, who can do what, the record, getting students in, and promotion.

## Turning it on

An owner turns Schools on under **Secrets & settings** (or **Schools > Settings**). It appears in the menu for everyone who may read the school's record (`school.read`). Turning it off hides it again and its pages and API answer that it is off; nothing is deleted, and turning it back on brings everything back as it was. Only an owner can turn it on or off, and each change is recorded in the audit log.

When an owner turns it on, Taskiem can also add a **Bursar** role (below) for whoever runs the fees office, when the plan has custom roles and the business has no role named `bursar` yet.

## Who can do what

| Permission | Lets a person | Built-in roles |
| --- | --- | --- |
| `school.read` | See students, families, classes and terms (and, as they arrive, fees, payments, debtors and clearance) | Owner, admin, operator, auditor, viewer |
| `school.manage` | Add and change students, families, classes, sessions and terms; import students; start a term; promote a session | Owner, admin, operator |
| `school.approve` | Approve cash and discounts above the school's thresholds, decide payment plans, override clearance (as those arrive) | Owner, and custom roles given it |
| `school.gate` | The gate screen only | Gate |

- **Bursar** is a custom role with `school.read`, `school.manage`, `invoice.read` and `invoice.manage`. Give it to people under **Members & keys**, and change it under custom roles like any other.
- **Gate** is a built-in role for whoever stands at the school gate. It has `school.gate` and nothing else, and the server refuses a gate session every page and API route but who it is and signing out (403, `gate_only`): nothing is merely hidden. The gate screen itself, a name search answering **Cleared** or **See the bursar** and never an amount, comes with clearance. Only an owner can give someone the gate role, as it takes holding `school.gate`.

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

Taskiem copies the school's students and classes to two [kept tables](data-tables.md#kept-tables) in the business's data tables, so templates and workflows can read them like the money log: `school_students` (student id, admission number, name in a personal column, class and class id, status, family id, when it last changed) and `school_classes` (class id, name, arm, label, order). Each row follows its record within a minute or so of a change. No person or workflow can add, change or delete their rows; the record changes only on the Schools pages and through the API.

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

## In a workflow

The **Schools** platform connector (`schools@1`, no connection needed) reads the business's own record only, and only while the area is on:

| Action | Does |
| --- | --- |
| `list_students` | Students by admission number, optionally of one `class` (its id or label, such as `JSS 1 A`) and `status` (`active` by default, `any` for all), at most `limit` (default 200, up to 1,000): id, admission number, name, class, status and family |
| `get_student` | One `student` by id or admission number, with their family's guardians (name, phone, email, relationship) |
| `list_classes` | The classes in their order, with how many active students each has |
| `current_term` | The current term (`found: false` when none runs), its session and dates |

Names, phones and emails are declared personal data, so they are sealed in the run's history like any personal data. It changes nothing: actions that do (invoicing a term, issuing account numbers) come with fees and accounts.

## API

Under `/v1/schools/`: `classes`, `sessions` (with `sessions/{id}/terms` and `sessions/{id}/promotion`), `terms/{id}` (with `start` and `enrollments`), `students` (with `students/import` and `students/{id}`, by id or admission number) and `families` (with `families/{id}/guardians`). The switch is `GET` and `PUT /v1/settings/schools`, and `GET /v1/me` says whether the area is on (`schools`). Lists mask personal data unless the caller holds `pii.reveal` and asks with `?reveal=true`. The import takes `{csv}` for the plan and `{csv, confirm: <the plan's hash>}` to save it; a file with lines that cannot be taken answers 422 with `code: import_unreadable`, `problems` (each line and why) and `total`. The full description is in the API reference (`api/openapi/openapi.yaml`, tag Schools).
