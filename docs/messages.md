# Messages to people outside an organisation

Storefronts and hosted forms (Phase 5) reach people who have no Taskiem account: a customer who ordered, a respondent sent a form link, someone asked for a one-time code. Taskiem sends them SMS and email through the **platform's own providers**, on the organisation's behalf. Members are reached as before, by WhatsApp, Telegram and alert channels.

It is built and idle until something queues a message: the storefront and form milestones do (S3, S7).

## How a message travels

1. **Queued with what caused it.** A feature queues the message (`sender.Queue.Enqueue`) in the same transaction as the order or submission, so it is never lost. A message with a key already queued for the organisation is not queued twice.
2. **Checked when queued:**
   - **SMS:**
     - Only to the calling codes in `TASKIEM_SMS_COUNTRIES` (default Nigeria, 234).
     - Every SMS starts with the business's name, so the customer knows who wrote.
     - At most three parts: 459 plain characters, or 201 once it has a character outside GSM-7, such as ₦.
   - **Email:** comes from `"<business> via Taskiem" <TASKIEM_MESSAGE_FROM>`, with Reply-To set to the business's own address when it has one.
3. **Limited per recipient, across every organisation:** 5 an hour and 20 a day to one number or address. This stops a public form being used to flood someone's phone.
4. **Sealed:**
   - The address and the text are encrypted under the recipient's personal-data subject, so erasing that person erases what is queued for them.
   - Both are wiped once the message is finished.
   - What stays (when, which provider, how many parts) is for usage and support.
5. **Sent by the `api` role** every two seconds, at most `TASKIEM_SMS_RATE` a second (default 10) to each provider.

## Failures

| What happened | What Taskiem does |
| --- | --- |
| The provider was not reached, or rate-limited it | Tries the next provider, then again later |
| The provider refused for its own reasons (key, balance, sender ID, outage) | Tries the next provider, then again later; logged for operators |
| The number cannot be reached (invalid, barred, do-not-disturb) | Dead at once |
| An answer that may mean it was sent (a timeout after sending, a reply it cannot read) | Marked `unknown` and **never retried**: an SMS has no idempotency key, so retrying could send it twice |
| The mail server refused for good (5xx) | Dead at once |
| A one-time code not sent within 10 minutes | Dropped |
| Anything else | Retried after 30 s, 2 min, 10 min, 30 min, 2 h; dead after 6 attempts |

A process that stops halfway through a send leaves the message to be tried again after 10 minutes. That can send it twice, the price of never losing one.

## For operators

| Variable | Default | |
| --- | --- | --- |
| `TASKIEM_SMS_TERMII_API_KEY`, `TASKIEM_SMS_TERMII_SENDER_ID` | — | The first SMS provider. The sender ID must be approved by Termii |
| `TASKIEM_SMS_TERMII_CHANNEL` | `dnd` | `dnd` reaches numbers on Nigeria's do-not-disturb register, which transactional messages must; `generic` does not |
| `TASKIEM_SMS_AT_USERNAME`, `TASKIEM_SMS_AT_API_KEY`, `TASKIEM_SMS_AT_SENDER_ID` | — | Africa's Talking, the fallback |
| `TASKIEM_SMS_TERMII_URL`, `TASKIEM_SMS_AT_URL` | the providers' | Tests only: a fake provider. A loopback address is let through the egress guard for that provider alone |
| `TASKIEM_SMS_RATE` | `10` | Sends a second, per provider |
| `TASKIEM_SMS_COUNTRIES` | `234` | Calling codes SMS may go to. International premium routes are where SMS-pumping fraud makes its money, so add a country only when a business needs it |
| `TASKIEM_MESSAGE_FROM` | `TASKIEM_ALERT_FROM` | The no-reply address emails come from, sent through `TASKIEM_SMTP_URL` |
| `TASKIEM_MESSAGE_HASH_KEY` | — | 32 bytes, base64 (`openssl rand -base64 32`). Keys the recipient hash the limits count by; required once anything queues messages |

Check the configuration before customers rely on it:

```bash
taskiem messages test --sms 08031234567
```

```bash
taskiem messages test --sms 08031234567 --provider africastalking
```

```bash
taskiem messages test --email you@example.com
```

## Usage

`sender.Usage` counts what an organisation sent since a date: SMS parts and emails. Plan allowances (`sms_monthly`) are enforced from it in S8, with top-ups.
