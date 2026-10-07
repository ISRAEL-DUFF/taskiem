# Prembly integration

Prembly (IdentityPass) verifies identities and documents. The connector is `connectors/prembly` (`prembly@1`), built on the Prembly 2.0 API from its public documentation at docs.prembly.com (API reference under docs.prembly.com/reference, guides under docs.prembly.com/docs; read 6 October 2026). It covers the Nigerian identity checks and the face biometrics; background checks, AML screening, global document checks and other countries are not covered.

## Connection

| Field | |
| --- | --- |
| `api_key` | The secret key from the dashboard (API Integrations), sent as `x-api-key`. Sandbox and live share `api.prembly.com`; the key decides the environment |
| `public_key` | The public key; Prembly signs webhooks with it. Needed only for the `verification` trigger |

## Actions

Every action is a read: Prembly bills each check, but none has an effect.

| Action | Endpoint | Notes |
| --- | --- | --- |
| `lookup_bvn` | `POST /verification/bvn` | Full record, watch-list flag |
| `lookup_bvn_basic` | `POST /verification/bvn_validation` | Names, date of birth, phone |
| `verify_bvn_with_face` | `POST /verification/bvn_w_face` | Adds `face_match`, `face_confidence` |
| `lookup_bvn_by_phone` | `POST /verification/bvn_with_phone_advance` | |
| `lookup_nin` | `POST /verification/vnin` | Full NIMC record |
| `lookup_nin_basic` | `POST /verification/vnin-basic` | |
| `verify_nin_with_face` | `POST /verification/nin_w_face` | |
| `lookup_phone` | `POST /verification/phone_number/advance` | The NIN record behind a phone number |
| `lookup_phone_basic` | `POST /verification/phone_number` | |
| `verify_drivers_license` | `POST /verification/drivers_license/advance/v2` | Names are matched; `verified` false on a mismatch |
| `verify_passport` | `POST /verification/national_passport_v2` | |
| `verify_voters_card` | `POST /verification/voters_card` | |
| `lookup_cac` | `POST /verification/cac/basic` or `/cac/advance` | `advanced` adds directors |
| `resolve_bank_account` | `POST /verification/bank_account/basic` | |
| `compare_faces` | `POST /verification/biometrics/face/comparison` | |
| `check_liveness` | `POST /verification/biometrics/face/liveliness_check` | |
| `get_wallet_balance` | `GET /api/v1/wallet` | Also the connection test |

**Results, not failures.** Prembly answers with HTTP 200 and a `response_code`. `00` is a record (`found: true`; `verified` says whether Prembly's verification status is `VERIFIED`). `01` (no record) is `found: false`. `07` (BVN blocked or watch-listed, NIN suspended) is `found: false, blocked: true`. A face that does not match is `match: false`. These are normal outputs a workflow branches on.

**Failures.** `02` (source unavailable) is retried. `03` (wallet empty) fails the step: top the wallet up. 4xx (bad key, bad input) fails; 429 and 503 are retried; other 5xx retry as reads. An undocumented code fails the step rather than paying for blind retries.

**Personal data.** As with Dojah, photos, signatures and face crops are dropped from every output unless the step sets `include_image`; `record` holds Prembly's record without them. Inputs carrying BVNs, NINs, phone numbers, names, dates of birth, account numbers, document numbers and face images are declared PII. Outputs cannot be declared under the manifest contract; BVNs, NINs and phone numbers in them are caught by detection, names and addresses are not (see the delivery report).

## Webhooks

Prembly posts SDK (widget) results, and results of checks that were `PENDING`, to the webhook URL in its dashboard; the payload is the same as the API response. One trigger, `verification`, at `/hooks/{tenant}/connectors/prembly@1/verification?env=prod&connection=<name>`. Deliveries are verified as Prembly documents: `x-prembly-signature` is base64 HMAC-SHA256 of the body keyed with the public key. Repeats are dropped on what the signature covers: the verification's `reference` and `status`, or, without a reference, a hash of the body. The `token` header is not used, because the signature does not cover it and a replayed delivery could carry a new one.

| Event (`verification.status`) | Correlation |
| --- | --- |
| `VERIFIED`, `NOT-VERIFIED`, `PENDING` (`UNSPECIFIED` when absent) | SDK: the widget's `user_ref`. API: the `reference` the lookup returned |

## To confirm with Prembly before go-live

1. The response for a face that does not match, and for no liveness (only the positive responses are documented); the connector reads anything but `00` (other than `02`/`03`) as a negative.
2. The wallet balance response (documented as `{}`); the connector returns it as given.
3. Whether an `app-id` header is still needed (Prembly 2.0 documents only `x-api-key`).
4. `nin_w_face` documents `number_nin` as an integer; the connector sends it as a JSON number.
5. Whether the signature covers the raw body (the connector verifies the raw body), and whether a retried delivery repeats the body byte for byte.
6. Whether the webhook carries the same `verification.reference` the API call returned.
