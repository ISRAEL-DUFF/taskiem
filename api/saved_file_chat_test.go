package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A saved file told to the owner on WhatsApp (H6 review): inside the
// window the message, the file's name and its link; outside it the alert
// template, whose link is the file's, so a long message cut to fit the
// template's line never cuts the link.
func TestSavedFileByChat(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	w.srv.Media = newProjectStorage(t).service()
	w.srv.Media.Scanner = chatScanner{}
	if err := w.env.Registry.Register(notify.New(&notify.Notifier{Pool: w.env.Store.Pool, Chat: []string{"whatsapp"},
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Files:    api.SavedFileLinks{Pool: w.env.Store.Pool, BaseURL: waPublicURL}})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@savedchat.test")
	const on = "+2348010000077"
	w.bind(t, owner, on)
	_, up := owner.postFile(t, "/v1/saved/uploads", "prices.png", pngPhoto(t, 70), nil)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "file",
		"details": map[string]any{"file": up["upload"].(map[string]any)["file"]}})
	long := strings.Repeat("Prices went up again this week. ", 20)
	def := `{"schema":"wd/v1","id":"wf_tellfile","version":1,"name":"Tell the prices","trigger":{"type":"manual"},
	 "steps":[{"id":"owner","type":"connector","connector":"notify@1","action":"tell",
	   "input":{"title":"This week's prices","message":"` + long + `","file":"=saved.price_list"}}]}`
	wf := publishFlow(t, owner, def)
	link := waPublicURL + "/v1/saved-files/"

	// Inside the window: the message, the file's name, its link.
	w.say(t, on, "status")
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	w.tick(t)
	got := w.graph.Last(t, on)
	if got.Template != "" || !strings.Contains(got.Text, "prices.png") || !strings.Contains(got.Text, link) {
		t.Fatalf("inside the window: %+v", got)
	}
	// Outside it: the template, the link whole in its own place.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, on); err != nil {
		t.Fatal(err)
	}
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	w.tick(t)
	got = w.graph.Last(t, on)
	i := strings.Index(got.Text, link)
	if got.Template == "" || i < 0 || len(strings.Fields(got.Text[i+len(link):])[0]) != 43 {
		t.Fatalf("outside the window: %+v", got)
	}
}
