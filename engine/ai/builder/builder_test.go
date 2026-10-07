package builder_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/termii"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

func registry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	for _, c := range []*connector.Connector{paystack.New(paystack.Options{}), termii.New(termii.Options{})} {
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func envelope(t *testing.T, wdDoc string, tests ...[2]string) ai.Response {
	t.Helper()
	ts := []map[string]string{}
	for _, c := range tests {
		ts = append(ts, map[string]string{"name": c[0], "case": c[1]})
	}
	raw, err := json.Marshal(map[string]any{"summary": "Pays approved loans.", "assumptions": []string{"a connection named main"}, "workflow": wdDoc, "tests": ts})
	if err != nil {
		t.Fatal(err)
	}
	return ai.Response{Text: string(raw)}
}

const unapproved = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"Pay","trigger":{"type":"manual"},
 "steps":[{"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":5000,"recipient":"RCP_1"}}]}`

const broken = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"Pay","trigger":{"type":"manual"},
 "steps":[{"id":"pay","type":"connector","needs":["nowhere"],"connector":"paystack@1","action":"transfer","input":{"amount":5000,"recipient":"RCP_1"}}]}`

type recorder struct {
	mu  sync.Mutex
	its []builder.Interaction
}

func (r *recorder) Record(_ context.Context, it builder.Interaction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.its = append(r.its, it)
	return nil
}

type meter struct{ allow int }

func (m *meter) Allow(context.Context) error {
	if m.allow <= 0 {
		return ai.ErrBudgetExhausted
	}
	m.allow--
	return nil
}

func newBuilder(t *testing.T, f *ai.Fake) (*builder.Builder, *recorder) {
	rec := &recorder{}
	return &builder.Builder{Provider: f, Connectors: registry(t), Recorder: rec}, rec
}

func TestExampleInPromptIsValid(t *testing.T) {
	if probs := wd.Validate([]byte(builder.ExampleWD)); len(probs) > 0 {
		t.Fatalf("the prompt's example is not a valid wd/v1 document: %v", probs)
	}
}

