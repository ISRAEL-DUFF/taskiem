package whatsapp

import (
	"time"

	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/media/mediatest"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
)

// graph is a fake Graph API with a media host on the same server.
type graph struct {
	srv      *httptest.Server
	voice    []byte
	digest   string
	uploaded []byte
	upType   string
	sent     []map[string]any
	expired  bool
	noTyping bool
}

func newGraph(t *testing.T) *graph {
	g := &graph{voice: mediatest.OggOpus(2 * time.Second)}
	sum := sha256.Sum256(g.voice)
	g.digest = hex.EncodeToString(sum[:])
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer EAAtest" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/media-1" && r.URL.Query().Get("phone_number_id") != "106540352242922":
			// Meta answers only for media sent to the number asked about.
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"(#100) Media does not belong to this number","type":"OAuthException","code":100}}`))
		case r.Method == "GET" && r.URL.Path == "/media-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": g.srv.URL + "/download/media-1", "mime_type": "audio/ogg; codecs=opus", "sha256": g.digest, "file_size": len(g.voice), "id": "media-1"})
		case r.Method == "GET" && r.URL.Path == "/media-elsewhere":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://evil.example/x", "mime_type": "audio/ogg"})
		case r.Method == "GET" && r.URL.Path == "/download/media-1":
			if g.expired {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(g.voice)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/media"):
			if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("messaging_product") != "whatsapp" {
				w.WriteHeader(400)
				return
			}
			f, h, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(400)
				return
			}
			g.uploaded, _ = io.ReadAll(f)
			g.upType = r.FormValue("type") + "|" + h.Header.Get("Content-Type")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "uploaded-9"})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/messages"):
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			g.sent = append(g.sent, b)
			if b["status"] == "read" && b["typing_indicator"] != nil && g.noTyping {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"message":"(#100) Param typing_indicator is not supported","type":"OAuthException","code":100}}`))
				return
			}
			if b["status"] == "read" {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messaging_product": "whatsapp", "contacts": []any{map[string]any{"input": b["to"], "wa_id": b["to"]}}, "messages": []any{map[string]any{"id": "wamid.out"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *graph) call(action string, in map[string]any, files connector.Files) (connector.Response, error) {
	c := New(Options{BaseURL: g.srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: in, HTTP: g.srv.Client(), Credentials: creds, Attempt: 1, Files: files})
}

func TestGetMediaKeepsTheVoiceNoteAsARunFile(t *testing.T) {
	g := newGraph(t)
	fs := runfilestest.NewFiles()
	r, err := g.call("get_media", map[string]any{"media_id": "media-1"}, fs)
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	ref := o["file"].(map[string]any)
	if ref["name"] != "whatsapp-media-1.ogg" || o["mime_type"] != "audio/ogg; codecs=opus" {
		t.Fatalf("output %v", o)
	}
	kept, personal := fs.Kept(ref["file"].(string))
	if string(kept) != string(g.voice) || !personal {
		t.Fatal("the voice note was not kept as a person's file")
	}
	if _, err := json.Marshal(o); err != nil || strings.Contains(string(mustJSON(o)), "voice note bytes") {
		t.Fatal("the bytes reached the step's output")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestGetMediaRefusals(t *testing.T) {
	g := newGraph(t)
	// Not on Meta's media host: never fetched.
	if _, err := g.call("get_media", map[string]any{"media_id": "media-elsewhere"}, runfilestest.NewFiles()); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("a URL elsewhere: %v", err)
	}
	// A digest that does not match: retried, never kept.
	g.digest = strings.Repeat("0", 64)
	fs := runfilestest.NewFiles()
	if _, err := g.call("get_media", map[string]any{"media_id": "media-1"}, fs); !errors.Is(err, effects.ErrRetryable) || fs.Len() != 0 {
		t.Fatalf("a wrong digest: %v, %d kept", err, fs.Len())
	}
	// The short-lived URL expired: retried (the id is looked up again).
	g = newGraph(t)
	g.expired = true
	if _, err := g.call("get_media", map[string]any{"media_id": "media-1"}, runfilestest.NewFiles()); !errors.Is(err, effects.ErrRetryable) {
		t.Fatalf("an expired URL: %v", err)
	}
	// The store refuses it (malware, too long): fatal.
	g.expired = false
	fs = runfilestest.NewFiles()
	fs.Refuse = true
	if _, err := g.call("get_media", map[string]any{"media_id": "media-1"}, fs); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("a refused file: %v", err)
	}
	// No file store: fatal, plainly.
	if _, err := g.call("get_media", map[string]any{"media_id": "media-1"}, nil); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("no store: %v", err)
	}
	if _, err := g.call("get_media", map[string]any{"media_id": "../x"}, runfilestest.NewFiles()); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("a bad id: %v", err)
	}
}

