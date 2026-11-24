package speech_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/speech"
	"github.com/israel-duff/taskiem/engine/voice"
)

func setup(t *testing.T, allowance int64) (*speech.Service, *voice.Fake, *voice.FakeSpeaker, uuid.UUID, *dbtest.DB) {
	t.Helper()
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	hear, say := &voice.Fake{Text: "Do you have rice?"}, &voice.FakeSpeaker{}
	s := &speech.Service{Pool: d.App, Hear: hear, Say: say,
		Allowance: func(context.Context, uuid.UUID) (int64, error) { return allowance, nil }}
	return s, hear, say, tn.ID, d
}

func turnOn(t *testing.T, d *dbtest.DB, tenant uuid.UUID, on bool) {
	t.Helper()
	if _, err := d.Admin.Exec(context.Background(), `INSERT INTO tenant_channel_settings (tenant_id, languages, default_language, voice_notes, workflow_voice, updated_by)
		VALUES ($1, '{}', 'en', false, $2, 'test') ON CONFLICT (tenant_id) DO UPDATE SET workflow_voice = EXCLUDED.workflow_voice`, tenant, on); err != nil {
		t.Fatal(err)
	}
}

func usage(t *testing.T, d *dbtest.DB, tenant uuid.UUID) (heard, spoken, chars, requests, refused int64) {
	t.Helper()
	err := d.Admin.QueryRow(context.Background(), `SELECT COALESCE(sum(seconds_heard),0), COALESCE(sum(seconds_spoken),0), COALESCE(sum(characters_spoken),0),
		COALESCE(sum(requests),0), COALESCE(sum(refused),0) FROM speech_usage WHERE tenant_id = $1`, tenant).Scan(&heard, &spoken, &chars, &requests, &refused)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// Off until the owner turns Voice in workflows on; then a voice note is
// heard with the language hint and its seconds are counted.
func TestHearOnlyWhenTurnedOn(t *testing.T) {
	s, hear, _, tenant, d := setup(t, 0)
	ctx := context.Background()
	note := voice.TestOgg(12, 32)
	if _, err := s.Transcribe(ctx, tenant, note, "audio/ogg", "yo"); !errors.Is(err, speech.ErrOff) {
		t.Fatalf("off: %v", err)
	}
	turnOn(t, d, tenant, true)
	h, err := s.Transcribe(ctx, tenant, note, "audio/ogg; codecs=opus", "yo")
	if err != nil || !h.OK || h.Text != "Do you have rice?" || h.Seconds != 12 {
		t.Fatalf("%+v %v", h, err)
	}
	if c := hear.Calls(); len(c) != 1 || c[0].Language != "yo" {
		t.Fatalf("calls %v", c)
	}
	// Pidgin has no hint: the service detects it.
	if _, err := s.Transcribe(ctx, tenant, note, "audio/ogg", "pcm"); err != nil || hear.Calls()[1].Language != "" {
		t.Fatalf("pidgin hint: %v %q", err, hear.Calls()[1].Language)
	}
	if heard, _, _, req, refused := usage(t, d, tenant); heard != 24 || req != 2 || refused != 1 {
		t.Fatalf("counted %d seconds, %d requests, %d refused", heard, req, refused)
	}
}

// A note that cannot be heard is not a failure: the fixed answer, in the
// customer's language, is given to send.
func TestCannotHearAnswers(t *testing.T) {
	s, hear, _, tenant, d := setup(t, 0)
	turnOn(t, d, tenant, true)
	ctx := context.Background()
	for name, c := range map[string]struct {
		data   []byte
		mime   string
		lang   string
		reason string
	}{
		"empty file":   {nil, "audio/ogg", "en", speech.ReasonEmpty},
		"too long":     {voice.TestOgg(400, 32), "audio/ogg", "pcm", speech.ReasonTooLong},
		"not audio":    {[]byte("%PDF-1.4"), "application/pdf", "ha", speech.ReasonUnreadable},
		"broken ogg":   {[]byte("not an ogg at all"), "audio/ogg", "ig", speech.ReasonUnreadable},
		"AMR untaken":  {append([]byte("#!AMR\n"), make([]byte, 64)...), "audio/amr", "yo", speech.ReasonUnreadable},
		"nothing said": {voice.TestOgg(3, 8), "audio/ogg", "yo", speech.ReasonEmpty},
	} {
		if name == "nothing said" {
			hear.Respond = func(voice.Audio) (voice.Transcript, error) { return voice.Transcript{}, voice.ErrEmpty }
		}
		h, err := s.Transcribe(ctx, tenant, c.data, c.mime, c.lang)
		hear.Respond = nil
		if err != nil || h.OK || h.Reason != c.reason || h.Reply == "" || h.Reply != s.CannotHear(c.reason, c.lang) {
			t.Errorf("%s: %+v %v", name, h, err)
		}
	}
	if !strings.Contains(s.CannotHear(speech.ReasonTooLong, "pcm"), "Abeg") || !strings.Contains(s.CannotHear("nonsense", "xx"), "Sorry") {
		t.Fatal("answers not in the language asked")
	}
}

// The service failing: a refusal is fatal, anything else may be retried.
func TestServiceFailures(t *testing.T) {
	s, hear, say, tenant, d := setup(t, 0)
	turnOn(t, d, tenant, true)
	ctx := context.Background()
	hear.Respond = func(voice.Audio) (voice.Transcript, error) {
		return voice.Transcript{}, &voice.ServiceError{Service: "transcription", Status: 503}
	}
	if _, err := s.Transcribe(ctx, tenant, voice.TestOgg(2, 8), "audio/ogg", ""); !errors.Is(err, speech.ErrService) {
		t.Fatalf("503: %v", err)
	}
	say.Respond = func(voice.SpeechRequest) (voice.Speech, error) {
		return voice.Speech{}, &voice.ServiceError{Service: "speech", Status: 401}
	}
	if _, err := s.Speak(ctx, tenant, "Yes", "", "", ""); !errors.Is(err, speech.ErrRefused) {
		t.Fatalf("401: %v", err)
	}
	// A voice note that is not Opus is refused, not sent.
	say.Respond = func(voice.SpeechRequest) (voice.Speech, error) {
		return voice.Speech{Data: []byte("ID3 not ogg"), MimeType: "audio/ogg"}, nil
	}
	if _, err := s.Speak(ctx, tenant, "Yes", "", "ogg", ""); !errors.Is(err, speech.ErrRefused) {
		t.Fatalf("not opus: %v", err)
	}
	// No service on this Taskiem.
	if _, err := (&speech.Service{Pool: d.App}).Speak(ctx, tenant, "Yes", "", "", ""); !errors.Is(err, speech.ErrUnavailable) {
		t.Fatalf("no service: %v", err)
	}
}

// A reply is spoken as Ogg/Opus in an offered voice, and its real length
// and characters are counted.
func TestSpeak(t *testing.T) {
	s, _, say, tenant, d := setup(t, 0)
	turnOn(t, d, tenant, true)
	ctx := context.Background()
	text := strings.Repeat("We have rice. ", 5) // 70 characters
	sp, err := s.Speak(ctx, tenant, text, "", "", "warm")
	if err != nil || sp.Type != "audio/ogg" || !media.IsOpus(sp.Data) || sp.Characters != 69 || sp.Seconds != 4 {
		t.Fatalf("%+v %v", sp, err)
	}
	if c := say.Calls(); c[0].Voice != voice.DefaultVoices[0] || c[0].Format != voice.FormatOgg || c[0].Instructions != "warm" {
		t.Fatalf("asked %+v", c[0])
	}
	if _, err := s.Speak(ctx, tenant, "Yes", "onyx", "", ""); !errors.Is(err, speech.ErrRefused) || !strings.Contains(err.Error(), "coral") {
		t.Fatalf("an unoffered voice: %v", err)
	}
	if _, err := s.Speak(ctx, tenant, strings.Repeat("a", voice.MaxSpeechChars+1), "", "", ""); !errors.Is(err, speech.ErrRefused) {
		t.Fatalf("too long: %v", err)
	}
	if mp3, err := s.Speak(ctx, tenant, "Yes", "Sage", "mp3", ""); err != nil || mp3.Type != "audio/mpeg" {
		t.Fatalf("mp3: %+v %v", mp3, err)
	}
	if _, spoken, chars, _, _ := usage(t, d, tenant); spoken != 5 || chars != 72 {
		t.Fatalf("counted %d seconds, %d characters", spoken, chars)
	}
}

// The month's allowance covers seconds heard and spoken together; past
// it both are refused, and the refusal is recorded.
func TestAllowance(t *testing.T) {
	s, _, _, tenant, d := setup(t, 20)
	var hits []string
	s.LimitHit = func(_ context.Context, _ uuid.UUID, key string) { hits = append(hits, key) }
	turnOn(t, d, tenant, true)
	ctx := context.Background()
	if _, err := s.Transcribe(ctx, tenant, voice.TestOgg(15, 8), "audio/ogg", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transcribe(ctx, tenant, voice.TestOgg(10, 8), "audio/ogg", ""); !errors.Is(err, speech.ErrOverAllowance) {
		t.Fatalf("heard past the allowance: %v", err)
	}
	if _, err := s.Speak(ctx, tenant, strings.Repeat("a", 150), "", "", ""); !errors.Is(err, speech.ErrOverAllowance) {
		t.Fatalf("spoke past the allowance: %v", err)
	}
	if _, err := s.Speak(ctx, tenant, "Yes, we do.", "", "", ""); err != nil {
		t.Fatalf("a short reply still fits: %v", err)
	}
	if len(hits) != 2 || hits[0] != "speech_seconds_monthly" {
		t.Fatalf("limit hits %v", hits)
	}
	if _, _, _, _, refused := usage(t, d, tenant); refused != 2 {
		t.Fatalf("refused %d", refused)
	}
}

// Calls at the same moment cannot both take the last seconds, and a call
// the service failed gives its seconds back.
func TestAllowanceHoldsUnderConcurrency(t *testing.T) {
	s, _, _, tenant, d := setup(t, 10)
	turnOn(t, d, tenant, true)
	ctx := context.Background()
	note := voice.TestOgg(3, 8)
	var wg sync.WaitGroup
	var ok atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h, err := s.Transcribe(ctx, tenant, note, "audio/ogg", ""); err == nil && h.OK {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if heard, _, _, _, _ := usage(t, d, tenant); ok.Load() != 3 || heard != 9 {
		t.Fatalf("%d heard, %d seconds counted: want 3 and 9 of a 10-second allowance", ok.Load(), heard)
	}

	s2, hear2, _, tenant2, d2 := setup(t, 0)
	turnOn(t, d2, tenant2, true)
	hear2.Respond = func(voice.Audio) (voice.Transcript, error) {
		return voice.Transcript{}, &voice.ServiceError{Service: "transcription", Status: 503}
	}
	if _, err := s2.Transcribe(ctx, tenant2, note, "audio/ogg", ""); !errors.Is(err, speech.ErrService) {
		t.Fatal(err)
	}
	if heard, _, _, req, _ := usage(t, d2, tenant2); heard != 0 || req != 0 {
		t.Fatalf("a failed call kept %d seconds, %d requests", heard, req)
	}
}
