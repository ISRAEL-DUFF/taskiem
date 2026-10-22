package expansion_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
)

func registry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// payroll pays each person with pay_bank and waits for it to settle.
const payroll = `{
  "schema": "wd/v1", "id": "wf_pay", "version": 1, "name": "Pay",
  "trigger": {"type": "manual"},
  "steps": [
    {"id": "people", "type": "transform", "config": {"output": [{"id": "e1", "amount": 500000, "bank": "058", "account": "0123456789", "name": "Bola Ade"}]}},
    {"id": "pay_all", "type": "foreach", "needs": ["people"], "config": {"items": "=steps.people.output", "max_concurrency": 5, "steps": [
      {"id": "pay", "type": "capability", "uses": "pay_bank", "connection": "rail",
       "input": {"amount_kobo": "=item.amount", "bank_code": "=item.bank", "account_number": "=item.account", "account_name": "=item.name", "narration": "Salary"},
       "effect": {"idempotency_seed": "='run1:' + item.id"},
       "retry": {"max": 5}},
      {"id": "settle", "type": "capability", "uses": "await_settlement", "needs": ["pay"], "timeout": "72h",
       "when": "=steps.pay.output.status == 'pending'", "input": {"payout_id": "=steps.pay.output.payout_id"}}
    ]}}
  ]
}`

func expand(t *testing.T, doc string, conns ...expansion.Connection) (*wd.Definition, []expansion.Choice, error) {
	t.Helper()
	out, choices, err := expansion.Expand([]byte(doc), expansion.ResolverFor(conns, registry(t)))
	if err != nil {
		return nil, nil, err
	}
	d, err := wd.Load(out)
	if err != nil {
		t.Fatalf("the expansion does not load: %v\n%s", err, out)
	}
	return d, choices, nil
}

func TestExpandOnEachRail(t *testing.T) {
	for _, rail := range []struct{ id, action, ref string }{
		{"paystack", "transfer", "paystack@1"},
		{"iswallet", "payout", "iswallet@1"},
		{"moniepoint", "transfer", "moniepoint@1"},
		{"flutterwave", "transfer", "flutterwave@1"},
		{"anchor", "transfer", "anchor@1"},
	} {
		doc := payroll
		if rail.id == "iswallet" {
			doc = strings.Replace(doc, `"narration": "Salary"}`, `"narration": "Salary", "source": "w1"}`, 1)
		}
		d, choices, err := expand(t, doc, expansion.Connection{Name: "rail", Connector: rail.id})
		if err != nil {
			t.Fatalf("%s: %v", rail.id, err)
		}
		pay := d.Step("pay")
		if pay == nil || pay.Type != "connector" || pay.Connector != rail.ref || pay.Action != rail.action || pay.Connection != "rail" {
			t.Fatalf("%s: pay is %+v", rail.id, pay)
		}
		if pay.Capability == nil || pay.Capability.Uses != "pay_bank" || len(pay.Capability.Output) == 0 {
			t.Fatalf("%s: no capability mapping: %+v", rail.id, pay.Capability)
		}
		if pay.Input["currency"] != "NGN" || pay.Effect == nil || pay.Retry == nil {
			t.Errorf("%s: input, effect and retry are the step's: %v %+v %+v", rail.id, pay.Input, pay.Effect, pay.Retry)
		}
		if race := d.Step("settle__wait"); race == nil || race.Type != "parallel" || race.When == "" {
			t.Fatalf("%s: no race: %+v", rail.id, race)
		}
		if sig := d.Step("settle__settled"); sig == nil || sig.Signal.Event != rail.ref+":payout.settled" || sig.Signal.Correlation != "=steps.pay.output.payout_id" {
			t.Fatalf("%s: settled signal %+v", rail.id, sig)
		}
		if look := d.Step("settle__lookup"); look == nil || look.Connector != rail.ref || look.Capability.Uses != "get_payout" {
			t.Fatalf("%s: lookup %+v", rail.id, look)
		}
		if out := d.Step("settle"); out == nil || out.Type != "transform" {
			t.Fatalf("%s: settle %+v", rail.id, out)
		}
		if len(choices) != 2 || choices[0].Connector != rail.ref {
			t.Errorf("%s: choices %+v", rail.id, choices)
		}
	}
}

