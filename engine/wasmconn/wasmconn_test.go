package wasmconn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

// The example connector, built once for every test, and one runtime: a
// module is compiled once per runtime.
var (
	exampleWasm, exampleManifest []byte
	shared                       *Runtime
)

func TestMain(m *testing.M) {
	var err error
	if shared, err = New(context.Background(), Limits{}); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func example(t *testing.T) {
	t.Helper()
	exampleManifest, exampleWasm = wasmtest.Example(t)
}

func load(t *testing.T, base string) *connector.Connector {
	t.Helper()
	example(t)
	c, err := shared.Load(context.Background(), exampleManifest, exampleWasm)
	if err != nil {
		t.Fatal(err)
	}
	c.Manifest.OverrideBaseURL(base)
	return c
}

// ledger is a fake provider: payments by reference, 409 on a repeat.
func ledger(t *testing.T, fail func(w http.ResponseWriter, r *http.Request) bool) (*httptest.Server, *atomic.Int32) {
	payments := map[string]bool{}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if fail != nil && fail(w, r) {
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/balance":
			_, _ = w.Write([]byte(`{"available":125000,"currency":"NGN"}`))
		case r.Method == "POST" && r.URL.Path == "/v1/payments":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			ref, _ := b["reference"].(string)
			if payments[ref] {
				w.WriteHeader(http.StatusConflict)
				return
			}
			payments[ref] = true
			_, _ = w.Write([]byte(`{"id":"pay_1","reference":"` + ref + `","status":"pending"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/payments":
			ref := r.URL.Query().Get("reference")
			if !payments[ref] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"id":"pay_1","reference":"` + ref + `","status":"completed"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func exec1(t *testing.T, c *connector.Connector, srv *httptest.Server, action string, in map[string]any) (connector.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.Actions[action].Execute(ctx, connector.Request{Input: in, Credentials: map[string]string{"api_key": "k"}, HTTP: srv.Client(), Attempt: 1})
}

func TestExampleConnectorEndToEnd(t *testing.T) {
	srv, _ := ledger(t, nil)
	c := load(t, srv.URL)
	r, err := exec1(t, c, srv, "get_balance", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["available"] != float64(125000) || out["currency"] != "NGN" {
		t.Errorf("balance %v", out)
	}
	in := map[string]any{"amount": 5000, "account_number": "0123456789", "reference": "tsk_abc"}
	r, err = exec1(t, c, srv, "create_payment", in)
	if err != nil || r.Output.(map[string]any)["status"] != "pending" {
		t.Fatalf("pay: %v %v", r.Output, err)
	}
	// A repeat under the same reference reports the payment already made.
	r, err = exec1(t, c, srv, "create_payment", in)
	if err != nil || r.Output.(map[string]any)["status"] != "completed" {
		t.Fatalf("repeat: %v %v", r.Output, err)
	}
	// Reconcile: an unknown reference is not found, so the engine may resend.
	if _, err := exec1(t, c, srv, "get_payment", map[string]any{"reference": "tsk_never"}); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestErrorsAreClassified(t *testing.T) {
	for status, want := range map[int]effects.ErrorKind{
		http.StatusTooManyRequests:     effects.KindRetryable,
		http.StatusInternalServerError: effects.KindUnknownOutcome,
		http.StatusBadRequest:          effects.KindFatal,
	} {
		srv, _ := ledger(t, func(w http.ResponseWriter, _ *http.Request) bool { w.WriteHeader(status); return true })
		c := load(t, srv.URL)
		_, err := exec1(t, c, srv, "create_payment", map[string]any{"amount": 1, "account_number": "1", "reference": "r"})
		if got := effects.Classify(err); got != want {
			t.Errorf("%d: %v, want %v", status, got, want)
		}
	}
}

func TestTransportFailures(t *testing.T) {
	// Refused connection: provably not sent.
	srv, _ := ledger(t, nil)
	c := load(t, "http://127.0.0.1:1")
	_, err := exec1(t, c, srv, "create_payment", map[string]any{"amount": 1, "account_number": "1", "reference": "r"})
	if effects.Classify(err) != effects.KindNotSent {
		t.Errorf("refused: %v", err)
	}
	// No network at all.
	c = load(t, srv.URL)
	_, err = c.Actions["get_balance"].Execute(context.Background(), connector.Request{Credentials: map[string]string{"api_key": "k"}})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no network: %v", err)
	}
}

func TestDeadlineAfterSendIsUnknownOutcome(t *testing.T) {
	release := make(chan struct{})
	srv, _ := ledger(t, func(_ http.ResponseWriter, _ *http.Request) bool {
		<-release // the provider has the request and never answers
		return true
	})
	t.Cleanup(func() { close(release) }) // runs before the server closes
	c := load(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := c.Actions["create_payment"].Execute(ctx, connector.Request{Input: map[string]any{"amount": 1, "account_number": "1", "reference": "r"},
		Credentials: map[string]string{"api_key": "k"}, HTTP: srv.Client()})
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("timeout after send: %v", err)
	}
}

func TestLoadRefusesBadConnectors(t *testing.T) {
	example(t)
	rt, ctx := shared, context.Background()
	builtinID := strings.Replace(string(exampleManifest), "id: x_example_ledger", "id: example_ledger", 1)
	unverified := strings.TrimRight(string(exampleManifest), "\n") + "\ntriggers:\n  paid:\n    type: webhook\n    verify: { scheme: none }\n"
	// Taskiem's platform OAuth apps are for its built-ins: a tenant's
	// connector naming one would have the app's secret sent to its own
	// token URL, or copied into its credentials (F1).
	apiKey := "auth:\n  type: api_key\n  fields:\n    - { key: api_key, label: API key, secret: true }\n"
	oauth := func(extra, modes string) []byte {
		return []byte(strings.Replace(string(exampleManifest), apiKey, "auth:\n  type: oauth2\n  fields:\n    - { key: api_key, label: API key, secret: true }\n    - { key: stolen, label: S, secret: true }\n"+
			"  oauth2:\n    authorize_url: https://evil.example/authorize\n    token_url: https://evil.example/token\n    pkce: true\n    scopes: [x]\n"+
			"    fields_from_token: { api_key: access_token }\n"+extra+modes, 1))
	}
	platformApp := oauth("    client: slack\n    platform_client_fields: { stolen: id }\n", "")
	platformFields := oauth("    platform_client_fields: { stolen: secret }\n", "")
	modeApp := oauth("    fields_from_callback: { stolen: realmId }\n", "  modes:\n    - { mode: live, label: Live, base_url: https://ledger.example.com }\n    - { mode: test, label: Test, base_url: https://sandbox.ledger.example.com, client: quickbooks_sandbox }\n")
	for name, tc := range map[string]struct {
		manifest, module []byte
		want             string
	}{
		"built-in id":         {[]byte(builtinID), exampleWasm, "must start with"},
		"unverified trigger":  {[]byte(unverified), exampleWasm, `trigger "paid" must verify`},
		"not wasm":            {exampleManifest, []byte("hello"), "does not compile"},
		"no export":           {exampleManifest, emptyModule, "must export taskiem_execute_v1"},
		"bad manifest":        {[]byte("manifest: connector/v1\nid: x_a\n"), exampleWasm, "manifest"},
		"foreign import":      {exampleManifest, importsEnv, "does not provide"},
		"platform app":        {platformApp, exampleWasm, "auth.oauth2.client"},
		"platform fields":     {platformFields, exampleWasm, "platform_client_fields"},
		"mode's platform app": {modeApp, exampleWasm, "auth.modes[1].client"},
	} {
		if _, err := rt.Load(ctx, tc.manifest, tc.module); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// emptyModule is (module).
var emptyModule = []byte{0, 'a', 's', 'm', 1, 0, 0, 0}

// importsEnv is (module (import "env" "f" (func))).
var importsEnv = []byte{0, 'a', 's', 'm', 1, 0, 0, 0,
	1, 4, 1, 0x60, 0, 0, // type section: one func type () -> ()
	2, 9, 1, 3, 'e', 'n', 'v', 1, 'f', 0, 0, // import section: env.f, func type 0
}

func TestInstancesShareNothing(t *testing.T) {
	srv, calls := ledger(t, nil)
	c := load(t, srv.URL)
	for range 3 {
		if _, err := exec1(t, c, srv, "get_balance", nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Errorf("calls %d", calls.Load())
	}
}

func TestMemoryLimit(t *testing.T) {
	example(t)
	rt, err := New(context.Background(), Limits{MemoryPages: 16}) // 1 MiB: less than the module asks for
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	if _, err := rt.Load(context.Background(), exampleManifest, exampleWasm); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "over limit") {
		t.Errorf("over the memory cap: %v", err)
	}
}
