package container

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }

const testImage = "registry.example.com/tools/pdf@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeAPI is a Kubernetes API server for the runner: it keeps the Pods and
// Secrets it is sent and moves Pods through phases the test chooses.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	pods     map[string]map[string]any
	secrets  map[string]map[string]any
	deleted  []string
	gets     int
	token    string
	status   func(gets int) map[string]any // the Pod's status on each GET
	log      string
	policies map[string]bool
}

func newFakeAPI(t *testing.T) (*fakeAPI, *Kubernetes) {
	f := &fakeAPI{t: t, pods: map[string]map[string]any{}, secrets: map[string]map[string]any{}, token: "sa-token", policies: map[string]bool{"taskiem-sandbox": true}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "token")
	if err := writeFile(tok, f.token+"\n"); err != nil {
		t.Fatal(err)
	}
	k := &Kubernetes{APIServer: srv.URL, HTTP: srv.Client(), TokenFile: tok, Namespace: "taskiem-sandbox",
		Registries: []string{"registry.example.com/tools"}, ShimImage: "ghcr.io/israel-duff/taskiem:1.0", Poll: 5 * time.Millisecond,
		StartTimeout: time.Second, ImagePullSecrets: []string{"regcred"}, Labels: map[string]string{"taskiem.dev/worker": "w1"}}
	return f, k
}

