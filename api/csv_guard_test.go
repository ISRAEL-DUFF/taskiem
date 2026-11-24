package api_test

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A school's exports (debtors, clearance) guard what a person typed: a
// name or class a spreadsheet would run as a formula is
// written as text.
func TestSchoolExportsGuardFormulas(t *testing.T) {
	w := newWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Messages = w.srv.Invoices.Messages
	rec := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: w.srv.Invoices}
	schools.WireReceipts(w.srv.Invoices, rec)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	j1 := owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	sess := owner.must(201, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2027"})
	term := owner.must(201, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/terms",
		map[string]any{"name": "First term", "starts_on": "2026-09-01", "ends_on": "2099-12-11", "fees_due_on": "2026-09-07"})
	tid := term["id"].(string)
	const name, adm = `=HYPERLINK("http://evil.test") Eze`, "GCA/001"
	in := "Name,Admission number,Class,Guardian name,Phone\n\"" + strings.ReplaceAll(name, `"`, `""`) + "\"," + adm + ",JSS 1 A,Ngozi Eze,08031234567\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": in})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": in, "confirm": plan["hash"]})
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/start", nil)
	owner.must(200, "PUT", "/v1/schools/terms/"+tid+"/fees/"+j1["id"].(string), map[string]any{"items": []any{
		map[string]any{"name": "Tuition", "amount_kobo": 10_000_000, "kind": "required"}}})
	p := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})["plan"].(map[string]any)
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": p["hash"], "send": true})

	for _, path := range []string{"/v1/schools/debtors?format=csv&reveal=true", "/v1/schools/clearance?format=csv&reveal=true"} {
		raw := owner.must(200, "GET", path, nil)["raw"].(string)
		rows, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s: %v %q", path, err, raw)
		}
		if rows[1][1] != adm || rows[1][2] != "'"+name {
			t.Errorf("%s: the admission number is %q and the name %q; want the name written as text", path, rows[1][1], rows[1][2])
		}
	}
}
