# Payrolla webhooks

Payrolla sends an HTTPS `POST` to your URL when something happens to a company's payroll. This guide covers subscribing, verifying each request, retries, and catching up with replay.

**Base URL:** `https://synledger.name.ng/payrolla/api/v1` (for example `https://synledger.name.ng/payrolla/api/v1`). Every path below is relative to it.

The schema for every event body is in [`payrolla-connect-v1.yaml`](payrolla-connect-v1.yaml), under `webhooks`. It is also served at `GET /connect/openapi.yaml`.

## Subscribe

You need a key with `webhooks:manage`, plus the read scope for the events you want:
- `payroll:read` for `run.*`, `payout.*` and `payslip.issued`;
- `employees:read` for `employee.*`.

```http
POST /connect/webhooks
Authorization: Bearer pyk_live_…
Content-Type: application/json

{ "url": "https://hooks.example.com/payrolla", "event_types": ["run.computed", "payout.failed"] }
```

Use `["*"]` for every event type the key may receive, including types added later.

The response holds `signing_secret`. It is shown once. Store it next to the receiver, the way you store the API key. If you lose it, delete the subscription and create a new one.

To check the receiver works, send it a `ping`. It goes to this subscription only:

```http
POST /connect/webhooks/{id}/test
```

## What arrives

```http
POST /payrolla HTTP/1.1
Content-Type: application/json
User-Agent: Payrolla-Webhooks/1
Payrolla-Event-Id: 01J…
Payrolla-Event-Type: payout.failed
Payrolla-Signature: t=1792700854,v1=5f2b…

{"id":"01J…","type":"payout.failed","api_version":"2026-11-01","created_at":"2026-10-22T10:01:12.345Z","company_id":"…","data":{…}}
```

