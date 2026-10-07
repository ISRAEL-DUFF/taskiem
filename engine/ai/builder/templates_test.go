package builder_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/templates"
)

func fullRegistry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func templateAnswer(t *testing.T, id string, params map[string]any) ai.Response {
	t.Helper()
	raw, _ := json.Marshal(params)
	env, _ := json.Marshal(map[string]any{"summary": "From the template.", "assumptions": []string{}, "workflow": "", "tests": []any{},
		"template": id, "template_params": string(raw)})
	return ai.Response{Text: string(env)}
}

// Templates first: the prompt offers the matching template, the model
// answers with parameter values only, and the builder instantiates it;
// what the goal did not say is listed as missing.
func TestTemplateFirst(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{templateAnswer(t, "debtor-reminder-sms",
		map[string]any{"business_name": "Ada Stores", "day": "Friday", "send_time": "9am", "spreadsheet_id": "not a spreadsheet id", "nonsense": 1})}}
	b := &builder.Builder{Provider: f, Connectors: fullRegistry(t), Templates: templates.Default()}
	p, err := b.Build(context.Background(), builder.Request{Goal: "Every Friday at 9am, text my customers who owe me. My shop is Ada Stores."})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid() || !p.ValidFirstTry || !p.TestsPassed() || p.Rounds != 1 {
		t.Fatalf("proposal: valid %v first %v tests %v problems %v results %+v", p.Valid(), p.ValidFirstTry, p.TestsPassed(), p.Problems, p.Results)
	}
	if len(p.TemplatesOffered) == 0 || p.TemplatesOffered[0] != "debtor-reminder-sms" {
		t.Errorf("offered: %v", p.TemplatesOffered)
	}
	use := p.Template
	if use == nil || use.ID != "debtor-reminder-sms" || !use.Instantiated {
		t.Fatalf("template use: %+v", use)
	}
	// The bad spreadsheet id is dropped and asked for again, like the
	// payment details the goal never gave.
	if strings.Join(use.Missing, ",") != "spreadsheet_id,payment_details" || use.Params["send_time"] != "09:00" || use.Params["day"] != "friday" {
		t.Errorf("params %v missing %v", use.Params, use.Missing)
	}
	found := false
	for _, w := range p.Warnings {
		found = found || (w.Kind == "template" && strings.Contains(w.Message, "spreadsheet_id"))
	}
	if !found {
		t.Errorf("warnings: %+v", p.Warnings)
	}
	ctxDoc, _ := builder.ContextFrom(f.Requests()[0].Messages[0].Text)
	tpls, _ := ctxDoc["starting_templates"].([]any)
	if len(tpls) == 0 || tpls[0].(map[string]any)["id"] != "debtor-reminder-sms" {
		t.Errorf("starting templates in the prompt: %v", tpls)
	}
	if sys := f.Requests()[0].System[0].Text; !strings.Contains(sys, "# Starting templates") {
		t.Error("the instructions do not explain starting templates")
	}
}

// A template the prompt did not offer is a problem fed back for
// correction; no template matching leaves the builder drafting freely.
func TestTemplateNotOffered(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{templateAnswer(t, "kyc-bvn-check", nil), envelope(t, builder.ExampleWD)}}
	b := &builder.Builder{Provider: f, Connectors: fullRegistry(t), Templates: templates.Default()}
	p, err := b.Build(context.Background(), builder.Request{Goal: "nightly, copy the settlement file from the bank's SFTP server into S3"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ValidFirstTry || !p.Valid() || p.Rounds != 2 || p.Template != nil || len(p.TemplatesOffered) != 0 {
		t.Fatalf("first %v valid %v rounds %d template %+v offered %v", p.ValidFirstTry, p.Valid(), p.Rounds, p.Template, p.TemplatesOffered)
	}
	if !strings.Contains(f.Requests()[1].Messages[2].Text, "not one of the starting templates") {
		t.Errorf("feedback: %s", f.Requests()[1].Messages[2].Text)
	}
}

// The evaluation suite's requests must never reach a prompt: not the
// instructions, the worked example, nor any template offered as context
// (spec 12.4; label leakage would let the suite measure recall of its own
// answers). Checked by whole request and by every run of eight words.
func TestNoEvalRequestInPrompts(t *testing.T) {
	var corpus strings.Builder
	f := &ai.Fake{Script: []ai.Response{envelope(t, builder.ExampleWD)}}
	b := &builder.Builder{Provider: f, Connectors: fullRegistry(t), Templates: templates.Default()}
	if _, err := b.Build(context.Background(), builder.Request{Goal: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.Requests()[0].System {
		corpus.WriteString(s.Text)
		corpus.WriteByte('\n')
	}
	for _, tpl := range templates.Default().List() {
		raw, _ := json.Marshal(builder.TemplateContext(tpl))
		corpus.Write(raw)
		corpus.WriteString(tpl.Summary + "\n" + strings.Join(tpl.Keywords, " ") + "\n")
	}
	text := normalise(corpus.String())

	files, _ := filepath.Glob("../../../evals/builder/*.jsonl")
	if len(files) == 0 {
		t.Fatal("no evaluation suite found")
	}
	n := 0
	for _, file := range files {
		fh, err := os.Open(file) //nolint:gosec // a test fixture
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			var c struct {
				ID      string `json:"id"`
				Request string `json:"request"`
			}
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			n++
			req := normalise(c.Request)
			if strings.Contains(text, req) {
				t.Errorf("%s: the request appears in a prompt", c.ID)
				continue
			}
			words := strings.Fields(req)
			for i := 0; i+8 <= len(words); i++ {
				if gram := strings.Join(words[i:i+8], " "); strings.Contains(text, gram) {
					t.Errorf("%s: %q appears in a prompt", c.ID, gram)
					break
				}
			}
		}
		_ = fh.Close()
	}
	if n < 200 {
		t.Errorf("checked %d requests; the suite has at least 200", n)
	}
}

func normalise(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return ' '
	}, s)
	return " " + strings.Join(strings.Fields(s), " ") + " "
}
