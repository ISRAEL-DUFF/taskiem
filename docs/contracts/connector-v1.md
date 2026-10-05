# Contract: Connector manifest `connector/v1`

The structure is defined by `schemas/connector-v1.schema.json`; `connectors/paystack/manifest.yaml` is the reference example. `engine/connector.ValidateManifest` enforces the schema plus these registration rules:

1. Every write action has a `class` (schema-enforced; there is no default).
2. `idempotent_write` actions declare `idempotency`; `reconcilable_write` actions declare `reconcile`; `read` actions declare neither `idempotency` nor `compensate`.
3. `reconcile` names a `read` action in the same manifest. `compensate`, when not null, names a write action in the same manifest.
4. The idempotency encoding fits the provider: `prefix` plus `length` encoded characters must fall within `limits.min_length`/`max_length`, and every character the encoding and prefix can produce must be in `limits.charset` (see [idempotency.md](idempotency.md)). The encoded hash must carry at least 128 bits.
5. `auth.test.action` names a `read` action.
6. Every `pii` field names a property of the action's input schema.
7. Webhook triggers declare `verify`. Schemes: `hmac_sha256` and `hmac_sha512` over the raw body (hex, optional `sha256=` prefix), `hmac_sha256_timestamped` over `<timestamp>.<raw body>` with the timestamp in `timestamp_header` and deliveries outside `tolerance` (default 5m) refused, `bearer`, `basic`, `none`.
8. Semver: a workflow pins the major version; minor and patch versions must not remove actions, inputs, or output fields, or change an action's class.
