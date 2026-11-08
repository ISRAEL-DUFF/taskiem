package voice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/media"
)

func TestOpenAISpeechAsksForOpus(t *testing.T) {
	var got map[string]any
	ogg := TestOgg(2, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write(ogg)
	}))
	defer srv.Close()
	o := &OpenAISpeech{BaseURL: srv.URL, APIKey: "sk-test", HTTP: srv.Client()}
	sp, err := o.Speak(context.Background(), SpeechRequest{Text: "We have rice.", Voice: "coral", Format: FormatOgg, Instructions: "warm"})
	if err != nil {
		t.Fatal(err)
	}
	if got["response_format"] != "opus" || got["model"] != DefaultSpeechModel || got["voice"] != "coral" || got["input"] != "We have rice." || got["instructions"] != "warm" {
		t.Fatalf("asked %v", got)
	}
	if sp.MimeType != "audio/ogg" || !media.IsOpus(sp.Data) {
		t.Fatalf("answered %s, Opus %v", sp.MimeType, media.IsOpus(sp.Data))
	}
	if _, err := o.Speak(context.Background(), SpeechRequest{Text: "x", Voice: "coral", Format: FormatMP3}); err != nil || got["response_format"] != "mp3" {
		t.Fatalf("mp3: %v %v", err, got)
	}
}

func TestOpenAISpeechRefusals(t *testing.T) {
	status := 400
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"the text 'secret words' is bad","type":"invalid_request_error","code":null}}`))
	}))
	defer srv.Close()
	o := &OpenAISpeech{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	for _, s := range []int{400, 429, 500} {
		status = s
		_, err := o.Speak(context.Background(), SpeechRequest{Text: "secret words", Voice: "coral", Format: FormatOgg})
		var se *ServiceError
		if !errors.As(err, &se) || se.Permanent() != (s == 400) {
			t.Fatalf("%d: %v", s, err)
		}
		if strings.Contains(err.Error(), "secret words") {
			t.Fatalf("the provider's message quoted the text: %v", err)
		}
	}
}

func TestCheckSpeech(t *testing.T) {
	ok := SpeechRequest{Text: "Hello", Voice: "coral", Format: FormatOgg}
	if err := CheckSpeech(ok, DefaultVoices); err != nil {
		t.Fatal(err)
	}
	for want, r := range map[error]SpeechRequest{
		ErrNoText:       {Text: "  ", Voice: "coral", Format: FormatOgg},
		ErrTextTooLong:  {Text: strings.Repeat("é", MaxSpeechChars+1), Voice: "coral", Format: FormatOgg},
		ErrBadFormat:    {Text: "x", Voice: "coral", Format: "wav"},
		ErrUnknownVoice: {Text: "x", Voice: "onyx", Format: FormatOgg},
	} {
		if err := CheckSpeech(r, DefaultVoices); !errors.Is(err, want) {
			t.Errorf("want %v, got %v", want, err)
		}
	}
}

func TestSpeechConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	c, err := SpeechConfigFromEnv(env(nil))
	if err != nil || c.Provider != "" {
		t.Fatalf("off by default: %v %v", c, err)
	}
	if _, err := SpeechConfigFromEnv(env(map[string]string{"TASKIEM_SPEECH_PROVIDER": "openai"})); err == nil {
		t.Fatal("openai without a key")
	}
	c, err = SpeechConfigFromEnv(env(map[string]string{"TASKIEM_SPEECH_PROVIDER": "openai", "TASKIEM_SPEECH_API_KEY": "k", "TASKIEM_SPEECH_VOICES": "Sage, coral,sage"}))
	if err != nil || c.Model != DefaultSpeechModel || strings.Join(c.Voices, ",") != "sage,coral" {
		t.Fatalf("%v %v", c, err)
	}
	if _, err := SpeechConfigFromEnv(env(map[string]string{"TASKIEM_SPEECH_PROVIDER": "openai", "TASKIEM_SPEECH_API_KEY": "k", "TASKIEM_SPEECH_VOICES": "a b"})); err == nil {
		t.Fatal("a voice name with a space")
	}
	if _, err := SpeechConfigFromEnv(env(map[string]string{"TASKIEM_SPEECH_PROVIDER": "acme"})); err == nil {
		t.Fatal("unknown provider")
	}
}

func TestFakeSpeakerAnswersOpus(t *testing.T) {
	f := &FakeSpeaker{}
	sp, err := f.Speak(context.Background(), SpeechRequest{Text: strings.Repeat("a", 45), Voice: "coral", Format: FormatOgg})
	if err != nil || !media.IsOpus(sp.Data) {
		t.Fatalf("%v, Opus %v", err, media.IsOpus(sp.Data))
	}
	if d, ok := OggDuration(sp.Data); !ok || d.Seconds() < 2.9 || d.Seconds() > 3.1 {
		t.Fatalf("duration %v", d)
	}
}
