package dojah

import (
	"context"
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
