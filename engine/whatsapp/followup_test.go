package whatsapp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The follow-up's template (forms-that-sell §19) is a library template,
// fixed in code and switched by nothing in the environment: submitted as
// utility with its button on the form host's /back/ and Stop, in the
// deployment's template language, and held only in own numbers'
// accounts, never the shared number's (the founder, 25 November 2026).
func TestFinishOrderTemplate(t *testing.T) {
	u := TplFinishOrder
	if got, ok := TemplateNamed("taskiem_finish_order"); !ok || got.Name != u.Name || !got.OwnOnly || Category(got) != "utility" {
		t.Fatalf("not in the library: %+v %v", got, ok)
	}
	hosts := Hosts{Forms: "https://forms.example.com"}
	sub, err := u.Submission("en", hosts)
	if s := jsonOf(sub); err != nil || !strings.Contains(s, "https://forms.example.com/back/{{1}}") || !strings.Contains(s, "QUICK_REPLY") || !strings.Contains(s, `"UTILITY"`) ||
		!strings.Contains(s, `"language":"en"`) {
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
	// An own number's account holds it; the shared number's does not.
	own := &Platform{Tenant: uuid.New()}
	if _, ok := own.TemplateNamed(u.Name); !ok || len(own.Library()) != len(AllTemplates) {
		t.Fatal("not in an own number's library")
	}
	shared := &Platform{}
	if _, ok := shared.TemplateNamed(u.Name); ok || len(shared.Library()) != len(AllTemplates)-1 {
		t.Fatal("in the shared number's library")
	}
	if _, ok := shared.TemplateNamed("taskiem_invoice"); !ok {
		t.Fatal("the shared number lost the library")
	}
	// Nothing for the shared number to say about its category.
	if c, err := shared.TemplateCategory(t.Context(), u); c != "" || err != nil {
		t.Fatalf("the shared number's category: %q %v", c, err)
	}
}
