# Call agent: a business's assistant on the phone

Date: 2026-11-09 · Status: **draft for review; decisions open (section 4), nothing built** · Owner: @EaziDeFi

The customer assistant now answers a business's customers on WhatsApp and Telegram, by text and voice note ([voice-assistant.md](voice-assistant.md), V0 to V10 built). Many Nigerian customers still **call**. This plan is the same assistant answering the business's **phone**: it picks up, speaks the customer's language, answers from what the business knows, passes the call to a person when it should, and records what was said for the owner. Later, and only for customers who asked for it, it **makes** calls: a callback, a delivery or appointment reminder.

The page lists what a call agent must do, what Taskiem already has for it, **everything missing** with the evidence in the code, the decisions to take, and a build plan in milestones. It was written from a read of the repository at `52e537a9` (PR 154) on 9 November 2026. Facts about outside providers were checked on the same day and are marked *to confirm* where only third-party sources were found.

## 1. What the call agent must do

| # | Behaviour |
| --- | --- |
| B1 | Answer calls to a number the business owns, or calls its existing line forwards (when busy, unanswered, or always) |
| B2 | Greet the caller, say it is the business's automatic assistant, that they can ask for a person, and whether the call is recorded |
| B3 | Hear the caller in Nigerian English, Pidgin, Yoruba, Hausa or Igbo, on phone-quality audio |
| B4 | Answer from the business's approved sources only, checked before it is spoken, in the caller's language |
| B5 | Take turns naturally: short silences, the caller cutting in, "hello?", a noisy line |
| B6 | Pass the call to a person (a staff phone) when asked or unsure; when nobody answers, take a message and tell the owners |
| B7 | Keep a record: transcript, duration, outcome, and audio only when the owner chose it and the caller was told |
| B8 | Cap cost: call length, minutes per month, calls per caller, calls at once per business |
| B9 | Be tried before going live (a test call) and shown afterwards (Conversations, with the WhatsApp and Telegram conversations) |
| B10 | Later: answer about the caller's own account without trusting caller ID alone |
| B11 | Later: call customers who asked to be called (a callback, a reminder), within the law on automated calls, never cold marketing |

## 2. What already exists and can be used as it is

- **The assistant's brain.** The `assistant-answer` callable template (`templates/library/assistant-answer.json`): memory, knowledge search (`knowledge@1 search`), the `ai` step with history, system, fallback and personal-data masking, the reply check (`assistant@1 check_reply`), hand-over (`assistant@1 hand_over`), owners told when the model cannot answer. A call turn is the same question with a different mouth and ear.
- **Knowledge sources** (V5b): pages, documents, sheets, typed facts, refreshed and approved by the owner (`engine/knowledge`).
- **The customer guard** (V6, `engine/assistant/guard.go`): STOP and START, the first-contact notice, hand-over hours, per-customer limits, the owner's pause, Taskiem's stop and beta, the queue cap, replayed by delivery key. Its rules carry over; its channel words do not (G1).
- **The conversation record** (V7, `assistant_turns`, migration 00322): `channel` and `kind` are free text (a `call` channel and `call` kind fit, `00322_conversations.sql:21,25`); the Conversations page, reply by hand, masked reading, metrics.
- **Speech** (V3, `engine/voice`, `engine/speech`): an OpenAI-compatible transcription and speech provider, metered in seconds against the plan (`SpeechSecondsMonthly`, `engine/runtime/limits.go:68-72`), reserved before and settled after (`engine/speech/speech.go:204-262`). Input accepts WAV, MP3, AMR, Ogg and others (`engine/voice/voice.go:84-88`).
- **A synchronous phone-network callback path**: USSD (`api/ussd.go`). Africa's Talking posts each step and must be answered within seconds; Taskiem checks a per-channel token by its hash in constant time (`api/ussd.go:293`) with an optional address allow-list, answers from cache within a 2-second budget (`api/ussd.go:47-49, 167-172`), seals the caller's number only when needed (`api/ussd.go:585-606`), and starts the run after answering (`api/ussd.go:613-626`). A turn-based call (CD2 option a) is the same shape.
- **Africa's Talking** is already a connector (SMS, airtime, USSD; `connectors/africastalking`) and an account many Nigerian businesses know.
- **Personal data**: phone numbers sealed per subject (`engine/secrets/subjects.go:166-171`), erase by number across conversations and tables (V5, V6), retention per assistant (30 days by default).
- **Alerts** to owners by WhatsApp, Telegram or email; the AI and speech allowances' 80 % and 100 % alerts.

