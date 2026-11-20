package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
)

// The model reads @name (saved objects S2): in another language, the model
// routing a message is told the saved things by @name and kind only, never
// a title or anything they hold; what it returns is read as the English
// words are.
func TestSavedReadByTheModel(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@savedmodel.test")
	const on = "+2348010000531"
	w.bind(t, owner, on)
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mama Adaeze", "kind": "person",
		"details": map[string]any{"phone": "0803 123 4567", "address": "4 Allen Avenue"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "warehouse", "title": "Apapa store", "kind": "place",
		"details": map[string]any{"line": "4 Creek Road", "area": "Apapa"}})

	var prompts []string
	answer := `{"intent":"saved","argument":"what is @mum's address"}`
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}, Model: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		prompts = append(prompts, ai.PromptText(req))
		return &ai.Response{Text: answer, StopReason: "end_turn"}, nil
	}}}
	w.say(t, on, "language pidgin")
	if r := w.say(t, on, "abeg where my mama dey stay?"); !strings.Contains(r, "4 Allen Avenue") {
		t.Fatalf("routed: %q", r)
	}
	if len(prompts) == 0 {
		t.Fatal("the model was not asked")
	}
	sent := prompts[len(prompts)-1]
	if !strings.Contains(sent, "@mum (person)") || !strings.Contains(sent, "@warehouse (place)") {
		t.Fatalf("names and kinds not told: %s", sent)
	}
	for _, held := range []string{"Mama Adaeze", "Apapa store", "Allen", "Creek", "0803", "4567"} {
		if strings.Contains(sent, held) {
			t.Errorf("the model was sent %q: %s", held, sent)
		}
	}
	// Words that are no saved command: the usual answer, nothing done.
	answer = `{"intent":"saved","argument":"delete everything"}`
	if r := w.say(t, on, "abeg comot everything"); strings.Contains(r, "Allen") {
		t.Errorf("not a saved command: %q", r)
	}
}