func TestPaystackIsTwoSteps(t *testing.T) {
	d, _, err := expand(t, payroll, expansion.Connection{Name: "rail", Connector: "paystack"})
	if err != nil {
		t.Fatal(err)
	}
	rec, pay := d.Step("pay__recipient"), d.Step("pay")
	if rec == nil || rec.Action != "create_recipient" || len(rec.Capability.Output) != 0 {
		t.Fatalf("recipient %+v", rec)
	}
	if rec.Effect == nil || rec.Effect.IdempotencySeed != pay.Effect.IdempotencySeed {
		t.Error("both steps keep the seed; their step ids make the keys differ")
	}
	if len(pay.Needs) != 1 || pay.Needs[0] != "pay__recipient" || pay.When != "" || pay.Capability.Steps["recipient"] != "pay__recipient" {
		t.Fatalf("transfer %+v", pay)
	}
	// The refused-to-the-rail inputs never reach it: Paystack takes no source.
	if _, ok := rec.Capability.Input["source"]; ok {
		t.Error("source mapped")
	}
}

func TestRefusals(t *testing.T) {
	for _, c := range []struct {
		name, doc string
		conns     []expansion.Connection
		want      string
	}{
		{"no connection", payroll, nil, `there is no connection called "rail"`},
		{"requires", payroll, []expansion.Connection{{"rail", "iswallet"}}, "iswallet needs the wallet or account to pay from (source) to pay a bank account"},
		{"unsupported", strings.Replace(payroll, `"narration": "Salary"}`, `"narration": "Salary", "source": "w1"}`, 1), []expansion.Connection{{"rail", "flutterwave"}}, "Flutterwave cannot take source"},
		{"fixed", strings.Replace(payroll, `"narration": "Salary"}`, `"narration": "Salary", "currency": "USD"}`, 1), []expansion.Connection{{"rail", "moniepoint"}}, "Moniepoint (Monnify) takes currency NGN only, not USD"},
		{"not a rail", payroll, []expansion.Connection{{"rail", "googlesheets"}}, "your Google Sheets connection cannot pay a bank account. Connect a rail that can, such as"},
		{"default needs one", strings.Replace(payroll, `"connection": "rail",`, "", 1), []expansion.Connection{{"a", "paystack"}, {"b", "anchor"}}, "several connections can pay a bank account (a, b)"},
	} {
		_, _, err := expand(t, c.doc, c.conns...)
		var ref *expansion.Refusal
		if !errors.As(err, &ref) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

func TestCannot(t *testing.T) {
	resolve := expansion.ResolverFor([]expansion.Connection{{Name: "rail", Connector: "paystack"}}, registry(t))
	_, err := resolve("rail", "virtual_account")
	if err == nil || !strings.Contains(err.Error(), "your Paystack connection cannot open a bank account number for a customer to pay into. Connect Moniepoint") {
		t.Fatalf("got %v", err)
	}
}

func TestDefaultConnection(t *testing.T) {
	doc := strings.Replace(payroll, `"connection": "rail",`, "", 1)
	d, _, err := expand(t, doc, expansion.Connection{Name: "sheets", Connector: "googlesheets"}, expansion.Connection{Name: "live", Connector: "anchor"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Step("pay").Connection != "live" || d.Step("settle__lookup").Connection != "live" {
		t.Fatal("the only capable connection is used, by the wait too")
	}
}

func TestPayWalletFinalHasNothingToWaitFor(t *testing.T) {
	doc := strings.NewReplacer(`"uses": "pay_bank"`, `"uses": "pay_wallet"`,
		`"bank_code": "=item.bank", "account_number": "=item.account", "account_name": "=item.name", `, `"wallet_id": "=item.account", "source": "w1", `).Replace(payroll)
	_, _, err := expand(t, doc, expansion.Connection{Name: "rail", Connector: "iswallet"})
	if err == nil || !strings.Contains(err.Error(), "nothing to wait for") {
		t.Fatalf("got %v", err)
	}
}

func TestNoCapabilitiesUnchanged(t *testing.T) {
	doc := `{"schema":"wd/v1","id":"wf_x","version":1,"name":"X","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":1}}]}`
	out, choices, err := expansion.Expand([]byte(doc), expansion.ResolverFor(nil, registry(t)))
	if err != nil || string(out) != doc || choices != nil {
		t.Fatalf("%s %v %v", out, choices, err)
	}
}

func TestOutcomeCompiles(t *testing.T) {
	d, _, err := expand(t, payroll, expansion.Connection{Name: "rail", Connector: "anchor"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d.Step("settle").Transform.Output)
	if !strings.Contains(string(b), "unconfirmed") {
		t.Fatalf("outcome %s", b)
	}
}

// A composite's earlier call handles its failure with a renamed copy of
// the step's on_error, reading that call's error.
func TestCompositeOnError(t *testing.T) {
	doc := strings.Replace(payroll, `"retry": {"max": 5}},`, `"retry": {"max": 5},
       "on_error": {"steps": [{"id": "note", "type": "transform", "config": {"output": {"why": "=steps.pay.error.message"}}},
                              {"id": "more", "type": "transform", "needs": ["note"], "config": {"output": "=steps[\"note\"].output"}}]}},`, 1)
	d, _, err := expand(t, doc, expansion.Connection{Name: "rail", Connector: "paystack"})
	if err != nil {
		t.Fatal(err)
	}
	rec := d.Step("pay__recipient")
	if rec.OnError == nil || len(rec.OnError.Steps) != 2 {
		t.Fatalf("recipient on_error %+v", rec.OnError)
	}
	note, more := rec.OnError.Steps[0], rec.OnError.Steps[1]
	b, _ := json.Marshal(note.Transform.Output)
	if note.ID != "note__recipient" || !strings.Contains(string(b), "steps.pay__recipient.error.message") {
		t.Errorf("note %s %s", note.ID, b)
	}
	b, _ = json.Marshal(more.Transform.Output)
	if more.ID != "more__recipient" || more.Needs[0] != "note__recipient" || !strings.Contains(string(b), `steps[\"note__recipient\"]`) {
		t.Errorf("more %s %v %s", more.ID, more.Needs, b)
	}
	if pay := d.Step("pay"); pay.OnError == nil || pay.OnError.Steps[0].ID != "note" {
		t.Error("the transfer keeps the on_error as written")
	}
}

// A watch over a list of earlier payouts names its connection, and blank
// inputs count as not given.
func TestWaitFromAListAndBlankInputs(t *testing.T) {
	doc := `{"schema":"wd/v1","id":"wf_w","version":1,"name":"w","trigger":{"type":"manual"},"steps":[
	  {"id":"watch","type":"foreach","config":{"items":"=trigger.body.payouts","steps":[
	    {"id":"back","type":"capability","uses":"await_reversal","connection":"rail","timeout":"35d","input":{"payout_id":"=item"}}]}},
	  {"id":"pay","type":"capability","uses":"pay_bank","connection":"rail","input":{"amount_kobo":100,"bank_code":"058","account_number":"0123456789","account_name":"A","narration":"n","source":""}}]}`
	d, _, err := expand(t, doc, expansion.Connection{Name: "rail", Connector: "paystack"})
	if err != nil {
		t.Fatal(err)
	}
	if sig := d.Step("back__reversed"); sig == nil || sig.Signal.Event != "paystack@1:payout.reversed" || sig.Signal.Correlation != "=item" {
		t.Fatalf("watch %+v", sig)
	}
	if _, ok := d.Step("pay").Input["source"]; ok {
		t.Error("a blank source is given")
	}
}
