# Running the production cloud

How Taskiem runs as a highly available, multi-tenant cloud (Phase 4 P4-3, spec 2.3 and 15.4): Postgres with a synchronous standby and point-in-time recovery, dedicated worker pools, a read replica, and fixed egress addresses. The design is in [decision 0024](decisions/0024-production-cloud.md). Choosing the provider, region, Postgres service, backup retention and egress addresses needs people ([needs people](needs-people.md#phase-4), P4-C1 to P4-C6).

Everything here also works on a single Postgres. None of it is needed for a small install.

## High availability Postgres

Postgres is the queue and the record (decision 0002). A primary with a **synchronous standby** in the same region means a committed step outcome is on two machines before the worker moves on, so a failover loses nothing.

### Primary and standby settings

On the primary:

```ini
wal_level = replica
synchronous_commit = on
# Two standbys, either may acknowledge: one can be down without stopping writes.
synchronous_standby_names = 'ANY 1 (standby_a, standby_b)'
max_wal_senders = 10
wal_keep_size = 2GB
archive_mode = on
archive_timeout = 60s        # bounds the loss if every server is lost at once
```

On each standby: `hot_standby = on`, `primary_conninfo` with `application_name=standby_a` (or `_b`), and a replication slot.

With a single synchronous standby, writes stop when it is down. Either run two (as above), or let the HA manager fall back to asynchronous while the only standby is away (Patroni's `synchronous_mode: true` without `synchronous_mode_strict`), accepting that a failover during that window can lose the last commits. A managed service's "HA" option usually sets this up for you; check that it is synchronous and in-region.

Failover itself belongs to the HA manager (Patroni, or the managed service), never to Taskiem.

### Connection strings

Give Taskiem every host and ask for the read-write one:

```
TASKIEM_DATABASE_URL=postgres://taskiem:...@pg-a:5432,pg-b:5432,pg-c:5432/taskiem?target_session_attrs=read-write&connect_timeout=5&sslmode=verify-full
```

A new connection tries the hosts in turn and keeps only the primary. A single address that the HA manager moves (a virtual IP or a DNS name) works too. Taskiem warns at start when the URL names several hosts without `target_session_attrs=read-write`, since a connection could then land on a standby.

Do not put a transaction-pooling proxy (PgBouncer in transaction mode) in front of the workers or the scheduler: the `LISTEN` connection needs a session of its own (spec 2.3).

### What happens during a failover

| Part | Behaviour |
| --- | --- |
| Connections | Broken connections are dropped from the pool. A connection that reaches a read-only server (a standby, or the old primary coming back as one) fails with `read_only_sql_transaction`. On either, the whole pool is reset, so the next connections find the new primary |
| Claims | The claim loop logs the error and tries again within a second. A claim lost with its connection holds its tasks until the lease (60 s) runs out; the scheduler then hands them back |
| `LISTEN` | The listening connection reconnects with backoff (0.1 to 5 s) and wakes the claimer once, since notifications sent meanwhile are lost. Workers also poll every second |
| Step outcomes | A worker that has a provider's answer keeps it and retries recording it for up to `TASKIEM_DATABASE_RETRY_WINDOW` (30 s) while its lease holds. Recording is fenced by the lease, so running it twice is harmless |
| Effect intents | Recording an `EffectIntent` (before a write is sent) is retried only when it certainly did not commit. If the connection was lost during its `COMMIT`, the step is left: the lease expires, and the next attempt reconciles with the provider before sending anything |
| API requests | Requests in flight fail with a 5xx. Clients retry with their `Idempotency-Key`; webhooks are retried by the provider and deduplicated |
| Scheduler | Every claim uses `SKIP LOCKED` and leases, so it resumes where it was |

`taskiem_db_retries_total{outcome}` counts retried transactions: `retried` for each extra attempt, `recovered` when one then succeeded, `gave_up` when the window ran out. Alert on `gave_up`.

### Point-in-time recovery

Point-in-time recovery (PITR) is for disasters and mistakes, not failover: a dropped table, a bad migration, every server lost. It restores a base backup and replays archived WAL up to a chosen moment.

1. **Archive WAL continuously** to object storage in another zone (or region), with a tool such as pgBackRest or WAL-G, or the managed service's backups. `archive_timeout = 60s` bounds how much an idle period can leave unarchived.
2. **Take a base backup daily**, and keep backups and WAL for the retention period (P4-C4; it also answers P4-K3, how long data from before a tenant brought its own key stays readable).
3. **Encrypt the archive** with a key held apart from the database's credentials. Secrets and personal data inside are already sealed by tenant keys; the archive still holds everything else.

**Restoring.** Restore into a new instance, never over the running one:

1. Stop the workers, scheduler and orchestrators (scale them to 0) if you are restoring production; leave the API on a maintenance page.
2. Restore the latest base backup before the target time, with `restore_command` fetching archived WAL and `recovery_target_time = '2026-11-02 09:41:00+01'` (and `recovery_target_action = 'promote'`).
3. Start it and wait for recovery to finish (`SELECT pg_is_in_recovery()` returns false).
4. Check it (below), then point `TASKIEM_DATABASE_URL` at it and start the roles.

**After a restore to an earlier moment**, everything after that moment is gone, including records of provider calls made after it. Runs that were running then resume from what the database holds: a write whose `EffectIntent` survived is reconciled with the provider before anything is sent; one whose intent was lost is sent again with the same idempotency key (derived from the run and step), which providers that honour keys deduplicate. Check `needs_reconciliation` runs and any payment steps active in the lost window with their providers.

### Restore drills

A backup nobody has restored is a hope. Once a month, and after any change to backups:

1. Restore yesterday's backup to a scratch instance at a recent time, following the steps above but leaving production alone.
2. Check it:
   - `SELECT max(recorded_at) FROM run_events` is close to the target time;
   - `taskiem migrate` against it applies nothing;
   - an audit export of a tenant (`GET /v1/audit/export`, against a test API pointed at the scratch instance) passes `taskiem audit verify`, and its chain heads match the signed anchors in `TASKIEM_ANCHOR_DIR` up to the target time (anchors live outside the database for exactly this);
   - a secret of a test tenant decrypts (the platform KMS key must still exist: never destroy a KMS key version a backup may need).
3. Record how long the restore took (the recovery time) and how far behind the target the data was (the recovery point), and delete the scratch instance.

## Dedicated worker pools

By default every worker serves every tenant (the **shared** pool), claiming round-robin across tenants with each tenant capped at its `worker_concurrency`, so one tenant's backlog cannot hold every slot. Some tenants need more: an enterprise plan promising dedicated workers (spec 16.1), or a tenant whose load would crowd others even within its cap.

A worker process serves one pool, named by `TASKIEM_WORKER_POOL` (default `shared`), on the queues in `TASKIEM_WORKER_QUEUES`. It claims only the tasks of tenants routed to its pool. A tenant's pool is, in order: its own routing, its partner's (for a sub-tenant), its plan's, else `shared`. Fair claiming across the tenants of a pool is unchanged.

1. **Start the pool.** With Helm, add an entry to `workerPools` ([Kubernetes](kubernetes.md#dedicated-worker-pools)). Elsewhere, run workers with `TASKIEM_WORKER_POOL=acme`.
2. **Route tenants to it:**

   ```sh
   taskiem pools                                      # live workers, queue depth by pool, routings
   taskiem pools assign acme --tenant 0190f0c2-...    # one tenant (and its sub-tenants)
   taskiem pools assign enterprise --plan enterprise  # every tenant on a plan
   taskiem pools unassign --tenant 0190f0c2-...       # back to its partner's, plan's or the shared pool
   ```

   Routing to a pool no live worker has reported from in the last five minutes is refused (`--force` overrides, for a pool about to start). Tenant changes are audited in the tenant's log as `worker_pool.assign` and `worker_pool.unassign` (actor `platform_admin`); every change, tenants' and plans', is kept in `worker_pool_changes`.
3. **Watch it.** `taskiem_pool_ready`, `taskiem_pool_leased` and `taskiem_pool_oldest_ready_seconds` (labels `pool`, `queue`) show each pool's backlog. Alert when a pool has ready tasks and its oldest is older than a minute: either it has too few workers or none.

Routing is read when tasks are claimed, so a change moves queued tasks at once; steps already running finish where they are. Workers of the previous release (during a rolling upgrade) claim for the shared pool only. The `container` queue is served by the container worker pool alone ([container steps](container-steps.md)); dedicated container capacity is not built.

`TASKIEM_WORKER_QUEUES` still splits work by kind: a pool can run, say, `connector` on one Deployment and `sandbox` on another, each with `TASKIEM_WORKER_POOL` set to the same name.

## Read replica

An optional streaming replica takes the run list (`GET /v1/runs`) and the dashboard (`GET /v1/dashboard`) off the primary:

```
TASKIEM_DATABASE_READ_URL=postgres://taskiem:...@pg-replica:5432/taskiem?sslmode=verify-full
TASKIEM_DATABASE_READ_MAX_LAG=10s
```

Set them on the `api` role (Helm: the URL in the Secret, the lag in `config`). Nothing else reads from the replica: claims, leases, decisions, writes, run detail, history, audit, reports and billing all stay on the primary, and a test fails if a new caller appears. Replica transactions are `READ ONLY`, the connection switches to `taskiem_app` as on the primary, and the tenant scope is set the same way, so row-level security applies on the replica exactly as on the primary.

Every two seconds the API writes the time to a heartbeat row on the primary and reads it back from the replica; the difference is the lag (`taskiem_db_replica_lag_seconds`, -1 when the replica cannot be reached). While the lag is beyond `TASKIEM_DATABASE_READ_MAX_LAG`, or the replica fails a query, reads go to the primary (`taskiem_db_replica_in_use` is 0) until a check finds it caught up. `taskiem_db_reads_total{target}` counts where reads went. A replica that is down at start does not stop the API.

Use an asynchronous replica for this, not the synchronous standby: a slow query there would slow every commit. Set `hot_standby_feedback = on` and `max_standby_streaming_delay = 30s` on it so long dashboard queries are not cancelled by replay; a cancelled query is retried on the primary.

## Fixed egress addresses

Customers allow Taskiem's addresses on their firewalls and KMS (P4-K4), so outbound traffic must leave from a few fixed IPs.

**Use network address translation.** Route the worker, API, edge and scheduler nodes' outbound traffic through a NAT gateway with reserved static addresses (Cloud NAT, a NAT gateway with Elastic IPs, or the equivalent), or put those roles on a node pool whose only way out is that gateway. Nothing in Taskiem changes: the egress guard resolves, vets and pins the address, then dials it directly, and the NAT gives the packets the fixed source address. Container steps leave through their worker's egress proxy, so through the same gateway. Publish the addresses once they are reserved (P4-C5).

**Not through `HTTPS_PROXY`.** Tenant traffic (connector calls, HTTP steps, sandbox fetches, alert webhooks, tenants' databases, BYOK key services) goes through the egress guard, which ignores `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` on purpose. A forward proxy given a host name resolves it again, after the guard checked it, which reopens DNS rebinding to private and metadata addresses (spec 14.2); the guard's per-tenant allow-list would also no longer be the last word. Setting those variables does not move tenant traffic.

Some platform calls do use Go's default transport and therefore honour those variables: the billing providers (Paystack, Flutterwave), the platform KMS (OpenBao), Git providers and a self-hosted model endpoint. If you set `HTTPS_PROXY` for them, list in-cluster services in `NO_PROXY` (OpenBao above all), and remember that tenant traffic still leaves directly.

If an explicit egress proxy is ever required (a customer's dedicated deployment that only allows its own proxy out), the design in [decision 0024](decisions/0024-production-cloud.md) keeps the guard in charge: the guard resolves and vets as now, then asks the proxy to `CONNECT` to the vetted IP and port, never to a name. It is not built.
