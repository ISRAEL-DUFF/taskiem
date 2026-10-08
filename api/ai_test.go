package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
)

func aiEnvelope(t *testing.T, doc string) ai.Response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"summary": "Pays approved loans.", "assumptions": []string{}, "workflow": doc, "tests": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	return ai.Response{Text: string(raw)}
}

// aiWorld is a world with AI building on, answered by a fake model.
func aiWorld(t *testing.T, script ...ai.Response) (*world, *ai.Fake) {
	t.Helper()
	w := newWorld(t)
	if err := w.env.Registry.Register(paystack.New(paystack.Options{})); err != nil {
		t.Fatal(err)
	}
	if len(script) == 0 {
		script = []ai.Response{aiEnvelope(t, builder.ExampleWD)}
	}
	f := &ai.Fake{Script: script, Models: "claude-opus-5-5"}
	w.srv.AI = &api.AISettings{Provider: f}
	return w, f
}

// build asks for a build and waits for it.
func build(t *testing.T, w *world, c *client, body map[string]any) map[string]any {
	t.Helper()
	out := c.must(202, "POST", "/v1/ai/build", body)
	w.srv.WaitBackground()
	return c.must(200, "GET", "/v1/ai/builds/"+out["id"].(string), nil)
}

func TestAIDisabled(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	st, out := owner.do("POST", "/v1/ai/build", map[string]any{"goal": "pay suppliers"})
	if st != http.StatusServiceUnavailable || out["code"] != "ai_disabled" {
		t.Fatalf("%d %v", st, out)
	}
	if s := owner.must(200, "GET", "/v1/ai/status", nil); s["enabled"] != false {
		t.Errorf("status: %v", s)
	}
}

