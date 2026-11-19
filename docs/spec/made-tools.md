# Made tools and pages: a workflow as a tool in the chat, and as a screen

Status: **plan; not built, and nothing here is decided.** Reviewed against the code the day it was written (§23 says what that changed) and again on 19 November, when pages were brought into it from a separate discussion (§24). Written 2026-11-13, after a conversation with the founder on 12 November about using Taskiem as a general automation tool and about workflows that people work with from the chat. §15 lists what the founder has to decide, each with a recommendation. Builds on [chat tools](chat-tools.md), [business chat tools](business-chat-tools.md), [WhatsApp](../whatsapp.md), [forms](../forms.md), [callable workflows](../contracts/wd-v1.md#callable-workflows) (decision 0048) and [no technical steps](no-technical-steps.md).

## 1. Summary

The tools in the chat today (Products, Stock, Orders, Tables, Money and the rest) are a list in code. A business cannot add one, and a new kind of work cannot have one until Taskiem builds it.

This plan lets **any workflow a person starts by hand become a tool**. The workflow says three things about itself: what it is called, what it needs, and what it answers with. From that one declaration Taskiem gives it a tile in the tools panel, a `/` command, plain words that find it, a form for its inputs, and an answer drawn in the conversation as text, a card, or a list with buttons.

Nothing about how a workflow runs changes. A tool starts a run the way `run <workflow>` does now; the run is durable, audited and under the same approvals. What is new is the way in and the way out.

Some work is not a message. An owner with twelve deliveries to confirm wants them on one screen with a button each, not twelve cards to scroll. So the same plan lets **a workflow declare its screens** (§12): named pages made of blocks Taskiem already draws (a figure, a list, a record, a table, a form, a row of actions), filled from the business's own tables and the run's outputs, placed under an area or in the tools panel, and drawn by the web, the phone app and, in a reduced form, WhatsApp. A tool's answer and a page's blocks are the same shapes (§7), so the two halves share one vocabulary and one renderer, and the built-in pages Taskiem hand-draws today (the deliveries page, the riders' Confirm) are what a workflow could have declared.

It serves both kinds of user:

- **A trader** types "fuel 4,500" and it is logged, or taps **Leave** and asks for three days off.
- **A builder** who uses Taskiem with every business part off types `/restart staging`, or gets "Disk at 90%" with a **Clean up** button.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| **Built-in tool** | A tool Taskiem ships: Products, Stock, Orders and the others in [chat tools](chat-tools.md) |
| **Made tool**, or just **tool** here | A workflow that declared itself a tool (§5) |
| **The reader** | What turns a typed message into a command: word lists first, then the model (`engine/ai/intent`), which may only choose among commands and never confirms anything |
| **Answer** | What a tool's run puts in the conversation when it ends (§7) |
| **Care** | How much a tool asks before it runs: nothing, **Yes**, or step-up (§8) |
| **Shape** | One of the things Taskiem can draw from data: text, a card, a list, a figure, a record, a table, a form, a row of actions. The answer of a tool is a shape; a page is a stack of them (§7, §12) |
| **Page**, or **view** in the definition | A screen a workflow declares, made of shapes, placed somewhere in the app (§12) |

Owners never see "made tool" or "view". On their screens it is **Your tools** and **Your pages**; making one is **Use it from chat** and **Show it as a page**.

## 3. What exists, and what is missing

| Piece | Where | What it gives |
| --- | --- | --- |
| `run <workflow>` | [WhatsApp](../whatsapp.md#for-people-using-taskiem-on-whatsapp), the same on Telegram, Slack and the web's Taskiem conversation | Finds a workflow deployed in `prod` by name, whatever its trigger, asks its inputs, shows what will run, waits for **yes**, and says how the run ended. Anyone with `run.start` may start any of them: **there is no rule for who may start one workflow and not another** |
| Inputs as a form | `taskiem_inputs` (a WhatsApp Flow), `SchemaForm` on the web | A form drawn from the workflow's input schema: ten flat fields at most in WhatsApp, asked one by one otherwise. **Telegram has no forms**: inputs are always asked one by one there. No date picker in WhatsApp's form yet |
| How a run ended | `waSendRunOutcome` in `api/whatsapp_approvals.go` | "*Workflow* completed" (or failed, with the step that failed in plain words) and a link to the run. **Never what the run found or made** |
| Outputs | `outputs` at the top of a workflow (decision 0048) | Named, typed values a workflow gives back. They are worked out when any run with outputs completes and kept in its `RunCompleted` event (`runOutputs` in `engine/decide`). Today only another workflow reads them |
| Approvals | The approval step, buttons and the PIN | A person decides with **Approve** and **Reject**, with step-up where the policy asks |
| Asking mid-run | The form step ([forms](../forms.md#a-form-step)) | A one-time link by SMS or email, or in the app for a role. **Not in the chat the run was started from** |
| Telling people | `notify.tell` (`connectors/notify`) | A message to the owner, a role or named members. **Text only, no buttons** |
| Public menus | [WhatsApp](../whatsapp.md#public-menus) | Up to nine workflows a business marks public on its own number. The person gets a reference, **not the outcome** |
| The tools panel | `web/src/lib/tools.ts`, `chatTools` in `api/chat_tool_requests.go` | Tiles, a sheet, `/` and a word. **A fixed list** |
| The reader's requests | `POST /v1/chat-tools/request` | A typed request is read by the model and answered with a filled-in form. **Built-in tools only.** Nothing is saved from words: the form's Save does it |

So the run, the form, the confirmation and the approval are built. Missing are the declaration, the answer, buttons, asking back in the same chat, and a panel and a reader that know about a business's own tools.

`whatsapp` is in the list of trigger types in `schemas/wd-v1.schema.json`, and no code under `engine/wd`, `engine/wdcheck`, `engine/runtime` or `api` reads a trigger of that type (checked 19 November). This plan does not use it; a later change may drop it from the schema.

## 4. The four things a tool is made of

The kinds of tool people will want (look something up, do something, record something, pick from a list, be asked a question, decide, be warned, get a report, see the whole of something at once) come down to four mechanisms.

| Mechanism | What it is | Kinds it covers | New work |
| --- | --- | --- | --- |
| **A tool that is called** (§5 to §8) | A workflow started by hand that declares itself, takes inputs and answers | Look up, Do, Record, Pick | The declaration, the answer, care, the panel, the reader |
| **A question inside a run** (§9) | A run stops to ask a person, in the chat it was started from, and goes on | Ask back, Decide | The form step in chat. Decide is the approval step, built |
| **A message with buttons** (§10) | A run that started by itself (a schedule, a webhook, an event) tells people, and its buttons call tools | Watch, Report | Buttons on `notify.tell` and on answers |
| **A page** (§12) | A screen the workflow declares: shapes filled from tables and outputs, with actions that are tools, signals or approvals | See, Work through, Report | The `views` block, the data reads, the placement, the renderers |

A useful tool is often two of these: a warning whose button is a Do, a list whose row action ends in an approval, or a page whose rows are the records a Do tool makes.

## 5. The declaration

An addition to `wd/v1`: an optional top-level `tool`. It needs a decision-log entry, as `outputs` and the form trigger did.

```json
"tool": {
  "name": "Log an expense",
  "command": "expense",
  "about": "Record money spent, with what it was for",
  "says": ["spent", "i bought"],
  "icon": "receipt",
  "for": "members",
  "roles": ["staff", "manager"],
  "answer": { "type": "text", "text": "='Logged ' + outputs.amount_words + ' for ' + outputs.what" }
}
```

| Field | Rule |
| --- | --- |
| `name` | What the tile and the form are called. 1 to 40 characters |
| `command` | The word after `/`. `^[a-z][a-z0-9-]{1,23}$`, one per business and environment, and never a word Taskiem's own commands use in any language that is on (§13) |
| `about` | One line, to 120 characters: under the tile, in `tools`, and what the reader is told the tool is for |
| `says` | Up to eight phrases that find the tool when a message starts with one. Optional |
| `icon` | One of a fixed set drawn by Taskiem. Optional |
| `for` | `members` (the business's own people), `customers` (anyone writing to the business's own number) or `linked` (businesses it is linked to). Phase by phase: §16 |
| `roles` | With `members`: who may use it, beside needing `run.start`. Left out: everyone with `run.start`. **It has to hold on every way a person can start the workflow** (the tool, `run <workflow>`, the web's Run, the API as a member), or it only hides a tile: §15, 8 |
| `answer` | What comes back (§7). Left out: today's "completed" message |

Rules:

- **Only a workflow that starts on demand.** `tool` is refused unless the trigger is `manual`, the rule subflow steps already have. A workflow that runs on a schedule or an event is not called; it tells people (§10). `run <workflow>` is unchanged and still offers every deployed workflow.
- **A tool with an answer declares `outputs`,** and so keeps the rules a callable workflow keeps ([the contract](../contracts/wd-v1.md#callable-workflows)): an inputs schema, no `types` of its own, step ids of at most 31 characters, no `respond` step, and the trigger read only as `trigger.body`. Two additions to an output: `public` (§11) and `x-pii` (§7).
- **Inputs are the workflow's `inputs`**, unchanged. What draws as a form today draws as this tool's form.
- **The deployed version is the tool.** A tool appears when a version with `tool` is deployed to `prod`, changes when the next is, and goes when a version without it is. A draft is never a tool. With Advanced on, the dev environment has its own tools, tried from the web only.
- **A button names a tool by its command in the definition and is held by the workflow's id once deployed,** so renaming a command does not break buttons already sent.
- **A recipe whose command is taken** is installed with the next free one (`expense-2`) and says so.
- **`tool` is not part of what the run does.** Like canvas layout it does not change `decide()`; unlike layout it is in the versioned document, because the command, the audience and the answer must be reviewed and published with the steps they describe.

## 6. Where a tool shows, and how it is called

| Place | How |
| --- | --- |
| The web and the phone app | **Your tools**, a row of tiles after the built-in ones in the tools panel, each with its name and `about`; `/` and its command or the start of its name; the two used last, as now |
| WhatsApp, Telegram, Slack | `/expense`, or the plain command where it clashes with nothing; `tools` and `help` list a person's tools with their commands; a phrase from `says`; or a sentence the reader takes to it |
| A button | On another tool's answer, or on a message a run sent (§10) |

**Calling it.**

1. **With nothing after it** (`/expense`, a tile, a button with no values): the form. A sheet on the web and the app, `taskiem_inputs` on WhatsApp, and one question at a time on Telegram and Slack and wherever a form cannot carry the inputs, as `run` does now.
2. **With words after it** (`/expense 4500 diesel`, or "spent 4,500 on diesel" by a `says` phrase or the reader): the reader fills what it can and the form opens **filled in**. A tool with one required input takes the rest of the message as that input, with no model.
3. **Then care** (§8): nothing more, **Yes**, or step-up.
4. **Then the run**, started as the person, in `prod`, with `tool: <command>` and the channel in the audit entry. The start carries a key taken from the conversation when the summary (or the echo) is shown, so a **yes** sent twice, or a call repeated after a lost reply, starts one run, as adding a row does now.
5. **Then the answer** (§7), in the conversation it was called from.

**Which business.** A person in several businesses on the shared number has the tools of the one the chat is working in (`switch`); answers carry that business's name in brackets, as every message there does.

**The `/` on each app.** On the web, the phone app and WhatsApp `/expense` is ordinary text and reaches Taskiem. On Telegram a `/command` typed to a bot reaches the bot, but only commands the bot registered are offered as you type, and those are the same for everyone. On Slack `/` commands belong to Slack and must be registered with the app, so a business's `/expense` typed there is refused by Slack before Taskiem sees it: on Slack a tool is called by its plain command or a `says` phrase. Both are from knowledge of those apps, not from a test, and are to be checked (§22).

**Order of reading a message.** A question Taskiem is waiting on, first, as now. Then an exact `/command`. Then Taskiem's own word lists. Then a made tool's `says` phrase at the start of the message. Then the model, which is given Taskiem's commands and the person's tools (name, `about`, input names) and may answer with one of them and proposed values, or with none. **Taskiem's own commands always win**, so a business cannot take over `invoice` or `yes`. Two tools that fit are asked about with a numbered pick, as two tables are.

**What the model may do is unchanged:** choose a command and propose values. It never starts a run, never answers **yes**, and its values are always shown to the person before anything runs (the filled-in form, or the echo line of §8).

## 7. The answer

A tool's answer is built from the workflow's `outputs`, which already exist with names and types and are already kept when a run completes. `answer.type` chooses the shape.

**The shapes are one named part of the schema**, `shapes/v1` inside `wd/v1`, referenced by `tool.answer` here and by every block of a page (§12). A shape is a `type` and its fields, each field a literal or an expression (`=`). Text, card and list are the three an answer may be; a page adds the rest (§12.3). The same renderer draws a shape wherever it meets one, so a `list` answer and a `list` block differ only in where their rows come from.

**Where the answer's expressions run.** Not in the run. The API works them out when it draws the answer, with the workflow expression engine (`engine/expr`) in a sandbox bound to two names only: `outputs` (what the completed run recorded) and, in a list, `item`, with a time and size budget. They cannot read steps, the trigger, secrets or the environment, so an answer can show nothing the workflow did not choose to give back, and `decide()` is not touched. The web and the phone app never evaluate an expression: one engine, one answer.

**`text`.** One message.

```json
"answer": { "type": "text", "text": "=outputs.name + ' has ' + string(outputs.days_left) + ' days of leave left'" }
```

**`card`.** The card frame the chat already draws for orders and invoices.

```json
"answer": { "type": "card",
  "title": "=outputs.ticket_title", "ref": "=outputs.ticket_ref",
  "state": { "text": "=outputs.status", "tone": "warn" },
  "lines": [ { "label": "Assigned to", "value": "=outputs.assignee" },
             { "label": "Opened", "value": "=outputs.opened" } ],
  "amount": "=outputs.amount_kobo",
  "buttons": [ { "label": "Close it", "tool": "close-ticket", "inputs": { "ref": "=outputs.ticket_ref" } } ] }
```

**`list`.** Rows from one output of type `list`, each with up to two buttons.

```json
"answer": { "type": "list", "from": "tickets", "title": "Open tickets",
  "item": { "title": "=item.title", "sub": "=item.customer", "state": { "text": "=item.age", "tone": "plain" } },
  "empty": "No open tickets",
  "actions": [ { "label": "Assign to me", "tool": "assign-ticket", "inputs": { "ref": "=item.ref" } } ] }
```

| Limit | Value | Why |
| --- | --- | --- |
| Text | 1,500 characters | As `notify.tell` |
| Card lines | 6 | The card frame |
| Buttons on a card | 3 | WhatsApp carries three reply buttons |
| Rows shown in chat | 10, then "and 14 more" with **See all** | WhatsApp's list message; **See all** opens the tool's sheet on the web and the app, where every row and its buttons are |
| Row buttons | 2 | A row on a phone |

The WhatsApp figures are from memory of Meta's limits and have to be checked against its current documentation before they are built in.

How each channel draws it:

| | Text | Card | List |
| --- | --- | --- | --- |
| Web, phone app | A message | The card frame, sliding in | A card with its first rows; the sheet for all |
| WhatsApp | A message | A message with reply buttons | A list message; choosing a row offers its buttons |
| Telegram, Slack | A message | A message with buttons | A message a row, or one message with a button a row, whichever the app allows |

Rules:

- **Masked as every message is.** Outputs pass through the same masking as today's messages (account numbers to four digits, phones, names, nothing named like a secret). An output declared personal with `x-pii` is masked whatever its name. A person who may see the full value opens the run. **Open:** an output built from a value sealed as personal data is sealed in the history; whether the answer opens it to mask it, or shows the mask without opening it, has to be designed with [privacy](../privacy.md) before T1.
- **A run that is waiting.** A tool's run that stops for an approval or a form tells its starter once ("Waiting for Ada to approve"), so silence never means lost.
- **A run that is queued.** A tool's run counts against the plan's run limits like any run (§15, 9). One held by `max_running_runs` says "Queued behind other work"; one refused by the day's or month's quota says so, with when it lifts.
- **Amounts** are in kobo in the workflow and drawn by Taskiem as naira (`naira()`), so no tool formats money for itself.
- **A run that fails** answers as now: what failed, in plain words, and a link.
- **A run that takes long.** If the run has not ended in ten seconds the person is told "Working on it. I'll tell you when it's done", and the answer comes when it does, through the run watch that exists.
- **Outside WhatsApp's 24-hour window** an answer cannot be free text or carry buttons: it goes as the run-outcome template with **Open**, as outcomes do now.
- **An answer is not a record.** It is drawn from the run's outputs when the run ends and kept as a message. It does not update itself afterwards, so a list can be stale by the time a row's button is pressed: the workflow behind the button must check the state itself.
- **An answer that comes when the app is closed.** When this was written, [chat push](chat-push.md) covered messages from linked businesses and left Taskiem's own conversation out (its decision 4). Push has since grown with personal accounts (decision 0068); whether an answer in the Taskiem conversation is announced is to be re-read against the current push decisions before T1, and if not, push for answers is its own small plan.
- **Not in this plan:** files and photos as inputs or as answers (a receipt, a report as a PDF), and dates picked from a calendar. A date is typed, and the reader may propose it from "Monday".

## 8. Care: what a tool asks before it runs

Today every start from chat needs an explicit **yes** after a summary. That is right for a write and tiresome for "how many days of leave do I have?".

**Care is worked out from the workflow, not declared by its author**, so a tool cannot claim to be harmless.

**What care does not cover: who may see the answer.** A workflow reads with the business's connections, not with the rights of the person who called it. A tool that reads the salaries table shows salaries to everyone who may use the tool, whether or not they may open that table. That is decided by who publishes the tool and by its `roles`, so publishing shows, in plain words, who will be able to use it and what it gives back, and a change to `for` or `roles` is called out for the approver where the business has four-eyes.

| Care | When | What the person sees |
| --- | --- | --- |
| **Answers at once** | Every step that reaches outside is a `read`: connector actions of class `read`, `http` steps that are `GET` or `HEAD`, code steps whose declared host functions are reads only (`host.fetch` allows only `GET` and `HEAD`, but a code step that may call a connector action or write a table is a write, and one whose host functions cannot be read from its declaration counts as a write), `ai` steps (within the AI budget). No step that writes a table, sends a message, asks a person (a form or approval step), runs a container, or starts another workflow | An echo of what was understood ("Leave left for: Musa"), with every value the reader filled in, then the answer |
| **Yes** | Anything else | The summary of exactly what will run, then **yes**, as now |
| **Step-up** | The business's policies ask for it for this workflow or a step in it | **Yes**, then the PIN or the hand-off link, as an approval does now |

- `wdcheck` works this out when a version is published and stores it with the tool. A step whose class is not known counts as a write. A template step is judged by the steps it expands into. A subflow step counts as a write: its child is a run of its own, and its version may change after this one is published.
- A `GET` that changes something at the far end (a badly made API) would pass as a read. The owner's switch below is the answer to that, and the publishing screen says a tool will answer without asking.
- An owner may make a tool **more** careful in its settings (always ask **yes**), never less.
- An approval step inside the workflow is separate and unchanged: care is about starting the run, approval is about a step in it.
- **The echo line matters.** With no **yes**, the person must still see what the reader understood, so a wrong reading is noticed.
- This changes one row of [WhatsApp's security model](../whatsapp.md#security-model) ("every start needs an explicit yes"). It is a decision (§15, 2). The reason to allow it is that such a tool changes nothing, not that it shows only what the person could already see: it may show more (above).

## 9. A question inside a run

The form step asks someone for answers mid-run by a one-time link, or in the app for a role. Added: **`to: "starter"`**, the person who started this run from a chat.

- They are asked **in that chat**: the same form as inputs (a sheet, a Flow, a Mini App form), or, for one field with up to three choices, buttons.
- The run waits, as it does for any form step, with the step's `timeout` and `on_timeout`.
- If the run was not started from a chat (a schedule, the web's Run button), `to: "starter"` falls back to the app's form requests for that member, and the validator says so.
- A person can have several runs asking at once. Each question names its tool, and an answer goes to the question it was a reply to, or the newest.

Decide (someone else says yes or no) needs nothing new: it is the approval step, with its buttons, PIN and rules.

## 10. A message with buttons

A workflow that starts by itself is not a tool, but it should be able to lead to one.

`notify.tell` gains `buttons`: up to three, each a `label`, a `tool` and optional `inputs`.

```json
{ "id": "warn", "type": "connector", "connector": "notify@1", "action": "tell",
  "input": { "to": "role", "role": "ops", "title": "Disk at 90% on db-2",
             "message": "=steps.check.output.summary",
             "buttons": [ { "label": "Clean up", "tool": "clean-disk", "inputs": { "host": "db-2" } } ] } }
```

- **A button fills a tool's form; it does not run the tool.** Pressing it is the same as typing the command with those values: the tool's care applies, as the person who pressed, with their roles. Someone who may not use the tool is told so.
- **Buttons are signed**, as approval buttons are: an HMAC under `TASKIEM_WHATSAPP_TOKEN_KEY` over the business, the tool, the values and an expiry, with a row for the nonce. They work for seven days, only for the business's members; a button on an answer to a customer works only from that customer's number.
- **Many presses, or one.** A message to a role reaches several people, and by default each may press: two people pressing **Assign to me** start two runs, and the workflow must cope. A button with `once: true` is spent by the first run it starts; the next person is told who took it and when. Two presses by one person within ten seconds are one.
- **Outside WhatsApp's 24-hour window** a message goes as the alert template, which carries one **Open** button to the web and none of these.
- **`notify.message`** (to people outside the business) does not get buttons in this plan. A customer reaches tools through the business's own number (§11).
- The same `buttons` shape is what cards and list rows use (§7).

## 11. Customers and linked businesses

**Customers** (`for: "customers"`). Only where public menus exist: a business's own WhatsApp number. Telegram has one bot for the platform and no menu per business. A customer picks from the menu by number, so no model reads a stranger's words. Today's public menu becomes tools for customers:

- The nine workflows a business marks public become tools with `for: "customers"`; **menu** lists them as now. Existing menus are carried over by a migration, each with the label it has as its `name`.
- Everything the public menu promises stays: inputs by form only, a summary and **yes** (customers' tools never answer at once without it, whatever their care), runs as `whatsapp_public:<number>` with no roles, the same rate limits, and nothing else reachable.
- **New: the answer.** A customer now gets the tool's answer, where today they get only a reference. This closes an item in WhatsApp's "Not yet".
- A customer's answer shows only outputs the workflow marks `public: true`. Anything else is left out, so a workflow written for staff does not leak when someone later opens it to customers.

**Linked businesses** (`for: "linked"`). A tool appears in the other business's side of the conversation ([business chat tools](business-chat-tools.md) §3), under "Their tools". The run is the offering business's, started by `link:<business>` with no roles, inputs by form, always **yes**, answer from `public` outputs only. This is the last phase, and it needs its own look at what a linked business may be shown.

## 12. Pages: a workflow's screens

### 12.1 Why

Made tools give a workflow a face in the conversation. Some work needs a screen. The dispatch work built a page listing the day's deliveries with **Confirm** on each, beside the card each delivery has in the chat, because an owner with twelve of them wants to see them together and act on several. That page is code in `web/` and in `mobile/`. Every workflow that keeps a list of things to work through (tickets, leave requests, quotes to price, stock to count, debts to chase) wants the same page, and Taskiem cannot hand-draw one for each.

A page is that screen as a declaration: which shapes, filled from what, placed where, with which actions. It is the second half of the same idea, and it reuses the first half's pieces: the shapes (§7), the buttons (§10), the care and roles (§8, §5), the reader of definitions (§18).

### 12.2 The declaration

An optional top-level `views` in `wd/v1`, a list of pages.

```json
"views": [{
  "id": "deliveries-today",
  "name": "Today's deliveries",
  "about": "Every delivery of the day and what each one is waiting for",
  "place": { "area": "orders", "tile": true },
  "roles": ["owner", "manager"],
  "data": {
    "today": { "table": "deliveries", "where": [["day", "=", "=today()"]], "order": "created_at", "limit": 50 },
    "owed":  { "table": "deliveries", "where": [["status", "=", "delivered"], ["fee_paid", "=", false]], "sum": "fee_kobo" }
  },
  "blocks": [
    { "type": "hero", "label": "Owed to riders", "amount": "=data.owed" },
    { "type": "list", "from": "today",
      "item": { "title": "=item.customer", "sub": "=item.rider + ' · ' + item.area", "state": { "text": "=item.status" } },
      "empty": "Nothing to deliver today",
      "actions": [ { "label": "Confirm", "signal": { "run": "=item.run_id", "name": "confirm" }, "when": "=item.status == 'delivered'" } ] }
  ]
}]
```

| Field | Rule |
| --- | --- |
| `id` | `^[a-z][a-z0-9-]{1,31}$`, one per workflow; the page's address is `/pages/<workflow>/<id>` |
| `name`, `about` | As a tool's: 1 to 40 characters, and one line to 120 |
| `place` | Where it lives (§12.5). Left out: under **Your pages** only |
| `roles` | Who may open it, beside being a member who may see the page's area. The same rule and the same enforcement as a tool's `roles` (§15, 8). Left out: every member who may see the area |
| `data` | Named reads (§12.4), at most eight |
| `blocks` | The shapes, in order, at most twelve |

Rules, which mirror a tool's:

- **The deployed version is the page.** A page appears when a version with `views` is deployed to `prod`, changes with the next, and goes when a version without it is. A draft is never a page; with Advanced on, dev has its own, on the web only.
- **A page belongs to a workflow but does not need the workflow to be a tool.** A scheduled workflow that fills a table can have a page over that table. A page's actions, where it has them, are tools, signals or approvals (§12.7); a page with no actions is a report.
- **`views` is not part of what the run does.** Like `tool`, it never reaches `decide()`. It is in the versioned document because what a page shows, to whom, must be reviewed and published with the steps that make the data.

### 12.3 The shapes a page may use

Each is drawn by the kit the screens guide names (`web/src/ui.tsx`, the phone app's `kit/`), so a page cannot draw anything the brand does not already draw.

| Shape | Fields | Drawn as |
| --- | --- | --- |
| `hero` | `label`, `amount` (kobo) or `value`, optional `sub` | The pine hero card: a page about one thing with money leads with it |
| `list` | `from`, `item` {`title`, `sub`, `state`, `tags`}, `empty`, `actions` (up to two a row), `open` (a page or a record to open on tap) | Rows (`Items`/`Item`), as the chat tools draw them |
| `record` | `from` (one row or `outputs`), `lines` [{`label`, `value`}], up to twelve | Term and value pairs, as a card's lines |
| `table` | `from`, `columns` [{`label`, `value`, `align`}], up to six columns | A table, which the guide allows only for figures and line items |
| `text` | `text`, up to 1,500 characters | A paragraph |
| `photo` | `file` (a media id from a row or an output), `alt` | One photo |
| `status` | `run` | A run's state in plain words: queued, working, waiting for whom, done, failed |
| `form` | `tool` | That tool's input form, inline, with its care |
| `actions` | Up to three buttons (§12.7) | A row of buttons |
| `card` | As a tool's card answer (§7) | The card frame |

`text`, `card` and `list` are the same shapes as a tool's answers, so the `shapes/v1` schema has exactly one definition of each.

### 12.4 Data: what a page may read

A page reads with the business's connections, as a workflow does, and shows what it reads to everyone who may open it. Who that is, is decided by `place`, `roles` and the area's switch, and the publishing screen says so in plain words, as it does for a tool (§8).

| Source | How | Bounds |
| --- | --- | --- |
| **A data table** | `table`, `where`, `order`, `limit`, or one `count` or `sum` | The checked query spec of decision 0060, point 7: names and types checked against the business's tables at publish and at read, operators from a fixed list, a limit of 50 rows a read (200 with **Show more**), one aggregate a read |
| **Known records** | `records: "invoices" \| "orders" \| "deliveries" \| "customers" \| "staff"`, with the same `where` | Read-only views Taskiem defines for the known tables and the records of its own areas, each listing the columns a page may see. An area that is off is refused |
| **The run** | `outputs` of the latest completed run of this workflow, or of a run named by the page's address (`/pages/<workflow>/<id>?run=<id>`) | Masked as every message is (§7) |
| **A tool's answer** | `answer: "<command>"`: the list answer of a tool as a page's rows, so **See all** on a long answer is a page | The answer's own limits |

Expressions in a page run where a tool's answer's do (§7): in the API, over `data.<name>`, `item`, `outputs` and the date functions, never over steps, the trigger, secrets or the environment, with a time and size budget. The web and the phone app never evaluate anything; they draw what the API gives them.

A page is read when it is opened and when the person pulls to refresh. A page whose data names a table or a run is told when they change through the same change notices the chat tools use (`api/chat_changes.go`), so a **Confirm** pressed on the web shows on the phone without a reload. There is no streaming from inside a page.

### 12.5 Where a page lives, and who sees it

| `place` | Where it appears |
| --- | --- |
| left out | **Your pages**, a list on the web (under More) and in the phone app's menu, and a `/pages` word in the chat that lists them with links |
| `{ "area": "orders" }` | A tab under that area's existing page, after Taskiem's own tabs; the same for `storefront`, `stock`, `money`, `links`, `schools`. It appears only while the area is on |
| `{ "tile": true }` | A tile in the tools panel after **Your tools**, opening the page in the sheet frame |
| `{ "card": "<kind>" }` | The page is also a card kind in the conversation: a record of that kind (a run of this workflow, a row of its table) is drawn with the page's `record` block and `actions`, so a made record gets a live card as orders and invoices have |
| `{ "alert": true }` | A page with a `count` in its data becomes an alert kind: when the count is above zero the alerts list shows the page's name and the count, and opens it. The alerts list already has `confirm` and `order_draft` kinds built this way by hand |

A page is never put in the sidebar as if it were Taskiem's own area (§15, 11). The line between what Taskiem guarantees and what a workflow promises stays visible.

Who sees a page: a member who may see its area (the area switch, the role's areas), with the page's `roles` on top, and for a page placed as a tile the chat tools switch. The person's own side (**You**, decision 0068) sees the pages of workflows made with `for: "me"` (§15, 10). Customers and linked businesses see no pages in this plan.

### 12.6 How each surface draws a page

| | Web | Phone app | WhatsApp, Telegram, Slack |
| --- | --- | --- | --- |
| The page | The kit's blocks in order, in the area's tab, the sheet frame or at its address; the hero first where there is one | The same blocks from the same data, in `mobile/`, with its picture in the parity test | A link to the page on the web; for a page with one `list`, that list as a list message (ten rows, **See all** as a link), and a row's first two actions as buttons |
| An action | A button; a tool's action opens its form with its care, a signal asks **Yes** unless the page's workflow is read-only, an approval is the approval sheet | The same | The same words as a tool's buttons (§10) |
| Refresh | The change notices, and pull to refresh | Pull to refresh (the gesture the app has) | A new link each time |

Phone first, as the screens guide requires: every block fits 390 px with nothing scrolling sideways, so a `table` with more than three columns is drawn as rows on a phone.

### 12.7 Actions on a page

An action is one of three things, and nothing else:

| Action | Field | What happens |
| --- | --- | --- |
| **Call a tool** | `tool`, `inputs` | The tool's form opens filled in, with its care (§8), as a button on an answer does (§10). A page may call tools of other workflows |
| **Signal a run** | `signal: { run, name, input }` | The named signal is sent to that run, as the run's own buttons send them today (the deliveries' **Confirm** is one). The page's workflow must be one the run belongs to, or declare the signal in `signals`, so a page cannot poke another workflow's runs |
| **Approve** | `approve: { run, step }` | The approval sheet, with its PIN and rules, unchanged |

`when` on an action hides it when false. Actions are signed and limited as buttons are (§10): a press acts as the presser, with their roles; two presses by one person within ten seconds are one; a signal to a run that has moved on says so.

### 12.8 Limits

| Limit | Value | Why |
| --- | --- | --- |
| Pages a business may have | 20 deployed (`made_pages`, a plan limit beside `made_tools`) | The same reasoning |
| Blocks a page | 12 | A phone screen |
| Reads a page | 8, each within the query spec's bounds | One page, one screen of reads |
| Rows a list | 50, then **Show more** to 200 | The query spec |
| Actions | 3 on a page, 2 a row | As buttons |
| Refresh | At most once a second a page a person | The change notices already coalesce |

### 12.9 What the validator refuses

Added to §13's table when a version with `views` is validated and deployed:

| Refused | Because |
| --- | --- |
| A `data` read naming a table or column the business does not have, an operator not in the list, or more than one aggregate | The query spec |
| A `records` source for an area the business has off | It would show nothing, and say nothing |
| A block naming a `data` entry the page does not read, or `item` outside a `list` | It could not be drawn |
| A `signal` to a workflow the page does not belong to and that does not declare it | §12.7 |
| A `card` place on a workflow with no record to be a card of | Nothing to draw |
| More than twelve blocks, eight reads, three actions | §12.8 |

A column dropped from a table after a page was published makes the page say "This page needs a change" to the owner and shows the blocks that still draw; it never fails to open.

### 12.10 Making a page

| Way | Who | How |
| --- | --- | --- |
| **Show it as a page** | Everyone | A switch on a workflow in the builder, beside **Use it from chat**: its name, where it goes (a list of the areas that are on, the tools panel, or Your pages), and **What should it show?** built by picking: a figure from a table (which table, which column, counted or summed), a list from a table with its columns, the record's lines. Never a typed expression |
| **Describe it** | Everyone | The AI builder: "show me today's deliveries with a Confirm button on each" drafts the `views` block with the workflow, read back in plain words |
| **A recipe** | Everyone | A template that carries `views`. `tool-list-and-act` and `tool-daily-report` (§14) each gain a page |
| **The definition** | Advanced | `views` in the JSON, `flow.view({...})` in the SDK, Git |

Publishing a page is publishing its workflow, with the same checks and call-outs. The approver reads, in plain words, what the page shows and to whom (§18).

### 12.11 What pages are not

- **Not a page builder.** No layout, no columns to drag, no colours, no fonts. The blocks stack in order and the kit draws them. A business chooses what to show, not how it looks.
- **Not the built-in pages rewritten.** Orders, Money, Stock and the dispatch pages stay as built; they hold rules a declaration must not be able to break. The test of this plan is that a workflow *could* declare the deliveries page, not that it does.
- **Not a dashboard product.** Charts are not in this plan; a `hero` and a `table` of figures are. If owners ask for a chart, it is a shape added to `shapes/v1`, drawn by the kit in both themes, never a library in a page.
- **Not a window onto other systems.** A page reads tables, known records and outputs. A workflow that calls a web address puts what it needs in a table or an output first, so every page is masked, bounded and offline-cached the same way.

## 13. What the validator refuses

When a version with `tool` is validated (`wdcheck`), and again when it is deployed (a version with `views` adds the rows of §12.9):

| Refused | Because |
| --- | --- |
| A trigger other than `manual` | A tool is called by a person |
| A `command` another deployed workflow of the business has, or a word of Taskiem's own commands in any language | One meaning for a word. The reserved words are read from the same lists the reader uses (`engine/lang`), so a new built-in command reserves its word |
| A `says` phrase that starts with a reserved word | The same |
| An `answer` naming an output the workflow does not declare, or a `list` whose `from` is not an output of type `list` | It could not be drawn |
| More than three buttons, six lines, two row actions | §7 |
| A button whose `inputs` name a field its tool does not have | Checked at deploy against the tool then deployed; a button to a tool that does not exist is a warning, and the button says "This is not available" if pressed |
| `for: "customers"` with inputs a form cannot carry (nested, more than ten) | Customers are never asked in chat |
| `for: "customers"` or `"linked"` with an `answer` that reads an output not marked `public` | §11 |

**A word reserved later.** A language turned on after a tool was deployed, or a new command of Taskiem's, can take a word a tool already uses. Taskiem's command wins from then on; the tool stays reachable by its tile, its `says` and `run`, and the owner is told to choose another command.

A plan limit, `made_tools` (deployed tools a business may have), joins the others in [operations](../operations.md#plan-limits). Twenty is a starting figure; the reader is given at most forty tools, the nearest by words when there are more.

## 14. Making a tool

| Way | Who | How |
| --- | --- | --- |
| **Use it from chat** | Everyone | A switch on a workflow that is started by hand, in the builder: its name, its command (suggested from the name), what it is for, who may use it, and **What should it say back?** with three choices (a sentence, a card, a list) filled from the workflow's outputs by picking, never by typing expressions |
| **Describe it** | Everyone | The AI builder, on the web or in chat: "when I type fuel and an amount, log it and tell me the month's total". It drafts the workflow, its inputs, its outputs and the `tool` block together, read back in plain steps. It saves a draft, as it always does |
| **A recipe** | Everyone | A template that carries `tool`. Installing it gives the workflow and the tool |
| **The definition** | Advanced | `tool` in the JSON, the TypeScript SDK (`flow.tool({...})`), Git |

Publishing a tool is publishing its workflow: the same checks, four-eyes where the business has it, and the chat-publishing rule of [no technical steps](no-technical-steps.md) (N0-7).

**Recipes to ship with it**, each generic, none tied to a business part:

| Recipe | Kind | What it does |
| --- | --- | --- |
| `tool-look-up` | Look up | Finds a row of a table by a word and shows it as a card |
| `tool-add-a-record` | Record | Adds a row to one table, with a sentence back. `add to <table>` with a name of its own, fixed to a table |
| `tool-list-and-act` | Pick | Rows of a table that match a state, each with a button that changes the state |
| `tool-ask-for-approval` | Do, Decide | A request with a reason; the approver decides with buttons; the asker is told |
| `tool-tell-the-team` | Do | A message to everyone with a role |
| `tool-call-a-system` | Look up or Do (Advanced) | Calls a web address and shows chosen fields of what comes back |
| `tool-daily-report` | Report | A schedule, a count or a sum over a table, and a message with a button to the list |
| `tool-watch-and-act` | Watch | A schedule or a webhook, a condition, and a message with a button to a Do tool |

## 15. For the founder to decide

| # | Question | Recommendation |
| --- | --- | --- |
| 1 | Is `tool` a block of the workflow, or a trigger type of its own (`chat`)? | **A block.** The inputs, the outputs and the run are the workflow's already, and one workflow then has one way to be started by a person wherever they are. A trigger type would give tools their own inputs and split `run` from tools |
| 2 | May a read-only tool answer without **yes**? | **Yes**, with the echo line, for members only, because it changes nothing. It may show a person more than they could open themselves (§8); that is settled by who may use the tool, not by asking **yes**. Customers and linked businesses always get **yes**. The echo line carries every value the reader filled in, not a summary, and the owner's **always ask** switch ships with T1 |
| 3 | Do made tools need the chat tools switch on? | **The panel does; the commands do not.** `/expense` works wherever `run` works today, so a business on WhatsApp alone has its tools |
| 4 | Does the public menu become tools for customers, or stay beside them? | **Become**, by migration, so there is one way to offer a workflow to customers and they get answers |
| 5 | Tools for linked businesses: in this plan, or later? | **Later**, as its own plan after members and customers are in use |
| 6 | Should made tools wait for [pages](#17-what-this-is-not) so both share one catalogue? | **No.** Build tools first with the three answer shapes; write pages so its list and action blocks are these same shapes |
| 7 | Decision number | **0070.** 0067, 0068 and 0069 merged between 12 and 17 November; 0063 and 0064 are absent from the log |
| 8 | A tool's `roles`: a rule for who may start the workflow on every path, or only who sees the tile? | **A rule on every path**, enforced where a person starts a run. Nothing limits who may start one workflow today, so this is new, and without it `run <workflow>` walks round a tool's roles. It is also useful with no tool at all, so it is **built first, as its own small change before T0**, and T1's diff stays about tools |
| 9 | Does a tool's run count against the day's and month's run limits? | **Yes**, as any run. A look-up is cheap but not free (it is a run, with its history and its retention). Watch the first businesses' counts before changing the limits; if look-ups dominate, a cheaper retention class for them is the fix, not an exemption |
| 10 | Tools and pages for a person's own side (**You**, decision 0068)? | **Yes, `for: "me"`**, in T1: a personal session has its own tools and pages, with no business. "Start something" on You (that plan's §18) is this. It costs little designed in now and much to retrofit |
| 11 | May an owner put a made page in the sidebar as if it were a Taskiem area? | **No, not in this plan.** Pages live under **Your pages**, under an area as a tab, in the tools panel, or as a card or alert kind. The line between what Taskiem guarantees and what a workflow promises stays visible. A setting to promote a page is a later question |
| 12 | One spec and one decision for tools and pages, or two? | **One** (this document, decision 0070). The shapes are shared, and two documents would hold two copies of them. The build still goes tools first (§16) |
| 13 | Do pages ship on the web and the phone app together, as the app's rule is? | **Together**, from P1. A page drawn on one and not the other is the thing the parity test exists to stop |
| 14 | Are the hand-drawn deliveries page and the Confirm alert rebuilt as a declared page when pages ship? | **Not rebuilt, but declared as the acceptance test**: a fixture workflow declares a page equal to the deliveries page, and the two are compared in the browser tests. Rewriting what works is not the point; proving the vocabulary is |

## 16. Phases

| Phase | What | Done when |
| --- | --- | --- |
| T0 | The decision, `tool` and `answer` in `schemas/wd-v1.schema.json` and the contract, the validator's rules (§13), care worked out and stored | A definition with `tool` validates or is refused for the right reason; nothing uses it yet |
| T1 | Members' tools everywhere: the index of deployed tools, **Your tools** in the panel and `/`, the commands on WhatsApp, Telegram and Slack, the form, care, the `text` answer, **Use it from chat** in the builder, the SDK | A person makes "Log an expense" without Advanced, and uses it from the web, the phone app and WhatsApp |
| T2 | `card` and `list` answers, buttons on answers and on `notify.tell`, the sheet for a list | "Open tickets" lists, and **Assign to me** on a row works from a phone |
| T3 | The reader: tools given to the model, proposed values, the numbered pick, evaluation cases in English and Pidgin | "spent 4,500 on diesel" opens the expense tool filled in; a built-in command is never taken by a tool |
| T4 | `to: "starter"` on the form step | A run asks "Which branch?" in the chat it was started from and goes on with the answer |
| T5 | Customers: the public menu carried over, answers for customers, `public` outputs | A customer picks from **menu**, fills the form, says **yes** and gets the answer |
| T6 | The eight recipes, and the AI builder drafting `tool` | "Describe it" produces a working tool from one sentence |
| T7 | Linked businesses | Its own plan |
| P0 | `views` and the full `shapes/v1` in the schema and the contract, the validator's rows (§12.9), the query spec read from a page's `data`, the records views | A definition with `views` validates or is refused for the right reason; nothing draws it yet |
| P1 | **Your pages** on the web and the phone app together: the renderer for every shape from the kit, reads with the change notices, `hero`, `list`, `record`, `table`, `text`, `status`; **Show it as a page** in the builder; the SDK | The deliveries fixture page draws on both and matches the hand-built page in the browser tests |
| P2 | Placement under an area and in the tools panel; actions (tool, signal, approve); the chat's `/pages` word and the list-message form | **Confirm** on the fixture page moves a delivery from a phone, and shows on the web without a reload |
| P3 | Card and alert kinds from a page; `form` and `photo` blocks; the two recipes' pages; the AI builder drafting `views` | A made record has a live card, and "show me today's deliveries with Confirm" drafts a working page |

T1 is the first thing worth showing. T2 and T3 can go side by side after it. P0 starts once T2 has built the shapes' renderer for answers, so pages begin from a renderer that exists; P1 and T3 can go side by side. The roles rule (§15, 8) comes before T0.

## 17. What this is not

- **Not a page builder, and not a dashboard product.** Pages (§12) are declared stacks of Taskiem's shapes, never layouts, styles or charts; §12.11 says what else they are not.
- **Not the built-in tools rewritten.** Products, Stock, Orders, Money and the rest stay as built. They hold rules a wiring of steps must not be able to break.
- **Not code in the chat, or on a page.** A tool or a page cannot bring its own drawing, script or frame. The shapes are Taskiem's, which is what lets the web, the phone app and WhatsApp all draw them and keeps the phone app's picture test meaningful.
- **Not a bot.** There is no free conversation with a tool: it is called, it may ask through a form step, it answers.
- **Not a new way to run.** No new step type, and nothing new in `decide()`: outputs are already worked out and kept when a run completes. One addition to an existing step (`to: "starter"` on the form step) and one to a connector (`buttons` on `notify.tell`). A page's signal and approval actions send what the run's own buttons send today.
- **Not in people's groups.** A tool or a page is a business's (or, with `for: "me"`, a person's own). Whether one can be called inside a group of people (decision 0069) is a later question, and nothing here assumes it.
- **Not for embedded builders.** A partner's sub-tenants do not get made tools in this plan; what an embedded app's end user may call needs its own look ([embedding](../embedding.md)).

## 18. What else has to learn about `tool`

A new block in the definition touches everything that reads one. None of it is hard, and each is easy to forget.

| Reader of a definition | What it must do |
| --- | --- |
| `engine/wd`, `engine/wdcheck`, `schemas/wd-v1.schema.json`, the contract page | Parse and validate `tool`, `views` and `shapes/v1` (§13, §12.9) |
| `engine/wddiff`, `engine/wdtext` | Say a change in plain words ("Customers can now use it", "It now answers without asking", "The page now shows the fee column to managers"), since these are what an approver reads |
| `engine/wdmerge` | Two people changing `tool` at once is a conflict for a person; it is not merged field by field |
| The builder and the canvas | **Use it from chat** and **Show it as a page** (§14, §12.10); the canvas shows that a workflow is a tool, a page, or both |
| The TypeScript SDK, its codegen, `engine/flowcode` | `flow.tool({...})` and `flow.view({...})`, round-tripping to the definition |
| `wd-test/v1` | A workflow test can state the answer it expects for given inputs, and the rows a page draws for given table contents, so a tool or a page is tested before it is published |
| Git-led deploys | The same validation and the same call-outs as publishing on the web |
| The template library and its tests | Recipes with `tool`; a command taken at install |
| The AI builder, its schema, its property rules and `evals/builder` | Drafts `tool`; a rule that a drafted tool for customers has only `public` outputs in its answer |
| Business parts | A tool whose steps belong to a part that is off is not offered, and says why to the owner, as templates follow the switches now; a page placed under an area that is off is not shown |
| The chat tools, the alerts list, the card frame (`web/src/lib/tools.ts`, `alerts.ts`, `cardframe.ts`, and the same in `mobile/`) | Take their lists of kinds from data as well as code, so a page's tile, alert and card kinds join the built-in ones |
| Docs | A page for owners in `docs/guide`, the contract, `docs/whatsapp.md` and `docs/telegram.md`, the atlas pages on definitions and channels |

## 19. Storage and API

**Storage.** One table, `workflow_tools`, a row for each deployed tool: tenant, workflow, environment, version, command, name, `about`, `says`, icon, audience, roles, care, the answer's type. Written in the transaction that deploys a version and removed by one that deploys a version without `tool`, so it never disagrees with what is deployed. Unique on tenant, environment and command. Under forced row-level security like every tenant table. Button nonces reuse the shape of `whatsapp_tokens`.

A second table, `workflow_views`, holds a row for each deployed page in the same way: tenant, workflow, environment, version, id, name, `about`, place, roles, and the checked `data` reads. Written and removed in the deploying transaction, unique on tenant, environment, workflow and id, under forced row-level security.

Runs gain nothing new: a run started by a tool is recorded with `run.start`, the channel, and `tool: <command>`; a signal sent from a page is recorded as the run's own buttons record theirs, with `page: <workflow>/<id>`.

**API** (each in `api/openapi/openapi.yaml` in the same change, and in `mobile/api.txt` for the app):

| Call | What |
| --- | --- |
| `GET /v1/tools` | The caller's tools in the business: command, name, `about`, icon, the input schema, care |
| `POST /v1/tools/{command}/read` | Words in, proposed values out (the reader). Nothing is started |
| `POST /v1/tools/{command}/start` | Values in. Answers the run, or the summary and a token to confirm with, or that step-up is needed |
| `POST /v1/tools/{command}/confirm` | The token from start |
| `GET /v1/runs/{id}/answer` | The answer as data (its type, text, lines, rows, buttons), masked, for the web and the app to draw |
| `POST /v1/tools/buttons/{token}` | A button press: the tool and its values, for the form |
| `GET /v1/pages` | The caller's pages: id, workflow, name, `about`, place, and the areas and kinds they join |
| `GET /v1/pages/{workflow}/{id}` | The page as data: its blocks with every expression already worked out, masked, for the web and the app to draw; `?run=` for a run's page; `?more=` for the next rows of a list |
| `POST /v1/pages/{workflow}/{id}/act` | An action: a tool (answers as `/v1/tools/{command}/start` does), a signal, or an approval, as the presser |

The chat apps reach the same functions through the conversation engine, not these routes.

## 20. Security and abuse

| Concern | Control |
| --- | --- |
| A tool that takes over a command of Taskiem's | Reserved words refused at validation and at deploy; Taskiem's commands are read first (§6, §13) |
| A tool that says it is harmless | Care is worked out from the steps, and an unknown step counts as a write (§8) |
| The model starting something | It proposes; the person sees the values and, for a write, says **yes**. Unchanged |
| A wrong reading on a tool that answers at once | The echo line; and such a tool only reads |
| Words meant to steer the model, in a tool's `about` or `says` | They are a business's own text, read only for that business's own members; they are length-limited and passed as data, and the model can still only return one of the listed commands |
| Someone using a tool they should not | `run.start`, the tool's `roles` held on every way to start the workflow (§15, 8), and the person's roles at that moment; refusals audited |
| A tool that shows more than its caller may open | Not prevented, by design: it reads with the business's connections. Publishing says who will be able to use it and what it gives back; a change of audience or roles is called out to the approver (§8) |
| Two people pressing one button | `once: true`, or a workflow that checks the state itself (§10) |
| A forwarded or forged button | Signed, with a nonce row; a press acts as the presser, never as whoever the message was sent to (§10) |
| Personal data in answers | The message masking, `x-pii` outputs, and for customers and linked businesses only `public` outputs (§7, §11) |
| A flood of calls | The per-number limits that exist for run starts (three, then one every 20 seconds) for tools that write; a looser one for tools that only read, set when T1 is measured; the plan's run limits apply to every run |
| Cost of the model | Within the business's AI budget, as the reader is now; `/command` and `says` use no model |
| One business's tools seen by another | `workflow_tools` and `workflow_views` are tenant data under row-level security, read with the conversation's business in scope |
| A page that reads what its viewer may not open | Not prevented, by design, as for a tool: a page reads with the business's connections. Publishing says who will see it and which tables and columns it shows; a change to `place`, `roles` or `data` is called out to the approver |
| A page that pokes another workflow's runs | A signal action is refused unless the run is the page's workflow's or that workflow declares the signal (§12.7) |
| A page used to read a table fast and wide | The query spec's bounds (50 rows, one aggregate, eight reads) and one refresh a second; a page is never a bulk export |
| A column removed, a table renamed | The page says it needs a change and draws what it can; nothing errors in front of staff (§12.9) |

## 21. Tests

| Test | What it proves |
| --- | --- |
| Schema and validator | Each row of §13 is refused with a plain sentence; a correct tool validates |
| Care | A workflow of reads answers at once; adding a table write, a message, a non-GET `http` step, or a subflow that writes makes it **Yes**; a policy makes it step-up; an owner can raise it and not lower it |
| Deploy keeps the index true | Deploy, deploy a change, deploy without `tool`, roll back: `workflow_tools` matches what is deployed each time |
| Reading order | A pending question, `/command`, Taskiem's words, `says`, the model, in that order; `invoice` and `yes` cannot be taken |
| The same tool on every channel | One fixture workflow called from the web, WhatsApp, Telegram and Slack gives the same run and the same answer's content |
| Answers | Text, card and list at their limits; masking; an empty list; a failed run; a slow run; outside the 24-hour window |
| Buttons | A press fills the form as the presser; another business's member, an expired token and an altered token are refused; a button to a missing tool says so; `once: true` is spent by the first run; a command renamed after the button was sent still works |
| Roles on every path | A member outside a tool's `roles` is refused by the tool, by `run <workflow>`, by the web's Run and by the API |
| Starting once | A **yes** sent twice, and a call repeated after a lost reply, start one run |
| Limits and waits | A queued run and a run refused by quota say so; a run waiting on an approval tells its starter once |
| Everything in §18 | Each reader of a definition handles `tool`: a diff in plain words, a merge conflict, the SDK round trip, a workflow test of an answer |
| Ask back | A question in the chat, answered, timed out, and two runs asking at once |
| Customers | The migration carries menus over; a customer gets the answer; an output not marked `public` never reaches them |
| Web | **Your tools** in the panel, the form sheet, each answer shape; added to `e2e/accessibility.spec.ts` and `src/lib/plainwords.test.ts` |
| Phone app | The same screens in `mobile/`, with their pictures in the parity test, in the same change as the web's |
| The reader | Cases in `evals/chat`: sentences that should reach a made tool with the right values, and sentences that must still reach Taskiem's own commands, in English and Pidgin |
| Pages: the schema and validator | Each row of §12.9 refused with a plain sentence; the deliveries fixture validates |
| Pages: the deliveries fixture | A workflow declaring the deliveries page draws, on the web and the app, what the hand-built page draws for the same data, including **Confirm**; compared in the browser tests and the parity test |
| Pages: data | A read within the spec; one past its bounds refused; a dropped column makes the page say so and still open; a change notice refreshes an open page on both surfaces |
| Pages: who sees it | An area off hides its tab; a member outside `roles` is refused at the address too; a personal session sees only `for: "me"` pages; a customer sees none |
| Pages: actions | A tool action opens the form with its care; a signal to a run that moved on says so; a signal to another workflow's run is refused; an approval opens the approval sheet |
| Pages: the chat | `/pages` lists them; a one-list page goes as a list message with its two row buttons; **See all** links to the page |
| Pages: plain words and axe | Every shape in both themes, added to `e2e/accessibility.spec.ts` and `src/lib/plainwords.test.ts`, and the app's large-text and touch-target checks |

## 22. Needs a person

| Item | Who |
| --- | --- |
| The decisions of §15 | The founder |
| Meta's current limits for reply buttons and list messages, and whether list messages need review for the business's account | Whoever holds the WhatsApp Business Account |
| How Telegram and Slack treat a typed `/command` the app did not register (§6) | Whoever holds the bot and the Slack app |
| The record views for pages: which columns of invoices, orders, deliveries, customers and staff a page may read (§12.4) | Engineering with whoever owns privacy |
| The words **Your pages** and **Show it as a page**, in the five languages | Product, native speakers |
| How an answer shows an output built from a sealed value (§7) | Engineering with whoever owns privacy |
| Words for owners ("Your tools", "Use it from chat", "What should it say back?") checked against the plain-words rules, and in the four other languages | Product, native speakers |
| The fixed set of icons | Design |
| Real requests for the evaluation cases, once design partners use it | Partner success |

## 23. What a review of this plan changed

The plan was read against the code on the day it was written. What that found:

| Was | Is |
| --- | --- |
| "Mini App form on Telegram" for inputs | Telegram has no forms; inputs are asked one by one (§3, §6) |
| A read-only tool "shows data the person may already see" | Wrong: a workflow reads with the business's connections. Who may see an answer is now its own point (§8, §15, §20) |
| `roles` on a tool, with nothing behind it | Nothing limits who may start one workflow today, so `run <workflow>` walked round it. Now a rule on every path, and a decision (§5, §15, 8) |
| `/command` on every app | Slack keeps `/` for itself, and Telegram offers only registered commands (§6), both to be checked |
| Nothing on where an answer's expressions run | The API, over `outputs` and `item` only (§7) |
| Nothing on a run started twice, or a button pressed by two people | A key from the conversation; `once: true` (§6, §10) |
| Nothing on a run that waits, is queued, or hits a quota | §7, and a decision on counting (§15, 9) |
| Nothing on a word reserved after a tool took it, a renamed command, a recipe whose command is taken | §5, §13 |
| Nothing on sealed personal data in outputs, push for late answers, files, dates | Named as open or as not in this plan (§7) |
| Nothing on the other readers of a definition | §18 |
| Unsure whether outputs are kept for an ordinary run | They are, for any run that completes with outputs (§3), so nothing in `decide()` changes |

## 24. What the 19 November revision changed

The plan was read again on 19 November, against the code after decisions 0067 to 0069 merged, and pages were brought into it from a separate discussion with the founder (12 November, on how a business adds UI to a workflow without Taskiem building an area for each).

| Was | Is |
| --- | --- |
| "Not pages": a separate plan to be written | Pages are §12 of this plan, sharing the shapes (§7), the buttons (§10), roles and care, with their own phases (P0 to P3) after T2 and a decision on one document (§15, 12) |
| Three answer shapes under `tool.answer` | One named `shapes/v1` inside `wd/v1`, referenced by `tool.answer` and by every block of a page, so there is one definition of each shape |
| Whether the `whatsapp` trigger type is used: open | Checked: no code reads it. This plan ignores it (§3) |
| Decision number: the next free one | 0070 (§15, 7) |
| Audiences `members`, `customers`, `linked` | Plus `for: "me"` for a person's own side, now that personal accounts exist (§15, 10) |
| Code steps count as reads | A code step is a read only when its declared host functions are reads; unknown counts as a write (§8) |
| The echo line | Carries every value the reader filled in; the owner's **always ask** switch ships in T1 (§8, §15, 2) |
| Roles on every path: a decision inside T0 | Built first, as its own small change (§15, 8) |
| Where answer expressions run: "the API" | The workflow expression engine, sandboxed to `outputs` and `item`, with a budget; the apps never evaluate (§7) |
| Push for answers: not covered | To be re-read against the push decisions since 0068 (§7) |
| Groups | Named as not in this plan (§17) |
| Phases T0 to T7 | Plus P0 to P3, with P0 starting after T2 (§16) |
