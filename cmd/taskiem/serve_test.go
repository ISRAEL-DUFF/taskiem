package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// server is a running `taskiem serve --role all` on a fresh database.
type server struct {
	base, metrics string
}

// startServer bootstraps a tenant (admin@smoke.test) and serves every role
// until the test ends.
func startServer(t *testing.T) server {
	t.Helper()
	d := dbtest.New(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	apiAddr, metricsAddr := freeAddr(t), freeAddr(t)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_LOCAL_KMS_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("TASKIEM_LISTEN", apiAddr)
	t.Setenv("TASKIEM_METRICS_LISTEN", metricsAddr)
	t.Setenv("TASKIEM_SECURE_COOKIES", "false")
	t.Setenv("TASKIEM_ARCHIVE_DIR", t.TempDir())
	t.Setenv("TASKIEM_BOOTSTRAP_PASSWORD", "correct horse battery")
	var out bytes.Buffer
	if err := run([]string{"bootstrap", "--tenant", "Smoke", "--email", "admin@smoke.test"}, &out, &out); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, []string{"--role", "all"}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(40 * time.Second):
			t.Error("serve did not stop")
		}
	})
	base := "http://" + apiAddr
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return server{base: base, metrics: metricsAddr}
}

// call makes an API request and decodes the JSON answer; it fails the test
// on an error status.
func (s server) call(t *testing.T, method, path, token string, body any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, s.base+path, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	return m
}

// TestServeAllSmoke runs the binary's own entry points end to end: bootstrap,
// serve --role all, a webhook-started run through the sandbox worker,
// metrics, an audit export verified offline, and a graceful stop.
func TestServeAllSmoke(t *testing.T) {
	srv := startServer(t)
	base, metricsAddr := srv.base, srv.metrics
	var out bytes.Buffer
	call := func(method, path, token string, body any) map[string]any {
		t.Helper()
		return srv.call(t, method, path, token, body)
	}
	login := call("POST", "/v1/auth/login", "", map[string]any{"email": "admin@smoke.test", "password": "correct horse battery"})
	tok, tenant := login["token"].(string), login["tenant_id"].(string)
	wf := call("POST", "/v1/workflows", tok, map[string]any{"name": "smoke", "definition": json.RawMessage(`{"schema":"wd/v1","id":"wf_smoke","version":1,"name":"smoke",
	  "trigger":{"type":"webhook","config":{"path":"/smoke","auth":"none"}},
	  "steps":[{"id":"double","type":"code","config":{"language":"typescript","source":"export default (input: {n: number}) => ({ n: input.n * 2 })"},"input":{"n":"=trigger.body.n"}},
	           {"id":"out","type":"transform","needs":["double"],"config":{"output":"=steps.double.output.n"}}]}`)})["id"].(string)
	call("POST", "/v1/workflows/"+wf+"/versions/1/publish", tok, nil)
	runID := call("POST", "/hooks/"+tenant+"/smoke", "", map[string]any{"n": 21})["run_id"].(string)

	var got map[string]any
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		got = call("GET", "/v1/runs/"+runID, tok, nil)
		if got["run"].(map[string]any)["status"] == "completed" {
			break
		}
	}
	if st := got["run"].(map[string]any)["status"]; st != "completed" || !strings.Contains(toJSON(got["events"]), `"output":42`) {
		t.Fatalf("run %v: %s", st, toJSON(got["events"]))
	}

	resp, err := http.Get("http://" + metricsAddr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, m := range []string{`taskiem_steps_total{outcome="completed",queue="sandbox",target="code"}`, `taskiem_ingest_deliveries_total{kind="webhook",result="202"}`, `taskiem_http_request_duration_seconds_bucket{route="/v1/runs/{run}"`} {
		if !strings.Contains(string(metrics), m) {
			t.Errorf("metrics lack %s", m)
		}
	}

	req, _ := http.NewRequest("GET", base+"/v1/audit/export", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	export, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	f := filepath.Join(t.TempDir(), "audit.jsonl")
	_ = os.WriteFile(f, export, 0o600)
	out.Reset()
	if err := run([]string{"audit", "verify", f}, &out, &out); err != nil || !strings.Contains(out.String(), "intact") {
		t.Errorf("audit verify: %v %s", err, out.String())
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestLogsAreRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: redactAttr}))
	log.Error("provider said account holder ada@example.ng has BVN 22212345678", "err", errors.New("rejected +2348031234567"), "amount", 5000)
	out := buf.String()
	for _, leak := range []string{"ada@example.ng", "22212345678", "+2348031234567"} {
		if strings.Contains(out, leak) {
			t.Errorf("%s leaked: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"amount":5000`) {
		t.Errorf("numbers are not personal: %s", out)
	}
}
