package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/lang"

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

// PreviewAnyPort lets link previews reach a test's local page.
func (s *Server) PreviewAnyPort() { s.previewAnyPort = true }

// BillsDueLine is the morning summary's line on linked suppliers' bills.
func (s *Server) BillsDueLine(ctx context.Context, tenant uuid.UUID) string {
	return s.billsDueDailyLine(ctx, lang.EN, tenant)
}
