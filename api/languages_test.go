package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/voice"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

func yo(id string, kv ...string) string { return lang.Default().Text(lang.YO, id, kv...) }

func TestWhatsAppLanguages(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	w.bind(t, owner, "+2348010000001")

	// Off by default: Yoruba is not understood, and replies are English.
	if got := w.say(t, "+2348010000001", "ìrànlọ́wọ́"); !strings.HasPrefix(got, "[Acme] Sorry, I did not understand that.") {
		t.Fatalf("before: %q", got)
	}
	got := owner.must(200, "GET", "/v1/languages", nil)
	if toJSON(got["on"]) != `["en"]` || got["voice_notes"] != false || got["voice_available"] != false {
		t.Fatalf("settings: %v", got)
	}
	owner.must(400, "PUT", "/v1/languages", map[string]any{"languages": []string{"fr"}})
	owner.must(400, "PUT", "/v1/languages", map[string]any{"languages": []string{"yo"}, "default_language": "ha"})
	owner.must(409, "PUT", "/v1/languages", map[string]any{"voice_notes": true})
	got = owner.must(200, "PUT", "/v1/languages", map[string]any{"languages": []string{"yo", "pcm"}})
	if toJSON(got["on"]) != `["en","yo","pcm"]` {
		t.Fatalf("on: %v", got)
	}

	// Asking which, then choosing: replies switch, with the draft note.
	if got := w.say(t, "+2348010000001", "language"); !strings.Contains(got, "Yorùbá, Naija") {
		t.Fatalf("language: %q", got)
	}
	got2 := w.say(t, "+2348010000001", "language yoruba")
	if !strings.Contains(got2, yo("lang.switched", "language", "Yorùbá")) || !strings.Contains(got2, yo("lang.draft_note")) {
		t.Fatalf("switched: %q", got2)
	}
	if got := w.say(t, "+2348010000001", "help"); !strings.Contains(got, yo("wa.help.intro")) || !strings.Contains(got, "*language*") {
		t.Fatalf("help in Yoruba: %q", got)
	}
	// Yoruba words work, and English ones still do.
	if got := w.say(t, "+2348010000001", "Fagilé"); got != "[Acme] "+yo("wa.nothing_to_cancel") {
		t.Fatalf("cancel: %q", got)
	}
	if got := w.say(t, "+2348010000001", "status"); !strings.Contains(got, yo("wa.status.none")) {
		t.Fatalf("status: %q", got)
	}
	// A chosen language is not replaced by detection.
	if got := w.say(t, "+2348010000001", "abeg wetin dey happen today"); !strings.HasPrefix(got, "[Acme] "+yo("wa.not_understood")) {
		t.Fatalf("chosen kept: %q", got)
	}

	// Another person: Pidgin detected from the first message.
	bob := w.member(t, owner, "bob@acme.test", "viewer")
	w.bind(t, bob, "+2348010000002")
	pcm := func(id string, kv ...string) string { return lang.Default().Text(lang.PCM, id, kv...) }
	got3 := w.say(t, "+2348010000002", "abeg wetin fail today")
	if !strings.Contains(got3, pcm("lang.detected", "language", "Naija")) || !strings.Contains(got3, pcm("wa.status.none")) {
		t.Fatalf("detected: %q", got3)
	}
	if got := w.say(t, "+2348010000002", "language english"); got != "[Acme] From now on I will write to you in English." {
		t.Fatalf("back to English: %q", got)
	}
	// Chosen English sticks.
	if got := w.say(t, "+2348010000002", "abeg wetin fail today"); !strings.HasPrefix(got, "[Acme] Sorry, I did not understand that.") {
		t.Fatalf("english kept: %q", got)
	}
	// A language that is not on cannot be chosen.
	if got := w.say(t, "+2348010000002", "language hausa"); !strings.Contains(got, "Hausa is not available here") {
		t.Fatalf("hausa: %q", got)
	}

	// The tenant turns its languages off: English again, whatever was chosen.
	owner.must(200, "PUT", "/v1/languages", map[string]any{"languages": []string{}})
	if got := w.say(t, "+2348010000001", "help"); !strings.Contains(got, "You can send:") {
		t.Fatalf("off again: %q", got)
	}
	// Changes are audited.
	if n := countRows(t, w.world, `SELECT count(*) FROM audit_log WHERE action = 'channel.languages.update'`); n != 2 {
		t.Fatalf("audited %d", n)
	}
}

