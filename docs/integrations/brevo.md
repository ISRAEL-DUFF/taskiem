# Brevo integration

The connector is `connectors/brevo` (`brevo@1`). It does five things for a business:

- sends transactional email and SMS from the business's own sender;
- keeps its contacts, matched by email;
- puts people who opted in on its marketing lists, and nobody else;
- sends its campaigns;
- hears deliveries, bounces and unsubscribes.

Built from Brevo's public API reference, read 26 October 2026; the manifest header lists the pages. It is K7 of [business connectors](../spec/business-connectors.md#9-email-resend-brevo-and-mailchimp).

## Connecting

The business pastes a Brevo API key (`xkeysib-…`, from Brevo → SMTP & API → API keys). The connection check reads the account and its sending domains, saying which are authenticated.

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `get_account` | read | The account and its sending domains; the connection's check |
| `send_email` | unsafe write | From a sender on an authenticated domain: to, cc, bcc, reply-to, subject and HTML or text, or a Brevo template and its params; tags, attachments |
| `send_sms` | unsafe write | A text from the business's sender name; a marketing text needs the person's consent |
| `create_or_update_contact` | idempotent write | A contact by email, updated when it exists (`updateEnabled`); putting them on lists needs their consent |
| `add_to_list` | idempotent write | Existing contacts onto a list; needs their consent; someone already on it is no change |
| `list_lists` | read | The contact lists (a picker) |
| `send_campaign` | idempotent write | Sends a campaign made in Brevo now; one already sent or on its way is answered as it is |

**Consent.** Nobody is put on a marketing list, or sent a marketing text, without saying yes:

- the input must carry `consent: {opted_in: true, source, at}`;
- the manifest only allows `true`;
- the connector refuses before calling when it is missing.

Templates set it only from a row whose `marketing_opt_in` is true. This follows the Nigeria Data Protection Act 2023 and Brevo's own terms (§4.5 of the plan).

**Never twice.** Brevo takes no idempotency key for sends, so email and SMS are unsafe writes: an unknown outcome is parked for a person rather than sent twice. Contacts, list additions and campaigns can be repeated without effect.

**Deliverability is the business's.** An email from a sender whose domain is not authenticated in Brevo is refused before anything is sent.

**Errors.** Brevo answers `{"code", "message"}`:

| Answer | What happens |
| --- | --- |
| 401 (`unauthorized`) | The key is refused: the connection is expired |
| 429 | Retried |
| 404 (`document_not_found`) | `not_found` |
| `not_enough_credits` and other refusals | Fatal |
| 5xx | Unknown outcome |

## Triggers

Taskiem creates each webhook in Brevo itself when a workflow on it is published (registration remote: decision 0021):

- **Verification.** Each webhook carries a bearer token of Taskiem's, which Brevo sends with every delivery (`bearer`).
- **Changes.** Taskiem never reads the token back, so a webhook whose URL or events no longer match is deleted and made again with a new token.
- **After a crash.** One made just before a crash is found by its description (the subscription's name) and made again.

### `transactional_events`

The transactional emails' events, by the webhook's names:

`request`, `delivered`, `hardBounce`, `softBounce`, `blocked`, `spam`, `invalid`, `deferred`, `click`, `opened`, `uniqueOpened`, `unsubscribed`

Brevo's deliveries name them in snake case (`hard_bounce`), and the trigger maps them back. The run gets Brevo's event as `trigger.body`: `event`, `email`, `message-id`, `subject`, `ts_event`, and the reason or link. The address and subject are sealed.

- **Correlation:** the email (`send_email`'s `message_id`).
- **Once each:** by the message, the event and its time.

### `marketing_events`

The campaigns' events:

`delivered`, `opened`, `click`, `hardBounce`, `softBounce`, `spam`, `unsubscribed`, `listAddition`, `contactUpdated`, `contactDeleted`

- **Correlation:** the person's email, so a run waiting on a contact wakes when they unsubscribe or bounce.
- **Once each:** by the email, the event, the campaign and its time.

## Templates

| Template | What it does |
| --- | --- |
| `brevo-opted-in-customers` | When a customer in the customers [data table](../data-tables.md) has `marketing_opt_in` true, add or update them on a Brevo list, with their consent; nobody else is ever added |
| `brevo-unsubscribes-to-customers` | When someone unsubscribes, hard-bounces or marks a campaign as spam, turn their `marketing_opt_in` off and set their `email_status` |

## Needs a person

- A Brevo account with an authenticated domain and an SMS sender for a live check: a send, a contact added twice, a list, a campaign, and both webhooks (K0-6).

## To confirm with Brevo

1. That a webhook made with `auth: {type: "bearer", token}` sends `Authorization: Bearer <token>`.
2. The event names in deliveries: transactional `hard_bounce`, `invalid_email`, `unique_opened`; marketing `unsubscribe` or `unsubscribed`, `list_addition`.
3. `GET /webhooks/{id}` and `DELETE /webhooks/{id}` (the list page names neither).
4. That `POST /contacts` with `updateEnabled` answers 204 when it updated a contact.
5. The message when contacts are already on a list (the connector treats it as no change).
