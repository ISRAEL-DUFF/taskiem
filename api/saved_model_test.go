package api_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The model reads @name (saved objects; folded into the chat menus' model):
// the model reading a message nothing recognised is told the saved things
// by @name and kind only, never a title or anything they hold, and picks a
// saved action from the catalogue, done as its command is.
func TestSavedReadByTheModel(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@savedmodel.test")
	const on = "+2348010000531"
	w.bind(t, owner, on)
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mama Adaeze", "kind": "person",
		"details": map[string]any{"phone": "0803 765 9876", "address": "4 Allen Avenue"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "warehouse", "title": "Apapa store", "kind": "place",
		"details": map[string]any{"line": "4 Creek Road", "area": "Apapa"}})

	var prompts []string
	answer := `{"kind":"action","id":"saved.ask","pick":"","values":[{"field":"name","value":"mum"},{"field":"what","value":"address"}],"command":"","reply":""}`
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
	if !strings.Contains(sent, "@mum (person)") || !strings.Contains(sent, "@warehouse (place)") || !strings.Contains(sent, "saved.ask") {
		t.Fatalf("names, kinds and the saved actions not told: %s", sent)
	}
	for _, held := range []string{"Mama Adaeze", "Apapa store", "Allen", "Creek", "765 9876", "7659876"} {
		if strings.Contains(sent, held) {
			t.Errorf("the model was sent %q", held)
		}
	}

	// Saved things as a form's chips: any for saved.ask, the people for an
	// invoice's customer.
	ch := owner.must(200, "GET", "/v1/chat/actions/saved.ask/choices", nil)["fields"].(map[string]any)
	if got := toJSON(ch["name"]); !strings.Contains(got, "@mum") || !strings.Contains(got, "@warehouse") {
		t.Errorf("saved.ask choices: %v", ch)
	}
	if got := toJSON(owner.must(200, "GET", "/v1/chat/actions/money.invoice/choices", nil)["fields"]); !strings.Contains(got, "@mum") || strings.Contains(got, "@warehouse") {
		t.Errorf("invoice choices: %s", got)
	}
}