func TestWhatsAppModelRoutesOtherLanguages(t *testing.T) {
	w := newWAWorld(t)
	answer := `{"intent":"status","argument":""}`
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) { return &ai.Response{Text: answer}, nil }}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO}, Model: fake}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	w.bind(t, owner, "+2348010000003")

	// English never goes to the model.
	w.say(t, "+2348010000003", "what is going on with my stuff")
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("English sent to the model: %d", n)
	}
	w.say(t, "+2348010000003", "language yo")
	got := w.say(t, "+2348010000003", "Ṣé nǹkan kan bàjẹ́? Pe mi lórí 08031234567")
	if !strings.Contains(got, yo("wa.status.none")) {
		t.Fatalf("routed: %q", got)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 || !strings.Contains(ai.PromptText(reqs[0]), "Yoruba (yo)") || strings.Contains(ai.PromptText(reqs[0]), "08031234567") {
		t.Fatalf("request: %d %v", len(reqs), reqs)
	}
	if n := countRows(t, w.world, `SELECT count(*) FROM ai_interactions WHERE kind = 'intent' AND outcome = 'valid' AND build_id IS NULL AND messages::text NOT LIKE '%08031234567%'`); n != 1 {
		t.Fatalf("recorded %d", n)
	}
	// The model cannot confirm anything.
	answer = `{"intent":"yes","argument":""}`
	if got := w.say(t, "+2348010000003", "Mo gba"); !strings.HasPrefix(got, "[Acme] "+yo("wa.not_understood")) {
		t.Fatalf("yes from the model: %q", got)
	}
	// A provider failure leaves the message unrecognised.
	fake.Respond = func(ai.Request) (*ai.Response, error) { return nil, errors.New("down") }
	if got := w.say(t, "+2348010000003", "Mo fẹ́ mọ̀"); !strings.HasPrefix(got, "[Acme] "+yo("wa.not_understood")) {
		t.Fatalf("provider down: %q", got)
	}
}

