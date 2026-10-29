# 0058 — Chat as a way in: signing up and connecting from WhatsApp, Telegram and Slack, beside the web

Date: 2026-10-29 · Status: **Proposed** · Plan: [chat as the way in](../spec/chat-way-in.md)

## Context

A person gets a Taskiem account in two ways today: the web form ([onboarding](../onboarding.md), [0022](0022-self-serve-onboarding.md)), which needs an email and a laptop or a patient phone browser, and a field agent who sits with the owner ([agents](../agents.md)). WhatsApp and Telegram are full clients of the platform ([WhatsApp](../whatsapp.md), [0035](0035-telegram-channel.md)), but only for a person who signed up on the web and linked their chat; a stranger who writes to the number or the bot is told to go to the web. Building in chat now goes all the way to publishing (N6), except where a workflow needs a service connected: then chat sends a link to the web's Connections page, behind a web sign-in.

The chat-first strategy ([gaps 1 and 2](../strategy/chat-first-nigeria.md#32-three-product-gaps-that-keep-the-chat-from-being-chat-first)) and the POS plan ([§5](../strategy/pos-terminals.md#5-what-this-does-not-do-yet-and-what-comes-next)) name signing up from WhatsApp and connecting from chat as the two milestones ahead of the pilot: a trader should never need a laptop.

The pieces exist. The field agent's sign-up already makes a business and an owner with no password from a proven WhatsApp number or Telegram contact; the conversation is one for every channel (`converse`); the guided connect form reads every key's help and checks from the manifest (N2); OAuth connections run through Taskiem's own apps (0034, 0051); WhatsApp forms (Flows) are encrypted to Taskiem. Slack is a third chat channel for approvals only ([0051](0051-platform-apps.md), K2): its bot sends but does not converse.

The founder asked for this way in on WhatsApp, the same on Telegram, the web's way in kept beside it, and Slack considered.

## Decision

1. **Chat is a way in, beside the web.** A person can sign up from Taskiem's shared WhatsApp number, its Telegram bot, and (by adding Taskiem's Slack app) Slack. The web form, linking a chat after it, and field agents stay as they are. One account model serves all: a chat-born person has an ordinary account, with no password and a placeholder sign-in name until they add an email, and can use the web, WhatsApp, Telegram and Slack in the same businesses.
2. **The channel proves who is writing; nothing else joins accounts.** A WhatsApp number, a Telegram account with its own shared contact (required to sign up there), or a Slack user in the installing workspace. An existing account is linked to a chat only with the existing proofs (the code, the Telegram link) or a one-time link sent to the account's email. An email Slack holds is not taken as confirmed and never merges accounts. A proven channel counts in place of a confirmed email for the holds that protect others (inviting members, API keys), not for sending email for the business.
3. **Consent is explicit and recorded.** Nothing is made before the person replies **Yes** to the terms and privacy notice; which versions they accepted, when and in which channel is recorded for every way in, the web form included.
4. **Secrets are never typed into a chat message.** A key is typed in a secure form: the WhatsApp Flow (encrypted to Taskiem), a Telegram Mini App (a Taskiem page opened inside Telegram, its user checked from Telegram's signed `initData`), or Taskiem's connect page. A key sent as a message is masked before it is kept and the person told to replace it.
5. **A connect link needs no web sign-in, for one connection only.** Chat sends a one-time link (15 minutes, once) for one service, one business and one person; it starts OAuth with 0034's checks unchanged, or shows the guided key form; it needs `secret.manage` when opened; the chat says what was connected.
6. **Only shared channels sign people up.** A stranger on a business's own WhatsApp number is its customer and gets its public menu or updates; messages from people outside a business (updates, invoices, riders, suppliers) are handled before any sign-up offer.
7. **Channels ship together.** Each capability of this way in is built once, behind the one conversation, and ships on WhatsApp and Telegram at once. Slack follows where its model allows: a Slack business is a workspace, installed through **Add to Slack**; its members join by the owner's say, never by being in the workspace.
8. **Off by default, with limits.** Chat sign-up is offered per channel only where the operator turns it on, within limits per identity, per hour and per country, with every outcome counted by its way in.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Keep sign-up on the web and only link chat afterwards (today) | Kept, but not enough: the traders Taskiem is for live on a phone, and the strategy's go-to-market starts in chat |
| A web sign-up link sent in chat | Still a web form and an email to confirm, the step that loses phone-only owners |
| Ask for keys as chat messages and delete them | Chat apps keep messages on their servers and on both phones; a bot cannot reliably delete a person's message; a key would sit in Taskiem's conversation records |
| Accounts keyed by phone number alone, with no consent step | No record of agreement to the terms, which the NDPA and the plans need |
| Join an existing account when the email from Slack (or typed in chat) matches | Whoever controls a workspace or types an address would take over that account |
| A Telegram bot or Slack app per business | Each needs setup at the provider that only a developer can do; the shared bot and app serve every business, as WhatsApp's shared number does |
| Sign-up on businesses' own WhatsApp numbers | Strangers there are the business's customers; offering them Taskiem accounts would be the business's number advertising Taskiem |

## Consequences

- A shared sign-up function, lifted from the field agent's, makes the person, the business and the binding for every way in; `tenant_onboarding.source` gains `whatsapp`, `telegram` and `slack`.
- New records: `terms_acceptances` (a person's), `chat_signups` (a person's state before there is a business, 30 minutes), connect links (a tenant's, one-time), Slack contacts, a platform-level Slack install request.
- New surfaces: the `taskiem_connect` Flow and the `taskiem_invite` template (WhatsApp); a Mini App page served by Taskiem (Telegram), which also gives Telegram forms for inputs and rows; `/channels/slack/events` for Taskiem's Slack app; the connect page `/c/<token>`.
- Configuration: `TASKIEM_CHAT_SIGNUP` (channels), `TASKIEM_CHAT_SIGNUP_PER_HOUR`, `TASKIEM_CHAT_SIGNUP_COUNTRIES`, `TASKIEM_TERMS_VERSION`, `TASKIEM_PRIVACY_VERSION`.
- People: the terms and privacy texts, publishing the Flow and approving the template, the Slack app's events URL, Messages tab and `files:read` ([plan §13](../spec/chat-way-in.md#13-c0-people-and-decisions)).

## Provenance

From the founder on 29 October 2026, after stock by chat (0057) merged: "write the plan for whatsapp as the way in. but don't remove the current way in today, they should coexists if possible. Also, whatever implementation we do for whatsapp, we should also do for telegram too. (and we should also consider doing them for Slack as well, is this even possible?)".