## 3. Everything missing

**Blocker** (it cannot work without it), **Launch** (needed before a real caller), **Later**.

### 3.1 Telephony rail

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| T1 | No voice connector. Africa's Talking is SMS, airtime and USSD only; nothing answers a call, says, plays, records, collects digits or transfers | `connectors/africastalking` actions; no `Say`, `Record`, `GetDigits` or `Dial` anywhere | Blocker |
| T2 | No phone number for a business: no way to buy, rent or link a Nigerian number, or to guide forwarding from an existing line | nothing | Blocker |
| T3 | Call webhooks (incoming call, call ended with duration and cost, recording ready, digits) have no path or verification. Africa's Talking does not sign callbacks; USSD uses a token in the URL, the same must hold here | `api/ussd.go:293`; `connectors/africastalking/manifest.yaml:172-191` | Blocker |
| T4 | No SIP or media streaming of any kind (for a real-time call, CD2 b or c) | no SIP, RTP or WebSocket in `api`, `engine`, `cmd` | Blocker for real time |
| T5 | Transfer to a person (dial a staff phone, with the caller kept on the line and told) | nothing | Launch |
| T6 | Call state: a call's id, its legs, who hung up, the cost the provider reports | nothing | Launch |
| T7 | A call's events must be idempotent like messages (a provider retries); a call's turns arrive many times a minute | dedup exists for messages (`engine/ingest`) | Launch |

### 3.2 Real-time speech

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| S1 | Speech is batch: whole audio in, whole text out; whole text in, whole audio out. No streaming transcription or speech | `engine/voice/voice.go:40-43`; `engine/voice/speech.go:38-40` | Blocker for real time |
| S2 | No telephone audio: 8 kHz μ-law or A-law in and out, no transcoding; speech is spoken only as Ogg or MP3 | `engine/voice/speech.go:29-46` ("plan F7") | Blocker |
| S3 | No turn-taking: voice activity detection, end of turn, the caller cutting in (barge-in) stopping playback, "are you there?" on silence | nothing | Blocker for real time; Launch for turn-based |
| S4 | Latency budget unmeasured for a call: transcribe, knowledge, model, check, speak. A voice note tolerates 5 seconds; a call does not (people say "hello?" after about 1.5) | `docs/performance.md` measures the engine only | Launch |
| S5 | Fillers while the answer is prepared ("let me check…") without promising anything | nothing | Launch |
| S6 | The limits are for voice notes (60 s heard by default, 4096 characters spoken); a call needs per-turn and per-call limits | `engine/voice/voice.go:54-67`; `engine/speech/speech.go:61-67` | Launch |

### 3.3 The guard and the conversation on a call

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| G1 | The guard knows only WhatsApp and Telegram: channel names, the phone category, the 24-hour window | `engine/assistant/guard.go:487-503, 520-528`; `engine/assistant/service.go:31-37, 75-76` | Blocker |
| G2 | STOP on a call means "do not call me": a do-not-call list per business, honoured by outbound calls and by the WhatsApp assistant's own outbound messages where they share the number | nothing | Launch (Blocker for outbound) |
| G3 | The notice is written; on a call it is spoken first, short, in the caller's language, and says whether the call is recorded | `guard.go:255-270` | Launch |
| G4 | Hand-over is "a person will write to you"; on a call it is a transfer, or a message taken and a callback promised, and the owners told with the caller's number | `assistant@1 hand_over` | Launch |
| G5 | Limits are messages per minute and day; a call needs minutes per caller per day, calls at once per business, and the longest call | `assistant_contacts` counters | Launch |
| G6 | A withheld number (no caller ID) has no customer key: answered, but nothing remembered and no account read | guard drops an empty sender (`guard.go:165`) | Launch |
| G7 | Transcripts of calls: `voice_transcripts` allows only `whatsapp` | `00146_voice_transcripts.sql:15` | Launch |
| G8 | Conversations shows messages; a call is one item with turns, duration, outcome (answered, transferred, message taken, hung up) and, when kept, its audio | `api/conversations.go`; web `Conversations.tsx` | Launch |
| G9 | The 24-hour reply window does not apply to calls; replying by hand after a call is a WhatsApp message or SMS to that number, with WhatsApp's rules | `api/conversations.go:182` | Later |

