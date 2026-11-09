# Telegram integration

The connector is `connectors/telegram` (`telegram@1`). It uses the Telegram Bot API (10.3), built only from Telegram's public documentation: core.telegram.org/bots/api, core.telegram.org/bots/webhooks and core.telegram.org/bots/faq, read 6 October 2026.

## Create the bot

1. In Telegram, open a chat with **@BotFather** and send `/newbot`. Choose a display name and a username ending in `bot`.
2. BotFather replies with the bot token (`123456789:AA...`). Treat it as a password. `/revoke` in BotFather issues a new one.
3. To use the bot in a group, add it to the group. Privacy mode (BotFather `/setprivacy`) controls whether it sees every group message or only commands and replies to it.

## Connection

| Field | |
| --- | --- |
| `bot_token` | The token from BotFather. The connection test calls `get_me` |
| `webhook_secret` | Shown as "Check value". Optional: Taskiem sets the bot's webhook with its own secret while a published workflow starts on its messages. Needed only for a webhook set by hand. Choose 1 to 256 characters from `A-Z a-z 0-9 _ -`. `set_webhook` sends it to Telegram as `secret_token`, and every delivery must carry it |

## Actions

| Action | Method | Class | Notes |
| --- | --- | --- | --- |
| `get_me` | `getMe` | read | Connection test |
| `get_chat` | `getChat` | read | Chat id or `@username` |
| `send_message` | `sendMessage` | unsafe write | `text`, `parse_mode` (`MarkdownV2`, `HTML`, `Markdown`), `reply_markup` (an inline keyboard or reply keyboard, passed through), `reply_to_message_id`, `disable_notification`, `protect_content`, `disable_link_preview`, `message_thread_id` |
| `send_photo` | `sendPhoto` | unsafe write | `photo` is an HTTP URL (Telegram fetches it, up to 5 MB) or a `file_id` |
| `send_document` | `sendDocument` | unsafe write | `document` is a URL (Telegram says only .PDF and .ZIP work by URL) or a `file_id` |
| `edit_message_text` | `editMessageText` | idempotent write | `chat_id` + `message_id`, or `inline_message_id`. Use it to replace an approval keyboard with the decision |
| `answer_callback_query` | `answerCallbackQuery` | idempotent write | Call after every button press, even without text, or the user's client keeps showing a spinner |
| `set_webhook` | `setWebhook` | idempotent write | Always sends `secret_token` = the connection's `webhook_secret` |
| `delete_webhook` | `deleteWebhook` | idempotent write | |
| `get_webhook_info` | `getWebhookInfo` | read | Shows `pending_update_count` and the last delivery error |
| `get_file` | `getFile`, then the file | read | A file from an update kept as a person's run file (scanned, personal, under a neutral name), up to a bot's 20 MB; the run gets its reference. `sender` (the chat or user id) ties the file to that person, so erasing them deletes it |
| `send_voice` | `sendVoice` | unsafe write | A run file (uploaded with the message), or a `voice` file_id or URL. OGG/Opus (an Ogg file that does not hold Opus, such as Vorbis, is refused: Telegram would not play it), MP3 or M4A |
| `send_audio` | `sendAudio` | unsafe write | A run file, or an `audio` file_id or URL. MP3 or M4A |
| `send_chat_action` | `sendChatAction` | idempotent write | `typing` (default) or `record_voice`, for up to 5 seconds or until the bot sends |

Send outputs include `message_id`, `chat_id` (as a string), `date` and Telegram's whole `message`.

**Never twice.** Telegram has no idempotency key, so the sends are unsafe writes. If a send's outcome is unknown (the connection dropped after sending, or a 5xx response), the step parks for a person rather than risking a second message. A **429** means flood control refused the request, so nothing was sent. The connector marks it retryable and proves-not-sent, and passes `parameters.retry_after` on, so even a send is retried. Edits, button answers and webhook changes set state and are safe to repeat. Telegram ignores the engine's key, so the connector does not send it. An edit that Telegram rejects as `message is not modified` succeeds with `modified: false`.

**Errors.** A 400 is fatal; a group upgraded to a supergroup reports its new chat id. A 401 (bad token) and a 403 (for example, the user blocked the bot) are fatal. A 5xx is an unknown outcome. The bot token is part of every request URL, so the connector replaces it with `<bot_token>` in every error message.

## Receiving updates

