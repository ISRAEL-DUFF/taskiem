# Slack integration

The connector is `connectors/slack` (`slack@1`). It uses Slack's Web API with a bot token, and takes the Events API and interactivity (button clicks, modal submissions) as triggers. Built from Slack's public developer documentation, read 6 October 2026 (api.slack.com now redirects to docs.slack.dev); the manifest header lists the pages.

## Add to Slack (one click)

Where Taskiem's own Slack app is registered (`TASKIEM_OAUTH_SLACK_CLIENT_ID`, `_CLIENT_SECRET` and `_WEBHOOK_SECRET`: [operations](../operations.md#configuration)), a business connects Slack with **Connect Slack**: Slack asks a workspace admin to allow Taskiem's app, and the connection keeps the bot token, the workspace's id (`team_id`) and its name, taken from the token answer (`oauth.v2.access`). Nothing is pasted. Deleting the connection gives the token back (`auth.revoke`).

The app's events and button clicks come to Taskiem's app URLs for every workspace, verified with the app's signing secret and delivered to the connections of the workspace they name (decision 0051, connector/v1 rule 18), so the triggers below work the same whichever way Slack was connected. When the app is uninstalled, or its bot token revoked, the workspace's connections are marked expired and the Connections page says to connect again.

Taskiem's Slack app (once, by Taskiem's operators):

1. Create the app at <https://api.slack.com/apps>, with the bot scopes `app_mentions:read`, `channels:history`, `channels:read`, `chat:write`, `chat:write.public`, `files:write`, `groups:read`, `im:history`, `im:write`, `reactions:write`, `users:read` and `users:read.email`, and the redirect URL `<TASKIEM_PUBLIC_URL>/v1/connections/oauth/callback`.
2. **Event Subscriptions** → Request URL `<TASKIEM_HOOKS_URL>/apps/slack@1/events`; subscribe the bot to the events workflows use, and to `app_uninstalled` and `tokens_revoked`.
3. **Interactivity** → Request URL `<TASKIEM_PUBLIC_URL>/channels/slack/interactions`: approval buttons are decided there, and every other click goes on to `/hooks/apps/slack@1/interactions` (see [approvals in Slack](#approvals-in-slack)).
4. **Manage Distribution** → turn on public distribution, so workspaces other than Taskiem's can install it.
5. Put the client id, client secret and signing secret in the environment.

## Approvals in Slack

Approval requests can come to a member in Slack, as they do on WhatsApp and Telegram, through the business's own workspace (decision 0051; the same notices, decision tokens and deciding as [governance](../governance.md) describes for chat).

- **Linking.** On their profile, a member links Slack in the business they are in (`POST /v1/me/slack`, with the same proof as adding a passkey). Taskiem finds them in the business's connected workspace by their email (`users.lookupByEmail`) and sends them, from the bot, a **Confirm** button; pressing it links that Slack user to them in this business (`slack_bindings`). Someone not in the workspace, or with another email there, is told so. A link is one business's, unlike a WhatsApp number or Telegram account. Unlink with `DELETE /v1/me/slack`.
- **Requests.** An approval step waiting for a role reaches each linked approver who holds it as a direct message from the bot: the request in plain text (personal data masked, as on every chat channel) with **Approve** and **Reject** buttons carrying the signed decision tokens. School approvals come the same way to holders of `school.approve`.
- **Deciding.** A button decides only for the member it was sent to, linked in that business; another person's click, an unlinked one, or the second button of a pair decides nothing. The answer comes as a reply, and the clicked message loses its buttons. An approval that asks for a passkey or code is handed to the web app, as on WhatsApp.
- **Where clicks go.** Taskiem's Slack app has one interactivity URL, `/channels/slack/interactions`, verified with the app's signing secret: Taskiem's own buttons (approvals, Confirm) are handled there, and every other click goes on, unchanged, to `/hooks/apps/slack@1/interactions`, the `interactions` trigger.

Not yet: the "First payment to this account" warning in chat requests (WhatsApp's and Telegram's do not show it either), and updating a request in Slack when it is decided somewhere else (its buttons then answer that it was already decided).

## Your own Slack app

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
| `signing_secret` | Verifies events and interactions (`X-Slack-Signature`, scheme `slack_v0`); for your own app only |
| `team_id`, `team_name` | The workspace; filled when Taskiem's app is added |

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
| `list_users` | `users.list` | read | A page of members (bots, Slackbot and deactivated people left out unless `include_bots`), with emails where `users:read.email` allows; `cursor` / `next_cursor` |
| `open_conversation` | `conversations.open` | read | The direct message with one person (or a group DM): the same channel every time, so safe to repeat |
| `schedule_message` | `chat.scheduleMessage` | unsafe write | `post_at` as RFC 3339 or Unix seconds; no idempotency key, so an unknown outcome is parked |

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

6. That `oauth.v2.access` ignores the PKCE `code_verifier` Taskiem always sends (Slack's OAuth page does not mention PKCE for distributed apps), and `/oauth/v2/authorize` the `code_challenge`.

1. That read methods honour form-encoded arguments exactly as JSON ones (the connector form-encodes reads; the reference lists both content types).
2. Whether `ratelimited` returned with HTTP 200 (as opposed to 429) also carries `Retry-After` (the error table says "refer to the Retry-After header").
3. Whether an undocumented `client_msg_id` argument gives `chat.postMessage` duplicate protection (the error list mentions `duplicate_message_not_found`/"associated with client_msg_id", but the argument is not documented, so the connector does not use it).
4. Whether every interactivity payload is signed with `slack_v0` (the verification page names Events API, shortcuts and slash commands).
5. Whether the `payload` JSON is ever sent with escaped forward slashes or other escaping that could split a `taskiem-corr:` key (keys of `[A-Za-z0-9_.-]` are unaffected).
