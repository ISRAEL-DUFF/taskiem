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
| `template` | Optional: the WhatsApp [template](../whatsapp.md#templates) that carries the message outside the 24-hour window in place of `taskiem_alert`, by name, written out: one of the registry's templates for the business's own people, filled from text alone (`taskiem_daily_close`, `taskiem_daily_summary`) |
| `template_vars` | With `template`: its variables, every one but the business's name (which Taskiem adds), each a line of text or an expression; `day` and `summary` for `taskiem_daily_close` |

Each person gets it on the first chat channel they linked to Taskiem, WhatsApp then Telegram, and by email when they linked neither. A role nobody holds goes to the owners, so someone always hears. It only ever reaches the business's own active members. A retry reaches nobody twice.

The message goes through the chat [outbox](../whatsapp.md#delivery-and-retries) like approval requests (`kind` `notice`). It is rendered when it is sent, from the step's input in the run's history, opened then, so the outbox holds no text. Outside WhatsApp's 24-hour window it goes as the `taskiem_alert` template, its title and message on the template's one line; a step that names a `template` goes as that template, its variables from `template_vars`, once Meta approved it in the number's account ([the registry](../whatsapp.md#the-template-registry)), and as `taskiem_alert` until then, the message marked sent with why. Publishing checks the template: it must be one a Tell me message can carry, and `template_vars` must hold exactly its variables (the `school-daily-close` template shows it). The output says how many people it went to, how many by chat and by email, and `nobody` when there was no one to tell.

## Messaging someone outside the business (`message`)

```json
{"id": "tell_staff", "type": "connector", "connector": "notify@1", "action": "message",
 "input": {"phone": "=trigger.body.phone", "about": "your leave request", "message": "Your leave is approved."}}
```

| Input | |
| --- | --- |
| `phone` | Their phone number, in any form (0803 123 4567, +234…) |
| `email` | Their email: used when there is no phone to text |
| `message` | What to say, in plain words (up to 1,500 characters; an SMS is cut to three parts) |
| `title` | Optional first line, and an email's subject |
| `about` | What it is about, in a few words ("your leave request", "order AD-12"): WhatsApp's update template names it |

It goes:

1. On the WhatsApp or Telegram the person linked with the business.
2. On the one they linked from this run's own form.
3. By SMS from the business's name (`sms_monthly`).
4. By email.

The output's `via` says which (`nobody` when none). On a chat app it goes through the chat outbox (`kind` `message`), like `tell`, rendered from the run's history when sent. Outside WhatsApp's window it goes as the `taskiem_update` template. If the person stopped, blocked the bot, or the template is not approved yet, it goes by SMS instead. Never ask anyone for a chat ID: see [updates](../updates.md) for how people link their app.

## Texting a customer (`text`)

```json
{"id": "thank", "type": "connector", "connector": "notify@1", "action": "text",
 "input": {"to": "=trigger.body.data.customer.phone", "message": "Thank you for your payment"}}
```

The SMS comes from the business's name through Taskiem's own SMS ([messages](../messages.md)). It counts in the plan's monthly texts (`sms_monthly`). Past the allowance it is not sent and the output says `over_allowance`, but the step still succeeds. The per-recipient limits across Taskiem apply, as for storefront messages.

## On a deployment

The worker role delivers to the outbox and mail queue; the api role sends chat messages. Chat needs WhatsApp or Telegram configured. Email and SMS need `TASKIEM_MESSAGE_HASH_KEY` and the platform's mail and SMS ([operations](../operations.md)). Without any of them, `tell` reaches nobody and says so. `message` on chat apps also needs `TASKIEM_MESSAGE_HASH_KEY` ([updates](../updates.md#for-operators)).
