# Payrolla integration

The connector is `connectors/payrolla` (`payrolla@1`). Payrolla runs a company's whole payroll and pays its staff itself, so Taskiem connects to it as a product, not as a way to pay ([decision 0044](../decisions/0044-rails-and-products.md)): its events start workflows, its reads tell them about runs and people, and it hands out links. **Nothing the connector does moves money, approves a payroll or changes an employee.** It was built from what Payrolla published (`docs/integrations/payrolla-reference`: the Connect API description, its webhook and embedding guides, and Payrolla's integration guide), read 23 October 2026.

## Connection

Each Payrolla company that uses Taskiem connects with **its own key**: a company owner creates it in Payrolla under **Settings → Connected apps → Connect an app**, chooses what it may do, and pastes it into Taskiem under **Connections → Payrolla**. A key reads and receives only its own company. The key is shown once in Payrolla; keep it nowhere else.

| Field | |
| --- | --- |
| `api_key` | The company key (`pyk_live_…`). Sent as `Authorization: Bearer <key>` |

**Live or test.** Connecting Payrolla asks **Live (real money)** or **Test (practice)** ([connector/v1](../contracts/connector-v1.md) rule 17), as Paystack's does; the connection keeps the choice and a test one is marked "Test: no real money" on Connections. Live is Payrolla's production deployment, `https://ispend.name.ng/payrolla/api/v1` (`TASKIEM_PAYROLLA_URL` moves it); test is its sandbox, `https://synledger.name.ng/payrolla/api/v1` (`TASKIEM_PAYROLLA_TEST_URL`), where the test company is. Keys from one never work in the other, so paste a key made in the same deployment. Connections made before modes keep going to the sandbox, or to `TASKIEM_PAYROLLA_URL` when it is set.

The scopes the owner ticks decide what the connection can do:

| Scope | Unlocks |
| --- | --- |
| `payroll:read` | `run_event`, `payout_event` and `payslip_event`; the run actions |
| `employees:read` | `employee_event`; `list_employees`, `get_employee` |
| `webhooks:manage` | Taskiem creating and deleting the subscriptions behind the triggers |
| `payslips:links` | `create_payslip_link` (and the owner must also turn payslip links on in Payrolla) |
| `embed:launch` | Showing Payrolla's screens inside Taskiem (planned: [rails and products](../spec/rails-and-products.md) R6) |

**The connection test** (`get_company`) names the company and the key's scopes. **A revoked key** (`401 API_KEY_INVALID`) marks the connection expired at that step, and runs stop using it until it is connected again; a missing scope (`403 SCOPE_REQUIRED`) fails the step and names the scope, which the owner adds by issuing a new key. Payrolla allows 120 requests a minute per key; a `429` is retried after its `Retry-After`.

## Triggers

Payrolla signs every delivery with HMAC-SHA256 over `<timestamp>.<raw body>`, sent as `Payrolla-Signature: t=<unix>,v1=<hex>`; deliveries more than five minutes old are refused. Taskiem creates the company's subscription itself when a workflow using a trigger is deployed (decision 0021), for exactly the events the workflow names (all of the trigger's when it names none), keeps the signing secret Payrolla returns once in the vault, and deletes the subscription when the workflow leaves the environment. Payrolla can neither change a subscription nor rotate its secret, so a redeploy with other events replaces it; and one Taskiem finds by its URL but never recorded (a crash between creating it and recording it) is deleted and made again, since its secret was never kept. Each event is taken once, by its id, however often Payrolla retries or replays it.

| Trigger | Events | Correlation |
| --- | --- | --- |
| `run_event` | `run.computed` (carries `approval_url`), `run.approved`, `run.rejected`, `run.locked`, `run.cancelled`, `run.disbursement_started`, `run.completed`, `run.partially_completed` | the run's id |
| `payout_event` | `payout.succeeded`, `payout.failed` (with `reason`), `payout.reversed` (returned by the bank after it succeeded) | the run's id |
| `employee_event` | `employee.created`, `employee.updated` (with `changed_fields`), `employee.terminated` | the employee's id |
| `payslip_event` | `payslip.issued` | the run's id |

