package remita

import (
	"net/url"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

// Remita's documented notification body is a JSON array of payments: each
// is its own event, and the URL's token authenticates the delivery.
func TestNotificationExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["invoice_payment"]
	body, err := expr.DecodeJSON([]byte(`[{"rrr":"110002071256","channel":"CARDPAYMENT","amount":650000.00,"orderId":"6954148807","type":"PY"},
	  {"rrr":"110002071257","channel":"BANKBRANCH","amount":1200.50,"orderId":"6954148808","type":"PY"}]`))
	if err != nil {
		t.Fatal(err)
	}
	e := expr.MustNewWithRoots("body", "headers", "query", "item")
	act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}, "item": nil}
	v, err := e.Eval(spec.Split, act)
	list, _ := v.([]any)
	if err != nil || len(list) != 2 {
		t.Fatalf("split %v %v", v, err)
	}
	for i, want := range [][3]string{{"payment_notification", "110002071256", "6954148807"}, {"payment_notification", "110002071257", "6954148808"}} {
		act["item"] = list[i]
		for j, src := range []string{spec.EventType, spec.Dedup, spec.Correlation} {
			v, err := e.Eval(src, act)
			if s, _ := v.(string); err != nil || s != want[j] {
				t.Errorf("item %d: %s = %v (%v), want %s", i, src, v, err, want[j])
			}
		}
	}
	if spec.Ack == nil || spec.Ack.Body != "Ok" {
		t.Errorf("ack %+v", spec.Ack)
	}
	if err := connector.VerifyQuerySecret(spec.Verify, "tok-1", url.Values{"token": {"tok-1"}}); err != nil {
		t.Errorf("token: %v", err)
	}
	if connector.VerifyQuerySecret(spec.Verify, "tok-1", url.Values{"token": {"tok-2"}}) == nil || connector.VerifyQuerySecret(spec.Verify, "tok-1", url.Values{}) == nil {
		t.Error("a wrong or missing token was accepted")
	}
}
