package whatsapp

import (
	"encoding/json"
	"errors"
	"testing"
)

// ToolFields are the required fields, then the optional ones a chat can
// carry, by name; an optional field no chat carries is left out, never a
// reason to refuse (build plan H6, g-ct-chip). InputFields stays the
// required ones.
func TestToolFields(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["what","amount"],"properties":{
		"amount":{"type":"number"},"what":{"type":"string"},
		"where":{"type":"string","title":"Where"},"paid":{"type":"boolean"},"kind":{"enum":["a","b"]},
		"photo":{"type":"object"},"tags":{"type":"array"}}}`)
	got, err := ToolFields(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
		if f.Required != (f.Name == "what" || f.Name == "amount") {
			t.Errorf("%s required: %v", f.Name, f.Required)
		}
	}
	if s, _ := json.Marshal(names); string(s) != `["what","amount","kind","paid","where"]` {
		t.Errorf("fields: %s", s)
	}
	req, err := InputFields(schema, nil)
	if err != nil || len(req) != 2 {
		t.Errorf("required: %v %v", req, err)
	}
	// A required field no chat carries still refuses.
	if _, err := ToolFields(json.RawMessage(`{"type":"object","required":["photo"],"properties":{"photo":{"type":"object"}}}`), nil); !errors.Is(err, ErrInputsUnsupported) {
		t.Errorf("required object: %v", err)
	}
}
