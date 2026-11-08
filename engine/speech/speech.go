// Package speech is speech in workflows (decision 0062 D5, D7; plan V3;
// docs/speech.md): it hears a customer's voice note and speaks a reply
// for speech@1, behind the owner's "Voice in workflows" switch and the
// plan's monthly allowance of seconds, and counts what each organisation
// used. The providers are engine/voice's; nothing here keeps audio or
// text, which the run's file store and history do (sealed).
package speech

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/voice"
)

// Errors the connector turns into a step's outcome. Every one of these is
// fatal for the step: retrying cannot help until a person acts.
var (
	// ErrOff: the owner has not turned "Voice in workflows" on.
	ErrOff = errors.New("speech: Voice in workflows is off for this business; the owner turns it on in Settings")
	// ErrUnavailable: this Taskiem has no speech service of that kind.
	ErrUnavailable = errors.New("speech: this Taskiem has no speech service set up")
	// ErrOverAllowance: this month's seconds are used up.
	ErrOverAllowance = errors.New("speech: this month's speech allowance is used up")
	// ErrRefused: the request itself cannot be spoken (empty, too long,
	// an unknown voice or format), or the service refused it.
	ErrRefused = errors.New("speech: refused")
)

// ErrService is a speech service that failed in a way a retry may mend
// (down, slow, rate limited).
var ErrService = errors.New("speech: the speech service did not answer")

// Reasons a voice note was not heard; each has a fixed answer in every
// language (T8), from the catalogue as speech.cannot_hear.<reason>.
const (
	ReasonEmpty       = "empty"       // no words in it
	ReasonTooLong     = "too_long"    // longer than this Taskiem hears
	ReasonUnreadable  = "unreadable"  // not audio this can hear
	ReasonUnavailable = "unavailable" // the service is down or the allowance used up
)

// Reasons lists them, for the connector's input check.
var Reasons = []string{ReasonEmpty, ReasonTooLong, ReasonUnreadable, ReasonUnavailable}

// Hearing limits: a customer's voice note may be longer than a command to
// Taskiem's own chat (voice.Limits' 60 seconds), never past the run file
// limits (16 MB, 5 minutes).
const (
	DefaultHearSeconds = 180
	MaxHearSeconds     = 300
	maxHearBytes       = media.MaxVoiceBytes
)

// Service hears and speaks for workflows.
type Service struct {
	Pool *pgxpool.Pool
	Hear voice.Provider // nil: no transcription
	Say  voice.Speaker  // nil: no speech
	// Voices offered, the first the default (voice.DefaultVoices).
	Voices []string
	// HearSeconds caps a voice note's length (DefaultHearSeconds).
	HearSeconds int
	// Allowance is the tenant's seconds a month (0: no limit); nil: no
	// limit.
	Allowance func(ctx context.Context, tenant uuid.UUID) (int64, error)
	// LimitHit records a refusal at the allowance (alerts); optional.
	LimitHit func(ctx context.Context, tenant uuid.UUID, key string)
	// Catalog has the fixed answers (lang.Default() when nil).
	Catalog *lang.Catalog
}

// Heard is what a voice note said, or why it could not be heard.
type Heard struct {
	OK       bool
	Text     string
	Language string // what the service heard, when it says
	Reason   string // when not OK: a Reasons value
	Reply    string // when not OK: the fixed answer to send the customer
	Seconds  int64  // counted against the allowance
}

// Spoken is a reply as audio.
type Spoken struct {
	Data       []byte
	Type       string // audio/ogg or audio/mpeg
	Seconds    int64
	Characters int
}

// Usage is a tenant's month.
type Usage struct {
	SecondsHeard, SecondsSpoken, CharactersSpoken, Requests, Refused int64
}

// Seconds is what counts against the allowance.
func (u Usage) Seconds() int64 { return u.SecondsHeard + u.SecondsSpoken }

// UsageThisMonth is what a tenant's workflows used this UTC month.
func UsageThisMonth(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Usage, error) {
	var u Usage
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(seconds_heard), 0)::bigint, COALESCE(sum(seconds_spoken), 0)::bigint,
		COALESCE(sum(characters_spoken), 0)::bigint, COALESCE(sum(requests), 0)::bigint, COALESCE(sum(refused), 0)::bigint
		FROM speech_usage WHERE tenant_id = $1 AND month = date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenant).
		Scan(&u.SecondsHeard, &u.SecondsSpoken, &u.CharactersSpoken, &u.Requests, &u.Refused)
	return u, err
}

