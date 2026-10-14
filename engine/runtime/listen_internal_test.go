package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// A worker is woken only by tasks for its own queue: a notification for
// another queue would only cost it a claim that finds nothing
// (docs/performance.md).
func TestListenWakesOnlyForWantedPayloads(t *testing.T) {
	d := dbtest.New(t)
	s := &Store{Pool: d.AppPool(t, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake, stop, err := listen(ctx, s, "taskiem_tasks", func(q string) bool { return q == "sandbox" })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	woken := func() bool {
		select {
		case <-wake:
			return true
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}
	notify := func(payload string) {
		if _, err := d.Admin.Exec(ctx, `SELECT pg_notify('taskiem_tasks', $1)`, payload); err != nil {
			t.Fatal(err)
		}
	}
	notify("connector")
	if woken() {
		t.Error("a connector task woke the sandbox listener")
	}
	notify("sandbox")
	if !woken() {
		t.Error("a sandbox task did not wake the sandbox listener")
	}
}
