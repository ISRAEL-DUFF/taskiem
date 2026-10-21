# 0039 — POS terminals: a connector for Moniepoint's terminal events, the record in data tables, templates that make their tables

Date: 2026-10-21 · Status: **Accepted** 2026-10-21 (built: `moniepoint_pos@1`, the `pos-*` templates, template tables)

## Context

Most Nigerian small businesses are paid through POS terminals, Moniepoint and OPay above all ([the problem and the plan](../strategy/pos-terminals.md)). Taskiem's `moniepoint@1` is Monnify, the online gateway, and `opay@1` is OPay Checkout: neither sees a terminal transaction, and neither provider lists past terminal transactions through an API. Moniepoint's POS API (channel.moniepoint.com, documented on TeamApt's Confluence space, read 21 October 2026) delivers each terminal transaction as a signed webhook event, for every terminal of the business, from a subscription the owner creates in Moniepoint Business. OPay's POS webhook exists but its signing and its scope are not public.

## Decision

1. **A separate connector, `moniepoint_pos@1`**, not more actions on `moniepoint@1`. A terminal business holds a Moniepoint Business account with API client credentials and a webhook secret; a Monnify merchant holds Monnify keys. One connection screen per account people actually have, each with its own guided steps and its own test call.
2. **Events, not polling.** The `transaction` trigger carries Moniepoint's ten event types; there is no list endpoint to poll, so the trigger declares no `poll` and the record starts when the subscription is created. History is a later import.
3. **The connector verifies its own deliveries** (`verify: {scheme: connector}`), as OPay's does: the signature is an HMAC-SHA256 over `<webhook id>__<timestamp>__<body>`, Base64, which no generic scheme describes (they sign the body alone, or a timestamp and the body). Deliveries are deduplicated on `eventId`, which retries repeat. No timestamp tolerance is enforced: the documentation states none, replay is already refused by the dedup, and a tolerance would only drop late retries. Revisit if Moniepoint documents one.
4. **Kobo in the connector, naira in the tables.** The connector speaks the provider's unit (integer kobo) like every payments connector. The templates write naira to the tables because owners read the tables; the conversion is in one JavaScript step the templates share.
5. **The record is the business's data tables**, not a Taskiem-owned store: `pos_sales` (one row per event), `pos_terminals` (serial to name), `pos_cashups` (one per check). Rows are inserted, never merged: a transaction Moniepoint reports twice (pending, completed) is two rows, and readers take the latest row per reference. Merging would need a unique constraint the tables API does not offer, and the log is the more honest record.
6. **Templates declare their tables** (`tables` in the template file, columns included) and Taskiem makes the missing ones when a template is used, after the draft is saved and outside its transaction, as AI drafts already do; a table that exists is left alone, and one that cannot be made is reported, never a reason to lose the draft. A table's name may be the marker of a parameter with the table picker, so a person can point a template at a table of their own.
7. **Four templates**, no more for now: record, evening summary, big-sale and declined-payment alert, end-of-day cash-up. The cash-up is the reconciliation owners actually do (the sales book against the terminal); settlement reconciliation waits for a settlement feed.
8. **OPay follows** on the same tables and templates once OPay answers two questions (the envelope's signing; whether the webhook covers walk-up sales).

## Alternatives considered

- **More actions and a second trigger on `moniepoint@1`.** Rejected: different credentials and account; a connection would hold keys for one product and be half-configured for the other, and the guided screen would have to explain both.
- **A generic `hmac_sha256_id_timestamped` verify scheme in the engine.** Possible, and it would make the trigger pollable, but nothing to poll exists. Kept in the connector until a second provider signs the same way.
- **Reading bank alert SMS or email instead.** Needs the owner's phone or mailbox, covers every provider, but arrives unsigned and unstructured. Kept as the path for providers without an API, through the email trigger.
- **Open banking (Mono) on the Moniepoint account.** Would also show settlement, but coverage of Moniepoint MFB business accounts is unconfirmed, and it is a statement, not a per-terminal feed.
- **A Taskiem-owned POS table** outside data tables. Rejected: the owner could not export, search or reuse it, and the templates could not read it like any other table.

## Provenance

Moniepoint POS developer documentation on teamapt.atlassian.net (Webhooks; Push Payment Request, API Reference; API Client Credentials; POS as a Platform), read 21 October 2026; a third-party setup guide (BrandDrive) for the dashboard's menu names; OPay's POS Integration (API) and General Payment Webhook pages on documentation.opayweb.com, and OPay's public Odoo addon, for what OPay offers. The design of templates that make their tables follows the AI builder's proposed tables ([0033](0033-data-tables-on-xenibase.md)). Our own.
