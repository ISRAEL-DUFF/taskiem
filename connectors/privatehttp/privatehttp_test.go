package privatehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/tunnel"
	"github.com/israel-duff/taskiem/engine/tunnel/tunneltest"
)

// world is a "private network": a service on loopback that the guard would
// never let a worker dial, and a bastion in front of it.
type world struct {
	srv   *httptest.Server
	b     *tunneltest.Bastion
	creds map[string]string
	dial  func(context.Context, string, string) (net.Conn, error)
}

func newWorld(t *testing.T, h http.Handler, tls bool) *world {
	t.Helper()
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	b, key := tunneltest.Start(t, addr)
	host, port, _ := net.SplitHostPort(addr)
	bh, bp := b.HostPort()
	creds := map[string]string{
		"host": host, "port": port, "scheme": "http",
		"tunnel_host": bh, "tunnel_port": bp, "tunnel_user": "taskiem", "tunnel_private_key": key, "tunnel_host_key": b.Pin(),
	}
	if tls {
		creds["scheme"] = "https"
	}
	g := &egress.Guard{Loopback: map[string]string{"tunnel:privatehttp": bp}}
	pol := egress.Policy{Tenant: "t", Hosts: []string{host}, Purpose: "connector:privatehttp"}
	d, err := tunnel.Dialer(g, pol, creds)
	if err != nil {
		t.Fatal(err)
	}
	return &world{srv: srv, b: b, creds: creds, dial: d}
}

func (w *world) call(t *testing.T, action string, in map[string]any) (map[string]any, error) {
	t.Helper()
	c := New()
	resp, err := c.Actions[action].Execute(context.Background(), connector.Request{Input: in, Credentials: w.creds, Dial: w.dial, Attempt: 1})
	if err != nil {
		return nil, err
	}
	return resp.Output.(map[string]any), nil
}

func TestGetThroughTheTunnel(t *testing.T) {
	var gotAuth, gotPath string
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.RequestURI()
		rw.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(rw, `{"name":"Ada","plan":"10mbps"}`)
	}), false)
	w.creds["token"] = "s3cret"
	out, err := w.call(t, "get", map[string]any{"path": "/rest/user", "query": map[string]any{"name": "Ada"}})
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != 200 || out["ok"] != true || out["body"].(map[string]any)["plan"] != "10mbps" {
		t.Fatalf("output %v", out)
	}
	if gotAuth != "Bearer s3cret" || gotPath != "/rest/user?name=Ada" {
		t.Fatalf("auth %q path %q", gotAuth, gotPath)
	}
	if ch := w.b.Channels(); len(ch) != 1 || ch[0] != "direct-tcpip" {
		t.Fatalf("channels %v", ch)
	}
}

func TestSendAndHowAnswersAreSorted(t *testing.T) {
	var method, ct, body string
	status := 200
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, ct, body = r.Method, r.Header.Get("Content-Type"), string(b)
		rw.WriteHeader(status)
		_, _ = io.WriteString(rw, "done")
	}), false)
	w.creds["username"], w.creds["password"] = "admin", "pw"
	out, err := w.call(t, "send", map[string]any{"method": "PUT", "path": "/rest/ppp/secret/Ada", "body": map[string]any{"disabled": true}})
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(body), &sent)
	if method != "PUT" || ct != "application/json" || sent["disabled"] != true || out["ok"] != true || out["body"] != "done" {
		t.Fatalf("method %s ct %s body %s out %v", method, ct, body, out)
	}
	// A 4xx is an answer, to branch on.
	status = 404
	out, err = w.call(t, "send", map[string]any{"method": "DELETE", "path": "/rest/ppp/secret/Nobody"})
	if err != nil || out["status"] != 404 || out["ok"] != false {
		t.Fatalf("404: %v %v", out, err)
	}
	// A 5xx on a write may have applied: the step parks.
	status = 500
	if _, err = w.call(t, "send", map[string]any{"method": "POST", "path": "/x"}); !errors.Is(err, effects.ErrUnknownOutcome) {
		t.Fatalf("500 on a write: %v", err)
	}
	// On a read it is retried.
	if _, err = w.call(t, "get", map[string]any{"path": "/x"}); !errors.Is(err, effects.ErrRetryable) {
		t.Fatalf("500 on a read: %v", err)
	}
	// Told to slow down: nothing was done, safe to send again.
	status = 429
	if _, err = w.call(t, "send", map[string]any{"method": "POST", "path": "/x"}); !errors.Is(err, effects.ErrNotSent) {
		t.Fatalf("429: %v", err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.Redirect(rw, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}), false)
	out, err := w.call(t, "get", map[string]any{"path": "/"})
	if err != nil || out["status"] != 302 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestSelfSignedCertificateNeedsAnExplicitYes(t *testing.T) {
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(rw, "inside") }), true)
	if _, err := w.call(t, "get", map[string]any{}); err == nil {
		t.Fatal("a self-signed certificate was accepted without being told to")
	}
	w.creds["skip_tls_verify"] = "yes"
	out, err := w.call(t, "get", map[string]any{})
	if err != nil || out["body"] != "inside" {
		t.Fatalf("%v %v", out, err)
	}
}

func TestOnlyTheEquipmentIsReachable(t *testing.T) {
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {}), false)
	for _, p := range []string{"http://evil.example/", "//evil.example/x", "relative", "/ok\r\nX: y"} {
		if _, err := w.call(t, "get", map[string]any{"path": p}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("path %q: want a fatal refusal, got %v", p, err)
		}
	}
	for _, h := range []string{"Host", "authorization", "Content-Length", "X-A\r\nB"} {
		if _, err := w.call(t, "get", map[string]any{"path": "/", "headers": map[string]any{h: "x"}}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("header %q: want a fatal refusal, got %v", h, err)
		}
	}
	w.creds["host"] = "evil.example/x"
	if _, err := w.call(t, "get", map[string]any{}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("host with a path: %v", err)
	}
	if len(w.b.Dials()) != 0 {
		t.Fatalf("the bastion was asked for %v", w.b.Dials())
	}
}

func TestWithoutATunnelItRefuses(t *testing.T) {
	c := New()
	_, err := c.Actions["get"].Execute(context.Background(), connector.Request{
		Input: map[string]any{}, Credentials: map[string]string{"host": "10.0.0.1"},
		Dial: func(context.Context, string, string) (net.Conn, error) { t.Fatal("dialled"); return nil, nil },
	})
	if !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "no tunnel") {
		t.Fatalf("got %v", err)
	}
}

func TestLargeAnswersAreCut(t *testing.T) {
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(rw, strings.Repeat("a", 5000))
	}), false)
	out, err := w.call(t, "get", map[string]any{"max_bytes": 1000})
	if err != nil || out["truncated"] != true || len(out["body"].(string)) != 1000 {
		t.Fatalf("%v %v", out, err)
	}
}
