package api_test

import (
	"slices"
	"strings"
	"testing"
)

// Decision 0060, R1: a business with no Google Sheets connection is
// offered the table twins and never the sheet templates they replace; a
// twin makes its known table, and a table of that name that exists is
// left as it is with what it lacks reported. Once Sheets is connected,
// both are offered, the twin first.
func TestNoSheetsTemplates(t *testing.T) {
	tw := newTablesWorld(t)
	w := tw.world
	registerSME(t, w.env.Registry)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	offered := func() []string {
		var out []string
		for _, x := range owner.must(200, "GET", "/v1/templates", nil)["templates"].([]any) {
			out = append(out, x.(map[string]any)["id"].(string))
		}
		return out
	}
	ids := offered()
	for _, id := range []string{"new-customer-welcome", "sales-ledger-sheet", "transfer-received-notify"} {
		if slices.Contains(ids, id) {
			t.Errorf("%s is offered to a business with no Google Sheets", id)
		}
	}
	if !slices.Contains(ids, "customer-welcome-table") || !slices.Contains(ids, "payment-received-tell-owner") {
		t.Fatalf("the twins are not offered: %v", ids)
	}

	// A customers table of the business's own, with a name only.
	owner.must(201, "POST", "/v1/tables", map[string]any{"name": "customers", "columns": []map[string]any{{"name": "name", "type": "text"}}})
	out := owner.must(201, "POST", "/v1/templates/customer-welcome-table/instantiate", map[string]any{"params": map[string]any{"business_name": "Acme Gym"}})
	made := out["tables"].([]any)
	if len(made) != 1 {
		t.Fatalf("tables: %v", made)
	}
	first := made[0].(map[string]any)
	if first["status"] != "exists" || toJSON(first["missing"]) != `["phone","email","area","marketing_opt_in","email_status"]` {
		t.Errorf("an existing table is left as it is, with what it lacks: %v", first)
	}

	// Pointed at a table that does not exist, the known shape is made.
	out = owner.must(201, "POST", "/v1/templates/customer-welcome-table/instantiate", map[string]any{"params": map[string]any{"business_name": "Acme Gym", "customers_table": "members"}})
	if first = out["tables"].([]any)[0].(map[string]any); first["status"] != "created" || first["name"] != "members" {
		t.Fatalf("made: %v", first)
	}
	for _, x := range owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
		m := x.(map[string]any)
		if m["name"] != "members" {
			continue
		}
		var cols []string
		for _, c := range m["columns"].([]any) {
			cols = append(cols, c.(map[string]any)["name"].(string))
		}
		if strings.Join(cols, ",") != "name,phone,email,area,marketing_opt_in,email_status" {
			t.Errorf("the known customers shape: %v", cols)
		}
	}

	// Google Sheets connected: both are offered, the twin first.
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "googlesheets@1",
		"credentials": map[string]string{"client_id": "app.apps.googleusercontent.com", "client_secret": "s", "refresh_token": "1//r"}})
	ids = offered()
	tw2, sh := slices.Index(ids, "customer-welcome-table"), slices.Index(ids, "new-customer-welcome")
	if tw2 < 0 || sh < 0 || tw2 != sh-1 {
		t.Errorf("with Sheets the twin is listed just before the sheet template: %d, %d in %v", tw2, sh, ids)
	}
}
