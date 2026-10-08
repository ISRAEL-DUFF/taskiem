package intent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/intent"
	"github.com/israel-duff/taskiem/engine/lang"
)

func TestClassifyRoutesWithTheLanguage(t *testing.T) {
	fake := &ai.Fake{Script: []ai.Response{{Text: `{"intent":"status","argument":""}`}}}
	c := &intent.Classifier{Provider: fake}
	yo, _ := lang.Lookup(lang.YO)
	r, resp, err := c.Classify(context.Background(), "Ṣé nǹkan kan bàjẹ́ lónìí? Pe mi lórí 08031234567", yo)
	if err != nil || r.Intent != lang.IntentStatus || resp == nil {
		t.Fatalf("%+v %v", r, err)
	}
	req := fake.Requests()[0]
	prompt := ai.PromptText(req)
	if !strings.Contains(prompt, "Yoruba (yo)") || req.Schema == nil {
		t.Fatalf("language not passed: %s", prompt)
	}
	// Redacted before it left.
	if strings.Contains(prompt, "08031234567") {
		t.Fatalf("phone number reached the model: %s", prompt)
	}
}

func TestParseRefusesWhatTheModelMayNotDo(t *testing.T) {
	cases := []struct {
		answer string
		want   lang.Intent
		arg    string
		bad    bool
	}{
		{`{"intent":"run","argument":"  Salary   reminders "}`, lang.IntentRun, "Salary reminders", false},
		{`{"intent":"build","argument":"every Friday text my customers who owe me"}`, lang.IntentBuild, "every Friday text my customers who owe me", false},
		{`{"intent":"unknown","argument":""}`, lang.IntentNone, "", false},
		{`{"intent":"yes","argument":""}`, "", "", true},      // the model never confirms
		{`{"intent":"cancel","argument":""}`, "", "", true},   // nor cancels
		{`{"intent":"publish","argument":"x"}`, "", "", true}, // nor anything else
		{`{"intent":"run","argument":"` + strings.Repeat("a", 400) + `"}`, "", "", true},
		{`not json`, "", "", true},
	}
	for _, c := range cases {
		r, err := intent.Parse(&ai.Response{Text: c.answer, StopReason: "end_turn"})
		if c.bad {
			if !errors.Is(err, intent.ErrUnusable) {
				t.Errorf("%s: accepted %+v", c.answer, r)
			}
			continue
		}
		if err != nil || r.Intent != c.want || r.Arg != c.arg {
			t.Errorf("%s: %+v %v", c.answer, r, err)
		}
	}
	if _, err := intent.Parse(&ai.Response{Text: `{"intent":"status","argument":""}`, StopReason: ai.StopRefusal}); !errors.Is(err, intent.ErrUnusable) {
		t.Error("a refusal was used")
	}
	for _, e := range intent.Schema()["properties"].(map[string]any)["intent"].(map[string]any)["enum"].([]any) {
		if e == "yes" || e == "no" || e == "cancel" {
			t.Errorf("schema offers %v", e)
		}
	}
}

func TestClassifyWithoutProvider(t *testing.T) {
	var c *intent.Classifier
	if _, _, err := c.Classify(context.Background(), "x", lang.Info{}); !errors.Is(err, ai.ErrNotConfigured) {
		t.Fatal(err)
	}
}