// On reports whether the owner turned "Voice in workflows" on.
func (s *Service) On(ctx context.Context, tenant uuid.UUID) (bool, error) {
	if s == nil || s.Pool == nil {
		return false, nil
	}
	on := false
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT workflow_voice FROM tenant_channel_settings WHERE tenant_id = $1`, tenant).Scan(&on)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return on, err
}

// CanHear and CanSpeak report whether this Taskiem has the service.
func (s *Service) CanHear() bool  { return s != nil && s.Hear != nil }
func (s *Service) CanSpeak() bool { return s != nil && s.Say != nil }

func (s *Service) voices() []string {
	if s != nil && len(s.Voices) > 0 {
		return s.Voices
	}
	return voice.DefaultVoices
}

// Voices the service offers, the first the default.
func (s *Service) VoiceList() []string { return slices.Clone(s.voices()) }

// CannotHear is the fixed answer for a reason, in a language (English
// when the language or reason is unknown).
func (s *Service) CannotHear(reason, language string) string {
	c := lang.Default()
	if s != nil && s.Catalog != nil {
		c = s.Catalog
	}
	if !slices.Contains(Reasons, reason) {
		reason = ReasonUnreadable
	}
	t := lang.Tag(strings.ToLower(strings.TrimSpace(language)))
	if !lang.Valid(t) {
		t = lang.EN
	}
	return c.Text(t, "speech.cannot_hear."+reason)
}

// ready checks the switch and the service, before anything is spent.
func (s *Service) ready(ctx context.Context, tenant uuid.UUID, have bool) error {
	if !have {
		return ErrUnavailable
	}
	on, err := s.On(ctx, tenant)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrService, err)
	}
	if !on {
		return ErrOff
	}
	return nil
}

// spend checks that n more seconds fit this month's allowance.
func (s *Service) spend(ctx context.Context, tenant uuid.UUID, n int64) error {
	if s.Allowance == nil {
		return nil
	}
	allowed, err := s.Allowance(ctx, tenant)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrService, err)
	}
	if allowed <= 0 {
		return nil
	}
	var used Usage
	if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		used, err = UsageThisMonth(ctx, tx, tenant)
		return err
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrService, err)
	}
	if used.Seconds()+n > allowed {
		if s.LimitHit != nil {
			s.LimitHit(ctx, tenant, "speech_seconds_monthly")
		}
		return fmt.Errorf("%w (%d of %d seconds used; it renews next month or when the plan's allowance is raised)", ErrOverAllowance, used.Seconds(), allowed)
	}
	return nil
}

// count adds to the tenant's month; a failure to count is logged by the
// caller's metrics only, never fails the reply.
func (s *Service) count(ctx context.Context, tenant uuid.UUID, heard, spoken, chars int64, refused bool) {
	if s.Pool == nil {
		return
	}
	req, ref := int64(1), int64(0)
	if refused {
		req, ref = 0, 1
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO speech_usage (tenant_id, month, seconds_heard, seconds_spoken, characters_spoken, requests, refused)
			VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id, month) DO UPDATE SET seconds_heard = speech_usage.seconds_heard + EXCLUDED.seconds_heard,
			  seconds_spoken = speech_usage.seconds_spoken + EXCLUDED.seconds_spoken,
			  characters_spoken = speech_usage.characters_spoken + EXCLUDED.characters_spoken,
			  requests = speech_usage.requests + EXCLUDED.requests, refused = speech_usage.refused + EXCLUDED.refused, updated_at = now()`,
			tenant, heard, spoken, chars, req, ref)
		return err
	})
}

// estimate is a recording's length when its container does not say: AMR
// at 12.2 kbit/s, anything else at 128 kbit/s (MP3, M4A, AAC, WebM). It
// may count high for low bit rates, never low enough to hide a long one.
func estimate(data []byte, mime string) int64 {
	rate := 16000
	if voice.Extension(mime) == "amr" {
		rate = 1600
	}
	return max(1, int64(math.Ceil(float64(len(data))/float64(rate))))
}

