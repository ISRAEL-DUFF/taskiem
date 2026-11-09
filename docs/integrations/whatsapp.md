# WhatsApp integration

The connector is `connectors/whatsapp` (`whatsapp@1`), used by tenants inside workflows with their own credentials. Taskiem's own number, through which people approve and run workflows by chat, is separate: see [WhatsApp](../whatsapp.md). It uses the WhatsApp Cloud API on Meta's Graph API v25.0, built only from Meta's public documentation (developers.facebook.com/docs/whatsapp/cloud-api, now served from developers.facebook.com/documentation/business-messaging/whatsapp), read 6 October 2026.

## Set up the app

1. In the Meta App Dashboard, create a Business app with the **WhatsApp** product (or the "Connect with customers through WhatsApp" use case), linked to your WhatsApp Business Account.
2. Add and register the business phone number. Note its **Phone number ID**: a long number, not the phone number itself.
3. In Business Settings, create a **system user** with access to the app and the WhatsApp Business Account. Generate a token for it with `whatsapp_business_messaging` (and `whatsapp_business_management`). Temporary test tokens expire after a day, so use a system user token.
4. Copy the **App secret** from App settings > Basic.

## Connection

| Field | |
| --- | --- |
| `access_token` | System user access token. The connection test calls `get_phone_number` |
| `phone_number_id` | The business phone number's ID |
| `app_secret` | Proves deliveries really come from Meta (signature check). Required to receive messages: without it the connection check and readiness warn that messages will be refused |
| `verify_token` | Any string you choose. Enter the same value as the **Verify token** in the dashboard |
| `business_account_id` | Optional: the WhatsApp Business Account ID, so templates can be chosen by what they say (`list_templates`) |

## Actions

A message's `to` is taken as people write it: spaces, dashes and brackets are dropped, `00` becomes `+`, and a Nigerian number starting `0` (0803 123 4567) gets `+234`.

