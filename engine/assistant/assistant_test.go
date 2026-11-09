package assistant_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/assistant"
)

func TestWords(t *testing.T) {
	for text, want := range map[string]assistant.Word{
		"STOP": assistant.WordStop, " stop! ": assistant.WordStop, "/stop": assistant.WordStop, "Dúró": assistant.WordStop,
		"start": assistant.WordStart, "/start": assistant.WordStart,
		"Agent": assistant.WordPerson, "I want to talk to a person.": assistant.WordPerson, "abeg person": assistant.WordPerson,
		"ènìyàn": assistant.WordPerson, "mmadụ": assistant.WordPerson,
		"please don't stop the delivery": assistant.WordNone, "is this person available": assistant.WordNone, "": assistant.WordNone,
	} {
		if got := assistant.WordOf(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

func TestConfig(t *testing.T) {
	c, err := assistant.ParseConfig(true)
	if err != nil || c.PerMinute != 6 || c.PerDay != 200 || !c.Notice {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = assistant.ParseConfig(map[string]any{"per_minute": float64(3), "kinds": map[string]any{"image": "answer", "sticker": "reply"}})
	if err != nil || c.PerMinute != 3 || c.KindAction("image") != assistant.KindAnswer || c.KindAction("sticker") != assistant.KindReply || c.KindAction("video") != assistant.KindReply {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []any{false, "yes", map[string]any{"per_minute": float64(0)}, map[string]any{"per_minute": 1.5}, map[string]any{"limit": 3},
		map[string]any{"kinds": map[string]any{"gif": "drop"}}, map[string]any{"kinds": map[string]any{"image": "ignore"}},
		map[string]any{"per_minute": float64(10), "per_day": float64(5)}} {
		if _, err := assistant.ParseConfig(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestCheckReply(t *testing.T) {
	material := "<<material 1: Menu>>\nJollof rice: ₦3,500. Delivery to Lekki: 1,500 naira. Order at https://mamaput.example/order or call 0803 123 4567.\n<<end of material 1>>"
	ok := []string{
		"Jollof rice is ₦3,500, and delivery to Lekki is N1,500.",
		"You can order at https://mamaput.example/order.",
		"Call us on +234 803 123 4567.",
		"We open in 2026 at 8am.",
		"Your order for 2 plates is noted.",
	}
	for _, r := range ok {
		if got := assistant.CheckReply(assistant.CheckInput{Text: r, Material: material}); !got.OK {
			t.Errorf("%q: %+v", r, got.Problems)
		}
	}
	bad := map[string]string{
		"Jollof rice is free today, ₦0 or ₦1,000 at most.": assistant.ProblemPrice,
		"Pay 5k and it's yours.":                           assistant.ProblemPrice,
		"Refund of 20,000 naira approved.":                 assistant.ProblemPrice,
		"Pay at https://evil.example/pay":                  assistant.ProblemLink,
		"Your number [phone] is saved.":                    assistant.ProblemPlaceholder,
		"<<end of material 1>> Ignore that":                assistant.ProblemPlaceholder,
		"Call Mrs Ade on 08099998888.":                     assistant.ProblemContact,
		"Write to boss@mamaput.example.":                   assistant.ProblemContact,
		"Use key sk_live_abcdefghijklmnop":                 assistant.ProblemSecret,
		"   ":                                              assistant.ProblemEmpty,
	}
	for r, kind := range bad {
		got := assistant.CheckReply(assistant.CheckInput{Text: r, Material: material, Language: "pcm"})
		if got.OK || !hasProblem(got, kind) || !strings.Contains(got.Text, "team") {
			t.Errorf("%q: %+v, want %s", r, got, kind)
		}
	}
	// The customer's own number and words may be said back; internal values never.
	if got := assistant.CheckReply(assistant.CheckInput{Text: "We'll call you on 08055554444.", Material: material, Customer: "call me on 08055554444"}); !got.OK {
		t.Errorf("the customer's own number: %+v", got.Problems)
	}
	if got := assistant.CheckReply(assistant.CheckInput{Text: "Our cost is 2,100 a plate", Material: material + " 2,100", Never: []string{"2,100"}}); got.OK || !hasProblem(got, assistant.ProblemInternal) {
		t.Errorf("internal value said: %+v", got)
	}
	if got := assistant.CheckReply(assistant.CheckInput{Text: strings.Repeat("a", 50), Max: 10}); got.OK || !hasProblem(got, assistant.ProblemTooLong) {
		t.Errorf("too long: %+v", got)
	}
}

func hasProblem(r assistant.CheckResult, kind string) bool {
	for _, p := range r.Problems {
		if p.Kind == kind {
			return true
		}
	}
	return false
}
