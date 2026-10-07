package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, err.Error(), nil
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// The canary set up through the API against a real engine: the signed
// webhook, the sandbox worker and the runs API, then its results on the
// public status page once the scheduler records them.
func TestCanaryEndToEnd(t *testing.T) {
	srv := startServer(t)
	login := srv.call(t, "POST", "/v1/auth/login", "", map[string]any{"email": "admin@smoke.test", "password": "correct horse battery"})
	tok := login["token"].(string)

	var out bytes.Buffer
	if err := run([]string{"canary", "setup", "--url", srv.base, "--key", tok}, &out, &out); err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	settings := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "TASKIEM_CANARY_") {
			settings[k] = v
			t.Setenv(k, v)
		}
	}
	if len(settings) != 4 || !strings.HasPrefix(settings["TASKIEM_CANARY_HOOK_URL"], srv.base+"/hooks/"+login["tenant_id"].(string)+"/taskiem-canary?env=prod") {
		t.Fatalf("settings %v from:\n%s", settings, out.String())
	}
	// The canary's key reads runs and nothing else.
	req, _ := http.NewRequest("GET", srv.base+"/v1/workflows", nil)
	req.Header.Set("Authorization", "Bearer "+settings["TASKIEM_CANARY_API_KEY"])
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("canary key lists workflows: %v %v", resp, err)
	}

	out.Reset()
	if err := run([]string{"canary", "probe", "--count", "2"}, &out, &out); err != nil {
		t.Fatalf("probe: %v\n%s", err, out.String())
	}
	if strings.Count(out.String(), "ok ") != 2 {
		t.Errorf("probe output: %s", out.String())
	}
	// Setting up again rotates the webhook key: the old one stops working.
	old := settings["TASKIEM_CANARY_SECRET"]
	out.Reset()
	if err := run([]string{"canary", "setup", "--url", srv.base, "--key", tok}, &out, &out); err != nil || !strings.Contains(out.String(), "exists") {
		t.Fatalf("setup again: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), old) {
		t.Error("setup again kept the webhook key")
	}
	out.Reset()
	if err := run([]string{"canary", "probe"}, &out, &out); err == nil || !strings.Contains(out.String(), "FAILED  at accept (http_401)") {
		t.Errorf("old secret: %v %s", err, out.String())
	}

	// Metrics the SLO rules read.
	_, metrics, _ := get(t, "http://"+srv.metrics+"/metrics")
	for _, m := range []string{`taskiem_canary_probes_total{result="ok"} 2`, `taskiem_canary_probes_total{result="accept"} 1`,
		`taskiem_step_dispatch_delay_seconds_bucket{queue="sandbox"`, "taskiem_run_start_seconds_bucket", `taskiem_scheduler_last_tick_timestamp_seconds{mode="scheduler"}`} {
		if !strings.Contains(metrics, m) {
			t.Errorf("metrics lack %s", m)
		}
	}

	code, body, hdr := get(t, srv.base+"/status.json")
	var page struct {
		Status     string `json:"status"`
		Components []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"components"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &page) != nil || page.Status != "operational" || len(page.Components) != 4 ||
		!strings.HasPrefix(hdr.Get("Cache-Control"), "public") || strings.Contains(body, login["tenant_id"].(string)) {
		t.Fatalf("status.json %d %v: %s", code, hdr, body)
	}
	if code, body, _ := get(t, srv.base+"/status"); code != 200 || !strings.Contains(body, "All systems operational") {
		t.Errorf("status page %d: %s", code, body)
	}
}

