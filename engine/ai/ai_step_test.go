package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// What an ai step may retry, and which models it may name (decision 0050).
func TestTransientAndStepModels(t *testing.T) {
	for err, want := range map[error]bool{
		&StatusError{Status: 429, Err: errors.New("x")}:                            true,
		fmt.Errorf("wrapped: %w", &StatusError{Status: 503, Err: errors.New("x")}): true,
		&StatusError{Status: 400, Err: errors.New("x")}:                            false,
		&StatusError{Status: 401, Err: errors.New("x")}:                            false,
		context.DeadlineExceeded:                                                   true,
		errors.New("anything else"):                                                false,
	} {
		if Transient(err) != want {
			t.Errorf("%v: %v", err, !want)
		}
	}
	c := Config{Model: "claude-opus-5-5", StepModels: []string{"claude-haiku-5-5"}}
	if m, ok := c.StepModel(""); !ok || m != "claude-opus-5-5" {
		t.Errorf("default: %s %v", m, ok)
	}
	if m, ok := c.StepModel("claude-haiku-5-5"); !ok || m != "claude-haiku-5-5" {
		t.Errorf("offered: %s %v", m, ok)
	}
	if _, ok := c.StepModel("gpt-9"); ok {
		t.Error("an unoffered model")
	}
	if got := c.OfferedStepModels(); len(got) != 2 || got[0] != "claude-opus-5-5" {
		t.Errorf("offered: %v", got)
	}
	env := map[string]string{"TASKIEM_AI_PROVIDER": "fake", "TASKIEM_AI_STEP_MODELS": "a, b ,a", "TASKIEM_AI_RETENTION": "Zero retention"}
	got, err := ConfigFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil || len(got.StepModels) != 2 || got.Retention != "Zero retention" {
		t.Errorf("from env: %+v %v", got, err)
	}
}
