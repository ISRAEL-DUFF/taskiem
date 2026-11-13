package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Regression tests for the chat findings of the second audit (E1 to E7):
// each was written to fail on the code as it stood.

// bindWA links a number to the client's person through the web app.
func (w *reachWorld) bindWA(t *testing.T, c *client, number string) {
	t.Helper()
	c.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": number, "password": testPassword})
	last := w.graph.Last(t, number)
	if last.Template != "taskiem_otp" {
		t.Fatalf("code message: %+v", last)
	}
	c.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(last.Text)})
}

// standingCode is the message a business's standing page fills in for
// WhatsApp, and the code a Telegram link carries.
func (w *reachWorld) standingPath(t *testing.T, owner *client) string {
	t.Helper()
	pageURL, _ := owner.must(200, "GET", "/v1/reach", nil)["page_url"].(string)
	if !strings.HasPrefix(pageURL, "https://"+reachFormHost+"/u/") {
		t.Fatalf("reach page: %q", pageURL)
	}
	return strings.TrimPrefix(pageURL, "https://"+reachFormHost)
}

// TestSomeoneOnTaskiemIsACustomerElsewhere (E2): a person with an account
// who writes to another business's own number is its customer, with
// nothing of their own businesses reachable there.
func TestSomeoneOnTaskiemIsACustomerElsewhere(t *testing.T) {
	w := newWAWorld(t)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Mama Put Kitchen", "owner@mamaput.test")
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	shop := w.tenant(t, "Chidi Shop", "chidi@chidi.test")
	const chidi = "+2348031112233"
	w.bind(t, shop, chidi)
	say := func(text string) []whatsapptest.Sent {
		t.Helper()
		return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, chidi, whatsapptest.Text(text))
	}
	if got := say("Do you deliver to Yaba?"); len(got) != 0 {
		t.Fatalf("the customer was answered: %+v", got)
	}
	chats := alpha.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(chats) != 1 || !strings.Contains(chats[0].(map[string]any)["last"].(string), "Yaba") {
		t.Fatalf("chats: %s", toJSON(chats))
	}
	// Their own business's words do nothing there: they are more of the
	// customer's messages.
	for _, word := range []string{"status", "help", "approvals", "invite +2348012340000 as viewer"} {
		if got := say(word); len(got) != 0 {
			t.Fatalf("%q was answered on another business's number: %+v", word, got)
		}
	}
	// STOP is the customer's.
	if got := say("STOP"); len(got) != 1 || !strings.Contains(got[0].Text, "no longer get messages from Mama Put Kitchen") {
		t.Fatalf("STOP: %+v", got)
	}
	// On the shared number they are still themselves.
	if got := w.say(t, chidi, "help"); !strings.Contains(got, "status") {
		t.Fatalf("the shared number: %q", got)
	}
}
