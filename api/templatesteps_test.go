package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/payrolla"
)

// A workflow that uses callable templates (decision 0047) pins each when a
// version is saved, its readiness names what the templates need, and its
// page offers a library update with its differences.
func TestTemplateSteps(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	registerSME(t, w.env.Registry)
	if err := w.env.Registry.Register(payrolla.New(payrolla.Options{})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	def := map[string]any{"schema": "wd/v1", "id": "wf_x", "version": 1, "name": "After payroll",
		"trigger": map[string]any{"type": "connector_event", "config": map[string]any{"connector": "payrolla@1", "trigger": "run_event", "events": []string{"run.completed"}, "connection": "payrolla"}},
		"steps": []any{
			map[string]any{"id": "sum", "type": "template", "template": "payrolla-payroll-summary", "params": map[string]any{"payrolla_connection": "payrolla", "business_name": "Acme"},
				"input": map[string]any{"data": "=trigger.body.data"}},
			map[string]any{"id": "rec", "type": "template", "template": "payrolla-record-payroll", "params": map[string]any{"payrolla_connection": "payrolla"},
				"input": map[string]any{"id": "=trigger.body.id", "data": "=trigger.body.data"}},
		}}
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "After payroll", "definition": def})["id"].(string)

	// Both templates are pinned with version 1.
	rows, err := w.env.DB.Admin.Query(ctx, `SELECT template_id FROM workflow_template_pins WHERE workflow_id = $1 AND version = 1 ORDER BY template_id`, wf)
	if err != nil {
		t.Fatal(err)
	}
	var pinned []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		pinned = append(pinned, id)
	}
	rows.Close()
	if strings.Join(pinned, ",") != "payrolla-payroll-summary,payrolla-record-payroll" {
		t.Fatalf("pinned %v", pinned)
	}

	// Readiness reads the templates' steps, and names the template step.
	r := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1/readiness", nil)
	conn := needsOf(t, r["needs"])["connection/payrolla/prod"]
	if conn == nil || !strings.Contains(toJSON(conn["steps"]), `"id":"sum"`) || !strings.Contains(toJSON(conn["steps"]), `"id":"rec"`) {
		t.Fatalf("the Payrolla connection need: %v (all: %v)", conn, r["needs"])
	}
	// Nothing to update while the library has what version 1 pinned.
	got := owner.must(200, "GET", "/v1/workflows/"+wf, nil)
	if got["template_updates"] != nil {
		t.Fatalf("updates: %v", got["template_updates"])
	}
	// The version pinned an older summary (its tell step named otherwise,
	// without its not_paid output): the page offers the library's.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE workflow_template_pins
		SET template = jsonb_set(jsonb_set(template, '{definition,steps,3,name}', '"Tell them"'), '{outputs}', (template->'outputs') - 2)
		WHERE workflow_id = $1 AND template_id = 'payrolla-payroll-summary'`, wf); err != nil {
		t.Fatal(err)
	}
	got = owner.must(200, "GET", "/v1/workflows/"+wf, nil)
	ups, _ := got["template_updates"].([]any)
	if len(ups) != 1 {
		t.Fatalf("updates: %v", got["template_updates"])
	}
	up := ups[0].(map[string]any)
	if up["template"] != "payrolla-payroll-summary" || !strings.Contains(toJSON(up["changes"]), "~ step tell") || !strings.Contains(toJSON(up["changes"]), "+ output not_paid") {
		t.Errorf("update: %v", up)
	}
	// Saving a new version takes it.
	owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": def})
	if got = owner.must(200, "GET", "/v1/workflows/"+wf, nil); got["template_updates"] != nil {
		t.Errorf("after saving: %v", got["template_updates"])
	}
}
