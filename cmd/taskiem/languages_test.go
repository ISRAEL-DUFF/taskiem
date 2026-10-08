package main

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
)

func TestLanguagesConfig(t *testing.T) {
	// Off by default: English only, no voice notes.
	c, err := languagesConfig()
	if err != nil || !slices.Equal(c.Enabled, []lang.Tag{lang.EN}) || c.Voice.Provider != "" {
		t.Fatalf("default: %+v %v", c, err)
	}
	ls, vs, err := channelLanguages(c, ai.Config{}, slog.New(slog.DiscardHandler))
	if err != nil || ls.Model != nil || vs != nil {
		t.Fatalf("default settings: %+v %+v %v", ls, vs, err)
	}

	t.Setenv("TASKIEM_LANGUAGES", "yo,pcm")
	t.Setenv("TASKIEM_TRANSCRIBE_PROVIDER", "openai")
	t.Setenv("TASKIEM_TRANSCRIBE_API_KEY", "sk-test")
	c, err = languagesConfig()
	if err != nil || !slices.Equal(c.Enabled, []lang.Tag{lang.EN, lang.YO, lang.PCM}) || c.Voice.Provider != "openai" {
		t.Fatalf("on: %+v %v", c, err)
	}
	ls, vs, err = channelLanguages(c, ai.Config{Provider: "fake"}, slog.New(slog.DiscardHandler))
	if err != nil || ls.Model == nil || vs == nil || vs.Provider.Name() != "openai" {
		t.Fatalf("settings: %+v %+v %v", ls, vs, err)
	}

	for k, v := range map[string]string{"TASKIEM_LANGUAGES": "yo,fr", "TASKIEM_TRANSCRIBE_PROVIDER": "fake", "TASKIEM_TRANSCRIBE_BASE_URL": "http://speech.internal"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := languagesConfig(); err == nil {
				t.Errorf("%s=%s accepted", k, v)
			}
		})
	}
}
