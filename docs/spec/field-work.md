# Field work: jobs done at customers' places, the staff who do them, and where they are

Status: **Draft** 2026-11-18, for review, not scheduled · Decision: [0070](../decisions/0070-field-work-and-isps.md) (proposed) · Workstream **F0 to F9**, one pull request per milestone · Written with the founder · First business to use it: an internet provider ([the ISP area](isps.md)) · Replaces: ISPOps, the founder's field-agent system (backend, admin portal, field app, customer app), absorbed here and retired; its repositories and where each feature's implementation is: [§20.1](#201-where-to-find-it-in-ispops)

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
10. [On duty, and where people are](#10-on-duty-and-where-people-are)
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
| Assigned → on the way → arrived → working → done | The journey, kept by the engine with a time and a place at each step (§7) | Record |
| "When X happens to a job, do Y" | Each step starts workflows: the `job` trigger, `jobs@1`, signals for runs that wait (§8) | Platform |
| Give jobs to people | By hand, in bulk, by suggestion, or by a workflow (§9) | Platform |
| Know where people are | On duty only: start and end the day in the app; location while on duty; a live map (§10) | Platform |
| Work from the field | The Taskiem app, in a field mode, the only place a field agent works (§11) | App |
| No signal at the site | Steps, notes, checklists and photos queued on the phone with their own time and place (§12) | App |
| Parts in the van | Stock held by each agent: issued, used on a job, returned (§13) | Stock, extended |
| The customer knows who is coming | A job card in the customer's conversation, "on the way" with a live map, then a rating (§14) | Chat |
| The office's view | Field work on the web: board, map, jobs, agents, reports; and `@Taskiem` in chat (§15) | Web, chat |
| Late and stuck jobs | Alerts for overdue, too long on site, no location, done far from the place (§16) | Alerts |
| Pay | Hours on duty and jobs done, to Payrolla or a pay list (§17) | Templates |

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
| Step | One move in a job's journey: on the way, arrived, started, done |
| On duty | The time between a field agent's **Start my day** and **End my day**, the only time their location is collected |
| Trail | The places a field agent's phone reported while on duty, in order |
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

**What does not exist:** a job; any idea of staff working away from the office; location of staff; stock held by a person; a trigger for anything a member does in the field. Dispatch riders are not the answer: a rider is a person **outside** the business, reached by chat, and is becoming a business of their own ([0065](../decisions/0065-riders-are-organisations.md)); a field agent is staff, works only for this business, is tracked while on duty and carries its stock.

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
| Arrived means within | 300 metres | How close an Arrived step must be to the job's place to count as at the place; farther is allowed and marked (§7) |
| A photo is needed to finish | Off | Done needs at least one photo |
| Customer's confirmation to finish | Off | Done needs the customer's OK in the app, in their chat, or a code they read out (§14) |
| Late after | 30 minutes past the window | When a job not yet arrived is late (§16) |
| Too long on site after | The template's expected time, plus half | When a job working is flagged (§16) |
| Location every | 30 seconds when moving, 5 minutes when still | How often the phone reports while on duty (§10) |
| Keep trails for | 90 days | Then deleted; steps keep their own place (§18) |
| Working hours | The business's opening hours | Outside them, Start my day asks "Working outside hours?" |

## 5. Who can do what

| Permission | Lets a person | Built-in roles |
| --- | --- | --- |
| `job.read` | See jobs, the board, the map, agents' states and trails, reports | Owner, admin, operator, auditor, viewer |
| `job.manage` | Make, change, assign, hold, cancel and close jobs; change settings, job types and templates; issue and take back stock from agents | Owner, admin, operator |
| `job.work` | The field mode in the app: their own jobs, their stock, their day | Field agent |

- **Field agent** is a **built-in** role holding `job.work` and nothing else, refused everywhere else by the server, as the Gate role is: their own jobs (assigned to them, or done by them in the last 30 days), their own stock and their day; who they are; signing out. Nothing is merely hidden. A field agent who is also something else in the business (a supervisor who goes out too) holds both roles, and `job.work` then only adds the field mode.
- **Dispatcher** is a custom role with `job.read` and `job.manage`, changed like any custom role.
- Custom roles that bundle Field work with other parts (customer care, cashier) are the vertical's to offer ([ISP §8](isps.md#8-staff-and-roles)).
- A field agent is a member, added under **Members & keys** by phone or email as any member is ([governance](../governance.md)). Signing in is the person's own (PR #198: phone, passkey, the business's rules); the field mode opens when the business they switch into gives them `job.work`.

Every change to a job, assignment, setting, stock with an agent, and every start and end of a day, is in the business's audit chain with who made it.

## 6. The job

**A job is a record with rules** ([0064](https://github.com/ISRAEL-DUFF/taskiem/blob/claude/core-cleanup-0ak3hh/docs/decisions/0064-business-records-out-of-the-core.md), proposed: core tables in the module's own schema, a kept copy for workflows).

| Field | What it is |
| --- | --- |
| Reference | `J-` and a number, per business, in order |
| Type | One of `job_types`; brings the template's description, checklist and expected time when made |
| Priority | Low, normal, high, urgent |
| Customer | A customer of the business (a person on Taskiem, through the customer door, or a contact with a name and phone); or a name and phone typed for this job. Sealed and masked as personal data |
| Place | An address, a landmark, and a map pin (latitude and longitude). The pin is what Arrived is measured from; without one, Arrived is not measured |
| What to do | Description; the checklist (items, each with a note or photo it may require) |
| When | A visit window (from, to), or "as soon as possible"; expected time on site |
| Who | One lead field agent, and helpers. Each agent's own steps are recorded; the job's state follows the lead |
| State and steps | §7 |
| Notes | From the office and from the field, each with who and when |
| Photos and files | Taken in the app or added in the office, in the business's private storage |
| Parts used | Items and quantities, from the agent's stock (§13), or free text when Stock is off |
| Customer's confirmation and rating | §14 |
| Came from | The office, a workflow, a schedule, a customer's booking, a complaint (the ISP area) |
| Links | The customer's conversation; an order, invoice or ticket it belongs to |

**Job types and templates are lists**, kept as data tables in the business's project (`job_types`, `job_templates`), so an owner edits them like any table and the AI builder reads them. A template's checklist is one line per item ("Test signal | photo", "Customer shown how to use the router"). Recurring jobs are **not** a part of the record: a workflow on a schedule calls `jobs.create` (§8), as monthly invoices are made ([business areas §9](business-areas.md#9-a-service-business-as-the-test-an-isp-or-cloud-provider)).

## 7. The journey of a job

```
new ──assign──▶ assigned ──On my way──▶ on the way ──Arrived──▶ arrived ──Start──▶ working ──Done──▶ done ──Close──▶ closed
  │                │                        │                      │                    │
  └────────────────┴──────── Hold ──▶ on hold ──Resume──▶ (where it was) ◀──────────────┘
  └──── Cancel (office) ──▶ cancelled                       working ──Could not finish──▶ not finished
```

| Step | Who | What is recorded | Rules |
| --- | --- | --- | --- |
| Assign | Office, or a workflow | Who, by whom | A job can be assigned to field agents only. Reassigning keeps the earlier agent's steps |
| On my way | The field agent | Time on the phone and at the server; the phone's place and its accuracy | On duty only. One job at a time on the way or further, per agent: starting another holds nothing, it asks "Finish J-1041 first?" |
| Arrived | The field agent | The same; the distance from the job's place | Farther than the setting is allowed and marked **away from the place** |
| Start | The field agent | The same | May come straight after On my way for a job with no place |
| Done | The field agent | The same; checklist answers, photos, parts used, the customer's confirmation when asked for | Refused until the checklist's required items, the photo setting and the confirmation setting are met. The app says which |
| Could not finish | The field agent | A reason from a short list (customer not in, no access, parts missing, needs another visit, other) and a note | Office then reassigns, reschedules or cancels; a workflow may make the follow-up job |
| Hold, Resume | Office, or the field agent with a reason | Who, why | Resume returns it to the step it was at |
| Cancel | Office | Who, why | Not after done |
| Close | Office | Who | Done jobs are closed after review. A business may close on Done by a setting; then Close is automatic |

- **The engine keeps the state.** A step is one write, under the step's own idempotency key from the phone (§12), so a step sent twice lands once.
- **Both times are kept.** The phone's time is what happened; the server's is when it was heard. Reports use the phone's, and say when a step came in late from offline.
- **Nothing moves a job backwards** but Hold and Resume. A wrong step is corrected by the office with a note, recorded as a correction, never by editing the step.

## 8. Each step is a trigger

The steps are not workflows; what happens **because of** them is.

**The `job` trigger** (`wd/v1` gains the type, a contract change under 0070):

```json
{ "type": "job", "config": { "on": ["arrived", "done"], "types": ["installation"] } }
```

`on` is one or more of `created`, `assigned`, `on_the_way`, `arrived`, `started`, `done`, `not_finished`, `on_hold`, `resumed`, `cancelled`, `closed`, `late`, `too_long`, `rated`. `types` filters by job type. `trigger.body` has the job as its kept copy shows it, the step, and who did it. The customer's personal fields reach a run sealed, as with every trigger.

**Signals.** A run waiting on a job (a workflow that assigns a job and then waits for it to be done) waits for `job.<step>` with the job's reference as correlation, as dispatch's `storefront.delivered` is waited for.

**`jobs@1`**, the platform connector, acting on the run's own business:

| Action | Class | What it does |
| --- | --- | --- |
| `create` | idempotent_write | Makes a job (type, customer, place, window, description, priority), keyed by the run's step key |
| `get`, `list` | read | One job, or jobs by state, type, agent, customer, day |
| `assign` | idempotent_write | To an agent by id, or `suggest` (§9) |
| `hold`, `resume`, `cancel`, `close` | idempotent_write | As the office would |
| `add_note` | idempotent_write | A note on the job, from the workflow |
| `agents` | read | Field agents with their state (off duty, free, on a job) and last place |

**Templates** that come with the area (each with workflow tests, as the subscription recipes have):

| Template | Trigger | What it does |
| --- | --- | --- |
| `job-tell-customer-assigned` | `job` assigned | "Musa will visit on Tuesday between 10 and 12" to the customer, in their conversation or by SMS |
| `job-tell-customer-on-the-way` | `job` on_the_way | "Musa is on the way" with the live-map link (§14) |
| `job-ask-rating` | `job` done | The rating card, or a link by SMS |
| `job-invoice-on-done` | `job` done | An invoice for the job type's price, with the parts used as lines (Money on) |
| `job-late-alert` | `job` late | Tells the dispatcher, and the customer if they chose |
| `job-follow-up-not-finished` | `job` not_finished | A new job for "needs another visit", the same customer and place |
| `job-recurring-visit` | `schedule` | A job every month (or week, or year) for each row of a table: maintenance contracts |
| `job-daily-summary` | `schedule` | The day's jobs done, late, not finished, by agent, to the owner |

The AI builder knows the trigger, the connector and the templates ("tell my customers when the technician is on the way", "a maintenance visit every three months for each customer on the contracts table").

## 9. Assigning

- **By hand:** on the job, on the board (drag to an agent), or many at once from the list. In chat: `@Taskiem assign J-1042 to Musa`.
- **Suggestions** when assigning: field agents on duty, sorted by distance from the job's place (their last place), then by jobs left today; off-duty agents after, marked. A suggestion is a list, never a choice made for the office.
- **By a workflow:** `jobs.assign` with `suggest: "nearest"` or `"least_busy"` takes the first suggestion. A business that wants assignment without a person uses it in a template.
- **The field agent is told** at once by push: "New job J-1042: installation, 12 Allen Avenue, today 10 to 12". There is no accept or decline: a job assigned is a job given, as at work. An agent who cannot go says so (Could not finish, or a note), and the office reassigns.

## 10. On duty, and where people are

**On duty is a field agent's own choice, made in the app.** **Start my day** begins it; **End my day** ends it; ending the last job of the day asks "End your day?". The office sees each agent as **off duty**, **free** (on duty, nothing in progress), or **on a job** (with its step). A day left open is closed at midnight, marked, and the agent told.

**Location is collected only while on duty, and never otherwise.** Starting the day asks for the phone's location permission the first time, saying why: "Your location is shared with Ada Networks while you are on duty, so the office can send you the nearest jobs and customers can see you're on the way. It stops when you end your day."

| | Android | iPhone |
| --- | --- | --- |
| While on duty | A foreground service, with its notification "Sharing your location with Ada Networks while on duty", the only honest way to report with the screen off | "While using the app" with background location updates, the blue indicator shown; "Always" is never asked for |
| How often | The setting: every 30 seconds when moving, 5 minutes when still (the phone's own motion detection) | The same, within what iOS allows; significant-change updates when backgrounded long |
| When the app is killed | The service stops; the office sees **location stopped** (§16) | The same |

Each report is a place, its accuracy and the phone's time, sent in batches; reports made offline are kept on the phone and sent later (§12). Every step also carries its own fresh place (§7), taken then, independent of the reports.

**The live map** (web, Field work > Map): each on-duty agent's last place, how old it is, their state and job; each open job's place, coloured by state. A job's page shows its trail from On my way to Done. An agent's page shows their day's trail. Nothing shows an off-duty agent's place, ever.

## 11. The app for people in the field

The field mode is the Taskiem app (PR #198), not a second app. A field agent signs in as themselves, switches into the business, and the app opens on **Jobs**, because their role in that business is Field agent. Their own chats with people and other businesses stay where they are.

| Screen | What it has |
| --- | --- |
| **Today** | Start my day / End my day; the job in progress, with its next step as one large button; today's jobs in order of their windows; tomorrow's below |
| **Job** | Customer's name and phone (Call), the place (Directions opens the phone's maps), the window, what to do, the checklist, notes, photos (camera), parts used (from My stock), the steps; Could not finish; the customer's confirmation when asked for |
| **My stock** | What they carry (§13) |
| **History** | Their jobs done, with the office's notes and the customers' ratings |
| **Me** | Their week: days on duty, hours, jobs done, rating; the location permission and battery saving advice |

- **Notifications** by push (Firebase, as the app's chat): a job assigned, changed, held, cancelled; a note from the office; a reminder ten minutes before a window with no On my way.
- **Chat with the office** is the business's team conversation, not a new thing; a job's page has **Message the office about J-1042**, which opens it with the job's card.
- **Look:** the app's own, as [the screens to redesign](mobile-app.md) list asks; one large button per step, readable in sunlight, usable with one hand.

## 12. Working without a connection

Field work happens where signal is poor, so the field mode works offline for what the agent does on a job.

**Queued on the phone and sent when back:** every step (with the phone's time and place), checklist answers, notes, photos, parts used, the customer's confirmation, location reports, start and end of the day. Each has an idempotency key made on the phone, so a send repeated after a timeout lands once.

**Kept on the phone for reading:** the agent's jobs for today and tomorrow (customer, place, what to do, checklist), their stock, the business's job types, in the app's encrypted store ([explore and offline](mobile-app-explore-and-offline.md)), deleted 24 hours after a job is done.

**This widens the app's rule** that "anything that changes money or stock is not queued". Parts used are part of the Done step, queued with it; they are taken from the agent's stock when the step lands at the server, never below zero (beyond is recorded as `short`, as Stock does). This is safe because the step records a fact already true (the cable is in the wall), not a sale or a payment that could still be refused. Nothing else that changes money or stock is queued. 0070 records the exception.

**What the office sees:** a step that came in late shows "sent 2:14 pm, done 11:02 am (offline)". A job whose agent has been out of touch for an hour while on a job is flagged (§16). Conflicts are few because the agent alone moves their job's steps; when the office cancelled or reassigned a job while the agent was offline, the agent's later steps are kept as the agent's record and the job does not move, and both sides are told.

## 13. Parts carried and used

With **Stock** on, Field work adds **where** stock is: in the store, or **with an agent**.

| Movement | Made by | Effect |
| --- | --- | --- |
| `issued` | Office: "Give Musa 2 routers, 100 m of cable" | Store count down, Musa's up |
| `used` | The Done step's parts | Musa's down; the job records it |
| `returned` | Office, when the agent hands back | Musa's down, store up |
| `lost` | Office, with a reason | Musa's down |

- The catalogue table's count stays **the store's** count, as today; what agents hold is a record of its own (`stock_held`), shown on the item ("12 in store, 5 with agents") and on each agent's page, and copied to a kept table for workflows.
- Movements follow Stock's rules: one change each, under an idempotency key, never below zero.
- Low-stock alerts can watch an agent's holding ("Musa has 1 router left").
- With Stock off, parts used are free text on the job, counted in reports by name, and nothing is issued or returned.

## 14. The customer's side

A job's customer is a customer of the business: a person on Taskiem who chats with it (PR #198), or a contact by phone.

**In the customer's conversation, a job card**, the same kind as an order or delivery card:

- what (the job type, in the business's words), the window, who is coming (name and photo, never their number);
- the state as it moves: assigned, **on the way** (with a live map while on the way: the agent's place, refreshed, and nothing after Arrived), arrived, done;
- **Confirm the work is done**, when the business asks for it: a tap in the card, or, for a customer without the app, a 4-digit code on the card or in their SMS, read out to the agent;
- **Rate the visit** (1 to 5 stars and words) after Done; the rating goes on the job and the agent's record.

**Booking a visit:** the business's conversation gains **Book a visit** when Field work is on and the business chooses to offer it: the customer picks a job type the business lets customers book, gives the place (a saved address, a pin) and the times they are in (visit windows, as ISPOps' customer app had). That makes a **new** job, unassigned, for the office.

**Customers not on Taskiem** get the same moments by WhatsApp or SMS through [updates](../updates.md), with a link to a page showing the job and, while on the way, the map. The page needs no sign-in, carries a token for that job alone, and ends with the job.

## 15. The office's side: web and chat

**Field work** on the web:

| Page | What it has |
| --- | --- |
| **Board** | Today: new, assigned, on the way, on site, done, late, not finished; each agent's state; drag a job to an agent |
| **Map** | §10 |
| **Jobs** | Filters (state, type, agent, day, customer), search by reference or customer; **New job** (from a template, a customer and a place, with a map to drop the pin); a job's page with its timeline, map and trail, checklist, photos, notes, parts, rating, and the office's actions |
| **Agents** | Each field agent: state, today, history, hours on duty, jobs done, average time on site, rating; stock with them |
| **Reports** | Jobs by type, state and agent over a period; time to arrive, time on site; late and not finished; ratings. CSV, as ISPOps' reports |
| **Settings** | §4; job types and templates open their tables |

**In chat**, `@Taskiem` gains the dispatcher's words, in every language the chat has: "new job: installation for Ada Obi at 12 Allen Avenue tomorrow morning", "assign J-1042 to Musa", "where is Musa", "jobs today", "what's late". Each answers with job cards, and changes need a yes, as other chat changes do.

## 16. Watching for trouble

Alerts, through [alerts](../alerts.md), to the people with `job.manage` (or the roles the business picks), and each a trigger (`late`, `too_long`) for workflows:

| Alert | When |
| --- | --- |
| Late | Not arrived by the end of the window plus the setting |
| Too long on site | Working past the template's time plus the setting |
| Location stopped | On duty, and no report for 15 minutes (the app killed, the phone off, no permission) |
| Out of touch | On a job and nothing heard for an hour |
| Away from the place | Arrived or Done farther than the setting |
| Not started | A window began and the assigned agent is off duty |

## 17. Hours, jobs and pay

- **Hours** are on-duty time; **jobs** are done jobs; both per agent per day, in reports and in a kept table for workflows.
- Pay stays out of the record. Templates send them where pay is worked out: `payrolla-record-payroll` with hours (Payrolla is connected already, as ISPOps used it), or a pay list (`pay-a-list`) for a per-job fee. ISPOps' embedded payroll screen is not carried over (§20).
- A field agent's bank account for pay is theirs as a member, as riders' are kept, if a pay list is used.

## 18. Privacy and safety

- **Field agents' location is their personal data**, collected for a stated purpose, only while on duty, shown only to `job.read`, deleted after the trail setting (default 90 days); steps keep their own place for the job's record. The agent sees their own trail. The [privacy notice](../privacy.md) and the business's own staff notice say so; the app says so when asking.
- **Customers' names, phones and addresses** are sealed and masked as elsewhere. A field agent sees them for their jobs only, from assignment until 24 hours after Done, and then only the job's reference and type.
- **The customer's map** shows the agent's place only while on the way, from the agent's last report, and never their phone number.
- **Photos** are scanned and cleaned of their own location and metadata, as all media; the step's place is kept on the step, not in the photo.
- **Every human action is audited**: assignments, steps, corrections, settings, stock, trails opened by someone in the office (who opened whose trail, when).

## 19. Data and server changes

| Kind | What |
| --- | --- |
| Area | `fieldwork` in `engine/areas`, with its door checks (routes, steps, chat words, app screens) and `areas.Affected` for workflows using it |
| Permissions and roles | `job.read`, `job.manage`, `job.work`; built-in **Field agent** (field-only, like Gate); custom **Dispatcher** on turning on |
| Records (module schema `fieldwork`) | `jobs`, `job_steps`, `job_agents`, `job_notes`, `job_media`, `job_parts`, `duty_days`; `stock_held` and new movement reasons in Stock's schema |
| Engine state | `agent_reports` (location reports, partitioned by day, deleted after the trail setting); the per-business job counter |
| Lists (the business's project) | `job_types`, `job_templates`, known tables in `templates/tables` |
| Kept copies | `jobs` (and `duty_days`) in the business's project, for workflows, chat search and the builder |
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
| Clock in and out per job with GPS and an idempotent offline sync | Steps with time and place (§7, §12) | Carried over, and finished: ISPOps' app never queued (its outbox was never filled) |
| Location pings every 30 seconds, in the foreground only | Reports while on duty, in the background (§10) | Carried over and widened |
| Agent status (available, assigned, en route, on site, offline) | On duty, free, on a job (§10) | Carried over |
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

**Known faults not to carry over** (found when ISPOps was read, 18 November 2026): the app's offline queue is never filled (`SyncDao.enqueue` has no caller), so clocking fails offline; the weak-GPS check in `clock_in_sheet.dart` compares accuracy with a colour's hash; a failed Depart is swallowed silently; the app handles one job at a time and ignores `on_hold` and `closed`; location is sent in the foreground only and dropped when offline.

## 21. Milestones

| # | Milestone | Size |
| --- | --- | --- |
| F0 | The area, permissions, Field agent and Dispatcher roles, settings; the job record and its journey; `/v1/jobs`; audit | L |
| F1 | Web: Jobs (list, new, the job's page), Board, Agents; job types and templates as tables | L |
| F2 | The app's field mode: Today, Job, steps with a place, notes, checklist, photos; push | L |
| F3 | Working offline: the outbox for steps and everything on a job; the encrypted copy of the day's jobs | M |
| F4 | On duty and location: Start and End my day, background reports, the Map, trails | L |
| F5 | The `job` trigger, signals, `jobs@1`, the templates, the AI builder | M |
| F6 | The customer's side: the job card, the live map, confirmation, rating, Book a visit, the public page and updates | M |
| F7 | Stock with agents | M |
| F8 | Watching for trouble: alerts; Reports and CSV; hours and jobs to pay templates | M |
| F9 | `@Taskiem` words for dispatchers; the guide; the business's starting set when it turns Field work on | S |

F0 to F4 are the minimum for the first business to stop using ISPOps; F5 and F6 make it Taskiem's rather than a copy of ISPOps.

## 22. Testing

- **Go:** the journey (every allowed and refused move, by role; Done refused until its rules are met; idempotent steps sent twice; late steps after a reassignment; times kept both ways); the Field agent role refused every route but its own, as the Gate role's router-walk test; the area off refusing routes, steps, chat words and the app; stock with agents (never below zero, `short`, used on a step that lands late); location reports only while on duty and deleted after the setting; the `job` trigger and signals; `jobs@1` against its contract; audit entries.
- **Web:** each page, with Field work off and on; the board's drag; the map with no agents on duty; accessibility and plain-words lists.
- **App:** the field mode only for `job.work`; steps offline then online; a step while the job was cancelled offline; permission refused; the foreground service starting and stopping with the day.
- **On real phones:** background location on Android (several makers' battery savers) and iPhone, over a working day, with battery measured.
- **End to end:** an office member makes and assigns a job; the agent starts the day, goes, arrives, works offline, finishes with a photo and parts; the customer sees the card move, confirms and rates; the invoice template runs on Done.

## 23. Risks and open questions

- **Background location on Android** is the hardest part and the one most likely to fail quietly: makers' battery savers kill services. The plan tests on real phones (F4), tells the agent how to exempt the app, and flags **location stopped** rather than trusting it.
- **App-store review** looks hard at background location. The purpose (staff on duty, stated, stopped with the day) is the accepted kind, but the wording and the store's forms need care.
- **Staff location and the law.** Employees' location is personal data under the NDPA. The design collects it only on duty, for a stated purpose, with a retention limit; a lawyer should see the staff notice (with PR #198's other legal reviews).
- **One app for customers and staff.** A field agent's personal chats and their work are in one app. The field mode is the business's; the person's own side is theirs. Is that what businesses want, or will some ask for work phones with nothing else?
- **The core-cleanup decision (0064)** is proposed, not accepted. This plan follows it (module schema, kept copies, lists as tables); if it changes, §19 changes with it.
- **Naming:** "Field work" and "Field agent" need the plain-words pass and an owner's ear; "technician" may be the common word.
- **Open:** whether the office can see a field agent's trail for a past day after End my day (planned: yes, within the retention); whether helpers have steps of their own or follow the lead; per-job prices on job types or only in templates.

## 24. Out of scope

- Field agents working by WhatsApp, Telegram or SMS (decided: the app only, for location).
- People who are not members doing jobs (contractors); they may come later as linked businesses, as riders do.
- Route planning and optimising a day's order of jobs.
- Signatures drawn on the screen (the confirmation is a tap or a code).
- Timesheets and leave beyond on-duty time.
- Shift rosters (who works which day).
