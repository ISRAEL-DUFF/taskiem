package ussd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/pii"
)

// Kind is what a walk ends on.
type Kind int

const (
	// Continue shows a screen and waits for input (CON).
	Continue Kind = iota
	// End shows a final screen; nothing is started (END).
	End
	// Confirmed: the caller chose Yes on a confirm screen. The edge hands
	// Inputs to the engine and ends the session with the screen's done text.
	Confirmed
)

// Result is where a path leads.
type Result struct {
	Kind   Kind
	Screen string // the screen shown (the confirm screen when Confirmed)
	Text   string // what to show; for Confirmed, the done text without its reference
	Inputs map[string]any
	// Invalid: the last input was refused and the screen is shown again.
	Invalid bool
}

// ReferenceLen is the length of a session's reference.
const ReferenceLen = 8

// maskedLen is the length of a masked value (****1234).
const maskedLen = 8

// Reference is the caller's reference for a session: stable across retries
// and replicas, derived from the tenant, provider and session id.
func Reference(tenant, provider, session string) string {
	sum := sha256.Sum256([]byte("ussd-ref\x00" + tenant + "\x00" + provider + "\x00" + session))
	return strings.ToUpper(hex.EncodeToString(sum[:])[:ReferenceLen])
}

// SplitPath turns an aggregator's text ("1*2*500") into the inputs typed so
// far.
func SplitPath(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "*")
}

type frame struct {
	screen *Screen
	set    string // input this screen set, removed on Back
	had    bool
	prev   any
}

// Walk replays the inputs a caller typed from the start screen and returns
// what to show now. It is pure: the same menu and path give the same
// result on any replica. Invalid inputs leave the caller on the same
// screen; only the last one is reported. "0" goes back a screen and "00"
// to the start, on every screen but the first. After a confirmation or an
// end screen, further inputs are ignored.
func (m *Menu) Walk(path []string) Result {
	start := m.StartScreen()
	if start == nil {
		return Result{Kind: End, Text: "This service is not available."}
	}
	if len(path) > MaxDepth {
		return Result{Kind: End, Text: "This session is too long. Please dial again."}
	}
	inputs := map[string]any{}
	cur := start
	var stack []frame
	invalid := false
	for _, in := range path {
		in = strings.TrimSpace(in)
		invalid = false
		if cur != start {
			switch in {
			case Home:
				cur, stack, inputs = start, nil, map[string]any{}
				continue
			case Back:
				if n := len(stack); n > 0 {
					f := stack[n-1]
					stack = stack[:n-1]
					if f.set != "" {
						if f.had {
							inputs[f.set] = f.prev
						} else {
							delete(inputs, f.set)
						}
					}
					cur = f.screen
				}
				continue
			}
		}
		switch cur.Type {
		case TypeMenu:
			opts := m.visible(cur, inputs)
			n, err := strconv.Atoi(in)
			if err != nil || n < 1 || n > len(opts) || in != strconv.Itoa(n) {
				invalid = true
				continue
			}
			o := opts[n-1]
			f := frame{screen: cur, set: cur.Input}
			if cur.Input != "" {
				f.prev, f.had = inputs[cur.Input]
				if o.Value != nil {
					inputs[cur.Input] = o.Value
				} else {
					inputs[cur.Input] = o.Label
				}
			}
			stack = append(stack, f)
			cur = m.byID[o.Next]
		case TypeInput:
			v, ok := m.accept(cur, in, inputs)
			if !ok {
				invalid = true
				continue
			}
			f := frame{screen: cur, set: cur.Input}
			f.prev, f.had = inputs[cur.Input]
			inputs[cur.Input] = v
			stack = append(stack, f)
			cur = m.byID[cur.Next]
		case TypeConfirm:
			switch in {
			case "1":
				return Result{Kind: Confirmed, Screen: cur.ID, Inputs: inputs, Text: orDefault(cur.Done, DefaultDone)}
			case "2":
				return Result{Kind: End, Screen: cur.ID, Inputs: inputs, Text: DefaultCancelled}
			}
			invalid = true
		case TypeEnd:
			// The session is over; anything after is ignored.
		}
		if cur == nil {
			return Result{Kind: End, Text: "This service is not available."}
		}
		if cur.Type == TypeEnd {
			break
		}
	}
	if cur.Type == TypeEnd {
		return Result{Kind: End, Screen: cur.ID, Inputs: inputs, Text: m.Render(cur.Text, inputs, "")}
	}
	return Result{Kind: Continue, Screen: cur.ID, Inputs: inputs, Invalid: invalid, Text: m.screen(cur, inputs, invalid, cur == start)}
}

