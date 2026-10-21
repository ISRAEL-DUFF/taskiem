# 0039 — Reaching people outside a business on WhatsApp and Telegram

Date: 2026-10-21 · Status: Accepted

## Context

Taskiem's chat channels serve members only: a WhatsApp number or Telegram account is linked to a Taskiem account ([decision 0035](0035-telegram-channel.md)), and `notify@1` `tell` reaches members on it. Everyone else a workflow messages (customers, applicants, and most of a small business's staff, who never sign in to Taskiem) is reached by SMS or email ([messages](../messages.md)), though nearly all of them use WhatsApp and many use Telegram.

So when an owner asked for a leave-request workflow that tells the employee the decision "on Telegram", the AI builder used `telegram@1` `send_message`. That needs a chat id, so the builder put a **Telegram chat ID** field on the form, which no employee can fill in. A username does not help either: a bot can write only to a chat whose person pressed **Start**.

## Decision

1. **One tap per person and business, on the platform's bot and number.** A link carries a one-time code (24 random bytes, kept as SHA-256, seven days, single use):
   - Telegram: `t.me/<bot>?start=u_<code>`.
   - WhatsApp: `wa.me/<number>?text=…U-<code>`, on the business's own number when it has one, else the shared number. Sending the message is the opt-in Meta asks for, and it opens the 24-hour window.
2. **Contacts belong to a business and are keyed by a proven phone.** A contact is a keyed hash of an E.164 phone number (`TASKIEM_MESSAGE_HASH_KEY`), with the phone and each chat address sealed under the person's personal-data subject. A phone is proven when Telegram shares it with its own button (the contact's `user_id` is the sender's), when it is the number a WhatsApp message came from, or when a form verified it with a code.
3. **An unproven link reaches its own submission only.** A typed phone proves nothing: binding a chat to it would let anyone receive another person's updates by typing their number. Such a link (`reach_request_links`) is used only by the run of the submission it came from, and only for the phones typed in it.
4. **Where links are offered.**
   - A form's thank-you page offers them when the deployed workflow messages a phone from the form (`notify@1` `message` with `phone` reading `trigger.body.<field>` or `trigger.form.respondent.phone`; recorded at deploy as the form's `reach` fields).
   - A business's standing page, `/u/<public id>` on the form host, makes a code only when a button is pressed.
   - The form host mints codes through narrow functions as `taskiem_public`, the codes' phones sealed with the public key for the api role.
5. **One step finds the person: `notify@1` `message`.** It takes a phone and/or an email, never a chat id. It tries:
   1. the contact's most recently linked channel;
   2. else the link of the run's own submission;
   3. else SMS (`sms_monthly`);
   4. else email.

   Chat messages go through the chat outbox like notices: the outbox row names a contact or a submission's link, never an address or text. Each message counts against the cross-tenant per-person limits SMS uses.
6. **WhatsApp outside the window uses `taskiem_update`** (utility): `[{{1}}] Update on {{2}}: {{3}}`, where `about` says what the person asked about. While Meta has not approved it, and when a person stopped or blocked the bot, the message goes by SMS instead.
7. **Outsiders cannot use the bot or number for anything else.** Their whole vocabulary is:
   - STOP, which stops the business that reached them most recently on that channel;
   - START, which undoes the last stop.

   Anything else gets one fixed answer naming who sends them updates. A person who is also a member is a member on their own linked account.

## Alternatives considered

- **Ask for a Telegram username.** Rejected: a bot cannot message one.
- **A business's own bot (a token from @BotFather) for its updates.** Deferred, as in 0035: one platform bot needs no setup, and messages carry the business's name.
- **Link a chat to whatever phone the form typed.** Rejected: anyone could receive someone else's updates by typing their number.
- **Members-only forms.** These are right for members, but most staff of the businesses Taskiem serves are not members.

## Consequences

- No workflow, and no form, ever handles a chat id.
- Staff and customers who never tap a link still hear by SMS, as before.
- WhatsApp for outsiders needs the Business account, the platform number's `TASKIEM_WHATSAPP_DISPLAY_NUMBER` (links point at it), and the `taskiem_update` template's approval; until then it falls back to SMS.
- A person stopping one business does not stop others; blocking the bot stops all of them on Telegram.
