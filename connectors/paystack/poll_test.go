package paystack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
)

// fakeList is a Paystack list endpoint over n records a minute apart from
// t0, newest first, filtered by from and to and paged, with or without
// Paystack's meta.
func fakeList(t *testing.T, t0 time.Time, n int, meta bool, asked *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		*asked++
		q := r.URL.Query()
		from, err := time.Parse(time.RFC3339, q.Get("from"))
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		to := t0.Add(24 * 365 * time.Hour)
		if v := q.Get("to"); v != "" {
			if to, err = time.Parse(time.RFC3339, v); err != nil {
				rw.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		per, _ := strconv.Atoi(q.Get("perPage"))
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		var in []map[string]any
		for i := n - 1; i >= 0; i-- {
			at := t0.Add(time.Duration(i) * time.Minute)
			if at.Before(from) || at.After(to) {
				continue
			}
			in = append(in, map[string]any{"reference": fmt.Sprintf("P-%03d", i), "amount": 1000, "created_at": at.Format(time.RFC3339)})
		}
		lo, hi := min((page-1)*per, len(in)), min(page*per, len(in))
		body := map[string]any{"status": true, "data": in[lo:hi]}
		if meta {
			body["meta"] = map[string]any{"total": len(in), "perPage": per, "page": page, "pageCount": (len(in) + per - 1) / per}
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// More records since the last poll than one page of a hundred: every one
// is listed, over as many calls as it takes, each carrying on from the
// "through" of the one before. With one page of a hundred and "since"
// moved to now, all but a hundred were lost for good.
func TestListSinceLosesNothingBeyondAPage(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	for _, meta := range []bool{true, false} {
		for _, pages := range []int{1, 3, 10} {
			const n = 730
			asked := 0
			srv := fakeList(t, t0, n, meta, &asked)
			c := New(Options{BaseURL: srv.URL, PollPages: pages, Now: func() time.Time { return t0.Add(n * time.Minute) }})
			seen := map[string]int{}
			from := t0
			calls := 0
			for {
				if calls++; calls > 200 {
					t.Fatalf("meta %v, %d pages: no end after %d calls (from %s)", meta, pages, calls, from)
				}
				r, err := c.Actions["list_transactions"].Execute(context.Background(), connector.Request{
					Input: map[string]any{"from": from.Format(time.RFC3339)}, Credentials: map[string]string{"secret_key": "sk_test_xyz"}, HTTP: srv.Client()})
				if err != nil {
					t.Fatalf("meta %v, %d pages: %v", meta, pages, err)
				}
				out := r.Output.(map[string]any)
				data := out["data"].([]any)
				if len(data) > pages*100 {
					t.Fatalf("one call listed %d records, over its bound of %d", len(data), pages*100)
				}
				for _, d := range data {
					seen[d.(map[string]any)["reference"].(string)]++
				}
				through, _ := out["through"].(string)
				if through == "" {
					break
				}
				next, err := time.Parse(time.RFC3339, through)
				if err != nil || !next.After(from) {
					t.Fatalf("through %q does not move on from %s", through, from)
				}
				from = next
			}
			if len(seen) != n {
				t.Errorf("meta %v, %d pages: listed %d of %d records", meta, pages, len(seen), n)
			}
			if pages == 10 && calls != 1 {
				t.Errorf("730 records fit ten pages: one call, not %d", calls)
			}
		}
	}
}
