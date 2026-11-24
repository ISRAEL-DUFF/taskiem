package runtime

import (
	"errors"
	"strings"
	"testing"
)

// A task that panics is an error for the executor, which logs it and goes
// on serving: the process, and every other tenant's tasks, stay up.
func TestRecoveredTurnsAPanicIntoAnError(t *testing.T) {
	var p *plan
	err := recovered(func() error { _ = p.conn.Manifest; return nil })
	if err == nil || !strings.Contains(err.Error(), "panic executing task") || !strings.Contains(err.Error(), "nil pointer") {
		t.Fatalf("err %v", err)
	}
	want := errors.New("plain")
	if err := recovered(func() error { return want }); !errors.Is(err, want) {
		t.Errorf("an error passes through: %v", err)
	}
}