func terminated(exit int, reason string) map[string]any {
	return map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{"name": "main", "state": map[string]any{"terminated": map[string]any{
		"exitCode": exit, "reason": reason, "startedAt": "2026-10-07T10:00:00Z", "finishedAt": "2026-10-07T10:01:30Z"}}}}}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, `{"message":"forbidden"}`, http.StatusUnauthorized)
		return
	}
	const ns = "/api/v1/namespaces/taskiem-sandbox/"
	p := r.URL.Path
	body, _ := io.ReadAll(r.Body)
	var obj map[string]any
	_ = json.Unmarshal(body, &obj)
	switch {
	case strings.HasPrefix(p, "/apis/networking.k8s.io/v1/namespaces/taskiem-sandbox/networkpolicies/"):
		if !f.policies[strings.TrimPrefix(p, "/apis/networking.k8s.io/v1/namespaces/taskiem-sandbox/networkpolicies/")] {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"kind":"NetworkPolicy"}`))
	case r.Method == http.MethodPost && p == ns+"pods":
		name := obj["metadata"].(map[string]any)["name"].(string)
		f.pods[name] = obj
		_, _ = w.Write([]byte(`{"metadata":{"name":"` + name + `","uid":"uid-1"}}`))
	case r.Method == http.MethodPost && p == ns+"secrets":
		f.secrets[obj["metadata"].(map[string]any)["name"].(string)] = obj
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodDelete:
		f.deleted = append(f.deleted, strings.TrimPrefix(p, ns))
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/log"):
		if r.URL.Query().Get("container") != "main" || r.URL.Query().Get("limitBytes") == "" {
			f.t.Errorf("log query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(f.log))
	case r.Method == http.MethodGet && strings.HasPrefix(p, ns+"pods/"):
		f.gets++
		st := map[string]any{"phase": "Pending"}
		if f.status != nil {
			st = f.status(f.gets)
		}
		raw, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": strings.TrimPrefix(p, ns+"pods/"), "uid": "uid-1"}, "status": st})
		_, _ = w.Write(raw)
	default:
		http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
		f.t.Errorf("unexpected %s %s", r.Method, p)
	}
}

func kubeSpec() Spec {
	return Spec{Name: "abc123-1", Tenant: "11111111-1111-1111-1111-111111111111", Run: "22222222-2222-2222-2222-222222222222",
		Step: "render", Attempt: 1, Image: testImage, Command: []string{"/bin/render"}, Args: []string{"--fast"},
		Input: []byte(`{"html":"<p>"}`), Secrets: map[string]string{"api_key": "sk_live_1"}, Env: map[string]string{"TASKIEM_IDEMPOTENCY_KEY": "tsk_k"},
		CPUMillis: 1500, MemoryMB: 1024, Timeout: 5 * time.Minute, OutputBytes: 4096, Proxy: "http://taskiem:tok@10.0.0.9:3128"}
}

func path(m any, keys ...any) any {
	for _, k := range keys {
		switch k := k.(type) {
		case string:
			mm, _ := m.(map[string]any)
			m = mm[k]
		case int:
			a, _ := m.([]any)
			if k >= len(a) {
				return nil
			}
			m = a[k]
		}
	}
	return m
}

func TestKubernetesPodSpecAndCleanup(t *testing.T) {
	f, k := newFakeAPI(t)
	if err := k.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.status = func(n int) map[string]any {
		if n < 3 {
			return map[string]any{"phase": "Pending"}
		}
		return terminated(0, "Completed")
	}
	f.log = Outcome{Output: json.RawMessage(`{"pages":3}`), Logs: "rendered", Elapsed: time.Hour}.Encode() + "\n"
	res, err := k.Run(context.Background(), kubeSpec())
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"pages":3}` || res.Logs != "rendered" {
		t.Fatalf("result %+v", res)
	}
	// Billed time is the kubelet's, not what the shim claimed.
	if res.Elapsed != 90*time.Second {
		t.Fatalf("elapsed %s", res.Elapsed)
	}

	pod := f.pods["tsk-abc123-1"]
	if pod == nil {
		t.Fatalf("pods: %v", f.pods)
	}
	// Round trip through JSON so the assertions see what the API server saw.
	raw, _ := json.Marshal(pod)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	spec := p["spec"]
	checks := map[string]struct {
		got, want any
	}{
		"runtimeClass":        {path(spec, "runtimeClassName"), "gvisor"},
		"no token":            {path(spec, "automountServiceAccountToken"), false},
		"no service links":    {path(spec, "enableServiceLinks"), false},
		"never restart":       {path(spec, "restartPolicy"), "Never"},
		"deadline":            {path(spec, "activeDeadlineSeconds"), float64(331)},
		"pod non-root":        {path(spec, "securityContext", "runAsNonRoot"), true},
		"pod user":            {path(spec, "securityContext", "runAsUser"), float64(65532)},
		"pod seccomp":         {path(spec, "securityContext", "seccompProfile", "type"), "RuntimeDefault"},
		"dns off":             {path(spec, "dnsPolicy"), "None"},
		"image":               {path(spec, "containers", 0, "image"), testImage},
		"read-only root":      {path(spec, "containers", 0, "securityContext", "readOnlyRootFilesystem"), true},
		"no escalation":       {path(spec, "containers", 0, "securityContext", "allowPrivilegeEscalation"), false},
		"caps dropped":        {path(spec, "containers", 0, "securityContext", "capabilities", "drop", 0), "ALL"},
		"not privileged":      {path(spec, "containers", 0, "securityContext", "privileged"), false},
		"cpu limit":           {path(spec, "containers", 0, "resources", "limits", "cpu"), "1500m"},
		"cpu request":         {path(spec, "containers", 0, "resources", "requests", "cpu"), "1500m"},
		"memory limit":        {path(spec, "containers", 0, "resources", "limits", "memory"), "1024Mi"},
		"shim init image":     {path(spec, "initContainers", 0, "image"), "ghcr.io/israel-duff/taskiem:1.0"},
		"shim init read-only": {path(spec, "initContainers", 0, "securityContext", "readOnlyRootFilesystem"), true},
		"pull secret":         {path(spec, "imagePullSecrets", 0, "name"), "regcred"},
		"sandbox label":       {path(p, "metadata", "labels", SandboxLabel), "true"},
		"tenant label":        {path(p, "metadata", "labels", "taskiem.dev/tenant"), "11111111-1111-1111-1111-111111111111"},
		"run label":           {path(p, "metadata", "labels", "taskiem.dev/run"), "22222222-2222-2222-2222-222222222222"},
		"worker label":        {path(p, "metadata", "labels", "taskiem.dev/worker"), "w1"},
		"step annotation":     {path(p, "metadata", "annotations", "taskiem.dev/step"), "render"},
		"namespace":           {path(p, "metadata", "namespace"), "taskiem-sandbox"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: %v, want %v", name, c.got, c.want)
		}
	}
	cmd, _ := json.Marshal(path(spec, "containers", 0, "command"))
	if !strings.HasPrefix(string(cmd), `["/taskiem/bin/taskiem-shim","run","--input-mode","stdin","--output-mode","stdout","--output-bytes","4096","--timeout","5m0s","--","/bin/render","--fast"]`) {
		t.Errorf("command %s", cmd)
	}
	// Secrets and the proxy credentials come from the Secret, never inline.
	env, _ := json.Marshal(path(spec, "containers", 0, "env"))
	for _, leak := range []string{"sk_live_1", "tok@"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("Pod spec holds %q: %s", leak, env)
		}
	}
	for _, want := range []string{`"name":"api_key","valueFrom":{"secretKeyRef":{"key":"secret.api_key","name":"tsk-abc123-1"}}`, `"name":"HTTPS_PROXY","valueFrom"`, `"name":"TASKIEM_IDEMPOTENCY_KEY","value":"tsk_k"`} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env lacks %s: %s", want, env)
		}
	}
	sec := f.secrets["tsk-abc123-1"]
	if path(sec, "stringData", "secret.api_key") != "sk_live_1" || path(sec, "stringData", "input.json") != `{"html":"<p>"}` ||
		path(sec, "stringData", "proxy") != "http://taskiem:tok@10.0.0.9:3128" || path(sec, "metadata", "ownerReferences", 0, "uid") != "uid-1" {
		t.Errorf("secret %v", sec)
	}
	if strings.Join(f.deleted, ",") != "pods/tsk-abc123-1,secrets/tsk-abc123-1" {
		t.Errorf("deleted %v", f.deleted)
	}
}

