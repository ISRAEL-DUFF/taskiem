package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
)

const botToken = "123456:ABC-DEF"

// botAPI is a fake Bot API with its file host.
type botAPI struct {
	srv      *httptest.Server
	voice    []byte
	noPath   bool
	gone     bool
	form     map[string]string
	upload   []byte
	upName   string
	actions  []map[string]any
	lastJSON map[string]any
}

func newBot(t *testing.T) *botAPI {
	b := &botAPI{voice: []byte("OggS customer voice note")}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := func(result any) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result}) }
		switch {
		case r.URL.Path == "/bot"+botToken+"/getFile":
			if b.noPath {
				ok(map[string]any{"file_id": "f1", "file_size": 30 << 20})
				return
			}
			ok(map[string]any{"file_id": "f1", "file_size": len(b.voice), "file_path": "voice/file_7.oga"})
		case r.URL.Path == "/file/bot"+botToken+"/voice/file_7.oga":
			if b.gone {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(b.voice)
		case strings.HasPrefix(r.URL.Path, "/bot"+botToken+"/send") && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				w.WriteHeader(400)
				return
			}
			b.form = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				b.form[k] = v[0]
			}
			for k, fh := range r.MultipartForm.File {
				f, _ := fh[0].Open()
				b.upload, _ = io.ReadAll(f)
				b.upName = k + ":" + fh[0].Filename
			}
			ok(map[string]any{"message_id": 77, "chat": map[string]any{"id": 42, "type": "private"}, "date": 1})
		case r.URL.Path == "/bot"+botToken+"/sendChatAction" || r.URL.Path == "/bot"+botToken+"/sendVoice":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			b.lastJSON = m
			if strings.HasSuffix(r.URL.Path, "sendChatAction") {
				b.actions = append(b.actions, m)
				ok(true)
				return
			}
			ok(map[string]any{"message_id": 78, "chat": map[string]any{"id": 42, "type": "private"}, "date": 1})
		default:
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 404, "description": "Not Found"})
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *botAPI) call(action string, in map[string]any, files connector.Files) (connector.Response, error) {
	c := New(Options{BaseURL: b.srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: in, HTTP: b.srv.Client(),
		Credentials: map[string]string{"bot_token": botToken}, Attempt: 1, Files: files})
}

func TestGetFileKeepsTheVoiceNote(t *testing.T) {
	b := newBot(t)
	fs := runfilestest.NewFiles()
	r, err := b.call("get_file", map[string]any{"file_id": "f1"}, fs)
	if err != nil {
		t.Fatal(err)
	}
	ref := r.Output.(map[string]any)["file"].(map[string]any)
	kept, personal := fs.Kept(ref["file"].(string))
	if string(kept) != string(b.voice) || !personal || ref["name"] != "telegram-file_7.oga" {
		t.Fatalf("kept %q personal %v ref %v", kept, personal, ref)
	}
	raw, _ := json.Marshal(r.Output)
	if strings.Contains(string(raw), "customer voice") || strings.Contains(string(raw), botToken) {
		t.Fatalf("output carries bytes or the token: %s", raw)
	}
}

func TestGetFileRefusals(t *testing.T) {
	b := newBot(t)
	b.noPath = true
	if _, err := b.call("get_file", map[string]any{"file_id": "f1"}, runfilestest.NewFiles()); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("over 20 MB: %v", err)
	}
	b.noPath, b.gone = false, true
	_, err := b.call("get_file", map[string]any{"file_id": "f1"}, runfilestest.NewFiles())
	if !errors.Is(err, effects.ErrRetryable) || strings.Contains(err.Error(), botToken) {
		t.Fatalf("an expired link: %v (the token must never show)", err)
	}
	b.gone = false
	fs := runfilestest.NewFiles()
	fs.Refuse = true
	if _, err := b.call("get_file", map[string]any{"file_id": "f1"}, fs); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("refused by the store: %v", err)
	}
	if _, err := b.call("get_file", map[string]any{"file_id": "f1"}, nil); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("no store: %v", err)
	}
}

func TestSendVoiceFromARunFile(t *testing.T) {
	b := newBot(t)
	fs := runfilestest.NewFiles()
	ref, _ := fs.Put(context.Background(), "reply.ogg", []byte("OggS spoken reply"), false)
	r, err := b.call("send_voice", map[string]any{"chat_id": 42, "file": ref, "reply_to_message_id": 5}, fs)
	if err != nil {
		t.Fatal(err)
	}
	if string(b.upload) != "OggS spoken reply" || b.upName != "voice:reply.ogg" || b.form["chat_id"] != "42" {
		t.Fatalf("uploaded %q as %q with %v", b.upload, b.upName, b.form)
	}
	if r.Output.(map[string]any)["message_id"] == nil {
		t.Fatalf("output %v", r.Output)
	}
	// By file_id, as JSON.
	if _, err := b.call("send_voice", map[string]any{"chat_id": 42, "voice": "AwACAgQ"}, fs); err != nil || b.lastJSON["voice"] != "AwACAgQ" {
		t.Fatalf("by file_id: %v %v", err, b.lastJSON)
	}
	pdf, _ := fs.Put(context.Background(), "a.pdf", []byte("%PDF-1.4"), false)
	for name, in := range map[string]map[string]any{
		"a PDF as voice": {"chat_id": 42, "file": pdf},
		"two sources":    {"chat_id": 42, "file": ref, "voice": "x"},
		"no source":      {"chat_id": 42},
	} {
		if _, err := b.call("send_voice", in, fs); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: want a fatal refusal, got %v", name, err)
		}
	}
	// sendAudio takes MP3 or M4A only.
	if _, err := b.call("send_audio", map[string]any{"chat_id": 42, "file": ref}, fs); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("Ogg as music: %v", err)
	}
	mp3, _ := fs.Put(context.Background(), "a.mp3", []byte("ID3 song"), false)
	if _, err := b.call("send_audio", map[string]any{"chat_id": 42, "file": mp3}, fs); err != nil || b.upName != "audio:a.mp3" {
		t.Errorf("MP3: %v %s", err, b.upName)
	}
}

func TestSendChatAction(t *testing.T) {
	b := newBot(t)
	if _, err := b.call("send_chat_action", map[string]any{"chat_id": 42, "action": "record_voice"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.call("send_chat_action", map[string]any{"chat_id": 42}, nil); err != nil {
		t.Fatal(err)
	}
	if len(b.actions) != 2 || b.actions[0]["action"] != "record_voice" || b.actions[1]["action"] != "typing" {
		t.Fatalf("actions %v", b.actions)
	}
	if _, err := b.call("send_chat_action", map[string]any{"chat_id": 42, "action": "dance"}, nil); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("an unknown action: %v", err)
	}
}
