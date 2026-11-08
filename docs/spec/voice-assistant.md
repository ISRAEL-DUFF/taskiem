# Customer voice assistant on WhatsApp and Telegram: gaps and build plan

Date: 2026-11-08 · Status: **for review; decisions taken (section 4), nothing built** · Owner: @EaziDeFi

*Reviewed on 2026-11-08: 32 gaps, 3 decisions and 2 people items added (marked ⊕), two sections added (3.10 knowledge, 3.11 engine, schema and rollout), milestones V2 to V8 extended and V10 added.*

A business wants an assistant its **customers** talk to on the business's WhatsApp number or Telegram bot, by text or voice note. It answers in the customer's language, remembers the conversation, replies by voice when spoken to, and, later, answers questions from the business's own records (a balance, a payment's status) without ever moving money on its own. The reference is an n8n build ("n8n Voice Assistant for WhatsApp and Telegram"), which this plan was checked against step by step.

This page lists **everything Taskiem does not have** for that, with the evidence in the code, then a build plan in milestones. It was written from a read of the repository at `0fdb46e` plus PR 154 on 8 November 2026. Nothing here is built.

## 1. What the assistant must do

| # | Behaviour | From the reference |
| --- | --- | --- |
| B1 | Receive a customer's message on a business's WhatsApp number and Telegram bot | Telegram and WhatsApp triggers |
| B2 | Reduce both to one shape: channel, conversation key, sender, text or voice, media reference | Normalize |
| B3 | Fetch a voice note's audio | TG Download Voice; WA Get Media URL, Download Media |
| B4 | Transcribe it, Pidgin included | Prep Audio, Whisper |
| B5 | Answer with a model, given the last messages of this conversation | AI Agent with Postgres memory |
| B6 | Reply by voice when the customer spoke, by text when they typed | Reply as voice?, Text to Speech, send audio |
| B7 | Send the reply on the channel it came from, inside WhatsApp's 24-hour window | send nodes |
| B8 | Serve only allowed customers, or everyone, as the business chooses | allowlist IF |
| B9 | Tell the business when it fails | error workflow |
| B10 | Cap cost and length (reply length, audio length, spend) | Step 6.4 |
| B11 | Later: read-only tools over the business's records; money moves only after a separate confirmation the model cannot skip | Step 7 |
| B12 | Be testable before going live, and debuggable after (which step failed, what was said) | Step 5, Executions |

## 2. What already exists and can be used as it is

