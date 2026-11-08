package runtime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	speechconn "github.com/israel-duff/taskiem/connectors/speech"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/media/mediatest"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/speech"
	"github.com/israel-duff/taskiem/engine/voice"
)

// A customer's WhatsApp voice note is heard by speech@1, answered, spoken
// and sent back as a voice note (decision 0062, plan V3): the words heard
// are sealed in the run's history, the seconds counted, and only file
// references travel between steps.
func TestWhatsAppVoiceReplyThroughSpeech(t *testing.T) {
	e := rt.New(t)
	e.RunFiles = &runfiles.Service{Pool: e.DB.App, Store: runfilestest.New(),
		Subject: func(_ context.Context, _ uuid.UUID, category, value string) (string, error) {
			return category + ":" + value, nil
		}}
	meta := newMetaFake(t, mediatest.OggOpus(4*time.Second))
	registerWhatsApp(t, e, meta)
	hear, say := &voice.Fake{Text: "Abeg, una get rice?"}, &voice.FakeSpeaker{}
	svc := &speech.Service{Pool: e.DB.App, Hear: hear, Say: say}
	if err := e.Registry.Register(speechconn.New(svc)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Admin.Exec(context.Background(), `INSERT INTO tenant_channel_settings (tenant_id, languages, default_language, voice_notes, workflow_voice, updated_by)
		VALUES ($1, '{}', 'en', false, true, 'test')`, e.Tenant); err != nil {
		t.Fatal(err)
	}

	wf := e.Publish(t, wfDoc(`{"id":"fetch","type":"connector","connector":"whatsapp@1","action":"get_media","input":{"media_id":"=trigger.media_id","sender":"=trigger.from"}},
		{"id":"hear","type":"connector","connector":"speech@1","action":"transcribe","needs":["fetch"],"input":{"file":"=steps.fetch.output.file","language":"pcm"}},
		{"id":"say","type":"connector","connector":"speech@1","action":"speak","needs":["hear"],"input":{"customer":"=trigger.from","text":"=steps.hear.output.heard ? 'Yes o, we get rice. You talk say: ' + steps.hear.output.text : steps.hear.output.reply"}},
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
	if c := hear.Calls(); len(c) != 1 || c[0].Language != "" || c[0].MimeType != "audio/ogg" {
		t.Fatalf("heard %+v", c)
	}
	if c := say.Calls(); len(c) != 1 || c[0].Text != "Yes o, we get rice. You talk say: Abeg, una get rice?" || c[0].Format != voice.FormatOgg {
		t.Fatalf("asked to say %+v", c)
	}
	meta.mu.Lock()
	uploaded, sent := meta.uploaded, meta.sent
	meta.mu.Unlock()
	if !media.IsOpus(uploaded) || len(sent) != 1 || sent[0]["audio"].(map[string]any)["voice"] != true {
		t.Fatalf("sent %v (Opus uploaded: %v)", sent, media.IsOpus(uploaded))
	}
	var history string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT string_agg(payload::text, ' ') FROM run_events WHERE run_id = $1`, ref.ID).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(history, "una get rice") {
		t.Fatal("what the customer said is in the run's history in the clear")
	}
	var heard, spoken int64
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT seconds_heard, seconds_spoken FROM speech_usage WHERE tenant_id = $1`, e.Tenant).Scan(&heard, &spoken); err != nil {
		t.Fatal(err)
	}
	if heard != 4 || spoken < 1 {
		t.Fatalf("counted %d heard, %d spoken", heard, spoken)
	}
	// The voice note and the reply both go when the customer is erased.
	var subjects string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT string_agg(DISTINCT coalesce(subject, '-'), ',') || '/' || count(*) FROM run_files WHERE tenant_id = $1`, e.Tenant).Scan(&subjects); err != nil {
		t.Fatal(err)
	}
	if subjects != "phone:2348031234567/2" {
		t.Fatalf("files tied to %s", subjects)
	}
}

// With Voice in workflows off, hearing fails the step at once, plainly,
// without a retry or a call to the service.
func TestSpeechOffFailsPlainly(t *testing.T) {
	e := rt.New(t)
	e.RunFiles = &runfiles.Service{Pool: e.DB.App, Store: runfilestest.New()}
	hear := &voice.Fake{Text: "x"}
	if err := e.Registry.Register(speechconn.New(&speech.Service{Pool: e.DB.App, Hear: hear})); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"hear","type":"connector","connector":"speech@1","action":"transcribe","input":{"file":"=trigger.file"}},
		{"id":"sorry","type":"connector","connector":"speech@1","action":"cannot_hear","input":{"reason":"unavailable","language":"pcm"}}`, ""))
	note, err := e.RunFiles.Put(context.Background(), runfiles.Owner{Tenant: e.Tenant, Environment: "prod"}, "reply.ogg", voice.TestOgg(2, 8), false)
	if err != nil {
		t.Fatal(err)
	}
	ref := e.Start(t, wf, map[string]any{"file": note.ID.String()})
	rt.WaitFor(t, 20*time.Second, "run to end", func() bool {
		e.Drain(t)
		st := e.Status(t, ref)
		return st == "completed" || st == "failed"
	})
	if st := e.Status(t, ref); st != "failed" || len(hear.Calls()) != 0 {
		t.Fatalf("status %s, %d calls", st, len(hear.Calls()))
	}
	var failed string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE run_id = $1 AND step_id = 'hear' AND type = 'StepFailed'`, ref.ID).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(failed, "Voice in workflows is off") {
		t.Fatalf("failed with %s", failed)
	}
}
