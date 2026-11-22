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

- **To:** a person ([device fields §2](spec/device-fields.md#2-who-is-asked)): a phone or email from the run, `role` for members of a role, or `member` for one member (their id, or an expression giving one), who find it under To fill in in the app and on the web (and are alerted), and are asked in WhatsApp or Telegram too where they are linked; or `link`, the person working a business link (its id, the linked business's, or an expression giving one): the linked business's owner finds it under To fill in on their side and answers it through the link ([device fields §15](spec/device-fields.md#15-as-built-d1)). `"starter"` is refused until made tools M4.
- **The link** goes by the first channel that reaches the person, through the platform's message outbox, and again at each `remind`. It is a one-time link: answered, it is used up; the run cancelled, it is withdrawn.
- **The step's output** is the answers, with `respondent` and `device` (below). Not answered by the `timeout` (default 7 days): `on_timeout` `fail` fails the step (its `on_error` can handle it); `continue` completes it with `timed_out: true`.

## Device fields

A form step may ask the person's device ([the app as a node](spec/device-fields.md), D0 built): a field of `type` `location`, `photo`, `scan` or `signature`, each with `why` (one sentence the person reads before the device is asked), and `require_source` (`device` on a location, `camera` on a photo), `max_accuracy_m` and `max_age` (a location), `formats` (a scan) and `allow_typed` (a location or a scan; default true). At most one of each in a question; a signature cannot be required of a phone or an email. They are asked by form steps only, not by a form that starts a workflow.

```json
"where": { "type": "location", "why": "so the delivery is recorded where it was handed over", "max_accuracy_m": 100 }
```

Each answer is `{state, source, ...}`: `state` is `answered`, `refused` or `unavailable` (with a `reason`), or `unverified` when its source is weaker than `require_source`; `source` is set by the server from where the answer came (a location read at the tap is `device`, typed `typed`, sent in a chat `sent`), never from the answer. A location is `{lat, lng, accuracy_m, at}` (with `coarse: true` past `max_accuracy_m`) or `{address}`. **A location is personal data**: sealed as `location`, masked to about 100 m wherever masked values are shown, exactly only with `pii.reveal`, and never given to an AI step (it is told only that one was answered, and how). Photos and signatures are files, below.

**Where they are asked** (D1): the app and the web answer through `POST /v1/form-requests/{id}/answers`, a location and a scan as values and a photo or a signature as a part of a multipart form. The phone app reads the location only at the tap and takes the photo with the camera, and keeps an answer made offline to send later (`online: false`); the web asks the browser's own location prompt, and takes a photo from the camera where the browser offers it or as a file (`sent`), and a signature on a pad. In WhatsApp and Telegram the person sends their location and a photo with the chat's own messages (`sent`); a signature is unavailable there. A field that requires the device's reading or the camera, answered in a chat, is held and the person offered the app's link or **Send as it is**, which sends it `unverified`. The form host's one-time page does not ask a device yet and sends such fields `unavailable`.

A photo or a signature is always a file uploaded with that answer: in `answers` it can only be `{state: refused}` or `{state: unavailable}`, so an earlier photo's reference cannot be sent again as a new one, and `riders@1` `record_proof` takes only a photo answered to its own run. A chat answers only questions put to the person themselves (a role they hold, them by name, or through a link still active), never another role's because they are the owner; what each person sends is their own draft until it is whole, and a form step is answered once: a second answer arriving together is refused ("the form is no longer taking answers") and its files discarded. A question put through a link is listed, read and answered only while the link is active, and does not show the asking business's run. What a chat held towards a question goes when it is answered (anywhere), withdrawn or expires.

**Every answer** (a form step's, and a form's in `trigger.form`) carries `device: {app, version, online, at, received_at}`: the app that answered (`app`, `web` or `link`), its version, whether it was online, the time by its clock, and the time the server received it, which is the one to rely on; `clock_differs: true` when the two differ by more than five minutes beyond what answering offline explains.

## Files

Files go to the organisation's private Xenibase storage ([media](media.md)), never into run history. A submission and its files are deleted with the run's data when the workflow's retention purges it, and when the respondent is erased ([privacy](privacy.md#erasure)); the api removes the stored files within a minute. A submission that starts no run (refused, or not started because a plan limit was reached) is kept as long as its run would have been, then deleted the same way, and a refused submission's files are removed at once. A file field's value is `{id, name, type, size, sha256}`; a step reads the content through `form@1` `file_url`, a link that expires within an hour. JPEG, PNG and PDF are taken, checked by their content; photos are cleaned of their metadata; at most 25 MB each and 10 a submission.

Every file is scanned for malware before the submission is accepted. The scanner is the operator's: a ClamAV daemon at `TASKIEM_CLAMD_ADDR` (host:port), with no network of its own. Without one, forms that ask for files cannot be deployed. (Which scanner Taskiem runs is an S0 decision.)

## Storefront forms and their fields

A storefront's forms are forms too ([fronts](spec/fronts.md), decision 0059): each is an action of a front, with its own questions written the same way (a JSON Schema object with `x-form` hints), its answers reaching the run as `trigger.answers` apart from Taskiem's record of the submission (`trigger.submission`). Beside the field types above, a storefront form draws on its front's list with one of four fields, each checked again on the server and, where something is held, in the database:

| Field | Used by | What the customer does | What is held |
| --- | --- | --- | --- |
| **basket** | Order now (Shop, Food) | Picks items and how many, with a Food dish's options | Nothing: prices are checked again before the run, stock on the catalogue only |
| **time** | Book, Book the space | Picks a service and a slot, or a space by the day or the hour on its list's own calendar | A claim on the calendar, one party at a time with setup time for a space |
| **dates and quantity** | Hire now | Picks an item, a first and a last day, and how many | A claim on the item for those days, refused beyond its count |
| **quantity against a count** | Buy tickets, Enrol | Picks an event or a class and how many | A claim on the item while the order lives, refused beyond its tickets or places |

Claims are one table (`storefront_claims`), taken by the public host with the submission in one transaction under the list's lock, so two customers cannot both take the last canopy or seat. A question form with questions of its own (a front's blank form) asks only those. A storefront form never asks for a file. See [storefronts](storefronts.md) for the kinds.

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
| `GET /v1/form-requests`, `GET /v1/form-requests/{id}`, `POST /v1/form-requests/{id}/answers` | Form steps for you, your roles, or put to you through a business link (`from`); answers take `device` (`app`, `version`, `online`, `at`), and photos and signatures as a multipart form |

## How it is kept safe

- The form host is its own name, with no cookies and a strict Content-Security-Policy; it runs as the `public` role, which holds no vault key. It seals what it receives with the public key (`TASKIEM_PUBLIC_KEY`); the api role opens it, checks the answers again, seals personal answers under the respondent's subject and starts the run.
- Bots meet a honeypot, a minimum time to fill in and signed one-time form tokens; addresses are limited per IP hash, and each form across every address (500 an hour). A form over its limit is treated as under attack: for the next hour it asks each person for a code sent to their phone before its questions, and the business's owners are alerted.
- A code for a verified form is asked for with the page's token, like any other step of the form, and is limited four ways (migration 00444): one address on one form, 3 in 10 minutes; one number or email across every form, 6 a day (counted by a keyed hash, besides the [sender's own limits](messages.md#how-a-message-travels)); one client address across every form, 3 in 10 minutes and 10 a day (and six an hour after a burst of four on each form host process); and one form, whose codes not yet confirmed in the last 10 minutes are counted against 20 plus twice the codes confirmed there in the last hour. A form over that is not locked: people who have not asked there in the last 10 minutes still get a code and the owners are alerted as for a form over its submission limit, while a connection with a code waiting is told to wait; only at three times the ceiling is everyone refused, for at most 10 minutes.
- A body with files is refused before its files are read. The page's form sends its token and the honeypot first; the form host reads only that far (at most 16 KB) and refuses at once a body that starts with a file or anything else large, has the honeypot filled in, or has a token that does not verify, leaving the rest unread. Before that, an address may send such bodies 120 times an hour (after a burst of 30), at most three at once, and a form takes at most twelve at once (`publicweb.FormUploadsPerIP`, `FormUploadsAtOncePerIP`, `FormUploadsAtOncePerForm`, per public host process). Anything that posts files to a form itself must send `token` and `website` before them.
- One-time links carry 24 random bytes and are stored only as a hash; form steps' links are derived from the request, so nothing secret is stored at all.

## Asking the person who started the run

A form step `to: "starter"` (made tools M4, [decision 0070](decisions/0070-made-tools.md)) asks whoever started the run (a member; an API key as its owner) in the chat they started it from: WhatsApp, Telegram, Slack or the web's Taskiem conversation. A question of one choice (a string with an `enum`, or `x-saved: "place"`, which offers the business's saved places) is asked there with its choices as buttons (three or fewer) or as words to reply with; pressing one (the button names the choice by what it says, so a list that changed meanwhile never answers another), or replying with exactly one of them, answers it. A typed reply answers only a question put to that person as choices in that very chat, the newest first, and never a bare number or one of Taskiem's own command words; anything else is read as an ordinary message, so a waiting question never takes a command. A subflow's question goes to whoever started the run at the top of its chain, and a resumed run's to whoever started the original. Any other question is answered as a member's form step is: in the app or on the web under To fill in, and locations and photos in WhatsApp and Telegram. A run started in the app, on the web's Run or by the API asks there, under To fill in. A run nobody started by hand (a schedule, an event) has nobody to ask: its request says so, and the step waits for its timeout and `on_timeout`.
