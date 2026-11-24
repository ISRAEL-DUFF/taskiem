package egress

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

// Callers build a client per call (a step, a host.fetch) and drop it: a
// connection kept for reuse would never be reused, only held open, with
// its goroutines, until the idle timeout (F80). Each one closes when its
// response does.
func TestClientPerCallLeavesNoConnectionOpen(t *testing.T) {
	var open atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	srv.Start()
	defer srv.Close()
	g := &Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && BlockedAddr(a) }}
	pol := Policy{Hosts: []string{"127.0.0.1"}, Purpose: "test"}
	for range 50 {
		resp, err := g.Client(pol, 5*time.Second).Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for open.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := open.Load(); n > 0 {
		t.Errorf("%d of 50 connections are still open after their responses were read", n)
	}
}
