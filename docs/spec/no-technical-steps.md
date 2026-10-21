# Build plan: no technical steps for business owners

Taskiem is for Nigerian small businesses and the people who run their offices: traders, shop owners, salon owners, finance and HR staff. Most are not technical. Deploying staging (October 2026) showed, and an audit of the code confirmed, that an owner still meets steps that assume a developer: API keys, JSON, expressions, ids typed by hand, names that must match, messages in developer terms, and one bug that stops a one-person business from accepting its own orders.

This plan removes them. It is a workstream of its own, **N0 to N10**, one pull request per milestone, and it runs **before the Phase 5 pilot** (S8's six weeks with traders and field agents): the pilot should test the product owners will actually meet.

## The rule

An owner or their staff, using the web app or WhatsApp or Telegram, can get a workflow, a storefront or a form working **without**:

- pasting a key or code from a developer dashboard, unless a guided screen says exactly where it is and checks it;
- typing JSON, an expression (`=steps…`), a cron line, a schema or a condition language;
- typing an id, a name that must match something else, or a value in a machine format (kobo, A1 ranges, `234…` without a plus, recipient codes);
- leaving Taskiem to set something up elsewhere (a webhook in a provider's dashboard, DNS records), unless the step is optional and guided;
- reading a message about paths, schemas, tokens or step ids.

Everything a non-technical person needs can be done from a guided screen, or from chat. The canvas, expressions and raw definitions stay available to people who want them, behind **Advanced**.

## Gate: how we know it worked (G-N)

1. **Observed sessions:** five owners who have never used Taskiem (at least two who use only a phone), each given the tasks below with no help. Every task is completed by at least four of them; none needs a developer's word or a dashboard other than Taskiem's.
   - Make a storefront, turn orders on, and accept a test order (one-person business).
   - From a goal typed or spoken in their own words, get a workflow that records something in a table and tells them on WhatsApp or Telegram, and publish it.
   - Connect Paystack and Google Sheets.
   - Start a template, fill in its details, and run it once.
   - Read and act on an error (a missing connection, an expired key).
2. **No technical text** on the owner's path: a test lists every user-facing string of the owner's pages and chat messages and fails on the words in the glossary of [Appendix C](#appendix-c-words-an-owner-never-sees).
3. **Time:** median under 10 minutes from sign-up (or agent hand-over) to the first workflow running, and from `make my page` to orders being taken.
4. **Support:** in the pilot, fewer than 1 in 10 businesses ask for help with set-up.

## N0: decisions (people, before or alongside N1)

| Decision | Recommendation | Blocks |
| --- | --- | --- |
| **N0-1 Solo approvals.** Four-eyes (a maker may not approve their own run or workflow) is on by default. A one-person business has no second person | Keep four-eyes. When the person approving is the **only** active member who holds the role, let them approve, recorded as `self_approved` in the audit log, and say so on the approval ("you are the only approver"). A policy can still forbid it outright. Owners count for every role in approvals | N1 |
| **N0-2 Google sign-in.** One-click Google (Sheets, Gmail) needs Taskiem's Google Cloud app verified; Gmail's scopes are *restricted* and need a yearly third-party security assessment, Sheets' *sensitive* scopes a review that takes weeks | Start the Sheets (and Drive file picker) verification now. For Gmail, decide whether to apply (cost and time) or send owners' email through the platform's own mail (as storefronts already do) and keep Gmail for businesses that need their own mailbox | N2 |
| **N0-3 Paystack.** Paystack connects merchants with a secret key; whether it offers a partner sign-in flow is to be asked | Ask Paystack. Until then: a guided key screen (N2), which is good enough | N2 |
| **N0-4 Notifying owners.** Templates text the owner through Termii (a paid account and keys) | A built-in **Tell me** step that uses the owner's linked WhatsApp or Telegram, and the platform's SMS (metered by the plan) when neither is linked. Termii stays for businesses texting their own customers in bulk | N4 |
| **N0-5 What is "advanced".** Which features the default view hides | Hide by default: the dev environment and promotion, raw definitions, expressions, cron, policies' CEL, own connectors, allowed hosts, drift. A switch in Settings (owners and admins) shows them. Plans above Shop show them by default | N5, N8 |
| **N0-6 Meta Embedded Signup.** A business's own WhatsApp number needs Taskiem to be a Meta Tech Provider | Apply when the shared number is not enough for a pilot business; until then, the guided screen | N9 |
| **N0-7 Chat publishing.** Letting chat publish workflows | Yes, with the same checks and approvals as the web app, and a WhatsApp PIN or step-up where policy asks | N6 |
| **N0-8 Receiving email.** "When an email comes in" needs mail received somewhere. Gmail's own notifications need restricted scopes (N0-2), and cover Gmail only | An address per workflow on Taskiem's own mail domain, received by a Taskiem `mail` role (no mail company sees the messages); owners forward from any mailbox with a filter. An inbound-mail service only as a fallback for operators who cannot open port 25 ([decision 0038](../decisions/0038-inbound-email.md)) | N10 |

## Milestones

Sizes: **S** a few days, **M** one to two weeks, **L** three weeks or more, for one engineer.

### N1: A one-person business can approve (S, a bug) — first

**Problem** ([A1](#appendix-a-the-audit-findings)). The storefront's order, booking, enquiry and callback workflows wait for the `owner` role. The owner turned them on, so the owner published them; four-eyes then refuses the owner as their maker (`engine/runtime/approvals.go:143-181`). Orders time out and are declined after 24 hours. Owners also do not count for other roles in approvals (`approvals.go:151`, `api/approvals.go:52-65`, `api/whatsapp_approvals.go:127`), though `Principal.HasRole` says they should. The storefront tests decide orders through `Orders.Decide` directly, which is why this was not caught.

**Build:**
- The rule of N0-1 in the vote check, in the approvals inbox and in chat notifications: owners hold every role; a maker who is the only active holder of the role may approve, recorded as `self_approved` (actor, run, step) in the audit chain and on the approval.
- The approvals page and chat say "You are the only person who can approve this".
- Approvals show the step's **name**, not its id (`web/src/pages/Approvals.tsx:28, 92`).

**Accept:**
- An end-to-end test that does what an owner does: sign up, make the page, turn orders on (publishing the default workflow), a customer orders on the public host, the owner accepts in chat and in the web app, the customer is told.
- Four-eyes still holds when a second member holds the role, and when a policy forbids self-approval.

### N2: Connecting a service (L)

**Problem** ([A2, A3, A13](#appendix-a-the-audit-findings)). Connecting means pasting what a developer dashboard shows (Paystack's secret key; Google service-account JSON or OAuth client and refresh token; WhatsApp tokens and ids). Only Xenibase signs in with one click. Connection names must match by hand, and the defaults disagree (`default` on the Connections page; `main` in templates, the guide, storefront payments and the AI's examples), so a workflow fails when it runs.

**Build:**
- **One name.** Every new connection is named `main` unless the business already has one for that service; every place that names a connection (templates, the canvas, storefront payments, the AI builder's context) offers a **picker** of the business's connections for that service, with **Connect** inline. A missing connection is never a free-text field.
- **One-click Google** (Sheets, and Gmail per N0-2): `auth.oauth2` in the manifests, Taskiem's Google app, the consent screen, and a **sheet picker** (Google's file picker) so nobody copies a spreadsheet id.
- **A guided Paystack screen:** where the key is ("Paystack → Settings → API Keys & Webhooks") with a picture; **test** and **live** shown as two choices instead of a key prefix to know; the key checked as it is pasted (its prefix, then a call to Paystack); clear words if it is a public key or a test key in live.
- **The same guided form for every key-based connector**, from its manifest: a help line and a link per field (a new `help` and `where` per field in the manifest schema), optional fields under **More options**, webhook secrets set by Taskiem where the provider allows.
- The checklist's **Connect an app** opens the service a chosen template needs, not a blank form.

**Accept:** a connection made from a template, the AI review and storefront payments without typing a name; a Paystack key pasted wrong (public, test in live, from another business's account) gets a plain message; Google connects with one click in staging (unverified app, test users) and the picker returns a sheet.

### N3: Ready to publish (M)

**Problem** ([A4, A5](#appendix-a-the-audit-findings)). The AI's **To confirm** list (connections, variables, secrets, roles) is text to read, not something to do; the chat builder drops it. Publishing checks the definition and policies only (`api/workflows.go:494-502`), so a missing connection, variable or role fails at run time. The builder does not know the business's roles, so it invents them (`credit_officer`). Roles are free text in the step panel, form steps and templates, and a business role alone does not let its holder approve (`approval.decide`).

**Build:**
- **Readiness**, one service for the web app, chat and the AI: for a draft, what is missing (a connection, a variable, a secret, a role nobody holds, a table) and in which environment. Publishing shows it and offers each fix; publishing anyway is possible only for what can be fixed later (a variable), never a connection a step needs at once.
- **The AI review's items become actions:** the builder returns structured needs (`connection`, `variable`, `role`, `table`) instead of prose assumptions; each has its form in the review (connect, set, assign, make), like tables in [#37](https://github.com/ISRAEL-DUFF/taskiem/pull/37).
- **The builder knows the roles** the business has and who holds them, and prefers `owner` for approvals in a business with one member.
- **Role pickers** wherever a role is named; a business role used by an approval lets its holders approve (the role grants `approval.decide` for the approvals that name it).
- **Chat:** the build's reply lists what is missing in plain words, and asks for it (see N6).

**Accept:** an AI draft that needs Paystack, a variable and an approver is made ready from the review without leaving it; publishing a workflow whose connection is missing shows the fix instead of failing later.

### N4: Telling the owner (M)

**Problem** ([A6, A7](#appendix-a-the-audit-findings)). Fifteen of the 24 templates text the owner through `termii@1`, which needs a paid Termii account and its keys, though the owner already uses WhatsApp or Telegram with Taskiem. Four templates, including the first one the guide suggests, start from a signed webhook (HMAC secrets, per-environment URLs), and Paystack-event templates need a URL pasted into Paystack's dashboard.

**Build:**
- A built-in **Tell me** connector (`notify@1`, no connection): to the owner, a role or named members, on their linked WhatsApp or Telegram, and the platform's SMS when neither is linked (counted in the plan's `sms_monthly`). Templates and the AI builder use it for messages to the business's own people.
- **Templates updated:** owner alerts move to `notify@1`; the first starter is one that needs no developer (a schedule or a form); webhook templates are labelled **Needs a developer** and not offered first.
- **Paystack events without a webhook:** where a template reacts to Paystack payments, Taskiem checks Paystack itself (as storefront payments do), or registers the webhook through Paystack's API if it allows; the dashboard step disappears.
- The editor shows only the production webhook address by default, with what it is for.

**Accept:** every template a Shop-plan business can use runs without Termii or a webhook set-up; the owner hears on WhatsApp, Telegram, or SMS.

### N5: Plain inputs and plain messages (M)

**Problem** ([A9, A10, A14, A16](#appendix-a-the-audit-findings)).
- Variables in Settings must be typed as JSON (`"Ade"` with quotes; a phone number becomes a number).
- Running a workflow by hand, in the editor and the guide, asks for raw JSON.
- Problems and errors use developer terms: JSON pointers, `wd-test/v1`, "connection is missing \"secret_key\"", token counts, "connectors considered".
- CSV import needs headers that match machine column names exactly.

**Build:**
- **Variables:** a plain text box (what the guide already does), with the type inferred; phone numbers in any form normalised; each variable's description shown (from the template that reads it).
- **Running by hand:** a form from the workflow's inputs (`SchemaForm` without expressions), as chat's `run` already does; JSON under **Advanced**.
- **Messages:** a single map from the platform's problem and error codes to plain sentences that name the step by its name, with a **Fix it** link (to the connection, the variable, the step). Applied to: publishing checks, the AI review, run failures, the guide, chat. Model, rounds and token counts under **Details**.
- **CSV import:** headers matched to column titles ignoring case and spaces, then a mapping step for the rest; **Make a table from this file**.
- The test of the gate's point 2 (Appendix C) is added here and kept.

**Accept:** a variable typed as `Ade` or `0803 123 4567` is saved as meant; a manual run from a form; every message an owner can meet on the five gate tasks is in plain words (the test passes).

### N6: Chat goes all the way (M)

**Problem** ([A11](#appendix-a-the-audit-findings), and S5's open items). Building from chat stops at a draft: tables, settings and publishing happen in the app. `make my page` does not turn orders on (that needs the web app's Forms page and `workflow.publish`). Storefront command words and the customer's "your order is ready" text are English only. `close shop today` never reopens by itself.

**Build:**
- After the owner says **yes** to an AI draft in chat: the proposed tables are made, missing settings asked for one at a time (as template details already are), missing connections sent as a link to the one screen that makes them, and **publish** offered (N0-7).
- `make my page` ends with **Turn on orders?** (and bookings for services): the default workflows are made and published from chat, with the approval rule of N1.
- Storefront command words in Hausa, Yoruba, Igbo and Pidgin (word lists, as approvals have); the customer messages in the business's language.
- `close shop today` reopens the next morning by itself, saying so.
- A chat reply never says "open the app to finish" for anything chat can do.

**Accept:** from WhatsApp alone, a trader makes a page, turns orders on, accepts an order, and builds and publishes a workflow that writes to a new table.

### N7: The web storefront without tables to build (S)

**Problem** ([A12, A13](#appendix-a-the-audit-findings)). In the web editor, a catalogue means making a table on the Tables page, then mapping name, price and photo columns and choosing naira or kobo; bookings the same. Chat makes them by itself. Storefront payments name the Paystack connection by typing.

**Build:**
- **Make my catalogue** (and **Make my services**): the tables and their mapping as chat makes them (`productColumns`), in one click; mapping only for a business that brings its own table.
- Prices in naira only on the owner's side; kobo stays internal.
- Storefront payments use N2's picker and say whether the connection is live.
- The checklist gains **Make your page** and **Link WhatsApp or Telegram**.

**Accept:** a storefront with products and photos made from the web editor without opening the Tables page.

### N8: Templates and the canvas for everyone (L)

**Problem** ([A8, A15, A17](#appendix-a-the-audit-findings)).
- Templates ask for technical ids: a spreadsheet id "from its link", A1 ranges, Meta template names, Paystack recipient codes (`RCP_…`), amounts in kobo, phone numbers "without +".
- The canvas exposes expressions, cron, JSON configuration and CEL, and **New workflow** (a blank canvas) is the main button.
- Forms can only be made by changing a workflow's trigger.
- The checklist asks for a connection before showing any value, and its help points at the connector SDK.

**Build:**
- **Templates:** a pasted sheet link or the picker instead of an id; a tab picker instead of a range; amounts in naira; phone numbers in any form; Paystack recipients from **bank and account number** (a `resolve_account` and `create_recipient` action in `paystack@1`, the name confirmed back to the owner before saving); Meta template names chosen from the business's approved templates.
- **The canvas:**
  - a **schedule** picker ("every Friday at 10am") instead of cron;
  - a **value picker** ("from an earlier step: the customer's phone") instead of expressions;
  - a **condition** builder for "run only when" and a form page's "show only when";
  - the approval and form-step settings without JSON.

  Expressions, JSON, cron and CEL under **Advanced** (N0-5).
- **Starting:** **Describe it** (the AI builder) and **Start from a template** are the main ways to start; a blank canvas is under **Advanced**. The Forms page has **New form**, which makes the workflow behind it.
- **The checklist** is reordered: pick a template or describe a goal, then connect what that needs; its help points at a guide for owners (a new `docs/guide/` of plain how-tos, not the developer docs).

**Accept:** every template's details can be filled in by the gate's owners; a workflow with a schedule, a value from an earlier step and a condition made without typing an expression.

### N9: The optional technical steps, guided (M)

**Problem** ([A18](#appendix-a-the-audit-findings), and S6's open items). An own domain means TXT and A or CNAME records at a registrar; embedding means a DNS record or a file; an own WhatsApp number means Meta's developer set-up; dev and prod environments double every connection and variable. An owner is not told when their own domain stops being verified.

**Build:**
- **Domains:** registering a domain through the domain platform (S6's remaining part, when its contract is agreed), which needs no DNS at all; until then, step-by-step pages for the registrars Nigerian businesses use (Whogohost, Qservers, Domainking, GoDaddy, Namecheap), with the exact screens, and a **Check now** that says which record is missing.
- **Embedding:** the same guides; **Send to my web designer** (an email with the snippet and the record).
- **Own WhatsApp number:** Meta Embedded Signup (N0-6); until then, a guide with screens.
- **Environments:** for businesses without Advanced, one environment (production) is all they see.
- The owner is told on WhatsApp or Telegram when their domain or an embedding site is unverified, with what to check.

**Accept:** a gate owner connects a domain they own using only the guide; an unverified domain reaches the owner in chat.

### N10: An email starts a workflow (L)

**Problem.** Owners want workflows that start when an email arrives: a supplier's invoice, an order by email, a bank's transaction alert, a website's contact form. wd/v1 reserves an `email` trigger (`schemas/wd-v1.schema.json:1272`; [architecture §8.1](architecture.md)) and the app already has words for it ("An email comes in", `web/src/lib/home.ts:51`), but nothing receives mail and publishing one is refused (`engine/ingest/triggers.go:177`). The Gmail connector has actions only; a Gmail trigger needs restricted scopes and a yearly security assessment (N0-2).

**Build** (the design is [decision 0038](../decisions/0038-inbound-email.md)):
- **Definition:** the `email` trigger's config in the schema (`senders`, a list of addresses or domains; `attachments`, `keep` or `ignore`), in the [wd/v1 contract](../contracts/wd-v1.md), `ingest.Check`, the validator's messages and the `triggers.type` check. The OpenAPI `Trigger.type` enum gains `email` and the `storefront` and `form` it lacks.
- **Addresses:** `email_endpoints` (12-character public id, per workflow and environment, stable across versions), made in `SyncEnvironments` like form endpoints; a **New address** action that retires the old one; the address in `GET /v1/workflows/{wf}/triggers`.
- **Receiving:** a `mail` role, an SMTP server (STARTTLS, `SIZE` 30 MB, recipients checked at `RCPT` against known addresses, SPF/DKIM/DMARC recorded) writing to an `email_inbox` table through narrow functions, as the form host does; the api role drains it. Settings `TASKIEM_MAIL_DOMAIN`, `TASKIEM_MAIL_LISTEN` and the certificate. The VM kit gains the role, port 25 and the MX record in its runbook.
- **Parsing:** raw MIME to `trigger.body`:
  - fields: `from`, `to`, `cc`, `reply_to`, `subject`, `date`, `message_id`, `text`, `latest` (without the quoted thread), `html`, `attachments`, `forwarded_by`;
  - character sets, HTML-only mail made into text, and a long body cut to `max_payload_bytes` with `truncated: true`;
  - shares the MIME walking with the Gmail connector (`connectors/gmail/gmail.go:305-337`).
- **Runs:**
  - The run's `trigger` gets `email: {address, received_at, auth}`.
  - Dedup is by Message-ID.
  - The loop guard refuses automatic replies and Taskiem's own mail.
  - `senders` is checked, trusting the From only when DKIM is aligned.
  - Personal data is sealed at the declared email paths (`engine/runtime/sealing.go` extended beyond `trigger.body`'s inputs schema).
  - The usual ingest gate, quotas and receipts apply.
- **Attachments:** scanned by ClamAV and stored in the private bucket as form files are (`engine/forms/service.go:360-425`, `engine/media`); JPEG, PNG and PDF, 25 MB each, 10 per message, the rest under `skipped`; a `file_url` action for steps. Without a scanner, `attachments: keep` cannot be deployed.
- **Forwarding confirmations:** mail from Gmail's and Outlook's forwarding-confirmation senders starts no run. Its code or link appears on the trigger's screen and goes to the owner's WhatsApp or Telegram (the N4 channel).
- **Web app:**
  - **An email comes in** in the trigger picker (`web/src/pages/Editor.tsx` `SettingsTab`), with the address and **Copy**.
  - **Only from these senders**, as a list.
  - **Keep attachments**.
  - A step-by-step guide with screens for forwarding from Gmail and Outlook (filter by sender or subject).
  - A live **Waiting for your first email…** that turns into the received message.
  - **Try it with a sample email**, which runs the workflow as if a sample message had arrived.
  - The canvas's trigger card shows the address.
- **Chat:** an owner can ask for "a workflow for when my bank emails me an alert"; the AI builder knows the trigger (`engine/ai/builder/prompt.go`) and the aieval suite covers it. Chat replies with the address and the forwarding guide's link.
- **Templates:** two in the library that start from email: a bank-alert email recorded in a table with the owner told, and a supplier's invoice saved with its PDF.

**Accept:**
- Tests of the parser on recorded messages: Gmail-forwarded, Outlook, multipart with attachments, HTML-only, non-UTF-8, a reply with a quoted thread, a malformed message.
- An end-to-end test: mail sent over SMTP to a workflow's address starts one run with the expected body and file references; the same Message-ID again starts none; an unknown address is refused at `RCPT`; an automatic reply is refused; a Gmail confirmation shows its code and starts nothing.
- A gate owner sets up Gmail forwarding with only the guide, and the next matching email runs the workflow.
- No owner-facing text from Appendix C on the new screens.

## Order and timing

| Order | Milestone | Size | Why here |
| --- | --- | --- | --- |
| 1 | N1 Solo approvals | S | A bug that stops one-person shops taking orders; nothing else waits for it |
| 2 | N3 Ready to publish | M | The base N2, N5 and N6 build on (readiness, structured needs, role pickers) |
| 3 | N2 Connections | L | The most common blocker; Google verification (N0-2) runs alongside |
| 4 | N4 Telling the owner | M | Unblocks most templates |
| 5 | N7 Web storefront | S | Small, and the pilot's businesses need it |
| 6 | N6 Chat all the way | M | Phone-only owners |
| 7 | N5 Plain inputs and messages | M | Polish across everything above, and the gate's text test |
| 8 | N8 Templates and canvas | L | The long tail; parts can ship separately |
| 9 | N9 Optional steps | M | Optional features; domains wait on the domain platform |
| 10 | N10 An email starts a workflow | L | Asked for after N1 to N9 were planned; needs N0-8, the mail domain and port 25 on the VMs, and reuses forms' file storage and N4's channel. M if an inbound-mail service is used instead of the `mail` role |

About 14 to 18 weeks for one engineer, or 8 to 10 for two working in parallel (N2 and N8 split well). The Phase 5 pilot starts after N1 to N7 and the gate's observed sessions; N8 and N9 can finish during the pilot.

## Out of scope

Operator and deployment work stays technical by design: the VM kit, environment files, the operator console, registering field agents, monitoring. They are covered by the [staging runbook](../runbooks/staging.md) and the [VM guide](../vm.md).

## Appendix A: the audit findings

From the read-only audit of the owner-facing code (21 October 2026). Every finding maps to the milestone that removes it.

| # | Finding | Where | Milestone |
| --- | --- | --- | --- |
| A1 | One-person businesses cannot approve their own workflows or storefront orders; owners do not count for other roles in approvals | `engine/runtime/approvals.go:143-181, 151`; `api/approvals.go:52-65`; `api/whatsapp_approvals.go:127`; `api/auth.go:108-111` | N1 |
| A2 | Connecting a service means pasting raw credentials; one-click sign-in only for Xenibase | `connectors/*/manifest.yaml`; `web/src/pages/Connections.tsx:437-509`; `web/src/pages/Start.tsx:195-227` | N2 |
| A3 | Connection names must match by hand; defaults disagree (`default` and `main`) | `Connections.tsx:440`; `Start.tsx:147`; `templates/library/*.json`; `StorefrontForms.tsx:209`; `engine/ai/builder/prompt.go:160` | N2 |
| A4 | Publishing does not check connections, variables or roles; the AI's "To confirm" is not actionable; the builder knows no roles; chat drops assumptions | `api/workflows.go:494-502`; `web/src/pages/AIBuild.tsx`; `engine/ai/builder/context.go`; `api/whatsapp_build.go:248-270` | N3 |
| A5 | Business roles are free text; approving needs `approval.decide` besides the role | `web/src/pages/Members.tsx:349-351`; `web/src/canvas/StepPanel.tsx:184-186`; `api/auth.go:66-73`; `api/approvals.go:44` | N3 |
| A6 | Owner alerts need a paid Termii account (15 of 24 templates) | `templates/library/*.json`; `connectors/builtin/builtin.go` | N4 |
| A7 | Webhook triggers need signing secrets, per-environment URLs, provider dashboards; the first starter template is one | `web/src/lib/onboarding.ts:65`; `web/src/pages/Editor.tsx:481-498, 580-651` | N4 |
| A8 | Templates ask for spreadsheet ids, A1 ranges, Meta template names, recipient codes, kobo, phone formats | `templates/library/*.json`; `connectors/paystack/manifest.yaml` | N8 |
| A9 | Variables must be typed as JSON | `web/src/pages/Settings.tsx:284, 317-322`; `web/src/ui.tsx:262-292` | N5 |
| A10 | Starting a run by hand takes raw JSON | `web/src/pages/Editor.tsx:692-719`; `Start.tsx:344-346` | N5 |
| A11 | Chat building stops at a draft; `make my page` does not turn orders on | `engine/lang/messages/en.json:653, 663`; `api/whatsapp_build.go:433-451`; `web/src/pages/StorefrontForms.tsx:61` | N6 |
| A12 | The web storefront needs a table built and columns mapped | `web/src/pages/Storefront.tsx:482-551`; `StorefrontBookings.tsx:427-448` | N7 |
| A13 | Storefront payments name the Paystack connection by typing | `web/src/pages/StorefrontForms.tsx:204-214` | N2, N7 |
| A14 | Problems and warnings in developer terms (paths, wd-test, tokens, step ids) | `AIBuild.tsx`; `engine/ai/builder/builder.go:367`; `Start.tsx:287-289`; `api/workflows.go:429`; `engine/runtime/worker.go:1209`; `Approvals.tsx` | N5 (step names on approvals: N1) |
| A15 | The canvas, forms and policies expose expressions, cron, JSON and CEL; "New workflow" is the main button; forms only through a trigger change | `Editor.tsx`; `StepPanel.tsx`; `web/src/forms/SchemaForm.tsx`; `web/src/pages/Forms.tsx`; `FormEditor.tsx`; `Policies.tsx`; `Workflows.tsx` | N8 |
| A16 | CSV import needs exact machine column names | `web/src/lib/tables.ts:178-185` | N5 |
| A17 | The checklist asks for a connection before showing value; help points at the connector SDK; no page or chat-linking items | `web/src/lib/onboarding.ts:31-35, 149`; `api/onboarding.go:441-474` | N7 (items), N8 (order, help) |
| A18 | Own domains, embedding, an own WhatsApp number and two environments are technical | `StorefrontDomains.tsx`; `docs/embed-sites.md`; `WhatsAppNumber.tsx`; `docs/environments.md` | N9 |

## Appendix B: other open items found along the way

| Item | Found in | Milestone |
| --- | --- | --- |
| Field agents land on the workflows page after signing in, not the agent app | S5 | N6 |
| Owners of agent-made businesses have no email or password for the web app | S5 | N6 (a sign-in link from chat) |
| Chat storefront words and the customer's "ready" text are English only | S5 | N6 |
| `close shop today` does not reopen by itself | S5 | N6 |
| Changing hours or services from chat republishes other unpublished draft edits without saying so | S5 | N6 (say so, or publish only the change) |
| The owner is not told when their own domain becomes unverified | S6 | N9 |
| The web app's billing tiers list lacks `shop` (plans compare only by accident) | S8 | N5 |
| The AI uses one model for building and moderation; a cheap model suits moderation, a strong one building | Staging | N3 (an optional `TASKIEM_AI_MODERATION_MODEL`) |
| The model does not judge photos in moderation (text only) | S8 | Not here: a trust item for the AI layer |

## Appendix C: words an owner never sees

The gate's text test fails if an owner's page or a chat message (outside **Advanced** and **Details**) contains: `JSON`, `schema`, `wd/v1`, `wd-test`, `expression`, `CEL`, `cron`, `kobo`, `token` (other than in "code"), `webhook`, `HMAC`, `endpoint`, `payload`, `API key` (outside the guided key screen), `secret_key`, `step id`, `trigger.body`, `=steps`, `null`, `undefined`, a JSON pointer (`/steps/0/…`), or a raw HTTP status.
