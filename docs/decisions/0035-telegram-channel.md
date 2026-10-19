# 0035 — Telegram as a second chat channel: one conversation, two transports

Date: 2026-10-19 · Status: Accepted

## Context

The chat-first strategy puts approvals, run status, building and data tables in a chat app (spec 11). WhatsApp is built (decisions 0029, 0033 and the WhatsApp guide), but it needs a verified WhatsApp Business account, approved message templates and a 24-hour window before it can reach anyone, and the account is still being set up. A Telegram bot needs none of that: a token from @BotFather, a webhook, and people can be reached at once.

The WhatsApp code was written against WhatsApp only: the conversation held a `*whatsapp.Platform`, and every table keyed a conversation by an E.164 number.

## Decision

1. **One conversation, two transports.** The conversation (commands, inputs, builds, data tables, approvals, run outcomes, alerts) is shared. It talks to a `chatChannel` (`api/channel.go`): send, contact, touch, prefix, name, and whether the channel speaks for one tenant. `*whatsapp.Platform` and `*telegram.Platform` both implement it. What only WhatsApp has (Flows forms, the PIN, own numbers, public menus, templates and the window) stays behind a check that the channel is WhatsApp; Telegram falls back to inputs in chat and the step-up link, as WhatsApp already does without Flows.
2. **Addresses never collide.** A Telegram account is `tg:<user id>` where a WhatsApp number is `+<E.164>`. The tables the conversation shares (`chat_sessions`, `channel_languages`) accept both; nothing is renamed, and WhatsApp's rows and keys are unchanged.
3. **Identity stays per person, per channel.** `telegram_bindings`, link codes, contacts, update ids and button payloads mirror WhatsApp's person-level tables (00035): no tenant reads them, narrow `SECURITY DEFINER` functions reach them. A person has one account on each channel.
4. **Linking by a one-time deep link.** The signed-in person proves it is them (as for adding a passkey) and gets `t.me/<bot>?start=<code>`: 24 random bytes, stored as SHA-256, 10 minutes, single use, five an hour. The account that opens it is bound by its user id, never its username.
5. **The same decision tokens.** Approval buttons carry the WhatsApp token format. Telegram's 64-byte limit on button data is met by storing the token and sending `c:<id>`, resolvable only from the account it was sent to. With WhatsApp off, `TASKIEM_CHAT_TOKEN_KEY` signs them.
6. **Per channel delivery.** Approval notices, the outbox, run watches and tokens carry a channel. An approver linked on both hears on both; the approval itself closes on the first decision, and the other buttons then say it was already answered.
7. **No manual setup.** The `api` role registers the webhook at start, with a secret checked in constant time on every update.

## Alternatives considered

- **Copy the WhatsApp code for Telegram.** Rejected: two copies of approvals, inputs and builds would drift, and every rule fixed in one would be missed in the other.
- **Generalise the WhatsApp tables in place** (rename columns, drop the number checks everywhere). Rejected: forward-only migrations would rewrite live tables and functions keyed by number for no gain; widening two checks and adding a channel column is enough.
- **HTML or MarkdownV2 messages.** Rejected: every value in a message would need escaping, and one miss breaks or reshapes a message. Text goes as plain text with explicit bold entities.
- **A PIN typed in the chat for step-up.** Rejected: it would stay in the chat's history. A Telegram Web App PIN form is possible later.
- **Per-tenant bots** (like own WhatsApp numbers). Deferred: one platform bot covers the need now.

## Consequences

- Telegram is ready as soon as a bot token is set; WhatsApp can follow without changing what people do.
- The PIN policy's step-up is met on Telegram by the web link (passkey or authenticator), which is stronger.
- Telegram's Bot API servers are outside Nigeria, as Meta's are for WhatsApp; messages carry masked personal data only, as on WhatsApp.
- The shared tables keep their WhatsApp names (`whatsapp_outbox`, `whatsapp_tokens`, ...) although they now hold both channels.
