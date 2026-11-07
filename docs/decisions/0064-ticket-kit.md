# 0064 — The ticket kit: tickets in data tables, a helpdesk address other workflows send from, and answers a mailbox sends

Date: 2026-11-07 · Status: **Accepted** 2026-11-07 (built: [tickets](../spec/tickets.md#7-phase-2-the-ticket-kit), [guide](../guide/tickets.md)) · Amends: [0063](0063-email-threads.md) (who may send in a thread, and which runs hold a reply back)

## Context

Decision 0063 lets one email workflow carry a conversation: it sends in a thread, and the reply wakes its run or starts a new one. Support tickets need more. A ticket is opened by email, by a web form, on WhatsApp or by a member; it is answered by a department from a form, followed up on a schedule and closed days later; and it is answered the way it came, which for some businesses is their own Zoho Mail or Gmail mailbox. One run per ticket waiting weeks for replies would hold every ticket's state in run history, out of the owner's sight, and pin it to the workflow version that started it.

## Decision

1. **A ticket's state is two data tables, not a run.** `tickets` and `ticket_messages` are known tables (`templates/tables/`). Every event is a short run of one of a family of templates that read and write them: the entrances open tickets and append replies, a members' form answers them, a schedule follows them up. No run waits for a customer.
2. **Shared pieces are callable templates.** `ticket-route` (department, priority, summary), `ticket-open` (number, save, route, tell the department, tell the customer; it uses `ticket-route`) and `ticket-answer` (answer the way the ticket came, record it, mark the ticket). Every entrance calls `ticket-open`. Since templates nest three deep counting the workflow, `ticket-open` is a step of a workflow and never of another template.
3. **Numbers come from the row.** A ticket is `T-` and 1000 plus its row's `id`, written by the step after the insert: unique and in order with no counter or unique constraint, which template tables cannot declare.
4. **Sending in a thread from another workflow's address.** `email@1 send_in_thread` takes `address`: the Taskiem address, or the workflow id, of a threaded email workflow of the same business and environment, whose address the email is sent from and whose records the thread's are. Anything else is refused for good. Only a still-going run of that address's own workflow holds a reply back (0063 held it for any run that sent in the thread); so a reply to an answer sent by the reply form starts a run of the helpdesk with `trigger.email.thread`, which appends it to the ticket.
5. **A mailbox's answers are rows its sending workflow sends.** An answer to a ticket that came to the business's Zoho Mail or Gmail is saved in `ticket_messages` with its `mail` and no `sent_at`; a template per mailbox, started by `row_changed` on that table, replies in the customer's conversation and marks it sent. The reply form and the follow-up therefore use no mailbox connection: readiness would otherwise make every business connect both mailboxes to publish them.
6. **Departments are one parameter of plain lines,** `name | role | what it looks after | words`, parsed by a `transform`, since a template parameter has no list type. The AI step reads the names and descriptions; rules read the words; `notify@1 tell` reaches the role.
7. **The AI step reads the message.** Routing by AI lists the message in `personal_data`, because the taint masker would hide a sealed message whole; the customer's name, email and phone are not in its input. `rules` and `chosen` send nothing to a model.

## Alternatives considered

- **One run per ticket** waiting on `email.reply` (0063's model). Weeks-long runs, state the owner cannot see, a version pinned for the ticket's life, and nothing for tickets that do not come by email.
- **A reply form and follow-up per mailbox.** Six templates where two do, and a change to answering made three times.
- **Departments as a table.** Empty when the template is picked, so tickets would route nowhere until it is filled; and a parameter is what the Start page asks for.
- **A list parameter type.** A change to the template format, the Start page and the API for one kit; the lines read plainly.
- **The reply form open to the department roles only.** A form trigger's `roles` is a list fixed in the definition, which a text parameter cannot fill; the form is open to every signed-in member of the business instead (open question).
- **Masking the message before the AI step reads it.** The masker hides a sealed value whole; trimming the text to defeat that would only hide the choice. `personal_data` makes it explicit and audited when the version is published.

## Consequences

- `email@1 send_in_thread` gains `address` (a connector manifest addition). The wd/v1 contract text says so; no schema changes.
- `ticket-follow-up` counts every hour (no business hours yet).
- Each new `ticket_messages` row starts a short run of a mailbox's sending workflow, which does nothing unless the row goes out by that mailbox.
- The other entrances (web form, WhatsApp, webhook, staff) are templates calling `ticket-open` ([how](../spec/tickets.md#8-how-another-entrance-plugs-in)).