- **Channels as connectors.** `whatsapp@1` (send text, template, media by link or media id, interactive, mark as read; inbound `messages` trigger with signature check, batch `split`, dedup on the message id, correlation on the sender) and `telegram@1` (send message, photo, document; `update` trigger with secret-token check, dedup on `update_id`, correlation on the chat id). An inbound voice note arrives as its media id (WhatsApp `trigger.item.message.audio.id`; Telegram `trigger.body.message.voice.file_id`), never its bytes.
- **Branching, loops, waiting.** `branch`, `foreach`, `wait`, `signal` (a run can wait for the same sender's next message by correlation), `on_error`.
- **One conversation at a time.** `settings.concurrency_key` with `max_concurrency` (`engine/runtime/concurrency.go`): keyed on the sender, a second message waits behind the first, FIFO.
- **Composition.** `subflow` steps and callable workflows (decisions 0048, 0049): two entry workflows can call one shared assistant workflow with no extra polling hop.
- **The `ai` step** (decision 0050): one prompt, typed JSON answer, Claude by default, personal data masked with placeholders put back, the tenant's monthly AI budget, the owner's switch, the plan's `ai` feature.
- **Tables** through `xenibase@1`: insert, read with filters, `order desc` and `limit`, delete by filter. Enough to keep the last N messages per conversation.
- **List blocks** (PR 154): `groupBy`, `joinBy` and friends for shaping history and records.
- **Pieces inside Taskiem's own chat that a workflow cannot reach yet:** media download from Meta (`engine/whatsapp/media.go`), Telegram `getFile` (`engine/telegram/client.go`), speech-to-text (`engine/voice`: OpenAI-compatible provider, size and Ogg duration caps, sealed transcripts with retention), language detection (`engine/lang.Detect`), media storage (`engine/media`, Xenibase buckets, signed URLs), malware scanning (ClamAV).
- **Failure alerts** to owners by WhatsApp, Telegram or email without any rule; alert rules by email, Slack, WhatsApp, webhook.
- **Fast enough engine path.** Runs start inside the webhook request and workers wake on `NOTIFY`; measured on a dev machine, a short run with three fake provider calls finishes at p95 257 ms (`docs/performance.md`). Model, transcription and speech calls will dominate.

## 3. Everything missing

Each gap says what is missing, the evidence, and how much it matters: **Blocker** (the assistant cannot work without it), **Launch** (needed before a real customer uses it), **Later**.

### 3.1 Channels

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| C1 | `whatsapp@1` cannot fetch inbound media (media id → URL → bytes) | Actions are `get_phone_number`, `list_templates`, `send_text`, `send_template`, `send_media`, `send_interactive`, `mark_as_read` (`connectors/whatsapp/manifest.yaml:30-187`) | Blocker |
| C2 | `whatsapp@1` cannot upload media (`POST /{phone_number_id}/media`); `send_media` takes only a public link or an existing media id | `whatsapp.go:249-280` | Blocker for voice replies |
| C3 | `whatsapp@1` cannot send a voice note (`"voice": true`, OGG/Opus); audio goes out as an audio file | audio object carries only `link` or `id` | Launch |
| C4 | `telegram@1` has no `getFile`/download | `connectors/telegram/telegram.go:47-58` | Blocker |
| C5 | `telegram@1` has no `sendVoice` or `sendAudio`, and no multipart upload (photo and document by URL or file id only) | same | Blocker for voice replies |
| C6 | No typing or "recording" indicator on either channel (WhatsApp typing indicator, Telegram `sendChatAction`) | not in either connector | Launch (a 5-second silence reads as broken) |
| C7 | The `telegram@1` `update` trigger is not registered with Telegram on publish; someone must call `set_webhook` | not `registration: remote` (decision 0021); `docs/integrations/telegram.md:41-45` | Launch |
| C8 | The `whatsapp@1` trigger is registered by hand in Meta's App Dashboard (callback URL, verify token, `messages` field); nothing guides an owner, and readiness does not flag it | `docs/integrations/whatsapp.md:50-56`; `needs_developer` only for `webhook` triggers (`api/templates.go:121`) | Launch |
| C9 | Readiness does not check a `connector_event` trigger's own connection (only steps' connections) | `api/readiness.go:161-203, 272-276` | Launch |
| C10 | A number connected to Taskiem's own channel (own numbers, public menus) cannot also feed `whatsapp@1`: Meta allows one callback URL per app, and the two paths are separate (`/channels/whatsapp/n/<id>` vs `/hooks/<tenant>/connectors/whatsapp@1/messages`) | `engine/whatsapp/graph.go:1-5`; no bridge into `connector_event` | Decision (D1) |
| C11 | Taskiem's shared platform number and platform bot cannot serve a business's customers: non-members get STOP/START or the public menu (own number only), and free text or voice never starts a business's workflow | `api/whatsapp_chat.go:460-542`; `api/whatsapp_public.go:94-120`; `docs/updates.md:49-53`; decision 0039 | Decision (D1) |
| C12 | The `whatsapp` trigger type in wd/v1 is an enum value with no config, no ingest path and no runs | `schemas/wd-v1.schema.json:1449`; nothing starts a run with it | Clean-up or build (D1) |
| C13 | No 24-hour-window tracking for a tenant's own `whatsapp@1` connection; a reply after the window fails with Meta's 131047 as fatal; no fall-back to a template | `whatsapp.go:113-135`; window tracking exists only for the platform channel (`engine/whatsapp/platform.go:301-325`) | Launch |
| C14 | No template submission or status sync for a tenant's own WhatsApp account (only `list_templates`) | manifest; registry is Taskiem's own | Later (the assistant only answers) |
| C15 | Telegram's 1 message per second per chat is not modelled | `telegram@1` rate limit is 30/s per connection only | Later |
| C16 | No connector fixture or test of an inbound voice note on either channel; `docs/integrations/whatsapp.md:69,73` still describes the pre-`split` payload | | Launch (docs fix is small) |
| C17 ⊕ | A connection without `app_secret` (WhatsApp) or `webhook_secret` (Telegram) receives nothing: every delivery is refused as unsigned (`engine/connector/webhook.go`), which is safe, but both fields are optional and nothing tells the owner why the assistant is silent | `connectors/whatsapp/manifest.yaml:22`; `connectors/telegram/manifest.yaml:16` | Launch (readiness) |
| C18 ⊕ | The WhatsApp trigger also delivers `status.*` events and Telegram every update kind (edited messages, channel posts, callback queries, group messages); nothing in a recipe yet limits the assistant to new messages in private chats, so a bot added to a Telegram group would answer everyone | `events` on `connector_event`; Telegram `update` | Launch |
| C19 ⊕ | No defined handling for message kinds other than text and voice: image, video, document, sticker, location, contact, reaction, button reply, unsupported. Each needs a fixed answer or a silent drop, decided per kind | | Launch |
| C20 ⊕ | Replies longer than a channel allows (4,096 characters on both) are refused, not split | `send_text`/`send_message` limits | Launch |
| C21 ⊕ | A media download must happen soon: Meta's media URLs last minutes and Telegram's file links an hour, and Telegram bots cannot fetch files over 20 MB. A run that waits in a queue (concurrency, backlog) can find the voice note gone | provider rules | Launch (download first, store, then queue) |
| C22 ⊕ | A number on the WhatsApp Cloud API cannot be used in the WhatsApp Business app at the same time unless Meta's coexistence applies to it, so staff who answer from the app on their phone lose that number. Owners must be told before connecting | Meta's rules; not in any Taskiem doc | Launch (guide, decision) |
| C23 ⊕ | No watch on the number's quality rating or messaging limit; a business whose assistant annoys people can be throttled by Meta without knowing | `get_phone_number` returns `quality_rating`; nothing reads it on a schedule | Later |

### 3.2 Files and audio inside a run

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| F1 | No file reference type in wd/v1: forms and email attachments carry `{id, name, type, size, sha256}` with their own `file_url` actions, but there is no shared kind a step can pass to another | `docs/forms.md:64`, `docs/email.md:41,50` | Blocker |
| F2 | No place a workflow can store a file it made or fetched (audio in, audio out); `engine/media` is used by storefront, forms, chat only | `engine/media/media.go` | Blocker |
| F3 | Step outputs are capped at 256 KB inline and the object-storage copy (`payload_ref`) is not built; a 30-second voice note in base64 is near or over the cap | `worker.go:605-633`; spec 4.9 | Blocker unless audio travels by reference (F1, F2) |
| F4 | The `http` step sends JSON only and reads at most 256 KB as text; no multipart, no binary, so it cannot call a speech API as a fall-back | `engine/runtime/httpstep.go:39-80` | Not needed if T1 and T2 are built |
| F5 | No scanning, type or duration check for audio a workflow stores (ClamAV is wired for forms and email; Ogg duration is checked only in the chat path) | `cmd/taskiem/forms.go:17-30`; `engine/voice` | Launch |
| F6 | No retention for files a workflow stores (voice notes are personal data) | | Launch |
| F7 ⊕ | No audio conversion. Speech providers return MP3 or Opus in various containers; a WhatsApp voice note must be OGG/Opus mono (and plays inline only under 512 KB), a Telegram voice note OGG/Opus. Taskiem has no transcoder (no ffmpeg; the sandbox runs JavaScript and Python in WebAssembly) | `engine/sandbox`; no `ffmpeg`/`opus` code | Blocker for voice replies unless the provider emits OGG/Opus mono |
| F8 ⊕ | Where stored audio lives (Xenibase storage region) is not stated; voice is personal data of Nigerian subjects | `docs/media.md` | Launch (people + docs) |

