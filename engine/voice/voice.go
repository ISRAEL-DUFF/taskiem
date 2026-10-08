// Package voice turns voice notes into text (spec 11.6, decision 0029):
// one small interface in front of a speech-to-text service, a fake for
// tests, and an implementation for the OpenAI-compatible transcription
// endpoint (OpenAI's own, or a self-hosted server speaking the same API),
// built from its public API reference.
//
// Transcription is a beta, off unless an operator configures a provider
// (TASKIEM_TRANSCRIBE_*) and a tenant turns voice notes on. Audio is held
// in memory only, capped in size and duration before it leaves; the
// transcript is personal data, which the caller seals.
package voice

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Audio is a recording to transcribe.
type Audio struct {
	Data     []byte
	MimeType string // as the channel reported it ("audio/ogg; codecs=opus")
	// Language is a hint (ISO 639-1); "" lets the provider detect it.
	Language string
}

// Transcript is what was heard.
type Transcript struct {
	Text string
	// Language is what the provider reports it heard, when it says.
	Language string
}

// Provider transcribes audio. Implementations must not keep the audio.
type Provider interface {
	Name() string
	Transcribe(ctx context.Context, a Audio) (Transcript, error)
}

// Errors a caller tells people apart.
var (
	ErrTooLarge    = errors.New("voice: the recording is too large")
	ErrTooLong     = errors.New("voice: the recording is too long")
	ErrUnsupported = errors.New("voice: not an audio format this takes")
	ErrEmpty       = errors.New("voice: no words were heard")
)

// Limits cap what is sent for transcription.
type Limits struct {
	MaxBytes    int64         // default 1 MiB
	MaxDuration time.Duration // default 60 seconds
}

// Defaults.
const (
	DefaultMaxBytes    = 1 << 20
	DefaultMaxDuration = 60 * time.Second
	DefaultRetention   = 7 * 24 * time.Hour
	// Ceilings an operator cannot raise past: WhatsApp's own audio limit
	// is 16 MB, and long recordings are not commands.
	maxBytesCeiling    = 16 << 20
	maxDurationCeiling = 5 * time.Minute
)

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxDuration <= 0 {
		l.MaxDuration = DefaultMaxDuration
	}
	return l
}

// WithDefaults is l with defaults filled in.
func (l Limits) WithDefaults() Limits { return l.withDefaults() }

// audioTypes are the containers taken (WhatsApp sends voice notes as
// Ogg/Opus; audio files may be any of these).
var audioTypes = map[string]string{
	"audio/ogg": "ogg", "audio/opus": "ogg", "audio/mpeg": "mp3", "audio/mp3": "mp3", "audio/mp4": "m4a",
	"audio/m4a": "m4a", "audio/x-m4a": "m4a", "audio/aac": "m4a", "audio/amr": "amr", "audio/wav": "wav", "audio/x-wav": "wav", "audio/webm": "webm",
}

// Extension is the file extension for a MIME type, or "" when the type is
// not taken.
func Extension(mime string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mime)), ";")
	return audioTypes[strings.TrimSpace(base)]
}

// Check applies the limits to a recording before it is sent: the type, the
// size, and for Ogg the duration read from the stream itself (the last
// page's granule position; Opus counts at 48 kHz). Other containers are
// held to the size cap only.
func Check(a Audio, l Limits) (time.Duration, error) {
	l = l.withDefaults()
	if Extension(a.MimeType) == "" {
		return 0, ErrUnsupported
	}
	if int64(len(a.Data)) > l.MaxBytes {
		return 0, ErrTooLarge
	}
	if len(a.Data) == 0 {
		return 0, ErrEmpty
	}
	if Extension(a.MimeType) == "ogg" {
		d, ok := OggDuration(a.Data)
		if !ok {
			return 0, ErrUnsupported
		}
		if d > l.MaxDuration {
			return d, ErrTooLong
		}
		return d, nil
	}
	return 0, nil
}

// OggDuration reads an Ogg stream's length from its last page's granule
// position (RFC 3533 page header: "OggS", version, flags, a 64-bit
// little-endian granule position). Opus streams count 48 kHz samples
// (RFC 7845), less the pre-skip in the identification header.
func OggDuration(data []byte) (time.Duration, bool) {
	if len(data) < 27 || string(data[:4]) != "OggS" {
		return 0, false
	}
	last := -1
	for i := len(data) - 27; i >= 0; i-- {
		if data[i] == 'O' && string(data[i:i+4]) == "OggS" && data[i+4] == 0 {
			last = i
			break
		}
	}
	if last < 0 {
		return 0, false
	}
	granule := binary.LittleEndian.Uint64(data[last+6 : last+14])
	if granule == ^uint64(0) {
		return 0, false // a page with no packet ending on it
	}
	rate := uint64(48000)
	var preSkip uint64
	if i := strings.Index(string(data[:min(len(data), 512)]), "OpusHead"); i >= 0 && i+12 <= len(data) {
		preSkip = uint64(binary.LittleEndian.Uint16(data[i+10 : i+12]))
	} else if i := strings.Index(string(data[:min(len(data), 512)]), "\x01vorbis"); i >= 0 && i+16 <= len(data) {
		rate = uint64(binary.LittleEndian.Uint32(data[i+12 : i+16]))
		if rate == 0 {
			return 0, false
		}
	}
	if granule < preSkip {
		granule = preSkip
	}
	n := granule - preSkip
	if n/rate > uint64(24*time.Hour/time.Second) {
		return 24 * time.Hour, true // absurd for a voice note: certainly too long
	}
	return time.Duration(n/rate)*time.Second + time.Duration(n%rate*uint64(time.Second)/rate), true //nolint:gosec // bounded to a day above
}

