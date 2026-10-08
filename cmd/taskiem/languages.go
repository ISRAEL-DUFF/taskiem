package main

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/voice"
)

// LanguagesConfig is languages and voice notes on WhatsApp, USSD and SMS
// (docs/languages.md, decision 0029): TASKIEM_LANGUAGES and
// TASKIEM_TRANSCRIBE_*. Both default off: English only, voice notes
// answered with a request to type.
type LanguagesConfig struct {
	Enabled []lang.Tag
	Voice   voice.Config
}

func languagesConfig() (LanguagesConfig, error) {
	var c LanguagesConfig
	var err error
	if c.Enabled, err = lang.ParseList(os.Getenv("TASKIEM_LANGUAGES")); err != nil {
		return c, fmt.Errorf("TASKIEM_LANGUAGES: %w", err)
	}
	if c.Voice, err = voice.ConfigFromEnv(os.LookupEnv); err != nil {
		return c, err
	}
	if c.Voice.Provider == "fake" {
		return c, fmt.Errorf("TASKIEM_TRANSCRIBE_PROVIDER=fake is for tests only")
	}
	if c.Voice.Provider != "" && c.Voice.BaseURL != "" {
		if u, err := url.Parse(c.Voice.BaseURL); err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return c, fmt.Errorf("TASKIEM_TRANSCRIBE_BASE_URL must be an https URL")
		}
	}
	return c, nil
}

// channelLanguages builds the API's language and voice settings. The model
// that routes messages in other languages is the AI builder's provider,
// when one is configured.
func channelLanguages(c LanguagesConfig, model ai.Config, log *slog.Logger) (*api.LanguageSettings, *api.VoiceSettings, error) {
	ls := &api.LanguageSettings{Enabled: c.Enabled}
	if len(c.Enabled) > 1 {
		prov, err := ai.New(model)
		if err != nil {
			return nil, nil, err
		}
		ls.Model = prov
		names := make([]string, len(c.Enabled))
		for i, t := range c.Enabled {
			names[i] = string(t)
		}
		log.Info("languages on (drafts until reviewed by native speakers)", "languages", strings.Join(names, ","), "model_routing", prov != nil)
	}
	if p := lang.Default().Check(); len(p) > 0 {
		log.Warn("language catalogues: messages missing or wrong; they fall back to English", "problems", len(p), "first", p[0].String())
	}
	lang.Default().SetOnMissing(func(t lang.Tag, id string) {
		log.Warn("language catalogue: message missing, sent in English", "language", t, "id", id)
	})
	if c.Voice.Provider == "" {
		return ls, nil, nil
	}
	o := &voice.OpenAI{BaseURL: c.Voice.BaseURL, APIKey: c.Voice.APIKey, Model: c.Voice.Model}
	// Through the egress guard, to the one host configured.
	o.HTTP = (&egress.Guard{Logger: log}).Client(egress.Policy{Tenant: "platform", Hosts: []string{o.Host()}, Purpose: "voice_transcription"}, 60*time.Second)
	log.Info("voice-note transcription on (beta)", "provider", o.Name(), "host", o.Host(), "model", o.Model,
		"max_seconds", int(c.Voice.Limits.MaxDuration/time.Second), "retention", c.Voice.Retention.String())
	return ls, &api.VoiceSettings{Provider: o, Limits: c.Voice.Limits, Retention: c.Voice.Retention}, nil
}
