# Business connectors: Slack, Instagram and Facebook, Zoho, QuickBooks, email

Status: **K1 built** 2026-10-26 (proposed 2026-10-24) · Decision: [0051](../decisions/0051-platform-apps.md) (accepted) · Workstream **K0 to K9**, one pull request per milestone

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

They share five things the platform does not do yet, and those come first (K1):

- **Taskiem's own app at each provider.** A business connects with OAuth through Taskiem's registered app instead of making its own. Slack, Meta and Intuit require this for one-click connecting; Zoho and Mailchimp offer it.
- **Webhooks sent to the app, not to the business.** Slack, Meta and Intuit send every connected business's events to the one URL on Taskiem's app. Today every hooks URL is a tenant's own (`/hooks/{tenant}/...`). [Decision 0051](../decisions/0051-platform-apps.md) adds a platform URL per provider and routes each verified event to the connections that hold its account (a workspace, a Page, a company).
- **Refresh tokens that change.** QuickBooks issues a new refresh token on each refresh and revokes the chain if an old one is used again; Zoho's access tokens last an hour. Connectors refresh in the process today (Google does), but cannot store what the provider hands back. K1 lets them, under a lock per connection.
- **Accounts in several places.** Zoho keeps each user's data in one of several data centres, and Mailchimp in a server prefix. The token answer says which, and the connection keeps it.
- **Books templates written once.** Payment templates already run on any rail through capabilities ([0044](../decisions/0044-rails-and-products.md)). Books capabilities (§4.7) do the same for Zoho Books and QuickBooks, so "copy an invoice to the books" is one template, not two.

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
| Callable templates and workflows; subflows, another workflow as a child run | [0047](../decisions/0047-composable-templates.md), [0048](../decisions/0048-callable-workflows.md), [0049](../decisions/0049-subflows.md) |
| AI steps: a model asked at run time, the run's personal data masked by default, an owner's switch | [0050](../decisions/0050-ai-steps.md) |
| Capabilities: templates written once for any rail (`pay_bank`, `collect_transfer`…), but only a `kind: rail` declares them | [0044](../decisions/0044-rails-and-products.md), [connector/v1](../contracts/connector-v1.md) rule 16 |
| Approvals decided by buttons on a member's linked WhatsApp or Telegram (signed decision tokens, step-up handed to the web app), from Home on a phone, and school approvals the same way; "First payment to this account" on an approval paying a new bank account | [Governance](../governance.md), [money log](../money-log.md#accounts-paid) |
| Slack alert channels, by a pasted incoming-webhook URL | [Alerts](../alerts.md) |
| The schools daily close and the money log's matched payments: what the books should receive | [Schools](schools.md) B9, [money log](../money-log.md) |
| Remita, the certified provider Zoho Books' Nigeria edition uses for NRS e-invoicing | [Remita](../integrations/remita.md) |

**Missing:**
- app-level webhooks;
- storing rotated refresh tokens;
- per-account API hosts;
- capabilities for anything but rails, so a books template would be written once for Zoho Books and again for QuickBooks;
- connectors for Meta messaging, Zoho, QuickBooks, Resend, Brevo and Mailchimp;
- templates for any of them.

## 4. Platform pieces

### 4.1 Platform apps and connecting

Each provider gets one registered app, its credentials in the environment as 0034 already does: `TASKIEM_OAUTH_SLACK_*`, `TASKIEM_OAUTH_META_*`, `TASKIEM_OAUTH_QUICKBOOKS_*`, `TASKIEM_OAUTH_ZOHO_*`, `TASKIEM_OAUTH_MAILCHIMP_*`. **One registration may serve several connectors**: the Zoho client serves Books, Mail, CRM and Inventory, and the Meta app serves Instagram and Messenger. So a manifest's `oauth2` names its client (`client: zoho`) instead of always its own id. When an app's variables are absent, its **Connect** button is hidden and the connector still takes pasted credentials where it has them.

Connecting asks only for the scopes the connector needs. A business that connects Zoho Books and later Zoho Mail grants Mail separately, so revoking one leaves the other.

### 4.2 App-level webhooks (decision 0051)

