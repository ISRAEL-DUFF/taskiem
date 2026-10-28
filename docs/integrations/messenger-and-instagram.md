# Messenger and Instagram integration

Two connectors in `connectors/meta`:

- `messenger@1`: a Facebook Page's messages.
- `instagram@1`: an Instagram professional account's direct messages, through the Facebook Page the account is linked to.

Both run on Taskiem's Meta app and the Graph API v26.0. They answer and follow up the people who write to a business, and start workflows when someone does. Built from Meta's public developer documentation, read 26 October 2026; each manifest's header lists the pages. They are K5 of [business connectors](../spec/business-connectors.md#6-instagram-and-facebook-messages), on [decision 0051](../decisions/0051-platform-apps.md).

## Connecting (one click)

A business connects with **Connect Facebook Messenger** or **Connect Instagram**. Meta asks them to log in with Facebook and choose the Page (and, for Instagram, its linked Instagram account) that Taskiem may use. Taskiem then makes the grant into the connection before saving anything (connector/v1 rule 21):

1. The code is exchanged for a short-lived user token. Graph takes the exchange as a GET.
2. The short-lived token is traded for a long-lived one.
3. From the long-lived token, Taskiem takes the Page's own token, which does not expire.
4. Taskiem subscribes the Page to its app (`POST /{page-id}/subscribed_apps`) for the message fields.

The user token is not kept. A connection is one Page, or one Instagram account. A login that grants several Pages is refused and the person is asked to connect again and choose one. A Page with no Instagram account is refused for Instagram.

- **One Meta app** serves both connectors (`client: meta`): `TASKIEM_OAUTH_META_CLIENT_ID` and `_CLIENT_SECRET` (the app id and secret), `_WEBHOOK_SECRET` (the app secret again: Meta signs webhooks with it) and `_VERIFY_TOKEN` ([operations](../operations.md#configuration)).
- **Before other businesses can connect:** Meta's App Review for Advanced Access to `pages_messaging`, `pages_manage_metadata`, `instagram_basic` and `instagram_manage_messages`, as a Tech Provider, and Business Verification of Taskiem (K0-1, K0-2). Until then, only people with a role on the app can connect.
- **Instagram accounts with no Page** need Instagram Login, which is a second app. It will be decided with the first businesses.
- **Disconnecting.** Deleting the connection forgets the Page token. To take the app away, remove it in the Page's Business Integrations.

Taskiem's Meta app (once, by Taskiem's operators): a Business app with Facebook Login for Business and the Messenger and Instagram products. Its webhooks go to:

- the Page object: `<TASKIEM_HOOKS_URL>/apps/messenger@1/messages`;
- the Instagram object: `<TASKIEM_HOOKS_URL>/apps/instagram@1/messages`.

Both use the verify token, and subscribe to `messages`, `messaging_postbacks`, `messaging_referrals`, `message_reads`/`messaging_seen`, `message_deliveries`, `message_echoes` and `message_reactions`. The redirect URI is `<TASKIEM_PUBLIC_URL>/v1/connections/oauth/callback`.

## Connection

| Field | Filled by |
| --- | --- |
| `page_id`, `page_name`, `page_token` | The link: the one Page granted, and its own token |
| `instagram_id`, `instagram_username` | The link: the Page's Instagram account (Instagram's account key) |