### 3.4 Identity and personal data

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| P1 | Caller ID can be spoofed. The WhatsApp assistant reads a customer's own account because Meta signed the sender (threat model B21); a call has no such proof, so V9's account answers must not run on caller ID alone | `docs/security/threat-model.md:228`; `templates/library/assistant-account.json:125-140`; `docs/ussd.md:110` | Blocker for B10 |
| P2 | Recording consent and notice wording for calls; whether keeping audio is needed at all | `docs/spec/voice-assistant.md:134` (P3, text only) | Launch |
| P3 | Call audio goes to the speech provider (and, with CD2 b, the model provider) as it is spoken; personal data cannot be masked out of audio before it leaves | masking works on text (`engine/pii`) | Launch (people: VP-C3) |
| P4 | Retention and erase for call audio, transcripts and call records, by number | erase covers messages, voice notes, tables (V5, V6) | Launch |

### 3.5 Outbound calls

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| O1 | Nothing places a call | nothing | Later (Blocker for B11) |
| O2 | Consent: a call only to a customer who asked (a callback they requested, reminders they opted into), recorded with when and how | nothing | Blocker for B11 |
| O3 | Nigerian rules on automated and unsolicited calls: the NCC's do-not-disturb regime (customers opt out through 2442) and direct-marketing rules on automated calling; the NDPA's lawful basis. What applies to a business's service calls is a people item | DND appears only for SMS (`engine/sender/sms.go:39-52`); no NCC rules in `docs/` | Blocker for B11 (people: VP-C4) |
| O4 | Calling hours (Lagos time), attempts per customer, an answering machine or voicemail detected and not talked to | nothing | Blocker for B11 |
| O5 | Toll fraud: outbound calls only to Nigerian mobile and fixed numbers, never premium or international, with a per-day cap | nothing | Blocker for B11 |

### 3.6 Money and metering

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| M1 | No call minutes in the plans; speech seconds and AI tokens exist, telephony cost does not | `deploy/plans.yaml:60-220` (placeholders, VP7); only `container_minutes_monthly` is per minute | Launch |
| M2 | Who pays the provider: Taskiem's account resold by the minute, or the business's own account (as with WhatsApp) | nothing | Decision (CD7) |
| M3 | A number's monthly rent and set-up | nothing | Launch |
| M4 | Alerts at 80 % and 100 % of call minutes; what a caller hears when they run out (a short "please send us a WhatsApp message", and the owners told) | speech and AI alerts exist (L10) | Launch |

### 3.7 Owner experience

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| W1 | Setting up a number: buying or linking one, or the forwarding codes for the business's existing line (busy, no answer, unreachable), and checking that it works | nothing | Launch |
| W2 | A test call before going live, without customers: the owner calls from their own phone into the sandbox, or talks in the browser | Talk to it here is text (V7) | Launch |
| W3 | Opening hours: outside them the agent answers, says when a person can call back, and takes a message | nothing | Launch |
| W4 | Where calls are transferred to (one or more staff phones, in order), and what happens when nobody picks up | nothing | Launch |
| W5 | Metrics: calls, answered by the agent, transferred, messages taken, hung up early, minutes, average wait to first answer | V7 metrics are messages | Launch |
| W6 | The guide, readiness (number linked, webhooks reached, minutes left, speech on) and the builder learning calls | `api/readiness.go` | Launch |

