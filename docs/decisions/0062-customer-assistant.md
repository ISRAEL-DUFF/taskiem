# 0062 — Customer assistant: a business's own WhatsApp and Telegram, one run per message, answers from the business's own facts

Date: 2026-11-08 · Status: **Accepted** (not built) · Plan: [customer voice assistant](../spec/voice-assistant.md)

## Context

A business wants an assistant its customers talk to on the business's WhatsApp number or Telegram bot, by text or voice note: it answers in the customer's language, remembers the conversation, replies by voice when spoken to, and knows the business (hours, prices, products, policies). The reference was an n8n build (Telegram and WhatsApp triggers, Whisper, a Claude agent with Postgres memory, text-to-speech, HTTP tools for a fintech backend).

A read of the code found most blocks present (channel connectors, `branch`, `concurrency_key`, callable workflows, the `ai` step, data tables) and about a hundred gaps around them: no media download or voice send on either connector, no file references in runs, transcription only inside Taskiem's own chat, no text-to-speech, a one-message `ai` step, no conversation memory, no knowledge of the business, and message text kept in clear in run history. The plan lists them all with evidence. These are the choices that shape the build.

## Decision

1. **The door (D1).** Customers reach the business's **own** WhatsApp app (`whatsapp@1`) and Telegram bot (`telegram@1`), through ordinary workflows. Taskiem's shared number and platform bot do not serve a business's customers in this plan. The `whatsapp` trigger type, which never started a run, is retired: kept in the schema so saved versions read, refused when saving or publishing, and gone from the builder.
2. **The recipe (D2).** Three templates: a WhatsApp entry and a Telegram entry, each calling one callable `assistant-answer` template. No new "bundle" concept.
3. **Runs (D3).** One run per message. Memory is a conversations table; one message at a time per customer through `settings.concurrency_key`. No run waits for a customer's next message, so no customer holds a running-run slot.
4. **Doing things (D4).** The model chooses what the customer wants from a fixed list; a `branch` runs a fixed step for it; a second ai step words the reply. Who the customer is always comes from the trigger, never from the model. No model tool calling.
5. **Speech (D5).** One OpenAI-compatible interface for transcription and speech (`engine/voice`, extended); OpenAI by default, any compatible or self-hosted server allowed. The account is chosen with its NDPA data-processing terms; Ogg/Opus is tested first.
6. **What the model sees (D6).** The customer's own message is let through (audited, as `personal_data` allows), phones and identifiers stay placeholders, no record of the business reaches the model unless a step fetched it for the sender the trigger names, and every reply is checked before it is sent.
7. **Budget (D7).** A separate monthly assistant allowance (model tokens and speech seconds), so an assistant never starves building, repair or chat routing.
8. **Money (D8).** No money moves from a conversation in this plan. Writes need their own plan, with a confirmation a non-member can give and the model cannot skip.
9. **Set-up (D9).** Owners set it up first, with a guide and readiness checks; field agents once they may install templates and connections.
10. **Where it sits (D10).** Plain workflows, not a business area. Consent and cost are covered by the AI switch, a "Voice in workflows" switch, a per-tenant beta flag, a notice and STOP at first contact, and the owner's pause.
11. **A lost reply (D11).** A chat reply send whose answer is lost is sent once more, accepting a rare duplicate, by a step setting; never for money or templates. Everything else keeps the action-class rules.
12. **What it knows (D12).** **Knowledge sources** inside Taskiem (storefront card, hours and catalogue; data tables the owner picks; an About my business page) and outside it (the business's own verified website, uploaded files, Google Sheets and Drive, connected systems such as books items), read into one store per business, searched per question, refreshed on a schedule, and shown to the owner before use. Text from outside is material for the model, never instructions.
13. **A person (D13).** A word ("agent", "person", and their equivalents in the five languages) pauses the assistant for that customer and tells members; the model may offer it when unsure. Resumed from the conversations page.

Also settled with the plan's open questions: the first customer is a **shop**, so read-only tools over customer records come after launch; conversations, transcripts, audio and the runs behind them are kept **30 days** by default, changeable by the owner within the plan's limit.

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Serve customers from Taskiem's own channel, which already downloads media, transcribes and tracks the 24-hour window | Needs Taskiem's Meta verification (W1) first, routes many businesses' customers through one number, and is not the workflow product. The pieces move into shared packages so this can follow |
| One long run per conversation, waiting on the next message | Each open conversation holds a running-run slot (5 on Starter), and a long run is harder to retry and erase |
| Model tool calling | The model would decide which step runs; identity and money rules would rest on a prompt. Fixed routing gives the same answers for a shop with no new engine path |
| Share the tenant's AI budget | A busy assistant would stop the owner building |
| A template bundle | A new concept for one recipe; callable templates already compose |
| Storefront only, or FAQ only, for the business's facts | A business without a storefront would know nothing; an FAQ only means typing prices twice. The founder asked for sources inside and outside Taskiem |

## Consequences

- A file reference kind in wd/v1 and a store for files a run makes or fetches; media actions on both connectors; speech steps; the `ai` step gains history, a reply length and a system instruction; a conversations table with row expiry; a knowledge store with search; customer guard rails; a conversations page; the recipe. In that order, as milestones V0 to V8 of the plan.
- Every wd/v1 change goes through the schema, the Go model and checks, the TypeScript SDK, read-back, the AI builder's rules and the canvas, and stays deterministic for replay.
- People items before production: the speech provider's DPA, the model provider's terms for customers' messages, consent wording, test recordings in the five languages, a Meta test app and Telegram bot on staging, pilot shops, prices for the allowance.
- A call agent (phone calls) is its own plan, started once this one is built.

## Provenance

From a discussion on 2026-11-08: the founder shared an n8n voice-assistant guide and asked how to do it with Taskiem, then asked for every gap mapped and a build plan for review before any code, then answered the plan's open questions and its thirteen decisions. Their answer on the business's facts was "the data can come from Taskiem or externally", which made knowledge sources a milestone of their own.
