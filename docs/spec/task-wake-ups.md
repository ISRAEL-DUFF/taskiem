# Task wake-ups outside the transaction

Status: **spec; not built.** Written 2026-11-12. Decision: [0067](../decisions/0067-wake-ups-leave-the-transaction.md), which amends [0002](../decisions/0002-postgres-as-queue.md). It closes the first item of "What remains" in [performance](../performance.md#what-remains).

## 1. Summary

A transaction that schedules a task sends `NOTIFY` so that an idle worker claims it at once. Postgres lets only one transaction that has notified commit at a time, so these commits cannot share a disk flush, and above a certain rate they queue. That is the collapse above about 40 runs a second in [performance](../performance.md).

This plan keeps Postgres as the queue and keeps `LISTEN/NOTIFY` as the wake-up. It changes only **who sends it**: no longer the transaction that did the work, but one small notifier in each process, after the commit, at most once every 10 ms for a queue. Workers in the same process are woken in memory.

Nothing about correctness changes. A wake-up is already only a hint: a worker that misses one finds the task at its next poll, within a second.

## 2. The problem, measured

### 2.1 Why the commits queue

A transaction that called `pg_notify` takes a lock shared by the whole database before it commits and holds it until its WAL flush is done. Two such transactions can never be in the same flush. Transactions that did not notify group freely: many commits, one flush.

So the ceiling for notifying commits is **one per disk flush**, whatever the number of connections or CPUs.

### 2.2 The measurement

PostgreSQL 16, `synchronous_commit = on`, 4 CPUs, a scratch cluster on the review machine on 2026-11-12. `pgbench`, 8 seconds a run, two rounds each, interleaved. One listener was connected throughout.

| Transaction | 8 clients | 64 clients |
| --- | --- | --- |
| Insert a row | 11,700 and 14,600 commits/s | 20,000 and 19,400 |
| Insert a row and `pg_notify` | 3,100 and 3,200 | 2,900 and 3,000 |
| Insert a row, while one other connection sends `pg_notify` 100 times a second | not run | 18,800 and 19,400 |

What it shows:

- With `NOTIFY`, 8 clients and 64 clients commit at the same rate. More connections add nothing; latency grows instead (2.5 ms to 22 ms).
- With the wake-ups sent from one other connection, the working transactions keep about 97% of what they do with no wake-ups at all.

The scripts:

```sql
-- insert only
begin;
insert into t(q) values ('connector');
select 1;
commit;

-- insert and notify
begin;
insert into t(q) values ('connector');
select pg_notify('taskiem_tasks','connector');
commit;

-- the notifier, run with: pgbench -c 1 -R 100
select pg_notify('taskiem_tasks','connector');
```

### 2.3 What the measurement does not show

- It is a bare insert, not the engine's transaction, which also locks the run row and writes several rows. The engine's own figure comes from §9.
- It was one machine with a fast disk (a flush of about 0.3 ms). The machine in [performance](../performance.md) was slow and saturated. A flush of about 10 ms there would cap notifying commits near 100 a second, which is about 40 runs a second if a run makes two or three of them. **That last step is an inference, not a measurement.**
- [Performance](../performance.md) records that at 40 runs a second and above the largest waits were on rows (`Lock:transactionid`: the run row and `connector_rate_buckets`) and on WAL writes, more than on the notify lock. Taking `NOTIFY` out lets commits share a flush; it does not remove those waits. See §10.

## 3. What exists

| Piece | Where | What it does |
| --- | --- | --- |
| The one task insert | `applyEffects` in `engine/runtime/store.go` | Inserts a task for a `StepScheduled` event. It always runs inside `decideInline`, in the caller's transaction |
| The trigger | `taskiem_notify_task`, migration 00258 | After each insert statement on `tasks`, sends `pg_notify('taskiem_tasks', <queue>)` once for each queue with a task due within a second |
| The listener | `engine/runtime/listen.go` | One `LISTEN` connection in a worker process; wakes the claimer when the payload is its queue; reconnects and wakes once after a drop |
| The claim loop | `Run` in `engine/runtime/worker.go` | Claims for its free slots. If the claim came back short, waits for a wake-up or one second. If it came back full, claims again as soon as a slot is free |
| The transaction helper | `db.InTenantTx` in `engine/db/db.go` | Every caller of `decideInline` reaches the database through it (the worker, the scheduler, approvals, signals, ingest, the operator's actions) |

Two facts make the change small: tasks are inserted in exactly one place in Go, and every transaction that reaches it was opened by one helper.

## 4. Decisions

| # | Question | Decision |
| --- | --- | --- |
| 1 | Does the queue move out of Postgres | No. The task and the event that schedules it commit together; that is the guarantee ([0002](../decisions/0002-postgres-as-queue.md), amendment of 28 October). Only the wake-up moves |
| 2 | Who sends the wake-up | One notifier in each process, on a connection of its own, after the commit |
| 3 | How often | At once if none was sent for that queue in the last 10 ms; otherwise one at the end of those 10 ms, for everything that arrived meanwhile |
| 4 | What about a task inserted some other way | The trigger stays. It is silent only in a transaction that has said the engine will send the wake-up itself. Any other insert notifies as today |
| 5 | Same process | A worker in the process that scheduled the task is woken in memory, with no trip to the database |
| 6 | A way back | `TASKIEM_WAKE_IN_TX=true` makes a process leave the wake-up to the trigger, as today |
| 7 | The channel and payload | Unchanged: `taskiem_tasks`, the queue's name. Old and new processes wake each other |

## 5. The design

### 5.1 After the commit

`db.InTenantTx` hands its function a transaction that can also hold things to do once the commit has succeeded.

- `db.AfterCommit(tx, fn)` adds `fn` and answers whether the transaction can hold it. It can when the transaction came from `InTenantTx`. It cannot for a savepoint opened inside one, or a transaction opened any other way.
- The functions run in order after a successful commit, and never after a rollback. A function that panics is recovered and logged: the commit has happened and the caller is told so.
- Nothing else about `InTenantTx` changes. Its 686 callers are not touched.

### 5.2 The engine says it will wake

Where `applyEffects` inserts a task that is due within a second:

1. It asks `db.AfterCommit(tx, …)` to tell the notifier this task's queue.
2. If that was accepted, and only then, it sets `app.task_wake` to `engine` for this transaction (`set_config(…, true)`), before the insert and in the same round trip.
3. It inserts the task.

If step 1 was refused (a savepoint, an unusual caller) or `TASKIEM_WAKE_IN_TX` is set, nothing is set and the trigger notifies as today. A wake-up is never dropped by a code path nobody thought of; at worst that path keeps today's cost.

A task due later (a retry's backoff) wakes nobody, as today. A worker finds it at a poll once it is due.

### 5.3 The trigger

A new forward migration replaces `taskiem_notify_task`. It is 00258's function with one line added at the top: if `current_setting('app.task_wake', true)` is `engine`, return. The trigger itself, its transition table and the one-second rule are unchanged.

Processes on the old binary never set the value, so the migration can go out before the binary. The value lasts for the transaction only.

### 5.4 The notifier

One in each process that has a `Store`, started with it and stopped with it.

- **State.** For each queue: whether it has a wake-up owed, and when the last was sent.
- **Being told.** The after-commit function marks the queue as owed and nudges the notifier. It never blocks and never touches the database, so it adds nothing to the caller's request.
- **Sending.** When a queue is owed and 10 ms have passed since its last send, the notifier sends for it at once. Otherwise it sends when the 10 ms are up. One statement carries every queue owed at that moment: `SELECT pg_notify('taskiem_tasks', q) FROM unnest($1::text[]) AS q`.
- **One at a time.** The notifier never has two sends under way. A slow database makes it send less often, with more behind each send. It cannot pile up.
- **Its connection.** Taken from the pool for each send, with a one-second limit. If the pool has none to give or the send fails, the queue stays owed, the notifier tries again after 100 ms, and a counter records it. Workers poll meanwhile.
- **Stopping.** On shutdown it sends what is owed once, within a second, and ends.

The interval is `TASKIEM_WAKE_INTERVAL` (default `10ms`; from `1ms` to `1s`). Ten milliseconds is a fifth of the dispatch objective's 50 ms ([reliability](../reliability.md)), and it is paid only by a task that arrives just after another for the same queue.

A process sends at most 100 wake-ups a second for a queue, however many tasks it schedules. Today it sends one for every task.

### 5.5 Waking a worker in the same process

The `Store` keeps the wake channels of the workers running in its process, by queue. The after-commit function signals them directly, before it marks the queue for the notifier.

- In a one-process install (`--role all`, the one-VM kit, `taskiem dev`) a task is claimed without waiting for the database to carry a message back.
- The notifier still sends, because other processes may have idle workers for that queue. A worker woken twice claims once: the wake channel holds one signal.

### 5.6 The listener and the claim loop

Unchanged. The listener's reconnect, its filter by queue and the one-second poll stay exactly as they are.

The claim loop was read again for this plan and is left alone. A claim that comes back short means nothing more could be claimed, so waiting for a wake-up is right; a claim that comes back full is followed by another as soon as a slot is free. Claiming again whenever a step finishes would add a claim to every step, and the claim is the most expensive statement the engine runs ([performance](../performance.md#results-the-original-code)).

One case does wait up to a second: a tenant at its cap, whose next task becomes claimable when one of its steps ends, with no new insert to announce it. That is today's behaviour and is not changed here (§10).

## 6. Why this is safe

| What goes wrong | What happens |
| --- | --- |
| The process dies after the commit and before the send | The task is in the table. A worker claims it at its next poll, within a second. The same as a notification lost in a reconnect today |
| The transaction rolls back | No function runs, nothing is sent |
| A savepoint holding the insert is rolled back, the outer transaction commits | A wake-up is sent for nothing. A worker claims, finds nothing, waits. Harmless |
| The database fails over | The listener reconnects and wakes once, as today. The notifier's send fails and is tried again |
| A wake-up arrives before a reader can see the row | It cannot: the send is after the commit has returned |
| Old and new binaries run together (a rolling update) | Old ones notify from the trigger, new ones from the notifier, on one channel with one payload. Either wakes both |
| A transaction-pooling proxy in front of the API | `pg_notify` needs no session of its own. The rule in [cloud](../cloud.md) about the `LISTEN` connection stands |

Exactly-once effects, fencing, leases and the audit chain are not touched. No table changes.

## 7. What a person running it sees

| | |
| --- | --- |
| `TASKIEM_WAKE_INTERVAL` | The least time between two wake-ups for one queue from one process. Default `10ms` |
| `TASKIEM_WAKE_IN_TX` | `true` returns this process to wake-ups sent by the trigger inside the transaction. Default `false`. For a comparison under load, or to rule this change out when something else is being investigated |

Metrics, beside the existing ones:

| Metric | Meaning |
| --- | --- |
| `taskiem_wakeups_sent_total{queue}` | Wake-ups the notifier sent |
| `taskiem_wakeups_merged_total{queue}` | Tasks whose wake-up rode on one already owed |
| `taskiem_wakeups_local_total{queue}` | Wake-ups delivered in memory |
| `taskiem_wakeups_in_tx_total{queue}` | Tasks left to the trigger (a savepoint, an unusual caller, the switch). Should be near nothing; a rise names a path to look at |
| `taskiem_wakeup_send_failures_total` | Sends that failed or found no connection |
| `taskiem_wakeup_send_duration_seconds` | How long a send took |

No alert is added. The dispatch objective already catches wake-ups that stop arriving: tasks would wait for the poll, and dispatch within 50 ms would fall.

## 8. Tests

| Test | What it proves |
| --- | --- |
| `TestAfterCommitRunsOnlyOnCommit` (`engine/db`) | Functions run once after a commit, in order; none after a rollback or an error from the function body; a panic in one is recovered and the commit stands |
| `TestAfterCommitRefusedOnSavepoint` | A savepoint's transaction refuses, so the caller falls back |
| `TestEngineTransactionsDoNotNotify` | With `app.task_wake` set, an insert into `tasks` sends nothing from the trigger; without it, 00258's tests (`TestTaskInsertsNotifyOncePerQueue`, `TestFutureTasksDoNotNotify`) pass unchanged |
| `TestTaskWakesAnotherProcess` | Two stores on one database: a task scheduled through the first wakes a worker of the second within the interval and a margin, with the trigger silent |
| `TestWakeupsAreMerged` | 500 tasks for one queue in 50 ms cause no more sends than the interval allows, and every task is claimed |
| `TestLocalWake` | A worker in the scheduling process claims with the notifier's sends held back |
| `TestWakeSurvivesSendFailure` | With the notifier's sends failing, every task is still claimed by the poll, the failure is counted, and sends resume |
| `TestWakeInTxSwitch` | With `TASKIEM_WAKE_IN_TX=true` the trigger notifies and the notifier sends nothing |
| `TestMixedVersionsWake` | A store that never sets the value (the old binary) and one that does wake each other's workers |
| The existing runtime, HA and chaos tests | Unchanged behaviour: `TestListenWakesOnlyForWantedPayloads`, `TestWorkerSurvivesConnectionDrops` and the rest |

## 9. Measuring it

Before and after, the two builds interleaved, as [performance](../performance.md) does.

1. **The transaction alone.** The `pgbench` script of the ingest transaction that performance names as "the first thing run on the target hardware", with and without `app.task_wake` set, at 8, 32 and 64 clients. This isolates the commit from everything else.
2. **The ramp.** `tools/loadtest` at 20, 40, 60 and 80 runs a second, 4 tenants, 2 workers, twice each. Record runs a second achieved, dispatch within 50 ms, run start within 5 s, and the share of waits by kind (`Lock:object`, `Lock:transactionid`, WAL).
3. **Low load.** 5 runs a second for 40 s: dispatch latency must not be worse than today, since the first wake-up for a quiet queue is sent at once.

**Done when:**

- `Lock:object` waits on engine commits are gone from the samples at every stage;
- above the knee, throughput levels off instead of falling;
- dispatch within 50 ms at 5 and at 20 runs a second is no worse than the old build, beyond the noise between rounds;
- about 37,000 fake transfers or more across the ramps, and none moved money twice.

No figure for runs a second is promised here. The review machine is shared and noisy, and the next limits (§10) are not touched. The number to quote comes from the target hardware (P4-R5).

## 10. What this does not do

| Left alone | Why |
| --- | --- |
| The run row and `connector_rate_buckets` | The largest waits above the knee in the last ramp. Each needs its own look: how long the run row is held across a decide, and whether the rate bucket can be taken in fewer, shorter updates |
| The claim statement | Still the most expensive statement. Fewer wake-ups for nothing help a little; the probe for each tenant remains |
| A tenant at its cap waiting up to a second for its next task | Needs a signal when a slot is released, with care not to add a claim to every step |
| Back-pressure at the edge | A decision for the product ([performance](../performance.md#what-remains)) |
| The chat stream, the bell, run streams | They do not use `NOTIFY` and are not changed |
| `engine/spike` | Phase 0's load test, kept as it was measured |
| A queue outside Postgres | Decided against ([0067](../decisions/0067-wake-ups-leave-the-transaction.md)) |

## 11. Steps

| Step | Work | Ships alone |
| --- | --- | --- |
| W1 | `db.AfterCommit` and the transaction `InTenantTx` hands out, with its tests | Yes: nothing uses it yet |
| W2 | The migration: the trigger is silent when `app.task_wake` is `engine` | Yes: no binary sets it yet |
| W3 | The notifier, the local wake, `applyEffects` using them, the two settings, the metrics, the tests of §8 | Yes, after W1 and W2 |
| W4 | The measurements of §9 on the review machine; [performance](../performance.md), spec 4.3 and [operations](../operations.md) brought up to date; the atlas page on durable execution rebuilt | With W3 |
| W5 | The same measurements on the target hardware | With P4-R5 |

W2 goes out before W3 in a deployment, as migrations already do before the binary.
