package ussd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ussd"
)

// A bill-payment menu: a choice that sets an input, an account number
// (personal, masked), an amount with a range and a CEL rule, an option
// shown only for large amounts, and a confirmation.
const billMenu = `{
  "service_code": "*384*123#",
  "screens": [
    {"id": "main", "type": "menu", "text": "Welcome to Acme", "input": "product", "options": [
      {"label": "Pay electricity", "next": "account", "value": "power"},
      {"label": "Pay water", "next": "account", "value": "water"},
      {"label": "Help", "next": "help"}]},
    {"id": "account", "type": "input", "text": "Enter meter number", "input": "account_number",
     "validate": {"pattern": "[0-9]{10}"}, "error": "A meter number is 10 digits.", "next": "amount"},
    {"id": "amount", "type": "input", "text": "Amount in naira", "input": "amount",
     "validate": {"type": "integer", "min": 100, "max": 50000, "when": "=trigger.body.product != 'water' || trigger.body.amount <= 20000"}, "next": "speed"},
    {"id": "speed", "type": "menu", "text": "Delivery", "input": "speed", "options": [
      {"label": "Standard", "next": "confirm", "value": "standard"},
      {"label": "Express", "next": "confirm", "value": "express", "when": "=trigger.body.amount >= 1000"}]},
    {"id": "confirm", "type": "confirm", "text": "Pay N{{amount}} for {{product}} to {{account_number}}?", "confirm_label": "Pay", "done": "Done. Ref {{reference}}. You will get an SMS."},
    {"id": "help", "type": "end", "text": "Call 0800 000 0000 for help."}
  ],
  "notify": {"sms": true, "completed": "Paid. Ref {{reference}}.", "failed": "Payment {{reference}} failed."}
}`

func mustParse(t *testing.T, doc string) *ussd.Menu {
	t.Helper()
	m, err := ussd.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheckAcceptsAGoodMenu(t *testing.T) {
	m := mustParse(t, billMenu)
	f := ussd.Fields{Known: true, Names: map[string]string{"product": "string", "account_number": "string", "amount": "integer", "speed": "string"}, Required: []string{"amount"}}
	if probs := ussd.Check(m, f); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
}

func TestCheckFindsProblems(t *testing.T) {
	long := strings.Repeat("x", 175)
	cases := []struct {
		name, doc, want string
		fields          ussd.Fields
	}{
		{"screen too long", `{"service_code":"*1#","screens":[{"id":"a","type":"menu","text":"` + long + `","options":[{"label":"Go","next":"c"}]},{"id":"c","type":"confirm","text":"ok?"}]}`, "the limit is 182", ussd.Fields{}},
		{"lower limit", `{"service_code":"*1#","max_chars":60,"screens":[{"id":"a","type":"menu","text":"` + strings.Repeat("y", 40) + `","options":[{"label":"Go on please","next":"c"}]},{"id":"c","type":"confirm","text":"ok?"}]}`, "the limit is 60", ussd.Fields{}},
		{"placeholder width", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"Name","input":"name","validate":{"max_length":150},"next":"c"},{"id":"c","type":"confirm","text":"Hello {{name}}, go on?"}]}`, "/screens/1/text", ussd.Fields{}},
		{"unreachable", `{"service_code":"*1#","screens":[{"id":"a","type":"menu","text":"Hi","options":[{"label":"Go","next":"c"}]},{"id":"c","type":"confirm","text":"ok?"},{"id":"lost","type":"end","text":"bye"}]}`, `"lost" cannot be reached`, ussd.Fields{}},
		{"bad regex", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"Code","input":"code","validate":{"pattern":"([0-9"},"next":"c"},{"id":"c","type":"confirm","text":"ok?"}]}`, "pattern does not compile", ussd.Fields{}},
		{"dangling next", `{"service_code":"*1#","screens":[{"id":"a","type":"menu","text":"Hi","options":[{"label":"Go","next":"nowhere"}]},{"id":"c","type":"confirm","text":"ok?"}]}`, `no screen "nowhere"`, ussd.Fields{}},
		{"no confirm", `{"service_code":"*1#","screens":[{"id":"a","type":"end","text":"Hi"}]}`, "no confirm screen", ussd.Fields{}},
		{"unknown placeholder", `{"service_code":"*1#","screens":[{"id":"a","type":"confirm","text":"Pay {{amount}}?"}]}`, "{{amount}} is not an input", ussd.Fields{}},
		{"reference too early", `{"service_code":"*1#","screens":[{"id":"a","type":"confirm","text":"Ref {{reference}}?"}]}`, "only once the request is confirmed", ussd.Fields{}},
		{"secret in condition", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"Pin","input":"pin","validate":{"when":"=secrets.pin == trigger.body.pin"},"next":"c"},{"id":"c","type":"confirm","text":"ok?"}]}`, "conditions may read only trigger.body", ussd.Fields{}},
		{"env in option", `{"service_code":"*1#","screens":[{"id":"a","type":"menu","text":"Hi","options":[{"label":"Go","next":"c","when":"=env.x == 'y'"}]},{"id":"c","type":"confirm","text":"ok?"}]}`, "conditions may read only", ussd.Fields{}},
		{"expression text", `{"service_code":"*1#","screens":[{"id":"a","type":"confirm","text":"=secrets.token"}]}`, "not expressions", ussd.Fields{}},
		{"non-ascii", `{"service_code":"*1#","screens":[{"id":"a","type":"confirm","text":"Pay ₦100?"}]}`, "plain ASCII", ussd.Fields{}},
		{"bad service code", `{"service_code":"384","screens":[{"id":"a","type":"confirm","text":"ok?"}]}`, "not a USSD code", ussd.Fields{}},
		{"duplicate id", `{"service_code":"*1#","screens":[{"id":"a","type":"menu","text":"Hi","options":[{"label":"Go","next":"a"}]},{"id":"a","type":"confirm","text":"ok?"}]}`, "already used", ussd.Fields{}},
		{"min over max", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"Amount","input":"amount","validate":{"type":"integer","min":10,"max":5},"next":"c"},{"id":"c","type":"confirm","text":"ok?"}]}`, "min is above max", ussd.Fields{}},
		{"not an input of the workflow", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"Amount","input":"amt","next":"c"},{"id":"c","type":"confirm","text":"ok?"}]}`, `"amt" is not a property`, ussd.Fields{Known: true, Names: map[string]string{"amount": "integer"}}},
		{"type mismatch", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"Amount","input":"amount","next":"c"},{"id":"c","type":"confirm","text":"ok?"}]}`, "collects text", ussd.Fields{Known: true, Names: map[string]string{"amount": "integer"}}},
		{"required not collected", `{"service_code":"*1#","screens":[{"id":"c","type":"confirm","text":"ok?"}]}`, `requires "amount"`, ussd.Fields{Known: true, Names: map[string]string{"amount": "integer"}, Required: []string{"amount"}}},
		{"notify without sms", `{"service_code":"*1#","screens":[{"id":"c","type":"confirm","text":"ok?"}],"notify":{"sms":false,"completed":"x"}}`, "set sms: true", ussd.Fields{}},
		{"options on input", `{"service_code":"*1#","screens":[{"id":"a","type":"input","text":"x","input":"x","next":"c","options":[{"label":"a","next":"c"}]},{"id":"c","type":"confirm","text":"ok?"}]}`, "input screens do not take options", ussd.Fields{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ussd.Parse([]byte(tc.doc))
			if err != nil {
				if strings.Contains(err.Error(), tc.want) {
					return
				}
				t.Fatal(err)
			}
			probs := ussd.Check(m, tc.fields)
			var all []string
			for _, p := range probs {
				all = append(all, p.String())
			}
			if !strings.Contains(strings.Join(all, "\n"), tc.want) {
				t.Fatalf("want %q in %v", tc.want, all)
			}
		})
	}
}

