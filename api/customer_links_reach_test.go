package api_test

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
)

// The shared number's new words (docs/spec/taskiem-for-everyone.md §12):
// updates end "Reply in the Taskiem app: {link}" for a business with no
// number of its own, inside WhatsApp's 24 hours (and outside them only in
// taskiem_update_chat, once the deployment says it is approved); the
// business can turn it off; an outsider replying gets "To chat with
// {business}, open {link}" once per business per day, then the fixed
// answer; on Telegram, the same.
func TestSharedNumberChatWords(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	w.srv.ChatLinks = &chatlinks.Links{Pool: w.env.DB.App, Key: bytes.Repeat([]byte{8}, 32), Base: waPublicURL}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	shift := publishFlow(t, owner, shiftFlow)
	link := owner.must(200, "GET", "/v1/customers/chat-link", nil)["url"].(string)
	if !strings.HasPrefix(link, waPublicURL+"/m/") {
		t.Fatalf("link: %s", link)
	}

	// Chidi taps WhatsApp on the business's standing page.
	pageURL := owner.must(200, "GET", "/v1/reach", nil)["page_url"].(string)
	path := strings.TrimPrefix(pageURL, "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	const chidi = "+2348039998888"
	w.whatsapp(t, chidi, loc.Query().Get("text"))
	update := func(to string) {
		t.Helper()
		owner.must(201, "POST", "/v1/workflows/"+shift+"/runs", map[string]any{"input": map[string]any{"phone": to}})
		w.env.Drain(t)
		w.tick(t)
	}

	// Inside the 24 hours: the link ends the update.
	update(chidi)
	if last := w.graph.Last(t, chidi); last.Type != "text" || !strings.Contains(last.Text, "Your shift starts") ||
		!strings.HasSuffix(last.Text, "Reply in the Taskiem app: "+link) {
		t.Fatalf("inside the window: %+v", last)
	}
	// Outside them, until the template is approved: taskiem_update, no link.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, chidi); err != nil {
		t.Fatal(err)
	}
	update(chidi)
	if last := w.graph.Last(t, chidi); last.Template != "taskiem_update" || strings.Contains(last.Text, "/m/") {
		t.Fatalf("outside the window: %+v", last)
	}
	// Approved: taskiem_update_chat, its button the link.
	w.srv.UpdateChatTemplate = true
	update(chidi)
	if last := w.graph.Last(t, chidi); last.Template != "taskiem_update_chat" || !strings.Contains(last.Text, "Reply in the Taskiem app.") {
		t.Fatalf("the approved template: %+v", last)
	}
	// Turned off by the business: neither.
	owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"link_in_updates": false})
	update(chidi)
	if last := w.graph.Last(t, chidi); last.Template != "taskiem_update" {
		t.Fatalf("turned off: %+v", last)
	}
	owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"link_in_updates_default": true})

	// Chidi replies: pointed to the chat link, once a day; then the fixed
	// answer.
	if sent := w.whatsapp(t, chidi, "Is my shift still on?"); len(sent) != 1 || sent[0].Text != "To chat with Ada Stores, open "+link {
		t.Fatalf("a reply: %+v", sent)
	}
	if sent := w.whatsapp(t, chidi, "Hello?"); len(sent) != 1 || !strings.Contains(sent[0].Text, "updates from Ada Stores. Send STOP") {
		t.Fatalf("a second reply: %+v", sent)
	}
	// A day later, the link again.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_chat_notices SET last_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	w.srv.ResetLimiters()
	if sent := w.whatsapp(t, chidi, "Hello again"); len(sent) != 1 || sent[0].Text != "To chat with Ada Stores, open "+link {
		t.Fatalf("a day later: %+v", sent)
	}

	// On Telegram: the link ends every update (no window there).
	const bola = int64(7101)
	tgTo, _ := url.Parse(w.page(t, path+"/telegram", nil).Header().Get("Location"))
	start := strings.TrimPrefix(tgTo.Query().Get("start"), "u_")
	if start == "" {
		t.Fatalf("the Telegram button: %v", tgTo)
	}
	w.telegram(t, bola, func(id int64) []byte { return telegramtest.Start(id, bola, "", "u_"+start) })
	w.telegram(t, bola, func(id int64) []byte { return telegramtest.ContactMessage(id, bola, "+234 803 123 4567", bola) })
	update("+2348031234567")
	if last := w.bot.Last(t, bola); !strings.Contains(last.Text, "Your shift starts") || !strings.HasSuffix(last.Text, "Reply in the Taskiem app: "+link) {
		t.Fatalf("on Telegram: %+v", last)
	}
	w.srv.ResetLimiters()
	got := w.telegram(t, bola, func(id int64) []byte { return telegramtest.Message(id, bola, "", "thanks") })
	if len(got) != 1 || got[0].Text != "To chat with Ada Stores, open "+link {
		t.Fatalf("a reply on Telegram: %+v", got)
	}
}
