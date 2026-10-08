package runtime

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
)

// minScrub is the shortest secret value scrubbed from step results: shorter
// values ("1", "true", a port) would replace ordinary text.
const minScrub = 6

// scrubbed replaces a secret value in what a step recorded.
const scrubbed = "[secret]"

// scrubSecrets replaces every secret value (and its URL-encoded form) in
// the strings of a JSON payload: error messages, logs and outputs.
func scrubSecrets(payload []byte, secrets []string) ([]byte, error) {
	r := secretReplacer(secrets)
	if r == nil || len(payload) == 0 {
		return payload, nil
	}
	v, err := expr.DecodeJSON(payload)
	if err != nil {
		return nil, err
	}
	changed := false
	v = scrubValue(v, r, &changed)
	if !changed {
		return payload, nil
	}
	return json.Marshal(v)
}

func secretReplacer(secrets []string) *strings.Replacer {
	seen := map[string]bool{}
	var vals []string
	for _, s := range secrets {
		for _, form := range []string{s, url.QueryEscape(s), url.PathEscape(s)} {
			if len(form) >= minScrub && !seen[form] {
				seen[form] = true
				vals = append(vals, form)
			}
		}
	}
	if len(vals) == 0 {
		return nil
	}
	// Longest first, so a secret containing another is replaced whole.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, scrubbed)
	}
	return strings.NewReplacer(pairs...)
}

func scrubValue(v any, r *strings.Replacer, changed *bool) any {
	switch t := v.(type) {
	case string:
		if s := r.Replace(t); s != t {
			*changed = true
			return s
		}
	case map[string]any:
		for k, c := range t {
			t[k] = scrubValue(c, r, changed)
		}
	case []any:
		for i, c := range t {
			t[i] = scrubValue(c, r, changed)
		}
	}
	return v
}

// scrubCodeSecrets notes the values of the secrets a code step may read, so
// they are scrubbed from what it returns, logs or fails with.
func (w *Worker) scrubCodeSecrets(ctx context.Context, p *plan) {
	if p.step == nil || p.step.Code == nil {
		return
	}
	for _, name := range p.step.Code.Secrets {
		if v, err := w.secret(ctx, p, name); err == nil {
			p.scrub = append(p.scrub, v)
		}
	}
}
