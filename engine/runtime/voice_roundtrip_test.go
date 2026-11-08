package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/whatsapp"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/media/mediatest"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const speechManifest = `
manifest: connector/v1
id: speech
version: 1.0.0
name: Speech stand-in
category: other
auth: { type: none }
actions:
  transcribe:
    title: Transcribe a voice note
    class: read
    input: { type: object, required: [file], properties: { file: {} } }
  speak:
    title: Speak a reply
    class: read
    input: { type: object, required: [text], properties: { text: { type: string } } }
`

// A customer's voice note goes the whole way (decision 0062, review gap
// 20): WhatsApp's media is fetched from a fake Graph API and kept as a run
// file tied to the sender; a stand-in for speech reads it and speaks a
// reply as a new run file; send_media uploads that reply to Meta and sends
// it as a voice note. Only references travel between the steps.
func TestWhatsAppVoiceRoundTrip(t *testing.T) {
	e := rt.New(t)
	store := runfilestest.New()
	var subjects sync.Map
	e.RunFiles = &runfiles.Service{Pool: e.DB.App, Store: store, Subject: func(_ context.Context, _ uuid.UUID, category, value string) (string, error) {
		subjects.Store(category+":"+value, true)
		return "subject-" + value, nil
	}}

	heard := mediatest.OggOpus(2 * time.Second)
	spoken := mediatest.OggOpus(3 * time.Second)
	meta := newMetaFake(t, heard)
	registerWhatsApp(t, e, meta)
	speech := &connector.Connector{Manifest: connector.MustParse([]byte(speechManifest)), Actions: map[string]connector.Action{
		"transcribe": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			b, ref, err := req.Files.Read(ctx, req.Input["file"])
			if err != nil {
				return connector.Response{}, err
			}
			if string(b) != string(heard) || ref["name"] != "voice-note.ogg" {
				t.Errorf("transcribe read %d bytes named %v", len(b), ref["name"])
			}
			return connector.Response{Output: map[string]any{"text": "Do you have rice?"}}, nil
		}),
		"speak": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			ref, err := req.Files.Put(ctx, "reply.ogg", spoken, false)
			return connector.Response{Output: map[string]any{"file": ref}}, err
		}),
	}}
	if err := e.Registry.Register(speech); err != nil {
		t.Fatal(err)
	}

	wf := e.Publish(t, wfDoc(`{"id":"fetch","type":"connector","connector":"whatsapp@1","action":"get_media","input":{"media_id":"=trigger.media_id","sender":"=trigger.from"}},
		{"id":"hear","type":"connector","connector":"speech@1","action":"transcribe","needs":["fetch"],"input":{"file":"=steps.fetch.output.file"}},
		{"id":"say","type":"connector","connector":"speech@1","action":"speak","needs":["hear"],"input":{"text":"='Yes. ' + steps.hear.output.text"}},
		{"id":"reply","type":"connector","connector":"whatsapp@1","action":"send_media","needs":["say"],"input":{"to":"=trigger.from","media_type":"audio","file":"=steps.say.output.file","voice":true}}`, ""))
	ref := e.Start(t, wf, map[string]any{"media_id": "media-7", "from": "2348031234567"})
	rt.WaitFor(t, 20*time.Second, "run to end", func() bool {
		e.Drain(t)
		st := e.Status(t, ref)
		return st == "completed" || st == "failed"
	})
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}

	meta.mu.Lock()
	defer meta.mu.Unlock()
	if string(meta.uploaded) != string(spoken) {
		t.Fatalf("Meta got %d bytes, not the spoken reply", len(meta.uploaded))
	}
	if len(meta.sent) != 1 {
		t.Fatalf("%d messages sent, want one", len(meta.sent))
	}
	audio, _ := meta.sent[0]["audio"].(map[string]any)
	if meta.sent[0]["to"] != "2348031234567" || audio["id"] != "uploaded-1" || audio["voice"] != true {
		t.Fatalf("sent %v", meta.sent[0])
	}
	// Both files kept; only the customer's scanned and tied to them.
	if store.Count() != 2 || store.Scanned() != 1 {
		t.Fatalf("%d kept, %d scanned: want 2 kept, the customer's scanned", store.Count(), store.Scanned())
	}
	if _, ok := subjects.Load("phone:2348031234567"); !ok {
		t.Fatal("the customer's voice note is not tied to them")
	}
	var subject string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT coalesce(string_agg(subject, ','), '') FROM run_files WHERE subject IS NOT NULL`).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if subject != "subject-2348031234567" {
		t.Fatalf("subjects kept: %q", subject)
	}
	// No audio bytes and no number in the clear in the run's history.
	var history string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT string_agg(payload::text, ' ') FROM run_events WHERE run_id = $1`, ref.ID).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(history, "OpusHead") || strings.Contains(history, "OggS") {
		t.Fatal("audio reached the run's history")
	}
}

// metaFake is a fake Graph API for one inbound voice note (media-7) and
// the uploads and sends that answer it.
type metaFake struct {
	srv      *httptest.Server
	mu       sync.Mutex
	uploaded []byte
	sent     []map[string]any
}

func newMetaFake(t *testing.T, heard []byte) *metaFake {
	f := &metaFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer EAAtest" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/media-7" && r.URL.Query().Get("phone_number_id") == "106540352242922":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": f.srv.URL + "/download/media-7", "mime_type": "audio/ogg; codecs=opus", "file_size": len(heard), "id": "media-7"})
		case r.Method == "GET" && r.URL.Path == "/download/media-7":
			_, _ = w.Write(heard)
		case r.Method == "POST" && r.URL.Path == "/106540352242922/media":
			file, _, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.uploaded, _ = io.ReadAll(file)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "uploaded-1"})
		case r.Method == "POST" && r.URL.Path == "/106540352242922/messages":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.sent = append(f.sent, b)
			_ = json.NewEncoder(w).Encode(map[string]any{"messaging_product": "whatsapp", "contacts": []any{map[string]any{"input": b["to"], "wa_id": b["to"]}}, "messages": []any{map[string]any{"id": "wamid.reply"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// registerWhatsApp registers whatsapp@1 against the fake, with a
// connection.
func registerWhatsApp(t *testing.T, e *rt.Env, f *metaFake) {
	t.Helper()
	wa := whatsapp.New(whatsapp.Options{BaseURL: f.srv.URL})
	wa.Manifest.Egress = append(wa.Manifest.Egress, "127.0.0.1")
	if err := e.Registry.Register(wa); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "whatsapp", "main", "api_key",
		map[string]string{"access_token": "EAAtest", "phone_number_id": "106540352242922", "app_secret": "app-secret-1", "verify_token": "vt-1"}, "admin"); err != nil {
		t.Fatal(err)
	}
}
