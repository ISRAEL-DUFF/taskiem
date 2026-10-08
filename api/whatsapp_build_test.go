package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const smsOnlyFlow = `{"schema":"wd/v1","id":"wf_dailyHello","version":1,"name":"Daily hello","trigger":{"type":"schedule","config":{"cron":"0 8 * * *"}},
 "steps":[{"id":"hello","type":"connector","name":"Text the owner good morning","connector":"termii@1","action":"send_sms","input":{"to":"=env.owner_phone","sms":"Good morning"}}]}`

// waBuildModel answers the debtors goal with the template and the values
// the goal states, and anything else with a one-step workflow.
func waBuildModel(t *testing.T, slow <-chan struct{}) *ai.Fake {
	return &ai.Fake{Models: "claude-opus-5-5", Respond: func(req ai.Request) (*ai.Response, error) {
		goal := builder.GoalFrom(req.Messages[0].Text)
		if strings.Contains(goal, "slowly") {
			<-slow
		}
		env := map[string]any{"summary": "Draft.", "assumptions": []string{}, "tests": []any{}, "template": "", "template_params": "", "workflow": smsOnlyFlow}
		if strings.Contains(goal, "owe") {
			ctxDoc, _ := builder.ContextFrom(req.Messages[0].Text)
			if tpls, _ := ctxDoc["starting_templates"].([]any); len(tpls) == 0 || tpls[0].(map[string]any)["id"] != "debtor-reminder-sms" {
				t.Errorf("the debtors template was not offered: %v", tpls)
			}
			env["workflow"], env["template"], env["template_params"] = "", "debtor-reminder-sms", `{"business_name":"Ada Stores","day":"friday","send_time":"09:00"}`
		}
		raw, _ := json.Marshal(env)
		return &ai.Response{Text: string(raw)}, nil
	}}
}

// lastTo is the newest message sent to a number after the background
// work finished.
func (w *waWorld) lastTo(t *testing.T, number string, before int) []whatsapptest.Sent {
	t.Helper()
	w.srv.WaitBackground()
	return w.graph.Sent(number)[before:]
}