func TestFirstDraftValid(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, builder.ExampleWD,
		[2]string{"rejected loans are not paid", `{"trigger":{"body":{"loan_id":"L1","amount_kobo":5000,"recipient_code":"RCP_1"}},"approvals":{"approve":{"decision":"rejected","by":"checker"}},"expect":{"status":"completed","steps":{"pay":"skipped"}}}`})}}
	b, rec := newBuilder(t, f)
	p, err := b.Build(context.Background(), builder.Request{Goal: "When a loan is approved, pay it out with Paystack after a credit officer approves"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid() || !p.ValidFirstTry || p.Rounds != 1 || p.PolicyViolations() != 0 {
		t.Fatalf("proposal: %+v", p)
	}
	if len(p.Results) != 2 || !p.TestsPassed() {
		t.Fatalf("dry run: %+v", p.Results)
	}
	if p.Results[0].Source != "generated" || p.Results[1].Source != "model" {
		t.Errorf("sources: %+v", p.Results)
	}
	if p.Connectors[0] != "paystack@1" {
		t.Errorf("retrieval: %v", p.Connectors)
	}
	if len(rec.its) != 1 || rec.its[0].Outcome != "valid" || rec.its[0].Kind != "draft" || rec.its[0].SystemDigest == "" {
		t.Errorf("interactions: %+v", rec.its)
	}
	req := f.Requests()[0]
	if req.Schema == nil || req.Effort != "high" || len(req.System) != 2 || !req.System[0].Cache || !req.System[1].Cache {
		t.Errorf("request: schema %v effort %q system %d", req.Schema != nil, req.Effort, len(req.System))
	}
	if !strings.Contains(req.System[1].Text, "paystack@1") || !strings.Contains(req.Messages[0].Text, `"transfer"`) {
		t.Errorf("catalogue or connector detail missing")
	}
}

func TestSelfCorrectConverges(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, broken), {Text: "not json"}, envelope(t, builder.ExampleWD)}}
	b, rec := newBuilder(t, f)
	p, err := b.Build(context.Background(), builder.Request{Goal: "pay approved loans with paystack"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid() || p.ValidFirstTry || p.Rounds != 3 {
		t.Fatalf("proposal: valid %v first %v rounds %d problems %v", p.Valid(), p.ValidFirstTry, p.Rounds, p.Problems)
	}
	reqs := f.Requests()
	if len(reqs) != 3 {
		t.Fatalf("calls: %d", len(reqs))
	}
	fb := reqs[1].Messages[len(reqs[1].Messages)-1]
	if fb.Role != "user" || !strings.Contains(fb.Text, "nowhere") {
		t.Errorf("validator errors are fed back: %q", fb.Text)
	}
	if reqs[1].Messages[1].Role != "assistant" {
		t.Errorf("the draft goes back as the assistant turn")
	}
	if !strings.Contains(reqs[2].Messages[len(reqs[2].Messages)-1].Text, "not the expected JSON") {
		t.Errorf("parse errors are fed back")
	}
	outcomes := []string{}
	for _, it := range rec.its {
		outcomes = append(outcomes, it.Kind+":"+it.Outcome)
	}
	if strings.Join(outcomes, ",") != "draft:problems,correct:unparseable,correct:valid" {
		t.Errorf("interactions: %v", outcomes)
	}
}

func TestSelfCorrectGivesUpAfterThree(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, broken)}}
	b, _ := newBuilder(t, f)
	p, err := b.Build(context.Background(), builder.Request{Goal: "pay with paystack"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Valid() || p.Rounds != 4 || len(f.Requests()) != 4 {
		t.Fatalf("rounds %d calls %d valid %v", p.Rounds, len(f.Requests()), p.Valid())
	}
	if len(p.Problems) == 0 || len(p.Definition) == 0 {
		t.Errorf("the last draft and its problems are returned: %+v", p)
	}
}

func TestPaymentWithoutApprovalIsFlagged(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, unapproved)}}
	b, _ := newBuilder(t, f)
	p, err := b.Build(context.Background(), builder.Request{Goal: "send a paystack transfer"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid() || p.PolicyViolations() != 1 || p.Warnings[0].Rule != builder.RulePaymentWithoutApproval {
		t.Fatalf("warnings: %+v", p.Warnings)
	}
	if p.Rounds != 4 {
		t.Errorf("policy findings are fed back for correction: rounds %d", p.Rounds)
	}
	if !strings.Contains(f.Requests()[1].Messages[2].Text, builder.RulePaymentWithoutApproval) {
		t.Errorf("feedback: %q", f.Requests()[1].Messages[2].Text)
	}

	// A read, or a payment after an approval, is fine.
	def, err := wd.Load([]byte(builder.ExampleWD))
	if err != nil {
		t.Fatal(err)
	}
	if w := builder.PolicyFindings(def, registry(t)); len(w) != 0 {
		t.Errorf("approved payment flagged: %v", w)
	}
	nested := `{"schema":"wd/v1","id":"wf_n","version":1,"name":"n","trigger":{"type":"manual"},"steps":[
	 {"id":"ok","type":"approval","config":{"role":"finance","count":1}},
	 {"id":"each","type":"foreach","needs":["ok"],"config":{"items":"=[1,2]","steps":[{"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":5000,"recipient":"RCP"}}]}},
	 {"id":"bal","type":"connector","connector":"paystack@1","action":"check_balance","input":{}}]}`
	def, err = wd.Load([]byte(nested))
	if err != nil {
		t.Fatal(err)
	}
	if w := builder.PolicyFindings(def, registry(t)); len(w) != 0 {
		t.Errorf("payment nested under an approved foreach flagged: %v", w)
	}
}

func TestPromptsAreRedacted(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, builder.ExampleWD)}}
	b, rec := newBuilder(t, f)
	goal := "Verify BVN 22198765432 for ada.obi@example.com and text 08039876543 when done"
	if _, err := b.Build(context.Background(), builder.Request{Goal: goal}); err != nil {
		t.Fatal(err)
	}
	sent := ai.PromptText(f.Requests()[0])
	logged := ai.PromptText(rec.its[0].Request)
	for _, leak := range []string{"22198765432", "ada.obi@example.com", "08039876543"} {
		if strings.Contains(sent, leak) || strings.Contains(logged, leak) {
			t.Errorf("%q left unredacted", leak)
		}
	}
	if !strings.Contains(sent, "[bvn]") {
		t.Errorf("goal not in prompt: %s", ai.PromptText(f.Requests()[0])[:200])
	}
}