### 3.3 Speech

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| T1 | No step or connector action that transcribes; `engine/voice` is imported only by `api/` and `cmd/` | `api/whatsapp_voice.go`, `api/languages.go` | Blocker |
| T2 | No text-to-speech anywhere: no provider, no interface, no configuration | searched: tts, speech synthesis, `/audio/speech` | Blocker for voice replies |
| T3 | Transcription is not metered or counted against any budget (only rate-limited and logged); speech would not be either | `voice_transcripts` only | Launch |
| T4 | No speech provider under a data-processing agreement; whether OpenAI accepts WhatsApp's Ogg/Opus is untested | `docs/needs-people.md` L5; `docs/languages.md:82` | Launch (people) |
| T5 | Pidgin has no speech language hint (the provider guesses); Yoruba, Hausa, Igbo transcription and speech quality are unknown; no recordings to test with | `engine/lang/lang.go:44-58` | Launch (people) |
| T6 | No voices chosen or tested for Nigerian English or Pidgin; no way to pick one per business | | Launch |
| T7 | The tenant's `voice_notes` switch covers Taskiem's chat only; nothing governs speech in workflows | `api/languages.go:352` | Launch |
| T8 ⊕ | No fixed text for "I could not hear that" (empty, too long, unreadable audio, provider down), in the five languages | | Launch |

### 3.4 The model

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| A1 | The `ai` step sends one user message; it cannot take a conversation (turns), though `engine/ai` supports `[]Message` | `engine/runtime/aistep.go:189-194, 267-277` | Launch (history can be packed into the prompt meanwhile) |
| A2 | No per-step reply length (max tokens is fixed at 8192 by the worker), temperature or system prompt; the system prompt is a constant | `aistep.go:49-56, 157-159`; `serve.go:1456` | Launch (cost and spoken length) |
| A3 | No tool calling | nothing in `engine/ai` | Not needed (D4: fixed routing instead) |
| A4 | Text only: no audio or image input to the model | `engine/ai/anthropic.go` | Later |
| A5 | Masking catches phones, emails, BVN, NIN and cards in free text, but not names or 10-digit account numbers; history read from a table may not be sealed as known personal data (`pii.sealAt` seals scalars, not row objects) | `engine/pii/detect.go:42-69, 331`; `engine/pii/pii.go:211-213` | Launch (decide what the model may see, D6) |
| A6 | The default model is the building model (`claude-opus-5-5`, effort high); a quick spoken reply wants a small fast model, which needs `TASKIEM_AI_STEP_MODELS` set in each deployment and named in the recipe | `engine/ai/ai.go:170, 241, 264-268` | Launch |
| A7 | Building, intent routing and ai steps share one monthly token budget; an assistant could starve the builder | `engine/runtime/limits.go:632` | Launch (D7) |
| A8 | `ai_monthly_tokens` of 0 means "no check", not "none"; Shop plans lack the `ai` feature; only Enterprise has AI overage; `docs/operations.md` and `docs/ai.md` disagree on what happens over budget | `aistep.go` (`if limit > 0`); `deploy/plans.yaml` | Launch (decide and fix docs) |
| A9 | No language detection a workflow can use (`lang.Detect` is chat-only), so "answer in the customer's language" rests on the prompt alone | `engine/lang/intents.go:306-350` | Launch |
| A10 | Each ai step takes two database transactions and a budget reservation before the call, and up to two model rounds; the latency of a voice round trip has never been measured | `aistep.go:163-188` | Launch (measure) |
| A11 ⊕ | Prompt injection: a customer's message is instructions to the model as much as the owner's prompt. With no tools (D4) the harm is limited to what the model says, but it can still be talked into promises (prices, refunds) the business never made | | Launch (reply policy, L6) |
| A12 ⊕ | When the AI budget runs out, is switched off, or the provider is down, the step fails and the customer hears nothing; there is no fall-back answer without a model | `aistep.go` failure kinds | Launch |
| A13 ⊕ | The AI builder and its evaluation suite know nothing of assistants: "make me a WhatsApp assistant" will not retrieve the recipe or draft one sensibly | `engine/ai/builder`, `tools/aieval` | Later |

### 3.5 Memory and state

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| M1 | No conversation memory for workflows; Taskiem's `chat_history` and `chat_sessions` belong to members' chat and are not readable by workflows | `api/chat_history.go`; migration 00295 | Blocker (a table works, M2) |
| M2 | No known table shape for conversations (`templates/tables/` has none) | | Blocker for the recipe |
| M3 | No expiry for table rows; old messages stay until a scheduled workflow deletes them | `docs/data-tables.md` | Launch |
| M4 | No key-value state across runs (last language, last channel, "awaiting PIN") apart from tables | expression roots `trigger, steps, run, env, secrets, item, index` | Later (a table works) |
| M5 | A table's message text is not declared personal, so a conversation kept in a table is readable in clear by anyone who can read tables | table columns are personal only for `phone` and `email` by default | Launch |
| M6 ⊕ | Conversation rows and run history have different lifetimes (table rows until deleted, runs by plan), so erasing or expiring one leaves the other | | Launch (one retention rule, V5) |

