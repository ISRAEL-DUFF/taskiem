# Payrolla connector for Taskiem: integration guide

This guide is everything needed to build Taskiem's Payrolla connector and take it to production: authentication, triggers, actions, embedding and the workflows customers will want first. The exact request and response shapes are in `api/payrolla-connect-v1.yaml`.

| | |
|---|---|
| API base | `https://synledger.name.ng/payrolla/api/v1` |
| OpenAPI | `api/payrolla-connect-v1.yaml`, also live at `https://synledger.name.ng/payrolla/api/v1/connect/openapi.yaml` |
| Payrolla app (where owners sign in) | `https://payroller.surge.sh` |
| Embed SDK | `https://synledger.name.ng/payrolla/widget/sdk/payroller-embed.umd.js` (or `.mjs`) |
| Support, including payroll days | duffisrael29@gmail.com |

## 1. The model in one minute

- **One connection per company.** Each Payrolla company that uses Taskiem has its own key. That matches one Taskiem sub-tenant per customer: a key reads and receives only its own company, and can never see another.
- **The owner creates the key in Payrolla** and enters it into Taskiem's connection screen. Payrolla never sends a key to Taskiem, and Taskiem never sends one to us.
- **Nothing in the API moves money, approves payroll or changes employees.** People do that in Payrolla, or inside the embed with their own confirmation. Taskiem's job is to react, notify and link.
- **Every response is wrapped:** `{"data": …}` on success, `{"error": {"code", "message"}}` on failure. Branch on `error.code`.
- **Money and time:** amounts are integers in kobo (₦1 = 100 kobo). Dates are `YYYY-MM-DD`; times are RFC 3339 in UTC.

## 2. Connection (authentication)

**Fields on Taskiem's "Connect Payrolla" screen:**
- **API key:** required and secret. It starts with `pyk_live_`. Store it like a password.

**Authenticating:** send the key as `Authorization: Bearer pyk_live_…`.

**Testing the connection.** Call `GET /connect/company`. Use the company name as the connection's label, and check the scopes against what your workflows need.

```http
GET https://synledger.name.ng/payrolla/api/v1/connect/company
Authorization: Bearer pyk_live_…
```
```json
{ "data": {
    "company": { "id": "0b6e…", "name": "Acme Foods Ltd", "pay_frequency": "MONTHLY" },
    "key": { "id": "4f9a…", "name": "Taskiem", "kind": "CONNECTED_APP",
             "scopes": ["payroll:read", "employees:read", "webhooks:manage", "payslips:links", "embed:launch"] } } }
```

| Scope | Unlocks |
|---|---|
| `payroll:read` | Run triggers and actions, payout triggers, `payslip.issued` |
| `employees:read` | Employee triggers and actions |
| `webhooks:manage` | Creating and deleting the trigger subscriptions |
| `payslips:links` | The "Get payslip link" action. The owner must also switch payslip links on. |
| `embed:launch` | Embedding Payrolla screens in Taskiem |

**When a key stops working.** A revoked key gets `401 API_KEY_INVALID`. Mark the connection disconnected and ask the customer to reconnect; don't retry.

## 3. Triggers

### Recommended: one subscription per connection

Payrolla subscriptions are REST hooks. When a customer connects, create a single subscription for everything the key can receive, and route events to workflows inside Taskiem:

```http
POST https://synledger.name.ng/payrolla/api/v1/connect/webhooks
Authorization: Bearer pyk_live_…
Content-Type: application/json

{ "url": "https://hooks.taskiem.com/payrolla/<your-connection-id>", "event_types": ["*"] }
```
```json
{ "data": {
    "webhook": { "id": "7c1d…", "url": "https://hooks.taskiem.com/payrolla/…", "event_types": ["*"], "status": "ACTIVE", "created_at": "…" },
    "signing_secret": "whsec_…" } }
```

- **Storing it:** keep `webhook.id` and `signing_secret` with the connection. The secret is shown only once.
- **Coverage:** `"*"` covers every event the key's scopes allow, including types we add later.
- **Unsubscribing:** when the customer disconnects, call `DELETE /connect/webhooks/{id}`.
- **Testing:** `POST /connect/webhooks/{id}/test` sends a `ping` to that subscription only. Use it as a "Test trigger" button.

