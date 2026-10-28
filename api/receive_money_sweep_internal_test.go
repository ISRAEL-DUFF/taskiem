package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

func sweepRegistry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func sweepDef(t *testing.T, tenant uuid.UUID, set sweepSettings) *wd.Definition {
	t.Helper()
	doc, err := sweepDefinition(tenant, "w_1", withdrawTo{BankCode: "058", AccountNumber: "0123456789"}, set, defaultPayoutFeeKobo)
	if err != nil {
		t.Fatal(err)
	}
	def, err := wd.Load(doc)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// The sweep pays what the wallet holds less what is kept and the fee's
// room, at least NGN 1,000, to the account written in, with no approval
// (testdata/receive_money_sweep.test.json).
func TestSweepWorkflowCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/receive_money_sweep.test.json")
	if err != nil {
		t.Fatal(err)
	}
	var tt struct {
		Schema string        `json:"schema"`
		Note   string        `json:"note"`
		Cases  []wdtest.Case `json:"cases"`
	}
	if err := json.Unmarshal(raw, &tt); err != nil || tt.Schema != wdtest.Schema || len(tt.Cases) == 0 {
		t.Fatalf("%v %q %d", err, tt.Schema, len(tt.Cases))
	}
	reg := sweepRegistry(t)
	def := sweepDef(t, uuid.New(), sweepSettings{Mode: "weekly", Day: "friday", Time: "17:00", KeepKobo: 2_000_000})
	if cron := def.Trigger.Config["cron"]; cron != "0 17 * * 5" {
		t.Errorf("weekly schedule %v", cron)
	}
	// No approval step: the one payout the builder's policy check flags,
	// by the founder's choice, and the only one.
	if f := builder.PolicyFindings(def, reg); len(f) != 1 || f[0].Rule != builder.RulePaymentWithoutApproval || f[0].Path != "/steps/sweep" {
		t.Errorf("policy findings %v", f)
	}
	for _, c := range tt.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if r := wdtest.RunCase(def, reg, nil, c); !r.Passed() {
				t.Errorf("%s\n  trace:\n    %s\n  errors: %v", strings.Join(r.Failures, "\n"), strings.Join(r.Trace, "\n    "), r.Errors)
			}
		})
	}
}

// Above an amount: checked hourly at the business's own minute, and
// nothing is sent while the wallet holds that amount or less.
func TestSweepAboveAnAmount(t *testing.T) {
	tenant := uuid.New()
	reg := sweepRegistry(t)
	def := sweepDef(t, tenant, sweepSettings{Mode: "above", AboveKobo: 20_000_000})
	want := strings.Fields(def.Trigger.Config["cron"].(string))
	if len(want) != 5 || want[1] != "*" || want[0] != strings.Fields(sweepDef(t, tenant, sweepSettings{Mode: "above", AboveKobo: 30_000_000}).Trigger.Config["cron"].(string))[0] {
		t.Fatalf("hourly schedule %v", want)
	}
	balance := func(kobo int, sweep string) wdtest.Case {
		var c wdtest.Case
		if err := json.Unmarshal([]byte(`{"name":"b","trigger":{"scheduled_time":"2026-10-30T16:07:00Z"},
			"mocks":{"balance":{"output":{"available_kobo":`+itoa(kobo)+`,"currency":"NGN"}},
			"check_name":{"output":{"account_name":"ADA"}},"sweep":{"output":{"payout_id":"p","status":"settled"}},"tell_done":{"output":{"people":1}}},
			"expect":{"status":"completed","steps":{"sweep":"`+sweep+`"}}}`), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if r := wdtest.RunCase(def, reg, nil, balance(20_000_000, "skipped")); !r.Passed() {
		t.Errorf("at the amount: %v %v", r.Failures, r.Errors)
	}
	if r := wdtest.RunCase(def, reg, nil, balance(20_000_001, "completed")); !r.Passed() {
		t.Errorf("above the amount: %v %v", r.Failures, r.Errors)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestSweepSettingsRefused(t *testing.T) {
	for _, set := range []sweepSettings{
		{Mode: "daily"},
		{Mode: "weekly", Day: "someday", Time: "17:00"},
		{Mode: "weekly", Day: "friday", Time: "25:00"},
		{Mode: "above", AboveKobo: 50_000},
		{Mode: "off", KeepKobo: -1},
	} {
		if err := set.check(); err == nil {
			t.Errorf("%+v accepted", set)
		}
	}
	for _, set := range []sweepSettings{{Mode: "off"}, {Mode: "weekly", Day: "friday", Time: "17:00"}, {Mode: "above", AboveKobo: 100_000, KeepKobo: 5_000_000}} {
		if err := set.check(); err != nil {
			t.Errorf("%+v: %v", set, err)
		}
	}
}