### 3.6 Privacy, consent and records

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| P1 | The `whatsapp@1` and `telegram@1` triggers declare no `pii`: message text, profile names, Telegram chat ids and usernames are kept in clear in run history (the sender's phone is caught by the detector) | `connectors/whatsapp/manifest.yaml:186-211`; Instagram declares them (`connectors/meta/instagram.yaml:224-228`) | Launch |
| P2 | No erasure of everything about one customer (messages sealed as `other` are their own subjects, not erased with the phone) | `engine/secrets/subjects.go:26-40` | Launch |
| P3 | No consent rule for recording and processing a customer's voice, no notice wording, no opt-out word for the assistant | `docs/compliance.md` covers reports and anchors only; decision 0039 defers a business's own bot | Launch (people + code) |
| P4 | Model provider terms and whether prompts may leave Nigeria are open (AI1); the speech provider's too (L5); terms and DPA under the NDPA (P4-B4) | `docs/needs-people.md:132-135, 156` | Launch (people) |
| P5 | No conversation view: a business sees what its assistant said only by opening each run; Taskiem keeps no message to a customer by design ("A message to a customer … is never kept") | `docs/whatsapp.md:332-337`; `Run.tsx` | Launch |
| P6 | Starter keeps run history 7 days; with no archive configured nothing is purged; retention of transcripts in workflows is undefined | `deploy/plans.yaml`; `docs/operations.md:75` | Launch (decide) |
| P7 ⊕ | The threat model has no entry for customer-facing chat (spoofed senders, a customer reading another's data, prompt injection, voice replay) | `docs/security/threat-model.md` | Launch |

### 3.7 Limits, cost and abuse

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| L1 | No limit per customer for connector triggers: one sender can start unlimited runs (ingest limits are per tenant; per-number limits exist only on Taskiem's channel) | `docs/operations.md`; `docs/whatsapp.md:316` | Launch |
| L2 | `max_running_runs` counts runs waiting on a signal: Starter 5, Growth 25. A design that waits for the customer's next message holds a slot per customer | `docs/operations.md` plan limits | Launch (D3) |
| L3 | No audio length check before transcription in workflows (the chat path has one) | | Launch |
| L4 | No cost view: AI tokens are counted, transcription and speech are not, WhatsApp sends on a tenant's own app are not counted by Taskiem | | Launch |
| L5 | No debounce: a customer who sends three short messages gets three answers | | Later |
| L6 | No moderation of what the assistant says to customers (moderation covers storefronts) | `docs/moderation.md` | Launch (D6) |
| L7 ⊕ | A reply send is an `unsafe_write`: if the answer from Meta or Telegram is lost, the step parks for an operator and the customer gets nothing, possibly for hours. A chat reply needs a different rule (retry once and accept a rare duplicate, or check the message id) | `docs/contracts/action-classes.md`; `send_text` class | Launch (D11) |
| L8 ⊕ | Queued runs count against `max_queued_runs`; a busy assistant with one conversation at a time per sender can fill the backlog and get 429 for everyone in the tenant | `docs/operations.md` | Launch |
| L9 ⊕ | No capacity number for many conversations at once: each message is five to eight steps with three slow calls; the engine's measured knee is about 20 runs per second on a dev machine | `docs/performance.md:148` | Launch (measure) |
| L10 ⊕ | No alert at a share of the allowance (80 %, 100 %) for AI tokens and speech; "plan limit reached" fires only at the limit | `docs/alerts.md` | Launch |
| L11 ⊕ | No kill switch: an owner cannot pause the assistant for everyone at once without unpublishing workflows, and Taskiem cannot pause it for a tenant | | Launch |

### 3.8 Identity and money (the "fintech tools")

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| I1 | No way to map a customer's phone or Telegram chat to their record in the business (account, wallet, invoices) apart from a table the business keeps | `invoices@1` has no lookup by phone; `list` returns no customer details | Blocker for tools |
| I2 | No capability for "this customer's balance" or "this customer's recent transactions": `balance` is the business's or a wallet's; `list_credits` is the business's | `schemas/capabilities-v1.json`; `iswallet` and `mono` have raw `list_transactions` | Blocker for tools |
| I3 | No PIN or step-up for people who are not members; the only check for an outsider is a form's one-time code, and the form link goes out by SMS or email, not WhatsApp | `engine/wd/form.go:57`; WhatsApp PIN is members' only | Blocker for any write |
| I4 | Areas gate `invoices@1`, `stock@1`, `storefront@1` steps when a part is off; an assistant that reads them depends on those parts being on | `engine/areas/areas.go:216-231` | Launch (readiness should say so) |

### 3.9 Building, recipes, field agents

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| R1 | A template is one workflow; the assistant is at least three (WhatsApp entry, Telegram entry, shared assistant) plus tables; there is no bundle | `templates/templates.go:141-188` | Blocker for "one recipe" |
| R2 | A callable template needs run inputs; event-started workflows are not callable | decision 0047 point 1 | Shapes R1 |
| R3 | No `assistant` (or similar) template category | free string; none in use | Later |
| R4 | Field agents cannot install templates or manage connections today; areas phase 6 is in review | `docs/agents.md:32-41` | Launch if agents set it up |
| R5 | The plain builder edits only the `ai` step's prompt and `personal_data`; input and output schema are JSON under Advanced; `on_error` is JSON | `web/src/canvas/StepPanel.tsx:569-645` | Launch for owners |
| R6 | New step kinds (speech, file) need a catalogue entry, a look and a panel each | `catalog.ts`, `look.ts`, `StepPanel.tsx` | with each step |
| R7 | Workflow tests take JSON mocks only and do not run a connector's `split`/`dedup`; there is no check that a hand-written trigger sample matches the manifest | `engine/wdtest/wdtest.go:51, 71-80` | Launch |
| R8 | No end-to-end test of a real voice round trip (Meta, Telegram, speech, model) and no latency number for one | `docs/performance.md` | Launch |
| R9 | No guide for owners or field agents (Meta app, callback URL, Telegram bot, what customers are told) | | Launch |
| R10 ⊕ | No way to try the assistant before going live except from a real phone: no "talk to it here" in the web, no test sender | | Launch |
| R11 ⊕ | Fixed customer-facing texts (notice, limit reached, could not hear, opted out, closed window) need catalogue entries in five languages, drafts until reviewed | `engine/lang/messages` | Launch |

### 3.10 What the assistant knows ⊕

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| K1 ⊕ | Nothing gives the model the business's own facts: opening hours, address, prices, products, policies. The reference guide answers general questions only; a business assistant that cannot say when the shop opens is not useful | no retrieval in `engine/ai` or the `ai` step | Blocker |
| K2 ⊕ | The facts already exist in Taskiem in several places (storefront card and hours, catalogue tables, fronts, the business's settings) but no step reads them as one "what the business is" summary | `engine/storefront`, data tables | Launch |
| K3 ⊕ | No place for the owner to write answers to common questions (FAQ), returns policy, delivery areas | | Launch |
| K4 ⊕ | The prompt is capped at 256 KB, and a website plus files plus a catalogue will not fit; no search to send only the passages that answer the question | `maxAIQuestion` (`aistep.go:300`) | Blocker once outside sources are in (D12): search moves into the first release |
| K5 ⊕ | No "hand me to a person": the customer cannot ask for a human, and no member is told to take over | | Launch |
| K6 ⊕ | No way to read the business's own website: no page fetcher for content (only verification fetches for embed sites), no HTML-to-text, no refresh. The site must be one the business has proven it owns (embed sites already verify origins by DNS or file) | `engine/embedsites` verifies origins only | Launch (D12) |
| K7 ⊕ | No text extraction from files: `engine/pdf` writes PDFs and reads none; no DOCX or spreadsheet reading; a scanned menu needs OCR, and the `ai` step takes no images (A4) | `engine/pdf/pdf.go` | Launch (D12; OCR later) |
| K8 ⊕ | Google Sheets can be read (`googlesheets@1` `get_values`); Google Docs and Drive files cannot (the Google connection asks only for `drive.file`, and there is no Drive or Docs connector) | `connectors/googlesheets/manifest.yaml:38` | Launch for Sheets; Drive and Docs need a connector |
| K9 ⊕ | Connected systems: items and prices from books exist as the `books_items` capability (QuickBooks, Zoho Books); there is no connector for shop platforms (Shopify, WooCommerce, Jumia) | `schemas/capabilities-v1.json:603` | Launch for books; shop platforms later |
| K10 ⊕ | No knowledge store: nowhere to keep what each source said as text, split into passages, with its source, when it was read, and a way to delete it; no owner review before a new or changed source is used | | Blocker (D12) |
| K11 ⊕ | Text from outside (a website, a file) can carry instructions aimed at the model; it must be treated as quoted material, never as instructions, and checked like a customer's message | | Launch |

### 3.11 Engine, schema and rollout ⊕

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| E1 ⊕ | Every new wd/v1 field (file references, ai `history`, `max_tokens`, `system`) must reach the JSON schema, the Go model and validator, the TypeScript SDK and its compiler, `wdtext` read-back, `wdcheck`, the AI builder's rules and the canvas; and stay deterministic for replay | `schemas/wd-v1.schema.json`, `engine/wd`, `sdk/src/wd.ts`, `engine/wdtext`, `engine/ai/builder` | Blocker (with each change) |
| E2 ⊕ | Moving media download and transcription out of `api/` (D1) touches the members' voice-note path, which has its own tests and limits; it must keep working unchanged | `api/whatsapp_voice.go` | Launch |
| E3 ⊕ | ~~A new business area (D10)~~: dropped, the assistant is plain workflows (Q3) | | Not needed |
| E4 ⊕ | No staged rollout: no per-tenant beta flag for assistants and no platform-wide off switch for speech or assistant runs | | Launch |
| E5 ⊕ | No metrics for assistants: round-trip time per channel, transcription and speech time, failures by kind, cost per conversation | `engine/telemetry` | Launch |

## 4. Decisions to take before building

| # | Decision | Options | Recommendation |
| --- | --- | --- | --- |
| D1 | **Which door customers come in by** (decided: own app and bot, Q2) | (a) the tenant's own `whatsapp@1`/`telegram@1` connection (own Meta app, own bot), as workflows; (b) extend Taskiem's own channel (own numbers already download media, transcribe, track the 24-hour window) with an "assistant" mode that starts a business's workflow; (c) both | (a) for the first release: it is the workflow product, it needs no Meta business verification by Taskiem (W1), and it keeps each business's number its own. Move the media, speech and window pieces out of `api/` into packages both paths use, so (b) can follow. Remove or define the dead `whatsapp` trigger (C12) |
| D2 | **One recipe or several** (decided: three templates, one callable) | (a) a template bundle (new); (b) one entry workflow per channel, each a template, calling a callable assistant template; (c) one template per channel with the assistant inlined | (b): no new template concept, and the assistant logic is written once. Callable templates need run inputs (R2), which the shared assistant has |
| D3 | **How a conversation continues** (decided: one run per message) | (a) one run per message, memory in a table; (b) one long run per conversation waiting on the next message by `signal` | (a): a waiting run holds a `max_running_runs` slot (L2), and a run per message is simpler to see, retry and erase. Serialise per sender with `concurrency_key` |
| D4 | **Tools** (decided: fixed routing) | (a) model tool calling (new in `engine/ai` and wd); (b) the model classifies the request into a fixed list, a `branch` runs the step for it, a second ai step words the reply | (b) for the first release: identity always comes from the trigger, never the model, and no step moves money because a model said so. Revisit (a) only with a design review |
| D5 | **Speech provider** (decided: OpenAI-compatible, account chosen with the DPA) | OpenAI (transcription and speech), a self-hosted OpenAI-compatible server, others | Keep the `engine/voice` interface (OpenAI-compatible) and add speech to it; the provider is chosen with the DPA (P4, L5). Test Ogg/Opus first |
| D6 | **What the model sees and says** (decided: message through, ids masked, replies checked) | masked by default (names not caught) vs letting the message through by `personal_data` | Customers' own messages must reach the model to be answered: let `text` through by `personal_data` (audited), keep phones and ids as placeholders, never send a customer's record fields unless a tool step put them there. Add a short refusal policy to the prompt and a check on the reply (L6) |
| D7 | **Whose budget** (decided: a separate assistant allowance) | share the tenant's AI budget; a separate assistant budget; per-message pricing | A separate monthly allowance for assistant runs (tokens plus speech seconds), so the builder is never starved (A7) |
| D8 | **Money** (decided: never moves from a conversation in this plan) | read-only tools first; writes later behind a non-member confirmation (I3) | Read-only only in this plan. Writes need their own plan |
| D9 | **Who sets it up** (decided: owners now, field agents later) | owners, field agents, Taskiem staff | Field agents, once phase 6 or a narrower permission exists (R4); owners with a guide meanwhile |
| D11 ⊕ | **A lost answer to a reply send** (L7) (decided: retry once, chat replies only) | park for an operator (today); retry once and accept a rare duplicate; look the message up first | Retry once on a lost answer for chat replies only, marked in the step; never for money or templates |
| D12 ⊕ | **Where the business's facts come from** (K1 to K3, K6 to K11) (decided: knowledge sources, inside Taskiem and outside it) | the storefront and catalogue only; a new "About my business" page with FAQ; both | **Knowledge sources** (answered: "the data can come from Taskiem or externally"). Inside Taskiem: the storefront card, hours and catalogue, data tables the owner picks, and an FAQ page. Outside: the business's own website, uploaded files (PDF, documents), Google Sheets and Drive, and other systems through connectors (books items, later shop platforms). Every source is read into one store the assistant searches, refreshed on a schedule, and shown to the owner before it is used |
| D13 ⊕ | **Hand-over to a person** (K5) (decided: a word plus the model's offer) | none; a word ("agent", "person") that pauses the assistant for that customer and tells members | The word, plus the model may suggest it when unsure; pausing is per customer, resumed from the conversations page |
| D10 | **Where it sits in What your business uses** (decided: plain workflows, Q3) | always on; a new area "Assistant" | Plain workflows, no area. Consent and cost are covered by the AI and voice switches, the beta flag, consent at first contact and the owner's pause |

## 5. Build plan

Milestones in order. Each lists what is built, its tests and docs. Nothing in a later milestone is needed by an earlier one. People items are in section 6.

### V0. Decisions and clean-up

- Record D1 to D13, all decided on 2026-11-08, as decision 0062 ("Customer assistant").
- Fix stale docs: `docs/integrations/whatsapp.md` (one event per delivery, `entry[0]`), `docs/operations.md` vs `docs/ai.md` on AI over budget.
- Remove the dead `whatsapp` trigger type from wd/v1, or define it (D1).

### V1. Files in runs

- A **file reference** kind in wd/v1: `{file: <id>, name, type, size, sha256}` in trigger bodies and step outputs, never the bytes (F1).
- A **run files** store on `engine/media`: private bucket, per tenant, signed links on request, a retention per workflow capped by plan (F2, F6), sealed as personal data when it came from a person.
- Type, size and (for Ogg) duration checks on store; ClamAV for anything a person sent (F5).
- `files@1` platform connector: `link` (a signed URL for a reference, minutes long), `delete`.
- Tests: a file stored by one step and read by another; expiry; refusal over size; erase.
- Docs: wd-v1 contract "Files".

### V2. Channel actions

- `whatsapp@1`: `get_media` (id → file reference: URL lookup then download from Meta's host only, size-capped, sha256 checked; reuses `engine/whatsapp/media.go`), `upload_media` (file reference → media id), `send_media` gains `voice: true` for OGG/Opus audio, `send_typing` (C1, C2, C3, C6).
- `telegram@1`: `get_file` (file id → file reference), `send_voice` and `send_audio` by file reference (multipart), `send_chat_action` (C4, C5, C6).
- `telegram@1` `update` trigger registered on publish (decision 0021's remote registration) (C7).
- Triggers declare `pii`: sender ids, names, text, media ids (P1).
- Readiness checks a `connector_event` trigger's connection, flags "set the callback URL in Meta" with the exact URL and verify token, and refuses an assistant without `app_secret` or `webhook_secret` (C8, C9, C17).
- Long replies split at sentence ends under each channel's limit (C20).
- Retry once on a lost answer for chat reply sends, behind a step setting (L7, D11).
- Fixtures: inbound voice note on each channel; tests of each new action against recorded shapes.

### V3. Speech steps

- `engine/voice` gains a speech (text-to-speech) interface and an OpenAI-compatible provider; configuration `TASKIEM_SPEECH_*` (T2).
- `speech@1` platform connector: `transcribe` (file reference → text, language guess; caps on length and size; sealed transcript; retention) and `speak` (text → file reference, OGG/Opus or MP3, voice from a short allowed list) (T1, T2, T6, L3).
- Metering: seconds transcribed and characters spoken per tenant, counted like AI tokens, with a monthly allowance (T3, L4).
- A tenant switch "Voice in workflows", separate from the chat's voice notes (T7).
- Output OGG/Opus mono from the provider where it can; otherwise a transcoder (a vetted static encoder run in the container step runner, or a provider that emits Opus), decided by a spike before V3 (F7).
- Fixed answers for audio that cannot be heard, in five languages (T8, R11).
- Tests with the fake provider; a recorded Ogg/Opus sample through the real provider in a nightly check (T4).

### V4. The model step, made fit for conversation

- `ai` step gains `history` (a list of `{role, text}` turns, capped), `max_tokens`, and a `system` instruction (kept under Taskiem's own fixed rules) (A1, A2).
- A `language` expression function over `lang.Detect`, so a workflow knows the customer's language before the model answers (A9).
- A small, fast model offered for ai steps by default in deployments that run assistants (A6).
- Separate assistant allowance from the builder's budget (A7, D7); fix "0 means no check" for plans without AI (A8).
- A fall-back answer when the model cannot answer (budget, switched off, provider down) (A12).
- Measure: time from webhook to reply, text and voice, on staging with real providers, and a capacity run with many conversations at once; record both in `docs/performance.md` (A10, R8, L9).
- Each wd/v1 change through schema, Go, SDK, read-back, checks, builder rules and canvas, with replay tests (E1).

### V5. Memory and the conversations table

- Known table `conversations`: conversation key, channel, sender, role, text (personal), language, created_at (M2, M5).
- Row expiry for tables (days to keep, enforced by a sweep), set to 30 days for this table by default (M3, Q4).
- History read as sealed values when the column is personal, so it is masked before the model like other personal data (A5).
- One retention rule for a conversation: rows, transcripts, files and runs expire together, 30 days by default, changeable by the owner within the plan's limit (M6, Q4).
- Tests: last N turns per conversation in order; expiry; erasure of a customer's rows with their phone (P2).

### V5b. Knowledge sources (D12)

- A **knowledge store** per business: each source's text split into passages with the source, the time it was read and a link back; replaced whole on each refresh; deleted with the source (K10).
- **Search** over the passages (Postgres full-text first, in Taskiem's database under forced row-level security), so a question sends only the passages that answer it (K4).
- `knowledge@1` platform connector: `search` (question → passages with their sources) for the assistant, `refresh` for a source.
- Sources inside Taskiem: the storefront card, hours and catalogue; data tables the owner picks; an **About my business** page with FAQ and policies the owner writes (K1 to K3).
- Sources outside:
  - **The business's website**, only an origin it has verified (embed-site verification), fetched through the egress guard, a page limit, HTML to text, refreshed daily (K6).
  - **Files**: PDF and DOCX text extraction from files the owner uploads or sends by chat; scanned documents refused with a plain message until OCR exists (K7).
  - **Google Sheets** through `googlesheets@1`; Drive and Docs once a Drive connector exists (K8).
  - **Connected systems**: items and prices through the `books_items` capability (K9).
- The owner sees what each source gave before it is used, and when it last changed; a source that fails to refresh keeps its last text and alerts once.
- Passages from outside are quoted to the model as material, never as instructions, and the reply check applies to anything they lead to (K11).
- Tests: each source kind against fixtures; refresh replaces; deletion removes; search finds the right passage; a page with injected instructions does not change what the assistant does.

### V6. Guard rails for customers

- A per-sender limit for connector triggers (messages per minute and per day, set on the workflow), with a fixed polite reply when exceeded (L1).
- Opt-in and notice: the first message to a new sender says it is an assistant, that voice notes are transcribed, and how to stop; `STOP` ends the assistant for that sender (P3). Wording reviewed by counsel (people).
- 24-hour window tracking per connection and sender, and a "window closed" outcome a workflow can branch on (C13).
- A reply check before sending: length, no placeholders left, nothing from the business's internal fields (L6).
- One erase action for a customer across messages, transcripts, files and the conversations table (P2).
- Hand-over: a word that pauses the assistant for that customer and tells members (K5, D13).
- Only new messages in private chats start the assistant; other message kinds get the fixed answer chosen for each (C18, C19).
- Download the media in the first step, before anything that can queue (C21).
- Owner's pause for everyone and a platform off switch; per-tenant beta flag (L11, E4).
- Alerts at 80 % and 100 % of the assistant allowance; a cap on queued runs per assistant so it cannot fill the tenant's backlog (L8, L10).
- Threat model entries for customer chat (P7, A11).

### V7. Conversations page

- A page per assistant: conversations by customer (masked until revealed with `pii.reveal`, audited), each with its turns, transcripts and the run behind each reply; links to the run inspector (P5).
- Owner can pause the assistant for one customer and answer by hand from the page (a send through the same connection).
- **Talk to it here**: a test conversation in the web against the draft, with a fake sender, before going live (R10).
- Assistant metrics on the page: replies, time to reply, failures, cost this month (E5).

### V8. The recipe

- Category `assistant`. Three templates: `assistant-whatsapp`, `assistant-telegram` (entry: normalise, allow-list, per-sender limit, voice → transcript, call the assistant, reply by voice or text) and `assistant-answer` (callable: history, model, reply, write turns) (R1, R2, R3, D2).
- Tables made on instantiate: `conversations`, `assistant_allowed` (optional allow-list).
- Readiness covers connections, the Meta callback, the Telegram webhook, the speech and AI switches and allowances.
- Workflow tests for each: text in/text out, voice in/voice out, photo dropped, stranger blocked, over the limit, window closed, model failure answered politely and alerted.
- Builder: catalogue entries and plain panels for transcribe, speak, send voice, typing (R5, R6).
- Guides: "Answer your customers on WhatsApp and Telegram" for owners and field agents, with Meta and BotFather steps, coexistence with the WhatsApp Business app, and what customers must be told (R9, C22).

### V9. Read-only tools (after launch: the first customer is a shop, Q1)

- A `customers` link: phone or chat id → the business's customer record (I1).
- Capabilities `customer_balance` and `customer_transactions` where a rail can answer them; invoices `find_by_phone` (I2).
- The assistant's fixed routing (D4): "balance", "payment status", "recent", "something else" → a step each, identity from the trigger only.
- Area checks surfaced at readiness (I4).

### V10. After launch

- Quality-rating watch for WhatsApp numbers (C23).
- The AI builder and evaluation suite learn assistants (A13).
- OCR for scanned files and photos of menus (K7, A4); a Drive and Docs connector; shop-platform connectors (K8, K9).
- Better search (embeddings) if full-text search is not good enough on real questions (K4).
- Debounce several quick messages into one answer (L5).

### Not in this plan

- Money moving from a conversation (needs a non-member confirmation design, I3, D8).
- Model tool calling (A3), image or audio input to the model (A4), streaming replies.
- Taskiem's own channel serving businesses' customers (D1 option b), template submission for tenants' WhatsApp accounts (C14).

## 6. People items (nothing in code can close these)

| # | Item | Who | Unblocks |
| --- | --- | --- | --- |
| VP1 | Speech provider account and its NDPA data-processing terms (no training, retention, transfer out of Nigeria); extends L5 | Founders, legal | V3 in production |
| VP2 | Model provider terms and residency (AI1) cover customers' messages, not only owners' | Founders, legal | V4 in production |
| VP3 | Consent and notice wording for customers; what the business must tell them; retention periods | Counsel | V6 |
| VP4 | Recordings in Pidgin, Yoruba, Hausa, Igbo and Nigerian English to test transcription and voices, from consenting speakers | Operations | V3 quality |
| VP5 | A Meta test app with a WhatsApp number and a Telegram test bot on staging (P13) | Engineering | V2, V8 tests |
| VP6 | One or two pilot businesses | Sales | V8 |
| VP7 | Prices: assistant allowance per plan (tokens, speech seconds) | Founders, finance | V3, V4 |
| VP8 ⊕ | Where Xenibase stores audio (region) and whether it satisfies residency | Xenibase, legal | V1 in production |
| VP9 ⊕ | Meta's current coexistence rules for numbers used in the WhatsApp Business app and the Cloud API at once | Engineering with Meta docs | V8 guide |

## 7. Risks

| Risk | Mitigation |
| --- | --- |
| Voice round trip too slow (download, transcribe, model, speak, upload, send) | Typing indicator at once; small model; measure in V4 before building V8 |
| Wrong transcription of Pidgin or names leads to a wrong answer | Echo what was heard when unsure; never act on a voice note alone (D4, D8) |
| Cost runs away (a busy number, long voice notes) | Per-sender limits, audio length cap, separate allowance, alerts at 80% |
| Personal data in model prompts and in clear in history | Trigger `pii` (V2), personal table columns (V5), placeholders, retention, erase |
| A customer talks the model into revealing another's data | No record reaches the model unless a step fetched it for the sender the trigger names (D4) |
| Meta's rules (window, business verification, quality rating) | Window tracking (V6); the business's own app and number (D1) |
| ⊕ Facts out of date (a price changed on the website but not yet re-read) | Daily refresh, "last read" shown to the owner, a refresh button, and the source named in the reply when it matters |
| ⊕ The assistant promises what the business never said (a price, a refund) | Facts from "About my business" only; reply check; hand-over when unsure (D12, D13) |
| ⊕ A customer gets silence (lost send, budget out, queue full) | Retry rule (D11), fall-back answer (A12), queue cap and alerts (L8, L10) |

## 8. Open questions, answered (2026-11-08)

| # | Question | Answer | What it changes |
| --- | --- | --- | --- |
| Q1 | Is the first customer a fintech or a shop? | **A shop.** | The first release answers from the business's facts, hours, products, prices and FAQ (K1 to K3). V9 (read-only tools over records) stays after launch; I1, I2 and I4 are not in the first release |
| Q2 | Should a business run its assistant on Taskiem's shared number (D1 option b)? | **Not now.** | D1 is decided as (a): the business's own WhatsApp app and Telegram bot. C11 and C12 stay out of the first release (the dead `whatsapp` trigger is still cleaned up in V0) |
| Q3 | A separate assistant area (D10), or plain workflows? | **Plain workflows.** | No new area: E3 is dropped. What a switch would have given is kept by other means: the owner's AI switch, the "Voice in workflows" switch (T7), the per-tenant beta flag (E4), consent at first contact (P3) and the owner's pause (L11) |
| Q4 | How long are conversations kept by default? | **30 days, the owner may change it** within the plan's limit. | One retention rule (M6) for messages, transcripts, audio and the runs behind them, 30 days by default, set per assistant in V5 |
