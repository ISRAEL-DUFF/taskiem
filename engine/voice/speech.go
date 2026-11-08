package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// Speech turns text into a voice reply (decision 0062 D5, plan V3): one
// small interface in front of a text-to-speech service, a fake for tests,
// and the OpenAI-compatible speech endpoint, built from OpenAI's public
// text-to-speech guide and API reference (developers.openai.com: "Text to
// speech" and "Create speech", read 8 November 2026):
//
//	POST {base}/v1/audio/speech   application/json
//	  model            e.g. gpt-4o-mini-tts
//	  input            the text, at most 4,096 characters
//	  voice            alloy, ash, ballad, coral, echo, fable, nova, onyx,
//	                   sage, shimmer, verse (and newer ones)
//	  response_format  mp3 (default), opus (Ogg/Opus), aac, flac, wav, pcm
//	  instructions     how to speak (gpt-4o-mini-tts only)
//	answers the audio itself.
//
// WhatsApp and Telegram play a voice note only in Ogg/Opus; the provider
// is asked for opus and the answer is checked to be Ogg/Opus by the caller
// (plan F7: no transcoder is needed while the provider emits Opus).

// Speaker speaks text. Implementations must not keep the text.
type Speaker interface {
	Name() string
	Speak(ctx context.Context, r SpeechRequest) (Speech, error)
}

// Formats a reply can be spoken in.
const (
	FormatOgg = "ogg" // Ogg/Opus: a voice note on WhatsApp and Telegram
	FormatMP3 = "mp3"
)

// SpeechRequest is what to say.
type SpeechRequest struct {
	Text   string
	Voice  string
	Format string // FormatOgg or FormatMP3
	// Instructions say how to speak ("warm, unhurried, Nigerian
	// English"); models that do not take them ignore them.
	Instructions string
}

// Speech is the spoken reply.
type Speech struct {
	Data     []byte
	MimeType string // audio/ogg or audio/mpeg
}

// MaxSpeechChars is the most text one request may speak (the provider's
// limit).
const MaxSpeechChars = 4096

// maxSpeechBytes caps the audio read back: 4,096 characters is about five
// minutes of speech, well under 16 MB in either format.
const maxSpeechBytes = 16 << 20

// Errors a speech caller tells apart.
var (
	ErrTextTooLong  = errors.New("voice: the text is too long to speak in one reply")
	ErrNoText       = errors.New("voice: there is nothing to say")
	ErrUnknownVoice = errors.New("voice: not one of the voices offered")
	ErrBadFormat    = errors.New("voice: the reply can be spoken as ogg or mp3")
)

// DefaultSpeechModel is OpenAI's current text-to-speech model.
const DefaultSpeechModel = "gpt-4o-mini-tts"

// DefaultVoices are offered when the operator names none; the first is the
// default. Chosen for clear speech on a phone; native speakers review them
// with the recordings of people item VP4.
var DefaultVoices = []string{"coral", "sage", "ash", "ballad"}

// CheckSpeech applies the limits every provider shares.
func CheckSpeech(r SpeechRequest, voices []string) error {
	if strings.TrimSpace(r.Text) == "" {
		return ErrNoText
	}
	if utf8.RuneCountInString(r.Text) > MaxSpeechChars {
		return ErrTextTooLong
	}
	if r.Format != FormatOgg && r.Format != FormatMP3 {
		return ErrBadFormat
	}
	if !slices.Contains(voices, r.Voice) {
		return ErrUnknownVoice
	}
	return nil
}

// OpenAISpeech speaks through an OpenAI-compatible endpoint.
type OpenAISpeech struct {
	BaseURL string // DefaultOpenAIURL when empty
	APIKey  string
	Model   string
	HTTP    HTTPClient // nil: http.DefaultClient (tests only)
}

func (o *OpenAISpeech) Name() string { return "openai" }

