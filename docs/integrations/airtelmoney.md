# Airtel Money integration

Status: **not built — waiting on documentation access** ([What needs people](../needs-people.md#phase-3), MM3).

`airtelmoney@1` was planned alongside [M-Pesa](mpesa.md) and [MTN MoMo](mtnmomo.md): OAuth client credentials, `X-Country` and `X-Currency` headers, collections by USSD push with enquiry, disbursements with an encrypted PIN and enquiry, KYC user enquiry, balance, and callbacks. Under the [clean-room policy](../clean-room-policy.md) Taskiem builds connectors only from the provider's own public documentation, and Airtel's is not public:

- The developer portal, https://developers.airtel.africa/, is a web application whose documentation pages (`/documentation/...`) load their content from the portal's back end (`/portal-bff/v1/product-documentation`, `/portal-bff/v1/product-documentation/all`), which answers `401 invalid_token` without a signed-in session.
- The API hosts (`openapi.airtel.africa`, `openapiuat.airtel.africa`, and per-country hosts such as `openapi.airtel.ug`, named in the portal's public configuration) publish no description of their own.
- Third-party articles, SDKs and open-source clients describe the API, but they are not the provider's documentation and the policy forbids building from them.

What the public portal does show (its configuration, read 7 October 2026) is not enough to write the API: that requests carry an `X-Country` header; that the portal manages per-account PINs and RSA keys for some products (disbursement, remittance, cash-in), "message signing" for some products in Uganda and digital signatures in Kenya; that callbacks are configured per account; and that Uganda is moving to new base URLs (`openapiuat.airtel.ug`, `openapi.airtel.ug`).

## To build it

1. Someone with authority to accept Airtel's portal terms registers a developer account (or an existing merchant account is used) and confirms the documentation may be used to build an integration.
2. With the documentation readable, build `connectors/airtelmoney` like the other two: manifest and Go package (token caching, `X-Country`/`X-Currency`, amounts by ISO 4217 scale), collections and their enquiry, disbursements with the PIN encrypted as documented (and their enquiry as the reconcile read), KYC and balance, callbacks with whatever signature or hash Airtel documents (a `connector`-scheme verifier if it signs fields inside the body), a fake server and fixtures, `TASKIEM_AIRTELMONEY_URL`.
3. Questions to settle from the documentation: whether disbursements deduplicate by the merchant's transaction id (idempotent write) or only allow an enquiry (reconcilable write); the callback signature and the countries where message signing is required; the encryption of the PIN and of signed messages; currency and amount rules per country.
