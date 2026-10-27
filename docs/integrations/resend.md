# Resend integration

The connector is `connectors/resend` (`resend@1`). It sends a business's transactional email (receipts, invoices, notices) from its own domain, once however often a step is retried, and hears what happens to each email. Built from Resend's public API reference, read 26 October 2026; the manifest header lists the pages. It is K7 of [business connectors](../spec/business-connectors.md#9-email-resend-brevo-and-mailchimp).

## Connecting

The business pastes a Resend API key with full access (`re_…`, from resend.com → API Keys): Taskiem lists its domains with it and sets up the email events webhook. The connection check lists the domains and says which are verified.

## Actions

| Action | Class | What it does |
| --- | --- | --- |
| `list_domains` | read | The sending domains and whether each is verified; the connection's check |
| `send_email` | idempotent write | From an address on a verified domain: to (up to 50), cc, bcc, reply-to, subject, HTML or text, tags, attachments (base64 or an https link), a time to send |
| `get_email` | read | A sent email and what last happened to it |

**Never twice.** Each send carries the step's idempotency key as Resend's `Idempotency-Key`, so Resend sends it once however often the step is retried within 24 hours. The same key sent with another body is refused (fatal); one still being sent is retried.

**Deliverability is the business's.** A send from an address whose domain is not verified in Resend is refused before anything is sent, saying so.

**Errors.** Resend answers `{"statusCode", "name", "message"}`:

| Answer | What happens |
| --- | --- |
| An invalid or suspended key | The connection is expired |
| `rate_limit_exceeded` (429) | Retried |
| `daily_quota_exceeded`, `monthly_quota_exceeded` | Fatal: nothing sends until the quota resets |
| Other refusals | Fatal, with Resend's words |
| 5xx | Unknown outcome, retried under the same key |

## Triggers

### `email_events`

When a workflow on `email_events` is published, Taskiem creates the webhook in Resend itself (registration remote: decision 0021). It keeps the signing secret Resend returns, and Resend's deliveries are verified by Svix (`svix`, connector/v1 rule 7). Undeploying deletes the webhook. A webhook made just before a crash, whose secret Taskiem never kept, is found by its URL, deleted and made again. A webhook turned off in Resend shows as paused, and redeploying turns it back on.

- **Events:** `email.sent`, `email.delivered`, `email.delivery_delayed`, `email.bounced`, `email.complained`, `email.opened`, `email.clicked`, `email.failed`, `email.suppressed`.
- **What a run gets:** `trigger.body` is Resend's event: `type`, `created_at` and `data` (`email_id`, `from`, `to`, `subject`, and `bounce` or `click` where they apply). The addresses and subject are sealed in run history.
- **Once each:** by the delivery's `svix-id`.
- **Correlation:** the email (`send_email`'s `email_id`), so a run waiting on its email wakes when it is delivered or bounces.

## Templates

| Template | What it does |
| --- | --- |
| `resend-paid-receipt` | When an invoice is paid in full, email the customer a receipt from the business's domain, once |
| `resend-bounces-to-customers` | When an email bounces, set the customer's `email_status` in the customers [data table](../data-tables.md); a complaint also turns `marketing_opt_in` off |

## Needs a person

- A Resend account with a verified domain for a live check: a send retried with the same key, a bounce through the webhook, and a webhook made, updated and deleted (K0-6).

## To confirm with Resend

1. That `GET /webhooks/{id}`, `PATCH /webhooks/{id}` (endpoint, events, status) and `DELETE /webhooks/{id}` work as the list page implies.
2. The bounce event's `data.bounce` fields.
3. The error body's field names (`statusCode`, `name`, `message`).
