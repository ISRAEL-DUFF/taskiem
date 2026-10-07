# 0021 — Remote trigger registration: intent in the deploy, a reconciler for the provider

Date: 2026-10-07 · Status: Accepted

## Context

Decision [0012](0012-trigger-registration.md) registers triggers inside Taskiem: publishing replaces a workflow's rows in `triggers` in the same transaction, and a provider is pointed at `/hooks/{tenant}/connectors/{connector}/{trigger}` by hand, with the signing key in a connection field. That suits providers with one webhook per account (Paystack, Flutterwave).

PGDock is different (plan item P1-T1, [the PGDock integration plan](../pgdock-integration.md)). A PGDock webhook watches chosen tables and events and has its own signing secret, returned once when it is created. A person would have to create one per workflow, copy the secret into Taskiem, and remember to delete it later. The plan asks Taskiem to do this itself: create the webhook when a workflow is deployed, update it on a redeploy, delete it when the workflow goes, and show when someone breaks or pauses it at PGDock. Telegram's `setWebhook`, WhatsApp's app subscriptions and mobile-money callback registration have the same shape, so the mechanism must be generic.

Calling the provider from the publishing transaction would tie a database commit to a remote call that can fail, time out or succeed without an answer. A webhook created by a publish that then rolls back, or by a call whose answer was lost, is an orphan: it keeps delivering to Taskiem and nobody knows it exists.

## Decision

1. **The manifest opts in.** A connector/v1 webhook trigger may declare `registration: remote`. The connector then supplies a `Registrar` for it (create, update, get, find by name, rotate the secret, delete). The trigger may also declare `options` (a JSON Schema for the workflow trigger's `config.options`, such as PGDock's tables and columns) and a `test_event` expression. A remote trigger is verified with the secret the provider returned, never a connection field.
2. **The deploy records intent; it never calls out.** `ingest.SyncEnvironments` (publish, promote, Git sync, undeploy) declares the subscription a deployed version wants in `remote_subscriptions` (`desired = 'present'`), in the deploying transaction. A version without the trigger, a different connector or connection, or an undeploy marks it `desired = 'absent'`. A redeploy keeps the row and marks it to be applied again.
3. **A reconciler makes the provider agree.** `engine/remote.Reconciler` runs in the scheduler role and, for a quick answer, right after a publish or undeploy in the API. Each pass leases due rows, calls the registrar, and records what the provider said. It retries with backoff until the two agree: transient failures quickly, refusals people must fix (a token without the write scope) hourly to daily and at once when a connection is added. Each change at the provider is an audit entry in the tenant's chain.
4. **Crash safety by name.** A subscription is named `taskiem-<row id>` at the provider. A row without a recorded provider id is looked up by that name before anything is created: one made just before a crash is adopted (its secret replaced, since it was never kept) rather than duplicated, and a released one is found and deleted. The secret is stored before the row records the provider id. The row is deleted only once the provider no longer has the subscription. An intent changed while a pass ran is never overwritten by that pass.
5. **Secrets are platform secrets.** The signing secret goes into the tenant's vault under the platform environment `_remote` (not listed to the tenant, never returned by the API), is read once per delivery with the read recorded, and is deleted with the subscription.
6. **Ingest routes by subscription.** The ingest URL Taskiem registers names the subscription (`?env=&connection=&subscription=`). A delivery for it is verified with its secret and starts only the workflow that owns it; one for a released subscription is answered 410, so the provider stops rather than retries. The manifest's `test_event` marks a provider's test: verified, acknowledged and recorded on the subscription, starting only workflows that subscribe to that event by name. A connector may supply an enricher that completes a verified event before the run starts (PGDock: fetch a truncated event's row); a refusal it can retry refuses the delivery with 503, so the provider sends it again.
7. **Drift is shown and repaired by republishing.** The reconciler checks live subscriptions every 10 minutes and records the provider's view (`healthy`, `failing`, `paused`, `broken`) or `missing`. The workflow's triggers, the publish answer and each connection show a summary (`ok`, `pending`, `failed`, `failing`, `paused`, `broken`, `missing`, `removing`). A health check never repairs on its own (the customer may have paused the webhook on purpose); publishing again, even the same version, applies every live subscription afresh, which resumes, reinstalls or recreates it.
8. **Undeploy exists.** `DELETE /v1/workflows/{wf}/deployments/{env}` removes a workflow's deployment and triggers from an environment and releases its subscriptions. Runs already started carry on.

The ingest URL's host is `TASKIEM_HOOKS_URL` (default `TASKIEM_PUBLIC_URL` + `/hooks`); without one nothing is created and the subscription shows why.

## Alternatives considered

- **Calling the provider inside the publishing transaction.** Rejected: a rollback after a successful call leaves an orphan, a timeout leaves the outcome unknown, and a slow provider holds row locks on `workflows`.
- **Calling after commit, with no record of intent.** Rejected: a crash between the commit and the call loses the work, and nothing knows to clean up.
- **Asking the customer to create the webhook and paste the secret** (as for Paystack). Rejected for PGDock: one webhook per workflow, a secret shown once, and nobody to delete it later. Manual registration stays the default for providers with one account-wide webhook.
- **Tagging webhooks with a static header instead of a name.** PGDock stores static headers encrypted and never shows them again, so a header could not be searched for. The name is the only field it returns that Taskiem controls.
- **Repairing drift automatically on each health check.** Rejected: a paused webhook may be the customer's decision; republishing is the explicit "make it as Taskiem says" action.

## Consequences

- Connectors can adopt remote registration one trigger at a time; triggers without it work as in 0012.
- Every environment a workflow is deployed to gets its own subscription. An environment with no connection for the connector shows `failed` (no connection) until one is added.
- A subscription's secret is replaced when Taskiem adopts a webhook it created before a crash; deliveries signed with the old secret in that window fail and the provider retries them.
- Release and repair are in the database, so they survive restarts; the reconciler can lag the publish by one pass when the provider is slow.
- `remote_subscriptions` (migration 00110) holds no secret and no event data. The dispatch role reads only routing columns.

## Where the idea came from

Declarative desired state reconciled against an external system, with idempotent retries and names as ownership tags, is a common infrastructure pattern (public literature on control loops and reconcilers). Nothing was taken from another workflow product.
