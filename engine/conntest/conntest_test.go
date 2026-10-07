package conntest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

// The example connector passes its own suite in the WebAssembly sandbox.
func TestExampleSuitePassesInTheSandbox(t *testing.T) {
	manifest, module := wasmtest.Example(t)
	rt, err := wasmconn.New(context.Background(), wasmconn.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	c, err := rt.Load(context.Background(), manifest, module)
	if err != nil {
		t.Fatal(err)
	}
	suite, err := conntest.LoadDir("../../examples/wasm-connector/testdata")
	if err != nil {
		t.Fatal(err)
	}
	base := c.Manifest.BaseURL
	rep := conntest.Run(context.Background(), c, suite, conntest.Options{})
	if !rep.Passed() || len(rep.Uncovered) > 0 || len(rep.KeyNotSent) > 0 || len(rep.ReadWrites) > 0 {
		t.Fatalf("report: %s", toJSON(rep))
	}
	if c.Manifest.BaseURL != base {
		t.Errorf("base URL not restored: %s", c.Manifest.BaseURL)
	}

	// The same suite, inlined as a package carries it, still passes.
	if rep := conntest.Run(context.Background(), c, suite.Inline(), conntest.Options{}); !rep.Passed() {
		t.Errorf("inline: %s", toJSON(rep))
	}

	// A wrong expectation fails with the reason.
	bad := suite.Inline()
	for i := range bad.Cases {
		if bad.Cases[i].Name == "get_balance" {
			bad.Cases[i].Expect.Output = map[string]any{"available": 1}
		}
	}
	rep = conntest.Run(context.Background(), c, bad, conntest.Options{})
	if rep.Passed() || rep.Failed() != 1 || !strings.Contains(toJSON(rep), "does not match") {
		t.Errorf("wrong expectation: %s", toJSON(rep))
	}
}

const native = `manifest: connector/v1
id: x_native
version: 1.0.0
name: Native
category: other
auth: { type: none }
base_url: https://api.native.example
actions:
  lookup:
    title: Lookup
    class: read
    input: { type: object, properties: {} }
    output: { type: object, properties: { count: { type: integer } } }
  send:
    title: Send
    class: idempotent_write
    idempotency: { field: ref, encoding: hex_lower, length: 32 }
    input: { type: object, properties: { ref: { type: string } } }
    output: { type: object, properties: {} }
  other:
    title: Other
    class: unsafe_write
    input: { type: object, properties: {} }
    output: { type: object, properties: {} }
`

// nativeConnector misbehaves on purpose: lookup reads by POST and returns
// a string where the schema says integer; send never sends its key; other
// calls a host it did not declare.
func nativeConnector(t *testing.T) *connector.Connector {
	m, probs := connector.Parse([]byte(native))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	c := &connector.Connector{Manifest: m, Actions: map[string]connector.Action{}}
	call := func(ctx context.Context, req connector.Request, method, url string) error {
		r, _ := http.NewRequestWithContext(ctx, method, url, nil)
		resp, err := req.HTTP.Do(r)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	c.Actions["lookup"] = connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
		return connector.Response{Output: map[string]any{"count": "seven"}}, call(ctx, req, "POST", m.BaseURL+"/search")
	})
	c.Actions["send"] = connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
		return connector.Response{Output: map[string]any{}}, call(ctx, req, "POST", m.BaseURL+"/send")
	})
	c.Actions["other"] = connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
		if err := call(ctx, req, "POST", "https://elsewhere.example/x"); err != nil {
			return connector.Response{}, fmt.Errorf("%w: %w", err, connector.ErrNotFound)
		}
		return connector.Response{Output: map[string]any{}}, nil
	})
	return c
}

func TestReportsWhatReviewersNeed(t *testing.T) {
	c := nativeConnector(t)
	var suite conntest.Suite
	if err := json.Unmarshal([]byte(`{"cases":[
	  {"name":"lookup","action":"lookup","exchanges":[{"request":{"method":"POST","path":"/search"},"response":{"status":200,"body":{}}}],"expect":{"output":{"count":"seven"}}},
	  {"name":"send","action":"send","exchanges":[{"request":{"method":"POST","path":"/send"},"response":{"status":200,"body":{}}}],"expect":{"output":{}}},
	  {"name":"other","action":"other","exchanges":[],"expect":{"error":"not_found"}}]}`), &suite); err != nil {
		t.Fatal(err)
	}
	if err := suite.Check(); err != nil {
		t.Fatal(err)
	}
	rep := conntest.Run(context.Background(), c, &suite, conntest.Options{})
	byCase := map[string]conntest.Result{}
	for _, r := range rep.Results {
		byCase[r.Case] = r
	}
	if r := byCase["lookup"]; r.Pass || len(r.Drift) == 0 {
		t.Errorf("drift not caught: %s", toJSON(r))
	}
	if r := byCase["send"]; !r.Pass || r.KeySent {
		t.Errorf("send: %s", toJSON(r))
	}
	if strings.Join(rep.KeyNotSent, ",") != "send" || strings.Join(rep.ReadWrites, ",") != "lookup" {
		t.Errorf("honesty findings: %s", toJSON(rep))
	}
	// The undeclared host was refused before anything was sent.
	if r := byCase["other"]; !r.Pass || len(r.Methods) != 0 {
		t.Errorf("other host: %s", toJSON(r))
	}

	// A suite naming a missing fixture or a bad kind is refused.
	var broken conntest.Suite
	_ = json.Unmarshal([]byte(`{"cases":[{"name":"a","action":"lookup","exchanges":["nope"],"expect":{"error":"oops"}}]}`), &broken)
	if err := broken.Check(); err == nil || !strings.Contains(err.Error(), `no fixture "nope"`) || !strings.Contains(err.Error(), "unknown error kind") {
		t.Errorf("check: %v", err)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
