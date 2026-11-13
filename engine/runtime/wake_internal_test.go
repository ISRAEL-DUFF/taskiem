package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// heard collects the notifications a connection of its own receives on
// taskiem_tasks until stop is called.
func heard(t *testing.T, d *dbtest.DB) (stop func() []uint32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l, err := pgx.Connect(ctx, d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Exec(ctx, `LISTEN taskiem_tasks`); err != nil {
		t.Fatal(err)
	}
	done := make(chan []uint32, 1)
	go func() {
		var pids []uint32
		for {
			n, err := l.WaitForNotification(ctx)
			if err != nil {
				done <- pids
				return
			}
			pids = append(pids, n.PID)
		}
	}()
	return func() []uint32 {
		cancel()
		pids := <-done
		_ = l.Close(context.Background())
		return pids
	}
}

func idle(s *Store) bool {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	return !s.wake.sending && len(s.wake.owed) == 0
}

func waitIdle(t *testing.T, s *Store, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); !idle(s); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the notifier was still sending after %s", within)
		}
	}
}

// Many tasks for one queue in a short time are told to other processes in
// a few wake-ups, not one each, and the last task still gets one
// (docs/spec/task-wake-ups.md, 5.4).
func TestWakeupsAreMerged(t *testing.T) {
	d := dbtest.New(t)
	s := &Store{Pool: d.AppPool(t, 4), WakeInterval: 50 * time.Millisecond}
	stop := heard(t, d)
	began := time.Now()
	for i := 0; i < 500; i++ {
		s.taskScheduled("connector")
		if i%50 == 49 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	burst := time.Since(began)
	waitIdle(t, s, 5*time.Second)
	whole := time.Since(began)
	time.Sleep(100 * time.Millisecond) // the last notification's delivery
	got := len(stop())
	if most := int(whole/(50*time.Millisecond)) + 2; got < 2 || got > most {
		t.Errorf("500 tasks in %s were told in %d wake-ups, want at least 2 (the first, and one after the burst) and at most %d", burst, got, most)
	}
}

// A worker of the process that scheduled a task is woken in memory, and
// can tell the notification its own process then sends from another
// process's (5.5).
func TestLocalWakeAndOwnNotifications(t *testing.T) {
	d := dbtest.New(t)
	s := &Store{Pool: d.AppPool(t, 4)}
	other := &Store{Pool: d.AppPool(t, 4)}
	local, unhear := s.localWakes("connector")
	sandbox, unhearSandbox := s.localWakes("sandbox")
	defer unhearSandbox()

	stop := heard(t, d)
	s.taskScheduled("connector")
	select {
	case <-local:
	default:
		t.Fatal("scheduling a task did not wake this process's worker at once")
	}
	select {
	case <-sandbox:
		t.Error("a connector task woke the sandbox worker")
	default:
	}
	waitIdle(t, s, 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	pids := stop()
	if len(pids) != 1 {
		t.Fatalf("one task sent %d notifications, want 1", len(pids))
	}
	if !s.sentHere(pids[0]) {
		t.Error("the process did not know its own notification")
	}
	if other.sentHere(pids[0]) {
		t.Error("another process took the notification for its own")
	}

	unhear()
	s.taskScheduled("connector")
	select {
	case <-local:
		t.Error("a worker that stopped was still woken")
	default:
	}
	waitIdle(t, s, 5*time.Second)
}

// A notifier that cannot reach the database tries again, then drops what
// it owes instead of trying for ever: workers poll, and it sends again
// when the next task is scheduled (5.4).
func TestWakeSurvivesSendFailure(t *testing.T) {
	d := dbtest.New(t)
	closed := d.AppPool(t, 2)
	closed.Close()
	s := &Store{Pool: closed}
	s.taskScheduled("connector")
	waitIdle(t, s, 10*time.Second)

	s.Pool = d.AppPool(t, 2)
	stop := heard(t, d)
	s.taskScheduled("connector")
	waitIdle(t, s, 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	if got := len(stop()); got != 1 {
		t.Errorf("after the database returned, a task sent %d notifications, want 1", got)
	}
}

// Which transactions the engine wakes for itself: one from InTenantTx,
// for a task due now. A savepoint, a task due later and a store told to
// leave wake-ups in the transaction are left to the trigger (5.2).
func TestWakeAfterCommitChoosesWhoWakes(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	s := &Store{Pool: d.AppPool(t, 4)}
	inTx := &Store{Pool: s.Pool, WakeInTx: true}
	now := time.Now()
	stop := heard(t, d)
	err := db.InTenantTx(context.Background(), s.Pool, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		if got := s.wakeAfterCommit(tx, "connector", now); got != wakeSetting {
			t.Errorf("a task due now in a tenant transaction: app.task_wake %q, want %q", got, wakeSetting)
		}
		if got := s.wakeAfterCommit(tx, "connector", now.Add(time.Minute)); got != "" {
			t.Errorf("a task due in a minute: app.task_wake %q, want it left to the trigger", got)
		}
		if got := inTx.wakeAfterCommit(tx, "connector", now); got != "" {
			t.Errorf("with WakeInTx: app.task_wake %q, want it left to the trigger", got)
		}
		sp, err := tx.Begin(context.Background())
		if err != nil {
			return err
		}
		if got := s.wakeAfterCommit(sp, "connector", now); got != "" {
			t.Errorf("a task in a savepoint: app.task_wake %q, want it left to the trigger", got)
		}
		if !idle(s) {
			t.Error("a wake-up was owed before the commit")
		}
		return sp.Commit(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, s, 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	if got := len(stop()); got != 1 {
		t.Errorf("the one task the engine wakes for sent %d notifications after the commit, want 1", got)
	}

	// A rollback sends nothing.
	stop = heard(t, d)
	_ = db.InTenantTx(context.Background(), s.Pool, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		s.wakeAfterCommit(tx, "connector", now)
		return context.Canceled
	})
	time.Sleep(150 * time.Millisecond)
	if got := len(stop()); got != 0 || !idle(s) {
		t.Errorf("a transaction rolled back sent %d notifications, want none", got)
	}
}
