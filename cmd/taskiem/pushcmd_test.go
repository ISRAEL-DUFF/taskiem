package main

import (
	"bytes"
	"strings"
	"testing"
)

// The VAPID settings of chat push (docs/spec/chat-push.md): none is off, a
// pair with a subject is on, and half a setting stops the process naming it.
func TestPushConfig(t *testing.T) {
	t.Setenv("TASKIEM_DATABASE_URL", "postgres://localhost/taskiem")
	for _, name := range []string{"TASKIEM_VAPID_PUBLIC_KEY", "TASKIEM_VAPID_PRIVATE_KEY", "TASKIEM_VAPID_SUBJECT"} {
		t.Setenv(name, "")
	}
	cfg, err := loadConfig()
	if err != nil || cfg.Push.Enabled() {
		t.Fatalf("no keys: on %v, %v", cfg.Push.Enabled(), err)
	}
	t.Setenv("TASKIEM_VAPID_PUBLIC_KEY", "pub")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TASKIEM_VAPID_PUBLIC_KEY") {
		t.Errorf("a public key alone: %v", err)
	}
	t.Setenv("TASKIEM_VAPID_PRIVATE_KEY", "priv")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TASKIEM_VAPID_SUBJECT") {
		t.Errorf("a pair with no subject: %v", err)
	}
	t.Setenv("TASKIEM_VAPID_SUBJECT", "mailto:ops@taskiem.test")
	cfg, err = loadConfig()
	if err != nil || !cfg.Push.Enabled() || cfg.Push.Subject != "mailto:ops@taskiem.test" {
		t.Errorf("a pair and a subject: %+v %v", cfg.Push, err)
	}
}

// `taskiem push keys` prints a usable pair as the settings to export.
func TestPushKeys(t *testing.T) {
	var out bytes.Buffer
	if err := pushCmd([]string{"keys", "--subject", "https://taskiem.test/contact"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TASKIEM_VAPID_PUBLIC_KEY=", "TASKIEM_VAPID_PRIVATE_KEY=", "TASKIEM_VAPID_SUBJECT=https://taskiem.test/contact"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s in %q", want, out.String())
		}
	}
	// A pair each time.
	var again bytes.Buffer
	_ = pushCmd([]string{"keys"}, &again)
	if again.String() == out.String() {
		t.Error("the same keys twice")
	}
	if err := pushCmd([]string{"keys", "--subject", "ada"}, &bytes.Buffer{}); err == nil {
		t.Error("a subject that is no address was accepted")
	}
	if err := pushCmd([]string{"nope"}, &bytes.Buffer{}); err == nil {
		t.Error("an unknown subcommand was accepted")
	}
}
