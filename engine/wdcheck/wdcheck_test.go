package wdcheck_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

func registry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const payrollaTrigger = `{"type":"connector_event","config":{"connector":"payrolla@1","trigger":"run_event","events":["run.completed"],"connection":"payrolla"}}`

func doc(trigger, steps string) []byte {
	return []byte(`{"schema":"wd/v1","id":"wf_t","version":1,"name":"T","trigger":` + trigger + `,"steps":` + steps + `}`)
}

// Template steps are checked as the template's steps (decision 0047).
func TestTemplateSteps(t *testing.T) {
	reg := registry(t)
	for name, c := range map[string]struct {
		doc        []byte
		path, want string
	}{
		"a callable template": {doc: doc(`{"type":"manual"}`, `[{"id":"inv","type":"template","template":"send-invoice","input":{"name":"Ada","amount_naira":5000,"for":"Rice"}}]`)},
		"an unknown template": {doc: doc(`{"type":"manual"}`, `[{"id":"x","type":"template","template":"nope"}]`), path: "/steps/x", want: `no template "nope"`},
		"a missing input":     {doc: doc(`{"type":"manual"}`, `[{"id":"inv","type":"template","template":"send-invoice","input":{"name":"Ada"}}]`), path: "/steps/inv", want: "needs amount_naira, for"},
		"origin by hand": {doc: doc(`{"type":"manual"}`, `[{"id":"t","type":"transform","origin":{"template":"pay-a-list","step":"p"},"config":{"output":{}}}]`),
			path: "/steps/t/origin", want: "set when a template step is expanded"},
		// Around a product, a template that only tells and records is
		// fine; one that pays is not (milestone R7).
		"around Payrolla, a summary": {doc: doc(payrollaTrigger, `[{"id":"sum","type":"template","template":"payrolla-payroll-summary","params":{"payrolla_connection":"payrolla","business_name":"Ada Foods"},"input":{"data":"=trigger.body.data"}}]`)},
		"around Payrolla, a payment": {doc: doc(payrollaTrigger, `[{"id":"pay","type":"template","template":"pay-a-list","input":{"batch":"=trigger.body.data.run.run_id"}}]`),
			path: "/steps/pay", want: "works around Payrolla, which makes the payments; the template pay-a-list moves money"},
	} {
		probs := wdcheck.Check(c.doc, reg)
		if c.want == "" {
			if len(probs) > 0 {
				t.Errorf("%s: %v", name, probs)
			}
			continue
		}
		found := false
		for _, p := range probs {
			found = found || p.Path == c.path && strings.Contains(p.Message, c.want)
		}
		if !found {
			t.Errorf("%s: %v, want %s: %q", name, probs, c.path, c.want)
		}
	}
}

// A connector's own check of a step's input runs at publish
// (connector.CheckInput): notify@1 tell's template must be one a Tell me
// message can carry, with exactly its variables.
func TestConnectorInputChecks(t *testing.T) {
	reg := registry(t)
	tell := func(input string) []byte {
		return doc(`{"type":"manual"}`, `[{"id":"x","type":"connector","connector":"schools@1","action":"daily_close","input":{}},
			{"id":"tell","type":"connector","connector":"notify@1","action":"tell","needs":["x"],"input":`+input+`}]`)
	}
	for name, c := range map[string]struct {
		doc  []byte
		want string
	}{
		"no template":         {doc: tell(`{"message":"Hello"}`)},
		"the close":           {doc: tell(`{"message":"=steps.x.output.text","template":"taskiem_daily_close","template_vars":{"day":"=steps.x.output.date","summary":"=steps.x.output.summary"}}`)},
		"a family's template": {doc: tell(`{"message":"Hello","template":"taskiem_receipt","template_vars":{}}`), want: `template "taskiem_receipt" is not one a Tell me message can carry: taskiem_daily_summary, taskiem_daily_close`},
		"a computed name":     {doc: tell(`{"message":"Hello","template":"=trigger.body.t"}`), want: "written out"},
		"a variable missing":  {doc: tell(`{"message":"Hello","template":"taskiem_daily_close","template_vars":{"day":"x"}}`), want: "needs template_vars.summary"},
		"a variable too many": {doc: tell(`{"message":"Hello","template":"taskiem_daily_close","template_vars":{"day":"x","summary":"y","link":"z"}}`), want: `has no variable "link"`},
		"vars alone":          {doc: tell(`{"message":"Hello","template_vars":{"day":"x"}}`), want: "template_vars needs template"},
	} {
		probs := wdcheck.Check(c.doc, reg)
		if c.want == "" {
			if len(probs) > 0 {
				t.Errorf("%s: %v", name, probs)
			}
			continue
		}
		found := false
		for _, p := range probs {
			found = found || p.Path == "/steps/tell/input" && strings.Contains(p.Message, c.want)
		}
		if !found {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}
