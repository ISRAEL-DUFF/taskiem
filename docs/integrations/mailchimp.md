# Mailchimp integration

The connector is `connectors/mailchimp` (`mailchimp@1`). It keeps a business's Mailchimp audience up to date, with only people who opted in, and starts workflows on the audience's events. It can:

- add and update members;
- tag members;
- create and send campaigns.

Built from Mailchimp's public developer documentation, read 26 October 2026; the manifest header lists the pages. It is K8 of [business connectors](../spec/business-connectors.md#9-email-resend-brevo-and-mailchimp).

## Connecting

**One click.** Where Taskiem's Mailchimp app is registered (`TASKIEM_OAUTH_MAILCHIMP_CLIENT_ID` and `_CLIENT_SECRET`: [operations](../operations.md#configuration)), a business connects with **Connect Mailchimp**:

- Mailchimp asks them to log in and allow Taskiem.
- Mailchimp's tokens do not expire and have no scopes, so no scope is asked for (connector/v1 rule 21 allows none).
- Before the connection is saved, the connector reads the account's server prefix (`dc`, as in `us6`) and name from Mailchimp's metadata (`https://login.mailchimp.com/oauth2/metadata`, rule 21's `Link`).

Taskiem's Mailchimp app is registered once, by Taskiem's operators, at <https://admin.mailchimp.com/account/oauth2/> with the redirect URI `<TASKIEM_PUBLIC_URL>/v1/connections/oauth/callback`.

**Or an API key.** A business may paste an API key instead (Profile → Extras → API keys); its server is the key's ending (`…-us6`).

Every call goes to the account's server, `https://<dc>.api.mailchimp.com/3.0`. Egress allows `*.api.mailchimp.com`, and the prefix is checked to look like one.

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `get_account` | read | The account; the connection's check |
| `list_audiences` | read | The audiences, with their subscribed members (the `audiences` picker) |
| `add_or_update_member` | idempotent write | A member by email: added as subscribed when new, updated when there; only with consent |
| `tag_member` | idempotent write | Adds or removes tags |
| `create_campaign` | idempotent write | A regular campaign to an audience (or a tag), with its subject, sender and HTML; named by its title, so a retry finds the one it made |
| `send_campaign` | idempotent write | Sends a campaign now; one already sent or sending is answered as it is |

**Consent.** A member is added only with `consent: {opted_in: true, source, at}`; the manifest allows only `true`, and the connector refuses without it. The opt-in time goes to Mailchimp as `timestamp_opt`. Taskiem sets only `status_if_new`, so someone who unsubscribed in Mailchimp stays unsubscribed.

**Never twice.** A member is named by the MD5 of their lowercased address, so `PUT` adds or updates the same member however often it is retried.

**Errors.** Mailchimp answers problem details (`title`, `status`, `detail`):

| Answer | What happens |
| --- | --- |
| 401 | The key or token is refused: the connection is expired |
| 429 | Too many requests at once: retried |
| 404 | `not_found` |
| "Forgotten Email Not Subscribed", "Member Exists", "Invalid Resource" and other refusals | Fatal |
| 5xx | Unknown outcome |

## Pickers

A connector may now declare **pickers** (connector/v1 rule 21):

- each is a read action and how its output reads as choices (`items`, `id`, `label`);
- a template parameter names one (`picker: choice`, `choices: mailchimp:audiences`, `from` its connection parameter);
- the Start page offers a list from `GET /v1/connections/choices`.

Mailchimp's `audiences` lists each audience with how many are subscribed, so no business types an audience id.

## Triggers

### `audience_events`

A workflow on `audience_events` names the audience it watches (`options.audience_id`). Taskiem creates a signed webhook on that audience (registration remote: decision 0021) and keeps the signing secret Mailchimp returns once.

**Before it saves the webhook,** Mailchimp checks the address with a plain GET. The trigger answers 200 without reading or starting anything (the handshake's new `open`).

**Deliveries** are form-encoded and signed: `X-Mailchimp-Signature: t=<time>,v1=<hex>`, an HMAC-SHA256 of `<time>.<body>`, within 5 minutes. Taskiem already verifies that scheme (`hmac_sha256_timestamped` with `t_v1`).

**Changing it.** A webhook whose events no longer match, or one found after a crash, is deleted and made again.

- **Events:** `subscribe`, `unsubscribe`, `profile`, `upemail`, `cleaned`, `campaign`.
- **What a run gets:** `trigger.body`, the form's fields as Mailchimp names them: `type`, `fired_at`, and `data[email]`, `data[list_id]` and `data[reason]` (for an unsubscribe or a cleaned address). The addresses and names are sealed.
- **Once each:** by the event, the address and when it fired.
- **Correlation:** the person's email.

## Templates

| Template | What it does |
| --- | --- |
| `mailchimp-opted-in-customers` | When a customer in the customers [data table](../data-tables.md) has `marketing_opt_in` true, add or update them on the audience, chosen by name; nobody else is ever added |
| `mailchimp-unsubscribes-to-customers` | When someone unsubscribes or Mailchimp cleans their address, turn their `marketing_opt_in` off and set their `email_status` |

## Needs a person

- Taskiem's Mailchimp app registered, and a Mailchimp account for a live check: connect, a member added twice, a campaign made and sent, and the webhook (K0-6).

## To confirm with Mailchimp

1. That the token exchange ignores the PKCE `code_verifier` Taskiem sends, and a consent URL with no `scope` is accepted.
2. That a webhook made with `signing_enabled: true` returns `signing_secret`, and how a delivery's `X-Mailchimp-Signature` is formed (the guide's form, verified here).
3. That `GET` and `DELETE /lists/{id}/webhooks/{webhook}` work as the add page implies.
4. That a static segment (a tag) is sent to with `segment_opts.saved_segment_id`.
