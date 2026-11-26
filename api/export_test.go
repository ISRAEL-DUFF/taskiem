package api

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/lang"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// Internals the external tests reach.
var SafeReturn = safeReturn

// BillOut is a bill as link@1 gives it to a workflow.
var BillOut = billOut

// CheckOldColumnsAll is the storefront sweep's old-columns check of every
// business with a catalogue.
func (s *Server) CheckOldColumnsAll(ctx context.Context) error { return s.checkOldColumnsAll(ctx) }

// WaitBackground waits for work done after answering (reset emails).
func (s *Server) WaitBackground() { s.bg.Wait() }

// GroupTickRuns is how many times live connections recomputed a group's
// ticks.
func (s *Server) GroupTickRuns() int64 { return s.groupTickRuns.Load() }

// SetUSSDStart replaces how the USSD hand-off starts runs (a slow engine).
func (s *Server) SetUSSDStart(fn func(context.Context, runtime.StartRequest) (runtime.RunRef, bool, error)) {
	s.ussd.start = fn
}

// PreviewAnyPort lets link previews reach a test's local page.
func (s *Server) PreviewAnyPort() { s.previewAnyPort = true }

// SetPreviewClock sets the clock of the composer's held previews.
func (s *Server) SetPreviewClock(fn func() time.Time) { s.previewClock = fn }

// HeldPreviews is how many of the composer's previews are held now.
func (s *Server) HeldPreviews() int { return s.holds.Len(s.now()) }

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

// LiveConnect and LiveClose open and close one of a person's live
// connections as GET /v1/me/live does.
func (s *Server) LiveConnect(ctx context.Context, person, conn uuid.UUID, front bool) error {
	return s.liveConnect(ctx, person, conn, front)
}

func (s *Server) LiveClose(person, conn uuid.UUID) { s.liveClose(person, conn) }

// FillLimiters fills the in-memory limiters to their most (memory
// pressure), with keys of a kind no route uses.
func (s *Server) FillLimiters() {
	for i := range maxLimiters {
		s.limiter("test-fill:"+uuid.NewString()+string(rune(i%10+'0')), time.Hour, 1)
	}
}

// SetGroupLinkJoinsPerHour changes how many may join one group by its link
// in an hour, answering a function that puts it back.
func SetGroupLinkJoinsPerHour(n int) func() {
	old := groupLinkJoinsPerHour
	groupLinkJoinsPerHour = n
	return func() { groupLinkJoinsPerHour = old }
}

// SetPeopleListPage changes how many chats a page of the lists answers,
// answering a function that puts it back.
func SetPeopleListPage(n int) func() {
	old := peopleListPage
	peopleListPage = n
	return func() { peopleListPage = old }
}

// SetPageToolWait sets how long a page's view waits for a tool it started.
func SetPageToolWait(d time.Duration) func() {
	old := pageToolWait
	pageToolWait = d
	return func() { pageToolWait = old }
}
