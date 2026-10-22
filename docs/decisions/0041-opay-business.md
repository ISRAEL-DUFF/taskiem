# 0041 — OPay Business: the general payment webhook through a verifier and an enricher, RSA keys made for the business, the same POS record

Date: 2026-10-22 · Status: **Accepted** 2026-10-22 (built: `opay_business@1`, the `pos-opay-*` templates, `multiline` and `generate` on auth fields)

## Context

[0040](0040-pos-terminals.md) built the terminal record on Moniepoint and left OPay for when two questions were answered. Reading OPay's offline-payments documentation in full changed the picture ([POS terminals, §2](../strategy/pos-terminals.md)): OPay has a **general payment webhook** for "all types of transactions" on the business account, each notification carrying the terminal serial, the fee and the settlement amount; a per-terminal **transaction history** (the TN integration API, seven days per call); an order query and a balance query. That is more than Moniepoint gives, and enough for gross-against-net reconciliation and backfill. Two things remain open: whether walk-up terminal sales fire the webhook, and whether its body is the signed RSA envelope (the POS page says so for the same URL; the general page shows only the decrypted notification). OPay's offline APIs are also enabled by a sales representative, not self-serve.

## Decision

1. **Build now, against the documentation**, as Moniepoint was built, and confirm the two questions on OPay's test business. The connector ships only the envelope verifier; if OPay posts plain JSON, the trigger moves to a URL secret plus confirmation by `get_transaction`, a manifest change. Shipping an unsigned path "just in case" would weaken the gate for every business for a case that may not exist.
2. **A separate connector, `opay_business@1`.** OPay Checkout (`opay@1`) is a different product with different keys; one connection screen per account people have.
3. **Verify, then decrypt, in two hooks the engine already has.** The webhook verifier gets one credential, so it takes OPay's public key (`secret_field: opay_public_key`) and checks the envelope, the five-minute timestamp and OPay's signature over `paramContent` + `timestamp`; the trigger's **enricher**, which gets every credential, checks the client auth key and decrypts the notification with the business's private key before the run starts, so workflows see plain fields. Dedup is the `X-Opay-Tranid` header, the only plain identifier; correlation is not possible on an encrypted body, so terminal orders are confirmed by `get_payment_request` rather than awaited as signals. No engine change was needed.
4. **Taskiem makes the RSA key pair.** OPay's authentication requires a merchant key pair, which no shop owner should generate with OpenSSL. Two guidance keys join the connector contract (rule 14): `multiline` (a text area for PEM) and `generate: rsa_key_pair`, on which the connection screen makes the pair in the browser, keeps the private key as the value and shows the public key once to paste into OPay's dashboard. The private key reaches the server only as the connection's secret; Taskiem never sees it in the clear elsewhere.
5. **Answers are verified before their code is believed.** An unsigned or mis-signed answer, even a refusal, is an unknown outcome, not a fact.
6. **The same record.** OPay's notifications land in `pos_sales` beside Moniepoint's, with two optional columns, `fee` and `settled`, that Moniepoint leaves empty. The evening summary, alert and cash-up need no change; a settlement check and the two history templates (nightly sync, backfill) are OPay's alone, because only OPay has fees, settlement and a history endpoint.
7. **Kobo in the connector, naira in the tables**, as before; OPay's naira text is converted exactly or refused.

## Alternatives considered

- **Wait for OPay's answers before building.** Rejected: everything but the two questions is documented, the connector is fixture-tested against the documented envelope, and the answers change one manifest line at most.
- **Extend the engine with an "RSA envelope" verify scheme.** Would need the verifier to see two credentials and to replace the body; the enricher already replaces the body for runs and has every credential. Kept in the connector until a second provider uses the same envelope.
- **Ask the owner to paste a private key.** Rejected under the [no-technical-steps rule](../spec/no-technical-steps.md); generating it is the only path an owner can follow.
- **A 2048-bit key.** OPay's documentation and samples generate 1024-bit keys and its chunking follows the key size; Taskiem follows the documentation and lists the question (to confirm, 4).

## Provenance

OPay's offline-payments documentation on documentation.opayweb.com (API Basics, Authentication, POS Integration (API), General Payment Webhook, Query Balance, Query Order Details, TN Integration API, Digital Wallets), read 22 October 2026; OPay's public Odoo integration (`services/opay_auth.py`) for the webhook signature string, the five-minute window and the client auth key check. Our own.
