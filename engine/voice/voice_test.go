package voice_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/voice"
)

func TestOggDuration(t *testing.T) {
	for _, secs := range []int{1, 12, 59, 61, 300} {
		d, ok := voice.OggDuration(voice.TestOgg(secs, 0))
		if !ok || d != time.Duration(secs)*time.Second {
			t.Errorf("%ds: %v %v", secs, d, ok)
		}
	}
	for _, bad := range [][]byte{nil, []byte("not ogg at all, but long enough to look"), []byte("OggS")} {
		if _, ok := voice.OggDuration(bad); ok {
			t.Errorf("%q read as Ogg", bad)
		}
	}
}

func TestCheckLimits(t *testing.T) {
	l := voice.Limits{MaxBytes: 4096, MaxDuration: 30 * time.Second}
	if d, err := voice.Check(voice.Audio{Data: voice.TestOgg(20, 0), MimeType: "audio/ogg; codecs=opus"}, l); err != nil || d != 20*time.Second {
		t.Fatalf("20s: %v %v", d, err)
	}
	if _, err := voice.Check(voice.Audio{Data: voice.TestOgg(31, 0), MimeType: "audio/ogg; codecs=opus"}, l); !errors.Is(err, voice.ErrTooLong) {
		t.Errorf("31s: %v", err)
	}
	if _, err := voice.Check(voice.Audio{Data: voice.TestOgg(5, 5000), MimeType: "audio/ogg"}, l); !errors.Is(err, voice.ErrTooLarge) {
		t.Errorf("large: %v", err)
	}
	if _, err := voice.Check(voice.Audio{Data: []byte("x"), MimeType: "image/jpeg"}, l); !errors.Is(err, voice.ErrUnsupported) {
		t.Errorf("image: %v", err)
	}
	if _, err := voice.Check(voice.Audio{Data: []byte("garbage, not an ogg stream at all...."), MimeType: "audio/ogg"}, l); !errors.Is(err, voice.ErrUnsupported) {
		t.Errorf("garbage ogg: %v", err)
	}
	if _, err := voice.Check(voice.Audio{Data: []byte("ID3 mp3 bytes"), MimeType: "audio/mpeg"}, l); err != nil {
		t.Errorf("mp3 within size: %v", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	c, err := voice.ConfigFromEnv(env())
	if err != nil || c.Provider != "" {
		t.Fatalf("default is off: %+v %v", c, err)
	}
	if _, err := voice.ConfigFromEnv(env("TASKIEM_TRANSCRIBE_PROVIDER", "openai")); err == nil {
		t.Error("openai without a key accepted")
	}
	c, err = voice.ConfigFromEnv(env("TASKIEM_TRANSCRIBE_PROVIDER", "openai", "TASKIEM_TRANSCRIBE_API_KEY", "k", "TASKIEM_TRANSCRIBE_MAX_SECONDS", "45"))
	if err != nil || c.Model != voice.DefaultOpenAIModel || c.Limits.MaxDuration != 45*time.Second || c.Limits.MaxBytes != voice.DefaultMaxBytes || c.Retention != voice.DefaultRetention {
		t.Fatalf("openai: %+v %v", c, err)
	}
	for _, bad := range [][]string{{"TASKIEM_TRANSCRIBE_PROVIDER", "whisperer"}, {"TASKIEM_TRANSCRIBE_PROVIDER", "fake", "TASKIEM_TRANSCRIBE_MAX_SECONDS", "900"},
		{"TASKIEM_TRANSCRIBE_PROVIDER", "fake", "TASKIEM_TRANSCRIBE_MAX_BYTES", "999999999"}, {"TASKIEM_TRANSCRIBE_PROVIDER", "fake", "TASKIEM_TRANSCRIBE_RETENTION", "5m"}} {
		if _, err := voice.ConfigFromEnv(env(bad...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestOpenAITranscribe(t *testing.T) {
	var got struct {
		auth, model, lang, format, filename, ctype string
		size                                       int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		got.auth, got.model, got.lang, got.format = r.Header.Get("Authorization"), r.FormValue("model"), r.FormValue("language"), r.FormValue("response_format")
		f, fh, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(f)
		got.filename, got.ctype, got.size = fh.Filename, fh.Header.Get("Content-Type"), len(b)
		if got.lang == "ha" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"secret details of the request","type":"rate_limit","code":"x"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"text":"  abeg run payroll  ","languages":[{"code":"en"}]}`)
	}))
	defer srv.Close()
	o := &voice.OpenAI{BaseURL: srv.URL, APIKey: "sk-test", HTTP: srv.Client()}
	data := voice.TestOgg(3, 0)
	tr, err := o.Transcribe(context.Background(), voice.Audio{Data: data, MimeType: "audio/ogg; codecs=opus", Language: "yo"})
	if err != nil || tr.Text != "abeg run payroll" || tr.Language != "en" {
		t.Fatalf("%+v %v", tr, err)
	}
	if got.auth != "Bearer sk-test" || got.model != "whisper-1" || got.lang != "yo" || got.format != "json" || got.filename != "voice.ogg" ||
		got.ctype != "audio/ogg" || got.size != len(data) {
		t.Fatalf("request: %+v", got)
	}
	// Errors keep the kind, never the provider's message.
	_, err = o.Transcribe(context.Background(), voice.Audio{Data: data, MimeType: "audio/ogg", Language: "ha"})
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "rate_limit") {
		t.Fatalf("error: %v", err)
	}
	if _, err := o.Transcribe(context.Background(), voice.Audio{Data: data, MimeType: "video/mp4"}); !errors.Is(err, voice.ErrUnsupported) {
		t.Fatalf("video: %v", err)
	}
}
