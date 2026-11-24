package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// Pages G3, done when: "desk" on WhatsApp answers with the figures, five
// rows and a link; "/desk" too; masked as on the page; only for those its
// roles let open it (anyone else is answered as if there were no page).
func TestPagesShortFormOnWhatsApp(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@pagechat.test")
	publishWF(t, owner, "Ticket counts", toolDef("wf_counts", "Ticket counts", "ticket-counts", "", `{}`,
		`[{"name":"open","type":"integer","title":"Open","description":"Open","value":"=steps.look.output.n"},
		  {"name":"rows","type":"list","title":"Rows","description":"Rows","value":"=steps.look.output.rows"}]`,
		`{"type":"text","text":"done"}`,
		`{"id":"look","type":"transform","config":{"output":{"n":7,"rows":[
			{"name":"Musa","phone":"Call 2348031112222"},{"name":"Ngozi","phone":"x"},{"name":"Ada","phone":"x"},
			{"name":"Bayo","phone":"x"},{"name":"Chi","phone":"x"},{"name":"Dayo","phone":"x"},{"name":"Efe","phone":"x"}]}}}`))
	doc := map[string]any{"page": "page/v1", "name": "desk", "title": "Desk", "roles": []string{"operator"},
		"blocks": []map[string]any{
			{"id": "open", "type": "figure", "label": "Open", "value": "output", "source": map[string]any{"tool": "ticket-counts", "output": "open"}},
			{"id": "list", "type": "list", "title": "Waiting", "source": map[string]any{"tool": "ticket-counts", "output": "rows"},
				"item": map[string]any{"title": "=item.name", "sub": "=item.phone"}},
		}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/desk/publish", nil)

	op := w.member(t, owner, "op@pagechat.test", "operator")
	const on = "+2348010000051"
	w.bind(t, op, on)
	w.env.Drain(t)
	got := w.say(t, on, "desk")
	for _, want := range []string{"*Desk*", "Open 7", "*Waiting*", "• Musa", "• Chi", "…and 2 more on the page.", "Open the page: " + waPublicURL + "/pages/desk"} {
		if !strings.Contains(got, want) {
			t.Errorf("short form: want %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Dayo") || strings.Contains(got, "2348031112222") {
		t.Errorf("five rows, masked: %q", got)
	}
	if again := w.say(t, on, "/desk"); !strings.Contains(again, "*Desk*") {
		t.Errorf("/desk: %q", again)
	}

	// Someone the page's roles do not let in: no page is named.
	bo := w.member(t, owner, "bo@pagechat.test", "builder")
	const bn = "+2348010000052"
	w.bind(t, bo, bn)
	if r := w.say(t, bn, "desk"); strings.Contains(r, "*Desk*") || strings.Contains(r, "Musa") {
		t.Errorf("a builder got the page: %q", r)
	}
}
