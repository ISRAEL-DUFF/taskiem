# Gmail integration

The connector is `connectors/gmail` (`gmail@1`), on the Gmail API v1. Built from Google's public documentation (developers.google.com/gmail/api and the OAuth 2.0 service-account and web-server guides), read 6 October 2026. Token minting and caching are shared with Google Sheets in `connectors/internal/google`.

## Connections

A connection is **one** of:

### One click (Taskiem's Google app)

With `TASKIEM_OAUTH_GMAIL_CLIENT_ID` and `_CLIENT_SECRET` set (registered as for [Google Sheets](googlesheets.md#one-click-taskiems-google-app), with the Gmail API enabled), an owner connects with **Connect Gmail** and nothing is pasted. It asks for `gmail.modify`, a **restricted** scope: until Google verifies the app, which for restricted scopes includes a yearly third-party security assessment (decision N0-2), only the consent screen's test users can connect. Businesses that only need to send email to customers can use the platform's own mail instead, as storefronts do.

### A service account with domain-wide delegation (Google Workspace)

Gmail has no mailbox of its own for a service account, so it must act as a Workspace user.

1. In the Google Cloud console, create (or pick) a project and **enable the Gmail API**.
2. **IAM & Admin → Service accounts → Create service account**. Open it, **Keys → Add key → JSON**, and keep the file.
3. Note the service account's numeric **Client ID** (Details tab).
4. A Workspace super administrator, in the Admin console: **Security → Access and data control → API controls → Manage Domain Wide Delegation → Add new**, with that client ID and the scope `https://www.googleapis.com/auth/gmail.modify` (or the narrower scopes the connection lists). Google says this can take up to 24 hours to apply.
5. Connection fields:

| Field | |
| --- | --- |
| `service_account_json` | The whole JSON key file |
| `subject` | The mailbox to act as, e.g. `ops@yourcompany.com` (required for Gmail) |
| `scopes` | Optional, space- or comma-separated; default `https://www.googleapis.com/auth/gmail.modify` |
| `user_id` | Optional; default `me`, which is the subject |

The connector signs a JWT (RS256; `iss` the service account, `sub` the subject, `scope`, `aud` `https://oauth2.googleapis.com/token`, one hour) with the key and exchanges it with the `urn:ietf:params:oauth:grant-type:jwt-bearer` grant. The key file's `token_uri` is ignored; tokens always come from Google's token endpoint.

### An OAuth client and refresh token (Gmail or Workspace)

1. Enable the Gmail API; configure the **OAuth consent screen**; create an **OAuth client ID** (type *Web application*).
2. Obtain a refresh token once, with the user's consent, for the scope `https://www.googleapis.com/auth/gmail.modify`, requesting offline access (`access_type=offline`; `prompt=consent` to be given a new one).
3. Connection fields: `client_id`, `client_secret`, `refresh_token` (and optionally `user_id`).

The refresh token is exchanged with `grant_type=refresh_token`; its scopes are those the user granted (`scopes` is ignored).

### Token caching

Access tokens are cached in memory per credential set (key, subject and scopes; or client and refresh token) until one minute before `expires_in`. A 401 from Gmail drops the cached token and the call is repeated once with a new one (a 401 means Gmail did nothing). Token failures never reach Gmail, so they are marked *not sent*: `invalid_grant`, `invalid_client`, `unauthorized_client` are fatal (fix the connection); a 429 or 5xx from the token endpoint is retried even for a send.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_profile` | `GET users/{user}/profile` | read | Connection test |
| `send_message` | `POST users/{user}/messages/send` | unsafe write | Builds RFC 5322 (below); `thread_id` to reply in a thread |
| `create_draft` | `POST users/{user}/drafts` | unsafe write | Same inputs as `send_message` |
| `list_messages` | `GET users/{user}/messages` | read | `q` (Gmail search), `label_ids`, `max_results`, `page_token` |
| `get_message` | `GET users/{user}/messages/{id}` | read | `full` (default) decodes the first text/plain and text/html bodies and lists attachments; `metadata`, `minimal` |
| `modify_labels` | `POST users/{user}/messages/{id}/modify` | idempotent write | Mark read: remove `UNREAD`; archive: remove `INBOX` |
| `list_labels` | `GET users/{user}/labels` | read | Ids for user labels (`Label_12`) |
| `new_messages` | `GET users/{user}/profile`, `messages?q=after:…`, then `messages/{id}` | read | Mail since a time, oldest first, at most 200: what the [`new_mail` trigger](#new_mail-polled) polls |

**Never twice.** Gmail has no idempotency key or client-side dedup for messages or drafts. A send whose outcome is unknown (connection dropped after sending, 500, 502, 504) parks for a person. Quota refusals (429, and 403 with `rateLimitExceeded`/`userRateLimitExceeded`/`dailyLimitExceeded`) mean Gmail refused before acting, so they are retried; Google notes the sending limit can last hours. `modify_labels` is a set operation, so it is declared `idempotent_write`; its `request_key` satisfies the contract and is not sent.

**Building the message safely.** Every header value is checked: CR, LF and other control characters are refused, so no input can add a header (for example a `Bcc`) or end the header block. Addresses are parsed with `net/mail` and re-written (display names quoted or RFC 2047-encoded); `from` must be one address; `in_reply_to`/`references` must be `<message-id>`s. Non-ASCII subjects are RFC 2047-encoded. Bodies are UTF-8 and base64, so their content cannot be read as headers or MIME boundaries. Text plus HTML becomes `multipart/alternative`; attachments (`content_base64` or text `content`, with a `content_type`) make it `multipart/mixed`, with RFC 2231 filenames. The message is sent base64url in `raw`, up to 25 MB.

Attachments **by URL** are not supported: fetching an arbitrary URL would need the egress policy to allow that host. Fetch the file in an `http` step and pass its base64.

**Personal data.** Recipients are declared `email`; subject, bodies and attachments `other`; the search query `other`. Message contents returned by `get_message` are declared too (addresses `email`, the rest `other`), and so are `new_messages`' and the `new_mail` trigger's (below).

**Errors.** 400 and 403 (other than quota) and 404 are fatal; 401 after one fresh token is fatal; 503 retryable; 500/502/504 unknown outcome. Google's `error.status` and `errors[0].reason` are in the message.

## Triggers

### `new_mail` (polled)

Gmail's push notifications need a Cloud Pub/Sub topic in the business's own Google project, so Taskiem asks the mailbox itself every 5 minutes while a workflow listens, as it does [Zoho Mail](zoho-mail.md#new_mail-polled) (`new_messages`). Polled deliveries are signed with the connection's refresh token, so the trigger's address takes nothing anyone else sends.

- **Connections it works with:** one-click and refresh-token connections. A **service-account** connection has no refresh token to sign its deliveries with, so the poll fails (its error is kept on the poll) and nothing is delivered; connect with **Connect Gmail**, or with a refresh token, for this trigger.
- **Scopes:** reading mail. `gmail.modify`, which one-click connections ask for, covers it; a refresh token granted only `gmail.send`, or a service account whose `scopes` list no reading scope (`gmail.readonly` or `gmail.modify`), is refused by Gmail with 403 and the poll fails.
- **What it reads:** `after:<since> -in:sent`, then each listed message in full. The first poll starts when the workflow is deployed (nothing earlier is replayed); each later one looks back ten minutes before where the last one got to. At most 200 messages a poll.
- **Never the mailbox's own mail:** a message labelled `SENT` (a message to oneself too) or `DRAFT`, or whose From is the mailbox's own address, is not delivered, so a workflow that replies cannot wake itself; nor are Chat, spam or trash. Mail the business sends from a send-as alias outside Gmail is not recognised as its own.
- **What a run gets:** `body.data`: `id` (Gmail's, for `get_message` and `modify_labels`), `thread_id` (for `send_message`'s `thread_id`), `message_id` (the RFC 5322 Message-ID, for `in_reply_to` and `references`), `from`, `from_address` (the address alone, lower case), `to`, `cc`, `subject`, `date` (the header), `received_at`, `snippet`, `latest` (the text without the earlier messages it quotes, as the [email trigger](../email.md) reads it; from the HTML when there is no text part; at most 32 KB, with `truncated`), `in_reply_to`, `references`, `label_ids`, `unread` and `has_attachments`. Read attachments and the whole body with `get_message`.
- **Once each:** by Gmail's message id, however many polls list it.
- **Correlation:** the thread. A run waiting on a `signal` step with event `gmail@1:new_mail` and the ticket's `thread_id` as its correlation wakes on the customer's reply. To keep the reply in the thread, send it with `send_message`'s `thread_id`, `in_reply_to` (the message's `message_id`), `references` (its `references` followed by its `message_id`) and the same subject.
- **Options** (`config.options` of the workflow's trigger), applied to each workflow on its own: `inbox_only` (default `true`: not mail a Gmail filter archived), `label_ids` (every one of these labels: ids as `list_labels` lists them, such as `Label_12`, `IMPORTANT` or `CATEGORY_PERSONAL`) and `senders` (addresses, `@domain` or `domain`; a subdomain is another domain). Options choose which mail starts the workflow; a run waiting on a thread hears every reply in it, whatever the options.
- **Personal data:** the addresses, subject, snippet and text are sealed in run history (rule 21's `pii`).

## Templates

- **A helpdesk on your Gmail mailbox** (`helpdesk-gmail`) and **Send ticket answers from your Gmail mailbox** (`helpdesk-gmail-send`): each new conversation in the inbox becomes a numbered ticket, replies join it (found by `thread_id`), and the business's answers go back with `send_message` in the customer's thread (`thread_id`, `in_reply_to`, `references`) ([customer tickets](../guide/tickets.md), [the kit](../spec/tickets.md#7-phase-2-the-ticket-kit)).

## Needs a person

- A Google Cloud project with the Gmail API enabled, and either a Workspace super administrator to grant domain-wide delegation to a service account, or a Gmail user to consent for a refresh token, for a live check.
- A live check of `new_mail` on a real mailbox: a customer's reply waking a waiting run, and the business's own reply not.

## To confirm with Google

1. The size limit for `raw` sent to the metadata (JSON) URI; the connector stops at 25 MB, and the media upload URI (35 MB) is not used.
2. Whether `MessagePartBody.data` is returned in the part's declared charset or converted to UTF-8 (the connector returns the bytes as text, assuming UTF-8).
3. Refresh tokens of OAuth clients whose consent screen is in *Testing* expire after a short period; publish the app (or use a service account) for production.
4. Whether `expires_in` is always present in token responses (the connector assumes five minutes without it).
5. That `after:` takes seconds since the epoch in `users.messages.list`'s `q` (the connector also checks each message's `internalDate`), and that `-in:sent` leaves out what the mailbox sent (the connector also skips `SENT`).
6. That header values in `payload.headers` come decoded (RFC 2047) as the web app shows them.
