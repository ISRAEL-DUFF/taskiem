# Support tickets: email threads

Status: **Phase 1 built** 7 November 2026 (email threading, §3 to §6); **phase 2, the ticket kit, built** 7 November 2026 (§7: the shared pieces and the email entrances; §8: the web form, WhatsApp, webhook and staff entrances) · Decisions: [0063](../decisions/0063-email-threads.md), [0064](../decisions/0064-ticket-kit.md) · Guide: [customer tickets](../guide/tickets.md) · Builds on: [email triggers](../email.md) ([0038](../decisions/0038-inbound-email.md)), signal steps ([wd/v1](../contracts/wd-v1.md#runtime-semantics-enginedecide)), [messages to people outside](../messages.md)

## 1. Summary

A family of ticketing templates is coming: a customer opens a ticket by email, a workflow records it, an `ai` step picks the department, the department is told, and the workflow then carries a conversation with the customer until the ticket is resolved. All of it rests on one thing a workflow cannot do today: keep an email conversation together. A reply must find the ticket it answers and reach the run that waits for it, and Taskiem's own email to the customer must thread in the customer's mail app.

Phase 1 builds the threading (§3 to §6). Phase 2 builds the templates on it (§7), and the entrances other than email after them (§8).

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Ticket key, thread | The key a workflow gives a conversation, such as `T-1042`: letters, digits and single hyphens, up to 24 characters, compared without regard to case and written in capitals |
| Reply address | The workflow's own email address with a tag for one thread: `k3x9q2m7a1bz+t-1042-q7m2x9k4a8bz@in.taskiem.app` |
| Reply tag | The part after `+`: the ticket key in lower case, a hyphen, and 12 characters Taskiem derives from the key and a secret of the address |
| Threaded workflow | One whose email trigger has `threads: true` |

## 3. What a workflow sees

- **Every email trigger** gets two more fields, whether threaded or not: `trigger.body.in_reply_to` (one Message-ID, or `""`) and `trigger.body.references` (a list, newest last, at most 20). Mail to `<address>+<tag>@…` reaches the address as before, and the tag is `trigger.email.tag`.
- **A threaded workflow** also gets `trigger.email.thread` (the ticket key) and `trigger.email.matched_by` (`tag`, `headers` or `subject`) when a message is found to belong to a thread (§5).
- **Sending:** `email@1 send_in_thread` (§4).
- **Waiting:** a `signal` step on event `email.reply` with the ticket key as its correlation. Its output is the reply: `{body, email}`, shaped as `trigger.body` and `trigger.email` are.

```json
{"id": "answer", "type": "signal", "config": {"event": "email.reply", "correlation": "=steps.ack.output.thread", "timeout": "7d"}}
```

## 4. Sending in a thread

`email@1 send_in_thread` takes `to`, `subject`, `message`, `thread` and optionally `in_reply_to`, `references` and `address` (below). It belongs to the email connector, not to Tell me, because everything it needs is the email trigger's: the workflow's address, the address's secret and the record of what was sent in each thread. It sends through the same outbox as Tell me's `message` (the platform's sending address, in the business's name, counted and limited per recipient, the recipient and text sealed).

The email:

- comes from `<Business> via Taskiem <the platform's sending address>`;
- has `Reply-To:` the thread's reply address;
- has a Message-ID Taskiem makes (`<tk-…@<mail domain>>`), the same on a retry of the step;
- has `In-Reply-To:` and `References:` so mail apps thread it: `in_reply_to` when given (the customer's message), else the last email Taskiem sent in the thread; `references` as given, then the earlier ones Taskiem sent, at most 20;
- has the subject tagged `[#T-1042]` once (a subject that carries it already is left as it is).

Its output is `{message_id, thread, reply_to, subject, queued}`. A step retried with the same idempotency key queues nothing new and answers the same.

The workflow must have an email trigger with `threads: true` in the run's environment; otherwise the step fails with a message saying so.

**From another workflow's address** (built 7 November 2026, for the ticket kit, §7). `address` names the threaded email workflow whose address the email is sent from: its Taskiem address (`trigger.email.address`; a tagged one also does) or its workflow id. So a ticket opened by a form, WhatsApp, a webhook or a member, and the department's reply form, email the customer through the business's helpdesk address. The step refuses, for good and before anything is queued, an address or id that is not an email workflow of the run's business, one in another environment, one without `threads: true`, and a closed or suspended address. Reply-To, the Message-ID and the `email_thread_messages` row are the helpdesk address's (the row's `run_id` is the sending run). A reply then reaches the helpdesk workflow (§5): no run of it waits, and only a still-going run **of that workflow** holds a reply back, so it starts a run of the helpdesk carrying `trigger.email.thread`.

## 5. Finding a reply's ticket

For a threaded workflow, in this order:

1. **The reply tag**, when it verifies against the address's secret. A tag that does not verify is ignored, so guessing ticket numbers gets nothing.
2. **The headers**: `In-Reply-To`, then `References` from newest to oldest, matched against the Message-IDs Taskiem sent from this workflow's address.
3. **A `[#T-1042]` subject tag**, only when the sender is proven (`auth.aligned`) and is someone Taskiem emailed in that thread (compared by the email's personal-data pseudonym, never in clear).

Then:

- **A run of the workflow waits** on `email.reply` for that key in the same environment: the reply is delivered to it, and no run starts. The trigger's tab lists the message as a reply given to the run (status `replied`).
- **A run of the workflow, which sent in the thread, is still going but not waiting** (it is between steps, or waiting on something else): the message stays in the inbox and is looked at again every 5 minutes, for up to 7 days. If the run waits by then, it gets the reply; if it ends, the reply starts a new run.
- **Otherwise** (the ticket's run ended, or there never was one): a new run starts, with `trigger.email.thread` set, so the workflow can reopen the ticket.

A message whose ticket cannot be found starts a run as in any email workflow.

## 6. Records, retention and erasure

`email_thread_messages` holds, per message Taskiem sent in a thread: the address, the ticket key, the Message-ID, the run that sent it and the recipient's personal-data pseudonym. It has no content. Rows go with the run that sent them (they are deleted when the run is purged at the end of its retention) and with the address. Erasing the recipient clears their pseudonym, so the subject-tag match no longer finds them; the rest is not personal.

Each address has a secret of its own (`email_endpoints.reply_key`, 32 random bytes, made the first time it sends in a thread), from which reply tags and Message-IDs are derived.

## 7. Phase 2: the ticket kit

### 7.1 Shape

A ticket's state lives in two data tables, not in one long-running run: each event (a message in, an answer out, a quarter-hour passing) is a short run of one of a family of small workflows that read and write them ([decision 0064](../decisions/0064-ticket-kit.md)). A run never waits for a customer, so a ticket can stay open for weeks, a workflow can be changed and republished meanwhile, and the owner sees every ticket on the Tables page.

| Table (known, `templates/tables/`) | Columns |
| --- | --- |
| `tickets` | `reference` (T-1042), `subject`, `customer_name`, `customer_email`, `customer_phone`, `channel` (`email`, `form`, `whatsapp`, `api`, `staff`), `department`, `role` (the department's role), `priority` (`low`, `normal`, `high`, `urgent`), `status` (`new`, `open`, `waiting_on_customer`, `resolved`, `closed`), `assigned_to` (a member's sign-in email), `summary`, `last_customer_at`, `last_reply_at`, `resolved_at`, `reminded_at`, `escalated_at`, `thread` (the conversation: the reference on Taskiem's address, the mailbox's `thread_id` on Zoho Mail and Gmail), `mail` (how its answers go: by email through `taskiem`, `zoho` or `gmail`; `whatsapp`, from the business's own WhatsApp number; `member`, to a member of the business who asked; or empty), `mailbox` (the helpdesk's Taskiem address), `last_message_id` (the customer's last email, to answer it), `previous_reference` (the ticket it follows); and every table's `id`, `created_at`, `updated_at` |
| `ticket_messages` | `reference`, `direction` (`in`, `out`, `note`), `author`, `body`, `at`, `message_id`, `channel`, `mail`, `sent_at` (empty on an answer waiting for the business's mailbox to send it) |

The subject, names, summary, author and body are personal columns; the email and phone are personal by type.

**Numbers.** A ticket's reference is `T-` and 1000 plus its row's `id`: the insert answers the row, and the next step writes the reference. It is unique and in order without a counter, and a retried insert (its idempotency key) does not number twice. A row without a reference (the second step failed) is not a ticket to the other workflows.

### 7.2 The pieces (templates)

| Template | Kind | Does |
| --- | --- | --- |
| `ticket-route` | callable | Department, role, priority and summary: an `ai` step reads the subject and message with the departments' descriptions (`routing: ai`), else the first department with one of its words in the message (`rules`), else the department the customer chose (`chosen`); one that is none of the departments gives the default department |
| `ticket-open` | callable, uses `ticket-route` | Inserts the ticket (`new`), numbers it, saves the first message, tells the department's role (`notify@1 tell`) with the reference, subject, priority, summary and the reply form's link (`env.ticket_reply_form` + `?ticket=T-1042`), and tells the customer their number (below) |
| `ticket-answer` | callable | Finds the ticket by reference, sends the answer the way the ticket came, saves it as an `out` message, and sets the status, `last_reply_at`, `resolved_at` and, when nobody has it, `assigned_to` |
| `ticket-reply` | members' form | A member's answer, resolution or note on a ticket by its number (prefilled from the link); answers go through `ticket-answer`; an unknown number is told to the member |
| `ticket-follow-up` | every 15 minutes | Reminds the department (or the assigned member) of `new` and `open` tickets with no answer since the customer last wrote, past the first-answer hours, once per customer message; tells the escalation role past twice that, once; resolves `waiting_on_customer` tickets quiet past the days allowed through `ticket-answer` with the closing message; closes `resolved` tickets past their days |
| `helpdesk-email` | email trigger, `threads: true` | T1: Taskiem's address (below) |
| `helpdesk-zoho-mail`, `helpdesk-gmail` | `new_mail` | T2, T3: the business's own mailbox (below) |
| `helpdesk-zoho-mail-send`, `helpdesk-gmail-send` | `row_changed` on `ticket_messages` | Send each answer left for that mailbox |
| `helpdesk-web-form` | public form | T4: a contact form (§8.1) |
| `helpdesk-whatsapp`, `helpdesk-whatsapp-send` | `whatsapp@1` `messages`; `row_changed` on `ticket_messages` | T5: the business's WhatsApp number, and sending its answers (§8.2) |
| `helpdesk-webhook` | webhook | T6: the business's app or website (§8.3) |
| `staff-it-desk`, `staff-hr-desk` | members' form | T7, T8: requests from the business's own people (§8.4) |

Parameters a business sets: `departments` (one per line, `name | role | what it looks after | words`), `default_department`, `routing`, `acknowledgement`, `reopen_days` (T1 to T3, T5); `helpdesk_address` (T4, T6); `approval_kinds`, `approver_role` (T8); `first_answer_hours`, `escalation_role`, `waiting_days`, `closing_message`, `close_days` (follow-up); the mailbox or WhatsApp connection (T2, T3, T5). Business hours are not a parameter yet: the follow-up counts every hour.

**How a customer hears from the business.** Every piece chooses the same way from the ticket: `mail` is `zoho` or `gmail` and there is an email, or `whatsapp` and there is a phone: the answer is saved as an `out` message with that `mail` and no `sent_at`, and the mailbox's (or WhatsApp number's) sending workflow sends it, as a reply in the conversation, and marks it sent; `mail` is `member` and there is an email: `notify@1 tell` to that member; `mail` is `taskiem` with an email and a `mailbox`: `email@1 send_in_thread` from that address (§4), thread the reference; else a phone or an email: `notify@1 message` (WhatsApp or Telegram where the customer asked, else SMS, else email); else nobody is told, and the answer is still saved. So `ticket-reply` and `ticket-follow-up` work for every entrance without a connection of their own: a mailbox connection would otherwise be needed to publish them even by a business that does not use that mailbox (readiness).

**The email entrances.** T1 finds the ticket of a reply by `trigger.email.thread` (the reference); T2 and T3 by `mail` and the mailbox's `thread_id`. A message in a known ticket's conversation is appended (`in`), the ticket's status becomes `open`, `last_customer_at` and `last_message_id` are kept, and the department's role (or the assigned member) is told. A ticket `resolved` or `closed` longer than `reopen_days` ago is not reopened: a new ticket opens with `previous_reference` naming it. Anything else opens a ticket through `ticket-open`.

### 7.3 Personal data

Run histories seal what the triggers and steps carry as usual. The `ai` step lets the message's subject and text reach the model (`personal_data: [message]`), since it must read them to route the ticket; the customer's name, email and phone are not in its input, and every other personal value in its prompt is masked ([AI steps](../contracts/wd-v1.md#ai-steps)). A business that does not want this chooses `rules` or `chosen`. The tables are the business's records: their personal columns are masked in run outputs and chat, as every table's are.

## 8. How another entrance plugs in

Built 7 November 2026: the web form, WhatsApp, webhook and staff entrances (IT, HR) are templates of their own whose workflow calls `ticket-open` as a template step (a workflow, not another template: `ticket-open` already nests `ticket-route`, and templates nest three deep):

```json
{"id": "open", "type": "template", "template": "ticket-open",
 "params": {"departments": "{{departments}}", "default_department": "{{default_department}}", "routing": "{{routing}}", "acknowledgement": "{{acknowledgement}}"},
 "input": {"channel": "form", "subject": "=trigger.body.subject", "message": "=trigger.body.message",
           "customer_name": "=trigger.body.name", "customer_email": "=trigger.body.email", "customer_phone": "=trigger.body.phone",
           "department": "=trigger.body.department", "mail": "taskiem", "mailbox": "{{helpdesk_address}}"}}
```

- `channel` is `form`, `whatsapp`, `api` or `staff`; `subject` and `message` are required; the customer's name, email and phone as far as known.
- `department` is the customer's choice, used with `routing: chosen`.
- To answer by email in a conversation whose replies come back as tickets, give `mail: taskiem` and `mailbox`, the address of the business's `helpdesk-email` workflow (a parameter the entrance asks for). Its replies then reach `helpdesk-email`, which appends them to the ticket. Without them the customer is answered on their phone (`notify@1 message`), or by a plain email when they gave only that.
- `mail: whatsapp` (with a phone) leaves every answer for `helpdesk-whatsapp-send`, as a mailbox's are; `mail: member` (with the member's sign-in email) tells the member with `notify@1 tell`.
- `ticket-open`'s outputs are `reference`, `department`, `role`, `priority`, `summary` and `told_by` (`email`, `mailbox`, `member`, `message` or `none`).

**Contract changes for these entrances** (additions; nothing an existing ticket or workflow relies on changes): `ticket-open`'s `mail` input and the tickets' `mail` column take `whatsapp` and `member` besides `taskiem`, `zoho` and `gmail`; `ticket-open` and `ticket-answer` choose `mailbox` for `whatsapp` with a phone and `member` for `member` with an email, before the rules above; `ticket-open` outputs `role`; the `whatsapp@1` `messages` trigger declares the personal data a delivery carries (its `pii`) and an example. A ticket with channel `whatsapp` and no `mail` (opened by another workflow for a phone) is still answered by `notify@1 message`.

### 8.1 The web form (`helpdesk-web-form`)

A public form: name, an optional phone (`format: phone-ng`), subject and message, and, only when routing is `chosen`, the department on a second page whose `show_if` is the routing (`'chosen' == 'chosen'` once filled in). A parameter cannot fill a field's `enum`, so the department is typed, with a hint, and `ticket-route` checks it against the departments: anything else gives the default department. The email address is asked first and checked with a code (`verify: email`), and the ticket takes the checked address (`trigger.form.respondent.email`), so a stranger's address cannot be made to receive the business's mail, nor a subject the sender chose. The thank-you page shows the reference: `after.mode: result` and a `form@1 respond` step after `ticket-open`. `helpdesk_address` (optional) gives `mail: taskiem` and `mailbox`.

### 8.2 WhatsApp (`helpdesk-whatsapp`, `helpdesk-whatsapp-send`)

The business's own number through the WhatsApp Cloud API connector: a `connector_event` trigger on `whatsapp@1` `messages`, event `message`, reading `trigger.item.message` (one event per message, however Meta batches them). The text is the message's text, a pressed button's or picked row's title, or an image's or document's caption; reactions, system notices and unsupported messages are left alone. The customer's last ticket with that phone and `mail: whatsapp` (newest first) takes the message when it is not `resolved` or `closed`, or was resolved within `reopen_days`: an `in` row in `ticket_messages`, status `open`, `last_customer_at` and `last_message_id` (the wamid) kept, and the department's role (or the assigned member) told. Otherwise a ticket opens with `channel` and `mail` `whatsapp`, its subject the first line of the message (80 characters), following the old one when there was one. The customer's WhatsApp profile name is not in the event, so the author is their number.

Answers go from the number: `ticket-open`, `ticket-answer` (and so the reply form and the follow-up) leave them in `ticket_messages`, and `helpdesk-whatsapp-send` (started by `row_changed`) sends each with `send_text` while the ticket's `last_customer_at` is under 23½ hours old, WhatsApp's customer service window; later, it goes as the `late_answers` parameter says: `sms` (the default) by `notify@1 message` (WhatsApp or Telegram where the customer asked the business for updates, else SMS); `email` by email when the ticket has `customer_email`, else as `sms`; `sms_and_email` both. The email goes by `email@1 send_in_thread` through the `helpdesk_address` parameter when it is set (so the reply joins the ticket), else by `notify@1 message` with only the email. WhatsApp itself is not used then: a free-form message outside the window is refused and a template message needs one approved for the business. It then marks the row sent (`sent_at`, the wamid). `send_text` is an unsafe write: a send whose outcome is unknown is parked for a person, never repeated.

### 8.3 The app or website (`helpdesk-webhook`)

A `webhook` trigger at a path the business chooses (`/support/tickets`), `auth: hmac` (`X-Taskiem-Signature: sha256=<hex HMAC-SHA256 of the body>` with the environment secret `webhook_<the workflow's id>`, made on the Endpoints page), as the repo's other server-to-server webhook templates. The body is JSON, checked against the inputs schema before a run starts (a wrong body gets `422`): `subject` and `message` (required), `name`, `email`, `phone`, `department` and `external_id` (the app's own id, given back). `respond: true`, so the caller waits for the `respond` step after `ticket-open` and gets `201` with `{reference, department, priority, told_by, external_id}`; a run that takes more than 30 seconds (an AI step that is slow) answers `202` with the run id, and the customer still gets their number from `ticket-open`. A call repeated with the same body is the same delivery (no new ticket). It is meant for the app's server: a browser cannot keep the HMAC key, and the trigger takes no `cors`.

### 8.4 Staff: IT and HR (`staff-it-desk`, `staff-hr-desk`)

Members' forms (`access: members`): the requester is the signed-in member, their name and email filled from the account and locked (`x-form.from`, hidden), so nothing about a customer is typed. The ticket opens with `channel: staff` and `mail: member`; the member is told its number on the thank-you page and by `notify@1 tell`, and every answer (the reply form, the follow-up's reminders and closing message) reaches them the same way. Default kinds of request: IT's accounts and access, hardware, software, network and other (role `it`, routing `ai`); HR's leave, payroll, benefits and policy (role `hr`) and confidential matters (role `hr_lead`), routing `rules`.

HR routes by rules, the default of every ticket template (decision 0064): a request about pay, health or a complaint is personal, and with rules nothing goes to a model; a business that wants the AI chooses `ai` (the request's subject and text then reach it, never the member's name or email, §7.3). There is no more sensitive personal-data category than `other` for free text, which the subject and details are. The kinds named in `approval_kinds` (`Leave` by default; `none` for none) wait for an `approval` step by `approver_role` (72 hours, then rejected); the decision goes to the member through `ticket-answer` (approved: the ticket stays `open` for HR; not approved: `resolved`) and to HR's role by `notify@1 tell`. A member cannot approve their own request: the form's run is started by the member, and approvals keep four eyes.

Staff tickets are rows of the same `tickets` table as customers', so whoever may read the business's tables sees them, HR's included, and the reply form is open to every signed-in member who knows a number (as for customer tickets, decision 0064).