The trigger is `update`. **Taskiem sets the bot's webhook itself, while a published workflow starts on it** (connector/v1 `registration: connection_when_used`, [decision 0021](../decisions/0021-remote-trigger-registration.md); [decision 0062](../decisions/0062-customer-assistant.md) C7). Setting a bot's webhook stops whatever else read its updates (another system's webhook, or `getUpdates` polling), so:

- **A bot used only to send is never touched.** Saving or checking the connection sets nothing; nor does publishing a workflow whose steps only send with it.
- **The first published workflow on the `update` trigger sets the webhook** for its connection (the trigger's `connection`, or the environment's only Telegram connection when it names none; with several and none named, the subscription fails with "several connections" until one is named). Each publish of such a workflow sets it again, which also repairs it.
- **One webhook per bot.** Further workflows on the same bot share it: one update starts every workflow subscribed to its event.
- **It is removed with the last one**: when no workflow published in that environment starts on the bot's updates any more (each unpublished, or published again on another trigger), Taskiem deletes the webhook, unless someone has since pointed the bot elsewhere. Deleting the connection removes it too.
- **One bot per environment.** A bot has one webhook, so it cannot serve dev and prod at once. When one bot is connected in two environments and both have a published workflow on it, the environment that set the webhook first keeps it, and the other's subscription fails with "this bot already receives messages for prod; use one bot per environment" (on the Connections page and in the publish answer) instead of taking the webhook back and forth. Once the first environment lets the bot go, the other is tried again at once and takes it. Use a separate bot (from @BotFather) for each environment.

The URL is `<hooks URL>/<tenant>/connectors/telegram@1/update?env=<env>&connection=<name>&subscription=<id>`, and the secret token is derived from the bot token and the subscription, so it stays the same each time Taskiem sets the webhook again. The connection page shows the webhook's health from `getWebhookInfo`, checked every 10 minutes: **failing** when Telegram reports a delivery error with updates waiting, or a delivery error in the last hour even with nothing waiting (Telegram retries, so a webhook that fails now and then shows no backlog between failures); **missing** when the bot's webhook is no longer Taskiem's (set by hand or elsewhere, or deleted); **healthy** otherwise. Publishing the workflow again sets it back.

A webhook can still be set by hand (`set_webhook`, with the connection's `webhook_secret`), for a bot whose connection predates this; deliveries without `subscription=` in the URL are checked against `webhook_secret`, and a connection without one refuses them. Setting a webhook by hand replaces Taskiem's, and the health check then shows it missing.

Telegram only delivers to ports 443, 80, 88 and 8443, over HTTPS with a valid certificate. Each delivery is checked against the `X-Telegram-Bot-Api-Secret-Token` header (scheme `header_secret`, compared in constant time).

**What a customer sends** (decision 0062, P1): who wrote (`from` and `chat` ids, names, usernames) and what they wrote (text, captions, contacts, locations, button data) are declared personal data, sealed in run history and readable by the run's steps. `get_file` downloads a file from an update into the run's files (up to a bot's 20 MB); `send_voice`, `send_audio` take a run file; `send_chat_action` shows typing or recording ([Files in runs](../files.md)).

| | |
| --- | --- |
| **Event** | The Update field that is present: `message`, `edited_message`, `channel_post`, `edited_channel_post`, `callback_query`, `my_chat_member`, `chat_member`, `chat_join_request`, `message_reaction`, `inline_query`, `poll_answer`, and the rest of Bot API 10.3's list. Telegram says at most one is present |
| **Dedup** | `update_id`. Telegram recommends it for ignoring repeated updates |
| **Correlation** | The chat id as a string: `message.chat.id` (and the edited, channel, business and guest variants), `callback_query.message.chat.id`, and the chat of member, join-request and reaction updates. Updates without a chat (inline queries, poll answers, button presses on inline-mode messages) wake no waiting run but still start subscribed workflows |

**Why the chat id.** A typical approval sends a message with an inline keyboard. The run then waits on `telegram@1:update` with correlation `steps.ask.output.chat_id`. The person's button press arrives as event `callback_query`, and the workflow reads `trigger.body.callback_query.data` to see which button was pressed. A typed reply in the same chat also wakes the run (`message`), so put the run id in each button's `callback_data` (up to 64 bytes) and check it. In a busy group, any message in the chat wakes the wait. Waits in groups should check the event and the data, and loop if they do not match.

Telegram keeps undelivered updates for 24 hours and retries failed deliveries. Delivery stops after "a reasonable amount of attempts", which Telegram does not quantify. `get_webhook_info` shows the backlog and the last error.

### Answering customers: the guard

A workflow that answers the business's customers puts `"guard": true` (or its settings) in the trigger's config. The trigger reads each event as a customer's message (its `conversation` block), and the guard decides before a run starts: STOP and START, limits per customer, asking for a person, the kinds of message, the owner's pause. Only new messages in one-to-one chats start a run: a bot added to a group answers no one there, and edits, channel posts and button presses on old messages start nothing. A voice note or audio is fetched when it arrives. The run reads `trigger.guard` (`outcome`, `reply`, `notice`, `sender`, `text`, `channel`, `file`). See [keeping a customer assistant safe](../assistant-guard.md).

## To confirm before go-live

1. Telegram documents that it retries failed deliveries but not how often or for how long. Check `get_webhook_info` after a planned outage.
2. The `message is not modified` text is how Telegram reports a no-op edit in practice. Its documentation does not describe it, and descriptions "are subject to change".
3. Whether a repeated `answerCallbackQuery` (a retry after a lost response) returns an error. If it does, that retry fails the step even though the first answer reached the user.
4. Group traffic. With privacy mode off, every group message is a delivery and starts every workflow subscribed to `message`.
