# Compliance reports and audit anchoring

## Reports

Members with `audit.read` (owners, admins, auditors) get five reports over a period, under **Reports** or `GET /v1/reports/{kind}?from=YYYY-MM-DD&to=YYYY-MM-DD` (`&format=csv` to download). Viewing or downloading a report is itself audited. Reports carry ids, amounts and actions, never personal values; CSV cells that a spreadsheet would read as a formula are escaped.

| Report | Rows | Summary |
| --- | --- | --- |
| `approvals` | Every approval decision: approver, whom they covered for under a delegation, decision, level, step-up, channel, workflow, run, step, policy, amount and band | Count and total by approver, band and decision |
| `effects` | Writes parked for an operator, results settled by reconciliation with the provider, and operator resolutions with their notes | Counts by outcome |
| `pii` | Every reveal of personal data in run history, and every erasure: who, when, from where | Counts by person |
| `changes` | Every workflow version: who wrote and published it, its Git commit or pull request, its digest, and what changed from the version before | — |
| `chain` | The signed anchors made in the period, with their signatures checked | The chain verified now, in the database: intact or the first broken entry |

**Amount bands.** The amount is the first number in the approval's subject under a field named for an amount (`amount`, `total`, or ending in `_kobo`, converted to naira). Bands: under ₦100k, ₦100k–₦1m, ₦1m–₦10m, ₦10m and over. Name subject fields so the report can find them.

## Audit-chain anchoring

The audit log is a hash chain: changing or removing any entry breaks every hash after it, which `GET /v1/audit/verify` and `taskiem audit verify FILE` detect. Someone with write access to the database could still rewrite an entry and recompute every hash after it. Anchors close that gap (spec 9.2).

Once a day, the scheduler role signs each tenant's chain head (sequence number and hash) with the platform's Ed25519 key (`TASKIEM_ANCHOR_KEY`), appends the signed anchor to `TASKIEM_ANCHOR_DIR/<tenant>.jsonl`, and records it in the database, where anchors are append-only. Point the directory at write-once storage: the anchors outside the database are the ones that count.

To check an export against them:

```sh
curl -H "Authorization: Bearer $KEY" https://taskiem.example/v1/audit/export > audit.jsonl
curl -H "Authorization: Bearer $KEY" https://taskiem.example/v1/audit/anchors > anchors.json   # or the write-once copy
taskiem audit verify --anchors anchors.json --key "$(jq -r .public_key anchors.json)" audit.jsonl
```

A chain rewritten after an anchor fails with "the chain was rewritten after it", even when every hash in the rewritten chain is consistent. Compare against the copy held outside the platform; the copy the API serves is only as trustworthy as the database it comes from.

## Not yet

- Emailing anchors to a tenant's compliance contact waits for email delivery (Phase 2, milestone 5).
- Streaming the audit log to a SIEM (syslog, webhook, S3) is not built; exports are pulled.
- The audit log is kept indefinitely; there is no deletion after its minimum retention.
