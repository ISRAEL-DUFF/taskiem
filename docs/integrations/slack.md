# Slack integration

The connector is `connectors/slack` (`slack@1`). It uses Slack's Web API with a bot token, and takes the Events API and interactivity (button clicks, modal submissions) as triggers. Built from Slack's public developer documentation, read 6 October 2026 (api.slack.com now redirects to docs.slack.dev); the manifest header lists the pages.

## Setting up the Slack app

1. At <https://api.slack.com/apps>, **Create New App** → *From scratch*, in the workspace the workflows will use.
2. **OAuth & Permissions** → *Bot Token Scopes*. Add what the actions you use need:

   | Scope | For |
   | --- | --- |
   | `chat:write` | `post_message`, `update_message`, `post_ephemeral` |
   | `chat:write.public` | posting in public channels the bot has not joined (optional) |
   | `chat:write.customize` | `username`, `icon_emoji`, `icon_url` (optional) |
   | `reactions:write` | `add_reaction` |
   | `channels:read`, `groups:read`, `im:read`, `mpim:read` | `list_conversations`, `get_conversation` (per channel type) |
   | `users:read`, `users:read.email` | `lookup_user_by_email` |
   | `files:write` | `upload_file` |

3. **Install to Workspace**, then copy the *Bot User OAuth Token* (`xoxb-...`) into the connection's `bot_token`.
4. **Basic Information** → *App Credentials* → *Signing Secret*: copy it into `signing_secret` (needed only for the triggers).
5. Invite the bot to private channels it should post in (`/invite @yourapp`).

## Connection

| Field | |
| --- | --- |
| `bot_token` | Bot User OAuth Token, `xoxb-...` |
| `signing_secret` | Verifies events and interactions (`X-Slack-Signature`, scheme `slack_v0`) |

The connection test is `auth_test` (`auth.test`).

## Actions

| Action | Method | Class | Notes |
| --- | --- | --- | --- |
| `auth_test` | `auth.test` | read | Connection test |
| `post_message` | `chat.postMessage` | unsafe write | `text`, `blocks`, `thread_ts`, `reply_broadcast`, `metadata`, ... Returns `ts`, `message_key` and `thread_key` |
| `update_message` | `chat.update` | idempotent write | A set operation: repeating it leaves the same message |
| `post_ephemeral` | `chat.postEphemeral` | unsafe write | Visible to one user |
| `add_reaction` | `reactions.add` | idempotent write | `already_reacted` is reported as success |
| `list_conversations` | `conversations.list` | read | Cursor paging (`cursor` / `next_cursor`) |
| `get_conversation` | `conversations.info` | read | |
| `lookup_user_by_email` | `users.lookupByEmail` | read | `found: false` for an unknown or deactivated address |
| `upload_file` | `files.getUploadURLExternal`, POST to the upload URL, `files.completeUploadExternal` | unsafe write | Text `content` or `content_base64`, up to 50 MB; shared in `channel_id` when given |

Write methods are sent as JSON; reads and the file methods are form-encoded, which every method accepts.

**Never twice.** Slack has no idempotency key for messages. A `post_message` whose outcome is unknown (connection dropped after sending, HTTP 5xx, `internal_error`/`fatal_error`, which Slack says may have partly succeeded) parks for a person instead of being posted again. `upload_file` failures before `files.completeUploadExternal` share nothing and are retried; the completion itself is not idempotent ("can only be called once").

`update_message` and `add_reaction` are declared `idempotent_write` because repeating them is harmless. The manifest's idempotency field (`request_key`) only satisfies the contract: Slack has no key and the connector does not send it.

**Errors.** Slack answers most failures with HTTP 200 and `{"ok": false, "error": "<code>"}`:

| Code | Class |
| --- | --- |
| `ratelimited`, `rate_limited`, HTTP 429 | retryable and *not sent* (nothing was done; unsafe writes may retry), `Retry-After` kept on the error |
| `service_unavailable`, `team_added_to_org`, `org_login_required`, `request_timeout`, HTTP 503 | retryable |
| `internal_error`, `fatal_error`, other HTTP 5xx | unknown outcome |
| `invalid_auth`, `not_authed`, `token_revoked`, `missing_scope`, `channel_not_found`, `not_in_channel`, `is_archived`, `invalid_blocks`, `no_text`, `msg_too_long`, `message_not_found`, `cant_update_message`, ..., and any code starting `invalid_`, `missing_`, `not_`, `restricted_action`, `too_many_`, `cant_`, `cannot_` or ending `_not_found`, `_too_long`, `_too_large`, `_disabled`, `_restricted` | fatal |
| anything else | unknown outcome (the safe default) |

