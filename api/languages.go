package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/intent"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/voice"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Languages on WhatsApp, USSD and SMS (spec 11.6, decision 0029,
// docs/languages.md). Everything Taskiem itself says comes from the
// catalogues in engine/lang, English unless a number's language is one
// that is turned on: by the operator for every tenant (TASKIEM_LANGUAGES),
// or by a tenant for its own people and callers. A number's language is
// the one it chose ("language yoruba"), else the one detected from its
// messages, else the tenant's default; English whenever that language is
// not on. Catalogues other than English are drafts until native speakers
// review them, and nothing here claims otherwise.

// LanguageSettings are the operator's choices.
type LanguageSettings struct {
	// Enabled are the languages on for every tenant (TASKIEM_LANGUAGES);
	// English is always on.
	Enabled []lang.Tag
	// Model routes messages the word lists do not recognise, in a language
	// other than English (engine/ai/intent); nil leaves them unrecognised.
	Model ai.Provider
}

// VoiceSettings turn voice-note transcription on (a beta; nil: off).
type VoiceSettings struct {
	Provider  voice.Provider
	Limits    voice.Limits
	Retention time.Duration // default voice.DefaultRetention
}

// tenantChannel is a tenant's channel settings.
type tenantChannel struct {
	Languages []lang.Tag
	Default   lang.Tag
	Voice     bool
	at        time.Time
}

const tenantChannelTTL = 30 * time.Second

// tr is a message in a language.
func tr(t lang.Tag, id string, kv ...string) string { return lang.Default().Text(t, id, kv...) }

func (s *Server) operatorLangs() []lang.Tag {
	if s.Languages == nil || len(s.Languages.Enabled) == 0 {
		return []lang.Tag{lang.EN}
	}
	return s.Languages.Enabled
}

// tenantChannel reads a tenant's settings, cached briefly (the USSD fast
// path reads them within its budget); uuid.Nil has none.
func (s *Server) tenantChannel(ctx context.Context, tenant uuid.UUID) (tenantChannel, error) {
	tc := tenantChannel{Default: lang.EN}
	if tenant == uuid.Nil {
		return tc, nil
	}
	if v, ok := s.chanSettings.Get(tenant); ok && time.Since(v.at) < tenantChannelTTL {
		return v, nil
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var langs []string
		var def string
		err := tx.QueryRow(ctx, `SELECT languages, default_language, voice_notes FROM tenant_channel_settings WHERE tenant_id = $1`, tenant).Scan(&langs, &def, &tc.Voice)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		tc.Default = lang.Tag(def)
		for _, l := range langs {
			tc.Languages = append(tc.Languages, lang.Tag(l))
		}
		return nil
	})
	if err != nil {
		return tc, err
	}
	tc.at = time.Now()
	s.chanSettings.Put(tenant, tc)
	return tc, nil
}

