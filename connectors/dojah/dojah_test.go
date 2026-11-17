package dojah

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	return New(Options{BaseURL: srv.URL}).Actions[action].Execute(context.Background(), connector.Request{
		Input: input, Credentials: map[string]string{"app_id": "app_123", "secret_key": "test_sk_abc"}, HTTP: srv.Client(),
	})
}

func TestRegister(t *testing.T) {
	if err := connector.NewRegistry().Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
}

func TestLookupBVNDropsPhotoByDefault(t *testing.T) {
	r, err := call(t, "lookup_bvn", map[string]any{"bvn": "22222222222"}, "bvn_full")
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["first_name"] != "JOHN" || out["image"] != nil {
		t.Errorf("output %v", out)
	}
	r, err = call(t, "lookup_bvn", map[string]any{"bvn": "22222222222", "include_image": true}, "bvn_full")
	if err != nil || r.Output.(map[string]any)["image"] == nil {
		t.Errorf("include_image: %v", err)
	}
}

func TestNotFoundIsFatal(t *testing.T) {
	_, err := call(t, "lookup_bvn", map[string]any{"bvn": "00000000000"}, "bvn_not_found")
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("want fatal, got %v", err)
	}
}

func TestBalance(t *testing.T) {
	r, err := call(t, "check_balance", nil, "balance")
	if err != nil || r.Output.(map[string]any)["wallet_balance"] != "4970.00" {
		t.Errorf("%v %v", r.Output, err)
	}
}

// Dojah's entity carries more than the manifest lists (a second phone, an
// email, addresses). Only the listed fields, each declared personal, are
// passed on: the rest would be stored in run history unsealed.
func TestOnlyDeclaredFieldsLeaveTheConnector(t *testing.T) {
	c := New(Options{})
	for action, fx := range map[string]string{"lookup_bvn": "bvn_full", "lookup_nin": "nin"} {
		in := map[string]any{"bvn": "22222222222", "include_image": true}
		if action == "lookup_nin" {
			in = map[string]any{"nin": "70123456789"}
		}
		r, err := call(t, action, in, fx)
		if err != nil {
			t.Fatal(err)
		}
		out := r.Output.(map[string]any)
		if out["first_name"] != "JOHN" {
			t.Errorf("%s: %v", action, out)
		}
		spec := c.Manifest.Actions[action]
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(spec.Output, &schema); err != nil {
			t.Fatal(err)
		}
		declared := map[string]bool{}
		for _, f := range spec.PII {
			if segs, ok := connector.OutputPIIPath(f.Field); ok && len(segs) == 1 {
				declared[segs[0]] = true
			}
		}
		for k := range out {
			if _, ok := schema.Properties[k]; !ok {
				t.Errorf("%s passes on %q, which its output schema does not list", action, k)
			}
			if !declared[k] {
				t.Errorf("%s passes on %q without declaring it personal", action, k)
			}
		}
	}
}