// Operators declare incidents through the admin API (with a token) and the
// CLI; both are recorded with who did it, and the page follows.
func TestStatusAdminAPIAndCLI(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"status", "token", "ops"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(out.String())
	var token, entry string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "tks_"):
			token = l
		case strings.HasPrefix(l, "ops:"):
			entry = l
		}
	}
	if token == "" || entry == "" {
		t.Fatalf("token output: %s", out.String())
	}
	t.Setenv("TASKIEM_STATUS_TOKENS", entry)
	srv := startServer(t)

	admin := func(method, path, tok string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	if code, _ := admin("POST", "/v1/status/admin/incidents", "tks_wrong", map[string]any{}); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	// A tenant's session is not an operator.
	login := srv.call(t, "POST", "/v1/auth/login", "", map[string]any{"email": "admin@smoke.test", "password": "correct horse battery"})
	if code, _ := admin("GET", "/v1/status/admin/incidents", login["token"].(string), nil); code != http.StatusUnauthorized {
		t.Errorf("tenant session: %d", code)
	}
	if code, m := admin("POST", "/v1/status/admin/incidents", token, map[string]any{"title": "x", "components": []string{"billing"}, "message": "m"}); code != http.StatusUnprocessableEntity {
		t.Errorf("bad component: %d %v", code, m)
	}
	code, m := admin("POST", "/v1/status/admin/incidents", token, map[string]any{"title": "Webhooks delayed", "components": []string{"webhooks"}, "impact": "partial_outage", "message": "Investigating."})
	if code != http.StatusCreated {
		t.Fatalf("open: %d %v", code, m)
	}
	id := m["id"].(string)
	if _, body, _ := get(t, srv.base+"/status.json"); !strings.Contains(body, `"status":"partial_outage"`) || strings.Contains(body, "api:ops") {
		t.Errorf("page after open: %s", body)
	}

	out.Reset()
	if err := run([]string{"status", "update", id, "--status", "monitoring", "--impact", "degraded", "--message", "A fix is out."}, &out, &out); err != nil {
		t.Fatalf("cli update: %v %s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"status", "resolve", id, "--message", "Resolved."}, &out, &out); err != nil {
		t.Fatalf("cli resolve: %v %s", err, out.String())
	}
	code, m = admin("GET", "/v1/status/admin/incidents", token, nil)
	raw, _ := json.Marshal(m)
	if code != 200 || !strings.Contains(string(raw), `"actor":"api:ops"`) || !strings.Contains(string(raw), `"actor":"cli:`) || !strings.Contains(string(raw), `"status":"resolved"`) {
		t.Errorf("admin list: %d %s", code, raw)
	}
	out.Reset()
	start := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().Add(26 * time.Hour).UTC().Format(time.RFC3339)
	if err := run([]string{"status", "maintenance", "--title", "Database upgrade", "--components", "runs,scheduler", "--from", start, "--to", end, "--message", "Runs pause briefly."}, &out, &out); err != nil {
		t.Fatalf("cli maintenance: %v %s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"status", "show"}, &out, &out); err != nil || !strings.Contains(out.String(), "Database upgrade") || !strings.Contains(out.String(), `"status": "operational"`) {
		t.Errorf("show: %v %s", err, out.String())
	}
	if code, body, _ := get(t, srv.base+"/status/feed.atom"); code != 200 || !strings.Contains(body, "Webhooks delayed: Investigating") {
		t.Errorf("feed %d: %s", code, body)
	}
}

// On SIGTERM readiness flips first; requests are still served during the
// shutdown delay; then the process stops.
func TestShutdownFlipsReadinessFirst(t *testing.T) {
	t.Setenv("TASKIEM_SHUTDOWN_DELAY", "1500ms")
	t.Setenv("TASKIEM_WORKER_DRAIN", "2s")
	srv := startServer(t)
	if code, _, _ := get(t, "http://"+srv.metrics+"/readyz"); code != 200 {
		t.Fatalf("metrics readyz before: %d", code)
	}
	srv.stop()
	stopped := time.Now()
	deadline := time.Now().Add(time.Second)
	for {
		code, body, _ := get(t, srv.base+"/readyz")
		if code == http.StatusServiceUnavailable && strings.Contains(body, "draining") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readyz did not flip: %d %s", code, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Still serving: liveness and the API answer during the delay.
	if code, _, _ := get(t, srv.base+"/healthz"); code != 200 {
		t.Errorf("healthz while draining: %d", code)
	}
	if code, _, _ := get(t, srv.base+"/v1/signup"); code != 200 {
		t.Errorf("API while draining: %d", code)
	}
	_, metrics, _ := get(t, "http://"+srv.metrics+"/metrics")
	if !strings.Contains(metrics, "taskiem_draining 1") {
		t.Error("taskiem_draining is not 1")
	}
	if err := srv.wait(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(stopped); took < 1400*time.Millisecond || took > 20*time.Second {
		t.Errorf("stopped after %s", took)
	}
	if code, _, _ := get(t, srv.base+"/healthz"); code != 0 {
		t.Errorf("still listening: %d", code)
	}
}

func TestReliabilityConfig(t *testing.T) {
	t.Setenv("TASKIEM_SHUTDOWN_DELAY", "5m")
	if _, err := shutdownConfig(); err == nil {
		t.Error("a delay beyond 2m was taken")
	}
	t.Setenv("TASKIEM_SHUTDOWN_DELAY", "10s")
	t.Setenv("TASKIEM_WORKER_DRAIN", "45s")
	if c, err := shutdownConfig(); err != nil || c.Delay != 10*time.Second || c.WorkerDrain != 45*time.Second {
		t.Errorf("%+v %v", c, err)
	}
	t.Setenv("TASKIEM_CANARY_HOOK_URL", "https://hooks.example/hooks/t/taskiem-canary")
	if _, err := canaryConfig(); err == nil {
		t.Error("canary without its secret and key")
	}
	t.Setenv("TASKIEM_CANARY_SECRET", "s")
	t.Setenv("TASKIEM_CANARY_API_KEY", "k")
	t.Setenv("TASKIEM_CANARY_API_URL", "https://api.example")
	if c, err := canaryConfig(); err != nil || !c.On() || c.Interval != time.Minute {
		t.Errorf("%+v %v", c, err)
	}
	t.Setenv("TASKIEM_STATUS_TOKENS", "ops:nothex")
	if _, err := statusConfig(); err == nil {
		t.Error("a malformed operator token was taken")
	}
	_ = context.Background()
}
