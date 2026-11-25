package builder_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

// customerTool is a customers' tool whose one public output is read from
// steps (and whatever those read).
func customerTool(steps string) string {
	return `{"schema":"wd/v1","id":"wf_ready","version":1,"name":"Is it ready","trigger":{"type":"manual"},
	 "inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","title":"Order"}}}},
	 "outputs":[{"name":"shown","type":"string","title":"Shown","description":"What the customer sees","value":"=string(steps.pick.output.shown)","public":true}],
	 "tool":{"name":"Is it ready","command":"is-it-ready","about":"Where your order is","for":"customers","answer":{"type":"text","text":"=outputs.shown"}},
	 "steps":[` + steps + `]}`
}

// A public output of a customers' tool is followed back through every
// step its data passes, so a transform between a table's rows and the
// answer hides nothing (review M6, 1); a value from the customer's own
// words is fine.
func TestCustomerToolPrivateOutputFollowsTheData(t *testing.T) {
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	find := `{"id":"find","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"orders","limit":1}}`
	for name, c := range map[string]struct {
		steps, warn string
	}{
		"a table's row, through a transform": {find + `,{"id":"pick","type":"transform","needs":["find"],"config":{"output":{"shown":"=steps.find.output.rows[0].phone"}}}`, `step "find", whose output is personal data`},
		"through two transforms":             {find + `,{"id":"mid","type":"transform","needs":["find"],"config":{"output":{"x":"=steps.find.output.rows"}}},{"id":"pick","type":"transform","needs":["mid"],"config":{"output":{"shown":"=steps.mid.output.x"}}}`, `step "find"`},
		"another system's answer":            {`{"id":"ask","type":"http","config":{"method":"GET","url":"https://api.example.com/o"}},{"id":"pick","type":"transform","needs":["ask"],"config":{"output":{"shown":"=steps.ask.output.body"}}}`, `step "ask", which reads another system's answer`},
		"a saved thing":                      {`{"id":"pick","type":"transform","config":{"output":{"shown":"=saved.price_list.body"}}}`, `step "pick", which reads a saved thing`},
		"the customer's own words":           {`{"id":"pick","type":"transform","config":{"output":{"shown":"=trigger.body.ref + ' is ready'"}}}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			def, err := wd.Load([]byte(customerTool(c.steps)))
			if err != nil {
				t.Fatal(err)
			}
			var got string
			for _, f := range builder.PolicyFindings(def, reg) {
				if f.Rule == builder.RuleCustomerToolPrivate {
					got = f.Message
				}
			}
			if c.warn == "" && got != "" || c.warn != "" && !strings.Contains(got, c.warn) {
				t.Errorf("want %q, got %q", c.warn, got)
			}
		})
	}
}
