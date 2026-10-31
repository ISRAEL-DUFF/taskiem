# Telegram

People use Taskiem from Telegram exactly as from [WhatsApp](whatsapp.md): ask what ran, start workflows, approve requests, build workflows with AI, work with [data tables](data-tables.md), and receive alerts. It is the same conversation with the same rules, roles and limits, run by one platform bot ([decision 0035](decisions/0035-telegram-channel.md)). A Telegram bot needs no business verification, so it is the chat channel to start with while a WhatsApp Business account is being approved; both can run side by side.

## For operators: setting up the bot

1. In Telegram, talk to [@BotFather](https://t.me/BotFather): `/newbot`, choose a name and a username ending in `bot`. Keep the token it gives you secret.
2. Set, on every role:

| Variable | What |
| --- | --- |
| `TASKIEM_TELEGRAM_BOT_TOKEN` | The bot's token. Turns the channel on. A secret: Kubernetes Secret or the VM's env file, never chart values |
| `TASKIEM_TELEGRAM_BOT_USERNAME` | The bot's username without `@`, for the links people open to link their account |
| `TASKIEM_TELEGRAM_WEBHOOK_SECRET` | 1 to 256 of `A-Z a-z 0-9 _ -` (`openssl rand -hex 32`): Telegram sends it with every update, and anything without it is refused |
| `TASKIEM_CHAT_TOKEN_KEY` | Only when WhatsApp is off: 32 bytes, base64 (`openssl rand -base64 32`). Signs approval buttons. With WhatsApp on, `TASKIEM_WHATSAPP_TOKEN_KEY` signs them on both channels |
| `TASKIEM_TELEGRAM_API_URL` | Tests only: a fake Bot API. A loopback address here is let through the egress guard for the bot only |

3. Nothing else: when the `api` role starts with an `https` `TASKIEM_PUBLIC_URL`, it registers `<TASKIEM_PUBLIC_URL>/channels/telegram` as the bot's webhook with the secret, retrying until Telegram accepts it. Updates are served on the `edge` role (and on the `api` role when it runs every role); the chart's ingress already sends `/channels/` to the edge.

Telegram delivers only to `https` URLs on ports 443, 80, 88 or 8443. A local install on plain HTTP has no webhook; the log says so.

## For people: using Taskiem on Telegram

**No account yet?** Where the operator turned it on, open the bot (or the website's `t.me/<bot>?start=signup` link) and choose **Start my business**: the same questions as on WhatsApp, then **Share my number**, which must be your own. The business and your account are made from the chat ([signing up from chat](onboarding.md#signing-up-from-chat)). A number that already uses Taskiem on WhatsApp makes no second account: an account made in chat gets this Telegram account too, and any other gets a link by email ([one person, many doors](onboarding.md#one-person-many-doors)).

**Invited.** An owner's `invite +234… as viewer` on Telegram gives them a `t.me/<bot>?start=inv_…` link to forward, since a bot cannot write first. Opening it shows the invitation with **Join**; after the terms, Taskiem asks you to share your own number, which must be the one invited: the link alone, which anyone could be forwarded, joins no one.

**Link your account.** Under Account > Telegram, choose **Link Telegram** and confirm it is you (passkey, authenticator code or password, as when adding a passkey). Open the link it shows on the device where you use Telegram, and press **Start**: the Telegram account that opens the link is linked to you. A link works once, for 10 minutes; at most five are made an hour. An account can be linked to one Taskiem account, and you have one Telegram account at a time (linking another replaces it). Unlink it on the same page.

**What you can send** is what you can send on WhatsApp ([the table](whatsapp.md#for-people-using-taskiem-on-whatsapp)): `help`, `status`, `run <workflow>`, `approvals`, `build <what to automate>`, `tables`, `add to <table>`, `find <table> <text>`, `switch <organisation>`, `cancel`, `language`, and voice notes where they are on. The records commands are the same too ([decision 0060](decisions/0060-no-sheet-needed.md)): `new table`, `change 2` and `delete 2` after a find, a CSV or Excel file sent as a document to bring a list in, the [debtor book](whatsapp.md#the-debtor-book) (`Ada owes 5,000`, `Ada paid 2,000 cash`, `who owes me?`, `send Ada her balance`), and [questions over the tables](whatsapp.md#data-tables) (`how many customers in Ikeja?`). A personal value when changing a row is asked in the chat, since Telegram has no row form yet. Telegram's `/help`, `/approvals` and so on work too. Every message from Taskiem starts with the organisation's name in brackets.

**Inputs** are asked one by one in the chat (Telegram has no forms), checked against the workflow's schema, and nothing starts until you reply **yes** to the summary.

**Approving.** A request comes with **Approve** and **Reject** buttons. A button works once, only from your account, for 24 hours or until the request times out; the web app's rules apply. When the policy needs step-up, Taskiem sends a link: open it signed in to Taskiem and confirm with your passkey or authenticator code within 10 minutes. (The WhatsApp PIN is entered in a WhatsApp form; on Telegram the link is used instead, which satisfies a PIN policy.) If you have linked both WhatsApp and Telegram, a request goes to both, and deciding on either closes it.

**Matching payments.** A payment Taskiem could not match to an invoice on its own comes with **Yes, match** and **No**, as on WhatsApp ([matching payments](whatsapp.md#matching-payments)), to members who manage invoices.

**What messages never show** is as on WhatsApp: no secrets, and personal data masked.

**People outside a business** (customers, staff who don't sign in) can get a business's workflow messages from the same bot, without a Taskiem account: they open the business's link and press **Start** ([updates](updates.md)). To them the bot only sends updates, and understands STOP and START.

## Connecting a service

`connect <service>` works as on WhatsApp ([connecting a service from the chat](whatsapp.md#connecting-a-service-from-the-chat)), with a connect link on a button. For a key-based service the button opens the connect page as a Mini App inside Telegram: Telegram hands the page its signed `initData`, and the key is taken only when that is signed for Taskiem's bot (HMAC-SHA-256 under the key derived from the bot token), at most ten minutes old, and for the account the link was sent to. A service that signs in (Google) opens in the browser instead, since Google refuses sign-in inside apps' own browsers. Telegram's web client shows Mini Apps in a frame, which the console's frame policy refuses: on the web, open the link in the browser.

## Alerts to Telegram

An alert channel of kind `telegram` names members by email; each alert goes to those who have linked Telegram, one message each, retried on its own (Settings > Alerts, or `POST /v1/alerts/channels` with `{"kind": "telegram", "to": [...]}`).

## Differences from WhatsApp

| WhatsApp | Telegram |
| --- | --- |
| 24-hour window, approved templates outside it, template costs | None: the bot can write to a person at any time after they pressed Start |
| A 6-digit code sent to the number | A one-time link opened from the account |
| Forms (Flows) for inputs and the approval PIN | Inputs in chat; step-up by the web link |
| Own numbers and public menus per organisation | One platform bot; not yet per organisation |
| Phone number as identity | The Telegram user id (never the username, which can change) |
| Photos (a product's, a logo) through the media API; images sent by link | Photos (the largest size, or an image sent as a file) through `getFile`; images sent with `sendPhoto` by link |

## Security model

| Concern | Control |
| --- | --- |
| Forged updates | `X-Telegram-Bot-Api-Secret-Token` is compared in constant time with `TASKIEM_TELEGRAM_WEBHOOK_SECRET` before the body is read; anything else gets 401 |
| Retried updates | Each `update_id` is claimed in the database before it is acted on |
| Who is speaking | A message speaks for the person whose binding holds the sender's Telegram user id, in the organisation the account is working in, with that person's roles there at that moment. Only one-to-one chats with the bot are answered: in a group, anyone present would read the replies |
| Linking someone else's account | The link is made by the signed-in person after proving it is them, carries 24 random bytes, is stored only as its SHA-256, works once for 10 minutes, and binds the account that opens it. An account belongs to one person |
| Buttons | Telegram's button data holds 64 bytes, so a button carries `c:<22 random characters>`; the decision token it stands for is kept in `telegram_callbacks`, usable only from the account it was sent to, for 8 days. The token itself is the WhatsApp one: signed, single use, bound to the approver, and checked against its row |
| Step-up | Never by chat text; the hand-off link, as on WhatsApp |
| A compromised account | The WhatsApp limits apply per account (`tg:<id>`); unlinking in the web app ends it |
| The bot token | Only in the environment; never in errors or logs (it is part of Bot API URLs, and is scrubbed) |
| Tenant isolation | Bindings, link codes, contacts, update ids and button payloads are a person's: no tenant reads them, and only narrow functions reach them. Conversation state, tokens, notices, run watches and the outbox are tenant data under forced row-level security, shared with WhatsApp and marked with their channel |
| Audit | `telegram.link_made`, `telegram.link` (in every organisation the person belongs to), `telegram.unlink`, and `approval.decide`, `run.start` and `table.row.add` with `channel: telegram` |
