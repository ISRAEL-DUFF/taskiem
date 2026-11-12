# 0067 — Task wake-ups leave the transaction; the queue stays in Postgres

Date: 2026-11-12 · Status: **Accepted** (12 November 2026; not built) · Plan: [task wake-ups outside the transaction](../spec/task-wake-ups.md) · Amends [0002](0002-postgres-as-queue.md)

## Context

A transaction that schedules a task sends `NOTIFY` from a trigger, so that an idle worker claims the task at once ([0002](0002-postgres-as-queue.md)). Postgres commits transactions that have notified one at a time: each holds a lock shared by the whole database from before its commit until its WAL flush is done, so they cannot share a flush. [Performance](../performance.md#what-remains) names this as the main reason throughput falls away above about 40 runs a second on the review machine, and lists three options without choosing one.

A measurement on 12 November 2026 (PostgreSQL 16, a bare insert, the plan's §2) separated the cause from the noise: with `NOTIFY` in the transaction, 8 clients and 64 clients both committed about 3,000 times a second; without it, 64 clients committed about 20,000 times; and with the wake-ups sent from one other connection 100 times a second, about 19,000.

A reviewer suggested the usual answer to a slow database queue: hold the tasks in a separate queue, for example Redis. The founder asked how n8n does it, and allowed its source to be read for that question (see "Where the ideas came from").

## Decision

1. **The queue stays in Postgres.** The task and the event that schedules it commit in one transaction. That is why a step cannot be lost or run twice by a crash between two systems, and no outside queue can join that transaction (0002's amendment of 28 October).
2. **The wake-up is sent after the commit, by one notifier in each process.** A transaction that schedules a task no longer notifies. Once it has committed, the process's notifier sends `pg_notify('taskiem_tasks', <queue>)` on a connection of its own, at once for a quiet queue and otherwise at most once every 10 ms, one send standing for every task scheduled meanwhile.
3. **Workers in the same process are woken in memory.**
4. **The trigger stays as the fallback.** It is silent only in a transaction that has declared the engine will send the wake-up (`app.task_wake`). An insert from anywhere else notifies as before, so no path loses its wake-up by being forgotten.
5. **A wake-up remains a hint.** Workers poll every second, as they do now. A process that dies between its commit and its send costs that task up to a second and nothing else.
6. **`TASKIEM_WAKE_IN_TX=true` returns a process to the old behaviour**, for comparison under load.

## Alternatives considered

| Option | Why not |
| --- | --- |
| A separate queue (Redis or similar) holding the tasks | Two writes where there is one. A crash between them leaves an event with no task or a task with no event, at every step. The cure is an outbox table in Postgres and a relay, which is the table Taskiem already has with a second system to run, back up and fail over on every install, the one-VM kit included |
| Redis only as the wake-up signal, the tasks staying in Postgres | The same effect as the notifier, with a new service for it |
| No wake-ups; workers poll more often when busy | Simple, but every idle worker pays for the claim, the engine's most expensive statement, many times a second, and dispatch within 50 ms depends on the poll interval |
| Notify only when a claimer of that queue may be idle | The sender cannot know what another process's workers are doing without asking the database, which costs more than the wake-up |
| `synchronous_commit = off` for these transactions | A commit acknowledged and then lost in a crash is a provider told to pay with no record of it. Not for a system whose first rule is that an effect is recorded before it is trusted |
| Wait for a larger machine | A faster disk raises the ceiling and leaves its shape: one notifying commit per flush, however many CPUs |

## Where the ideas came from

- **Sending a notification after the commit, and merging many into one,** are general practice with PostgreSQL's `LISTEN/NOTIFY`, and follow from PostgreSQL's own documentation of how notifications are delivered at commit. The design was worked out from Taskiem's code and the measurement above before any other product was looked at.
- **n8n was read, once, for one question.** On 12 November 2026 the founder allowed the source of n8n (commit `d76d304d`, a shallow copy kept outside this repository) to be read to answer "how does n8n solve this?". This is the exposure the [clean-room policy](../clean-room-policy.md) (rule 1) asks to be recorded. It was read behind a wall: a separate reader, working only in that copy, reported behaviour in its own plain words, with no code, no identifiers beyond the names of third-party libraries and file paths, and no configuration; the author of this decision and its plan read that report and never the source. Nothing was copied.
- **What the report said, and what was taken from it: nothing was adopted.** n8n keeps its jobs in a queue library on Redis; a job points at an execution row in the SQL database; the row and the job are written one after the other with nothing joining them; workers wait on Redis, and the SQL database plays no part in waking them; one job is a whole execution, run in one worker; a worker that dies leaves its execution marked crashed, not resumed; no idempotency keys were found around a step's outside effects. That is the "separate queue" alternative above, and the report is the evidence for refusing it: it suits a product in which a failed run is run again by hand, and not one that moves money once.
- **The resemblance to record** (rule 4): none in the design chosen. Taskiem differs from n8n at each of those points, and did before this decision.

## Consequences

- Throughput above the knee should level off instead of falling. The next limits are the run row, `connector_rate_buckets` and WAL writes ([performance](../performance.md#what-remains)); this does not touch them, and no capacity is promised until the plan's measurements are run on the target hardware.
- A task scheduled just after another for the same queue may wait up to 10 ms longer for its wake-up, a fifth of the dispatch objective.
- Each process holds one more small piece of state and sends at most 100 wake-ups a second for a queue, whatever its load.
- Old and new binaries wake each other during a rolling update: the channel and the payload are unchanged.
- Spec 4.3 and 0002 still say "`LISTEN/NOTIFY` wakes idle workers; a 1 s poll is the fallback", and that stays true.
