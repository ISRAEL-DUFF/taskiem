# Youverify integration

Youverify provides KYC, KYB, address verification and AML screening. The connector is `connectors/youverify` (`youverify@1`), built on API v2 from Youverify's public documentation at doc.youverify.co (API reference under doc.youverify.co/api-reference, webhooks under doc.youverify.co/webhooks; read 6 October 2026). The Cowork entity, case and transaction-monitoring APIs are not covered.

## Connection

| Field | |
| --- | --- |
| `secret_key` | The API secret key (Settings → API Keys), sent as the `token` header. It also signs webhooks |
| `environment` | `sandbox` uses `api.sandbox.youverify.co`; anything else is live (`api.youverify.co`). Keys work only in their own environment |

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `verify_bvn` | `POST /v2/api/identity/ng/bvn` | read | Optional name and date-of-birth validation; `premium` |
| `verify_nin` | `POST /v2/api/identity/ng/nin` | read | Optional validation and selfie match |
| `verify_drivers_license` | `POST /v2/api/identity/ng/drivers-license` | read | |
| `verify_phone` | `POST /v2/api/identity/ng/phone` | read | Names and dates of birth on the number |
| `search_phone` | `POST /v2/api/identity/ng/nin-phone` | read | NIN record for a phone number |
| `resolve_bank_account` | `POST /v2/api/identity/ng/bank-account-number/resolve` | read | |
| `compare_faces` | `POST /v2/api/identity/compare-image` | read | `match`, `confidence`, `threshold` |
| `verify_business` | `POST /v2/api/verifications/ng/company/basic` | read | By CAC registration number |
| `search_businesses` | `GET /v2/api/verifications/global/search-companies` | read | |
| `get_business_details` | `GET /v2/api/verifications/ng/company-details/{id}` | read | |
| `screen_aml` | `POST /v2/api/verifications/advanced/name/aml-checks` | read | PEP, sanctions, adverse media |
| `get_aml_check` | `GET /v2/api/verifications/aml-checks/{id}` | read | |
| `create_address_candidate` | `POST /v2/api/addresses/candidates` | unsafe write | |
| `request_address_verification` | `POST /v2/api/addresses/individual/request` | unsafe write | Dispatches a field agent (billed) |
| `get_address_verification` | `GET /v2/api/addresses/{id}` | read | |

There is no connection test: Youverify documents no free, side-effect-free call.

**Consent.** Youverify requires the subject's consent on every check. The step must say so (`subject_consent: true`); the connector refuses the check otherwise instead of asserting consent on the workflow's behalf.

**Results, not failures.** Identity checks answer HTTP 200 with `status` `found`, `not_found` or `pending`. `not_found` is `found: false`, a normal output. `pending` means the source is down; the result arrives as `identity.verification.completed`, correlated by the `id` the check returned. `failed` is retried. A face that does not match is `match: false`.

**Failures.** 402 (wallet empty), 401, 403, 404 and 422 fail the step; 429 is retried; 5xx are unknown outcomes (retried for reads; the two writes park rather than send a second agent or candidate).

**Personal data.** Photos and signatures are dropped unless `include_image` is set; `record` holds Youverify's record without them. Inputs carrying BVNs, NINs, phone numbers, names, dates of birth, emails, addresses, account numbers and images are declared PII. Outputs cannot be declared under the manifest contract (see the delivery report).

## Webhooks

Youverify posts every event to the callback URL in its dashboard, so there is one trigger, `event`, at `/hooks/{tenant}/connectors/youverify@1/event?env=prod&connection=<name>`. Deliveries are verified as Youverify documents: `x-yv-signature` is the hex HMAC-SHA256 of the body keyed with the API secret key. KYC events carry no documented event id, so repeats are dropped on the verification id, its status and its time.

| Events | Correlation |
| --- | --- |
| `identity.verification.completed` | the verification `id` a pending check returned |
| `address.verification.completed` | the `reference_id` `request_address_verification` returned |

## To confirm with Youverify before go-live

1. The BVN reference names the validation object `validation`, the NIN one `validations`; the connector sends each as documented.
2. Whether the webhook signature covers the raw body (the documented example re-serialises the parsed JSON); the connector verifies the raw body.
3. Whether `identity.verification.completed` carries the verification `id` (the documented example does not) and an event id for deduplication.
4. Whether `address.verification.completed`'s `referenceId` is the one returned when the verification was requested.
5. The response shape of the AML name search (the documentation shows the request in place of the response); the connector reads it like the retrieve endpoint.
6. Whether retrying a request after an unknown outcome can dispatch two agents, and whether `metadata` could carry an idempotency key.
7. Whether Youverify accepts HTTP 202, which Taskiem's ingest answers with; Youverify documents retrying anything but 200.
