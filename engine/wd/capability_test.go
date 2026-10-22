package wd

import (
	"strings"
	"testing"
)

func capDoc(steps string) string {
	return `{"schema":"wd/v1","id":"wf_c","version":1,"name":"c","trigger":{"type":"manual"},"steps":[` + steps + `]}`
}

const goodPay = `{"id":"pay","type":"capability","uses":"pay_bank","connection":"rail",
  "input":{"amount_kobo":500000,"bank_code":"058","account_number":"0123456789","account_name":"=trigger.body.name","narration":"Salary"}}`

func TestCapabilitySteps(t *testing.T) {
	if probs := Validate([]byte(capDoc(goodPay + `,{"id":"settle","type":"capability","uses":"await_settlement","needs":["pay"],"timeout":"72h","input":{"payout_id":"=steps.pay.output.payout_id"}}`))); len(probs) > 0 {
		t.Fatal(probs)
	}
	for name, c := range map[string]struct{ steps, want string }{
		"unknown":        {`{"id":"x","type":"capability","uses":"pay_moon","input":{}}`, "schema"},
		"missing input":  {strings.Replace(goodPay, `"narration":"Salary"`, `"source":"w1"`, 1), "pay_bank needs narration"},
		"unknown input":  {strings.Replace(goodPay, `"narration":"Salary"`, `"narration":"Salary","memo":"x"`, 1), `pay_bank has no input "memo"`},
		"literal type":   {strings.Replace(goodPay, `"amount_kobo":500000`, `"amount_kobo":"lots"`, 1), "pay_bank input"},
		"zero amount":    {strings.Replace(goodPay, `"amount_kobo":500000`, `"amount_kobo":0`, 1), "pay_bank input"},
		"double under":   {strings.Replace(goodPay, `"id":"pay"`, `"id":"pay__x"`, 1), "may not contain __"},
		"long id":        {strings.Replace(goodPay, `"id":"pay"`, `"id":"`+strings.Repeat("p", 41)+`"`, 1), "at most 40"},
		"wait timeout":   {goodPay + `,{"id":"settle","type":"capability","uses":"await_settlement","needs":["pay"],"input":{"payout_id":"=steps.pay.output.payout_id"}}`, "needs a timeout"},
		"wait payout":    {goodPay + `,{"id":"settle","type":"capability","uses":"await_settlement","needs":["pay"],"timeout":"1h","input":{}}`, "needs payout_id"},
		"wait needs pay": {goodPay + `,{"id":"settle","type":"capability","uses":"await_settlement","timeout":"1h","input":{"payout_id":"=steps.pay.output.payout_id"}}`, "add it to needs"},
	} {
		probs := Validate([]byte(capDoc(c.steps)))
		var msgs []string
		for _, p := range probs {
			msgs = append(msgs, p.String())
		}
		if !strings.Contains(strings.Join(msgs, "\n"), c.want) {
			t.Errorf("%s: want %q in %v", name, c.want, msgs)
		}
	}
}
