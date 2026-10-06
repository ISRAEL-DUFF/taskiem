package opay

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/expr"
)

func TestCallbackExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["payment"]
	body, err := expr.DecodeJSON([]byte(docCallback))
	if err != nil {
		t.Fatal(err)
	}
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
	for src, want := range map[string]string{spec.EventType: "transaction-status", spec.Correlation: "10023",
		spec.Dedup: "9f605d69f04e94172875dc156537071cead060bbcaeaca94a7b8805af9f89611e2fdf6836713c9c90b028ca7e4470b1356e996975f2abc862315aaa9b7f2ae2d"} {
		v, err := e.Eval(src, act)
		if s, _ := v.(string); err != nil || s != want {
			t.Errorf("%s = %v (%v), want %s", src, v, err, want)
		}
	}
}
