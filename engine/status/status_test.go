package status_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/status"
)

var ctx = context.Background()

func component(p *status.Page, id string) status.Component {
	for _, c := range p.Components {
		if c.ID == id {
			return c
		}
	}
	return status.Component{}
}

func TestPageFromIncidentsAndMaintenance(t *testing.T) {
	d := dbtest.New(t)
	pool := d.App
	now := time.Now()

	p, err := status.Load(ctx, pool, now, status.Options{})
	if err != nil || p.Status != status.Operational || len(p.Components) != 4 || p.Summary != "All systems operational" {
		t.Fatalf("empty page: %+v %v", p, err)
	}

	inc, err := status.Open(ctx, pool, status.Declaration{Title: "Slow webhooks", Components: []string{"webhooks", "integration:paystack"}, Impact: status.PartialOutage, Message: "We are looking into it."}, "cli:ada")
	if err != nil {
		t.Fatal(err)
	}
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	maint, err := status.Open(ctx, pool, status.Declaration{Kind: "maintenance", Title: "Database upgrade", Components: []string{"runs"}, Message: "Runs pause for a few minutes.", StartsAt: &start, EndsAt: &end}, "api:ops")
	if err != nil {
		t.Fatal(err)
	}
	p, err = status.Load(ctx, pool, now, status.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != status.PartialOutage || component(p, "webhooks").Status != status.PartialOutage || component(p, "integration:paystack").Status != status.PartialOutage ||
		component(p, "runs").Status != status.Operational || len(p.Active) != 1 || len(p.Upcoming) != 1 || p.Upcoming[0].ID != maint {
		t.Fatalf("page: %+v", p)
	}
	// Within the window, maintenance shows without anyone posting.
	in, _ := status.Load(ctx, pool, start.Add(time.Minute), status.Options{})
	if component(in, "runs").Status != status.Maintenance {
		t.Errorf("runs during maintenance: %+v", component(in, "runs"))
	}
	if strings.Contains(mustJSON(t, p), "cli:ada") || strings.Contains(mustJSON(t, p), "api:ops") {
		t.Error("the public page names an operator")
	}

	// Updates; the impact carries over unless changed; resolving closes.
	if err := status.Post(ctx, pool, inc, status.Change{Status: "identified", Message: "A provider is slow."}, "cli:ada"); err != nil {
		t.Fatal(err)
	}
	if err := status.Post(ctx, pool, inc, status.Change{Status: "monitoring", Impact: status.Degraded, Message: "Recovering."}, "cli:ada"); err != nil {
		t.Fatal(err)
	}
	p, _ = status.Load(ctx, pool, time.Now(), status.Options{})
	if component(p, "webhooks").Status != status.Degraded || len(p.Active[0].Updates) != 3 || p.Active[0].Updates[0].Status != "monitoring" {
		t.Fatalf("after updates: %+v", p.Active)
	}
	if err := status.Post(ctx, pool, inc, status.Change{Status: "resolved", Message: "Fixed."}, "cli:ada"); err != nil {
		t.Fatal(err)
	}
	p, _ = status.Load(ctx, pool, time.Now(), status.Options{})
	if p.Status != status.Operational || len(p.Recent) != 1 || !p.Recent[0].Closed || component(p, "integration:paystack").ID != "" {
		t.Fatalf("after resolving: %+v", p)
	}
	if err := status.Post(ctx, pool, inc, status.Change{Status: "monitoring", Message: "again"}, "cli:ada"); !errors.Is(err, status.ErrInvalid) {
		t.Errorf("update after resolved: %v", err)
	}

	// Refusals.
	for name, d := range map[string]status.Declaration{
		"component":   {Title: "x", Components: []string{"billing"}, Message: "m"},
		"no message":  {Title: "x", Components: []string{"api"}},
		"impact":      {Title: "x", Components: []string{"api"}, Impact: status.Maintenance, Message: "m"},
		"window":      {Kind: "maintenance", Title: "x", Components: []string{"api"}, Message: "m"},
		"bad status":  {Title: "x", Components: []string{"api"}, Status: "scheduled", Message: "m"},
		"bad kind":    {Kind: "outage", Title: "x", Components: []string{"api"}, Message: "m"},
		"integration": {Title: "x", Components: []string{"integration:Pay Stack"}, Message: "m"},
	} {
		if _, err := status.Open(ctx, pool, d, "cli:ada"); !errors.Is(err, status.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := status.Post(ctx, pool, maint, status.Change{Status: "investigating", Message: "m"}, "x"); !errors.Is(err, status.ErrInvalid) {
		t.Errorf("incident status on maintenance: %v", err)
	}
	if err := status.Post(ctx, pool, uuid.New(), status.Change{Status: "resolved", Message: "m"}, "x"); !errors.Is(err, status.ErrNotFound) {
		t.Errorf("unknown incident: %v", err)
	}

	// The updates are the audit trail: who, and nothing can be rewritten,
	// not even by the superuser.
	all, err := status.Incidents(ctx, pool, time.Time{}, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("incidents: %v %v", all, err)
	}
	for _, i := range all {
		for _, u := range i.Updates {
			if u.Actor == "" {
				t.Errorf("update without actor: %+v", u)
			}
		}
	}
	for _, q := range []string{
		`UPDATE status_updates SET message = 'nothing happened'`,
		`DELETE FROM status_updates`,
		`UPDATE status_incidents SET title = 'x'`,
		`DELETE FROM status_incidents`,
		`TRUNCATE status_updates CASCADE`,
	} {
		if _, err := d.Admin.Exec(ctx, q); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v", q, err)
		}
	}
	// The application role reads but cannot write directly.
	if _, err := pool.Exec(ctx, `INSERT INTO status_incidents (id, kind, title, components, created_by) VALUES (gen_random_uuid(), 'incident', 'x', '{api}', 'x')`); err == nil {
		t.Error("the app role inserted an incident without the function")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO status_probes (ok) VALUES (true)`); err == nil {
		t.Error("the app role inserted a probe without the function")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCanaryMarksComponentsDegraded(t *testing.T) {
	d := dbtest.New(t)
	pool := d.App
	opts := status.Options{Canary: true}
	for i := 0; i < 2; i++ {
		if err := status.RecordProbe(ctx, pool, false, 30*time.Millisecond, 0, "complete:timeout"); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := status.Load(ctx, pool, time.Now(), opts)
	if p.Status != status.Operational || p.Canary == nil || p.Canary.Probes24h != 2 || p.Canary.LastOK {
		t.Fatalf("two failures: %+v %+v", p, p.Canary)
	}
	_ = status.RecordProbe(ctx, pool, false, 0, 0, "complete:run_failed")
	p, _ = status.Load(ctx, pool, time.Now(), opts)
	if c := component(p, "runs"); c.Status != status.Degraded || !c.Automatic || p.Status != status.Degraded {
		t.Fatalf("three failures: %+v", p.Components)
	}
	// Stale results say nothing.
	if p, _ := status.Load(ctx, pool, time.Now().Add(time.Hour), opts); component(p, "runs").Status != status.Operational {
		t.Errorf("stale probes: %+v", p.Components)
	}
	// A webhook that is not accepted points at ingest.
	for i := 0; i < 3; i++ {
		_ = status.RecordProbe(ctx, pool, false, 0, 0, "accept:http_503")
	}
	p, _ = status.Load(ctx, pool, time.Now(), opts)
	if component(p, "webhooks").Status != status.Degraded || component(p, "runs").Status != status.Operational {
		t.Errorf("accept failures: %+v", p.Components)
	}
	// One success clears it; an operator's declaration wins over the canary.
	_ = status.RecordProbe(ctx, pool, true, 20*time.Millisecond, 900*time.Millisecond, "")
	p, _ = status.Load(ctx, pool, time.Now(), opts)
	if p.Status != status.Operational || !p.Canary.LastOK || p.Canary.Passed24h <= 0 {
		t.Errorf("after a success: %+v %+v", p.Components, p.Canary)
	}
	if p, _ := status.Load(ctx, pool, time.Now(), status.Options{}); p.Canary != nil {
		t.Error("canary shown while off")
	}
}

func TestHandlerIsPublicAndCacheable(t *testing.T) {
	d := dbtest.New(t)
	h := &status.Handler{Pool: d.App, PublicURL: "https://taskiem.example", TTL: time.Hour}
	if _, err := status.Open(ctx, d.App, status.Declaration{Title: "API errors <b>", Components: []string{"api"}, Impact: status.MajorOutage, Message: "Investigating <script>alert(1)</script>"}, "cli:ada"); err != nil {
		t.Fatal(err)
	}
	get := func(fn http.HandlerFunc, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/status", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		fn(w, r)
		return w
	}
	html := get(h.HTML, nil)
	body := html.Body.String()
	if html.Code != 200 || !strings.Contains(body, "Major outage") || strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;") ||
		strings.Contains(body, "cli:ada") || !strings.HasPrefix(html.Header().Get("Cache-Control"), "public, max-age=30") || html.Header().Get("Set-Cookie") != "" {
		t.Fatalf("html %d %v: %s", html.Code, html.Header(), body)
	}
	js := get(h.JSON, nil)
	var page status.Page
	if js.Code != 200 || json.Unmarshal(js.Body.Bytes(), &page) != nil || page.Status != status.MajorOutage || js.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("json %d: %s", js.Code, js.Body.String())
	}
	if again := get(h.JSON, map[string]string{"If-None-Match": js.Header().Get("ETag")}); again.Code != http.StatusNotModified {
		t.Errorf("etag: %d", again.Code)
	}
	feed := get(h.Atom, nil)
	if feed.Code != 200 || !strings.Contains(feed.Body.String(), "<feed xmlns=\"http://www.w3.org/2005/Atom\">") ||
		!strings.Contains(feed.Body.String(), "https://taskiem.example/status/feed.atom") || !strings.Contains(feed.Body.String(), "API errors &lt;b&gt;: Investigating") {
		t.Fatalf("feed: %s", feed.Body.String())
	}

	// Cached: a new incident is not seen until the TTL passes or the cache
	// is invalidated (the admin API does).
	if _, err := status.Open(ctx, d.App, status.Declaration{Title: "Second", Components: []string{"runs"}, Message: "m"}, "cli:ada"); err != nil {
		t.Fatal(err)
	}
	if p := decode(t, get(h.JSON, nil)); len(p.Active) != 1 {
		t.Errorf("not cached: %d", len(p.Active))
	}
	h.Invalidate()
	if p := decode(t, get(h.JSON, nil)); len(p.Active) != 2 {
		t.Errorf("after invalidate: %d", len(p.Active))
	}

	// A database outage serves the last page, marked stale; with none, 503.
	d.App.Close()
	h.Invalidate()
	if w := get(h.JSON, nil); w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("no page and no database: %d", w.Code)
	}
}

func decode(t *testing.T, w *httptest.ResponseRecorder) status.Page {
	t.Helper()
	var p status.Page
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
