# Forms

A **form** puts a page in front of a workflow: people fill it in at a link (or on the organisation's own website, or in the Taskiem app), and each submission starts a run. A **form step** asks someone for answers in the middle of a run: they get a one-time link by SMS or email, and the run waits for them. The design is [the forms spec](spec/forms.md); this page is what is built (Phase 5, S7).

The form is part of the workflow: its questions are the workflow's inputs schema, so a form, a manual run and an API call ask the same questions with the same checks. It is versioned, reviewed and deployed with the workflow.

## A form that starts a workflow

```json
"trigger": { "type": "form", "config": {
  "title": "Apply for a loan",
  "description": "Takes about 5 minutes.",
  "access": "public",
  "verify": "phone",
  "pages": [
    { "title": "About you", "fields": ["full_name", "phone", "bvn"] },
    { "title": "Your employer", "fields": ["employer", "payslip"], "show_if": "answers.employed == true" }
  ],
  "after": { "mode": "message", "message": "Thank you. We will reply within a day.", "status": true }
} }
```

| Setting | |
| --- | --- |
| `title`, `description` | Shown at the top. The title is checked like storefront names: no banks, payment providers or government agencies |
| `access` | `public`: anyone with the link. `link`: only one-time links the organisation makes. `members`: signed-in members, in the app (`roles` limits it) |
| `verify` | `phone` or `email`: a 6-digit code sent there before submitting (five tries, 10 minutes) |
| `pages` | Groups of fields; `show_if` (over earlier pages' answers) hides a page, whose fields are then not asked. With no pages, the form is one page |
| `after` | `message` (default), `redirect` (only to one of the organisation's verified websites), or `result` (the run's own message, waiting up to 30 seconds). `status: true` gives a status link. When the workflow messages a phone typed in the form (`notify@1` `message`), the page also offers **Get updates on WhatsApp / Telegram** ([updates](updates.md)), and a `redirect` waits for that |
| `once_per` | `phone`, `email` or `member`: one submission each |

**Fields** are the inputs schema, with display hints in `x-form`: `label`, `help`, `placeholder`, `widget` (`textarea`, `consent`), `prefill` (may be set from the link), `hidden`, `from` (`member.name`, `member.email` for members forms), `accept` and `max_mb` for files, `order`. Types: text, number, email, Nigerian phone (`format: phone-ng`), URL, date and time, choices (`enum`, or an array of `enum`), yes or no, consent, and files (`format: file`).

**Never asked.** A form is refused at deploy if it asks for a password, a PIN, a card number, a CVV, a one-time code or bank login details. BVN, NIN and account numbers must carry their `x-pii` category (so they are sealed at once), and only on a `link` or `members` form, or a public one with `verify`.

**Deploying** puts the form live at `https://forms.<preview domain>/f/<id>` (embeddable on the organisation's verified websites under `/embed/f/<id>`). The address stays the same across versions. Public forms need the owner's email confirmed. Forms that ask for files need a malware scanner on the deployment (below).

**The run** gets the answers as `trigger.body`, and `trigger.form` holds the submission's id and reference, when it was sent, the `respondent` (`kind`: `anonymous`, `verified`, `link` or `member`, with the verified phone or email, or the member's id), the `channel` and the consents agreed to. A members form's run is started by the member, so approvals and four-eyes rules know who asked.

## A form step

```json
{ "id": "ask_bank", "type": "form", "config": {
  "title": "Confirm your bank details",
  "to": { "phone": "=trigger.body.phone" },
  "via": ["sms", "email"],
  "verify": true,
  "schema": { "type": "object", "required": ["bank"], "properties": {
    "bank": { "type": "string" },
    "account_number": { "type": "string", "x-pii": "account_number" } } },
  "prefill": { "name": "=trigger.body.full_name" },
  "timeout": "72h",
  "remind": ["24h", "60h"],
  "on_timeout": "fail"
} }
```

- **To:** a phone or email from the run, or `role` for members of a role, who find it in the app (and are alerted).
- **The link** goes by the first channel that reaches the person, through the platform's message outbox, and again at each `remind`. It is a one-time link: answered, it is used up; the run cancelled, it is withdrawn.
- **The step's output** is the answers, with `respondent`. Not answered by the `timeout` (default 7 days): `on_timeout` `fail` fails the step (its `on_error` can handle it); `continue` completes it with `timed_out: true`.

## Files

Files go to the organisation's private Xenibase storage ([media](media.md)), never into run history. A file field's value is `{id, name, type, size, sha256}`; a step reads the content through `form@1` `file_url`, a link that expires within an hour. JPEG, PNG and PDF are taken, checked by their content; photos are cleaned of their metadata; at most 25 MB each and 10 a submission.

Every file is scanned for malware before the submission is accepted. The scanner is the operator's: a ClamAV daemon at `TASKIEM_CLAMD_ADDR` (host:port), with no network of its own. Without one, forms that ask for files cannot be deployed. (Which scanner Taskiem runs is an S0 decision.)

## The form connector

`form@1` (no connection) acts for the run's own organisation:

| Action | Does |
| --- | --- |
| `respond` | Sets the message the respondent sees after submitting (`after.mode: result`) and on their status link: "Your application is with our credit team" |
| `file_url` | A short-lived download link for a form file (`file`: its id) |

## In the web app

- **New form** on the Forms page (`workflow.edit`) makes a form and the workflow behind it: its title, a starter question or two, and what happens with each answer (Tell me on WhatsApp or by email, or just keep them). The editor opens on its questions; publishing it gives its link.
- **Forms** lists every form: its workflow and environment, link, status and submissions this week; pause, reopen or close it, set opening and closing times or a maximum number of submissions, and make one-time links for link forms (`form.links`, owners and admins).
- **Submissions** are masked like runs; `pii.reveal` shows them (audited), and file downloads are audited.
- **Members forms** and **form steps for a role** open in the app.

## API

| Endpoint | |
| --- | --- |
| `GET /v1/forms`, `PATCH /v1/forms/{id}` | The forms; pause, reopen, close, opening times, limit (`workflow.publish`) |
| `POST /v1/forms/{id}/links` | One-time links (`form.links`), open or bound to a phone or email; shown once |
| `GET /v1/form-submissions`, `GET /v1/form-submissions/{id}` | Submissions, masked; `?reveal=true` with `pii.reveal` |
| `GET /v1/form-files/{id}` | A file's download link (`pii.reveal`, audited) |
| `GET /v1/forms/{id}/view`, `POST /v1/forms/{id}/submissions` | A members form, and sending it |
| `GET /v1/form-requests`, `GET /v1/form-requests/{id}`, `POST /v1/form-requests/{id}/answers` | Form steps for your roles |

## How it is kept safe

- The form host is its own name, with no cookies and a strict Content-Security-Policy; it runs as the `public` role, which holds no vault key. It seals what it receives with the public key (`TASKIEM_PUBLIC_KEY`); the api role opens it, checks the answers again, seals personal answers under the respondent's subject and starts the run.
- Bots meet a honeypot, a minimum time to fill in and signed one-time form tokens; addresses are limited per IP hash.
- One-time links carry 24 random bytes and are stored only as a hash; form steps' links are derived from the request, so nothing secret is stored at all.
