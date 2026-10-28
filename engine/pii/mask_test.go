package pii

import (
	"reflect"
	"regexp"
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

// Object keys reach the model like values, so personal data in a key is
// masked too, and put back in the answer's keys.
func TestMaskerMasksKeys(t *testing.T) {
	taint := Taint{}
	taint.Add("Adaeze Okafor", "name")
	m := NewMasker(taint)
	in := map[string]any{"balances": map[string]any{"Adaeze Okafor": int64(5000), "ada@example.com": int64(20), "rice": int64(3)}}
	got := m.Mask(in).(map[string]any)["balances"].(map[string]any)
	want := map[string]any{"[[name_1]]": int64(5000), "[[email_1]]": int64(20), "rice": int64(3)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("masked keys: %v", got)
	}
	back := m.Unmask(map[string]any{"[[name_1]]": "owes", "Note for [[email_1]]": "late"}).(map[string]any)
	if back["Adaeze Okafor"] != "owes" || back["Note for ada@example.com"] != "late" {
		t.Errorf("unmasked keys: %v", back)
	}
	if m.Restored()["s:Adaeze Okafor"] != "name" {
		t.Errorf("restored: %v", m.Restored())
	}
}

// A known value shorter than MinRedact is masked inside text only as a
// whole word, and one or two characters never are.
func TestMaskerShortValues(t *testing.T) {
	taint := Taint{}
	taint.Add("Ada", "name")
	taint.Add("4821", "pin")
	taint.Add("Jo", "name")
	m := NewMasker(taint)
	got := m.Mask(map[string]any{
		"note":  "Ada (pin 4821) met Adaeze and Jo; ref 148210, Ada_x, Ada.",
		"short": "Jo",
	}).(map[string]any)
	if want := "[[name_1]] (pin [[pin_1]]) met Adaeze and Jo; ref 148210, Ada_x, [[name_1]]."; got["note"] != want {
		t.Errorf("note: %v, want %s", got["note"], want)
	}
	if got["short"] != "[[name_2]]" {
		t.Errorf("a short whole value: %v", got["short"])
	}
	// A known text built from a short known value is not hidden whole.
	built := Taint{}
	built.Add("Ada", "name")
	built.Add("Write a greeting for Ada", "name")
	if got := NewMasker(built).Mask("Write a greeting for Ada"); got != "Write a greeting for [[name_1]]" {
		t.Errorf("a composite text: %v", got)
	}
}

// Phone numbers are masked however their digits are grouped, and numbers
// in international form are phones too.
func TestMaskerPhones(t *testing.T) {
	m := NewMasker(Taint{})
	one := regexp.MustCompile(`^call \[\[phone_[0-9]+\]\] today$`)
	for _, phone := range []string{"+234 803 123 4567", "+234-803-123-4567", "(0803) 123 4567", "0803 123 4567", "0803.123.4567",
		"+234 (0)803 123 4567", "+44 20 7946 0958", "+254 712 345678", "+1 (415) 555-2671", "+447911123456"} {
		if got := m.MaskText("call " + phone + " today"); !one.MatchString(got) {
			t.Errorf("%s: %s", phone, got)
		}
	}
	if got := m.MaskText("call +44 20 7946 0958 about 5000"); !strings.HasSuffix(got, "]] about 5000") {
		t.Errorf("an amount after a phone: %s", got)
	}
}
