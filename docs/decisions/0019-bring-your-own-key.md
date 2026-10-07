# 0019 — Bring your own key: the customer's key wraps outside the platform's, keys re-wrap in the background, and work parks when a key is lost

Date: 2026-10-07 · Status: Accepted

## Context

Spec 14.1 promises envelope encryption: data keys under a per-tenant key, under a root key in a KMS. It also promises bring your own key (BYOK) for enterprise tenants, where "their KEK lives in their KMS, and revoking it renders their data unreadable". The `byok` plan feature existed (decision 0017), but there was no key path behind it. Two findings of the internal self-review were waiting on this work:

- **S23.** Rotation re-wrapped secrets' data keys but not personal-data subject keys, and subject ids were HMACs keyed by tenant key version 1 itself. Version 1 therefore could never be retired.
- **S33.** A secret's ciphertext was bound to its id, but not to its environment or name.

A tenant key cannot be unwrapped once a customer revokes its key, and Taskiem moves money. A revocation must never turn into a half-done payment, a lost webhook or a failed payroll run that someone then re-runs by hand.

## Decision

1. **Double wrapping, customer outermost.** While a tenant has a customer key in use, each new tenant key version is stored as `customer.Wrap(platformKMS.Encrypt(root, kek))` (`tenant_keys.byok_key_id`). Unwrapping needs both KMSs. The customer's KMS sees only an already-wrapped blob, never the tenant key. Revoking either key makes the tenant key unreadable. The alternative orders were rejected:
   - wrapping by the customer key alone makes the customer's KMS the only control, and shows it the tenant key;
   - platform outermost lets the platform peel its layer and hand the customer's KMS the bare tenant key.

2. **A small provider interface, clean-room clients.** `byok.Provider` has two methods, `Wrap` and `Unwrap`, on a few hundred bytes. Four clients are written from public HTTP API documentation only, with the standard library:
   - OpenBao/Vault transit, with a token or AppRole and namespaces;
   - AWS KMS, with Signature Version 4 and an encryption context;
   - Google Cloud KMS, with a JWT-bearer service-account token and AAD;
   - Azure Key Vault `wrapkey`/`unwrapkey`, with RSA-OAEP-256 and client credentials.

   Each ciphertext names its provider (`vault:`, `aws:`, `gcp:`, `azure:<version>:`). Every call goes through the egress guard, with TLS verified and an optional private CA added to the system roots. Private address ranges are reachable only on dedicated deployments (`TASKIEM_BYOK_ALLOW_PRIVATE`).

3. **Credentials are platform secrets.** A customer key's credentials are encrypted by the platform KMS key, with the tenant and the key's row id prefixed to the plaintext. This binds them to their place: copied to another row, they are refused. They live in `tenant_byok_keys`, outside the tenant's vault, so no workflow can name them. The API takes them and never returns them. A short fingerprint identifies which set is stored. They cannot be encrypted under the tenant key, because they are what unwraps it.

4. **Onboarding proves the key first.** A random 32-byte canary is wrapped and unwrapped before anything is stored. The wrapped canary is kept, and it is what the health check unwraps. Replacing credentials must unwrap the same canary, which proves the new credentials reach the same key.

5. **Rotation re-wraps in the background (S23).** Rotation adds a version and queues the tenant (`key_rewrap_due`). Onboarding and offboarding are rotations. The key job then re-wraps data keys, subject keys and the pseudonymisation key in batches under row locks (`SKIP LOCKED`, one re-wrapper per tenant). When nothing is left under an older version, it retires those versions. After `TASKIEM_KEY_DESTROY_AFTER` it blanks the wrapped material of retired versions nothing references, and it forgets a retired customer key's credentials once no live version depends on it. A rotation in one transaction, as before, would not scale to large tenants and would hold every secret row locked while it called the KMS.

6. **A pseudonymisation key of its own (S23).** Subject ids stay `hmac(key, category, value)`. The key is now a row of its own (`tenant_pseudonym_keys`), sealed under the current tenant key and re-wrapped like any data key. It is initialised from tenant key version 1's material, so every existing subject id is unchanged. Choosing a new random key would have orphaned every existing subject from erasure by value. Afterwards, version 1 is an ordinary version that can be retired and destroyed.

7. **Secrets bound to their place (S33).** New ciphertext carries associated data made of the tenant, environment, name (or `connection:<id>` for connection credentials) and id (`secrets.aad_version = 2`). Rows written before are upgraded by the same background job: it decrypts with the old binding and re-seals with the new one under the same data key. A downgrade of `aad_version` is useless to an attacker, because a version-2 ciphertext does not open under the version-1 associated data.

8. **Bounded caching.** A tenant key that depends on a customer key is cached for `TASKIEM_BYOK_CACHE_TTL` (default 5 minutes, at most an hour). Revocation therefore takes effect in every process within that bound. A failed unwrap is remembered for 15 seconds so that reads fail fast. A failed health check drops the tenant's cached keys at once in the process that ran it. Platform-only keys are cached for the life of the process, as before.

