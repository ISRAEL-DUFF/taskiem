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

// A text with a web address in it asks WhatsApp for the page's preview;
// one without does not (docs/spec/chat-previews-and-editing.md §2.2).
func TestSendTextPreviewURL(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		got = append(got, v["text"].(map[string]any))
		_, _ = io.WriteString(w, `{"messages":[{"id":"wamid.1"}]}`)
	}))
	t.Cleanup(srv.Close)
	c := &whatsapp.Client{BaseURL: srv.URL, PhoneNumberID: "PN", Token: "t", HTTP: srv.Client()}
	for _, body := range []string{"See jumia.com.ng/phones", "No address here.", "javascript:alert(1)"} {
		if _, err := c.SendText(context.Background(), "+2348010000001", body); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 || got[0]["preview_url"] != true || got[1]["preview_url"] != false || got[2]["preview_url"] != false {
		t.Fatalf("preview_url: %v", got)
	}
}
