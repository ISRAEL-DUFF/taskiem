package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/gmail"
	"github.com/israel-duff/taskiem/connectors/googlesheets"
	"github.com/israel-duff/taskiem/connectors/termii"
	"github.com/israel-duff/taskiem/engine/connector"
)

// registerSME makes the connectors of the SME templates' tests available.
func registerSME(t *testing.T, reg *connector.Registry) {
	t.Helper()
	for _, c := range []*connector.Connector{googlesheets.New(googlesheets.Options{}), termii.New(termii.Options{}), gmail.New(gmail.Options{})} {
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}
}

var debtorParams = map[string]any{"business_name": "Ada Stores", "spreadsheet_id": "1AbCdEfGhIjKlMnOpQrStUvWxYz012", "day": "Friday", "send_time": "9am",
	"payment_details": "Pay to 0123456789, GTBank"}

func TestTemplatesListAndInstantiate(t *testing.T) {
	w := newWorld(t)
	registerSME(t, w.env.Registry)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "viewer@acme.test", "password": testPassword, "roles": []string{"viewer"}})
	viewer := w.login(t, "viewer@acme.test", testPassword)

	// Any member browses the library.
	list := viewer.must(200, "GET", "/v1/templates", nil)["templates"].([]any)
	if len(list) < 15 {
		t.Fatalf("%d templates", len(list))
	}
	byID := map[string]map[string]any{}
	for _, x := range list {
		m := x.(map[string]any)
		byID[m["id"].(string)] = m
		if len(m["steps"].([]any)) < 2 || len(m["params"].([]any)) == 0 || m["definition"] != nil {
			t.Errorf("%s: steps %v params %v", m["id"], m["steps"], m["params"])
		}
	}
	debtor := byID["debtor-reminder-sms"]
	if debtor["available"] != true || byID["kyc-bvn-check"]["available"] != false {
		t.Errorf("availability follows the tenant's connectors: %v %v", debtor["available"], byID["kyc-bvn-check"]["available"])
	}
	first := debtor["steps"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(first["text"].(string), "Every Friday at 10:00") {
		t.Errorf("plain steps: %v", first)
	}
	q := viewer.must(200, "GET", "/v1/templates?q=every+Friday+text+my+customers+who+owe+me", nil)["templates"].([]any)
	if len(q) == 0 || q[0].(map[string]any)["id"] != "debtor-reminder-sms" {
		t.Errorf("search: %v", q)
	}
	one := viewer.must(200, "GET", "/v1/templates/debtor-reminder-sms", nil)
	if !strings.Contains(string(mustJSON(t, one["definition"])), "{{business_name}}") {
		t.Errorf("full template: %v", one["definition"])
	}
	viewer.must(404, "GET", "/v1/templates/nope", nil)

	// Instantiating takes workflow.edit.
	viewer.must(403, "POST", "/v1/templates/debtor-reminder-sms/instantiate", map[string]any{"params": debtorParams})

	// Missing, invalid and unknown parameters are refused, by name.
	for name, tc := range map[string]struct {
		params map[string]any
		param  string
	}{
		"missing":    {map[string]any{"business_name": "Ada"}, "spreadsheet_id"},
		"invalid":    {withParam(debtorParams, "send_time", "25:99"), "send_time"},
		"expression": {withParam(debtorParams, "business_name", "=secrets.paystack"), "business_name"},
		"unknown":    {withParam(debtorParams, "surprise", 1), "surprise"},
	} {
		out := owner.must(400, "POST", "/v1/templates/debtor-reminder-sms/instantiate", map[string]any{"params": tc.params})
		found := false
		for _, p := range out["params"].([]any) {
			found = found || p.(map[string]any)["param"] == tc.param
		}
		if out["code"] != "invalid_params" || !found {
			t.Errorf("%s: %v", name, out)
		}
	}

	out := owner.must(201, "POST", "/v1/templates/debtor-reminder-sms/instantiate", map[string]any{"params": debtorParams, "name": "Friday debtors"})
	if out["state"] != "draft" || out["template"] != "debtor-reminder-sms" || len(out["problems"].([]any)) != 0 {
		t.Fatalf("instantiate: %v", out)
	}
	wf := out["id"].(string)
	got := owner.must(200, "GET", "/v1/workflows/"+wf, nil)
	if got["workflow"].(map[string]any)["name"] != "Friday debtors" || got["versions"].([]any)[0].(map[string]any)["state"] != "draft" {
		t.Errorf("workflow: %v", got)
	}
	v := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil)
	def := string(mustJSON(t, v["definition"]))
	if !strings.Contains(def, `"cron":"0 9 * * 5"`) || !strings.Contains(def, `"business_name":"Ada Stores"`) || strings.Contains(def, "{{") {
		t.Errorf("version: %s", def)
	}
	var tpl string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT template_id FROM workflow_versions WHERE workflow_id = $1`, wf).Scan(&tpl); err != nil || tpl != "debtor-reminder-sms" {
		t.Errorf("provenance: %q %v", tpl, err)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'workflow.create' AND detail->>'template' = 'debtor-reminder-sms'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit: %d %v", n, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func withParam(m map[string]any, k string, v any) map[string]any {
	out := map[string]any{}
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

// Embed apps name templates of the library (C1's allowed_templates): an
// unknown id is refused when the app is registered; end users see and
// instantiate only the app's templates, within its connectors.
func TestEmbedTemplates(t *testing.T) {
	w := newWorld(t)
	registerSME(t, w.env.Registry)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	p.key.must(400, "POST", "/v1/partner/embed-apps", map[string]any{"name": "x", "allowed_connectors": []string{}, "allowed_templates": []string{"salary-reminder"}})
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{"termii", "gmail"},
		"allowed_templates": []string{"payroll-reminder", "debtor-reminder-sms"}, "end_user_permissions": []string{"workflow.read", "workflow.edit"}})
	u := p.endUser(t, w, app, sub, "cust-1", "", "workflow.read", "workflow.edit")

	list := u.call(200, "GET", "/templates", nil)["templates"].([]any)
	if len(list) != 2 {
		t.Fatalf("templates: %v", list)
	}
	avail := map[string]any{}
	for _, x := range list {
		avail[x.(map[string]any)["id"].(string)] = x.(map[string]any)["available"]
	}
	if avail["payroll-reminder"] != true || avail["debtor-reminder-sms"] != false {
		t.Errorf("available within the app's connectors: %v", avail)
	}
	u.call(403, "POST", "/templates/kyc-bvn-check/instantiate", map[string]any{"params": map[string]any{}})
	if out := u.call(403, "POST", "/templates/debtor-reminder-sms/instantiate", map[string]any{"params": debtorParams}); out["not_allowed"] == nil {
		t.Errorf("googlesheets is not the app's: %v", out)
	}
	u.call(400, "POST", "/templates/payroll-reminder/instantiate", map[string]any{"params": map[string]any{}})
	out := u.call(201, "POST", "/templates/payroll-reminder/instantiate", map[string]any{"params": map[string]any{"business_name": "Customer Ltd"}})
	if out["state"] != "draft" {
		t.Fatalf("instantiate: %v", out)
	}
	u.call(200, "GET", "/workflows/"+out["id"].(string), nil)
}
