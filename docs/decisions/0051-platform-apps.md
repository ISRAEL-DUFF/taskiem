# 0051. Platform apps, webhooks routed to the business, and books capabilities

**Status:** Accepted (2026-10-26; proposed 2026-10-24, built in K1)

## Context

The [business connectors plan](../spec/business-connectors.md) connects Slack, Instagram and Facebook messages, Zoho, QuickBooks Online and Mailchimp so that a business clicks **Connect** and signs in. Three of these providers allow that only through an app the integrator registers once and every business installs: Slack's distributed apps, Meta's Tech Provider apps and Intuit's apps. Zoho and Mailchimp offer the same. These apps send every installed business's events to **one** webhook URL, set on the app, not one per business. Each event names its account (Slack's `team_id`, a Page or Instagram account id, an Intuit `realmId`).

Taskiem's hooks today are all a tenant's own: `/hooks/{tenant}/connectors/{ref}/{trigger}`, verified with a secret of that tenant's connection ([0021](0021-remote-trigger-registration.md)). [0031](0031-shared-provider-clients.md) routes one partner's subscription into its own sub-tenants by an account key, but only within the partner. The WhatsApp platform number ([WhatsApp](../whatsapp.md)) already receives Meta's events for Taskiem's app at `/channels/whatsapp`, as a channel rather than a connector.

Two smaller gaps come with these apps:

- **Rotated tokens.** QuickBooks issues a new refresh token on a refresh and revokes the chain when an old one is used again. Connectors refresh inside the process ([0034](0034-oauth-connections.md)), but a connector's response carries only its output, so a rotated token cannot be stored.
- **Accounts that live in one place.** Zoho keeps each account in one data centre, with its own accounts server and API domain. Mailchimp keeps each account in a server prefix.

## Decision

1. **One platform app per provider.** Taskiem registers one app at each provider. Its credentials are process secrets in the environment: `TASKIEM_OAUTH_<CLIENT>_CLIENT_ID` and `_SECRET` as in 0034, plus the provider's webhook secret where it has one. One app may serve several connectors: a manifest's `auth.oauth2.client` names it (`zoho` for Books, Mail and CRM; `meta` for Messenger and Instagram), and defaults to the connector's own name. Without the app's variables, **Connect** is hidden and pasted credentials still work.
2. **A connection records its account key.** A connection made through a platform app keeps the provider's account key as a non-secret field, read from the token answer or the callback (`fields_from_token`, or a callback parameter such as `realmId`). One account may be connected by more than one tenant only where the provider allows it, and each tenant's connection receives the account's events.
3. **App-level webhooks.** The edge serves `POST /hooks/apps/{connector}/{trigger}`, one URL per app-delivered trigger (a provider's app may take several, as Slack's events and interactivity do), and `GET` for handshakes (Meta's `hub.challenge`; Slack's `url_verification` comes by POST). A request is verified with the platform app's secret (`TASKIEM_OAUTH_<CLIENT>_WEBHOOK_SECRET`), by the scheme the connector's trigger declares (`slack_v0`; `hmac_sha256` for Meta's `X-Hub-Signature-256` and, base64, Intuit's `intuit-signature`). It is then:
   - split into events by the trigger's `split`;
   - for each event, matched by the trigger's account-key expression against the connections made through that app;
   - ingested into each matching connection's tenant, exactly as if it had arrived at that tenant's own URL: the same dedup, correlation, subscribers, limits and history. Which connections hold the account comes from one database function (`taskiem_app_route_event`), the only step that crosses tenants: it reads routing columns only and returns where to deliver, never a credential.
4. **Unknown accounts are dropped.** An event whose account no connection holds is acknowledged with 200, counted per connector, trigger and day, and never stored. Providers retry what is not acknowledged, and an uninstalled account would be retried for days.
5. **Own apps keep the tenant URL.** A business that connects its own app keeps `/hooks/{tenant}/connectors/...` and its connection's secret, as today. Both kinds of connection may exist for one connector.
6. **Refreshed credentials are written back.** A connector whose provider rotates refresh tokens refreshes through `Request.RotateCredentials`: the vault locks the connection's row, reads its credentials as stored now, lets the connector refresh only if they are still stale, and stores the result, sealed, before the lock goes. Refreshes for one connection happen one at a time across workers, and a worker that waited gets the newer token instead of refreshing. The write is audited as `connection.refresh` with no values. A refresh the provider refuses marks the connection expired with the reason. (Proposed as credentials returned with the response; built as a refresh under the lock, since a response comes too late to stop a second worker spending the same token.)
7. **Per-account hosts are connection fields.** Zoho's accounts server (named on the callback) and API domain (in the token answer), and Mailchimp's server prefix, are kept on the connection when it is made. The token exchange, refreshes and every call use them, and the egress allow-list follows them as it follows Xenibase's `base_url`. The allowed hosts are fixed per connector in `connection_hosts` (`*.zoho.com`, `*.zoho.eu`, …), so a field cannot point a connection, Taskiem's client secret or a code anywhere else (connector/v1 rule 19).

8. **Books capabilities.** Connector/v1 rule 16 lets only a `kind: rail` declare capabilities. A `kind: product` may now declare the books capabilities of `schemas/capabilities-v1.json` (`books_customer`, `books_invoice`, `books_payment`, `books_sale`, `books_expense`, `books_bill`, and the pickers `books_accounts`, `books_items`, `books_taxes`) and the events `books.invoice.created`, `books.invoice.paid` and `books.payment.recorded`, and no others. A template that keeps the books is written once and runs on Zoho Books, QuickBooks or a later books, as a payroll template runs on any rail ([plan §4.7](../spec/business-connectors.md#47-books-capabilities)).

## Alternatives considered

| Alternative | Why not |
|---|---|
| Each business registers its own app | What the Slack connector does today. It needs a developer console, scopes and secrets: the technical steps the no-technical-steps work removed. Meta and Intuit don't let small businesses do it in practice |
| A webhook URL per business on the provider | Slack, Meta and Intuit don't offer it: the URL belongs to the app |
| A relay service that receives the app's webhooks and forwards them to tenant URLs | A second ingress with its own verification, secrets and retries, and a second place for an event to be lost. Routing inside the edge reuses ingest's guarantees |
| Extend 0031's partner routing to the whole platform | 0031's trust boundary is one partner and its own sub-tenants; crossing tenants needs a platform secret and the account key recorded at connect time, which is this decision |
| A books template per provider | Two copies of every books template to keep in step, and a third with Xero; the rails already showed capabilities avoid that |
| A refresh service that owns all tokens | Refreshing in the connector stays as 0034 has it; only the write-back and the lock are new |

## Consequences

- Connector/v1 manifests gain `auth.oauth2.client`, `account_field` and `fields_from_callback`, a trigger's `delivery: app`, `account_key` and `revokes` (rule 18), and `connection_hosts` (rule 19); the `svix` verification scheme and the renewal of subscriptions that expire are added in the same work ([plan](../spec/business-connectors.md) K1).
- `schemas/capabilities-v1.json` gains the books capabilities and events; rule 16 names which kinds may declare which.
- `connector.Request` gains `Rotate` and `RotateCredentials`; `secrets.Vault` gains `RotateCredentials`.
- The edge gains `/hooks/apps/{connector}/{trigger}` (migration 00228: `connection_app_accounts`, `app_event_drops`, `taskiem_app_route_event`). It is the one ingress path that writes into a tenant named by data rather than by the URL, so its routing function is the place to review and test cross-tenant isolation.
- Uninstall and revoke events (Slack's `app_uninstalled` and `tokens_revoked`, Meta's deauthorisation callback, Intuit's disconnect) mark the account's connections expired: a trigger's `revokes` expression says which events are.
- Each platform app is an account Taskiem holds at the provider, with reviews and lead times ([plan](../spec/business-connectors.md#14-people-accounts-and-reviews), [needs people](../needs-people.md#business-connectors-k0)).

## Provenance

From the founder's list of business connectors (2026-10-24): finish Slack, Instagram and Facebook messages, Zoho's products, QuickBooks, and Brevo, Resend and Mailchimp. Writing the plan found that three of the five need an app-level webhook Taskiem does not have.
