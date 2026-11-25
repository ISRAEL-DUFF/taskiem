package whatsapp

import (
	"encoding/json"
	"testing"
)

// The review of forms that sell L4 (finding 2): a template Meta holds is
// ours only when its body and buttons are the library's, component by
// component, as Meta lists them.
func TestComponentsMatch(t *testing.T) {
	hosts := Hosts{App: "https://app.example.com", Forms: "https://forms.example.com"}
	tpl := FollowupTemplate("taskiem_finish_order", "", "utility")
	def, err := tpl.Submission("en", hosts)
	if err != nil {
		t.Fatal(err)
	}
	// Meta lists what was submitted (with examples, which do not count).
	var ours []MetaComponent
	raw, _ := json.Marshal(def["components"])
	if err := json.Unmarshal(raw, &ours); err != nil {
		t.Fatal(err)
	}
	if !ComponentsMatch(tpl, "en", hosts, ours) {
		t.Fatalf("ours: %+v", ours)
	}
	clone := func() []MetaComponent {
		out := make([]MetaComponent, len(ours))
		for i, c := range ours {
			out[i] = c
			out[i].Buttons = append([]MetaButton(nil), c.Buttons...)
		}
		return out
	}
	other := clone()
	other[0].Text = "[{{1}}] Buy {{2}} now, at half price!"
	footer := append(clone(), MetaComponent{Type: "FOOTER", Text: "Reply STOP"})
	url := clone()
	url[1].Buttons[0].URL = "https://elsewhere.example.com/back/{{1}}"
	words := clone()
	words[1].Buttons[1].Text = "Unsubscribe"
	noButtons := clone()[:1]
	for name, c := range map[string][]MetaComponent{"another body": other, "a footer of its own": footer, "another address": url,
		"another button": words, "no buttons": noButtons, "nothing listed": nil} {
		if ComponentsMatch(tpl, "en", hosts, c) {
			t.Errorf("%s matched", name)
		}
	}
	// An authentication template's words are Meta's own.
	if !ComponentsMatch(TplOTP, "en", hosts, []MetaComponent{{Type: "BODY", Text: "anything"}}) {
		t.Error("authentication")
	}
}
