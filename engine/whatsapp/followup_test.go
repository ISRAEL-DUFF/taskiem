package whatsapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The follow-up's template (forms-that-sell L4) is off until the
// deployment names it, and then needs its category; its language is the
// deployment's unless given.
func TestFollowupTemplateFromEnv(t *testing.T) {
	env := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	if got, err := FollowupTemplateFromEnv(env()); got != nil || err != nil {
		t.Fatalf("unset: %v %v", got, err)
	}
	if got, err := FollowupTemplateFromEnv(env("TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "utility")); got != nil || err != nil {
		t.Fatalf("a category alone: %v %v", got, err)
	}
	for _, bad := range [][]string{
		{"TASKIEM_WA_FOLLOWUP_TEMPLATE", "finish_order"},
		{"TASKIEM_WA_FOLLOWUP_TEMPLATE", "finish_order", "TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "authentication"},
		{"TASKIEM_WA_FOLLOWUP_TEMPLATE", "Finish Order", "TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "utility"},
		{"TASKIEM_WA_FOLLOWUP_TEMPLATE", "taskiem_update", "TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "utility"},
		{"TASKIEM_WA_FOLLOWUP_TEMPLATE", "finish_order", "TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "utility", "TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE", "english"},
	} {
		if _, err := FollowupTemplateFromEnv(env(bad...)); err == nil {
			t.Errorf("%v: accepted", bad)
		}
	}
	got, err := FollowupTemplateFromEnv(env("TASKIEM_WA_FOLLOWUP_TEMPLATE", "finish_order", "TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "Marketing",
		"TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE", "en_US"))
	if err != nil || got == nil || got.Name != "finish_order" || got.Category != "MARKETING" || !got.OptIn() || got.LanguageOr("en") != "en_US" {
		t.Fatalf("set: %+v %v", got, err)
	}
	u, _ := FollowupTemplateFromEnv(env("TASKIEM_WA_FOLLOWUP_TEMPLATE", "finish_order", "TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "utility"))
	if u.OptIn() || u.LanguageOr("ha") != "ha" || Category(*u) != "utility" {
		t.Fatalf("utility: %+v", u)
	}
	hosts := Hosts{Forms: "https://forms.example.com"}
	// Submitted with its button on the form host's /back/ and Stop.
	sub, err := u.Submission(u.LanguageOr("en"), hosts)
	if s := jsonOf(sub); err != nil || !strings.Contains(s, "https://forms.example.com/back/{{1}}") || !strings.Contains(s, "QUICK_REPLY") || !strings.Contains(s, `"UTILITY"`) {
		t.Fatalf("submission: %s %v", s, err)
	}
	// Its button opens only a link under the form host's /back/.
	p, err := u.Payload("en", map[string]string{"business": "Ada", "what": "Rice", "link": "https://forms.example.com/back/abc"}, []string{FollowupStopPayload}, hosts)
	if s := jsonOf(p); err != nil || !strings.Contains(s, `"abc"`) || !strings.Contains(s, FollowupStopPayload) {
		t.Fatalf("payload: %s %v", s, err)
	}
	if _, err := u.Payload("en", map[string]string{"business": "Ada", "what": "Rice", "link": "https://evil.example.com/back/abc"}, []string{FollowupStopPayload}, hosts); err == nil {
		t.Fatal("a link elsewhere")
	}
	// The number knows it beside the library; the library does not.
	pl := &Platform{Config: Config{Templates: []Template{*u}}}
	if _, ok := pl.TemplateNamed("finish_order"); !ok || len(pl.Library()) != len(AllTemplates)+1 {
		t.Fatal("not in the number's library")
	}
	if _, ok := TemplateNamed("finish_order"); ok || len((&Platform{}).Library()) != len(AllTemplates) {
		t.Fatal("in the shared library")
	}
}
