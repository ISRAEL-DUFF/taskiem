# Decision log

One file per decision, numbered in order: `NNNN-short-title.md`. Each entry records what was decided, why, the alternatives considered, and, where the design resembles an existing product, where the idea came from. Entries are never rewritten: amendments are appended with a date, and a later decision supersedes an earlier one by number.

Names: the index uses today's names. Xenibase was called PGDock until October 2026, and entries written before then (0018, 0021, 0025) say PGDock and `pgdock@1`; see the amendment to 0018.

| # | Decision | Status |
| --- | --- | --- |
| [0001](0001-event-sourced-execution.md) | Event-sourced runs with a pure `decide()` | Accepted |
| [0002](0002-postgres-as-queue.md) | PostgreSQL as the task queue in v1 | Accepted; amended after the G0 spike |
| [0003](0003-cel-expressions.md) | CEL for workflow expressions | Accepted |
| [0004](0004-tenant-isolation-rls.md) | RLS with tenant scope arrays and a dispatch role | Accepted |
| [0005](0005-idempotency-keys.md) | Derived and per-connector encoded idempotency keys | Accepted |
| [0006](0006-working-name.md) | Working name Taskiem | Accepted, pending trademark search |
| [0007](0007-licence-policy.md) | Two-tier licence policy | Accepted |
| [0008](0008-wd-v1-control-flow.md) | Nested sub-flows for branch, parallel and foreach | Accepted |
| [0009](0009-write-call-deadlines.md) | Call deadlines for writes | Accepted |
| [0010](0010-secret-only-expressions.md) | Expressions that read secrets read nothing else | Accepted |
| [0011](0011-sandbox-quickjs-fork.md) | Code steps in QuickJS on wazero, via a vendored fork | Accepted |
| [0012](0012-trigger-registration.md) | Trigger registration, environments, and webhook secrets | Accepted |
| [0013](0013-private-network-access.md) | Private databases through an SSH tunnel first, a relay later, private ranges only single-tenant | Proposed |
| [0014](0014-ai-model-layer.md) | A provider-agnostic AI model layer; the builder's safety boundary is its import graph | Accepted |
| [0015](0015-embedding-tenancy.md) | Embedding: sub-tenants entered through one audited function, opaque end-user tokens, a separate embed API | Accepted |
| [0016](0016-repair-and-resume.md) | Self-repair: a background queue, rules before the model, a shadow sandbox, and resuming a failed run as a fork | Accepted |
| [0017](0017-plans-and-billing.md) | Plans and billing: a config-file catalogue under the limits, subscriptions per top-level tenant, degraded never stranded | Accepted (prices pending B1) |
| [0018](0018-pgdock-integration-principles.md) | Xenibase integration: public contracts only, each product stands alone | Accepted |
| [0019](0019-bring-your-own-key.md) | Bring your own key: the customer's key wraps outside the platform's, keys re-wrap in the background, work parks when a key is lost | Accepted |
| [0020](0020-connector-catalogue.md) | The connector catalogue: signed packages, automated checks, four-eyes review, pinned installs with consent | Accepted (publisher agreement and reviewers pending P4-E1 to P4-E3) |
| [0021](0021-remote-trigger-registration.md) | Remote trigger registration: intent recorded in the deploy, a reconciler for the provider (amends 0012) | Accepted |
| [0022](0022-self-serve-onboarding.md) | Self-serve onboarding: confirm email without blocking the first run, and measure G4 where runs end | Accepted |
| [0023](0023-slos-and-alerting.md) | SLOs as code: burn-rate alerts from one list, a black-box canary, a self-hosted status page, readiness before drain | Accepted (on-call, paging, status domain, G4 owner pending P4-R1 to P4-R4) |
| [0024](0024-production-cloud.md) | Production cloud: worker pools routed at claim time, a replica only for stale-tolerant views, retries only where safe (amends 0002) | Accepted (infrastructure pending P4-C1 to P4-C6) |
| [0025](0025-public-docs-and-api-reference.md) | Public docs and API reference: a hand-written OpenAPI document checked against the router, a static site from docs/ | Accepted (domain, hosting and review owner pending P4-D1 to P4-D3) |
| [0026](0026-bound-step-up-and-cookie-sessions.md) | Step-up bound to its operation, cookie sessions without a body token, key changes behind step-up | Accepted |
| [0027](0027-operator-console.md) | An operator identity apart from tenants, a console on it, and a platform audit chain | Accepted (operator accounts and SSO issuer pending P4-X1, P4-X2) |
| [0028](0028-explicit-egress-proxy.md) | Explicit egress proxy: the guard vets the destination, then CONNECTs to the vetted IP, never a name (amends 0024) | Accepted |
| [0029](0029-languages-and-voice-notes.md) | Languages and voice notes: catalogues with English as the source, drafts off until reviewed, the model routes but never confirms, transcripts sealed | Accepted (native-speaker review and the speech-to-text account pending L1 to L5) |
| [0030](0030-partner-onboarding.md) | Partner onboarding: people join a sub-tenant by invitation (a passkey link only for unclaimed accounts, the partner makes the first owner only), workflow packages installed as the sub-tenant's own copies, secrets set by its own people | Accepted |
| [0031](0031-shared-provider-clients.md) | Shared provider clients: a wallet allow-list per connection, and a partner's provider events routed to its sub-tenants through one audited function | Accepted (iswallet wallet-scoped keys and per-wallet subscriptions asked for, P4-W1) |
| [0032](0032-deployment-target.md) | Deployment target: one VM (with the platform's guarantees kept) until a named trigger, then Kubernetes with the Helm chart | Proposed (provider and region pending P4-C1); amended: the VM runs itself |
| [0033](0033-data-tables-on-xenibase.md) | Data tables: a Xenibase project per organisation, provisioned by Taskiem and reached as a managed `xenibase@1` connection; rows through the data API with idempotency keys | Proposed |
| [0034](0034-oauth-connections.md) | OAuth connections: state and PKCE kept server-side, ten-minute requests, one project per connection, revocation both ways, client registration in the environment for now | Proposed |
| [0035](0035-telegram-channel.md) | Telegram as a second chat channel: one shared conversation behind a channel interface, tg:<id> addresses, one-time deep-link linking, the same decision tokens | Accepted |
| [0036](0036-storefronts.md) | Storefronts: a one-page public storefront per tenant (business card, catalogue from a data table, actions as hosted forms starting workflows through a `storefront` trigger), on its own domain, with trust and safety and a free plan | Proposed |
| [0037](0037-forms.md) | Forms: a `form` trigger and a `form` step whose fields are the workflow's inputs schema, served on a cookieless form host; members sign in, outsiders use one-time links or codes; fields that are never collected | Proposed |
| [0038](0038-inbound-email.md) | Inbound email: an address per workflow on Taskiem's own mail domain, received by a Taskiem `mail` role over SMTP; raw MIME parsed to `trigger.body`, attachments scanned and stored as references, forwarding confirmations surfaced | Accepted |
| [0039](0039-reaching-people-outside.md) | Reaching people outside a business on WhatsApp and Telegram: a one-tap link per person and business on the platform's bot and number, contacts keyed by a proven phone, unproven links limited to their own submission, `notify@1` `message` by phone | Accepted |
| [0040](0040-pos-terminals.md) | POS terminals: a separate `moniepoint_pos@1` connector for Moniepoint's terminal events (verified by the connector, no polling), the record in the business's data tables in naira, four templates, and templates that make their tables | Accepted |
