package api

import (
	"context"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// Internals the external tests reach.
var SafeReturn = safeReturn

// WaitBackground waits for work done after answering (reset emails).
func (s *Server) WaitBackground() { s.bg.Wait() }

// SetUSSDStart replaces how the USSD hand-off starts runs (a slow engine).
func (s *Server) SetUSSDStart(fn func(context.Context, runtime.StartRequest) (runtime.RunRef, bool, error)) {
	s.ussd.start = fn
}
