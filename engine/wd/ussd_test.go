package wd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

const ussdFlow = `{"schema":"wd/v1","id":"wf_airtime","version":1,"name":"Airtime","trigger":{"type":"ussd","config":{
  "service_code":"*384*55#",
  "screens":[
    {"id":"main","type":"menu","text":"Buy airtime","input":"network","options":[{"label":"MTN","next":"amount","value":"mtn"},{"label":"Airtel","next":"amount","value":"airtel"}]},
    {"id":"amount","type":"input","text":"Amount","input":"amount","validate":{"type":"integer","min":50,"max":5000},"next":"ok"},
    {"id":"ok","type":"confirm","text":"Buy N{{amount}} {{network}} airtime?"}],
  "notify":{"sms":true}}},
  "inputs":{"schema":{"$ref":"#/types/Req"}},
  "types":{"Req":{"type":"object","required":["network","amount"],"properties":{"network":{"type":"string"},"amount":{"type":"integer"}}}},
  "steps":[{"id":"t","type":"transform","config":{"output":{"amount":"=trigger.body.amount"}}}]}`

func TestUSSDTriggerValidates(t *testing.T) {
	if probs := wd.Validate([]byte(ussdFlow)); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	d, err := wd.Load([]byte(ussdFlow))
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.USSDMenu()
	if err != nil || m.ServiceCode != "*384*55#" || !m.SMS() {
		t.Fatalf("menu: %+v %v", m, err)
	}
}

func TestUSSDTriggerProblems(t *testing.T) {
	cases := map[string]struct{ from, to, want string }{
		"config required":    {`"trigger":{"type":"ussd","config":{`, `"trigger":{"type":"ussd","x":{`, "schema"},
		"unknown field":      {`"notify":{"sms":true}`, `"notify":{"sms":true},"colour":"red"`, "schema"},
		"too many options":   {`{"label":"MTN","next":"amount","value":"mtn"}`, strings.Repeat(`{"label":"MTN","next":"amount"},`, 9) + `{"label":"MTN","next":"amount"}`, "schema"},
		"menu path":          {`"text":"Buy airtime"`, `"text":"` + strings.Repeat("a", 180) + `"`, "/trigger/config/screens/0/text"},
		"schema mismatch":    {`"input":"amount","validate":{"type":"integer","min":50,"max":5000}`, `"input":"amount"`, "collects text"},
		"secret in screen":   {`"text":"Amount"`, `"text":"=secrets.key"`, "/trigger"},
		"unreachable screen": {`"next":"ok"}`, `"next":"ok"},{"id":"lost","type":"end","text":"x"}`, "cannot be reached"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc := strings.Replace(ussdFlow, tc.from, tc.to, 1)
			if doc == ussdFlow {
				t.Fatal("replacement did not apply")
			}
			probs := wd.Validate([]byte(doc))
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
