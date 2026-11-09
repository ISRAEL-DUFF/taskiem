// Package assistant keeps a business's assistant safe for its customers
// and for the business (decision 0062 V6, docs/assistant-guard.md): the
// customer guard a chat trigger runs before a run can start, the words a
// customer controls it with, and the check of a reply before it is sent.
package assistant

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
)

// What the guard does with a kind of message.
const (
	KindAnswer = "answer" // the workflow answers it
	KindReply  = "reply"  // a fixed answer in the customer's language says what the assistant can read
	KindDrop   = "drop"   // no run, no answer
)

// DefaultKinds is what the guard does with each kind of message unless the
// workflow says otherwise: text, voice notes, audio and button presses are
// answered; pictures, videos, documents, places and contacts get a fixed
// answer; stickers, reactions and anything else are dropped.
var DefaultKinds = map[string]string{
	"text": KindAnswer, "voice": KindAnswer, "audio": KindAnswer, "button": KindAnswer,
	"image": KindReply, "video": KindReply, "document": KindReply, "location": KindReply, "contact": KindReply,
	"sticker": KindDrop, "reaction": KindDrop, "other": KindDrop,
}

// Config is a chat trigger's customer guard (wd/v1 connector_event
// config.guard).
type Config struct {
	// PerMinute and PerDay bound the messages one customer starts runs
	// with (L1). Over either, the customer is told once (a fixed answer)
	// and the rest are dropped until the minute or day is over.
	PerMinute int `json:"per_minute"`
	PerDay    int `json:"per_day"`
	// MaxQueued bounds this workflow's runs waiting in its queue (L8), so
	// a busy assistant never fills the business's backlog.
	MaxQueued int `json:"max_queued"`
	// Notice: the first message from a new customer is answered with the
	// notice first (an assistant, voice notes are heard, STOP, a person).
	Notice bool `json:"notice"`
	// HandoverHours is how long the assistant stays quiet for a customer
	// who asked for a person (D13), unless a member resumes it sooner.
	HandoverHours int `json:"handover_hours"`
	// Kinds overrides DefaultKinds.
	Kinds map[string]string `json:"kinds"`
}

// Limits of the settings.
const (
	MaxPerMinute     = 60
	MaxPerDay        = 5000
	MaxQueuedRuns    = 1000
	MaxHandoverHours = 168
)

// Defaults.
func Defaults() Config {
	return Config{PerMinute: 6, PerDay: 200, MaxQueued: 20, Notice: true, HandoverHours: 24}
}

// ParseConfig reads config.guard: an object (or true for the defaults).
// Unknown fields and values out of range are refused in plain words.
func ParseConfig(v any) (Config, error) {
	c := Defaults()
	if b, ok := v.(bool); ok {
		if !b {
			return c, fmt.Errorf("guard is an object of settings, or true for the defaults")
		}
		return c, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return c, fmt.Errorf("guard is an object of settings, or true for the defaults")
	}
	num := func(k string, lo, hi int, dst *int) error {
		x, ok := m[k]
		if !ok {
			return nil
		}
		var f float64
		switch n := x.(type) {
		case float64:
			f, ok = n, true
		case int:
			f, ok = float64(n), true
		case int64:
			f, ok = float64(n), true
		case json.Number:
			v, err := n.Float64()
			f, ok = v, err == nil
		default:
			ok = false
		}
		if !ok || f != float64(int(f)) || int(f) < lo || int(f) > hi {
			return fmt.Errorf("guard.%s is a whole number from %d to %d", k, lo, hi)
		}
		*dst = int(f)
		return nil
	}
	for k := range m {
		if !slices.Contains([]string{"per_minute", "per_day", "max_queued", "notice", "handover_hours", "kinds"}, k) {
			return c, fmt.Errorf("guard has no setting %q (per_minute, per_day, max_queued, notice, handover_hours, kinds)", k)
		}
	}
	for _, f := range []struct {
		k      string
		lo, hi int
		dst    *int
	}{{"per_minute", 1, MaxPerMinute, &c.PerMinute}, {"per_day", 1, MaxPerDay, &c.PerDay}, {"max_queued", 1, MaxQueuedRuns, &c.MaxQueued},
		{"handover_hours", 1, MaxHandoverHours, &c.HandoverHours}} {
		if err := num(f.k, f.lo, f.hi, f.dst); err != nil {
			return c, err
		}
	}
	if c.PerDay < c.PerMinute {
		return c, fmt.Errorf("guard.per_day (%d) is less than guard.per_minute (%d)", c.PerDay, c.PerMinute)
	}
	if x, ok := m["notice"]; ok {
		b, ok := x.(bool)
		if !ok {
			return c, fmt.Errorf("guard.notice is true or false")
		}
		c.Notice = b
	}
	if x, ok := m["kinds"]; ok {
		km, ok := x.(map[string]any)
		if !ok {
			return c, fmt.Errorf("guard.kinds maps a kind of message to answer, reply or drop")
		}
		c.Kinds = map[string]string{}
		for k, v := range km {
			if !slices.Contains(connector.ConversationKinds, k) {
				return c, fmt.Errorf("guard.kinds: no kind of message %q (%s)", k, strings.Join(connector.ConversationKinds, ", "))
			}
			s, _ := v.(string)
			if s != KindAnswer && s != KindReply && s != KindDrop {
				return c, fmt.Errorf("guard.kinds.%s is answer, reply or drop", k)
			}
			c.Kinds[k] = s
		}
	}
	return c, nil
}

// KindAction is what the guard does with a kind of message.
func (c Config) KindAction(kind string) string {
	if a, ok := c.Kinds[kind]; ok {
		return a
	}
	if a, ok := DefaultKinds[kind]; ok {
		return a
	}
	return KindDrop
}

// Map is the config as stored with the trigger.
func (c Config) Map() map[string]any {
	m := map[string]any{"per_minute": c.PerMinute, "per_day": c.PerDay, "max_queued": c.MaxQueued, "notice": c.Notice, "handover_hours": c.HandoverHours}
	if len(c.Kinds) > 0 {
		k := map[string]any{}
		for n, a := range c.Kinds {
			k[n] = a
		}
		m["kinds"] = k
	}
	return m
}