// visible are a menu screen's options whose condition holds.
func (m *Menu) visible(s *Screen, inputs map[string]any) []Option {
	out := make([]Option, 0, len(s.Options))
	for _, o := range s.Options {
		if o.When != "" {
			ok, err := conditions.EvalBool(o.When, map[string]any{"trigger": map[string]any{"body": inputs}})
			if err != nil || !ok {
				continue
			}
		}
		out = append(out, o)
	}
	return out
}

// accept checks an input against its screen's rules.
func (m *Menu) accept(s *Screen, in string, inputs map[string]any) (any, bool) {
	if in == "" {
		return nil, false
	}
	v := s.Validate
	if v == nil {
		v = &Validate{}
	}
	n := utf8.RuneCountInString(in)
	if v.MinLength != nil && n < *v.MinLength || v.MaxLength != nil && n > *v.MaxLength {
		return nil, false
	}
	if v.Pattern != "" {
		re := v.re
		if re == nil {
			return nil, false // Parse compiles every pattern; an unchecked menu refuses
		}
		if !re.MatchString(in) {
			return nil, false
		}
	}
	var val any = in
	switch v.Type {
	case "integer":
		i, err := strconv.ParseInt(in, 10, 64)
		if err != nil {
			return nil, false
		}
		if v.Min != nil && float64(i) < *v.Min || v.Max != nil && float64(i) > *v.Max {
			return nil, false
		}
		val = i
	case "number":
		f, err := strconv.ParseFloat(in, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		if v.Min != nil && f < *v.Min || v.Max != nil && f > *v.Max {
			return nil, false
		}
		val = f
	}
	if v.When != "" {
		trial := make(map[string]any, len(inputs)+1)
		for k, x := range inputs {
			trial[k] = x
		}
		trial[s.Input] = val
		ok, err := conditions.EvalBool(v.When, map[string]any{"trigger": map[string]any{"body": trial}})
		if err != nil || !ok {
			return nil, false
		}
	}
	return val, true
}

// screen renders a screen for a CON reply.
func (m *Menu) screen(s *Screen, inputs map[string]any, invalid, first bool) string {
	var b strings.Builder
	if invalid {
		e := s.Error
		if e == "" {
			e = DefaultChoiceError
			if s.Type == TypeInput {
				e = DefaultInputError
			}
		}
		b.WriteString(m.Render(e, inputs, ""))
		b.WriteString("\n")
	}
	b.WriteString(m.Render(s.Text, inputs, ""))
	switch s.Type {
	case TypeMenu:
		for i, o := range m.visible(s, inputs) {
			fmt.Fprintf(&b, "\n%d. %s", i+1, o.Label)
		}
	case TypeConfirm:
		fmt.Fprintf(&b, "\n1. %s\n2. %s", orDefault(s.ConfirmLabel, DefaultConfirm), orDefault(s.CancelLabel, DefaultCancel))
	}
	if !first {
		b.WriteString("\n" + backLine)
	}
	return clip(b.String(), m.Limit())
}

// Render fills {{name}} placeholders with collected values, personal ones
// masked, and {{reference}} with ref.
func (m *Menu) Render(t string, inputs map[string]any, ref string) string {
	return placeholder.ReplaceAllStringFunc(t, func(p string) string {
		name := placeholder.FindStringSubmatch(p)[1]
		if name == "reference" {
			return ref
		}
		v, ok := inputs[name]
		if !ok {
			return ""
		}
		if m.Personal[name] {
			return Mask(display(v, ""))
		}
		if _, personal := pii.Classify(name, v, inputs); personal {
			return Mask(display(v, ""))
		}
		return display(v, "")
	})
}

// display is a value as a screen shows it.
func display(v any, fallback string) string {
	switch t := v.(type) {
	case nil:
		return fallback
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "Yes"
		}
		return "No"
	}
	return fmt.Sprint(v)
}

// Mask hides all but the last four characters of a personal value.
func Mask(s string) string {
	r := []rune(s)
	if len(r) <= 6 {
		return "****"
	}
	return "****" + string(r[len(r)-4:])
}

// MaskNumber masks a caller's number for logs and history.
func MaskNumber(n string) string { return Mask(n) }

// clip keeps a screen within the limit; Check makes this a safety net.
func clip(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit])
}