func TestWhatsAppBuild(t *testing.T) {
	w := newWAWorld(t)
	registerSME(t, w.env.Registry)
	slow := make(chan struct{})
	f := waBuildModel(t, slow)
	w.srv.AI = &api.AISettings{Provider: f}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000021"
	w.bind(t, owner, on)

	if r := w.say(t, on, "help"); !strings.Contains(r, "*build*") {
		t.Errorf("help: %q", r)
	}
	// Natural phrasing, without "build".
	before := len(w.graph.Sent(on))
	if r := w.say(t, on, "Every Friday at 9am, text my customers who owe me"); !strings.Contains(r, "Working on it") {
		t.Fatalf("start: %q", r)
	}
	got := w.lastTo(t, on, before+1)
	if len(got) != 1 {
		t.Fatalf("draft messages: %+v", got)
	}
	draft := got[0].Text
	for _, want := range []string{"Here is the workflow I drafted", "Weekly SMS reminders to customers who owe you", "1. Every Friday at 09:00 (Africa/Lagos time)",
		"Read the debtors sheet (Google Sheets)", "4a. Text the customer a reminder of what they owe (Termii), only when its condition holds",
		"I need 2 more detail(s)", "1/2. *Debtors spreadsheet*"} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft lacks %q:\n%s", want, draft)
		}
	}
	// Plain steps only: no JSON, no expressions.
	for _, leak := range []string{"{", "wd/v1", "=has(", "steps.", "{{"} {
		if strings.Contains(draft, leak) {
			t.Errorf("draft shows %q:\n%s", leak, draft)
		}
	}
	if r := w.say(t, on, "the debtors sheet"); !strings.Contains(r, "does not fit") || !strings.Contains(r, "1/2.") {
		t.Errorf("bad value: %q", r)
	}
	if r := w.say(t, on, "1AbCdEfGhIjKlMnOpQrStUvWxYz012"); !strings.Contains(r, "2/2. *How to pay*") {
		t.Errorf("next parameter: %q", r)
	}
	confirm := w.send(t, on, whatsapptest.Text("Pay to 0123456789, GTBank"))
	if len(confirm) != 1 || len(confirm[0].Buttons) != 2 || !strings.Contains(confirm[0].Text, "With your details") ||
		!strings.Contains(confirm[0].Text, "Save this as a draft workflow? Reply *yes* or *no*") {
		t.Fatalf("confirmation: %+v", confirm)
	}
	// Nothing is saved before the yes.
	if wfs := owner.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any); len(wfs) != 0 {
		t.Fatal("saved before confirmation")
	}
	saved := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, save draft"))
	if len(saved) != 1 || !strings.Contains(saved[0].Text, "as a draft") || !strings.Contains(saved[0].Text, waPublicURL+"/workflows/") {
		t.Fatalf("saved: %+v", saved)
	}
	wfs := owner.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any)
	if len(wfs) != 1 {
		t.Fatalf("workflows: %v", wfs)
	}
	wf := wfs[0].(map[string]any)["id"].(string)
	detail := owner.must(200, "GET", "/v1/workflows/"+wf, nil)
	if v := detail["versions"].([]any)[0].(map[string]any); v["state"] != "draft" || v["ai_build"] == nil || len(detail["deployments"].([]any)) != 0 {
		t.Errorf("a draft only, by the AI as co-author: %v", detail)
	}
	def := string(mustJSON(t, owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil)["definition"]))
	if !strings.Contains(def, "1AbCdEfGhIjKlMnOpQrStUvWxYz012") || !strings.Contains(def, "Pay to 0123456789, GTBank") || !strings.Contains(def, `"cron":"0 9 * * 5"`) {
		t.Errorf("the details reached the draft: %s", def)
	}
	ctx := context.Background()
	var tpl, channel string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT v.template_id, b.channel FROM workflow_versions v JOIN ai_builds b ON b.id = v.ai_build_id WHERE v.workflow_id = $1`, wf).
		Scan(&tpl, &channel); err != nil || tpl != "debtor-reminder-sms" || channel != "whatsapp" {
		t.Errorf("provenance: %q %q %v", tpl, channel, err)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'ai.save' AND detail->>'channel' = 'whatsapp' AND detail->>'template' = 'debtor-reminder-sms'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit: %d %v", n, err)
	}

	// "build" with a goal no template fits; "no" saves nothing.
	before = len(w.graph.Sent(on))
	w.say(t, on, "build every morning text me good morning")
	got = w.lastTo(t, on, before+1)
	if len(got) != 1 || !strings.Contains(got[0].Text, "1. Every day at 08:00") || !strings.Contains(got[0].Text, "Save this as a draft workflow?") ||
		strings.Contains(got[0].Text, "template") || len(got[0].Buttons) != 2 {
		t.Fatalf("free draft: %+v", got)
	}
	if r := w.say(t, on, "maybe"); !strings.Contains(r, "Reply *yes*") {
		t.Errorf("unclear answer: %q", r)
	}
	if r := w.say(t, on, "no"); !strings.Contains(r, "nothing was saved") {
		t.Errorf("no: %q", r)
	}
	if wfs := owner.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any); len(wfs) != 1 {
		t.Errorf("no saves nothing: %d workflows", len(wfs))
	}

	// Cancel while drafting: the draft is not sent.
	before = len(w.graph.Sent(on))
	if r := w.say(t, on, "build every morning text me good morning, slowly"); !strings.Contains(r, "Working on it") {
		t.Fatalf("slow build: %q", r)
	}
	if r := w.say(t, on, "what now"); !strings.Contains(r, "still drafting") {
		t.Errorf("while drafting: %q", r)
	}
	if r := w.say(t, on, "cancel"); r != "[Acme] Cancelled." {
		t.Errorf("cancel: %q", r)
	}
	close(slow)
	if got := w.lastTo(t, on, before+3); len(got) != 0 {
		t.Errorf("a cancelled build answered: %+v", got)
	}

	// Without workflow.edit, nothing is built.
	viewer := w.member(t, owner, "viewer@acme.test", "viewer", "approver")
	const vn = "+2348010000022"
	w.bind(t, viewer, vn)
	calls := len(f.Requests())
	if r := w.say(t, vn, "build every Friday text my customers who owe me"); !strings.Contains(r, "it takes workflow.edit") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, vn, "help"); strings.Contains(r, "*build*") {
		t.Errorf("help offers build without workflow.edit: %q", r)
	}
	if len(f.Requests()) != calls {
		t.Error("the model was asked for someone without workflow.edit")
	}

	// Over the month's AI budget: refused before any model call.
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	if err := w.env.Store.SetLimits(ctx, tenant, map[string]any{"ai_monthly_tokens": int64(1)}, "operator"); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, on, "build every morning text me good morning"); !strings.Contains(r, "AI budget") || !strings.Contains(r, "used up") {
		t.Errorf("budget: %q", r)
	}
	w.srv.WaitBackground()
	if len(f.Requests()) != calls {
		t.Error("the model was asked over budget")
	}
}

// Without AI configured, building over WhatsApp says so.
func TestWhatsAppBuildDisabled(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000023"
	w.bind(t, owner, on)
	if r := w.say(t, on, "build every Friday text my customers who owe me"); !strings.Contains(r, "not set up") {
		t.Errorf("disabled: %q", r)
	}
}
