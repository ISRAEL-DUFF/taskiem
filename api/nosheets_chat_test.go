package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A template picked in chat makes the tables it declares once the draft is
// saved (decision 0060, R1), saying first that the business's records are
// being set up when this is its first table.
func TestChatTemplateMakesItsTables(t *testing.T) {
	w := newWATablesWorld(t)
	registerSME(t, w.env.Registry)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000009"
	w.bind(t, owner, on)

	if got := w.press(t, on, "su:tpl:customer-welcome-table"); len(got) != 1 || !strings.Contains(got[0].Text, "Business name") {
		t.Fatalf("template: %+v", got)
	}
	if got := w.send(t, on, whatsapptest.Text("Acme Gym")); len(got) != 1 || !strings.Contains(got[0].Text, "Save this as a draft workflow?") {
		t.Fatalf("detail: %+v", got)
	}
	got := w.send(t, on, whatsapptest.Text("yes"))
	all := ""
	for _, m := range got {
		all += m.Text + "\n"
	}
	if !strings.Contains(all, "Setting up your records") || !strings.Contains(all, "I made the table it uses: customers") {
		t.Fatalf("saved: %s", all)
	}
	if !strings.Contains(got[0].Text, "Setting up your records") {
		t.Errorf("the setting-up line comes first: %+v", got)
	}

	// Picked again, the table exists and has what it needs: nothing to say
	// about it, and nothing is set up again.
	w.send(t, on, whatsapptest.Text("cancel"))
	w.press(t, on, "su:tpl:customer-welcome-table")
	w.send(t, on, whatsapptest.Text("Acme Gym"))
	all = ""
	for _, m := range w.send(t, on, whatsapptest.Text("yes")) {
		all += m.Text + "\n"
	}
	if strings.Contains(all, "Setting up your records") || strings.Contains(all, "I made the table") || strings.Contains(all, "has no") {
		t.Errorf("second time: %s", all)
	}
}
