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
| `text` | The plain text; for mail with only HTML, the HTML's words |
| `latest` | The text without the quoted earlier messages of a reply ("On … wrote:", Outlook's header block, `>` lines) |
| `html` | The HTML, as sent |
| `attachments` | Files kept: `{id, name, type, size, sha256}` each |
| `skipped` | Files not kept, `{name, type, size, reason}` each: another type, too large, past ten, attachments not kept, or failed the malware check |
| `forwarded_by` | The mailbox that forwarded it by a filter, when the provider says (Gmail does) |
| `truncated` | `true` when `text` (100,000 characters) or `html` (200,000) was cut |

`trigger.email` is `{address, received_at, auth}`, where `auth` is what the receiving server found: `spf` (the sender's server check), `dkim` (the domains whose signatures verified), `dmarc` (the From domain's published policy) and `aligned`. `aligned` is true when a signature or the SPF check belongs to the From address's domain, so the sender is not forged.

The sender and recipients (`email` and `name`), the subject, the text, the HTML and the files' names are sealed as personal data at the start of the run, like a form's personal answers. Steps read them as usual. The run's history and the web app show them masked to members without `pii.reveal`.

A step gets a file through the `email` connector's **Get a link to an attachment** (`email@1 file_url`, input `{file: trigger.body.attachments[0].id}`): a link that works for 15 minutes (at most 60). The file's content never goes into the run. Members with `pii.reveal` download one with `GET /v1/email-files/{id}` (audited).

## What does not start a run

Each of these is recorded with its reason and listed in the tab:

- **Mail from a sender not on the list.** The list trusts a sender only when the message proves it (`aligned`), so a forged From does not pass.
- **Automatic mail:** out-of-office replies (`Auto-Submitted`, `Precedence: auto_reply`), delivery reports (no return address), and Taskiem's own mail (its sending address, or a message carrying `X-Taskiem-Run`). This ends loops between a workflow that sends email and an address that runs it.
- **Forwarding confirmations** from Gmail, Zoho Mail, Yahoo Mail and Outlook, when the message proves it comes from them. They are shown, not run (see above).
- **A message that cannot be read** as email.
- **The plan's run limits**, as for any run.

The same message delivered twice starts one run (by its Message-ID, or the whole message when it has none). One address takes at most 60 messages an hour, and one business 300. Past that, the sending server is asked to try again later, and it does, for days.

## For operators

The `mail` role (`serve --role mail`, or inside `all` when `TASKIEM_MAIL_DOMAIN` and `TASKIEM_PUBLIC_KEY` are set) is an SMTP server for the mail domain. It:

- takes mail only for addresses of live email triggers, refusing anything else at `RCPT` (`550`), so nothing is accepted and bounced later;
- checks SPF, DKIM and DMARC and records the results with the message, but filters nothing by content: most mail comes already filtered by the mailbox that forwards it;
- offers STARTTLS and takes messages up to 25 MB, with at most 200 connections at once;
- like the public host, connects as `taskiem_public`, holds only the public key, and leaves messages sealed in the inbox, which the api role drains every 2 seconds. A message is acknowledged (`250`) only once it is stored.

To set it up ([settings](operations.md#configuration)):

1. Pick a mail domain that is a subdomain of yours, such as `in.example.com`. Set `TASKIEM_MAIL_DOMAIN` on the `mail` and `api` roles.
2. Point an MX record for it at the `mail` role's host: `in.example.com. MX 10 mx.in.example.com.`, and an A (and AAAA) record for `mx.in.example.com` (`TASKIEM_MAIL_HOSTNAME`). Make the host's reverse DNS name match it if your provider lets you: some senders check it.
3. Open port 25 inbound and map it to `TASKIEM_MAIL_LISTEN` (`:2525`). Some clouds block port 25 until asked; only inbound is needed, since Taskiem sends nothing from this server.
4. Optionally give it a certificate (`TASKIEM_MAIL_TLS_CERT`, `TASKIEM_MAIL_TLS_KEY`); otherwise it makes a self-signed one, which sending servers accept.
5. Set `TASKIEM_CLAMD_ADDR` on the `api` role, as for forms, so attachments can be kept. Without it, a workflow that keeps attachments cannot be published.

On the VM kit, the `taskiem-mail` service (Compose profile `mail`) does this with `public.env` and a `mail.env` holding `TASKIEM_MAIL_DOMAIN`. Set `TASKIEM_MAIL_DOMAIN` in `taskiem.env` too.

Check it from anywhere with `swaks --to <address> --server mx.in.example.com`, or by sending an ordinary email.
