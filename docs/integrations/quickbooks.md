# QuickBooks Online integration

The connector is `connectors/quickbooks` (`quickbooks@1`, `kind: product`, category `accounting`). It keeps a business's books in QuickBooks Online up to date through Intuit's Accounting API, for one company per connection, and takes QuickBooks' change notifications as a trigger. Built from Intuit's public developer documentation, read 26 October 2026; the manifest header lists the pages. It is K3 of [business connectors](../spec/business-connectors.md#8-quickbooks-online), on [decision 0051](../decisions/0051-platform-apps.md).

## Connecting (one click)

A business connects QuickBooks with **Connect QuickBooks**: Intuit asks them to sign in and choose a company, and sends the browser back with the company's id (`realmId`), which the connection keeps as `realm_id`, its account (connector/v1 rule 18). Nothing is pasted. Before going to Intuit, the person chooses **Your company (real books)** or **An Intuit sandbox company (practice)**: Intuit's production keys reach only real companies and its development keys only sandbox ones, so each mode is linked with its own keys of Taskiem's Intuit app (rule 20) and its calls go to that mode's host (`quickbooks.api.intuit.com` or `sandbox-quickbooks.api.intuit.com`). The Connections page offers only the modes this Taskiem has keys for.

- **Tokens.** Intuit's access token lasts an hour. The connector refreshes it with Taskiem's client and stores what Intuit hands back, the refresh token included, once across every worker (`connector.Request.RotateCredentials`): Intuit may hand back a new refresh token on any refresh, and only the latest is good. A refresh token Intuit refuses (revoked, expired, or a disconnected app) marks the connection expired, and the Connections page says to connect again. Intuit caps a refresh token's life at five years from the first consent.
- **Disconnecting.** Deleting the connection gives the refresh token back to Intuit (`/v2/oauth2/tokens/revoke`, a JSON body, the client in HTTP Basic), which ends the grant.
- **Pasting.** A QuickBooks connection is made only by connecting: the tokens must be refreshed and stored by Taskiem, so a pasted refresh token is refused with that said.

Taskiem's Intuit app (once, by Taskiem's operators):

