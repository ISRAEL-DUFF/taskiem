package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/speech"
	"github.com/israel-duff/taskiem/engine/voice"
)

// SpeechConfig is speech in workflows (docs/speech.md, decision 0062 D5):
// TASKIEM_SPEECH_* for speaking replies; hearing uses the transcription
// service already set for voice notes (TASKIEM_TRANSCRIBE_*), with its own
// length cap for customers' voice notes (TASKIEM_SPEECH_HEAR_SECONDS).
type SpeechConfig struct {
	Say         voice.SpeechConfig
	HearSeconds int
}

func speechConfig() (SpeechConfig, error) {
	var c SpeechConfig
	var err error
	if c.Say, err = voice.SpeechConfigFromEnv(os.LookupEnv); err != nil {
		return c, err
	}
	if c.Say.Provider == "fake" {
		return c, fmt.Errorf("TASKIEM_SPEECH_PROVIDER=fake is for tests only")
	}
	if c.Say.Provider != "" && c.Say.BaseURL != "" {
		if u, err := url.Parse(c.Say.BaseURL); err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return c, fmt.Errorf("TASKIEM_SPEECH_BASE_URL must be an https URL")
		}
	}
	c.HearSeconds = speech.DefaultHearSeconds
	if s := strings.TrimSpace(os.Getenv("TASKIEM_SPEECH_HEAR_SECONDS")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > speech.MaxHearSeconds {
			return c, fmt.Errorf("TASKIEM_SPEECH_HEAR_SECONDS: %q is not between 1 and %d", s, speech.MaxHearSeconds)
		}
		c.HearSeconds = n
	}
	return c, nil
}

// speechService builds speech for workflows: each provider through the
// egress guard, to its one host.
func speechService(e *engine, hear voice.Config, c SpeechConfig, log *slog.Logger) *speech.Service {
	s := &speech.Service{Pool: e.pool, Voices: c.Say.Voices, HearSeconds: c.HearSeconds,
		Allowance: func(ctx context.Context, tenant uuid.UUID) (int64, error) {
			lim, err := e.store.LimitsFor(ctx, tenant)
			return lim.SpeechSecondsMonthly, err
		},
		LimitHit: e.store.LimitHit}
	if hear.Provider == "openai" {
		o := &voice.OpenAI{BaseURL: hear.BaseURL, APIKey: hear.APIKey, Model: hear.Model}
		o.HTTP = (&egress.Guard{Logger: log}).Client(egress.Policy{Tenant: "platform", Hosts: []string{o.Host()}, Purpose: "speech_hear"}, 90*time.Second)
		s.Hear = o
	}
	if c.Say.Provider == "openai" {
		o := &voice.OpenAISpeech{BaseURL: c.Say.BaseURL, APIKey: c.Say.APIKey, Model: c.Say.Model}
		o.HTTP = (&egress.Guard{Logger: log}).Client(egress.Policy{Tenant: "platform", Hosts: []string{o.Host()}, Purpose: "speech_say"}, 90*time.Second)
		s.Say = o
	}
	if s.Hear != nil || s.Say != nil {
		log.Info("speech in workflows", "hear", s.Hear != nil, "speak", s.Say != nil, "voices", strings.Join(s.VoiceList(), ","), "hear_seconds", c.HearSeconds)
	}
	return s
}