| Envelope field | Meaning |
|---|---|
| `id` | Unique per event. The same event keeps its id across retries, resends and replay. |
| `type` | One of the event types below, or `ping`. |
| `api_version` | The shape of `data`. See [Versioning](#versioning). |
| `created_at` | When it happened (UTC). |
| `company_id` | The Payrolla company. With one key per company, this always matches your key. |
| `data` | Depends on `type`. |

### Event types

| Type | When | `data` |
|---|---|---|
| `run.computed` | A run was calculated and is waiting for approval | `run`, `approval_url` |
| `run.approved` | An approver approved it | `run`, `approved_by` |
| `run.rejected` | An approver sent it back | `run`, `note`, `rejected_by` |
| `run.locked` | It was locked; payslips are final | `run` |
| `run.cancelled` | It was cancelled | `run` |
| `run.disbursement_started` | Payouts went to the payment provider | `run`, `wallet_count`, `bank_count` |
| `run.completed` | Every payout succeeded | `run`, `paid_count`, `failed_count` |
| `run.partially_completed` | Some payouts failed | `run`, `paid_count`, `failed_count` |
| `payout.succeeded` | One employee was paid | `run_id`, `payslip_id`, `employee_id`, `amount_kobo`, `method`, `reference` |
| `payout.failed` | One payout failed | as above, plus `reason` |
| `payout.reversed` | A payout that had succeeded was returned by the bank. The employee is not paid. | as `payout.succeeded` |
| `employee.created` | An employee was added | `employee` |
| `employee.updated` | A field you can see changed | `employee`, `changed_fields` |
| `employee.terminated` | An employee was terminated | `employee`, `changed_fields` |
| `payslip.issued` | A payslip went to the employee | `payslip_id`, `employee_id`, `run_id`, `period` |

**A reversal changes a run's outcome.** When a payout is returned after the run finished, Payrolla sends `payout.reversed` followed by `run.partially_completed`, even if `run.completed` was sent earlier. Always use the latest run event.

A payout the bank returns *before* it went through is reported as `payout.failed`, and can be retried.

`run` is a summary with these fields: `run_id`, `period_start`, `period_end`, `pay_date`, `run_type`, `status`, `employee_count`, `total_gross_kobo`, `total_net_kobo`.

`employee` has only these fields: `id`, names, `email`, `phone`, `department`, `status`, `employment_type`, `start_date`, `pay_method`, `external_ref`.

**Never in any event:** bank account numbers, salary breakdowns, tax or ID numbers, or earned-wage-access data. For one employee's net pay and payout status, use `GET /connect/runs/{id}/lines`.

**`approval_url`** opens the run's review in Payrolla. It carries no credential, so you can put it in a message to the approver. They still sign in and confirm with their own factor; there is no API to approve.

## Verify every request

Do not trust a request until you have checked its signature. The header looks like this:

```
Payrolla-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256>
```

The HMAC is computed with your signing secret over the string `"<t>.<raw body>"`. To verify:

1. Read the **raw** request body as bytes, before any JSON parsing. Re-serialised JSON will not match.
2. Compute `HMAC-SHA256(secret, t + "." + body)` and hex-encode it.
3. Compare it with `v1` in constant time.
4. Reject the request if `t` is more than 5 minutes from your clock.
5. De-duplicate on `id`. Retries, resends and replay can deliver the same event more than once.

### Node.js

```js
import crypto from 'node:crypto'
import express from 'express'

const app = express()
const SECRET = process.env.PAYROLLA_WEBHOOK_SECRET

app.post('/payrolla', express.raw({ type: 'application/json' }), (req, res) => {
  const header = req.get('Payrolla-Signature') ?? ''
  const parts = Object.fromEntries(header.split(',').map(p => p.split('=', 2)))
  const t = Number(parts.t)
  if (!t || Math.abs(Date.now() / 1000 - t) > 300) return res.sendStatus(400)

  const expected = crypto.createHmac('sha256', SECRET)
    .update(`${parts.t}.`).update(req.body).digest('hex')
  const given = Buffer.from(parts.v1 ?? '', 'hex')
  if (given.length !== 32 || !crypto.timingSafeEqual(given, Buffer.from(expected, 'hex'))) {
    return res.sendStatus(400)
  }

  const event = JSON.parse(req.body.toString('utf8'))
  // if (alreadyHandled(event.id)) return res.sendStatus(200)
  queue.push(event)          // do the work after answering
  res.sendStatus(200)
})
```

### Python

```python
import hashlib, hmac, json, os, time
from flask import Flask, request, abort

app = Flask(__name__)
SECRET = os.environ["PAYROLLA_WEBHOOK_SECRET"].encode()

@app.post("/payrolla")
def payrolla():
    header = request.headers.get("Payrolla-Signature", "")
    parts = dict(p.split("=", 1) for p in header.split(",") if "=" in p)
    try:
        t = int(parts["t"])
    except (KeyError, ValueError):
        abort(400)
    if abs(time.time() - t) > 300:
        abort(400)

    body = request.get_data()  # raw bytes
    expected = hmac.new(SECRET, f"{t}.".encode() + body, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, parts.get("v1", "")):
        abort(400)

    event = json.loads(body)
    # if already_handled(event["id"]): return "", 200
    enqueue(event)  # do the work after answering
    return "", 200
```

### Test vector

Use this to check your code before connecting to Payrolla:

| Input | Value |
|---|---|
| secret | `whsec_test` |
| t | `1700000000` |
| body | `{"id":"evt_1","type":"ping"}` |
| header | `t=1700000000,v1=33ff6664879612e191c4e01184f3da32597a3c9b33ca2655fac317d01c5abc3f` |

Payrolla's own tests check this vector, so it will stay correct.

## Answering

| You answer | Payrolla does |
|---|---|
| Any `2xx` within 10 seconds | Marks it delivered |
| `410 Gone` | Switches the subscription off; nothing more is sent to it |
| Anything else, or a timeout | Retries |

Answer first and do the work afterwards. A slow handler causes timeouts, and timeouts cause duplicate deliveries.

## Retries

After a failed attempt, Payrolla waits 1 minute, then 5 minutes, 30 minutes, 2 hours, 6 hours, 12 hours and 24 hours. If the last of those also fails, the delivery is marked `DEAD`.

- **`Retry-After`:** if you send it (in seconds, or as an HTTP date) and it is longer than the next wait, Payrolla waits that long instead.
- **Identical body:** every retry carries exactly the same body bytes. The signature is fresh, because `t` changes.
- **Switched off after 3 days:** a subscription that has failed continuously for 3 days is switched off. The company owner sees it in Payrolla → Settings → Connected apps and can turn it back on. Owners can also pause a subscription there, resend a delivery, and send a test.

You can inspect and resend deliveries yourself:

```http
GET  /connect/webhooks/{id}/deliveries
POST /connect/webhooks/{id}/deliveries/{deliveryId}/resend
```

## Replay: catching up

`GET /connect/events` returns the company's events from the last 30 days, oldest first. Each item is the exact envelope a webhook would carry. Use it:
- after downtime, to fill any gap;
- instead of webhooks, if you prefer polling.

```http
GET /connect/events?after=<last event id you processed>&types=payout.failed,run.completed&limit=100
```

Keep reading while `next_cursor` is not null, passing it as `after`. Replay returns only the types your key's scopes allow. It does not include `ping`.

A safe pattern is to store the `id` of the last event you processed. On start-up, and every few minutes, replay from that id. Your handler de-duplicates anyway, so overlap with webhooks costs nothing.

## Versioning

`api_version` (currently `2026-11-01`) names the shape of `data`.

- **Within a version:** Payrolla may add new event types; ignore types you don't know. A `"*"` subscription receives new types automatically.
- **New version:** removing or renaming a field, or adding one to `data`, means a new `api_version`. The old shape keeps being sent until you move.
- **Reading safely:** branch on `type` and read only the fields you need.

## Security checklist

- Keep the signing secret and the API key on your server. Never put them in a browser, an app, email, chat or a ticket.
- Use HTTPS only. Payrolla refuses other URLs.
- Verify the signature and the timestamp on every request.
- De-duplicate on `id`.
- Treat `approval_url` as safe to share, and payslip links (`POST /connect/payslips/{id}/link`) as secret until they reach the employee.
