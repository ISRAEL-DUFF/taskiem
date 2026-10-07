# Compliance reports and audit anchoring

## Reports

Members with `audit.read` (owners, admins, auditors) get six reports over a period, under **Reports** or `GET /v1/reports/{kind}?from=YYYY-MM-DD&to=YYYY-MM-DD` (`&format=csv` to download). Viewing or downloading a report is itself audited. Reports carry ids, amounts and actions, never personal values; CSV cells that a spreadsheet would read as a formula are escaped.

| Report | Rows | Summary |
| --- | --- | --- |
| `approvals` | Every approval decision: approver, whom they covered for under a delegation, decision, level, step-up, channel, workflow, run, step, policy, amount and band | Count and total by approver, band and decision |
| `effects` | Writes parked for an operator, results settled by reconciliation with the provider, and operator resolutions with their notes | Counts by outcome |
| `pii` | Every reveal of personal data in run history, and every erasure: who, when, from where | Counts by person |
| `changes` | Every workflow version: who wrote and published it, its Git commit or pull request, its digest, and what changed from the version before | — |
| `chain` | The signed anchors made in the period, with their signatures checked | The chain verified now, in the database: intact or the first broken entry |
| `secret-use` | Reads of each secret and connection per day: count, last read, purposes ([secret use](#secret-use)) | Reads by kind; secrets and connections not used in the period, with their last use; the hourly digests checked against the reads |

**Amount bands.** The amount is the first number in the approval's subject under a field named for an amount (`amount`, `total`, or ending in `_kobo`, converted to naira). Bands: under ₦100k, ₦100k–₦1m, ₦1m–₦10m, ₦10m and over. Name subject fields so the report can find them.

## Audit-chain anchoring

The audit log is a hash chain: changing or removing any entry breaks every hash after it, which `GET /v1/audit/verify` and `taskiem audit verify FILE` detect. Someone with write access to the database could still rewrite an entry and recompute every hash after it. Anchors close that gap (spec 9.2).

Once a day, the scheduler role signs each tenant's chain head (sequence number and hash) with the platform's Ed25519 key (`TASKIEM_ANCHOR_KEY`), appends the signed anchor to `TASKIEM_ANCHOR_DIR/<tenant>.jsonl`, and records it in the database, where anchors are append-only. Point the directory at write-once storage: the anchors outside the database are the ones that count. Tenants can also have each anchor emailed to them as it is made, so a copy sits outside the platform altogether: an **audit anchor** [alert rule](alerts.md#emailed-audit-anchors).

To check an export against them:

```sh
curl -H "Authorization: Bearer $KEY" https://taskiem.example/v1/audit/export > audit.jsonl
curl -H "Authorization: Bearer $KEY" https://taskiem.example/v1/audit/anchors > anchors.json   # or the write-once copy
taskiem audit verify --anchors anchors.json --key "$(jq -r .public_key anchors.json)" audit.jsonl
```

A chain rewritten after an anchor fails with "the chain was rewritten after it", even when every hash in the rewritten chain is consistent. Compare against the copy held outside the platform; the copy the API serves is only as trustworthy as the database it comes from.

## Secret use

Every time the platform decrypts a tenant secret or a connection's credentials to use it, it records the read (spec 14.1): which secret or connection, in which environment, for what purpose, by which run, step and attempt, and when. The value is never recorded, nor any hash of it. The read is written in the same transaction that decrypts, so a value the vault hands out always has its record; if the record cannot be written, the decryption fails.

| Read path | Kind | Purpose | Run, step, attempt |
| --- | --- | --- | --- |
| A step's `secrets.x` expressions (any step type) | `secret` | `step.<type>`, e.g. `step.http` | Yes |
| A code step's declared `secrets` | `secret` | `step.code` | Yes |
| A connector step's connection, built-in or the tenant's own WebAssembly connector | `connection` | `step.connector`, or `step.reconcile` when the step asks the provider what happened | Yes |
| Webhook trigger keys, connector webhook credentials, Git push webhook secrets, checked on delivery | `webhook`, `connection`, `git` | `ingest.verify` | No |
| Git credentials for a sync, a pull request proposal, or reading the head a push names; stored credentials reused when a connection is changed or approved | `git` | `git.sync`, `git.connect` | No |
| Alert channels' Slack URL or webhook signing key | `alert_channel` | `alert.deliver` | No |
| SSO client secrets and TOTP seeds | `identity` | `sso.login`, `totp.enrol`, `totp.verify` | No |

Each secret is decrypted at most once per step attempt, so a step that retries three times records three reads. Writing a secret, deleting one, or checking that one exists decrypts nothing and records nothing (writes and deletions are in the audit log as `secret.write` and `secret.delete`). There is no API that reveals a secret's value.

**Where to see it.** `GET /v1/secrets/reads` (permission `audit.read`) lists reads newest first, filtered by `name`, `connection` (id), `run`, `env`, `kind`, `since` and `until` (RFC 3339 or a date), `limit` (default 100, at most 1000). A key limited to one environment sees only that environment's reads (and is refused another `env`); platform reads (Git, alert channels, identity) are in reserved environments (`_git`, `_alerts`, `_identity`) and visible to tenant-wide callers only. The Secrets and Connections pages show each one's last use, and a run's page lists the secrets each step used. The `secret-use` report gives reads per secret per day with their purposes, the secrets and connections not used in the period (with their last use ever), and the digest check below.

**Why not the audit log.** A busy tenant decrypts thousands of times a minute; one chained entry per read would make the chain mostly secret reads and serialise every step on the tenant's chain head. Reads go to their own table, `secret_reads`, which the application role may insert into and read but never update or delete (and a trigger refuses updates from anyone). Tenants are isolated by row-level security like every other table.

**Digests in the chain.** The scheduler role digests each closed hour (15 minutes after it ends) into each tenant's audit chain as one `secret.read.digest` entry: the number of reads, the count per secret, and a SHA-256 over every column of every read that hour, in order. The chain, and its anchors outside the database, then hold what the reads were: a read removed, changed or added afterwards no longer matches. The `secret-use` report recomputes every digest in the period from the rows and reports `ok`, `mismatch`, `purged` (removed by retention, as expected) or `undigested` (hours the job has not reached). The digest covers the reads, not their absence: someone with write access to the database could still remove reads of the current hour before it is digested.

**Retention.** Reads are kept at least 400 days, longer than any run's payload retention: they are security evidence, carry no personal data, and outlive the runs they name. Only reads whose hour is digested are purged; the digest stays in the chain for good.

## Not yet

- Emailing anchors to a tenant's compliance contact waits for email delivery (Phase 2, milestone 5).
- Streaming the audit log to a SIEM (syslog, webhook, S3) is not built; exports are pulled.
- The audit log is kept indefinitely; there is no deletion after its minimum retention.
