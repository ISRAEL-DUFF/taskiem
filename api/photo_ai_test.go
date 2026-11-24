package api_test

import (
	"bytes"
	"context"
	"errors"
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

// A photo read with AI needs the plan's AI feature, as every other model
// call does: without it the setting says not available and a read is
// refused in words before anything reaches the model.
func TestReadPhotoWithAIPlan(t *testing.T) {
	w := billingWorld(t)
	var calls int
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Models: "claude-opus-5-5", Respond: func(ai.Request) (*ai.Response, error) {
		calls++
		return &ai.Response{Text: "Chidi 0123456789 GTB 45k", StopReason: "end_turn"}, nil
	}}}
	owner := w.tenant(t, "Shop Ltd", "owner@photoplan.test")
	owner.must(200, "PUT", "/v1/settings/ai-photo-reading", map[string]any{"ai_photo_reading": true})
	ctype, body := photoForm(t, pngHeader)
	// The trial's plan has AI.
	if got := owner.must(200, "GET", "/v1/settings/ai-photo-reading", nil); got["available"] != true {
		t.Fatalf("on growth: %v", got)
	}
	if code, out := owner.rawPost(t, "/v1/photo-text", ctype, body); code != 200 || calls != 1 {
		t.Fatalf("on growth: %d %v", code, out)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE subscriptions SET plan_id = 'shop' WHERE tenant_id = (SELECT id FROM tenants WHERE name = 'Shop Ltd')`); err != nil {
		t.Fatal(err)
	}
	if got := owner.must(200, "GET", "/v1/settings/ai-photo-reading", nil); got["available"] != false || got["ai_photo_reading"] != true {
		t.Fatalf("on shop: %v", got)
	}
	if got := owner.must(200, "PUT", "/v1/settings/ai-photo-reading", map[string]any{"ai_photo_reading": true}); got["available"] != false {
		t.Fatalf("on shop, put: %v", got)
	}
	code, e := owner.rawPost(t, "/v1/photo-text", ctype, body)
	if code != 402 || e["code"] != "plan" || !strings.Contains(e["error"].(string), "plan") || calls != 1 {
		t.Fatalf("on shop: %d %v (calls %d)", code, e, calls)
	}
}

// A photo over the Messages API's 5 MB is refused here; one the provider
// still refuses as an image is said to be too large (not "try again"); an
// answer cut off at the token limit loses its last line and says so.
func TestReadPhotoWithAILimits(t *testing.T) {
	w := newWAWorld(t)
	var respond func(ai.Request) (*ai.Response, error)
	var got []ai.Request
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Models: "claude-opus-5-5", Respond: func(r ai.Request) (*ai.Response, error) {
		got = append(got, r)
		return respond(r)
	}}}
	owner := w.tenant(t, "Bola Foods", "bola@photolimits.test")
	owner.must(200, "PUT", "/v1/settings/ai-photo-reading", map[string]any{"ai_photo_reading": true})
	ctype, body := photoForm(t, pngHeader)

	// Over 5 MB: refused before the model.
	big := append(append([]byte{}, pngHeader...), make([]byte, 5<<20)...)
	ctypeBig, bodyBig := photoForm(t, big)
	if code, e := owner.rawPost(t, "/v1/photo-text", ctypeBig, bodyBig); code != 400 || e["code"] != "no_photo" || !strings.Contains(e["error"].(string), "5 MB") || len(got) != 0 {
		t.Fatalf("over 5 MB: %d %v", code, e)
	}

	// The provider refuses the image: too large, in words.
	respond = func(ai.Request) (*ai.Response, error) {
		return nil, &ai.StatusError{Status: 400, Err: errors.New("anthropic: 400 invalid_request_error: messages.0.content.0.image.source.base64: image exceeds 5 MB maximum")}
	}
	if code, e := owner.rawPost(t, "/v1/photo-text", ctype, body); code != 400 || e["code"] != "too_large" || e["error"] != "That photo is too large to read: try a smaller one." {
		t.Fatalf("provider refused the image: %d %v", code, e)
	}
	// Another failure is still "try again".
	respond = func(ai.Request) (*ai.Response, error) {
		return nil, &ai.StatusError{Status: 529, Err: errors.New("anthropic: 529 overloaded")}
	}
	if code, e := owner.rawPost(t, "/v1/photo-text", ctype, body); code != 502 || e["code"] != "ai_down" {
		t.Fatalf("provider down: %d %v", code, e)
	}

	// Cut off at the token limit: the last (half-written) line dropped.
	respond = func(ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: "Chidi 0123456789 GTB 45k\nBola 2034567891 UBA 5k\nEmeka 30456", StopReason: ai.StopMaxTokens}, nil
	}
	code, out := owner.rawPost(t, "/v1/photo-text", ctype, body)
	if code != 200 || out["text"] != "Chidi 0123456789 GTB 45k\nBola 2034567891 UBA 5k" || out["truncated"] != true {
		t.Fatalf("truncated: %d %v", code, out)
	}
	if got[len(got)-1].MaxTokens != 8000 {
		t.Fatalf("max tokens: %d", got[len(got)-1].MaxTokens)
	}
	// A whole answer is not truncated.
	respond = func(ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: "Chidi 0123456789 GTB 45k", StopReason: "end_turn"}, nil
	}
	if code, out := owner.rawPost(t, "/v1/photo-text", ctype, body); code != 200 || out["truncated"] != false || out["text"] != "Chidi 0123456789 GTB 45k" {
		t.Fatalf("whole: %d %v", code, out)
	}
}
