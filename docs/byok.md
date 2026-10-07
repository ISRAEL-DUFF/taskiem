# Bring your own key

With bring your own key (BYOK), the key that protects your organisation's secrets and personal data is wrapped by a key in **your** key-management service. Taskiem can use it only while you allow it. If you revoke or disable the key, Taskiem can no longer read any of it. Spec 14.1; design in [decision 0019](decisions/0019-bring-your-own-key.md).

BYOK is a plan feature (`byok`, the Enterprise plan; [billing](billing.md)). With billing off, as on a self-hosted deployment, every tenant has it. Only members with `key.manage` can see or change keys. Among the built-in roles, that is owners.

## How your data is protected

```
value (a secret, connection credentials, a BVN)
  └─ data key            AES-256-GCM, one per value (one per data subject for personal data)
       └─ tenant key     one per organisation, versioned
            └─ Taskiem's key        the platform KMS (OpenBao transit in production)
                 └─ your key        with BYOK: your OpenBao/Vault, AWS KMS, Google Cloud KMS or Azure Key Vault
```

Without BYOK, the tenant key is wrapped by Taskiem's key only.

With BYOK, every new tenant key version is wrapped by both keys: Taskiem's key wraps the tenant key, and then your key wraps the result. To unwrap the tenant key Taskiem needs both your KMS and its own:

- **Neither side alone can read your data.** Someone holding a copy of Taskiem's database and Taskiem's key still needs your key.
- **Your KMS never sees your tenant key.** It only ever handles a blob that is already encrypted by Taskiem's key.

Secrets are also bound to where they belong. The encrypted value of a secret only decrypts as that secret: the same tenant, environment, name (or connection) and id. A row moved to another name or environment in the database no longer decrypts.

## Supported key services

| Service | `provider` | Key | Taskiem signs in with | Least privilege |
| --- | --- | --- | --- | --- |
| OpenBao or HashiCorp Vault, transit engine | `vault_transit` | A transit key (`aes256-gcm96` recommended): `address`, `mount` (default `transit`), `key`, optional `namespace`, optional `ca_cert` (PEM of a private CA, added to the system roots) | `token`, or AppRole `role_id` and `secret_id` (`auth_mount`, default `approle`). Taskiem logs in again when the AppRole token is refused | A policy with `update` on `transit/encrypt/<key>` and `transit/decrypt/<key>` only |
| AWS KMS | `aws_kms` | A symmetric `ENCRYPT_DECRYPT` key: `region`, `key` (key id, key ARN, alias name or alias ARN) | `access_key_id`, `secret_access_key`, optional `session_token` | An IAM user whose only permissions are `kms:Encrypt` and `kms:Decrypt` on that key. Taskiem sends an encryption context (`taskiem:tenant`, `taskiem:purpose`), so you can require it with a `kms:EncryptionContext` condition and see each call in CloudTrail |
| Google Cloud KMS | `gcp_kms` | A symmetric key with purpose `ENCRYPT_DECRYPT`: `key` = `projects/P/locations/L/keyRings/R/cryptoKeys/K` | `service_account_json` (a service account key file) | `roles/cloudkms.cryptoKeyEncrypterDecrypter` on that key only. Taskiem sends the tenant as additional authenticated data |
| Azure Key Vault (or Managed HSM) | `azure_key_vault` | An RSA key (2048 bits or more): `address` (`https://NAME.vault.azure.net`), `key`, `key_version` (required) | An app registration: `tenant_id`, `client_id`, `client_secret` | `wrapKey` and `unwrapKey` on that key (the Key Vault Crypto User role, scoped to the key) |

