# MTN MoMo integration

MTN Mobile Money (MoMo) runs in many African countries behind one Open API. The connector is `connectors/mtnmomo` (`mtnmomo@1`), built clean-room from MTN's public developer documentation only (read 7 October 2026), with no SDK or other client consulted:

| Page | URL |
| --- | --- |
| Introduction | https://momodeveloper.mtn.com/api-documentation |
| Getting started (products, subscription keys) | https://momodeveloper.mtn.com/api-documentation/getting-started |
| API user and API key management, OAuth 2.0, methods | https://momodeveloper.mtn.com/api-documentation/api-description |
| Use cases (request to pay, transfer, account holder, balance) | https://momodeveloper.mtn.com/api-documentation/use-cases |
| Sandbox use cases (test numbers, EUR) | https://momodeveloper.mtn.com/api-documentation/testing |
| Common error codes, target environments | https://momodeveloper.mtn.com/api-documentation/common-error |
| Callback | https://momodeveloper.mtn.com/api-documentation/callback |
| Best practices | https://momodeveloper.mtn.com/best-practices |
| API reference (Collection, Disbursement, Remittance) | https://momodeveloper.mtn.com/API-collections |
| Production gateway host | https://momoapi.mtn.com/developer/apis/collection/hostnames?api-version=2022-04-01-preview |

The API reference was read through the portal's own developer API (`https://momodeveloper.mtn.com/developer/apis/{collection,disbursement,remittance}/operations/{operation}?api-version=2022-04-01-preview` and the products' schemas), which serves the operations, headers, examples and error examples the API-collections page shows.