Prefer one subscription per workflow trigger? That works too: use the event type instead of `"*"`.

### Verifying every delivery

Each request carries:
- `Payrolla-Event-Id`;
- `Payrolla-Event-Type`;
- `Payrolla-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, "<t>.<raw body>")>`.

To accept a delivery:
1. Verify the signature over the **raw** bytes.
2. Reject a timestamp more than 5 minutes old.
3. De-duplicate on the event `id`.

`api/webhooks.md` has Node and Python code and a test vector.

**Answering:**
- Answer `2xx` within 10 seconds, then do the work.
- Anything else is retried after 1 minute, then 5 minutes, 30 minutes, 2 hours, 6 hours, 12 hours and 24 hours.
- `410 Gone` switches the subscription off for good. Return it only when the connection no longer exists.

### Catching up, or polling instead

`GET /connect/events?after=<last event id>` returns the last 30 days of events, oldest first. Each one has exactly the same body as the webhook. Keep paging while `next_cursor` isn't null.

Store the last event id you processed per connection, and replay from it after any downtime. If Taskiem's trigger model prefers polling, use this endpoint as the polling trigger.

### Trigger catalogue

| Trigger | Event | Useful fields in `data` |
|---|---|---|
| Payroll ready for approval | `run.computed` | `run`, `approval_url` |
| Payroll approved | `run.approved` | `run`, `approved_by` |
| Payroll sent back | `run.rejected` | `run`, `note`, `rejected_by` |
| Payroll locked | `run.locked` | `run` |
| Payroll cancelled | `run.cancelled` | `run` |
| Payments started | `run.disbursement_started` | `run`, `wallet_count`, `bank_count` |
| Payroll fully paid | `run.completed` | `run`, `paid_count`, `failed_count` |
| Payroll paid with failures | `run.partially_completed` | `run`, `paid_count`, `failed_count` |
| Employee paid | `payout.succeeded` | `run_id`, `payslip_id`, `employee_id`, `amount_kobo`, `method`, `reference` |
| Employee payment failed | `payout.failed` | as above, plus `reason` |
| Payment returned by bank | `payout.reversed` | as `payout.succeeded`; the employee is **not** paid |
| Employee added | `employee.created` | `employee` |
| Employee changed | `employee.updated` | `employee`, `changed_fields` |
| Employee left | `employee.terminated` | `employee`, `changed_fields` |
| Payslip issued | `payslip.issued` | `payslip_id`, `employee_id`, `run_id`, `period` |

**`run`** has these fields: `run_id`, `period_start`, `period_end`, `pay_date`, `run_type` (`REGULAR` or `OFF_CYCLE`), `status`, `employee_count`, `total_gross_kobo`, `total_net_kobo`.

**`employee`** has these fields: `id`, `first_name`, `last_name`, `email`, `phone`, `department`, `status`, `employment_type`, `start_date`, `pay_method` (`WALLET`, `BANK` or `NONE`), and `external_ref`.

**Never included:** bank account numbers, salary breakdowns or ID numbers.

**Sample: `run.computed`**
```json
{ "id": "01J9Z…", "type": "run.computed", "api_version": "2026-11-01", "created_at": "2026-10-25T09:12:03.120Z",
  "company_id": "0b6e…",
  "data": {
    "run": { "run_id": "5d2a…", "period_start": "2026-10-01", "period_end": "2026-10-31", "pay_date": "2026-10-28",
             "run_type": "REGULAR", "status": "COMPUTED", "employee_count": 48,
             "total_gross_kobo": 1240000000, "total_net_kobo": 920000000 },
    "approval_url": "https://payroller.surge.sh/runs/5d2a…/review" } }
```

**Sample: `payout.failed`**
```json
{ "id": "01J9Z…", "type": "payout.failed", "api_version": "2026-11-01", "created_at": "…", "company_id": "0b6e…",
  "data": { "run_id": "5d2a…", "payslip_id": "a81c…", "employee_id": "e3f0…", "amount_kobo": 18500000,
            "method": "BANK", "reference": "NIP-…", "reason": "beneficiary account is dormant" } }
```

**Order of events.** A run produces events in its natural order:
1. `computed`;
2. `approved` (or `rejected`);
3. `locked`;
4. `disbursement_started`;
5. one `payout.*` per employee;
6. `completed` or `partially_completed`;
7. `payslip.issued`.

