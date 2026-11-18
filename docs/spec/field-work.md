# Field work: jobs done at customers' places, the staff who do them, and where they are

Status: **Draft** 2026-11-18, for review, not scheduled · Decision: [0070](../decisions/0070-field-work-and-isps.md) (proposed) · Workstream **F0 to F9**, one pull request per milestone · Written with the founder · First business to use it: an internet provider ([the ISP area](isps.md)) · Replaces: ISPOps, the founder's field-agent system (backend, admin portal, field app, customer app), absorbed here and retired; its repositories and where each feature's implementation is: [§20.1](#201-where-to-find-it-in-ispops) · Worked examples, one job followed end to end per scenario: [field work examples](field-work-examples.md)

A business whose staff go out to customers (an internet provider installing and repairing lines, a solar installer, a CCTV company, a generator or air-conditioner repairer, a cleaning firm) needs the same things whatever it sells:

- jobs made by the office;
- each job given to a person in the field, who goes, arrives, works and finishes;
- the office seeing where everyone is and how each job is going;
- the customer told who is coming and when, and asked how it went;
- the parts each person carries, and what each job used;
- hours and jobs counted for pay.

Field work is that, as an **area** any business can turn on, as Schools, Stock and Money are ([business areas](business-areas.md)). It is general: nothing in it is about internet providers. The [ISP area](isps.md) turns it on and adds what only a provider needs.

Contents:

1. [Summary](#1-summary)
2. [Words used here](#2-words-used-here)
3. [Where we are](#3-where-we-are)
4. [The area and its settings](#4-the-area-and-its-settings)
5. [Who can do what](#5-who-can-do-what)
6. [The job](#6-the-job)
7. [The journey of a job](#7-the-journey-of-a-job)
8. [Each step is a trigger](#8-each-step-is-a-trigger)
9. [Assigning](#9-assigning)
10. [Agents' states, and where people are](#10-agents-states-and-where-people-are)
11. [The app for people in the field](#11-the-app-for-people-in-the-field)
12. [Working without a connection](#12-working-without-a-connection)
13. [Parts carried and used](#13-parts-carried-and-used)
14. [The customer's side](#14-the-customers-side)
15. [The office's side: web and chat](#15-the-offices-side-web-and-chat)
16. [Watching for trouble](#16-watching-for-trouble)
17. [Hours, jobs and pay](#17-hours-jobs-and-pay)
18. [Privacy and safety](#18-privacy-and-safety)
19. [Data and server changes](#19-data-and-server-changes)
20. [What comes from ISPOps](#20-what-comes-from-ispops)
21. [Milestones](#21-milestones)
22. [Testing](#22-testing)
23. [Risks and open questions](#23-risks-and-open-questions)
24. [Out of scope](#24-out-of-scope)

---

## 1. Summary

| Need | In Taskiem | Kind |
| --- | --- | --- |
| Turn it on for one business | The **Field work** area (§4) | Area |
| People in the field are staff | Members with the built-in **Field agent** role, which sees its own jobs and nothing else (§5) | Role |
| A job with a customer, a place, a type, a checklist | The job record (§6) | Record |
| Assigned → started (on the way) → arrived (on site) → ended (done) → closed | The journey of each job, kept by the engine with a time and a place at each step (§7) | Record |
| "When X happens to a job, do Y" | Each step starts workflows: the `job` trigger, `jobs@1`, signals for runs that wait (§8) | Platform |
| Give jobs to people | By hand, in bulk, by suggestion, or by a workflow (§9) | Platform |
| Know where people are | Location during each job, from Start job to End job (the default), or off, as the business chooses; agents' states from their jobs; a live map (§10) | Platform |
| Work from the field | The Taskiem app, in a field mode, the only place a field agent works (§11) | App |
| No signal at the site | Steps, notes, checklists and photos queued on the phone with their own time and place (§12) | App |
| Parts in the van | Stock held by each agent: issued, used on a job, returned (§13) | Stock, extended |
| The customer knows who is coming | A job card in the customer's conversation, "on the way" with a live map, then a rating (§14) | Chat |
| The office's view | Field work on the web: board, map, jobs, agents, reports; and `@Taskiem` in chat (§15) | Web, chat |
| Late and stuck jobs | Alerts for overdue, too long on site, no location, done far from the place (§16) | Alerts |
| Pay | Jobs done and time on jobs, to Payrolla or a pay list (§17) | Templates |

**The job is the unit of everything.** There is no working day, shift or duty to start and end: a field agent holds any number of assigned jobs and works them one at a time, each started and ended on its own, as in ISPOps. An agent's state, their location and their time are all read from their jobs.

Three choices shape everything else, and are decision [0070](../decisions/0070-field-work-and-isps.md):

1. **Field work is a general area**, and the ISP area builds on it.
2. **The journey is a record, and its steps are triggers.** The engine keeps the job's state, with the rules on who may move it, the time and place of each step, and safety when offline. What happens around each step (telling the customer, an invoice, a rating, an alert) is workflows the business chooses and changes. A business cannot break its own job states, and can shape everything around them.
3. **Field agents work from the Taskiem app only.** Chat apps cannot give the location the office needs, so there is no WhatsApp or Telegram way of working a job in this plan.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Field work | The area. The owner's words; the code's are `fieldwork` (area) and `jobs` (records) |
| Job | One piece of work at one place for one customer: "Install fibre at 12 Allen Avenue". Numbered `J-1042`, per business |
| Field agent | A member of the business who goes out to do jobs. The role's name; a business may call them technicians, installers or engineers in its own words (§4) |
| Office | Everyone who makes, assigns and watches jobs: admins, dispatchers, customer care. Not a role, a way of speaking |
| Dispatcher | A member who makes and assigns jobs (`job.manage`) |
| Job type | What kind of job: installation, repair, survey. The business's own list |
| Template | A job type's ready-made description, checklist and expected time |
| Step | One move in a job's journey. The field agent's three are **Start job** (on the way), **Arrived** (on site) and **End job** (done) |
| In execution | A job that is started and not yet ended: on the way or on site. An agent has at most one. A job on hold is not in execution |
| Agent's state | Read from their jobs: **available** (holds none), **assigned** (holds some, none started), **on the way**, **on site** |
| Trail | The places a field agent's phone reported during one job, from Start job to End job, in order |
| Visit window | A time the customer says they are in for a visit |
| With an agent | Stock issued to a field agent and not yet used or returned: the parts in their van |

## 3. Where we are

| Exists | Where | Used here for |
| --- | --- | --- |
| Areas: switchable parts of the product, refused where they are used when off | `engine/areas`, [business areas](business-areas.md) | The Field work area (§4) |
| Members, custom roles, permissions, approvals | [Governance](../governance.md) | Field agent and dispatcher roles (§5) |
| A role that sees one screen and nothing else, refused by the server everywhere else | The Schools **Gate** role (`access.GateOnly`, `api/auth.go`), [schools](../schools.md#who-can-do-what) | The Field agent role (§5) |
| A delivery's steps tapped by the person doing it, each telling the customer, a workflow waiting on a signal | Dispatch: Picked up, Arrived, Delivered; `storefront.delivered` ([dispatch](../dispatch.md)) | The pattern for the journey and its signals (§7, §8) |
| Area triggers and platform connectors | `invoice` and `link` triggers; `riders@1`, `stock@1`, the schools connector | The `job` trigger and `jobs@1` (§8) |
| Stock: items, movements under idempotency keys, a count per item | [Stock](../stock.md) | Stock held by agents (§13) |
| Photos in the business's private storage, scanned, cleaned of metadata | [Media](../media.md) | Job photos (§11) |
| The phone app: a chat app for people and businesses, sign-in by phone, push through Firebase, an encrypted store and an outbox for messages written offline, location asked for when needed (`geolocator`) | [Mobile app](mobile-app.md), [explore and offline](mobile-app-explore-and-offline.md), PR [#198](https://github.com/ISRAEL-DUFF/taskiem/pull/198) | The field mode (§11, §12) |
| Customers as people with their own accounts, one conversation per business, cards for orders and deliveries, the customer door | PR [#198](https://github.com/ISRAEL-DUFF/taskiem/pull/198) (Taskiem for everyone, stages 1 to 4) | The customer's job card and visit booking (§14) |
| Updates by WhatsApp and SMS to people not on Taskiem | [Updates](../updates.md) | Customers without the app (§14) |
| Alerts | [Alerts](../alerts.md) | Late and stuck jobs (§16) |
| Payrolla and its templates | [Payrolla](../integrations/payrolla.md) | Pay (§17) |
| Kept tables: a record copied into the business's project for workflows to read | [Data tables](../data-tables.md#kept-tables) | `jobs` as a kept table (§19) |

**What does not exist:** a job; any idea of staff working away from the office; location of staff; stock held by a person; a trigger for anything a member does in the field. Dispatch riders are not the answer: a rider is a person **outside** the business, reached by chat, and is becoming a business of their own ([0065](../decisions/0065-riders-are-organisations.md)); a field agent is staff, works only for this business, is located during its jobs and carries its stock.

**ISPOps** (four repositories, used by one business, which starts afresh: there is nothing to migrate) has most of this as a separate product. §20 says what comes over and what is dropped.

## 4. The area and its settings

**Field work** joins `engine/areas`: "Jobs your staff do at customers' places, who does them, where they are and what they used." It depends on no other area. Stock is used when it is on (§13), and Money when a workflow invoices a job.

Turning it on (owner only, as every area) does three things:

1. shows **Field work** in the menu of everyone with `job.read`;
2. adds the **Dispatcher** custom role (§5) if the plan has custom roles and the business has no role of that name;
3. makes the `job_types` and `job_templates` tables in the business's project, with the defaults of the business's kind (the ISP area brings its own, [ISP §4](isps.md#4-the-isp-area)), or three general ones (installation, repair, inspection) otherwise.

Turning it off hides it and refuses its routes, steps, chat words and app screens, as every area. Jobs in flight stay as they are; a field agent's app says "Field work is off for this business" and stops collecting location at once.

**Settings** (Field work > Settings, `job.manage`):

| Setting | Default | What it does |
| --- | --- | --- |
| What we call field agents | "Field agent" | The word in the office's screens and in messages to customers ("technician", "installer") |
| Collect field agents' location | During jobs | **During jobs**: from Start job until End job, Could not finish, Hold or Cancel, and never between jobs. **Off**: no location at all, steps included. What off turns off is in §10. Changed by the owner only, audited, and every field agent is told |
| Arrived means within | 300 metres | How close Arrived must be to the job's place to count as at the place; farther is allowed and marked (§7) |
| A photo is needed to end a job | Off | End job needs at least one photo, as ISPOps' completion-photo setting |
| Customer's confirmation to end a job | Off | End job needs the customer's OK in the app, in their chat, or a code they read out (§14) |
| Late after | 30 minutes past the window | When a job not yet arrived is late (§16) |
| No time chosen after | 4 hours | When a customer asked for a time and has not answered is an alert (§14.1) |
| Too long on site after | The template's expected time, plus half; 3 hours when the job has no expected time (ISPOps' overtime threshold) | When a job on site is flagged (§16) |
| Messages to customers not on Taskiem | SMS | SMS, or email for customers with an email address (ISPOps sent email by default); the business's SMS sender is its Taskiem messages setting, not a key typed here as ISPOps' Termii fields were |
| Location every | 30 seconds when moving, 5 minutes when still | How often the phone reports during a job (§10) |
| Keep trails for | 90 days | Then deleted; steps keep their own place (§18) |

## 5. Who can do what

| Permission | Lets a person | Built-in roles |
| --- | --- | --- |
| `job.read` | See jobs, the board, the map, agents' states and trails, reports | Owner, admin, operator, auditor, viewer |
| `job.manage` | Make, change, assign, hold, cancel and close jobs; change settings, job types and templates; issue and take back stock from agents | Owner, admin, operator |
| `job.work` | The field mode in the app: their own jobs and their stock | Field agent |

- **Field agent** is a **built-in** role holding `job.work` and nothing else, refused everywhere else by the server, as the Gate role is: their own jobs (assigned to them, or done by them in the last 30 days) and their own stock; who they are; signing out. Nothing is merely hidden. A field agent who is also something else in the business (a supervisor who goes out too) holds both roles, and `job.work` then only adds the field mode.
- **Dispatcher** is a custom role with `job.read` and `job.manage`, changed like any custom role.
- Custom roles that bundle Field work with other parts (customer care, cashier) are the vertical's to offer ([ISP §8](isps.md#8-staff-and-roles)).
- A field agent is a member, added under **Members & keys** by phone or email as any member is ([governance](../governance.md)). Signing in is the person's own (PR #198: phone, passkey, the business's rules); the field mode opens when the business they switch into gives them `job.work`.

Every change to a job, assignment, step, setting and stock with an agent is in the business's audit chain with who made it.

## 6. The job

**A job is a record with rules** ([0064](https://github.com/ISRAEL-DUFF/taskiem/blob/claude/core-cleanup-0ak3hh/docs/decisions/0064-business-records-out-of-the-core.md), proposed: core tables in the module's own schema, a kept copy for workflows).

| Field | What it is |
| --- | --- |
| Reference | `J-` and a number, per business, in order |
| Type | One of `job_types`; brings the template's description, checklist and expected time when made |
| Priority | Low, normal, high, urgent |
| Customer | Name, phone and the place, kept on the job whether or not the customer is saved; and, when they are, a link to their row of the business's `customers` table (§6.1). Sealed and masked as personal data |
| Place | An address, a landmark, and a map pin (latitude and longitude), as ISPOps' site latitude and longitude. The pin is what Arrived is measured from; without one, Arrived is not measured |
| What to do | Description; the checklist (items, each with a note or photo it may require) |
| When | When it is scheduled for: a visit window (from, to), or "as soon as possible"; expected time on site. An agent's assigned jobs are their schedule, earliest first |
| Who | The assigned field agent. A job has one at a time; reassigning before it is started keeps the history of who held it (ISPOps' `agent_assignments`) |
| State and steps | §7 |
| Notes | From the office and from the field, each with who and when |
| Photos and files | Taken in the app or added in the office, in the business's private storage |
| Parts used | Items and quantities, from the agent's stock (§13), or free text when Stock is off |
| Customer's confirmation and rating | §14 |
| Came from | The office, a workflow, a schedule, a customer's booking, a complaint (the ISP area) |
| Links | The customer's conversation; an order, invoice or ticket it belongs to |

### 6.1 The business's own customer list

A business keeps **its own list of its customers** in its `customers` table, a known table in its own project ([data tables](../data-tables.md)), which it can search, edit, export and use in workflows like any table. The list is the business's: **a customer is on it because the business chose to save them**, never because Taskiem copied someone in. However the business deals with a person (they chat with it in the app or on WhatsApp under PR #198, they ordered, they phoned the office), that stays as it is; saving them to the list is a separate act, which is always available:

- **on the New job form**, **Save to my customers**, ticked by default, for a customer typed in (§15.1);
- **in a customer's conversation**, **Save to my customers** in the conversation's menu, which fills a new row with the name and phone the business has for them, to be checked before saving;
- **on the table's own page**, by adding a row, or by importing a file, as any table.

A saved customer's row is what the New job search finds, and links their jobs: the row's page lists the customer's jobs, and a job's page links to the row. For Field work the table gains two columns, **address** and **location** (a map pin), so a saved customer's place comes with them; the earlier jobs' places are offered too. Changing the row changes nothing on jobs already made: each job keeps the name, phone and place it was made with.

A job reaches a customer the same way whether they are saved or not: in their conversation with the business if they have one (PR #198), else by SMS ([updates](../updates.md)).

**Job types and templates are lists**, kept as data tables in the business's project (`job_types`, `job_templates`), so an owner edits them like any table and the AI builder reads them. A template's checklist is one line per item ("Test signal | photo", "Customer shown how to use the router"). Recurring jobs are **not** a part of the record: a workflow on a schedule calls `jobs.create` (§8), as monthly invoices are made ([business areas §9](business-areas.md#9-a-service-business-as-the-test-an-isp-or-cloud-provider)).

## 7. The journey of a job

```
new ──Assign──▶ assigned ──Start job──▶ on the way ──Arrived──▶ on site ──End job──▶ done ──Close──▶ closed
     (office)             (agent)                 (agent)             (agent)               (office)

Reassign (office):        new, assigned or on hold ──▶ assigned to someone else
Hold (office):            any state before done    ──▶ on hold   (frees the agent)
Resume (office):          on hold                  ──▶ assigned  (the agent starts it again)
Could not finish (agent): on the way or on site    ──▶ not finished
Cancel (office):          any state before done    ──▶ cancelled
```

The same journey as ISPOps (`draft` → `assigned` → `en_route` → `in_progress` → `completed` → `closed`, with `on_hold` and `cancelled`), with ISPOps' names for the agent's taps changed to the owner's words: **Depart** is **Start job**, **Clock in** is **Arrived**, **Clock out** is **End job**. Two are added: **Could not finish**, and **Resume** for a job on hold (ISPOps has no way out of `on_hold` but Cancel). One rule changes: in ISPOps a job on hold still counts as the agent's one job in execution, so a held job stops its agent working; here **holding a job frees its agent**.

| Step | Who | What is recorded | Rules |
| --- | --- | --- | --- |
| Assign | Office, or a workflow | Who, by whom | To field agents only, and only while the job is new, assigned or on hold: once started (on the way or on site) it cannot be reassigned (ISPOps' `assignable`, with on hold added). An agent may hold any number of assigned jobs; assigning many at once is all or nothing |
| Start job | The assigned field agent | Time on the phone and at the server; the phone's place and its accuracy (unless location is off) | Only from assigned, and only the job's own agent. **One job in execution per agent**: while another is on the way or on site, Start job is refused with "End J-1041 first" (ISPOps' `jobs_one_in_execution_per_agent_idx`, without on hold). **Before the job's window begins**, the app asks "J-1064's window is 1 to 4. Start now?" with **Start now** and **Not yet**: it never refuses (the customer may have agreed an earlier time by phone), and a job started early is marked **started early** in its history and on the office's board. Location for the job starts here |
| Arrived | The field agent | The same; the distance from the job's place | Only from on the way. Farther than the setting is allowed and marked **away from the place**. The work begins: time on site counts from here |
| End job | The field agent | The same; checklist answers, photos, notes, parts used, the customer's confirmation when asked for; time on site (from Arrived) | Only from on site. Refused until the checklist's required items, the photo setting and the confirmation setting are met; the app says which. Location for the job stops here |
| Could not finish | The field agent | A reason from a short list (customer not in, no access, parts missing, needs another visit, other) and a note | From on the way or on site. Ends the job's execution, so the agent can start their next; the office then reschedules or cancels it, and a workflow may make the follow-up job |
| Hold, Resume | Office | Who, why; whether the customer was told, and what | Hold from any state before done, with a reason for the office ("no mast clamp in the van") and **Tell the customer**, ticked by default, with the words they will get, filled from the reason's kind and editable ("We need a part for your visit and will come back with it. We'll tell you when."). **The agent is freed**: the job is no longer in execution, its location stops, and the agent may start another job. A held job stays on its agent's schedule, marked held, and can be reassigned. **Resume** returns it to **assigned** (to whoever holds it then), with its window set again or left as it was, and the agent taps Start job again when they go back; the customer is told the new time as on assigning (`job-tell-customer-assigned` on `resumed`); the steps before the hold stay in its history, and time on the job counts both stretches |
| Cancel | Office | Who, why | Any state before done. Frees the agent |
| Close | Office | Who | Only from done, after review. A business may close on End job by a setting; then Close is automatic |

- **The engine keeps the state.** A step is one write, under the step's own idempotency key from the phone (§12), so a step sent twice lands once.
- **Both times are kept.** The phone's time is what happened; the server's is when it was heard (ISPOps' `device_timestamp` and `server_timestamp`). Times on a job (travel: Start job to Arrived; on site: Arrived to End job) use the phone's, and say when a step came in late from offline.
- **Every step is in the job's history** (ISPOps' `job_status_history`), with who, when and why.
- **Nothing moves a job backwards** but Hold and Resume. A wrong step is corrected by the office with a note, recorded as a correction, never by editing the step.

## 8. Each step is a trigger

The steps are not workflows; what happens **because of** them is.

**The `job` trigger** (`wd/v1` gains the type, a contract change under 0070):

```json
{ "type": "job", "config": { "on": ["arrived", "ended"], "types": ["installation"] } }
```

`on` is one or more of `created`, `assigned`, `started` (on the way), `arrived`, `ended` (done), `not_finished`, `on_hold`, `resumed`, `cancelled`, `closed`, `late`, `too_long`, `rated`, `time_asked`, `time_chosen` and `no_time_chosen` (§14.1). `types` filters by job type. `trigger.body` has the job as its kept copy shows it, the step, and who did it. The customer's personal fields reach a run sealed, as with every trigger.

**Signals.** A run waiting on a job (a workflow that assigns a job and then waits for it to be done) waits for `job.<step>` with the job's reference as correlation, as dispatch's `storefront.delivered` is waited for.

**`jobs@1`**, the platform connector, acting on the run's own business:

| Action | Class | What it does |
| --- | --- | --- |
| `create` | idempotent_write | Makes a job (type, customer, place, window, description, priority), keyed by the run's step key |
| `get`, `list` | read | One job, or jobs by state, type, agent, customer, day |
| `assign` | idempotent_write | To an agent by id, or `suggest` (§9) |
| `hold`, `resume`, `cancel`, `close` | idempotent_write | As the office would |
| `add_note` | idempotent_write | A note on the job, from the workflow |
| `agents` | read | Field agents with their state (available, assigned, on the way, on site), their jobs, and their last place with its age |

**Templates** that come with the area (each with workflow tests, as the subscription recipes have):

| Template | Trigger | What it does |
| --- | --- | --- |
| `job-tell-customer-assigned` | `job` assigned, resumed | "Musa will visit on Tuesday between 10 and 12" to the customer, in their conversation or by SMS |
| `job-tell-customer-on-the-way` | `job` started | "Musa is on the way" with the live-map link (§14) |
| `job-ask-rating` | `job` ended | The rating card, or a link by SMS (ISPOps sent its rating email on every clock-out; here it is a template the business can change) |
| `job-invoice-on-done` | `job` ended | An invoice for the job type's price, with the parts used as lines (Money on) |
| `job-late-alert` | `job` late | Tells the dispatcher, and the customer if they chose |
| `job-tell-customer-held` | `job` on_hold | The words the office approved at Hold (§7), in the customer's conversation or by SMS; nothing when **Tell the customer** was unticked |
| `job-follow-up-not-finished` | `job` not_finished | A new job for "needs another visit", the same customer and place |
| `job-recurring-visit` | `schedule` | A job every month (or week, or year) for each row of a table: maintenance contracts |
| `job-daily-summary` | `schedule` | The day's jobs done, late, not finished, by agent, to the owner |
| `job-tell-office` | `job` (every step) | ISPOps told staff at every step (created, assigned, departed, clocked in, clocked out, cancelled); here the office's notifications are this template, on by default, so a business can quiet it |

The AI builder knows the trigger, the connector and the templates ("tell my customers when the technician is on the way", "a maintenance visit every three months for each customer on the contracts table").

## 9. Assigning

- **By hand:** on the job, on the board (drag to an agent), or many at once from the list. In chat: `@Taskiem assign J-1042 to Musa`.
- **Suggestions** when assigning (ISPOps' available agents, improved): agents **available** first, then **assigned** ones by how many jobs they hold for that day; within each, nearest first by their **last known place**: where their most recent job's last report or step was, shown with its age ("near J-1041, 40 minutes ago"). Nothing is collected between jobs to make this; with location off, there is no distance. A suggestion is a list, never a choice made for the office.
- **By a workflow:** `jobs.assign` with `suggest: "nearest"` or `"least_busy"` takes the first suggestion. A business that wants assignment without a person uses it in a template.
- **The field agent is told** at once by push: "New job J-1042: installation, 12 Allen Avenue, today 10 to 12". There is no accept or decline: a job assigned is a job given, as at work, and joins the agent's schedule. An agent who cannot go says so in a note, and the office reassigns while it is not yet started.

## 10. Agents' states, and where people are

**An agent's state is read from their jobs**, never set by hand, as ISPOps' `recomputeAgentStatus` does: **on site** if a job of theirs is on site; else **on the way** if one is on the way; else **assigned** if they hold assigned or held jobs; else **available**. It is worked out again whenever one of their jobs is made, assigned, reassigned, started, arrived, ended, held, resumed or cancelled. There is no day, shift or duty to start and end.

**Whether and when location is collected is the business's choice** (the setting in §4):

| Setting | Collected | The office gets | Not available |
| --- | --- | --- | --- |
| **During jobs** (default) | From Start job until End job, Could not finish, Hold or Cancel, per job, as ISPOps did; nothing between jobs | Agents on a job on the map; each job's trail; a place on every step; the customer's "on the way" map; each agent's last known place for suggestions; location alerts during a job | Where an agent is between jobs |
| **Off** | Nothing, steps included | Steps with times only | Every map, trail, place, Arrived distance, **away from the place** mark, distance in suggestions, and location alert |

The phone's permission is asked the first time it is needed (the agent's first Start job), saying why: "Your location is shared with Ada Networks while you are on a job, so the office can follow the job and the customer can see you're on the way. It stops when you end the job." The background service starts with Start job and stops when the job ends.

**How ISPOps did it** (read in `ispops-mobile` and `ispops-backend`, 18 November 2026), and what changes:

| | ISPOps | Here |
| --- | --- | --- |
| When | Per job only: from Depart (`en_route`) through `in_progress` to clock-out (`home_provider.dart`, `_applyJob`); the server drops reports for any other state and refuses a job not the agent's (`location_handler.go`) | The same: per job, from Start job to End job; the server keeps the same two refusals |
| How | A Dart `Timer` every 30 seconds calling `Geolocator.getCurrentPosition` (high accuracy, 30-second limit) | A position stream (`Geolocator.getPositionStream`, already in the Taskiem app as `geolocator` 14), with a distance filter, so the phone reports when the agent moves rather than on a clock |
| Which fix | The last known fix when it was up to 120 seconds old and within 200 metres, else a fresh one (`location_service.dart`); sent and stored as if taken then | Each report carries **the fix's own time** and accuracy, and the server stores that time; a cached fix is never presented as a new one |
| Screen off, app in the background | Not covered: Android declares only `ACCESS_FINE_LOCATION` and `ACCESS_COARSE_LOCATION`, no foreground service and no background location; iPhone asks "when in use" with no background mode. The timer stops when Android suspends the app | Android: a **foreground service of type `location`** started from Start job while the app is open (geolocator's `foregroundNotificationConfig`), with its notification "Sharing your location with Ada Networks for job J-1042", stopped when the job ends. A foreground service started from the open app needs only "while using the app" permission, so `ACCESS_BACKGROUND_LOCATION`, and the Play review that comes with it, are not needed; `FOREGROUND_SERVICE` and `FOREGROUND_SERVICE_LOCATION` are declared. iPhone: "while using the app" with the `location` background mode and its blue indicator; "Always" is never asked for |
| Offline | Reports skipped when offline, errors swallowed | Reports kept on the phone and sent in batches when back (§12) |
| Clock-in and clock-out place | A fresh fix, with accuracy, on each | Every step carries its own fix, taken then, with accuracy (§7); a weak fix (over 100 metres) is recorded and marked, never refused |

| | Android | iPhone |
| --- | --- | --- |
| How often | The setting: every 30 seconds when moving (a distance filter of about 50 metres), 5 minutes when still | The same, within what iOS allows; significant-change updates when backgrounded long |
| When the app is killed or the phone restarts | The service stops; the office sees **location stopped** (§16); the app offers to resume on opening | The same |

**Changing the setting** takes effect on each agent's phone at the next sync: off stops collection at once, even mid-job; on starts at the agent's next Start job, asking permission if the phone has not given it. Jobs, steps, photos and everything else work the same either way. Reports already kept stay until the trail setting deletes them.

**The live map** (web, Field work > Map, unless location is off, as ISPOps' dispatch dashboard): each agent with a job on the way or on site, at their last report, how old it is, and their job; each open job's place, coloured by state. A job's page shows its trail from Start job to End job (ISPOps' route replay). Agents between jobs are listed with their state, not placed on the map.

## 11. The app for people in the field

The field mode is the Taskiem app (PR #198), not a second app. A field agent signs in as themselves, switches into the business, and the app opens on **Jobs**, because their role in that business is Field agent. Their own chats with people and other businesses stay where they are.

| Screen | What it has |
| --- | --- |
| **Jobs** | The job in execution, if any, with its next step as one large button (Start job, Arrived, End job); then the agent's schedule, earliest first (ISPOps' `/jobs/agent/assigned`): today's, then later. Any assigned job can be started from here when none is in execution |
| **Job** | Reference (copy), customer's name and phone (Call), the place with a small map (Directions opens the phone's maps), the window, what to do, the checklist, notes, photos (camera), parts used (from My stock), the steps and their times; Could not finish; the customer's confirmation when asked for |
| **My stock** | What they carry (§13) |
| **History** | Their jobs done, with the office's notes and the customers' ratings |
| **Me** | Their week: jobs done, time on jobs, rating; **Waiting to send**: what is queued on the phone and why, with **Send now** (ISPOps' sync diagnostics); the location permission and battery saving advice |

- **Notifications** by push (Firebase, as the app's chat): a job assigned, changed, held, cancelled; a note from the office; a reminder ten minutes before a window when the job is not started. A push opens the job.
- **Chat with the office** is the business's team conversation, not a new thing; a job's page has **Message the office about J-1042**, which opens it with the job's card.
- **Look:** the app's own, as [the screens to redesign](mobile-app.md) list asks; one large button per step, readable in sunlight, usable with one hand.

## 12. Working without a connection

Field work happens where signal is poor, so the field mode works offline for what the agent does on a job.

**Queued on the phone and sent when back:** every step (with the phone's time and place), checklist answers, notes, photos, parts used, the customer's confirmation, location reports. ISPOps had this designed (its `/clock/sync` takes a batch and answers success or skipped per idempotency key) but its app never filled the queue (§20.1); here it is used. Each has an idempotency key made on the phone, so a send repeated after a timeout lands once.

**Kept on the phone for reading:** the agent's scheduled jobs for today and tomorrow (customer, place, what to do, checklist), their stock, the business's job types, in the app's encrypted store ([explore and offline](mobile-app-explore-and-offline.md)), deleted 24 hours after a job is done.

**This widens the app's rule** that "anything that changes money or stock is not queued". Parts used are part of the End job step, queued with it; they are taken from the agent's stock when the step lands at the server, never below zero (beyond is recorded as `short`, as Stock does). This is safe because the step records a fact already true (the cable is in the wall), not a sale or a payment that could still be refused. Nothing else that changes money or stock is queued. 0070 records the exception.

**What the office sees:** a step that came in late shows "sent 2:14 pm, done 11:02 am (offline)". A job whose agent has been out of touch for an hour while on a job is flagged (§16). Conflicts are few because the agent alone moves their job's steps; when the office cancelled or reassigned a job while the agent was offline, the agent's later steps are kept as the agent's record and the job does not move, and both sides are told.

## 13. Parts carried and used

With **Stock** on, Field work adds **where** stock is: in the store, or **with an agent**.

| Movement | Made by | Effect |
| --- | --- | --- |
| `issued` | Office: "Give Musa 2 routers, 100 m of cable" | Store count down, Musa's up |
| `used` | The End job step's parts | Musa's down; the job records it (ISPOps' `consume`) |
| `returned` | Office, when the agent hands back | Musa's down, store up |
| `lost` | Office, with a reason | Musa's down |

- The catalogue table's count stays **the store's** count, as today; what agents hold is a record of its own (`stock_held`), shown on the item ("12 in store, 5 with agents") and on each agent's page, and copied to a kept table for workflows.
- Movements follow Stock's rules: one change each, under an idempotency key, never below zero. The store's own movements stay Stock's: goods **received**, **adjustments** with a reason, counts.
- **Items** are Stock's catalogue items, with what ISPOps kept on each: a code (SKU), a category, the unit (pieces, metres), the unit cost and the reorder level. A job's parts are costed from the unit cost, and reports show parts cost per job and per agent.
- **Equipment with a serial number** (a router, a radio: ISPOps' `serialized` items): parts used ask for the serial number of each one fitted, recorded on the job and the movement, so the business can find which router went to which customer. ISPOps had the flag but never asked for the number.
- Low-stock alerts can watch an agent's holding ("Musa has 1 router left").
- With Stock off, parts used are free text on the job, counted in reports by name, and nothing is issued or returned.

## 14. The customer's side

A job's customer is whoever the job was made for: saved in the business's `customers` table or not (§6.1). If they chat with the business (PR #198), the job comes to their conversation; if not, by SMS.

**In the customer's conversation, a job card**, the same kind as an order or delivery card:

- what (the job type, in the business's words), the window, who is coming (name and photo, never their number);
- the state as it moves: assigned, **on the way** (with a live map while on the way, unless the business turned location off: the agent's place, refreshed, and nothing after Arrived), arrived, done;
- **Confirm the work is done**, when the business asks for it: a tap in the card, or, for a customer without the app, a 4-digit code on the card or in their SMS, read out to the agent;
- **Rate the visit** (1 to 5 stars and words, as Taskiem rates deliveries; ISPOps used 1 to 4) after End job; the rating goes on the job and the agent's record.

### 14.1 Choosing a time

A visit needs a time the customer is in. There are three ways to get one, and all of them set the job's **window**:

1. **The customer gives it** when they book (**Book a visit**, below) or report a problem (the ISP area's complaint, [ISP §6.4](isps.md#64-complaints)): they pick from the times the business offers.
2. **The office types it**, from a phone call, on the New job form (§15.1).
3. **The office asks for it**: **Ask for a time** on a new, assigned, held or not-finished job, on the web, in the job's sheet in chat, or from the **Plan a follow-up** card. The office picks the choices to offer (up to four windows, such as "Tomorrow 9 to 12", "Tomorrow 1 to 4", "Saturday 9 to 12"), filled from the business's working hours and the assigned agent's schedule when there is one, and taps **Send**.

What the customer gets: in their conversation, a **Choose a time** card with the choices as buttons and **None of these work**; without Taskiem, an SMS with a link to the job's page, which shows the same. A choice sets the job's window at once and fires `time_chosen`: the card says "Thanks, see you tomorrow between 9 and 12", the assigned agent gets a push, and the office a diary line. **None of these work** opens a line for the customer's words ("I'm only in after 5"), sent to the office as a message in the conversation, and fires `no_time_chosen` with them. No answer within the setting (4 hours by default) is an alert card, **No time chosen**, with **Ask again** and **Call**. A time the customer chose can still be changed by the office, which tells them as on assigning.

This replaces ISPOps' availability windows (customers declaring when they are in, and staff linking a window to a job, `src/routes/_app.availability.tsx`): the customer always answers for a particular visit, and the answer lands on the job.

**Booking a visit:** the business's conversation gains **Book a visit** when Field work is on and the business chooses to offer it: the customer picks a job type the business lets customers book, gives the place (a saved address, a pin) and picks a time from the windows the business offers (§14.1). That makes a **new** job, unassigned, for the office.

**Customers not on Taskiem** get the same moments by WhatsApp or SMS through [updates](../updates.md), with a link to a page showing the job and, while on the way, the map. The page needs no sign-in, carries a token for that job alone, and ends with the job.

## 15. The office's side: web and chat

**Field work** on the web:

| Page | What it has |
| --- | --- |
| **Board** | Today: new, assigned, on the way, on site, done, late, not finished; each agent's state; drag a job to an agent |
| **Map** | §10 |
| **Jobs** | Filters (state, type, priority, agent, day, customer), search by reference, customer, phone or address; jobs past their time marked; tick new or assigned jobs and **Assign to agent** for many at once (all or nothing); **New job** ([§15.1](#151-making-a-job-in-the-office)); a job's page with its history, map and trail, steps and their times, checklist, photos, notes, parts, rating, and the office's actions: assign or reassign (new, assigned or on hold), **Ask for a time** (§14.1), hold, resume, cancel with a reason, close (done only), add a note |
| **Agents** | Each field agent (ISPOps' workforce page): state, their schedule, history, jobs done, time on jobs (travel and on site), average time on site, rating and reviews; stock with them; bank details when a pay list is used |
| **Reports** | As ISPOps' reports and dashboard, over a period, per team and per agent, each with CSV: jobs made, done, late and not finished; by type and state; **response time** (assigned to Arrived, ISPOps' measure), travel time and time on site, with each agent's averages and their last 7 days against their usual; jobs per day; top performers; ratings; parts used and their cost. An agent's report lists each job with its times and the places of its steps. **The steps log**: every Start job, Arrived and End job with its phone and server times, place and accuracy, filtered and exported to CSV (ISPOps' clock events page) |
| **Settings** | §4; job types and templates open their tables |

### 15.1 Making a job in the office

ISPOps' **Create new job** page (`exact-hue-hearth`, `src/routes/_app.jobs.new.tsx`), with what changes. Anyone with `job.manage` uses it (in ISPOps: super admin, admin, customer care).

| Part | ISPOps | Here |
| --- | --- | --- |
| Customer | Search existing customers by name, phone or email; picking one fills name and phone (not the address) and links the record ("rating will be sent after job completion"); or type name, phone and address | Search the business's **saved customers** (its `customers` table, §6.1) by name, phone or address; picking one fills name, phone, address and pin, and lists the places of their earlier jobs to choose from instead. Or type a name, phone and address, with **Save to my customers** below, **ticked by default**: left ticked, the job's customer becomes a row of the table; unticked, they are on this job only. A typed phone that is already a saved customer's offers that customer instead. Every customer gets the rating (by card or by SMS), saved or not |
| Site | An optional map pin, by clicking a map or typing coordinates | The same, and a saved address brings its pin. Without a pin, Arrived is not measured, and the form says so |
| Template | **Start from a template** fills type, priority and description, still editable; the checklist and time estimate are attached by the server, not shown | The same, and the checklist is shown, so the office can add an item for this job |
| Details | Type, priority, one scheduled date and time, description ("internal notes") | Type, priority, **when**: a visit window (from, to) or a time, "as soon as possible", or **Ask the customer**, which saves the job and sends **Choose a time** (§14.1); what to do (the agent sees it); a separate **note for the office** (the agent does not) |
| Assignment | A list of agents whose status is available, and only them | Every field agent with their state and suggestions as in §9: available first, then by jobs held that day, nearest by last known place. Assigning someone already holding jobs adds this to their schedule |
| Buttons | **Create & Assign** (needs an agent) or **Save as Draft** | **Create and assign** or **Save without assigning** (the job is **new**). Both tell the office (`job-tell-office`); assigning tells the agent and the customer (§8) |

After saving, the office lands on the job's page, not the list, so a new job's reference and card can be read out or sent to the customer at once.

### 15.2 The office's tools in chat

Customer care, dispatchers and admins can do their Field work from the chat, as a school's bursar and a shop's cashier do. This uses the chat tools that are built ([chat tools](chat-tools.md): the tools panel, sheets, cards, diary lines, alert cards and the briefing), so it needs **chat tools** on for the business. It works in the web chat and in the Taskiem app; on the app, the office's staff are in the chat as anyone is, and only `job.work` opens the field mode (§11).

**Tiles in the tools panel** (`/jobs`, `/new`, `/agents`...), each only for those whose role may open it, with a count or badge:

| Tile | Badge | Opens a sheet with |
| --- | --- | --- |
| **Jobs** | "2 new · 1 late" | Chips: New, Today, On the way, On site, Late, Not finished, To review (done, not closed), On hold; search by reference, customer or phone. A job opens its own sheet: its card, history, the agent's route on a small map, photos, checklist, parts, rating, and the office's actions in place: **Assign** (with the suggestions of §9), **Reassign**, **Hold**, **Resume**, **Cancel**, **Close**, **Add a note** |
| **New job** | | The §15.1 form in a sheet. Opened from a customer's conversation, it starts with that customer filled in |
| **Agents** | "3 on jobs" | Each field agent's state and job; a small map of agents on jobs; their schedule; **Issue stock** (§13) |
| **Customers** | | The business's `customers` table (§6.1): search, a customer's jobs, **New job for this customer** |

**Alert cards** at the end of the person's Taskiem conversation, read when they look and gone when dealt with, as chat tools' phase 4:

| Card | Who sees it | Its next step |
| --- | --- | --- |
| A job made by a customer or a workflow, not assigned ("NEW JOB · fault repair · Chidi Okafor · today 12 to 3") | `job.manage` | **Assign**: the suggestions, first one ready |
| Not started: a window began and the job is only assigned | `job.manage` | **Call Musa**; **Reassign** |
| Late, too long on site, location stopped, away from the place (§16) | `job.manage` | **Call** the agent; **Message** them in the team conversation; for late, **Tell the customer** |
| Not finished, with the agent's reason | `job.manage` | **Plan a follow-up** (the New job sheet, filled from this one, with **Ask the customer** for its time) or **Cancel** |
| No time chosen: a customer asked for a time has not answered, or answered "none of these" | `job.manage` | **Ask again**; **Call**; for "none of these", the customer's words and **Set a time** |
| Started early (§7), for a job whose customer was told a later time | `job.manage` | **Tell the customer** ("Musa is on his way earlier than planned") |
| Done, waiting for review | `job.manage` | **Close** (after a look at its sheet) |
| An agent holding few of a part ("Musa has 1 router left") | `job.manage` with Stock | **Issue more** |

**Cards and diary lines.** A job is a card in the card frame: its reference and type, its state as a pill, the four steps (Assigned, On the way, On site, Done), customer, agent and window, and its next step as the filled button. The office's own actions are diary lines: "Assigned J-1042 to Musa", "Held J-1062: parts missing", "Closed J-1042".

**The briefing** gains a Field work part for those with `job.read`: today's jobs done and to go, new and unassigned, late, agents on jobs now, and yesterday's ratings. Tapping it asks "how did the jobs go yesterday".

**In a customer's conversation**, the office's side has **New job for this customer**, **Save to my customers**, and the customer's jobs to drop in as cards with `#`, the same card the customer sees.

**Customer care and dispatchers can work from chat alone**, as chat tools' phase 5 has cashiers do: everything above, without opening the web pages. The web keeps what a phone screen cannot hold: the full board and map, reports and settings.

**In chat**, `@Taskiem` gains the dispatcher's words, in every language the chat has: "new job: installation for Ada Obi at 12 Allen Avenue tomorrow morning", "assign J-1042 to Musa", "where is Musa", "jobs today", "what's late". Each answers with job cards, and changes need a yes, as other chat changes do.

## 16. Watching for trouble

Alerts, through [alerts](../alerts.md), to the people with `job.manage` (or the roles the business picks), and each a trigger (`late`, `too_long`) for workflows:

| Alert | When |
| --- | --- |
| Late | Not arrived by the end of the window plus the setting |
| Too long on site | On site past the template's time plus the setting |
| Location stopped | A job on the way or on site, location on, and no report for 15 minutes (the app killed, the phone off, no permission) |
| Out of touch | On a job and nothing heard for an hour |
| Away from the place | Arrived or End job farther than the setting |
| Not started | A window began and the job is still only assigned |

## 17. Hours, jobs and pay

- **Time on jobs** is travel (Start job to Arrived) and on site (Arrived to End job), as ISPOps' duration from clock-in to clock-out; **jobs** are jobs ended; both per agent per day, in reports and in a kept table for workflows. There is no time on duty: Taskiem does not keep timesheets (§24).
- Pay stays out of the record. Templates send them where pay is worked out: `payrolla-record-payroll` with jobs and time on jobs, or a pay list (`pay-a-list`) for a per-job fee.
- **The staff list in Payrolla.** ISPOps pushed its staff (field agents and office staff, names and roles, never pay) into the business's Payrolla company as employees are added, changed and removed, and opened Payrolla's own screen inside its dashboard. Taskiem's Payrolla connector reads employees but cannot push them ([Payrolla](../integrations/payrolla.md)). Field work needs `payrolla@1` to gain a **sync employee** action under Payrolla's sync scope, and a template that runs it when a member with a Field work role is added, changed or removed; Payrolla's own screens open by its links (`get_approval_link`, `create_payslip_link`) rather than inside Taskiem. To be agreed with Payrolla (F8).
- A field agent's bank account for pay is theirs as a member, as riders' are kept, if a pay list is used.

## 18. Privacy and safety

- **Field agents' location is their personal data**, collected only during their jobs (unless the business turned it off), for a stated purpose, shown only to `job.read`, deleted after the trail setting (default 90 days); steps keep their own place for the job's record. The agent sees their own trails. The [privacy notice](../privacy.md) and the business's own staff notice say so; the app says so when asking.
- **Customers' names, phones and addresses** are sealed and masked as elsewhere. A field agent sees them for their jobs only, from assignment until 24 hours after End job, and then only the job's reference and type.
- **The customer's map** shows the agent's place only while on the way, from the agent's last report, and never their phone number.
- **Photos** are scanned and cleaned of their own location and metadata, as all media; the step's place is kept on the step, not in the photo.
- **Every human action is audited**: assignments, steps, corrections, settings, stock, trails opened by someone in the office (who opened whose trail, when).

## 19. Data and server changes

| Kind | What |
| --- | --- |
| Area | `fieldwork` in `engine/areas`, with its door checks (routes, steps, chat words, app screens) and `areas.Affected` for workflows using it |
| Permissions and roles | `job.read`, `job.manage`, `job.work`; built-in **Field agent** (field-only, like Gate); custom **Dispatcher** on turning on |
| Records (module schema `fieldwork`) | `jobs`, `job_steps`, `job_assignments`, `job_notes`, `job_media`, `job_parts`; `stock_held` and new movement reasons in Stock's schema |
| Engine state | `agent_reports` (location reports, partitioned by day, deleted after the trail setting); the per-business job counter |
| Lists (the business's project) | `job_types`, `job_templates`, known tables in `templates/tables`; the `customers` known table gains `address` and `location` (§6.1) |
| Kept copies | `jobs` in the business's project, for workflows, chat search and the builder |
| Contracts | `wd/v1` trigger type `job`; signals `job.<step>`; connector `jobs@1` |
| API | `/v1/jobs` (list, make, change, steps, assign, hold, cancel, close, notes, media, parts), `/v1/field/agents`, `/v1/field/map`, `/v1/field/reports`, `/v1/field/settings`; the field mode's own routes `/v1/me/field/...` (today, steps, reports batch, stock), the only routes the Field agent role reaches |
| Push | Kinds for job assigned, changed, reminder |
| Alerts | Late, too long, location stopped, out of touch, away, not started |
| App | The field mode, the outbox for steps and reports, background location (Android foreground service, iOS background updates) |
| Web | Field work pages; the job card for customers' conversations; the job's public page |

## 20. What comes from ISPOps

ISPOps is the founder's own code (Go backend, React admin portal, Flutter field app, React customer app), used by one business that will start afresh on Taskiem. It is a design to carry over, not data to move. What it had, and where it goes:

| ISPOps | Here | |
| --- | --- | --- |
| Organisations, the platform admin and its pages | Businesses; Taskiem Ops | Dropped: Taskiem has them |
| Users and roles (super admin, admin, customer care, field agent), login by slug and email | Members, roles, Taskiem sign-in | Dropped; roles mapped (§5, [ISP §8](isps.md#8-staff-and-roles)) |
| Jobs, statuses, multi-agent assignment, notes, history, hold, cancel, close | The job and its journey (§6, §7) | Carried over; "on the way" from its `en_route` and its Depart; Arrived and Could not finish added |
| Job templates with checklists | `job_templates` (§6) | Carried over as a list |
| Job schedules (daily, weekly, monthly, yearly) and their "run due" worker | A schedule workflow calling `jobs.create` (§8) | Replaced |
| Depart, clock in and clock out per job with GPS, phone and server time, and an idempotent offline sync | Start job, Arrived, End job (§7, §12) | Carried over, and finished: ISPOps' app never queued (its outbox was never filled) |
| Location every 30 seconds, per job, with the app open only | Reports per job, from Start job to End job, in the background, with the fix's own time; or off (§10) | Carried over and finished |
| Agent status (available, assigned, en route, on site), worked out from the agent's jobs; `offline`, which nothing sets | The same states, read from jobs (§10); `offline` dropped | Carried over |
| Live agent map, dispatch dashboard | Board and Map (§15) | Carried over |
| Inventory: items, stock per agent, issue, consume, return, adjust, low stock | Stock with agents (§13) | Carried over onto Stock |
| Attachments and the completion photo | Job photos (§6), the photo setting (§4) | Carried over |
| Ratings by token link | The rating card and page (§14) | Carried over |
| Reports with CSV | Reports (§15) | Carried over |
| Customers, OTP login, the customer app (availability windows, chat, ratings) | Customers on Taskiem and their conversation (§14) | Replaced by PR #198 |
| Staff chat with customers, mirrored to a Telegram group | The business's conversations | Dropped |
| Notifications, FCM, web push | Taskiem push | Dropped: Taskiem has them |
| Settings: overdue and overtime, completion photo, Termii, Telegram group | Field work settings (§4); SMS through Taskiem's messages | Carried over where they belong |
| Payroll through Payrolla, embedded | Templates to Payrolla (§17) | Replaced by templates |
| The iswallet business wallet (off in ISPOps) | Taskiem Money | Dropped |
| Agents' bank details and name check | Members' bank account for pay lists (§17) | Carried over if needed |

When the business is running on Taskiem, the four ISPOps repositories are archived.

### 20.1 Where to find it in ISPOps

The implementation details to port are in four private repositories (read them for the design; write Taskiem's in its own conventions):

| Repository | What it is |
| --- | --- |
| [ISRAEL-DUFF/ispops-backend](https://github.com/ISRAEL-DUFF/ispops-backend) | Go API and PostgreSQL schema: the source of truth for the model and the rules |
| [ISRAEL-DUFF/exact-hue-hearth](https://github.com/ISRAEL-DUFF/exact-hue-hearth) | The admin portal (React, TanStack Router): what the office sees |
| [ISRAEL-DUFF/ispops-mobile](https://github.com/ISRAEL-DUFF/ispops-mobile) | The field agent's app (Flutter, package `field_agent`) |
| [ISRAEL-DUFF/ispops-customer-app](https://github.com/ISRAEL-DUFF/ispops-customer-app) | The customer app (React PWA), replaced by PR #198 |

| For | Look at |
| --- | --- |
| The job, its statuses, assignments, notes, history (§6, §7) | backend `migrations/000001_initial_schema.up.sql` (`jobs`, `job_status_history`, `job_notes`, `agent_assignments`), `migrations/000013_multi_job_assignment.up.sql`, `internal/service/job/service.go`; every route in `internal/httpapi/server.go` |
| Templates, checklists and schedules (§6, §8) | backend `migrations/000018_job_templates_schedules.up.sql`, `migrations/000020_job_checklist.up.sql`, `internal/service/scheduling`; portal `src/routes/_app.scheduling.tsx` |
| Steps with time and place, offline idempotency (§7, §12) | backend `clock_events` and `sync_queue_log` in `migrations/000001_initial_schema.up.sql`, `internal/service/clock/service.go` (and its completion policy), routes `/clock/in`, `/clock/out`, `/clock/sync`; app `lib/features/home/home_provider.dart`, `lib/features/home/clock_in_sheet.dart`, `lib/core/sync/sync_provider.dart`, `lib/core/database/tables.dart` |
| The completion photo rule (§4) | backend `migrations/000019_completion_photo_and_schedule_health.up.sql`, `migrations/000016_attachments.up.sql` |
| Location reports and the live map (§10) | backend `migrations/000008_agent_location_pings.up.sql`, `internal/httpapi/location_handler.go`; app `lib/core/location/location_service.dart`; portal `src/components/job-map.tsx`, `src/routes/_app.dashboard.tsx` |
| Agent states, suggestions when assigning (§9, §10) | backend routes `/users/agents/status`, `/users/agents/available`; portal `src/routes/_app.agents.tsx`, `src/routes/_app.jobs.new.tsx` |
| The office's job page (§15) | portal `src/components/job-detail-panel.tsx`, `src/routes/_app.jobs.$jobId.tsx` |
| Stock with agents (§13) | backend `migrations/000017_inventory.up.sql`, `internal/service/inventory`; portal `src/routes/_app.inventory.tsx` |
| Ratings (§14) | backend `migrations/000004_ratings.up.sql`, `internal/service/rating`; customer app `src/routes/$slug.rate.$jobId.tsx` |
| Visit windows (§14) | backend `migrations/000003_customer_pwa.up.sql` (`availability_windows`); customer app `src/routes/$slug._app.availability.tsx`; portal `src/routes/_app.availability.tsx` |
| Reports and the board's figures (§15) | backend `internal/httpapi/reports_handler.go`, `internal/httpapi/stats_handler.go`; portal `src/routes/_app.reports.tsx` |
| Settings (§4) | portal `src/routes/_app.settings.tsx`; backend `organization_settings` |
| Push to the app (§11) | app `lib/core/notifications/fcm_service.dart` |
| Payroll through Payrolla (§17) | backend `internal/service/payroll` |

**Known faults not to carry over** (found when ISPOps was read, 18 November 2026): the app's offline queue is never filled (`SyncDao.enqueue` has no caller), so clocking fails offline; the weak-GPS check in `clock_in_sheet.dart` compares accuracy with a colour's hash; a failed Depart is swallowed silently; the app handles one job at a time and ignores `on_hold` and `closed`; location is sent only while the app is open (no foreground service or background permission on Android, no background mode on iPhone) and dropped when offline; a report can carry a last-known fix up to 120 seconds old, stored as if taken then; the overdue check ignores its own threshold setting (`ListOverdue` compares the scheduled time only); the overtime notification exists but nothing sends it; a rating is asked only of a customer linked to a record with an email address.

### 20.2 Checked against ISPOps' code

On 18 November 2026 every table, route, setting and screen of the four repositories was read against this plan. What was found missing here has been added in its own section; what is left behind is left on purpose:

| ISPOps has | Here | Where |
| --- | --- | --- |
| Inventory items with SKU, category, unit, unit cost, reorder level, and a "serialized" flag; stock received, adjusted, issued, consumed, returned | Stock's catalogue and movements, agents as holders, parts costed, serial numbers asked for on parts fitted | §13 |
| Dashboard figures (today's jobs made, done, overdue, average time, agents; each agent's assigned, done, average and usual per day) and reports (response time, top performers, by type, jobs per day, per-job clock places), with CSV | Reports and the board's figures, with CSV | §15 |
| The clock events page and its CSV | The steps log | §15 |
| Overdue and overtime thresholds | Late after; too long on site, falling back to 3 hours | §4, §16 |
| The completion-photo rule | A photo is needed to end a job | §4 |
| Customers told by email or SMS (an org setting, email by default) | SMS, or email for customers who have one | §4 |
| Ratings 1 to 4 stars by an emailed link | 1 to 5, by card or SMS, for every customer | §14 |
| The field app's sync diagnostics | **Waiting to send**, with **Send now** | §11 |
| Staff pushed to Payrolla, and Payrolla's screen inside the dashboard | A sync employee action and template, and Payrolla's links | §17 |
| Job schedules with health (failures counted, a schedule paused after repeated failures, the last error) and **Run due now** | Schedules are workflows (§8), whose runs, failures and alerts are Taskiem's own | §8 |
| Customers' availability windows linked to jobs | Ask for a time | §14.1 |
| Notifications list with unread count; device registration; FCM | Taskiem's notifications, alert cards and push | §11, §15.2 |
| Attachments on jobs (photos), with upload confirmation | Job photos in the business's storage | §6 |

| Left behind on purpose | Why |
| --- | --- |
| Organisations, platform admins and their pages, the platform settings page | Businesses and Taskiem Ops |
| Login by organisation slug and email, password reset by code, refresh tokens, **session idle timeout** setting | Taskiem's sign-in and sessions (phone, passkeys, the business's own rules) |
| The customer app's OTP sign-in, push and its own chat (WebSocket, typing) | PR #198 |
| Staff answering customers from a **Telegram supergroup**, a topic per customer | The business's conversations in Taskiem's web chat and app; a business wanting Telegram uses Taskiem's Telegram support for its own bot |
| The iswallet business wallet, KYC, virtual account and payouts (off in ISPOps) | Taskiem Money |
| Attachments on inventory items and on invoices | Stock's item photos; Money's invoices |
| Termii API key and sender typed into settings | The business's Taskiem messages settings |
| The `offline` agent status | Never set by anything in ISPOps; whether an agent can mark themselves not available is an open question (§23) |
| Mock data and mock mode | Development only |

## 21. Milestones

| # | Milestone | Size |
| --- | --- | --- |
| F0 | The area, permissions, Field agent and Dispatcher roles, settings; the job record and its journey; `/v1/jobs`; audit | L |
| F1 | Web: Jobs (list, new, the job's page), Board, Agents; job types and templates as tables | L |
| F2 | The app's field mode: Today, Job, steps with a place, notes, checklist, photos; push | L |
| F3 | Working offline: the outbox for steps and everything on a job; the encrypted copy of the day's jobs | M |
| F4 | Location during jobs: the setting, background reports from Start job to End job (foreground service, position stream), the Map, trails, last known place in suggestions | L |
| F5 | The `job` trigger, signals, `jobs@1`, the templates, the AI builder | M |
| F6 | The customer's side: the job card, the live map, confirmation, rating, Book a visit, the public page and updates | M |
| F7 | Stock with agents | M |
| F8 | Watching for trouble: alerts; Reports, the steps log and CSV; parts costs; jobs and time on jobs to pay templates; the Payrolla staff sync, with Payrolla | M |
| F9 | The office's tools in chat (§15.2): the tiles and sheets, alert cards, job cards and diary lines, the briefing's part, the customer conversation's actions; `@Taskiem` words for dispatchers; the guide; the business's starting set when it turns Field work on | M |

F0 to F4 are the minimum for the first business to stop using ISPOps; F5 and F6 make it Taskiem's rather than a copy of ISPOps.

## 22. Testing

- **Go:** the journey (every allowed and refused move, by role; End job refused until its rules are met; one job in execution per agent; no reassigning after Start job; an agent's state worked out again on every change; idempotent steps sent twice; late steps after a reassignment; times kept both ways); the Field agent role refused every route but its own, as the Gate role's router-walk test; the area off refusing routes, steps, chat words and the app; stock with agents (never below zero, `short`, used on a step that lands late); location reports only within the setting's span (on duty, during jobs only) and deleted after the retention; with location off, no report is taken and steps carry no place; the `job` trigger and signals; `jobs@1` against its contract; audit entries.
- **Web:** each page, with Field work off and on; the board's drag; the map with no agent on a job; accessibility and plain-words lists.
- **App:** the field mode only for `job.work`; steps offline then online; a step while the job was cancelled offline; permission refused; the foreground service starting with Start job and stopping with End job, Could not finish, Hold and Cancel.
- **On real phones:** background location on Android (several makers' battery savers) and iPhone, over a day of several jobs, with battery measured.
- **End to end:** each [worked example](field-work-examples.md) is a test, step by step; the first: an office member makes and assigns a job; the agent starts the job, arrives, works offline, ends it with a photo and parts, and starts their next; the customer sees the card move, confirms and rates; the invoice template runs on End job.

## 23. Risks and open questions

- **Background location on Android** is the hardest part and the one most likely to fail quietly: makers' battery savers kill services, and ISPOps never attempted it (§10), so there is no field experience to lean on. The plan tests on real phones (F4), tells the agent how to exempt the app, and flags **location stopped** rather than trusting it.
- **App-store review** looks hard at background location. The purpose (staff on a job, stated, stopped when the job ends) is the accepted kind, but the wording and the store's forms need care.
- **Staff location and the law.** Employees' location is personal data under the NDPA. The design collects it only during jobs, for a stated purpose, with a retention limit; a lawyer should see the staff notice (with PR #198's other legal reviews).
- **One app for customers and staff.** A field agent's personal chats and their work are in one app. The field mode is the business's; the person's own side is theirs. Is that what businesses want, or will some ask for work phones with nothing else?
- **The core-cleanup decision (0064)** is proposed, not accepted. This plan follows it (module schema, kept copies, lists as tables); if it changes, §19 changes with it.
- **Naming:** "Field work" and "Field agent" need the plain-words pass and an owner's ear; "technician" may be the common word.
- **Open:** whether a job needs more than one agent (helpers), which ISPOps did not have; whether an agent should be able to mark themselves not available (on leave, off sick), which ISPOps' `offline` state hinted at but never had; per-job prices on job types or only in templates.

## 24. Out of scope

- Field agents working by WhatsApp, Telegram or SMS (decided: the app only, for location).
- People who are not members doing jobs (contractors); they may come later as linked businesses, as riders do.
- Route planning and optimising a day's order of jobs.
- Signatures drawn on the screen (the confirmation is a tap or a code).
- Working days, shifts, timesheets and leave: Taskiem keeps time on jobs, not time at work.
- Shift rosters (who works which day).
