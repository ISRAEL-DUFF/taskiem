package ingest_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
)

// More payments since the last poll than one poll reads (an outage, a
// busy merchant): the poller moves "since" only as far as what it
// delivered and carries on at the next tick, so every payment starts its
// workflow. It used to read one page and move on to now: the rest were
// never seen.
func TestPollerCarriesABacklogOverTicks(t *testing.T) {
	w := newWorld(t)
	start := time.Now().UTC().Truncate(time.Second)
	const n = 130 // twenty seconds apart from two minutes after the first look
	fake := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, _ := time.Parse(time.RFC3339, q.Get("from"))
		to := start.Add(24 * time.Hour)
		if v := q.Get("to"); v != "" {
			to, _ = time.Parse(time.RFC3339, v)
		}
		per, _ := strconv.Atoi(q.Get("perPage"))
		page, _ := strconv.Atoi(q.Get("page"))
		var in []map[string]any
		for i := n - 1; i >= 0; i-- {
			at := start.Add(2*time.Minute + time.Duration(i)*20*time.Second)
			if !at.Before(from) && !at.After(to) {
				in = append(in, map[string]any{"reference": fmt.Sprintf("B-%03d", i), "amount": 1000})
			}
		}
		lo, hi := min((page-1)*per, len(in)), min(page*per, len(in))
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{"status": true, "data": in[lo:hi],
			"meta": map[string]any{"pageCount": (len(in) + per - 1) / per}})
	}))
	t.Cleanup(fake.Close)
	now := start
	clock := func() time.Time { return now }
	if err := w.Registry.Register(paystack.New(paystack.Options{BaseURL: fake.URL, PollPages: 1, Now: clock})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	wf := w.publish(t, onChargeFlow)
	p := &ingest.Poller{Store: w.Store, Registry: w.Registry, Connections: w.Vault, Hooks: w.h,
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}, Now: clock}
	if n, err := p.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("first tick: %d %v", n, err)
	}
	// An hour on (the poller was down): 130 payments are waiting, and one
	// poll reads a hundred.
	now = start.Add(time.Hour)
	for tick := 0; tick < 12 && w.runCount(t, wf) < n; tick++ {
		got, err := p.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got > 100 {
			t.Fatalf("one tick delivered %d, over its bound", got)
		}
		// The next tick is a minute later, well inside the five-minute
		// interval: a backlog is carried on at once.
		now = now.Add(time.Minute)
	}
	if got := w.runCount(t, wf); got != n {
		t.Fatalf("%d of %d payments started their workflow", got, n)
	}
}
