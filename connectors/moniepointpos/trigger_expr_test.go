package moniepointpos

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/expr"
)

func TestTransactionExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["transaction"]
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	eval := func(raw, src string) string {
		t.Helper()
		body, err := expr.DecodeJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		v, err := e.Eval(src, map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}})
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		s, _ := v.(string)
		return s
	}
	// The documentation's example: a walk-up transfer, no merchant reference.
	for src, want := range map[string]string{spec.EventType: "V1_POS_TRANSFER_TRANSACTION", spec.Dedup: "7a35b4d4-f985-4c14-846a-173af3fa677c", spec.Correlation: ""} {
		if got := eval(docEvent, src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
	// A transaction that answers a payment request correlates with it.
	pushed := strings.Replace(docEvent, `"merchantReference": null`, `"merchantReference": "`+ref+`"`, 1)
	if got := eval(pushed, spec.Correlation); got != ref {
		t.Errorf("correlation = %q, want the merchant reference", got)
	}
	// And one with no merchantReference field at all.
	absent := strings.Replace(docEvent, `"merchantReference": null, `, "", 1)
	if got := eval(absent, spec.Correlation); got != "" {
		t.Errorf("correlation without the field = %q", got)
	}
	for _, ev := range []string{"V1_POS_PURCHASE_TRANSACTION", "V1_POS_WITHDRAWAL_TRANSACTION", "V1_POS_CARD_TRANSFER_TRANSACTION", "V1_POS_TRANSFER_TRANSACTION", "V1_TRANSFER_TRANSACTION"} {
		found := false
		for _, s := range spec.Events {
			found = found || s == ev
		}
		if !found {
			t.Errorf("the trigger does not list %s", ev)
		}
	}
}
