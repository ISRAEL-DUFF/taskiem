# Speech in workflows

A workflow can hear a customer's voice note and answer by voice ([decision 0062](decisions/0062-customer-assistant.md) D5, [plan V3](spec/voice-assistant.md#v3-speech-steps)). The `speech@1` connector turns a voice note into text and text into a voice note. Audio comes from the run's files and goes back to them ([Files in runs](files.md)), so a run only ever carries references.

This is separate from voice notes in Taskiem's own chat, which let an owner give commands by voice ([Languages](languages.md#voice-notes)). That has its own switch and its own limits.

## Turning it on

Two things must be true, and readiness checks both before a workflow that hears or speaks can be published ([Ready to publish](readiness.md)):

1. **This Taskiem has a speech service.** Whoever runs it sets one up (see [For operators](#for-operators)). Hearing uses the transcription service, and speaking uses the speech service. Either can be set up without the other.
2. **The owner turned on "Voice in workflows"** in Settings. It is off for every business until its owner turns it on, and it cannot be turned on while this Taskiem has no service. Through the API it is `PUT /v1/languages` with `voice_in_workflows`. Leaving the field out keeps the switch as it is.

## The actions

| Action | Class | What it does |
| --- | --- | --- |
| `transcribe` | read | Turns a voice note (`file`: a run file reference, from `whatsapp@1 get_media` or `telegram@1 get_file`) into `text`. `language` (`en`, `pcm`, `yo`, `ha`, `ig`) is a hint for the service and the language of `reply`. Pidgin has no code, so the service detects it. Output: `heard`, `text` (sealed in run history), `language`, `seconds`. |
| `speak` | read | Speaks `text` (at most 4,096 characters; split longer replies with `chunks()`) as a voice note (`format` `ogg`, the default) or as MP3. `voice` is one of the voices this Taskiem offers (the first is the default). `how` says how to speak, for services that take it ("warm and unhurried"). `customer` (with `customer_kind` `phone`, the default, or `chat` for a Telegram chat id) ties the reply to whom it answers, so erasing them deletes it. Output: `file` (a run file reference, kept like any file a step made), `seconds`, `characters`, and `plays_inline`: WhatsApp plays a voice note in the chat only up to 512 KB (about two minutes); a longer one arrives as a file to download, so keep spoken replies short. |
| `cannot_hear` | read | The fixed answer for a `reason` (`empty`, `too_long`, `unreadable`, `unavailable`) in a `language`. It needs no service and spends nothing. |

**A voice note that cannot be heard is not a failure.** The step completes with `heard: false`, a `reason`, and a `reply`: a fixed answer in the customer's language, ready to send. The reasons are:

- `empty`: no words were heard.
- `too_long`: longer than this Taskiem hears (180 seconds by default, at most 300).
- `unreadable`: not audio this can hear. AMR voice notes from older phones are kept as files, but OpenAI-compatible services do not take AMR, so they are not heard until a transcoder exists (plan F7).

The fixed answers are in all five languages. The four non-English ones are drafts until native speakers review them (VP4).

**The service failing** (down, slow, rate limited) fails the step after its retries. An `on_error` path can then send `cannot_hear`'s `unavailable` answer. A refusal that retrying cannot mend fails the step at once, plainly:
- the switch is off;
- the allowance is used up;
- an unknown voice;
- text over the limit;
- the service refused the request;
- a voice-note answer that is not mono Ogg/Opus.

A reply in steps, for a WhatsApp voice note:

```json
{"id": "fetch", "type": "connector", "connector": "whatsapp@1", "action": "get_media",
 "input": {"media_id": "=trigger.item.message.audio.id", "sender": "=trigger.item.message.from"}},
{"id": "hear", "type": "connector", "connector": "speech@1", "action": "transcribe", "needs": ["fetch"],
 "input": {"file": "=steps.fetch.output.file", "language": "pcm"},
 "on_error": {"steps": [{"id": "sorry", "type": "connector", "connector": "speech@1", "action": "cannot_hear",
   "input": {"reason": "unavailable", "language": "pcm"}}]}},
{"id": "say", "type": "connector", "connector": "speech@1", "action": "speak", "needs": ["hear"],
 "input": {"customer": "=trigger.item.message.from",
           "text": "=steps.hear.output.heard ? 'Yes, we have rice today.' : steps.hear.output.reply"}},
{"id": "reply", "type": "connector", "connector": "whatsapp@1", "action": "send_media", "needs": ["say"],
 "effect": {"resend_if_lost": true},
 "input": {"to": "=trigger.item.message.from", "media_type": "audio", "file": "=steps.say.output.file", "voice": true}}
```

On Telegram, use `get_file` and `send_voice` instead. The model step that writes the answer comes with plan V4, and the recipe with V8.

**Time.** Both `transcribe` and `speak` may take up to three minutes per call (`call_timeout: 180` in the manifest), instead of the 30 seconds every other connector call gets: hearing a five-minute voice note, or speaking a 4,096-character reply, can take longer than 30 seconds. Without that, the step would be cut short, retried and billed again each time.

## Voice notes and Opus

WhatsApp and Telegram play a reply as a voice note only when it is Ogg/Opus. `speak` asks the service for Opus and checks the answer really is Ogg/Opus, and mono (WhatsApp takes mono voice notes only), before keeping it. A service that answers anything else fails the step with a plain message, rather than sending a note neither app can play.

OpenAI's speech endpoint answers Opus in an Ogg container. So no transcoder is needed while the service emits Opus (plan F7, decided this way). The nightly live check (below) confirms it against the real service.

## The allowance

Each plan has a monthly allowance of seconds of speech, heard and spoken together: `speech_seconds_monthly`. It is separate from AI building's tokens, so an assistant never starves building or repair (D7).
- **Shop:** 30 minutes.
- **Shop Plus and Starter Trader:** 60 minutes.
- **Growing Business:** 5 hours.
- **Business:** 10 hours.
- **Enterprise & Regulated:** no limit.

The default when a plan names none is 100 minutes. The prices are placeholders until people item VP7 sets them.

How seconds are counted:
- **Seconds heard:** an Ogg voice note's length is read from the stream. Other formats are estimated from their size, at 128 kbit/s (AMR at 12.2 kbit/s), which never counts a long note as short.
- **Seconds spoken:** the reply's real length. Before the service is asked, about 15 characters a second is assumed.
- **Taken before the service is asked:** the seconds are reserved from the allowance in one step, so two calls at the same moment cannot both pass the last seconds. A call the service fails gives them back.
- **Rounding:** each request counts at least one second.

A request that would go past the allowance is refused, and the refusal is recorded. Settings and Billing show the seconds used this month (`speech_seconds_this_month` in `GET /v1/limits`).

`speech_usage` keeps, per organisation and month:
- the seconds heard and spoken;
- the characters spoken;
- the requests;
- the refusals (switch off, allowance used up).

## Privacy

- **What a customer said** (`transcribe`'s `text`) is personal data, sealed in run history like their messages. The run's own steps read it.
- **Spoken text** is sealed in the `speak` step's input, and so is `customer`.
- **Audio** is held in memory only while the service is called. Its file lives in the organisation's storage for the run files' retention.
- **Erasing a customer** (`POST /v1/pii/erase`) deletes their voice notes (`get_media`/`get_file` with `sender`) and the replies spoken to them (`speak` with `customer`) at once. Their transcripts and messages, sealed as free text, are each sealed under a key of their own and are not yet erased with them. That is P2, due in [plan V6](spec/voice-assistant.md#v6-guard-rails-for-customers).
- **The service** gets the audio or text and nothing else: no number, no name.
- **Its account** is chosen with its data-processing terms (no training on the audio, a stated retention, transfer out of Nigeria), people item VP1. A self-hosted OpenAI-compatible server avoids the third party.

## For operators

| Variable | Default | |
| --- | --- | --- |
| `TASKIEM_TRANSCRIBE_PROVIDER`, `_API_KEY`, `_BASE_URL`, `_MODEL` | off | Hearing: the same transcription service as chat voice notes ([Languages](languages.md#voice-notes)) |
| `TASKIEM_SPEECH_HEAR_SECONDS` | 180 | Longest customer voice note heard (1 to 300) |
| `TASKIEM_SPEECH_PROVIDER` | off | Speaking: `openai` (the OpenAI-compatible speech API, or a self-hosted server speaking it) |
| `TASKIEM_SPEECH_API_KEY` | — | Its key (from the Secret) |
| `TASKIEM_SPEECH_BASE_URL` | `https://api.openai.com` | An https URL |
| `TASKIEM_SPEECH_MODEL` | `gpt-4o-mini-tts` | |
| `TASKIEM_SPEECH_VOICES` | `coral,sage,ash,ballad` | The voices offered, the first the default. Chosen for clear speech on a phone; native speakers review them with the recordings of VP4 |

Set them on the `worker` role, which runs the steps, and on `api` and `edge`, which show the switch and readiness. Each service is reached through the egress guard, to its one host.

Metrics:
- `taskiem_speech_requests_total{kind="hear"|"speak", outcome="done"|"not_heard"|"refused"|"failed"}`;
- `taskiem_speech_duration_seconds{kind}`, the time each call takes, which is part of every voice reply's wait.

**The live check.** `.github/workflows/speech-live.yml` runs nightly with the repository secret `TASKIEM_SPEECH_LIVE_KEY`. It speaks a sentence as Ogg/Opus, checks it is Opus, and hears it back from that Ogg/Opus, the format customers' voice notes arrive in. This answers T4's open question of whether the service takes Ogg/Opus. Without the secret it warns and checks nothing; started by hand, it fails.

## Not yet

- **Voices and quality in Pidgin, Yoruba, Hausa and Igbo:** recordings from consenting speakers (VP4) decide which voices and models are good enough.
- **A transcoder for AMR**, and for a service that cannot emit Opus (F7).
- **A voice per business** (T6): today a workflow picks from the voices this Taskiem offers.
- **AAC voice notes** (ADTS, from some older phones) are sent to the service as M4A. Whether it hears them is untested until recordings exist (VP4).
