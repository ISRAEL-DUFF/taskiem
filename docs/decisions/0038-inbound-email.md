# 0038 — Inbound email: an address per workflow, received by Taskiem

Date: 2026-10-21 · Status: **Accepted** 2026-10-21 (built: N10, [email triggers](../email.md))

## Context

"When an email comes in, do something" is one of the most common things owners ask for: a supplier's invoice, an order sent by email, a bank's transaction alert, a form a website emails. wd/v1 has reserved an `email` trigger since the start ([architecture §8.1](../spec/architecture.md): an inbound address per workflow, deduplicated by Message-ID), but nothing receives mail, and publishing one is refused ("email triggers are not available yet", `engine/ingest/triggers.go:177`).

The Gmail connector cannot fill the gap. Google's push notifications (`users.watch`) and polling both need `gmail.readonly`, `gmail.metadata` or `gmail.modify`. All three are *restricted* scopes, which means verification plus a yearly paid third-party security assessment (CASA) before anyone outside a 100-person test list can connect ([N0-2](../spec/no-technical-steps.md#n0-decisions-people-before-or-alongside-n1)). They would also cover Gmail only, and give Taskiem a whole mailbox to answer for, when a workflow wants one kind of message.

## Decision

1. **Each workflow with an `email` trigger gets its own address**, `<id>@<TASKIEM_MAIL_DOMAIN>`.
   - The id is 12 random characters, like a form's public id: stable across versions, one per workflow and environment, and changeable (the old address stops working).
   - An owner sends mail there directly, or forwards it from any mailbox with a filter (Gmail, Outlook, Zoho, cPanel). Taskiem never sees the rest of the mailbox.
2. **Taskiem receives the mail itself.** A new `mail` role is an SMTP server for the mail domain's MX record, accepting mail only for known addresses (others are refused at `RCPT`, not bounced later). It is the same reasoning as running ClamAV ourselves ([0037](0037-forms.md) §6): message bodies, invoices and bank alerts never pass through a mail company, which would be a new data processor (NDPA).
   - Its job is narrow: SMTP with STARTTLS, a size limit, and recording SPF, DKIM and DMARC results. It does not filter spam by content; most mail arrives already filtered by the mailbox that forwards it.
   - Like the form host, it writes received messages to an inbox table through narrow database functions, and the api role drains them into runs. A message is acknowledged (`250`) only once it is stored.
3. **The parsing is provider-neutral:** raw RFC 5322 in, `trigger.body` out. An HTTP endpoint that takes raw MIME from an inbound-mail service (Postmark, Mailgun, SendGrid and SES can all deliver raw) can be added later for an operator who cannot run port 25, without changing workflows.
4. **What a run sees** is `trigger.body`:
   - `from`, `to`, `cc`, `reply_to`, `subject`, `date`, `message_id`;
   - `text` (the plain text, or text made from the HTML), `latest` (the text without quoted earlier messages) and `html`;
   - `attachments`, as file references;
   - `forwarded_by` when a forwarding mailbox can be told apart from the original sender.

   Next to it, `trigger.email` holds `{address, received_at, auth: {spf, dkim, dmarc, aligned}}`. Senders, recipients, subject and bodies are sealed as personal data at run start (declared paths, not only detection).
5. **Attachments are stored like form files.**
   - They are scanned by ClamAV and stored in the tenant's private bucket. JPEG, PNG and PDF only, 25 MB each, 10 per message.
   - Anything else is listed under `skipped` with the reason, not stored.
   - Runs hold references only; a step gets a short-lived link through a `file_url` action.
   - Without a scanner, a workflow that keeps attachments cannot be deployed.
6. **Safety:**
   - Deduplication by Message-ID, else a hash of the message.
   - A loop guard refuses automatic replies (`Auto-Submitted`, `Precedence: bulk/auto_reply`) and Taskiem's own outgoing mail.
   - Per-address and per-tenant rates through the existing ingest gate.
   - An optional `senders` list, trusted only when DKIM is aligned with the sender's domain, so a forged From cannot pass it.
7. **Forwarding confirmations are surfaced, not run.** Gmail and Outlook confirm a forwarding address by mailing it a code or link. Mail from their confirmation senders does not start a run. Taskiem shows the code on the trigger's screen and sends it to the owner's WhatsApp or Telegram, so setting up forwarding never needs a developer.

## Alternatives considered

- **Gmail push or polling with restricted scopes.** Not chosen for now: it costs a yearly assessment, works for Gmail only, and is more access than the job needs. It stays possible later as a Gmail trigger for businesses that want their whole mailbox connected.
- **An inbound-mail service as the receiver.** Simpler to operate (no MX record, no port 25), but a new processor for every message's content. Kept as the fallback adapter in decision 3.
- **IMAP polling of the owner's mailbox.** It needs the mailbox password or an app password (prohibited on owners' screens by the [no-technical-steps rule](../spec/no-technical-steps.md#the-rule)) and polls every account forever.

## Consequences

- A new `mail` role, an MX record and inbound port 25 in the VM kit and the operator runbook. Staging needs its own mail subdomain.
- The OpenAPI `Trigger.type` enum, which already lags (no `storefront` or `form`), is brought up to date while adding `email`.
- Forms' file storage becomes shared with email; `form_files` is generalised, or a sibling table is added if that is cleaner.
- Replying from a workflow (threaded by `In-Reply-To`) is a natural next step, but not part of this decision.
