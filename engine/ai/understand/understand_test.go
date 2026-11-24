package understand_test

import (
	"context"
	"reflect"
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
	r, resp, err := h.Understand(context.Background(), "I just sold 3 bags of rice for 45k cash, call me 08031234567", "• *sold 3 bags of rice 45,000 cash*: record a sale", nil, en)
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
		{`{"kind":"action","id":"riders.add","pick":"","values":[{"field":"name","value":" Musa  Bello "},{"field":"phone","value":""}],"command":"","reply":""}`,
			understand.Result{Kind: understand.Action, ID: "riders.add", Values: map[string]string{"name": "Musa Bello"}}, false},
		{`{"kind":"topic","id":"riders","pick":"riders.add","values":[{"field":"name","value":"Musa"}],"command":"","reply":""}`,
			understand.Result{Kind: understand.Topic, ID: "riders", Pick: "riders.add"}, false},
		{`{"kind":"action","id":"Riders; drop","pick":"","values":[],"command":"","reply":""}`, understand.Result{}, true},
		{`{"kind":"action","id":"riders.add","pick":"","values":[{"field":"Name!","value":"x"}],"command":"","reply":""}`, understand.Result{}, true},
		{`{"kind":"command","command":"","reply":""}`, understand.Result{}, true},
		{`{"kind":"command","command":"` + long + `","reply":""}`, understand.Result{}, true},
		{`{"kind":"ask","command":"","reply":""}`, understand.Result{}, true},
		{`{"kind":"run","command":"x","reply":""}`, understand.Result{}, true},
		{`not json`, understand.Result{}, true},
	}
	for _, c := range cases {
		got, err := understand.Parse(&ai.Response{Text: c.answer})
		if c.bad != (err != nil) || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v %v", c.answer, got, err)
		}
	}
	if _, err := understand.Parse(&ai.Response{StopReason: ai.StopRefusal}); err == nil {
		t.Error("a refusal was used")
	}
}