func TestKubernetesNetworkNoneHasNoProxy(t *testing.T) {
	_, k := newFakeAPI(t)
	s := kubeSpec()
	s.Proxy = ""
	s.SecretsMode = "file"
	raw, _ := json.Marshal(k.Pod(s))
	if strings.Contains(string(raw), "PROXY") || strings.Contains(string(raw), `"name":"api_key","valueFrom"`) {
		t.Fatalf("pod %s", raw)
	}
	if !strings.Contains(string(raw), `"mountPath":"/taskiem/secrets"`) {
		t.Fatalf("no secrets volume: %s", raw)
	}
}

func TestKubernetesFailures(t *testing.T) {
	cases := map[string]struct {
		status func(int) map[string]any
		log    string
		want   error
		msg    string
	}{
		"image cannot be pulled": {func(int) map[string]any {
			return map[string]any{"phase": "Pending", "containerStatuses": []any{map[string]any{"name": "main", "state": map[string]any{"waiting": map[string]any{"reason": "ImagePullBackOff", "message": "denied"}}}}}
		}, "", effects.ErrNotSent, "ImagePullBackOff"},
		"never scheduled":   {func(int) map[string]any { return map[string]any{"phase": "Pending"} }, "", effects.ErrNotSent, "not running after"},
		"out of memory":     {func(int) map[string]any { st := terminated(137, "OOMKilled"); st["phase"] = "Failed"; return st }, "", effects.ErrUnknownOutcome, "out of memory"},
		"exit status":       {func(int) map[string]any { return terminated(0, "Completed") }, Outcome{Exit: 2, Logs: "bad input"}.Encode(), effects.ErrFatal, "exited with status 2: bad input"},
		"no result":         {func(int) map[string]any { return terminated(1, "Error") }, "segfault\n", effects.ErrUnknownOutcome, "without a result"},
		"output too large":  {func(int) map[string]any { return terminated(0, "Completed") }, Outcome{Error: "output_too_large"}.Encode(), effects.ErrFatal, "larger"},
		"deadline exceeded": {func(int) map[string]any { return map[string]any{"phase": "Failed", "reason": "DeadlineExceeded"} }, "", effects.ErrNotSent, "DeadlineExceeded"},
	}
	for name, c := range cases {
		f, k := newFakeAPI(t)
		f.status, f.log = c.status, c.log
		_, err := k.Run(context.Background(), kubeSpec())
		if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v, want %v with %q", name, err, c.want, c.msg)
		}
		if len(f.deleted) != 2 {
			t.Errorf("%s: deleted %v", name, f.deleted)
		}
	}
}

func TestKubernetesRefusesImagesAndCancels(t *testing.T) {
	f, k := newFakeAPI(t)
	for _, img := range []string{
		"docker.io/library/python@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", // registry not allowed
		"registry.example.com/tools-evil/x@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"registry.example.com/tools/pdf:latest",
		"registry.example.com/tools/pdf:1@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		s := kubeSpec()
		s.Image = img
		if _, err := k.Run(context.Background(), s); !errors.Is(err, ErrRefused) || !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: %v", img, err)
		}
	}
	if len(f.pods) != 0 {
		t.Fatalf("pods created for refused images: %v", f.pods)
	}

	// Cancelling a running step deletes its Pod.
	f.status = func(int) map[string]any {
		return map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "main", "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-07T10:00:00Z"}}}}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := k.Run(ctx, kubeSpec())
	if !errors.Is(err, effects.ErrUnknownOutcome) {
		t.Fatalf("cancelled: %v", err)
	}
	if strings.Join(f.deleted, ",") != "pods/tsk-abc123-1,secrets/tsk-abc123-1" {
		t.Fatalf("deleted %v", f.deleted)
	}

	// No NetworkPolicy: the runner will not start.
	f.policies = map[string]bool{}
	if err := k.Check(context.Background()); err == nil {
		t.Fatal("Check passed without a NetworkPolicy")
	}
}
