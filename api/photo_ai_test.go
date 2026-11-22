package api_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
)

// pngHeader is enough of a PNG for the server to know it as one.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00")

func photoForm(t *testing.T, data []byte) (string, []byte) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, err := mw.CreateFormFile("photo", "list.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	return mw.FormDataContentType(), b.Bytes()
}

// Reading a photo with AI (I3): off until an owner turns it on; then the
// photo goes to the model as an image, its words come back to be read as
// a phone's are, the call counts against the AI budget, and nothing of
// the photo is kept.
func TestReadPhotoWithAI(t *testing.T) {
	w := newWAWorld(t)
	var got []ai.Request
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Models: "claude-opus-5-5", Respond: func(r ai.Request) (*ai.Response, error) {
		got = append(got, r)
		return &ai.Response{Text: "Chidi 0l23456789 GTB 45k\nBola 2034567891 UBA 5k", StopReason: "end_turn", Usage: ai.Usage{InputTokens: 1700, OutputTokens: 30}}, nil
	}}}
	owner := w.tenant(t, "Ada Foods", "ada@photoai.test")
	member := w.member(t, owner, "musa@photoai.test", "admin")
	ctype, body := photoForm(t, pngHeader)

	// Off: refused, and nothing reaches the model.
	if got := owner.must(200, "GET", "/v1/settings/ai-photo-reading", nil); got["ai_photo_reading"] != false || got["available"] != true {
		t.Fatalf("setting: %v", got)
	}
	if code, e := owner.rawPost(t, "/v1/photo-text", ctype, body); code != 409 || e["code"] != "off" {
		t.Fatalf("off: %d %v", code, e)
	}
	// Only an owner turns it on.
	member.must(403, "PUT", "/v1/settings/ai-photo-reading", map[string]any{"ai_photo_reading": true})
	owner.must(200, "PUT", "/v1/settings/ai-photo-reading", map[string]any{"ai_photo_reading": true})

	// On: anyone in the business may have a photo read.
	code, out := member.rawPost(t, "/v1/photo-text", ctype, body)
	if code != 200 || !strings.HasPrefix(out["text"].(string), "Chidi 0l23456789") {
		t.Fatalf("read: %d %v", code, out)
	}
	if len(got) != 1 || len(got[0].Messages) != 1 || len(got[0].Messages[0].Images) != 1 || got[0].Messages[0].Images[0].MediaType != "image/png" {
		t.Fatalf("to the model: %+v", got)
	}
	// Its words read as a phone's are.
	if r := owner.must(200, "POST", "/v1/payment-batches/read", map[string]any{"text": out["text"], "from": "photo"}); !strings.Contains(toJSON(r), "0123456789") {
		t.Fatalf("read as a list: %v", r)
	}
	// Counted, with no words kept.
	var n int
	var msgs string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*), COALESCE(max(messages::text), '') FROM ai_interactions WHERE kind = 'photo_text' AND total_tokens > 0`).Scan(&n, &msgs); err != nil || n != 1 || strings.Contains(msgs, "Chidi") {
		t.Fatalf("recorded: %d %q %v", n, msgs, err)
	}

	// Not a photo.
	ctype2, body2 := photoForm(t, []byte("%PDF-1.4 not a photo"))
	if code, e := member.rawPost(t, "/v1/photo-text", ctype2, body2); code != 400 || e["code"] != "not_a_photo" {
		t.Fatalf("not a photo: %d %v", code, e)
	}

	// No model here: said so.
	w.srv.AI = nil
	if code, e := member.rawPost(t, "/v1/photo-text", ctype, body); code != 503 || e["code"] != "unavailable" {
		t.Fatalf("no model: %d %v", code, e)
	}
}