func walk(t *testing.T, m *ussd.Menu, text string) ussd.Result {
	t.Helper()
	return m.Walk(ussd.SplitPath(text))
}

func TestWalkOptionPaths(t *testing.T) {
	m := mustParse(t, billMenu)
	r := walk(t, m, "")
	if r.Kind != ussd.Continue || r.Text != "Welcome to Acme\n1. Pay electricity\n2. Pay water\n3. Help" {
		t.Fatalf("start: %+v", r)
	}
	r = walk(t, m, "3")
	if r.Kind != ussd.End || r.Text != "Call 0800 000 0000 for help." {
		t.Fatalf("help: %+v", r)
	}
	r = walk(t, m, "1")
	if r.Kind != ussd.Continue || r.Screen != "account" || !strings.HasSuffix(r.Text, "\n0. Back") {
		t.Fatalf("account: %+v", r)
	}
	r = walk(t, m, "1*0123456789*500")
	// Express is offered only from 1,000.
	if r.Screen != "speed" || strings.Contains(r.Text, "Express") {
		t.Fatalf("speed for 500: %q", r.Text)
	}
	r = walk(t, m, "1*0123456789*5000*2")
	if r.Screen != "confirm" || r.Text != "Pay N5000 for power to ****6789?\n1. Pay\n2. Cancel\n0. Back" {
		t.Fatalf("confirm: %q", r.Text)
	}
	r = walk(t, m, "1*0123456789*5000*2*1")
	if r.Kind != ussd.Confirmed {
		t.Fatalf("confirmed: %+v", r)
	}
	want := map[string]any{"product": "power", "account_number": "0123456789", "amount": int64(5000), "speed": "express"}
	for k, v := range want {
		if r.Inputs[k] != v {
			t.Errorf("input %s = %#v, want %#v", k, r.Inputs[k], v)
		}
	}
	if got := m.Render(r.Text, r.Inputs, "AB12CD34"); got != "Done. Ref AB12CD34. You will get an SMS." {
		t.Errorf("done: %q", got)
	}
	r = walk(t, m, "1*0123456789*5000*2*2")
	if r.Kind != ussd.End || r.Text != ussd.DefaultCancelled {
		t.Fatalf("cancel: %+v", r)
	}
}

