# Tell me

`notify@1` is built in. It sends a workflow's messages to the business's own people and texts its customers, with nothing to connect and no account elsewhere. It is what the templates use, and what the AI builder uses for "tell me", "let the manager know" or "text the customer".

## Telling the business's people (`tell`)

```json
{"id": "tell_owner", "type": "connector", "connector": "notify@1", "action": "tell",
 "input": {"title": "New order", "message": "=trigger.body.order_id + ' from ' + trigger.body.customer_name"}}
```

| Input | |
| --- | --- |
| `message` | What to say, in plain words (up to 1,500 characters) |
| `title` | Optional first line, and an email's subject |
| `to` | `owner` (the default), `role` with `role`, or `members` with `members` (their sign-in emails) |

Each person gets it on the first chat channel they linked to Taskiem, WhatsApp then Telegram, and by email when they linked neither. A role nobody holds goes to the owners, so someone always hears. It only ever reaches the business's own active members. A retry reaches nobody twice.

The message goes through the chat [outbox](../whatsapp.md#delivery-and-retries) like approval requests (`kind` `notice`). It is rendered when it is sent, from the step's input in the run's history, opened then, so the outbox holds no text. Outside WhatsApp's 24-hour window it goes as the `taskiem_alert` template, its title and message on the template's one line. The output says how many people it went to, how many by chat and by email, and `nobody` when there was no one to tell.

## Texting a customer (`text`)

```json
{"id": "thank", "type": "connector", "connector": "notify@1", "action": "text",
 "input": {"to": "=trigger.body.data.customer.phone", "message": "Thank you for your payment"}}
```

The SMS comes from the business's name through Taskiem's own SMS ([messages](../messages.md)). It counts in the plan's monthly texts (`sms_monthly`). Past the allowance it is not sent and the output says `over_allowance`, but the step still succeeds. The per-recipient limits across Taskiem apply, as for storefront messages.

## On a deployment

The worker role delivers to the outbox and mail queue; the api role sends chat messages. Chat needs WhatsApp or Telegram configured. Email and SMS need `TASKIEM_MESSAGE_HASH_KEY` and the platform's mail and SMS ([operations](../operations.md)). Without any of them, `tell` reaches nobody and says so.