// Config selects a provider (docs/languages.md#voice-notes).
type Config struct {
	// Provider: "openai" (the OpenAI-compatible transcription API), "fake"
	// (tests only), or "" for none: voice notes are answered with a
	// request to type.
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Limits   Limits
	// Retention is how long a sealed transcript is kept.
	Retention time.Duration
}

// ConfigFromEnv reads TASKIEM_TRANSCRIBE_*.
func ConfigFromEnv(lookup func(string) (string, bool)) (Config, error) {
	get := func(k string) string { v, _ := lookup(k); return strings.TrimSpace(v) }
	c := Config{Provider: strings.ToLower(get("TASKIEM_TRANSCRIBE_PROVIDER")), BaseURL: get("TASKIEM_TRANSCRIBE_BASE_URL"),
		APIKey: get("TASKIEM_TRANSCRIBE_API_KEY"), Model: get("TASKIEM_TRANSCRIBE_MODEL"), Retention: DefaultRetention}
	switch c.Provider {
	case "", "off", "none":
		c.Provider = ""
		return c, nil
	case "openai":
		if c.APIKey == "" {
			return c, errors.New("TASKIEM_TRANSCRIBE_PROVIDER=openai needs TASKIEM_TRANSCRIBE_API_KEY")
		}
		if c.Model == "" {
			c.Model = DefaultOpenAIModel
		}
	case "fake":
	default:
		return c, fmt.Errorf("TASKIEM_TRANSCRIBE_PROVIDER: unknown provider %q (openai)", c.Provider)
	}
	if s := get("TASKIEM_TRANSCRIBE_MAX_BYTES"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1024 || n > maxBytesCeiling {
			return c, fmt.Errorf("TASKIEM_TRANSCRIBE_MAX_BYTES: %q is not between 1024 and %d", s, maxBytesCeiling)
		}
		c.Limits.MaxBytes = n
	}
	if s := get("TASKIEM_TRANSCRIBE_MAX_SECONDS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || time.Duration(n)*time.Second > maxDurationCeiling {
			return c, fmt.Errorf("TASKIEM_TRANSCRIBE_MAX_SECONDS: %q is not between 1 and %d", s, int(maxDurationCeiling/time.Second))
		}
		c.Limits.MaxDuration = time.Duration(n) * time.Second
	}
	if s := get("TASKIEM_TRANSCRIBE_RETENTION"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < time.Hour || d > 90*24*time.Hour {
			return c, fmt.Errorf("TASKIEM_TRANSCRIBE_RETENTION: %q is not a duration between 1h and 2160h", s)
		}
		c.Retention = d
	}
	c.Limits = c.Limits.withDefaults()
	return c, nil
}

// Fake is a provider for tests: it answers with Respond, or else Text, and
// records what it was sent.
type Fake struct {
	Respond func(a Audio) (Transcript, error)
	Text    string

	mu    sync.Mutex
	calls []Audio
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Transcribe(ctx context.Context, a Audio) (Transcript, error) {
	if err := ctx.Err(); err != nil {
		return Transcript{}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, Audio{MimeType: a.MimeType, Language: a.Language, Data: append([]byte(nil), a.Data...)})
	f.mu.Unlock()
	if f.Respond != nil {
		return f.Respond(a)
	}
	return Transcript{Text: f.Text}, nil
}

// Calls returns what the fake was sent.
func (f *Fake) Calls() []Audio {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Audio(nil), f.calls...)
}

// New returns the provider c describes (nil when voice notes are off). A
// fake configured from the environment hears nothing; tests build their
// own.
func New(c Config, client HTTPClient) (Provider, error) {
	switch c.Provider {
	case "":
		return nil, nil
	case "openai":
		return &OpenAI{BaseURL: c.BaseURL, APIKey: c.APIKey, Model: c.Model, HTTP: client}, nil
	case "fake":
		return &Fake{}, nil
	}
	return nil, fmt.Errorf("voice: unknown provider %q", c.Provider)
}
