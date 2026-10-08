package api

import (
	"context"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/voice"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Voice notes on WhatsApp (spec 11.6, decision 0029; a beta). A bound
// person's voice note, in a tenant that turned voice notes on, on a
// deployment with a transcription provider, is fetched from the Graph API
// (capped in size), checked for length, sent to the provider with the
// person's language as a hint, and then read exactly as if it had been
// typed, with the reply saying it is a beta and what was heard. Only new
// requests are taken by voice: an answer, a confirmation or a "yes" must
// be typed or tapped. The audio is held in memory only; the transcript is
// stored sealed for the retention period (voice_transcripts) and never
// logged. Anything that goes wrong asks for text instead.

// voiceOn reports whether voice notes are transcribed for a tenant.
func (s *Server) voiceOn(tc tenantChannel) bool {
	return s.Voice != nil && s.Voice.Provider != nil && tc.Voice
}

const (
	voiceTranscribeTimeout = 45 * time.Second
	voiceMaxTranscript     = 1000 // runes read as a command
	voiceEcho              = 300  // runes shown back
)

func (s *Server) waVoice(ctx context.Context, c *chat) error {
	if !s.voiceOn(c.lang.Channel) {
		return c.say(ctx, s, c.t("voice.off"))
	}
	if c.state != stateIdle {
		return c.say(ctx, s, c.t("voice.type_now"))
	}
	// Per number and per tenant: transcription costs money and time.
	if !s.limiter("wa-voice:"+c.number, 30*time.Second, 5).Allow() || !s.limiter("wa-voice-tenant:"+c.tenant.ID.String(), 2*time.Second, 30).Allow() {
		return c.say(ctx, s, c.t("voice.too_many"))
	}
	lim := s.Voice.Limits.WithDefaults()
	media := c.in.Media
	refuse := func(status, reason, msg string, dur time.Duration, size int) error {
		if err := s.recordVoice(ctx, c, status, reason, dur, size, ""); err != nil {
			return err
		}
		return c.say(ctx, s, msg)
	}
	if voice.Extension(media.MimeType) == "" {
		return refuse("refused", "unsupported", c.t("voice.unsupported"), 0, 0)
	}
	info, err := c.wa.Client.MediaInfo(ctx, media.ID)
	if err != nil {
		s.Logger.Warn("whatsapp: looking up a voice note", "err", err)
		return refuse("failed", "download_error", c.t("voice.failed"), 0, 0)
	}
	mime := media.MimeType
	if info.MimeType != "" {
		mime = info.MimeType
	}
	if info.SHA256 == "" {
		info.SHA256 = media.SHA256
	}
	data, err := c.wa.Client.Download(ctx, info, lim.MaxBytes)
	if errors.Is(err, whatsapp.ErrMediaTooLarge) {
		return refuse("refused", "too_large", c.t("voice.too_large"), 0, int(info.Size))
	}
	if err != nil {
		s.Logger.Warn("whatsapp: downloading a voice note", "err", err)
		return refuse("failed", "download_error", c.t("voice.failed"), 0, 0)
	}
	audio := voice.Audio{Data: data, MimeType: mime}
	if info, ok := lang.Lookup(c.lang.Tag); ok {
		audio.Language = info.Speech
	}
	dur, err := voice.Check(audio, lim)
	switch {
	case errors.Is(err, voice.ErrTooLong):
		return refuse("refused", "too_long", c.t("voice.too_long", "seconds", strconv.Itoa(int(lim.MaxDuration/time.Second))), dur, len(data))
	case errors.Is(err, voice.ErrTooLarge):
		return refuse("refused", "too_large", c.t("voice.too_large"), dur, len(data))
	case errors.Is(err, voice.ErrEmpty):
		return refuse("refused", "empty", c.t("voice.empty"), dur, len(data))
	case err != nil:
		return refuse("refused", "unsupported", c.t("voice.unsupported"), dur, len(data))
	}
	tctx, cancel := context.WithTimeout(ctx, voiceTranscribeTimeout)
	got, err := s.Voice.Provider.Transcribe(tctx, audio)
	cancel()
	clear(data) // the recording is not kept
	switch {
	case errors.Is(err, voice.ErrEmpty):
		return refuse("refused", "empty", c.t("voice.empty"), dur, len(audio.Data))
	case err != nil:
		s.Logger.Warn("whatsapp: transcribing a voice note", "provider", s.Voice.Provider.Name(), "err", err)
		return refuse("failed", "provider_error", c.t("voice.failed"), dur, len(audio.Data))
	}
	text := got.Text
	if utf8.RuneCountInString(text) > voiceMaxTranscript {
		text = string([]rune(text)[:voiceMaxTranscript])
	}
	if err := s.recordVoice(ctx, c, "transcribed", "", dur, len(audio.Data), text); err != nil {
		return err
	}
	// Read as typed, in the language the words are in.
	c.lang = s.chooseLang(ctx, c.tenant.ID, c.number, text)
	echo := text
	if utf8.RuneCountInString(echo) > voiceEcho {
		echo = string([]rune(echo)[:voiceEcho]) + "…"
	}
	c.note = c.t("voice.heard", "transcript", whatsapp.SafeText(pii.Redact(echo)))
	if c.lang.Detected {
		c.note += "\n" + c.t("lang.detected", "language", langName(c.lang.Tag))
	}
	c.in.Text, c.in.Type, c.in.Media, c.in.Reply = text, "text", nil, ""
	if err := s.waCommand(ctx, c); err != nil {
		return err
	}
	if c.note != "" {
		// The command answered with something other than a reply (fresh
		// approval requests): say what was heard anyway.
		return s.waSay(ctx, c.wa, c.number, c.tenant.Name, c.note)
	}
	return nil
}

// recordVoice keeps what happened to a voice note: the transcript sealed,
// for the retention period; refusals and failures with their reason only.
func (s *Server) recordVoice(ctx context.Context, c *chat, status, reason string, dur time.Duration, size int, text string) error {
	retention := s.Voice.Retention
	if retention <= 0 {
		retention = voice.DefaultRetention
	}
	var durMS *int
	if dur > 0 {
		ms := int(dur / time.Millisecond)
		durMS = &ms
	}
	var reasonP *string
	if reason != "" {
		reasonP = &reason
	}
	return db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var sealed any // SQL NULL unless transcribed
		if text != "" {
			env, err := s.Store.PII.SealTx(ctx, tx, c.tenant.ID, "other", text)
			if err != nil {
				return err
			}
			sealed = env
		}
		_, err := tx.Exec(ctx, `INSERT INTO voice_transcripts (id, tenant_id, user_id, provider, language, status, reason, duration_ms, bytes, transcript, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now() + $11::interval)`,
			uuid.Must(uuid.NewV7()), c.tenant.ID, c.p.UserID, s.Voice.Provider.Name(), string(c.lang.Tag), status, reasonP, durMS, size, sealed, retention.String())
		return err
	})
}

// purgeTranscripts deletes transcripts past their retention, in every
// tenant, through a definer function that reads no payload.
func (s *Server) purgeTranscripts(ctx context.Context) error {
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_voice_transcripts_purge()`)
	return err
}
