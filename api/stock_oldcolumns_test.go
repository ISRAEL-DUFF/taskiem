package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	stockconn "github.com/israel-duff/taskiem/connectors/stock"
)

// Workflows on the catalogue's old stock columns (what-a-business-buys
// §4.3, S1b): one reading stock from the catalogue and one setting
// cost_price are listed with what to use instead; one on stock@1 and the
// kept copy is not; the owner is told once, however often it runs.
func TestStockOldColumns(t *testing.T) {
	w := newTablesWorld(t)
	if err := w.env.Registry.Register(stockconn.New(withStock(w.world))); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")

	// No catalogue: nothing to list, nothing told.
	if out := owner.must(200, "GET", "/v1/stock/old-columns", nil); len(out["uses"].([]any)) != 0 {
		t.Fatalf("before: %v", out)
	}
	owner.must(200, "POST", "/v1/stock/setup", nil) // the catalogue: products

	draft := func(name, doc string) string {
		t.Helper()
		out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": name, "definition": json.RawMessage(doc)})
		if probs := out["problems"].([]any); len(probs) > 0 {
			t.Fatalf("%s: problems: %v", name, probs)
		}
		return out["id"].(string)
	}
	publish := func(name, doc string) string {
		t.Helper()
		id := draft(name, doc)
		owner.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
		return id
	}
	reads := publish("Low shelf", `{"schema":"wd/v1","id":"wf_low","version":1,"name":"Low shelf","trigger":{"type":"manual"},"steps":[
		{"id":"read","type":"connector","name":"Read the shelf","connector":"xenibase@1","action":"read_rows","connection":"data_tables",
		 "input":{"table":"products","select":"name,stock","filters":[{"column":"stock","op":"lt","value":5}]}}]}`)
	sets := publish("New cost", `{"schema":"wd/v1","id":"wf_cost","version":1,"name":"New cost","trigger":{"type":"manual"},"steps":[
		{"id":"cost","type":"connector","connector":"xenibase@1","action":"update_rows","connection":"data_tables",
		 "input":{"table":"products","key":"=trigger.body.id","set":{"cost_price":"=trigger.body.cost"}}}]}`)
	publish("New way", `{"schema":"wd/v1","id":"wf_new","version":1,"name":"New way","trigger":{"type":"manual"},"steps":[
		{"id":"item","type":"connector","connector":"stock@1","action":"get_item","input":{"id":"=trigger.body.id"}},
		{"id":"adj","type":"connector","connector":"stock@1","action":"adjust","needs":["item"],"when":"=steps.item.output.stock > 0","input":{"id":"=trigger.body.id","change":-1}}]}`)
	// Drafts too: one on the kept copy is not listed (its table is made
	// with the first movement); one reading the unit is, as a draft.
	draft("Kept copy", `{"schema":"wd/v1","id":"wf_kept","version":1,"name":"Kept copy","trigger":{"type":"manual"},"steps":[
		{"id":"kept","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"stock_items","select":"name,count,cost_kobo"}}]}`)
	draft("Units", `{"schema":"wd/v1","id":"wf_units","version":1,"name":"Units","trigger":{"type":"manual"},"steps":[
		{"id":"units","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"products","select":"name,unit"}}]}`)

	out := viewer.must(200, "GET", "/v1/stock/old-columns", nil)
	uses := out["uses"].([]any)
	byKey := map[string]map[string]any{}
	for _, x := range uses {
		u := x.(map[string]any)
		byKey[u["workflow"].(string)+" "+u["step"].(string)+" "+u["uses"].(string)+" "+u["column"].(string)] = u
	}
	if len(uses) != 3 || uses[2].(map[string]any)["workflow"] != "Units" {
		t.Fatalf("uses: %v", uses)
	}
	if u := byKey["Units units reads unit"]; u == nil || u["published"] != false || u["version"] != float64(1) {
		t.Fatalf("the draft: %v", uses)
	}
	r, s := byKey["Low shelf read reads stock"], byKey["New cost cost sets cost_price"]
	if r == nil || r["workflow_id"] != reads || r["step_name"] != "Read the shelf" || r["table_column"] != "stock" || r["published"] != true ||
		!strings.Contains(r["instead"].(string), "stock_items") || !strings.Contains(r["instead"].(string), "stock@1") {
		t.Fatalf("the read: %v", uses)
	}
	if s == nil || s["workflow_id"] != sets || !strings.Contains(s["instead"].(string), "cost_kobo") {
		t.Fatalf("the cost: %v", uses)
	}

	// Told once: the sweep and the check again tell nothing more.
	if _, told, err := w.srv.CheckOldColumns(context.Background(), tenantID(t, owner)); err != nil || told {
		t.Fatalf("again: told %v, %v", told, err)
	}
	owner.must(200, "GET", "/v1/stock/old-columns", nil)
	var mine []map[string]any
	for _, x := range owner.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any) {
		if n := x.(map[string]any); strings.Contains(n["title"].(string), "old stock columns") {
			mine = append(mine, n)
		}
	}
	body, _ := mine[0]["body"].(string)
	if len(mine) != 1 || mine[0]["count"] != float64(1) || mine[0]["link"] != "/workflows" || !strings.Contains(mine[0]["title"].(string), "3 workflows") ||
		!strings.Contains(body, "Low shelf, Read the shelf: reads stock") || !strings.Contains(body, "New cost, cost: sets cost_price") ||
		!strings.Contains(body, "Units (draft), units: reads unit") {
		t.Fatalf("told: %v", mine)
	}

	// A mapping that named its own column for the count, stored before S1:
	// that name is the old column, not stock.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE storefronts SET catalogue_columns = catalogue_columns || '{"stock":"qty"}' WHERE tenant_id = $1`,
		tenantID(t, owner)); err != nil {
		t.Fatal(err)
	}
	if uses := owner.must(200, "GET", "/v1/stock/old-columns", nil)["uses"].([]any); len(uses) != 2 || strings.Contains(toJSON(uses), "Low shelf") {
		t.Fatalf("by the mapping's name: %v", uses)
	}
}
