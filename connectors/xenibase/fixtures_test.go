package xenibase_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

// The fixtures (testdata/events) are Xenibase's published samples
// (docs/integrations/fixtures/webhooks, real deliveries with their
// headers, signed body and throwaway secret, which Xenibase's CI keeps
// current): each passes the manifest's verification as it is, with the
// timestamp in its signature as now, and its expressions read what the
// trigger needs.
func TestFixtureDeliveries(t *testing.T) {
	c := xenibase.New(xenibase.Options{})
	tr := c.Manifest.Triggers["row_changed"]
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	want := map[string][3]string{ // event, dedup, test
		"insert": {"INSERT", "evt_cf2c458a19a9_1", "false"}, "update": {"UPDATE", "evt_cf2c458a19a9_2", "false"},
		"delete": {"DELETE", "evt_cf2c458a19a9_3", "false"}, "insert-truncated": {"INSERT", "evt_cf2c458a19a9_4", "false"},
		"test": {"TEST", "evt_test_2ae453fab2c74b6a", "true"},
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
			Description string            `json:"description"`
			Secret      string            `json:"secret"`
			Headers     map[string]string `json:"headers"`
			Body        string            `json:"body"`
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
		// The sample's own time is the t of its signature.
		ts, _, _ := strings.Cut(strings.TrimPrefix(h.Get("Xenibase-Signature"), "t="), ",")
		at, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || h.Get("Xenibase-Event-Id") == "" || fx.Secret == "" {
			t.Fatalf("%s: not a Xenibase sample (%s): %v", f, fx.Description, err)
		}
		connector.Now = func() time.Time { return time.Unix(at+30, 0) }
		if err := connector.VerifyWebhook(tr.Verify, fx.Secret, h, []byte(fx.Body)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		if connector.VerifyWebhook(tr.Verify, "whsec_other", h, []byte(fx.Body)) == nil {
			t.Errorf("%s: verified with another secret", f)
		}
		connector.Now = func() time.Time { return time.Unix(at+301, 0) }
		if connector.VerifyWebhook(tr.Verify, fx.Secret, h, []byte(fx.Body)) == nil {
			t.Errorf("%s: verified outside the five-minute window", f)
		}
		body, _ := expr.DecodeJSON([]byte(fx.Body))
		act := map[string]any{"body": body, "headers": hs, "query": map[string]any{}, "item": nil}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		// Every change event of a keyed table carries project_id and
		// primary_key (Xenibase's reply, Q23 and Q28); a test event neither.
		ev := body.(map[string]any)
		if _, pk := ev["primary_key"].(map[string]any); name != "test" && (!pk || ev["project_id"] == "" || ev["table"] == "") {
			t.Errorf("%s: no primary_key, project_id or table: %v", name, ev)
		}
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
