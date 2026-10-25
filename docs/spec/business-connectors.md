# Business connectors: Slack, Instagram and Facebook, Zoho, QuickBooks, email

Status: **Proposed** 2026-10-24 · Decision: [0051](../decisions/0051-platform-apps.md) (proposed) · Workstream **K0 to K9**, one pull request per milestone

The services small businesses already run their day on, connected so that a business clicks **Connect** and signs in, with no developer setup:

- **Slack**, finished: one click to add it, and templates that use it.
- **Instagram and Facebook messages**: where much Nigerian selling happens.
- **Zoho**: Books (a Nigeria edition since September 2026), Mail, CRM and Inventory.
- **QuickBooks Online**: the books for the businesses not on Zoho.
- **Email**: Resend (transactional), Brevo (transactional, marketing and SMS) and Mailchimp (marketing).

Contents:

1. [Summary](#1-summary)
2. [Words used here](#2-words-used-here)
3. [Where we are](#3-where-we-are)
4. [Platform pieces](#4-platform-pieces)
5. [Slack, finished](#5-slack-finished)
6. [Instagram and Facebook messages](#6-instagram-and-facebook-messages)
7. [Zoho](#7-zoho)
8. [QuickBooks Online](#8-quickbooks-online)
9. [Email: Resend, Brevo and Mailchimp](#9-email-resend-brevo-and-mailchimp)
10. [Templates and the AI builder](#10-templates-and-the-ai-builder)
11. [Milestones](#11-milestones)
12. [Order and timing](#12-order-and-timing)
13. [Testing](#13-testing)
14. [People, accounts and reviews](#14-people-accounts-and-reviews)
15. [Risks and open questions](#15-risks-and-open-questions)
16. [Out of scope](#16-out-of-scope)

---

## 1. Summary

Five providers, each built as ordinary connector/v1 connectors under the clean-room policy: from their public documentation only, read and cited in each manifest's header.

They share four things the platform does not do yet, and those come first (K1):

- **Taskiem's own app at each provider.** A business connects with OAuth through Taskiem's registered app instead of making its own. Slack, Meta and Intuit require this for one-click connecting; Zoho and Mailchimp offer it.
- **Webhooks sent to the app, not to the business.** Slack, Meta and Intuit send every connected business's events to the one URL on Taskiem's app. Today every hooks URL is a tenant's own (`/hooks/{tenant}/...`). [Decision 0051](../decisions/0051-platform-apps.md) adds a platform URL per provider and routes each verified event to the connections that hold its account (a workspace, a Page, a company).
- **Refresh tokens that change.** QuickBooks issues a new refresh token on each refresh and revokes the chain if an old one is used again; Zoho's access tokens last an hour. Connectors refresh in the process today (Google does), but cannot store what the provider hands back. K1 lets them, under a lock per connection.
- **Accounts in several places.** Zoho keeps each user's data in one of several data centres, and Mailchimp in a server prefix. The token answer says which, and the connection keeps it.

Then, in order: Slack (small, and proves the platform pieces), QuickBooks and Zoho Books (the books, which invoices and the money log of [0045](../decisions/0045-tally-in-taskiem.md) feed), Instagram and Facebook (the longest wait is Meta's review, so its application starts in K0), Zoho's other products, and email.

**Nothing here moves money.** Accounting connectors write records (invoices, payments, expenses) that describe money moved elsewhere. They are writes with idempotency or reconciliation like any other, and they never pay anyone.

## 2. Words used here

- **Platform app**: the app Taskiem registers once at a provider (a Slack app, a Meta app, an Intuit app, a Zoho client). Its credentials are process secrets in the environment, like the WhatsApp platform number's.
- **Own app**: an app the business made itself and whose tokens it pastes, as Slack connections work today. Kept where the provider allows it, for businesses that want it.
- **Account key**: what a provider's event names its account by: Slack's `team_id`, a Facebook Page id, an Instagram professional account id, an Intuit `realmId`. A connection made through a platform app records it.
- **Messaging window**: Meta's rule that a business may answer a person freely for 24 hours after their last message; after that only with a message tag.

## 3. Where we are

| Have | Where |
| --- | --- |
| `slack@1`: 9 actions, the Events API and interactivity, with a pasted bot token and signing secret; **no templates use it** | [Slack](../integrations/slack.md) |
| OAuth connections: state and PKCE kept server-side, client registration in the environment per connector (`TASKIEM_OAUTH_<CONNECTOR>_*`), refresh tokens kept as credentials (Gmail, Sheets) | [Decision 0034](../decisions/0034-oauth-connections.md), `api/oauthconnections.go` |
| Remote registration of webhook subscriptions, crash-safe by name | [Decision 0021](../decisions/0021-remote-trigger-registration.md) |
| A partner's one subscription routed into its sub-tenants by an account key | [Decision 0031](../decisions/0031-shared-provider-clients.md) |
| Taskiem's Meta app, for the WhatsApp platform number (app secret, `X-Hub-Signature-256`) | [WhatsApp](../whatsapp.md) |
| Messages to people outside a business, consent by a one-tap link | [Decision 0039](../decisions/0039-reaching-people-outside.md) |
| Invoices, the money log and matching | [0045](../decisions/0045-tally-in-taskiem.md), [money log](../money-log.md) |
| Callable templates and workflows | [0047](../decisions/0047-composable-templates.md), [0048](../decisions/0048-callable-workflows.md) |
| Remita, the certified provider Zoho Books' Nigeria edition uses for NRS e-invoicing | [Remita](../integrations/remita.md) |

**Missing:**
- app-level webhooks;
- storing rotated refresh tokens;
- per-account API hosts;
- connectors for Meta messaging, Zoho, QuickBooks, Resend, Brevo and Mailchimp;
- templates for any of them.

## 4. Platform pieces

### 4.1 Platform apps and connecting

Each provider gets one registered app, its credentials in the environment as 0034 already does: `TASKIEM_OAUTH_SLACK_*`, `TASKIEM_OAUTH_META_*`, `TASKIEM_OAUTH_QUICKBOOKS_*`, `TASKIEM_OAUTH_ZOHO_*`, `TASKIEM_OAUTH_MAILCHIMP_*`. **One registration may serve several connectors**: the Zoho client serves Books, Mail, CRM and Inventory, and the Meta app serves Instagram and Messenger. So a manifest's `oauth2` names its client (`client: zoho`) instead of always its own id. When an app's variables are absent, its **Connect** button is hidden and the connector still takes pasted credentials where it has them.

Connecting asks only for the scopes the connector needs. A business that connects Zoho Books and later Zoho Mail grants Mail separately, so revoking one leaves the other.

### 4.2 App-level webhooks (decision 0051)

- **A platform URL per provider**: `POST /hooks/apps/{provider}` on the edge, and `GET` for handshakes (Meta's `hub.challenge`, Slack's `url_verification`).
- **Verified with the platform app's secret** from the environment:
  - Slack's signing secret, scheme `slack_v0`;
  - Meta's app secret, `X-Hub-Signature-256`, as for WhatsApp;
  - Intuit's verifier token, `intuit-signature`, base64 HMAC-SHA256 of the body.
- **Routed by account key.** The manifest's trigger declares the key's expression (`=body.team_id`, `=item.id` for a Meta entry, `=item.realmId`). The event goes, through one audited function as in 0031, into every connection made through the platform app whose recorded account key matches, in any tenant, as if it had arrived at that tenant's own URL: the same dedup, correlation, subscribers and limits.
- **An event for an account no connection holds** is acknowledged, counted and dropped, never stored. Providers retry what is not acknowledged, and an uninstalled account would otherwise be retried for days.
- **Meta delivers several entries in one request**, already handled by the trigger's `split`.
- **Own apps keep their per-tenant URL** (`/hooks/{tenant}/connectors/...`), as today.

### 4.3 Refreshing tokens

A connector may return credentials it refreshed (`Response.Credentials`, and the same from a refresh helper on every call). Taskiem then:

- **Stores them before the step's result is recorded**, under a per-connection advisory lock: one refresh at a time per connection, across workers, so two workers never spend one QuickBooks refresh token. A worker that waited reads the stored token instead of refreshing again.
- **Never logs them**, and records the write in the audit log as `connection.refresh` with no values.
- **Marks the connection expired** (the existing `ErrCredentialsRejected` path) when the provider says the refresh token is invalid, with the plain reason: "QuickBooks asks you to connect again".

Access tokens are cached in memory per connection until shortly before they expire, so a busy workflow does not refresh on every step.

### 4.4 Where an account lives

- **Zoho:** the authorize redirect carries `location` and `accounts-server`. Taskiem uses that accounts server for the token exchange and refreshes, and keeps it and the answer's `api_domain` as non-secret connection fields. Every call goes to that API domain, and the egress allow-list follows the field, as Xenibase's `base_url` does.
- **Mailchimp:** after the token, Taskiem reads `https://login.mailchimp.com/oauth2/metadata` once and keeps the `dc` server prefix.
- **QuickBooks:** the callback carries `realmId`, kept as the connection's company and its account key.

### 4.5 Messaging rules

- **The window is checked before sending.** Instagram and Messenger replies inside the window are free-form. Outside it, a step may only use a message tag Meta allows for its purpose (`HUMAN_AGENT`, up to 7 days, once Meta grants it). A step outside the window with no allowed tag fails as fatal before anything is sent, saying when the window closed.
- **Promotional messages go only inside the window**, as Meta's policy says.
- **Email marketing needs consent.** A contact added to a marketing list must have opted in (the Nigeria Data Protection Act 2023; the providers' own terms). Templates add only contacts whose row says so, and every marketing send carries the provider's unsubscribe.

### 4.6 Pickers

Each connector gets the pickers its templates need, so no owner types an id (no-technical-steps N8):

- a Slack channel;
- a Facebook Page or Instagram account;
- a Zoho organisation, Mail account or CRM module;
- a QuickBooks income account or item;
- a Brevo or Mailchimp list;
- a Resend sending domain.

Each picker is a list action plus `picker` in the template parameter, as sheets and tables already are.

## 5. Slack, finished

**Connecting.** **Add to Slack** through Taskiem's Slack app (OAuth v2, `oauth.v2.access`):
- It asks for the bot scopes the actions use (the table in [Slack](../integrations/slack.md)).
- It keeps the bot token, the team id (the account key) and the team's name.
- The pasted-token connection stays, as an own app.
- Slack's app must have public distribution turned on to be installed outside Taskiem's own workspace. A Marketplace listing is optional and comes later.

**Events and interactivity** for platform connections come to `/hooks/apps/slack` and are routed by `team_id`. Slack sends `url_verification` once for the app; the existing handshake answers it. `app_uninstalled` and `tokens_revoked` mark the workspace's connections expired.

**Actions added:**

| Action | Method | For |
| --- | --- | --- |
| `list_users` | `users.list` | A member picker; who to mention |
| `open_conversation` | `conversations.open` | A direct message to one person found by email |
| `schedule_message` | `chat.scheduleMessage` | Reminders at a time |

**Templates** (all post to a channel the owner picks):
- Failed payouts and returned salaries.
- A daily money summary from the money log.
- New storefront orders and paid invoices.
- An approval request with Approve and Reject buttons, decided through interactivity and checked against the approver's role. This is the Slack counterpart of the WhatsApp buttons, and uses the same decision tokens.

**Done when:**
- a business adds Slack with one click;
- a workflow posts, waits for a button and goes on;
- the events of two businesses' workspaces reach only their own workflows;
- uninstalling marks the connection expired.

## 6. Instagram and Facebook messages

**What it is for:** answering and following up customers who write to a business's Instagram account or Facebook Page:
- an order taken in a DM becomes a payment link or an invoice;
- an auto-reply outside working hours;
- a new message assigned to a member;
- a paid order thanked in the same chat.

**Connectors:** `messenger@1` (a Facebook Page) and `instagram@1` (an Instagram professional account), on Taskiem's Meta app.

**Connecting** (to decide in K0, from Meta's current documentation):
- **Proposed: Facebook Login for Business for both.** One login grants a business's Pages and the Instagram accounts linked to them, with `pages_messaging`, `pages_manage_metadata`, `instagram_basic` and `instagram_manage_messages`.
- **Instagram API with Instagram Login** (`instagram_business_basic`, `instagram_business_manage_messages`) reaches Instagram accounts with no Facebook Page. Meta's documentation says an app uses one login or the other for its Instagram product. If many businesses have no Page, this becomes a second app, or the default for `instagram@1`. K0 measures this with the first businesses.
- **Taskiem's Meta app needs, for other businesses' accounts:**
  - Advanced Access to these permissions, through App Review, as a Tech Provider;
  - Business Verification of Taskiem.

  Both are slow, and applied for in K0 (§14).

**Triggers** (app-level, `/hooks/apps/meta`, routed by the entry's Page or Instagram account id):

| Trigger | Events |
| --- | --- |
| `messages` | A message (text, attachments, story replies, quick-reply answers), a postback (a button), a referral (an ad or link that opened the chat), seen and delivered |
| `comments` (later) | Comments on posts, for "reply in DM to everyone who commented PRICE" |

After connecting, Taskiem subscribes the Page to the app (`POST /{page-id}/subscribed_apps`). Meta unsubscribes an app whose webhook fails for long, so Taskiem's health check re-reads subscriptions daily and alerts.

**Actions:**

| Action | Notes |
| --- | --- |
| `send_message` | Text, image or file, quick replies or buttons, to a person by their Page- or Instagram-scoped id; the messaging window enforced (§4.5) |
| `send_typing`, `mark_seen` | Sender actions |
| `get_profile` | The person's name (and profile picture for Instagram), sealed |
| `get_conversation` | Recent messages, for context |
| `reply_to_comment` (later) | A private reply to a comment |

**Never twice.** Meta's Send API takes no idempotency key, so `send_message` is an unsafe write: an unknown outcome parks it for a person.

**People and consent.** A person who wrote first opened the window. Their Instagram or Page-scoped id is their address there, and becomes a contact of the business, as 0039 does for WhatsApp. Nothing is sent to anyone who has not written.

**Done when:**
- a business connects its Page and Instagram account in one login;
- a DM starts a workflow that answers in the same chat and makes a payment link;
- a reply after the window closes is refused before sending, saying why;
- two businesses' messages reach only their own workflows.

## 7. Zoho

**Why Zoho now:** many Nigerian businesses use Zoho for mail and books. Zoho launched a **Nigeria edition of Zoho Books on 17 September 2026**, with:
- VAT at the Nigerian rates;
- TaxProMax-format VAT schedules;
- NRS-compliant e-invoicing through Remita, a certified provider.

Businesses on the global edition can move to it if their organisation is in Nigeria with base currency NGN.

**One Zoho client, several connectors.** Taskiem registers one server-based client at Zoho's developer console, with multi-data-centre support on, and every Zoho connector uses it (§4.1). Access tokens last an hour; refresh tokens are kept and refreshed (§4.3); the account's data centre and API domain are kept (§4.4).

### 7.1 `zoho_books@1` (first)

Books' API (v3) calls carry the organisation (`organization_id`). A connection is one organisation, chosen in a picker after connecting (`GET /organizations`), as Xenibase connections are one project.

| Action | Notes |
| --- | --- |
| `list_organizations` | The connection test and the picker |
| `create_contact`, `find_contact` | Customers and vendors, found by email or phone before creating, so a retry finds the one it made |
| `create_invoice`, `get_invoice`, `find_invoices` | With items, VAT and due date; `reference_number` set from Taskiem's invoice number, so a retry finds the invoice instead of making a second (a reconcilable write) |
| `record_payment` | A customer payment against invoices, with the money log entry's reference; reconcilable by that reference |
| `create_expense`, `create_bill` | Purchases and supplier bills |
| `list_items`, `list_accounts`, `list_taxes` | Pickers |

**Triggers:** Books calls webhooks from its workflow rules (an invoice created or paid, a payment recorded). K0 confirms from the webhooks API whether Taskiem can create them itself (then remote registration, 0021) or the owner adds the URL in Books (then the URL is shown on the connection page). Until then, polling lists what changed since the last look.

**E-invoicing:** Books sends invoices to the NRS through Remita itself, once the business sets it up in Books. Taskiem does not submit to the NRS. A workflow can read an invoice's e-invoicing status and tell the owner when one was refused.

### 7.2 `zoho_mail@1`

Like `gmail@1`:
- send, reply, list and read messages, labels and folders;
- the account picked from the user's Mail accounts (`accountId`);
- the scopes asked for match the actions (read only for a reading workflow).

**New mail:** by polling, unless K0 finds a webhook Zoho Mail offers.

### 7.3 `zoho_crm@1`

- **Actions:** leads, contacts, deals and notes, by module; upsert by a unique field (CRM's own upsert), so a retry updates instead of duplicating.
- **Triggers:** CRM's notification API "watches" modules for a time and must be renewed before it expires. That is remote registration with a renewal: the reconciler renews each watch daily, a small extension of 0021 (`Renew`, or `Update` on a schedule).

### 7.4 Later Zoho products

These are each a small connector on the same client, built when a template needs one:
- **Inventory:** items, stock levels, sales orders; stock alerts and reorders.
- **Invoice:** for businesses on Zoho Invoice rather than Books.
- **Campaigns:** email marketing.
- **Desk:** support tickets.
- **Sign:** documents to sign.
- **People:** HR.

## 8. QuickBooks Online

**Connector:** `quickbooks@1`. Intuit's Accounting API, OAuth 2.0 on Taskiem's Intuit app:

- **Scope:** `com.intuit.quickbooks.accounting`.
- **Tokens:**
  - Access tokens last about an hour.
  - A refresh can return a **new refresh token**, which must be stored, and an old one must never be used again (§4.3).
  - Intuit's 2025 policy gives refresh tokens a maximum life of five years. K0 confirms the dates.
- **The company:** the callback carries the `realmId`, the account key.
- **Sandbox and production:** Intuit gives developers sandbox companies, which all fixtures and tests use. Production keys need Intuit's app review (§14).
- **Nigeria:** QuickBooks Online has a Nigeria edition and naira. K0 confirms what Nigerian companies see in the API (tax codes, currency).

| Action | Notes |
| --- | --- |
| `get_company` | The connection test (CompanyInfo) |
| `find_customer`, `create_customer` | By email or display name first |
| `create_invoice`, `get_invoice`, `query_invoices` | `DocNumber` from Taskiem's invoice number. Creates send Intuit's `requestid`, so a repeated request is answered once (K0 confirms the window); also reconcilable by `DocNumber` |
| `create_payment` | A received payment applied to invoices, with the money log reference in its memo |
| `create_sales_receipt` | A day's POS or storefront takings |
| `create_purchase`, `create_bill` | Expenses and supplier bills |
| `list_items`, `list_accounts`, `list_tax_codes` | Pickers |

**Triggers:**
- **Where they arrive:** QuickBooks sends change notifications to the one webhook URL of Taskiem's Intuit app. They come to `/hooks/apps/quickbooks`, are verified with the verifier token, and are routed by `realmId`.
- **What they carry:** a notification names the entities that changed (Invoice, Payment, Customer…) and their ids; an enricher reads each entity before the run starts.
- **Format:** Intuit is moving notifications to a CloudEvents format; K0 confirms which Taskiem receives and builds to the current one.
- **Catching up:** after an outage, Taskiem reads what changed since the last notification (Intuit's change data capture) for each connected company.

**Done when:**
- a business connects QuickBooks in one click;
- every invoice sent from Taskiem appears once in QuickBooks with its payments, however often a step is retried;
- an invoice created in QuickBooks starts a workflow;
- a refresh that rotates the token is stored and survives concurrent workers.

## 9. Email: Resend, Brevo and Mailchimp

Three providers for different jobs. Taskiem's own SMTP (alerts and password emails) is unchanged; these send a business's email from its own domain.

| | Resend | Brevo | Mailchimp |
| --- | --- | --- | --- |
| For | Transactional email from code: receipts, invoices, notices | Transactional and marketing email, and SMS | Marketing to an audience |
| Connecting | API key (pasted) | API key (pasted) | OAuth on Taskiem's Mailchimp app, or an API key |
| Never twice | `Idempotency-Key` on `POST /emails` (24 hours; a reused key with another body is refused): an idempotent write | No key: an unsafe send, parked on an unknown outcome | Contacts upserted by `PUT /lists/{id}/members/{md5 of the lowercased email}`, an idempotent write; campaigns sent once by their id |
| Events | Sent, delivered, bounced, complained, opened, clicked, by webhook. Signed by Svix (`svix-id`, `svix-timestamp`, `svix-signature`, base64 HMAC-SHA256 over `id.timestamp.body`), so connector/v1 gains a `svix` verification scheme | Transactional and marketing events; Taskiem creates the webhook through the API (remote registration) with a bearer token it chooses (scheme `bearer`) | List events (subscribe, unsubscribe, profile and email changes, cleaned) by a webhook Taskiem creates on the list; K0 confirms how deliveries are signed |
| First actions | `send_email` (HTML or text, attachments, tags), `get_email`, `list_domains`; audiences, contacts and broadcasts later | `send_email`, `send_sms`, `create_or_update_contact`, `add_to_list`, `list_lists`, `send_campaign` | `add_or_update_member`, `tag_member`, `list_audiences`, `create_campaign`, `send_campaign` |

**Deliverability is the business's.** Each sends from a domain the business verified with the provider. The connection test says which domains are verified, and a send from an unverified domain is refused before calling.

**Done when:**
- a receipt is emailed once through Resend, however often its step is retried;
- a new customer who opted in is added once to a Brevo or Mailchimp list;
- a bounce or unsubscribe updates the customer's row;
- nobody who did not opt in is ever added to a marketing list.

## 10. Templates and the AI builder

Each connector ships with templates, made callable where they return something (0047). They have tests on fakes, Start page entries once the service is connected (as Payrolla's are, through the template's `product` field or the connector it needs), and AI eval cases:

| Connector | Templates |
| --- | --- |
| Slack | Money alerts to a channel; daily money summary; approvals with buttons; new orders and paid invoices |
| Instagram, Messenger | Answer DMs outside working hours; a DM order becomes a payment link; thank the customer when paid; assign new conversations |
| Zoho Books, QuickBooks | Copy each Taskiem invoice to the books; record matched payments; a day's POS takings as a sales receipt; supplier bills from email |
| Zoho Mail | Invoices and receipts from the business's own mailbox; supplier bills from mail into the books |
| Zoho CRM | New customers and storefront orders as contacts and deals |
| Resend | Receipts and invoices by email |
| Brevo, Mailchimp | Opted-in customers into a list; win-back campaigns to customers who have not bought in 60 days |

The AI builder learns each connector from its manifest as it does today. The eval suite gains cases per connector, and rules:
- a reply after the window is never planned;
- marketing only to opted-in contacts;
- invoices copied once, with their reference.

## 11. Milestones

### K0: facts, accounts and reviews (people, S of engineering)

1. **Register the platform apps:** Slack, Meta, Intuit, Zoho and Mailchimp.
2. **Start Meta's Business Verification and App Review** (§14), the longest wait.
3. **Read each provider's current documentation** on the points marked "K0 confirms", and record the answers in each integration page:
   - Meta's login choice;
   - QuickBooks' notification format, refresh-token dates and `requestid` window;
   - Zoho Books' webhook API and Zoho Mail's push;
   - Zoho's limits on refresh tokens;
   - how Mailchimp signs webhooks;
   - Resend's webhook API;
   - QuickBooks' and Zoho's Nigeria editions in their APIs.
4. **Make sandbox accounts:** an Intuit sandbox company, Zoho Books test organisations (Nigeria and global editions), a Slack test workspace, a Meta test Page and Instagram account.

**Done when:** every "K0 confirms" has an answer in the integration pages, the apps exist, and Meta's review is submitted.

### K1: platform apps, routing and refreshing (L)

- **App-level webhooks** (§4.2, decision 0051): `/hooks/apps/{provider}`, verification by the platform secret, routing by account key in one audited function, the counted drop of unknown accounts, handshakes.
- **Clients shared by several connectors** (§4.1).
- **Refreshed credentials**, stored under a per-connection lock with an in-memory access-token cache (§4.3).
- **Per-account API hosts** (§4.4), with the egress allow-list following them.
- **The `svix` verification scheme.**
- **Remote registration's renewal** for subscriptions that expire.

**Done when:**
- two tenants' events for two accounts at one provider each reach only their own connection;
- an unknown account's event is dropped and counted;
- ten workers refreshing one connection's token at once spend one refresh;
- a rotated token survives a restart.

### K2: Slack (M; needs K1)

§5: **Add to Slack**, platform routing, uninstall handling, the three actions, the templates, the approval buttons.

### K3: QuickBooks Online (L; needs K1, K0's sandbox)

§8, on Intuit's sandbox companies, with the invoice and payment templates over invoices and the money log.

### K4: Zoho platform and Zoho Books (L; needs K1)

The Zoho client with data centres (§7, §4.4), `zoho_books@1` (§7.1), the organisation picker, the Books templates, both editions in tests.

### K5: Instagram and Facebook messages (L; needs K1, Meta's review)

§6. It can be built and tested on Meta's test Page and accounts as soon as K1 lands. It goes to businesses only when Meta grants Advanced Access.

### K6: Zoho Mail and Zoho CRM (M; needs K4)

§7.2 and §7.3, with the watch renewal.

### K7: Resend and Brevo (M; needs K1 for `svix`)

§9: both connectors, their webhooks, the consent rule, the receipt and list templates.

### K8: Mailchimp (S; needs K1)

§9, with OAuth and the server prefix.

### K9: AI and Start page (S)

The eval cases and rules of §10, the Start page entries by connected service, and the website's connector pages.

## 12. Order and timing

| Order | Milestone | Why then |
| --- | --- | --- |
| 1 | K0, started now | Meta's review and Intuit's production review take weeks and cost little to start |
| 2 | K1 | Everything after it needs it |
| 3 | K2 Slack | Small, and proves the platform pieces on a provider Taskiem already knows |
| 4 | K3 QuickBooks, K4 Zoho Books | The books: invoices and the money log are built and waiting for them |
| 5 | K5 Instagram and Facebook | Built while Meta reviews; released when it grants |
| 6 | K6, K7, K8 | Zoho's other products and email, in the order businesses ask |
| 7 | K9 | Alongside each, finished last |

**About 10 to 13 weeks for one engineer;** about 6 to 8 for two, one on K1 to K3 and one on K4 to K8. This doesn't count Meta's review, which runs alongside.

## 13. Testing

| Level | What |
| --- | --- |
| Conformance | Each connector's actions on recorded fixtures from the providers' sandboxes (Intuit sandbox companies, Zoho test organisations, Slack and Meta test accounts, Resend and Brevo test domains) through the conformance kit |
| Verification | Each scheme on the provider's documented examples, a changed body, a wrong secret and a stale timestamp: `slack_v0`, `X-Hub-Signature-256`, `intuit-signature`, `svix` |
| Routing | Two tenants and two accounts per provider; an unknown account; an account connected twice in one tenant; a connection deleted while its events arrive |
| Refreshing | Concurrent refreshes across workers spend one refresh token; a rotated token persists; an invalid refresh expires the connection with its reason |
| Never twice | An invoice copied to QuickBooks and Zoho Books once despite a lost answer; a Resend email sent once; a Brevo send whose outcome is unknown parks |
| Rules | A Meta reply after the window refused before sending; a marketing add without consent refused |
| Templates | Workflow tests for each template on fake services, as R4 and R7's do |
| End to end | One per provider on the real stack with a fake provider: connect by OAuth, an event in, a write out |

## 14. People, accounts and reviews

| Item | Who | Lead time |
| --- | --- | --- |
| Meta Business Verification of Taskiem's business | Founders | Days to weeks |
| Meta App Review for `pages_messaging`, `pages_manage_metadata`, `instagram_basic`, `instagram_manage_messages` (Advanced Access, as a Tech Provider), with screencasts of each use | Founders, with engineering | Weeks; may need several rounds |
| Slack app with public distribution; optional Marketplace listing later | Engineering | A day; Marketplace review takes weeks |
| Intuit app: development keys now; production keys after Intuit's app assessment (security questionnaire, privacy policy and terms URLs) | Founders, with engineering | Weeks |
| Zoho developer client (server-based, multi-DC) | Engineering | A day |
| Mailchimp app registration | Engineering | A day |
| Privacy policy and terms naming each provider's data use | Founders, legal | Before the reviews |
| Who answers businesses when a review asks for a demo account | Founders | During review |

## 15. Risks and open questions

| Risk or question | Plan |
| --- | --- |
| Meta refuses or delays Advanced Access | Build and test on test accounts. Release Messenger and Instagram when granted; until then businesses may connect their own Meta app (own app) |
| Instagram accounts without a Facebook Page | K0 measures. Then a second login (Instagram Login), as a second app if Meta requires it |
| QuickBooks' refresh token rotation done wrong locks a business out | One refresh at a time per connection under a lock; the newest token written before use; tests with concurrent workers (K1) |
| Zoho's limits on refresh tokens per user and client | K0 confirms. Taskiem keeps one refresh token per connection and reuses it, never issuing one per worker |
| Providers change webhook formats (Intuit's CloudEvents move) | Build to the current format; fixtures recorded from the sandbox; the integration page names the version |
| Email sent from unverified domains, or marketing without consent | Refused before calling (§9, §4.5) |
| A business with several Zoho organisations or QuickBooks companies | One connection each, as Xenibase projects are |
| Should Taskiem write to the books automatically, or only when a person confirms? | Proposed: templates copy invoices and matched payments automatically (they are records of what happened), and never edit or delete what the accountant wrote |

## 16. Out of scope

- Mailchimp Transactional (Mandrill), a separate paid product; Resend and Brevo cover transactional email.
- Instagram and Facebook posting and ads; comments come after messages.
- Zoho Desk, Sign, People and Campaigns until a template needs them (§7.4).
- Payroll inside Zoho or QuickBooks: payroll is Run payroll or a payroll product ([0044](../decisions/0044-rails-and-products.md)).
- Submitting e-invoices to the NRS from Taskiem: Zoho Books does it through Remita itself.
- Xero and Sage: the same accounting templates, after QuickBooks and Zoho Books.
