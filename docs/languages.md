# Languages and voice notes

Taskiem can talk to people on [WhatsApp](whatsapp.md), [USSD](ussd.md) and SMS in Nigerian Pidgin, Yoruba, Hausa and Igbo as well as English, and can transcribe WhatsApp voice notes (spec 11.6, decision [0029](decisions/0029-languages-and-voice-notes.md)). This is milestone A4 of [Phase 3](phase-3-status.md).

**The translations are drafts.** They were written without native speakers and have not been reviewed. They are not accurate enough to rely on, and nothing in the product presents them as final. Every language other than English is off until an operator or a tenant turns it on, and a person who chooses one is told it is a draft. Each needs review by native speakers before launch ([needs people](needs-people.md#phase-3), L1 to L4). Voice notes are a beta, off by default.

## What is covered

| Covered | Not covered (English only) |
| --- | --- |
| Every reply in the WhatsApp conversation: help, status, run and its confirmation, inputs asked in chat (the wording around each field), builds, approvals by button, step-up hand-off and PIN messages, forms' messages and buttons, switching organisation, errors and rate limits; public menus on own numbers | Template messages outside the 24-hour window: Meta approves each template per language (`TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE`), so only the language they were approved in is sent |
| How runs started from WhatsApp ended, inside the window | Field questions generated from a workflow's input schema, and its titles and descriptions: they are the tenant's own words |
| Approval requests inside the window: their heading, expiry and buttons | The workflow steps read back after a build (`engine/wdtext`), and template parameter hints |
| Taskiem's own USSD screens (busy, ended, expired, too many requests, not taken, not available) and the reconciliation SMS | USSD menus and their outcome SMS: the tenant writes them, in any language, within the plain-ASCII rule |
| | The web app, emails and the CLI |

## How it works

**Catalogues.** Everything Taskiem says is a message id in `engine/lang/messages/<language>.json`. English (`en.json`) is the source: each entry has a `context` saying where it is sent. A message is filled with named placeholders such as `{workflow}`, which every translation must keep. A message missing from a catalogue is sent in English and logged once (`language catalogue: message missing`). A test fails the build when an English id is missing from any catalogue, when placeholders differ, or when a catalogue has an id English does not.

**Words for commands.** `engine/lang/intents/<language>.json` lists, per language:

- *phrases*: whole messages that mean a command (`wetin fail today` is `status`);
- *prefixes*: words that start one, followed by what it is about (`abeg run payroll`, `bẹ̀rẹ̀ payroll`);
- *fillers*: polite words a command may start with (`abeg`, `biko`, `don allah`);
- *markers*: words typical of the language, used to recognise it.

Messages are compared without case, accents or tone marks, so `bẹ́ẹ̀ni` and `beeni` match. English commands always work, whatever the language. Yes, no and cancel come only from these lists.

**Which language a number gets.**

1. The language it chose with `language <name>` (`language yoruba`, `èdè hausa`, `language english`), if that language is on.
2. Otherwise, the language detected from its messages: when one language other than English clearly leads, it is recorded and the reply says so, with how to change it. A chosen language is never replaced by a detected one.
3. Otherwise, the tenant's default language, if it is on.
4. Otherwise, English.

A number's language is the person's, not the tenant's (`channel_languages`, reached only through two functions). It applies in every organisation where that language is on.

**When the word lists do not match.** In a language other than English, the message goes to the [model layer](ai.md) with its language named (`engine/ai/intent`). The model may only route: help, status, approvals, switch, run, build or language, with a short argument. It can never confirm, cancel or answer yes. What it returns is then handled exactly as the typed command, with the person's permissions, so `run` still ends in a summary and an explicit **yes**. The call is redacted, counts against the tenant's AI budget, is limited per number and is recorded in `ai_interactions` with kind `intent`. Without a model, or over budget, the message is simply not understood. A build goal in another language goes to the builder with its language named.

**USSD and SMS.** USSD answers within two seconds and its input is digits, so callers get the tenant's default language. Taskiem's own SMS uses the caller's language when one is recorded. Both are folded to plain ASCII (accents and tone marks dropped, `ɗ` written `d`), because USSD screens and SMS carry the GSM 7-bit alphabet. A test keeps these texts within 160 characters.

## Turning languages on

| Where | How |
| --- | --- |
| For every tenant | `TASKIEM_LANGUAGES=pcm,yo,ha,ig` (any subset; English is always on) in the deployment's environment |
| For one tenant | `PUT /v1/languages` with `{"languages": ["yo", "pcm"], "default_language": "en"}` (`secret.manage`; audited `channel.languages.update`). `GET /v1/languages` shows what is on and each catalogue's review progress |

The default language must be English or a language that is on. Turning a language off sends that language's people English again; their choice is kept for when it is back on.

## Voice notes

A beta. A voice note (or audio message) from a person whose number is linked, in a tenant that turned voice notes on, on a deployment with a transcription provider, is:

1. looked up and downloaded from Meta (only from Meta's media host, refused above the size cap without downloading when Meta reports the size, checked against Meta's digest);
2. checked: an audio type, within the size cap, and for Ogg (WhatsApp's voice notes) within the duration cap, read from the stream itself (the largest granule position of any page);
3. sent to the provider, with the person's language as a hint where the provider takes one (not for Pidgin, which has no ISO 639-1 code);
4. read as if typed. The reply starts *"Voice notes are in beta, so please check this. I heard: "…""*, with personal data in the transcript masked.

A voice note can only start something: help, status, approvals, run, build. While Taskiem waits for an answer or a **yes**, a voice note gets *"Please type your answer"*. A run still needs a typed or tapped **yes**. Any failure asks the person to type: transcription off, too long, too large, not audio, nothing heard, or the provider down. Unlinked numbers' voice notes are never fetched. Voice notes are limited per number (five, then one every 30 seconds) and per tenant (30, then one every 2 seconds).

The audio is held in memory only and wiped after the call. The transcript is personal data. It is stored sealed under the tenant's subject keys, like inputs typed in chat (`voice_transcripts`), with refusals and failures recorded by reason only. It is never logged, and is deleted after the retention period by the WhatsApp notifier. Native-speaker testers with `pii.reveal` read transcripts with `GET /v1/languages/transcripts` (audited `voice.transcripts.read`), to compare what was said with what was heard.

| Variable | |
| --- | --- |
| `TASKIEM_TRANSCRIBE_PROVIDER` | `openai`: the OpenAI-compatible transcription API. Unset: voice notes are off |
| `TASKIEM_TRANSCRIBE_API_KEY` | The provider's key, from the deployment's Secret |
| `TASKIEM_TRANSCRIBE_BASE_URL` | `https://api.openai.com` by default; any https server speaking the same API, such as a self-hosted speech server |
| `TASKIEM_TRANSCRIBE_MODEL` | `whisper-1` by default |
| `TASKIEM_TRANSCRIBE_MAX_SECONDS` | Longest voice note taken, default 60, at most 300 |
| `TASKIEM_TRANSCRIBE_MAX_BYTES` | Largest file taken, default 1 MiB, at most 16 MiB |
| `TASKIEM_TRANSCRIBE_RETENTION` | How long sealed transcripts are kept, default `168h`, between `1h` and `2160h` |

Then turn voice notes on for each tenant with `PUT /v1/languages` and `"voice_notes": true` (409 when no provider is configured).

The provider is reached through the egress guard, to the configured host only. Before production, the operator needs a provider account and a data-processing agreement that covers voice recordings of Nigerian data subjects, with no training on the audio and a stated retention ([needs people](needs-people.md#phase-3), L5). OpenAI's guide lists mp3, mp4, mpeg, mpga, m4a, wav and webm, not Ogg; whether its endpoint takes WhatsApp's Ogg/Opus voice notes is untested (L5). A self-hosted server that takes Ogg avoids both the question and the third party.

The client was built from OpenAI's public speech-to-text guide and API reference (developers.openai.com: "Speech to text" and "Create transcription", read 8 October 2026), and the WhatsApp side from Meta's public Cloud API media documentation ("Retrieve media URL", "Download media"), under the [clean-room policy](clean-room-policy.md).

## Reviewing a language

A native speaker of the language, ideally two who check each other, reviews it.

1. Open `engine/lang/messages/<language>.json` beside `en.json`. For each entry, read the English `context` (where it is sent) and the draft. Fix the text. Keep every `{placeholder}` and every `*command*` word exactly as in English: commands are matched in English as well. Keep it short: buttons hold 20 characters, and USSD and SMS texts are folded to ASCII and must fit 160 characters (the tests check both).
2. Set `"reviewed": true` on each entry you checked.
3. Do the same for `engine/lang/intents/<language>.json`: the phrases and prefixes people would really type, and fillers and markers that are common in the language but not in English or the other three. A word must not mean a different command in English (a test checks).
4. Add yourself to `"reviewers"` with the date (`"Ada Obi, 2026-11-02"`). When every entry is reviewed, set the file's `"reviewed": true` and change `"status"` from DRAFT to a line saying who reviewed it and when. A test refuses entries marked reviewed with no reviewer named.
5. Run `go test ./engine/lang/` and open a pull request. The tester then uses the language on a test number, including voice notes, and records what failed.

Until a file is reviewed, a person who chooses the language is told: *"This translation is a draft that native speakers have not checked yet."*

## Not yet

- Templates in other languages (each needs Meta's approval per language).
- Choosing a language on USSD itself, for example a language menu before the tenant's menu.
- Field prompts from input schemas, the read-back of built workflows, and the web app.
- Streaming or longer recordings; video and documents.
