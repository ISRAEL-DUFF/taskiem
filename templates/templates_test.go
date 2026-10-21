package templates_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/flowcode"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/wdtext"
	"github.com/israel-duff/taskiem/templates"
)

func registry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestLibrary(t *testing.T) {
	lib := templates.Default()
	if n := len(lib.List()); n < 15 {
		t.Fatalf("the library has %d templates, want at least 15", n)
	}
	reg := registry(t)
	cats := map[string]int{}
	for _, tpl := range lib.List() {
		cats[tpl.Category]++
		if !templates.IDPattern.MatchString(tpl.ID) {
			t.Errorf("%s: id does not fit allowed_templates", tpl.ID)
		}
		for _, c := range tpl.Connectors {
			if _, ok := reg.Get(c); !ok {
				t.Errorf("%s: unknown connector %s", tpl.ID, c)
			}
		}
		for _, p := range tpl.Params {
			if p.Type == "connection" && p.Connector == "" {
				t.Errorf("%s: connection parameter %s names no connector", tpl.ID, p.Name)
			}
		}
	}
	if len(cats) < 4 {
		t.Errorf("categories: %v", cats)
	}
}

// Every template, filled with its examples, is a definition that passes the
// publishing checks, uses the connectors it declares, runs its generated
// happy path, reads aloud as plain steps, and survives the flow-code round
// trip (spec 10.2) unchanged.
func TestTemplatesInstantiateCheckDryRunAndRoundTrip(t *testing.T) {
	reg := registry(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, tpl := range templates.Default().List() {
		t.Run(tpl.ID, func(t *testing.T) {
			doc, err := tpl.Instantiate(tpl.Examples(), "")
			if err != nil {
				t.Fatal(err)
			}
			if probs := wdcheck.Check(doc, reg); len(probs) > 0 {
				t.Fatalf("publishing checks: %v\n%s", probs, doc)
			}
			def, err := wd.Load(doc)
			if err != nil {
				t.Fatal(err)
			}
			used := map[string]bool{}
			var walk func([]*wd.Step)
			walk = func(steps []*wd.Step) {
				for _, st := range steps {
					if st.Connector != "" {
						used[st.Connector] = true
					}
					for _, sub := range st.Children() {
						walk(sub)
					}
				}
			}
			walk(def.Steps)
			if ref, ok := def.Trigger.Config["connector"].(string); ok {
				used[ref] = true
			}
			for _, c := range tpl.Connectors {
				if !used[c] {
					t.Errorf("declares %s but does not use it", c)
				}
			}
			for c := range used {
				if !contains(tpl.Connectors, c) {
					t.Errorf("uses %s without declaring it", c)
				}
			}
			if f := builder.PolicyFindings(def, reg); len(f) > 0 {
				t.Errorf("policy findings: %v", f)
			}
			_, results, err := builder.DryRun(doc, reg, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range results {
				if !r.Passed {
					t.Errorf("dry run %s: %s %v", r.Name, r.Status, r.Failures)
				}
			}
			lines := wdtext.Describe(def, reg)
			if len(lines) < 2 {
				t.Errorf("plain steps: %v", lines)
			}
			text := wdtext.Text(lines)
			if strings.Contains(text, "=") || strings.Contains(text, "{{") {
				t.Errorf("plain steps show an expression:\n%s", text)
			}
			code, err := flowcode.Generate(ctx, doc)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			back, err := flowcode.CompileSource(ctx, code)
			if err != nil {
				t.Fatalf("compile: %v\n%s", err, code)
			}
			if !sameJSON(t, doc, back) {
				t.Fatalf("lossy round trip\n--- definition\n%s\n--- code\n%s\n--- compiled\n%s", doc, code, back)
			}
		})
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	dec := func(doc []byte) any {
		d := json.NewDecoder(strings.NewReader(string(doc)))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	return reflect.DeepEqual(dec(a), dec(b))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestInstantiateParams(t *testing.T) {
	tpl, ok := templates.Default().Get("debtor-reminder-sms")
	if !ok {
		t.Fatal("debtor-reminder-sms missing")
	}
	good := map[string]any{"business_name": "Ada's Shop", "spreadsheet_id": "1AbCdEfGhIjKlMnOpQrStUvWxYz", "day": "Fri", "send_time": "9:30am",
		"payment_details": "Pay to 0123456789 (Zenith) - say 'paid' when done"}
	doc, err := tpl.Instantiate(good, "Friday debtors")
	if err != nil {
		t.Fatal(err)
	}
	def, err := wd.Load(doc)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "Friday debtors" || def.Trigger.Config["cron"] != "30 9 * * 5" {
		t.Errorf("name %q, cron %v", def.Name, def.Trigger.Config["cron"])
	}
	// Text parameters are data in a transform, never inside an expression.
	out := def.Step("settings").Transform.Output.(map[string]any)
	if out["business_name"] != "Ada's Shop" || out["payment_details"] != good["payment_details"] {
		t.Errorf("settings: %v", out)
	}
	// Defaults fill what was not given; key order is the template's.
	if def.Step("read_debtors").Input["range"] != "Debtors" || def.Step("read_debtors").Connection != "main" {
		t.Errorf("defaults: %v", def.Step("read_debtors").Input)
	}
	if !strings.HasPrefix(string(doc), `{"schema":"wd/v1","id":"wf_debtorReminderSms"`) {
		t.Errorf("key order: %.60s", doc)
	}

	for name, tc := range map[string]struct {
		values map[string]any
		param  string
	}{
		"missing":     {map[string]any{"business_name": "Ada"}, "spreadsheet_id"},
		"expression":  {with(good, "business_name", "=secrets.paystack"), "business_name"},
		"bad time":    {with(good, "send_time", "25:00"), "send_time"},
		"bad day":     {with(good, "day", "someday"), "day"},
		"pattern":     {with(good, "spreadsheet_id", "not an id"), "spreadsheet_id"},
		"too long":    {with(good, "business_name", strings.Repeat("x", 61)), "business_name"},
		"unknown":     {with(good, "surprise", "x"), "surprise"},
		"marker":      {with(good, "payment_details", "{{business_name}}"), "payment_details"},
		"newline":     {with(good, "business_name", "a\nb"), "business_name"},
		"connection":  {with(good, "sms_connection", "bad name!"), "sms_connection"},
		"wrong type":  {with(good, "business_name", 12), "business_name"},
		"blank value": {with(good, "spreadsheet_id", "   "), "spreadsheet_id"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tpl.Instantiate(tc.values, "")
			var pe templates.ErrParams
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v", err)
			}
			found := false
			for _, p := range pe {
				found = found || p.Param == tc.param
			}
			if !found {
				t.Errorf("errors %v do not name %s", pe, tc.param)
			}
		})
	}
}

func with(m map[string]any, k string, v any) map[string]any {
	out := map[string]any{}
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

// Markers inside an expression become CEL literals: a quote in a value
// cannot end the literal and change the expression around it.
func TestExpressionLiterals(t *testing.T) {
	tpl := `{"id":"lit","title":"T","summary":"S","description":"D","category":"c","tags":[],"connectors":[],
	 "params":[{"name":"label","type":"string","title":"L","description":"L","required":true,"example":"x"},
	           {"name":"limit","type":"number","title":"N","description":"N","required":true,"example":5}],
	 "definition":{"schema":"wd/v1","id":"wf_lit","version":1,"name":"Lit","trigger":{"type":"manual"},
	  "steps":[{"id":"t","type":"transform","config":{"output":{"ok":"=size({{label}}) > 0 && {{limit:kobo}} > 100","raw":"{{limit}}","text":"limit {{limit}}"}}}]}}`
	lib, err := templates.Load(fstest.MapFS{"library/lit.json": {Data: []byte(tpl)}})
	if err != nil {
		t.Fatal(err)
	}
	x, _ := lib.Get("lit")
	doc, err := x.Instantiate(map[string]any{"label": `a' || true || '\`, "limit": 2.5}, "")
	if err != nil {
		t.Fatal(err)
	}
	def, err := wd.Load(doc)
	if err != nil {
		t.Fatal(err)
	}
	out := def.Step("t").Transform.Output.(map[string]any)
	if out["ok"] != `=size('a\' || true || \'\\') > 0 && 250 > 100` {
		t.Errorf("expression: %v", out["ok"])
	}
	if out["raw"] != 2.5 || out["text"] != "limit 2.5" {
		t.Errorf("raw %v (%T), text %v", out["raw"], out["raw"], out["text"])
	}
	// A marker naming no parameter, or a parameter the definition never
	// uses, is refused when the library loads.
	bad := strings.Replace(tpl, "{{limit}}", "{{nothing}}", 1)
	if _, err := templates.Load(fstest.MapFS{"library/lit.json": {Data: []byte(bad)}}); err == nil {
		t.Error("unknown marker accepted")
	}
}

func TestCoerce(t *testing.T) {
	min := 0.0
	cases := []struct {
		p    templates.Param
		in   any
		want any
	}{
		{templates.Param{Type: "integer"}, "1,000", int64(1000)},
		{templates.Param{Type: "number", Minimum: &min}, "₦2,500.50", 2500.5},
		{templates.Param{Type: "boolean"}, "Yes", true},
		{templates.Param{Type: "time"}, "5pm", "17:00"},
		{templates.Param{Type: "time"}, "12am", "00:00"},
		{templates.Param{Type: "weekday"}, "Mondays", "monday"},
		{templates.Param{Type: "string", Enum: []string{"en", "yo"}}, "YO", "yo"},
	}
	for _, c := range cases {
		got, err := c.p.Coerce(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s %v: %v, %v", c.p.Type, c.in, got, err)
		}
	}
	if _, err := (&templates.Param{Type: "number", Minimum: &min}).Coerce("-5"); err == nil {
		t.Error("below the minimum accepted")
	}
	if _, err := (&templates.Param{Type: "integer"}).Coerce(2.5); err == nil {
		t.Error("a fraction accepted as an integer")
	}
}

func TestMatch(t *testing.T) {
	lib := templates.Default()
	for goal, want := range map[string]string{
		"Every Friday, text my customers who owe me":                        "debtor-reminder-sms",
		"remind my debtors on whatsapp every week":                          "debtor-reminder-whatsapp",
		"send me a summary of today's sales every evening":                  "daily-sales-summary",
		"when a customer pays on paystack send them a thank you sms":        "payment-thank-you-sms",
		"alert me when my paystack balance is low":                          "low-balance-alert",
		"pay my suppliers after I approve":                                  "supplier-payment-approval",
		"remind me before salaries are due every month":                     "payroll-reminder",
		"tell me when stock is running low on my inventory sheet":           "stock-reorder-alert",
		"verify a new customer's BVN before onboarding them":                "kyc-bvn-check",
		"pay staff salaries from the payroll sheet once the owner approves": "salary-payout-approval",
	} {
		m := lib.Match(goal, 3)
		if len(m) == 0 || m[0].Template.ID != want {
			var got []string
			for _, x := range m {
				got = append(got, x.Template.ID)
			}
			t.Errorf("%q: got %v, want %s first", goal, got, want)
		}
	}
	if m := lib.Match("reconcile our settlement files from the bank's SFTP server into S3", 3); len(m) != 0 {
		t.Errorf("an unrelated goal matched %s", m[0].Template.ID)
	}
}

// A keyword stays distinctive however many other templates mention the
// word in their prose: only keywords and tags make a word common.
func TestMatchKeywordsOutweighProse(t *testing.T) {
	tpl := func(id, keywords, desc string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(`{"id":"` + id + `","title":"T","summary":"S","description":"` + desc + `","category":"c",
		 "tags":[],"keywords":[` + keywords + `],"connectors":[],"params":[],
		 "definition":{"schema":"wd/v1","id":"wf_` + id + `","version":1,"name":"N","trigger":{"type":"manual"},
		  "steps":[{"id":"t","type":"transform","config":{"output":{"ok":true}}}]}}`)}
	}
	fs := fstest.MapFS{"library/esusu.json": tpl("esusu", `"esusu","contribution","rota","thrift"`, "Collects esusu contributions.")}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		fs["library/"+id+".json"] = tpl(id, `"other"`, "Sends a note on whatsapp about the esusu contribution, the rota and the thrift.")
	}
	for _, id := range []string{"g", "h", "i", "j", "k", "l", "m", "n", "o", "p"} {
		fs["library/"+id+".json"] = tpl(id, `"unrelated"`, "Does something else.")
	}
	lib, err := templates.Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	m := lib.Match("collect every member's esusu contribution by the rota for our thrift group", 1)
	if len(m) == 0 || m[0].Template.ID != "esusu" {
		t.Fatalf("got %v, want esusu", m)
	}
}

// What other templates say in their prose does not move a template's
// match on its own keywords: adding common words to their descriptions
// leaves its score as it was (the S3 and refunds regressions).
func TestProseElsewhereKeepsAMatch(t *testing.T) {
	tpl := func(id, keywords, title, desc string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(`{"id":"` + id + `","title":"` + title + `","summary":"S","description":"` + desc + `","category":"c",
		 "tags":[],"keywords":[` + keywords + `],"connectors":[],"params":[],
		 "definition":{"schema":"wd/v1","id":"wf_` + id + `","version":1,"name":"N","trigger":{"type":"manual"},
		  "steps":[{"id":"t","type":"transform","config":{"output":{"ok":true}}}]}}`)}
	}
	build := func(prose string) *templates.Library {
		fs := fstest.MapFS{"library/salary.json": tpl("salary", `"salary","payroll","approve","pay"`, "Pay salaries after approval", "Pays staff from the payroll sheet.")}
		for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
			fs["library/"+id+".json"] = tpl(id, `"other`+id+`"`, "Something "+id, "Does something else. "+prose)
		}
		lib, err := templates.Load(fs)
		if err != nil {
			t.Fatal(err)
		}
		return lib
	}
	const goal = "pay this month's salary after two of us approve"
	before := build("").Match(goal, 1)
	after := build("When a paid order is declined, approve to refund the pay and salary through Paystack.").Match(goal, 1)
	if len(before) == 0 || before[0].Template.ID != "salary" {
		t.Fatalf("before: %v", before)
	}
	if len(after) == 0 || after[0].Template.ID != "salary" || after[0].Score != before[0].Score {
		t.Fatalf("the match moved with other templates' prose: %v, was %v", after, before)
	}
}

// Details as an owner has them (no-technical-steps N8): a sheet's link
// for its id, a tab's name for the range, and a picker that must be one
// Taskiem has.
func TestOwnerShapedParams(t *testing.T) {
	sheet := templates.Param{Name: "spreadsheet_id", Type: "string", Picker: "sheet"}
	if v, err := sheet.Coerce("https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123/edit#gid=0"); err != nil || v != "1AbCdEfGhIjKlMnOpQrStUvWxYz0123" {
		t.Errorf("a link: %v %v", v, err)
	}
	tab := templates.Param{Name: "sheet_range", Type: "string", Picker: "sheet_tab", From: "spreadsheet_id"}
	for in, want := range map[string]string{"Debtors": "Debtors", "Staff list": "'Staff list'", "Ada's": "'Ada''s'", "Sales!A:C": "Sales!A:C", "'Q1 sales'": "'Q1 sales'"} {
		if v, err := tab.Coerce(in); err != nil || v != want {
			t.Errorf("tab %q: %v %v", in, v, err)
		}
	}
	bad := []byte(`{"id":"x","title":"X","summary":"s","description":"d","category":"c","tags":[],"connectors":[],"params":[
	 {"name":"sheet_range","type":"string","title":"Tab","description":"d","example":"A","picker":"sheet_tab"}],
	 "definition":{"schema":"wd/v1","id":"wf_x","version":1,"name":"X","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":{"r":"{{sheet_range}}"}}}]}}`)
	if _, err := templates.Load(fstest.MapFS{"library/x.json": {Data: bad}}); err == nil || !strings.Contains(err.Error(), "picker") {
		t.Errorf("a tab picker without its sheet: %v", err)
	}
}

// Every template's details can be filled in by an owner (no-technical-steps
// N8): each is a number, a yes or no, a time, a day, a connection, words
// whose title and description ask nothing technical, or chosen with a
// picker. Templates another system starts (a webhook) need a developer
// anyway and are left out.
func TestEveryDetailIsOwnerShaped(t *testing.T) {
	technical := regexp.MustCompile(`(?i)\b(id|ids|A1|range|kobo|RCP_|recipient code|without \+|schema|expression|cron|json|body variables?|language code|schema\.table)\b|\{\{`)
	for _, tp := range templates.Default().List() {
		var head struct {
			Trigger struct {
				Type string `json:"type"`
			} `json:"trigger"`
		}
		if err := json.Unmarshal(tp.Definition, &head); err != nil {
			t.Fatal(err)
		}
		if head.Trigger.Type == "webhook" {
			continue
		}
		for _, p := range tp.Params {
			if technical.MatchString(p.Title) || technical.MatchString(p.Description) {
				t.Errorf("%s: %s asks in technical words: %q / %q", tp.ID, p.Name, p.Title, p.Description)
			}
			if p.Type != "string" || p.Picker != "" || len(p.Enum) > 0 {
				continue
			}
			// Free words: no format to get right.
			if p.Pattern != "" {
				t.Errorf("%s: %s is typed but must match %s; give it a picker", tp.ID, p.Name, p.Pattern)
			}
		}
		// A run form asks nothing technical either.
		if strings.Contains(string(tp.Definition), "recipient_code") && !strings.Contains(string(tp.Definition), `"format": "paystack-recipient"`) && head.Trigger.Type == "manual" {
			t.Errorf("%s: its run form asks for a recipient code", tp.ID)
		}
	}
}
