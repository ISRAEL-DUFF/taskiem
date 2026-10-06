# Gmail integration

The connector is `connectors/gmail` (`gmail@1`), on the Gmail API v1. Built from Google's public documentation (developers.google.com/gmail/api and the OAuth 2.0 service-account and web-server guides), read 6 October 2026. Token minting and caching are shared with Google Sheets in `connectors/internal/google`.

## Connections

A connection is **one** of:

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

**Never twice.** Gmail has no idempotency key or client-side dedup for messages or drafts. A send whose outcome is unknown (connection dropped after sending, 500, 502, 504) parks for a person. Quota refusals (429, and 403 with `rateLimitExceeded`/`userRateLimitExceeded`/`dailyLimitExceeded`) mean Gmail refused before acting, so they are retried; Google notes the sending limit can last hours. `modify_labels` is a set operation, so it is declared `idempotent_write`; its `request_key` satisfies the contract and is not sent.

**Building the message safely.** Every header value is checked: CR, LF and other control characters are refused, so no input can add a header (for example a `Bcc`) or end the header block. Addresses are parsed with `net/mail` and re-written (display names quoted or RFC 2047-encoded); `from` must be one address; `in_reply_to`/`references` must be `<message-id>`s. Non-ASCII subjects are RFC 2047-encoded. Bodies are UTF-8 and base64, so their content cannot be read as headers or MIME boundaries. Text plus HTML becomes `multipart/alternative`; attachments (`content_base64` or text `content`, with a `content_type`) make it `multipart/mixed`, with RFC 2231 filenames. The message is sent base64url in `raw`, up to 25 MB.

Attachments **by URL** are not supported: fetching an arbitrary URL would need the egress policy to allow that host. Fetch the file in an `http` step and pass its base64.

**Personal data.** Recipients are declared `email`; subject, bodies and attachments `other`; the search query `other`. Message contents returned by `get_message` are detected by the engine (emails) but not declared.

**Errors.** 400 and 403 (other than quota) and 404 are fatal; 401 after one fresh token is fatal; 503 retryable; 500/502/504 unknown outcome. Google's `error.status` and `errors[0].reason` are in the message.

## Needs a person

- A Google Cloud project with the Gmail API enabled, and either a Workspace super administrator to grant domain-wide delegation to a service account, or a Gmail user to consent for a refresh token, for a live check.

## To confirm with Google

1. The size limit for `raw` sent to the metadata (JSON) URI; the connector stops at 25 MB, and the media upload URI (35 MB) is not used.
2. Whether `MessagePartBody.data` is returned in the part's declared charset or converted to UTF-8 (the connector returns the bytes as text, assuming UTF-8).
3. Refresh tokens of OAuth clients whose consent screen is in *Testing* expire after a short period; publish the app (or use a service account) for production.
4. Whether `expires_in` is always present in token responses (the connector assumes five minutes without it).
