# 0004 — RLS with tenant scope arrays and a dispatch role

Date: 2026-10-05 · Status: Accepted

## Decision

- Every tenant-owned table has `tenant_id` and an RLS policy `tenant_id = ANY(taskiem_tenant_scope())`, where the scope comes from `SET LOCAL app.tenant_scope`.
- The application role `taskiem_app` cannot bypass RLS. RLS is `FORCE`d so even table owners are subject to it.
- Cross-tenant work discovery (claiming tasks, timers, runs to orchestrate, expired leases) goes only through `SECURITY DEFINER` functions owned by `taskiem_dispatch`. They return routing columns only; the caller then narrows its scope to one tenant before touching data.
- Partners see sub-tenants only when the API puts sub-tenant ids into the scope (partner admin API, spec 5.3).

## Why

A single tenant id per transaction cannot express partner access to sub-tenants, and workers must find work across tenants. Without a narrow, reviewed path, the pressure is to give workers a BYPASSRLS role, which defeats isolation.

## Alternatives

A schema or database per tenant (operationally heavy at thousands of sub-tenants); application-only filtering (one missed `WHERE` leaks data).

## Provenance

PostgreSQL documentation on row security policies and `SECURITY DEFINER` functions.
