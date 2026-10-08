package decide

import (
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/history"
)

// A provider's Retry-After, recorded with the failure, delays the next
// attempt beyond the backoff (up to an hour); a shorter one changes nothing.
func TestRetryAfterDelaysTheRetry(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"q","type":"connector","connector":"pgdock@1","action":"query_rows",
	  "retry":{"max":5,"backoff":"exponential","initial":"2s"}}`, ""), map[string]any{})
	fail := func(ms int64) time.Duration {
		s.external(history.StepFailed, "q", s.lastAttempt("q"), history.FailedPayload{Error: history.Error{Kind: "retryable", Message: "429", Next: "retry", RetryAfterMS: ms}}, history.OriginWorker)
		at, _ := history.ParseTime(s.payload(history.RetryScheduled, "q")["at"].(string))
		return at.Sub(s.now)
	}
	if d := fail(30_000); d != 30*time.Second {
		t.Errorf("Retry-After 30s: next attempt in %s", d)
	}
	if d := fail(500); d < 4*time.Second || d > 4800*time.Millisecond {
		t.Errorf("a Retry-After shorter than the backoff: %s", d)
	}
	if d := fail(int64(3 * time.Hour / time.Millisecond)); d != time.Hour {
		t.Errorf("Retry-After of 3h: %s, want the 1h cap", d)
	}
}
