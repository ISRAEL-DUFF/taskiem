# 0062 — Webhook tokens Taskiem makes: Prembly verified by a URL token, not its public-key signature

Date: 2026-11-05 · Status: **Accepted** 2026-11-05 (built: `generate: webhook_token`, `prembly@1` `verification` on `query_secret`)

## Context

Prembly signs its webhooks with the business's public key (base64 HMAC-SHA256 of the body, `x-prembly-signature`). Its widget is configured with that same public key in the business's web page, so anyone who reads the page can sign a delivery that says `VERIFIED` for a `user_ref` they chose. `prembly@1` trusted that signature, so a forged result could start, or resume, a KYC or onboarding workflow (review finding F55). Prembly documents no lookup of a widget verification by reference and no other signing secret.

The connectors whose providers sign nothing (M-Pesa, Africa's Talking, Remita, MTN MoMo) put a token in the URL, but the business chooses it (`openssl rand -hex 32`), which nothing enforces.

## Decision

1. **Prembly's trigger is verified by a URL token** (`verify: { scheme: query_secret, query: token, secret_field: callback_token }`). The public-key signature is no longer checked, and the `public_key` field is gone: it proved nothing. Nobody used the connector yet, so there is no transition.
2. **The token is made by Taskiem.** A new auth-field value, `generate: webhook_token` (connector/v1 rule 14), marks a secret, optional field the server fills with 32 random bytes (hex) when the connection is saved: on the connection screen, a connect link, or a partner's provisioning. A value given for it is refused, it cannot be set or cleared afterwards (`PUT /v1/connections/{id}/credentials`), and a partner's credential replacement keeps it.
3. **Shown once, in the address.** The answer to saving the connection carries `webhooks`: each trigger verified by that token, with its full address, token included, and where to paste it. The web shows it with a copy button. Taskiem does not show it again; a new token means connecting again. The WhatsApp key form is not offered for such a service, since it could not show the address.
4. As for every URL token, it is compared in constant time (`connector.VerifyQuerySecret`) and stripped before the event reaches a run; hook paths, not query strings, are logged.

## Consequences

- A forged Prembly delivery needs the address, which only the business and Prembly hold.
- The token is only as secret as the address: whoever can read Prembly's dashboard setting can send. Rotation is reconnecting.
- Prembly must keep the query string of the configured URL (to confirm with Prembly, 7).
- The other URL-token connectors still take a token the business chooses; moving them to `generate: webhook_token` is a manifest change each, left for when they are next touched.
