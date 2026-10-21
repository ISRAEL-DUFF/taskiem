# Updates on WhatsApp and Telegram for people outside the business

Most of the people a small business's workflows message never sign in to Taskiem: customers, applicants, and the staff other than the owner, HR and managers. A workflow reaches them by their **phone number**. They can choose, with one tap, to get those messages on WhatsApp or Telegram instead of by text ([decision 0040](decisions/0039-reaching-people-outside.md)). Nobody is ever asked for a chat ID.

Members of the business (people who sign in) link WhatsApp or Telegram on their account page instead, and hear from **Tell me** ([notify](integrations/notify.md)).

## How a person asks for updates

They tap a link, choose their app, and send the message it fills in (WhatsApp) or press **Start** (Telegram). Taskiem's bot or number answers: "You'll get updates from Acme Ltd here. Send STOP to stop them."

There are two places to find the link:

- **A form's thank-you page.** When the workflow behind a form messages the phone typed in it, the page shows **Get updates on WhatsApp** and **Get updates on Telegram**. If they don't tap, messages come by text.
- **The business's own link,** in **Settings > People you message**, with **Copy** and **Share on WhatsApp**. Share it once, for example in the staff WhatsApp group.

The link must prove the person's phone before every workflow can reach them on that app. A typed number is not enough, or anyone could get someone else's updates by typing their number. A phone is proven when:

| How they linked | The phone is proven by |
| --- | --- |
| WhatsApp | the number the message came from |
| Telegram | Telegram's **Share my number** button, which the bot shows: Telegram sends the number on the account |
| A form with phone verification (`"verify": "phone"`) | the code the form sent to it |

A Telegram link from a thank-you page whose phone was not proven reaches the person **about that request only**, and the bot asks them to share their number for the rest.

## Sending them messages

Use **Message a person** (`notify@1` `message`) with their phone from the form or a table:

```json
{"id": "tell_staff", "type": "connector", "connector": "notify@1", "action": "message",
 "input": {"phone": "=trigger.body.phone", "about": "your leave request", "title": "Leave request",
           "message": "=steps.decide.output.decision == 'approved' ? 'Your leave is approved.' : 'Your leave was not approved.'"}}
```

The person gets it, in this order of preference:

1. On the app they linked with the business, the most recent if both.
2. On the app they linked from this run's own form.
3. By SMS, from the business's name, counted in the plan's monthly texts.
4. By email, when an `email` is given and there is no phone to text.

The step's output says how (`via`: `whatsapp`, `telegram`, `sms`, `email` or `nobody`). On WhatsApp outside the 24-hour window, the message goes as the `taskiem_update` template: "[Acme Ltd] Update on your leave request: …", so `about` should say what they asked about. See [the integration page](integrations/notify.md#messaging-someone-outside-the-business-message) for every input.

The AI builder uses this step whenever a goal messages someone outside the business, even when it says "on Telegram". The publish check warns about a form field that asks for a chat ID or a Telegram username. The **Leave request** template is a complete example.

## Stopping

- **STOP** stops the business that messaged the person most recently, on that app. They then get its messages by text.
- **START** undoes it.
- Blocking the Telegram bot stops every business there.
- **Remove** in **Settings > People you message** stops the business reaching someone on chat apps.

Each business sees only its own contacts, with phones masked. Erasing a person's personal data erases their contact.

## For operators

You need these settings:

- `TASKIEM_MESSAGE_HASH_KEY` (it keys the contacts' hashes). Without it, updates are off and messages go by text.
- Hosted forms' public key, for the links on thank-you pages and the business's link.
- The Telegram bot ([telegram](telegram.md)) and/or the WhatsApp platform number ([whatsapp](whatsapp.md)).
- For WhatsApp, `TASKIEM_WHATSAPP_DISPLAY_NUMBER` set to the platform number (links point at it), and the `taskiem_update` template submitted to Meta with the others. Until it is approved, WhatsApp is used inside the 24-hour window only, and other messages go by text.

The api role records the bot's username and the number for the form host at start. Messages go through the chat outbox ([delivery and retries](whatsapp.md#delivery-and-retries)), which holds no address or text. Each message counts in the per-person limits across Taskiem that SMS uses (5 an hour, 20 a day).
