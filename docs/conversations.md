# Conversations with customers

A business's assistant answers its customers on its own WhatsApp number or Telegram bot ([decision 0062](decisions/0062-customer-assistant.md), [plan V7](spec/voice-assistant.md#v7-conversations-page)). **Conversations** (`/conversations`) is where the business watches it:
- what each customer said and what was answered;
- answering a customer by hand when they ask for a person;
- the assistant's month in numbers;
- **Talk to it here**, to try it before customers do.

## What is recorded

Taskiem keeps each conversation itself (`assistant_turns`), whatever the workflow does:

| Turn | Recorded when |
| --- | --- |
| The customer's message | It arrives and the [guard](assistant-guard.md) decides: what they wrote (a caption, a button's title), its kind (text, voice note, picture...), and what the guard did with it (answered, kept quiet, or not answered and why: STOP, too many messages, a kind it does not read, too busy) |
| A voice note's words | A run hears it (`speech@1 transcribe`) |
| The assistant's answer | A run's step sends something to that customer: an action that declares `turn` ([connector/v1 rule 24](contracts/connector-v1.md)), such as `whatsapp@1 send_text`, `send_media` (a voice reply) or `send_template`, or `telegram@1 send_message`, `send_voice`. The guard's fixed answers are sent by the run too, so they are recorded the same way |
| A reply held back | `assistant@1 check_reply` held it: the words that were not sent and why, shown only on this page |
| A member's answer | A person answers by hand on this page |

Each message links to the run that answered it (**See how it was answered**, the run's page).

**Personal data.** The words are sealed under the customer's key, like everything their messages start ([erasing a customer](assistant-guard.md#erasing-a-customer-p2)): erasing them erases their conversations. Turns go after the business's conversation days (30 by default, capped by the plan), with the conversations table and the runs.

## Who sees what

| Permission | Lets a person | Built-in roles |
| --- | --- | --- |
| `conversation.read` | See conversations. Numbers, emails and identity numbers in the words are hidden, and a WhatsApp customer shows as the last four digits of their number | owner, admin, operator, builder, auditor, viewer |
| `conversation.reply` | Answer customers by hand, and pause or resume the assistant for one customer | owner, admin, operator |
| `pii.reveal` | **Show numbers and emails**: the customer's number and the words as written. Each reveal is audited (`conversation.reveal`) | owner |

## Answering by hand

- **Answer them myself** pauses the assistant for that customer for 24 hours (a hand-over by a member, no alert; `hours` 1 to 168 in the API), so it does not answer over the person. **Let the assistant answer them again** ends it.
- **Answer them** sends the member's message from the business's own number or bot, through the same connection the assistant uses, as the business. Sending pauses the assistant for them too (24 hours) when it was answering. It is recorded with who sent it, and audited (`conversation.reply`).
- **Once.** Each message has a request id: pressing **Send** twice sends once. A message that failed may be sent again with the same id; one still being sent, or whose outcome is unknown, is refused rather than sent twice. A send is finished even when the page is closed.
- **Not to someone who wrote STOP**, and not while the business is suspended.
- **WhatsApp's 24-hour window.** A business may write freely within 24 hours of the customer's last message, and only with an approved template after. Outside the window the page offers the business's approved templates (`whatsapp@1 list_templates`), with a box for each `{{1}}` in the body (each must be filled), and sends one (`send_template`). What is recorded is the template's words as the customer read them.
- **When it fails.** Not sent: said in plain words. When WhatsApp or Telegram did not say whether it was sent, the page says so, and does not send it again by itself.

## The month in numbers

For the assistant shown, this month:
- customers and messages;
- answers, and the typical time to answer (from the message arriving to the first answer sent; the slowest tenth too, in the API);
- replies held back, customers who asked for a person, answers by hand (sent);
- runs that did not finish (failed or cancelled).

Also how much of this month's allowance for AI answers and voice notes the business has used, all its assistants together.

## Talk to it here

A member writes as a customer would, and sees what the assistant would answer:
- **The guard is real.** The message goes through the assistant's guard on a test chat of the member's own, one per assistant (`test/<member>.<workflow>/<env>`), so the notice, STOP and START, the limits and asking for a person all work as they will for customers. Only that member sees it, on this page only (not in the list of customers). **Start again** forgets the member's test chats (and STOP or a hand-over tried in them).
- **The run is real.** It runs the version published in the environment (dev before prod), not a draft, with what the business knows, the real model, and hearing and speaking. It counts against the month's allowances.
- **Nothing changes.** It runs as a *sandbox run* (`runs.sandbox`). Every step that would change something (a WhatsApp or Telegram message, a row, a hand-over) is simulated: it answers an output of the action's shape, marked `simulated`, and sends nothing.
- **What you see.** What it would have sent appears as its answer, with replies the check held back, and a link to the run. A test run that fails alerts no one.
- **What it refuses.** A workflow with a step that asks a person (an approval, a form, a callback) cannot be tried here.

**What it cannot try.** The conversation's memory: the recipe reads it from the conversations table, which a sandbox run does not write. So each test message is answered as if it were the first. Voice notes cannot be sent from the page yet, and a voice note or picture in a conversation shows as its kind (and transcript), not played.

## For operators

- **Storage.** `assistant_turns` (migration 00322), under forced row-level security; text in sealed envelopes. The hourly sweep in the `api` role finds the businesses with turns past their days through `taskiem_conversation_turns_due` (ids and days only) and deletes them in each business's scope.
- **API.** `/v1/conversations` (assistants and customers), `/thread` (a conversation; `reveal=true` with `pii.reveal`), `/reply`, `/templates`, `/pause`, `/resume`, `/metrics`, `/test` (and `DELETE /test` to start again) and `/test/{run}`. The page asks again for new messages every 15 seconds, except while numbers are shown (each showing is audited).
