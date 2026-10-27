# Zoho Books integration

The connector is `connectors/zohobooks` (`zoho_books@1`, `kind: product`, category `accounting`). It keeps a business's books in Zoho Books up to date through Books' API v3, for one organisation per connection, in either edition: the global one, or the Nigeria edition Zoho launched on 17 September 2026 (the same API, with Nigerian VAT among its taxes and e-invoicing through Remita, which Books does itself). Built from Zoho's public developer documentation, read 26 October 2026; the manifest header lists the pages. It is K4 of [business connectors](../spec/business-connectors.md#71-zoho_books1-first), on [decision 0051](../decisions/0051-platform-apps.md).

## Connecting (one click)

A business connects Zoho Books with **Connect Zoho Books**: Zoho asks them to sign in and allow Taskiem, and sends the browser back naming the data centre the account lives in (`location`, `accounts-server`). The code is exchanged at that accounts server, and the token answer names the account's API domain (`api_domain`); the connection keeps both, and every call goes there (connector/v1 rule 19). A callback or token answer naming a server outside Zoho's data centres is refused before the code is exchanged. Nothing is pasted.

- **One Zoho client.** Taskiem registers one server-based client at Zoho's developer console with multi-DC on; every Zoho connector uses it (`client: zoho`): `TASKIEM_OAUTH_ZOHO_CLIENT_ID` and `_CLIENT_SECRET` ([operations](../operations.md#configuration)).
- **Tokens.** Zoho gives a refresh token only with `access_type=offline` (and `prompt=consent` asks again on a reconnect). Access tokens last an hour, and Zoho issues at most ten every ten minutes per refresh token, so the connector keeps the one it got with the connection's credentials and refreshes under the connection's lock (`connector.Request.RotateCredentials`) at the account's accounts server. Zoho's refresh tokens do not rotate, last until revoked, and a user may hold twenty per client (a twenty-first revokes the oldest). A refresh token Zoho refuses (`invalid_code`) marks the connection expired.
- **The organisation.** A connection is one organisation. On the first call the connector asks which the user reaches (`GET /organizations`) and keeps the only one, or else Zoho's default; `list_organizations` (the connection check) answers the one in use as `current`. A user whose organisations include no default is told to make one the default.
- **Disconnecting.** Deleting the connection gives the refresh token back (`/oauth/v2/token/revoke` at the account's accounts server).
- **Pasting.** A Zoho Books connection is made only by connecting: its tokens are refreshed and kept by Taskiem.

Taskiem's Zoho client (once, by Taskiem's operators): at <https://api-console.zoho.com>, a **Server-based Application** with the redirect URI `<TASKIEM_PUBLIC_URL>/v1/connections/oauth/callback`, and **Multi-DC** turned on under Settings, for every data centre Taskiem serves. Put its client id and secret in the environment.

## Connection

| Field | Filled by |
| --- | --- |
| `accounts_server`, `location` | The callback |
| `refresh_token`, `api_domain` | The token answer |
| `client_id`, `client_secret` | Taskiem's Zoho client (`platform_client_fields`) |
| `organization_id`, `organization_name` | The connector, on the first call |
| `access_token`, `access_expires_at` | The connector, on each refresh |

## Actions

Amounts are in kobo in and out; Books keeps decimal amounts in the organisation's currency (NGN for the Nigeria edition), and the connector converts both ways.

