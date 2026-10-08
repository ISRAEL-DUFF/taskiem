# 0024 — Production cloud: worker pools routed at claim time, a replica only for stale-tolerant views, retries only where safe

Date: 2026-10-07 · Status: Accepted (provider, region, Postgres service, backup retention and egress addresses pending P4-C1 to P4-C6)

## Context

Phase 4 P4-3 asks for a Nigeria-region cloud with Postgres HA (a synchronous standby and point-in-time recovery), fixed egress IPs, workers split by queue, dedicated pools and read replicas (spec 2.3, 15.4). Splitting by queue existed (`TASKIEM_WORKER_QUEUES`). Everything else was either infrastructure, which needs people, or behaviour the code did not have: every worker served every tenant; every read went to the primary; and a worker that lost its connection while recording an outcome abandoned it to lease expiry, which for a payment means a reconcile round trip with the provider.

Postgres is the queue (decision 0002), so these are database questions: who claims what, where reads may go, and which transactions may run twice.

## Decision

1. **A pool is a property of the worker; routing is a property of the tenant, read at claim time.** A worker serves one pool (`TASKIEM_WORKER_POOL`, default `shared`). `taskiem_claim_tasks` takes the pool and keeps only tenants routed to it, through `taskiem_tenant_worker_pool`: the tenant's own routing, its partner's, its plan's, else `shared` (migration 00120). Tasks carry no pool, so a routing change moves queued work at once and nothing is rewritten. Fair claiming within a pool (round-robin across tenants, each under its cap) is unchanged.
2. **Rolling upgrades stay safe.** The five-argument claim of the previous release now claims for the shared pool only, so an old worker never takes a dedicated tenant's task.
3. **No routing to an empty pool.** Workers report their pool and queues every 30 seconds (`worker_pool_workers`); assigning a tenant or plan to a pool with no live worker is refused unless forced. Changes go through `taskiem_dispatch`-owned definer functions; tenant changes are audited in the tenant's chain, and all are kept in `worker_pool_changes`.
4. **The replica serves only views that may be seconds stale**: the run list and the dashboard. `db.ReadTx` is the only way in: a `READ ONLY` transaction with the same `SET ROLE taskiem_app` and tenant scope as the primary, so row-level security holds there. A test lists its callers, so a new one is a deliberate change. Claims, leases, decisions, writes, run detail, history, audit, reports and billing stay on the primary.
5. **Replica lag is measured end to end** with a heartbeat row written on the primary and read on the replica (migration 00121). It needs no privileges on the standby and does not mistake an idle primary for a lagging replica. Beyond `TASKIEM_DATABASE_READ_MAX_LAG`, or when the replica fails a query, reads go to the primary.
6. **Retry only what certainly did not commit, or what is idempotent.** `db.InTenantTxRetry` retries a transaction after a lost connection, a server shutting down or a read-only server (resetting the pool so connections find the new primary), for up to `TASKIEM_DATABASE_RETRY_WINDOW`. A transaction that failed before its `COMMIT` rolled back and is always retried. One that lost its connection during `COMMIT` may have committed; it is retried only when the caller declares it idempotent. The worker's `finish` and `postpone` are idempotent because they are fenced by the lease: a repeat after a commit finds the fence moved. Recording an `EffectIntent` is not, so it is retried only before its `COMMIT`; otherwise the step falls to lease expiry and reconciliation, as before.
7. **HA belongs to Postgres tooling.** Connection strings name every host with `target_session_attrs=read-write`; failover is the HA manager's (Patroni or the managed service). Taskiem warns at start about several hosts without the attribute.
8. **Fixed egress addresses come from NAT, not from proxies.** The egress guard resolves, vets and pins addresses and ignores `HTTPS_PROXY` by design. A forward proxy given a name would resolve it again after the check. If an explicit proxy is ever required, the guard will `CONNECT` through it to the vetted IP, never a name. (Built since: [decision 0028](0028-explicit-egress-proxy.md), `TASKIEM_EGRESS_PROXY`.)

## Alternatives considered

- **A pool column on tasks, set when tasks are created.** Rejected: every enqueue path would need the routing; a change would leave queued tasks in the old pool, and moving them means rewriting rows a worker may be claiming.
- **One queue per pool (`connector@acme`).** Rejected: it multiplies queue names, mixes the kind of work with whose work it is, and old workers would never see renamed queues.
- **Pools chosen by a plan limit in `tenant_limits`.** Rejected: limits are numbers merged across defaults, plans and overrides; a pool is a routing, and an operator must be able to move one tenant without touching its plan.
- **Replica lag from `pg_last_xact_replay_timestamp()`.** Rejected: on an idle primary it grows without any lag, and the WAL receiver's status needs `pg_read_all_stats`, which the application role should not have.
- **Retrying every transaction, or wrapping `InTenantTx` itself.** Rejected: many callers collect results across attempts or call providers around their transactions, and a lost `COMMIT` would apply a non-idempotent write twice. Opting in per call keeps that judgement next to the code.
- **Reading run detail and history from the replica.** Rejected for now: a run's summary from the primary next to its history from a lagging replica could disagree on what happened.

## Consequences

- A claim does one routing lookup per tenant with ready tasks; with no routing at all it is a few primary-key misses per tenant.
- A pool whose workers all stop strands its tenants' work. The live-worker check guards assignment, and `taskiem_pool_oldest_ready_seconds` shows it afterwards; alerting on it is the operator's job.
- During a failover, outcomes already fetched survive for the retry window; intents whose `COMMIT` was lost fall back to reconciliation; claims in flight wait for lease expiry (60 s).
- Processes with a replica write one heartbeat row every two seconds on the primary.
- A pgx quirk found while testing: an error from `COMMIT` on a connection cut just after the server received it can be reported as safe to retry. `InTenantTxRetry` therefore treats every connection loss during `COMMIT` as an unknown outcome.
