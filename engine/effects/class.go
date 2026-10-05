package effects

import (
	"errors"
	"fmt"
)

// Class is an action's effect class. It alone decides retry behaviour.
type Class string

const (
	Read              Class = "read"
	IdempotentWrite   Class = "idempotent_write"
	ReconcilableWrite Class = "reconcilable_write"
	UnsafeWrite       Class = "unsafe_write"
)

// ParseClass rejects unknown and empty classes; there is no default.
func ParseClass(s string) (Class, error) {
	switch c := Class(s); c {
	case Read, IdempotentWrite, ReconcilableWrite, UnsafeWrite:
		return c, nil
	}
	return "", fmt.Errorf("effects: unknown action class %q", s)
}

// IsWrite reports whether the class has external side effects.
func (c Class) IsWrite() bool { return c != Read }

// Sentinel errors that handlers wrap to classify a failure.
var (
	ErrRetryable      = errors.New("retryable")
	ErrFatal          = errors.New("fatal")
	ErrUnknownOutcome = errors.New("unknown_outcome")
	// ErrNotSent marks a retryable failure that provably happened before any
	// request byte left the process (DNS failure, connection refused). Only
	// these may be retried for an unsafe_write.
	ErrNotSent = errors.New("not_sent")
)

// ErrorKind is the engine's view of a step failure.
type ErrorKind int

const (
	KindUnknownOutcome ErrorKind = iota // the safe default
	KindRetryable
	KindFatal
	KindNotSent
)

func (k ErrorKind) String() string {
	switch k {
	case KindRetryable:
		return "retryable"
	case KindFatal:
		return "fatal"
	case KindNotSent:
		return "not_sent"
	}
	return "unknown_outcome"
}

// Classify maps a handler error to an ErrorKind. Unclassified errors are
// unknown outcomes, because assuming nothing was sent is the unsafe guess.
func Classify(err error) ErrorKind {
	switch {
	case errors.Is(err, ErrFatal):
		return KindFatal
	case errors.Is(err, ErrNotSent):
		return KindNotSent
	case errors.Is(err, ErrUnknownOutcome):
		return KindUnknownOutcome
	case errors.Is(err, ErrRetryable):
		return KindRetryable
	}
	return KindUnknownOutcome
}

// Next is what the engine does after a failure, or before re-executing a
// step that has an open EffectIntent from an earlier attempt.
type Next int

const (
	Retry     Next = iota // send again with the same idempotency key
	Reconcile             // run the connector's reconcile action first
	Park                  // stop; needs_reconciliation and alert a human
	Fail                  // the attempt failed for good; on_error or fail the run
	Complete              // the effect is known to have happened; record its output
)

func (n Next) String() string {
	return [...]string{"retry", "reconcile", "park", "fail", "complete"}[n]
}

// AfterError decides the next move once an attempt failed with kind. Retry
// budgets (max attempts, durations) are applied by the caller.
func AfterError(c Class, kind ErrorKind) Next {
	if kind == KindFatal {
		return Fail
	}
	switch c {
	case Read, IdempotentWrite:
		return Retry
	case ReconcilableWrite:
		if kind == KindUnknownOutcome {
			return Reconcile
		}
		return Retry
	case UnsafeWrite:
		if kind == KindNotSent {
			return Retry
		}
		return Park
	}
	return Park
}

// OnOpenIntent decides what to do when a worker is about to execute a write
// and finds an EffectIntent from an earlier attempt with no recorded outcome.
func OnOpenIntent(c Class) Next {
	switch c {
	case Read, IdempotentWrite:
		return Retry
	case ReconcilableWrite:
		return Reconcile
	}
	return Park
}

// ReconcileResult is what a reconcile action reports.
type ReconcileResult string

const (
	Found         ReconcileResult = "found"
	NotFound      ReconcileResult = "not_found"
	Indeterminate ReconcileResult = "indeterminate"
)

// AfterReconcile maps a reconcile result to the next move. Retry after
// NotFound must use the next attempt group (a fresh key).
func AfterReconcile(r ReconcileResult) (next Next, newAttemptGroup bool) {
	switch r {
	case Found:
		return Complete, false
	case NotFound:
		return Retry, true
	}
	return Park, false
}