Sandbox and production credentials are not available yet ([What needs people](../needs-people.md#phase-3), MM2), so the connector is tested against a fake Open API written from the same pages (`connectors/mtnmomo/internal/fakemomo`): per-product subscription keys and tokens, X-Target-Environment and currency checks, UUID reference ids usable once (409), requests that stay pending until settled, the sandbox's documented test numbers, the callback host rule, one PUT callback per request, lost answers. `testdata/callbacks.json` holds MTN's documented status examples.

## Connection

| Field | |
| --- | --- |
| `api_user`, `api_key` | The API user (a UUID) and its key: in production from the Partner portal of the country; in the sandbox from the provisioning API (`POST /v1_0/apiuser`, then `/apikey`) |
| `collection_subscription_key`, `disbursement_subscription_key`, `remittance_subscription_key` | Each product's primary or secondary key (`Ocp-Apim-Subscription-Key`); only the products used |
| `<product>_api_user`, `<product>_api_key` | Optional: a separate API user for one product |
| `environment` | `sandbox` uses `sandbox.momodeveloper.mtn.com`; anything else `proxy.momoapi.mtn.com` |
| `target_environment` | `X-Target-Environment`: `mtnuganda`, `mtnghana`, `mtnivorycoast`, `mtnzambia`, `mtncameroon`, `mtnbenin`, `mtncongo`, `mtnswaziland`, `mtnguineaconakry`, `mtnsouthafrica`, `mtnliberia`, `mtnsouthsudan`, `mtnnigeria`, `mtnrwanda` (sandbox: `sandbox`, the default there) |
| `currency` | The target environment's currency (sandbox: EUR, the default there) |
| `hooks_url` | `https://<taskiem>/hooks/<tenant>/connectors/mtnmomo@1`; its host must be the callback host registered with the API user (`providerCallbackHost`) |
| `hooks_env`, `hooks_connection` | The Taskiem environment (default `prod`) and this connection's name, which callback URLs carry in their path |
| `callback_token` | A long random value; the last segment of every callback URL |

One connection serves one country (one target environment and currency); use one connection per country.

**Auth.** Each product issues its own token: `POST /{product}/token/` with Basic `api_user:api_key` and the product's subscription key. MTN says to reuse a token until it expires; the connector caches one per product, credential set and host, renews it a minute before `expires_in`, and on a 401 fetches a new one once and repeats the call. A refused token request is fatal (check the API user, key and subscription key); every API call carries `Authorization`, `Ocp-Apim-Subscription-Key` and `X-Target-Environment`.

## Actions

| Action | Endpoint | Class | Notes |
| --- | --- | --- | --- |
| `get_balance` | `GET /{product}/v1_0/account/balance` | read | Connection test; the first product with a key unless one is named |
| `validate_account_holder` | `GET /{product}/v1_0/accountholder/msisdn/{msisdn}/active` | read | `active` true or false; says nothing about amounts |
| `get_account_holder_name` | `GET /{product}/v1_0/accountholder/msisdn/{msisdn}/basicuserinfo` | read | Given and family name, without the holder's consent, as MTN allows |
| `request_to_pay` | `POST /collection/v1_0/requesttopay` | idempotent write | Reconcile: `get_payment` |
| `get_payment` | `GET /collection/v1_0/requesttopay/{referenceId}` | read | |
| `transfer` | `POST /{disbursement,remittance}/v1_0/transfer` | idempotent write | Reconcile: `get_transfer` |
| `get_transfer` | `GET /{product}/v1_0/transfer/{referenceId}` | read | Without a product it looks in Disbursement, then Remittance |

**Never twice.** MTN's POSTs are asynchronous: `202 Accepted` and the request is `PENDING` until `SUCCESSFUL` or `FAILED` ("do not treat HTTP 202 Accepted as success"). The `X-Reference-Id` identifies the request and "if a POST is using a reference id that is already used, then a duplication error response will be sent" (409 `RESOURCE_ALREADY_EXIST`), so both writes are **idempotent writes**:

- The engine's key (32 hex characters, 128 bits) is laid out as a version-4 UUID, as MTN requires (6 bits are fixed by the UUID format, 122 remain). The same key always gives the same `X-Reference-Id`, so a resend after a lost answer gets 409 and the connector reads the request back by that id and reports it: pending or successful as an output, failed as a failed step with MTN's reason (nothing moved). 409 with nothing under the id parks the step.
- Refusals MTN returns as 5xx although they describe the request (`INVALID_CURRENCY`, `NOT_ALLOWED_TARGET_ENVIRONMENT`, `INVALID_CALLBACK_URL_HOST`, `PAYEE_NOT_FOUND`, `PAYER_NOT_FOUND`, `NOT_ALLOWED`) are checked by reading the id back: nothing there fails the step with MTN's code. `INTERNAL_PROCESSING_ERROR` and other 5xx are unknown outcomes, resent under the same id; 503 is retried.
- Each write also declares a read-only reconcile action (`get_payment`, `get_transfer`) that finds the request by the engine's key, for a person settling a parked step. The connector never generates its own reference ids.

**Amounts** are minor units of `currency` (the step's, or the connection's). MTN takes and returns decimal strings in the major unit; the scale is ISO 4217's (UGX, XAF, XOF, RWF, GNF have no minor unit, so 5000 is 5,000 francs or shillings; GHS, ZMW, ZAR, NGN, EUR have two decimals). Whole amounts are sent without decimals, as in MTN's examples (`"1000"`), others with them (`"1500.50"`). Amounts MTN returns are read exactly; balances are floored to the minor unit.

**Inputs MTN refuses** are checked before sending: MSISDNs must be digits with the country code; `payer_message` and `payee_note` at most 160 characters and without apostrophes. MTN sends `externalId` into reports; the connector sets it to the reference id so callbacks correlate.

## Callbacks and verification

MTN calls the `X-Callback-Url` of a request once, with PUT or POST, when it reaches a final state; the body is "similar to the response returned by the status check API". There is no retry, and MTN signs nothing. Its best practices also say callback URLs may carry only path parameters, not a query string, so the connector uses Taskiem's path form with `path_secret` verification:

`<hooks_url>/<trigger>/<hooks_env>/<hooks_connection>/<callback_token>`

A delivery whose last segment is not the connection's `callback_token` is refused with 401; the token never reaches a run. Taskiem answers 200, as MTN asks. Without `hooks_url` no callback is requested and status is read with `get_payment` / `get_transfer`.

| Trigger | Events | Dedup | Correlation (signal steps wait for `mtnmomo@1:<trigger>`) |
| --- | --- | --- | --- |
| `payment_callback` | `requesttopay.SUCCESSFUL`, `requesttopay.FAILED` | `externalId:status` | `reference_id` (sent as `externalId`) |
| `transfer_callback` | `transfer.SUCCESSFUL`, `transfer.FAILED` | same | `reference_id` |

MTN recommends validating callbacks against approved source IP ranges it does not publish, and polling status as a fallback: a callback is a report; read `get_payment` / `get_transfer` before releasing value, and poll when no callback arrives. MTN's callback gateway also only trusts certain certificate authorities (the Callback page lists them); Taskiem's hooks host certificate must chain to one.

## Setting up

1. Sign up on the developer portal (sandbox) and subscribe to Collection, Disbursement and/or Remittance; note each product's subscription key.
2. Sandbox: create an API user and key with the provisioning API, with `providerCallbackHost` set to Taskiem's hooks host. Production (after MTN's go-live in each country): create them in the Partner portal and register the callback host there; share your outgoing IPs with your MTN account manager (Disbursement, Remittance and some Collection services require IP whitelisting).
3. Create one Taskiem connection per country with the fields above.

## To confirm before go-live

1. Whether amounts with decimals are accepted for two-decimal currencies, and whether whole amounts must be sent without decimals.
2. Whether the API user and key are shared across products in production, or one per product (both are supported).
3. The account holder id type in `basicuserinfo` for Collection and Disbursement (`MSISDN` or `msisdn`; the reference lists `MSISDN` while `/active` requires lower case; the connector sends `msisdn`).
4. Whether callbacks always carry `externalId` (correlation depends on it), and whether they come as PUT or POST per country.
5. The source IP ranges of callbacks, to allow-list them in front of the token.
6. How long `X-Reference-Id`s stay unique (the connector assumes forever, as documented).
7. The production base URL per country: `proxy.momoapi.mtn.com` is the gateway host MTN's production portal lists; confirm no country uses another.
8. Which `transferType` values aggregators must send (the Aggregator API makes it required; the connector does not send it).
