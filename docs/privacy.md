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
- **Phone**: a Nigerian mobile number, `+234`/`234`/`0` then a 70x, 71x, 80x, 81x, 90x or 91x prefix.
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

`POST /v1/pii/erase` with a subject destroys its key (`pii.erase`): every envelope for that subject, in events, approvals and archives, becomes unreadable in place, and the audit chain still verifies. Runs still in flight for the subject must finish or be cancelled first.
