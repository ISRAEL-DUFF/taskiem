# Rails and products: specification and build plan

Status: **R1 to R5, R7, R8 and R9 built**, R6 planned · Decision: [0044](../decisions/0044-rails-and-products.md) · Workstream **R0 to R9**, one pull request per milestone.

Taskiem connects to two kinds of service. **Rails** move money (Paystack, iswallet, Moniepoint and the rest) and Taskiem builds products on them as workflows. **Products** (Payrolla, and later accounting, HR and POS back offices) already run a whole process, and Taskiem connects to them, embeds their interface, and works around them. This document specifies both halves, and the retirement of the Payrolla integration that mixed them up.

Contents:

1. [Summary](#1-summary)
2. [Words used here](#2-words-used-here)
3. [Where we are](#3-where-we-are)
4. [Capabilities: one template, any rail](#4-capabilities-one-template-any-rail)
5. [Products built on rails, as templates](#5-products-built-on-rails-as-templates)
6. [Products as connectors](#6-products-as-connectors)
7. [Retiring Payrolla-as-a-rail](#7-retiring-payrolla-as-a-rail)
8. [Milestones](#8-milestones)
9. [Order and timing](#9-order-and-timing)
10. [Testing](#10-testing)
11. [Risks and open questions](#11-risks-and-open-questions)
12. [Out of scope](#12-out-of-scope)

---

## 1. Summary

| | Rails | Products |
| --- | --- | --- |
| Examples | Paystack, Flutterwave, iswallet, Moniepoint, Anchor, Lenco, Remita, Interswitch, OPay, MTN MoMo, M-Pesa | Payrolla; later accounting, HR, POS back offices |
| Who owns the money movement | Taskiem's workflow, through the rail | The product |
| Who owns the process logic | Taskiem (a template) | The product |
| How Taskiem uses it | Capability steps in templates: pay, collect, open accounts, read balances, wait for confirmation | A connector: events start workflows, actions are steps, the interface opens as an app view |
| What Taskiem adds | The whole product: approvals, exactly-once, confirmation, reversals, records, messages | Everything around it: WhatsApp and Telegram messages, approvals by chat, alerts, records, summaries |
| What Taskiem never does | — | Move money the product moves |

What gets built:

- **A capability layer** so one template works on any rail the business has connected: rails declare what they can do, templates use capability steps, and deployment turns each into the chosen rail's own action (§4).
- **Templates on rails**: Run payroll (from today's dogfood flow), Pay a list, a payment link per order, a virtual account per customer, refunds with approval, supplier payments (§5).
- **App views**, so a product's embeddable interface opens inside Taskiem, safely (§6.3).
- **A Payrolla connector** and templates around it (§6.2, §6.4).
- **The retirement** of the Payrolla-specific workflow, tests and guide, keeping the generic partner machinery (§7).
- **The Connector catalogue** listing Taskiem's own connectors, grouped as rails, products and tools (R9).

## 2. Words used here

| Word | Meaning |
| --- | --- |
| **Rail** | A service that moves money and offers primitives (pay, collect, accounts, balance, events) but no business process of its own |
| **Product** | A service that runs a business process end to end, usually moving money itself over a rail, with its own data and screens |
| **Tool** | A service that is neither: Google Sheets, Gmail, Slack, Telegram. Unchanged by this plan |
| **Capability** | One thing a rail can do, with a Taskiem-defined input and output: `pay_bank`, `pay_wallet`, `collect_link`, … (§4.1) |
| **Capability step** | A workflow step that names a capability instead of a connector action: `uses: "pay_bank"` with a connection |
| **Expansion** | Replacing each capability step with the chosen rail's own action and input mapping when the version is deployed (§4.4) |
| **Settlement** | The final outcome of a payout: `settled`, `failed` or `reversed` (§4.5) |
| **App view** | A product's embeddable interface opened inside Taskiem (§6.3) |

## 3. Where we are

### 3.1 What each rail can do today

From the manifests (`connectors/<id>/manifest.yaml`). Every rail uses `auth.type: api_key`. Y = an action exists, q = a read-only query, — = none.

| Rail | Collect | Pay bank | Pay wallet | Bulk | Virtual account | Balance | Name enquiry | Refund | Payout lookup | Payout events |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| paystack | Y `initialize_transaction` | Y `create_recipient` + `transfer` | — | — | — | Y | Y | Y | Y `verify_transfer` | success, failed, reversed |
| flutterwave | q `verify_payment` | Y `transfer` (queued) | — | — | — | Y | Y | — | Y `get_transfer_by_reference` | completed (status in body) |
| iswallet | — | Y `payout` (pending) | Y `transfer` (final) | — | — | Y | Y | — | Y `get_payout` | confirmed, failed, reversed |
| moniepoint | Y `init_transaction` | Y `transfer` (may need OTP) | — | — | Y `create_reserved_account` | Y | Y | — | Y `get_transfer` | successful, failed, reversed |
| anchor | — | Y `transfer` | Y `book_transfer` | — | — | Y | Y | — | Y `get_transfer_by_reference` | successful, failed, reversed |
| lenco | — | Y `transfer` | — | — | Y `create_virtual_account` (unsafe) | Y | Y | — | Y `get_transfer` | successful, failed |
| remita | Y `generate_invoice` (RRR) | Y `transfer` | — | Y `bulk_transfer` | — | — | Y | — | Y `get_transfer`, `get_bulk_transfer` | — (lookup only) |
| interswitch | q `get_payment` | Y `transfer` | — | — | — | — | Y | — | Y `get_transfer` | — (lookup only) |
| opay | Y `create_cashier_payment`, temp account | — | — | — | temporary only | — | — | Y | — | — |
| mtnmomo | Y `request_to_pay` | — | Y `transfer` (mobile money) | — | — | Y | Y | — | Y `get_transfer` | successful, failed |
| mpesa | Y `stk_push` (unsafe) | — | Y `b2c_payment` (mobile money) | — | — | async | — | reversal | async | completed, failed |

Also relevant: moniepointpos, opaybusiness (in-store collection on terminals), mono (account data, DirectPay, mandates), breet (crypto). They are rails too; this plan adds capabilities for them only where a template needs them.

Engine machinery the templates rely on, all of which exists:

- **Effect classes** (`docs/contracts/action-classes.md`): `idempotent_write` resends under the same key, `reconcilable_write` looks the outcome up first, `unsafe_write` parks an unknown outcome in `needs_reconciliation` and alerts. An `EffectIntent` is recorded before every external call (`engine/runtime/worker.go`).
- **Idempotency keys** (`docs/contracts/idempotency.md`): derived from tenant, seed, step instance and attempt group. With `effect.idempotency_seed`, a key is stable per item across retries, resends and forks, which is what makes "one payment per employee per payroll" hold.
- **Loops**: `foreach` with `max_concurrency` and `max_items`; a failed item with `on_error` is recorded and the loop carries on.
- **Approvals**: policies with levels, `forbid_self_approval`, and step-up (`passkey`, `totp`, `whatsapp_pin`) bound to one decision (`docs/governance.md`, `docs/whatsapp.md`).
- **Waiting**: a `signal` step waits for one named event, correlated (for example on a transfer reference); events that arrive early are buffered (`engine/runtime/store.go`). A step waits for a single event name, so "settled or failed" needs a `parallel` with `join: any`, or a lookup after a timeout. §4.5 removes that limit for payouts.

### 3.2 The Payrolla integration as built

`flows/dogfood/payrolla-salary-disbursement.flow.ts` / `.wd.json` (id `wf_payrollaSalaryDisbursement`), documented for Payrolla's engineers in [integrations/archive/payrolla.md](../integrations/archive/payrolla.md):

1. A **webhook** at `/hooks/<tenant>/payrolla/payroll-approved?env=`, signed with `X-Taskiem-Signature` (decision 0012), deduplicated on `payroll_id`. Payload: `payroll_id`, `period`, `funding_wallet_id`, `total_kobo`, and up to 5,000 employees, each with `employee_id`, `net_pay_kobo`, and either an iswallet `wallet_id` or bank details.
2. `summarise`, then `check_balance` (iswallet `get_balance` on the funding wallet).
3. `approve`: role `payroll_approver`, 24 h, rejected on timeout; the author cannot approve.
4. `pay_all`: a `foreach`, five at a time. Wallet employees get an iswallet `transfer`; bank employees get an iswallet `payout` and then `settle`, a signal on `iswallet@1:outflow_event` correlated on the outflow id, up to 72 h. The seed `payroll_id:employee_id` makes each payment happen at most once.
5. `summary`, then `report`: an HTTP POST of every outcome and the totals to Payrolla's `/payrolls/{id}/disbursement-report`.
6. A **reversal watch** for 35 days on every confirmed bank payout, each reversal posted to Payrolla's `/payrolls/{id}/reversal-notice`. A rejected or expired approval posts a `rejected` report.

It is well tested: `engine/decide/decide_test.go` (`TestPayrollaDogfoodEndToEnd`, `TestPayrollaRejectedPayrollIsReported`), `e2e/triggers_test.go` (`TestPayrollaDisbursementEndToEnd`, on the real stack with fake iswallet and Payrolla), ten workflow tests in `flows/dogfood/tests/payrolla-salary-disbursement.test.json`, an AI-builder eval case and four repair eval cases.

Built alongside it, and generic (they stay): sub-tenants and partner onboarding (decision 0030; `api/partner*.go`), partner workflow packages, shared provider clients with a wallet allow-list and event routing to sub-tenants (decision 0031; `engine/ingest/route.go`, iswallet's `shared_client` and `wallet_ids`).

Section 7 lists every Payrolla-specific file and what happens to it.

### 3.3 Templates today

Templates (`templates/library/*.json`, loaded by `templates/templates.go`) name their connectors outright, and the template test checks that a template uses exactly the connectors it declares. There is no way to say "any rail that can pay a bank account". The money templates are therefore tied to one rail each:

| Template | Rail |
| --- | --- |
| `salary-payout-approval` | Google Sheets → approval → **Paystack** recipient and transfer → SMS |
| `supplier-payment-approval` | **Paystack** |
| `failed-transfer-alert`, `transfer-received-notify`, `low-balance-alert`, `weekly-balance-email` | One rail each |
| `pos-*` | Moniepoint POS, OPay Business |

The only provider abstraction in the code is the storefront's `Payments` interface (`engine/storefront/orders.go`), with Paystack as its single implementation.

### 3.4 What is missing

- Templates cannot be written once for every rail (§4).
- Taskiem has no way to show a product's own interface: the app's Content-Security-Policy allows framing only the public host's look previews (`engine/httpsec/httpsec.go`) (§6.3).
- Payrolla is not a connector, so it does not appear among connections, and its events cannot start workflows (§6.2).
- The **Connector catalogue** page lists only community-published connectors, so a business looking for iswallet or Paystack there finds nothing (R9).

---

## 4. Capabilities: one template, any rail

### 4.1 The capability set

Each capability has one input and one output shape, defined by Taskiem in `schemas/capabilities-v1.json`. Amounts are always integer kobo (or the minor unit of `currency`); people and accounts are always the same fields. Personal fields are declared `x-pii` so they are sealed whatever the rail.

| Capability | Input | Output | Class required of the rail's action |
| --- | --- | --- | --- |
| `balance` | `account` (optional: a wallet or sub-account id), `currency` (default NGN) | `available_kobo`, `currency` | read |
| `resolve_account` | `bank_code`, `account_number` | `account_name` | read |
| `list_banks` | — | `banks[] {code, name}` | read |
| `pay_bank` | `amount_kobo`, `currency` (default NGN), `bank_code`, `account_number`, `account_name`, `narration` (up to 100 characters), `source` (optional) | `payout_id`, `status` (`settled` \| `pending` \| `failed`), `fee_kobo`, `reason` | idempotent or reconcilable |
| `pay_wallet` | `amount_kobo`, `currency` (default NGN), `wallet_id`, `narration`, `source` (optional) | `payout_id`, `status`, `fee_kobo`, `reason` | idempotent or reconcilable |
| `pay_mobile_money` | `amount_kobo`, `currency`, `msisdn`, `narration` | `payout_id`, `status`, `fee_kobo`, `reason` | idempotent or reconcilable |
| `get_payout` | `payout_id` | `status` (`settled` \| `pending` \| `failed` \| `reversed`), `settled_at`, `fee_kobo`, `reason` | read |
| `pay_many` | `items[]` (as `pay_bank`), `currency`, `narration`, `source` | `batch_id`, `items[] {status, payout_id}` | idempotent; **native only** (§4.7) |
| `collect_link` | `amount_kobo`, `currency` (default NGN), `customer {name, email, phone, bvn}`, `description`, `return_url` | `payment_url`, `payment_id` | idempotent or reconcilable |
| `collect_transfer` | `amount_kobo`, `currency` (default NGN), `customer`, `description`, `expires_minutes` | `payment_id`, `account_number`, `bank_name`, `account_name`, `amount_kobo`, `expires_at` | idempotent or reconcilable; unsafe allowed and declared. Added by T1 of [invoices and the money log](invoices-and-money-log.md#4-one-time-accounts-collect_transfer), with the mapping's `transfer` facts |
| `collect_push` | `amount_kobo`, `currency`, `msisdn`, `description` | `payment_id`, `status` | idempotent or reconcilable |
| `get_payment` | `payment_id` (as `collect_link` answered it, or as a payment event carried it) | `status` (`paid` \| `pending` \| `failed` \| `refunded`), `amount_kobo`, `currency`, `paid_at` | read |
| `virtual_account` | `customer {name, email, phone, bvn}` | `account_number`, `bank_name`, `account_name`, `reference` (what `virtual_account.credited` carries) | idempotent preferred; unsafe allowed and declared |
| `refund` | `payment_id`, `amount_kobo` (optional: partial), `reason` | `refund_id`, `status` (`refunded` \| `pending` \| `failed`), `amount_kobo` | idempotent or reconcilable |

Events, normalised the same way (§4.5): `payout.settled`, `payout.failed`, `payout.reversed`, `payment.received`, `payment.returned` (added by T1), `virtual_account.credited`, `pos.sale` (added by T2: a POS terminal's transaction with its `status`, approved, pending, declined or reversed, from `moniepoint_pos@1` and `opay_business@1`).

No capability takes a reference: the reference a rail needs is the idempotency key Taskiem derives for the step (one for each step of a composite), which is what makes a retry or a resend the same payment.

The set is deliberately small. A capability is added when a second template needs it on a second rail; a rail-specific action stays available to templates that want it by name.

### 4.2 Declaring capabilities in a rail's manifest

A new optional `capabilities` block in `connector/v1` (schema `schemas/connector-v1.schema.json`, contract [rule 16](../contracts/connector-v1.md)). Each entry names the rail action and maps the capability's input to it and the action's output back, with the same expression language steps use. A string starting with `=` is an expression, anything else a value as it is; an expression that comes out null (`=input.?source` when no source was given) leaves the field out. As built for iswallet:

```yaml
# connectors/iswallet/manifest.yaml
capabilities:
  balance:
    action: get_balance
    input: { wallet_id: "=input.account" }
    output:
      available_kobo: "=sum(output.balances.filter(b, b.currency == input.currency).map(b, b.available))"
      currency: "=input.currency"
    requires: [account]
  pay_bank:
    action: payout
    input:
      wallet_id: "=input.source"
      amount: "=input.amount_kobo"
      bank_code: "=input.bank_code"
      account_number: "=input.account_number"
      account_name: "=input.account_name"
      narration: "=input.narration"
    output:
      payout_id: "=output.outflow_id"
      status: "=output.status == 'confirmed' ? 'settled' : output.status in ['failed', 'reversed'] ? 'failed' : 'pending'"
      fee_kobo: "=output.?fee"
    requires: [source]
    fixed: { currency: NGN }
    settles_by: { trigger: outflow_event, lookup: get_payout }
  pay_wallet:
    action: transfer
    input: { from_wallet_id: "=input.source", to_wallet_id: "=input.wallet_id", amount: "=input.amount_kobo", currency: "=input.currency", narration: "=input.narration" }
    output: { payout_id: "=output.transfer_id", status: settled, fee_kobo: 0 }
    requires: [source]
events:
  payout.settled:
    trigger: outflow_event
    when: "=body.event_type == 'wallet.outflow.confirmed'"
    fields: { payout_id: "=body.data.outflow_id", amount_kobo: "=body.data.?amount" }
```

Besides the mapping, an entry says how the rail meets the capability's optional inputs: `requires` (it cannot do without them: iswallet pays from a named wallet), `unsupported` (it cannot honour them, so a step that gives one is refused rather than quietly ignored: Paystack refunds in full), `fixed` (one value only: `currency: NGN`) and `ignores` (not needed; dropped: Paystack's checkout asks no phone number). `settles_by` says how a pending payout ends: the trigger whose payout events report it, and the `get_payout` lookup. `notes` is free text for reviewers.

**Composite capabilities.** Some rails need two calls for one capability: Paystack pays a bank account by creating a transfer recipient and then a transfer. A mapping may list `steps:` (`id`, `action`, `input`) instead of one `action:`; a step's input reads earlier steps as `steps.<id>.output`, and the output mapping reads `output` (the last step's) and `steps`. Expansion (§4.4) produces both steps, each with its own idempotency seed derived from the capability step's, so both remain exactly-once.

**Validation** (`engine/connector`, at registration and in `taskiem validate`; every rule in [rule 16](../contracts/connector-v1.md)):

- the action exists, and its class meets the capability's requirement (§4.1); `unsafe_write` only with `unsafe: true`;
- every required action input is mapped, no mapped key is unknown to the action, and none is its idempotency field;
- every required capability output is produced and nothing else is; every expression compiles;
- every capability input is used or listed under `ignores`, `unsupported` or `fixed`; an optional one a required action input needs is listed under `requires`;
- personal (`x-pii`) inputs reach only action inputs marked `pii`, and personal outputs come only from action outputs marked `pii`;
- a payment that may be pending declares `settles_by`, whose `trigger` produces `payout.settled` and `payout.failed` and whose `lookup` is the rail's `get_payout`;
- events name a trigger, and give every required field.

A rail that cannot meet a capability simply does not declare it.

### 4.3 Capability steps in a workflow

A new step form in `wd/v1` (`schemas/wd-v1.schema.json`, `docs/contracts/wd-v1.md`):

```json
{
  "id": "pay",
  "type": "capability",
  "uses": "pay_bank",
  "connection": "{{rail_connection}}",
  "input": {
    "amount_kobo": "=item.net_pay_kobo",
    "currency": "'NGN'",
    "bank_code": "=item.bank_code",
    "account_number": "=item.account_number",
    "account_name": "=item.account_name",
    "narration": "='Salary ' + steps.settings.output.period"
  },
  "effect": { "idempotency_seed": "=steps.settings.output.payroll_id + ':' + item.employee_id" },
  "retry": { "max_attempts": 8, "backoff": "exponential", "initial": "5s", "max": "10m" }
}
```

`steps.pay.output` is the capability's output shape, whichever rail runs it. The flow-code SDK (`sdk/`) gains `pay.bank(...)`, `pay.wallet(...)`, `collect.link(...)` and the rest, generated from `schemas/capabilities-v1.json` by `go generate ./sdk`.

### 4.4 Expansion at deploy

When a version is published or deployed to an environment, `engine/ingest` resolves each capability step:

1. The step's `connection` names a connection in that environment; its connector is the rail.
2. The rail's manifest must declare the capability; if not, publishing is refused with a plain reason: *"Your Flutterwave connection cannot open virtual accounts. Connect a rail that can, such as Moniepoint."*
3. The step becomes the rail's own action step (or steps, for a composite), with the input mapped through the manifest's `input` expressions, and an output projection that turns the action's output into the capability's shape (`steps.pay.output` keeps its meaning).
4. The step keeps its id, `when`, `retry`, `on_error` and `effect.idempotency_seed`. The rail action's class, idempotency encoding, reconciliation and pii rules apply unchanged.
5. The expanded definition is stored with the deployed version and recorded in its audit entry, so the run history, the canvas's run view, replay (`decide.Verify`) and drift checks all see the real provider call.

The editable definition keeps the capability step. Changing the connection to another rail and redeploying re-expands it; runs already in flight finish on the version they started with.

Why expansion and not a dispatcher connector: see decision 0044, alternatives. In short, every rail's exactly-once behaviour is already written and tested as an ordinary action; expansion reuses it as it is.

### 4.5 Waiting for a payout to settle

A pending payout (`status: pending`) is followed in templates by a `settle` step:

```json
{ "id": "settle", "type": "capability", "uses": "await_settlement",
  "input": { "payout_id": "=steps.pay.output.payout_id" },
  "timeout": "72h", "when": "=steps.pay.output.status == 'pending'" }
```

`await_settlement` expands, from the rail's `settles_by`, into:

- **a rail with events:** a `parallel` with `join: any` over signals for `payout.settled`, `payout.failed` and `payout.reversed`, each correlated on the payout id, plus a deadline branch;
- **on timeout, or a rail without events** (Remita, Interswitch): a loop of `get_payout` lookups (every 15 minutes for the first hour, then hourly, up to the timeout).

Output: `status` (`settled` \| `failed` \| `unconfirmed`) and `settled_at`. This removes the one-event-per-signal limit (§3.1) for every template, and makes the reversal watch (§5.1) one more capability step, `await_reversal`, with the same shape.

### 4.6 Connections by capability

A template param of `type: "connection"` gains `capability: "pay_bank"` (or a list). Everywhere a connection is chosen, only connections whose rail declares the capabilities are offered, with the rail's name: "Your iswallet (prod)", "Your Paystack (prod)".

- **Template loader** (`templates/templates.go`): a `capability` param replaces `connector`; the template's `connectors` list may hold `capability:pay_bank` instead of a connector id, and the declared-equals-used check counts capability steps.
- **Start page and Guide** (`web/src/pages/Start.tsx`): "Connect a rail that can pay bank accounts" lists the capable rails, best known first (Paystack, Moniepoint, iswallet, Flutterwave, Anchor), and an already-connected capable rail is picked by default.
- **Chat build** (`api/whatsapp_build.go`): the param prompt asks "Which account should pay? 1. Paystack 2. iswallet" from the business's capable connections, or offers to connect one.
- **AI builder** (`engine/ai/builder`): the prompt describes capabilities next to connectors, and the rules prefer a capability step whenever the request does not name a provider. `tools/aieval/rules.go` gains `capability_when_unnamed`; eval cases in `evals/builder/payouts.jsonl` are updated.
- **Dry run** (`engine/ai/builder/dryrun.go`): samples capability outputs from `schemas/capabilities-v1.json`, so a template is checked without choosing a rail.

### 4.7 Per-rail mapping

What each rail's `capabilities` block will declare in R1 (✓), declare later (later), or cannot (—). Notes say what is not obvious.

| Rail | balance | resolve_account | pay_bank | pay_wallet | get_payout | settles by | pay_many | collect_link | get_payment | virtual_account | refund |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| paystack | ✓ | ✓ | ✓ composite: `create_recipient` + `transfer` | — | ✓ `verify_transfer` | events (`transfer_event`), polling fallback | — | ✓ `initialize_transaction` | ✓ `verify_charge` | — | ✓ |
| iswallet | ✓ per wallet | ✓ `name_enquiry` | ✓ `payout` | ✓ `transfer` (final) | ✓ `get_payout` | events (`outflow_event`) | — | — | — | — | — |
| moniepoint | ✓ | ✓ | ✓ `transfer`; `PENDING_AUTHORIZATION` (OTP by email) maps to `pending` with reason `needs_otp` | — | ✓ | events | — | ✓ `init_transaction` (40 min) | ✓ | ✓ `create_reserved_account` | — |
| flutterwave | ✓ | ✓ | ✓ `transfer` (queued) | — | ✓ `get_transfer_by_reference` | events (`transfer.completed`, status in the body); no reversal event, so `await_reversal` uses lookups | — | — (no create action yet) | ✓ `verify_payment` | — | — |
| anchor | ✓ | ✓ `verify_account` | ✓ `transfer` (refuses on name mismatch) | ✓ `book_transfer` | ✓ | events | — | — | — | — | — |
| lenco | ✓ | ✓ | ✓ | — | ✓ | events (no reversal event) | — | — | — | later: `create_virtual_account` is unsafe (an unknown outcome parks), declared with `unsafe: true` | — |
| remita | — | ✓ | ✓ | — | ✓ | lookups only | ✓ native `bulk_transfer` | ✓ `generate_invoice` (RRR) | ✓ `get_invoice` | — | — |
| interswitch | — | ✓ | ✓ (name check and payout in one) | — | ✓ | lookups only | — | — | ✓ `get_payment` | — | — |
| mtnmomo | ✓ | ✓ holder name | — | as `pay_mobile_money` | ✓ | events | — | as `collect_push` | ✓ | — | — |
| mpesa | later (async) | — | — | as `pay_mobile_money` (`b2c_payment`) | later (async) | events | — | later (`stk_push` is unsafe) | later | — | — |
| opay | — | — | — | — | — | — | — | ✓ `create_cashier_payment` | ✓ `get_payment` | later (temporary accounts only) | ✓ |

**`pay_many`** is declared only where the rail has a real bulk endpoint (Remita). Elsewhere, templates use a `foreach` over `pay_bank`, which is also what makes per-person exactly-once, failure isolation and per-person messages straightforward. A template may prefer `pay_many` when the rail has it (fewer calls, one bank fee line on some rails) with `prefer: ["pay_many", "pay_bank"]`; expansion picks the first the rail declares.

**Gaps found while mapping**, each a small connector change, fixed in R1 where a template needs it:

- Flutterwave has no action that creates a payment; `collect_link` needs Flutterwave Standard's payment link endpoint added.
- Remita and Interswitch send no payout events; settlement is by lookup (§4.5).
- Paystack's `transfer` can answer `otp` when the business has OTP on transfers turned on; the capability maps it to `failed` with reason `otp_enabled` and the connection check warns: *"Turn off OTP for transfers in your Paystack dashboard so Taskiem can pay."*

---

## 5. Products built on rails, as templates

Every template here is written once with capability steps, tested on every rail that declares its capabilities (§10), and plain enough for the no-technical-steps rule: no ids, kobo or expressions for the owner to type.

### 5.1 Run payroll

The dogfood flow, rebuilt for any business and any rail. File: `templates/library/run-payroll.json` (flow code: `templates/library/run-payroll.flow.ts` if the library takes flow code by then).

**For:** a business paying staff monthly, with or without a payroll product. A business that uses Payrolla does not use this template; it uses the Payrolla templates (§6.4).

**Params** (asked in this order, in the Guide and in chat):

| Param | Type | Default | Notes |
| --- | --- | --- | --- |
| `business_name` | string ≤ 60 | the business's name | In messages to staff |
| `staff_source` | table (picker) or sheet | creates `staff` | Where the staff list lives |
| `pay_day` | integer 1–28 | 25 | Day of the month; the run starts at 08:00 Lagos |
| `rail_connection` | connection, capability `pay_bank` (+ `pay_wallet` if the rail has it) | the business's only capable rail | Which account pays |
| `funding_account` | string, optional | — | Only for rails with several wallets or sub-accounts (iswallet, Anchor); a picker lists them |
| `approver_role` | role | owner | Who releases the money |
| `approval_hours` | integer 1–72 | 24 | After that, nothing is paid and the owner is told |
| `tell_staff` | boolean | true | WhatsApp or SMS to each person when paid (notify@1) |
| `reversal_days` | integer 1–60 | 35 | How long to watch bank payouts for reversals |

**Tables it makes** (when they do not exist):

- `staff`: `name` (text), `phone` (phone), `email` (email), `net_pay` (number, naira), `bank_code` (text), `account_number` (text, pii), `account_name` (text, pii), `wallet_id` (text, optional), `active` (boolean).
- `payroll_runs`: `period` (text), `status` (text), `staff_count`, `total_naira`, `paid`, `failed`, `fees_naira`, `approved_by` (text), `approved_at` (datetime).
- `payroll_lines`: `period`, `staff_name`, `amount_naira`, `rail` (text), `status` (text: paid, failed, unconfirmed, reversed), `payout_id`, `reason`, `settled_at`.

**Steps:**

1. **Settings**: period (`YYYY-MM` in Lagos), a payroll id `<period>-<run id>` used as the idempotency root.
2. **Read staff** from the table or sheet; drop inactive rows; validate each row (amount ≥ ₦1, bank details or a wallet); invalid rows go to the summary as `refused` with a reason, never paid.
3. **Check names** (`resolve_account` per bank row, five at a time): a name that does not match the row's `account_name` closely is flagged on the approval, not paid silently.
4. **Balance** (`balance` on `funding_account`): shown on the approval; if it is below the total plus estimated fees, the approval says so in its first line.
5. **Approve** (role `approver_role`, `forbid_self_approval` when the business has more than one member, step-up `whatsapp_pin` when the business has set a PIN). Subject: period, people, total, how many to banks, balance, flagged names. Timeout `approval_hours`, then rejected.
6. **Pay** (`foreach` staff, `max_concurrency: 5`, `max_items: 5000`):
   - `pay_wallet` when the row has a wallet and the rail can pay wallets, else `pay_bank`; seed `payroll_id:row id`.
   - `settle` (`await_settlement`, 72 h) when pending.
   - record a `payroll_lines` row.
   - `tell` the person (notify@1 `text`): "Your salary for October from Adunni Bakes, ₦85,000, has been paid." Only once settled.
   - `on_error`: the line records `failed` with the reason; the loop continues.
7. **Summary**: totals, update `payroll_runs`, a message to the approver and owner: paid, failed (with names and reasons), unconfirmed, fees.
8. **Reversal watch** (`await_reversal` per settled bank payout, until `reversal_days`): each reversal updates its line, tells the owner, and is listed in a weekly digest. It never pays anyone again.
9. **Rejected or expired**: `payroll_runs` row with the reason, message to the owner; nothing paid.

**Guarantees** (each pinned by a test, §10): nobody is paid twice for the same payroll, even across retries, a crash mid-loop, a resend of the run, or a fork; nothing moves before approval; one failure never stops the others; the reversal watch never pays; personal fields are sealed at rest and out of logs and the AI.

**Supersedes** `salary-payout-approval` (Paystack-only, no confirmation, no records). Its users are offered "Upgrade to Run payroll" on the template's workflows page; the old template is hidden from the picker but kept loadable for existing workflows (§7).

### 5.2 Pay a list

`templates/library/pay-a-list.json`. Pay everyone on a sheet or table once, with one approval: suppliers, refunds owed, prizes, commissions. Same steps as Run payroll without the schedule, staff messages optional, `pay_many` preferred where the rail has it. Params: source, rail connection, approver, a reference for the batch (shown on statements).

### 5.3 A payment link per order

`templates/library/payment-link-per-order.json`. When a new row lands in `orders` (or a storefront order is accepted), `collect_link` creates a link for the amount; the customer gets it by WhatsApp or SMS; `payment.received` (or a `get_payment` loop where the rail has no event) marks the row paid and thanks the customer; unpaid after N hours, one reminder. Rails: Paystack, Moniepoint, Remita (RRR), OPay. This also replaces the storefront's Paystack-only `Payments` interface in a later step (out of scope here, §12).

### 5.4 A virtual account per customer

`templates/library/virtual-account-per-customer.json`. Each new customer row gets a dedicated account number (`virtual_account`), stored on the row and sent to the customer; `virtual_account.credited` matches the credit to the customer and their open invoices, marks them paid, and thanks them; a credit that matches nothing goes to the owner. Rails: Moniepoint first; Lenco once its unsafe create is acceptable to the owner (the Guide says what happens when an outcome is unknown).

### 5.5 Refund with approval

`templates/library/refund-with-approval.json`. A refund request (form, chat or table row) → approval with the original payment shown (`get_payment`) → `refund` (full or partial) → tell the customer. Rails: Paystack, OPay.

### 5.6 Other money templates moved onto capabilities

| Template | Change |
| --- | --- |
| `supplier-payment-approval` | Paystack → `pay_bank` on any rail |
| `failed-transfer-alert` | One rail's event → `payout.failed` and `payout.reversed` on any rail |
| `transfer-received-notify` | → `payment.received` / `virtual_account.credited` |
| `low-balance-alert`, `weekly-balance-email` | → `balance` |

Each keeps its id so existing workflows and links keep working; the template test proves the new version on every capable rail.

---

## 6. Products as connectors

### 6.1 What a product connector is

An ordinary `connector/v1` connector, built-in or catalogue, with three parts:

- **Triggers** from the product's webhooks, verified by the product's signature scheme (rule 7), each with a `correlation` so steps can wait on them.
- **Actions** from the product's API: reads (get a payroll, list employees, fetch a payslip) and writes where the product offers them (approve or reject, add an employee), each classed as rule 1 requires.
- **Views** (new, §6.3): the product's embeddable interface, opened inside Taskiem.

A product connector never declares rail capabilities: it does not pay, it reports what it paid.

Manifests gain `kind: rail | product | tool` (default `tool`), used by the catalogue (R9), the AI builder (a product's events are preferred over rebuilding its process) and the rule in decision 0044.

### 6.2 `payrolla@1`

**Confirmed** against Payrolla's Connect API, webhook and embedding references (23 October 2026, kept in [`integrations/payrolla-reference`](../integrations/payrolla-reference/02-taskiem-integration-guide.md)); built in R5. The draft guessed differently in the places marked.

| Part | Confirmed |
| --- | --- |
| Auth | `api_key`: one key per company, made by its owner with chosen scopes (`payroll:read`, `employees:read`, `webhooks:manage`, `payslips:links`, `embed:launch`); no OAuth |
| Base URL | `https://synledger.name.ng/payrolla/api/v1`, overridden by `TASKIEM_PAYROLLA_URL`. Payrolla's test company lives in production, with an unfunded wallet |
| Triggers | Other names than the draft's: `run_event` (`run.computed` carrying `approval_url`, `run.approved`, `run.rejected`, `run.locked`, `run.cancelled`, `run.disbursement_started`, `run.completed`, `run.partially_completed`), `payout_event` (`payout.succeeded`, `payout.failed`, `payout.reversed`), `employee_event` (`employee.created`, `employee.updated`, `employee.terminated`), `payslip_event` (`payslip.issued`); `ping` on request |
| Verification | `hmac_sha256_timestamped` with `signature_format: t_v1` on `Payrolla-Signature`, 5 minutes' tolerance |
| Routing | Per company: the subscriptions are REST hooks made with each company's key, so Taskiem registers them remotely (decision 0021), one per deployed trigger; no partner routing |
| Actions (reads) | `get_company`, `list_runs`, `get_run`, `get_run_lines` (each employee's net pay and payout status), `get_approval_link`, `list_employees`, `get_employee`, `list_events` (30 days of replay) |
| Actions (writes) | None that change payroll: **no approve, reject or add employee** (approval is the approver's own, in Payrolla). `create_payslip_link` issues a 7-day link (an unsafe write: Payrolla takes no idempotency key) |
| Views | A launch token from Taskiem's server and Payrolla's SDK, not URL templates: modes `full`, `launch` (`run-review`, `team`, `payslip`) and `payout`; the owner lists the allowed origins. For R6, which revises §6.3 to match |
| PII | Events and reads carry names, emails, phones and net pay, never bank account numbers, salary breakdowns or ID numbers; the reads mark names, emails, phones and payslip links `pii` |

Docs: the connector's page is [`integrations/payrolla.md`](../integrations/payrolla.md); the old partner guide stays [archived](../integrations/archive/payrolla.md).

### 6.3 App views: a product's interface inside Taskiem

**Manifest** (new optional `views` block, schema and rule 17):

```yaml
views:
  payroll:
    title: Payroll
    url: "https://app.payrolla.ng/embed/payrolls/{payroll_id}?company={connection.company_id}"
    params: [payroll_id]
    origins: ["https://app.payrolla.ng"]
    height: 720
    events:                       # postMessage events the view may send
      payroll.approved: { payload: { payroll_id: string } }
      view.close: {}
```

**Where views open:**

- **The connection's page** (Connections → Payrolla): a "Open Payrolla" panel with the connector's default view.
- **A run step**: a new `view` step type (or an action output field `view: payroll`) shows "Open in Payrolla" on the run page and in the approval inbox, with the view's params filled from the run.
- **Chat**: views do not embed in WhatsApp or Telegram; the message carries the same view as an https link that opens it in the browser.

**Security:**

- The app's Content-Security-Policy gains `frame-src` for the exact `origins` of views on connectors the business has connected, computed per tenant at request time (extends `AppCSP`, `engine/httpsec/httpsec.go`, and `securityHeaders` in `api/server.go`). Nothing else may be framed.
- The frame is `sandbox="allow-scripts allow-forms allow-same-origin allow-popups"` scoped to the product's origin; it never sees Taskiem's session cookie (different origin) or any secret. The product signs its user in by its own means.
- `postMessage` from the frame is accepted only from a declared origin and only for declared events; an event can complete a waiting step (as a `signal`) but cannot start payments or change Taskiem data directly.
- Views are links on hosts that forbid framing (the product's own `frame-ancestors`); the panel falls back to "Open in a new tab".
- The product's `frame-ancestors` must list Taskiem's app origin, and partner-embedded deployments' origins; this is in the R0 checklist with Payrolla.

### 6.4 Templates around Payrolla

Each one moves no money: Payrolla pays, Taskiem tells, records and asks.

| Template | Trigger | Does |
| --- | --- | --- |
| `payrolla-tell-staff` | `payout.succeeded` | WhatsApp or SMS to the employee with the amount and a payslip link |
| `payrolla-payroll-summary` | `payroll.paid` / `payroll.failed` | Summary to the owner: paid, failed with reasons, total; failed ones flagged |
| `payrolla-approve-by-chat` | `payroll.locked` (if Payrolla's API can approve) | Approval request in WhatsApp with PIN; on yes, `approve_payroll`; on no, `reject_payroll`; "Open in Payrolla" view link |
| `payrolla-reversal-alert` | `payout.reversed` | Alert to the owner and payroll admin |
| `payrolla-record-payroll` | `payroll.paid` | Rows in the business's `payroll_runs` / `payroll_lines` tables or a Google Sheet, for the accountant |
| `payrolla-payday-check` | schedule, N days before pay day | Reads the funding rail's `balance` (iswallet or other) and the upcoming payroll total; warns if short |
| `payrolla-lock-reminder` | schedule | Reminds the payroll admin to lock the payroll before the cut-off |

---

## 7. Retiring Payrolla-as-a-rail

Everything below was found by searching the repository for Payrolla and reading the generic partner code built with it.

**Removed or rewritten (Payrolla-specific):**

| Item | Action |
| --- | --- |
| `flows/dogfood/payrolla-salary-disbursement.flow.ts`, `.wd.json` | Removed once Run payroll's tests carry its guarantees (R3) |
| `flows/dogfood/tests/payrolla-salary-disbursement.test.json` (10 cases) | Each case re-expressed against `run-payroll` on fake iswallet and fake Paystack (R3), then removed |
| `flows/dogfood/README.md` §1 | Rewritten: the dogfood flows no longer include Payrolla |
| `engine/decide/decide_test.go` `TestPayrollaDogfoodEndToEnd`, `TestPayrollaRejectedPayrollIsReported` | Replaced by `TestRunPayrollEndToEnd`, `TestRunPayrollRejected` on the template |
| `engine/decide/sim_test.go` (`payrolla_api_url` in the sim env) | Removed |
| `e2e/triggers_test.go` `TestPayrollaDisbursementEndToEnd` | Replaced by `TestRunPayrollEndToEnd` (real stack, fake rails); the generic webhook checks it made (signature, dedup, forged webhook 401) move to a generic webhook test |
| `evals/builder/seed.jsonl` "payrolla-salary", `evals/builder/baseline.json` | Replaced by a "run payroll on my rail" case; baseline regenerated; `tools/aieval/main_test.go`'s count of dogfood cases updated |
| `evals/repair/failures.jsonl` (4 cases pointing at the flow) | Re-pointed at `run-payroll` |
| `docs/integrations/archive/payrolla.md` | Moved to `integrations/archive/`, superseded; replaced by the connector page (§6.2) |
| `docs/contracts/wd-test-v1.md` example | Uses the run-payroll test file |
| `tools/docsite/pages.go`, `main_test.go` | The archive page stays internal; the new connector page is public |

**Kept (generic partner machinery, used by any partner):** `api/partner*.go`, `engine/ingest/route.go`, iswallet's `shared_client` and `wallet_ids`, migrations 00045, 00047, 00056, 00170, 00171, decisions 0030 and 0031. Their tests use "Payrolla" only as a fixture name; renaming the fixture to a neutral partner is optional and done only where the test is touched anyway. `api/partnerpackages_test.go` copies the old flow's path and variables as a fixture; it is changed to a neutral package so it does not look like a Payrolla contract.

**Docs and tracking updated:** `docs/embedding.md` (the `x_payrolla@1` and install examples become a neutral partner), `docs/integrations/iswallet.md` (Payrolla named as an iswallet user, not as our payer), `docs/partner-tracker.md` (Payrolla items N1–N7 withdrawn; new items: API docs, webhooks, embed origins, sandbox), `docs/needs-people.md` (D1, D2, D7, D9 closed; R0 items added), `docs/spec/build-plan.md` (the Payrolla dogfood acceptance test becomes Run payroll's).

**People:** Payrolla's engineers are told, before they build the webhook sender or the report and notice endpoints from the old guide, that none of it is needed (R0).

---

## 8. Milestones

Sizes: S up to a week, M one to two weeks, L two to four weeks, for one engineer.

### R0: decisions and Payrolla's documents (people, alongside R1)

| Item | Owner | Blocks |
| --- | --- | --- |
| **R0-1 Tell Payrolla.** Withdraw the old guide's asks before they build them; explain decision 0044 | Founders | Nothing; urgent so no work is wasted |
| **R0-2 Payrolla's API.** Auth (key or OAuth), sandbox, webhook events and signature, approval API (if any), payslip access, rate limits | Payrolla | R5 |
| **R0-3 Payrolla's embed.** Embeddable URLs, how its user is signed in inside a frame, `frame-ancestors` for Taskiem's origins, postMessage events | Payrolla | R6 |
| **R0-4 One subscription or one per company?** Whether Payrolla's webhooks are set per company (per-connection URLs) or once for all its companies (partner routing) | Payrolla + Taskiem | R5 |
| **R0-5 Rail priorities.** Which rails get capabilities first (proposed: Paystack, iswallet, Moniepoint, Flutterwave, Anchor) | Founders | R1 order |

### R1: Capabilities in rail manifests (L)

**Problem.** Money templates are tied to one rail each; a business on iswallet or Moniepoint cannot use a template written for Paystack.

**Build:**

- `schemas/capabilities-v1.json`: the capability set of §4.1, inputs and outputs, `x-pii`, the normalised events.
- `connector/v1`: the `capabilities` and `events` blocks (§4.2), composite `steps`, `settles_by`; schema, contract rule 16, validation in `engine/connector`, `taskiem validate`.
- Capability blocks for Paystack, iswallet, Moniepoint, Flutterwave and Anchor (R0-5), then Lenco, Remita, Interswitch, MTN MoMo, OPay as §4.7 says.
- The connector gaps of §4.7 that these need (Paystack OTP warning).
- `engine/conntest`: a capability conformance suite: each declared capability is replayed against the rail's fixtures and its output checked against the capability schema; normalised events produced from recorded webhooks.

**Done when:** every listed rail declares its capabilities and passes the conformance suite; a manifest that maps a capability to a wrong-class action, or misses an output field, fails validation with a clear message; `go generate ./sdk` produces the capability types.

**Built** (first pull request): the capability set, rule 16 and its validation, the conformance suite (`conntest.RunCapabilities`, cases in `connectors/<rail>/testdata/capabilities/`), the SDK's capability types (`Capability<Name>Input`, `Capability<Name>Output`, `CAPABILITY_RAILS`), and the blocks for Paystack, iswallet, Moniepoint, Flutterwave and Anchor as §4.7 lists them, with these differences found on the way:

- iswallet does not declare `list_banks`: its bank list's fields are not documented, so the mapping cannot be checked.
- Paystack's OTP case is mapped (`failed`, reason `otp_enabled`), but the connection check's warning is not built: Paystack has no endpoint that says whether OTP is on.
- Moniepoint's two-factor transfers answer `pending` with reason `needs_otp`; Flutterwave's transfers waiting for approval in its dashboard answer `pending` with reason `needs_approval`.
- A payout Paystack does not know is a `not_found` error from `get_payout`, not a status.

**Left for a second pull request:** Lenco, Remita, Interswitch, MTN MoMo and OPay.

### R2: Capability steps and expansion (L)

**Problem.** Workflows can only name connector actions.

**Build:**

- `wd/v1`: the `capability` step type (§4.3), `await_settlement` and `await_reversal` (§4.5); schema, contract, `wdcheck` (capability exists, input shape, connection param has the capability).
- Expansion at deploy in `engine/ingest` (§4.4): resolution, refusal messages, composite expansion with derived seeds, output projection, the expanded definition stored with the deployment and audited.
- Canvas: a capability step shows as "Pay a bank account (via your Paystack)"; the step panel picks the connection among capable ones; the run view shows the real provider step.
- Flow code (`engine/flowcode`, SDK): `pay.bank`, `pay.wallet`, `collect.link`, …, round-trip lossless.
- `decide.Verify` and drift checks run on the expanded definition.

**Done when:** a workflow with capability steps deploys on each R1 rail, its runs show the rail's real calls, the same definition redeploys on another rail without edits, exactly-once holds across a crash mid-loop on every rail (§10), and changing rails while runs are in flight leaves them on their version.

**Built** (R2): the `capability` step ([wd-v1, capability steps](../contracts/wd-v1.md#capability-steps)), `await_settlement` and `await_reversal`; expansion (`engine/expansion`) with plain refusals; the run's expansion pinned and stored by digest; decide maps capability input to the action's, the worker maps the answer back (`provider_output` kept); ingest delivers each rail's normalised events as signals; deploys refuse steps the environment cannot run and record the rails; `GET /v1/capabilities`; the SDK's `pay.bank`, `pay.wallet`, `collect.link`, `rail.balance`, `waitFor.settlement` and the rest, round-tripped by codegen; the canvas's capability step with a connection picker among capable connections. Differences from the plan above:

- Expansion happens when a run starts, against the environment's connections, rather than once at deploy: the result is the same for every run until a connection changes, and is stored once by digest. A deploy expands too, to refuse a connection that cannot do a step and to record the rails; an environment not connected yet is let through (publishing deploys to dev and prod at once), and its runs are refused in plain words until it is.
- `await_settlement` on a rail without payout events (lookups only) is refused for now; none of the R1 rails needs it.
- In a composite, an earlier call (Paystack's recipient) handles its failure with a copy of the step's `on_error`, its steps renamed `<id>__<name>` and reading that call's error; a `foreach` then reports the failure under the call's id (`pay__recipient`).

### R3: Run payroll (M)

**Problem.** Taskiem's best payroll code is wired to one partner's payload and one rail.

**Build:**

- `templates/library/run-payroll.json` as §5.1: params with `capability`, tables, steps, guarantees, messages, plain description and details.
- Template loader: `capability` connection params, `connectors` holding capabilities; Start page and Guide, chat build and the AI builder as §4.6.
- The dogfood flow's ten workflow tests re-expressed against `run-payroll`, run on fake iswallet and fake Paystack; `TestRunPayrollEndToEnd` on the real stack.
- `salary-payout-approval` hidden from the picker with an upgrade note.
- AI eval: "pay my staff on the 25th from my iswallet" and "…from Paystack" produce the template's shape; baseline regenerated.

**Done when:** an owner with iswallet, and another with Paystack, set up Run payroll from the Start page or chat without help; every guarantee of §5.1 is pinned by a test on both rails; the old dogfood tests' cases all pass against the template.

**Built** (R3): `run-payroll` (`templates/library/run-payroll.json`, with its three tables), `connection` parameters by `capability`, `capability:<name>` connectors, `superseded_by` (the old salary template), twelve workflow tests in `templates/tests/run-payroll.test.json` (the dogfood flow's cases re-expressed, and the name check, refused rows and staff messages), `TestRunPayrollEndToEnd` (`api/runpayroll_test.go`: the template from the API with data tables and Paystack, approval, one payment per person, the events, the records and a reversal), readiness and the Start page and Guide by capability, chat's needs naming the rails, the AI builder's capabilities and rule, the dry run sampling capability outputs, the plain read-back of capability steps, and the eval cases (`capability_when_unnamed`; baseline regenerated). Differences from §5.1:

- **Staff from a data table only**, not yet a sheet; Taskiem makes the table.
- **Bank payouts only:** `pay_wallet` for rows with a wallet waits until a template can tell which rail it runs on (a capability step the rail cannot do refuses the whole run at expansion).
- **The idempotency root is the month** (`payroll:<period>:<row id>`), not the run, so a second run for the same month never pays anyone twice; paying someone who failed is done by hand.
- **No weekly reversal digest and no step-up PIN** on the approval yet; the owner is told of each reversal as it comes.
- **Names that do not match are not paid**, and the approval lists them, rather than paid after a flag.
- **Chat asks for no rail:** the connection defaults to the only one that can, and readiness says what to connect, naming the rails.
- **The real-stack test runs on Paystack;** the other rails are covered by the workflow tests (which mock the capability's output, the same on every rail) and R2's rail tests.
- **The eval baseline changed:** with the Paystack-only salary template gone from matches, the offline (heuristic) builder finds no template for some generic "approve, then pay a list" requests; R4's *Pay a list* template is the one for them.

### R4: More templates on rails (M)

**Build:** Pay a list (§5.2), a payment link per order (§5.3), a virtual account per customer (§5.4, Moniepoint), refund with approval (§5.5), and the five existing money templates moved onto capabilities (§5.6), each with template tests on every capable rail, Start page starters and AI eval cases.

**Done when:** each template runs on at least two rails in tests; the moved templates keep their ids and existing workflows keep running.

**Built** (R4): a `capability_event` trigger (wd/v1) that starts on a rail's normalised payment events whichever rail is connected, registered as each sending rail's own connector trigger and started by ingest from the normalised event only; an `await_payment` wait (a `collect_link` payment's `payment.received` raced against a deadline, then `get_payment`); and the templates `pay-a-list` (§5.2), `payment-link-per-order` (§5.3), `virtual-account-per-customer` (§5.4) and `refund-with-approval` (§5.5), with `supplier-payment-approval`, `failed-transfer-alert`, `transfer-received-notify`, `low-balance-alert` and `weekly-balance-email` moved onto capabilities under their ids (§5.6). `TestTemplatesOnEveryCapableRail` expands every rail template on each built-in rail that can run it, and wants two; each template has workflow tests in `templates/tests/`. Readiness, the Start page and the connection picker know payment events (`GET /v1/capabilities` lists them as `kind: event`); the website names the rails each template runs on; the builder evals grade a request that names a rail as met by a capability that rail runs, with eight new cases. Differences from §5:

- **One rail for two templates for now:** `virtual-account-per-customer` runs on Moniepoint only (Lenco's account is unsafe, OPay's temporary) and `refund-with-approval` on Paystack only (OPay's refund is not declared yet). The test names both and fails when another rail can.
- **Pay a list pays one by one** (`pay_bank` per row, five at a time): no R1 rail declares `pay_many`, so `prefer` is not built yet. Rows come from a data table by batch, not a sheet.
- **A payment link is per run, not per new row:** the order's details start the run (manual, or the API), and one reminder is sent after `pay_hours`; the outcome is a row in `order_payments`.
- **Credits to customers' account numbers** are told and logged by `transfer-received-notify` (`virtual_account.credited`), not matched to invoices inside the template.
- **Refunds are in full:** Paystack refunds in full, so the template asks no amount.
- **The moved templates changed what they ask:** the supplier template takes the supplier's bank account (checked with `resolve_account`) instead of a Paystack recipient code, and follows the payment until the bank confirms; the weekly email reports one payments account's balance, not two. Workflows made from the old versions keep their own definitions and keep running.
- **Found on the way:** `await_reversal` on a rail without a reversal event (Flutterwave) was refused at expansion, so Run payroll could not run there; it now looks the payout up when the watch ends.

### R5: The Payrolla connector (M; needs R0-2, R0-4)

**Build:** `connectors/payrolla` (manifest, Go code, fixtures from Payrolla's sandbox), triggers and actions as confirmed in R0, verification, routing per R0-4, `kind: product`, `docs/integrations/archive/payrolla.md` rewritten as the connector page, the old guide archived, SDK regenerated, conntest suite.

**Done when:** a business connects Payrolla from Connections, a sandbox payroll's events start a test workflow, the reads return sealed personal fields, and the connector passes conformance.

**Built** (R5): `connectors/payrolla` (`payrolla@1`, `kind: product`, no capabilities): the four triggers above, registered remotely per company and verified with Payrolla's own test vector; the eight reads and the payslip link, each against fixtures shaped after Payrolla's published examples and passing the conformance kit; a revoked key marks the connection expired (`ErrCredentialsRejected`); `TestPayrollaPayoutFailedStartsOnce` takes a signed `payout.failed` through deploy, subscription, ingest and dedup to one run, refuses a forged one, and deletes the subscription on undeploy. The connector page, the SDK and the spec are updated. Differences from the plan:

- **Fixtures follow Payrolla's documentation, not recordings:** Payrolla's test company is in production and each person makes their own key; recordings follow once Taskiem's people have keys (partner tracker P4).
- **No automatic catch-up yet:** `list_events` replays 30 days for a workflow that asks, but ingest does not replay by itself after an outage; Payrolla retries a delivery for about two days.
- **A subscription is replaced, not updated:** Payrolla can neither change a subscription nor rotate its secret, so a redeploy with other events deletes and recreates it, and one found by URL but never recorded is deleted and made again.
- **The embed launch token waits for R6**, where the view that uses it is built.

### R6: App views (L; needs R0-3)

**Build:** the `views` manifest block and rule 17; the per-tenant `frame-src` (§6.3); the view panel on the connection page, "Open in …" on run pages and the approval inbox, the `view` step or action field; postMessage handling (declared origins and events only, able to complete a waiting step); the new-tab fallback; chat links; Payrolla's views.

**Done when:** a business opens a payroll in Payrolla inside Taskiem; no origin outside a connected product's views can be framed (test); a forged postMessage from another origin or for an undeclared event is ignored (test); a product that forbids framing opens in a new tab.

### R7: Templates around Payrolla (S–M; needs R5)

**Build:** the seven templates of §6.4 (`payrolla-approve-by-chat` only if Payrolla's API can approve), each with template tests on fake Payrolla events, Start page entries when a business has Payrolla connected, AI eval cases ("tell my staff on WhatsApp when Payrolla pays them").

**Done when:** a Payrolla customer gets WhatsApp payslip messages and an owner summary from templates alone, and no template has a money-moving step.

**Built** (R7): the seven templates of §6.4 on Payrolla's real events (`templates/library/payrolla-*.json`, each with workflow tests on fake Payrolla events in `templates/tests/`); a template `product` field, so retrieval offers them only for goals that name Payrolla and above the rest, and the Start page only to businesses with a Payrolla connection; `TestProductTemplatesMoveNoMoney` (capability steps and rail steps only read); the builder eval rule `no_payment_with_product` with five Payrolla cases (baselines regenerated). Differences from §6.4:

- **`payrolla-approve-by-chat` sends Payrolla's approval link** instead of approving: Payrolla has no approve API, by design, so the approver confirms in Payrolla with their own factor. It starts on `run.computed` and reminds once the day before payday.
- **Payrolla's event names:** `run.completed` and `run.partially_completed` instead of `payroll.paid` and `payroll.failed`; the people not paid come from `get_run_lines`.
- **The payslip link is a second message**, so a payslip link Payrolla refuses (`PAYSLIP_LINKS_DISABLED`) never stops the employee hearing they were paid.
- **The payday check reads a connected payments account** (`balance`): Payrolla does not expose its wallet's balance.
- **The lock reminder runs each morning** for approved payrolls due within a set number of days, rather than at a cut-off Payrolla does not publish.
- **Recording keeps every report:** a payroll reported again after a reversal adds new rows; the latest are the outcome.

### R8: Retire Payrolla-as-a-rail (S; after R3)

**Build:** every row of §7, in one pull request: removals, re-pointed evals and tests, doc rewrites, tracker updates.

**Done when:** a repository search finds "payrolla" only in the connector, its templates, its docs, the archive, decisions, and neutral partner fixtures; all suites pass; no coverage from the old tests is lost (each old case maps to a new one, listed in the pull request).

**Built** (R8): the old flow, its tests and the decide and e2e tests on it are removed; every old case maps to a new one (below). The repair evals run on Run payroll (`evals/repair/run-payroll.wd.json`), the builder's dogfood case is "run payroll on my rail", and both baselines are regenerated. The guide is archived (`docs/integrations/archive/payrolla.md`, internal on the docs site); the partner tracker and needs-people withdraw Payrolla's old items and add R0's; the embedding guide and tests use a neutral partner ("Wagely"). Kept by design: the iswallet answers (correspondence), the phase status pages and strategy notes (history), and Payrolla as an iswallet user.

| Old test | Now |
| --- | --- |
| Dogfood case: pays a wallet and a bank employee, reports | `run-payroll` test "pays everyone once…"; `TestRunPayrollEndToEnd` |
| Reversal after the report becomes a notice, pays no one | "a reversal after payment is recorded…"; `e2e.TestCapabilityPayoutsOnIswalletEndToEnd` |
| A reversal for an outflow the run does not watch sends nothing | `e2e.TestCapabilityPayoutsOnIswalletEndToEnd` (a stray reversal wakes nothing) |
| A notice Payrolla refuses ends that watch only | Withdrawn with the notice; a failed record or message never fails the payroll (`on_error` on each) |
| Totals by outcome; only confirmed bank payouts watched | "only confirmed bank payments are watched…"; summary totals in "pays everyone once…" |
| A refused payment does not stop the others | "a refused payment does not stop the others…"; the e2e test's refused payout |
| A lost response is retried under the same key | "a lost answer is retried…"; the e2e test (iswallet's replay); runtime `TestCapabilityRetryKeepsTheKey` |
| A bank payout never confirmed counts as failed | "a bank payment never confirmed is reported as not confirmed" |
| A rejected payroll pays no one, reported | "a rejected payroll pays nobody and is recorded" |
| An approval nobody answers expires, reported | "an approval nobody answers expires…" |
| decide `TestPayrollaDogfoodEndToEnd`, `TestPayrollaRejectedPayrollIsReported` | The template's workflow tests (the same orchestrator) and `TestRunPayrollEndToEnd` |
| e2e `TestPayrollaDisbursementEndToEnd` (HMAC webhook, dedup, approval by another, forged event 401, lost response, refusal, reversal, sealing, replay) | `e2e.TestCapabilityPayoutsOnIswalletEndToEnd`, every one of those checks |

### R9: The catalogue lists Taskiem's own connectors (S; independent)

**Problem.** A business looking for iswallet or Paystack under **Connector catalogue** finds nothing: that page lists only community-published connectors.

**Build:** the catalogue page lists built-in connectors first, grouped by `kind` (Rails, Products, Tools), with what each can do (capabilities for rails, events and views for products), and a **Connect** button; community connectors follow under their own heading.

**Done when:** iswallet, Paystack and (after R5) Payrolla are found from the catalogue's search, and connecting from there opens the same dialog as Connections.

## 9. Order and timing

| Order | Milestone | Size | Why here |
| --- | --- | --- | --- |
| 0 | R0-1 Tell Payrolla | — | So Payrolla builds nothing that is about to be withdrawn |
| 1 | R9 Catalogue | S | Small, independent, and fixes today's "I can't find iswallet" |
| 2 | R1 Capabilities in manifests | L | The base of everything on rails |
| 3 | R2 Capability steps and expansion | L | Templates need it; the riskiest engine change, so early |
| 4 | R3 Run payroll | M | The first and most demanding rail product; carries the dogfood flow's guarantees |
| 5 | R8 Retire Payrolla-as-a-rail | S | As soon as R3 holds the old guarantees |
| 6 | R5 Payrolla connector | M | When Payrolla's documents arrive (R0-2, R0-4); can move earlier |
| 7 | R4 More rail templates | M | Uses R1 to R3 as they are |
| 8 | R6 App views | L | Needs Payrolla's embed details (R0-3); valuable for every future product |
| 9 | R7 Templates around Payrolla | S–M | After R5; the approval relay after R6 if it uses a view |

About 11 to 15 weeks for one engineer; about 7 to 9 for two, with one on R1–R4 and R8 (rails) and one on R9, R5–R7 (products).

## 10. Testing

| Level | What | Where |
| --- | --- | --- |
| Capability conformance | Each rail's declared capabilities replayed on fixtures; outputs match `schemas/capabilities-v1.json`; recorded webhooks produce the normalised events | `engine/conntest`, `connectors/<rail>/testdata` |
| Expansion | Capability steps expand to the rail's actions with the right class, idempotency, reconcile and pii; composites derive distinct seeds; refusal messages; redeploy on another rail | `engine/ingest` tests |
| Exactly-once per rail | Run payroll on each R1 rail's fake: a lost response, a crash mid-loop, a resend, a fork; each person paid once (the key is the rail's) | `engine/decide` simulation, `e2e/` with `e2e/*_fake_test.go` fakes per rail |
| Template tests | Every template, every capable rail: wdcheck, policy findings, dry run, declared-equals-used (capabilities), owner-shaped details, round trip | `templates/templates_test.go` |
| Workflow tests | The ten old dogfood cases against `run-payroll` (happy path, reversal notice → owner alert, stray reversal, refused payment, lost response, unconfirmed payout, rejection, expiry, totals) | `templates/tests/run-payroll.test.json` |
| Product connector | Payrolla fixtures from its sandbox; signature verification and forged-webhook 401; routing | `connectors/payrolla` |
| App views | CSP lists only connected products' origins; postMessage origin and event checks; fallback | `api` tests, Playwright `web/e2e/views.spec.ts` |
| AI | "Pay my staff…" on each rail; "when Payrolla pays…" uses events, no payment steps (`no_payment_with_product` rule) | `evals/builder`, `tools/aieval` |

## 11. Risks and open questions

| Risk or question | Mitigation or owner |
| --- | --- |
| Expansion makes the editable definition and the running one differ | The run view and history always show the expanded steps, labelled with the capability; the canvas shows both; replay and drift use the expanded one |
| A rail's capability mapping is subtly wrong (a status meaning) and money is reported settled when it is not | Conformance suite on recorded webhooks; `settled` only from a terminal provider status; unknowns stay `pending` and fall to lookup |
| Paystack OTP, Moniepoint OTP authorisation: payouts stall waiting for a person | Mapped to a reason; the connection check and the approval warn; documented per rail |
| Payrolla's API cannot approve payrolls | `payrolla-approve-by-chat` is dropped; the approval stays in Payrolla's view |
| Payrolla's interface cannot be framed | Views open in a new tab; everything else works |
| A business connects both Payrolla and a rail and also sets up Run payroll | The Guide refuses Run payroll when Payrolla is connected, explaining why (two systems paying the same staff), unless the owner confirms they are separate payrolls |
| Partner-embedded deployments (Taskiem inside a partner's app) framing a product inside a frame | Views open in a new tab when Taskiem itself is framed |
| Removing the dogfood flow loses an acceptance test the build plan relied on | R8 lists each old case and its replacement; R8 waits for R3 |

Open questions for the founders: whether `run-payroll` is offered to Payrolla's own customers at all (proposed: no, R7's templates instead); whether views count against any plan limit; whether `kind: product` connectors are allowed in the community catalogue (proposed: yes, reviewed like the rest).

## 12. Out of scope

- The storefront's `Payments` interface moving onto `collect_link` (it stays Paystack-only until a later storefront milestone, which can reuse R1 and R2).
- Card collection inside Taskiem's pages (rails' hosted checkouts are used as links).
- Rails for other countries beyond what their connectors already do.
- Moving money between rails (treasury); the reconciliation of a rail's settlement reports against bank statements.
- Building any part of Payrolla's product inside Taskiem.
