# 0029 — Languages and voice notes: catalogues with English as the source, drafts off until reviewed, the model routes but never confirms, transcripts sealed

Date: 2026-10-08 · Status: Accepted (native-speaker review and the speech-to-text account pending L1 to L5)

## Context

Spec 11.6 adds Nigerian Pidgin, Yoruba, Hausa and Igbo to the WhatsApp conversation, and voice-note transcription, because many SME owners prefer to speak. It also says quality must be tested with native speakers before launch. No native speaker has worked on this yet, so whatever text the team writes is a draft.

Before A4, every reply was an English string literal in `api/whatsapp*.go` and `api/ussd*.go`, and commands were matched against English words in a `switch`. The AI layer (decision 0014) only drafted workflows.

The risks are specific:

- a wrong translation on an approval or a payment summary misleads someone about money;
- a model that "understands" a message could start or confirm something the person did not mean;
- a voice recording is personal data, sent to a third party.

## Decision

1. **Message catalogues keyed by id, English as the source.** `engine/lang` holds one JSON file per language, embedded in the binary. English entries carry a `context` saying where each is sent. Messages take named placeholders. A message missing from a catalogue is sent in English and reported once in the log. A test fails the build on a missing id, an extra id or different placeholders, and checks that every id the API uses exists. A wrong or missing translation therefore degrades to English, never to an empty message or a crash.

2. **Drafts are marked, off by default, and say so.** Each file and each entry has `reviewed`. Drafts have `reviewed: false` and a status beginning DRAFT. A test refuses entries marked reviewed with no reviewer named. A language is shown only when the operator turns it on for every tenant (`TASKIEM_LANGUAGES`) or a tenant turns it on for itself (`tenant_channel_settings`, `PUT /v1/languages`, `secret.manage`). A person who chooses an unreviewed language is told it is a draft. Shipping nothing until reviewers exist was rejected: the code path, tests and review process can be built and exercised now, and the drafts give reviewers something to correct rather than a blank page.

3. **A number's language is the person's; detection never overrides a choice.** The order is: chosen by command, then detected from markers and letters, then the tenant's default, then English. It is stored per number in a person-level table reached only through two definer functions, like the WhatsApp contact (`channel_languages`). A language that is not on falls back to English. USSD uses the tenant's default, because its input is digits and its two-second budget leaves no room for more.

4. **Word lists first; the model routes and never confirms.** Commands are matched against per-language phrases, prefixes and fillers, without accents or tone marks. English always matches. In another language, an unmatched message goes to the model layer with the language named (`engine/ai/intent`, under the same import rule as the rest of `engine/ai`). The structured output allows only help, status, approvals, switch, run, build and language, with a bounded argument. Yes, no and cancel are not in its schema, and the parser refuses them. The result goes down exactly the path the typed command takes, under the person's permissions, so a run still needs a summary and an explicit **yes** matched from the word lists. Calls are redacted, budgeted, rate-limited and recorded (`ai_interactions`, kind `intent`). Sending every message to the model was rejected: it would cost every tenant tokens for routine commands, and would make English behaviour depend on a model.

5. **Voice notes are a beta behind two switches.** The deployment needs a provider (`TASKIEM_TRANSCRIBE_PROVIDER`) and the tenant must turn voice notes on. One provider interface (`engine/voice`) has a fake for tests and a client for the OpenAI-compatible transcription endpoint, built from its public documentation. The same API is served by self-hosted speech servers, so an operator can keep audio inside its own network. Media come only from Meta's media host (or the Graph API's own host, for test fakes), through the egress guard. The size is capped before download when Meta reports it, and always while reading, and the file is checked against Meta's digest. Ogg duration is read from the stream's last page before anything is sent.

6. **Transcripts are personal data.** The audio stays in memory and is wiped after the call. The transcript is sealed under the tenant's subject keys (`voice_transcripts`, x-pii category `other`), kept for a configurable retention (7 days by default) so that native-speaker testers can compare what was said with what was heard (`GET /v1/languages/transcripts`, `pii.reveal`, audited), and then deleted by a definer function. That function sees only the expiry column. Transcripts are never logged. The reply echoes them only after masking. A voice note may only start something; it cannot answer a question or confirm.

7. **What stays English.** Meta-approved templates (outside the 24-hour window) are approved per language, so they are sent in the language they were approved in. Tenants' own words stay as written: workflow names, input schema prompts, USSD menus and their outcome SMS. So does the read-back of built workflows, which is generated from definitions.

## Consequences

- Every Taskiem-authored WhatsApp, USSD and SMS reply now goes through a catalogue lookup. English output is unchanged, and the existing tests pass unmodified.
- Adding a language is a pair of JSON files and a tag. Reviewing one is editing JSON (docs/languages.md).
- The model is called only for unrecognised messages in a language other than English, and only where AI is configured. On the `edge` role it is configured from the same `TASKIEM_AI_*` settings, for routing only.
- Migrations 00145 (`channel_languages`, `tenant_channel_settings`) and 00146 (`voice_transcripts`, the purge function). Boundary B10 is amended for audio from the internet and a third-party transcription provider.
- Needs people: native-speaker reviewers per language (L1 to L4), and the speech-to-text provider account and its data-processing terms (L5).