Delivery isn't strictly ordered, so use `created_at` and the run's `status`. If a payout is reversed after the run finished, `payout.reversed` is followed by a fresh `run.partially_completed`. Always trust the latest run event.

## 4. Actions

| Action | Request | Scope |
|---|---|---|
| Find payroll runs | `GET /connect/runs?status=COMPUTED,APPROVED&updated_since=…&limit=50` | `payroll:read` |
| Get a payroll run | `GET /connect/runs/{runId}` | `payroll:read` |
| Get each employee's pay status in a run | `GET /connect/runs/{runId}/lines` | `payroll:read` |
| Get the approval link | `GET /connect/runs/{runId}/approval-link` | `payroll:read` |
| Find employees | `GET /connect/employees?status=ACTIVE&limit=100` | `employees:read` |
| Get an employee | `GET /connect/employees/{employeeId}` | `employees:read` |
| Get a payslip link for an employee | `POST /connect/payslips/{payslipId}/link` | `payslips:links` |
| Open Payrolla in Taskiem (launch token) | `POST /connect/embed/launch-token` | `embed:launch` |

- **Paging:** lists return `next_cursor`. Pass it back as `cursor`; an empty string means the end.
- **Dropdowns:** use "Find payroll runs" and "Find employees" for dynamic dropdowns.
- **Run lines:** each line has the employee's name, `net_kobo`, `disbursement_status` (`CONFIRMED`, `FAILED`, `REVERSED`, …), the provider `reference` and the failure `reason`. That's enough for a "who wasn't paid" message without any bank details.

**Payslip links.**
- **Finding the `payslip_id`:** it comes from `payslip.issued`, or from the run's lines.
- **What you get:** `{ "url", "expires_at" }`. The link lasts 7 days, and the employee confirms the last four digits of their phone before it opens.
- **Before the owner allows it:** the call returns `403 PAYSLIP_LINKS_DISABLED`. Show the customer "Turn on payslip links in Payrolla → Settings → Connected apps".
- **Before the run is locked:** the call returns `409 PAYSLIP_NOT_FINAL`.
- **Handling:** treat the URL as private until it reaches the employee.

## 5. Ready-made workflows

1. **Approve payroll on WhatsApp.** On `run.computed`, send each approver something like: "October payroll is ready: ₦9.2m for 48 people. Review and approve: `approval_url`".
   - **The link:** it opens the run in Payrolla, and the approver confirms with their own factor.
   - **Reminders:** if no `run.approved` arrives by the day before `pay_date`, send a reminder.
2. **Failed payment alert.** On `payout.failed`, tell finance who wasn't paid, the amount and the `reason`. Use the employee from `GET /connect/employees/{id}`.
   - **Daily digest:** on `run.partially_completed`, list everyone who wasn't paid from `/runs/{id}/lines`.
3. **Payslips to employees.** On `payslip.issued`, call the payslip-link action and send the link to the employee's `phone`.
   - **Prerequisites:** payslip links switched on, and the employee's consent to WhatsApp messages.
4. **New starter onboarding.** On `employee.created`, create onboarding tasks in Taskiem. On `employee.terminated`, create offboarding tasks.
5. **Payroll calendar.** On `run.approved` or `run.locked`, post to the team channel. On `run.completed`, confirm "everyone is paid".

## 6. Embedding Payrolla in Taskiem

Embedding works only for companies whose owner has allowed it. The owner sets this up in Payrolla → Settings → Connected apps → Taskiem → Embedding:
- **Allowed origins:** Taskiem's origins, for example `https://app.taskiem.com`. Please tell us the exact origins so we can put them in the customer guide.
- **Modes and screens:** which of them Taskiem may show.
- **Sign-in:** who can sign in.
- **Approving and paying:** whether these need a code.

**1. Your server mints a launch token** each time you show the frame:

```http
POST https://synledger.name.ng/payrolla/api/v1/connect/embed/launch-token
Authorization: Bearer pyk_live_…

{ "scope": "LAUNCH_CONTEXT", "context": { "screen": "run-review", "run_id": "5d2a…" },
  "user": { "external_ref": "<taskiem user id>", "email": "ngozi@acme.ng", "name": "Ngozi Eze", "role": "APPROVER" } }
```
```json
{ "data": { "session_token": "…", "expires_at": "…", "host_id": "hst_4f9a…" } }
```