func TestWhatsAppVoiceNotes(t *testing.T) {
	w := newWAWorld(t)
	tr := &voice.Fake{Text: "status"}
	w.srv.Voice = &api.VoiceSettings{Provider: tr, Limits: voice.Limits{MaxBytes: 64 << 10, MaxDuration: 30 * time.Second}, Retention: time.Hour}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const num = "+2348010000004"
	w.bind(t, owner, num)
	audio := func(id string) string {
		t.Helper()
		got := w.send(t, num, whatsapptest.Audio(id, "audio/ogg; codecs=opus", true))
		if len(got) != 1 {
			t.Fatalf("%s: %d replies: %+v", id, len(got), got)
		}
		return got[0].Text
	}
	w.graph.AddMedia("m-ok", "audio/ogg; codecs=opus", voice.TestOgg(5, 100), 0)

	// Off for the tenant: asked to type, nothing fetched or sent.
	if got := audio("m-ok"); got != "[Acme] Voice notes are not available here yet. Please type your message." {
		t.Fatalf("off: %q", got)
	}
	if len(tr.Calls()) != 0 || w.graph.Downloads() != 0 {
		t.Fatal("transcribed while off")
	}
	owner.must(200, "PUT", "/v1/languages", map[string]any{"voice_notes": true})

	// On: transcribed, said back as a beta, and read as typed.
	got := audio("m-ok")
	if !strings.Contains(got, `Voice notes are in beta, so please check this. I heard: "status"`) || !strings.Contains(got, "No runs in the last 24 hours.") {
		t.Fatalf("voice: %q", got)
	}
	if c := tr.Calls(); len(c) != 1 || c[0].MimeType != "audio/ogg; codecs=opus" || c[0].Language != "en" || len(c[0].Data) == 0 {
		t.Fatalf("calls: %+v", c)
	}
	// Sealed: the transcript is not in the table in the clear.
	if n := countRows(t, w.world, `SELECT count(*) FROM voice_transcripts WHERE status = 'transcribed' AND transcript ? '$pii' AND transcript::text NOT LIKE '%status%' AND duration_ms = 5000`); n != 1 {
		t.Fatalf("sealed rows: %d", n)
	}

	// Too long: refused before it leaves.
	w.graph.AddMedia("m-long", "audio/ogg", voice.TestOgg(31, 0), 0)
	if got := audio("m-long"); !strings.Contains(got, "longer than 30 seconds") {
		t.Fatalf("long: %q", got)
	}
	// Too large by Meta's own count: not even downloaded.
	downloads := w.graph.Downloads()
	w.graph.AddMedia("m-big", "audio/ogg", voice.TestOgg(5, 0), 5<<20)
	if got := audio("m-big"); !strings.Contains(got, "too large") || w.graph.Downloads() != downloads {
		t.Fatalf("big: %q (downloads %d -> %d)", got, downloads, w.graph.Downloads())
	}
	// Not audio Taskiem takes.
	w.graph.AddMedia("m-video", "video/mp4", []byte("x"), 0)
	if got := w.send(t, num, whatsapptest.Audio("m-video", "video/mp4", false)); len(got) != 1 || !strings.Contains(got[0].Text, "I can only listen to voice notes") {
		t.Fatalf("video: %+v", got)
	}
	// The provider fails: asked to type.
	tr.Respond = func(voice.Audio) (voice.Transcript, error) { return voice.Transcript{}, errors.New("provider down") }
	if got := audio("m-ok"); !strings.Contains(got, "I could not understand that voice note. Please type your message.") {
		t.Fatalf("failed: %q", got)
	}
	tr.Respond = nil
	if calls := len(tr.Calls()); calls != 2 {
		t.Fatalf("provider called %d times (only for the notes within limits)", calls)
	}

	// In the middle of something, a voice note cannot answer.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO chat_sessions (tenant_id, number, user_id, state, data, expires_at)
		SELECT m.tenant_id, $1, m.user_id, 'awaiting_confirmation', '{"name":"Payroll"}', now() + interval '10 minutes' FROM memberships m JOIN users u ON u.id = m.user_id WHERE u.email = 'owner@acme.test'`, num); err != nil {
		t.Fatal(err)
	}
	if got := audio("m-ok"); !strings.Contains(got, "Please type your answer") || len(tr.Calls()) != 2 {
		t.Fatalf("mid-confirmation: %q", got)
	}

	// Testers read them back (pii.reveal, audited).
	out := owner.must(200, "GET", "/v1/languages/transcripts", nil)
	list := out["transcripts"].([]any)
	if len(list) != 5 || !strings.Contains(toJSON(list), `"transcript":"status"`) || !strings.Contains(toJSON(list), `"reason":"too_long"`) {
		t.Fatalf("transcripts: %s", toJSON(list))
	}
	if n := countRows(t, w.world, `SELECT count(*) FROM audit_log WHERE action = 'voice.transcripts.read'`); n != 1 {
		t.Fatalf("read audited %d", n)
	}
	bob := w.member(t, owner, "bob@acme.test", "viewer")
	bob.must(403, "GET", "/v1/languages/transcripts", nil)

	// An unbound number's voice note is never transcribed.
	if got := w.send(t, "+2348019999999", whatsapptest.Audio("m-ok", "audio/ogg", true)); len(got) != 1 || !strings.Contains(got[0].Text, "not linked") || len(tr.Calls()) != 2 {
		t.Fatalf("unbound: %+v", got)
	}

	// Past retention, the notifier deletes them.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE voice_transcripts SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if n := countRows(t, w.world, `SELECT count(*) FROM voice_transcripts`); n != 0 {
		t.Fatalf("%d left after retention", n)
	}
}

func TestUSSDTextsInTheTenantLanguage(t *testing.T) {
	r := newUSSDRig(t, 1)
	r.edges[0].Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO}}
	// English until the tenant picks a default.
	if _, body := r.dial(t, 0, "s-en", ussdPhone, "*999#", ""); body != "END This service is not available." {
		t.Fatalf("english: %q", body)
	}
	r.owner.must(200, "PUT", "/v1/languages", map[string]any{"default_language": "yo"})
	_, body := r.dial(t, 0, "s-yo", ussdPhone, "*999#", "")
	if body != "END "+lang.ASCII(yo("ussd.gone")) || body != "END Ise yii ko si." {
		t.Fatalf("yoruba: %q", body)
	}
}

func countRows(t *testing.T, w *world, q string) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
