# 0037 — Forms: a hosted form that starts a workflow, or asks someone for input mid-run

Date: 2026-10-19 · Status: **Accepted** 2026-10-20, as amended below (built: S7, [forms](../forms.md))

## Context

Taskiem has no general hosted forms. Its only forms are WhatsApp Flows and the canvas's input form for manual runs. Workflow tools commonly offer a form as a trigger, and people use them for intake, applications and internal requests. Q10 of the [Xenibase plan](../xenibase-integration.md) asks whether Taskiem wants public forms and how respondents sign in. [Storefronts](0036-storefronts.md) answer it for businesses' public commerce pages, with fixed actions; they do not cover an organisation's own forms, internal forms, file uploads, or asking someone for input in the middle of a run.

The full design is [the forms spec](../spec/forms.md).

## Decision

1. **A `form` trigger and a `form` step in wd/v1**, not a separate form product. The form is part of the workflow definition: versioned, validated, deployed and synced to Git with it.
2. **The fields are the inputs schema**, with display hints in `x-form`, plus pages and CEL conditions in the trigger's config. A submission is checked like any run's input.
3. **Three kinds of access:**
   - `public`, optionally with a code by SMS or email;
   - `link`, with one-time links stored only as hashes;
   - `members`, signed in to Taskiem and served by the app.

   There are no respondent accounts and no Xenibase auth.
4. **The form step is a signal** with a one-time link sent through the outbox, reminders and a timeout, like an approval request.
5. **A cookieless form host** on its own domain (`forms.` under the preview domain by default; a multi-page form carries its earlier answers sealed in the page, not in a session table), rendered on the server by the `public` role shared with storefronts, working without JavaScript. The `/embed/` pages are framed only by the organisation's verified websites, sharing storefronts' design.
6. **Files go to Xenibase storage** in a private bucket, scanned for malware and stripped of metadata. Runs see only references; downloads need `pii.reveal` and are audited.
   - **The scanner is ClamAV, run by Taskiem** (decided 2026-10-20): a clamd beside the api role, reachable by it alone, updating its signatures from the ClamAV mirrors. Files then never leave Taskiem's systems for a scanning company, which would be a new data processor for payslips and identity documents (NDPA). Forms take only JPEG, PNG and PDF, photos are re-encoded and PDFs with scripts or actions refused, so the scanner's remaining job is mostly malicious PDFs. Without a scanner, forms that ask for files cannot be deployed. A scanning service can be added behind the same interface for a customer who needs one.
7. **Fields that are never collected:** passwords, card numbers, CVV, PINs, one-time codes and bank logins. Government and bank identifiers are allowed only when marked `x-pii` (sealed at once), and not on anonymous public forms.
8. **The public host, renderer, embed frame, abuse controls and `embed_sites` are shared with storefronts**, built by whichever ships first.

## Alternatives considered

- **A separate form builder, linked to workflows.** Rejected: two definitions of the same fields drift apart, and a form would need its own versioning, review and deploy.
- **Respondent sign-in through Xenibase auth.** Rejected: outsiders fill in a form once, and a code or a one-time link proves what is needed without another account system or data processor. Members already have Taskiem's sign-in.
- **Forms served on the app's domain.** Rejected for `public` and `link` forms, for the same reason as storefronts: a public page next to the app's cookies. `members` forms are in the app because they need its session.
- **A third-party captcha.** Rejected for now: a new data processor, and heavy on cheap phones. A honeypot, timing, proof-of-work and codes under attack come first.
- **File contents in run history.** Rejected: history is kept for audit and replay, and files would bloat it and spread personal data. Files stay as references.

## Consequences

- The Xenibase storage integration (P4-T1) and a malware scanner become dependencies of v1, shared with storefronts.
- SMS codes and links cost money, and are metered in the SMS allowance storefronts introduce.
- The ingest pipeline, signals and outbox gain a new caller, but no new mechanism.
- A form's public link changes nothing in the workflow's permissions: designing it is `workflow.edit`, putting it live is `workflow.publish`, and minting links is the new `form.links`.
- Public forms are a phishing surface. The refused fields, verified domains, reports, suspension and storefront moderation (decision 0036, 13: titles and descriptions) are the controls.
- The operator runs a ClamAV daemon (about 1.5 GB of memory); the VM kit and the Helm chart start one with the public pages.
- Not built in v1 yet: proof-of-work in the page, options read from a data table, text blocks, the organisation's logo on forms, and the moderation and report handling, which come with S8.