**2. Your page mounts it.** Store `host_id` per connection; it isn't secret.

```html
<div id="payroll" style="height: 720px"></div>
<script src="https://synledger.name.ng/payrolla/widget/sdk/payroller-embed.umd.js"></script>
<script>
  PayrollerEmbed.mount(document.getElementById('payroll'), {
    embedUrl: 'https://synledger.name.ng/payrolla/api/v1/embed',
    hostId: 'hst_4f9a…',
    mode: 'launch',                                   // 'full', 'launch' or 'payout'
    getSessionToken: () => fetch('/api/payrolla/launch-token', { method: 'POST' })
      .then(r => r.json()).then(d => d.session_token), // your endpoint calling step 1
    theme: { primary: '#5B3DF5' },
    onEvent: (type, payload) => console.log(type, payload),
  })
</script>
```

**Where it fits in Taskiem:**

| Placement | `scope` and `context` | SDK `mode` |
|---|---|---|
| A "Payroll" tab | `FULL` | `full` |
| An approval card, e.g. from workflow 1 | `LAUNCH_CONTEXT`, `{ "screen": "run-review", "run_id": … }` | `launch` |
| An employee's payslips | `LAUNCH_CONTEXT`, `{ "screen": "payslip", "employee_id": … }` | `launch` |
| Pay contractors and vendors | `LAUNCH_CONTEXT`, `{ "screen": "payout" }` | `payout` |

**Who the user is.**
- **Matching:** Payrolla matches `external_ref`, then `email`.
- **Unknown people:** depending on the owner's setting, someone unknown is either added (capped at the owner's maximum role, never Owner) or told to ask for an invite.
- **Roles:** an existing person keeps their Payrolla role. Taskiem can't raise it.

**Approving and paying in the frame.**
- **Default:** each action asks for a one-time code sent to the person's own phone, email or authenticator, set up in Payrolla.
- **No factor yet:** someone without one sees "Ask your Payrolla owner to add your phone number", and everything else still works.
- **Trust:** an owner may instead choose to trust Taskiem's sign-in for their company.

**Messages from the frame:**
- `EMBED_READY` and `EMBED_ERROR` (could not start);
- `EMBED_NAV` `{screen}` and `EMBED_CLOSE`;
- in payout mode, `EMBED_PAYOUT_SUBMITTED` and `EMBED_PAYOUT_COMPLETE` `{batch_id, status, paid_count, failed_count}`.

Full detail is in `api/embedding.md`.

## 7. Errors and limits

| Status | Codes | What Taskiem should do |
|---|---|---|
| 401 | `API_KEY_REQUIRED`, `API_KEY_INVALID` | Mark the connection disconnected; ask the customer to reconnect |
| 403 | `SCOPE_REQUIRED` | Tell the customer which permission to add. The owner issues a new key with it. |
| 403 | `PAYSLIP_LINKS_DISABLED`, `EMBED_NOT_CONFIGURED`, `EMBED_SCREEN_NOT_ALLOWED`, `EMBED_MODE_NOT_ALLOWED` | Show the setting the owner needs to change |
| 404 | `RUN_NOT_FOUND`, `PAYSLIP_NOT_FOUND`, … | The object isn't in this company |
| 409 | `PAYSLIP_NOT_FINAL`, `WEBHOOK_DISABLED` | Wait, or ask the owner to resume the webhook |
| 422 | Validation codes | Fix the request |
| 429 | `RATE_LIMITED` | Wait for `Retry-After` seconds |

- **Rate limit:** 120 requests per minute per key. Ask us if a workflow needs more.
- **Webhooks:** a subscription that fails continuously for 3 days is switched off. The owner sees it in Payrolla and can resume it; replay `/connect/events` afterwards to fill the gap.

## 8. Security

- **Where secrets live:** keys and signing secrets stay on Taskiem's servers, never in a browser, mobile app, log, email or ticket.
- **Every webhook:** verify the signature and timestamp on every delivery, and de-duplicate on the event id.
- **Owner control:** the owner can revoke Taskiem at any moment. API calls, deliveries and open embedded sessions all stop at once.
- **Links:** an `approval_url` is safe to share. Payslip links and launch tokens aren't.
