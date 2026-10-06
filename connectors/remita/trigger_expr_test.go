package remita

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Remita's documented notification body is a JSON array of payments.
func TestNotificationExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["invoice_payment"]
	body, err := expr.DecodeJSON([]byte(`[{"rrr":"110002071256","channel":"CARDPAYMENT","amount":650000.00,"orderId":"6954148807","type":"PY"}]`))
	if err != nil {
		t.Fatal(err)
	}
	e := expr.MustNewWithRoots("body", "headers", "query")
	act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
	for src, want := range map[string]string{spec.EventType: "payment_notification", spec.Dedup: "110002071256", spec.Correlation: "6954148807"} {
		v, err := e.Eval(src, act)
		if s, _ := v.(string); err != nil || s != want {
			t.Errorf("%s = %v (%v), want %s", src, v, err, want)
		}
	}
}
