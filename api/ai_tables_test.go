package api_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/templates"
)

// The builder's copy of the column types is the tables service's.
func TestAIBuilderColumnTypes(t *testing.T) {
	var want []string
	for _, x := range datatables.Types {
		want = append(want, x.Name)
	}
	if !slices.Equal(builder.ColumnTypes, want) {
		t.Fatalf("builder.ColumnTypes %v, datatables.Types %v", builder.ColumnTypes, want)
	}
	if !slices.Equal(templates.ColumnTypes, want) {
		t.Fatalf("templates.ColumnTypes %v, datatables.Types %v", templates.ColumnTypes, want)
	}
}

const leaveDraft = `{"schema":"wd/v1","id":"wf_leaveRequest","version":1,"name":"Leave request","trigger":{"type":"manual"},
 "steps":[{"id":"add","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables",
  "input":{"table":"leave","rows":[{"name":"=trigger.body.name","start_date":"=trigger.body.start"}]}}]}`

func leaveEnvelope(t *testing.T) ai.Response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"summary": "Records leave.", "assumptions": []string{}, "workflow": leaveDraft, "tests": []any{},
		"template": "", "template_params": "",
		"tables": []any{map[string]any{"name": "leave", "title": "Leave", "columns": []any{
			map[string]any{"name": "name", "title": "Name", "type": "text", "required": true},
			map[string]any{"name": "start_date", "title": "Start date", "type": "date", "required": false},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	return ai.Response{Text: string(raw)}
}

// TestAIBuildProposesItsTables: a draft that writes to a table that does
// not exist proposes it; saving makes it as the person reviewed it.
func TestAIBuildProposesItsTables(t *testing.T) {
	tw := newTablesWorld(t)
	w := tw.world
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Script: []ai.Response{leaveEnvelope(t)}}}
	owner := w.tenant(t, "Acme", "owner@acme.test")

	b := build(t, w, owner, map[string]any{"goal": "record approved leave in a table"})
	tables := b["proposal"].(map[string]any)["tables"].([]any)
	if len(tables) != 1 || tables[0].(map[string]any)["name"] != "leave" {
		t.Fatalf("proposed tables: %v", b["proposal"])
	}
	if strings.Contains(toJSON(b["proposal"].(map[string]any)["warnings"]), `"leave"`) {
		t.Fatalf("a warning about a proposed table: %v", b["proposal"])
	}

	// Saved with a column the person added.
	reviewed := map[string]any{"name": "leave", "title": "Leave", "columns": []any{
		map[string]any{"name": "name", "title": "Name", "type": "text", "required": true},
		map[string]any{"name": "start_date", "title": "Start date", "type": "date"},
		map[string]any{"name": "reason", "title": "Reason", "type": "text"},
	}}
	saved := owner.must(201, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", map[string]any{"tables": []any{reviewed}})
	res := saved["tables"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["status"] != "created" {
		t.Fatalf("saved: %v", saved)
	}
	list := toJSON(owner.must(200, "GET", "/v1/tables", nil))
	if !strings.Contains(list, `"leave"`) || !strings.Contains(list, `"reason"`) || !strings.Contains(list, `"start_date"`) {
		t.Fatalf("tables: %s", list)
	}

	// Another draft saved with the same table: it exists, and the draft is
	// saved all the same.
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Script: []ai.Response{leaveEnvelope(t)}}}
	b2 := build(t, w, owner, map[string]any{"goal": "record leave again"})
	if n := len(b2["proposal"].(map[string]any)["tables"].([]any)); n != 0 {
		t.Fatalf("an existing table proposed again: %v", b2["proposal"])
	}
	saved = owner.must(201, "POST", "/v1/ai/builds/"+b2["id"].(string)+"/save", map[string]any{"tables": []any{reviewed}})
	if r := saved["tables"].([]any)[0].(map[string]any); r["status"] != "exists" {
		t.Fatalf("again: %v", saved)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), "table.create") {
		t.Error("no table.create in the audit log")
	}
}
