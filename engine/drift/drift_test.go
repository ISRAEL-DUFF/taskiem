package drift

import (
	"testing"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/engine/connector"
)

var m = iswallet.New(iswallet.Options{}).Manifest

func TestConformingOutputHasNoFindings(t *testing.T) {
	out := map[string]any{"wallet_id": "6f1e", "balances": []any{
		map[string]any{"currency": "NGN", "total": int64(5), "available": 5, "pending": 0, "scale": 2},
	}}
	if f := Check(m, "get_balance", out); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
	if f := Check(m, "payout", map[string]any{"outflow_id": "8d3f", "status": "pending", "fee": 5350.0}); len(f) != 0 {
		t.Errorf("payout: %v", f)
	}
}

func TestDriftIsFound(t *testing.T) {
	out := map[string]any{"wallet_id": "6f1e", "balances": []any{
		map[string]any{"currency": "NGN", "total": "5", "available": 5.5, "scale": 2},
		map[string]any{"currency": "USDT", "total": "7", "available": 1, "scale": 6},
	}}
	got := Check(m, "get_balance", out)
	want := []Finding{
		{Path: "/balances/*/available", Kind: KindType, Expected: "integer", Observed: "number"},
		{Path: "/balances/*/total", Kind: KindType, Expected: "integer", Observed: "string"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v", got)
	}
}

func TestNewEnumValueIsShownWhenSafe(t *testing.T) {
	got := Check(m, "payout", map[string]any{"outflow_id": "8d3f", "status": "settled"})
	if len(got) != 1 || got[0].Kind != KindEnum || got[0].Observed != `"settled"` || got[0].Path != "/status" {
		t.Errorf("got %v", got)
	}
	// A personal value is never copied into a finding.
	got = Check(m, "payout", map[string]any{"outflow_id": "8d3f", "status": "ada@example.com"})
	if len(got) != 1 || got[0].Observed != "a string (not shown)" {
		t.Errorf("personal: %v", got)
	}
}

func TestRequiredAndRefs(t *testing.T) {
	man, problems := connector.Parse([]byte(`manifest: connector/v1
id: x_t
version: 1.0.0
name: T
category: payments
auth: { type: none }
base_url: https://t.example
actions:
  pay:
    title: Pay
    class: read
    input: { type: object, properties: {} }
    output:
      type: object
      required: [id, status]
      properties:
        id: { type: string }
        status: { type: string, enum: [ok, failed] }
  get:
    title: Get
    class: read
    input: { type: object, properties: {} }
    output: { $ref: '#/actions/pay/output' }
`))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	got := Check(man, "get", map[string]any{"status": nil})
	if len(got) != 2 || got[0].Path != "/id" || got[0].Kind != KindMissing || got[1].Path != "/status" || got[1].Kind != KindType {
		t.Errorf("got %v", got)
	}
}