// langsOn are the languages on for a tenant: the operator's and the
// tenant's, English first.
func (s *Server) langsOn(tc tenantChannel) []lang.Tag {
	out := slices.Clone(s.operatorLangs())
	for _, t := range tc.Languages {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// numberLang is the language a number chose or was detected in ("" when
// none).
func (s *Server) numberLang(ctx context.Context, number string) (lang.Tag, string, error) {
	var l, src string
	err := s.Store.Pool.QueryRow(ctx, `SELECT language, source FROM taskiem_lang_get($1)`, number).Scan(&l, &src)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return lang.Tag(l), src, err
}

func (s *Server) setNumberLang(ctx context.Context, number string, t lang.Tag, source string) error {
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_lang_set($1, $2, $3)`, number, string(t), source)
	return err
}

// langChoice is the language for a number in a tenant, and the lists to
// match commands against.
type langChoice struct {
	Tag     lang.Tag
	On      []lang.Tag
	Channel tenantChannel
	// Detected is set when this message's language was just detected and
	// recorded.
	Detected bool
}

// Match is the order commands are matched in: the number's language,
// then English.
func (l langChoice) Match() []lang.Tag {
	if l.Tag == lang.EN || l.Tag == "" {
		return []lang.Tag{lang.EN}
	}
	return []lang.Tag{l.Tag, lang.EN}
}

// chooseLang decides the language for a message from number in tenant
// (uuid.Nil: the shared number before the person is known). With text, a
// language not chosen by command is detected from it and recorded.
func (s *Server) chooseLang(ctx context.Context, tenant uuid.UUID, number, text string) langChoice {
	c := langChoice{Tag: lang.EN, On: s.operatorLangs()}
	tc, err := s.tenantChannel(ctx, tenant)
	if err != nil {
		s.Logger.Error("languages: reading a tenant's settings", "err", err)
		return c
	}
	c.Channel, c.On = tc, s.langsOn(tc)
	if len(c.On) == 1 {
		return c // English only: nothing to look up
	}
	pref, src, err := s.numberLang(ctx, number)
	if err != nil {
		s.Logger.Error("languages: reading a number's language", "err", err)
		return c
	}
	if src != "command" && strings.TrimSpace(text) != "" {
		if d, ok := lang.Detect(text, c.On); ok && d != pref {
			if err := s.setNumberLang(ctx, number, d, "detected"); err != nil {
				s.Logger.Error("languages: recording a detected language", "err", err)
			} else {
				pref, c.Detected = d, true
			}
		}
	}
	switch {
	case pref != "" && slices.Contains(c.On, pref):
		c.Tag = pref
	case slices.Contains(c.On, tc.Default):
		c.Tag = tc.Default
	}
	if c.Tag == lang.EN {
		c.Detected = false
	}
	return c
}

// langNames lists languages by their own names.
func langNames(tags []lang.Tag) string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		if i, ok := lang.Lookup(t); ok {
			names = append(names, i.Native)
		}
	}
	return strings.Join(names, ", ")
}

func langName(t lang.Tag) string {
	if i, ok := lang.Lookup(t); ok {
		return i.Native
	}
	return string(t)
}

// waLanguage answers "language" (what is on, and which this number gets)
// and "language <name>" (choose one).
func (s *Server) waLanguage(ctx context.Context, c *chat, arg string) error {
	if strings.TrimSpace(arg) == "" {
		return c.say(ctx, s, c.t("lang.current", "language", langName(c.lang.Tag), "languages", langNames(c.lang.On)))
	}
	info, ok := lang.Find(arg)
	if !ok || !slices.Contains(c.lang.On, info.Tag) {
		name := whatsapp.SafeText(arg)
		if ok {
			name = info.Native
		}
		return c.say(ctx, s, c.t("lang.unavailable", "name", name, "languages", langNames(c.lang.On)))
	}
	if err := s.setNumberLang(ctx, c.number, info.Tag, "command"); err != nil {
		return err
	}
	c.lang.Tag = info.Tag
	msg := c.t("lang.switched", "language", info.Native)
	if !lang.Default().Reviewed(info.Tag, "lang.switched") {
		msg += "\n\n" + c.t("lang.draft_note")
	}
	return c.say(ctx, s, msg)
}

// waModelIntent asks the model which command a message in a language other
// than English gives, within the tenant's AI budget and a per-number
// limit; the call is recorded like a build's (ai_interactions, kind
// intent). It returns IntentNone when it cannot tell.
func (s *Server) waModelIntent(ctx context.Context, c *chat, text string) lang.Match {
	if s.Languages == nil || s.Languages.Model == nil || c.lang.Tag == lang.EN {
		return lang.Match{}
	}
	if !s.limiter("wa-intent:"+c.number, 6*time.Second, 10).Allow() {
		return lang.Match{}
	}
	limit, used, err := s.aiBudget(ctx, c.tenant.ID)
	if err != nil || limit > 0 && used >= limit {
		return lang.Match{}
	}
	info, _ := lang.Lookup(c.lang.Tag)
	cl := &intent.Classifier{Provider: s.Languages.Model}
	res, resp, err := cl.Classify(ctx, text, info)
	outcome, errText := "valid", ""
	switch {
	case errors.Is(err, intent.ErrUnusable):
		outcome = "unparseable"
	case err != nil:
		outcome, errText = "error", err.Error()
	case res.Intent == lang.IntentNone:
		outcome = "unknown"
	}
	s.recordIntent(ctx, c, text, resp, outcome, errText)
	if err != nil {
		if !errors.Is(err, intent.ErrUnusable) {
			s.Logger.Warn("whatsapp: routing a message through the model", "err", err)
		}
		return lang.Match{}
	}
	return lang.Match{Intent: res.Intent, Arg: res.Arg, Language: c.lang.Tag}
}

func (s *Server) recordIntent(ctx context.Context, c *chat, text string, resp *ai.Response, outcome, errText string) {
	var u ai.Usage
	var respText *string
	var stop, model string
	if resp != nil {
		r := pii.Redact(resp.Text)
		respText, stop, model, u = &r, resp.StopReason, resp.Model, resp.Usage
	}
	if model == "" {
		model = s.Languages.Model.Model()
	}
	ctx = context.WithoutCancel(ctx)
	msgs := []map[string]string{{"role": "user", "text": pii.Redact(text)}}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_interactions (id, tenant_id, build_id, round, kind, actor, provider, model, system_digest, messages,
			response, stop_reason, outcome, error, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens)
			VALUES ($1, $2, NULL, 0, 'intent', $3, $4, $5, 'intent', $6, $7, NULLIF($8, ''), $9, NULLIF($10, ''), $11, $12, $13, $14, $15)`,
			uuid.Must(uuid.NewV7()), c.tenant.ID, c.p.Actor(), s.Languages.Model.Name(), model, msgs, respText, stop, outcome, pii.Redact(errText),
			u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens, u.Total())
		return err
	})
	if err != nil {
		s.Logger.Error("whatsapp: recording a model routing", "err", err)
	}
}

// --- settings API ---

