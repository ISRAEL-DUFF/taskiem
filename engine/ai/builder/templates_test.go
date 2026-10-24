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
	"github.com/israel-duff/taskiem/engine/expansion"
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

// The builder knows invoices and the money log: the instructions say
// invoices are invoices@1's and paid only by payments (waited for with
// invoice.paid, or started on by the invoice trigger), and that the money log is read, never written; a goal
// about invoices however worded carries invoices@1's schema and starts
// from the invoice templates.
func TestInvoiceKnowledge(t *testing.T) {
	for goal, tpl := range map[string]string{
		"bill a customer for 5 bags of rice and tell me when they pay": "send-invoice",
		"remind customers about overdue invoices":                      "chase-overdue-invoices",
		"send a receipt when an invoice is paid":                       "invoice-paid-receipt",
		"text me every week who still owes me money":                   "",
	} {
		f := &ai.Fake{Script: []ai.Response{envelope(t, builder.ExampleWD)}}
		b := &builder.Builder{Provider: f, Connectors: fullRegistry(t), Templates: templates.Default()}
		if _, err := b.Build(context.Background(), builder.Request{Goal: goal}); err != nil {
			t.Fatal(err)
		}
		req := f.Requests()[0]
		sys := req.System[0].Text
		for _, want := range []string{"use invoices@1", `"event": "invoice.paid"`, `uses the "invoice" trigger ({"event": "paid"})`, "Never keep customers' invoices in a data table with a status or paid column",
			"never make a payment link or account number for an invoice yourself", "money_matches has one row per match", "never insert_rows"} {
			if !strings.Contains(sys, want) {
				t.Errorf("the instructions do not say %q", want)
			}
		}
		ctxDoc, _ := builder.ContextFrom(req.Messages[0].Text)
		found := false
		for _, c := range ctxDoc["relevant_connectors"].([]any) {
			found = found || c.(map[string]any)["ref"] == "invoices@1"
		}
		if !found {
			t.Errorf("%q: invoices@1 is not among the relevant connectors", goal)
		}
		if tpl != "" {
			tpls, _ := ctxDoc["starting_templates"].([]any)
			if len(tpls) == 0 || tpls[0].(map[string]any)["id"] != tpl {
				t.Errorf("%q: starting templates %v, want %s first", goal, tpls, tpl)
			}
		}
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

// The business's own callable workflows (decision 0048) are listed in the
// context, and a draft that uses one is dry-run with its steps through the
// builder's resolver.
func TestCallableWorkflowsBuild(t *testing.T) {
	const id = "2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12"
	own := templates.Template{ID: id, Title: "Double it", Summary: "S", Description: "D", Category: "workflow", Tags: []string{}, Connectors: []string{}, Params: []templates.Param{},
		Callable: true, Outputs: []templates.Output{{Name: "kobo", Type: "integer", Title: "Total", Description: "Twice", Value: "=steps.total.output.kobo"}},
		Definition: json.RawMessage(`{"schema":"wd/v1","id":"wf_d","version":1,"name":"Double it","trigger":{"type":"manual"},
		  "inputs":{"schema":{"type":"object","required":["amount_kobo"],"properties":{"amount_kobo":{"type":"integer"}}}},
		  "steps":[{"id":"total","type":"transform","config":{"output":{"kobo":"=trigger.body.amount_kobo * 2"}}}]}`)}
	draft := `{"schema":"wd/v1","id":"wf_use","version":1,"name":"Use it","trigger":{"type":"manual"},
	 "inputs":{"schema":{"type":"object","required":["amount_kobo"],"properties":{"amount_kobo":{"type":"integer"}}}},
	 "steps":[{"id":"sub","type":"template","workflow":"` + id + `","input":{"amount_kobo":"=trigger.body.amount_kobo"}},
	          {"id":"after","type":"transform","needs":["sub"],"config":{"output":{"kobo":"=steps.sub.output.kobo"}}}]}`
	f := &ai.Fake{Script: []ai.Response{envelope(t, draft)}}
	b, _ := newBuilder(t, f)
	lib := expansion.Library(templates.Default())
	b.Find = func(want string) (*templates.Template, error) {
		if want == id {
			return &own, nil
		}
		return lib(want)
	}
	p, err := b.Build(context.Background(), builder.Request{Goal: "double the amount with my Double it workflow", Context: builder.Context{
		CallableWorkflows: []builder.CallableWorkflow{{ID: id, Name: "Double it", Inputs: json.RawMessage(`{"type":"object"}`), Outputs: own.Outputs, Effect: "only reads"}},
		Startable:         []builder.StartableWorkflow{{ID: id, Name: "Double it", Inputs: json.RawMessage(`{"type":"object"}`)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid() || !p.TestsPassed() {
		t.Fatalf("valid %v, tests %+v, problems %v", p.Valid(), p.Tests, p.Problems)
	}
	ctxDoc, _ := builder.ContextFrom(f.Requests()[0].Messages[0].Text)
	if raw, _ := json.Marshal(ctxDoc["callable_workflows"]); !strings.Contains(string(raw), `"Double it"`) || !strings.Contains(string(raw), `"only reads"`) {
		t.Errorf("the context lacks the callable workflows: %s", raw)
	}
	if !strings.Contains(f.Requests()[0].System[0].Text, "callable_workflows") || !strings.Contains(f.Requests()[0].System[0].Text, "startable_workflows") {
		t.Errorf("the prompt does not say how to use them")
	}
	if raw, _ := json.Marshal(ctxDoc["startable_workflows"]); !strings.Contains(string(raw), `"Double it"`) {
		t.Errorf("the context lacks the startable workflows: %s", raw)
	}
}
