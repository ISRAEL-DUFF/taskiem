package storefront_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// The forms of fronts F1: the built-in four check; their basket and time
// are widgets, not questions; a question may not take one of Taskiem's
// names, ask for a file, or come with a second basket.
func TestActionForms(t *testing.T) {
	for _, k := range storefront.Kinds {
		f := storefront.BuiltinForm(k)
		if err := storefront.CheckForm(f); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if q := storefront.FormFields(f); len(q) != 0 {
			t.Fatalf("%s: questions %v", k, q)
		}
	}
	if !storefront.HasWidget(storefront.BuiltinForm("order"), "basket") || !storefront.HasWidget(storefront.BuiltinForm("booking"), "time") ||
		storefront.HasWidget(storefront.BuiltinForm("enquiry"), "basket") {
		t.Fatal("the built-in widgets")
	}
	own := []byte(`{"type":"object","properties":{"basket":{"type":"array","x-form":{"widget":"basket","list":"menu"}},"spice":{"type":"string"},"note":{"type":"string"}}}`)
	if q := storefront.FormFields(own); !slices.Equal(q, []string{"note", "spice"}) {
		t.Fatalf("questions: %v", q)
	}
	if string(storefront.EffectiveForm(nil, "order")) != string(storefront.BuiltinForm("order")) || string(storefront.EffectiveForm(own, "order")) != string(own) {
		t.Fatal("the effective form")
	}
	for name, form := range map[string]string{
		"not an object":   `{"type":"array"}`,
		"a reserved name": `{"type":"object","properties":{"total_kobo":{"type":"integer"}}}`,
		"a file":          `{"type":"object","properties":{"photo":{"type":"string","format":"file"}}}`,
		"two baskets":     `{"type":"object","properties":{"a":{"type":"array","x-form":{"widget":"basket"}},"b":{"type":"array","x-form":{"widget":"basket"}}}}`,
		"a bad name":      `{"type":"object","properties":{"Spice Level":{"type":"string"}}}`,
	} {
		if err := storefront.CheckForm([]byte(form)); !errors.Is(err, storefront.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