Events never carry bank account numbers, salary breakdowns or ID numbers. They do carry names, emails and phones (employee events) and amounts in kobo. A run's events can arrive out of order; trust the latest, and after a reversal `run.partially_completed` comes again even when `run.completed` came before. `ping`, sent only when you ask Payrolla to test a subscription, is a test event: it is verified and acknowledged and starts nothing.

## Actions

| Action | Endpoint | Class | |
| --- | --- | --- | --- |
| `get_company` | `GET /connect/company` | read | The connection test: the company and the key's scopes |
| `list_runs` | `GET /connect/runs` | read | `status` (comma-separated), `updated_since`, `cursor`, `limit` (1 to 100); newest first |
| `get_run` | `GET /connect/runs/{run_id}` | read | The run and its timeline; `approval_url` while it waits for approval |
| `get_run_lines` | `GET /connect/runs/{run_id}/lines` | read | Each employee's net pay and payout status (`CONFIRMED`, `FAILED`, `REVERSED`, …), the provider's reference and the failure reason. Names are sealed |
| `get_approval_link` | `GET /connect/runs/{run_id}/approval-link` | read | Where an approver reviews the run in Payrolla; safe to send |
| `list_employees` | `GET /connect/employees` | read | `status`, `updated_since`, `cursor`, `limit`; names, emails and phones sealed |
| `get_employee` | `GET /connect/employees/{employee_id}` | read | One employee, sealed the same way |
| `create_payslip_link` | `POST /connect/payslips/{payslip_id}/link` | unsafe write | A 7-day link to one payslip; the employee confirms the last four digits of their phone. Payrolla takes no idempotency key, so an answer lost in transit parks the step rather than asking twice. The link is sealed. Refused with `PAYSLIP_LINKS_DISABLED` until the owner allows it, and `PAYSLIP_NOT_FINAL` before the run is locked |
| `list_events` | `GET /connect/events` | read | The last 30 days of the company's events, oldest first, after an event id: for catching up after an outage |

## In Taskiem's own screens

Payroll is Payrolla's; Taskiem shows it as its own (P4a in the [build plan](../spec/build-plan-saved-tools-payments.md)), through the same connection, with no Payrolla screens embedded:

- **Payroll** (`GET /v1/payroll`, `GET /v1/payroll/runs/{run}`): the company's runs, newest first; a run's total, its timeline, each person's net pay and whether it arrived (paid, waiting, not paid with Payrolla's reason, returned), and **Review and approve in Payrolla** while it waits for approval. Seeing it needs the `payroll.read` permission, the owner's alone unless given to a role: it shows what people earn, whatever the key's scopes.
- **Staff in Payrolla** (`GET /v1/payroll/staff`, `PUT /v1/staff/{user}/payrolla`): each staff member linked to the Payrolla employee they are, suggested by the same sign-in email or chat number and confirmed by an owner (one employee, one staff member). Only Payrolla's id and the name it gave are kept.
- **Their own chat:** a `notify@1` `message` naming the employee (`payrolla_employee`) reaches a linked staff member on their own chat with the business, so `payrolla-tell-staff` sends pay arrived and the payslip link there, not by SMS.

A refused key says what to do: `scope_required` (make a key with Read payroll and Read employees), `reconnect` (the key was revoked), `payrolla_down` (try again in a minute).

## What is not here yet

