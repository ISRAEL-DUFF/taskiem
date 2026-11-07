# Support tickets: email threads

Status: **Phase 1 built** 7 November 2026 (email threading, §3 to §6); phase 2, the ticket templates, planned · Decision: [0063](../decisions/0063-email-threads.md) · Builds on: [email triggers](../email.md) ([0038](../decisions/0038-inbound-email.md)), signal steps ([wd/v1](../contracts/wd-v1.md#runtime-semantics-enginedecide)), [messages to people outside](../messages.md)

## 1. Summary

A family of ticketing templates is coming: a customer opens a ticket by email, a workflow records it, an `ai` step picks the department, the department is told, and the workflow then carries a conversation with the customer until the ticket is resolved. All of it rests on one thing a workflow cannot do today: keep an email conversation together. A reply must find the ticket it answers and reach the run that waits for it, and Taskiem's own email to the customer must thread in the customer's mail app.

Phase 1, this document, builds the threading. Phase 2 builds the templates on it.

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

## 7. Phase 2: the templates

Not built here: the ticket templates (opening, numbering, routing by an `ai` step, telling the department, the conversation loop, resolving and reopening), their data table, and an owner's view of open tickets.
