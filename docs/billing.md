# Plans and billing

Taskiem sells flat-priced plans (spec 16): a monthly or annual fee per tier, never a charge per run. A plan sets a tenant's capacity (concurrent runs, workers, ingest rate, retention, AI tokens, WhatsApp templates...) and its features (single sign-on, Git, embedding...). Pass-through costs, WhatsApp templates beyond the plan's allowance and, where a plan allows it, AI beyond its budget, are invoiced at cost. Prices are in naira and paid through Paystack (or Flutterwave). Design: [decision 0017](decisions/0017-plans-and-billing.md).

**Billing is off by default.** A self-hosted deployment, and any deployment without `TASKIEM_BILLING=on`, puts every tenant on the internal `self_hosted` plan: the platform's default limits (`TASKIEM_DEFAULT_*`), every feature, no invoices. Nothing on this page applies until billing is turned on.

> The prices in `deploy/plans.yaml` are **placeholders** pending decision B1 ([needs people](needs-people.md#phase-4)). The billing page says so while `placeholder_prices: true`.

## For operators

### Turning billing on

1. Edit `deploy/plans.yaml` (below) and check it: `taskiem billing plans --file deploy/plans.yaml`.
2. Put internal and design-partner tenants on a plan first, or they start a trial when billing comes on:
   `taskiem billing grant <tenant> self_hosted` (internal, indefinitely) or `taskiem billing grant <tenant> business --until 2027-03-31` (a comp).
3. Set on the `api` and `scheduler` roles (with `all`, once), from the Secret:

| Variable | Default | Notes |
| --- | --- | --- |
| `TASKIEM_BILLING` | `off` | `on` turns plans, subscriptions and payments on |
| `TASKIEM_BILLING_PLANS` | `deploy/plans.yaml` | The catalogue; validated and loaded into the database at start (and by `taskiem billing plans --load`) |
| `TASKIEM_BILLING_PROVIDER` | `paystack` | Provider for new checkouts: `paystack` or `flutterwave` |
| `TASKIEM_BILLING_PAYSTACK_SECRET_KEY` | — | The **platform's** Paystack secret key (the merchant account Taskiem is paid into; never a tenant's connection). Also verifies Paystack's webhook signatures |
| `TASKIEM_BILLING_FLUTTERWAVE_SECRET_KEY`, `TASKIEM_BILLING_FLUTTERWAVE_WEBHOOK_HASH` | — | The platform's Flutterwave secret key and the secret hash set on its dashboard |
| `TASKIEM_BILLING_PAYSTACK_URL`, `TASKIEM_BILLING_FLUTTERWAVE_URL` | the providers' APIs | Tests only |
| `TASKIEM_SMTP_URL`, `TASKIEM_ALERT_FROM` | — | Dunning and receipts by email (the same mail settings as alerts); without them only the in-app banner tells tenants |
| `TASKIEM_PUBLIC_URL` | — | Checkout return links and links in emails |

4. In the provider's dashboard set the webhook URL to `https://<api host>/v1/billing/webhooks/paystack` (or `/flutterwave`). It is on the API, not the edge.
5. Turn on signup if tenants should sign themselves up (`TASKIEM_ALLOW_SIGNUP=true`, [onboarding](onboarding.md)); each new tenant starts its trial.

### The catalogue

`deploy/plans.yaml` has the commercial terms and the plans. Amounts are kobo. Limits use the keys of `taskiem tenants limits` ([plan limits](operations.md#plan-limits)); a key left out keeps the platform default, and `0` is no limit.

| Field | Meaning |
| --- | --- |
| `placeholder_prices` | `true` until B1 decides the prices |
| `vat_percent` | VAT shown separately on invoices (7.5) |
| `invoice_prefix` | Invoice numbers: `TKM-2026-000001`, without gaps per year |
| `trial_days`, `trial_plan` | Each new tenant's trial |
| `grace_days` | How long a past-due subscription keeps working before new runs are refused |
| `dunning_days` | Days after falling past due when the card is retried and owners reminded |
| `due_days` | Invoice payment terms |
| `whatsapp_template_kobo` | Meta's price per template beyond the allowance, by category (marketing, utility, authentication), passed through at cost |
| `plans[].limits` | Any `tenant_limits` key, including `max_retention_days` (history kept at most this long after a run ends) |
| `plans[].features` | `sso`, `scim`, `custom_roles`, `white_label`, `byok`, `git`, `ai`, `embedded` |
| `plans[].partner` | Embedded tiers: `subtenant_runs_per_day`, `subtenant_runs_per_month` (used when the operator set none with `taskiem tenants partner`), `max_subtenants` (a downgrade below the partner's count is refused) |
| `plans[].overage.ai_kobo_per_million_tokens` | Bill AI beyond the budget instead of refusing it (0: the budget is a hard cap) |
| `plans[].public`, `active` | Offered self-serve; retired (subscribers keep it). A plan removed from the file is retired, never deleted |

Limits layer: platform defaults, then the plan's limits, then the tenant's own overrides (`taskiem tenants limits --set`), which still win. A sub-tenant runs on its partner's plan and overrides, lowered by what the partner sets for it.

### Commands

```sh
taskiem billing plans [--file FILE] [--load]               # validate, print, and with --load write the catalogue
taskiem billing grant TENANT PLAN [--until YYYY-MM-DD]      # a plan without payment (comped); audited
taskiem tenants limits TENANT                               # effective limits (plan and overrides) and usage
```

A grant forgives open invoices (voided) and is audited (`billing.grant`) in the tenant's chain. At `--until` the comp ends like a trial: an invoice, then past due if unpaid.

### What runs where

The scheduler role runs the billing job every minute: trials and periods that ended are invoiced (and charged to a saved card), past-due tenants are reminded on the dunning days and degraded after the grace period, cancellations take effect at period end, payments pending over two minutes are verified with the provider (a lost webhook, a bank transfer) and abandoned after a day, and usage is snapshotted hourly. Every transition is audited (`billing.renew`, `billing.dun`, `billing.degrade`, `billing.cancel`, `billing.invoice.issue`, `billing.invoice.paid`, `billing.payment.mismatch`, ...).

A payment whose verified amount or currency differs from its invoice is recorded as `mismatch`, logged as an error, and leaves the invoice open: find it with `SELECT * FROM billing_payments WHERE status = 'mismatch'` and refund or settle it by hand. A payment that arrives for a voided invoice is audited as `billing.payment.unapplied` for a refund.

## For tenants

**Settings > Billing** (`/settings/billing`) shows the plan, the subscription, usage against the plan's limits, the last month of daily usage, invoices and the saved card. Owners hold the `billing.manage` permission (grant it to a custom role for a finance person); every member sees the plan and the banner.

| State | What it means |
| --- | --- |
| Trial | Everything the trial plan allows, for `trial_days`. Choose a plan and pay before it ends |
| Active | Paid for the period. Renewals are charged to the saved card, or invoiced |
| Past due | An invoice is unpaid. Everything keeps working until the grace period ends; reminders by email |
| Degraded | Still unpaid after the grace period: **new runs are refused** (402 `billing_degraded`). Runs already running, approvals, signals, timers and reconciliation continue, so no payment in flight is stranded. Paying restores service at once |
| Cancelled | Ended at the period end you asked for. Workflows and history are kept; new runs are refused until you choose a plan |
| Comped | Granted by the operator |

**Paying.** Choose a plan (monthly or annual) and pay on the provider's page by card or bank transfer. A card payment is saved for renewals. Invoices list the plan, any WhatsApp template overage at Meta's cost (by category and month), any AI overage, credits, and VAT separately; open one as a printable page from the invoice list.

**Changing plans.** An upgrade starts a new period at the new price now, less a credit for the unused part of the current one, and takes effect when paid (at once with a saved card). A downgrade takes effect at the end of the period; it is refused while you use more than the smaller plan allows, with a list of what to remove first (workflows over its count, SSO connections, custom roles, Git connections, embed apps...). Changing plans during the trial just changes the plan you try. Cancelling takes effect at period end and can be undone until then.

**Features** a plan lacks are refused when you set them up (402 `plan_feature_required`): single sign-on and SCIM, custom roles, Git, AI building, embedding (the partner API), white-label custom domains and bringing your own key ([BYOK](byok.md); returning to Taskiem's key, rotating and checking need no feature).

## API

| Method and path | Permission | Does |
| --- | --- | --- |
| `GET /v1/billing/status` | any member | Plan, features, status and banner |
| `GET /v1/billing` | `billing.manage` | Plan, subscription, plans offered, limits and usage, daily snapshots, sub-tenants' summed usage (partners), invoices, open invoice, saved card, banner |
| `GET /v1/billing/invoices/{id}` | `billing.manage` | An invoice; `?format=html` for a printable page |
| `POST /v1/billing/checkout` | `billing.manage` | `{plan, interval, email, channels, provider}` subscribe from a trial or after cancelling, or pay what is owed (`{invoice}`, or nothing while past due): returns `checkout_url` |
| `POST /v1/billing/plan` | `billing.manage` | `{plan, interval}`: returns `effective` (`now`, `on_payment` with `checkout_url`, `period_end`); 409 `downgrade_blocked` with `blockers` |
| `POST /v1/billing/cancel` | `billing.manage` | Cancel at period end; `{resume: true}` undoes it |
| `POST /v1/billing/webhooks/{provider}` | provider signature | Paystack `charge.success`, Flutterwave `charge.completed` |

With billing off, `GET` answers the internal plan and the others answer 409.

## Security model

- Payment credentials are the operator's, from the environment; tenants' Paystack and Flutterwave connections are never used for billing.
- A webhook must carry the provider's signature (Paystack: HMAC-SHA512 of the body under the secret key; Flutterwave: the secret hash), checked by the same code as connector triggers, else 401. Its body is then **not trusted**: the payment is re-verified with the provider's API, and settled only if the amount and currency equal the invoice's. Redelivered webhooks are no-ops (receipts), settling is idempotent, and the payment reference carries the tenant so the webhook is processed inside that tenant's row-level-security scope.
- Invoices cannot be changed or deleted once issued (a database trigger, even for the superuser); numbers have no gaps; plan prices are written only through an audited definer function from the operator's file.
- Saved card authorizations are never returned by the API; they are useless without the platform's secret key.
- Tenants cannot raise their own limits or change their plan without paying: plan changes go through the service, which issues the invoice and applies the change only on a verified payment.
