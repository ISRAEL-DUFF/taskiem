package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/embed"
)

// Verified custom domains are checked again: a TXT record that disappears
// for long enough unverifies the domain (it stops serving the app), tells
// the partner by webhook and is audited; a timeout counts for nothing, and
// verifying again restores it.
func TestCustomDomainRecheck(t *testing.T) {
	w := ssoWorld(t)
	w.srv.EmbedDir = t.TempDir()
	_ = os.WriteFile(filepath.Join(w.srv.EmbedDir, "taskiem.js"), []byte("//"), 0o600)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	setCapabilities(t, w, p.id, "custom_domains")
	var mu sync.Mutex
	var events []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		events = append(events, m)
		mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hook.Close)
	// The app subscribes to nothing in particular: domain.unverified is
	// sent to the domain's app all the same.
	app, _ := p.app(t, map[string]any{"name": "web", "allowed_origins": []string{siteApp}, "webhook_url": hook.URL, "webhook_events": []string{"run.failed"}})
	const host = "automations.payrolla.test"
	domains := "/v1/partner/embed-apps/" + app + "/domains"
	dom := p.key.must(201, "POST", domains, map[string]any{"domain": host})
	record := "_taskiem-verify." + host
	w.txt[record] = []string{dom["txt_value"].(string)}
	p.key.must(200, "POST", domains+"/"+host+"/verify", nil)
	serves := func() bool {
		resp, _ := raw(t, "GET", w.base+"/embed/frame", host, "", nil)
		return resp.StatusCode == 200
	}
	if !serves() {
		t.Fatal("the verified domain does not serve")
	}

	clock := time.Now()
	timeout := false
	checker := &embed.DomainChecker{Pool: w.env.Store.Pool, Every: time.Hour, Failures: 3, Grace: 24 * time.Hour, Now: func() time.Time { return clock },
		Unverified: w.srv.ForgetCustomDomain,
		Lookup: func(ctx context.Context, name string) ([]string, error) {
			if timeout {
				return nil, &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
			}
			return w.txt[name], nil
		}}
	ctx := context.Background()
	step := func(d time.Duration) {
		t.Helper()
		clock = clock.Add(d)
		if err := checker.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	domainView := func() map[string]any {
		t.Helper()
		return p.key.must(200, "GET", "/v1/partner/embed-apps/"+app, nil)["domains"].([]any)[0].(map[string]any)
	}
	// Not due yet, then due and fine.
	verified := domainView()["checked_at"]
	step(time.Minute)
	if d := domainView(); d["checked_at"] != verified {
		t.Errorf("checked before due: %v", d)
	}
	step(2 * time.Hour)
	if d := domainView(); d["checked_at"] == verified || d["check_failures"] != nil {
		t.Errorf("after a good check: %v", d)
	}
	// The record goes; failures count, but within the grace it still serves.
	w.txt[record] = []string{"something else"}
	for range 3 {
		step(2 * time.Hour)
	}
	if d := domainView(); d["check_failures"] != float64(3) || d["verified_at"] == nil || d["last_check_error"] == nil || !serves() {
		t.Fatalf("within the grace: %v", d)
	}
	// A timeout says nothing about the record.
	timeout = true
	step(24 * time.Hour)
	if d := domainView(); d["check_failures"] != float64(3) || d["verified_at"] == nil {
		t.Fatalf("after a timeout: %v", d)
	}
	timeout = false
	step(2 * time.Hour)
	d := domainView()
	if d["verified_at"] != nil || d["unverified_at"] == nil || d["check_failures"] != float64(4) {
		t.Fatalf("not unverified: %v", d)
	}
	if serves() {
		t.Error("the unverified domain still serves the app")
	}
	if n := auditCount(t, w, p.id.String(), "embed_app.domain_unverified"); n != 1 {
		t.Errorf("audited %d times", n)
	}
	hooks := &embed.Webhooks{Pool: w.env.Store.Pool, Secrets: w.env.Vault, Client: http.DefaultClient}
	if err := hooks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := events
	mu.Unlock()
	if len(got) != 1 || got[0]["event"] != "domain.unverified" || got[0]["data"].(map[string]any)["domain"] != host {
		t.Fatalf("webhooks: %v", got)
	}
	// No longer checked; verifying again with the record back restores it.
	step(48 * time.Hour)
	if n := auditCount(t, w, p.id.String(), "embed_app.domain_unverified"); n != 1 {
		t.Errorf("unverified again: %d", n)
	}
	w.txt[record] = []string{dom["txt_value"].(string)}
	p.key.must(200, "POST", domains+"/"+host+"/verify", nil)
	if d := domainView(); d["verified_at"] == nil || d["check_failures"] != nil || d["unverified_at"] != nil || !serves() {
		t.Errorf("verified again: %v", d)
	}
}
