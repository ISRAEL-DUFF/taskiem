# Email triggers

A workflow can start when an email arrives: an alert from your bank, an invoice from a supplier, an order a website emails you. The workflow gets its own address, such as `k3x9q2m7a1bz@in.taskiem.app`. Mail sent there, or forwarded there by a filter in your inbox, starts it. Taskiem never signs in to your mailbox and sees only the mail you forward. The design is [decision 0038](decisions/0038-inbound-email.md).

## Setting one up

1. In the workflow's **When it runs** tab, choose **When an email comes in**.
2. Optionally, list who it takes mail from (**Only take mail from**): addresses (`alerts@yourbank.com`) or domains (`yourbank.com`, which includes `mail.yourbank.com`). Leave it empty to take mail from anyone.
3. Tick **Keep attachments** if the workflow needs the files. Pictures (JPEG, PNG) and PDFs are kept, up to 10 an email and 25 MB each.
4. Publish. The tab then shows the workflow's address, with **Copy**. It stays the same when you change the workflow later. **Get a new address** retires it, for example when it gets spam, and mail to the old address is then refused.
5. Send or forward mail there. **Try it with a sample email** runs the workflow with a sample message, so you can see what a run gets before setting anything up.

The tab lists the last ten messages that arrived and what came of each: the run it started, or why it did not start. Until the first one arrives it shows **Waiting for your first email…**, and the message appears there as soon as it comes in.

### Forwarding from Gmail

Gmail forwards only to an address it has confirmed:

1. In Gmail's settings, open **Forwarding and POP/IMAP**, choose **Add a forwarding address**, and enter the workflow's address.
2. Gmail mails a confirmation code to that address. Taskiem does not run the workflow for it. Instead the code appears in the **When it runs** tab, and the business's owners get it on the WhatsApp or Telegram they linked. Enter it in Gmail.
3. Leave forwarding itself off. Instead, search for the mail you want (from your bank, say), choose **Create filter**, tick **Forward it to**, and pick the address. Only matching mail is forwarded.

Outlook needs no confirmation: add a rule (**Settings → Mail → Rules**) with a condition (such as **From**) and the action **Forward to**. A work account may not be allowed to forward outside the company; its administrator decides that.

The web app shows both guides, step by step, under **Forward mail here from your inbox**.

## What a run gets

`trigger.body` is the message:

| Field | What it is |
| --- | --- |
| `from` | `{address, name}` of the sender |
| `to`, `cc`, `reply_to` | Lists of `{address, name}` |
| `subject` | The subject |
| `date` | When it was sent (RFC 3339), when the message says |
| `message_id` | Its Message-ID |
| `in_reply_to` | The Message-ID it answers (`In-Reply-To`), or `""` |
| `references` | The thread's earlier Message-IDs (`References`), oldest first, at most 20 |
| `text` | The plain text; for mail with only HTML, the HTML's words |
| `latest` | The text without the quoted earlier messages of a reply ("On … wrote:", Outlook's header block, `>` lines) |
| `html` | The HTML, as sent |
| `attachments` | Files kept: `{id, name, type, size, sha256}` each |
| `skipped` | Files not kept, `{name, type, size, reason}` each: another type, too large, past ten, attachments not kept, or failed the malware check |
| `forwarded_by` | The mailbox that forwarded it by a filter, when the provider says (Gmail does) |
| `truncated` | `true` when `text` (100,000 characters) or `html` (200,000) was cut |

`trigger.email` is `{address, received_at, auth}`, with `tag` when the mail came to `<address>+<tag>@…` (letters, digits and hyphens, at most 40), and `thread` and `matched_by` when it belongs to a [thread](#threads). `auth` is what the receiving server found: `spf` (the sender's server check), `dkim` (the domains whose signatures verified), `dmarc` (the From domain's published policy) and `aligned`. `aligned` is true when a signature or the SPF check belongs to the From address's domain, so the sender is not forged.

The sender and recipients (`email` and `name`), the subject, the text, the HTML and the files' names are sealed as personal data at the start of the run, like a form's personal answers. Steps read them as usual. The run's history and the web app show them masked to members without `pii.reveal`.

A step gets a file through the `email` connector's **Get a link to an attachment** (`email@1 file_url`, input `{file: trigger.body.attachments[0].id}`): a link that works for 15 minutes (at most 60). The file's content never goes into the run. Members with `pii.reveal` download one with `GET /v1/email-files/{id}` (audited).

## Threads

A workflow can carry an email conversation per ticket or order: it emails the customer, and their reply comes back to the run that waits for it, instead of starting a new one. The design is [decision 0063](decisions/0063-email-threads.md); the [ticket templates](guide/tickets.md) are built on it ([spec](spec/tickets.md)).

1. Tick **Replies continue the conversation** in the **When it runs** tab (`threads: true` in the email trigger's `config`).
2. Give each conversation a key, such as `T-1042`: letters, digits and single hyphens, up to 24 characters.
3. Email the customer with the `email` connector's **Email a customer in a conversation** (`email@1 send_in_thread`): `to`, `subject`, `message`, `thread`, and optionally `in_reply_to` (the customer's `message_id`), `references` and `address` (below).
4. Wait for the answer with a `signal` step on event `email.reply`, correlated by the key (`=steps.ack.output.thread`). Its output is the reply: `body` is shaped as `trigger.body` (so `body.latest` is what they wrote), and `email` as `trigger.email`.

The email comes from Taskiem's sending address in the business's name ("Ada's Kitchen via Taskiem"), like every message Taskiem sends a customer, and is counted and limited the same way. It carries:

- **Reply-To:** the workflow's address with the conversation's reply tag, `k3x9q2m7a1bz+t-1042-q7m2x9k4a8bz@in.taskiem.app`. The tag ends with 12 characters Taskiem makes from a secret of the address, so nobody can guess another conversation's address;
- **Message-ID:** one Taskiem makes, given in the step's output (`message_id`) and the same if the step is retried;
- **In-Reply-To and References:** the customer's message it answers, else the last email sent in the conversation, so mail apps show one conversation;
- **the subject** with `[#T-1042]` before it, once.

**Sending from a helpdesk address.** A workflow without an email trigger of its own (a department's reply form, a ticket opened from a web form or WhatsApp, a scheduled follow-up) can email a customer in a conversation through another workflow's address: give `address` the threaded workflow's Taskiem address (`k3x9q2m7a1bz@in.taskiem.app`, as its `trigger.email.address` reads, a tagged one also does) or its workflow id. That workflow must be of the same business, in the same environment as the run, with **Replies continue the conversation** ticked and its address live; anything else fails the step for good, saying why, and sends nothing. The email then belongs to that address in every way: its Reply-To, its Message-ID and the conversation's records, so the customer's reply goes to that workflow, never to the sender. Since no run of that workflow waits for it, the reply starts a run of it carrying `trigger.email.thread`, as the [ticket templates](guide/tickets.md) want: the helpdesk workflow appends it to the ticket. A run of another workflow that is still going never holds a reply back.

A reply is matched to its conversation by, in order: its verified reply tag; its `In-Reply-To` or `References` naming an email this workflow sent; or a `[#T-1042]` in its subject, only when the sender is proven (`auth.aligned`) and is someone the workflow emailed in that conversation. Then:

- a run of the workflow waiting on `email.reply` for that key gets it, and nothing starts. The tab lists the message as **a reply, given to the conversation's run**. Runs of other workflows never get it, even with the same key;
- a run of the workflow that emailed in the conversation and is still going, but is not waiting yet (it is between steps), gets it when it waits: the message is looked at again every 5 minutes, for up to 7 days;
- otherwise (the conversation's run ended) a new run starts with `trigger.email.thread` set, so the workflow can reopen the ticket.

The reply's sender, recipients, subject and text are sealed before the run gets them, as a trigger's are. A message matched to nothing starts a run as usual. Without **Replies continue the conversation**, every message starts a run as before; the tag and the threading headers are only shown to it.

What is kept per email sent in a conversation is its key, its Message-ID, the run that sent it and the recipient's personal-data pseudonym (never their address). It is deleted with that run at the end of its retention; erasing the recipient removes their pseudonym.

## What does not start a run

Each of these is recorded with its reason and listed in the tab:

- **Mail from a sender not on the list.** The list trusts a sender only when the message proves it (`aligned`), so a forged From does not pass.
- **Automatic mail:** out-of-office replies (to a conversation too: they wake no run) (`Auto-Submitted`, `Precedence: auto_reply`), delivery reports (no return address), and Taskiem's own mail (its sending address, or a message carrying `X-Taskiem-Run`). This ends loops between a workflow that sends email and an address that runs it.
- **Forwarding confirmations** from Gmail, Zoho Mail, Yahoo Mail and Outlook, when the message proves it comes from them. They are shown, not run (see above).
- **A message that cannot be read** as email.
- **The plan's run limits**, as for any run.

The same message delivered twice starts one run (by its Message-ID, or the whole message when it has none). One address takes at most 60 messages an hour, and one business 300. Past that, the sending server is asked to try again later, and it does, for days.

## For operators

The `mail` role (`serve --role mail`, or inside `all` with `TASKIEM_MAIL_ENABLED=true`) is an SMTP server for the mail domain. It:

- takes mail only for addresses of live email triggers, refusing anything else at `RCPT` (`550`), so nothing is accepted and bounced later; a connection that names five unknown addresses is closed with `421` (the sender retries later), so one connection cannot look up addresses without end;
- checks SPF, DKIM and DMARC and records the results with the message, but filters nothing by content: most mail comes already filtered by the mailbox that forwards it;
- offers STARTTLS and takes messages up to 25 MB, with at most 200 connections at once;
- like the public host, connects as `taskiem_public`, holds only the public key, and leaves messages sealed in the inbox, which the api role drains every 2 seconds. A message is acknowledged (`250`) only once it is stored.

To set it up ([settings](operations.md#configuration)):

1. Pick a mail domain that is a subdomain of yours, such as `in.example.com`. Set `TASKIEM_MAIL_DOMAIN` on the `mail` and `api` roles.
2. Point an MX record for it at the `mail` role's host: `in.example.com. MX 10 mx.in.example.com.`, and an A (and AAAA) record for `mx.in.example.com` (`TASKIEM_MAIL_HOSTNAME`). Make the host's reverse DNS name match it if your provider lets you: some senders check it.
3. Open port 25 inbound and map it to `TASKIEM_MAIL_LISTEN` (`:2525`). Some clouds block port 25 until asked; only inbound is needed, since Taskiem sends nothing from this server.
4. Optionally give it a certificate (`TASKIEM_MAIL_TLS_CERT`, `TASKIEM_MAIL_TLS_KEY`); otherwise it makes a self-signed one, which sending servers accept.
5. Set `TASKIEM_CLAMD_ADDR` on the `api` role, as for forms, so attachments can be kept. Without it, a workflow that keeps attachments cannot be published.

On the VM kit, `taskiem-vps setup` does all of this from one answer ([one VM](vm.md#email-triggers)); the [staging runbook](runbooks/staging.md#9-email-triggers) walks through it. On Kubernetes, the chart's `mail` values do ([Kubernetes](kubernetes.md#email-triggers)).

Prometheus counts SMTP outcomes as `taskiem_ingest_deliveries_total{kind="email_smtp", result="accepted|unknown_recipient|deferred"}` and what came of each message as `{kind="email", result="started|refused|confirmation"}`.

Check it from anywhere with `swaks --to <address> --server mx.in.example.com`, or by sending an ordinary email.
