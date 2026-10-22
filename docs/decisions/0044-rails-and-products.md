# 0044 — Rails are building blocks; products are connected, never rebuilt

Date: 2026-10-22 · Status: **Accepted** 2026-10-22 (build plan: [rails and products](../spec/rails-and-products.md))

## Context

Taskiem connects to two different kinds of service, and until now it has treated them the same way.

- **Rails** move money and nothing else: Paystack, Flutterwave, iswallet, Moniepoint, Anchor, Lenco, Remita, Interswitch, OPay, MTN MoMo, M-Pesa. They offer primitives: pay a bank account, pay a wallet, collect a payment, open a virtual account, read a balance, tell us when money moved. What a business does with those primitives (a payroll, a bulk payout, a payment link per order) is product logic, and the rail does not have it.
- **Products** are finished applications with their own logic, data and screens: Payrolla (payroll), and in the same family an accounting package, a POS back office, an HR tool. They usually move money themselves, over a rail, and they expose events (webhooks), an API and often an embeddable interface (as Paystack and Flutterwave do with checkout).

The "Payrolla integration" built in Phases 3 and 4 (`flows/dogfood/payrolla-salary-disbursement`, [docs/integrations/payrolla.md](../integrations/payrolla.md)) treats Payrolla as if it needed Taskiem to move its money. Payrolla sends each locked payroll to a Taskiem webhook; a Taskiem approver releases it; Taskiem pays every employee over iswallet, waits for bank confirmations, reports back, and watches 35 days for reversals.

Payrolla already does all of that itself, end to end, over iswallet, and has webhooks for its events and an embeddable interface. The workflow is therefore a payroll product built on the iswallet rail, wired to one partner's payload. Run as designed, it would put two systems in a position to pay the same employees (Taskiem's exactly-once guarantees hold only inside Taskiem), ask companies to approve the same payroll twice in two products, and keep two copies of the hardest code in the domain correct.

The workflow is good work: it is exactly the kind of product Taskiem should build on a rail. It is wired to the wrong counterpart.

## Decision

1. **Rails are building blocks.** Taskiem builds products on rails as workflows, shipped as templates: payroll, bulk transfers, a payment link or a virtual account per order or customer, refunds, supplier payments, reconciliation. Taskiem owns the product logic (approvals, exactly-once, waiting for confirmation, reversals, records, messages); the rail carries the money.
2. **Templates are written against what a rail can do, not against one rail.** A template that pays people asks for "a connection that can pay a bank account", and the business picks its own Paystack, iswallet, Moniepoint or other rail. Each rail declares which capabilities it has and how its actions map to them; a capability step is expanded into the chosen rail's own action when the workflow is deployed, so every rail keeps its own tested idempotency, reconciliation and events. (Design: [rails and products §4](../spec/rails-and-products.md#4-capabilities-one-template-any-rail).)
3. **Products are connected, never rebuilt.** A product becomes an ordinary connector: its webhooks are triggers, its API calls are actions, and its embeddable interface, where it has one, opens inside Taskiem as an **app view**. Taskiem adds what the product does not do (WhatsApp and Telegram messages to staff and customers, approvals by chat, alerts, records in the business's tables, summaries) and never moves money the product moves.
4. **The test, for any new integration:** *who owns the money movement and the product logic?* If the service only moves money, it is a rail and Taskiem may build on it. If it already runs the process end to end, it is a product: connect it, embed it, and build around it.
5. **Payrolla becomes a product connector** (`payrolla@1`), built from Payrolla's own API and webhook documentation, with its interface as an app view. The current payroll workflow becomes Taskiem's own **Run payroll** template, rail-agnostic, for businesses that do not use a payroll product. The Payrolla-specific pieces (the payroll webhook path and payload, the disbursement report and reversal notice sent to Payrolla, the guide asking Payrolla to build them) are retired. The generic partner machinery built alongside them (sub-tenants, partner packages, shared provider clients and event routing, decisions [0030](0030-partner-onboarding.md) and [0031](0031-shared-provider-clients.md)) stays: it serves any partner.

## Alternatives considered

| Option | Why not |
| --- | --- |
| Keep the current design and ask Payrolla to stop paying itself | Payrolla's payouts, approvals and screens are its product; asking a partner to hollow out its product to fit ours inverts the relationship and will not be accepted |
| Keep both: Payrolla pays, and Taskiem's flow stays as an option for Payrolla customers | Two systems able to pay the same payroll is the one failure payroll cannot have. Exactly-once cannot span two products |
| One template per rail (Run payroll on Paystack, on iswallet, …) | Eleven rails times every money template; each copy to keep correct; the AI builder and the template picker drown in near-duplicates |
| A runtime dispatcher connector (`money@1`) that calls the chosen rail at run time | Hides the real provider call inside another connector: effect classes, idempotency headers, reconciliation lookups and events are each rail's own and already tested, and a dispatcher would have to re-implement or nest them. Deploy-time expansion keeps the real step in the run history and the canvas |
| Rebuild Payrolla's features inside Taskiem for its customers | That is building a payroll product to compete with the partner that brings us the customers |

## Consequences

- Rail manifests gain a `capabilities` block; templates and the AI builder gain capability steps and capability-aware connection pickers.
- The dogfood flow's tests (exactly-once per employee, waiting for bank confirmation, reversal watching, rejection paths) move to the Run payroll template and its fake rails, so none of that coverage is lost.
- Payrolla's open engineering items in the partner tracker (build the payroll webhook sender, the report and reversal-notice receivers) are withdrawn and replaced by asking for its API documentation.
- The web app's **Connector catalogue** lists Taskiem's own connectors grouped as rails, products and tools, so a business can find iswallet or Payrolla where it looks for them.

## Provenance

From a review on 2026-10-22: the founders confirmed Payrolla pays employees itself through iswallet end to end, has webhooks for its events, and an interface other apps can embed.
