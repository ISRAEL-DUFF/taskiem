package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// One workflow per storefront form (docs/spec/fronts.md F0): publishing a
// second workflow on the order form is refused with the form and the
// workflow that runs it named; Replace takes that one out of use and
// publishes this one in one change, audited; under four-eyes publishing
// Replace is not offered, with the reason, and no request is left waiting
// that could never be approved.
func TestPublishReplacesTheFormsWorkflow(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ctx := context.Background()
	if err := w.env.Registry.Register(sfconn.New(w.orders)); err != nil {
		t.Fatal(err)
	}
	var old string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT workflow_id::text FROM triggers WHERE tenant_id = $1 AND type = 'storefront' AND storefront_action = 'order' AND environment = 'prod'`,
		w.biz).Scan(&old); err != nil {
		t.Fatal(err)
	}
	tpl, _ := templates.Default().Get("storefront-order")
	draft := func(name string) string {
		t.Helper()
		def, err := tpl.Instantiate(map[string]any{}, name)
		if err != nil {
			t.Fatal(err)
		}
		return ada.must(201, "POST", "/v1/workflows", map[string]any{"name": name, "definition": json.RawMessage(def)})["id"].(string)
	}
	mine := draft("Orders with a thank-you")

	// Refused, naming the form and the workflow that runs it.
	e := ada.must(409, "POST", "/v1/workflows/"+mine+"/versions/1/publish", nil)
	held, _ := e["workflow"].(map[string]any)
	if e["code"] != "action_taken" || e["action"] != "order" || e["action_words"] != "order form" || e["can_replace"] != true || held["id"] != old ||
		!strings.Contains(e["error"].(string), "order form already runs") {
		t.Fatalf("refused: %v", e)
	}

	// Replace: this one runs the form now, the other is out of use, in one
	// change.
	if r := ada.must(200, "POST", "/v1/workflows/"+mine+"/versions/1/publish", map[string]any{"replace": true}); r["state"] != "published" {
		t.Fatalf("replaced: %v", r)
	}
	var runs string
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT min(workflow_id::text), count(*) FROM triggers WHERE tenant_id = $1 AND type = 'storefront' AND storefront_action = 'order' AND environment = 'prod'`,
		w.biz).Scan(&runs, &n); err != nil || n != 1 || runs != mine {
		t.Fatalf("the form starts %d workflow(s), %s: %v", n, runs, err)
	}
	var deployed, audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM deployments WHERE workflow_id = $1`, old).Scan(&deployed); err != nil || deployed != 0 {
		t.Errorf("the replaced workflow is still deployed: %d %v", deployed, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'workflow.undeploy' AND detail->>'via' = 'replace'
		AND detail->>'replaced_by' = $2 AND detail->>'storefront_action' = 'order'`, w.biz, mine).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audited: %d %v", audited, err)
	}
	// Replace with nothing to replace is an ordinary publish.
	ada.must(200, "POST", "/v1/workflows/"+mine+"/versions/1/publish", map[string]any{"replace": true})

	// Four-eyes: refused with the reason, and no request waits.
	ada.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	another := draft("Orders, the other way")
	e = ada.must(409, "POST", "/v1/workflows/"+another+"/versions/1/publish", map[string]any{"replace": true})
	if e["code"] != "action_taken" || e["can_replace"] != false || !strings.Contains(e["refused"].(string), "four-eyes") ||
		!strings.Contains(e["error"].(string), "Orders with a thank-you") {
		t.Fatalf("four-eyes: %v", e)
	}
	var waiting int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM publish_requests WHERE workflow_id = $1`, another).Scan(&waiting); err != nil || waiting != 0 {
		t.Errorf("a request that could never be approved: %d %v", waiting, err)
	}
}