func TestContextCarriesNamesOnly(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, builder.ExampleWD)}}
	b, _ := newBuilder(t, f)
	_, err := b.Build(context.Background(), builder.Request{Goal: "pay approved loans", Context: builder.Context{
		Connections: []builder.Connection{{Environment: "prod", Connector: "paystack", Name: "main"}},
		Variables:   []builder.Variable{{Environment: "prod", Name: "ops_phone"}},
		Policies:    []builder.Policy{{Name: "high_value", Summary: "credit_officer then head_of_credit", Document: json.RawMessage(`{"rules":[{"levels":[{"role":"credit_officer"}]}]}`)}},
		Workflows:   []builder.ExistingWorkflow{{Name: "Loan payouts", Definition: json.RawMessage(builder.ExampleWD)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctxDoc, ok := builder.ContextFrom(f.Requests()[0].Messages[0].Text)
	if !ok {
		t.Fatal("no context document")
	}
	raw, _ := json.Marshal(ctxDoc)
	for _, want := range []string{`"main"`, `"ops_phone"`, `"high_value"`, `"Loan payouts"`, `approve: approval`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("context lacks %s: %s", want, raw)
		}
	}
	// Similar workflows carry structure, not inputs or expressions.
	if strings.Contains(string(raw), "trigger.body.amount_kobo") || strings.Contains(string(raw), "Loan disbursement") {
		t.Errorf("similar workflows leak their inputs: %s", raw)
	}
	// Policy documents are for the dry run, not the prompt.
	if strings.Contains(string(raw), "levels") {
		t.Errorf("policy documents in prompt: %s", raw)
	}
}

func TestBudgetExhausted(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{envelope(t, broken), envelope(t, broken)}}
	b, _ := newBuilder(t, f)
	b.Meter = &meter{allow: 0}
	if _, err := b.Build(context.Background(), builder.Request{Goal: "pay"}); !errors.Is(err, ai.ErrBudgetExhausted) {
		t.Fatalf("err %v", err)
	}
	if len(f.Requests()) != 0 {
		t.Fatalf("no call once the budget is spent")
	}
	// Spent mid-way: the last draft comes back with a warning.
	b.Meter = &meter{allow: 2}
	p, err := b.Build(context.Background(), builder.Request{Goal: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Rounds != 2 || p.Warnings[0].Kind != "budget" {
		t.Fatalf("rounds %d warnings %+v", p.Rounds, p.Warnings)
	}
}

func TestRefusal(t *testing.T) {
	f := &ai.Fake{Script: []ai.Response{{StopReason: ai.StopRefusal}}}
	b, rec := newBuilder(t, f)
	if _, err := b.Build(context.Background(), builder.Request{Goal: "pay"}); !errors.Is(err, builder.ErrRefused) {
		t.Fatalf("err %v", err)
	}
	if len(rec.its) != 1 || rec.its[0].Outcome != "refused" {
		t.Errorf("refusals are recorded: %+v", rec.its)
	}
}

func TestHandlersAreHidden(t *testing.T) {
	reg := builder.ManifestOnly(registry(t))
	c, ok := reg.Get("paystack@1")
	if !ok || c.Manifest == nil || c.Actions != nil {
		t.Fatalf("the builder sees manifests only: %+v", c)
	}
	for _, c := range reg.List() {
		if c.Actions != nil || c.Verifiers != nil {
			t.Fatalf("%s carries handlers", c.Ref())
		}
	}
}

func TestRankAndCatalogueAreDeterministic(t *testing.T) {
	reg := registry(t)
	r := builder.Rank("send an SMS with termii when a payout fails", reg, nil)
	if len(r) == 0 || r[0].Ref != "termii@1" {
		t.Fatalf("rank: %+v", r)
	}
	first := builder.Catalogue(reg)
	if second := builder.Catalogue(registry(t)); first != second {
		t.Fatal("catalogue differs between calls")
	}
}