9. **Fail closed, park, resume.** Any failure to unwrap a tenant key is `secrets.ErrKeyUnavailable`. What follows:
   - **API.** The API answers 503 `key_unavailable`.
   - **Ingest.** Webhook verification answers 503, so providers retry.
   - **Worker.** A step that cannot read its secrets or credentials before sending fails with kind `key_unavailable` and next `park`. The run goes to `needs_reconciliation`, and the step is listed in `key_parked_steps` with its mode (`send` or `reconcile`).
   - **Resumption.** When the key works again, the key job (or a manual check) appends a `key_restored` failure (next `retry`) for a step parked in `send` mode, where nothing was sent: its kind maps to `not_sent`, so even an unsafe write may be sent. A step parked in `reconcile` mode gets `key_restored_reconcile` (next `reconcile`). `decide` exempts both kinds from the retry budget and the backoff, so the outage does not count against the step.
   - **Results.** A step whose result cannot be sealed (personal data in the output) is left to the existing crash path. The task is abandoned and recovered after its lease: reads repeat, and writes reconcile or park for a person. Nothing is invented for it, because marking a completed write as "not sent" could pay twice.

10. **Health and alerts.** The key job checks each customer key every `TASKIEM_KEY_CHECK_INTERVAL` by unwrapping the canary. Two failures in a row mark the key `unavailable`, and success marks it `active` again. Both transitions are audited, and both raise a `key_health` alert (once per outage, once per recovery). A failed check of a platform-only key is counted in a metric. Because the key job unwraps the current tenant key after the canary check, the platform KMS failing parks and resumes work in the same way.

11. **Plans gate onboarding, not leaving.** Bringing a key needs the `byok` feature (402 otherwise). Rotating, checking, replacing credentials and returning to the platform key do not, so an organisation can always reach its data and leave. A downgrade is blocked while a customer key is in use.

12. **Dedicated deployments need nothing new.** A single-tenant Helm install can point `TASKIEM_KMS=openbao` at the customer's own OpenBao, which makes the platform key the customer's. Per-tenant BYOK works on top of it.

## Alternatives considered

- **Re-encrypting every value under a data key from the customer's KMS (AWS `GenerateDataKey` style).** This needs a KMS call per value or a cache that amounts to the same thing as a tenant key. Wrapping the existing tenant key keeps one customer call per cache period.
- **Making the platform key the customer's key for every tenant (one KMS per tenant).** This is what a dedicated deployment does. In a multi-tenant cloud it would mean one OpenBao per customer.
- **Failing steps on a lost key.** Rejected: a revocation would fail payroll runs and invite manual re-runs. Parking keeps the run where it was, and resuming spends no retries.
- **Resuming through the operator's `retry` resolution.** Rejected: that resolution spends the retry budget, so a code step with no retries would fail on resume. It also cannot express "reconcile first".
- **Keeping old tenant key versions forever (as before).** Rejected for BYOK: data re-wrapped onto the customer's version would still sit beside a platform-only wrapped copy of the old key, and the backups of tomorrow would hold it too. Destruction after a grace period keeps a window to recover from a bad rotation.
- **An SDK per cloud.** Licence-compatible (Apache-2.0), but large. Each of the four needs two calls and a token, so stdlib clients keep the dependency tree and the licence check as they are.

## Consequences

- Rotation is no longer finished when `Rotate` returns. Status pages and `taskiem tenants keys` show the progress, and `taskiem tenants keys TENANT rotate --wait` (or `Vault.RewrapAll`) finishes it inline.
- Rolling back past migration 00100 leaves tenant keys wrapped by customer keys, and secrets sealed under the new binding, unreadable by older code.
- A customer key outage touches more than workflow secrets. While it lasts:
  - SSO client secrets, TOTP seeds, and Slack and webhook alert channels are unreadable;
  - people sign in with passwords or passkeys;
  - email and WhatsApp alerts still go out.
- Backups taken before re-wrapping finished stay readable with the platform key until they expire. This is documented for customers ([BYOK](../byok.md)), and the legal wording is a people item.
- Follow-ups: AWS STS role assumption, Google workload identity federation and Azure managed identities, external key managers (Cloud EKM, AWS XKS), keys per environment, and step-up before key operations.

## Sources

Envelope encryption and key hierarchies are from general cryptographic engineering literature and the public documentation of the KMSs involved. The client code follows only the services' public API references: the OpenBao transit and AppRole HTTP APIs, the AWS KMS API reference and the IAM guide's description of Signature Version 4, Google Cloud KMS REST and service-account OAuth documentation, and the Azure Key Vault REST and Microsoft Entra client-credentials documentation. No SDK or server source was consulted.
