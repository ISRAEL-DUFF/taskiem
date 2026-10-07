package pgdock_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/pgdock"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

// The fixtures (testdata/events) are deliveries shaped as PGDock's
// docs/webhooks.md publishes them: each passes the manifest's
// verification, and its expressions read what the trigger needs.
func TestFixtureDeliveries(t *testing.T) {
	c := pgdock.New(pgdock.Options{})
	tr := c.Manifest.Triggers["row_changed"]
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	want := map[string][3]string{ // event, dedup, test
		"insert": {"INSERT", "evt_3f2a9c1b7d4e_41", "false"}, "update": {"UPDATE", "evt_3f2a9c1b7d4e_42", "false"},
		"delete": {"DELETE", "evt_3f2a9c1b7d4e_43", "false"}, "truncated": {"UPDATE", "evt_3f2a9c1b7d4e_44", "false"},
		"test": {"TEST", "evt_test_9", "true"},
	}
	files, _ := filepath.Glob("testdata/events/*.json")
	if len(files) != len(want) {
		t.Fatalf("fixtures %v", files)
	}
	defer func() { connector.Now = time.Now }()
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // test fixtures
		if err != nil {
			t.Fatal(err)
		}
		var fx struct {
			Secret  string            `json:"secret"`
			At      int64             `json:"at"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatal(err)
		}
		h := http.Header{}
		hs := map[string]any{}
		for k, v := range fx.Headers {
			h.Set(k, v)
			hs[strings.ToLower(k)] = v
		}
		connector.Now = func() time.Time { return time.Unix(fx.At+30, 0) }
		if err := connector.VerifyWebhook(tr.Verify, fx.Secret, h, []byte(fx.Body)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		if connector.VerifyWebhook(tr.Verify, "whsec_other", h, []byte(fx.Body)) == nil {
			t.Errorf("%s: verified with another secret", f)
		}
		connector.Now = func() time.Time { return time.Unix(fx.At+301, 0) }
		if connector.VerifyWebhook(tr.Verify, fx.Secret, h, []byte(fx.Body)) == nil {
			t.Errorf("%s: verified outside the five-minute window", f)
		}
		body, _ := expr.DecodeJSON([]byte(fx.Body))
		act := map[string]any{"body": body, "headers": hs, "query": map[string]any{}, "item": nil}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		for i, src := range []string{tr.EventType, tr.Dedup, tr.TestEvent} {
			v, err := e.Eval(src, act)
			if got := fmt.Sprint(v); err != nil || got != want[name][i] {
				t.Errorf("%s: %s = %v (%v), want %s", name, src, v, err, want[name][i])
			}
		}
	}
	// Without the event id header, the payload's id deduplicates.
	v, err := e.Eval(tr.Dedup, map[string]any{"body": map[string]any{"id": "evt_x"}, "headers": map[string]any{}, "query": map[string]any{}, "item": nil})
	if err != nil || v != "evt_x" {
		t.Errorf("fallback dedup: %v %v", v, err)
	}
}
