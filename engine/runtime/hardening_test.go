package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// leaks reports every history event that repeats one of the values.
func leaks(t *testing.T, h []history.Event, values ...string) {
	t.Helper()
	for _, ev := range h {
		for _, v := range values {
			if strings.Contains(string(ev.Payload), v) {
				t.Errorf("%s(%s) repeats a secret: %s", ev.Type, ev.StepID, ev.Payload)
			}
		}
	}
}

// failure returns the message of a step's last failure.
func failure(h []history.Event, step string) string {
	msg := ""
	for _, ev := range h {
		if ev.Type == history.StepFailed && ev.StepID == step {
			var fp history.FailedPayload
			_ = json.Unmarshal(ev.Payload, &fp)
			msg = fp.Error.Message
		}
	}
	return msg
}

// A transport error names the URL; secrets put in its path or query never
// reach history.
func TestHTTPStepTransportErrorKeepsSecretsOut(t *testing.T) {
	e := rt.New(t)
	e.Secrets["path_token"] = "ptok-7a9b3c"
	e.Secrets["query_key"] = "qkey-55d1e0"
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"get","type":"http","retry":{"max":0},"config":{"method":"GET",
	  "url":"='http://127.0.0.1:1/v1/' + secrets.path_token + '/balance'","query":{"api_key":"=secrets.query_key"}}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "failed" {
		t.Fatalf("status %s", st)
	}
	h := events(t, e, ref)
	leaks(t, h, "ptok-7a9b3c", "qkey-55d1e0", "api_key=")
	if msg := failure(h, "get"); !strings.Contains(msg, "http://127.0.0.1:1/v1/[secret]/balance") {
		t.Errorf("failure message: %q", msg)
	}
}

// A connector that repeats its credential in an error does not record it.
func TestConnectorErrorKeepsCredentialsOut(t *testing.T) {
	e := rt.New(t)
	conn := e.Provider.Connector()
	conn.Manifest.ID = "leaky"
	conn.Manifest.Auth.Type = "api_key"
	conn.Manifest.Auth.Fields = []connector.AuthField{{Key: "api_key", Secret: true}, {Key: "account"}}
	conn.Actions["verify"] = connector.ActionFunc(func(_ context.Context, r connector.Request) (connector.Response, error) {
		return connector.Response{}, fmt.Errorf("GET https://leaky.test/check?api_key=%s&account=%s: refused: %w", r.Credentials["api_key"], r.Credentials["account"], effects.ErrFatal)
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "leaky", "main", "api_key", map[string]string{"api_key": "sk_live_8f2c1d", "account": "acct-000123"}, "admin"); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"check","type":"connector","connector":"leaky@1","action":"verify","retry":{"max":0},"input":{"reference":"r1"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	h := events(t, e, ref)
	leaks(t, h, "sk_live_8f2c1d")
	// Fields the manifest does not mark secret stay readable.
	if msg := failure(h, "check"); !strings.Contains(msg, "api_key=[secret]") || !strings.Contains(msg, "acct-000123") {
		t.Errorf("failure message: %q", msg)
	}
}

// A script's secrets are scrubbed from what it logs and throws, and it may
// fetch only so many times.
func TestCodeStepSecretsAndFetchCap(t *testing.T) {
	e := rt.New(t)
	e.Secrets["fx_key"] = "fxk-31415926"
	calls := 0
	fx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fx.Close()
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	leak, _ := json.Marshal(`export default (input: any, host: any) => {
	    console.log("using", host.secret("fx_key"));
	    throw new Error("rejected key " + host.secret("fx_key"));
	  }`)
	wf := e.Publish(t, wfDoc(`{"id":"calc","type":"code","retry":{"max":0},"config":{"language":"typescript","secrets":["fx_key"],"source":`+string(leak)+`}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	h := events(t, e, ref)
	leaks(t, h, "fxk-31415926")
	if msg := failure(h, "calc"); !strings.Contains(msg, "rejected key [secret]") {
		t.Errorf("failure message: %q", msg)
	}

	loop, _ := json.Marshal(`export default async (input: { url: string }, host: any) => {
	    for (let i = 0; i < 60; i++) { await host.fetch(input.url); }
	    return { done: true };
	  }`)
	wf = e.Publish(t, wfDoc(`{"id":"calc","type":"code","retry":{"max":0},"input":{"url":"=trigger.url"},"config":{"language":"typescript","source":`+string(loop)+`}}`, ""))
	ref = e.Start(t, wf, map[string]any{"url": fx.URL})
	e.Drain(t)
	if st := e.Status(t, ref); st != "failed" || calls != 50 || !strings.Contains(failure(events(t, e, ref), "calc"), "more than 50 fetches") {
		t.Errorf("fetch cap: status %s after %d calls: %q", st, calls, failure(events(t, e, ref), "calc"))
	}
}
