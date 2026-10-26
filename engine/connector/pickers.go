package connector

import (
	"encoding/json"
	"fmt"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Choice is one entry a picker offers: the value a parameter takes, and
// what the person reads.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var pickerExprs = expr.MustNewWithRoots("output", "item")

// Choices reads a picker's action output as its choices.
func (m *Manifest) Choices(picker string, output any) ([]Choice, error) {
	pk, ok := m.Pickers[picker]
	if !ok {
		return nil, fmt.Errorf("%s has no picker %q", m.Name, picker)
	}
	// As the engine keeps outputs: plain JSON values, never Go structs.
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	if output, err = expr.DecodeJSON(raw); err != nil {
		return nil, err
	}
	v, err := pickerExprs.Eval(pk.Items, map[string]any{"output": output})
	if err != nil {
		return nil, fmt.Errorf("%s picker %s: items: %w", m.Name, picker, err)
	}
	items, _ := v.([]any)
	out := make([]Choice, 0, len(items))
	for _, it := range items {
		act := map[string]any{"output": output, "item": it}
		id, err := pickerExprs.Eval(pk.ID, act)
		if err != nil {
			return nil, fmt.Errorf("%s picker %s: id: %w", m.Name, picker, err)
		}
		label, err := pickerExprs.Eval(pk.Label, act)
		if err != nil {
			return nil, fmt.Errorf("%s picker %s: label: %w", m.Name, picker, err)
		}
		out = append(out, Choice{ID: fmt.Sprint(id), Label: fmt.Sprint(label)})
	}
	return out, nil
}
