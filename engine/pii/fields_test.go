package pii

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// SealFields seals a record's picked fields as a declared object's values
// (strings remembered, numbers sealed but not followed) and leaves the
// rest; it declines, sealing nothing, what is not records or too many.
func TestSealFields(t *testing.T) {
	phone := func(k string) bool { return k == "phone" || k == "balance" }
	in := map[string]any{"output": map[string]any{"rows": []any{
		map[string]any{"name": "Rice", "phone": "+2348031112222", "balance": float64(500)},
		map[string]any{"name": "Beans", "phone": nil},
	}}}
	c := &plainCipher{}
	taint := Taint{}
	out, ok, err := SealFields(context.Background(), c, nil, uuid.Nil, in, []string{"output", "rows"}, phone, "other", taint)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	rows := out.(map[string]any)["output"].(map[string]any)["rows"].([]any)
	r0 := rows[0].(map[string]any)
	if r0["name"] != "Rice" || !IsEnvelope(r0["phone"]) || !IsEnvelope(r0["balance"]) || r0["balance"].(map[string]any)[NoFollow] != true || rows[1].(map[string]any)["phone"] != nil {
		t.Errorf("sealed: %v", rows)
	}
	if _, ok := taint["s:+2348031112222"]; !ok || len(taint) != 1 || c.sealed != 2 {
		t.Errorf("taint %v, %d sealed", taint, c.sealed)
	}
	if in["output"].(map[string]any)["rows"].([]any)[0].(map[string]any)["phone"] != "+2348031112222" {
		t.Error("the value given was changed")
	}
	// One record.
	one := map[string]any{"output": map[string]any{"row": map[string]any{"name": "Ada", "phone": "08031112222"}}}
	if out, ok, _ := SealFields(context.Background(), c, nil, uuid.Nil, one, []string{"output", "row"}, phone, "other", Taint{}); !ok || !IsEnvelope(out.(map[string]any)["output"].(map[string]any)["row"].(map[string]any)["phone"]) {
		t.Errorf("one record: %v", out)
	}
	// Nothing there: nothing to do.
	if _, ok, _ := SealFields(context.Background(), c, nil, uuid.Nil, map[string]any{}, []string{"output", "rows"}, phone, "other", Taint{}); !ok {
		t.Error("a missing place")
	}
	// Not records, or more values than are sealed one by one: declined.
	if _, ok, _ := SealFields(context.Background(), c, nil, uuid.Nil, map[string]any{"rows": []any{"a"}}, []string{"rows"}, phone, "other", Taint{}); ok {
		t.Error("a list of strings")
	}
	var many []any
	for i := 0; i <= MaxSealedLeaves; i++ {
		many = append(many, map[string]any{"phone": "x", "name": "y"})
	}
	c = &plainCipher{}
	if _, ok, _ := SealFields(context.Background(), c, nil, uuid.Nil, map[string]any{"rows": many}, []string{"rows"}, phone, "other", Taint{}); ok || c.sealed != 0 {
		t.Errorf("too many: %v, %d sealed", ok, c.sealed)
	}
}
