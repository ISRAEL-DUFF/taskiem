package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A message nothing recognises goes to the model with the commands the
// person may send (chat_understand.go), instead of sending them all of
// them: a command that only shows something is done at once, one that
// asks by itself is begun, and any other is shown and done only on Yes;
// a missing value is asked for and read with the message; what Taskiem
// cannot do is said with the way to see what it can. The model never
// confirms, and the long list comes only for "help".
func TestChatModelHelpsWithWhatItDoesNotRecognise(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	answers := map[string]string{
		"anything break today":          `{"kind":"command","command":"*status*","reply":""}`,
		"I bought diesel":               `{"kind":"ask","command":"","reply":"How much did the diesel cost?"}`,
		"I bought diesel\n12k":          `{"kind":"command","command":"spent 12,000 on diesel","reply":""}`,
		"Ada still has 5k to pay me":    `{"kind":"command","command":"Ada owes 5,000","reply":""}`,
		"book me a flight to Abuja":     `{"kind":"cannot","command":"","reply":"I can't book flights. I can record what you spent on one."}`,
		"sure go ahead":                 `{"kind":"command","command":"yes","reply":""}`,
		"please do the thing you know":  `not json`,
		"what about the other business": `{"kind":"command","command":"blah blah nothing","reply":""}`,
	}
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		msg := p[strings.Index(p, "<message>\n")+len("<message>\n") : strings.Index(p, "\n</message>")]
		return &ai.Response{Text: answers[msg]}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	const on = "+2348010000061"
	w.bind(t, owner, on)
	asked := func() int { return len(fake.Requests()) }

	// Only shows something: done at once, from the person's own commands.
	if r := w.say(t, on, "anything break today"); !strings.Contains(r, "No runs in the last 24 hours.") {
		t.Fatalf("status: %q", r)
	}
	prompt := ai.PromptText(fake.Requests()[0])
	if !strings.Contains(prompt, "*status*") || !strings.Contains(prompt, "*invoice*") {
		t.Fatalf("the commands were not given: %s", prompt)
	}

	// A missing value is asked for; the answer is read with the message,
	// and a command that asks by itself is begun at once.
	if r := w.say(t, on, "I bought diesel"); !strings.Contains(r, "How much did the diesel cost?") {
		t.Fatalf("ask: %q", r)
	}
	if r := w.say(t, on, "12k"); !strings.Contains(r, "Record ₦12,000 spent on diesel") {
		t.Fatalf("after the answer: %q", r)
	}
	w.say(t, on, "no")

	// One that would change something without asking: shown, done on Yes.
	if r := w.say(t, on, "Ada still has 5k to pay me"); !strings.Contains(r, "I think you mean: *Ada owes 5,000*") {
		t.Fatalf("confirm: %q", r)
	}
	if r := w.say(t, on, "no"); !strings.Contains(r, "OK, I left it.") {
		t.Fatalf("no: %q", r)
	}
	w.say(t, on, "Ada still has 5k to pay me")
	n := asked()
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Ada is new") {
		t.Fatalf("yes: %q", r)
	}
	if asked() != n {
		t.Fatal("the Yes went to the model")
	}
	w.say(t, on, "cancel")

	// What Taskiem cannot do: said, with the way to see what it can.
	if r := w.say(t, on, "book me a flight to Abuja"); !strings.Contains(r, "I can't book flights.") || !strings.Contains(r, "*help*") {
		t.Fatalf("cannot: %q", r)
	}

	// The model cannot confirm, and an unusable answer gets the short
	// answer; the model is asked once.
	for _, msg := range []string{"sure go ahead", "please do the thing you know"} {
		n := asked()
		r := w.say(t, on, msg)
		if r != "[Mama Put] Sorry, I did not understand that. Send *help* to see what I can do." {
			t.Fatalf("%s: %q", msg, r)
		}
		if asked() != n+1 {
			t.Fatalf("%s: asked the model %d times", msg, asked()-n)
		}
	}
	// A command it gave that nothing recognises: on Yes, the short answer,
	// not the model again.
	w.say(t, on, "what about the other business")
	n = asked()
	if r := w.say(t, on, "yes"); r != "[Mama Put] Sorry, I did not understand that. Send *help* to see what I can do." || asked() != n {
		t.Fatalf("unknown command: %q, asked %d", r, asked()-n)
	}
	var refused int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM ai_interactions WHERE kind = 'understand' AND outcome = 'refused'`).Scan(&refused); err != nil || refused != 1 {
		t.Errorf("refusal recorded: %d %v", refused, err)
	}

	// The whole list comes for help.
	if r := w.say(t, on, "help"); !strings.Contains(r, "*invoice*") || !strings.Contains(r, "*status*") {
		t.Fatalf("help: %q", r)
	}

	// Without the model, the short answer.
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}}
	if r := w.say(t, on, "anything break today"); r != "[Mama Put] Sorry, I did not understand that. Send *help* to see what I can do." {
		t.Fatalf("no model: %q", r)
	}
}