func TestAIBuildProposesAndSavesADraft(t *testing.T) {
	w, f := aiWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	me := owner.must(200, "GET", "/v1/me", nil)
	userID := me["user"].(map[string]any)["id"].(string)

	// Values that must never reach a prompt.
	const secretValue, credValue, varValue = "sk_live_SEEDED_SECRET_5150", "sk_live_SEEDED_CRED_7731", "VARIABLE_VALUE_SEEDED_4242"
	owner.must(204, "PUT", "/v1/secrets/prod/paystack_key", map[string]any{"value": secretValue})
	owner.must(204, "PUT", "/v1/variables/prod/ops_phone", map[string]any{"value": varValue})
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main",
		"credentials": map[string]string{"secret_key": credValue}})

	b := build(t, w, owner, map[string]any{"goal": "When a loan is approved pay it with Paystack after approval; borrower BVN 22198765432, email ada@example.com", "environment": "prod"})
	if b["status"] != "proposed" {
		t.Fatalf("build: %v", b)
	}
	prop := b["proposal"].(map[string]any)
	if prop["valid_first_try"] != true || prop["rounds"] != float64(1) || len(prop["results"].([]any)) != 1 {
		t.Fatalf("proposal: %v", prop)
	}
	if strings.Contains(toJSON(b), "22198765432") || strings.Contains(toJSON(b), "ada@example.com") {
		t.Errorf("the stored goal is not redacted: %v", b["goal"])
	}

	sent := ""
	for _, r := range f.Requests() {
		sent += ai.PromptText(r)
	}
	for _, leak := range []string{secretValue, credValue, varValue, "22198765432", "ada@example.com"} {
		if strings.Contains(sent, leak) {
			t.Errorf("%q reached the model", leak)
		}
	}
	for _, want := range []string{`"ops_phone"`, `"main"`, "paystack@1"} {
		if !strings.Contains(sent, want) {
			t.Errorf("prompt lacks %s", want)
		}
	}

	// Every model call is logged, redacted, with usage.
	its := owner.must(200, "GET", "/v1/ai/builds/"+b["id"].(string)+"/interactions", nil)["interactions"].([]any)
	if len(its) != 1 {
		t.Fatalf("interactions: %v", its)
	}
	it := its[0].(map[string]any)
	if it["model"] != "claude-opus-5-5" || it["outcome"] != "valid" || it["usage"].(map[string]any)["input_tokens"].(float64) <= 0 || it["system_digest"] == "" {
		t.Errorf("interaction: %v", it)
	}
	if strings.Contains(toJSON(it), "22198765432") {
		t.Errorf("logged prompt not redacted")
	}
	if st := owner.must(200, "GET", "/v1/ai/status", nil); st["budget"].(map[string]any)["used_tokens"].(float64) <= 0 {
		t.Errorf("usage not counted: %v", st)
	}

	// Saving creates a draft, authored by the person, co-authored by the AI.
	saved := owner.must(201, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", map[string]any{"name": "Loan payouts"})
	if saved["state"] != "draft" || saved["version"] != float64(1) || len(saved["problems"].([]any)) != 0 {
		t.Fatalf("save: %v", saved)
	}
	wf := saved["id"].(string)
	got := owner.must(200, "GET", "/v1/workflows/"+wf, nil)
	if got["workflow"].(map[string]any)["active_version"] != nil {
		t.Errorf("an AI draft is never published: %v", got["workflow"])
	}
	v := got["versions"].([]any)[0].(map[string]any)
	if v["state"] != "draft" || v["created_by"] != userID || v["ai_build"] != b["id"] {
		t.Errorf("version: %v", v)
	}
	owner.must(409, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", nil)

	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, action := range []string{"ai.propose", "ai.save"} {
		if !strings.Contains(audit, action) {
			t.Errorf("audit lacks %s", action)
		}
	}
	if !strings.Contains(audit, "ai:claude-opus-5-5") {
		t.Errorf("audit does not name the AI co-author")
	}

	// Modifying the saved workflow makes version 2 of it.
	b2 := build(t, w, owner, map[string]any{"goal": "also notify the borrower", "workflow": wf})
	if !strings.Contains(ai.PromptText(f.Requests()[len(f.Requests())-1]), "<current_workflow>") {
		t.Errorf("the workflow being modified is not in the prompt")
	}
	s2 := owner.must(201, "POST", "/v1/ai/builds/"+b2["id"].(string)+"/save", nil)
	if s2["id"] != wf || s2["version"] != float64(2) {
		t.Errorf("modify: %v", s2)
	}
}

func TestAIBudgetExhausted(t *testing.T) {
	w, _ := aiWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"ai_monthly_tokens": int64(10)}, "operator"); err != nil {
		t.Fatal(err)
	}
	if b := build(t, w, owner, map[string]any{"goal": "pay approved loans with paystack"}); b["status"] != "proposed" {
		t.Fatalf("first build: %v", b)
	}
	st, out := owner.do("POST", "/v1/ai/build", map[string]any{"goal": "pay approved loans with paystack"})
	if st != http.StatusTooManyRequests || out["code"] != "ai_budget_exhausted" || !strings.Contains(out["error"].(string), "canvas") {
		t.Fatalf("over budget: %d %v", st, out)
	}
	// Building by hand is unaffected.
	owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "manual", "definition": json.RawMessage(builder.ExampleWD)})
	lim := owner.must(200, "GET", "/v1/limits", nil)
	if lim["limits"].(map[string]any)["ai_monthly_tokens"] != float64(10) || lim["usage"].(map[string]any)["ai_tokens_this_month"].(float64) <= 10 {
		t.Errorf("limits: %v", lim)
	}
}

func TestAIPermissions(t *testing.T) {
	w, _ := aiWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	viewer.must(403, "POST", "/v1/ai/build", map[string]any{"goal": "pay"})
	b := build(t, w, owner, map[string]any{"goal": "pay approved loans"})
	viewer.must(403, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", nil)
	builderC := addMember(t, w, owner, "bea", "builder")
	saved := builderC.must(201, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", nil)
	// The builder role cannot publish what it saved, AI-assisted or not.
	builderC.must(403, "POST", "/v1/workflows/"+saved["id"].(string)+"/versions/1/publish", nil)
	// Other tenants cannot see the build.
	other := w.tenant(t, "Other", "owner@other.test")
	other.must(404, "GET", "/v1/ai/builds/"+b["id"].(string), nil)
}

// TestAIRoutes holds /v1/ai to proposing and saving drafts: no route there
// publishes, approves, decides, runs or touches secrets.
func TestAIRoutes(t *testing.T) {
	w, _ := aiWorld(t)
	var got []string
	err := chi.Walk(w.srv.Handler().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/v1/ai/") {
			got = append(got, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{
		"GET /v1/ai/builds/{id}",
		"GET /v1/ai/builds/{id}/interactions",
		"GET /v1/ai/status",
		"POST /v1/ai/build",
		"POST /v1/ai/builds/{id}/save",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("/v1/ai routes changed; every new one needs a safety review (docs/ai.md):\n%s", strings.Join(got, "\n"))
	}
	for _, r := range got {
		for _, bad := range []string{"publish", "approv", "decide", "secret", "run", "connection"} {
			if strings.Contains(r, bad) {
				t.Errorf("%s", r)
			}
		}
	}
}
