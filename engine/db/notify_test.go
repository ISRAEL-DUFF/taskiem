package db_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// taskWakeups inserts tasks for the given queues (one per entry, in one
// statement and transaction) and returns how many times the notify
// trigger ran and the notifications a listener received.
func taskWakeups(t *testing.T, d *dbtest.DB, queues []string, due string) (int, []string) {
	t.Helper()
	tn := d.SeedTenant(t, nil)
	run := d.StartRun(t, tn)
	l, err := pgx.Connect(ctx, d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close(ctx) }()
	if _, err := l.Exec(ctx, `LISTEN taskiem_tasks`); err != nil {
		t.Fatal(err)
	}
	var calls int
	err = pgx.BeginFunc(ctx, d.Admin, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL track_functions = 'all'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tasks (id, tenant_id, run_id, step_id, attempt, queue, available_at)
			SELECT gen_random_uuid(), $1, $2, 's' || i, 1, q, now() + $4::interval
			  FROM unnest($3::text[]) WITH ORDINALITY AS u(q, i)`, tn.ID, run, queues, due); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(calls), 0) FROM pg_stat_xact_user_functions WHERE funcname = 'taskiem_notify_task'`).Scan(&calls)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		n, err := l.WaitForNotification(wctx)
		cancel()
		if err != nil {
			break
		}
		got = append(got, n.Payload)
	}
	sort.Strings(got)
	return calls, got
}

// Inserting many tasks sends one wake-up per queue, from one trigger call
// per statement rather than one per row (docs/performance.md).
func TestTaskInsertsNotifyOncePerQueue(t *testing.T) {
	d := dbtest.New(t)
	same := make([]string, 20)
	for i := range same {
		same[i] = "connector"
	}
	calls, got := taskWakeups(t, d, same, "0 seconds")
	if calls != 1 {
		t.Errorf("20 tasks in one statement ran the notify trigger %d times, want 1", calls)
	}
	if len(got) != 1 || got[0] != "connector" {
		t.Errorf("20 connector tasks sent %q, want one connector wake-up", got)
	}

	_, got = taskWakeups(t, d, []string{"connector", "sandbox", "connector", "sandbox", "ai"}, "0 seconds")
	if want := []string{"ai", "connector", "sandbox"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("mixed tasks sent %q, want one wake-up per queue %q", got, want)
	}
}

// A task that is not due yet wakes nobody: the worker's poll finds it
// once it is due.
func TestFutureTasksDoNotNotify(t *testing.T) {
	d := dbtest.New(t)
	if _, got := taskWakeups(t, d, []string{"connector"}, "1 minute"); len(got) != 0 {
		t.Errorf("a task due in a minute sent %q, want no wake-up", got)
	}
}
