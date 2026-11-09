# Keeping a customer assistant safe

A business's assistant answers its customers on the business's own WhatsApp number or Telegram bot ([decision 0062](decisions/0062-customer-assistant.md), [plan V6](spec/voice-assistant.md#v6-guard-rails-for-customers)). Customers are not members: they can write anything, as often as they like, in any language, and some will try to talk the model into things. This page is what keeps that safe for them and for the business:
- **the customer guard**, which runs when a message arrives, before a run can start;
- **`assistant@1`**, the actions a workflow uses: checking a reply before it is sent, handing a customer to a person, and WhatsApp's 24-hour window;
- **the switches**: the owner's pause, and Taskiem's own.

## The customer guard

A chat trigger gets a guard with `guard` in its config: `true` for the defaults, or settings.

```json
"trigger": {"type": "connector_event", "config": {
  "connector": "whatsapp@1", "trigger": "messages",
  "guard": {"per_minute": 6, "per_day": 200, "max_queued": 20, "notice": true, "handover_hours": 24,
            "kinds": {"image": "answer"}}}}
```

Only triggers whose manifest reads their events as customers' messages take a guard: `whatsapp@1` `messages` and `telegram@1` `update` (their `conversation` block, [connector/v1 rule 23](contracts/connector-v1.md)).

| Setting | Default | |
| --- | --- | --- |
| `per_minute`, `per_day` | 6, 200 | Messages one customer may send (L1). Over either, they are told once (a fixed answer) and the rest are dropped until the minute or the day (Lagos time) is over |
| `max_queued` | 20 | This workflow's runs waiting in its queue (L8). A full queue drops new messages and alerts the owners once a day (`assistant_busy`), so one busy assistant never fills the business's backlog and gets everyone else refused |
| `notice` | true | A new customer's first message is answered with the notice first (P3) |
| `handover_hours` | 24 | How long the assistant stays quiet for a customer who asked for a person (1 to 168) |
| `kinds` | below | What to do with each kind of message: `answer`, `reply` (a fixed answer), or `drop` |

Kinds of message (C19): text, voice notes, audio and button presses are answered; pictures, videos, documents, places and contacts get a fixed answer ("I can only read text messages and listen to voice notes"); stickers, reactions and anything else are dropped.

### What it decides, in order

1. **Only customers' new messages in private chats** (C18). A WhatsApp status report or error, a Telegram edit, a channel post, a button press on an old message, or anything in a group starts nothing. A bot added to a Telegram group answers no one there.
2. **Taskiem's switches**: `TASKIEM_ASSISTANTS` off, or `beta` and the business is not in the beta, or Taskiem has stopped the business's assistants: nothing starts.
3. **STOP and START** (P3). STOP (or its words in Pidgin, Yoruba, Hausa and Igbo) is confirmed once, and then nothing more from that customer starts a run on that channel until they write START, which is confirmed too.
4. **The limit** per customer.
5. **The owner's pause**: the message is kept (a run starts, outcome `quiet`) and nothing is answered.
6. **A customer waiting for a person**: kept and not answered, until the hand-over ends.
7. **Asking for a person** (D13): "agent", "person", "talk to someone", "customer care" and the same in the four other languages, as the whole message. The customer is told someone will answer, the owners get an alert (`assistant_handover`, by WhatsApp or Telegram too) with the number's last four digits, and the assistant is quiet for them for `handover_hours`.
8. **The kind** of message.
9. **The workflow's queue**.

A word counts only as the whole message, folded (lower case, no tone marks, no punctuation): "stop" is STOP; "please don't stop the delivery" is not. The words are drafts until native speakers review them, like Taskiem's other messages ([languages](languages.md)).

### What a run sees: `trigger.guard`

| Field | |
| --- | --- |
| `outcome` | `answer`: answer the message. `reply`: send `reply` and nothing else. `quiet`: keep the message (write it to the conversation) and answer nothing |
| `reason` | Why, for `reply` and `quiet`: `stopped`, `started`, `limited`, `handover`, `kind`; `paused`, `waiting` |
| `reply` | The fixed answer, in the customer's language |
| `notice` | For a new customer, the notice to send before the answer ("You're chatting with {business}'s automatic assistant … Reply PERSON … or STOP"); empty otherwise |
| `language` | The customer's language: of this message when Taskiem is sure, else of their last message it was sure of, else English |
| `first` | Their first message on this channel |
| `kind`, `text` | The message's kind and words (a caption, a button's title) |
| `channel`, `sender` | Who and where, for replies and for `assistant@1` |
| `file` | A voice note or audio, already fetched: a run file reference (C21) |
| `file_error` | Why it could not be fetched when it arrived; fetch it in the run (`get_media`, `get_file`) |

**Fetched first** (C21). Meta's media links last minutes and Telegram's an hour, and a run can wait in a queue (one conversation at a time per customer). So a voice note or audio is fetched when it arrives, before the run is recorded, into a run file tied to the customer, within the delivery's time (3 seconds). It is scanned and kept as with `get_media` ([files](files.md)).

`sender` and `text` are personal data: sealed in the run's history, readable by its steps.

## A reply is checked before it is sent: `assistant@1 check_reply`

A customer's message is instructions to the model as much as the owner's prompt, and outside material can carry instructions too (A11). With no tools (D4), the harm is in what the model says. So the recipe checks the reply before sending it (L6):

```json
{"id": "check", "type": "connector", "connector": "assistant@1", "action": "check_reply", "needs": ["reply"],
 "input": {"text": "=steps.reply.output.text", "material": "=steps.find.output.material",
           "customer": "=trigger.guard.text", "language": "=trigger.guard.language",
           "links": ["https://mamaput.example"], "never": ["=vars.cost_note"]}}
```

| Problem | The reply has |
| --- | --- |
| `placeholder` | A mask or marker copied from what the model was given: `[phone]`, `{{…}}`, `<<material …>>` |
| `price` | An amount (₦3,500, N1,500, 5k, 20,000 naira, $10) the material and the customer's message do not have |
| `link` | A link to a host the material does not name and `links` does not allow |
| `contact` | A phone number or email in neither the material nor the customer's message: never another customer's |
| `secret` | Something shaped like a key or a token |
| `internal` | One of `never`: values the business keeps from customers (cost prices, staff numbers) |
| `too_long`, `empty` | Over `max` (4,000 characters by default), or nothing |

`ok` false answers, as `text`, "Let me check that with the team and get back to you" in the customer's language, to send instead. A recipe may also hand the customer over (`hand_over`).

## A person: `assistant@1 hand_over` and `resume`

`hand_over` (`channel`, `sender`, `hours`) is the customer's own word, for a workflow: when the model is unsure, or the customer is upset. `resume` ends it. Members also see the customers waiting for a person in the API (`GET /v1/assistant`) and resume them there; the conversations page (V7) shows them with their messages.

## WhatsApp's 24-hour window: `assistant@1 window`

A business may answer a WhatsApp customer freely within 24 hours of their last message, and only with an approved template after (C13). The guard records when each customer last wrote, per channel. `window` (`channel`, `sender`) answers `open`, `last_inbound_at` and `closes_at`, for anything sent later than the answer: a follow-up, a member's reply.

A send Meta refuses for the window (131047) fails with `steps.<id>.error.code` `window_closed`, which `on_error` branches on: send a template instead, or tell a member.

## Erasing a customer (P2)

In a run started by a guarded message, the customer's words, the transcript of their voice note, the model's replies and anything else of category `other` or `name` are sealed under the customer's own key: their number's (WhatsApp) or chat id's (Telegram). Identifiers (another phone number, an email) keep their own keys, so erasing whoever they name still reaches them. A child run (a callable `assistant-answer`) and a resumed run inherit it.

So one erase (Data & privacy, by the number or chat id) erases what they said in every run, in its history and its archive, with:
- their conversation rows (V5);
- their files (V1);
- the guard's record of them (their counts, STOP, a hand-over).

The answer counts what it found (`customer_runs`, `assistant_contacts_deleted`).

**A customer who comes back.** Erasure is one way: a destroyed key is never restored. A customer erased who writes again gets a new key (their subject's next generation, `<subject>~2`). Their new messages are answered and sealed under it, and erasing them again destroys every generation.

## The switches

| Switch | Who | What it does |
| --- | --- | --- |
| **Pause for everyone** (`POST /v1/assistant/pause`) | The owner (`secret.manage`) | Customers' messages are kept, nothing is answered (L11). Audited |
| `TASKIEM_ASSISTANTS` | The operator | `on` (default), `beta` (only businesses Taskiem has put in the beta), or `off` (no assistant answers anyone) (E4) |
| **Beta** and **stop** for one business | Taskiem's operators (`POST /ops/tenants/{id}/assistants-beta-on`, `-beta-off`, `assistants-stop`, `assistants-start`, with a reason and a passkey) | Recorded in the platform's and the business's audit. A stopped business's assistants answer no one |

## Allowance alerts (L10)

The owners are told when this month's allowance for AI steps or for voice (seconds heard and spoken) passes 80 %, and again when it is used up (`allowance`), once a month each. When it is used up, AI steps answer with their `fallback` and speech with its fixed answers.

## For operators

- **Storage.** The guard keeps `assistant_contacts` (one row per customer per channel, keyed by their subject: never the number in the clear; the sender itself sealed), `assistant_controls` (Taskiem's beta flag and stop, written only with the platform chain in scope), `tenant_channel_settings.assistant_paused_at`, `triggers.guard`, and `runs.customer_subject` (migration 00320).
- **One delivery, two guarded workflows** on the same trigger count the customer twice: give a channel one assistant.
- **A provider's retry** of a delivery is counted again by the guard; its run is not started twice.

## Not yet

- **Debounce** (L5): three quick messages get three answers.
- **The conversations page** (V7): reading a conversation and answering by hand.
- **The recipe** (V8) wires all of this into the WhatsApp and Telegram templates.