The account key is the Page id for Messenger and the Instagram account id for Instagram. Each routes the app's webhook events (rule 18).

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `get_page` (Messenger), `get_account` (Instagram) | read | The connection's check |
| `send_message` | unsafe write | Text, or an image (video, audio, file) by its link, with up to 13 quick replies, to a person by the id the trigger gives (`sender_id`) |
| `send_typing`, `mark_seen` | idempotent write | Sender actions |
| `get_profile` | read | The person's name (Instagram: username) and picture, sealed |
| `get_conversation` | read | The latest messages (at most 20, Meta's limit), and when the person last wrote |

**The window.** Meta allows a reply within 24 hours of the person's last message. After that, only a person replying by hand may answer, within 7 days, with the `HUMAN_AGENT` tag once Meta grants it.

- `send_message` reads the conversation first (`/{page-id}/conversations`). Outside the window it fails as fatal before anything is sent, saying when the person last wrote and when the window closed.
- It sends nothing to someone who has never written.
- Its output says when the window ends.
- Meta's other tags were retired on 27 April 2026.

**Never twice.** Meta's Send API takes no idempotency key, so `send_message` is an unsafe write: an unknown outcome is parked for a person rather than sent twice.

**Errors.** Graph's `error.code` decides:

| Code | Meaning | What happens |
| --- | --- | --- |
| 190 | The Page token is no longer accepted | The connection is expired |
| 4, 17, 32, 613 | Rate limits | Retried |
| 10, 100, 200 | Refusals | Fatal, with Meta's message |
| 551 | Someone who cannot be reached now | Fatal |
| 5xx | — | Unknown outcome |

## Triggers

### `messages`

Meta sends every connected Page's (or Instagram account's) messaging events to the one webhook of Taskiem's app (`delivery: app`). Deliveries are signed with the app secret (`X-Hub-Signature-256`) and checked with a GET challenge (`hub.verify_token`). Batches are split so that each event is its own, routed by its Page or account (the entry id) to the connections that hold it.

- **Events:** `message`, `echo` (the business's own messages), `postback` (a button), `referral` (an ad or link that opened the chat), `read`, `delivery` (Messenger) and `reaction`.
- **What a run gets:** `trigger.item`:
  - `account`;
  - `sender_id`, the person, the address `send_message` takes;
  - `event`, Meta's messaging event: `sender`, `recipient`, `timestamp`, and `message` (`mid`, `text`, `attachments`, `quick_reply`), `postback` (`title`, `payload`), `referral`, `read` or `reaction`.
- **Personal data:** the sender's and recipient's ids, the text and the attachments are sealed in run history, in `trigger.item` and in the delivery's `trigger.body` (connector/v1 rule 21's `pii`). An `ai` step reading a message gets them as placeholders (decision 0050). A signal waking a run that waits on the person carries them as delivered, as WhatsApp's do today.
- **Once each:** each message by its `mid`; other events by the person and time.
- **Correlation:** the person, so a run waiting on their reply wakes when they write. The business's own echoes correlate on the person they went to.

Meta wants an answer within 5 seconds, and unsubscribes an app whose webhook keeps failing for an hour; connecting again subscribes the Page again.

## Templates

| Template | What it does |
| --- | --- |
| `messenger-away-reply`, `instagram-away-reply` | Outside opening hours (Lagos time), answer once a day for each person with a message the business sets |
| `messenger-new-message-alert`, `instagram-new-message-alert` | Tell the owner, or a role's members, once a day for each person that someone has written, without the message |

## Needs a person

- Meta's App Review and Business Verification (K0-1, K0-2). A live check on Meta's test Page and Instagram test account covers connecting, a message through the app webhook, a reply, and a reply refused after the window.

## Not yet

- A daily check that each Page is still subscribed to the app, with an alert when Meta has dropped it.
- Comments (`comments` trigger, `reply_to_comment`).
- Templates that turn an order in a DM into a payment link: Meta gives no email address, which most rails' payment links need.

## To confirm with Meta

1. That the code exchange ignores the PKCE `code_verifier` Taskiem always sends.
2. That the conversations edge takes the field expansion `messages.limit(20){id,created_time,from,message}` (the page documents reading each message separately).
3. That `POST /{page-id}/subscribed_apps` with the Page token also subscribes the linked Instagram account's messages, or what Instagram needs instead.
4. The exact shape of `referral`, `read`, `delivery` and `reaction` events (the overview shows message and postback only).
5. Whether a click-to-Messenger ad's 7-day window shows in the conversation, so the window check can allow it.
