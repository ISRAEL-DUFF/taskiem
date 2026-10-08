package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/container"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const image = "registry.example.com/tools/pdf@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func containerStep(extra string) string {
	return `{"id":"render","type":"container","input":{"html":"=trigger.html"},"config":{"image":"` + image + `","command":["/bin/render"]` + extra + `}}`
}

func enableContainers(t *testing.T, e *rt.Env, minutes int) {
	t.Helper()
	if err := e.Store.SetLimits(ctx, e.Tenant, map[string]any{"container_minutes_monthly": minutes}, "op"); err != nil {
		t.Fatal(err)
	}
}

func completedOutput(t *testing.T, h []history.Event, step string) (map[string]any, []string) {
	t.Helper()
	for _, ev := range h {
		if ev.Type == history.StepCompleted && ev.StepID == step {
			var p struct {
				Output map[string]any `json:"output"`
				Logs   []string       `json:"logs"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			return p.Output, p.Logs
		}
	}
	t.Fatalf("no StepCompleted for %s: %s", step, types(h))
	return nil, nil
}

func containerFailure(h []history.Event, step string) history.Error {
	var last history.Error
	for _, ev := range h {
		if ev.Type == history.StepFailed && ev.StepID == step {
			var fp history.FailedPayload
			_ = json.Unmarshal(ev.Payload, &fp)
			last = fp.Error
		}
	}
	return last
}

// A container step runs on the runner with its input, declared secrets
// (read through the vault and audited), clamped limits and an idempotency
// key; secrets are scrubbed from what it returns and logs; its time counts
// against the plan's minutes.
func TestContainerStepEndToEnd(t *testing.T) {
	e := rt.New(t)
	e.VaultSecrets = true
	if _, err := e.Vault.Put(ctx, e.Tenant, "prod", "api_key", []byte("sk_container_51f0"), "admin"); err != nil {
		t.Fatal(err)
	}
	enableContainers(t, e, 10)
	fake := &container.Fake{Handle: func(_ context.Context, s container.Spec) (container.Result, error) {
		var in map[string]any
		_ = json.Unmarshal(s.Input, &in)
		out, _ := json.Marshal(map[string]any{"html": in["html"], "echo": "key=" + s.Secrets["api_key"], "pages": 3})
		return container.Result{Output: out, Logs: "using sk_container_51f0\nrendered", Elapsed: 61500 * time.Millisecond}, nil
	}}
	e.Containers = fake
	wf := e.Publish(t, wfDoc(containerStep(`,"secrets":["api_key"],"class":"idempotent_write","limits":{"cpu":"2","memory_mb":2048,"timeout":"10m"}`), ""))
	ref := e.Start(t, wf, map[string]any{"html": "<p>hi</p>"})
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s %+v", st, types(h), containerFailure(h, "render"))
	}
	out, logs := completedOutput(t, h, "render")
	if out["html"] != "<p>hi</p>" || out["echo"] != "key=[secret]" || out["pages"] != float64(3) {
		t.Fatalf("output %v", out)
	}
	if strings.Join(logs, "|") != "using [secret]|rendered" {
		t.Fatalf("logs %q", logs)
	}
	specs := fake.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d runs", len(specs))
	}
	s := specs[0]
	if s.Image != image || s.Command[0] != "/bin/render" || s.CPUMillis != 2000 || s.MemoryMB != 2048 || s.Timeout != 10*time.Minute ||
		s.OutputBytes != 256<<10 || s.Proxy != "" || s.Tenant != e.Tenant.String() || s.Attempt != 1 {
		t.Fatalf("spec %+v", s)
	}
	if !strings.HasPrefix(s.Env["TASKIEM_IDEMPOTENCY_KEY"], "tsk_") || count(h, history.EffectIntent, "render") != 1 {
		t.Fatalf("idempotent write: key %q, %s", s.Env["TASKIEM_IDEMPOTENCY_KEY"], types(h))
	}
	// The secret read is audited for this step and attempt.
	var reads int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM secret_reads WHERE run_id = $1 AND step_id = 'render' AND purpose = 'step.container' AND name = 'api_key'`, ref.ID).Scan(&reads); err != nil || reads != 1 {
		t.Fatalf("secret reads %d %v", reads, err)
	}
	var secs, runs int64
	if err := e.DB.Admin.QueryRow(ctx, `SELECT seconds, runs FROM container_usage WHERE tenant_id = $1`, e.Tenant).Scan(&secs, &runs); err != nil || secs != 62 || runs != 1 {
		t.Fatalf("usage %d s, %d runs, %v", secs, runs, err)
	}
	view, err := e.Store.ViewLimits(ctx, e.Tenant)
	if err != nil || view.Usage.ContainerSeconds != 62 || view.Limits.ContainerMinutesMonthly != 10 {
		t.Fatalf("limits view %+v %v", view.Usage, err)
	}
}

// Container steps are off unless the plan has minutes, and stop when the
// month's minutes are used up.
func TestContainerStepPlanLimits(t *testing.T) {
	e := rt.New(t)
	e.Containers = &container.Fake{}
	wf := e.Publish(t, wfDoc(containerStep(`,"class":"read"`), ""))
	ref := e.Start(t, wf, map[string]any{"html": "x"})
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "failed" || !strings.Contains(containerFailure(h, "render").Message, "not part of this plan") {
		t.Fatalf("off: %s %+v", st, containerFailure(h, "render"))
	}

	e2 := rt.New(t)
	e2.Containers = &container.Fake{}
	enableContainers(t, e2, 1)
	if _, err := e2.DB.Admin.Exec(ctx, `INSERT INTO container_usage (tenant_id, month, seconds, runs) VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, 60, 1)`, e2.Tenant); err != nil {
		t.Fatal(err)
	}
	wf = e2.Publish(t, wfDoc(containerStep(`,"class":"read"`), ""))
	ref = e2.Start(t, wf, map[string]any{"html": "x"})
	e2.Drain(t)
	h = events(t, e2, ref)
	if st := e2.Status(t, ref); st != "failed" || !strings.Contains(containerFailure(h, "render").Message, "used up") {
		t.Fatalf("used up: %s %+v", st, containerFailure(h, "render"))
	}
	if n := len(e2.Containers.(*container.Fake).Specs()); n != 0 {
		t.Fatalf("ran %d times beyond the plan", n)
	}
	var refused int64
	if err := e2.DB.Admin.QueryRow(ctx, `SELECT refused FROM container_usage WHERE tenant_id = $1`, e2.Tenant).Scan(&refused); err != nil || refused != 1 {
		t.Fatalf("refused %d %v", refused, err)
	}

	// No runner on the platform: the step fails, it does not hang.
	e3 := rt.New(t)
	enableContainers(t, e3, 10)
	wf = e3.Publish(t, wfDoc(containerStep(``), ""))
	ref = e3.Start(t, wf, map[string]any{"html": "x"})
	e3.Drain(t)
	if h := events(t, e3, ref); !strings.Contains(containerFailure(h, "render").Message, "not enabled on this platform") {
		t.Fatalf("no runner: %+v", containerFailure(h, "render"))
	}
}

// Failures follow the step's effect class: a program that never started
// is retried even for an unsafe write; one that may have done part of its
// work parks an unsafe write and is retried for a read.
func TestContainerStepEffectClasses(t *testing.T) {
	e := rt.New(t)
	enableContainers(t, e, 10)
	var calls atomic.Int32
	e.Containers = &container.Fake{Handle: func(_ context.Context, s container.Spec) (container.Result, error) {
		switch n := calls.Add(1); {
		case n == 1:
			return container.Result{}, container.NotStarted("ImagePullBackOff")
		case strings.Contains(string(s.Input), "unsafe"):
			return container.Result{Elapsed: time.Second}, container.TimedOut(5 * time.Minute)
		}
		return container.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
	}}
	retry := `,"retry":{"max":3,"initial":"10ms","backoff":"fixed"}`
	doc := strings.Replace(containerStep(``), `"config"`, strings.TrimPrefix(retry, ",")+`,"config"`, 1)
	wf := e.Publish(t, wfDoc(doc, ""))

	ref := e.Start(t, wf, map[string]any{"html": "unsafe"})
	rt.WaitFor(t, 10*time.Second, "run to settle", func() bool {
		e.Drain(t)
		st := e.Status(t, ref)
		return st != "running"
	})
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "needs_reconciliation" {
		t.Fatalf("unsafe write after a timeout: %s %s", st, types(h))
	}
	if f := containerFailure(h, "render"); !f.MaybeApplied || f.Next != "park" {
		t.Fatalf("failure %+v", f)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d calls: not-started should be retried once, the timeout never", calls.Load())
	}

	calls.Store(0)
	wf = e.Publish(t, wfDoc(strings.Replace(doc, `"command"`, `"class":"read","command"`, 1), ""))
	ref = e.Start(t, wf, map[string]any{"html": "read"})
	rt.WaitFor(t, 10*time.Second, "run to settle", func() bool {
		e.Drain(t)
		return e.Status(t, ref) != "running"
	})
	if st := e.Status(t, ref); st != "completed" || calls.Load() != 2 {
		t.Fatalf("read: %s after %d calls", st, calls.Load())
	}
}

// Cancelling the run stops the container (the runner's context ends, so it
// deletes the Pod) while the step runs.
func TestContainerStepCancelled(t *testing.T) {
	e := rt.New(t)
	enableContainers(t, e, 10)
	started := make(chan struct{})
	stopped := make(chan error, 1)
	e.Containers = &container.Fake{Handle: func(ctx context.Context, _ container.Spec) (container.Result, error) {
		close(started)
		select {
		case <-ctx.Done():
			stopped <- ctx.Err()
		case <-time.After(20 * time.Second):
			stopped <- fmt.Errorf("not cancelled")
		}
		return container.Result{Elapsed: 2 * time.Second}, container.TimedOut(0)
	}}
	wf := e.Publish(t, wfDoc(containerStep(`,"class":"read"`), ""))
	ref := e.Start(t, wf, map[string]any{"html": "x"})
	w := e.ContainerWorker("cw")
	w.CancelPoll = 20 * time.Millisecond
	done := make(chan struct{})
	go func() { defer close(done); _, _ = w.RunOnce(ctx) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("container did not start")
	}
	if err := e.Store.CancelRun(ctx, ref, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err == nil || err.Error() == "not cancelled" {
		t.Fatalf("runner context: %v", err)
	}
	<-done
	if st := e.Status(t, ref); st != "cancelled" {
		t.Fatalf("status %s", st)
	}
}

// With network "egress", the container gets the proxy and a token that
// reaches only hosts on both the step's list and the environment's.
func TestContainerStepEgressThroughProxy(t *testing.T) {
	e := rt.New(t)
	enableContainers(t, e, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`"pong"`)) }))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	e.Proxy = &egress.Proxy{Guard: e.Egress, Ports: []string{port}}
	ps := httptest.NewServer(e.Proxy)
	defer ps.Close()
	e.ProxyAddr = strings.TrimPrefix(ps.URL, "http://")
	var proxyURL string
	e.Containers = &container.Fake{Handle: func(_ context.Context, s container.Spec) (container.Result, error) {
		proxyURL = s.Proxy
		pu, err := url.Parse(s.Proxy)
		if err != nil {
			return container.Result{}, err
		}
		c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 5 * time.Second}
		get := func(target string) string {
			resp, err := c.Get(target)
			if err != nil {
				return "error"
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				return fmt.Sprint(resp.StatusCode)
			}
			return string(b)
		}
		out, _ := json.Marshal(map[string]string{"allowed": get(upstream.URL), "other": get("http://localhost:" + port + "/"), "token": pu.User.String()})
		return container.Result{Output: out}, nil
	}}
	wf := e.Publish(t, wfDoc(containerStep(`,"class":"read","network":"egress","hosts":["127.0.0.1","localhost"]`), ""))
	ref := e.Start(t, wf, map[string]any{"html": "x"})
	e.Drain(t)
	h := events(t, e, ref)
	out, _ := completedOutput(t, h, "render")
	if out["allowed"] != `"pong"` || out["other"] != "403" {
		t.Fatalf("through the proxy: %v", out)
	}
	if !strings.HasPrefix(proxyURL, "http://taskiem:") || !strings.Contains(out["token"].(string), "[secret]") {
		t.Fatalf("proxy %q, token in output %q (should be scrubbed)", proxyURL, out["token"])
	}
	// The grant ends with the step.
	pu, _ := url.Parse(proxyURL)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 5 * time.Second}
	if resp, err := c.Get(upstream.URL); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusProxyAuthRequired {
			t.Fatalf("token still valid after the step: %d", resp.StatusCode)
		}
	}
}
