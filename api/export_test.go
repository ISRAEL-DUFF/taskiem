package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/lang"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// Internals the external tests reach.
var SafeReturn = safeReturn

// BillOut is a bill as link@1 gives it to a workflow.
var BillOut = billOut

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

// WriteLinkedOrder writes one linked order into its seller's business, as
// the worker does for each it claims (two workers may run at once).
func (s *Server) WriteLinkedOrder(ctx context.Context, buyer, id uuid.UUID) error {
	return s.writeLinkedOrder(ctx, buyer, id)
}

// ResetLimiters forgets every in-memory limit (a test that waits hours).
func (s *Server) ResetLimiters() {
	s.limiters.mu.Lock()
	s.limiters.m, s.limiters.shared = nil, nil
	s.limiters.mu.Unlock()
}

// SetClosedReplyFault fails every closed reply after it is claimed (nil:
// none fails).
func (s *Server) SetClosedReplyFault(fn func() error) { s.closedReplyFault = fn }

// OpenBetween is how long hours were open between two times.
var OpenBetween = openBetween

// WarnTransfer says whether words ask for a transfer to a personal account.
var WarnTransfer = warnTransfer