| Action | Request | Class | Notes |
| --- | --- | --- | --- |
| `get_phone_number` | `GET /{phone-number-id}` | read | Display number, verified name, quality rating |
| `list_templates` | `GET /{waba-id}/message_templates?status=APPROVED` | read | The approved templates' `name`, `language`, `category`, body words and how many values the body takes. Needs `business_account_id`. The web app's template picker (`GET /v1/whatsapp/templates`) uses it |
| `send_text` | `POST /{id}/messages`, `type: text` | unsafe write | Only within the 24-hour customer service window. `preview_url`, `reply_to` (a wamid to quote) |
| `send_template` | `type: template` | unsafe write | `name`, `language` (`en_US`), `components` as Meta documents them, for example `[{type: body, parameters: [{type: text, parameter_name: first_name, text: Ada}]}]`. The only message allowed outside the window |
| `send_media` | `type: image/document/audio/video/sticker` | unsafe write | `link` (Meta fetches it and caches it for 10 minutes), `media_id`, or `file`, a run file uploaded first ([Files in runs](../files.md)). `voice: true` sends audio as a voice note (Ogg/Opus; plays inline up to 512 KB). `caption` for image, document and video. `filename` for documents |
| `send_interactive` | `type: interactive` | unsafe write | Meta's `interactive` object, passed through: up to three reply `button`s, or a `list` (up to 10 sections and 10 rows) |
| `mark_as_read` | `status: read` | idempotent write | A wamid from an inbound message (within 30 days). Also marks earlier messages read |
| `get_media` | `GET /{media-id}?phone_number_id={id}`, then its URL | read | The media of an inbound message, looked up on the connection's own number (Meta refuses media sent to another), fetched only from Meta's media hosts (`lookaside.fbsbx.com`, and redirects only to `*.fbcdn.net` or `*.whatsapp.net`, all in the manifest's egress list), checked against its sha256, and kept as a person's run file (scanned, personal, under a neutral name). At most 25 MB, and 16 MB for audio, checked before the download is read. `sender` (the message's `from`) ties the file to that person, so erasing their number deletes it. An expired URL is retried with a fresh lookup |
| `upload_media` | `POST /{phone-number-id}/media` | idempotent write | A run file to a media id; `voice: true` checks the Ogg really holds Opus (Vorbis is refused) and uploads it as `audio/ogg`, the type Meta lists. A repeat only makes a second id |
| `send_typing` | `status: read` with `typing_indicator` | idempotent write | Marks the message read and shows "typing…" until the reply is sent, at most 25 seconds. Where Meta refuses the typing field (an older API version or account), it marks the message read only; the output's `typing` says which |

Every send can carry `biz_opaque_callback_data`, which Meta returns in that message's status events. A send's output has `message_id` (the wamid), `wa_id` (the user's WhatsApp ID), `input` and, for paced templates, `message_status`.

`send_text`, `send_media` and `send_interactive` are marked `resend_if_lost` ([action classes](../contracts/action-classes.md)): a workflow step may ask that a send whose answer was lost be sent once more instead of parked (decision 0062 D11), accepting a possible duplicate message.

A 200 only means Meta **accepted** the message. Delivery comes as status events.

**Never twice.** The Cloud API has no idempotency key for sends, so they are unsafe writes. A lost response, a 5xx, or Meta's `1` / `131000` ("something went wrong") parks the step for a person. Meta asks clients to act on `error.code`, and the connector does:

| Codes | Treated as |
| --- | --- |
| `4`, `80007`, `130429`, `131056` (rate, throughput and pair limits); `2`, `131016`, `133004` (temporarily unavailable); `131057`, `2494100` (maintenance) | Refused, nothing sent: retryable, including for sends |
| `1`, `131000`, and any undocumented code with a 5xx | Unknown outcome: sends park |
| Everything else Meta explains (`0`, `3`, `10`, `190`, `200`–`299` permissions and tokens, `100` / `131008` / `131009` parameters, `131047` window closed, `132xxx` templates, `131026` undeliverable, `368` / `131031` policy, ...) | Fatal, with Meta's message, details and `fbtrace_id` |

## Receiving events

The trigger is `messages`. In the App Dashboard > WhatsApp > Configuration, set:

- **Callback URL**: `<public URL>/hooks/<tenant>/connectors/whatsapp@1/messages?env=prod`. Add `&connection=<name>` if the tenant has more than one WhatsApp connection in the environment.
- **Verify token**: the connection's `verify_token`.

Meta first sends `GET ...?hub.mode=subscribe&hub.verify_token=…&hub.challenge=…`. The trigger's handshake (`{method: GET, token_query: hub.verify_token, secret_field: verify_token, respond: '=query["hub.challenge"]'}`) answers with the challenge when the token matches, and with 403 otherwise. Then **subscribe to the `messages` field**.

Every POST is verified with `X-Hub-Signature-256: sha256=<hex>`, an HMAC-SHA256 of the raw body keyed with the app secret.

| Event | When | Dedup | Correlation |
| --- | --- | --- | --- |
| `message` | A user sent a message, tapped a reply button (`interactive.button_reply`) or picked a list row (`list_reply`) | the message's wamid | the sender's WhatsApp ID (`messages[0].from`), equal to a send's `wa_id` output |
| `status.sent`, `status.delivered`, `status.read`, `status.failed`, `status.played` | A message you sent changed status | `wamid:status` | the wamid, equal to a send's `message_id` output |
| `error` | A system, app or account error | none: Meta gives no id | none |

Meta may batch many messages and statuses in one POST. The trigger splits a delivery into one event per message, status or error (`split` in the manifest), and each event has its own type, dedup key and correlation. A run gets that one event as `trigger.item`: `{kind, message | status | error, metadata}`, with `metadata.phone_number_id` and `display_phone_number`; `trigger.body` is still the whole delivery. So a run that asks a question waits on `whatsapp@1:messages` with correlation `steps.ask.output.wa_id` and reads `trigger.item.message`; a workflow that starts on messages reads the same. A run that must know a message arrived waits with correlation `steps.notify.output.message_id`. A `read` can arrive without a `delivered`; Meta notes that read implies delivered.

**What a customer sends** (decision 0062, P1): the sender's number and profile name, and what they wrote (text, captions, locations, contacts, button and list replies), are declared personal data, sealed in run history and readable by the run's steps.

Meta sends every field the app subscribes to (template status, account updates, ...) to the same URL. The trigger acknowledges the other fields and ignores them. Meta retries failed deliveries for up to 7 days and expects quick 200s.

### Answering customers: the guard

A workflow that answers the business's customers puts `"guard": true` (or its settings) in the trigger's config. The trigger reads each event as a customer's message (its `conversation` block), and the guard decides before a run starts: STOP and START, limits per customer, asking for a person, the kinds of message, the owner's pause. Statuses and errors start nothing, and a voice note or audio is fetched when it arrives, while Meta's link still works. The run reads `trigger.guard` (`outcome`, `reply`, `notice`, `sender`, `text`, `channel`, `file`). See [keeping a customer assistant safe](../assistant-guard.md).

## To confirm before go-live

1. **Batching.** Settled: each item of a batched delivery is its own event (see above). What is left to check is how often Meta batches at your volume, since every item of one delivery is handled in the same request.
2. HTTP status codes for `130429` and the other throttles. The connector keys on `error.code`, so the status does not matter, but confirm that Meta attaches no `Retry-After` the engine should honour.
3. Graph API version: the connector pins `v25.0`, the version Meta's examples use. Plan upgrades before Meta retires it.
4. Group messaging (`recipient_type: group`) is not supported. Sends are always to individuals.
5. Whether the business phone number has been moved to higher throughput (80 messages a second by default). The manifest rate limit assumes the default.
