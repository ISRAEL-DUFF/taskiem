package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Task wake-ups (docs/spec/task-wake-ups.md, decision 0067).
//
// A worker waiting for work is woken by a notification on taskiem_tasks
// whose payload is the task's queue. The transaction that schedules a task
// does not send it: Postgres commits transactions that have notified one
// at a time, so they could not share a WAL flush. The process sends it
// after the commit instead, at most once every WakeInterval for a queue,
// and wakes its own workers in memory.
//
// A wake-up is a hint. A worker that misses one finds the task at its next
// poll, within a second; nothing here is needed for a task to run.

const (
	// DefaultWakeInterval is the least time between two wake-ups a process
	// sends for one queue (TASKIEM_WAKE_INTERVAL).
	DefaultWakeInterval = 10 * time.Millisecond
	// wakeDueWithin matches the trigger (migration 00258): a task due later
	// than this wakes nobody and is found by a poll once it is due.
	wakeDueWithin = time.Second
	// wakeSendTimeout bounds one send, the wait for a connection included.
	wakeSendTimeout = time.Second
	// wakeRetryAfter and wakeGiveUpAfter: a send that fails is tried again,
	// and after this many failures in a row what is owed is dropped. By
	// then every worker has polled, and a database that has been away for
	// a second wakes every listener when it returns (listen.go).
	wakeRetryAfter  = 100 * time.Millisecond
	wakeGiveUpAfter = 10
	// wakeOwnFor is how long a notification from a connection this
	// process's notifier sent on is taken to be its own.
	wakeOwnFor = time.Second
)

// waker is a Store's wake-ups: what is owed to each queue, the workers of
// this process to wake in memory, and the connections its sends used.
type waker struct {
	mu      sync.Mutex
	owed    map[string]bool
	last    map[string]time.Time
	sending bool
	local   map[string][]chan struct{}
	sentBy  map[uint32]time.Time // backend PID -> when the notifier last sent on it
}

// wakeSetting is the value of app.task_wake that tells the task trigger
// the engine sends this transaction's wake-up (migration 00426).
const wakeSetting = "engine"

// wakeAfterCommit arranges the wake-up for a task of queue due at at, and
// returns the value app.task_wake must have when the task is inserted:
// wakeSetting when the wake-up will be sent after tx commits, and empty
// when it is left to the trigger (the transaction cannot hold an
// after-commit function, or WakeInTx is set). A task not due soon wakes
// nobody either way.
func (s *Store) wakeAfterCommit(tx pgx.Tx, queue string, at time.Time) string {
	if time.Until(at) > wakeDueWithin {
		return "" // the trigger sends nothing for it either (00258)
	}
	if s.WakeInTx || !db.AfterCommit(tx, func() { s.taskScheduled(queue) }) {
		telemetry.WakeupsInTx.WithLabelValues(queue).Inc()
		return ""
	}
	return wakeSetting
}

// taskScheduled runs after the commit of a transaction that scheduled a
// task for queue. It never blocks and never touches the database.
func (s *Store) taskScheduled(queue string) {
	w := &s.wake
	w.mu.Lock()
	for _, ch := range w.local[queue] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	if len(w.local[queue]) > 0 {
		telemetry.WakeupsLocal.WithLabelValues(queue).Inc()
	}
	if w.owed == nil {
		w.owed = map[string]bool{}
		w.last = map[string]time.Time{}
	}
	if w.owed[queue] {
		telemetry.WakeupsMerged.WithLabelValues(queue).Inc()
	}
	w.owed[queue] = true
	start := !w.sending
	w.sending = true
	w.mu.Unlock()
	if start {
		go s.sendWakes()
	}
}

// sendWakes sends what is owed, one send at a time, until nothing is. It
// is the only sender: a slow database makes it send less often with more
// behind each send, never more sends at once.
func (s *Store) sendWakes() {
	w := &s.wake
	interval := s.WakeInterval
	if interval <= 0 {
		interval = DefaultWakeInterval
	}
	failures := 0
	for {
		w.mu.Lock()
		now := time.Now()
		var ready []string
		wait := time.Duration(-1)
		for q := range w.owed {
			if since := now.Sub(w.last[q]); since >= interval {
				ready = append(ready, q)
			} else if wait < 0 || interval-since < wait {
				wait = interval - since
			}
		}
		if len(ready) == 0 && wait < 0 {
			w.sending = false
			w.mu.Unlock()
			return
		}
		for _, q := range ready {
			delete(w.owed, q)
		}
		w.mu.Unlock()
		if len(ready) == 0 {
			time.Sleep(wait)
			continue
		}
		started := time.Now()
		err := s.sendWake(ready)
		telemetry.WakeupSendSeconds.Observe(time.Since(started).Seconds())
		w.mu.Lock()
		if err != nil {
			telemetry.WakeupSendFailures.Inc()
			failures++
			if failures < wakeGiveUpAfter {
				for _, q := range ready {
					w.owed[q] = true
				}
			}
			w.mu.Unlock()
			time.Sleep(wakeRetryAfter)
			continue
		}
		failures = 0
		sent := time.Now()
		for _, q := range ready {
			w.last[q] = sent
			telemetry.WakeupsSent.WithLabelValues(q).Inc()
		}
		w.mu.Unlock()
	}
}

// sendWake notifies the queues in one statement, on a connection of the
// pool. The transaction has written nothing, so its commit waits for no
// WAL flush.
func (s *Store) sendWake(queues []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), wakeSendTimeout)
	defer cancel()
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	w := &s.wake
	w.mu.Lock()
	if w.sentBy == nil {
		w.sentBy = map[uint32]time.Time{}
	}
	now := time.Now()
	for pid, at := range w.sentBy {
		if now.Sub(at) > wakeOwnFor {
			delete(w.sentBy, pid)
		}
	}
	w.sentBy[conn.Conn().PgConn().PID()] = now
	w.mu.Unlock()
	_, err = conn.Exec(ctx, `SELECT pg_notify('taskiem_tasks', q) FROM unnest($1::text[]) AS q`, queues)
	return err
}

// localWakes is a channel signalled when this process schedules a task for
// queue, and a function that ends it. A worker waits on it beside its
// listener, so a task scheduled in its own process is claimed without the
// database carrying a message back.
func (s *Store) localWakes(queue string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w := &s.wake
	w.mu.Lock()
	if w.local == nil {
		w.local = map[string][]chan struct{}{}
	}
	w.local[queue] = append(w.local[queue], ch)
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		for i, c := range w.local[queue] {
			if c == ch {
				w.local[queue] = append(w.local[queue][:i], w.local[queue][i+1:]...)
				break
			}
		}
	}
}

// sentHere reports whether a notification from backend pid is one this
// process's notifier sent. A worker that has localWakes was already woken
// for it in memory, and waking again would only cost a claim that finds
// nothing. A connection the notifier used a moment ago may, rarely, carry
// some other notification of this process (the trigger's, for a task
// inserted in a savepoint); skipping that one costs a wait for the poll.
func (s *Store) sentHere(pid uint32) bool {
	w := &s.wake
	w.mu.Lock()
	defer w.mu.Unlock()
	at, ok := w.sentBy[pid]
	return ok && time.Since(at) <= wakeOwnFor
}
