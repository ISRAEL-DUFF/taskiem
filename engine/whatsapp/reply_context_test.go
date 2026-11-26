package whatsapp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A text sent as a reply names the message it quotes (context.message_id),
// as the "✏️ Edited" follow-up does (docs/spec/chat-previews-and-editing.md
// §3.3); a plain text names none.
func TestSendTextReplyContext(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		got = append(got, v)
		_, _ = io.WriteString(w, `{"messages":[{"id":"wamid.2"}]}`)
	}))
	t.Cleanup(srv.Close)
	c := &whatsapp.Client{BaseURL: srv.URL, PhoneNumberID: "PN", Token: "t", HTTP: srv.Client()}
	if _, err := c.SendTextReply(context.Background(), "+2348010000001", "✏️ Edited: *Rice is ₦44,000*", "wamid.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendText(context.Background(), "+2348010000001", "Hello"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("sent %d", len(got))
	}
	if ctx, ok := got[0]["context"].(map[string]any); !ok || ctx["message_id"] != "wamid.1" {
		t.Fatalf("the reply's context: %v", got[0])
	}
	if _, ok := got[1]["context"]; ok {
		t.Fatalf("a plain text has a context: %v", got[1])
	}
}
