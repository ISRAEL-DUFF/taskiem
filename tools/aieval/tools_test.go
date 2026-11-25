package main

import (
	"strings"
	"testing"
)

const goodCustomerTool = `{"schema":"wd/v1","id":"wf_ready","version":1,"name":"Is it ready","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","title":"Order"}}}},
 "outputs":[{"name":"state","type":"string","title":"State","description":"Where the order is","value":"=steps.look.output.state","public":true},
            {"name":"cost","type":"integer","title":"Cost","description":"What it cost us","value":"=steps.look.output.cost"}],
 "tool":{"name":"Is it ready","command":"is-it-ready","about":"Where your order is","for":"customers","answer":{"type":"text","text":"=outputs.state"}},
 "steps":[{"id":"look","type":"transform","config":{"output":{"state":"=trigger.body.ref + ' is ready'","cost":100}}}]}`

const savedTool = `{"schema":"wd/v1","id":"wf_prices","version":1,"name":"Our prices","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{}}},
 "outputs":[{"name":"prices","type":"string","title":"Prices","description":"The price list","value":"=steps.read.output.prices","public":true}],
 "tool":{"name":"Our prices","command":"our-prices","about":"The price list","for":"customers","answer":{"type":"text","text":"=outputs.prices"}},
 "steps":[{"id":"read","type":"transform","config":{"output":{"prices":"=saved.price_list.body"}}}]}`

// The tool rules (build plan M6): a tool is declared; a customers' tool
// shows public outputs only and nothing the builder warns of; a saved
// thing is read by name.
func TestToolRules(t *testing.T) {
	reg := registry(t)
	good, saved, payout := mustLoad(t, goodCustomerTool), mustLoad(t, savedTool), mustLoad(t, goodPayout)
	for _, name := range []string{"tool_declared", "customer_tool_public"} {
		if ok, why := rules[name](good, reg); !ok {
			t.Errorf("%s fails the good tool: %s", name, why)
		}
		if ok, _ := rules[name](payout, reg); ok {
			t.Errorf("%s passes a payout that is no tool", name)
		}
	}
	if ok, why := rules["reads_saved"](saved, reg); !ok {
		t.Errorf("reads_saved: %s", why)
	}
	if ok, _ := rules["reads_saved"](good, reg); ok {
		t.Errorf("reads_saved passes a tool that reads none")
	}
	// For members, it is not a customers' tool.
	members := mustLoad(t, strings.Replace(goodCustomerTool, `"for":"customers"`, `"for":"members"`, 1))
	if ok, _ := rules["customer_tool_public"](members, reg); ok {
		t.Errorf("a members' tool passes as a customers' one")
	}
	// A public output copied from a saved thing: the builder warns the
	// reviewer, so the rule does not hold.
	if ok, why := rules["customer_tool_public"](saved, reg); ok || !strings.Contains(why, "saved thing") {
		t.Errorf("a saved thing shown to customers: %v %s", ok, why)
	}
}
