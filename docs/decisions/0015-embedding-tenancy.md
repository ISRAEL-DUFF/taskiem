# 0015 — Embedding: sub-tenants, the partner scope, and end-user tokens

Date: 2026-10-07 · Status: Accepted

Spec 13.1, 13.4, 5.3. Builds on [0004](0004-tenant-isolation-rls.md) (tenant scope arrays and a dispatch role). The partner guide is [embedding.md](../embedding.md).

## Decision

1. **A sub-tenant is a tenant.** `tenants.parent_id` names its partner; it has its own row-level security scope, audit chain and key-encryption key (all keyed by tenant id), its own plan limits, workspace and environments. A tenant's parent is fixed at creation (a trigger refuses changes). Only a partner (a `partners` row, written by operators through `taskiem tenants partner`) may have sub-tenants, and a sub-tenant cannot be a partner, so the hierarchy is one level deep.

2. **The partner scope is entered, never held.** Amending 0004's "a partner's scope includes its sub-tenants": no session or key ever has a sub-tenant in its scope, and `taskiem_auth_tenant_scope(user, true)` now refuses. A partner reaches a sub-tenant only through `taskiem_partner_enter(sub, …)`, a `SECURITY DEFINER` function owned by `taskiem_dispatch` that:
   - requires the transaction's scope to be exactly one active partner;
   - requires `sub.parent_id` to be that partner (else "not found");
   - appends the access to the partner's audit chain;
   - sets `app.tenant_scope` to `{sub}` for the rest of the transaction (a definer function without a `SET` clause for it leaves the change in place);
   - appends the access to the sub-tenant's chain, as `partner:<partner>/<actor>`.

   So the check, the dual audit trail and the narrowing happen in one call, inside the transaction that then reads or writes the sub-tenant's data under ordinary RLS, and the partner's own rows are out of scope while it does. The partner admin API (`api/partner.go`) is the only caller. Metadata the partner owns (its sub-tenants' names and status, run counts for its caps) comes from narrow definer functions returning routing columns and counts only (`taskiem_partner_subtenants`, `taskiem_partner_usage`), not audited.

3. **Limits inherit downward only.** A sub-tenant's effective limits are its partner's effective limits with its own overrides applied, then capped at the partner's: it cannot exceed them, and "no limit" is not allowed where the partner has one. The cap is applied when the partner sets limits (400) and again when limits are read (`runtime.Store.effective`), so lowering a partner lowers its sub-tenants within the limits cache's 30 seconds. `worker_concurrency` is also written into the sub-tenant's own `tenant_limits` row, because the claim function reads it there. Partner-wide caps (`max_subtenants`, `subtenant_runs_per_day`, `subtenant_runs_per_month`) are checked at creation and at run start across all the partner's sub-tenants.

4. **End-user tokens are opaque and stored hashed.** `tsk_eut_` and 256 random bits, kept as SHA-256 in `end_user_tokens` with the sub-tenant, app, end user, permissions, optional bound origin and expiry (15 minutes by default, at most an hour, enforced by a `CHECK`). They are resolved by `taskiem_auth_end_user_token`, which also requires the sub-tenant, the partner and the app to be active and the partner still a partner.

5. **End users are lightweight principals.** An `end_users` row per (sub-tenant, app, partner's id for them); no platform account or login. Their actor is `end_user:<app>/<id>`; their row id is what `created_by` and `published_by` record. Their permissions are the token's, intersected on every request with the app's current allow-list and a fixed ceiling: `workflow.read`, `workflow.edit`, `workflow.publish`, `run.read`, `run.start`, `run.cancel`. Never secrets, connections, members, roles, policies, Git, alerts, audit, personal data, or approvals: those need a person accountable to the partner or the platform (approval decisions are recorded against platform users), or are the partner's to provision (C3).

6. **A separate API surface for end users.** End-user tokens work only on `/v1/embed/{app}/…`, which mounts exactly the handlers an embedded builder and run view need (the same handlers as `/v1`, so behaviour cannot drift), behind guards that check definitions against the app's allowed connectors and gated step types at save, validate, publish and run start. The app id in the path is the token's audience. CORS exists only there, only for the app's exact origins, without credentials; a request without an `Origin` is accepted only for a headless app.

7. **Partner webhooks are queued in the database.** Triggers on `runs` and `workflow_versions` queue `run.completed`, `run.failed` and `workflow.published` for the partner's subscribed apps in the transaction that causes them; the scheduler queues `usage.threshold` once per period and delivers everything, signed exactly like alert webhooks (`alerts.Sign`), retried with backoff, logged in `partner_webhook_deliveries`. Signing secrets are in the partner's vault.

## Why

- **Opaque tokens over signed (JWT-like) ones.** Revocation per end user, suspension of a sub-tenant and disabling an app must stop tokens at once; a signed token would need a revocation list checked on every request anyway, which is the database lookup an opaque token already does, and would add a platform signing key to protect and rotate. Hashed opaque tokens match sessions and API keys, so one review covers all three. A forged or altered token is simply not found. The cost is a database read per request, which every authenticated request already makes.
- **Entering over holding.** A scope array holding the partner and all its sub-tenants (0004's original reading) would let any query in a partner request touch thousands of tenants, and leave dual auditing to each handler. One function that audits and narrows makes the audit impossible to skip and keeps every partner request as narrow as an ordinary one.
- **A separate route tree** keeps the large `/v1` surface (with routes that check people, not permissions) out of reach of end users by construction, and gives CORS a natural boundary.
- **Database triggers for events** cannot miss a run that ends on any path (worker, orchestrator, cancel) and cannot invent one that rolled back.

## Alternatives

- Sub-tenants as workspaces of the partner (no tenant of their own): no separate audit chain or key, and isolation from the partner's own users would rest on application filters.
- A `BYPASSRLS` partner role: defeats 0004.
- EdDSA-signed tokens verified without the database: rejected above; a short TTL alone does not give immediate revocation.
- Accepting end-user tokens on `/v1` with a route allow-list in the middleware: a new route would be reachable unless someone remembered to exclude it.

## Provenance

PostgreSQL documentation on `set_config`, `SECURITY DEFINER` and row security. The signed-webhook format follows Taskiem's own alert webhooks ([alerts](../alerts.md)), itself the common `t=…,v1=…` HMAC scheme.