1. Create the app at <https://developer.intuit.com> with the `com.intuit.quickbooks.accounting` scope and the redirect URI `<TASKIEM_PUBLIC_URL>/v1/connections/oauth/callback`, under both **Development** and **Production** settings.
2. **Webhooks**, under both: the endpoint `<TASKIEM_HOOKS_URL>/apps/quickbooks@1/changes`, the entities and operations workflows use, and the CloudEvents format.
3. Put the production keys and verifier token in `TASKIEM_OAUTH_QUICKBOOKS_CLIENT_ID`, `_CLIENT_SECRET` and `_WEBHOOK_SECRET`, and the development ones in `TASKIEM_OAUTH_QUICKBOOKS_SANDBOX_*` ([operations](../operations.md#configuration)). Production keys need Intuit's app assessment; until then, set only the sandbox ones.

## Connection

| Field | Filled by |
| --- | --- |
| `realm_id` | The callback's `realmId`: the company, and the account the change notifications are routed by |
| `refresh_token` | The token answer, then each refresh |
| `client_id`, `client_secret` | Taskiem's Intuit app for the connection's mode (`platform_client_fields`) |
| `access_token`, `access_expires_at`, `refresh_expires_at` | The connector, on each refresh |

The connection check is `get_company`.

## Actions

Amounts are in kobo (minor units) in and out; QuickBooks keeps decimal amounts in the company's currency, and the connector converts both ways. Every call asks for minor version 75 (Intuit ignores earlier ones since August 2025).

| Action | Class | What it does |
| --- | --- | --- |
| `get_company` | read | CompanyInfo: name, legal name, country, email |
| `find_customer` | read | By email, then by display name |
| `create_customer` | idempotent write | Finds by email, then by display name, before adding; a name already taken (6240) answers that customer |
| `find_vendor`, `create_vendor` | read, idempotent write | Suppliers, by display name |
| `create_invoice` | idempotent write | Numbered `doc_number` (Taskiem's invoice number); an invoice with that number already there is answered (`created: false`), and so is a number taken (6140) |
| `get_invoice`, `query_invoices` | read | One invoice; or by number, customer, changed since, unpaid only |
| `create_payment` | idempotent write | A payment received, applied to an invoice (its customer then the invoice's); the reference kept in full in the note, and as the reference number (21 characters); found by it among the customer's payments that day before recording |
| `create_sales_receipt` | idempotent write | A sale paid at once (a day's takings), numbered by the day and source |
| `create_purchase` | idempotent write | An expense paid at once from a bank, cash or card account; `vat_kobo` taken out of the first line as its own VAT line |
| `create_bill` | idempotent write | A supplier's bill to pay later, numbered by the supplier's bill number, found by it for that supplier |
| `list_items`, `list_accounts`, `list_tax_codes` | read | Pickers; a tax code's percent is the sum of its sales rates (VAT at 7.5%) |
| `get_entity` | read | Any record a change names, as QuickBooks returns it |
| `changes_since` | read | Change data capture: the records of the named kinds changed since a time (at most 30 days back), deleted ones marked |

**Never twice.** Every write is sent with Intuit's `requestid` (the step's idempotency key, at most 50 characters), so a request sent again is answered with the first one's result, and is looked for first by its number or reference, so a retry after Intuit has forgotten the `requestid` still finds what it made. QuickBooks keeps numbers to 21 characters: a longer one is shortened the same way every time (its first 12 characters and 8 of its hash).

**Tax.** Line amounts are before tax. A line naming a tax code that is not a US one (`TAX`, `NON`) makes the transaction `TaxExcluded`, so QuickBooks adds the VAT; `tax_calculation` says otherwise. A line without an item uses the company's default item.

**Errors.** Intuit's `Fault` is read for its code: 401 (3200) is a refused access token, refreshed once and the call made again, then the connection is expired; 429 is the throttle (500 requests a minute per company, 10 at a time) and is retried; code 610 is a record that is not there (`not_found`); other validation faults are fatal with Intuit's message; 5xx is an unknown outcome, retried under the same `requestid`.

## Books capabilities

QuickBooks declares all nine books capabilities ([connector/v1 rule 16](../contracts/connector-v1.md)), so the books templates run on it:

| Capability | Action | Notes |
| --- | --- | --- |
| `books_customer` | `create_customer` | |
| `books_invoice` | `create_invoice` | The company's home currency: `currency` is ignored |
| `books_payment` | `create_payment` | `method` is ignored |
| `books_sale` | `create_sales_receipt` | One receipt goes into one account: the first taking's |
| `books_expense` | `create_purchase` | Needs `paid_from_id`; the supplier and description go in the note |
| `books_bill` | `create_bill` | |
| `books_accounts`, `books_items`, `books_taxes` | `list_accounts`, `list_items`, `list_tax_codes` | Cash on hand is a Bank account in QuickBooks |

**Events.** `books.invoice.created` (an invoice made in QuickBooks) and `books.payment.recorded` (a payment recorded there) come from the `changes` trigger. QuickBooks gives no `books.invoice.paid`: an invoice paid in full is an `invoice.updated`, told apart only by reading it.

## Triggers

### `changes` (QuickBooks' change notifications)

QuickBooks sends every connected company's changes to the one webhook URL of Taskiem's Intuit app, `/hooks/apps/quickbooks@1/changes` (`delivery: app`), as a JSON array of CloudEvents, signed with the app's verifier token (`intuit-signature`: the base64 HMAC-SHA256 of the body). Either app's notifications are taken there, the development app's for sandbox companies and the production app's for real ones. Each event is its own, routed by its company (`intuitaccountid`) to the connections holding it, deduplicated on its `id`.

- **Events:** `<entity>.<operation>` from the CloudEvents type (`qbo.invoice.created.v1` is `invoice.created`): invoices, payments, customers, suppliers, sales receipts, purchases, bills, bill payments, estimates, credit memos and items, as the manifest lists them.
- **What a run gets:** the event (`item`): `intuitentityid` (the record's id), `intuitaccountid`, `type`, `time`. Intuit wants an answer within three seconds, so nothing is read before answering: the workflow reads the record first (`get_entity`).
- **Correlation:** the record's id, so a run waiting on an invoice wakes when it changes; payments correlate on nothing.
- **Catching up:** after notifications were missed, `changes_since` reads what changed.

## Templates

They are written against the books capabilities, so they run on [Zoho Books](zoho-books.md) too.

- **Copy an invoice to the books** (`books-copy-invoice`, callable): finds or adds the customer, then records the invoice under its Taskiem number, with its total as one line.
- **Record invoice payments in the books** (`books-record-invoice-payments`): each payment counted towards an invoice copies the invoice (if it is not there yet) and records the payment against it with the money log's reference.
- **Record the school's daily takings in the books** (`books-school-daily-takings`): each evening, the daily close's money in as one sales receipt, its lines into account numbers and at the desk. For books that keep takings rather than each family's invoices.

## Needs a person

- An Intuit developer account and its sandbox company for a live check: connect, each write twice, a notification through ingest, a refresh that rotates the token.
- Intuit's app assessment for production keys.

## To confirm with Intuit

1. How long `requestid` is remembered (the docs say a repeated request is answered once, not for how long).
2. That a refresh token in use is still good when a refresh's answer was lost (the connector retries with the one it holds).
3. The CloudEvents `type` for each operation (`voided`, `emailed`, `merged`): the manifest lists the documented entities' create, update and delete, and these.
4. That `PrimaryEmailAddr` is queryable on Customer for every company (it is documented as filterable).
5. What Nigerian companies see: tax codes (VAT 7.5% and WHT), NGN as the home currency, and whether `GlobalTaxCalculation` is accepted as sent.
