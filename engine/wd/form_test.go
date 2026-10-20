package wd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

const formTrigger = `{"schema":"wd/v1","id":"wf_loan","version":1,"name":"Loan",
 "trigger":{"type":"form","config":{"title":"Apply for a loan","access":"public","verify":"phone",
  "pages":[{"title":"About you","fields":["full_name","phone","bvn"]},{"title":"The loan","fields":["amount_naira","employed"]},
           {"title":"Your employer","fields":["employer","payslip"],"show_if":"answers.employed == true"}],
  "after":{"mode":"message","message":"Thank you."}}},
 "inputs":{"schema":{"type":"object","required":["full_name","phone","amount_naira"],"properties":{
  "full_name":{"type":"string","title":"Full name"},"phone":{"type":"string","format":"phone-ng","x-pii":"phone"},
  "bvn":{"type":"string","pattern":"^[0-9]{11}$","x-pii":"bvn"},"amount_naira":{"type":"integer","minimum":10000},
  "employed":{"type":"boolean"},"employer":{"type":"string"},"payslip":{"type":"string","format":"file","x-form":{"max_mb":5}},
  "source":{"type":"string","x-form":{"hidden":true,"prefill":true}}}}},
 "steps":[{"id":"ask","type":"form","config":{"title":"Confirm your bank","to":{"phone":"=trigger.body.phone"},"via":["sms"],"verify":true,
   "schema":{"type":"object","required":["bank"],"properties":{"bank":{"type":"string","title":"=not an expression"},"account_number":{"type":"string","x-pii":"account_number"}}},
   "prefill":{"bank":"=trigger.body.employer"},"timeout":"72h","remind":["24h","60h"],"on_timeout":"continue"}}]}`

func TestFormsValidate(t *testing.T) {
	if p := wd.Validate([]byte(formTrigger)); len(p) != 0 {
		t.Fatalf("a good form: %v", p)
	}
	d, err := wd.Load([]byte(formTrigger))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := d.FormTriggerConfig()
	if f.Access != "public" || len(f.Pages) != 3 || d.Steps[0].Form.To.Phone != "=trigger.body.phone" || d.Steps[0].Form.Schema["required"] == nil {
		t.Fatalf("parsed: %+v %+v", f, d.Steps[0].Form)
	}
	for _, c := range []struct{ from, to, want string }{
		{`"verify":"phone",`, ``, "may be asked only on a link or members form"},
		{`"x-pii":"bvn"`, `"x-form":{}`, "mark it with its x-pii category"},
		{`"employed":{"type":"boolean"}`, `"employed":{"type":"boolean"},"card_number":{"type":"string"}`, "never asks for passwords"},
		{`"fields":["employer","payslip"]`, `"fields":["employer"]`, `"payslip" is on no page`},
		{`"fields":["amount_naira","employed"]`, `"fields":["amount_naira","employed","full_name"]`, "is on page 1 already"},
		{`answers.employed == true`, `answers.employer == 'x'`, "not on an earlier page"},
		{`answers.employed == true`, `answers.employed ==`, "does not compile"},
		{`"title":"Apply for a loan",`, ``, "missing"},
		{`"to":{"phone":"=trigger.body.phone"}`, `"to":{"phone":"=trigger.body.phone","email":"a@b.c"}`, "/to"},
		{`"remind":["24h","60h"]`, `"remind":["80h"]`, "shorter than the timeout"},
		{`"pin":`, `"pin":`, ""},
		{`"bank":{"type":"string","title":"=not an expression"}`, `"pin":{"type":"string"}`, "never asks for passwords"},
		{`"max_mb":5`, `"max_mb":50`, "at most 25 MB"},
	} {
		if c.want == "" {
			continue
		}
		doc := strings.Replace(formTrigger, c.from, c.to, 1)
		if doc == formTrigger {
			t.Fatalf("%q not in the fixture", c.from)
		}
		p := wd.Validate([]byte(doc))
		if !strings.Contains(fmtProblems(p), c.want) {
			t.Errorf("%s → %s: %v, want %q", c.from, c.to, p, c.want)
		}
	}
}

func fmtProblems(ps []wd.Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Path + ": " + p.Message + "\n")
	}
	return b.String()
}
