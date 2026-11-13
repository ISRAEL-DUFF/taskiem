# Made tools: a workflow as a tool in the chat

Status: **plan; not built, and nothing here is decided.** Written 2026-11-13, after a conversation with the founder on 12 November about using Taskiem as a general automation tool and about workflows that people work with from the chat. §14 lists what the founder has to decide, each with a recommendation. Builds on [chat tools](chat-tools.md), [business chat tools](business-chat-tools.md), [WhatsApp](../whatsapp.md), [forms](../forms.md), [callable workflows](../contracts/wd-v1.md#callable-workflows) (decision 0048) and [no technical steps](no-technical-steps.md).

## 1. Summary

The tools in the chat today (Products, Stock, Orders, Tables, Money and the rest) are a list in code. A business cannot add one, and a new kind of work cannot have one until Taskiem builds it.

This plan lets **any workflow a person starts by hand become a tool**. The workflow says three things about itself: what it is called, what it needs, and what it answers with. From that one declaration Taskiem gives it a tile in the tools panel, a `/` command, plain words that find it, a form for its inputs, and an answer drawn in the conversation as text, a card, or a list with buttons.

Nothing about how a workflow runs changes. A tool starts a run the way `run <workflow>` does now; the run is durable, audited and under the same approvals. What is new is the way in and the way out.

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

Owners never see "made tool". On their screens it is **Your tools**, and making one is **Use it from chat**.

## 3. What exists, and what is missing

| Piece | Where | What it gives |
| --- | --- | --- |
| `run <workflow>` | [WhatsApp](../whatsapp.md#for-people-using-taskiem-on-whatsapp), the same on Telegram, Slack and the web's Taskiem conversation | Finds a workflow deployed in `prod` by name, asks its inputs, shows what will run, waits for **yes**, and says how the run ended |
| Inputs as a form | `taskiem_inputs` (a WhatsApp Flow), `SchemaForm` on the web | A form drawn from the workflow's input schema: ten flat fields at most in WhatsApp, asked one by one otherwise |
| How a run ended | `waSendRunOutcome` in `api/whatsapp_approvals.go` | "*Workflow* completed" (or failed, with the step that failed in plain words) and a link to the run. **Never what the run found or made** |
| Outputs | `outputs` at the top of a workflow (decision 0048) | Named, typed values a workflow gives back. Today only another workflow reads them |
| Approvals | The approval step, buttons and the PIN | A person decides with **Approve** and **Reject**, with step-up where the policy asks |
| Asking mid-run | The form step ([forms](../forms.md#a-form-step)) | A one-time link by SMS or email, or in the app for a role. **Not in the chat the run was started from** |
| Telling people | `notify.tell` (`connectors/notify`) | A message to the owner, a role or named members. **Text only, no buttons** |
| Public menus | [WhatsApp](../whatsapp.md#public-menus) | Up to nine workflows a business marks public on its own number. The person gets a reference, **not the outcome** |
| The tools panel | `web/src/lib/tools.ts`, `chatTools` in `api/chat_tool_requests.go` | Tiles, a sheet, `/` and a word. **A fixed list** |
| The reader's requests | `POST /v1/chat-tools/request` | A typed request is read by the model and answered with a filled-in form. **Built-in tools only.** Nothing is saved from words: the form's Save does it |

So the run, the form, the confirmation and the approval are built. Missing are the declaration, the answer, buttons, asking back in the same chat, and a panel and a reader that know about a business's own tools.

One thing was not settled by reading: `whatsapp` is in the list of trigger types in `schemas/wd-v1.schema.json`, and no code under `engine/wd` or `engine/wdcheck` names it. Whether it is used anywhere has to be checked before §5 is built, since this plan does not use it.

## 4. The three things a tool is made of

The kinds of tool people will want (look something up, do something, record something, pick from a list, be asked a question, decide, be warned, get a report) come down to three mechanisms.

| Mechanism | What it is | Kinds it covers | New work |
| --- | --- | --- | --- |
| **A tool that is called** (§5 to §8) | A workflow started by hand that declares itself, takes inputs and answers | Look up, Do, Record, Pick | The declaration, the answer, care, the panel, the reader |
| **A question inside a run** (§9) | A run stops to ask a person, in the chat it was started from, and goes on | Ask back, Decide | The form step in chat. Decide is the approval step, built |
| **A message with buttons** (§10) | A run that started by itself (a schedule, a webhook, an event) tells people, and its buttons call tools | Watch, Report | Buttons on `notify.tell` and on answers |

A useful tool is often two of these: a warning whose button is a Do, or a list whose row action ends in an approval.

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
| `command` | The word after `/`. `^[a-z][a-z0-9-]{1,23}$`, one per business and environment, and never a word Taskiem's own commands use in any language that is on (§12) |
| `about` | One line, to 120 characters: under the tile, in `tools`, and what the reader is told the tool is for |
| `says` | Up to eight phrases that find the tool when a message starts with one. Optional |
| `icon` | One of a fixed set drawn by Taskiem. Optional |
| `for` | `members` (the business's own people), `customers` (anyone writing to the business's own number) or `linked` (businesses it is linked to). Phase by phase: §15 |
| `roles` | With `members`: who may use it, beside needing `run.start`. Left out: everyone with `run.start` |
| `answer` | What comes back (§7). Left out: today's "completed" message |

Rules:

- **Only a workflow a person starts.** `tool` is refused unless the trigger is `manual`. A workflow that runs on a schedule or an event is not called; it tells people (§10).
- **Inputs are the workflow's `inputs`**, unchanged. What draws as a form today draws as this tool's form.
- **The deployed version is the tool.** A tool appears when a version with `tool` is deployed to `prod`, changes when the next is, and goes when a version without it is. A draft is never a tool. With Advanced on, the dev environment has its own tools, tried from the web only.
- **`tool` is not part of what the run does.** Like canvas layout it does not change `decide()`; unlike layout it is in the versioned document, because the command, the audience and the answer must be reviewed and published with the steps they describe.

## 6. Where a tool shows, and how it is called

| Place | How |
| --- | --- |
| The web and the phone app | **Your tools**, a row of tiles after the built-in ones in the tools panel, each with its name and `about`; `/` and its command or the start of its name; the two used last, as now |
| WhatsApp, Telegram, Slack | `/expense`, or the plain command where it clashes with nothing; `tools` and `help` list a person's tools with their commands; a phrase from `says`; or a sentence the reader takes to it |
| A button | On another tool's answer, or on a message a run sent (§10) |

**Calling it.**

1. **With nothing after it** (`/expense`, a tile, a button with no values): the form. A sheet on the web and the app, `taskiem_inputs` on WhatsApp, a Mini App form on Telegram, and one question at a time where a form cannot carry the inputs, as `run` does now.
2. **With words after it** (`/expense 4500 diesel`, or "spent 4,500 on diesel" by a `says` phrase or the reader): the reader fills what it can and the form opens **filled in**. A tool with one required input takes the rest of the message as that input, with no model.
3. **Then care** (§8): nothing more, **Yes**, or step-up.
4. **Then the run**, started as the person, in `prod`, with `tool: <command>` and the channel in the audit entry.
5. **Then the answer** (§7), in the conversation it was called from.

**Order of reading a message.** A question Taskiem is waiting on, first, as now. Then an exact `/command`. Then Taskiem's own word lists. Then a made tool's `says` phrase at the start of the message. Then the model, which is given Taskiem's commands and the person's tools (name, `about`, input names) and may answer with one of them and proposed values, or with none. **Taskiem's own commands always win**, so a business cannot take over `invoice` or `yes`. Two tools that fit are asked about with a numbered pick, as two tables are.

**What the model may do is unchanged:** choose a command and propose values. It never starts a run, never answers **yes**, and its values are always shown to the person before anything runs (the filled-in form, or the echo line of §8).

## 7. The answer

A tool's answer is built from the workflow's `outputs`, which already exist with names and types. `answer.type` chooses the shape.

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

- **Masked as every message is.** Outputs pass through the same masking as today's messages (account numbers to four digits, phones, names, nothing named like a secret). An output declared personal with `x-pii` is masked whatever its name. A person who may see the full value opens the run.
- **Amounts** are in kobo in the workflow and drawn by Taskiem as naira (`naira()`), so no tool formats money for itself.
- **A run that fails** answers as now: what failed, in plain words, and a link.
- **A run that takes long.** If the run has not ended in ten seconds the person is told "Working on it. I'll tell you when it's done", and the answer comes when it does, through the run watch that exists.
- **Outside WhatsApp's 24-hour window** an answer cannot be free text or carry buttons: it goes as the run-outcome template with **Open**, as outcomes do now.
- **An answer is not a record.** It is drawn from the run's outputs when the run ends and kept as a message. It does not update itself afterwards.

## 8. Care: what a tool asks before it runs

Today every start from chat needs an explicit **yes** after a summary. That is right for a write and tiresome for "how many days of leave do I have?".

**Care is worked out from the workflow, not declared by its author**, so a tool cannot claim to be harmless:

| Care | When | What the person sees |
| --- | --- | --- |
| **Answers at once** | Every step that reaches outside is a `read`: connector actions of class `read`, `http` steps that are `GET` or `HEAD`, code steps (whose `host.fetch` allows only those), no step that writes a table, sends a message, or starts another workflow that does | An echo of what was understood ("Leave left for: Musa"), then the answer |
| **Yes** | Anything else | The summary of exactly what will run, then **yes**, as now |
| **Step-up** | The business's policies ask for it for this workflow or a step in it | **Yes**, then the PIN or the hand-off link, as an approval does now |

- `wdcheck` works this out when a version is published and stores it with the tool. A step whose class is not known counts as a write.
- An owner may make a tool **more** careful in its settings (always ask **yes**), never less.
- An approval step inside the workflow is separate and unchanged: care is about starting the run, approval is about a step in it.
- **The echo line matters.** With no **yes**, the person must still see what the reader understood, so a wrong reading is noticed.
- This changes one row of [WhatsApp's security model](../whatsapp.md#security-model) ("every start needs an explicit yes"). It is a decision (§14, 2).

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
- **Buttons are signed**, as approval buttons are: an HMAC under `TASKIEM_WHATSAPP_TOKEN_KEY` over the business, the tool, the values and an expiry, with a row for the nonce. They work for seven days, any number of times, only for the business's members; a button on an answer to a customer works only from that customer's number.
- **`notify.message`** (to people outside the business) does not get buttons in this plan. A customer reaches tools through the business's own number (§11).
- The same `buttons` shape is what cards and list rows use (§7).

## 11. Customers and linked businesses

**Customers** (`for: "customers"`). Today's public menu becomes tools for customers:

- The nine workflows a business marks public become tools with `for: "customers"`; **menu** lists them as now. Existing menus are carried over by a migration, each with the label it has as its `name`.
- Everything the public menu promises stays: inputs by form only, a summary and **yes** (customers' tools never answer at once without it, whatever their care), runs as `whatsapp_public:<number>` with no roles, the same rate limits, and nothing else reachable.
- **New: the answer.** A customer now gets the tool's answer, where today they get only a reference. This closes an item in WhatsApp's "Not yet".
- A customer's answer shows only outputs the workflow marks `public: true`. Anything else is left out, so a workflow written for staff does not leak when someone later opens it to customers.

**Linked businesses** (`for: "linked"`). A tool appears in the other business's side of the conversation ([business chat tools](business-chat-tools.md) §3), under "Their tools". The run is the offering business's, started by `link:<business>` with no roles, inputs by form, always **yes**, answer from `public` outputs only. This is the last phase, and it needs its own look at what a linked business may be shown.

## 12. What the validator refuses

When a version with `tool` is validated (`wdcheck`), and again when it is deployed:

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

A plan limit, `made_tools` (deployed tools a business may have), joins the others in [operations](../operations.md#plan-limits). Twenty is a starting figure; the reader is given at most forty tools, the nearest by words when there are more.

## 13. Making a tool

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

## 14. For the founder to decide

| # | Question | Recommendation |
| --- | --- | --- |
| 1 | Is `tool` a block of the workflow, or a trigger type of its own (`chat`)? | **A block.** The inputs, the outputs and the run are the workflow's already, and one workflow then has one way to be started by a person wherever they are. A trigger type would give tools their own inputs and split `run` from tools |
| 2 | May a read-only tool answer without **yes**? | **Yes**, with the echo line, for members only. It shows data the person may already see and changes nothing. Customers and linked businesses always get **yes** |
| 3 | Do made tools need the chat tools switch on? | **The panel does; the commands do not.** `/expense` works wherever `run` works today, so a business on WhatsApp alone has its tools |
| 4 | Does the public menu become tools for customers, or stay beside them? | **Become**, by migration, so there is one way to offer a workflow to customers and they get answers |
| 5 | Tools for linked businesses: in this plan, or later? | **Later**, as its own plan after members and customers are in use |
| 6 | Should made tools wait for [pages](#16-what-this-is-not) so both share one catalogue? | **No.** Build tools first with the three answer shapes; write pages so its list and action blocks are these same shapes |
| 7 | Decision number | The next free one. 0067 is in an open pull request; 0063 and 0064 are absent from the log |

## 15. Phases

| Phase | What | Done when |
| --- | --- | --- |
| T0 | The decision, `tool` and `answer` in `schemas/wd-v1.schema.json` and the contract, the validator's rules (§12), care worked out and stored | A definition with `tool` validates or is refused for the right reason; nothing uses it yet |
| T1 | Members' tools everywhere: the index of deployed tools, **Your tools** in the panel and `/`, the commands on WhatsApp, Telegram and Slack, the form, care, the `text` answer, **Use it from chat** in the builder, the SDK | A person makes "Log an expense" without Advanced, and uses it from the web, the phone app and WhatsApp |
| T2 | `card` and `list` answers, buttons on answers and on `notify.tell`, the sheet for a list | "Open tickets" lists, and **Assign to me** on a row works from a phone |
| T3 | The reader: tools given to the model, proposed values, the numbered pick, evaluation cases in English and Pidgin | "spent 4,500 on diesel" opens the expense tool filled in; a built-in command is never taken by a tool |
| T4 | `to: "starter"` on the form step | A run asks "Which branch?" in the chat it was started from and goes on with the answer |
| T5 | Customers: the public menu carried over, answers for customers, `public` outputs | A customer picks from **menu**, fills the form, says **yes** and gets the answer |
| T6 | The eight recipes, and the AI builder drafting `tool` | "Describe it" produces a working tool from one sentence |
| T7 | Linked businesses | Its own plan |

T1 is the first thing worth showing. T2 and T3 can go side by side after it.

## 16. What this is not

- **Not pages.** A screen made of blocks for a workflow (a list, a board, figures) is a separate plan, discussed with the founder and not written. It should reuse this plan's shapes: a tool is an action block, a `list` answer is a list block.
- **Not the built-in tools rewritten.** Products, Stock, Orders, Money and the rest stay as built. They hold rules a wiring of steps must not be able to break.
- **Not code in the chat.** A tool cannot bring its own drawing, script or frame. The shapes are Taskiem's, which is what lets the web, the phone app and WhatsApp all draw them and keeps the phone app's picture test meaningful.
- **Not a bot.** There is no free conversation with a tool: it is called, it may ask through a form step, it answers.
- **Not a new way to run.** No new step type, nothing in `decide()`.

## 17. Storage and API

**Storage.** One table, `workflow_tools`, a row for each deployed tool: tenant, workflow, environment, version, command, name, `about`, `says`, icon, audience, roles, care, the answer's type. Written in the transaction that deploys a version and removed by one that deploys a version without `tool`, so it never disagrees with what is deployed. Unique on tenant, environment and command. Under forced row-level security like every tenant table. Button nonces reuse the shape of `whatsapp_tokens`.

Runs gain nothing new: a run started by a tool is recorded with `run.start`, the channel, and `tool: <command>`.

**API** (each in `api/openapi/openapi.yaml` in the same change, and in `mobile/api.txt` for the app):

| Call | What |
| --- | --- |
| `GET /v1/tools` | The caller's tools in the business: command, name, `about`, icon, the input schema, care |
| `POST /v1/tools/{command}/read` | Words in, proposed values out (the reader). Nothing is started |
| `POST /v1/tools/{command}/start` | Values in. Answers the run, or the summary and a token to confirm with, or that step-up is needed |
| `POST /v1/tools/{command}/confirm` | The token from start |
| `GET /v1/runs/{id}/answer` | The answer as data (its type, text, lines, rows, buttons), masked, for the web and the app to draw |
| `POST /v1/tools/buttons/{token}` | A button press: the tool and its values, for the form |

The chat apps reach the same functions through the conversation engine, not these routes.

## 18. Security and abuse

| Concern | Control |
| --- | --- |
| A tool that takes over a command of Taskiem's | Reserved words refused at validation and at deploy; Taskiem's commands are read first (§6, §12) |
| A tool that says it is harmless | Care is worked out from the steps, and an unknown step counts as a write (§8) |
| The model starting something | It proposes; the person sees the values and, for a write, says **yes**. Unchanged |
| A wrong reading on a tool that answers at once | The echo line; and such a tool only reads |
| Words meant to steer the model, in a tool's `about` or `says` | They are a business's own text, read only for that business's own members; they are length-limited and passed as data, and the model can still only return one of the listed commands |
| Someone using a tool they should not | `run.start`, the tool's `roles`, and the person's roles at that moment; refusals audited |
| A forwarded or forged button | Signed, with a nonce row; a press acts as the presser, never as whoever the message was sent to (§10) |
| Personal data in answers | The message masking, `x-pii` outputs, and for customers and linked businesses only `public` outputs (§7, §11) |
| A flood of calls | The per-number limits that exist for run starts (three, then one every 20 seconds) for tools that write; a looser one for tools that only read, set when T1 is measured; the plan's run limits apply to every run |
| Cost of the model | Within the business's AI budget, as the reader is now; `/command` and `says` use no model |
| One business's tools seen by another | `workflow_tools` is tenant data under row-level security, read with the conversation's business in scope |

## 19. Tests

| Test | What it proves |
| --- | --- |
| Schema and validator | Each row of §12 is refused with a plain sentence; a correct tool validates |
| Care | A workflow of reads answers at once; adding a table write, a message, a non-GET `http` step, or a subflow that writes makes it **Yes**; a policy makes it step-up; an owner can raise it and not lower it |
| Deploy keeps the index true | Deploy, deploy a change, deploy without `tool`, roll back: `workflow_tools` matches what is deployed each time |
| Reading order | A pending question, `/command`, Taskiem's words, `says`, the model, in that order; `invoice` and `yes` cannot be taken |
| The same tool on every channel | One fixture workflow called from the web, WhatsApp, Telegram and Slack gives the same run and the same answer's content |
| Answers | Text, card and list at their limits; masking; an empty list; a failed run; a slow run; outside the 24-hour window |
| Buttons | A press fills the form as the presser; another business's member, an expired token and an altered token are refused; a button to a missing tool says so |
| Ask back | A question in the chat, answered, timed out, and two runs asking at once |
| Customers | The migration carries menus over; a customer gets the answer; an output not marked `public` never reaches them |
| Web | **Your tools** in the panel, the form sheet, each answer shape; added to `e2e/accessibility.spec.ts` and `src/lib/plainwords.test.ts` |
| Phone app | The same screens in `mobile/`, with their pictures in the parity test, in the same change as the web's |
| The reader | Cases in `evals/chat`: sentences that should reach a made tool with the right values, and sentences that must still reach Taskiem's own commands, in English and Pidgin |

## 20. Needs a person

| Item | Who |
| --- | --- |
| The decisions of §14 | The founder |
| Meta's current limits for reply buttons and list messages, and whether list messages need review for the business's account | Whoever holds the WhatsApp Business Account |
| Words for owners ("Your tools", "Use it from chat", "What should it say back?") checked against the plain-words rules, and in the four other languages | Product, native speakers |
| The fixed set of icons | Design |
| Real requests for the evaluation cases, once design partners use it | Partner success |
