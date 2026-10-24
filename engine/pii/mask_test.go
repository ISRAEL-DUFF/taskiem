package pii

import (
	"reflect"
	"strings"
	"testing"
)

// A masker swaps the run's personal values and detected ones for stable
// placeholders, and swaps them back in an answer (decision 0050).
func TestMasker(t *testing.T) {
	taint := Taint{}
	taint.Add("Adaeze Okafor", "name")
	taint.Add(int64(22123456789), "bvn")
	m := NewMasker(taint)
	in := map[string]any{
		"customer": "Adaeze Okafor",
		"note":     "Call Adaeze Okafor on 08031234567 or mail ada@example.com about card 4111 1111 1111 1111",
		"bvn":      int64(22123456789),
		"phone":    "+2348031234567",
		"amount":   int64(5000),
		"items":    []any{"rice", "Adaeze Okafor"},
	}
	got := m.Mask(in).(map[string]any)
	if got["customer"] != "[[name_1]]" || got["items"].([]any)[1] != "[[name_1]]" || got["bvn"] != "[[bvn_1]]" || got["amount"] != int64(5000) {
		t.Fatalf("masked: %v", got)
	}
	if got["phone"] != "[[phone_1]]" {
		t.Errorf("a phone field: %v", got["phone"])
	}
	note := got["note"].(string)
	for _, gone := range []string{"Adaeze", "08031234567", "ada@example.com", "4111"} {
		if strings.Contains(note, gone) {
			t.Errorf("the note still has %s: %s", gone, note)
		}
	}
	if !strings.Contains(note, "[[name_1]]") || !strings.Contains(note, "[[email_1]]") || !strings.Contains(note, "[[card_1]]") {
		t.Errorf("the note's placeholders: %s", note)
	}
	if in["customer"] != "Adaeze Okafor" {
		t.Error("Mask changed its input")
	}
	// The answer gets the values back, a number as a number.
	answer := map[string]any{"who": "[[name_1]]", "bvn": "[[bvn_1]]", "summary": "Pay [[name_1]] by [[phone_1]]; [[other_9]] stays", "ok": true}
	back := m.Unmask(answer).(map[string]any)
	want := map[string]any{"who": "Adaeze Okafor", "bvn": int64(22123456789), "summary": "Pay Adaeze Okafor by +2348031234567; [[other_9]] stays", "ok": true}
	if !reflect.DeepEqual(back, want) {
		t.Errorf("unmasked: %v", back)
	}
	if r := m.Restored(); r["s:Pay Adaeze Okafor by +2348031234567; [[other_9]] stays"] != "name" || r["n:22123456789"] != "bvn" {
		t.Errorf("restored: %v", r)
	}
	if m.Masked() < 5 {
		t.Errorf("masked %d values", m.Masked())
	}
	// A known text built from a known value is not hidden whole: the value
	// inside it is.
	built := Taint{}
	built.Add("Adaeze Okafor", "name")
	built.Add("Write a greeting for Adaeze Okafor", "name")
	bm := NewMasker(built)
	if got := bm.Mask("Write a greeting for Adaeze Okafor"); got != "Write a greeting for [[name_1]]" {
		t.Errorf("a composite text: %v", got)
	}
	// Nothing personal, nothing changed.
	plain := NewMasker(Taint{})
	if v := plain.Mask(map[string]any{"text": "Send 5000 to the supplier"}); v.(map[string]any)["text"] != "Send 5000 to the supplier" || plain.Masked() != 0 {
		t.Errorf("plain: %v", v)
	}
}