// Transcribe hears a voice note. A note that cannot be heard (empty, too
// long, not audio) is not an error: Heard says why and gives the fixed
// answer, so the workflow can reply. language is a Taskiem language tag
// (en, pcm, yo, ha, ig) for the service's hint, or "" to let it detect.
func (s *Service) Transcribe(ctx context.Context, tenant uuid.UUID, data []byte, mime, language string) (Heard, error) {
	if err := s.ready(ctx, tenant, s.CanHear()); err != nil {
		s.outcome("hear", err)
		if errors.Is(err, ErrOff) {
			s.count(ctx, tenant, 0, 0, 0, true)
		}
		return Heard{}, err
	}
	notHeard := func(reason string, secs int64) (Heard, error) {
		telemetry.SpeechRequests.WithLabelValues("hear", "not_heard").Inc()
		return Heard{Reason: reason, Reply: s.CannotHear(reason, language), Seconds: secs}, nil
	}
	limit := s.HearSeconds
	if limit <= 0 {
		limit = DefaultHearSeconds
	}
	limit = min(limit, MaxHearSeconds)
	d, err := voice.Check(voice.Audio{Data: data, MimeType: mime}, voice.Limits{MaxBytes: maxHearBytes, MaxDuration: time.Duration(limit) * time.Second})
	if err == nil && voice.Extension(mime) == "amr" {
		// Kept as a run file, but OpenAI-compatible services take no AMR:
		// heard once a transcoder exists (plan F7).
		err = voice.ErrUnsupported
	}
	switch {
	case errors.Is(err, voice.ErrTooLong), errors.Is(err, voice.ErrTooLarge):
		return notHeard(ReasonTooLong, 0)
	case errors.Is(err, voice.ErrEmpty):
		return notHeard(ReasonEmpty, 0)
	case err != nil:
		return notHeard(ReasonUnreadable, 0)
	}
	secs := int64(math.Ceil(d.Seconds()))
	if d == 0 {
		secs = estimate(data, mime)
		if secs > int64(limit) {
			return notHeard(ReasonTooLong, 0)
		}
	}
	secs = max(secs, 1)
	if err := s.spend(ctx, tenant, secs); err != nil {
		s.outcome("hear", err)
		s.count(ctx, tenant, 0, 0, 0, true)
		return Heard{}, err
	}
	hint := ""
	if info, ok := lang.Lookup(lang.Tag(strings.ToLower(strings.TrimSpace(language)))); ok {
		hint = info.Speech
	}
	start := time.Now()
	t, err := s.Hear.Transcribe(ctx, voice.Audio{Data: data, MimeType: mime, Language: hint})
	telemetry.SpeechSeconds.WithLabelValues("hear").Observe(time.Since(start).Seconds())
	switch {
	case errors.Is(err, voice.ErrEmpty):
		// The service listened: the seconds count.
		s.count(ctx, tenant, secs, 0, 0, false)
		return notHeard(ReasonEmpty, secs)
	case errors.Is(err, voice.ErrUnsupported):
		return notHeard(ReasonUnreadable, 0)
	case err != nil:
		err = serviceErr(err)
		s.outcome("hear", err)
		return Heard{}, err
	}
	s.count(ctx, tenant, secs, 0, 0, false)
	telemetry.SpeechRequests.WithLabelValues("hear", "done").Inc()
	return Heard{OK: true, Text: t.Text, Language: t.Language, Seconds: secs}, nil
}

// Speak speaks text as a reply: Ogg/Opus (a voice note) or MP3. The voice
// is one the service offers ("" for the default).
func (s *Service) Speak(ctx context.Context, tenant uuid.UUID, text, voiceName, format, instructions string) (Spoken, error) {
	if err := s.ready(ctx, tenant, s.CanSpeak()); err != nil {
		s.outcome("speak", err)
		if errors.Is(err, ErrOff) {
			s.count(ctx, tenant, 0, 0, 0, true)
		}
		return Spoken{}, err
	}
	if voiceName == "" {
		voiceName = s.voices()[0]
	}
	if format == "" {
		format = voice.FormatOgg
	}
	text = strings.TrimSpace(text)
	r := voice.SpeechRequest{Text: text, Voice: strings.ToLower(voiceName), Format: format, Instructions: strings.TrimSpace(instructions)}
	if err := voice.CheckSpeech(r, s.voices()); err != nil {
		if errors.Is(err, voice.ErrUnknownVoice) {
			err = fmt.Errorf("%w (one of %s)", err, strings.Join(s.voices(), ", "))
		}
		err = fmt.Errorf("%w: %w", ErrRefused, err)
		s.outcome("speak", err)
		return Spoken{}, err
	}
	chars := utf8.RuneCountInString(text)
	// About 15 characters a second of speech; the real length is counted
	// once the audio is back.
	guess := max(1, int64(chars/15))
	if err := s.spend(ctx, tenant, guess); err != nil {
		s.outcome("speak", err)
		s.count(ctx, tenant, 0, 0, 0, true)
		return Spoken{}, err
	}
	start := time.Now()
	sp, err := s.Say.Speak(ctx, r)
	telemetry.SpeechSeconds.WithLabelValues("speak").Observe(time.Since(start).Seconds())
	if err != nil {
		err = serviceErr(err)
		s.outcome("speak", err)
		return Spoken{}, err
	}
	secs := guess
	if format == voice.FormatOgg {
		// A voice note must be Opus, or neither app plays it (plan F7):
		// a service that answers anything else is set up wrongly.
		if !media.IsOpus(sp.Data) {
			err := fmt.Errorf("%w: the speech service did not answer Ogg/Opus audio; it cannot be sent as a voice note", ErrRefused)
			s.outcome("speak", err)
			return Spoken{}, err
		}
		if d, ok := voice.OggDuration(sp.Data); ok {
			secs = max(1, int64(math.Ceil(d.Seconds())))
		}
	}
	s.count(ctx, tenant, 0, secs, int64(chars), false)
	telemetry.SpeechRequests.WithLabelValues("speak", "done").Inc()
	return Spoken{Data: sp.Data, Type: sp.MimeType, Seconds: secs, Characters: chars}, nil
}

// serviceErr sorts a provider's failure: a refusal retrying cannot mend
// is ErrRefused, anything else ErrService.
func serviceErr(err error) error {
	var se *voice.ServiceError
	if errors.As(err, &se) && se.Permanent() {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return fmt.Errorf("%w: %w", ErrService, err)
}

func (s *Service) outcome(kind string, err error) {
	o := "failed"
	if errors.Is(err, ErrOff) || errors.Is(err, ErrOverAllowance) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrRefused) {
		o = "refused"
	}
	telemetry.SpeechRequests.WithLabelValues(kind, o).Inc()
}
