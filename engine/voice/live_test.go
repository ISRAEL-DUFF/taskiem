package voice

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/media"
)

// TestLiveSpeechRoundTrip checks the real provider (plan V3, T4): it
// speaks a sentence as Ogg/Opus, which must really be Opus (plan F7), and
// hears it back from that Ogg/Opus, the format WhatsApp and Telegram send
// voice notes in. It runs nightly (.github/workflows/speech-live.yml) with
// TASKIEM_SPEECH_LIVE_KEY; without it, it is skipped.
func TestLiveSpeechRoundTrip(t *testing.T) {
	key := os.Getenv("TASKIEM_SPEECH_LIVE_KEY")
	if key == "" {
		t.Skip("TASKIEM_SPEECH_LIVE_KEY is not set: the live speech check needs a provider account (people item VP1)")
	}
	base := os.Getenv("TASKIEM_SPEECH_LIVE_URL")
	hc := &http.Client{Timeout: 90 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	say := &OpenAISpeech{BaseURL: base, APIKey: key, Model: os.Getenv("TASKIEM_SPEECH_LIVE_MODEL"), HTTP: hc}
	sp, err := say.Speak(ctx, SpeechRequest{Text: "Good morning. Yes, we have rice and beans today.", Voice: DefaultVoices[0], Format: FormatOgg})
	if err != nil {
		t.Fatalf("speak: %v", err)
	}
	if !media.IsOpus(sp.Data) {
		t.Fatalf("the provider's opus answer is not Ogg/Opus (%d bytes, starts %q): a transcoder is needed (plan F7)", len(sp.Data), sp.Data[:min(8, len(sp.Data))])
	}
	d, ok := OggDuration(sp.Data)
	t.Logf("spoke %d bytes, %v of Ogg/Opus", len(sp.Data), d)
	if !ok || d < time.Second || d > 30*time.Second {
		t.Fatalf("duration %v (%v)", d, ok)
	}
	hear := &OpenAI{BaseURL: base, APIKey: key, Model: os.Getenv("TASKIEM_TRANSCRIBE_LIVE_MODEL"), HTTP: hc}
	tr, err := hear.Transcribe(ctx, Audio{Data: sp.Data, MimeType: "audio/ogg; codecs=opus", Language: "en"})
	if err != nil {
		t.Fatalf("hearing Ogg/Opus: %v", err)
	}
	t.Logf("heard %q (%s)", tr.Text, tr.Language)
	if l := strings.ToLower(tr.Text); !strings.Contains(l, "rice") || !strings.Contains(l, "beans") {
		t.Fatalf("heard %q", tr.Text)
	}
}