- **A platform URL per app-delivered trigger**: `POST /hooks/apps/{connector}/{trigger}` on the edge (Slack's app takes two: events and interactivity), and `GET` for handshakes (Meta's `hub.challenge`; Slack's `url_verification` comes by POST).
- **Verified with the platform app's secret** from the environment:
  - Slack's signing secret, scheme `slack_v0`;
  - Meta's app secret, `X-Hub-Signature-256`, as for WhatsApp;
  - Intuit's verifier token, `intuit-signature`, base64 HMAC-SHA256 of the body.
- **Routed by account key.** The manifest's trigger declares the key's expression (`=body.team_id`, `=item.id` for a Meta entry, `=item.realmId`). The event goes, through one audited function as in 0031, into every connection made through the platform app whose recorded account key matches, in any tenant, as if it had arrived at that tenant's own URL: the same dedup, correlation, subscribers and limits.
- **An event for an account no connection holds** is acknowledged, counted and dropped, never stored. Providers retry what is not acknowledged, and an uninstalled account would otherwise be retried for days.
- **Meta delivers several entries in one request**, already handled by the trigger's `split`.
- **Own apps keep their per-tenant URL** (`/hooks/{tenant}/connectors/...`), as today.

### 4.3 Refreshing tokens

A connector whose provider rotates refresh tokens refreshes through `req.RotateCredentials` (decision 0051 point 6, as built). Taskiem:

- **Refreshes once, and stores first**: the vault locks the connection's row, reads its credentials as stored now, and lets the connector refresh only if they are still stale; what it returns is stored before the lock goes. Ten workers that find the token expired at once spend one refresh token, and a worker that waited uses the new one.
- **Never logs them**, and records the write in the audit log as `connection.refresh` with no values; the worker scrubs the new secret values from step records.
- **Marks the connection expired** (the existing `ErrCredentialsRejected` path) when the provider says the refresh token is invalid, with the plain reason: "QuickBooks asks you to connect again".

The access token and its expiry are kept with the credentials, so the next step uses it without refreshing, whichever worker runs it.

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

### 4.7 Books capabilities

Zoho Books and QuickBooks (and Xero and Sage later) do the same few things. As payment rails do for paying, a books connector says what it can do in Taskiem's words, and a template written once runs on whichever books the business connected ([decision 0051](../decisions/0051-platform-apps.md) point 8; connector/v1 rule 16 extended beyond `kind: rail`):

| Capability | Input | Never twice by |
| --- | --- | --- |
| `books_customer` | Name, email, phone; answers the books' own id | Found by email or phone before creating |
| `books_invoice` | Taskiem's invoice number, customer, lines, VAT, due date | The invoice number as the books' reference (`reference_number`, `DocNumber`) |
| `books_payment` | Invoice, amount, date, the money log entry's reference | That reference |
| `books_sale` | A day's takings by method (a sales receipt) | The day and the source as the reference |
| `books_expense`, `books_bill` | Supplier, amount, account, date, VAT | The supplier's bill number |
| `books_accounts`, `books_items`, `books_taxes` | Pickers | Reads |

The normalised events `books.invoice.created`, `books.invoice.paid` and `books.payment.recorded` start a `capability_event` trigger on either books. A books mapping needs only the expressions its rail mappings already use; what is new is that a `kind: product` may declare the books capabilities, and only those.

## 5. Slack, finished

**Connecting.** **Add to Slack** through Taskiem's Slack app (OAuth v2, `oauth.v2.access`):
- It asks for the bot scopes the actions use (the table in [Slack](../integrations/slack.md)).
- It keeps the bot token, the team id (the account key) and the team's name.
- The pasted-token connection stays, as an own app.
- Slack's app must have public distribution turned on to be installed outside Taskiem's own workspace. A Marketplace listing is optional and comes later.

**Events and interactivity** for platform connections come to `/hooks/apps/slack@1/events` (and `/interactions`) and are routed by `team_id`. Slack sends `url_verification` once for the app; the existing handshake answers it. `app_uninstalled` and `tokens_revoked` mark the workspace's connections expired.

**Actions added:**

| Action | Method | For |
| --- | --- | --- |
| `list_users` | `users.list` | A member picker; who to mention |
| `open_conversation` | `conversations.open` | A direct message to one person found by email |
| `schedule_message` | `chat.scheduleMessage` | Reminders at a time |

**Approvals in Slack: a third chat channel.** WhatsApp and Telegram already decide approvals by buttons on a member's linked chat. Slack joins them on the same machinery rather than as a workflow waiting on a button:
- **Linking:** a member links their Slack user from their profile, found by their email (`users.lookupByEmail`) and confirmed by them in Slack. An unlinked Slack user's click decides nothing.
- **The request:** the approval's heading, amount, payee and expiry in the member's language, with "First payment to this account" when it applies. Sent as a direct message to each approver who linked Slack, besides their other channels.
- **The buttons:** they carry the signed decision token chat buttons carry, checked against the approver's role as the web inbox is. An approval that needs a passkey or code hands off to the web app, as WhatsApp's step-up does. Once it is decided anywhere, the Slack message is updated to say by whom.
- **School approvals** (cash postings and discounts) come the same way to holders of `school.approve`.

**Alerts:** an alert channel can be a channel of a Slack connection, picked rather than pasted. The incoming-webhook URL stays for businesses that don't connect Slack. A connection's bot token is in the vault like the URL, so the BYOK note on Slack channels holds for both.

**Templates** (all post to a channel the owner picks):
- Failed payouts and returned salaries.
- A daily money summary from the money log, and the schools daily close where the area is on.
- New storefront orders and paid invoices.

**Done when:**
- a business adds Slack with one click;
- an approval is decided by a linked member's button in Slack, refused for an unlinked user, and handed to the web app when it needs a passkey;
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

**Triggers** (app-level, `/hooks/apps/messenger@1/messages` and `/hooks/apps/instagram@1/messages`, routed by the entry's Page or Instagram account id):

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

**Personal data.** The `messages` trigger's schema marks the sender's id, name and the message text `x-pii`, so they are sealed in history. An `ai` step that reads a DM to draft a reply or pull out an order (decision 0050) gets them as placeholders and puts the name back in its answer. A template that needs the model to read the text as it is (an address written in words) lists it in `personal_data`, which is audited at publish. Without the owner's AI switch or the plan's `ai` feature, the auto-reply templates offer fixed replies instead.

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
| `list_organizations` | The connection test and the picker; every action below also maps to a books capability (§4.7) |
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
| `get_company` | The connection test (CompanyInfo); every action below also maps to a books capability (§4.7) |
| `find_customer`, `create_customer` | By email or display name first |
| `create_invoice`, `get_invoice`, `query_invoices` | `DocNumber` from Taskiem's invoice number. Creates send Intuit's `requestid`, so a repeated request is answered once (K0 confirms the window); also reconcilable by `DocNumber` |
| `create_payment` | A received payment applied to invoices, with the money log reference in its memo |
| `create_sales_receipt` | A day's POS or storefront takings |
| `create_purchase`, `create_bill` | Expenses and supplier bills |
| `list_items`, `list_accounts`, `list_tax_codes` | Pickers |

**Triggers:**
- **Where they arrive:** QuickBooks sends change notifications to the one webhook URL of Taskiem's Intuit app. They come to `/hooks/apps/quickbooks@1/changes`, are verified with the verifier token, and are routed by `realmId`.
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
| Slack | Money alerts to a channel; daily money summary and the schools daily close; new orders and paid invoices (approvals are the chat channel of §5, not a template) |
| Instagram, Messenger | Answer DMs outside working hours (an `ai` step drafting from the business's FAQ table, or fixed replies); a DM order becomes a payment link; thank the customer when paid; assign new conversations |
| Books, on either (§4.7) | **Copy an invoice to the books**, a callable template (0047) that finds or creates the customer and records the invoice, used by the others; record matched payments from the money log; a day's POS and storefront takings, or a school's daily close, as a sales receipt; supplier bills from email |
| Zoho Mail | Invoices and receipts from the business's own mailbox; supplier bills from mail into the books (an `ai` step reads the bill into the `books_bill` input; paying it later is an ordinary approval, with the first-payment warning for a new supplier account) |
| Zoho CRM | New customers and storefront orders as contacts and deals |
| Resend | Receipts and invoices by email |
| Brevo, Mailchimp | Opted-in customers into a list; win-back campaigns to customers who have not bought in 60 days |

**Template step or subflow.** Copying to the books is a template step (0047): it is short, and its writes belong to the run that made the invoice. A long job that runs on its own, such as a monthly catch-up copying every invoice the books are missing, is a workflow the others start as a subflow (0049), so its history and retries stay its own.

The AI builder learns each connector from its manifest as it does today. The eval suite gains cases per connector, and rules:
- a reply after the window is never planned;
- marketing only to opted-in contacts;
- invoices copied once, with their reference;
- books work through the books capabilities, not one provider's actions, unless the business asks for that provider's own feature.

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

- **App-level webhooks** (§4.2, decision 0051): `/hooks/apps/{connector}/{trigger}`, verification by the platform secret, routing by account key through one function that alone crosses tenants, the counted drop of unknown accounts, handshakes, uninstalls.
- **Clients shared by several connectors** (§4.1).
- **Refreshed credentials**, refreshed and stored under a per-connection lock (§4.3).
- **Per-account API hosts** (§4.4), with the egress allow-list following them.
- **The `svix` verification scheme.**
- **Remote registration's renewal** for subscriptions that expire.
- **Books capabilities** (§4.7): the schema, rule 16 for `kind: product`, the books events. The first mappings come with K3 and K4.

**Done when:**
- two tenants' events for two accounts at one provider each reach only their own connection;
- an unknown account's event is dropped and counted;
- ten workers refreshing one connection's token at once spend one refresh;
- a rotated token survives a restart.

**Built** (K1, 26 October 2026): one commit per piece on the K1 branch.
- **Svix** (`svix` scheme), tested on Svix's published example and its refusals.
- **Rotating refresh tokens:** `connector.Request.Rotate`/`RotateCredentials`, `secrets.Vault.RotateCredentials` (row lock, re-read, store, `connection.refresh`), wired into every connector call the worker makes; ten concurrent workers spend one refresh token, and the rotated tokens survive a fresh read.
- **Platform apps** (connector/v1 rule 18; migration 00224): `auth.oauth2.client`, `account_field`, `fields_from_callback`; a connection made by OAuth records its account (`connection_app_accounts`); `delivery: app` triggers at `/hooks/apps/{connector}/{trigger}` verified with `TASKIEM_OAUTH_<CLIENT>_WEBHOOK_SECRET`, routed by `account_key` through `taskiem_app_route_event` into every active connection holding the account (two businesses each get only their own workspace's events; a workspace connected twice reaches both); unknown accounts answered 200 and counted (`app_event_drops`); `revokes` expires the account's connections; GET checks compare `_VERIFY_TOKEN`. The per-tenant, partner and app paths share the delivery's item evaluation and the target tenant's gate.
- **Accounts on one server** (rule 19): `connection_hosts`; OAuth token and revoke URLs from `${connection.<field>}`, refused before the code exchange when the callback names a server outside the list; egress drops connection-named hosts outside it.
- **Renewal** (migration 00225): `RemoteState.ExpiresAt`; due a day before expiry (halfway for under two days), applied again with `Update`; a health check reading no expiry keeps the last.
- **Books capabilities** (decision 0051 point 8): nine capabilities and three events of `kind: product` in `capabilities-v1.json`; rule 16 checks kinds both ways; wd/v1, the SDK helpers and the catalogue's words take them; books events never reach the money log.

Differences from the plan: the app URL is per trigger (`/hooks/apps/{connector}/{trigger}`), not per provider, since one app takes several (Slack's events and interactivity); refreshed credentials are refreshed under the lock rather than returned with the response, which would come too late to stop a second worker; the in-memory token cache is not needed, since the access token is stored with the credentials. Mailchimp's server prefix (a metadata call after connecting) comes with K8, and connector categories for accounting (QuickBooks, Zoho Books) with K3 (added there).

### K2: Slack (M; needs K1)

§5: **Add to Slack**, platform routing, uninstall handling, the three actions, Slack as an approvals chat channel with member linking, alert channels from the connection, the templates.

**Built** (K2, 26 October 2026), on the K1 pull request.
- **Add to Slack:** `slack@1` 1.1.0 connects through Taskiem's Slack app (OAuth v2, `client: slack`, comma-separated scopes, `team.id` from the token answer as the account); `events` and `interactions` are `delivery: both` (an own app's URL, or the app's for every workspace, routed by `team_id`); `app_uninstalled` and a revoked bot token expire the workspace's connections. Generic: `auth.oauth2.scope_separator`, dotted `fields_from_token`, a 200 `{ok: false}` exchange reported as refused, `delivery: both`.
- **Actions:** `list_users`, `open_conversation`, `schedule_message`.
- **Approvals in Slack** (migration 00226): members link their Slack user in a business (`/v1/me/slack`, the passkey-strength proof, found by email, confirmed by a button in Slack); `slackChannel` is the third `chatChannel`, sending direct messages with the business's bot and the signed decision tokens as buttons; `/channels/slack/interactions` decides Taskiem's buttons through the shared path and passes every other click to the trigger; a decided message loses its buttons; school approvals come the same way.
- **Alerts:** a Slack alert channel can be a channel of the connection, posted by its bot.
- **Templates:** `slack-payment-alerts`, `slack-money-received`, `slack-paid-invoices`, `slack-school-daily-close`; the `slack_channel` picker (`GET /v1/slack/channels`); linking Slack on the Account page.

Differences from §5: the daily money summary in Slack waits for a money-summary action (the daily chat summary already goes to WhatsApp and Telegram), and new storefront orders stay with `storefront-order` (a storefront form takes one workflow). The "First payment to this account" line is not in chat requests yet, on any channel; a request decided elsewhere is not updated in Slack (its buttons answer that it was already decided).

### K3: QuickBooks Online (L; needs K1, K0's sandbox)

§8, on Intuit's sandbox companies: the connector, its books capability mappings, and the books templates of §10 written against the capabilities. K4 then only adds Zoho Books' mappings, and the same templates run on it.

**Built** (K3, 26 October 2026), on the K1 pull request. Fixtures are shaped from Intuit's published examples; nothing has run against a sandbox company yet.
- **Connecting:** `quickbooks@1` (`kind: product`, the new `accounting` category) through Taskiem's Intuit app, `realmId` from the callback as the account. Live and test are hosts with their own keys (connector/v1 rule 20; migration 00227): the person chooses before linking, the request keeps the mode, and the code is exchanged with that mode's app (`quickbooks`, `quickbooks_sandbox`). Generic: `client_auth: basic`, `revoke_encoding: json`, `oauth_modes` in the connector list, a mode chooser by the Connect button, and app deliveries verified with any of the connector's apps.
- **Tokens:** refreshed under the connection's lock with `RotateCredentials`, the access token kept with its expiry; a refused access token is refreshed once, a refused refresh token expires the connection; eight concurrent workers refresh once.
- **Actions:** the company, customers and suppliers (found before added), invoices (found by number), payments (found by reference among the customer's payments that day), sales receipts, purchases (VAT as its own line), bills (found by number for the supplier), the three pickers, any record, and change data capture. Every write sends `requestid`; numbers over 21 characters are shortened the same way every time.
- **Books:** all nine capabilities mapped; `books.invoice.created` and `books.payment.recorded` from the `changes` trigger (CloudEvents, split per event, routed by `intuitaccountid`, nothing read before the three-second answer). The conformance kit's event check now follows a trigger's `split`.
- **Templates:** `books-copy-invoice` (callable), `books-record-invoice-payments` (on the invoice trigger's `payment` event), `books-school-daily-takings`. The rail coverage tests count products that declare capabilities; the three run on QuickBooks only until K4.

Differences from §8: there is no enricher, so a run reads the record it was told of (`get_entity`): an enricher would read before the three-second answer. QuickBooks gives no `books.invoice.paid` (a paid invoice is only an `invoice.updated`). Catching up after an outage is the `changes_since` action, not automatic yet. An invoice is copied with its total as one line, since `invoices@1` does not give its lines.

### K4: Zoho platform and Zoho Books (L; needs K1)

The Zoho client with data centres (§7, §4.4), `zoho_books@1` (§7.1) with its books capability mappings, the organisation picker, and K3's books templates passing on Zoho Books in both editions.

**Built** (K4, 26 October 2026), on the K1 pull request. Fixtures are shaped from Zoho's published examples; nothing has run against a Zoho organisation yet.
- **Connecting:** `zoho_books@1` (`kind: product`, `accounting`) through the shared Zoho client (`client: zoho`): the accounts server from the callback and the API domain from the token, both held to Zoho's data centres (rule 19); `access_type=offline` and `prompt=consent`; comma-joined scopes.
- **Tokens:** the access token kept with the credentials and refreshed under the connection's lock, since Zoho issues ten every ten minutes; Zoho's 200 `invalid_code` expires the connection; six concurrent workers get one token.
- **The organisation:** the only one the user reaches, or Zoho's default, chosen on the first call and kept with the credentials.
- **Actions:** contacts (customers and suppliers, found by email, phone, then name), invoices (found by `reference_number`, Taskiem's number), payments (found by reference), sales receipts (by number, the walk-in customer's without one), expenses (VAT as its own line), bills (by number for the supplier), the three pickers, and `changes_since`. Books has no idempotency key: each write finds by the business's own number first.
- **Books:** all nine capabilities and all three events, `books.invoice.paid` included, from a polled `changes` trigger (every 15 minutes while a workflow listens).
- **Templates:** K3's three books templates run on both books unchanged; the rail coverage tests now find two connectors for each.
- **On the way:** the money log's poller no longer polls triggers whose events are not rail events (it would have spent a Zoho organisation's daily quota); QuickBooks uses the shared money package.

Differences from §7.1: there is no organisation picker in the web app; the connection takes the only organisation, or the default, and says which. Zoho Books' webhooks are not used yet: its API does not document how they are signed (to confirm), so changes are polled.

### K5: Instagram and Facebook messages (L; needs K1, Meta's review)

§6. It can be built and tested on Meta's test Page and accounts as soon as K1 lands. It goes to businesses only when Meta grants Advanced Access.

**Built** (K5, 26 October 2026), on the K1 pull request. Fixtures are shaped from Meta's published examples; nothing has run against a Meta app yet.
- **Connecting:** `messenger@1` and `instagram@1` (`connectors/meta`) on Facebook Login through one Meta app (`client: meta`). Connector/v1 rule 21: a connector's `Link` completes the grant before the connection is saved. For Meta it trades the user token for a long-lived one, takes the one Page's own token (which does not expire), records the Page (or its Instagram account) as the account, and subscribes the Page to the app. Several Pages granted is refused in the person's words. The user token is not kept. Generic: `token_method: get`, `linked_fields`, a linked connection's lifetime taken from what Link made.
- **Actions:** `send_message` (text or an attachment, quick replies, `HUMAN_AGENT`), with the window read from the conversation before sending: outside it, or to someone who never wrote, it fails before sending. Also `send_typing`, `mark_seen`, `get_profile` (sealed), `get_conversation`, and the connection checks.
- **Triggers:** `messages` at `/hooks/apps/messenger@1/messages` and `/hooks/apps/instagram@1/messages`, verified with the app secret, the GET challenge answered, batches split per event and routed by the entry's Page or account, correlated on the person. Generic: a trigger's `pii` seals the sender and the text in run history (a run's trigger), and its `example` feeds dry runs.
- **Templates:** an away reply outside opening hours and a new-message alert, for each.

Differences from §6: the daily re-check of each Page's subscription is not built (connecting again subscribes again); comments come later; there is no "order in a DM becomes a payment link" template yet, since Meta gives no email, which most rails' payment links need. A signal waking a run that waits on the person carries the message as delivered (as WhatsApp's do); only a started run's trigger is sealed.

### K6: Zoho Mail and Zoho CRM (M; needs K4)

§7.2 and §7.3, with the watch renewal.

**Built** (K6, 26 October 2026), on the K1 pull request. Fixtures are shaped from Zoho's published examples; nothing has run against a Zoho account yet.
- **The Zoho client, shared:** `connectors/internal/zoho` keeps the tokens (refreshed under the connection's lock at the account's accounts server) and the data centre's servers for Books, Mail and CRM; Books now uses it.
- **`zoho_mail@1`:** connecting finds the user's own mailbox at their data centre's Mail server before saving (rule 21's Link) and keeps the granted token. Actions: send and reply (unsafe writes), list, search, read (as HTML and text), mark, move and label, folders and labels. `new_mail` is polled every 5 minutes, its subject, addresses and summary sealed.
- **`zoho_crm@1`:** upsert by the module's unique fields (a retry updates), update, read, search, notes found before added, modules. `record_changes` is CRM's notification watch: registered remotely with a token of Taskiem's, verified by the new `body_token` scheme, asked for six days and renewed before it lapses, split into one event per record. A watch found after a crash is stopped and made again.
- **On the way:** a registrar's calls and a polled trigger's read get the vault's rotation (`RemoteCall.Rotate`, the poller's requests). Without it, Zoho Books' polling would have failed once its access token expired.
- **Templates:** paid invoices as Zoho CRM contacts and won deals; a receipt from the business's Zoho mailbox.

Differences from §7.2 and §7.3: the scopes are one set (reading and sending), not chosen by what a workflow does; the mailbox is the user's first Zoho mailbox, with no picker; supplier bills read from mail into the books wait for an `ai` step template; storefront orders as deals wait, since a storefront form takes one workflow.

### K7: Resend and Brevo (M; needs K1 for `svix`)

§9: both connectors, their webhooks, the consent rule, the receipt and list templates.

**Built** (K7, 26 October 2026), on the K1 pull request. Fixtures are shaped from Resend's and Brevo's published examples; nothing has run against either yet.
- **`resend@1`:** `send_email` carries the step's key as Resend's `Idempotency-Key` (one send per key for 24 hours; a reused key with another body is fatal), refused before calling from a domain not verified in Resend; `get_email`, `list_domains`. `email_events` is registered remotely: Taskiem creates the webhook, keeps the Svix secret Resend returns (`svix` now allowed for remote triggers), turns a paused webhook back on, and makes again one found after a crash.
- **`brevo@1`:** email (from an authenticated domain only) and SMS as unsafe writes; contacts upserted by email; lists; campaigns sent once. Consent: a list addition, or a marketing text, needs `consent.opted_in: true`, which the manifest allows only as true and the connector checks again. `transactional_events` and `marketing_events` are registered remotely with a bearer token of Taskiem's; Brevo's snake-case event names are mapped to the webhook's.
- **Templates:** a receipt through Resend; opted-in customers onto a Brevo list (only those whose row says so); bounces, complaints and unsubscribes back onto the customers table.
- **On the way:** a test compiles every built-in trigger's expressions, since a manifest does not check them when it loads (it caught one here).

Differences from §9: there is no Brevo list picker yet (the list is its number); Resend's audiences and broadcasts come later.

### K8: Mailchimp (S; needs K1)

§9, with OAuth and the server prefix.

**Built** (K8, 26 October 2026), on the K1 pull request. Fixtures are shaped from Mailchimp's published examples; nothing has run against Mailchimp yet.
- **Connecting:** `mailchimp@1` through Taskiem's Mailchimp app (no scopes: rule 21 now allows none), the server prefix read from Mailchimp's metadata before the connection is saved (`Link`); or a pasted API key, its server its ending. Egress allows `*.api.mailchimp.com`.
- **Actions:** members upserted by the MD5 of the address, only with consent and never re-subscribing someone who unsubscribed; tags; campaigns made once by title and sent once; audiences.
- **Triggers:** `audience_events`, a signed webhook Taskiem creates on the audience (`hmac_sha256_timestamped` with `t_v1`, Mailchimp's `X-Mailchimp-Signature`); Mailchimp's plain GET check is answered by the new `open` handshake.
- **Pickers:** a connector's `pickers` and `picker: choice` with `GET /v1/connections/choices` (rule 21): the audience is chosen by name. Brevo's lists, Zoho's organisations and QuickBooks' accounts can use the same.
- **Templates:** opted-in customers onto a Mailchimp audience; unsubscribes and cleaned addresses back onto the customers table.

Differences from §9: the win-back campaign (customers who have not bought in 60 days) waits for a way to read purchase dates from the money log in a template.

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
| Rules | A Meta reply after the window refused before sending; a marketing add without consent refused; a DM's sender and text reach an `ai` step only as placeholders unless let through |
| Capabilities | Every books template run on both Zoho Books and QuickBooks fakes with the same expected writes |
| Approvals | A Slack button from a linked approver decides once; an unlinked user's, a forged token's and a second click's decide nothing; a step-up approval hands off |
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
| Zoho Books and QuickBooks differ more than the capabilities allow (tax codes, multicurrency) | Mappings carry the differences as rails' do; a field one books lacks is in `unsupported`, and a template that needs it says so when the business picks that books |
| Should Taskiem write to the books automatically, or only when a person confirms? | Proposed: templates copy invoices and matched payments automatically (they are records of what happened), and never edit or delete what the accountant wrote |

## 16. Out of scope

- Mailchimp Transactional (Mandrill), a separate paid product; Resend and Brevo cover transactional email.
- Instagram and Facebook posting and ads; comments come after messages.
- Zoho Desk, Sign, People and Campaigns until a template needs them (§7.4).
- Payroll inside Zoho or QuickBooks: payroll is Run payroll or a payroll product ([0044](../decisions/0044-rails-and-products.md)).
- Submitting e-invoices to the NRS from Taskiem: Zoho Books does it through Remita itself.
- Xero and Sage: the same accounting templates, after QuickBooks and Zoho Books.
