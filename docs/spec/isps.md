# Internet providers: the ISP area, PHP Radius in the customer's chat, and the provider's staff

Status: **Draft** 2026-11-18, for review, not scheduled · Decision: [0070](../decisions/0070-field-work-and-isps.md) (proposed) · Workstream **I0 to I6**, one pull request per milestone · Written with the founder · Builds on: [Field work](field-work.md), [business areas §9](business-areas.md#9-a-service-business-as-the-test-an-isp-or-cloud-provider) (the subscription recipes, built), PR [#198](https://github.com/ISRAEL-DUFF/taskiem/pull/198) (Taskiem for everyone: customers chatting with businesses) · Provider reference: [PHP Radius client API](../integrations/phpradius-reference/README.md) · Worked examples: [field work examples](field-work-examples.md) · Related: PR [#154](https://github.com/ISRAEL-DUFF/taskiem/pull/154) (reaching private networks, decision 0013)

An internet provider runs on Taskiem as one business, with two kinds of people around it:

- **its subscribers** see their plan and data, recharge, redeem a voucher, raise a complaint and book a technician, in their conversation with the provider in the Taskiem app, instead of logging in to the provider's own portal;
- **its staff** (customer care, cashiers, admins, and technicians in the field) run the provider's work in Taskiem: jobs, the field, money, and its customers' conversations.

Many Nigerian providers bill and run their network on **PHP Radius**, which they do not host: its vendor gives each provider an admin dashboard and a **client API** for its subscribers. The admin dashboard stays where it is. Taskiem becomes the subscriber's way in, through that client API, and the place everything else about the provider's business happens.

Contents:

1. [Summary](#1-summary)
2. [Words used here](#2-words-used-here)
3. [Where we are](#3-where-we-are)
4. [The ISP area](#4-the-isp-area)
5. [PHP Radius](#5-php-radius)
6. [A subscriber's conversation](#6-a-subscribers-conversation)
7. [Workflows for providers](#7-workflows-for-providers)
8. [Staff and roles](#8-staff-and-roles)
9. [Signing up as a provider](#9-signing-up-as-a-provider)
10. [Data and server changes](#10-data-and-server-changes)
11. [Milestones, with Field work](#11-milestones-with-field-work)
12. [Testing](#12-testing)
13. [Risks and open questions](#13-risks-and-open-questions)
14. [Out of scope](#14-out-of-scope)

---

## 1. Summary

| Need | In Taskiem | Kind |
| --- | --- | --- |
| A provider turns it on | The **ISP** area: turns on Field work and Money; brings provider job types, roles and templates (§4) | Area |
| Talk to PHP Radius | The `phpradius` connector: one connection per provider, its portal address (§5) | Connector |
| A subscriber is recognised without a password | Signed in to PHP Radius with the phone Taskiem has proved is theirs (§5.3) | Connector, platform |
| Plan, expiry, data, sessions, invoices, receipts | **My internet** in the subscriber's conversation (§6) | Chat |
| Recharge and pay | A plan picked, paid to the **provider's own** Paystack or Flutterwave, confirmed to PHP Radius (§6.2) | Chat, connector |
| Voucher | Typed or scanned, redeemed (§6.3) | Chat |
| Complaint | Opened in PHP Radius, and a repair job in Field work if the provider wants (§6.4) | Chat, workflow |
| A technician's visit | Field work's job card and **Book a visit** ([Field work §14](field-work.md#14-the-customers-side)) | Field work |
| Expiry reminders, outage notices, receipts | Templates for providers on PHP Radius (§7) | Templates |
| Staff | Customer care, cashier, admin and field agent roles (§8) | Roles |

The two choices that shape this plan are in decision [0070](../decisions/0070-field-work-and-isps.md): the ISP area **builds on Field work**, and the subscriber is signed in to PHP Radius **by the phone Taskiem has proved**, so Taskiem never asks for or keeps a PHP Radius password.

## 2. Words used here

| Word | Meaning |
| --- | --- |
| Provider | An internet service provider: the business. "ISP" in the code (`isp`) and in the area's name |
| Subscriber | A provider's customer who pays for internet. On Taskiem, a person in a conversation with the provider (PR #198), or a row of the `subscribers` table for a provider not on PHP Radius |
| PHP Radius | The vendor-hosted billing and RADIUS product many providers use. "The vendor" is its maker |
| Portal address | The provider's own address on PHP Radius (`https://portal.adanet.ng`), where its client API answers |
| Linked | A subscriber whose Taskiem conversation is joined to their PHP Radius account (§5.3) |
| Plan, top-up | What a subscriber buys: a plan for a period, or more data on the one they have |
| Recharge | Paying for a plan or top-up, which extends the account |
| Voucher | A code sold or handed out by the provider, worth a recharge |
| Complaint | A subscriber's report of a problem, kept in PHP Radius (its "SLA") |

## 3. Where we are

| Exists | Where |
| --- | --- |
| The subscription recipes (monthly invoices, renewal reminder, suspend on overdue with approval, reconnect on payment, outage notice), the `subscribers` table, the "I bill customers for a service" sign-up answer | [Business areas §9](business-areas.md#9-a-service-business-as-the-test-an-isp-or-cloud-provider) |
| Reaching a provider's system on a private address: an SSH tunnel through the provider's bastion | PR [#154](https://github.com/ISRAEL-DUFF/taskiem/pull/154), [decision 0013](../decisions/0013-private-network-access.md) |
| Customers chatting with a business in the Taskiem app and the web chat, with their phone proved; cards and tools in the conversation; the customer door | PR [#198](https://github.com/ISRAEL-DUFF/taskiem/pull/198) |
| Field work: jobs, field agents, the customer's job card | [Field work](field-work.md) (this plan's sibling) |
| Paystack and Flutterwave connectors | `connectors/paystack`, [Flutterwave](../integrations/flutterwave.md) |
| ISPOps: the founder's field-agent system, used by one provider | [Field work §20](field-work.md#20-what-comes-from-ispops) |
| PHP Radius's client API, as the vendor published it | [Reference](../integrations/phpradius-reference/README.md) |

**What the subscription recipes cannot do for a PHP Radius provider.** They assume the provider can switch a subscriber off and on, and holds a list of all its subscribers. PHP Radius's client API has neither: there is no admin API, and every call acts as one subscriber. Expiry and cut-off are PHP Radius's own (a plan expires, with grace, and a recharge extends it). So for a PHP Radius provider: suspend and reconnect are PHP Radius's business, not Taskiem's; renewal reminders reach the subscribers who have linked (§7); monthly invoices are PHP Radius's. The recipes stay for providers billing from a table.

## 4. The ISP area

**ISP** joins `engine/areas`: "Your subscribers' internet accounts in their chat with you, recharges, vouchers, complaints and visits." It depends on **Field work** and **Money**, as Schools depends on Money: turning ISP on turns them on.

Turning it on:

1. adds the provider's job types to `job_types`: **installation**, **relocation**, **fault repair**, **maintenance**, **survey**, **recovery** (collecting equipment), each with a template and checklist (the installation's: "Mount the antenna or run the fibre | photo", "Signal reading | note", "Router set up and customer connected", "Customer shown how to recharge in Taskiem");
2. adds the **Customer care** and **Cashier** custom roles (§8) where the plan has custom roles;
3. offers to **connect PHP Radius** (§5.1), or to keep subscribers in the `subscribers` table instead;
4. offers the templates of §7 on the area's starting page.

**Settings** (ISP > Settings):

| Setting | Default | What it does |
| --- | --- | --- |
| Billing system | PHP Radius, if connected; else the `subscribers` table | Which way §6's tools work |
| A complaint opens a repair job | On | A complaint from chat also makes a **fault repair** job, unassigned (§6.4) |
| Remind before expiry | 3 days and on the day | For linked subscribers (§7) |
| Gateways offered | The ones PHP Radius returns that Taskiem supports | §6.2 |
| Book a visit offers | Installation, relocation, fault repair | Field work's Book a visit, for subscribers |

## 5. PHP Radius

### 5.1 The connection

A built-in connector, `phpradius`, written from the vendor's published collection ([reference](../integrations/phpradius-reference/README.md); cited in the manifest's header under the [clean-room policy](../clean-room-policy.md)).

| Field | What it is |
| --- | --- |
| Portal address | The provider's PHP Radius address, `https://` only. The connector's only host (`egress_hosts: ["${connection.portal_url}"]`), any public host allowed; a private one is refused by the egress guard as always. PHP Radius is hosted by its vendor, so PR #154's tunnel is not needed |

There is no key: the client API has none. **Test** reads `GET /api/ispdetail` and shows the provider's name, logo and country as PHP Radius gives them, so the owner sees it is their own portal before saving. The answer's `phoneAsUsername` and the Quick Recharge way in (§5.3) are checked: without the phone way in, the connection is saved but §6 is off, with the reason.

### 5.2 The actions

Every action but `isp_details` and `sign_in` acts as **one linked subscriber**, with their token (§5.3), and refuses an action the subscriber's `abilities` do not allow.

| Action | Calls | Class | Notes |
| --- | --- | --- | --- |
| `isp_details` | `GET /api/ispdetail` | read | No subscriber |
| `sign_in` | `POST /api/loginPhone` | read | A token for a phone (§5.3). Repeating it only makes another token |
| `account` | `GET /api/dashboard/1`, `GET /api/userDetail` | read | Plan, expiry, grace, quota and fair-use, status, balance |
| `plans` | `GET /api/recharge/5` | read | Plans and top-ups, prices, tax, the provider's gateways |
| `usage` | `GET /api/session`, `PUT /api/traffic/{id}` | read | Sessions in a period; online or not |
| `invoices`, `receipts`, `ledger`, `recharges` | `GET /api/invoice`, `/receipt`, `/userledger`, `/rechargeList` | read | In a period |
| `start_payment` | `POST /api/paystack` or `/flutterwave` | idempotent_write | Makes PHP Radius's temporary invoice and returns the provider's public key and amount. A repeat makes a second open temporary invoice, which is harmless and never charged; to confirm with the vendor (§13) |
| `confirm_payment` | `PUT /api/paystack/1` or `/flutterwave/1` | idempotent_write | The gateway's whole answer, keyed by the gateway's reference. Whether PHP Radius refuses the same reference twice is to be confirmed (§13); until it is, a timeout parks the step for a person |
| `redeem_voucher` | `PUT /api/voucher/{code}` | idempotent_write | Keyed by the code; a voucher is used once by PHP Radius itself |
| `complaints` | `GET /api/sla` | read | |
| `open_complaint` | `POST /api/sla` | reconcilable_write | Reconciled by reading `complaints` for one with the same words since the call |
| `register` | `POST /api/userregister` | unsafe_write | A new subscriber from chat, where the provider allows it (`autoUserRegistration`); later (I6) |

Password calls (`login`, `password`, `forgotPassword`) are **not** offered: Taskiem never handles a PHP Radius password. The eighteen other gateways in the collection are left out until a provider uses one; Paystack and Flutterwave are what Nigerian providers use.

### 5.3 Signing a subscriber in

PHP Radius's Quick Recharge way in, `POST /api/loginPhone` with `loginType: phone`, gives a subscriber's token for **their phone number alone**. Anyone who can call it with a number gets that account: so Taskiem calls it **only with a number Taskiem has proved belongs to the person asking**.

1. A person opens their conversation with the provider (PR #198). Their phone is proved: by their sign-in, or by their WhatsApp line joined to them (stage 4).
2. The first time they use a tool of §6, Taskiem calls `sign_in` with that phone. PHP Radius answers with the account's username, plan and abilities, and a token.
3. Taskiem shows "Is this your account? **Ada Obi**, plan **Home 20 Mbps**", and on **Yes** the conversation is **linked**: the token is kept sealed, for this person and this provider only; the username and abilities beside it.
4. When PHP Radius answers `401`, Taskiem signs in again with the same proved phone, silently; if PHP Radius no longer knows the phone, the link ends and the person is told.
5. **Another number** (the account is under an old number, or a family member's): the person proves that number with a code Taskiem sends to it, then step 2 runs with it. A number is never used unproved.
6. **No account** for the number: "Ada Networks has no account for 0803 ••• 4567. Ask them to add this number, or sign up" (sign up when the provider allows it, I6).

Unlinking is the person's (**Forget my internet account**) or ends with the person's account, a change of phone, or the provider turning ISP off; the token is deleted.

**What the provider's staff see.** Staff do not act as a subscriber: the token is used for the subscriber's own requests and the provider's templates for that subscriber (§7), never opened by a member. Staff see what the subscriber's tools put in the conversation (cards), and use PHP Radius's admin dashboard for everything else.

## 6. A subscriber's conversation

The tools sit in the provider's conversation in the app and the web chat, beside Field work's **Book a visit**, each leading to cards both sides see.

### 6.1 My internet

A card: plan, valid until (and grace), data used against the plan's quota, online now or not, balance due; buttons **Recharge**, **Voucher**, **Report a problem**, **Usage**, **Invoices and receipts**. It refreshes when opened.

### 6.2 Recharging

1. **Recharge** lists the plans and top-ups `plans` returns, with prices and tax as PHP Radius shows them.
2. The subscriber picks one; `start_payment` gives the provider's public key, the amount and a reference.
3. A **Pay** card opens the gateway's own checkout (Paystack or Flutterwave) with the **provider's** public key. The money goes to the provider's gateway account; it never passes through Taskiem and is not Taskiem's Money.
4. The gateway's answer comes back to Taskiem's server (not trusted from the phone alone: the server reads the transaction from the gateway's public verification where the gateway offers one) and `confirm_payment` hands it to PHP Radius.
5. A **Recharged** card: the plan and its new end, and PHP Radius's receipt.

A payment the gateway took but PHP Radius did not confirm (a timeout, PHP Radius down) is **never lost and never retried blindly**: it parks, the subscriber's card says "Paid. Waiting for Ada Networks to confirm", and the provider's staff are told with the reference to settle in PHP Radius.

### 6.3 Vouchers

**Voucher**: typed, or scanned from the card with the app's camera; `redeem_voucher`; a **Recharged** card. Five wrong codes in an hour lock the tool for that person for an hour.

### 6.4 Complaints

**Report a problem**: a short list ("No internet", "Slow", "Keeps dropping", "Billing", "Other") and the subscriber's words. `open_complaint` records it in PHP Radius; the card shows its number and status, read again from `complaints` when opened. With the setting on, a **fault repair** job is made in Field work for the subscriber's saved address, and its job card follows in the same conversation when assigned.

### 6.5 Without PHP Radius

A provider billing from the `subscribers` table gets **My internet** from the row whose phone matches the person's proved phone (plan, price, renews on), and **Pay** against its invoices in Money. Recharge and vouchers need PHP Radius.

## 7. Workflows for providers

PHP Radius's client API cannot list a provider's subscribers, so a provider's templates work over **linked subscribers**: the people who have linked in Taskiem. That set grows as subscribers use the app; it is never all of them, and the templates say so.

| Template | Trigger | What it does |
| --- | --- | --- |
| `isp-expiry-reminder` | `schedule` (daily) | For each linked subscriber, `account`; when the plan ends within the setting, a card "Your plan ends Friday" with **Recharge** |
| `isp-recharged-receipt` | A recharge confirmed | The receipt in the conversation; tells the cashier role |
| `isp-complaint-to-job` | A complaint opened | The fault repair job (the setting, as a template the provider can change) |
| `isp-outage-notice` | `webhook` from monitoring, or **Send an outage notice** | A notice to linked subscribers, all or by PHP Radius zone (`zoneName`), and to the `subscribers` table's rows; replaces nothing, adds to `subscription-outage-notice` |
| `isp-installation-from-sign-up` | A subscriber registered (I6) | An installation job for their address |
| `job-*` | Field work's templates | As in [Field work §8](field-work.md#8-each-step-is-a-trigger), in the provider's words |

The subscription recipes of [business areas §9](business-areas.md#9-a-service-business-as-the-test-an-isp-or-cloud-provider) are offered only to a provider billing from the table. Suspend and reconnect stay available for a provider whose own system has an HTTPS address that can do it (reached through PR #154 when private).

## 8. Staff and roles

| ISPOps role | Here | Holds |
| --- | --- | --- |
| Super admin | Owner | Everything |
| Admin | Admin (built-in) | Everything but owner-only |
| Customer care | **Customer care** (custom, added by the ISP area) | Customers' conversations, `job.read`, `job.manage` (make and assign jobs), `invoice.read` |
| Cashier | **Cashier** (custom, added) | `invoice.read`, `invoice.manage`, the money log; the recharge and parked-payment alerts |
| Dispatcher | **Dispatcher** (Field work's custom role) | `job.read`, `job.manage` |
| Field agent | **Field agent** (Field work's built-in role) | `job.work` only |

All are changed under custom roles like any other.

## 9. Signing up as a provider

The sign-up question gains **"I run an internet service"**: kind `service`, with ISP, Field work and Money on, and the area's starting page next. A business that answered "I bill customers for a service" turns ISP on later under **What your business uses**. The starting page's checklist: connect PHP Radius (or choose the table), add staff and field agents, add job types' prices, put the chat link and QR code where subscribers see them (on the router, the receipt, the PHP Radius portal).

## 10. Data and server changes

| Kind | What |
| --- | --- |
| Area | `isp` in `engine/areas`, depending on `fieldwork` and `money`; door checks; settings in its module schema |
| Connector | `connectors/phpradius` (manifest, Go actions, recorded fixtures from the vendor's examples with the tokens scrubbed) |
| Records (module schema `isp`) | `isp_links` (person, provider, PHP Radius username, sealed token, abilities, linked and last used), `isp_payments` (start, gateway reference, confirm, state, for parked payments) |
| Chat | The provider's tools in the conversation and their cards; their words in chat |
| Contracts | No new trigger type: recharges and complaints reach workflows as `capability_event`s of the area (`isp.recharged`, `isp.complaint`); to settle in I3 against how `invoice` was added |
| Templates | §7, and the area's job types and checklists |
| Roles | Customer care, Cashier |
| Sign-up | The answer "I run an internet service" |

## 11. Milestones, with Field work

| # | Milestone | Size | After |
| --- | --- | --- | --- |
| I0 | The `phpradius` connector: connection, test, the read actions, fixtures; the reference | M | Can start now |
| I1 | The ISP area, roles, settings, job types and templates; sign-up answer | S | F0 |
| I2 | Linking a subscriber by proved phone; My internet; Usage, invoices and receipts | M | I0, PR #198 merged |
| I3 | Recharge and pay through the provider's gateway; parked payments; vouchers | L | I2 |
| I4 | Complaints, and a complaint to a repair job | S | I2, F5 |
| I5 | Templates: expiry reminder, receipt, outage notice; the guide | M | I3, F5 |
| I6 | Subscribers signing up from chat, with an installation job | M | I3, F6 |

**Order with Field work.** F0 to F4 first: that is what lets the first provider stop using ISPOps, and it needs nothing from PHP Radius. I0 runs alongside from the start. Then I1 to I3 and F5, F6 together, then the rest.

```
F0 ─ F1 ─ F2 ─ F3 ─ F4 ─ F5 ─ F6 ─ F7 ─ F8 ─ F9
I0 ─────────── I1 ─ I2 ─ I3 ─ I4 ─ I5 ─ I6
```

## 12. Testing

- **Connector:** each action against the vendor's recorded examples (status `201` on reads, `{"message"}` errors, `401` and `422`); refused for a host not https or private; classes as in §5.2; no action takes or returns a password.
- **Linking:** a proved phone links; an unproved number is never sent to `sign_in`; another number only after its code; `401` signs in again; unlinking deletes the token; a token is never readable by a member or the API.
- **Paying:** the provider's key is used, never Taskiem's; a gateway success with PHP Radius down parks and tells the cashier; the same reference confirmed twice lands once (once the vendor confirms it); the amount shown is PHP Radius's.
- **Abilities:** a provider that turned off vouchers shows no Voucher tool.
- **Area:** ISP off hides the tools, refuses the routes and keeps links; ISP on turns Field work and Money on.
- **With a real provider:** a test subscriber on the first provider's portal, with the vendor's and the provider's agreement, before I3 ships.

## 13. Risks and open questions

- **The phone way in is the vendor's design, and Taskiem leans on it.** Anyone who can call `loginPhone` with a number gets that subscriber's token, from any client. Taskiem's own use is safe (proved phones only), but the provider should know how its portal behaves, and the vendor may change it. Ask the vendor: is `loginPhone` meant for third-party clients, and can a provider restrict it?
- **The vendor's agreement.** Taskiem would be a client of PHP Radius used by many providers. Ask whether the vendor allows that, offers a partner key, or has a staging account; the demo host and its demo accounts are not used without the founder's go-ahead.
- **Unknowns the collection does not answer:** how long a token lasts; whether a gateway reference confirmed twice is refused; rate limits; whether every provider's portal is on its own domain or under the vendor's; which currencies Nigerian providers' portals use (the examples show rupees and rand).
- **Linked subscribers only.** Reminders and notices reach those who linked; a provider will want all. Only an admin API from the vendor (or the provider's own list in the `subscribers` table) answers that.
- **The provider's gateway keys.** PHP Radius hands out the provider's public key, which is meant to be public. Taskiem never needs the provider's secret key; if the server-side check of a payment (§6.2 step 4) needs one, it becomes a Paystack or Flutterwave connection the provider makes, and the plan changes.
- **Depends on PR #198** for subscribers being people with proved phones and conversations; I2 waits for it to merge.

## 14. Out of scope

- PHP Radius's admin side (adding subscribers, plans, NAS, suspending): the provider's dashboard does it, and there is no API for it.
- Hotspot and captive-portal logins.
- The eighteen gateways not used in Nigeria.
- Changing a subscriber's PHP Radius password from Taskiem.
- Billing systems other than PHP Radius and the `subscribers` table (Splynx, UISP, MikroTik's own): each is its own connector when a provider asks.