| Action | Class | What it does |
| --- | --- | --- |
| `list_organizations` | read | The organisations the user reaches, and the one in use |
| `find_contact` | read | A customer or supplier by email, then phone, then display name (the same name only) |
| `create_contact` | idempotent write | Finds by email, phone, then name before adding |
| `create_invoice` | idempotent write | `reference_number` is Taskiem's invoice number, found by it first; Zoho numbers it unless `invoice_number` is given |
| `get_invoice`, `find_invoices` | read | One invoice; or by reference, number, customer, status, changed since |
| `record_payment` | idempotent write | A payment received, applied to an invoice (its customer then the invoice's), with the money log's reference, found by it among the customer's payments first |
| `create_sales_receipt` | idempotent write | A sale paid at once, numbered by the day and source, found by its number first; the walk-in customer's when none is given |
| `create_expense` | idempotent write | An expense paid at once, found by its reference first; `vat_kobo` recorded as its own line |
| `create_bill` | idempotent write | A supplier's bill, found by its number for that supplier first |
| `list_items`, `list_accounts`, `list_taxes` | read | Pickers |
| `changes_since` | read | Invoices changed and payments recorded since a time, each as the change it was |

**Never twice.** Zoho Books has no idempotency key. Each write looks first for what it would make by the business's own number or reference, so a retry after an unknown outcome finds the first attempt's record; the step's key (`request_key`) is not sent to Zoho, as for Slack's `update_message`.

**Errors.** Books answers `{"code": n, "message": …}`, `0` for success. 401 is a refused access token: refreshed once and the call made again, then the connection is expired; 429 (codes 44, 45, 1070: a minute's, a day's or concurrent calls) is retried; 404 or code 1002 is a record that is not there (`not_found`); other refusals are fatal with Zoho's message; 5xx is an unknown outcome. Books allows 100 requests a minute per organisation and 1,000 to 10,000 a day by plan.

## Books capabilities

Zoho Books declares the nine books capabilities, so the books templates written for QuickBooks run on it unchanged:

| Capability | Action | Notes |
| --- | --- | --- |
| `books_customer` | `create_contact` | As a customer |
| `books_invoice` | `create_invoice` | Taskiem's number as `reference_number`; the organisation's base currency |
| `books_payment` | `record_payment` | `method` as Zoho's payment mode (transfer: bank transfer, card and POS: credit card) |
| `books_sale` | `create_sales_receipt` | Zoho takes no deposit account for a sales receipt through the API: the takings' `account_id` is not used |
| `books_expense` | `create_expense` | Needs `paid_from_id`; the supplier and description go in the description |
| `books_bill` | `create_bill` | |
| `books_accounts`, `books_items`, `books_taxes` | `list_accounts`, `list_items`, `list_taxes` | |

**Events.** `books.invoice.created`, `books.invoice.paid` and `books.payment.recorded`, all three, from the `changes` trigger.

## Triggers

### `changes` (polled)

Zoho Books calls webhooks only from workflow rules a person sets up in Books, and its webhooks API does not document how deliveries are signed, so Taskiem asks Books itself every 15 minutes while a workflow listens (`changes_since`): invoices changed since the last look (`last_modified_time`) and the latest payments recorded. Each is an event: `invoice.created` (made since), `invoice.paid` (paid), `invoice.updated` (changed otherwise) or `payment.created`. Polling every 15 minutes is 192 calls a day per organisation, within the free plan's 1,000. The money log's own polling never polls books, whose events are records of money rather than movements of it.

- **What a run gets:** `body.event` and `body.data`: the record's `id`, `kind`, `number`, `reference`, `customer_id`, `invoice_id` (a payment's first invoice), `total_kobo` or `amount_kobo`, `currency`, `status`, `modified_at`.
- **Once each:** an invoice made, paid or a payment is taken once; a change to an invoice once per time it changed.
- **Correlation:** the invoice, so a run waiting on it wakes when it is paid or a payment is applied to it.

## Templates

The books templates ([QuickBooks](quickbooks.md#templates)) run on Zoho Books unchanged: **Copy an invoice to the books**, **Record invoice payments in the books**, and **Record the school's daily takings in the books** (for the last, turn on Sales Receipts in Books: Settings → General).

## Needs a person

- A Zoho developer client with Multi-DC, and Books test organisations in the Nigeria and global editions, for a live check: connect from two data centres, each write twice, the poll, a refresh (K0-5).

## To confirm with Zoho

1. That a line without an `item_id` is accepted on invoices and sales receipts (the create reference marks `item_id` on line items as required; the connector sends a name, description, rate and quantity, and the item when given).
2. Which scope covers sales receipts (the scope list names none; the connector asks for `ZohoBooks.invoices.ALL`).
3. That customer payments list by `sort_column=created_time` and `sort_order=D` (the list reference names neither for payments).
4. The token and revoke endpoints taking their parameters as a form body (Zoho's examples put them in the query).
5. That `last_modified_time` takes `2026-10-26T09:00:00+0000` (the reference writes the format as `YYYY-MM-DDTHH:MM:SS-UTC`).
6. How Books signs webhooks (the `secret` field's header and algorithm), to receive them as well as poll.
7. What the Nigeria edition's taxes look like through the API (VAT 7.5% and withholding tax), and its e-invoicing status on an invoice.