func TestSendAVoiceNoteFromARunFile(t *testing.T) {
	g := newGraph(t)
	fs := runfilestest.NewFiles()
	spoken := mediatest.OggOpus(3 * time.Second)
	ref, _ := fs.Put(context.Background(), "reply.ogg", spoken, false)
	r, err := g.call("send_media", map[string]any{"to": "2348031234567", "media_type": "audio", "file": ref, "voice": true}, fs)
	if err != nil {
		t.Fatal(err)
	}
	if string(g.uploaded) != string(spoken) || g.upType != "audio/ogg|audio/ogg" {
		t.Fatalf("uploaded %q as %q", g.uploaded, g.upType)
	}
	audio := g.sent[0]["audio"].(map[string]any)
	if audio["id"] != "uploaded-9" || audio["voice"] != true || out(r)["message_id"] != "wamid.out" {
		t.Fatalf("sent %v, output %v", g.sent[0], out(r))
	}
	// A voice note must be Ogg/Opus; voice is for audio only; one source.
	mp3, _ := fs.Put(context.Background(), "a.mp3", []byte("ID3 mp3"), false)
	vorbis, _ := fs.Put(context.Background(), "v.ogg", mediatest.OggVorbis(), false)
	for name, in := range map[string]map[string]any{
		"mp3 as voice":    {"to": "234", "media_type": "audio", "file": mp3, "voice": true},
		"vorbis as voice": {"to": "234", "media_type": "audio", "file": vorbis, "voice": true},
		"image as voice":  {"to": "234", "media_type": "image", "link": "https://x.test/a.jpg", "voice": true},
		"two sources":     {"to": "234", "media_type": "audio", "file": ref, "media_id": "m"},
		"no source":       {"to": "234", "media_type": "audio"},
		"unknown file":    {"to": "234", "media_type": "audio", "file": map[string]any{"file": "nope"}},
	} {
		if _, err := g.call("send_media", in, fs); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: want a fatal refusal, got %v", name, err)
		}
	}
}

func TestUploadMediaAndTyping(t *testing.T) {
	g := newGraph(t)
	fs := runfilestest.NewFiles()
	ref, _ := fs.Put(context.Background(), "doc.pdf", []byte("%PDF-1.4"), false)
	r, err := g.call("upload_media", map[string]any{"file": ref}, fs)
	if err != nil || out(r)["media_id"] != "uploaded-9" {
		t.Fatalf("upload: %v %v", r, err)
	}
	r, err = g.call("send_typing", map[string]any{"message_id": wamid}, nil)
	if err != nil || out(r)["success"] != true {
		t.Fatalf("typing: %v %v", r, err)
	}
	last := g.sent[len(g.sent)-1]
	if last["status"] != "read" || last["message_id"] != wamid || last["typing_indicator"].(map[string]any)["type"] != "text" {
		t.Fatalf("typing sent %v", last)
	}
	if _, err := g.call("send_typing", map[string]any{}, nil); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("typing without a message: %v", err)
	}
}

func TestTypingFallsBackToAReadReceipt(t *testing.T) {
	g := newGraph(t)
	g.noTyping = true
	r, err := g.call("send_typing", map[string]any{"message_id": wamid}, nil)
	if err != nil || out(r)["typing"] != false || out(r)["success"] != true {
		t.Fatalf("%v %v", r, err)
	}
	last := g.sent[len(g.sent)-1]
	if last["status"] != "read" || last["typing_indicator"] != nil {
		t.Fatalf("the fallback sent %v", last)
	}
}

func TestOnlyMetaHostsAreFollowed(t *testing.T) {
	for h, ok := range map[string]bool{"lookaside.fbsbx.com": true, "scontent.xx.fbcdn.net": true, "mmg.whatsapp.net": true,
		"evil.example": false, "fbcdn.net.evil.example": false, "lookaside.fbsbx.com.evil.example": false} {
		if metaMediaHost(h) != ok {
			t.Errorf("%s: want %v", h, ok)
		}
	}
}
