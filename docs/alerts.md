# Alerts

Under **Alerts** (`alert.manage`), a tenant chooses where alerts go (channels) and what to watch (rules). The scheduler role checks rules every 30 seconds, records each finding once, and delivers it to the rule's channels.

## Channels

| Kind | Setup | What arrives |
| --- | --- | --- |
| Email | Recipients (up to 20). The deployment sets `TASKIEM_SMTP_URL` and `TASKIEM_ALERT_FROM` | A plain-text message: subject `[Taskiem] <title>`, the details, and a link to the run |
| Slack | An [incoming webhook](https://api.slack.com/messaging/webhooks) URL (`https://hooks.slack.com/services/…`). It is a credential: it is kept encrypted in the vault and never shown again | The title in bold, the details and the link |
| Webhook | An HTTPS URL. Taskiem creates a signing key and shows it once | `POST` of the alert as JSON (`id`, `kind`, `rule`, `title`, `body`, `link`, `detail`, `created_at`) |

**Send test** delivers a test message straight away and reports the error if it fails. Calls to Slack and webhooks go through the egress guard: private and metadata addresses are refused.

**Verifying a webhook.** The `Taskiem-Signature` header is `t=<unix seconds>,v1=<hex>`, where `v1` is HMAC-SHA256 with the signing key over `<t>.<raw body>`. Compare in constant time and refuse a `t` more than five minutes old. `Taskiem-Alert-Id` is the same on every retry of one alert, so use it to drop duplicates.

## Rules

| When | Default threshold | Sent once per |
| --- | --- | --- |
| A run fails | — | run |
| A run takes longer than | 1h | run |
| An approval waits longer than | 24h | approval step |
| A step needs reconciliation | — | run |
| A provider changes its responses ([contract drift](connector-sdk.md)) | — | drift finding |
| A connection or API key expires within | 7 days (`168h`) | credential and expiry date |
| The audit log is anchored | — | anchor |
| A plan limit is reached (run quota, full backlog, ingest rate, running runs, steps per run...) | — | limit and day; at most one alert per limit per day ([plan limits](operations.md#plan-limits)) |

Run and approval rules can be limited to one environment or workflow. A rule watches from when it was created (or switched back on), so it does not replay history. Thresholds are durations such as `30m`, `4h` or `72h`.

## Emailed audit anchors

An **audit anchor** rule sends each signed head of the audit chain (made daily by the scheduler, see [compliance](compliance.md)) to the rule's channels: point it at an email address outside your organisation's Taskiem administrators, such as the compliance team's mailbox. The message holds the anchor as one JSON line; saved to a file, it checks an export later: `taskiem audit verify --anchors anchors.jsonl export.jsonl`. An anchor someone else holds shows whether the log was rewritten after it was made, even by someone with access to the database.

## Delivery

A delivery that fails is retried after 1, 4, 16 and 64 minutes, then every few hours, up to eight attempts, after which it is marked failed. **Recent alerts** shows each delivery's status and last error. Creating, changing and deleting channels and rules is audited; alert bodies name workflows, environments and run ids, never run data.