func TestWalkInvalidInputRePrompts(t *testing.T) {
	m := mustParse(t, billMenu)
	r := walk(t, m, "7")
	if r.Kind != ussd.Continue || !r.Invalid || !strings.HasPrefix(r.Text, ussd.DefaultChoiceError+"\nWelcome") {
		t.Fatalf("bad choice: %+v", r)
	}
	r = walk(t, m, "1*12345")
	if !r.Invalid || r.Screen != "account" || !strings.HasPrefix(r.Text, "A meter number is 10 digits.\nEnter meter number") {
		t.Fatalf("bad meter: %+v", r)
	}
	// The error shows once: a correct retry moves on.
	r = walk(t, m, "1*12345*0123456789")
	if r.Invalid || r.Screen != "amount" {
		t.Fatalf("retry: %+v", r)
	}
	for _, bad := range []string{"50", "60000", "12.5", "abc"} {
		if r = walk(t, m, "1*0123456789*"+bad); !r.Invalid || r.Screen != "amount" {
			t.Errorf("amount %s accepted: %+v", bad, r)
		}
	}
	// The CEL rule: water at most 20,000.
	if r = walk(t, m, "2*0123456789*30000"); !r.Invalid {
		t.Errorf("water 30000 accepted")
	}
	if r = walk(t, m, "1*0123456789*30000"); r.Invalid {
		t.Errorf("power 30000 refused")
	}
	if r = walk(t, m, "1*0123456789*5000*2*9"); !r.Invalid || r.Screen != "confirm" {
		t.Errorf("confirm accepted 9: %+v", r)
	}
}

func TestWalkBackAndRestart(t *testing.T) {
	m := mustParse(t, billMenu)
	r := walk(t, m, "1*0123456789*0")
	if r.Screen != "account" {
		t.Fatalf("back: %+v", r)
	}
	if _, ok := r.Inputs["account_number"]; ok {
		t.Errorf("back kept the account number")
	}
	r = walk(t, m, "1*0123456789*0*0")
	if r.Screen != "main" || len(r.Inputs) != 0 {
		t.Fatalf("back twice: %+v", r)
	}
	r = walk(t, m, "2*0123456789*00")
	if r.Screen != "main" || len(r.Inputs) != 0 || strings.Contains(r.Text, "0. Back") {
		t.Fatalf("home: %+v", r)
	}
	r = walk(t, m, "2*0123456789*00*1*1111111111*700*1*1")
	if r.Kind != ussd.Confirmed || r.Inputs["product"] != "power" || r.Inputs["account_number"] != "1111111111" {
		t.Fatalf("after home: %+v", r)
	}
	// Back on the first screen is just an invalid choice.
	if r = walk(t, m, "0"); !r.Invalid || r.Screen != "main" {
		t.Errorf("back on start: %+v", r)
	}
}

func TestWalkLimits(t *testing.T) {
	m := mustParse(t, billMenu)
	if r := m.Walk(make([]string, ussd.MaxDepth+1)); r.Kind != ussd.End {
		t.Errorf("an endless session continued: %+v", r)
	}
	a, b := ussd.Reference("t", "p", "s1"), ussd.Reference("t", "p", "s1")
	if a != b || ussd.Reference("t", "p", "s1") == ussd.Reference("t", "p", "s2") || len(ussd.Reference("t", "p", "s")) != ussd.ReferenceLen {
		t.Errorf("references are not stable and distinct")
	}
	if ussd.Mask("+2348031234567") != "****4567" || ussd.Mask("12345") != "****" {
		t.Errorf("mask")
	}
}

func TestCheckRequest(t *testing.T) {
	ok := ussd.Request{SessionID: "ATUid_1", ServiceCode: "*384*123#", Phone: "+254711000000", Input: "1*2"}
	if err := ussd.CheckRequest(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ussd.Request{
		{SessionID: "", ServiceCode: "*1#", Phone: "+254711000000"},
		{SessionID: "x y", ServiceCode: "*1#", Phone: "+254711000000"},
		{SessionID: "s", ServiceCode: "1", Phone: "+254711000000"},
		{SessionID: "s", ServiceCode: "*1#", Phone: "hello"},
		{SessionID: "s", ServiceCode: "*1#", Phone: "+254711000000", Input: strings.Repeat("1*", 300)},
	} {
		if ussd.CheckRequest(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
