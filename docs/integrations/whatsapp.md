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
| `app_secret` | Verifies webhook signatures. Required to receive events |
| `verify_token` | Any string you choose. Enter the same value as the **Verify token** in the dashboard |

## Actions

| Action | Request | Class | Notes |
| --- | --- | --- | --- |
| `get_phone_number` | `GET /{phone-number-id}` | read | Display number, verified name, quality rating |
| `send_text` | `POST /{id}/messages`, `type: text` | unsafe write | Only within the 24-hour customer service window. `preview_url`, `reply_to` (a wamid to quote) |
| `send_template` | `type: template` | unsafe write | `name`, `language` (`en_US`), `components` as Meta documents them, for example `[{type: body, parameters: [{type: text, parameter_name: first_name, text: Ada}]}]`. The only message allowed outside the window |
| `send_media` | `type: image/document/audio/video/sticker` | unsafe write | `link` (Meta fetches it and caches it for 10 minutes) or `media_id`. `caption` for image, document and video. `filename` for documents |
| `send_interactive` | `type: interactive` | unsafe write | Meta's `interactive` object, passed through: up to three reply `button`s, or a `list` (up to 10 sections and 10 rows) |
| `mark_as_read` | `status: read` | idempotent write | A wamid from an inbound message (within 30 days). Also marks earlier messages read |

Every send can carry `biz_opaque_callback_data`, which Meta returns in that message's status events. A send's output has `message_id` (the wamid), `wa_id` (the user's WhatsApp ID), `input` and, for paced templates, `message_status`.

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

So a run that asks a question waits on `whatsapp@1:messages` with correlation `steps.ask.output.wa_id` and reads `trigger.body.entry[0].changes[0].value.messages[0]`. A run that must know a message arrived waits with correlation `steps.notify.output.message_id`. A `read` can arrive without a `delivered`; Meta notes that read implies delivered.

Meta sends every field the app subscribes to (template status, account updates, ...) to the same URL. The trigger acknowledges the other fields and ignores them. Meta retries failed deliveries for up to 7 days and expects quick 200s.

## To confirm before go-live

1. **Batching.** Meta may put up to 1000 updates in one POST ("batching cannot be guaranteed"). Taskiem evaluates one event per delivery, so the event type, dedup key and correlation describe the **first** message or status only. The others are in `trigger.body` but wake no waiting run. Check how often batching happens at your volume. Fanning out per item needs an engine change.
2. HTTP status codes for `130429` and the other throttles. The connector keys on `error.code`, so the status does not matter, but confirm that Meta attaches no `Retry-After` the engine should honour.
3. Graph API version: the connector pins `v25.0`, the version Meta's examples use. Plan upgrades before Meta retires it.
4. Group messaging (`recipient_type: group`) is not supported. Sends are always to individuals.
5. Whether the business phone number has been moved to higher throughput (80 messages a second by default). The manifest rate limit assumes the default.
