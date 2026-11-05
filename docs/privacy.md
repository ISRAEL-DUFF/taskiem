# Personal data: sealing, detection, redaction, erasure

Personal data in a run is encrypted under its data subject's own key before it is written (spec 4.9), so it can be shown only to those allowed to see it, and erased by destroying the key (spec 9.4).

## What is sealed

| Source | How it is found |
| --- | --- |
| **Declared** | Fields marked `"x-pii": "<category>"` in a workflow's inputs schema, and fields connector manifests declare (`pii` on an action) |
| **Copied** | Any later value equal to one already sealed in the run: a transform copying a BVN into an approval subject seals the copy too |
| **Detected** (Phase 2) | Validators recognise undeclared Nigerian identifiers in step results, triggers and every other payload before they are written |

Detection is conservative, because a step's data is full of digit strings that are not personal:

- Only strings are examined; numbers only under a field name that says what they are (`bvn`, `nin`, `phone`, `account_number`, `card_number`, ...).
- **Phone**: a Nigerian mobile number, `+234`/`234`/`0` then a 70x, 71x, 80x, 81x, 90x or 91x prefix, however its digits are grouped (`0803 123 4567`, `(0803) 123-4567`, `+234 (0)803 123 4567`); or any number in international form, `+` and a country code with 8 to 15 digits in all (`+44 20 7946 0958`).
- **BVN / NIN**: 11 digits (BVNs start with 22), unless they are a phone number.
- **Account number (NUBAN)**: 10 digits under an account field name, or beside a bank code whose CBN check digit it satisfies.
- **Card**: 13 to 19 digits passing the Luhn check with a Visa, Mastercard, Verve, American Express or Discover prefix and length. Card numbers should never reach a workflow: connectors tokenise with the provider.
- **Email**.

Names and addresses cannot be recognised reliably; declare them.

## Where it is redacted

- **Run history and the web app**: sealed values show as `🔒 <category>`. Members with `pii.reveal` can open a run's history; every reveal is audited.
- **Logs**: code step log lines and provider error messages are masked (`[bvn]`, `[phone]`, `[email]`, `[card]`) before they are stored, and so is every string the Taskiem process logs.
- **Audit log**: holds ids, digests and amounts, never personal values, so erasure never touches the hash chain.
- **Approvals**: approvers see the subject opened, because it is what they decide on; what they saw is recorded with their decision, sealed.

Workflows keep working on the plain values: the orchestrator and workers open sealed values inside the transaction that uses them.

## Erasure

Subject ids are keyed by the value's canonical form (a phone in E.164, so `0803 123 4567` and `+2348031234567` are one subject; an email trimmed and in lower case; a BVN, NIN, account or card number by its digits; names as they are) and by the organisation's pseudonymisation key, which is kept under its tenant key and re-wrapped when that key rotates, so ids stay the same across rotations. Subject keys are re-wrapped too. With [bring your own key](byok.md), every subject key is ultimately under the organisation's own key: revoking it makes all personal data unreadable at once, and runs that need it pause until it returns.

Free text (category `other`: a note, an email's body, a transcript) is no one's, so it has no subject of its own: it is sealed under one subject for the whole organisation, under its owner where it has one (an email's subject line, text and attachment names under its sender), and under each known person it names. Erasing the sender, or a person it names, erases it; free text adds no subject key per text, so key rotation re-wraps a key per person, not per message. Texts sealed before under a subject of their own keep it: those keys are not deleted, since archived history may still hold envelopes under them.

`POST /v1/pii/erase` with a subject, or a category and value, destroys its key (`pii.erase`; by value, also the keys of the ways the value was commonly written before ids were canonical, all listed in `subjects`): every envelope for that subject, in events, approvals and archives, becomes unreadable in place, and the audit chain still verifies. It cannot be undone, so it is done by a person who confirms it with a passkey (a step-up challenge for `pii.erase` and the subject id) or an authenticator code; an API key cannot erase. The category `other`, and the organisation's free-text subject, are refused (`400`): erasing it would erase every free text, not a person. A string built from a person's data (`'ref-' + bvn`) is sealed under that person too, so it is erased with them; so is declared free text naming a known person (an email's body holding its sender's address, a note), with every copy of it. Runs still in flight for the subject must finish or be cancelled first (a run with history kept out of line, in object storage, cannot be searched and counts as holding it): while any holds it, the erasure is refused (`409`, code `runs_in_flight`, listing up to 50 of them), and with `"cancel_runs": true` it cancels them (ended at once, not reversed, each audited) and then erases. A person erased who comes back (orders again, fills in a form again) is a new subject, a new generation of the same id (`phone:…~1`): they are sealed and served as anyone new, and what was kept for them before stays erased.

What is not sealed goes with the key, in the same transaction:

- **Form submissions** whose answers or respondent hold the subject are deleted, with their files; the api then removes the files from the organisation's storage ([forms](forms.md)).
- **Kept tables** in the business's data tables: a student's rows in `school_students` and `school_term_fees` are copied again with the name `(erased)`, and a payer's entries in `money_log` get the payer `[erased]` ([kept tables](data-tables.md#kept-tables)).
- **Keyed hashes of a phone**, which whoever holds the platform's hash key could reverse by trying every number: the person's contact (how Taskiem reaches them on Telegram or WhatsApp) is deleted and their number taken off the business's request links; a rider's or supplier's row keeps its history with a hash that matches no number, and what the rider told the platform number is dropped once no business has them.

## Sign-in sessions

Each sign-in keeps a session: the hash of its token, the person, the organisation, when it began, when it expires or was revoked, the sign-in method and the client's IP address. A session is deleted, IP address and all, 30 days after it expires; a revoked session keeps its expiry, so it goes 30 days after it would have expired. The scheduler does this every hour (`taskiem_purge_sessions`, migration 00356). The audit log's record of each sign-in is not touched.
