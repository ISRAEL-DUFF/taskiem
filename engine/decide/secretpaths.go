package decide

import (
	"strconv"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Decide leaves an expression that reads secrets as a string for the worker
// (decision 0010). An expression may also evaluate to text that reads like
// one: a webhook field whose whole value is "=secrets.api_token". The two
// look the same in the scheduled input, so decide records where the
// author's own expressions are (history.ScheduledPayload.SecretPaths) and
// the worker evaluates those places only.

// httpConfig is an http step's input as its author wrote it.
func httpConfig(c *wd.HTTPConfig) map[string]any {
	cfg := map[string]any{"method": c.Method, "url": c.URL}
	if c.Headers != nil {
		cfg["headers"] = c.Headers
	}
	if c.Query != nil {
		cfg["query"] = c.Query
	}
	if c.Body != nil {
		cfg["body"] = c.Body
	}
	if c.Class != "" {
		cfg["class"] = c.Class
	}
	if c.IdempotencyHeader != "" {
		cfg["idempotency_header"] = c.IdempotencyHeader
	}
	return cfg
}

// under puts JSON pointers below a prefix.
func under(prefix string, paths []string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, prefix+p)
	}
	return out
}

// mappedSecretPaths reports where a capability's input mapping carried the
// secret expressions of its input (at kept) into the action's input, out.
//
// The mapping is evaluated twice more, each time with a different marker
// in place of every kept expression. A place is a secret's only when it
// holds the first marker in one result and the second in the other: the
// mapping copied that input field there whole. A value of the run is the
// same in both results, so it cannot match both markers, whatever it is.
// An expression the mapping changed (joined to other text, say) is no
// longer an expression and stays as text, as it always did.
func mappedSecretPaths(mapping map[string]any, in any, kept []string, steps map[string]any, out any) []string {
	if len(kept) == 0 {
		return nil
	}
	markA := func(i int) string { return "\x00kept:a:" + strconv.Itoa(i) }
	markB := func(i int) string { return "\x00kept:b:" + strconv.Itoa(i) }
	first, second := in, in
	which := map[string]int{}
	for i, at := range kept {
		var errA, errB error
		first, errA = expr.SetAt(first, at, markA(i))
		second, errB = expr.SetAt(second, at, markB(i))
		if errA != nil || errB != nil {
			return nil
		}
		which[markA(i)] = i
	}
	outA, errA := capability.ResolveMapping(mapping, first, nil, steps)
	outB, errB := capability.ResolveMapping(mapping, second, nil, steps)
	if errA != nil || errB != nil {
		// The mapping does more with the field than carry it: nothing
		// is treated as an expression.
		return nil
	}
	var paths []string
	expr.WalkStrings(outA, func(at, s string) {
		i, ok := which[s]
		if !ok {
			return
		}
		if b, _ := expr.At(outB, at); b != markB(i) {
			return
		}
		want, _ := expr.At(in, kept[i])
		if got, ok := expr.At(out, at); ok && got == want {
			paths = append(paths, at)
		}
	})
	return paths
}

// AuthoredSecretPaths finds the secret expressions of a scheduled task
// whose payload records none: one written before SecretPaths existed, or a
// step that has none. The places come from the step as authored, never
// from the payload: a place counts only when the definition holds a secret
// expression there and the payload holds the very same string. Resolving
// keeps the shape of what the author wrote, so a value of the run cannot
// sit at such a place. For a payload written since, the answer is empty,
// as its own list is.
func AuthoredSecretPaths(s *wd.Step, p history.ScheduledPayload) []string {
	if s == nil {
		return nil
	}
	var authored any
	switch {
	case p.Compensates != "":
		if s.Compensate == nil {
			return nil
		}
		authored = s.Compensate.Input
	case s.Type == "http":
		if s.HTTP == nil {
			return nil
		}
		authored = httpConfig(s.HTTP)
	case s.Type == "ai":
		var prompt any
		if s.AI != nil {
			prompt = s.AI.Prompt
		}
		authored = map[string]any{"prompt": prompt, "input": orEmpty(s.Input)}
	default:
		authored = orEmpty(s.Input)
	}
	// What decide resolved: the capability's input for an expanded step
	// (the action's input is a mapping of it), else the input itself.
	resolved := p.Input
	mapped := p.Compensates == "" && s.Type == "connector" && s.Capability != nil
	if mapped {
		if p.Capability == nil {
			return nil
		}
		resolved = p.Capability.Input
	}
	var kept []string
	for _, at := range engine.SecretPaths(authored) {
		want, _ := expr.At(authored, at)
		if got, ok := expr.At(resolved, at); ok && got == want {
			kept = append(kept, at)
		}
	}
	if mapped {
		return mappedSecretPaths(s.Capability.Input, resolved, kept, p.Capability.Steps, p.Input)
	}
	return kept
}