**Reachability.** Taskiem calls your KMS through its egress guard. In Taskiem's cloud the address must be a public HTTPS endpoint, and you can allow only Taskiem's fixed egress IPs. Private addresses are refused. A dedicated single-tenant deployment can reach a KMS on a private network (see [Operators](#operators)). TLS is always verified. For a Vault behind a private CA, give `ca_cert`. Verification is never turned off.

**Credentials** are sealed by Taskiem's own key, bound to your organisation and to this key. They are kept apart from your vault, so no workflow, expression or code step can name them, and no page or API ever returns them. Pages show a short fingerprint so you can tell which set is stored.

## Bringing your key

In the web app: **Settings > Encryption keys > Bring your own key**. Or use the API:

```sh
curl -X PUT https://taskiem.example.com/v1/keys/byok \
  -H "Authorization: Bearer $TASKIEM_API_KEY" -H "Content-Type: application/json" -d '{
    "provider": "aws_kms",
    "region": "af-south-1",
    "key": "arn:aws:kms:af-south-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab",
    "credentials": { "access_key_id": "AKIA…", "secret_access_key": "…" }
  }'
```

1. **Verification.** Taskiem first wraps a random 32-byte value with your key and unwraps it again. If the round trip fails, nothing is stored and the answer is 422 `key_verification_failed`, with your KMS's error. A configuration problem (an `http://` address, an unknown region, a malformed key name, extra or missing credential fields) is 400 `invalid_key_config`, sent before any call.
2. **Switch.** In one transaction:
   - the key is recorded, with its sealed credentials and the verification value (the health check's canary);
   - a new tenant key version, wrapped by your key, becomes current. Everything written from then on is under it.
3. **Re-wrapping.** The scheduler's key job then re-wraps every existing data key, personal-data key and the pseudonymisation key onto the new version, in batches of 500. Values are not re-encrypted. Secrets from before migration 00100 also move to the new binding to tenant, environment and name. **Settings > Encryption keys** shows how many are left.
4. **Older versions.** Once nothing uses an older version (the one wrapped by Taskiem's key alone), it is retired. After `TASKIEM_KEY_DESTROY_AFTER` (24 hours by default) its wrapped material is destroyed. From then on, nothing in the live database can be read without your key.

**Backups** taken before step 4 still hold the older versions, wrapped by Taskiem's key alone. They stay readable that way until they expire under the backup retention policy. Ask your account team for the retention period if it matters for your assessment.

Every step is in your audit log (see [Audit](#audit)).

## Rotation

- **Tenant key.** **Rotate tenant key** (`POST /v1/keys/rotate`) adds a version wrapped by whatever is in use and re-wraps in the background as above. It needs no plan feature.
- **Your key, OpenBao/Vault and AWS.** Rotating in your KMS (transit `rotate`, AWS automatic rotation) works transparently: new wraps use the newest version, and older ciphertext still unwraps. To move Taskiem's tenant key onto the newest version of your key, rotate the tenant key afterwards.
- **Your key, Google Cloud KMS.** A new primary version is used for new wraps. Keep older versions enabled until you have rotated the tenant key and re-wrapping has finished, because disabling a version Taskiem still needs makes the key unavailable.
- **Your key, Azure Key Vault.** Wraps are pinned to `key_version`. To use a new version, bring the key again with the new `key_version` (`PUT /v1/keys/byok`). That replaces the key in use, and the old version must stay enabled until re-wrapping has finished.
- **Replacing a key.** Bringing a different key while one is in use replaces it the same way. The old key is retired once nothing depends on it, and its credentials are then forgotten.
- **Credentials.** When you rotate the token or access key Taskiem uses, send the new set with `PUT /v1/keys/byok/credentials` (or **Replace credentials**). The new set must unwrap the onboarding canary, which proves it reaches the same key. Otherwise the answer is 422 and the old set stays.

## When the key is unavailable

If you revoke Taskiem's access, disable or delete the key, or your KMS cannot be reached, Taskiem **fails closed**: it encrypts and decrypts nothing more for your organisation.

**How fast.** A process keeps an unwrapped tenant key in memory for at most `TASKIEM_BYOK_CACHE_TTL` (5 minutes by default; the page shows the value). Within that bound every process stops. A process without the key cached stops at once. After a failure, a process does not ask your KMS again for 15 seconds, so reads fail at once instead of each waiting for a timeout.

**What stops:**

- reading a secret or connection credentials, and writing them (the API answers 503 `key_unavailable` with `Retry-After`);
- sealing or opening personal data: starting a run whose input has personal data, an approval whose subject is personal data, reports that open it;
- verifying webhooks signed with a secret in your vault. These deliveries get 503, so providers retry them;
- single sign-on (the client secret is in your vault) and TOTP step-up. People can still sign in with a password or passkey;
- Slack and webhook alert channels, because their URL and signing key are in your vault. Email and WhatsApp alerts still go out.

**What happens to runs.** A step that needs a secret, credentials or personal data **pauses rather than fails**:

- It is recorded as failed with kind `key_unavailable` and parks. The run shows **needs reconciliation**, with the message "the tenant's encryption key is unavailable …".
- Nothing is lost. A step parked before sending was never sent. A step parked while checking an earlier attempt is checked again first, before anything is resent.
- The step's retry budget is not spent, so even a code step with no retries survives.
- A step whose **result** holds personal data cannot be recorded either, because recording it seals that data. Taskiem treats it as if the worker had stopped in the middle of the call. A read is repeated. A write is checked with the provider, or parked for a person if the provider has no way to check. Either way this waits for the key to return, and a write is never sent twice.
- Steps that need no keys, approvals that need no personal data, timers and signals carry on.

**Health checks and alerts.** The key job checks your key every minute (`TASKIEM_KEY_CHECK_INTERVAL`) by unwrapping the canary. After two failures in a row, the key is marked **unavailable**. **Settings > Encryption keys** then shows when it started failing and your KMS's error, and the change is audited (`key.byok_unavailable`).

Create an alert rule of kind **"Your own encryption key (BYOK) is unavailable, or works again"** (`key_health`; [alerts](alerts.md)) with an email or WhatsApp channel. It alerts once per outage and once per recovery. A Slack or webhook channel cannot deliver while the key is unavailable. **Check now** (`POST /v1/keys/byok/check`) checks immediately.

**Recovery.** Re-enable the key or restore Taskiem's access. At the next check, at most a minute later, or at once with **Check now**:

- the key is marked active again (`key.byok_recovered`, with how long it was down);
- parked steps resume on their own (`key.steps_resumed`): steps that sent nothing are retried, and steps that were checking an earlier attempt check again first;
- runs continue from where they paused.

**Permanent revocation.** If the key never returns, your secrets, connection credentials and personal data cannot be recovered by anyone, including Taskiem. That includes personal data in archived run history and in backups taken after re-wrapping finished. The rest of the record stays readable and the audit chain still verifies: workflow definitions, run history without sealed values, the audit log, and invoices. Runs that were paused stay paused until someone cancels them.

## Returning to Taskiem's key

**Return to Taskiem's key** (`DELETE /v1/keys/byok`) works even without the plan feature, so an organisation can always leave:

- It retires your key and makes a new tenant key version, wrapped by Taskiem's key alone, current.
- Re-wrapping onto it still needs your key, to unwrap the versions your key wrapped. Keep your key available until **Settings > Encryption keys** shows no re-wrapping left.
- Once re-wrapping is done, Taskiem forgets your key's credentials.

Moving to a plan without `byok` is refused while your key is in use. The downgrade blocker says to return to Taskiem's key first.

## Audit

| Action | When |
| --- | --- |
| `key.byok_enabled` | A key was brought or replaced: provider, the key's description, the new tenant key version, the credentials' fingerprint, the key it replaced |
| `key.byok_credentials_replaced` | New credentials for the key in use |
| `key.byok_checked` | Someone checked the key (the key job's own checks are not logged unless the status changes) |
| `key.byok_unavailable`, `key.byok_recovered` | The status changed (once per outage) |
| `key.byok_disabled` | Returned to Taskiem's key |
| `key.byok_forgotten` | A retired key's credentials were erased once nothing depended on it |
| `secret.rotate_key` | A tenant key rotation |
| `key.rewrap`, `key.rewrap_completed`, `key.versions_destroyed` | Background re-wrapping progress, retired versions, destroyed versions |
| `key.steps_resumed` | Steps parked for the key were resumed: how many, which runs |

Operators' actions (`taskiem tenants keys`) appear in your log as `platform_admin` with their name.

## Operators

| Variable | Default | Notes |
| --- | --- | --- |
| `TASKIEM_BYOK_CACHE_TTL` | `5m` | How long an unwrapped tenant key that depends on a customer key stays in memory (1s to 1h). It is the bound on how long revocation takes. Shorter means more calls to customers' KMSs |
| `TASKIEM_KEY_CHECK_INTERVAL` | `1m` | How often the scheduler's key job checks customer keys, re-wraps and resumes parked steps |
| `TASKIEM_KEY_DESTROY_AFTER` | `24h` | How long a retired, unused tenant key version is kept before its material is destroyed |
| `TASKIEM_BYOK_ALLOW_PRIVATE` | off | Let customer KMS addresses resolve to private (RFC 1918, ULA) ranges. **Dedicated single-tenant deployments only.** Loopback, link-local and metadata addresses stay refused |

The key job runs in the `scheduler` role. Set the cache TTL on every role, because the API, edge and worker roles all unwrap keys.

**CLI.** `taskiem tenants keys TENANT_ID` shows a tenant's versions, what wraps them, the customer key's health, re-wrapping progress and parked steps. `rotate [--wait]`, `rewrap` and `check` (which also resumes parked steps) act on them. Customer keys are brought, replaced and removed by the tenant's owners, never by the CLI.

**Metrics.** `taskiem_tenant_key_checks_failed_total{wrapped_by="customer"|"platform"}` counts failed key checks. Customer keys failing are the customer's business, but a jump in `platform` means Taskiem's own KMS is failing.

**Dedicated deployments.** A single-tenant deployment ([Kubernetes](kubernetes.md#dedicated-single-tenant-deployments)) can go further: its platform KMS can itself be the customer's OpenBao (`TASKIEM_KMS=openbao`, `TASKIEM_OPENBAO_ADDR`), so every key in the deployment is the customer's. Per-tenant BYOK still works on top of that.

## What BYOK does not do

- **It does not hide data from Taskiem while the key is enabled.** Running workflows needs the data. Taskiem's processes hold unwrapped keys in memory, for at most the cache TTL after the last use. BYOK protects against copies of the database and backups, a compromise of Taskiem's KMS alone, and it gives you the switch to stop everything.
- **It does not cover workflow definitions, run history (other than sealed personal data), audit logs or invoices.** These are not secrets and stay readable so that the record survives.
- **Not built yet:**
  - signing in to AWS by assuming a role in your account (STS) instead of an IAM user's keys;
  - Google workload identity federation and Azure managed identities;
  - external key managers (Cloud EKM, AWS XKS);
  - a separate key per environment;
  - step-up (passkey) before key operations.

  See the [Phase 4 status](phase-4-status.md#p4-5-enterprise).
