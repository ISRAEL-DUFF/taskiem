package api_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Review of the pages API: a json column is never offered as an order nor
// taken as one; a draft written to a file leaves out what it holds of a
// personal column; a version names who published it without their whole
// email address; files read at once each get their own free name and stay
// within the plan's limit.
func TestPagesReviewFixes(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "kit", "title": "Kit", "columns": []map[string]any{
		{"name": "item", "type": "text"}, {"name": "meta", "type": "json"}, {"name": "phone", "type": "phone"}}})
	doc := map[string]any{"page": "page/v1", "name": "kit", "title": "Kit",
		"filters": []any{map[string]any{"id": "who", "type": "choice", "column": "phone", "options": []any{map[string]any{"value": "2348012345678"}}}},
		"blocks": []any{map[string]any{"id": "all", "type": "table", "source": map[string]any{"table": "kit"},
			"columns": []any{map[string]any{"column": "item"}, map[string]any{"column": "meta"}}}}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	// The unused choice filter on a personal column is refused.
	got := owner.must(200, "GET", "/v1/pages/kit", nil)
	if !strings.Contains(mustString(t, got["problems"]), "it is personal") {
		t.Errorf("problems: %v", got["problems"])
	}
	// The draft as a file: the phone left out, and counted.
	status, hdr, body := owner.rawGet(t, "/v1/pages/kit/export?draft=1")
	if status != 200 || strings.Contains(body, "2348012345678") || hdr.Get("Taskiem-Left-Out") != "1" {
		t.Errorf("draft export: %d %s %s", status, hdr.Get("Taskiem-Left-Out"), body)
	}
	delete(doc, "filters")
	owner.must(200, "PUT", "/v1/pages/kit/draft", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/kit/publish", nil)
	cols := blockOf(t, viewOf(t, owner, "kit", nil), "all")["columns"].([]any)
	if cols[0].(map[string]any)["sortable"] != true || cols[1].(map[string]any)["sortable"] != false {
		t.Errorf("sortable: %v", cols)
	}
	if s, _ := owner.do("GET", "/v1/pages/kit/blocks/all?order=meta", nil); s != 400 {
		t.Errorf("ordered by json: %d", s)
	}
	// Who published, never a whole email address.
	vs := owner.must(200, "GET", "/v1/pages/kit/versions", nil)["versions"].([]any)
	if by := vs[0].(map[string]any)["published_by"].(string); by == "" || strings.Contains(by, "@") {
		t.Errorf("published by %q", by)
	}

	// Five copies of a file read at once: five pages, five names.
	file := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "blocks": []any{map[string]any{"id": "h", "type": "heading", "title": "Hi"}}}
	var wg sync.WaitGroup
	statuses := make([]int, 5)
	names := make([]string, 5)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, out := owner.do("POST", "/v1/pages/import", map[string]any{"document": file})
			statuses[i] = s
			if pg, ok := out["page"].(map[string]any); ok {
				names[i], _ = pg["name"].(string)
			}
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, s := range statuses {
		if s != 201 || seen[names[i]] {
			t.Errorf("import %d: %d %q (%v)", i, s, names[i], names)
		}
		seen[names[i]] = true
	}
	if !seen["desk"] || !seen["desk-5"] {
		t.Errorf("names: %s", fmt.Sprint(names))
	}
}