Payrolla shipped everything Taskiem asked for in P7 ([the request](payrolla-api-requests.md); Payrolla's replies [A](payrolla-reference/05-phase-a-response.md), [B](payrolla-reference/06-phase-b-response.md) and [C](payrolla-reference/07-phase-c-response.md)). Taskiem builds on it as P4b, in five slices ([decision 0074](../decisions/0074-payroll-run-from-taskiem.md), proposed; [the plan](../spec/chat-pay-people.md#p4b-slice-by-slice)). None is built yet:

1. **Connecting in one step** (P4b-1), **built 24 November 2026** ([as built](../spec/chat-pay-people.md#p4b-1-connect-in-one-step-keep-keys-reconnect-link-approvers)), live once Payrolla registers Taskiem:
   - OAuth 2.0 with PKCE, with sign-up in the same step (`POST /v1/payroll/connect`; Payrolla sends the browser back to `/v1/payrolla/callback`);
   - key connections keep working, and **Reconnect Payrolla** moves the same connection onto the tokens;
   - Taskiem's own `"*"` subscription per connection; `company.disconnected` and a refused refresh token end it;
   - Taskiem people linked to Payrolla users with one code (`/v1/payroll/approvers`, `payroll.approve`).

   Payrolla registers Taskiem first, from [our answers](payrolla-reference/08-taskiem-answers.md), drafted for the founder to send. The deployment then sets `TASKIEM_OAUTH_PAYROLLA_CLIENT_ID`/`_SECRET` (live), `TASKIEM_OAUTH_PAYROLLA_SANDBOX_CLIENT_ID`/`_SECRET` (test) and `TASKIEM_PAYROLLA_APP_URL` (production's sign-in app; the sandbox's is `payroller.surge.sh`, `TASKIEM_PAYROLLA_TEST_APP_URL` moves it).
2. **Staff and pay from Taskiem** (P4b-2):
   - adding, changing and ending employees;
   - bank changes held for a second approver;
   - an employee's own salary account change.
3. **This month's run in Taskiem** (P4b-3), **built 24 November 2026** ([as built](../spec/chat-pay-people.md#p4b-3-this-months-run-in-taskiem); `/v1/payroll/runs`, `payroll.prepare`, the run's card and the code typed in the WhatsApp or Telegram thread):
   - prepare it, add a bonus or a deduction, leave someone out;
   - approve and **Lock and pay**, confirmed with **Payrolla's own one-time code**, typed into Taskiem.

   Approving is no longer a link into Payrolla, and there is no Taskiem-signed approval: Payrolla chose its own code instead.
4. **After locking** (P4b-4): the payment's progress, the wallet's shortfall, **Pay now**, **Pay the failed ones again**, and payslips as Taskiem's own cards.
5. **Settings and the tax and pension schedules** (P4b-5).

**Also not here yet:**

- **Catching up by itself.** `list_events` replays the last 30 days, but nothing calls it automatically after an outage yet. Payrolla retries a failed delivery for about two days, and the owner can resend one.
- **Production's OpenAPI file.** The sandbox's current file was fetched on 24 November 2026 (`payrolla-reference/api/payrolla-connect-v1-2026-11-24.yaml`, beside the 23 October copy); production's still served the older one that day.

## Templates

Seven templates work around Payrolla ([template library](../templates.md)), offered on the Start page once Payrolla is connected. None moves money or approves a payroll.

| Template | Starts on | Does |
| --- | --- | --- |
| `payrolla-tell-staff` | `payout.succeeded` | Tells the employee their pay arrived (on the WhatsApp they asked for updates on, else by SMS), and sends their payslip link when you allow it |
| `payrolla-payroll-summary` | `run.completed`, `run.partially_completed` | Tells the owner and a role who was paid, the total, and who was not and why |
| `payrolla-approve-by-chat` | `run.computed` | Sends the approvers the total and `approval_url`; reminds them the day before payday if it is still not approved |
| `payrolla-reversal-alert` | `payout.reversed` | Alerts the owner and the payroll admin whose pay came back |
| `payrolla-record-payroll` | `run.completed`, `run.partially_completed` | Rows in `payrolla_payrolls` and `payrolla_payments` for the accountant; no bank details |
| `payrolla-payday-check` | Every morning | Compares payrolls due soon with a connected payments account's balance and warns if short |
| `payrolla-lock-reminder` | Every morning | Reminds the payroll admin of approved payrolls near payday that are not locked |

Messages to employees go by `notify@1`: on WhatsApp only to someone who asked for updates there with the business's link, else by SMS. Payrolla's go-live checklist asks for each employee's consent before WhatsApp messages; asking through that link is how it is recorded.
