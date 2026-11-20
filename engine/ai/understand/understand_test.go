package understand_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/understand"
	"github.com/israel-duff/taskiem/engine/lang"
)

func TestUnderstandSendsTheCommandsAndTheMessage(t *testing.T) {
	fake := &ai.Fake{Script: []ai.Response{{Text: `{"kind":"command","command":"*sold* 3 bags of rice 45,000 cash","reply":""}`}}}
	h := &understand.Helper{Provider: fake}
	en, _ := lang.Lookup(lang.EN)
	r, resp, err := h.Understand(context.Background(), "I just sold 3 bags of rice for 45k cash, call me 08031234567", "• *sold 3 bags of rice 45,000 cash*: record a sale", en)
	if err != nil || resp == nil || r.Kind != understand.Command || r.Command != "sold 3 bags of rice 45,000 cash" {
		t.Fatalf("%+v %v", r, err)
	}
	req := fake.Requests()[0]
	prompt := ai.PromptText(req)
	if !strings.Contains(prompt, "record a sale") || !strings.Contains(prompt, "English (en)") || req.Schema == nil {
		t.Fatalf("prompt: %s", prompt)
	}
	if strings.Contains(prompt, "08031234567") {
		t.Fatalf("phone number reached the model: %s", prompt)
	}
}

func TestParseRefusesWhatCannotBeUsed(t *testing.T) {
	long := strings.Repeat("a ", understand.MaxCommand)
	cases := []struct {
		answer string
		want   understand.Result
		bad    bool
	}{
		{`{"kind":"command","command":"  stock   rice ","reply":"x"}`, understand.Result{Kind: understand.Command, Command: "stock rice"}, false},
		{`{"kind":"ask","command":"","reply":"How much did Musa pay?"}`, understand.Result{Kind: understand.Ask, Reply: "How much did Musa pay?"}, false},
		{`{"kind":"cannot","command":"","reply":"I can't book flights."}`, understand.Result{Kind: understand.Cannot, Reply: "I can't book flights."}, false},
		{`{"kind":"command","command":"","reply":""}`, understand.Result{}, true},
		{`{"kind":"command","command":"` + long + `","reply":""}`, understand.Result{}, true},
		{`{"kind":"ask","command":"","reply":""}`, understand.Result{}, true},
		{`{"kind":"run","command":"x","reply":""}`, understand.Result{}, true},
		{`not json`, understand.Result{}, true},
	}
	for _, c := range cases {
		got, err := understand.Parse(&ai.Response{Text: c.answer})
		if c.bad != (err != nil) || got != c.want {
			t.Errorf("%s: %+v %v", c.answer, got, err)
		}
	}
	if _, err := understand.Parse(&ai.Response{StopReason: ai.StopRefusal}); err == nil {
		t.Error("a refusal was used")
	}
}