// Host is the endpoint's host name (the egress allow-list).
func (o *OpenAISpeech) Host() string {
	u, err := url.Parse(o.base())
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (o *OpenAISpeech) base() string {
	if o.BaseURL == "" {
		return DefaultOpenAIURL
	}
	return strings.TrimRight(o.BaseURL, "/")
}

// Speak asks for the reply in the format wanted and returns its audio.
func (o *OpenAISpeech) Speak(ctx context.Context, r SpeechRequest) (Speech, error) {
	model := o.Model
	if model == "" {
		model = DefaultSpeechModel
	}
	format, mime := "mp3", "audio/mpeg"
	if r.Format == FormatOgg {
		format, mime = "opus", "audio/ogg"
	}
	body := map[string]any{"model": model, "input": r.Text, "voice": r.Voice, "response_format": format}
	if r.Instructions != "" && strings.HasPrefix(model, "gpt-") {
		body["instructions"] = r.Instructions
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Speech{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base()+"/v1/audio/speech", bytes.NewReader(raw))
	if err != nil {
		return Speech{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	hc := o.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Speech{}, fmt.Errorf("voice: speaking: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSpeechBytes+1))
	if err != nil {
		return Speech{}, fmt.Errorf("voice: speaking: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Type string `json:"type"`
				Code any    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		// The provider's message may quote the text; only its kind is kept.
		return Speech{}, &ServiceError{Service: "speech", Status: resp.StatusCode, Kind: fmt.Sprintf("%s %v", e.Error.Type, e.Error.Code)}
	}
	if len(data) > maxSpeechBytes {
		return Speech{}, errors.New("voice: the speech service answered more audio than a reply can be")
	}
	if len(data) == 0 {
		return Speech{}, errors.New("voice: the speech service answered no audio")
	}
	return Speech{Data: data, MimeType: mime}, nil
}

// ServiceError is a speech service's refusal: a 4xx other than 408 and
// 429 means retrying the same request cannot help.
type ServiceError struct {
	Service string // "speech" or "transcription"
	Status  int
	Kind    string
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("voice: the %s service answered %d %s (%s)", e.Service, e.Status, http.StatusText(e.Status), strings.TrimSpace(e.Kind))
}

// Permanent reports whether retrying cannot help.
func (e *ServiceError) Permanent() bool {
	return e.Status/100 == 4 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

// FakeSpeaker is a speaker for tests: it answers Respond, or else a
// well-formed Ogg/Opus stream a second long per 15 characters (MP3: an
// ID3-tagged stand-in), and records what it was asked.
type FakeSpeaker struct {
	Respond func(r SpeechRequest) (Speech, error)

	mu    sync.Mutex
	calls []SpeechRequest
}

func (f *FakeSpeaker) Name() string { return "fake" }

func (f *FakeSpeaker) Speak(ctx context.Context, r SpeechRequest) (Speech, error) {
	if err := ctx.Err(); err != nil {
		return Speech{}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, r)
	f.mu.Unlock()
	if f.Respond != nil {
		return f.Respond(r)
	}
	if r.Format == FormatMP3 {
		return Speech{Data: append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), []byte(r.Text)...), MimeType: "audio/mpeg"}, nil
	}
	return Speech{Data: TestOgg(max(1, utf8.RuneCountInString(r.Text)/15), 64), MimeType: "audio/ogg"}, nil
}

// Calls returns what the fake was asked.
func (f *FakeSpeaker) Calls() []SpeechRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SpeechRequest(nil), f.calls...)
}

// SpeechConfig selects a speech provider (docs/speech.md).
type SpeechConfig struct {
	// Provider: "openai" (the OpenAI-compatible speech API), "fake"
	// (tests only), or "" for none: speech@1 speak is unavailable.
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	// Voices offered to workflows, the first the default.
	Voices []string
}

// SpeechConfigFromEnv reads TASKIEM_SPEECH_*.
func SpeechConfigFromEnv(lookup func(string) (string, bool)) (SpeechConfig, error) {
	get := func(k string) string { v, _ := lookup(k); return strings.TrimSpace(v) }
	c := SpeechConfig{Provider: strings.ToLower(get("TASKIEM_SPEECH_PROVIDER")), BaseURL: get("TASKIEM_SPEECH_BASE_URL"),
		APIKey: get("TASKIEM_SPEECH_API_KEY"), Model: get("TASKIEM_SPEECH_MODEL"), Voices: slices.Clone(DefaultVoices)}
	switch c.Provider {
	case "", "off", "none":
		c.Provider = ""
		return c, nil
	case "openai":
		if c.APIKey == "" {
			return c, errors.New("TASKIEM_SPEECH_PROVIDER=openai needs TASKIEM_SPEECH_API_KEY")
		}
		if c.Model == "" {
			c.Model = DefaultSpeechModel
		}
	case "fake":
	default:
		return c, fmt.Errorf("TASKIEM_SPEECH_PROVIDER: unknown provider %q (openai)", c.Provider)
	}
	if s := get("TASKIEM_SPEECH_VOICES"); s != "" {
		c.Voices = nil
		for _, v := range strings.Split(s, ",") {
			v = strings.ToLower(strings.TrimSpace(v))
			if v == "" || len(v) > 32 || strings.Trim(v, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
				return c, fmt.Errorf("TASKIEM_SPEECH_VOICES: %q is not a voice name", v)
			}
			if !slices.Contains(c.Voices, v) {
				c.Voices = append(c.Voices, v)
			}
		}
	}
	return c, nil
}

// NewSpeaker returns the speaker c describes (nil when speech is off).
func NewSpeaker(c SpeechConfig, client HTTPClient) (Speaker, error) {
	switch c.Provider {
	case "":
		return nil, nil
	case "openai":
		return &OpenAISpeech{BaseURL: c.BaseURL, APIKey: c.APIKey, Model: c.Model, HTTP: client}, nil
	case "fake":
		return &FakeSpeaker{}, nil
	}
	return nil, fmt.Errorf("voice: unknown speech provider %q", c.Provider)
}
