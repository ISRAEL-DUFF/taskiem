package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// streamRun sends a run's events as they are recorded, as server-sent
// events (spec 15.1, live view): each event's id is its sequence number,
// so a reconnecting client (Last-Event-ID, or ?after=) misses nothing.
// Personal data stays sealed. The stream ends with an "end" event once
// the run has ended and every event is sent, and closes after half an
// hour or at shutdown; clients reconnect.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request) {
	ref, _, err := s.runRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if v, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64); err == nil && v > after {
		after = v
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // proxies must not buffer the stream
	w.WriteHeader(http.StatusOK)
	write := func(format string, args ...any) error {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		return rc.Flush()
	}
	if write("retry: 2000\n\n") != nil {
		return
	}
	ctx := r.Context()
	deadline := time.Now().Add(30 * time.Minute)
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()
	lastWrite := time.Now()
	for time.Now().Before(deadline) {
		events, err := s.Store.HistoryAfter(ctx, ref, after, 500)
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("run stream", "run", ref.ID, "err", err)
			}
			return
		}
		ended := false
		for _, e := range events {
			b, _ := json.Marshal(e)
			if write("id: %d\nevent: run_event\ndata: %s\n\n", e.Seq, b) != nil {
				return
			}
			after, lastWrite = e.Seq, time.Now()
			ended = ended || e.IsTerminal()
		}
		if ended {
			_ = write("event: end\ndata: {}\n\n")
			return
		}
		if time.Since(lastWrite) > 15*time.Second {
			if write(": keep-alive\n\n") != nil {
				return
			}
			lastWrite = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.done():
			return
		case <-poll.C:
		}
	}
}

// done is closed when the server shuts down, ending long-lived streams.
func (s *Server) done() <-chan struct{} {
	if s.Done == nil {
		return nil
	}
	return s.Done
}
