# Zoho Mail integration

The connector is `connectors/zohomail` (`zoho_mail@1`). It sends, replies to, reads, searches, labels and files email in one Zoho Mail mailbox, and starts workflows when mail arrives, much as `gmail@1` does for Gmail. Built from Zoho's public developer documentation, read 26 October 2026; the manifest header lists the pages. It is K6 of [business connectors](../spec/business-connectors.md#72-zoho_mail1).

## Connecting (one click)

A business connects with **Connect Zoho Mail**, through the same Zoho client as [Zoho Books](zoho-books.md#connecting-one-click) (`client: zoho`). The account's data centre is kept as for Books: its accounts server from the callback, its API domain from the token, both held to Zoho's hosts (connector/v1 rule 19).

Before the connection is saved, the connector finds the mailbox (rule 21, `Link`):

- **The server.** It serves the data centre's Mail server: `mail.zoho.eu` for `accounts.zoho.eu`.
- **The mailbox.** It asks for the user's Mail accounts (`GET /api/accounts`) and keeps the first that is the user's own Zoho mailbox, not an IMAP account it reads. A user with none is told so, and nothing is saved.
- **The first token.** The granted access token is kept for its hour, so the first call does not spend another.

Tokens are refreshed and kept as for Books (`connectors/internal/zoho`, shared by every Zoho connector). The scopes are `ZohoMail.accounts.READ`, `ZohoMail.messages.ALL`, `ZohoMail.folders.READ` and `ZohoMail.tags.READ`. A connection is made only by connecting, never by pasting.

## Connection

| Field | Filled by |
| --- | --- |
| `accounts_server`, `location` | The callback |
| `refresh_token`, `api_domain` | The token answer |
| `client_id`, `client_secret` | Taskiem's Zoho client |
| `account_id`, `email_address`, `mail_server` | The link: the mailbox and its data centre's Mail server |
| `access_token`, `access_expires_at` | The link, then each refresh |

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `get_account` | read | The mailbox; the connection's check |
| `send_message` | unsafe write | To, cc, bcc, subject, text or HTML, from the mailbox or an alias |
| `reply` | unsafe write | A reply, or a reply to all, in the email's thread |
| `list_messages` | read | A folder's emails (the inbox by default), by status or label, newest first |
| `search_messages` | read | Zoho Mail's search (`subject:invoice`, `sender:…`, `has:attachment`) |
| `get_message` | read | An email's headers and content, as HTML and as text |
| `update_message` | idempotent write | Mark read or unread, move to a folder, add or remove a label |
| `list_folders`, `list_labels` | read | Pickers |
| `new_messages` | read | The inbox's emails since a time: what the trigger polls |

**Never twice.** Zoho Mail has no idempotency key, so sending and replying are unsafe writes: an unknown outcome is parked for a person rather than sent twice. Marking, moving and labelling leave an email the same when done twice.

**Errors.** Zoho Mail answers `{"status": {"code", "description"}, "data": {"errorCode", "moreInfo"}}`:

| Answer | What happens |
| --- | --- |
| 401 | The token is refreshed once, then the connection is expired |
| 429 | Retried |
| 404 | `not_found` |
| Other refusals | Fatal, with Zoho's words |
| 5xx | Unknown outcome |

## Triggers

### `new_mail` (polled)

Zoho Mail documents no webhook for new mail, so Taskiem asks the mailbox itself every 5 minutes while a workflow listens (`new_messages`). Polled deliveries are signed with the connection's refresh token, so the trigger's address takes nothing anyone else sends.

- **What a run gets:** `body.data`, the email as `list_messages` lists it: `message_id`, `folder_id`, `thread_id`, `subject`, `from`, `to`, `summary`, `received_at`, `unread`, `has_attachment`. Read the whole email with `get_message`.
- **Personal data:** the subject, addresses and summary are sealed in run history (rule 21's `pii`).
- **Once each:** by the email's id.
- **Correlation:** the thread, so a run waiting on a reply wakes when one comes.

## Templates

- **Email a receipt from your Zoho mailbox** (`zoho-mail-paid-receipt`): when an invoice is paid in full, the customer is emailed a short receipt from the business's own address.

## Needs a person

- A Zoho Mail mailbox in two data centres for a live check: connect, send, reply, read, search, move and label, and new mail through the poll (K0-5).

## To confirm with Zoho

1. That the first `ZOHO_ACCOUNT` in `GET /api/accounts` is the user's primary mailbox. The answer has no default flag.
2. The `updatemessage` modes other than `markAsRead` (`markAsUnread`, `moveMessage` with `destfolderId`, `applyLabel` and `removeLabel` with `labelId`): only `markAsRead` is shown on its page.
3. That a listed email's `status` is `0` for unread and `hasAttachment` is `0` or `1`.
4. The reply body's `action` (`reply`, `replyall`) and whether a reply's `subject` may be left out.
5. That `/api/accounts/{id}/messages/search?searchKey=` takes the web app's search syntax.
