# Forms: a hosted form that starts a workflow, or asks someone for input mid-run

Status: **draft for review** (proposed in [decision 0037](../decisions/0037-forms.md)). Nothing here is built. It answers open question Q10 of the [Xenibase plan](../xenibase-integration.md#5-open-questions) for general forms; [storefronts](storefronts.md) (decision 0036, also proposed) are the public-commerce case and share this spec's form host.

## 1. Summary

Any organisation can give a workflow a **form**: a page at a link, made from the workflow's inputs, that starts a run when someone submits it. A workflow can also **ask a person for input in the middle of a run**: a `form` step sends them a one-time link and waits for their answers, as an approval waits for a decision.

Typical uses:
- **Intake:** a client onboarding form, a loan or job application, a support request, an event registration.
- **Inside the organisation:** a leave request, an expense claim with a receipt, an IT access request, each followed by approvals.
- **Collecting from someone mid-process:** a KYC step that asks the customer for their ID and a selfie, a vendor's bank details for a payment, a manager's comments on a review.

The form is part of the workflow definition, so it is versioned, reviewed, deployed and synced to Git with it. It is not a separate form builder product.

## 2. Who it is for

| Person | Uses forms to |
| --- | --- |
| **A workflow builder** in an organisation | Put a front door on a workflow without a website, a form tool and glue between them |
| **A respondent outside the organisation** (a customer, an applicant, a vendor) | Fill in a form on a phone, often on a slow connection, without an account |
| **A member** of the organisation | Fill in internal forms signed in to Taskiem, with their name and email already known |
| **A partner** (embedding) | Later: give their own end users forms inside their product ([12](#12-phasing)) |

## 3. Goals and non-goals

**Goals:**
1. What people use n8n's and Zapier's hosted forms for, inside Taskiem's guarantees: durable runs, approvals, PII sealing, audit.
2. **One definition, checked once:** the form is the workflow's inputs schema plus layout. A submission is checked against the same schema as an API or manual run.
3. **Human input mid-run** as a first-class step, which most form tools cannot do.
4. **Works on cheap phones and 3G:** rendered on the server, usable without JavaScript.
5. **Cannot become a phishing kit.**

**Non-goals:**
- A survey tool: no scoring, quizzes, response charts or A/B tests.
- A website or page builder, or custom HTML, CSS or JavaScript in a form.
- Payments inside the form: a workflow sends a provider's payment link, as storefronts do.
- Collecting passwords, card details, PINs or one-time codes, ever ([7.2](#72-fields-a-form-never-collects)).

## 4. Concepts

| Term | Meaning |
| --- | --- |
| **Form trigger** | A workflow trigger of type `form`. Deploying the workflow puts its form live at a link |
| **Form step** | A step of type `form`: it sends a one-time link to someone and waits for their answers |
| **Fields** | The workflow's `inputs.schema` (for a trigger) or the step's `schema` (for a step), with display hints |
| **Pages** | An optional grouping of fields into steps, each shown only if its condition holds |
| **Access** | Who may open the form: `public`, `link`, or `members` ([6.4](#64-access)) |
| **Submission** | One completed form: recorded, checked, and turned into a run (trigger) or the step's output (step) |
| **Respondent** | Whoever fills it in: anonymous, verified by a code, or a signed-in member |

## 5. Journeys

### 5.1 Building a form

1. In the canvas, the builder sets the trigger to **Form**. The trigger panel becomes a form editor: fields (each one edits the inputs schema), pages, conditions, the thank-you message, and a live preview.
2. The AI builder can draft it ("a leave request form with dates, a reason and the manager's approval"), and `build` on WhatsApp or Telegram can too. The draft is validated like any workflow.
3. Deploying the workflow puts the form live. The panel shows its link, a QR code, and the embed code.
4. In a draft, **Preview** opens the form against the draft. A preview submission starts a test run, marked as one, which is not counted against the plan.

### 5.2 A respondent fills in a public form

1. They open the link (or the form on the organisation's website), see the organisation's name and logo, and a short notice of who receives the data and why.
2. They fill in a page, go to the next if there is one, attach a file if asked, and submit.
3. They see the thank-you message (or the workflow's answer, or are redirected; [6.6](#66-after-submitting)), and a reference.
4. The run starts. Nothing about the run's internals is shown to them.

### 5.3 A workflow asks someone mid-run

1. A KYC workflow reaches a `form` step: "ask the customer for their ID and a selfie", sent to the customer's phone number from the run's data.
2. Taskiem sends the customer an SMS (or an email, WhatsApp or Telegram message, by the step's settings) with a one-time link.
3. The customer opens it, verifies with the code sent to the same number if the step asks for it, fills it in and submits.
4. The run continues with their answers as the step's output. If nobody answers before the timeout, the step takes its `on_timeout` path.

### 5.4 An internal form

1. A staff member opens the leave request form in the Taskiem app (or from a link, which asks them to sign in).
2. Their name and email are filled in from their account and cannot be changed. They fill in dates and a reason.
3. The run starts with the member as its initiator, so the approval goes to their manager, and four-eyes rules know who asked.

## 6. Functional specification

### 6.1 Where the form lives in the definition

```json
"trigger": {
  "type": "form",
  "config": {
    "title": "Apply for a loan",
    "description": "Takes about 5 minutes. Have your BVN and a payslip ready.",
    "access": "public",
    "verify": "phone",
    "pages": [
      { "title": "About you", "fields": ["full_name", "phone", "email", "bvn"] },
      { "title": "The loan", "fields": ["amount_naira", "purpose", "employed"] },
      { "title": "Your employer", "fields": ["employer", "payslip"], "show_if": "answers.employed == true" }
    ],
    "after": { "mode": "message", "message": "Thank you. We will reply within a day." }
  }
},
"inputs": { "schema": { "type": "object", "required": ["full_name", "phone", "amount_naira"], "properties": {
  "full_name": { "type": "string", "title": "Full name", "maxLength": 100 },
  "phone": { "type": "string", "format": "phone-ng", "x-pii": "phone" },
  "bvn": { "type": "string", "pattern": "^[0-9]{11}$", "x-pii": "bvn", "x-form": { "help": "11 digits" } },
  "amount_naira": { "type": "integer", "minimum": 10000 },
  "payslip": { "type": "string", "format": "file", "x-form": { "accept": ["application/pdf", "image/*"], "max_mb": 5 } }
} } }
```

- **Fields are the inputs schema**, so a form, a manual run, an API call and a WhatsApp Flow ask the same questions with the same checks. Display hints live in `x-form` (label, help, placeholder, widget, prefill). `title` is the label when no hint gives one.
- **Pages** only group and order fields. With no `pages`, the form is one page in schema order.
- **Conditions** (`show_if`) are CEL expressions over the answers given so far, as elsewhere in wd/v1. A field on a page that is not shown is not required and not sent.
- **Validation at deploy:** every schema field is on exactly one page (or hidden), conditions compile and only read earlier pages, and the field rules of [7.2](#72-fields-a-form-never-collects) hold.

### 6.2 Field types

| Type | Schema | Shown as |
| --- | --- | --- |
| Short text, long text | `string` (`x-form.widget: textarea` for long) | Input, text area |
| Number, money | `integer` / `number`; `x-form.unit: naira` | Numeric keyboard; naira shown with separators, the run receives kobo when the schema asks for kobo |
| Email, phone, URL | `format: email`, `phone-ng`, `uri` | Checked as typed; Nigerian phone numbers normalised to `+234` |
| Date, time, date and time | `format: date`, `time`, `date-time` | Native pickers, in the organisation's time zone |
| Choice, several choices | `enum`, or an `array` of `enum` | Radio buttons or a list; checkboxes |
| Choice from a data table | `x-form.options_from: {table, label, value}` | A list read from a [data table](../data-tables.md) when the form opens (up to 500 rows; a search box above 20) |
| Yes or no, consent | `boolean`; `x-form.widget: consent` with the text to agree to | A checkbox; consent records the text's hash and the time |
| File | `format: file` | An upload ([6.5](#65-files)) |
| Hidden | `x-form.hidden: true` | Not shown; set from the link (`prefill`) or by the step |
| Text block | `x-form.blocks` on a page | A heading or paragraph between fields, not an input |

Nested objects and repeating groups ("add another dependant") are v2.

### 6.3 Prefill

- **From the link:** fields with `x-form.prefill: true` can be set by query parameters (`?plan=gold`). Nothing else can, so a link cannot put words in the respondent's mouth. Hidden prefilled fields are how a campaign or a source is passed.
- **From the run (form step):** the step's `prefill` expressions fill fields from earlier steps, for example the customer's name and the loan amount to confirm.
- **From the account (members):** fields with `x-form.from: member.name` or `member.email` are filled and locked.

### 6.4 Access

| Access | Who can open it | Where it is served | Respondent in the run |
| --- | --- | --- | --- |
| `public` | Anyone with the link | The form host ([8.3](#83-the-form-host)) | Anonymous, or verified by a code (`verify: phone` or `email`) |
| `link` | Only holders of a one-time link minted for them (by the API, a bulk list, or a form step) | The form host | The person the link was made for, plus anything they verified |
| `members` | Signed-in members of the organisation, optionally limited to roles (`roles: ["staff"]`) | The Taskiem app, under its normal session | The member, as the run's initiator |

- **Verification by code** sends a 6-digit code by SMS or email, five attempts, valid 10 minutes, and records the verified address as `respondent.phone` or `respondent.email`. SMS codes are counted against the plan's SMS allowance.
- **One-time links** carry 24 random bytes, are stored only as a hash, work once (a submission uses them up; opening does not), and expire (7 days by default, at most 30). A link can be bound to a person's phone or email, and then also asks for the code sent there.
- **Limits per respondent:** `once_per: phone | email | member` refuses a second submission from the same verified respondent.
- **Open and close:** a form can be paused, given an opening and closing time, or a maximum number of submissions. A closed form shows its closed message.

No Xenibase auth and no new account system: members use Taskiem's own sign-in, outsiders use one-time links or codes. (This is the answer to Q10 for general forms.)

### 6.5 Files

- **Where they go:** Xenibase storage, in the organisation's Xenibase project, in its private bucket, reached only through signed URLs ([media](../media.md#files-from-forms)).
- **Limits:** the field's `accept` types and `max_mb` (default 10 MB, at most 25), and at most 10 files a submission. The type is checked by the file's content, not its name.
- **Scanning:** every file is scanned for malware before the submission is accepted. A file that fails is refused and the respondent is told. The scanner runs in its own sandbox, with no network.
- **Images:** metadata (including location) is removed. Large photos are scaled down to 2,000 pixels.
- **In the run:** a file field's value is a reference: `{id, name, type, size, sha256}`. Steps read the content through a platform action that issues a short-lived signed URL. Contents never go into run history, logs or AI prompts.
- **People** see and download files from the run or the submission in the app, with `pii.read`, and each download is audited.
- **Retention:** files are deleted with the run's data, by the workflow's retention.

### 6.6 After submitting

| `after.mode` | The respondent sees |
| --- | --- |
| `message` (default) | A thank-you message (Markdown, no links outside the organisation's verified domains) and the reference |
| `redirect` | A redirect to a URL on one of the organisation's verified domains. Never anywhere else, so a form cannot become an open redirect |
| `result` | The run's answer: the page waits up to 30 seconds for a `respond` step (the synchronous mode of spec 8.2) and shows its message, then falls back to the thank-you message and a status link |

- **The status link** (optional, `after.status: true`): an unguessable link to a page that shows the latest message the workflow has set with `respond` ("Your application is with our credit team"). It never shows the run's steps or data.
- **A copy for the respondent** (optional): an email of their answers to a verified email address, without files and with sensitive fields masked.

### 6.7 The form step

```json
{ "id": "ask_kyc", "type": "form", "config": {
  "title": "Verify your identity",
  "to": { "phone": "{{steps.lookup.output.phone}}" },
  "via": ["sms", "whatsapp"],
  "verify": true,
  "schema": { "type": "object", "required": ["id_type", "id_front", "selfie"], "properties": { } },
  "prefill": { "full_name": "{{trigger.body.full_name}}" },
  "timeout": "72h",
  "remind": ["24h", "60h"],
  "on_timeout": "fail"
} }
```

- **Recipients:** a phone number or email from the run, or members by role (`to: {role: "finance"}`), whose links open in the app.
- **Delivery:** the platform sends the link by the channels listed, in order of what it has for the recipient, through the outbox, as approval requests are sent. WhatsApp to people outside the organisation needs an approved template (the 24-hour window); Telegram reaches only people linked to Taskiem.
- **Waiting:** the step is built on the signal machinery ([architecture 4.6](architecture.md#46-timers-signals-and-approvals)). The orchestrator records `AwaitSignal` with the form request's id as the correlation key; the submission is the signal, and an early or repeated one is handled as signals are. Reminders and the timeout are timers.
- **Output:** the answers, checked against the step's schema, with files as references and `respondent` (who answered, how they were verified).
- **Cancelling the run** withdraws the link: opening it says the request is no longer needed.

### 6.8 Look and language

- **The form shows** the organisation's name and logo, and an accent colour from the [embedding theming tokens](../embedding.md#9-theming), in light or dark.
- **Language:** the form's own text is in the language the form sets, and its controls follow the respondent's browser among Taskiem's [languages](../languages.md).
- **No custom HTML, CSS or JavaScript.** "Powered by Taskiem" is shown on lower plans; the safety line ("Never enter a password or card PIN here") always is.

### 6.9 On a website

A form can be embedded with a script and an element (`<taskiem-form form="…">`), a plain frame, or a button that opens it in an overlay. The design is the storefront's ([storefronts 6.11](storefronts.md)): a frame from the form host, allowed only on the organisation's verified websites by `frame-ancestors`, no cookies, and `postMessage` events (`form:height`, `form:submitted` with the reference) that carry no answers. `members` forms are not embeddable, since they need the app's session.

### 6.10 In chat (v2)

The same form can be filled in from WhatsApp (as a Flow) or Telegram (one question at a time), with the conversation's existing field logic. A form step can then reach a bound person in their chat without a link. Files and conditional pages come to chat after the basic fields.

## 7. Trust and safety

### 7.1 Who can publish a public form

- Public forms need the organisation's **email confirmed** ([onboarding](../onboarding.md)), and a tenant less than 7 days old has a lower submission limit.
- Every public form has a **Report** link. Operators can suspend a form or every form of an organisation from the [operator console](../operator-console.md), audited.
- A form's title and the organisation's name are checked against the same reserved and look-alike names as storefronts (banks, payment providers, government agencies, Taskiem itself).
- If storefront moderation is adopted ([decision 0036](../decisions/0036-storefronts.md)), public forms' text and logos go through it too.

### 7.2 Fields a form never collects

- **Refused at deploy and at submission, in every form:** passwords, card numbers, CVV, card or bank PINs, one-time codes and bank login details. They are checked by field names, `x-pii` categories, and the PII detector on submitted values (which rejects values that look like card numbers). Credentials belong in the vault, as connections and secrets entered by members in the app ([compliance](../compliance.md#secret-use)).
- **Government and bank identifiers** (BVN, NIN, account numbers) are allowed only when the field is marked with its `x-pii` category, so they are sealed at once, and only on `link` or `members` forms, or `public` forms with `verify` on.

### 7.3 Abuse

- **Rate limits:** per form and per IP hash (default 50 submissions an hour per IP for a public form, generous because mobile networks share IPs; see [storefronts 7.5](storefronts.md#75-abuse-of-forms)), and the organisation's ingest limits.
- **Bots:** a hidden honeypot field, a minimum time to fill in, and a light proof-of-work in the page's script. A form under attack falls back to requiring the code (`verify`) for a while. No third-party captcha, which would be a new data processor.
- **Size:** a submission is at most 256 KB without files.
- **Plan limits:** submissions count as runs. Beyond the plan, the form shows its closed message and the organisation is alerted.

## 8. Architecture

### 8.1 Data model (tenant data under forced row-level security unless marked)

| Table | Holds |
| --- | --- |
| `form_endpoints` | One per workflow with a form trigger and environment: public id, status (live, paused, closed), the deployed version, opening and closing times, counters |
| `form_requests` | One per form step waiting: run, step, recipient (sealed), link token hash, expiry, channels, reminders sent, status |
| `form_links` | One-time links for `link` forms: token hash, bound phone or email (sealed), expiry, used at |
| `form_sessions` | A multi-page form in progress: answers so far (sealed), page, expiry (24 hours) |
| `form_submissions` | Submission id, endpoint or request, idempotency token hash, sealed answers, file references, respondent, IP hash, channel (direct, embed), the run started or signalled |
| `form_files` | Upload records: hashes, type, size, scan result, the object's key in Xenibase storage |
| `form_codes` | Verification codes: hash, address hash, attempts, expiry |
| `embed_sites` | Websites allowed to frame forms and storefronts: origin, verification, status. Shared with storefronts (their `storefront_embed_sites` becomes this) |
| `form_hosts` (platform) | Public id → tenant, read by the form host through a narrow `SECURITY DEFINER` function |

### 8.2 The trigger's input

The run's `trigger.body` is the answers, checked against `inputs.schema`, exactly as a manual run's body. Besides it, `trigger.form` holds:
- `submission_id` and `submitted_at`;
- `respondent`: `{kind: anonymous | verified | link | member, phone, email, member_id}`;
- `channel` (`direct`, `embed`, `chat`) and the embedding site;
- `consents`: each consent field's text hash and time.

**How it starts:** through the ingest pipeline ([architecture 8.2](architecture.md#82-ingest-pipeline)), deduplicated on the submission id, within the tenant's ingest limits. A `members` submission starts the run as that member, so permissions, four-eyes and audit apply to them.

### 8.3 The form host

- **A domain of its own** (for example `forms.<platform domain>`), separate from the app and the API, serving nothing else. Organisations can use their own subdomain (`forms.example.com`), verified like embed domains ([embedding 10](../embedding.md#10-custom-domains)). The storefront host and the form host can be the same service with separate domains.
- **No cookies.** The idempotency token and the session id of a multi-page form are in the form. `members` forms are served by the app instead, under its session.
- **Strict CSP:** `default-src 'self'`, no inline scripts except hashed ones, `form-action 'self'`, `frame-ancestors 'none'` except `/embed/` pages for verified sites.
- **Rendering** by the `public` role shared with storefronts, with its own Postgres role and pool ([storefronts 8.3](storefronts.md#83-isolation-from-the-app)) (Go templates), working without JavaScript. Conditions are evaluated on the server between pages; a small script shows and hides fields on the same page and uploads files with progress. Page weight is under 50 KB before the logo.
- **Narrow access:** the host reads a form's deployed definition and options for `x-form.options_from` through narrow functions, never with a tenant's broad scope.

### 8.4 API

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/forms` | Every form of the organisation: workflow, environment, link, status, counts |
| `PATCH /v1/forms/{id}` | Pause, reopen, close, set opening times (`workflow.publish`) |
| `POST /v1/forms/{id}/links` | Mint one-time links for a `link` form, singly or from a list (`form.links`) |
| `GET /v1/form-submissions`, `GET /v1/form-submissions/{id}` | Submissions, with personal fields masked as everywhere |
| `GET /v1/form-files/{id}` | A short-lived download URL (`pii.read`, audited) |
| `GET/POST/DELETE /v1/embed-sites...`, `POST .../{id}/verify` | Websites allowed to embed forms and storefronts |
| Public, on the form host: `GET/POST /f/{id}` (pages and submit), `POST /f/{id}/files`, `POST /f/{id}/code`, `GET /r/{token}` (a one-time link), `GET /s/{token}` (a status link), `GET /embed/...`, `POST /report` | Served by the edge, no session |
| In the app: `/forms/{id}` | `members` forms |

**Permissions:** designing a form is `workflow.edit` and putting it live is `workflow.publish`, since it is part of the workflow. A new `form.links` permission mints links (owners and admins by default). Events are audited: `form.live`, `form.paused`, `form.links_minted`, `form.file_download`, `form.suspended`.

### 8.5 What it reuses

- wd/v1, inputs schemas, CEL, the ingest pipeline, signals, timers and the outbox.
- The vault's PII sealing and the PII detector, the audit chain, plan limits, the operator console.
- Embed domain verification and theming tokens.
- The chat channels' field logic (for v2).
- Shared with storefronts: the public host, the renderer, the embed frame, the abuse controls, `embed_sites`, and Xenibase storage. Whichever ships first builds them. A storefront's `custom` action (storefronts v3) is a form trigger.

What is new: the `form` trigger and step, the form host, uploads with scanning, one-time links and codes, and the form editor in the canvas.

## 9. Privacy (NDPA)

- **Every form shows** who receives the answers (the organisation by name), the purpose (the form's description), and a link to the organisation's privacy notice, or one Taskiem generates from its details.
- **The organisation is the controller**; Taskiem is its processor, as for any run.
- **Fields marked `x-pii`** are sealed when received, and masked in the app, logs and AI. The detector also catches unmarked personal data in text fields.
- **Erasure** of a respondent's data uses the existing erase-by-subject function, which now covers submissions and files.
- **Retention** is the workflow's. Unfinished multi-page sessions are deleted after 24 hours.
- **The IP address** is kept only as a keyed hash, for abuse limits.

## 10. What the builder sees

- On the workflow: the form's link, QR code, embed code, status, and submissions this week.
- On each run started by a form: the submission (masked), the respondent and how they were verified, and the files.
- A **Forms** page listing every form, and a form step's pending requests on the run (with **Resend link** and **Cancel**).

## 11. Plans

Submissions are runs and count against the plan's runs. Forms add:
- file storage, counted against the Xenibase project's storage;
- SMS codes and SMS links, counted against an SMS allowance (`sms_monthly` in `plans.yaml`, shared with storefronts);
- an own subdomain for forms, and removing "Powered by Taskiem", on higher plans.

The limits per plan are for the pricing work (B1), like the others.

## 12. Phasing

v1 is milestone S7 of Phase 5 in the [build plan](build-plan.md#phase-5--storefronts-and-forms-about-20-weeks), on the public host built in S1.

| Phase | Scope |
| --- | --- |
| **v1** | The `form` trigger with `public`, `link` and `members` access; the field types of 6.2 including files; pages and conditions; prefill; verification by SMS or email code; the three `after` modes and the status link; the `form` step with SMS and email delivery; embedding; the form editor and preview in the canvas; the AI builder drafting forms; the trust-and-safety controls |
| **v2** | Forms in chat (WhatsApp Flows, Telegram); form steps delivered by WhatsApp and Telegram; repeating groups and nested fields; a copy to the respondent; saving a long form to finish on another device |
| **v3** | Forms for partners' end users ([embedding](../embedding.md)); signatures; drop-off counts per page |

**v1 depends on:**
- the Xenibase storage integration (P4-T1);
- a malware scanner;
- the form host's domain;
- an SMS sender (Termii) and an email sender (P4-O1).

**Success measures:**
- the share of workflows with a form trigger;
- the completion rate of opened forms;
- the median time from a form step's link being sent to its answer;
- reports and suspensions per 1,000 public forms.

## 13. Open questions

1. **The form host's domain**, and whether it is the same service as the storefront host.
2. **The malware scanner:** an open-source one run by Taskiem (no new data processor) or a scanning service.
3. **Moderation** of public forms: the same decision as storefronts (decision 0036), or lighter, since forms have no catalogue or photos.
4. **The `result` mode:** whether 30 seconds is the right wait.
5. **Default retention** for files, if shorter than the run's.
6. **The name** people see: "Forms" is generic and works, but could share a brand with storefronts.

## 14. Where the ideas come from

From public user documentation only, under the [clean-room policy](../clean-room-policy.md); no competitor code was read:
- **A form as a trigger, multi-page forms, responding when the workflow finishes, redirect after submitting:** n8n's Form Trigger and Form nodes.
- **Form builders with conditional fields, prefill from the link, file uploads, and access restricted to signed-in people:** Zapier Interfaces.
- **Asking a person for input in the middle of a flow:** Activepieces' human input pieces, and Taskiem's own approvals.

What is Taskiem's own: the form as the workflow's inputs schema (one definition for every way a run starts), the form step built on signals, one-time links and codes instead of accounts, files kept out of run history, and fields a form can never collect.
