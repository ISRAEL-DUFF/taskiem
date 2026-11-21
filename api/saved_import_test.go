package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func stringOf(v any) string { return fmt.Sprint(v) }

// Saved things from a file (saved objects S3, §9.6): the plan first, each
// thing with its masked hint; saved with the plan's hash, each as one added
// by hand; a file with a bad line, a name saved already or a changed file
// saves nothing; only those who manage saved things import.
func TestSavedImport(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@savedimport.test")
	viewer := w.member(t, owner, "vic@savedimport.test", "viewer")
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum", "title": "Mum", "kind": "person", "details": map[string]any{"phone": "0803 111 2222"}})

	file := "Title,Bank,Account number,Phone,Street,Area\n" +
		"Dangote Cement,GTBank,0123456789,,,\n" +
		"Bola Stores,,,0803 123 4567,,\n" +
		"Apapa warehouse,,,,4 Creek Road,Apapa\n"
	viewer.must(403, "POST", "/v1/saved/import", map[string]any{"csv": file})
	plan := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file})
	rows := plan["rows"].([]any)
	if plan["saved"] != false || len(rows) != 3 || plan["total"] != float64(3) {
		t.Fatalf("plan: %v", plan)
	}
	if r := rows[0].(map[string]any); r["name"] != "dangote-cement" || r["kind"] != "bank_account" || r["hint"] != "GTBank ••6789" {
		t.Fatalf("an account: %v", r)
	}
	if strings.Contains(stringOf(plan), "0123456789") || strings.Contains(stringOf(plan), "1234567") {
		t.Fatalf("the plan shows a number in full: %v", plan)
	}
	if got := owner.must(200, "GET", "/v1/saved", nil)["saved"].([]any); len(got) != 1 {
		t.Fatalf("the plan saved: %d", len(got))
	}
	// Another file than the one shown.
	owner.must(409, "POST", "/v1/saved/import", map[string]any{"csv": file + "Tolu,,,0803 999 0000,,\n", "confirm": plan["hash"]})
	done := owner.must(200, "POST", "/v1/saved/import", map[string]any{"csv": file, "confirm": plan["hash"]})
	if done["saved"] != true || len(done["added"].([]any)) != 3 || len(done["failed"].([]any)) != 0 {
		t.Fatalf("saved: %v", done)
	}
	got := owner.must(200, "GET", "/v1/saved", nil)["saved"].([]any)
	if len(got) != 4 {
		t.Fatalf("after: %d", len(got))
	}
	if r := owner.must(200, "GET", "/v1/saved/resolve?name=apapa-warehouse", nil); !strings.Contains(stringOf(r), "Creek Road") {
		t.Errorf("the place: %v", r)
	}

	// Again: its names are saved now, each line said; and a bad line.
	again := owner.must(422, "POST", "/v1/saved/import", map[string]any{"csv": file})
	if ps := again["problems"].([]any); len(ps) != 3 || !strings.Contains(ps[0].(map[string]any)["message"].(string), "@dangote-cement is saved already") {
		t.Fatalf("again: %v", again)
	}
	bad := owner.must(422, "POST", "/v1/saved/import", map[string]any{"csv": "title,account_number,bank\nTolu Bello,12345,GTBank\n"})
	if ps := bad["problems"].([]any); len(ps) != 1 || ps[0].(map[string]any)["line"] != float64(2) || bad["code"] != "import_unreadable" {
		t.Fatalf("a bad line: %v", bad)
	}
}
