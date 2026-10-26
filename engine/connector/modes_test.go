package connector

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/effects"
)

const modesManifest = `manifest: connector/v1
id: wallet
version: 1.0.0
name: Wallet
category: payments
auth:
  type: api_key
  fields: [{ key: api_key, label: API key, secret: true }]
  modes:
    - { mode: live, label: Live (real money) }
    - { mode: test, label: Test (practice), base_url: https://sandbox.wallet.example/v1 }
base_url: https://sandbox.wallet.example/v1
actions:
  ping: { title: Ping, class: read, input: { type: object } }
`

func TestHostModes(t *testing.T) {
	m, probs := Parse([]byte(modesManifest))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if got := m.Auth.Modes(); len(got) != 2 || got[0].Mode != "live" || got[1].Label != "Test (practice)" {
		t.Errorf("modes %+v", got)
	}
	// Live has no address until the deployment gives it one.
	if _, ok := m.ModeURL("live"); ok {
		t.Error("live offered with no address")
	}
	if _, err := m.BaseFor(map[string]string{ModeKey: "live"}); !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "no address") {
		t.Errorf("a live connection with no live address: %v", err)
	}
	if base, err := m.BaseFor(map[string]string{ModeKey: "test"}); err != nil || base != "https://sandbox.wallet.example/v1" {
		t.Errorf("test: %q %v", base, err)
	}
	// A connection made before modes goes where it always went.
	if base, _ := m.BaseFor(map[string]string{}); base != "https://sandbox.wallet.example/v1" {
		t.Errorf("no mode: %q", base)
	}
	m.SetModeURL("live", "https://api.wallet.example/v1/")
	m.SetModeURL("test", "")
	if base, err := m.BaseFor(map[string]string{ModeKey: "live"}); err != nil || base != "https://api.wallet.example/v1" {
		t.Errorf("live once set: %q %v", base, err)
	}
	if base, _ := m.BaseFor(map[string]string{ModeKey: "test"}); base != "https://sandbox.wallet.example/v1" {
		t.Errorf("an empty override keeps the mode: %q", base)
	}
	// Both hosts may be reached, even after the base URL is overridden.
	m.OverrideBaseURL("http://127.0.0.1:8701")
	if h := m.Hosts(); !slices.Contains(h, "api.wallet.example") || !slices.Contains(h, "sandbox.wallet.example") || !slices.Contains(h, "127.0.0.1") {
		t.Errorf("hosts %v", h)
	}
	if (Request{Credentials: map[string]string{ModeKey: "test"}}).Mode() != "test" {
		t.Error("Request.Mode")
	}
}

func TestHostModesRules(t *testing.T) {
	for name, c := range map[string]struct{ from, to, want string }{
		"twice": {"- { mode: test, label: Test (practice), base_url: https://sandbox.wallet.example/v1 }",
			"- { mode: live, label: Again }", `mode "live" is named twice`},
		"with prefixes": {"fields: [{ key: api_key, label: API key, secret: true }]",
			"fields: [{ key: api_key, label: API key, secret: true, prefixes: [{ prefix: sk_live_, mode: live }] }]", "hosts or key prefixes, not both"},
		"http": {"base_url: https://sandbox.wallet.example/v1 }", "base_url: http://sandbox.wallet.example/v1 }", "modes"},
	} {
		src := strings.Replace(modesManifest, c.from, c.to, 1)
		_, probs := Parse([]byte(src))
		if !strings.Contains(strings.Join(probs, "; "), c.want) {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}
