# Field work: worked examples

Status: **Draft** 2026-11-18, with [Field work](field-work.md) and [internet providers](isps.md) · Decision: [0070](../decisions/0070-field-work-and-isps.md) (proposed)

Each example follows one job, or a few, from the moment it is made to the moment it is closed: who does what, on which screen, what the engine records, which triggers fire, and what each person sees. They are how the plans are checked against real work, and the end-to-end tests are written from them. Where an example and the plans disagree, the plans are wrong or the example is, and one of them is fixed in the same pull request.

Contents:

1. [How each example is written](#1-how-each-example-is-written)
2. [The cast](#2-the-cast)
3. [Example 1: a subscriber's complaint becomes a repair, worked partly offline](#3-example-1-a-subscribers-complaint-becomes-a-repair-worked-partly-offline)
4. [Example 2: the office makes the jobs, one assigned at once and three assigned later in bulk](#4-example-2-the-office-makes-the-jobs-one-assigned-at-once-and-three-assigned-later-in-bulk)
5. [Examples still to write](#5-examples-still-to-write)

---

## 1. How each example is written

Every example has the same parts, in this order:

| Part | What it holds |
| --- | --- |
| **Setting** | The business, its areas and the settings that matter here (location, photo, confirmation), and the templates it has on |
| **People** | Who takes part, and their roles |
| **Steps** | Numbered, in time order, grouped by moment. Each says who acts and where; what the engine records (state, times, place, history, audit); which triggers fire and what their templates do; what each person then sees |
| **Variations** | What changes if one thing goes differently, with a pointer to the example that covers it in full |
| **What it checks** | The rules of the plan the example exercises, by section |
| **Open points** | What writing the example showed the plan does not yet say |

Words are the plans' own: the job's states are **new**, **assigned**, **on the way**, **on site**, **done**, **closed**, **on hold**, **not finished** and **cancelled**; the field agent's taps are **Start job**, **Arrived** and **End job**; the `job` trigger's events are `created`, `assigned`, `started`, `arrived`, `ended`, `not_finished`, `on_hold`, `resumed`, `cancelled`, `closed`, `late`, `too_long` and `rated` ([Field work §7, §8](field-work.md#7-the-journey-of-a-job)).

## 2. The cast

The same business and people appear in every example unless one says otherwise, so the examples can refer to each other.

| Who | What they are |
| --- | --- |
| **Ada Networks** | An internet provider in Lagos on PHP Radius, with the ISP area on (so Field work and Money too) and Stock on |
| **Ngozi** | Ada's customer care, with the **Customer care** role ([ISP §8](isps.md#8-staff-and-roles)) |
| **Bola** | Ada's dispatcher, with the **Dispatcher** role |
| **Musa**, **Tunde** | Field agents, with the **Field agent** role, each on the Taskiem app on their own phone |
| **Chidi Okafor** | A subscriber on the Home 20 Mbps plan, on the Taskiem app, his phone proved, at 14 Adeola Street |
| **Mrs Bello** | A subscriber not on Taskiem, reached by SMS |
| **Kemi Adeyemi** | A new subscriber who has chatted with Ada in the Taskiem app |
| **Lekki Plaza** | A business customer not on Taskiem, with three rooftop radios; its facilities manager is **Mr Okon** |
| **The owner** | Ada's owner |

Ada's settings, unless an example says otherwise: location **during jobs**; Arrived within **300 m**; a photo needed to end a job **on**; the customer's confirmation **on**; late after **30 minutes** past the window; a complaint opens a repair job **on**. Templates on: `isp-complaint-to-job`, `job-tell-office`, `job-tell-customer-assigned`, `job-tell-customer-on-the-way`, `job-ask-rating`, `job-late-alert`, `job-follow-up-not-finished`.

## 3. Example 1: a subscriber's complaint becomes a repair, worked partly offline

**Setting:** the cast's. Musa holds three jobs today: J-1041 at 9:00, J-1042 (this one) between 10 and 12, J-1043 in the afternoon. The office issued him 3 connectors and 50 m of cable this morning (two `issued` movements: the store's counts down, Musa's up).

**People:** Chidi, Ngozi, Musa.

The job is made by a template from the customer's complaint. Jobs the office makes itself are [Example 2](#4-example-2-the-office-makes-the-jobs-one-assigned-at-once-and-three-assigned-later-in-bulk).

### Steps

**Before the job**

1. Musa signed in to the Taskiem app with his own phone and switched into Ada Networks. Because his role there is Field agent, the app opens on **Jobs**: his job in execution, if any, then his schedule, earliest first. The server refuses him every route but his own jobs and his stock ([Field work §5](field-work.md#5-who-can-do-what)).

**The job is made (8:10)**

2. Chidi opens his conversation with Ada Networks. The first time he uses one of its tools, Taskiem signs him in to PHP Radius with the phone Taskiem has proved is his (`sign_in`, PHP Radius's `loginPhone`), and asks "Is this your account? **Chidi Okafor**, plan **Home 20 Mbps**". He taps **Yes**; his conversation is **linked** and the token kept sealed for him and Ada only ([ISP §5.3](isps.md#53-signing-a-subscriber-in)).
3. He taps **My internet**, then **Report a problem**, picks "Keeps dropping" and writes "Since yesterday evening". `open_complaint` files it in PHP Radius as complaint **#47**; a card in his conversation shows the number and "open".
4. The area's event `isp.complaint` starts `isp-complaint-to-job`, which asks Chidi "When are you in for a technician?" and he picks **today, 10 to 12**. The template calls `jobs.create`: **J-1042**, type **fault repair**, priority normal, customer Chidi, place his saved address with its map pin, the visit window, the fault-repair template's checklist ("Check signal at the radio | note", "Check cable and connectors", "Restart and test speed | photo"), came from: complaint #47.
5. **Recorded:** J-1042 in state **new**; its history's first line (made by the template, for complaint #47); the audit entry. **Fires:** `job` `created`; `job-tell-office` puts it on the office's notifications.

**Assigning (8:20)**

6. On the web, Ngozi sees J-1042 in the board's **New** column and opens it. **Assign** lists suggestions: **available** agents first, then those holding jobs, fewest first; within each, nearest by last known place. She sees "Tunde: available, near J-1039, 50 min ago, 3.0 km" and "Musa: assigned, 2 jobs today, near the office, 1.2 km". She picks Musa.
7. **Recorded:** J-1042 **assigned** to Musa, by Ngozi; the assignment in the job's assignments and history; Musa's state worked out again (still **assigned**: he holds jobs, none started); audit. **Fires:** `job` `assigned`:
   - Musa's phone gets a push, "New job J-1042: fault repair, 14 Adeola St, today 10 to 12"; it joins his schedule between J-1041 and J-1043. There is no accept or decline.
   - `job-tell-customer-assigned` puts a **job card** in Chidi's conversation: "Musa will visit today between 10 and 12", with Musa's name and photo, never his number.
   - `job-tell-office` notes it.
8. Until Musa starts it, Ngozi or Bola can still reassign J-1042 to someone else.

**Musa's first job (9:00 to 9:35)**

9. Musa works J-1041 the same way (Start job, Arrived, End job). When it ends, he holds two jobs and none in execution, so his state goes back to **assigned**.

**Start job (9:40)**

10. Musa opens J-1042 from his schedule: reference (copy), Chidi's name and phone (**Call**), the place on a small map (**Directions** opens his maps app), the window, what to do, the checklist, his stock. He taps **Start job**.
11. **Checked:** J-1042 is assigned and his own; he has **no other job in execution** (with J-1041 still on the way, on site or on hold he would be refused, "End J-1041 first").
12. **Recorded:** J-1042 **on the way**; the step with the phone's time 9:40, the server's time, his fix and its accuracy; history; Musa's state **on the way**. J-1042 can no longer be reassigned.
13. **Location for this job starts:** the app starts the foreground service with its notification "Sharing your location with Ada Networks for job J-1042" (the first time ever, it asks for the location permission and says why). Reports come from a position stream as he moves, each with its fix's own time.
14. **Fires:** `job` `started`; `job-tell-customer-on-the-way` changes Chidi's card to "Musa is on the way", with a live map of Musa's last report, refreshed. On the office's map, Musa appears on his way to the job's pin.

**Arrived (9:58)**

15. Musa taps **Arrived**. His fix is 40 m from the pin: within 300 m, so at the place. (Two kilometres away, the step would still be recorded, marked **away from the place**, with an alert to the office.)
16. **Recorded:** J-1042 **on site**; the step with its times, place and distance; travel time 18 minutes; history; Musa's state **on site**. **Fires:** `job` `arrived`. Chidi's card says "Musa has arrived" and its map stops.

**The work, without signal (10:00 to 10:47)**

17. Inside the compound there is no signal. The app shows "Offline: your work will be sent when you're back" and keeps working from the copy of J-1042 on the phone.
18. Musa answers the checklist (signal reading as a note; cable and connectors checked; a photo after the speed test), adds a note, "Connector at the radio corroded, replaced", and records **parts used** from his stock: 1 connector, 5 m of cable. Each is queued on the phone with its own idempotency key and the phone's time.
19. The customer's confirmation is on, so before ending, Chidi confirms: he taps **Confirm the work is done** in his card (his own phone has signal). Without the app he would read Musa the 4-digit code from his SMS.
20. At 10:47 Musa taps **End job**. The phone checks what it can (required checklist items answered, a photo taken) and queues the step. **Location for J-1042 stops** on the phone. Musa's next job, J-1043, can now be started even before anything is sent.

**Signal returns (11:02)**

21. The app sends its queue: the checklist, photos, note, parts, End job, and the location reports kept while offline. Each lands once; a repeat after a timeout is answered as already done.
22. **Checked at the server:** the photo setting (a photo has landed), the confirmation (Chidi's tap), the required checklist items.
23. **Recorded:** J-1042 **done**, at 10:47 by the phone, received 11:02, marked **offline**; 49 minutes on site; the checklist answers, photo, note; a `used` movement for each part (Musa's connectors 3 → 2, cable 50 → 45 m; the store's counts unchanged); history; Musa's state worked out again. **Fires:** `job` `ended`:
   - `job-ask-rating` puts **Rate the visit** in Chidi's conversation.
   - `job-tell-office` notes it.
   - A run waiting on `job.ended` for J-1042, if any, goes on.

**Rating and closing (11:15 onwards)**

24. Chidi gives 5 stars, "Fast and polite". **Recorded:** the rating on J-1042 and on Musa's record. **Fires:** `job` `rated`.
25. Ngozi opens J-1042: its history, each step's phone and server times, the trail on the map from 9:40 to 10:47, the photo, checklist, note, parts and rating. She taps **Close**. **Recorded:** **closed**, by Ngozi; audit. **Fires:** `job` `closed`.
26. Complaint #47 is still open in PHP Radius: the client API has no call to close it. Ada's staff close it in their PHP Radius dashboard (see Open points).

**The rest of Musa's day**

27. Musa does J-1043 the same way. When it ends and he holds nothing, his state is **available**. There is nothing to end for the day: his jobs done and time on jobs (travel and on site) are in the reports, and Ada's pay template sends them to Payrolla on Friday.

### Variations

| If | Then | Covered in full by |
| --- | --- | --- |
| Chidi is not in | Musa taps **Could not finish**, "customer not in"; the job is **not finished** and Musa can start his next; `job-follow-up-not-finished` makes a new visit for Chidi, which the office schedules | Example (to write): a visit that could not finish |
| Ngozi puts J-1042 on hold while Musa is on site | It is **on hold**, still Musa's one job in execution; location stops; Resume returns it to on site | Example (to write): held and resumed |
| Bola cancels J-1042 while Musa is offline | Musa's later steps are kept as his record, the job stays **cancelled**, and both are told when he is back | Example (to write): the office cancels a job while the agent is offline |
| J-1042 is not started by 10:00, or not arrived by 12:30 | `not started`, then `late`: the office is alerted; `job-late-alert` may tell Chidi | — |
| The app is killed during the job | No report for 15 minutes: **location stopped** alert | — |
| Ada turns location off | Steps carry times only; no maps, trail, distance or location alerts; suggestions have no distance | Example (to write): a business without location |
| The customer is Mrs Bello, not on Taskiem | The same moments by SMS, with a link to the job's own page and, while on the way, its map; confirmation by the code | Example (to write): a customer not on Taskiem |

### What it checks

- The job's journey and its rules: one job in execution per agent; no reassigning after Start job; End job refused until its rules are met ([Field work §7](field-work.md#7-the-journey-of-a-job)).
- Agents' states read from their jobs, and suggestions by state and last known place ([§9](field-work.md#9-assigning), [§10](field-work.md#10-agents-states-and-where-people-are)).
- Location during a job only, starting at Start job and stopping at End job ([§10](field-work.md#10-agents-states-and-where-people-are)).
- Working offline: queued steps landing once, both times kept, parts taken from the agent's stock when End job lands ([§12](field-work.md#12-working-without-a-connection), [§13](field-work.md#13-parts-carried-and-used)).
- Triggers and templates at each step ([§8](field-work.md#8-each-step-is-a-trigger)); the customer's card, live map, confirmation and rating ([§14](field-work.md#14-the-customers-side)).
- A subscriber linked by proved phone and a complaint filed in PHP Radius ([ISP §5.3](isps.md#53-signing-a-subscriber-in), [§6.4](isps.md#64-complaints)).

### Open points

1. **Closing the PHP Radius complaint.** The client API opens and reads complaints but cannot close them. Taskiem can remind Ada's staff on Close ("Close complaint #47 in PHP Radius"), but cannot close it itself.
2. **The visit window on a complaint.** This example has `isp-complaint-to-job` ask Chidi when he is in, as Book a visit does. [ISP §6.4](isps.md#64-complaints) does not yet say so.
3. **The confirmation code while the agent is offline.** It can only be checked when End job lands. The phone could hold a hashed copy to catch a wrong code at once; [Field work §14](field-work.md#14-the-customers-side) does not yet say.
4. **An arrival estimate** on the "on the way" card is not in the plan, and would need a routing service.
5. **More than one agent on a job** (helpers), and an agent marking themselves **not available** (leave, sickness): neither is in ISPOps or the plan ([Field work §23](field-work.md#23-risks-and-open-questions)).

## 4. Example 2: the office makes the jobs, one assigned at once and three assigned later in bulk

Jobs do not only come from customers' complaints and bookings: the office makes them. This follows ISPOps' **Create new job** page and its two buttons, **Create & Assign** and **Save as Draft** (`exact-hue-hearth`, `src/routes/_app.jobs.new.tsx`; the server's `POST /api/v1/jobs` in `ispops-backend`, `internal/httpapi/job_handler.go`), then its bulk **Assign to agent** on the jobs list (`src/routes/_app.jobs.index.tsx`), as [Field work §15.1](field-work.md#151-making-a-job-in-the-office) has them.

**Setting:** the cast's. On Wednesday, Musa holds two jobs for Thursday morning; Tunde holds none.

**People:** Ngozi, Bola, Musa, Tunde, Kemi, Mr Okon of Lekki Plaza.

### Steps

**Part A: made and assigned at once (Wednesday, 9:05)**

1. Kemi phones Ada's office: she has moved, and wants internet installed at her new flat, 22 Bourdillon Road, tomorrow morning.
2. Ngozi, on the web, opens **Field work > Jobs > New job**.
3. **Customer.** She types "Kemi" in the search. Ada's customers include Kemi, because she has chatted with Ada; picking her fills her name and phone, and offers her saved addresses. The new flat is not among them, so Ngozi types "22 Bourdillon Road, Ikoyi" and drops the pin on the map. (Without a pin, the form says "Arrival won't be checked against the place".)
4. **Template.** **Start from a template**: "Home installation (radio)". Type **installation**, priority **normal** and the description fill in, still editable; the template's checklist shows below ("Mount the radio | photo", "Signal reading | note", "Router set up and customer connected", "Customer shown how to recharge in Taskiem"). Ngozi adds an item for this job: "Mount on the rear wall, as the customer asked".
5. **When.** A visit window: Thursday, 9 to 12. **What to do** keeps the template's words; in **Note for the office** (the agent does not see it) she writes "New flat; referred by Chidi Okafor".
6. **Assignment.** The panel lists every field agent with their state and suggestions ([Field work §9](field-work.md#9-assigning)): "Tunde: available, near the office, 1 km" and "Musa: assigned, 2 jobs Thursday, near J-1047, 2.4 km". Ngozi wants Musa, who installed in that estate before, and picks him. (ISPOps would not have shown Musa: its form lists only agents whose status is available.)
7. She taps **Create and assign**.
8. **Checked:** a customer, a type and a place; Musa is a field agent of Ada's.
9. **Recorded:** **J-1050**, with two lines in its history, made and assigned, both by Ngozi; Musa's assignment; the template's checklist with Ngozi's extra item and the template's expected time; the office note, kept apart from what the agent sees; audit. Musa's state stays **assigned**; J-1050 joins his Thursday schedule after his two other jobs, by its window.
10. **Fires:** `job` `created` and `assigned`:
    - `job-tell-office` notes the new job for the office.
    - Musa's phone gets a push, "New job J-1050: installation, 22 Bourdillon Rd, Thursday 9 to 12".
    - `job-tell-customer-assigned` puts a **job card** in Kemi's conversation with Ada: "Musa will install your internet on Thursday between 9 and 12", with his name and photo.
11. Ngozi lands on J-1050's page and reads the reference to Kemi, who is still on the phone.

**Part B: saved without assigning (Wednesday, 14:00)**

12. Mr Okon emails Ada: Lekki Plaza's three rooftop radios are due their quarterly maintenance, any time on Friday between 10 and 2.
13. Bola opens **New job**. **Customer:** "Lekki Plaza" is not found, so Bola types the name, Mr Okon's phone and the address, and drops the pin. **Template:** "Quarterly maintenance". **When:** Friday, 10 to 2. **What to do:** "Unit 1 of 3: north mast". He taps **Save without assigning**.
14. **Recorded:** **J-1051**, state **new**, made by Bola; Lekki Plaza **saved as a contact** of Ada's, with that address and pin. **Fires:** `job` `created`; `job-tell-office` only. No agent and no customer is told: nobody is assigned yet.
15. He makes **J-1052** and **J-1053** the same way. This time the search finds Lekki Plaza, with its address and pin.

**Assigning in bulk (Thursday, 17:00)**

16. Bola opens **Jobs**, filters **state: new**, **type: maintenance**, and ticks J-1051, J-1052 and J-1053. Only new and assigned jobs can be ticked.
17. **Assign to agent** lists the agents as in §9. Tunde is **available**; Bola picks him.
18. **Checked:** every ticked job is still new or assigned, and Tunde is a field agent. **All or nothing**: had one been cancelled in the meantime, none would be assigned and Bola would be told which one stopped it (as ISPOps' `AssignBulk`).
19. **Recorded:** the three assignments, each in its job's history, by Bola; Tunde's state **assigned**; audit. **Fires:** `job` `assigned` three times:
    - Tunde's phone gets one push per job; his Friday schedule shows the three, in the order Bola made them, all in the same window.
    - `job-tell-customer-assigned` reaches Mr Okon, who is not on Taskiem, **by SMS** through [updates](../updates.md): "Ada Networks: Tunde will visit Lekki Plaza on Friday between 10 and 2 for maintenance (J-1051)", once for each job.
    - `job-tell-office` notes them.

**A reassignment, and one refused (Friday)**

20. At 8:00, so that the three units are done sooner, Bola opens J-1053, still **assigned**, and **Reassign**s it to Musa. **Recorded:** the reassignment in the history, with Tunde as the earlier agent; both agents' states worked out again. **Fires:** `assigned`. Musa gets the push, Tunde's app drops the job, and Mr Okon gets the SMS with Musa's name.
21. Tunde starts **J-1051** at 10:05 (**Start job**). At 10:10 Bola tries to reassign J-1051 to Musa as well. It is refused: "J-1051 is on the way; a started job can't be reassigned. Hold or cancel it first." (ISPOps' `assignable`.)

**The work**

22. Musa works J-1050 on Thursday, and Tunde and Musa work the Lekki Plaza jobs on Friday, as in [Example 1](#3-example-1-a-subscribers-complaint-becomes-a-repair-worked-partly-offline) steps 10 to 25: Start job, Arrived, End job, the customer's card or SMS at each step, the rating, and Close.
23. Things that differ for an installation:
    - The parts used are a radio, a router and 20 m of cable, from Musa's stock.
    - If Ada has `job-invoice-on-done` on for installations, End job makes an invoice for the installation fee, with the parts as lines, and puts it in Kemi's conversation with **Pay**.
    - Kemi's PHP Radius account is made by Ada's staff in their PHP Radius dashboard. Kemi then links to it in chat with her proved phone ([ISP §5.3](isps.md#53-signing-a-subscriber-in)).

### Variations

| If | Then | Covered in full by |
| --- | --- | --- |
| Ngozi makes the job in chat: `@Taskiem new job: installation for Kemi Adeyemi at 22 Bourdillon Road, Thursday morning, give it to Musa` | A draft job card asks her to check it (customer found, address, template, window, Musa), and **Make it** saves it as in step 9. A place without a pin is pointed out | — |
| No field agent is free and Ngozi does not want to add to anyone's schedule | **Save without assigning**; the job waits under **New** on the board until someone is assigned, and **not started** fires if its window begins first | — |
| The phone Bola types for a new customer is already one of Ada's customers | The form offers that customer instead ("This number is Kemi Adeyemi's: use her?") | Open point 2 |
| Mr Okon is a person on Taskiem who chats with Ada | The job card in his conversation, as Kemi gets, instead of SMS | Example 1 |

### How this differs from ISPOps

| ISPOps | Here | Why |
| --- | --- | --- |
| Only agents whose status is **available** can be picked, at creation and when assigning on the job's panel | Every field agent, with their state and suggestions; an agent holding jobs can be given more | ISPOps' own server allows it (multi-job assignment, migration 000013). Its form had not caught up |
| Picking a customer fills name and phone, not the address | Name, phone and saved addresses with their pins | The address is what the agent needs |
| A typed customer is not kept | Kept as a contact of the business | Their next job finds them |
| The rating email goes only to a linked customer with an email address | Every customer gets the rating, by card or by SMS | Most of Ada's customers have no email on record |
| One field, "Description / internal notes", seen by the agent | **What to do** for the agent; **Note for the office** kept apart | Office notes about money or referrals are not the agent's business |
| The template's checklist is attached unseen | Shown, and can have an item added | The office can say what this job needs |
| Lands on the jobs list after saving | Lands on the new job's page | The reference is wanted at once |
| The agent and staff are told only when assigned at creation; the customer only by email when a visit window is linked | The office always; the agent and the customer when assigned, whenever that is | §8 |

### What it checks

- Making a job in the office: customer search or typed contact, template, place, window, the two buttons ([Field work §15.1](field-work.md#151-making-a-job-in-the-office)).
- Assignment rules: new or assigned only, all or nothing in bulk, no reassigning after Start job ([§7](field-work.md#7-the-journey-of-a-job), [§9](field-work.md#9-assigning)).
- Suggestions by state and last known place, including agents who already hold jobs ([§9](field-work.md#9-assigning)).
- Who is told when: nobody but the office for an unassigned job; the agent and the customer on assignment, by card or SMS ([§8](field-work.md#8-each-step-is-a-trigger), [§14](field-work.md#14-the-customers-side)).

### Open points

1. **Where a business's contacts live.** A typed customer is "saved as a contact". PR #198 makes customers people with a conversation. The `customers` known table also exists. Which one holds a contact who is not on Taskiem is to be settled with PR #198's owners.
2. **Duplicate customers.** Offering an existing customer when a typed phone matches one is not yet in [§15.1](field-work.md#151-making-a-job-in-the-office).
3. **Many messages from a bulk assignment.** Mr Okon gets one SMS per job. Grouping them ("3 visits on Friday") is a template choice, or a rule for the updates channel. Not yet decided.
4. **ISPOps' availability windows.** In ISPOps a customer declares windows when they are in, and staff link one to an existing job, which emails a confirmation (`src/routes/_app.availability.tsx`). Here a customer gives their window when booking ([§14](field-work.md#14-the-customers-side)). For a job the office makes, nothing yet asks the customer "when are you in?" before assigning. A **Ask the customer for a time** action on the job, sending a card or SMS with choices, would cover it.
5. **A typed address for a known customer.** Should it be added to their saved addresses? It is not done in step 3.

## 5. Examples still to write

Each is a job, or a few, that tests a different part of the plans. Add one here when it is thought of; move it to its own numbered section when written.

| # | Scenario | Tests mostly |
| --- | --- | --- |
| 3 | A new subscriber books an installation in chat (Book a visit): parts issued and used, an invoice on End job, their PHP Radius account linked after | Book a visit, stock, `job-invoice-on-done`, ISP sign-up |
| 4 | A visit that could not finish (customer not in), and its follow-up visit | Could not finish, `job-follow-up-not-finished` |
| 5 | Held and resumed: parts missing, the job held, resumed the next day | Hold, Resume, one job in execution |
| 6 | The office cancels a job while the agent is offline | Offline conflicts |
| 7 | A business with location off | Every place-less path |
| 8 | A customer not on Taskiem from start to finish (Mrs Bello) | Updates by SMS, the job's page, the code |
| 9 | Recurring maintenance from a contracts table | `job-recurring-visit`, schedules |
| 10 | A day of several jobs for one agent, one of them late and one too long on site | Schedule order, alerts, time on jobs, pay |
| 11 | A business that is not an ISP: a solar installer on Field work alone | Field work without the ISP area |
| 12 | An outage: many complaints, many repair jobs, assigned in bulk | Bulk assign, `isp-outage-notice` |
| 13 | A subscriber recharges and redeems a voucher in chat, no job at all | ISP §6.2, §6.3 |