## Triggers

### `events` (Events API)

In the app's **Event Subscriptions**, turn events on and set the Request URL to

```
<public URL>/hooks/<tenant>/connectors/slack@1/events?env=prod
```

(add `&connection=<name>` when the tenant has several Slack connections). Slack first sends a signed `url_verification` POST; the trigger's `handshake` answers it with the challenge. Then subscribe to bot events (`message.channels`, `reaction_added`, `app_mention`, ... with their scopes) and reinstall the app.

- Signature: `slack_v0`, HMAC-SHA256 of `v0:<X-Slack-Request-Timestamp>:<raw body>` keyed with the signing secret, compared with `X-Slack-Signature`; timestamps more than five minutes from now are refused.
- Event type: `body.event.type` for `event_callback` (`message`, `reaction_added`, ...); otherwise `body.type` (`app_rate_limited`).
- Dedup: `event_id`, which is the same on Slack's retries (it retries up to three times, `x-slack-retry-num`).
- Correlation, for runs waiting on `slack@1:events`:
  - a message in a thread: `<channel>:<thread_ts>`, which is `post_message`'s `thread_key`, so a run can post and wait for a reply in that thread;
  - a reaction: `<item.channel>:<item.ts>`, which is `post_message`'s `message_key`.
  - other events have none; workflows subscribed to the event type still start.

### `interactions` (buttons, modals, shortcuts)

In **Interactivity & Shortcuts**, set the Request URL to

```
<public URL>/hooks/<tenant>/connectors/slack@1/interactions?env=prod
```

Slack posts `application/x-www-form-urlencoded` with one field, `payload`, holding JSON. Ingest exposes form fields as `body.<field>`, so expressions see `body.payload` as a **string**: Taskiem's expression language has no JSON decoding. The trigger therefore:

- takes the event type by matching `"type": "<kind>"` in the text: `block_actions`, `view_submission`, `view_closed`, `shortcut`, `message_action`, `block_suggestion`, else `interaction`. Inner objects' types (`button`, `modal`, ...) never equal these, and JSON-escaped quotes inside user text do not match.
- reads correlation from a marker the workflow writes: put `taskiem-corr:<key>` in a button's `value`, a block's `block_id` or a modal's `private_metadata`, optionally followed by `|<anything>` (`taskiem-corr:run-42|approve`). The correlation is the text after the **first** marker up to `"` or `|`. Give every button of a message the same key: the clicked action and the message's blocks both appear in the payload, and the first marker is not necessarily the clicked one.
- dedups on the body's hash (no dedup expression): a retried delivery is byte-identical.

Limits, honestly: which button was clicked cannot be read by an expression, so it is not part of the event or correlation. A run woken by the signal (or a workflow started by it) gets the whole `payload` string in the signal body and must parse it in a code step (`json.loads`) to see `actions[0].action_id`/`value`. A `taskiem-corr:` marker typed by a user into message text would also match. Slack expects HTTP 200 within three seconds; ingest acknowledges before the workflow runs.

## Needs a person

- A Slack workspace and an app installed there for a live check: post, update, react, upload, the Events URL handshake, and a real button click through ingest.

## To confirm with Slack

1. That read methods honour form-encoded arguments exactly as JSON ones (the connector form-encodes reads; the reference lists both content types).
2. Whether `ratelimited` returned with HTTP 200 (as opposed to 429) also carries `Retry-After` (the error table says "refer to the Retry-After header").
3. Whether an undocumented `client_msg_id` argument gives `chat.postMessage` duplicate protection (the error list mentions `duplicate_message_not_found`/"associated with client_msg_id", but the argument is not documented, so the connector does not use it).
4. Whether every interactivity payload is signed with `slack_v0` (the verification page names Events API, shortcuts and slash commands).
5. Whether the `payload` JSON is ever sent with escaped forward slashes or other escaping that could split a `taskiem-corr:` key (keys of `[A-Za-z0-9_.-]` are unaffected).