type languagesView struct {
	// Operator are the languages the operator turned on for every tenant.
	Operator []lang.Tag `json:"operator"`
	// Languages are the drafts this tenant turned on for itself.
	Languages []lang.Tag `json:"languages"`
	// On are the languages its people and callers may get.
	On              []lang.Tag      `json:"on"`
	DefaultLanguage lang.Tag        `json:"default_language"`
	VoiceNotes      bool            `json:"voice_notes"`
	VoiceAvailable  bool            `json:"voice_available"`
	Catalogues      []lang.Progress `json:"catalogues"`
}

func (s *Server) languagesView(tc tenantChannel) languagesView {
	v := languagesView{Operator: s.operatorLangs(), Languages: tc.Languages, On: s.langsOn(tc), DefaultLanguage: tc.Default,
		VoiceNotes: tc.Voice, VoiceAvailable: s.Voice != nil && s.Voice.Provider != nil, Catalogues: lang.Default().Progress()}
	if v.Languages == nil {
		v.Languages = []lang.Tag{}
	}
	return v
}

// getLanguages shows the tenant's language and voice-note settings.
func (s *Server) getLanguages(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	s.chanSettings.Delete(p.TenantID)
	tc, err := s.tenantChannel(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.languagesView(tc))
}

// putLanguages turns draft languages and voice notes on or off for the
// tenant.
func (s *Server) putLanguages(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Languages       []string `json:"languages"`
		DefaultLanguage string   `json:"default_language"`
		VoiceNotes      bool     `json:"voice_notes"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tc := tenantChannel{Default: lang.EN, Voice: req.VoiceNotes, Languages: []lang.Tag{}}
	for _, l := range req.Languages {
		t := lang.Tag(strings.ToLower(strings.TrimSpace(l)))
		if t == lang.EN {
			continue // always on
		}
		if !lang.Valid(t) {
			writeErr(w, http.StatusBadRequest, "unknown language "+l+" (pcm, yo, ha, ig)")
			return
		}
		if !slices.Contains(tc.Languages, t) {
			tc.Languages = append(tc.Languages, t)
		}
	}
	if req.DefaultLanguage != "" {
		tc.Default = lang.Tag(req.DefaultLanguage)
	}
	if !slices.Contains(s.langsOn(tc), tc.Default) {
		writeErr(w, http.StatusBadRequest, "default_language must be English or a language that is on")
		return
	}
	if tc.Voice && (s.Voice == nil || s.Voice.Provider == nil) {
		writeErr(w, http.StatusConflict, "voice notes need a transcription provider configured on this deployment (TASKIEM_TRANSCRIBE_PROVIDER)")
		return
	}
	langs := make([]string, len(tc.Languages))
	for i, t := range tc.Languages {
		langs[i] = string(t)
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO tenant_channel_settings (tenant_id, languages, default_language, voice_notes, updated_by)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (tenant_id) DO UPDATE SET languages = EXCLUDED.languages,
			default_language = EXCLUDED.default_language, voice_notes = EXCLUDED.voice_notes, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			p.TenantID, langs, string(tc.Default), tc.Voice, p.Actor())
		if err != nil {
			return err
		}
		return auditTx(r, tx, "channel.languages.update", p.TenantID.String(), map[string]any{"languages": langs, "default_language": tc.Default, "voice_notes": tc.Voice})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.chanSettings.Delete(p.TenantID)
	writeJSON(w, http.StatusOK, s.languagesView(tc))
}

type transcriptView struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	Language   lang.Tag  `json:"language"`
	Provider   string    `json:"provider"`
	Status     string    `json:"status"`
	Reason     *string   `json:"reason,omitempty"`
	DurationMS *int      `json:"duration_ms,omitempty"`
	Transcript *string   `json:"transcript,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// getTranscripts lists the tenant's voice-note transcripts still within
// retention, opened (pii.reveal; audited): how native-speaker testers
// compare what was said with what was heard.
func (s *Server) getTranscripts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := []transcriptView{}
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, user_id, language, provider, status, reason, duration_ms, transcript, created_at, expires_at
			FROM voice_transcripts WHERE tenant_id = $1 AND expires_at > now() ORDER BY created_at DESC LIMIT 100`, p.TenantID)
		if err != nil {
			return err
		}
		type row struct {
			v   transcriptView
			env map[string]any
		}
		got, err := pgx.CollectRows(rows, func(rr pgx.CollectableRow) (row, error) {
			var x row
			var l string
			err := rr.Scan(&x.v.ID, &x.v.UserID, &l, &x.v.Provider, &x.v.Status, &x.v.Reason, &x.v.DurationMS, &x.env, &x.v.CreatedAt, &x.v.ExpiresAt)
			x.v.Language = lang.Tag(l)
			return x, err
		})
		if err != nil {
			return err
		}
		for _, x := range got {
			if x.env != nil {
				v, err := s.Store.PII.OpenTx(r.Context(), tx, p.TenantID, x.env)
				if errors.Is(err, pii.ErrErased) {
					e := pii.Erased
					x.v.Transcript = &e
				} else if err != nil {
					return err
				} else if t, ok := v.(string); ok {
					x.v.Transcript = &t
				}
			}
			out = append(out, x.v)
		}
		return auditTx(r, tx, "voice.transcripts.read", p.TenantID.String(), map[string]any{"count": len(out)})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transcripts": out})
}
