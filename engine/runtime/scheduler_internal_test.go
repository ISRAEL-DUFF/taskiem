package runtime

import (
	"errors"
	"testing"
)

// A tick whose only errors are timers or runs it could not decide still
// counts as a tick: the staleness alert is for a scheduler that stopped,
// not for one tenant's run.
func TestOnlyItemErrors(t *testing.T) {
	item := &itemError{errors.New("run x: no key")}
	for err, want := range map[error]bool{
		item:                                   true,
		errors.Join(item, nil, item):           true,
		errors.Join(errors.Join(item), item):   true,
		errors.New("claim failed"):             false,
		errors.Join(item, errors.New("purge")): false,
	} {
		if got := onlyItemErrors(err); got != want {
			t.Errorf("%v: %v, want %v", err, got, want)
		}
	}
}