### 3.8 Engine and deployment

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| E1 | A call does not fit a run: a run per turn is fine for turn-based calls (a short run's p95 is 257 ms, `docs/performance.md`), but a real-time call is one long session with sub-second turns, which needs its own loop outside the run engine, calling workflows for knowledge, the answer and the record | runs are the only execution unit | Blocker for real time |
| E2 | Long-lived connections: every server has a 120 s write timeout and 20 s graceful shutdown; a call lasts minutes, so it needs its own server role, deploys that drain calls, and the load balancer's idle timeouts | `cmd/taskiem/serve.go:1621-1640` | Blocker for real time |
| E3 | Calls at once: per business and per Taskiem, with what a caller hears when full | queue cap exists for messages (L8) | Launch |
| E4 | Observability of a call: per-turn latency, transcription confidence, held sentences, dropped audio | nothing | Launch |

### 3.9 Languages

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| L1 | Transcription quality of the five languages on 8 kHz phone audio is unknown, and worse than on voice notes; names, amounts and addresses matter most | VP4 recordings are voice notes | Launch (spike in CA0) |
| L2 | Speech voices that sound Nigerian and speak Yoruba, Hausa and Igbo well enough to be understood on a phone line | V3 voices are the provider's | Launch |
| L3 | Switching language mid-call when the caller does | `language()` per message (V4) | Later |

### 3.10 Security

| # | Gap | Evidence | Weight |
| --- | --- | --- | --- |
| X1 | The threat model has no phone boundary: spoofed caller ID, call flooding to run up cost, prompt injection by voice, a recording replayed to the agent | `docs/security/threat-model.md` B21 covers chat | Launch |
| X2 | Call webhook authentication (token per number, address allow-list where the provider publishes ranges) | as USSD | Blocker |
| X3 | Audio and transcripts reaching the owner's team: masked as messages are, revealed with the same permission | V7 masking | Launch |

## 4. Decisions to take before building

| # | Decision | Options | Recommendation |
| --- | --- | --- | --- |
| CD1 | **Telephony provider** | **Africa's Talking voice** (Nigerian numbers, XML call control: Say, Play, Record, GetDigits, Dial, Conference, Redirect; no media streaming found, *to confirm*); **Twilio** (Nigerian numbers for businesses with Nigerian registration, address and a letter of authorisation; media streams and SIP); a **Nigerian SIP trunk** (a local carrier or aggregator) into a media server Taskiem runs; others with media streams (*to confirm* for Nigerian numbers) | Choose with CA0's spike: Africa's Talking for the turn-based first release if its numbers and recording latency are good; keep the connector interface able to take a streaming provider second |
| CD2 | **How a call is handled** | (a) **Turn-based**: the provider records each turn, Taskiem transcribes, answers, checks and plays the answer; 3 to 6 seconds a turn, no barge-in, every safety rule kept, little new infrastructure. (b) **Speech-to-speech model over SIP** (for example OpenAI's Realtime API with SIP): sub-second, natural, but the reply check cannot run before words are spoken, and the audio goes to the model provider. (c) **Streaming pipeline Taskiem runs**: media stream in, streaming transcription, the model, the reply check sentence by sentence, streaming speech out; about a second a turn, every rule kept, the most to build | (a) first, to learn what callers ask with every rule in place; then (c). Not (b) while the reply check is how the assistant is kept honest (D13 of the voice plan) |
| CD3 | **Inbound first; outbound only later and only on consent** | Inbound only; both at once | Inbound only until CA6; outbound for callbacks and opted-in reminders, never marketing |
| CD4 | **Recording** | Keep audio by default; keep transcripts only; owner's choice | Transcripts kept like messages (30 days by default); audio not kept unless the owner turns it on, and then the greeting says so |
| CD5 | **Identity for account answers** | Trust caller ID; a one-time code by SMS or WhatsApp; never on calls | Never read an account from caller ID alone. Offer "I can send it to your WhatsApp": the answer goes to the WhatsApp of that number, which a spoofer does not hold. A spoken one-time code later, if pilots ask |
| CD6 | **Languages in the first release** | All five; English and Pidgin first | English and Pidgin first, the others when CA0's phone-audio test passes for them |
| CD7 | **Whose provider account** | Taskiem's account, minutes resold in the plan; the business's own account connected as a connection | The business's own account where the provider allows it (cost and number in their name, as with WhatsApp); Taskiem's account as an option later |
| CD8 | **Number** | A new number; the business's existing line forwarded to it | Both: forwarding guided for the existing line (busy, no answer, always), so customers keep calling the number they know |

## 5. Build plan

Each milestone ends with tests, docs, a review for bugs and gaps, and a push, as in the voice plan.

### CA0. Decisions and spikes

- Take CD1 to CD8.
- Open a provider test account and a Nigerian test number (people item VP-C1).
- **Phone-audio spike:** 50 to 100 recordings of consenting speakers over a real phone line, in the five languages (VP-C5), transcribed by the current provider; word error rate on names, amounts and places. Decides CD6.
- **Latency spike:** a turn end to end on the chosen rail (record, fetch, transcribe, answer, check, speak, play); the target is under 4 seconds for (a) and under 1.2 seconds for (c), measured at p50 and p95.

### CA1. Phone calls as a connector

- A voice connector for the chosen provider (`africastalking@1` voice actions, or `phone@1` if CD1 picks another): incoming call trigger, call-ended event (duration, cost, who hung up), actions say, play, record, collect digits, transfer, hang up.
- Call webhooks verified by a token per connection (and address ranges where published); events deduplicated.
- Telephone audio: WAV and 8 kHz μ-law in and out of `engine/voice` and `engine/speech` (S2), with per-turn limits (S6).
- Readiness: the number linked, the callback address set at the provider, speech on.

### CA2. A turn-based call (first release)

- The call loop on the USSD pattern: answer within the provider's budget, a run per turn after answering, the call's state in a table (`assistant_calls`: call id, number sealed, started, ended, outcome, minutes).
- Greeting with the spoken notice (G3); each turn: the caller's speech recorded, transcribed, answered by `assistant-answer` (memory, knowledge, the reply check), spoken and played; short fillers while it works (S5); silence and "are you there?"; the longest call.
- The guard for calls (G1, G5, G6): channel `call`, the phone category, STOP as do-not-call, minutes per caller, calls at once, the owner's pause, Taskiem's stop and beta.
- Hand-over: transfer to staff phones in order (W4); nobody answers or out of hours (W3): a message taken, transcribed, the owners told, the caller told when to expect a call.
- The record: each call in `assistant_turns` with kind `call`, transcripts sealed (G7), retention and erase (P4).
- A template: **Your assistant on the phone** (`assistant-phone`).

### CA3. The owner's side

- Setup: link a number or follow the forwarding codes, with a check call (W1).
- **Test call**: the owner calls a test number from their own phone, recognised as theirs, into a sandbox run (V7's sandbox), or talks in the browser (W2).
- Conversations: calls listed with the chats, opened as turns with duration and outcome, audio played when kept (G8); metrics (W5).
- Call minutes in the plans, alerts at 80 % and 100 %, what callers hear when minutes run out (M1, M4).
- Guide `docs/guide/answer-calls.md`; the builder and evaluation cases learn calls (W6).

### CA4. Real-time calls

- A `calls` server role for long-lived media connections, drained on deploy (E1, E2).
- The streaming pipeline (CD2 c): media in, voice activity and end of turn, streaming transcription, knowledge and the model, the reply check on each sentence before it is spoken (a held sentence becomes "someone from the team will call you back"), streaming speech out, barge-in (S1, S3).
- Per-turn latency, held sentences and dropped audio in metrics (E4).
- The turn-based path stays for providers without streaming.

### CA5. Account answers on calls

- "How much do I have?" on a call: the agent offers to send it to the caller's WhatsApp (or SMS) and sends it there through the V9 templates, keyed on the number (CD5, P1). Never spoken from caller ID alone.

### CA6. Outbound calls on consent

- Purposes only: a callback the customer asked for, reminders they opted into (delivery, appointment, payment due).
- Consent recorded; the do-not-call list (G2) and the NCC's DND checked; calling hours; attempts per customer; voicemail detected and not talked to; Nigerian numbers only; per-day caps (O1 to O5).
- Templates: **Call back customers who asked** and **Remind by phone**.

### CA7. Hardening

- Threat model boundary for calls (X1); load test of calls at once; cost alerts by business; a call evaluation suite (transcripts of real pilot calls, with consent).

### Not in this plan

- Marketing calls, or any call to someone who did not ask.
- Money moving on a call.
- A cloned voice of the owner or staff.
- International numbers and calls.
- Conference calls, call queues with hold music beyond a short transfer.

## 6. People items (nothing in code can close these)

| # | Item | Who | Unblocks |
| --- | --- | --- | --- |
| VP-C1 | Provider account, a Nigerian test number, the business documents the provider needs (registration, Nigerian address, letter of authorisation) | Founders, operations | CA0, CA1 |
| VP-C2 | Whether Taskiem, reselling minutes or running calls for businesses, needs an NCC licence or a value-added-service registration; and the provider's terms for AI-handled calls | Legal | CA2 in production |
| VP-C3 | Data-processing terms for call audio with the speech provider (and the model provider for CD2 b); where audio is processed | Legal | CA1 in production |
| VP-C4 | The rules for automated service calls in Nigeria (NCC direct marketing and DND, NDPA lawful basis), recording consent, and the spoken notice's wording | Counsel | CA2 (notice), CA6 (outbound) |
| VP-C5 | Consenting speakers recorded over real phone lines in the five languages | Operations | CA0 |
| VP-C6 | One or two pilot businesses that take many calls (a restaurant, a clinic, a school office) | Sales | CA2 |
| VP-C7 | Prices: call minutes per plan, number rent, overage | Founders, finance | CA3 |

## 7. Risks

| Risk | Mitigation |
| --- | --- |
| Turn-based calls feel slow and robotic | Fillers, short answers, the greeting sets expectations; CA4 replaces it where streaming is available |
| Phone audio in Yoruba, Hausa and Igbo transcribes badly | CA0 spike decides; English and Pidgin first; the agent says what it heard back for names and amounts, and hands over when unsure |
| A spoofed caller learns someone else's balance | Never spoken from caller ID; sent to the number's WhatsApp instead (CD5) |
| Call flooding runs up a business's bill | Minutes per caller, calls at once, monthly minutes, alerts, Taskiem's stop |
| The agent says something the business never said | The same reply check, before playing (a) or per sentence (c); not option (b) |
| A long call dies on a deploy | A separate role drained on deploy; a dropped call gets an SMS or WhatsApp "sorry, we were cut off" |
| Outbound calls break NCC rules | Inbound only until CA6; consent, DND, hours, caps; counsel's review (VP-C4) |
| Provider facts change (Nigerian numbers, streaming) | CA0 confirms on a real account before CA1; the connector keeps one interface for several providers |

## 8. Open questions for the owner

1. **Provider (CD1):** is there a provider you already have an account or relationship with (Africa's Talking, Twilio, a Nigerian carrier)?
2. **Speed versus safety (CD2):** do you accept a turn-based first release (a few seconds a turn, every safety rule kept) before the real-time version?
3. **Recording (CD4):** keep transcripts only by default, audio only when the owner turns it on?
4. **Whose account (CD7):** the business's own provider account, or Taskiem's account with minutes in the plan?
5. **Pilot (VP-C6):** which kind of business pilots it first?

Sources checked on 2026-11-09: Africa's Talking voice actions (SDK and help pages), Twilio's Nigeria regulatory and voice pricing pages, OpenAI's Realtime API with SIP guide, and the NCC's consumer pages on do-not-disturb (2442).
